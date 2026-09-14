package mapa

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"io"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// caja construye un anillo rectangular cerrado — lo bastante para ejercitar
// la proyección y la simplificación sin depender de un archivo externo. No
// hace falta que se parezca a la costa real de nadie: lo que se comprueba es
// dónde cae en el plano, no si dibuja Baja California.
func caja(lon0, lat0, lon1, lat1 float64) [][]float64 {
	return [][]float64{
		{lon0, lat0}, {lon1, lat0}, {lon1, lat1}, {lon0, lat1}, {lon0, lat0},
	}
}

// featurePropiedades arma las propiedades de una feature con la forma real
// que trae Natural Earth: ISO_A2 puede faltar o venir «-99», ISO_A2_EH es el
// que hay que probar primero.
func featurePropiedades(iso2, iso2eh, iso3 string) map[string]any {
	p := map[string]any{}
	if iso3 != "" {
		p["ISO_A3"] = iso3
	}
	if iso2 != "" {
		p["ISO_A2"] = iso2
	}
	if iso2eh != "" {
		p["ISO_A2_EH"] = iso2eh
	}
	return p
}

func poligono(iso2, iso2eh, iso3 string, anillo [][]float64) map[string]any {
	return map[string]any{
		"type":       "Feature",
		"properties": featurePropiedades(iso2, iso2eh, iso3),
		"geometry": map[string]any{
			"type":        "Polygon",
			"coordinates": [][][]float64{anillo},
		},
	}
}

// multipoligono arma un MultiPolygon con un polígono por anillo recibido —
// cada uno con su único anillo exterior, que es lo único que primerAnillo
// necesita leer.
func multipoligono(iso2, iso3 string, anillos ...[][]float64) map[string]any {
	coords := make([][][][]float64, len(anillos))
	for i, anillo := range anillos {
		coords[i] = [][][]float64{anillo}
	}
	return map[string]any{
		"type":       "Feature",
		"properties": featurePropiedades(iso2, "", iso3),
		"geometry": map[string]any{
			"type":        "MultiPolygon",
			"coordinates": coords,
		},
	}
}

// coleccionDePrueba junta las features en un FeatureCollection y lo
// serializa — así el decodificador de procesar() ve exactamente la forma
// que verá con un GeoJSON real, en vez de un valor Go a medio camino.
func coleccionDePrueba(t *testing.T, features ...map[string]any) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"type":     "FeatureCollection",
		"features": features,
	})
	if err != nil {
		t.Fatalf("serializar el GeoJSON de prueba: %v", err)
	}
	return b
}

func prepararDePrueba(t *testing.T, entrada []byte) string {
	t.Helper()
	var out bytes.Buffer
	n, err := Preparar(bytes.NewReader(entrada), &out, "prueba", "deadbeef")
	if err != nil {
		t.Fatalf("Preparar: %v", err)
	}
	if n <= 0 {
		t.Fatalf("Preparar devolvió %d países", n)
	}
	return out.String()
}

// fixture: México al oeste, China al este, Brasil al sur, un islote
// minúsculo, Francia con el ISO_A2 en «-99» (como lo trae de verdad Natural
// Earth), la Antártida, y Estados Unidos como MultiPolygon con una isla
// aparte — todo lo que procesar() tiene que distinguir.
func fixtureCompleta(t *testing.T) []byte {
	t.Helper()
	return coleccionDePrueba(t,
		poligono("MX", "", "MEX", caja(-110, 15, -90, 30)),
		poligono("CN", "", "CHN", caja(100, 20, 120, 40)),
		poligono("BR", "", "BRA", caja(-60, -30, -40, -10)),
		poligono("", "FR", "FRA", caja(-5, 43, 8, 50)), // ISO_A2 ausente, como -99
		poligono("AQ", "", "ATA", caja(-170, -89, 170, -61)),
		poligono("XX", "", "ISL", [][]float64{ // islote: triángulo de una milésima de grado
			{10.0000, 10.0000}, {10.0001, 10.0000}, {10.0000, 10.0001}, {10.0000, 10.0000},
		}),
		multipoligono("US", "USA",
			caja(-125, 25, -70, 49),
			caja(-160, 19, -154, 22), // una «isla» aparte, mismo país
		),
	)
}

func TestPaisesGrandesQuedanEnSuLugar(t *testing.T) {
	svg := prepararDePrueba(t, fixtureCompleta(t))

	for _, iso := range []string{"MX", "CN", "BR", "FR", "US"} {
		if !strings.Contains(svg, `id="`+iso+`"`) {
			t.Errorf("falta %q en el SVG", iso)
		}
	}

	// MX al oeste de CN: coordenada X del primer punto de cada uno.
	xMX := primerX(t, svg, "MX")
	xCN := primerX(t, svg, "CN")
	if xMX >= xCN {
		t.Errorf("México (x=%.1f) debería quedar al oeste de China (x=%.1f)", xMX, xCN)
	}

	// BR al sur de MX: en el viewBox, sur es Y mayor (el origen del SVG es
	// arriba-izquierda, y latMax se proyecta a y=0).
	yMX := primerY(t, svg, "MX")
	yBR := primerY(t, svg, "BR")
	if yBR <= yMX {
		t.Errorf("Brasil (y=%.1f) debería quedar más al sur que México (y=%.1f) — Y mayor", yBR, yMX)
	}
}

func TestLaAntartidaSeOmiteAunqueVengaEnLaFuente(t *testing.T) {
	svg := prepararDePrueba(t, fixtureCompleta(t))
	if strings.Contains(svg, `id="AQ"`) {
		t.Error("la Antártida (AQ / ATA) no debería aparecer en el mapa")
	}
}

func TestIslotesMinusculosSeDescartan(t *testing.T) {
	svg := prepararDePrueba(t, fixtureCompleta(t))
	if strings.Contains(svg, `id="XX"`) {
		t.Error("un anillo por debajo del área mínima no debería sobrevivir a la conversión")
	}
}

func TestMultiPoligonoConservaLasDosPartes(t *testing.T) {
	svg := prepararDePrueba(t, fixtureCompleta(t))
	d := trazoDe(t, svg, "US")
	if n := strings.Count(d, "M"); n != 2 {
		t.Errorf("Estados Unidos como MultiPolygon debería dejar 2 subtrazos «M…Z», hay %d", n)
	}
}

func TestFranciaResuelvePorISOA2EH(t *testing.T) {
	// Natural Earth marca Francia con ISO_A2="-99" (aquí, ausente) y
	// ISO_A2_EH="FR": si el preparador probara ISO_A2 primero, Francia
	// desaparecería del mapa sin que nada avisara.
	svg := prepararDePrueba(t, fixtureCompleta(t))
	if !strings.Contains(svg, `id="FR"`) {
		t.Fatal("Francia debería resolverse por ISO_A2_EH")
	}
}

func TestFeatureSinISO2SeOmiteSinError(t *testing.T) {
	entrada := coleccionDePrueba(t,
		poligono("MX", "", "MEX", caja(-110, 15, -90, 30)),
		poligono("", "", "ZZZ", caja(0, 0, 5, 5)), // sin ISO_A2 ni ISO_A2_EH
	)
	svg := prepararDePrueba(t, entrada)
	if !strings.Contains(svg, `id="MX"`) {
		t.Error("México debería seguir presente")
	}
	if strings.Contains(svg, `id="ZZZ"`) {
		t.Error("una feature sin ISO2 utilizable no debería aparecer con ningún id")
	}
}

func TestPuntosManualesSoloCuandoFaltaLaForma(t *testing.T) {
	// Sin Singapur ni Hong Kong en la fuente: los dos deben salir como
	// <circle>, que es la marca de «país sin forma a esta resolución».
	svg := prepararDePrueba(t, fixtureCompleta(t))
	for _, iso := range []string{"SG", "HK"} {
		if !strings.Contains(svg, `<circle id="`+iso+`"`) {
			t.Errorf("%s debería aparecer como punto manual", iso)
		}
	}

	// Si la fuente SÍ trae a Singapur como polígono, el punto manual no
	// debe duplicarlo.
	conSG := coleccionDePrueba(t,
		poligono("MX", "", "MEX", caja(-110, 15, -90, 30)),
		poligono("SG", "", "SGP", caja(103.6, 1.2, 104.0, 1.5)),
	)
	svg2 := prepararDePrueba(t, conSG)
	if strings.Contains(svg2, `<circle id="SG"`) {
		t.Error("Singapur ya tenía forma propia: no debería añadirse también como punto")
	}
	if !strings.Contains(svg2, `<path id="SG"`) {
		t.Error("Singapur debería aparecer como <path>, no como punto, cuando la fuente trae su forma")
	}
}

func TestElResultadoCabeDentroDelViewBox(t *testing.T) {
	svg := prepararDePrueba(t, fixtureCompleta(t))

	m := regexp.MustCompile(`viewBox="0 0 ([\d.]+) ([\d.]+)"`).FindStringSubmatch(svg)
	if m == nil {
		t.Fatal("no se encontró el viewBox en el SVG")
	}
	ancho, _ := strconv.ParseFloat(m[1], 64)
	alto, _ := strconv.ParseFloat(m[2], 64)
	if ancho != AnchoViewBox {
		t.Errorf("ancho del viewBox = %.1f, se esperaba %.1f", ancho, AnchoViewBox)
	}

	const margen = 0.5 // redondeo de %.1f al escribir
	for _, x := range todasLasCoordenadasX(svg) {
		if x < -margen || x > ancho+margen {
			t.Errorf("coordenada X %.2f fuera del viewBox [0, %.1f]", x, ancho)
		}
	}
	for _, y := range todasLasCoordenadasY(svg) {
		if y < -margen || y > alto+margen {
			t.Errorf("coordenada Y %.2f fuera del viewBox [0, %.1f]", y, alto)
		}
	}
}

func TestLaSalidaEsDeterminista(t *testing.T) {
	entrada := fixtureCompleta(t)
	a := prepararDePrueba(t, entrada)
	b := prepararDePrueba(t, entrada)
	if a != b {
		t.Error("dos conversiones del mismo GeoJSON deberían producir bytes idénticos — el orden de los países no puede depender del recorrido de un mapa de Go")
	}
}

func TestCabeceraDocumentaFuenteYHuella(t *testing.T) {
	var out bytes.Buffer
	if _, err := Preparar(bytes.NewReader(fixtureCompleta(t)), &out, "https://ejemplo/x.geojson", "abc123"); err != nil {
		t.Fatalf("Preparar: %v", err)
	}
	svg := out.String()
	if !strings.Contains(svg, "https://ejemplo/x.geojson") {
		t.Error("la cabecera debería documentar la URL de origen")
	}
	if !strings.Contains(svg, "abc123") {
		t.Error("la cabecera debería documentar la huella SHA-256 de la entrada")
	}
}

func TestGeoJSONMalformadoDaError(t *testing.T) {
	var out bytes.Buffer
	if _, err := Preparar(strings.NewReader("esto no es json"), &out, "x", "y"); err == nil {
		t.Fatal("se esperaba un error con un GeoJSON ilegible")
	}
}

func TestISO2DePrefiereEHSobreISOA2(t *testing.T) {
	casos := []struct {
		nombre string
		props  map[string]any
		quiere string
		ok     bool
	}{
		{"EH corrige el -99", map[string]any{"ISO_A2": "-99", "ISO_A2_EH": "FR"}, "FR", true},
		{"EH manda aunque ISO_A2 también sea válido", map[string]any{"ISO_A2": "MX", "ISO_A2_EH": "ZZ"}, "ZZ", true},
		{"solo ISO_A2", map[string]any{"ISO_A2": "DE"}, "DE", true},
		{"los dos en -99", map[string]any{"ISO_A2": "-99", "ISO_A2_EH": "-99"}, "", false},
		{"ninguno de los dos campos", map[string]any{}, "", false},
	}
	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			got, ok := iso2De(c.props)
			if ok != c.ok || got != c.quiere {
				t.Errorf("iso2De(%v) = %q, %v — quería %q, %v", c.props, got, ok, c.quiere, c.ok)
			}
		})
	}
}

// ── ayudantes de la prueba ─────────────────────────────────────────────

func trazoDe(t *testing.T, svg, iso string) string {
	t.Helper()
	m := regexp.MustCompile(`<path id="` + iso + `" d="([^"]+)"`).FindStringSubmatch(svg)
	if m == nil {
		t.Fatalf("no se encontró el trazo de %q", iso)
	}
	return m[1]
}

var reNumero = regexp.MustCompile(`[ML](-?[\d.]+) (-?[\d.]+)`)

func primerX(t *testing.T, svg, iso string) float64 {
	t.Helper()
	m := reNumero.FindStringSubmatch(trazoDe(t, svg, iso))
	if m == nil {
		t.Fatalf("no se encontró ningún punto en el trazo de %q", iso)
	}
	v, _ := strconv.ParseFloat(m[1], 64)
	return v
}

func primerY(t *testing.T, svg, iso string) float64 {
	t.Helper()
	m := reNumero.FindStringSubmatch(trazoDe(t, svg, iso))
	if m == nil {
		t.Fatalf("no se encontró ningún punto en el trazo de %q", iso)
	}
	v, _ := strconv.ParseFloat(m[2], 64)
	return v
}

func todasLasCoordenadasX(svg string) []float64 {
	var out []float64
	for _, m := range reNumero.FindAllStringSubmatch(svg, -1) {
		v, _ := strconv.ParseFloat(m[1], 64)
		out = append(out, v)
	}
	for _, m := range regexp.MustCompile(`cx="(-?[\d.]+)"`).FindAllStringSubmatch(svg, -1) {
		v, _ := strconv.ParseFloat(m[1], 64)
		out = append(out, v)
	}
	return out
}

func todasLasCoordenadasY(svg string) []float64 {
	var out []float64
	for _, m := range reNumero.FindAllStringSubmatch(svg, -1) {
		v, _ := strconv.ParseFloat(m[2], 64)
		out = append(out, v)
	}
	for _, m := range regexp.MustCompile(`cy="(-?[\d.]+)"`).FindAllStringSubmatch(svg, -1) {
		v, _ := strconv.ParseFloat(m[1], 64)
		out = append(out, v)
	}
	return out
}

// TestElSVGEsXMLValido es la prueba que nace de un defecto real, visto en el
// navegador y no en ninguna suite: el archivo se sirve dentro de un <img>, y
// ahí el navegador lo parsea como XML ESTRICTO. La primera cabecera decía
// «mundo.svg -- generado por…», y un comentario XML NO puede contener dos
// guiones seguidos (XML 1.0 §2.5): el documento entero dejaba de ser válido y
// el fondo del mapa no cargaba. En línea dentro del HTML sí funcionaba,
// porque ese parser es indulgente — por eso no lo cazó ninguna prueba.
func TestElSVGEsXMLValido(t *testing.T) {
	svg := prepararDePrueba(t, fixtureCompleta(t))

	// 1. Bien formado de verdad, según el parser de la biblioteca estándar.
	dec := xml.NewDecoder(strings.NewReader(svg))
	for {
		_, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("el SVG no es XML válido: %v", err)
		}
	}

	// 2. Y el caso concreto que lo rompió, dicho por su nombre para que el
	//    día que alguien vuelva a escribir «--» en la cabecera lea por qué no.
	const abre = "<!--"
	cierre := strings.Index(svg, "-->")
	if cierre < 0 || !strings.HasPrefix(svg, abre) {
		t.Fatal("no se encontró el comentario de cabecera")
	}
	// Desde DESPUÉS del «<!--», que lleva sus propios dos guiones.
	if strings.Contains(svg[len(abre):cierre], "--") {
		t.Error("la cabecera lleva «--» dentro del comentario: el <img> no cargará")
	}
}
