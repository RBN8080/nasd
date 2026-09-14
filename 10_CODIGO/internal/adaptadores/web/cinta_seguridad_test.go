package web

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/netip"
	"regexp"
	"strings"
	"testing"
	"time"

	"nasd/internal/seguridad"
)

// EL DEFECTO QUE ESTA PRUEBA FIJA, Y ES EL MOTIVO DE QUE EXISTA EL ARCHIVO.
//
// «Apartados por conducta» imprimía «hasta 0000-12-31 17:23» en toda fila de
// operador: un `Apartado.Hasta` en cero significa «no caduca» —solo lo llevan
// los de operador, ADR-0083— y la plantilla lo pasaba por `fecha` sin
// preguntar. El cero de time.Time por .Local() da el año 1 desplazado por el
// huso, que no es un dato: es un artefacto con forma de fecha.
//
// Se prueba contra la PLANTILLA RENDERIZADA y no contra una función, porque el
// defecto vivía exactamente ahí. Una prueba sobre un ayudante en Go habría
// pasado con la plantilla rota.
func TestUnApartadoSinCaducidadNoImprimeUnaFechaImposible(t *testing.T) {
	s := servidorConAuth(t)

	casos := []struct {
		nombre    string
		hasta     time.Time
		contiene  string
		noContine string
	}{
		{
			nombre:    "de operador: Hasta en cero es «sin caducidad»",
			hasta:     time.Time{},
			contiene:  "sin caducidad",
			noContine: "0000-",
		},
		{
			nombre:   "de dirección: conserva su fecha",
			hasta:    time.Date(2026, 9, 20, 16, 55, 0, 0, time.Local),
			contiene: "hasta 2026-09-20 16:55",
		},
	}

	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			v := vistaSeguridad{
				Apartados: []seguridad.Apartado{{
					IP:    netip.MustParseAddr("2a02:c204:2165:6937::1"),
					Senal: seguridad.SenalOperadorDesconocido,
					Desde: time.Date(2026, 9, 14, 6, 12, 0, 0, time.Local),
					Hasta: c.hasta,
				}},
			}
			v.CaducidadApartados = fraseDeCaducidad(v.Apartados)

			var buf bytes.Buffer
			if err := s.plantillas.ExecuteTemplate(&buf, "seguridad.html", v); err != nil {
				t.Fatalf("render de la plantilla: %v", err)
			}
			cuerpo := buf.String()
			if !strings.Contains(cuerpo, c.contiene) {
				t.Errorf("no se encontró %q en la columna «Frenados»", c.contiene)
			}
			if c.noContine != "" && strings.Contains(cuerpo, c.noContine) {
				t.Errorf("se imprimió una fecha imposible: la página contiene %q", c.noContine)
			}
		})
	}
}

// EL RÓTULO DE LA TABLA TAMPOCO PUEDE AFIRMAR DE MÁS. Decía «caducan solos»
// para todos, y desde ADR-0083 conviven apartados que caducan con apartados
// que no. Enumerar lo que de verdad hay es lo único que no puede mentir.
func TestElRotuloDeApartadosDiceLaCaducidadQueDeVerdadHay(t *testing.T) {
	eterno := seguridad.Apartado{Hasta: time.Time{}}
	conFecha := seguridad.Apartado{Hasta: time.Now().Add(24 * time.Hour)}

	casos := []struct {
		nombre    string
		apartados []seguridad.Apartado
		quiere    string
	}{
		{"sin filas, no se afirma nada", nil, "los puso el nodo"},
		{"todos de operador", []seguridad.Apartado{eterno, eterno},
			"los puso el nodo · son de operador y no caducan"},
		{"todos de dirección", []seguridad.Apartado{conFecha},
			"los puso el nodo · caducan solos"},
		{"mezclados: no se puede elegir uno de los dos", []seguridad.Apartado{eterno, conFecha},
			"los puso el nodo · los de dirección caducan solos; los de operador, no"},
	}
	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			if got := fraseDeCaducidad(c.apartados); got != c.quiere {
				t.Errorf("fraseDeCaducidad = %q, se esperaba %q", got, c.quiere)
			}
		})
	}
}

// LA CINTA ENUMERA SIEMPRE LOS TRES GRUPOS, con su cuenta o con un «sin».
// Se lee SIN las tablas delante —ese es su motivo de existir—, y una
// enumeración a la que le faltan grupos no se distingue de una a la que le
// falta un dato: «4 apartados» a secas no dice si hay cero bloqueos o si no se
// están mirando.
func TestLaCintaEnumeraLosTresGruposSiempre(t *testing.T) {
	casos := []struct {
		a, b, h int
		quiere  string
	}{
		{0, 0, 0, "sin apartados · sin bloqueos · sin respuestas inesperadas"},
		{1, 1, 1, "1 apartado · 1 bloqueo · 1 respuesta inesperada"},
		{4, 5, 0, "4 apartados · 5 bloqueos · sin respuestas inesperadas"},
	}
	for _, c := range casos {
		if got := fraseDeContencion(c.a, c.b, c.h); got != c.quiere {
			t.Errorf("fraseDeContencion(%d,%d,%d) = %q, se esperaba %q",
				c.a, c.b, c.h, got, c.quiere)
		}
	}
}

// UN HECHO QUE NO EXISTE NO SE INVENTA UNA FECHA. Es el mismo criterio que la
// columna «Frenados» de arriba: un instante cero se pintaría como el año 1, y
// decir una fecha falsa es peor que no decir ninguna. El segmento se queda
// vacío y lo esconde el CSS, conservando su nodo para que el flujo pueda
// escribirlo cuando ese hecho aparezca.
func TestUnSegmentoSinHechoSeQuedaVacioYNoInventaFecha(t *testing.T) {
	ahora := time.Date(2026, 9, 14, 19, 42, 6, 0, time.Local)
	c := componerCinta(hechosDeCinta{
		Apartados: 4, Bloqueos: 5,
		Rechazo: ultimoHecho{Hay: true, Cuando: ahora.Add(-6 * time.Minute), Que: "2a01:7c8::1"},
		Frenado: ultimoHecho{}, // nunca se ha cerrado una puerta
		Red:     "Internet",
		Ahora:   ahora,
	})

	porClave := map[string]segmentoCinta{}
	for _, s := range c.Segmentos {
		porClave[s.Clave] = s
	}
	if v := porClave[cintaFrenado].Valor; v != "" {
		t.Errorf("el segmento de la puerta cerrada debía ir vacío y trae %q", v)
	}
	if v := porClave[cintaRechazo].Valor; !strings.Contains(v, "2026-09-14 19:36") {
		t.Errorf("el último rechazo no lleva su fecha: %q", v)
	}
	if c.Hora != "19:42:06" {
		t.Errorf("la hora del piloto = %q, se esperaba 19:42:06 — el reloj es la prueba de vida", c.Hora)
	}
	if !strings.Contains(c.Foto, "19:42") {
		t.Errorf("el estado «foto» no dice de cuándo es: %q", c.Foto)
	}
}

// SIN NADA VIGENTE NO HAY ÁMBAR. Un ámbar permanente enseña a ignorar el panel
// (ADR-0065), y en esta consola el ámbar es el color de lo que hay que mirar.
func TestLaContencionSoloSePintaEnAmbarCuandoHayAlgoQueContener(t *testing.T) {
	conAlgo := componerCinta(hechosDeCinta{Apartados: 1, Ahora: time.Now()})
	sinNada := componerCinta(hechosDeCinta{Ahora: time.Now()})
	if conAlgo.Segmentos[0].Clase != "sg-av" {
		t.Error("con un apartado vigente, la contención tenía que ir en ámbar")
	}
	if sinNada.Segmentos[0].Clase != "" {
		t.Errorf("sin nada vigente la contención no debe llevar color, y lleva %q",
			sinNada.Segmentos[0].Clase)
	}
}

// LA PÁGINA Y EL FLUJO NO PUEDEN DISCREPAR SOBRE LA CINTA — la misma regla que
// TestCadaFilaVivaDeLaPaginaTieneAnclaEnElFlujo fija para /estado.
//
// LO QUE ESTA PRUEBA VIGILA DE VERDAD, dicho con precisión porque se midió:
// renombrar la CLAVE de un segmento no la rompe, y está bien que no la rompa —
// la clave se declara una vez en Go y la plantilla la imprime de esa misma
// estructura, así que ahí no hay dos criterios que puedan separarse. Lo que sí
// caza es que la plantilla deje de escribir el atributo, que el flujo deje de
// mandar segmentos, o que se quede sin velocidad o sin hora: cualquiera de las
// tres deja la cinta quieta o envejeciendo en silencio. El contrato que puede
// pudrirse con el tiempo es el otro, y lo fija
// TestLaPlantillaYElGuionNombranLosMismosGanchos.
func TestCadaSegmentoDeLaCintaTieneAnclaEnElFlujo(t *testing.T) {
	s := servidorConAuth(t)
	cookie := superusuarioEn(t, s)

	pagina := pedirDesde(t, s.Rutas(), http.MethodGet, "/seguridad", "192.168.1.18:5000", cookie).Body.String()

	marco := s.marcoDeSeguridad(seguridad.Filtro{})
	if len(marco.Cinta) == 0 {
		t.Fatal("el flujo no manda ni un segmento de cinta")
	}
	for _, sg := range marco.Cinta {
		ancla := `data-seg="` + sg.Clave + `"`
		if !strings.Contains(pagina, ancla) {
			t.Errorf("el flujo manda la clave %q y la página no tiene %s", sg.Clave, ancla)
		}
	}
	if marco.Velocidad == "" {
		t.Error("el flujo no manda la clase de velocidad: la cinta se aceleraría al cambiar de largo")
	}
	if marco.Hora == "" {
		t.Error("el flujo no manda la hora: el piloto se quedaría sin prueba de vida")
	}
	// El marco tiene que seguir siendo JSON válido con los campos nuevos.
	if _, err := json.Marshal(marco); err != nil {
		t.Fatalf("el marco de seguridad ya no serializa: %v", err)
	}
}

// LA PASTILLA «#latido» SE RETIRA DE ESTA PÁGINA, Y SU FUNCIÓN NO SE PIERDE.
//
// Las dos mitades van en la misma prueba a propósito: quitar el indicador sin
// sustituto es el modo de fallo que ADR-0083 persigue —un panel congelado
// pareciendo vivo—, así que comprobar solo la ausencia dejaría pasar
// exactamente el error que hay que evitar.
func TestElPanelPierdeLaPastillaDeLatidoPeroNoElIndicadorDeConexion(t *testing.T) {
	s := servidorConAuth(t)
	cookie := superusuarioEn(t, s)
	cuerpo := pedirDesde(t, s.Rutas(), http.MethodGet, "/seguridad", "192.168.1.18:5000", cookie).Body.String()

	if strings.Contains(cuerpo, `id="latido"`) {
		t.Error("la pastilla de latido sigue en /seguridad; se retiró por encargo del responsable")
	}
	if !strings.Contains(cuerpo, `id="piloto"`) {
		t.Fatal("no hay piloto en la cinta: el panel se quedó SIN indicador de conexión")
	}
	// Nace en «foto» y lo dice: sin JavaScript esta página no se refresca sola,
	// y callarlo la dejaría pareciendo viva. Es lo que la pastilla NO hacía —
	// nacía oculta y no decía nada en absoluto.
	if !strings.Contains(cuerpo, "pil-foto") {
		t.Error("el piloto no nace en el estado «foto»")
	}
	if !strings.Contains(cuerpo, "no se refresca sola") {
		t.Error("sin JavaScript la página no declara que es una foto fija")
	}
}

// EL AVISO DE CONTENCIÓN CAMBIADA SE MUDA A LA CINTA, y con él su ancla. Al
// pie de su sección quedaba fuera de la pantalla justo cuando servía: quien
// está leyendo la cronología dejó ese párrafo ocho pantallas más arriba.
func TestElAvisoDeContencionViveEnLaCintaYNoAlPieDeSuSeccion(t *testing.T) {
	s := servidorConAuth(t)
	cookie := superusuarioEn(t, s)
	cuerpo := pedirDesde(t, s.Rutas(), http.MethodGet, "/seguridad", "192.168.1.18:5000", cookie).Body.String()

	i := strings.Index(cuerpo, `id="contencion-nueva"`)
	if i < 0 {
		t.Fatal("desapareció el aviso de contención cambiada")
	}
	j := strings.Index(cuerpo, `class="marq"`)
	k := strings.Index(cuerpo, `<main class="cuerpo">`)
	if j < 0 || k < 0 {
		t.Fatal("no se encontró la cinta o el cuerpo de la página")
	}
	if !(j < i && i < k) {
		t.Error("el aviso no está dentro de la cinta: al desplazarse dejaría de verse")
	}
	// El ancla que el flujo compara viaja con él.
	if !strings.Contains(cuerpo, "data-contencion=") {
		t.Error("falta data-contencion: el flujo no podría descubrir el aviso")
	}
}

// EL CONTRATO QUE DE VERDAD SE PUEDE PUDRIR ES ESTE, Y NO EL DE ARRIBA.
//
// La clave de cada segmento se declara UNA vez en Go y la plantilla la imprime
// desde la misma estructura, así que esas dos no pueden desincronizarse —se
// comprobó rompiendo la constante a propósito y la prueba del ancla siguió en
// verde, que es el resultado honesto: allí no hay dos criterios, hay uno—.
//
// Donde sí hay dos archivos que nadie compila juntos es entre la PLANTILLA y
// seguridad.js: los selectores con los que el guion busca los nodos son
// literales suyos. Si la plantilla renombra «.vl» o el piloto pierde
// «.pil-hr», el guion deja de encontrarlos, el flujo sigue llegando y la cinta
// se queda con el texto de la carga envejeciendo EN SILENCIO — el modo de
// fallo exacto que este panel lleva meses corrigiendo. Go no puede ejecutar
// ese guion, pero sí puede exigir que las dos mitades nombren lo mismo.
func TestLaPlantillaYElGuionNombranLosMismosGanchos(t *testing.T) {
	guion, err := fs.ReadFile(recursos, "estatico/seguridad.js")
	if err != nil {
		t.Fatalf("no se pudo leer el guion incrustado: %v", err)
	}
	js := string(guion)

	s := servidorConAuth(t)
	cookie := superusuarioEn(t, s)
	pagina := pedirDesde(t, s.Rutas(), http.MethodGet, "/seguridad", "192.168.1.18:5000", cookie).Body.String()

	// Cada pareja es «lo que el guion busca» y «lo que la plantilla escribe».
	ganchos := []struct {
		enElGuion     string
		enLaPlantilla string
		porQue        string
	}{
		{`'data-seg'`, `data-seg="`, "el ancla de cada segmento de la cinta"},
		{`'.vl'`, `class="vl"`, "el hueco donde se escribe el valor de un segmento"},
		{`'piloto'`, `id="piloto"`, "el indicador de conexión que sustituye a #latido"},
		{`'.pil-txt'`, `class="pil-txt"`, "la palabra de estado del piloto"},
		{`'.pil-hr'`, `class="pil-hr`, "el reloj del piloto, que es su prueba de vida"},
		{`'.marq-pista'`, `class="marq-pista`, "la pista que se anima"},
		{`'.marq-caja'`, `class="marq-caja"`, "la ventana que recorta la pista"},
		{`'.tira'`, `class="tira"`, "la copia que se mide para decidir si hace falta moverse"},
		{`'contencion-nueva'`, `id="contencion-nueva"`, "el aviso de contención cambiada"},
		{`'data-cifra'`, `data-cifra="`, "el ancla de los cuadros de «Volumen observado»"},
	}
	for _, g := range ganchos {
		if !strings.Contains(js, g.enElGuion) {
			t.Errorf("seguridad.js ya no busca %s (%s)", g.enElGuion, g.porQue)
		}
		if !strings.Contains(pagina, g.enLaPlantilla) {
			t.Errorf("la página ya no escribe %s (%s)", g.enLaPlantilla, g.porQue)
		}
	}

	// Las seis clases de velocidad que Go puede emitir tienen que existir en la
	// hoja de estilos, o la cinta se quedaría sin «animation-duration» y
	// quieta — sin que nada fallara a gritos, porque una clase que no existe no
	// es un error en CSS.
	hoja, err := fs.ReadFile(recursos, "estatico/estilo.css")
	if err != nil {
		t.Fatalf("no se pudo leer la hoja incrustada: %v", err)
	}
	css := string(hoja)
	for _, v := range []string{"v1", "v2", "v3", "v4", "v5", "v6"} {
		if !strings.Contains(css, "."+v+"{animation-duration:") {
			t.Errorf("estilo.css no define la duración de la clase %q que Go puede emitir", v)
		}
		// Y el guion tiene que saber retirarla al poner otra.
		if !strings.Contains(js, "'"+v+"'") {
			t.Errorf("seguridad.js no conoce la clase de velocidad %q: no sabría retirarla", v)
		}
	}

	// EL DEFECTO QUE SOLO VIO EL NAVEGADOR, fijado aquí para que no vuelva.
	//
	// El aviso se pinta con el atributo «hidden», que el navegador aplica con
	// un «display:none» de SU hoja — y cualquier regla de autor que fije
	// «display» lo pisa. «.marq-nvo» fija «display:flex», así que sin una
	// regla «[hidden]» explícita el aviso nacía VISIBLE en toda carga,
	// diciéndole «recargue» a quien acababa de recargar. Ninguna prueba lo
	// cazó: se vio en la captura del navegador.
	if strings.Contains(css, ".marq-nvo{") && !strings.Contains(css, ".marq-nvo[hidden]{display:none") {
		t.Error(".marq-nvo fija display y no desactiva [hidden]: el aviso nacería visible en toda carga")
	}

	// LA CINTA SE MUEVE SIEMPRE, y las dos mitades tienen que conocer las
	// MISMAS copias.
	//
	// Aquí hubo una regla que PARABA la cinta cuando el texto ya cabía en la
	// ventana, y en el escritorio eso era casi siempre: lo reportó el
	// responsable viéndolo quieto. Ahora el guion repite el texto las copias
	// que hagan falta y pide al CSS que desplace 100/n —UNA copia exacta—, para
	// que la distancia recorrida no dependa del ancho de la pantalla.
	//
	// SI UNA CLASE FALTA EN LA HOJA la pista se queda con el «-50%» por
	// omisión, y a n copias eso recorre n/2 veces la distancia en el mismo
	// tiempo: la cinta se dispara de velocidad sin que nada falle a gritos.
	//
	// Se compara la LISTA del guion contra las clases de la hoja, y no se busca
	// cada nombre suelto en el texto: «n2» aparece por casualidad dentro de
	// otras palabras, así que esa comprobación pasaba sin comprobar nada —se
	// vio al escribirla, porque solo fallaba para «n10» y «n12»—.
	m := regexp.MustCompile(`COPIAS = \[([0-9, ]+)\]`).FindStringSubmatch(js)
	if m == nil {
		t.Fatal("seguridad.js ya no declara la lista COPIAS: nada decide cuántas veces se repite el texto")
	}
	for _, n := range strings.Split(m[1], ",") {
		n = strings.TrimSpace(n)
		clase := ".n" + n + "{animation-name:"
		if !strings.Contains(css, clase) {
			t.Errorf("el guion puede pedir %s copias y estilo.css no define %q", n, clase)
		}
	}
	if strings.Contains(css, "marq-quieta") || strings.Contains(js, "marq-quieta") {
		t.Error("vuelve a haber una regla que para la cinta; se pidió una marquesina, no un cartel")
	}
	// La alternativa sin movimiento no es opcional: una marquesina que ignora
	// «prefers-reduced-motion» es un patrón hostil.
	if !strings.Contains(css, "prefers-reduced-motion") {
		t.Error("la hoja no atiende prefers-reduced-motion")
	}
	if !strings.Contains(css, "animation-play-state:paused") {
		t.Error("la cinta no se detiene al posar el cursor ni al enfocar con el tabulador")
	}
}

// LA VELOCIDAD SE ELIGE POR LONGITUD, Y TIENE QUE SER MONÓTONA: más texto
// nunca puede dar una vuelta más corta. Sin esto, una puerta cerrada nueva
// podría acelerar la cinta en vez de alargar su ciclo.
func TestLaVelocidadDeLaCintaCreceConElTexto(t *testing.T) {
	corta := velocidadDeCinta([]segmentoCinta{{Etiqueta: "Contención", Valor: "sin nada"}})
	larga := velocidadDeCinta([]segmentoCinta{
		{Etiqueta: "Contención vigente", Valor: strings.Repeat("x", 120)},
		{Etiqueta: "Último rechazo · Internet", Valor: strings.Repeat("y", 120)},
		{Etiqueta: "Última puerta cerrada", Valor: strings.Repeat("z", 120)},
	})
	if corta >= larga {
		t.Errorf("una cinta larga (%s) no dura más que una corta (%s)", larga, corta)
	}
	// Un segmento vacío no cuenta: no se pinta, así que no ocupa pista.
	conVacio := velocidadDeCinta([]segmentoCinta{
		{Etiqueta: "Contención", Valor: "sin nada"},
		{Etiqueta: "Última puerta cerrada", Valor: ""},
	})
	if conVacio != corta {
		t.Errorf("un segmento vacío cambió la velocidad: %s frente a %s", conVacio, corta)
	}
}

// LA CINTA NO PUEDE LLEVAR UN ESTILO EN LÍNEA. Con «style-src 'self'» sin
// 'unsafe-inline' (ADR-0060) un `style="animation-duration:…"` se descarta EN
// SILENCIO y la cinta se queda quieta sin que nada falle a gritos. La
// comprobación general de esto es TestNingunaPaginaDelCromoLlevaEstiloEnLinea;
// esta fija además que la duración viaja como CLASE, que es el mecanismo que
// la sustituye.
func TestLaDuracionDeLaCintaViajaComoClaseYNoComoEstilo(t *testing.T) {
	s := servidorConAuth(t)
	cookie := superusuarioEn(t, s)
	cuerpo := pedirDesde(t, s.Rutas(), http.MethodGet, "/seguridad", "192.168.1.18:5000", cookie).Body.String()

	if strings.Contains(cuerpo, "animation-duration") {
		t.Error("la duración de la cinta se escribió en el HTML; la CSP la descartaría en silencio")
	}
	if !strings.Contains(cuerpo, `class="marq-pista v`) {
		t.Error("la pista no lleva su clase de velocidad «v1…v6»")
	}
}
