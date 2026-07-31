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

type escritorConPlazo struct {
	w     io.Writer
	rc    *http.ResponseController
	plazo time.Duration
}

// escrituraConPlazo envuelve el ResponseWriter para descargas largas.
func escrituraConPlazo(w http.ResponseWriter, plazo time.Duration) io.Writer {
	rc := http.NewResponseController(w)
	// Si el ResponseWriter no admite plazos, se sigue sin ellos en lugar de
	// fallar: el resto de controles (IdleTimeout, contexto) siguen en pie.
	if err := rc.SetWriteDeadline(time.Now().Add(plazo)); err != nil {
		return w
	}
	return &escritorConPlazo{w: w, rc: rc, plazo: plazo}
}

func (e *escritorConPlazo) Write(p []byte) (int, error) {
	if err := e.rc.SetWriteDeadline(time.Now().Add(e.plazo)); err != nil {
		return 0, err
	}
	return e.w.Write(p)
}

type lectorConPlazo struct {
	r     io.Reader
	rc    *http.ResponseController
	plazo time.Duration
}

// lecturaConPlazo envuelve el cuerpo de la petición para subidas largas.
func lecturaConPlazo(w http.ResponseWriter, r io.Reader, plazo time.Duration) io.Reader {
	rc := http.NewResponseController(w)
	if err := rc.SetReadDeadline(time.Now().Add(plazo)); err != nil {
		return r
	}
	return &lectorConPlazo{r: r, rc: rc, plazo: plazo}
}

func (l *lectorConPlazo) Read(p []byte) (int, error) {
	if err := l.rc.SetReadDeadline(time.Now().Add(l.plazo)); err != nil {
		return 0, err
	}
	return l.r.Read(p)
}
