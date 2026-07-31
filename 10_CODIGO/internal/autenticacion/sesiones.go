package autenticacion

import (
	"crypto/rand"
	"encoding/base64"
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
	m        map[string]time.Time // testigo -> instante de caducidad
	duracion time.Duration
}

func NuevasSesiones(duracion time.Duration) *Sesiones {
	return &Sesiones{m: make(map[string]time.Time), duracion: duracion}
}

// Abrir crea una sesión y devuelve su testigo.
//
// 32 bytes de crypto/rand: el espacio es tan grande que adivinarlo no es una
// vía de ataque, lo que importa porque sin TLS (ADR-0018) el testigo viaja en
// claro por la LAN y su exposición real es esa, no la fuerza bruta.
func (s *Sesiones) Abrir() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generar el testigo de sesión: %w", err)
	}
	testigo := base64.RawURLEncoding.EncodeToString(b)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[testigo] = time.Now().Add(s.duracion)
	return testigo, nil
}

// Valida indica si el testigo sigue vigente. Caducidad ABSOLUTA, no
// deslizante: una sesión robada no se prolonga sola con el uso del ladrón.
func (s *Sesiones) Valida(testigo string) bool {
	if testigo == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	caduca, ok := s.m[testigo]
	if !ok {
		return false
	}
	if time.Now().After(caduca) {
		delete(s.m, testigo)
		return false
	}
	return true
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
	for t, caduca := range s.m {
		if ahora.After(caduca) {
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
