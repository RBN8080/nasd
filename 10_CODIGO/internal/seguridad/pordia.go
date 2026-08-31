package seguridad

import (
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"
)

// «Actividad por día» — la serie que pinta /seguridad.
//
// # POR QUÉ ESTO DEJÓ DE DERIVARSE DE LOS EVENTOS
//
// Hasta el 2026-08-31 la serie se calculaba recorriendo los eventos YA
// FILTRADOS que la página tenía a mano, y por eso el histórico de días
// anteriores no existía. Dos causas encadenadas, las dos estructurales:
//
//  1. LA VENTANA DEL FILTRO. El panel abre en «24 horas», así que los eventos
//     que llegaban a la gráfica nunca pasaban de un día: once de las doce
//     columnas salían en cero SIEMPRE, para cualquiera que no cambiara el
//     desplegable. Una gráfica de doce días alimentada por una ventana de
//     veinticuatro horas es una contradicción, no un ajuste fino.
//  2. EL ANILLO OLVIDA. Aun con «todo lo guardado», el anillo conserva
//     Capacidad eventos y no días: un barrido de 732 rechazos en una tarde
//     —el de DRIFTNET, medido— empuja fuera del buffer los días anteriores, y
//     con ellos las columnas que ya se habían dibujado ayer.
//
// La respuesta es un CONTEO POR DÍA que no cabe en un anillo porque no crece
// con el tráfico: un día son cinco enteros pase lo que pase, así que doce días
// son doce líneas. Lo escribe el mismo Anillo, en el mismo archivo y en el
// mismo volcado —no hay un segundo escritor que pueda discrepar del primero— y
// sobrevive al reinicio por la misma vía que el total histórico (marcaTotal).
//
// LO QUE ESTA CAPA NO ES: los acumulados por hora que el panel SIEM necesita.
// Aquí hay un entero por día y por red, sin motivo, sin ruta y sin origen. Es
// lo que la gráfica dibuja y ni un campo más.

// DiasGrafica es cuántos días pinta «Actividad por día» en /seguridad, y
// también cuántos se conservan en disco: la serie y la retención son la misma
// cifra porque guardar un día que nadie puede dibujar sería estado sin uso.
const DiasGrafica = 12

// formatoDia es la clave de un día. ISO 8601, que ordena alfabéticamente igual
// que cronológicamente — por eso el archivo se puede leer y ordenar sin
// convertir nada a fecha.
const formatoDia = "2006-01-02"

// medianocheLocal es el arranque del día de calendario en el que cae un
// instante.
//
// NO ES time.Truncate(24*time.Hour), y la diferencia no es cosmética: Truncate
// mide desde el cero absoluto —el 1 de enero del año 1 en UTC— así que en una
// zona con desfase devuelve un instante que NO es medianoche de ningún sitio.
// Este nodo corre en America/Mexico_City (UTC-6), donde Truncate cae a las
// 18:00 del día ANTERIOR: la serie y el conteo estarían hablando de días
// distintos con seis horas de diferencia, y los rechazos de la tarde se
// pintarían en la columna del día siguiente.
//
// Venía así de la versión de barras y nunca se notó porque el cálculo y su
// prueba compartían el error.
func medianocheLocal(t time.Time) time.Time {
	l := t.Local()
	a, m, d := l.Date()
	return time.Date(a, m, d, 0, 0, 0, 0, l.Location())
}

// marcaDia es la cabecera que lleva el conteo de UN día en el archivo del
// anillo. Va como comentario, igual que marcaTotal, para que un archivo
// escrito por esta versión lo siga leyendo una anterior sin romperse: las
// líneas que empiezan por «#» ya se saltaban.
const marcaDia = "# dia: "

// Dia es un día con su recuento, para la gráfica de actividad de /seguridad.
type Dia struct {
	Fecha    time.Time
	Rechazos int
}

// porDia es el conteo persistido: cuántos rechazos hubo cada día, separados
// POR RED.
//
// SEPARADOS POR RED Y NO UN SOLO TOTAL, porque el panel abre filtrado en
// «Internet» por encargo del responsable: una gráfica que sumara la LAN
// —que es casi todo el tráfico de este nodo— dibujaría una curva que no
// corresponde a ninguna de las tablas que tiene debajo. Con la red en la
// clave, la gráfica responde al MISMO filtro de origen que la tabla y no hay
// dos verdades en la misma pantalla.
//
// Cinco enteros por día en el peor caso. No crece con el tráfico, que es la
// propiedad que permite conservar días enteros donde el anillo solo conserva
// eventos.
type porDia struct {
	dias map[string]map[Red]int
}

func (p *porDia) anotar(momento time.Time, red Red) {
	if p.dias == nil {
		p.dias = make(map[string]map[Red]int, DiasGrafica)
	}
	clave := momento.Local().Format(formatoDia)
	if p.dias[clave] == nil {
		p.dias[clave] = make(map[Red]int, 2)
	}
	p.dias[clave][red]++
}

// podar deja solo los DiasGrafica días que la gráfica puede dibujar. Se llama
// al volcar y no con un temporizador: sin crecimiento no hay prisa, y una
// tarea menos es un modo de fallo menos.
func (p *porDia) podar(ahora time.Time) {
	if len(p.dias) <= DiasGrafica {
		return
	}
	corte := medianocheLocal(ahora).AddDate(0, 0, -(DiasGrafica - 1)).Format(formatoDia)
	for clave := range p.dias {
		if clave < corte {
			delete(p.dias, clave)
		}
	}
}

// absorber sube cada día al MÁXIMO entre lo que ya se sabía y lo que dice el
// otro conteo.
//
// EL MÁXIMO Y NO LA SUMA, por el mismo argumento con el que releer resuelve
// marcaTotal: los dos conteos hablan de los mismos rechazos, así que sumarlos
// los contaría dos veces. Y el máximo es el correcto en la única dirección en
// que pueden discrepar — el anillo solo puede quedarse CORTO, nunca de más,
// porque la rotación borra eventos y no los inventa.
func (p *porDia) absorber(otro porDia) {
	for clave, redes := range otro.dias {
		for red, n := range redes {
			if p.dias == nil {
				p.dias = make(map[string]map[Red]int, DiasGrafica)
			}
			if p.dias[clave] == nil {
				p.dias[clave] = make(map[Red]int, len(redes))
			}
			if n > p.dias[clave][red] {
				p.dias[clave][red] = n
			}
		}
	}
}

// deEventos cuenta por día y por red lo que un lote de eventos contiene. Es
// con lo que el anillo siembra el conteo al arrancar.
func deEventos(eventos []Evento) porDia {
	var p porDia
	for _, e := range eventos {
		p.anotar(e.Momento, e.Red)
	}
	return p
}

// serie devuelve las DiasGrafica columnas que terminan hoy y desde cuándo hay
// dato de verdad.
//
// «red» nulo significa cualquier origen, y entonces se suman las redes: es
// exactamente lo que hace el filtro de la tabla con «Cualquier origen», así
// que la gráfica y la tabla no pueden discrepar sobre qué se está mirando.
//
// DESDE es el día MÁS ANTIGUO CON DATO, no el primero de la serie: una gráfica
// de doce columnas recién estrenada tiene once vacías, y decir «desde hace
// doce días» prometería una profundidad que todavía no existe. Mismo criterio
// que PaquetesDesde.
func (p *porDia) serie(ahora time.Time, red *Red) (columnas []Dia, desde time.Time) {
	hoy := medianocheLocal(ahora)
	inicio := hoy.AddDate(0, 0, -(DiasGrafica - 1))

	columnas = make([]Dia, DiasGrafica)
	for i := range columnas {
		f := inicio.AddDate(0, 0, i)
		n := 0
		for r, v := range p.dias[f.Format(formatoDia)] {
			if red == nil || *red == r {
				n += v
			}
		}
		columnas[i] = Dia{Fecha: f, Rechazos: n}
		if n > 0 && desde.IsZero() {
			desde = f
		}
	}
	return columnas, desde
}

// lineas escribe el conteo para el archivo, un día por línea y en orden.
func (p *porDia) lineas() []string {
	claves := slices.Sorted(maps.Keys(p.dias))
	out := make([]string, 0, len(claves))
	for _, clave := range claves {
		redes := p.dias[clave]
		partes := make([]string, 0, len(redes))
		for r := RedDesconocida; r <= RedInternet; r++ {
			if n := redes[r]; n > 0 {
				partes = append(partes, r.String()+"="+strconv.Itoa(n))
			}
		}
		if len(partes) == 0 {
			continue
		}
		out = append(out, marcaDia+clave+" "+strings.Join(partes, " "))
	}
	return out
}

// diaDeCabecera lee una línea «# dia: 2026-08-31 internet=118 lan=402».
//
// Una línea ilegible se DESCARTA en vez de tumbar la lectura, al revés que un
// evento mal formado: un evento roto significa un archivo corrupto y hay que
// enterarse, pero esto es un resumen que el propio anillo puede reconstruir
// con absorber. Negarse a leer el historial entero por una columna de una
// gráfica sería cambiar lo importante por lo accesorio.
func diaDeCabecera(linea string) (string, map[Red]int, bool) {
	resto, ok := strings.CutPrefix(linea, marcaDia)
	if !ok {
		return "", nil, false
	}
	campos := strings.Fields(resto)
	if len(campos) < 2 {
		return "", nil, false
	}
	if _, err := time.Parse(formatoDia, campos[0]); err != nil {
		return "", nil, false
	}
	redes := make(map[Red]int, len(campos)-1)
	for _, campo := range campos[1:] {
		nombre, valor, ok := strings.Cut(campo, "=")
		if !ok {
			return "", nil, false
		}
		red, ok := redDesde(nombre)
		if !ok {
			return "", nil, false
		}
		n, err := strconv.Atoi(valor)
		if err != nil || n < 0 {
			return "", nil, false
		}
		redes[red] = n
	}
	return campos[0], redes, true
}

// redDesde recupera una red de su forma estable, igual que MotivoDesde y
// SenalDesde. Sin esto el conteo viajaría por el número del const, que cambia
// al insertar una red nueva en medio y dejaría ilegible lo ya escrito.
func redDesde(s string) (Red, bool) {
	for r := RedDesconocida; r <= RedInternet; r++ {
		if r.String() == s {
			return r, true
		}
	}
	return RedDesconocida, false
}

// Serie es lo que la página pide: las columnas de la gráfica para un origen.
func (a *Anillo) Serie(ahora time.Time, red *Red) ([]Dia, time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.dias.serie(ahora, red)
}
