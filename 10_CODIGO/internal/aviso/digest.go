package aviso

import (
	"slices"
	"sync"
	"time"

	"nasd/internal/seguridad"
)

// El resumen — el volumen que NO merece un mensaje cada vez, y la prueba de que
// el canal sigue vivo.
//
// # LAS DOS COSAS QUE HACE, Y LA SEGUNDA NO ES OBVIA
//
//  1. Absorbe la actividad de bajo nivel: conexiones, rutas inexistentes,
//     sondeos de software que aquí no existe. Cifras, no mensajes.
//
//  2. DEMUESTRA QUE EL CANAL FUNCIONA. Este nodo recibió 8 peticiones de
//     Internet en 21 días medidos y CERO hallazgos desde siempre. Con ese
//     volumen, un canal que solo habla cuando hay problemas puede pasar semanas
//     mudo — y un canal mudo es indistinguible de uno roto. El resumen se envía
//     AUNQUE TODO SEA CERO, por eso mismo: es al canal de avisos lo que el
//     latido es al nodo.
//
// # SE CALCULA DE UNA LECTURA NUEVA, NO DE CONTADORES ACUMULADOS
//
// La tentación era ir sumando lo que ve cada ciclo de vigilancia. Habría estado
// mal: los ciclos miran una ventana de una hora CADA MINUTO, así que el mismo
// rechazo entra en sesenta lecturas y el resumen habría multiplicado por sesenta
// la actividad real. Aquí se pide una Entrada de la ventana del resumen entero y
// se cuenta una sola vez.

// topeRetenidas acota cuántas situaciones retenidas caben.
//
// Existe porque el modo silencio puede coincidir con un barrido, y entonces
// quien decide cuántas situaciones se acumulan es un extraño. Veinte llenan de
// sobra un mensaje legible; a partir de ahí el resumen dice cuántas se
// recortaron, igual que hacen SondasVistas y RutasVistas en el panel.
const topeRetenidas = 20

// Resumen es lo que se cuenta cada periodo.
type Resumen struct {
	Desde time.Time
	Hasta time.Time

	// Volumen. Son CIFRAS de hechos, no interpretaciones.
	ConexionesInternet int
	OrigenesDeInternet int
	Rechazos           int
	RutasInexistentes  int
	SondasAjenas       int

	// Inferencias y acciones, contadas, no narradas.
	OrigenesConSenal int
	Cuarentenas      int
	Hallazgos        int

	// Retenidas son las situaciones que el modo silencio no dejó salir. Van
	// ENTERAS, no como número: ver el comentario de EntregaRetenida.
	Retenidas []Situacion
	// RetenidasOmitidas es cuántas no cupieron. Se publica para que el resumen
	// pueda decir que recorta, en vez de mentir por omisión — la misma
	// honestidad que el panel aplica a sus tres tablas.
	RetenidasOmitidas int

	// Avisadas y Absorbidas son la cuenta del propio Registro: cuántos mensajes
	// se emitieron y cuántas observaciones se absorbieron sin emitir nada. Es
	// lo que hace COMPROBABLE que esta capa reduce el ruido.
	Avisadas   int64
	Absorbidas int64
}

// Tranquilo dice si no pasó absolutamente nada digno de contar.
//
// Se publica para que el mensaje pueda escribirse distinto —una línea en vez de
// una tabla de ceros— sin que el formateador tenga que repetir estas ocho
// comparaciones y arriesgarse a olvidar una.
func (r Resumen) Tranquilo() bool {
	return r.ConexionesInternet == 0 && r.Rechazos == 0 &&
		r.OrigenesConSenal == 0 && r.Cuarentenas == 0 &&
		r.Hallazgos == 0 && len(r.Retenidas) == 0
}

// Resumir cuenta una Entrada. Función pura, igual que Evaluar.
func Resumir(e Entrada, retenidas []Situacion, ahora time.Time) Resumen {
	r := Resumen{
		Desde:              ahora.Add(-e.Ventana),
		Hasta:              ahora,
		ConexionesInternet: e.ConexionesInternet,
		Hallazgos:          len(e.Hallazgos),
	}

	for _, o := range e.Origenes {
		if !o.Red.DeFuera() {
			continue
		}
		r.OrigenesDeInternet++
		r.Rechazos += o.Eventos
		r.RutasInexistentes += o.PorMotivo[seguridad.RutaInexistente]
		if len(o.Senales) > 0 {
			r.OrigenesConSenal++
		}
		if o.Apartado != nil {
			r.Cuarentenas++
		}
		// Las sondas se cuentan por PETICIONES y no por rutas distintas: la
		// pregunta que contesta esta cifra es «cuánto se está buscando aquí»,
		// no «cuántas cosas distintas se buscaron».
		for _, sd := range o.Evidencia {
			if seguridad.RutaDeSoftwareAjeno(sd.Ruta) {
				r.SondasAjenas += sd.Veces
			}
		}
	}

	if len(retenidas) > topeRetenidas {
		r.RetenidasOmitidas = len(retenidas) - topeRetenidas
		retenidas = retenidas[:topeRetenidas]
	}
	r.Retenidas = retenidas
	return r
}

// Retencion guarda lo que el modo silencio no dejó salir, hasta el resumen.
//
// # POR QUÉ NO SE PERSISTE, Y EL COSTE DICHO EN VOZ ALTA
//
// Vive solo en memoria. Si el nodo se reinicia con situaciones retenidas, esas
// situaciones NO se vuelven a anunciar: el Registro ya las tiene marcadas como
// avisadas, así que no reaparecen como nuevas.
//
// Se acepta, y con el coste acotado: lo que se pierde es el TEXTO del aviso,
// nunca el hecho. Los cuatro historiales de internal/seguridad conservan todo, y
// la marca de novedades del panel sigue encendida porque se deriva de esos
// mismos hechos y no de esto. Persistirlo habría añadido un séptimo archivo de
// estado para recuperar un mensaje que el panel ya puede contar entero.
//
// # ESTADO COMPARTIDO (ADR-0013)
//
// Lleva mutex: lo escribe el ciclo de vigilancia y lo vacía el del resumen, que
// son dos temporizadores distintos.
type Retencion struct {
	mu          sync.Mutex
	situaciones []Situacion
	omitidas    int
}

// Guardar retiene una situación. Lleno, cuenta la omisión en vez de crecer:
// quien empuja este tamaño es un extraño, no el responsable.
func (r *Retencion) Guardar(s Situacion) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.situaciones) >= topeRetenidas {
		r.omitidas++
		return
	}
	r.situaciones = append(r.situaciones, s)
}

// Vaciar entrega lo retenido y lo olvida, de lo más grave a lo menos.
func (r *Retencion) Vaciar() ([]Situacion, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out, omitidas := r.situaciones, r.omitidas
	r.situaciones, r.omitidas = nil, 0
	ordenar(out)
	return slices.Clip(out), omitidas
}

// Cuantas dice cuánto hay retenido sin vaciarlo.
func (r *Retencion) Cuantas() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.situaciones)
}
