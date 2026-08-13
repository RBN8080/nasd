package web

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"nasd/internal/adaptadores/sistema"
)

// Estado en vivo — ADR-0051.
//
// La página de estado se refresca sola. El servidor empuja los valores por una
// conexión larga y el navegador solo sustituye texto; no hay recarga, no hay
// sondeo y no hay ninguna dependencia nueva (ADR-0013, ADR-0017).
//
// # POR QUÉ SSE Y NO WEBSOCKET
//
// Aquí los datos van en UN SOLO SENTIDO: del nodo a la pantalla. El navegador
// no tiene nada que contar. SSE es exactamente eso —una respuesta HTTP que no
// se cierra, con eventos separados por líneas en blanco— y sale de la
// biblioteca estándar sin escribir protocolo. Un WebSocket sobre stdlib exige
// el apretón de manos y el enmarcado a mano: unas trescientas líneas delicadas
// para ganar un canal de vuelta que no se usa.
//
// # POR QUÉ UN SOLO MUESTREADOR
//
// El coste real no está en las conexiones, está en LEER. Si cada espectador
// disparara su propia lectura, tres pestañas abiertas serían el triple de
// trabajo sobre el nodo. Aquí muestrea UNA goroutine y reparte lo mismo a
// todos: el coste de medir queda acotado y no depende de cuánta gente mire.
//
// Y solo corre mientras alguien mira. Sin espectadores no hay muestreo: la
// última desconexión para el bucle, y la primera conexión lo arranca. Un panel
// que nadie tiene abierto no debe costar nada.

// intervaloVivo es cada cuánto se muestrea y se emite.
//
// EL SUELO NO ES UNA PREFERENCIA, LO PONE /proc/stat. El porcentaje de CPU no
// se lee, se resta entre dos muestras, y el kernel cuenta en «jiffies» de
// 10 ms por núcleo. En 250 ms los cuatro núcleos del nodo acumulan ~100
// jiffies, así que el porcentaje sale con resolución de ~1 punto. A 100 ms
// serían ~40 y el número saltaría de dos en dos puntos y medio: más rápido en
// el reloj y PEOR en la pantalla, que es un mal negocio.
//
// Cuatro veces por segundo también es el límite de lo que sirve de algo: por
// encima, el ojo ya no distingue el cambio de un número de tres cifras.
//
// Lo que se paga por tic son siete lecturas de archivos que el kernel sirve
// desde memoria (ver sistema.Vivo). Ni proceso hijo, ni espera, ni disco: el
// bus USB 2.0, que es el recurso escaso de este nodo (RES-02), no se toca.
const intervaloVivo = 250 * time.Millisecond

// marcoVivo es lo que viaja en cada evento.
//
// Van SIEMPRE todas las filas, no solo las que cambiaron. Un protocolo de
// diferencias obligaría a que ningún espectador se perdiera nunca un marco, y
// abajo se descartan a propósito los de un cliente lento: con diferencias, ese
// cliente se quedaría con un valor viejo para siempre. Enviarlo entero cuesta
// menos de un kilobyte y se recupera solo del siguiente marco. Quien evita el
// trabajo inútil es el navegador, que no toca el DOM si el texto no cambió.
type marcoVivo struct {
	Nodo     []filaViva `json:"nodo"`
	Servicio []filaViva `json:"servicio"`
}

// muestreador es el CUARTO punto de estado compartido del programa, tras el
// registro de subidas, el de sesiones y los contadores. ADR-0013 dejó
// vinculante documentarlos todos, así que aquí está:
//
//   - «oyentes» y «activo» los tocan la goroutine del bucle y la de cada
//     petición. Van bajo el mismo cerrojo y nunca se leen fuera de él.
//   - «activo» es lo que garantiza que haya COMO MUCHO un bucle vivo. No basta
//     con mirar si el mapa está vacío: entre que el bucle decide salir y sale,
//     una conexión nueva podría arrancar un segundo bucle y el nodo pasaría a
//     muestrearse el doble de veces, en silencio y para siempre.
//
// # POR QUÉ ES GENÉRICO — ADR-0056
//
// Nació sirviendo solo a /estado. Cuando /administracion necesitó lo mismo
// para su pastilla de sesión, había dos caminos: copiar estas cuarenta líneas
// o parametrizar el marco. Se parametrizó, y el motivo no es la elegancia: es
// que ESTA es la parte delicada del programa —los dos peores defectos del
// proyecto fueron carreras (rector v1.11.0)— y una copia sería un quinto punto
// de estado compartido con sus propias carreras posibles. Compartiéndolo, las
// cuatro pruebas de vivo_test.go cubren las dos páginas a la vez.
type muestreador[T any] struct {
	mu      sync.Mutex
	oyentes map[chan T]struct{}
	activo  bool

	// abrir entrega el lector que produce cada marco. Se inyecta para que el
	// muestreador no sepa nada del servidor y se pueda probar sin levantar uno
	// entero.
	//
	// LA DOBLE FUNCIÓN NO ES ADORNO, Y ES FÁCIL DE ROMPER AL SIMPLIFICARLA. El
	// porcentaje de CPU no se lee, se RESTA entre dos muestras, así que el
	// lector de /estado guarda la muestra anterior. Ese estado tiene que nacer
	// cuando arranca el BUCLE, no cuando arranca el servidor: si naciera una
	// sola vez, tras un rato sin espectadores la primera resta se haría contra
	// una lectura rancia de hace horas y el marco inicial daría un porcentaje
	// falso justo al abrir la pantalla. Con «abrir» se pide un lector nuevo en
	// cada arranque del bucle, que es exactamente lo que hacía la versión no
	// genérica al declarar «previa» dentro de bucle().
	//
	// El flujo de cuentas no guarda nada entre marcos: su «abrir» devuelve el
	// método y ya está.
	abrir func() func() T
}

func nuevoMuestreador[T any](abrir func() func() T) *muestreador[T] {
	return &muestreador[T]{
		oyentes: make(map[chan T]struct{}),
		abrir:   abrir,
	}
}

// suscribir devuelve el canal por el que llegan los marcos y la función que
// cancela la suscripción. La función DEBE llamarse: sin ella el oyente queda
// en el mapa y el muestreo no se para nunca.
func (m *muestreador[T]) suscribir() (<-chan T, func()) {
	// Capacidad 1 y no 0: el bucle no puede quedarse esperando a que un
	// espectador lento lea, porque los demás dependen de él.
	c := make(chan T, 1)

	m.mu.Lock()
	m.oyentes[c] = struct{}{}
	arrancar := !m.activo
	if arrancar {
		m.activo = true
	}
	m.mu.Unlock()

	if arrancar {
		go m.bucle()
	}

	var unaVez sync.Once
	return c, func() {
		unaVez.Do(func() {
			m.mu.Lock()
			delete(m.oyentes, c)
			m.mu.Unlock()
		})
	}
}

// bucle muestrea y reparte hasta que no queda nadie mirando.
func (m *muestreador[T]) bucle() {
	// El lector se pide AQUÍ, al arrancar el bucle, y no al construir el
	// muestreador: ver la nota de «abrir» más arriba. Para /estado esta línea
	// es la que toma la primera muestra de CPU antes del primer tic, de modo
	// que el marco inicial ya trae el porcentaje medido en una ventana real en
	// lugar de estrenar la pantalla con un «no disponible».
	leer := m.abrir()

	t := time.NewTicker(intervaloVivo)
	defer t.Stop()

	for range t.C {
		marco := leer()

		m.mu.Lock()
		if len(m.oyentes) == 0 {
			// Se apaga bajo el mismo cerrojo con el que se enciende: quien
			// llegue después verá «activo» en falso y arrancará un bucle nuevo.
			m.activo = false
			m.mu.Unlock()
			return
		}
		for c := range m.oyentes {
			select {
			case c <- marco:
			default:
				// Espectador que aún no ha leído el marco anterior. Se descarta
				// este: en un flujo de estado el dato NUEVO siempre vale más que
				// el viejo, y bloquear aquí congelaría a todos los demás.
			}
		}
		m.mu.Unlock()
	}
}

// flujoDeEstado sirve el flujo SSE de /estado. Va detrás de la sesión, como
// /estado y por los mismos motivos (ver el encabezado de estado.go).
func (s *Servidor) flujoDeEstado(w http.ResponseWriter, r *http.Request) {
	servirFlujo(s, w, r, s.muestreador)
}

// flujoDeCuentas sirve el flujo SSE de /administracion — ADR-0056. Misma
// puerta que el resto del panel: solo el superusuario.
func (s *Servidor) flujoDeCuentas(w http.ResponseWriter, r *http.Request) {
	servirFlujo(s, w, r, s.cuentas)
}

// servirFlujo es la fontanería de SSE, común a los dos flujos: cabeceras,
// suscripción, y un bucle que escribe marcos hasta que el cliente se va.
//
// VA COMO FUNCIÓN Y NO COMO MÉTODO porque los métodos de Go no admiten
// parámetros de tipo propios. No hay ninguna intención de diseño detrás de esa
// forma; es la única que el lenguaje permite.
func servirFlujo[T any](s *Servidor, w http.ResponseWriter, r *http.Request, m *muestreador[T]) {
	rc := http.NewResponseController(w)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	// Sin esto, cualquier intermediario que almacene en búfer retendría los
	// eventos hasta llenar un bloque y el «tiempo real» llegaría a ráfagas.
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	if err := rc.Flush(); err != nil {
		// Sin Flush no hay flujo posible: cada marco se quedaría en el búfer.
		// Mejor decirlo y cerrar que servir una conexión que nunca entrega.
		s.reg.Warn("flujo en vivo no disponible: el ResponseWriter no deja vaciar", "error", err, "ruta", r.URL.Path)
		return
	}

	marcos, cancelar := m.suscribir()
	defer cancelar()

	// EL PLAZO POR ACTIVIDAD DE ADR-0026 SE APLICA IGUAL QUE EN UNA DESCARGA, y
	// hay que comprobar que no corta esto: es un plazo de 60 s que se RENUEVA
	// con cada escritura, y aquí se escribe cuatro veces por segundo. Una
	// conexión viva lo renueva 240 veces antes de acercarse al plazo; una
	// conexión muerta deja de aceptar escrituras y el flujo se cierra solo, que
	// es justo lo que se quiere. Verificado con TestElFlujoSobreviveAlPlazo.
	//
	// SE PASA nil COMO TOCADOR, Y ES UNA DECISIÓN, NO UN OLVIDO (ADR-0059).
	// Este envoltorio escribe cuatro veces por segundo; si cada una avisara a
	// la sesión, una pestaña de /estado o /administracion abierta y olvidada
	// haría que la sesión NUNCA caducara por inactividad, que es justo el
	// requisito que ADR-0059 existe para cumplir. Un flujo abierto no es
	// actividad de nadie — la caducidad de esta conexión concreta la impone
	// el bucle de abajo, comprobando la sesión en cada marco.
	escribir := escrituraConPlazo(w, s.plazoInactividad, nil)
	cod := json.NewEncoder(escribir)

	// testigo identifica la sesión que abrió este flujo. Se captura una vez,
	// aquí: exigirSesion ya validó que hay cookie con una sesión vigente, así
	// que el error solo puede venir de que el navegador la retire a mitad de
	// conexión, y en ese caso no hay nada que comprobar en el bucle —sin
	// cookie no hay testigo, y sesiones.Valida("") ya es false—.
	testigo := ""
	if c, err := r.Cookie(nombreCookie); err == nil {
		testigo = c.Value
	}

	for {
		select {
		case <-r.Context().Done():
			// El espectador cerró la pestaña o se fue la red. Nada que
			// registrar: es el final normal de toda conexión de este tipo.
			return
		case marco := <-marcos:
			// LA SESIÓN SE COMPRUEBA EN CADA MARCO — ADR-0059. Sin esto, un
			// flujo abierto antes de que la sesión caducara por inactividad
			// seguiría entregando telemetría del nodo entero indefinidamente:
			// exigirSesion solo se ejecuta al ABRIR la conexión SSE, y esta
			// puede vivir horas. Valida() es una consulta pura —no cuenta
			// como actividad—, así que comprobar aquí no alarga la sesión
			// que se está comprobando.
			if !s.sesiones.Valida(testigo) {
				return
			}
			if _, err := escribir.Write([]byte("data: ")); err != nil {
				return
			}
			// Encode ya escribe el salto de línea final; el segundo cierra el
			// evento, que es lo que el formato SSE exige para entregarlo.
			if err := cod.Encode(marco); err != nil {
				return
			}
			if _, err := escribir.Write([]byte("\n")); err != nil {
				return
			}
			if err := rc.Flush(); err != nil {
				return
			}
		}
	}
}

// abrirLectorVivo entrega el lector del flujo de /estado. Se llama una vez por
// arranque del bucle y ahí toma la primera muestra de CPU; el lector que
// devuelve guarda esa muestra para poder RESTAR en el tic siguiente.
//
// «previa» solo la toca la goroutine del bucle —una por muestreador, y «activo»
// lo garantiza—, así que no necesita cerrojo. No es un quinto punto de estado
// compartido: es estado local de una goroutine.
func (s *Servidor) abrirLectorVivo() func() marcoVivo {
	previa := sistema.LeerCPU()
	return func() marcoVivo {
		var v sistema.Vivo
		v, previa = sistema.LeerVivo(previa)
		return s.marcoDelServidor(v)
	}
}

// marcoDelServidor mide el servicio en el mismo instante que el nodo, para que
// las dos tablas cuenten el mismo momento.
func (s *Servidor) marcoDelServidor(v sistema.Vivo) marcoVivo {
	nodo, servicio := filasVivas(v, s.instantaneaCompleta())
	return marcoVivo{Nodo: nodo, Servicio: servicio}
}
