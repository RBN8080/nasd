package autenticacion

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Sesiones guarda las sesiones abiertas EN MEMORIA.
//
// Consecuencia declarada: un reinicio del servicio cierra todas las sesiones
// y hay que volver a entrar. Se acepta a propósito — persistirlas obligaría a
// guardar en disco un material que da acceso directo, y con un solo usuario
// el coste de reabrir sesión es teclear la contraseña una vez.
//
// Charter §6.1: estado compartido entre goroutines, protegido por un mutex y
// documentado aquí.
type Sesiones struct {
	mu       sync.Mutex
	m        map[string]sesion
	duracion time.Duration
}

type sesion struct {
	// usuario es DE QUIÉN es esta sesión, y es lo que decide qué carpeta ve
	// (ADR-0055). Nunca está vacío: Abrir lo rechaza.
	//
	// Vive aquí y no en la cookie a propósito. Si viajara en la cookie, sería
	// el cliente quien dice quién es, y cambiar una letra bastaría para
	// mirar la carpeta de otro. El navegador solo lleva un testigo opaco; el
	// nombre lo pone el servidor al verificar la contraseña y no se vuelve a
	// preguntar.
	usuario string

	caduca time.Time
	// csrf acompaña a la sesión y se exige en TODA petición que cambie algo.
	//
	// SameSite=Lax ya frena el POST entre sitios en navegadores modernos,
	// pero con operaciones que borran de forma irreversible y sin papelera
	// (D-15) una sola capa no basta: un navegador viejo o una configuración
	// rara bastarían para destruir datos que no se pueden recuperar.
	csrf string
}

func NuevasSesiones(duracion time.Duration) *Sesiones {
	return &Sesiones{m: make(map[string]sesion), duracion: duracion}
}

// Abrir crea una sesión A NOMBRE DE ALGUIEN y devuelve su testigo.
//
// 32 bytes de crypto/rand: el espacio es tan grande que adivinarlo no es una
// vía de ataque, lo que importa porque sin TLS (ADR-0018) el testigo viaja en
// claro por la LAN y su exposición real es esa, no la fuerza bruta.
//
// EL NOMBRE ES OBLIGATORIO y se comprueba aquí, en el sitio donde nacen las
// sesiones. Una sesión sin dueño sería una sesión a la que después habría que
// asignarle una carpeta adivinando, y adivinar aquí significa enseñarle a
// alguien la carpeta de otro (ADR-0055).
func (s *Sesiones) Abrir(usuario string) (string, error) {
	if usuario == "" {
		return "", errors.New("abrir sesión: falta el usuario")
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generar el testigo de sesión: %w", err)
	}
	testigo := base64.RawURLEncoding.EncodeToString(b)

	c := make([]byte, 32)
	if _, err := rand.Read(c); err != nil {
		return "", fmt.Errorf("generar el testigo CSRF: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[testigo] = sesion{
		usuario: usuario,
		caduca:  time.Now().Add(s.duracion),
		csrf:    base64.RawURLEncoding.EncodeToString(c),
	}
	return testigo, nil
}

// Csrf devuelve el testigo CSRF de una sesión vigente.
func (s *Sesiones) Csrf(testigo string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	se, ok := s.m[testigo]
	if !ok || time.Now().After(se.caduca) {
		return "", false
	}
	return se.csrf, true
}

// CsrfValido compara en TIEMPO CONSTANTE el testigo recibido con el de la
// sesión. Comparar con == filtraría por tiempo cuántos bytes se acertaron.
func (s *Sesiones) CsrfValido(testigo, recibido string) bool {
	esperado, ok := s.Csrf(testigo)
	if !ok || recibido == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(esperado), []byte(recibido)) == 1
}

// Usuario devuelve de quién es la sesión, y si sigue vigente.
//
// Es la consulta PRINCIPAL: quien atiende una petición no necesita saber si
// hay sesión, necesita saber de quién es, porque de eso depende qué carpeta
// mira. Devolver las dos cosas juntas evita la versión rota de esto —validar
// primero y preguntar el nombre después—, que deja un hueco entre ambas
// llamadas en el que la sesión puede caducar.
//
// Caducidad ABSOLUTA, no deslizante: una sesión robada no se prolonga sola
// con el uso del ladrón.
func (s *Sesiones) Usuario(testigo string) (string, bool) {
	if testigo == "" {
		return "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	se, ok := s.m[testigo]
	if !ok {
		return "", false
	}
	if time.Now().After(se.caduca) {
		delete(s.m, testigo)
		return "", false
	}
	return se.usuario, true
}

// Valida indica si el testigo sigue vigente, sin mirar de quién es.
func (s *Sesiones) Valida(testigo string) bool {
	_, ok := s.Usuario(testigo)
	return ok
}

// Cerrar invalida un testigo concreto.
func (s *Sesiones) Cerrar(testigo string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, testigo)
}

// Purgar elimina las caducadas. Lo llama el ciclo de mantenimiento: sin esto
// el mapa crecería con cada sesión abierta y nunca menguaría, que es
// exactamente la fuga que ya nos costó una revisión con las subidas.
func (s *Sesiones) Purgar() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	ahora := time.Now()
	n := 0
	for t, se := range s.m {
		if ahora.After(se.caduca) {
			delete(s.m, t)
			n++
		}
	}
	return n
}

// Abiertas devuelve cuántas hay vigentes. Para el registro (P7).
func (s *Sesiones) Abiertas() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.m)
}
