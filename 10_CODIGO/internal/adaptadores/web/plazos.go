package web

import (
	"io"
	"net/http"
	"time"
)

// Plazos por actividad — ADR-0026.
//
// El plazo se renueva cada vez que avanzan datos. Una transferencia legítima
// de 4 GB nunca lo agota; una conexión colgada sí. Es la única forma de
// cumplir RNF-12 (plazo declarado) y RF-09 (4 GB íntegros) a la vez.
//
// Se aplica UNA VEZ, aquí, envolviendo el lector o el escritor. Así ningún
// manejador puede olvidarse de ponerlo: el cumplimiento es estructural, no
// disciplina de quien escriba el siguiente.
//
// Desde ADR-0059 hacen además una SEGUNDA cosa, sin relación con el plazo de
// E/S: avisan a la sesión de que hay actividad, para que una descarga o
// subida larga no encuentre su propia sesión caducada a mitad de camino
// (ver tocadorDe en sesion.go). tocar puede ser nil —vivo.go lo pasa así a
// propósito, ver su comentario— y entonces no se avisa de nada.

// intervaloDeToque acota cada cuánto, como mucho, un bloque de bytes avisa a
// la sesión. Tocar es una escritura bajo mutex (autenticacion.Sesiones); sin
// este suelo, un flujo rápido de bloques pequeños lo llamaría en cada uno.
const intervaloDeToque = 10 * time.Second

type escritorConPlazo struct {
	w     io.Writer
	rc    *http.ResponseController
	plazo time.Duration
	tocar func()
	// ultimoToque es estado LOCAL de esta escritura: nace y muere con la
	// petición, la usa una sola goroutine, y por eso no lleva cerrojo.
	ultimoToque time.Time
}

// escrituraConPlazo envuelve el ResponseWriter para descargas largas. tocar
// puede ser nil: ver el comentario de cabecera.
func escrituraConPlazo(w http.ResponseWriter, plazo time.Duration, tocar func()) io.Writer {
	rc := http.NewResponseController(w)
	// Si el ResponseWriter no admite plazos, se sigue sin ellos en lugar de
	// fallar: el resto de controles (IdleTimeout, contexto) siguen en pie.
	if err := rc.SetWriteDeadline(time.Now().Add(plazo)); err != nil {
		return w
	}
	return &escritorConPlazo{w: w, rc: rc, plazo: plazo, tocar: tocar}
}

func (e *escritorConPlazo) Write(p []byte) (int, error) {
	ahora := time.Now()
	if err := e.rc.SetWriteDeadline(ahora.Add(e.plazo)); err != nil {
		return 0, err
	}
	if e.tocar != nil && ahora.Sub(e.ultimoToque) >= intervaloDeToque {
		e.tocar()
		e.ultimoToque = ahora
	}
	return e.w.Write(p)
}

type lectorConPlazo struct {
	r           io.Reader
	rc          *http.ResponseController
	plazo       time.Duration
	tocar       func()
	ultimoToque time.Time
}

// lecturaConPlazo envuelve el cuerpo de la petición para subidas largas.
// tocar puede ser nil: ver el comentario de cabecera.
func lecturaConPlazo(w http.ResponseWriter, r io.Reader, plazo time.Duration, tocar func()) io.Reader {
	rc := http.NewResponseController(w)
	if err := rc.SetReadDeadline(time.Now().Add(plazo)); err != nil {
		return r
	}
	return &lectorConPlazo{r: r, rc: rc, plazo: plazo, tocar: tocar}
}

func (l *lectorConPlazo) Read(p []byte) (int, error) {
	ahora := time.Now()
	if err := l.rc.SetReadDeadline(ahora.Add(l.plazo)); err != nil {
		return 0, err
	}
	if l.tocar != nil && ahora.Sub(l.ultimoToque) >= intervaloDeToque {
		l.tocar()
		l.ultimoToque = ahora
	}
	return l.r.Read(p)
}
