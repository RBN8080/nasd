package web

// «Procedencia» — el mapa de países del panel de seguridad (ADR-0089).
//
// Vive aquí y no en internal/seguridad por lo mismo que grafica_seguridad.go:
// esto son cubos de color y coordenadas de un SVG concreto, y aquel paquete
// no sabe de HTML. El dominio sigue sin conocer la geografía; lo único que
// cruza la frontera es lo que la tabla de orígenes YA traía resuelto.
//
// # NO HAY DATO NUEVO, SOLO UNA AGREGACIÓN
//
// El país de cada origen se resuelve desde ADR-0062 y se pinta en la tabla
// de orígenes desde entonces. Esta sección no lee nada más: agrupa las MISMAS
// filas por su código ISO-2 y las coloca en un mapa. De ahí que no necesite
// estado nuevo en disco, ni tocar el anillo, ni un ADR de persistencia — la
// profundidad que enseña es exactamente la del filtro que gobierna la tabla.
//
// # LO QUE ESTA SECCIÓN NO ES
//
// No es una serie temporal. La maqueta que el responsable aprobó enseñaba
// además una tira de doce días por país y un selector de día, y eso EXIGE un
// conteo por país persistido en el archivo del anillo —la «opción B» del
// plan—. Se dejó fuera a propósito: sin ese conteo, una ráfaga como la de
// DRIFTNET expulsa del anillo los días anteriores y la tira mentiría sobre su
// propia profundidad. Cuando la pregunta que se le haga al mapa sea «¿esto
// viene pasando o es de hoy?», ese es el momento de abrir B.

import (
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"nasd/internal/geoip"
	"nasd/internal/seguridad"
)

// cortesDeCubo son los umbrales de la escala de color. El cubo 0 es «ninguna
// actividad» y se pinta del mismo tono de tierra que el mapa de fondo; de ahí
// en adelante, ESTE VALOR O MÁS enciende el siguiente escalón.
//
// SON SEIS ESCALONES Y NO UNA RAMPA CONTINUA, y es deliberado: el ojo no
// compara dos azules parecidos, y una escala continua obligaría a leer la
// leyenda para cada país. Con seis, la leyenda se lee una vez.
//
// Los cortes crecen por saltos grandes —1, 5, 20, 80, 300— porque esto es lo
// que mide: un puñado de orígenes normales contra un barrido. Una escala
// lineal pondría a DRIFTNET y a los otros quince países en el mismo color.
var cortesDeCubo = [...]int{1, 5, 20, 80, 300}

// TopeListaProcedencia es cuántos países se ven de entrada en la lista de al
// lado del mapa. El resto sigue en el HTML y lo descubre un interruptor de
// CSS —el mismo mecanismo de «Filtrar» (ADR-0060: sin JavaScript)—, así que
// recortar aquí no esconde nada: solo decide qué se ve sin pedirlo.
const TopeListaProcedencia = 5

func cuboDe(valor int) int {
	cubo := 0
	for _, corte := range cortesDeCubo {
		if valor >= corte {
			cubo++
		}
	}
	return cubo
}

// elementoMapa es la geometría de UN país tal como la dejó cmd/preparar-mapa
// en mundo.svg: o el «d» de un <path>, o el centro de un <circle> para los
// que no tienen forma a la resolución de Natural Earth 110m.
type elementoMapa struct {
	Trazo  string
	CX, CY float64
	Punto  bool
}

var (
	reMapaTrazo   = regexp.MustCompile(`<path id="([A-Z]{2})" d="([^"]+)"`)
	reMapaCirculo = regexp.MustCompile(`<circle id="([A-Z]{2})" cx="([\d.]+)" cy="([\d.]+)"`)
)

// elementosMapa recorre mundo.svg UNA vez y publica el trazo de cada país.
//
// # POR QUÉ SE LEE DEL MISMO ARCHIVO QUE SE SIRVE, Y NO DE UNA TABLA APARTE
//
// El fondo del mapa se entrega tal cual como imagen —/estatico/mundo.svg, con
// su ETag y su 304 en las recargas— y la capa de color se dibuja encima con
// los mismos trazos. Con dos archivos, uno para servir y otro para colorear,
// bastaría regenerar uno y olvidar el otro para que la capa quedara
// desplazada respecto del fondo, y eso no falla a gritos: se ve torcido. Con
// uno solo no hay nada que pueda discrepar.
//
// Misma disciplina que estaticos() y huellaDelBinario: sync.OnceValue, se
// construye al primer uso y no se vuelve a escribir, así que se lee desde
// varias peticiones a la vez sin cerrojo.
var elementosMapa = sync.OnceValue(func() map[string]elementoMapa {
	m := make(map[string]elementoMapa, 180)
	r, ok := estaticos()["/estatico/mundo.svg"]
	if !ok {
		// El archivo va incrustado con //go:embed: si no está, el binario
		// está mal construido. No se entra en pánico —al revés que
		// estaticos(), que sí lo hace— porque aquí la consecuencia es
		// acotada: la sección de procedencia no pinta y el resto del panel
		// sigue entero, que es la misma degradación que ya tiene sin base
		// de operadores (RF-30, criterio 1).
		return m
	}
	svg := string(r.crudo)
	for _, c := range reMapaTrazo.FindAllStringSubmatch(svg, -1) {
		m[c[1]] = elementoMapa{Trazo: c[2]}
	}
	for _, c := range reMapaCirculo.FindAllStringSubmatch(svg, -1) {
		cx, _ := strconv.ParseFloat(c[2], 64)
		cy, _ := strconv.ParseFloat(c[3], 64)
		m[c[1]] = elementoMapa{CX: cx, CY: cy, Punto: true}
	}
	return m
})

// paisMapa es UN país con su cifra ya calculada y su geometría ya resuelta.
type paisMapa struct {
	ISO    string
	Nombre string
	Valor  int
	Cubo   int
	// Puesto es el número de la fila, empezando en 1. Se calcula aquí porque
	// html/template no sabe sumar: la alternativa era estrenar una función
	// «suma» en el FuncMap para un «+1», y este panel ya tiene el hábito de
	// llegar a la plantilla con todo resuelto (ver diaGrafica.Etiqueta).
	Puesto int

	// La geometría, copiada de elementosMapa. Punto distingue a los países
	// sin forma —Singapur y Hong Kong— que se marcan con un círculo.
	Trazo  string
	CX, CY float64
	Punto  bool
	// EnMapa es falso cuando la base de operadores devuelve un código que el
	// mapa no dibuja. No debería pasar con un ISO-2 real, pero si pasa el
	// país NO desaparece: sale en la lista y se dice que no se pudo situar.
	// Perder un origen en silencio sería peor que no tener mapa.
	EnMapa bool

	// Lo que se lee al posar el cursor y en la fila de la lista.
	//
	// Operador y Senal son los del origen que MÁS aporta a la cifra de este
	// país, no un resumen de todos: un país puede traer varios operadores y
	// varias señales, y fundirlos en uno inventaría un dato que no existe.
	// La lista dice cuántas direcciones hay detrás para que se vea que el
	// operador es el principal y no el único.
	Operador    string
	Direcciones int
	Senal       string
	ConSenal    bool
	Ultima      time.Time
	// Titulo es el <title> del trazo — lo que el navegador enseña al posar,
	// sin JavaScript de por medio. Corto: nombre, código y cifra.
	Titulo string
	// Lectura es la línea larga que seguridad.js escribe bajo el mapa al
	// posar, enfocar o tocar. Se compone AQUÍ y no en la plantilla porque el
	// mapa y la lista tienen que decir exactamente lo mismo del mismo país, y
	// dos plantillas que lo compusieran por su cuenta podrían separarse.
	Lectura string

	// contribucion es cuánto aportó el origen del que salieron Operador y
	// Senal, para saber si el siguiente lo supera. No se exporta: la
	// plantilla no tiene por qué verlo, y html/template no alcanza los
	// campos sin exportar.
	contribucion int
}

// opcionCapa es uno de los dos botones del selector: qué capa pinta el mapa.
//
// VIAJA COMO ENLACE Y NO COMO JAVASCRIPT, igual que el orden del listado
// (P-3) y el filtro de esta misma página: el estado va en la URL, se puede
// marcar y compartir, y el servidor sigue siendo quien decide qué se pinta
// (ADR-0076). La alternativa —recalcular en el cliente— habría exigido mandar
// las dos capas de todos los países en cada carga para usar una.
type opcionCapa struct {
	Valor    string
	Etiqueta string
	Enlace   string
	Elegido  bool
}

// panelProcedencia es la sección entera, lista para la plantilla.
type panelProcedencia struct {
	// Hay dice si se pinta la sección. Falso sin base de operadores: sin ella
	// no hay país que situar, y un mapa vacío se leería como «no ha tocado
	// nadie» cuando significa «no se ha instalado la base» — mismo criterio
	// que HayGeo aplica a la columna de operador.
	Hay bool
	// SinDatos es cierto cuando la base está pero el filtro no deja ningún
	// país. La sección se pinta igual y lo dice: una ausencia anunciada se
	// puede comprobar, un hueco en blanco no (ADR-0083).
	SinDatos bool

	Capa         string
	EtiquetaCapa string
	Opciones     []opcionCapa

	// Paises va ordenado de más a menos, y lleva TODOS los que tienen
	// actividad — la lista recorta a TopeListaProcedencia con CSS, no aquí.
	Paises []paisMapa
	// Max es la cifra más alta alcanzada, y es lo que rotula el extremo de la
	// leyenda. El máximo REAL y no el corte más alto de la escala: un eje que
	// dijera «300+» sin decir cuánto es ese «más» prometería menos de lo que
	// se sabe. Mismo criterio que marcasDelEje.
	Max int
	// Cortes son los umbrales de la escala, para que la leyenda los escriba
	// en vez de repetirlos a mano en la plantilla.
	Cortes []int
	// Tope se publica para que el interruptor de «ver todos» pueda decir
	// cuántos se están viendo y cuántos hay.
	Tope int
}

// procedenciaDe agrupa por país las filas que la tabla de orígenes ya tiene
// resueltas.
//
// LAS FILAS QUE NO SON DE INTERNET SE CAEN SOLAS, sin necesidad de mirar el
// filtro de red: unirOrigenes solo resuelve la geo de los orígenes de fuera
// (f.Red.DeFuera()), así que una dirección de la LAN llega aquí con TieneGeo
// en falso. Es la misma razón por la que la columna de operador no necesita
// su propia condición.
func procedenciaDe(origenes []filaOrigen, q url.Values, hayGeo, permitePaquetes bool) panelProcedencia {
	if !hayGeo {
		return panelProcedencia{}
	}

	capa := capaElegida(q, permitePaquetes)
	etiqueta := "rechazos"
	if capa == "paquetes" {
		etiqueta = "paquetes"
	}

	porPais := make(map[string]*paisMapa)
	for _, f := range origenes {
		if !f.TieneGeo || f.Geo.Pais == "" {
			continue
		}
		valor := f.Rechazos
		if capa == "paquetes" {
			valor = f.Toques
		}
		// Un país sin actividad EN ESTA CAPA no entra: pintarlo en el cubo 0
		// sería repetir el color del fondo, y ocuparía una fila de la lista
		// para decir «cero».
		if valor <= 0 {
			continue
		}

		p, ok := porPais[f.Geo.Pais]
		if !ok {
			p = &paisMapa{ISO: f.Geo.Pais, Nombre: geoip.NombreDePais(f.Geo.Pais)}
			porPais[f.Geo.Pais] = p
		}
		p.Valor += valor
		p.Direcciones++
		if f.Ultima.After(p.Ultima) {
			p.Ultima = f.Ultima
		}
		if valor > p.contribucion {
			p.contribucion = valor
			p.Operador = fmt.Sprintf("AS%d %s", f.Geo.ASN, f.Geo.Nombre)
			p.Senal, p.ConSenal = "", false
			if len(f.Senales) > 0 {
				p.Senal, p.ConSenal = f.Senales[0].Etiqueta(), true
			}
		}
	}

	geometria := elementosMapa()
	paises := make([]paisMapa, 0, len(porPais))
	for _, p := range porPais {
		p.Cubo = cuboDe(p.Valor)
		if el, ok := geometria[p.ISO]; ok {
			p.Trazo, p.CX, p.CY, p.Punto, p.EnMapa = el.Trazo, el.CX, el.CY, el.Punto, true
		}
		p.Titulo = fmt.Sprintf("%s · %s · %d %s", p.Nombre, p.ISO, p.Valor, etiqueta)
		p.Lectura = lecturaDe(*p, etiqueta)
		paises = append(paises, *p)
	}

	// De más a menos, y el código como desempate: sin él, dos países con la
	// misma cifra cambiarían de sitio entre dos cargas idénticas porque el
	// recorrido de un mapa de Go no tiene orden.
	sort.Slice(paises, func(i, j int) bool {
		if paises[i].Valor != paises[j].Valor {
			return paises[i].Valor > paises[j].Valor
		}
		return paises[i].ISO < paises[j].ISO
	})

	max := 0
	if len(paises) > 0 {
		max = paises[0].Valor
	}
	// El puesto se numera DESPUÉS de ordenar, que es lo único que lo hace
	// significar algo.
	for i := range paises {
		paises[i].Puesto = i + 1
	}

	return panelProcedencia{
		Hay:          true,
		SinDatos:     len(paises) == 0,
		Capa:         capa,
		EtiquetaCapa: etiqueta,
		Opciones:     opcionesDeCapa(q, capa, permitePaquetes),
		Paises:       paises,
		Max:          max,
		Cortes:       cortesDeCubo[:],
		Tope:         TopeListaProcedencia,
	}
}

// paisesDistintos cuenta los países distintos que hay detrás de los rechazos
// de la ventana. Es la cifra del cuadro «Países» de «Volumen observado».
//
// # POR QUÉ CUENTA RECHAZOS Y NO LA CAPA QUE EL MAPA TENGA PUESTA
//
// Porque es el hermano de «Direcciones», que cuenta IPs distintas SOBRE LOS
// MISMOS rechazos (resumen.IPsUnicas). Los dos cuadros responden la misma
// pregunta a dos alturas —cuántos y desde dónde— y atarlos a capas distintas
// haría que uno se moviera al cambiar un selector que está media pantalla más
// abajo y que el otro ignora.
//
// El mapa dice por su cuenta, en su propio rótulo, cuántos países está
// pintando en SU capa. Son dos cifras distintas a propósito y cada una lleva
// escrito de qué habla.
func (s *Servidor) paisesDistintos(origenes []seguridad.Origen) int {
	if s.geo == nil {
		return 0
	}
	vistos := make(map[string]struct{}, len(origenes))
	for _, o := range origenes {
		if !o.Red.DeFuera() {
			continue
		}
		if info, ok := s.geo.Buscar(o.IP); ok && info.Pais != "" {
			vistos[info.Pais] = struct{}{}
		}
	}
	return len(vistos)
}

// lecturaDe compone la línea que se lee bajo el mapa: quién es ese país, con
// qué operador principal, cuánto ha tocado y cuándo fue la última vez.
//
// Se arma por trozos y no con un formato fijo porque cada pieza puede faltar:
// un país sin señal no debe arrastrar un punto medio suelto, y una fila sin
// instante —que no debería pasar, pero el cero de time.Time existe— no debe
// escribir «último 01/01/0001».
func lecturaDe(p paisMapa, etiquetaCapa string) string {
	partes := []string{p.Nombre, p.ISO}
	if p.Operador != "" {
		partes = append(partes, p.Operador)
	}
	partes = append(partes, fmt.Sprintf("%d %s", p.Valor, etiquetaCapa))

	direcciones := "direcciones"
	if p.Direcciones == 1 {
		direcciones = "dirección"
	}
	partes = append(partes, fmt.Sprintf("%d %s", p.Direcciones, direcciones))

	if p.ConSenal {
		partes = append(partes, p.Senal)
	}
	if !p.Ultima.IsZero() {
		partes = append(partes, "último "+p.Ultima.Local().Format(formatoFecha))
	}
	return strings.Join(partes, " · ")
}

// capaElegida resuelve qué capa pinta el mapa.
//
// «rechazos» es la omisión y no «paquetes», aunque la capa de paquetes sea la
// que ve a un escáner que muere en el saludo TLS: los rechazos existen
// siempre, y los paquetes solo con el sensor instalado y mirando Internet. Un
// panel que abriera en una capa que a veces no hay abriría a veces vacío.
//
// UN «?capa=paquetes» QUE YA NO SE PUEDE SERVIR SE CAE A RECHAZOS EN SILENCIO,
// y es lo correcto: pasa al cambiar el filtro de red con la capa de paquetes
// puesta —el enlace conserva los dos parámetros—, y la alternativa sería
// pintar un mapa vacío que parecería decir «desde ahí no ha tocado nadie».
func capaElegida(q url.Values, permitePaquetes bool) string {
	if q.Get("capa") == "paquetes" && permitePaquetes {
		return "paquetes"
	}
	return "rechazos"
}

func opcionesDeCapa(q url.Values, elegida string, permitePaquetes bool) []opcionCapa {
	out := []opcionCapa{{
		Valor: "rechazos", Etiqueta: "Rechazos",
		Enlace: enlaceConCapa(q, "rechazos"), Elegido: elegida == "rechazos",
	}}
	// El botón de paquetes NO se pinta deshabilitado cuando no se puede:
	// desaparece. Un control apagado invita a preguntarse qué hay que hacer
	// para encenderlo, y la respuesta —instalar el sensor y filtrar por
	// Internet— no cabe en un botón.
	if permitePaquetes {
		out = append(out, opcionCapa{
			Valor: "paquetes", Etiqueta: "Paquetes",
			Enlace: enlaceConCapa(q, "paquetes"), Elegido: elegida == "paquetes",
		})
	}
	return out
}

// enlaceConCapa conserva el resto del filtro y cambia solo la capa.
//
// CONSERVARLO NO ES UN ADORNO: sin esto, cambiar de capa perdería el filtro de
// red, la ventana y el motivo, y el mapa pasaría a hablar de otra cosa que la
// tabla de debajo. Son dos controles ortogonales y tienen que comportarse
// como tales.
//
// La capa por omisión no se escribe en la URL: «/seguridad» y
// «/seguridad?capa=rechazos» son la misma vista, y ensuciar la barra de
// direcciones con el caso normal hace más difícil compartir el enlace útil.
// Por eso «rechazos» viaja como cadena vacía, que es como enlaceDeSeguridad
// dice «quita esta clave».
func enlaceConCapa(q url.Values, capa string) string {
	if capa == "rechazos" {
		capa = ""
	}
	return enlaceDeSeguridad(q, map[string]string{"capa": capa})
}
