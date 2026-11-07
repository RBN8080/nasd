package web

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"time"

	"nasd/internal/adaptadores/sistema"
)

// LA MINIGRÁFICA DE CPU LLEGA DIBUJADA — ADR-0106, 2026-11-07.
//
// El responsable pidió que la gráfica «se conservara siempre» y no volviera a
// empezar vacía cada vez que se entra en /estado o en /resumen. El navegador
// solo guarda lo que ve mientras la página está abierta, así que la historia
// vive aquí y viaja en la propia página («data-muestras»).
//
// ES LA ÚNICA MEDICIÓN QUE CORRE SIN NADIE MIRANDO, y es barata a propósito:
// una lectura de /proc/stat cada intervaloVivo y nada más. El flujo de /estado
// sigue parándose sin espectadores (ADR-0051). Medido en el nodo el mismo día:
// 205 µs por lectura con Python por encima, menos del 0.1 % de un núcleo a
// cuatro por segundo.
//
// ES EL SEXTO PUNTO DE ESTADO COMPARTIDO del programa (ADR-0013, tras los
// cinco de ADR-0064): escribe la goroutine de MedirCPU y leen las peticiones de
// /estado y /resumen. Todo va bajo «mu», y el anillo es fijo: no crece nunca.

// muestrasCPU son las que dibuja la minigráfica: 30 s a una por intervaloVivo.
// estado.js usa el mismo número (MUESTRAS).
const muestrasCPU = 120

type muestraCPU struct {
	momento time.Time
	uso     float64
}

// anilloCPU guarda las últimas muestrasCPU, la más vieja pisada por la nueva.
type anilloCPU struct {
	mu     sync.Mutex
	datos  [muestrasCPU]muestraCPU
	sig, n int
}

func (a *anilloCPU) anotar(momento time.Time, uso float64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.datos[a.sig] = muestraCPU{momento, uso}
	a.sig = (a.sig + 1) % muestrasCPU
	if a.n < muestrasCPU {
		a.n++
	}
}

// serie devuelve, de la más vieja a la más nueva, las muestras de los últimos
// 30 s contados desde «ahora». Lo anterior no se entrega: tras un hueco —el
// servicio parado, el reloj movido— la gráfica juntaría dos momentos como si
// fueran seguidos.
func (a *anilloCPU) serie(ahora time.Time) []float64 {
	desde := ahora.Add(-muestrasCPU * intervaloVivo)
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]float64, 0, a.n)
	for i := range a.n {
		m := a.datos[(a.sig-a.n+i+muestrasCPU)%muestrasCPU]
		if m.momento.After(desde) && !m.momento.After(ahora) {
			out = append(out, m.uso)
		}
	}
	return out
}

// MedirCPU anota el uso de CPU cada intervaloVivo hasta que ctx se cancele. Lo
// arranca main. Fuera de Linux no hay /proc/stat y vuelve en el acto.
func (s *Servidor) MedirCPU(ctx context.Context) {
	previa := sistema.LeerCPU()
	if !previa.OK {
		return
	}
	t := time.NewTicker(intervaloVivo)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case ahora := <-t.C:
			actual := sistema.LeerCPU()
			if uso, ok := sistema.PorcentajeCPU(previa, actual); ok {
				s.historiaCPU.anotar(ahora, uso)
			}
			if actual.OK {
				previa = actual
			}
		}
	}
}

// serieCPU es la historia lista para «data-muestras»: números con un decimal
// separados por espacios, con la lectura de esta misma carga al final cuando
// la hay. Así la gráfica llega con un punto aunque el servicio acabe de
// arrancar, y vacía —sin gráfica— solo donde no se mide la CPU.
func (s *Servidor) serieCPU(v sistema.Vivo) string {
	serie := s.historiaCPU.serie(time.Now())
	if v.CPUOK {
		serie = append(serie, v.CPU)
	}
	if len(serie) > muestrasCPU {
		serie = serie[len(serie)-muestrasCPU:]
	}
	partes := make([]string, len(serie))
	for i, uso := range serie {
		partes[i] = strconv.FormatFloat(uso, 'f', 1, 64)
	}
	return strings.Join(partes, " ")
}
