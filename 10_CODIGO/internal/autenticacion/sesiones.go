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
	mu          sync.Mutex
	m           map[string]sesion
	duracion    time.Duration
	inactividad time.Duration
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
	// ultimoUso es el segundo reloj — ADR-0059. caduca no se toca nunca tras
	// Abrir; este sí, cada vez que Tocar ve actividad. Los dos tienen que
	// cumplirse a la vez para que la sesión siga viva (ver vigente).
	ultimoUso time.Time
	// csrf acompaña a la sesión y se exige en TODA petición que cambie algo.
	//
	// SameSite=Lax ya frena el POST entre sitios en navegadores modernos,
	// pero con operaciones que borran de forma irreversible y sin papelera
	// (D-15) una sola capa no basta: un navegador viejo o una configuración
	// rara bastarían para destruir datos que no se pueden recuperar.
	csrf string
}

// NuevasSesiones arma el almacén. inactividad es el plazo deslizante de
// ADR-0059: una sesión sin actividad ese tiempo deja de ser válida aunque el
// tope absoluto (duracion) siga lejos.
//
// inactividad <= 0 DESACTIVA ese segundo reloj y deja el comportamiento
// anterior a ADR-0059 —solo el tope absoluto—. No es un caso especial
// pensado para producción: existe porque las pruebas de este paquete y del
// adaptador web construyen sesiones fijando solo el tope, y esas pruebas no
// dejan de ser válidas por no conocer el reloj nuevo.
func NuevasSesiones(duracion, inactividad time.Duration) *Sesiones {
	return &Sesiones{m: make(map[string]sesion), duracion: duracion, inactividad: inactividad}
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

	ahora := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[testigo] = sesion{
		usuario:   usuario,
		caduca:    ahora.Add(s.duracion),
		ultimoUso: ahora,
		csrf:      base64.RawURLEncoding.EncodeToString(c),
	}
	return testigo, nil
}

// vigente aplica LOS DOS relojes de ADR-0059: el absoluto, que no se renueva
// nunca, y el de inactividad, que sí. Uno solo no basta —el absoluto deja
// viva una pestaña olvidada durante días, el deslizante por sí solo dejaría
// viva para siempre una sesión que alguien usa sin parar—.
//
// Único sitio donde se compara con «ahora»: quien quiera cambiar el criterio
// de caducidad lo cambia aquí y no en cada método, que es justo el error que
// vigilar() ya avisa en otro contexto (mantenimiento.go).
func (s *Sesiones) vigente(se sesion, ahora time.Time) bool {
	if ahora.After(se.caduca) {
		return false
	}
	if s.inactividad > 0 && ahora.Sub(se.ultimoUso) > s.inactividad {
		return false
	}
	return true
}

// Csrf devuelve el testigo CSRF de una sesión vigente.
func (s *Sesiones) Csrf(testigo string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	se, ok := s.m[testigo]
	if !ok || !s.vigente(se, time.Now()) {
		return "", false
	}
	return se.csrf, true
}

// Tocar registra actividad en una sesión — ADR-0059. Es lo que hace
// deslizante el plazo de inactividad: cada petición autenticada, y cada
// bloque de una descarga o subida en curso, llama aquí.
//
// NUNCA RESUCITA una sesión que ya no es vigente —ni por el tope absoluto ni
// por inactividad—. Es la condición que hace seguro tocar desde una
// escritura en curso (plazos.go): un toque que llega tarde, tras el plazo,
// no prolonga nada, simplemente no encuentra sesión que tocar.
func (s *Sesiones) Tocar(testigo string) {
	if testigo == "" {
		return
	}
	ahora := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	se, ok := s.m[testigo]
	if !ok || !s.vigente(se, ahora) {
		return
	}
	se.ultimoUso = ahora
	s.m[testigo] = se
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

// Vigencia es lo que el gestor SABE de un testigo, y solo lo que puede
// demostrar.
//
// # POR QUÉ HACE FALTA UN TERCER VALOR Y NO BASTA UN BOOLEANO
//
// Usuario() devolvía «false» para dos hechos opuestos: un testigo que este
// proceso emitió y venció, y una cadena que el cliente se inventó. El
// adaptador web tenía que elegir uno de los dos para el panel, y elegía
// «caducada» en cuanto la petición traía cookie — que es una inferencia sobre
// el pasado a partir de algo que escribe el cliente. `nas_sesion=loquesea` no
// prueba que ninguna sesión existiera.
//
// Con esto, «caducada» se afirma SOLO cuando la entrada sigue en el mapa y
// vigente() la ve vencida, que es cuando de verdad se sabe.
//
// LÍMITE DECLARADO, y no se disimula: Consultar BORRA la entrada vencida al
// verla, igual que hacía Usuario. Así que la primera petición tras vencer
// dice Caducada y las siguientes dicen Desconocida. Es correcto — el gestor
// deja de saberlo de verdad — y no se cambia: recordar los testigos muertos
// para poder seguir contestando «caducada» sería guardar en memoria una lista
// que solo crece y que existe para el panel.
type Vigencia uint8

const (
	// Desconocida — el gestor no tiene ni recuerdo de este testigo. No dice si
	// existió alguna vez: dice que AHORA no consta.
	Desconocida Vigencia = iota
	// Caducada — la entrada está en el mapa y ya no cumple los relojes de
	// ADR-0059. Esto sí es historia demostrable: la abrió este proceso.
	Caducada
	// Vigente — la sesión vale.
	Vigente
)

// Consultar responde de quién es un testigo y qué se sabe de él.
//
// Es la consulta completa; Usuario() es la vista corta de esta misma, para que
// no existan dos formas de decidir si una sesión vale.
func (s *Sesiones) Consultar(testigo string) (string, Vigencia) {
	if testigo == "" {
		return "", Desconocida
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	se, ok := s.m[testigo]
	if !ok {
		return "", Desconocida
	}
	if !s.vigente(se, time.Now()) {
		delete(s.m, testigo)
		return "", Caducada
	}
	return se.usuario, Vigente
}

// Usuario devuelve de quién es la sesión, y si sigue vigente.
//
// Es la consulta PRINCIPAL: quien atiende una petición no necesita saber si
// hay sesión, necesita saber de quién es, porque de eso depende qué carpeta
// mira. Devolver las dos cosas juntas evita la versión rota de esto —validar
// primero y preguntar el nombre después—, que deja un hueco entre ambas
// llamadas en el que la sesión puede caducar.
//
// Caducidad de dos relojes — ADR-0059: el absoluto no se prolonga nunca (una
// sesión robada no se alarga sola con el uso del ladrón), pero por debajo
// corre uno de inactividad que sí se desliza con Tocar.
func (s *Sesiones) Usuario(testigo string) (string, bool) {
	u, v := s.Consultar(testigo)
	return u, v == Vigente
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

// Purgar elimina las que ya no son vigentes, por cualquiera de los dos
// relojes. Lo llama el ciclo de mantenimiento: sin esto el mapa crecería con
// cada sesión abierta y nunca menguaría, que es exactamente la fuga que ya
// nos costó una revisión con las subidas.
func (s *Sesiones) Purgar() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	ahora := time.Now()
	n := 0
	for t, se := range s.m {
		if !s.vigente(se, ahora) {
			delete(s.m, t)
			n++
		}
	}
	return n
}

// Abiertas devuelve cuántas hay VIGENTES ahora mismo — para el registro
// (P7). Con el reloj de inactividad de ADR-0059 el mapa lleva sesiones
// caducadas entre una purga y la siguiente, así que len(s.m) sobrestimaría;
// se cuenta como ActivosPorUsuario, filtrando en la lectura.
func (s *Sesiones) Abiertas() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	ahora := time.Now()
	n := 0
	for _, se := range s.m {
		if s.vigente(se, ahora) {
			n++
		}
	}
	return n
}

// ActivosPorUsuario dice, de cada cuenta con sesión VIGENTE ahora mismo, que
// la tiene. Lo usa el panel de administración (P-4, etapa 2) para mostrar
// quién está dentro en este instante.
//
// Es una consulta, no una purga: a diferencia de Usuario(), no borra las
// caducadas al pasar por ellas — de eso ya se encarga Purgar() en el ciclo de
// mantenimiento, y duplicar ese trabajo aquí solo añadiría una segunda razón
// para que el mapa cambiara mientras alguien lo recorre.
func (s *Sesiones) ActivosPorUsuario() map[string]bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	ahora := time.Now()
	activos := make(map[string]bool, len(s.m))
	for _, se := range s.m {
		if !s.vigente(se, ahora) {
			continue
		}
		activos[se.usuario] = true
	}
	return activos
}
