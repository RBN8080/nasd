// Package mapa prepares the base map for "Procedencia" in /seguridad: a world
// SVG, one country per element, ready for the web adapter to paint over with
// whatever colours the ring data dictates.
//
// # WHAT THIS IS AND WHAT IT IS NOT
//
// This package does NOT run inside nasd. It is used by a single host binary,
// cmd/preparar-mapa, triggered by hand with "make mapa" when the map needs
// regenerating - which should almost never happen: borders do not change at the
// rate of the geoip country and operator data, which is refreshed monthly. The
// result, internal/adaptadores/web/estatico/mundo.svg, IS COMMITTED to the
// repository and travels embedded in the binary like any other static asset
// (ADR-0017): "make mapa" is a preparation step, just like "nasd
// --preparar-geoip", but one that runs on the host and once, not a timer on the
// node.
//
// # WHERE THE COUNTRY SHAPES COME FROM
//
// From Natural Earth 110m (naturalearthdata.com), public domain and with no
// account or key to manage - the same criterion by which geoip.go discarded
// GeoLite2. The input is its countries GeoJSON
// ("ne_110m_admin_0_countries.geojson"); it is not downloaded here, the caller
// brings it, just as 18_geoip.sh brings the IPtoASN TSVs before calling "nasd
// --preparar-geoip".
//
// # THE PROJECTION, AND WHY MILLER AND NOT ANOTHER
//
// Miller cylindrical: it exaggerates the poles less than Mercator without the
// area distortion of an azimuthal one, and it is a closed formula of two lines
// - no cartographic projection library is needed to draw six colour buckets
// over a world background.
//
// # THE SIMPLIFICATION, AND WHY IT IS NEEDED
//
// The source GeoJSON carries thousands of vertices per country so it can be
// printed on paper at 1:110,000,000. This graphic is 1000x501 viewBox units: a
// vertex below the Douglas-Peucker tolerance changes not one pixel of the
// result and does inflate the weight of the file shipped on every load of
// /seguridad. Rings that survive simplification and still fall below the
// minimum area - islets that at this scale are a dot with no surface to fill -
// are discarded whole.
package mapa

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
)

// Geometría del viewBox de salida. Cien de ancho hubiera bastado para un
// trazo cualquiera, pero mil da un paso de simplificación con margen antes de
// que la textura se note, y es la misma razón por la que las gráficas de
// grafica_seguridad.go trabajan en unidades finas y dejan que el navegador
// escale.
const (
	AnchoViewBox = 1000.0

	// latMin y latMax recortan la proyección. latMin deja fuera la
	// Antártida —que además se descarta explícitamente más abajo, por si
	// algún día un país reclamado se acerca al borde del recorte— y latMax
	// deja fuera del recuadro únicamente el casquete polar ártico, que no
	// tiene tráfico de Internet que resolver.
	latMin = -58.0
	latMax = 84.0

	// toleranciaSimplificado es la distancia máxima, en unidades del
	// viewBox de salida, que Douglas-Peucker deja perder a un vértice antes
	// de conservarlo. Se eligió mirando el resultado: por debajo de esto el
	// contorno de países grandes (Rusia, Canadá, Brasil) no cambia a ojo, y
	// por encima empiezan a perderse penínsulas que ayudan a reconocer la
	// silueta.
	toleranciaSimplificado = 0.55

	// areaMinima descarta anillos —tras simplificar— más pequeños que esto,
	// en unidades de área del viewBox. Un islote que a esta escala mide
	// menos de un punto no aporta silueta y sí una «M…Z» que hay que
	// transmitir en cada carga.
	areaMinima = 0.9

	// RadioPunto es el radio del círculo con el que se marca un país sin
	// forma a esta resolución (ver puntosManuales). El mismo valor lo
	// necesita el adaptador web para calcular el aria-label y el <title>,
	// así que se exporta.
	RadioPunto = 5.5
)

// puntoManual es un país que Natural Earth 110m no incluye como polígono
// —demasiado pequeño para esa escala— y que este paquete marca como un
// círculo en su capital, o en su centroide cuando no hay una capital que
// sirva mejor de referencia visual.
//
// SOLO ENTRAN AQUÍ LOS QUE DE VERDAD APARECEN EN EL PANEL. Los dos primeros
// —Singapur y Hong Kong— no son un capricho de cobertura: son origen
// habitual de barridos alojados en nube (el propio 04_SEGURIDAD.md los
// nombra), así que perderlos en silencio del mapa sería peor que no tener
// mapa. Añadir uno nuevo es una línea; no hace falta tocar nada más.
type puntoManual struct {
	Lon, Lat float64
}

var puntosManuales = map[string]puntoManual{
	"SG": {103.8198, 1.3521},  // Singapur
	"HK": {114.1694, 22.3193}, // Hong Kong
}

// paisesOmitidos son ISO_A3 que no se dibujan aunque el GeoJSON los traiga.
// Solo la Antártida: sin población ni tráfico de Internet propio, y su
// polígono real —más allá del recorte de latitud— no aporta nada a un mapa
// que existe para leer PROCEDENCIA de tráfico.
var paisesOmitidos = map[string]bool{
	"ATA": true,
}

// Elemento es un país ya resuelto a coordenadas de pantalla: o un trazo de
// <path> (el caso normal) o un punto (puntosManuales).
type Elemento struct {
	ISO   string
	Trazo string // el atributo «d» de un <path>; vacío si Punto
	CX    float64
	CY    float64
	Punto bool
}

// Preparar lee el GeoJSON de Natural Earth 110m y escribe el SVG del mundo
// en destino. Devuelve cuántos países quedaron, para que quien llama pueda
// exigir un mínimo razonable — el mismo criterio con el que
// prepararBaseGeoIP desconfía de un «terminó sin errores» que podría
// significar «leyó un archivo vacío».
func Preparar(entrada io.Reader, destino io.Writer, fuente, huellaFuente string) (int, error) {
	elementos, err := procesar(entrada)
	if err != nil {
		return 0, err
	}
	if len(elementos) == 0 {
		return 0, fmt.Errorf("ningún país sobrevivió a la conversión — ¿el GeoJSON de entrada es el correcto?")
	}
	if err := escribirSVG(destino, elementos, fuente, huellaFuente); err != nil {
		return 0, err
	}
	return len(elementos), nil
}

// procesar decodifica el GeoJSON, proyecta y simplifica cada país, y añade
// los puntos manuales que la propia fuente no trae.
func procesar(entrada io.Reader) ([]Elemento, error) {
	var coleccion geoColeccion
	dec := json.NewDecoder(entrada)
	if err := dec.Decode(&coleccion); err != nil {
		return nil, fmt.Errorf("decodificar el GeoJSON: %w", err)
	}

	x0, _ := miller(-180, latMin)
	x1, y1 := miller(180, latMax)
	escala := AnchoViewBox / (x1 - x0)
	proyecta := func(lon, lat float64) (float64, float64) {
		x, y := miller(lon, lat)
		return (x - x0) * escala, (y1 - y) * escala
	}

	vistos := make(map[string]bool, len(coleccion.Features))
	var out []Elemento
	for _, f := range coleccion.Features {
		iso, ok := iso2De(f.Propiedades)
		if !ok || paisesOmitidos[iso3De(f.Propiedades)] {
			continue
		}
		anillos := anillosDe(f.Geometria)
		var trazo strings.Builder
		for _, anillo := range anillos {
			pts := make([]punto, len(anillo))
			for i, c := range anillo {
				x, y := proyecta(c[0], c[1])
				pts[i] = punto{x, y}
			}
			pts = douglasPeucker(pts, toleranciaSimplificado)
			if len(pts) < 4 || area(pts) < areaMinima {
				continue
			}
			trazo.WriteString(rutaDe(pts))
		}
		if trazo.Len() == 0 {
			continue
		}
		out = append(out, Elemento{ISO: iso, Trazo: trazo.String()})
		vistos[iso] = true
	}

	// Los puntos manuales solo se añaden si el propio GeoJSON no trajo YA
	// un contorno para ese país: si algún día Natural Earth incorpora la
	// forma de Singapur a esta escala, la entrada manual deja de tener
	// efecto sin que haga falta tocar este código.
	claves := make([]string, 0, len(puntosManuales))
	for iso := range puntosManuales {
		claves = append(claves, iso)
	}
	sort.Strings(claves) // salida determinista: el diff de mundo.svg no baila entre corridas
	for _, iso := range claves {
		if vistos[iso] {
			continue
		}
		p := puntosManuales[iso]
		cx, cy := proyecta(p.Lon, p.Lat)
		out = append(out, Elemento{ISO: iso, CX: cx, CY: cy, Punto: true})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].ISO < out[j].ISO })
	return out, nil
}

// ── GeoJSON, lo mínimo para leer un FeatureCollection de países ───────────

type geoColeccion struct {
	Features []geoFeature `json:"features"`
}

type geoFeature struct {
	Propiedades map[string]any `json:"properties"`
	Geometria   geoGeometria   `json:"geometry"`
}

type geoGeometria struct {
	Tipo        string `json:"type"`
	Coordenadas any    `json:"coordinates"`
}

// iso2De resuelve el código de dos letras del país.
//
// SE PRUEBA ISO_A2_EH ANTES QUE ISO_A2, y no al revés. Natural Earth marca
// con «-99» el campo ISO_A2 de algunos países con disputas de soberanía —en
// la versión 110m, Francia, Noruega y Kosovo—, y dedica «_EH» («de facto»)
// a la variante que sí lleva un código utilizable. Probar primero ISO_A2 se
// tradujo, en la primera corrida de este preparador, en perder Francia y
// Noruega del mapa: dos de los orígenes más comunes en la tabla de
// operadores no aparecían y nada avisaba de por qué.
func iso2De(props map[string]any) (string, bool) {
	for _, campo := range []string{"ISO_A2_EH", "ISO_A2"} {
		if v, ok := props[campo].(string); ok && v != "" && v != "-99" {
			return v, true
		}
	}
	return "", false
}

func iso3De(props map[string]any) string {
	if v, ok := props["ISO_A3"].(string); ok {
		return v
	}
	return ""
}

// anillosDe aplana Polygon y MultiPolygon a una lista de anillos EXTERIORES
// —el primero de cada polígono—. Los agujeros (anillos interiores, como el
// de Lesoto dentro de Sudáfrica) se descartan a propósito: a esta escala, y
// pintando un solo color sólido por país, un agujero que ni Douglas-Peucker
// ni el ojo notarían no vale la complejidad de un «d» con regla par-impar.
func anillosDe(g geoGeometria) [][][2]float64 {
	var out [][][2]float64
	switch g.Tipo {
	case "Polygon":
		if anillo, ok := primerAnillo(g.Coordenadas); ok {
			out = append(out, anillo)
		}
	case "MultiPolygon":
		polys, ok := g.Coordenadas.([]any)
		if !ok {
			return nil
		}
		for _, p := range polys {
			if anillo, ok := primerAnillo(p); ok {
				out = append(out, anillo)
			}
		}
	}
	return out
}

// primerAnillo lee el anillo exterior de UN polígono, tal como json.Decode
// lo deja: []any de []any de []any de float64.
func primerAnillo(poligono any) ([][2]float64, bool) {
	anillos, ok := poligono.([]any)
	if !ok || len(anillos) == 0 {
		return nil, false
	}
	crudo, ok := anillos[0].([]any)
	if !ok {
		return nil, false
	}
	out := make([][2]float64, 0, len(crudo))
	for _, punto := range crudo {
		par, ok := punto.([]any)
		if !ok || len(par) < 2 {
			continue
		}
		lon, ok1 := par[0].(float64)
		lat, ok2 := par[1].(float64)
		if !ok1 || !ok2 {
			continue
		}
		out = append(out, [2]float64{lon, lat})
	}
	if len(out) < 3 {
		return nil, false
	}
	return out, true
}

// ── Proyección ─────────────────────────────────────────────────────────

// miller proyecta longitud/latitud en grados a un plano, sin normalizar
// todavía al viewBox de salida — eso lo hace la escala que calcula
// procesar a partir de las dos esquinas del recorte.
//
// Se acota la latitud a ±89.5° antes de aplicar la fórmula: a exactamente
// ±90° el logaritmo de la proyección de Miller diverge a infinito, y ningún
// polígono de un país real llega tan lejos — es una guarda contra un dato de
// entrada corrupto, no un caso que se espere ejercitar.
func miller(lonGrados, latGrados float64) (x, y float64) {
	lat := latGrados
	if lat > 89.5 {
		lat = 89.5
	}
	if lat < -89.5 {
		lat = -89.5
	}
	x = lonGrados * math.Pi / 180
	y = 1.25 * math.Log(math.Tan(math.Pi/4+0.4*lat*math.Pi/180))
	return x, y
}

// ── Simplificación y geometría ────────────────────────────────────────

type punto struct{ X, Y float64 }

// douglasPeucker conserva los dos extremos del trazo y cualquier vértice
// intermedio que se aleje de la línea recta entre ellos más que tolerancia,
// aplicándose recursivamente a los dos tramos que ese vértice separa. Es el
// algoritmo estándar para esto — no hay razón para reinventarlo — recortado
// a la firma exacta que este paquete necesita: puntos de entrada y salida en
// las mismas unidades del viewBox de destino.
func douglasPeucker(pts []punto, tolerancia float64) []punto {
	if len(pts) < 3 {
		return pts
	}
	dmax, idx := 0.0, 0
	p1, p2 := pts[0], pts[len(pts)-1]
	dx, dy := p2.X-p1.X, p2.Y-p1.Y
	norma := math.Hypot(dx, dy)
	for i := 1; i < len(pts)-1; i++ {
		p := pts[i]
		var d float64
		if norma == 0 {
			d = math.Hypot(p.X-p1.X, p.Y-p1.Y)
		} else {
			d = math.Abs(dy*p.X-dx*p.Y+p2.X*p1.Y-p2.Y*p1.X) / norma
		}
		if d > dmax {
			dmax, idx = d, i
		}
	}
	if dmax > tolerancia {
		izq := douglasPeucker(pts[:idx+1], tolerancia)
		der := douglasPeucker(pts[idx:], tolerancia)
		return append(izq[:len(izq)-1], der...)
	}
	return []punto{p1, p2}
}

// area es el área del polígono cerrado por la fórmula del cordón (shoelace),
// en las mismas unidades del viewBox. Sirve solo para comparar contra
// areaMinima, así que el signo —que diría el sentido de recorrido— se
// descarta con el valor absoluto.
func area(pts []punto) float64 {
	var s float64
	for i := range pts {
		j := (i + 1) % len(pts)
		s += pts[i].X*pts[j].Y - pts[j].X*pts[i].Y
	}
	return math.Abs(s) / 2
}

// rutaDe escribe un anillo cerrado como subtrazo SVG: «M x y L x y … Z».
func rutaDe(pts []punto) string {
	var b strings.Builder
	for i, p := range pts {
		if i == 0 {
			fmt.Fprintf(&b, "M%.1f %.1f", p.X, p.Y)
		} else {
			fmt.Fprintf(&b, "L%.1f %.1f", p.X, p.Y)
		}
	}
	b.WriteByte('Z')
	return b.String()
}

// ── Escritura del SVG ──────────────────────────────────────────────────

// escribirSVG produce el archivo tal como lo espera el adaptador web:
// un <path id="XX" d="…"> por país con forma, un <circle id="XX" cx="…"
// cy="…"> por país marcado a mano, y un color de relleno fijo — el
// «tierra» de la consola (ADR-0075) — porque esta imagen se sirve tal cual
// dentro de un <img> como fondo, y un <img> no hereda ninguna clase CSS de
// la página que lo incrusta. La capa que SÍ cambia de color según los datos
// la construye web/mapa_seguridad.go por encima, leyendo estos mismos
// trazos con las clases b0…b5 en vez del fill fijo — ver ADR-0089.
func escribirSVG(w io.Writer, elementos []Elemento, fuente, huellaFuente string) error {
	x0, y0 := miller(-180, latMin)
	x1, y1 := miller(180, latMax)
	escala := AnchoViewBox / (x1 - x0)
	alto := (y1 - y0) * escala

	// NI UN SOLO «--» DENTRO DEL COMENTARIO, y no es una manía de estilo: un
	// comentario XML no puede contener dos guiones seguidos (XML 1.0 §2.5).
	// Este archivo se sirve dentro de un <img>, y ahí el navegador lo parsea
	// como XML ESTRICTO: un «--» en la cabecera invalida el documento entero
	// y la imagen no carga. En línea dentro del HTML sí funcionaba —ese
	// parser es indulgente—, así que el defecto solo se ve al mirar la
	// página, que es donde se vio.
	fmt.Fprintf(w, "<!-- mundo.svg · generado por cmd/preparar-mapa (make mapa). NO EDITAR A MANO.\n")
	fmt.Fprintf(w, "     Fuente: %s\n", fuente)
	fmt.Fprintf(w, "     SHA-256 de esa entrada: %s -->\n", huellaFuente)
	fmt.Fprintf(w, "<svg xmlns=\"http://www.w3.org/2000/svg\" viewBox=\"0 0 %.0f %.1f\">\n", AnchoViewBox, alto)
	for _, e := range elementos {
		if e.Punto {
			fmt.Fprintf(w, "<circle id=\"%s\" cx=\"%.1f\" cy=\"%.1f\" r=\"%.1f\" fill=\"#1b1e24\"/>\n",
				e.ISO, e.CX, e.CY, RadioPunto)
			continue
		}
		fmt.Fprintf(w, "<path id=\"%s\" d=\"%s\" fill=\"#1b1e24\"/>\n", e.ISO, e.Trazo)
	}
	fmt.Fprint(w, "</svg>\n")
	return nil
}
