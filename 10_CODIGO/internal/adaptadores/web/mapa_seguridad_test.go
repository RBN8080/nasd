package web

import (
	"io"
	"log/slog"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nasd/internal/autenticacion"
	"nasd/internal/geoip"
	"nasd/internal/seguridad"
)

// filaDePrueba arma una fila de la tabla de orígenes ya resuelta, que es
// exactamente lo que procedenciaDe recibe en la página real.
func filaDePrueba(ip, pais, operador string, asn uint32, rechazos, toques int) filaOrigen {
	return filaOrigen{
		IP:       netip.MustParseAddr(ip),
		Red:      seguridad.RedInternet,
		Geo:      geoip.Info{ASN: asn, Pais: pais, Nombre: operador},
		TieneGeo: pais != "",
		Rechazos: rechazos,
		Toques:   toques,
		Ultima:   time.Date(2026, 9, 13, 9, 22, 0, 0, time.Local),
	}
}

func TestLosCortesDeCuboCaenDondeSeDice(t *testing.T) {
	casos := []struct {
		valor int
		cubo  int
	}{
		{0, 0},
		{1, 1}, {4, 1},
		{5, 2}, {19, 2},
		{20, 3}, {79, 3},
		{80, 4}, {299, 4},
		{300, 5}, {5000, 5},
	}
	for _, c := range casos {
		if got := cuboDe(c.valor); got != c.cubo {
			t.Errorf("cuboDe(%d) = %d, se esperaba %d", c.valor, got, c.cubo)
		}
	}
}

func TestProcedenciaAgrupaPorPaisYOrdena(t *testing.T) {
	filas := []filaOrigen{
		filaDePrueba("203.0.113.1", "CN", "CHINANET", 4134, 100, 5),
		filaDePrueba("203.0.113.2", "CN", "CHINANET", 4134, 172, 8),
		filaDePrueba("198.51.100.7", "GB", "DRIFTNET", 211298, 677, 3),
		filaDePrueba("192.0.2.9", "DE", "HETZNER", 24940, 63, 1),
	}
	p := procedenciaDe(filas, url.Values{}, true, false)

	if !p.Hay || p.SinDatos {
		t.Fatalf("la sección debería pintarse con datos: Hay=%v SinDatos=%v", p.Hay, p.SinDatos)
	}
	if len(p.Paises) != 3 {
		t.Fatalf("se esperaban 3 países, hay %d", len(p.Paises))
	}
	// De más a menos: GB (677), CN (272 = 100+172), DE (63).
	quiere := []struct {
		iso   string
		valor int
	}{{"GB", 677}, {"CN", 272}, {"DE", 63}}
	for i, q := range quiere {
		if p.Paises[i].ISO != q.iso || p.Paises[i].Valor != q.valor {
			t.Errorf("puesto %d: %s con %d, se esperaba %s con %d",
				i+1, p.Paises[i].ISO, p.Paises[i].Valor, q.iso, q.valor)
		}
		if p.Paises[i].Puesto != i+1 {
			t.Errorf("%s: Puesto = %d, se esperaba %d", q.iso, p.Paises[i].Puesto, i+1)
		}
	}
	if p.Max != 677 {
		t.Errorf("Max = %d, se esperaba el máximo real (677)", p.Max)
	}
	// Las dos direcciones de China tienen que contarse como dos, que es lo
	// que dice que el operador es el principal y no el único.
	if p.Paises[1].Direcciones != 2 {
		t.Errorf("China: Direcciones = %d, se esperaban 2", p.Paises[1].Direcciones)
	}
}

// TestProcedenciaNombraLosPaisesEnEspanol comprueba la única pieza nueva que
// cruza desde geoip: sin ella el mapa diría «CN» y volveríamos al problema
// que geoip.go describe — un código que no le dice nada a nadie.
func TestProcedenciaNombraLosPaisesEnEspanol(t *testing.T) {
	p := procedenciaDe([]filaOrigen{
		filaDePrueba("203.0.113.1", "CN", "CHINANET", 4134, 10, 0),
	}, url.Values{}, true, false)
	if p.Paises[0].Nombre != "China" {
		t.Errorf("Nombre = %q, se esperaba «China»", p.Paises[0].Nombre)
	}
	if !strings.Contains(p.Paises[0].Titulo, "China") {
		t.Errorf("el <title> debería llevar el nombre: %q", p.Paises[0].Titulo)
	}
}

// TestSinBaseDeOperadoresNoSePintaLaSeccion es el criterio 1 de RF-30: la
// ausencia de la base no impide que el resto del panel funcione.
func TestSinBaseDeOperadoresNoSePintaLaSeccion(t *testing.T) {
	p := procedenciaDe([]filaOrigen{
		filaDePrueba("203.0.113.1", "CN", "CHINANET", 4134, 10, 0),
	}, url.Values{}, false, false)
	if p.Hay {
		t.Error("sin base de operadores la sección no debería pintarse")
	}
}

// TestLosOrigenesDeCasaNoLleganAlMapa: unirOrigenes solo resuelve la geo de
// los de fuera, así que una dirección de la LAN llega con TieneGeo en falso y
// tiene que caerse sola, sin que esta función mire el filtro de red.
func TestLosOrigenesDeCasaNoLleganAlMapa(t *testing.T) {
	lan := filaOrigen{
		IP:       netip.MustParseAddr("192.168.1.50"),
		Red:      seguridad.RedLocal,
		Rechazos: 500,
	}
	p := procedenciaDe([]filaOrigen{lan}, url.Values{}, true, false)
	if !p.SinDatos {
		t.Errorf("una dirección de la LAN no debería situarse en el mapa: %d países", len(p.Paises))
	}
}

func TestUnPaisSinActividadEnLaCapaNoSale(t *testing.T) {
	// Esta dirección tiene rechazos pero CERO paquetes: con la capa de
	// paquetes puesta no debe ocupar una fila para decir «cero».
	filas := []filaOrigen{filaDePrueba("203.0.113.1", "CN", "CHINANET", 4134, 90, 0)}
	q := url.Values{"capa": {"paquetes"}}
	p := procedenciaDe(filas, q, true, true)
	if !p.SinDatos {
		t.Errorf("con la capa de paquetes y cero toques no debería haber países: %d", len(p.Paises))
	}
}

func TestLaCapaDePaquetesCuentaToquesYNoRechazos(t *testing.T) {
	filas := []filaOrigen{filaDePrueba("203.0.113.1", "CN", "CHINANET", 4134, 900, 63)}
	p := procedenciaDe(filas, url.Values{"capa": {"paquetes"}}, true, true)
	if p.Capa != "paquetes" {
		t.Fatalf("Capa = %q, se esperaba «paquetes»", p.Capa)
	}
	if p.Paises[0].Valor != 63 {
		t.Errorf("Valor = %d, se esperaban los 63 toques", p.Paises[0].Valor)
	}
}

// TestUnaCapaQueYaNoSePuedeServirSeCaeARechazos cubre el caso real: se elige
// «paquetes» y después se cambia el filtro de red, con lo que esa capa deja de
// existir. Pintar un mapa vacío parecería decir «desde ahí no ha tocado
// nadie»; caerse a rechazos dice la verdad disponible.
func TestUnaCapaQueYaNoSePuedeServirSeCaeARechazos(t *testing.T) {
	filas := []filaOrigen{filaDePrueba("203.0.113.1", "CN", "CHINANET", 4134, 900, 63)}
	p := procedenciaDe(filas, url.Values{"capa": {"paquetes"}}, true, false)
	if p.Capa != "rechazos" {
		t.Errorf("Capa = %q, se esperaba caer a «rechazos»", p.Capa)
	}
	if p.Paises[0].Valor != 900 {
		t.Errorf("Valor = %d, se esperaban los 900 rechazos", p.Paises[0].Valor)
	}
	// Y el botón de paquetes no se ofrece, en vez de ofrecerse apagado.
	for _, o := range p.Opciones {
		if o.Valor == "paquetes" {
			t.Error("no debería ofrecerse la capa de paquetes cuando no se puede servir")
		}
	}
}

func TestElEnlaceDeCapaConservaElRestoDelFiltro(t *testing.T) {
	q := url.Values{"red": {"internet"}, "horas": {"168"}, "motivo": {"ruta_inexistente"}}
	enlace := enlaceConCapa(q, "paquetes")
	for _, trozo := range []string{"red=internet", "horas=168", "motivo=ruta_inexistente", "capa=paquetes"} {
		if !strings.Contains(enlace, trozo) {
			t.Errorf("el enlace %q perdió %q — cambiar de capa no puede cambiar el filtro", enlace, trozo)
		}
	}
}

// TestLaCapaPorOmisionNoEnsuciaLaURL: «/seguridad» y «/seguridad?capa=rechazos»
// son la misma vista, y escribir el caso normal en la barra de direcciones
// hace más difícil compartir el enlace útil.
func TestLaCapaPorOmisionNoEnsuciaLaURL(t *testing.T) {
	if enlace := enlaceConCapa(url.Values{}, "rechazos"); enlace != "/seguridad" {
		t.Errorf("enlace = %q, se esperaba «/seguridad» a secas", enlace)
	}
	if enlace := enlaceConCapa(url.Values{"capa": {"paquetes"}}, "rechazos"); strings.Contains(enlace, "capa=") {
		t.Errorf("enlace = %q: volver a la capa por omisión debería quitar el parámetro", enlace)
	}
}

// TestLaGeometriaDelMapaSeLeeDelSVGIncrustado es la prueba que ata las dos
// capas del dibujo: si el archivo que se sirve como fondo y el que da los
// trazos de color dejaran de ser el mismo, esto se pondría en rojo aquí y no
// en la pantalla del responsable, que es donde se vería «torcido» sin más.
func TestLaGeometriaDelMapaSeLeeDelSVGIncrustado(t *testing.T) {
	geo := elementosMapa()
	if len(geo) < 150 {
		t.Fatalf("solo se leyeron %d países de mundo.svg — ¿se regeneró el archivo?", len(geo))
	}
	// Cuatro grandes que tienen que tener forma.
	for _, iso := range []string{"MX", "US", "CN", "GB"} {
		el, ok := geo[iso]
		if !ok {
			t.Errorf("falta %s en mundo.svg", iso)
			continue
		}
		if el.Punto || el.Trazo == "" {
			t.Errorf("%s debería tener trazo propio, no un punto", iso)
		}
	}
	// Y los dos que NO tienen forma a esta resolución: se marcan con un
	// punto en vez de perderse en silencio, que es lo que importa aquí.
	for _, iso := range []string{"SG", "HK"} {
		el, ok := geo[iso]
		if !ok {
			t.Errorf("falta %s en mundo.svg — es origen habitual de barridos desde nube", iso)
			continue
		}
		if !el.Punto {
			t.Errorf("%s debería marcarse como punto", iso)
		}
		if el.CX <= 0 || el.CY <= 0 {
			t.Errorf("%s: punto en (%.1f, %.1f), fuera del viewBox", iso, el.CX, el.CY)
		}
	}
}

func TestLosPaisesDelMapaLlevanSuGeometriaResuelta(t *testing.T) {
	p := procedenciaDe([]filaOrigen{
		filaDePrueba("203.0.113.1", "CN", "CHINANET", 4134, 10, 0),
		filaDePrueba("203.0.113.2", "SG", "TENCENT", 132203, 5, 0),
	}, url.Values{}, true, false)

	for _, pais := range p.Paises {
		if !pais.EnMapa {
			t.Errorf("%s debería poder situarse en el mapa", pais.ISO)
		}
	}
	for _, pais := range p.Paises {
		switch pais.ISO {
		case "CN":
			if pais.Punto || pais.Trazo == "" {
				t.Error("China debería llevar trazo")
			}
		case "SG":
			if !pais.Punto {
				t.Error("Singapur debería llevar punto")
			}
		}
	}
}

// TestUnCodigoQueElMapaNoDibujaNoDesapareceDeLaLista: la base de operadores
// puede devolver un código que Natural Earth no trae. El país tiene que
// seguir contándose y saliendo en la lista, marcado — perder un origen real
// en silencio sería peor que no tener mapa.
func TestUnCodigoQueElMapaNoDibujaNoDesapareceDeLaLista(t *testing.T) {
	p := procedenciaDe([]filaOrigen{
		filaDePrueba("203.0.113.1", "ZZ", "OPERADOR RARO", 65000, 40, 0),
	}, url.Values{}, true, false)
	if len(p.Paises) != 1 {
		t.Fatalf("el país debería seguir en la lista, hay %d", len(p.Paises))
	}
	if p.Paises[0].EnMapa {
		t.Error("EnMapa debería ser falso para un código que el mapa no dibuja")
	}
	if p.Paises[0].Valor != 40 {
		t.Errorf("Valor = %d, se esperaban 40: la cifra no se pierde", p.Paises[0].Valor)
	}
}

// TestElOperadorEsElDelOrigenQueMasAporta documenta la simplificación: un
// país puede traer varios operadores, y la lista enseña el principal con el
// número de direcciones al lado para que se vea que no es el único.
func TestElOperadorEsElDelOrigenQueMasAporta(t *testing.T) {
	p := procedenciaDe([]filaOrigen{
		filaDePrueba("203.0.113.1", "US", "PEQUENO", 111, 5, 0),
		filaDePrueba("203.0.113.2", "US", "DIGITALOCEAN", 14061, 500, 0),
	}, url.Values{}, true, false)
	if !strings.Contains(p.Paises[0].Operador, "DIGITALOCEAN") {
		t.Errorf("Operador = %q, se esperaba el del origen con más rechazos", p.Paises[0].Operador)
	}
	if p.Paises[0].Direcciones != 2 {
		t.Errorf("Direcciones = %d, se esperaban 2", p.Paises[0].Direcciones)
	}
}

func TestLaLecturaNoDejaSeparadoresSueltos(t *testing.T) {
	// Sin operador, sin señal y sin instante: la línea no puede quedar con
	// puntos medios colgando ni escribir «último 01/01/0001».
	p := paisMapa{ISO: "ZZ", Nombre: "Sitio raro", Valor: 3, Direcciones: 1}
	l := lecturaDe(p, "rechazos")
	if strings.Contains(l, "· ·") || strings.HasSuffix(l, "·") {
		t.Errorf("lectura con separadores sueltos: %q", l)
	}
	if strings.Contains(l, "0001") {
		t.Errorf("la lectura escribió un instante que no existe: %q", l)
	}
	if !strings.Contains(l, "1 dirección") {
		t.Errorf("una sola dirección debería ir en singular: %q", l)
	}
}

// ── La sección entera, de la petición al HTML ────────────────────────────

// servidorConGeo es servidorConAuth con una base de país/operador de verdad:
// la misma que prepara geoip.Preparar, escrita en un temporal. Sin esto no se
// puede probar la sección, porque sin base no se pinta — que es justamente lo
// primero que hay que comprobar.
func servidorConGeo(t *testing.T) *Servidor {
	t.Helper()

	// Un rango que cubre 203.0.113.0/24, que es el que usan las demás
	// pruebas de este archivo para simular un origen de Internet.
	const v4 = "203.0.113.0\t203.0.113.255\t4134\tCN\tCHINANET\n"
	const v6 = "::\t::1\t0\tNone\tNot routed\n"

	ruta := filepath.Join(t.TempDir(), "geoip")
	if _, err := geoip.Preparar(strings.NewReader(v4), strings.NewReader(v6), ruta); err != nil {
		t.Fatalf("geoip.Preparar: %v", err)
	}
	base, err := geoip.Abrir(ruta)
	if err != nil {
		t.Fatalf("geoip.Abrir: %v", err)
	}
	t.Cleanup(func() { base.Cerrar() })

	linea, err := autenticacion.Derivar(claveDePrueba, 1000)
	if err != nil {
		t.Fatalf("Derivar: %v", err)
	}
	s, err := Nuevo(Opciones{
		Almacen:          almacenVacio{},
		AlmacenDe:        almacenPorUsuarioDePrueba(almacenVacio{}),
		PromoverUsuario:  promoverUsuarioDePrueba,
		Registro:         slog.New(slog.NewJSONHandler(io.Discard, nil)),
		PlazoInactividad: time.Minute,
		Credencial:       linea,
		Usuarios:         registroDePrueba(t),
		DuracionSesion:   time.Hour,
		Metricas:         metricasDePrueba(t),
		Seguridad:        seguridadDePrueba(t),
		Cuarentena:       cuarentenaDePrueba(t),
		Lista:            listaDePrueba(t),
		Novedades:        novedadesDePrueba(t),
		Hallazgos:        hallazgosDePrueba(t),
		Conexiones:       conexionesDePrueba(t),
		GeoIP:            base,
	})
	if err != nil {
		t.Fatalf("Nuevo: %v", err)
	}
	return s
}

// tocarDesde provoca un rechazo real desde una dirección, que es como llegan
// los orígenes a la tabla en producción.
func tocarDesde(t *testing.T, s *Servidor, ip string) {
	t.Helper()
	r := httptest.NewRequest("GET", "/wp-login.php", nil)
	r.RemoteAddr = ip + ":44001"
	s.Rutas().ServeHTTP(httptest.NewRecorder(), r)
}

func TestElPanelPintaElMapaDeProcedencia(t *testing.T) {
	s := servidorConGeo(t)
	tocarDesde(t, s, "203.0.113.7")

	cuerpo := panelSeguridad(t, s, "")

	for _, quiero := range []string{
		`id="procedencia"`,          // la sección existe
		`src="/estatico/mundo.svg"`, // el fondo, cacheable
		`class="mcapa"`,             // la capa de color en línea
		"China",                     // el nombre en español, no solo «CN»
		"AS4134 CHINANET",           // el operador principal
	} {
		if !strings.Contains(cuerpo, quiero) {
			t.Errorf("el panel no trae %q", quiero)
		}
	}

	// El color tiene que ir en una clase y NUNCA en un «fill» en línea: la
	// CSP lo descartaría en silencio y el mapa saldría todo del color de la
	// tierra sin que nada fallara a gritos (ADR-0060).
	if strings.Contains(cuerpo, `fill="#`) {
		t.Error("hay un fill en línea en la página: la CSP lo descartaría sin avisar")
	}
	if !strings.Contains(cuerpo, `class="pa b`) {
		t.Error("el país pintado no lleva su clase de cubo")
	}
}

// TestSinBaseElPanelSigueEnteroSinElMapa es el criterio 1 de RF-30 visto
// desde la página: la ausencia de la base no impide que el resto funcione.
func TestSinBaseElPanelSigueEnteroSinElMapa(t *testing.T) {
	s := servidorConAuth(t) // sin GeoIP
	tocarDesde(t, s, "203.0.113.7")

	cuerpo := panelSeguridad(t, s, "")

	if strings.Contains(cuerpo, `id="procedencia"`) {
		t.Error("sin base de operadores no debería pintarse la sección de procedencia")
	}
	// Y lo que importa: el resto de la página sigue ahí.
	for _, quiero := range []string{"Volumen observado", "Orígenes", "203.0.113.7"} {
		if !strings.Contains(cuerpo, quiero) {
			t.Errorf("sin base se perdió %q, que no depende de ella", quiero)
		}
	}
	// El cuadro de países se queda, diciendo que no se está mirando en vez
	// de un cero que se leería como «no ha venido nadie de fuera».
	if !strings.Contains(cuerpo, `data-cifra="paises"`) {
		t.Error("el cuadro de países debería seguir, con «—»")
	}
}

// TestElQuintoCuadroEsPaisesYNoContrasenas fija la decisión del responsable
// del 2026-09-13. La cifra de contraseñas no se pierde de la página: la señal
// sigue saliendo en la fila del origen y en «Contención activa».
func TestElQuintoCuadroEsPaisesYNoContrasenas(t *testing.T) {
	s := servidorConGeo(t)
	tocarDesde(t, s, "203.0.113.7")
	cuerpo := panelSeguridad(t, s, "")

	if !strings.Contains(cuerpo, `data-cifra="paises"`) {
		t.Error("falta el cuadro de países")
	}
	if strings.Contains(cuerpo, `data-cifra="contrasenas"`) {
		t.Error("el cuadro de contraseñas debería haber salido de la banda")
	}
}

// TestElSelectorDeCapaNoSeOfreceSinSensor: sin nas-sensor no hay capa de
// paquetes que servir, y un control apagado invitaría a preguntarse qué hay
// que hacer para encenderlo.
func TestElSelectorDeCapaNoSeOfreceSinSensor(t *testing.T) {
	s := servidorConGeo(t)
	tocarDesde(t, s, "203.0.113.7")
	cuerpo := panelSeguridad(t, s, "")

	if strings.Contains(cuerpo, "capa=paquetes") {
		t.Error("sin sensor no debería ofrecerse la capa de paquetes")
	}
}

// ── El filtro sobrevive a la navegación ──────────────────────────────────
//
// Todo lo de aquí abajo nace de un defecto que encontró el responsable
// usando el panel: con «todo lo guardado» puesto, pulsar una dirección de la
// tabla devolvía la página a «Internet · 24 horas» Y ADEMÁS no abría el
// detalle. Las dos mitades eran el mismo fallo: el enlace decía
// «?origen=…» a secas, así que la ventana volvía a 24 h, el origen ya no
// estaba en la lista recalculada, y no había detalle que enseñar.

func TestElEnlaceDeUnOrigenConservaElFiltro(t *testing.T) {
	q := url.Values{"red": {"internet"}, "horas": {"0"}, "motivo": {"ruta_inexistente"}}
	enlace := enlaceDeSeguridad(q, map[string]string{"origen": "203.0.113.7"})

	for _, trozo := range []string{"red=internet", "horas=0", "motivo=ruta_inexistente", "origen=203.0.113.7"} {
		if !strings.Contains(enlace, trozo) {
			t.Errorf("el enlace %q perdió %q", enlace, trozo)
		}
	}
}

func TestCerrarElDetalleConservaElFiltroYSoloQuitaElOrigen(t *testing.T) {
	q := url.Values{"red": {"internet"}, "horas": {"0"}, "origen": {"203.0.113.7"}}
	enlace := enlaceDeSeguridad(q, map[string]string{"origen": ""})

	if strings.Contains(enlace, "origen=") {
		t.Errorf("cerrar debería quitar el origen: %q", enlace)
	}
	for _, trozo := range []string{"red=internet", "horas=0"} {
		if !strings.Contains(enlace, trozo) {
			t.Errorf("cerrar no puede tirar el filtro, perdió %q: %q", trozo, enlace)
		}
	}
}

// TestVolverNoEsUnaRedireccionAbierta es la contrapartida de que el destino
// viaje en un campo del formulario, es decir: que lo mande el cliente. El
// destino no se repite, se RECONSTRUYE por lista blanca, así que nada de lo
// que entre puede sacar al navegador de /seguridad.
func TestVolverNoEsUnaRedireccionAbierta(t *testing.T) {
	casos := []string{
		"https://evil.example/robado",
		"//evil.example/robado",
		"/etc/passwd",
		"red=internet&siguiente=https://evil.example",
		"%zz%zz",
		"",
	}
	for _, crudo := range casos {
		destino := volverA(crudo)
		if !strings.HasPrefix(destino, "/seguridad") {
			t.Errorf("volverA(%q) = %q — tiene que quedarse en /seguridad", crudo, destino)
		}
		if strings.Contains(destino, "evil.example") || strings.Contains(destino, "passwd") {
			t.Errorf("volverA(%q) = %q — se coló contenido del cliente", crudo, destino)
		}
	}
}

func TestVolverConservaLoQueSiEsFiltro(t *testing.T) {
	destino := volverA("red=internet&horas=0&motivo=ruta_inexistente&origen=203.0.113.7&basura=x")
	for _, trozo := range []string{"red=internet", "horas=0", "motivo=ruta_inexistente", "origen=203.0.113.7"} {
		if !strings.Contains(destino, trozo) {
			t.Errorf("volverA perdió %q: %q", trozo, destino)
		}
	}
	if strings.Contains(destino, "basura") {
		t.Errorf("volverA dejó pasar una clave que no es del filtro: %q", destino)
	}
}

// TestPulsarUnOrigenConTodoLoGuardadoAbreSuDetalle es el defecto EXACTO que
// se reportó, de punta a punta: un origen que solo tiene actividad fuera de
// la ventana de 24 horas tiene que poder abrirse cuando el filtro dice «todo
// lo guardado», y la página resultante tiene que seguir diciendo lo mismo.
func TestPulsarUnOrigenConTodoLoGuardadoAbreSuDetalle(t *testing.T) {
	s := servidorConGeo(t)
	tocarDesde(t, s, "203.0.113.7")

	// «horas=0» es «todo lo guardado». Con la tabla así filtrada, el enlace
	// de la fila tiene que llevar ese mismo filtro consigo.
	cuerpo := panelSeguridad(t, s, "?red=internet&horas=0")
	if !strings.Contains(cuerpo, "horas=0") {
		t.Fatal("el enlace de la fila no lleva la ventana consigo")
	}

	// Y al seguirlo, el detalle se abre Y el filtro sigue puesto.
	detalle := panelSeguridad(t, s, "?red=internet&horas=0&origen=203.0.113.7")
	if !strings.Contains(detalle, `class="detalle"`) {
		t.Error("el panel de detalle no se abrió")
	}
	if !strings.Contains(detalle, "Todo lo guardado") {
		t.Error("la ventana volvió a su valor por omisión al abrir el detalle")
	}
}

// TestLasAccionesQueEscribenDevuelvenALaMismaVista: soltar, bloquear y
// retirar mandan el destino en un campo oculto, y la página tiene que
// ponerlo. Sin él, actuar sobre una fila deshace el filtro con el que se
// había llegado hasta ella.
func TestLasAccionesQueEscribenDevuelvenALaMismaVista(t *testing.T) {
	s := servidorConGeo(t)
	tocarDesde(t, s, "203.0.113.7")

	cuerpo := panelSeguridad(t, s, "?red=internet&horas=0&origen=203.0.113.7")
	if !strings.Contains(cuerpo, `name="volver"`) {
		t.Error("los formularios de acción no llevan el campo «volver»")
	}
}
