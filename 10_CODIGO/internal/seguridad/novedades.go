package seguridad

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"nasd/internal/atomico"
)

// Novedades — «hay algo que no había visto» en un número.
//
// # POR QUÉ EXISTE
//
// Un control que nadie mira no existe. Todo lo demás de este trabajo —la
// cuarentena, la lista, la puerta— actúa sin que nadie esté delante, y hasta
// aquí la única forma de enterarse era que al responsable le diera por abrir
// /seguridad. Esto pone el resultado donde ya mira: en el botón de la barra.
//
// # LAS TRES COSAS QUE LA ENCIENDEN, Y LA QUE SE DEJÓ FUERA
//
//  1. Un origen de Internet NUNCA VISTO.
//  2. Una cuarentena disparada — el nodo se defendió solo y hay que saberlo.
//  3. Un bloqueo puesto a mano que frenó algo — dice que la regla sigue viva.
//  4. Un HALLAZGO nuevo — el nodo respondió con contenido en una ruta que no
//     publica (hallazgos.go).
//
// La cuarta se añadió con los hallazgos, y no por completismo: es la única de
// las cuatro que pide ir a arreglar algo en vez de solo mirar. Las otras tres
// informan de lo que hizo un extraño; esta informa de lo que hace el nodo. Una
// alarma así encendida en un panel que nadie abre no habría avisado de nada —
// que es literalmente el motivo por el que existe este archivo.
//
// Se descartó a propósito «acceso correcto desde una red desconocida», que
// sería la única señal capaz de detectar una credencial robada USADA con
// éxito. El motivo es de uso, no de ingeniería: hay una segunda persona
// entrando desde fuera de casa, y se encendería por ella hasta que sus redes
// fueran conocidas. Una marca que se enciende por lo de siempre deja de
// mirarse, y entonces no avisa de nada.
//
// # SE CALCULA DE HECHOS PERSISTIDOS, NO DE UN CONTADOR EN MEMORIA
//
// Un contador que se incrementa al vuelo se pondría a cero en cada arranque
// —15 en 14 días— y la marca se apagaría sola sin que nadie hubiera mirado
// nada. Aquí lo único que se guarda es CUÁNDO se miró por última vez; el resto
// se deriva de los anillos, la cuarentena y la lista, que ya sobreviven al
// reinicio. Es el mismo criterio que separa hecho de inferencia en el panel
// (ADR-0061): la visita es un hecho, «hay novedades» es una cuenta sobre él.

// Novedades cuenta lo que ha pasado desde la última visita al panel.
type Novedades struct {
	mu     sync.Mutex
	ruta   string
	visita time.Time
	cuenta int
	sucio  bool
}

// enDisco es lo único que se persiste.
type enDisco struct {
	Visita time.Time `json:"ultima_visita"`
}

func CargarNovedades(ruta string) (*Novedades, error) {
	n := &Novedades{ruta: ruta}
	f, err := os.Open(ruta)
	if errors.Is(err, os.ErrNotExist) {
		return n, nil
	}
	if err != nil {
		return n, fmt.Errorf("abrir la marca de novedades %q: %w", ruta, err)
	}
	defer f.Close()
	var d enDisco
	if err := json.NewDecoder(f).Decode(&d); err != nil {
		return n, fmt.Errorf("leer la marca de novedades %q: %w", ruta, err)
	}
	n.visita = d.Visita
	return n, nil
}

// Recalcular vuelve a contar. Lo llama el ciclo de mantenimiento, no cada
// página: la barra la pinta CADA petición, y recorrer dos anillos en cada una
// convertiría un adorno en un coste permanente.
func (n *Novedades) Recalcular(conectados []OrigenConectado, apartados []Apartado, bloqueos []Entrada, hallazgos []Hallazgo) {
	n.mu.Lock()
	desde := n.visita
	n.mu.Unlock()

	cuenta := 0
	for _, o := range conectados {
		// «Nuevo» es que su PRIMERA conexión sea posterior a la visita, no que
		// haya conectado hoy: una dirección conocida que vuelve no es una
		// novedad, y contarla dejaría la marca encendida para siempre.
		if o.Primera.After(desde) {
			cuenta++
		}
	}
	for _, a := range apartados {
		if a.Desde.After(desde) {
			cuenta++
		}
	}
	for _, b := range bloqueos {
		if b.UltimoFrenado.After(desde) {
			cuenta++
		}
	}
	for _, h := range hallazgos {
		// Se cuenta por PRIMERA y no por Ultima, igual que los orígenes nuevos:
		// lo que enciende la marca es que aparezca una ruta expuesta que no
		// estaba, no que una ya conocida se vuelva a pedir. Con Ultima, un
		// escáner insistiendo sobre la misma ruta dejaría la marca encendida
		// para siempre y volvería a ser lo que este archivo evita: un aviso que
		// se enciende por lo de siempre y se deja de mirar.
		//
		// LA RUTA SIGUE EN EL PANEL mientras nadie la arregle: apagar la marca
		// no borra el hallazgo, solo dice «esto ya lo has visto».
		if h.Primera.After(desde) {
			cuenta++
		}
	}

	n.mu.Lock()
	n.cuenta = cuenta
	n.mu.Unlock()
}

// Cuantas es lo que pinta la barra. Barato a propósito: un candado y un entero.
func (n *Novedades) Cuantas() int {
	if n == nil {
		return 0
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.cuenta
}

// Visto sella la visita y apaga la marca. Lo llama /seguridad al pintarse.
func (n *Novedades) Visto(ahora time.Time) {
	if n == nil {
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	n.visita = ahora
	n.cuenta = 0
	n.sucio = true
}

// Mantener recalcula y vuelca en la misma cadencia que todo lo demás.
func (n *Novedades) Mantener(hecho <-chan struct{}, cx *Conexiones, c *Cuarentena, l *Lista, h *Hallazgos, alFallar func(error)) {
	t := time.NewTicker(intervaloVolcado)
	defer t.Stop()
	recalcular := func(ahora time.Time) {
		n.Recalcular(
			PorOrigenConectado(cx.Desde(time.Time{})),
			c.Vigentes(ahora),
			l.Vigentes(ahora),
			h.Todos(),
		)
	}
	// Una vez al arrancar, para que la marca no tarde un minuto en aparecer
	// tras un reinicio — que es justo cuando más falta hace saber si pasó algo
	// mientras el nodo estaba abajo.
	recalcular(time.Now())
	for {
		select {
		case <-hecho:
			if err := n.Volcar(); err != nil {
				alFallar(err)
			}
			return
		case ahora := <-t.C:
			recalcular(ahora)
			if err := n.Volcar(); err != nil {
				alFallar(err)
			}
		}
	}
}

// Volcar guarda la fecha de la última visita.
func (n *Novedades) Volcar() error {
	n.mu.Lock()
	if !n.sucio {
		n.mu.Unlock()
		return nil
	}
	d := enDisco{Visita: n.visita}
	n.sucio = false
	n.mu.Unlock()

	err := atomico.Escribir(n.ruta, ".novedades-*", func(w io.Writer) error {
		return json.NewEncoder(w).Encode(d)
	})
	if err != nil {
		n.mu.Lock()
		n.sucio = true
		n.mu.Unlock()
	}
	return err
}
