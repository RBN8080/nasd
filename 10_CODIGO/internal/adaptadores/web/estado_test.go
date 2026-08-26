package web

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"nasd/internal/adaptadores/sistema"
	"nasd/internal/autenticacion"
)

// nodoSano es un nodo con todo medido y en orden, para partir de ahí y
// estropear una sola cosa en cada prueba.
func nodoSano() sistema.Nodo {
	return sistema.Nodo{
		Vivo: sistema.Vivo{
			Momento:            time.Now(),
			Disponible:         true,
			RAMOK:              true,
			RAMTotalBytes:      948 << 20,
			RAMDisponibleBytes: 640 << 20,
			TemperaturaOK:      true,
			TemperaturaC:       55.8,
		},
		Throttled: sistema.Throttled{Disponible: true, TermicoBlandoOcurrida: true},
		Datos: sistema.Volumen{
			Disponible: true, Punto: "/srv/nas", Dispositivo: "/dev/sdb1",
			TotalBytes: 916 << 30, LibresBytes: 900 << 30,
			SoloLecturaOK: true, ErroresOK: true,
		},
		Arranque: sistema.Volumen{
			Disponible: true, Punto: "/", Dispositivo: "/dev/sda2",
			TotalBytes: 28 << 30, LibresBytes: 20 << 30,
			SoloLecturaOK: true, ErroresOK: true,
		},
	}
}

func buscarIndicador(t *testing.T, is []indicador, contiene string) indicador {
	t.Helper()
	for _, i := range is {
		if strings.Contains(i.Nombre, contiene) {
			return i
		}
	}
	t.Fatalf("no hay indicador que contenga %q en %v", contiene, is)
	return indicador{}
}

// El límite térmico blando YA ALCANZADO (0x80000) es un riesgo aceptado por
// escrito en 00_RECTOR.md §4.5. Si lo pintara en ámbar, el panel estaría en
// ámbar de forma permanente y dejaría de mirarse — que es exactamente lo que
// D-16 razonó sobre la ceguera al banner.
func TestLimiteTermicoYaAceptadoNoDejaElPanelEnAmbarParaSiempre(t *testing.T) {
	is := evaluar(nodoSano(), Instantanea{})
	if got := buscarIndicador(t, is, "Limitación").Veredicto; got != vOK {
		t.Fatalf("0x80000 (ocurrido, no activo) debería ser ok y dio %q", got)
	}
	if got := peorDe(is); got == vAtencion || got == vFallo {
		t.Fatalf("un nodo en su estado normal conocido no puede dar %q", got)
	}
}

// Lo contrario: si está ocurriendo AHORA, sí es noticia.
func TestLimiteTermicoActivoSiAvisa(t *testing.T) {
	n := nodoSano()
	n.Throttled.TermicoBlandoAhora = true
	if got := buscarIndicador(t, evaluar(n, Instantanea{}), "Limitación").Veredicto; got != vAtencion {
		t.Fatalf("el límite activo debería avisar, dio %q", got)
	}
}

// La subtensión NO está aceptada: el charter §3.6 la midió a cero, así que
// cualquier bit encendido ahí es información nueva.
func TestSubtensionEsFallo(t *testing.T) {
	n := nodoSano()
	n.Throttled.SubtensionAhora = true
	ind := buscarIndicador(t, evaluar(n, Instantanea{}), "Limitación")
	if ind.Veredicto != vFallo {
		t.Fatalf("la subtensión debería ser fallo, dio %q", ind.Veredicto)
	}
	if ind.Accion == "" {
		t.Fatal("un fallo sin acción no es accionable; el charter §8 lo exige")
	}
}

// El respaldo parcial NO puede pintarse en verde: no sabe nada de lo térmico,
// y un «ok» ahí afirmaría que RNF-11 se está midiendo cuando no lo está.
func TestElThrottledParcialNoSeDaPorBueno(t *testing.T) {
	n := nodoSano()
	n.Throttled = sistema.Throttled{Disponible: true, Parcial: true}
	ind := buscarIndicador(t, evaluar(n, Instantanea{}), "Limitación")
	if ind.Veredicto != vDesconocido {
		t.Fatalf("un throttled parcial dio %q; debería ser desconocido", ind.Veredicto)
	}
	if !strings.Contains(ind.Accion, "SupplementaryGroups=video") {
		t.Fatalf("la acción debe decir qué falta en la unidad: %q", ind.Accion)
	}
}

// Pero la subtensión sí se conoce por esa vía, y sigue siendo un fallo.
func TestLaSubtensionSeDetectaTambienConLaFuenteParcial(t *testing.T) {
	n := nodoSano()
	n.Throttled = sistema.Throttled{
		Disponible: true, Parcial: true,
		SubtensionAhora: true, SubtensionOcurrida: true,
	}
	if got := buscarIndicador(t, evaluar(n, Instantanea{}), "Limitación").Veredicto; got != vFallo {
		t.Fatalf("veredicto = %q; la subtensión es fallo venga de donde venga", got)
	}
}

func TestThrottledNoLeidoEsDesconocidoYNoOk(t *testing.T) {
	n := nodoSano()
	n.Throttled = sistema.Throttled{} // no disponible
	if got := buscarIndicador(t, evaluar(n, Instantanea{}), "Limitación").Veredicto; got != vDesconocido {
		t.Fatalf("sin lectura debe ser desconocido, no %q: un ok aquí sería afirmar "+
			"que el nodo nunca se limitó, que es falso", got)
	}
}

// Un medio de arranque remontado de solo lectura pesa más que la ocupación:
// puede estar medio vacío y estar muriéndose igual.
func TestSoloLecturaGanaALaOcupacion(t *testing.T) {
	n := nodoSano()
	n.Arranque.SoloLectura = true
	ind := buscarIndicador(t, evaluar(n, Instantanea{}), "Medio de arranque")
	if ind.Veredicto != vFallo {
		t.Fatalf("veredicto = %q", ind.Veredicto)
	}
	if !strings.Contains(ind.Accion, "20_APROVISIONAMIENTO") {
		t.Fatalf("la acción debe decir cómo reconstruir el nodo: %q", ind.Accion)
	}
}

func TestErroresDeExt4SonFallo(t *testing.T) {
	n := nodoSano()
	n.Datos.Errores = 2
	if got := buscarIndicador(t, evaluar(n, Instantanea{}), "Disco de datos").Veredicto; got != vFallo {
		t.Fatalf("veredicto = %q", got)
	}
}

func TestDiscoLlenoAvisaYLuegoFalla(t *testing.T) {
	n := nodoSano()
	n.Datos.LibresBytes = n.Datos.TotalBytes / 10 // 90 % usado
	if got := buscarIndicador(t, evaluar(n, Instantanea{}), "Disco de datos").Veredicto; got != vAtencion {
		t.Fatalf("al 90 %% debería avisar, dio %q", got)
	}
	n.Datos.LibresBytes = n.Datos.TotalBytes / 50 // 98 % usado
	if got := buscarIndicador(t, evaluar(n, Instantanea{}), "Disco de datos").Veredicto; got != vFallo {
		t.Fatalf("al 98 %% debería fallar, dio %q", got)
	}
}

// El defecto que 00_RECTOR.md §12.5 lleva cuatro veces corrigiendo en los
// verificadores del proyecto: aprobar sin haber verificado nada.
func TestSinMuestrasNoSeApruebaNada(t *testing.T) {
	is := evaluar(nodoSano(), Instantanea{})
	for _, nombre := range []string{"SLI-1", "SLI-2", "SLI-3"} {
		ind := buscarIndicador(t, is, nombre)
		if ind.Veredicto != vDesconocido {
			t.Fatalf("%s sin muestras dio %q; un ok aquí sería aprobar sin verificar",
				nombre, ind.Veredicto)
		}
	}
}

func TestSLIsConMuestras(t *testing.T) {
	i := Instantanea{
		Peticiones: 1000, ErroresServidor: 1, // 99.9 %
		SubidasConfirmadas: 10, SubidasFallidas: 0, // 100 %
		Listados: 100, ListadosLentos: 2, // 98 %
	}
	is := evaluar(nodoSano(), i)
	for _, nombre := range []string{"SLI-1", "SLI-2", "SLI-3"} {
		if got := buscarIndicador(t, is, nombre).Veredicto; got != vOK {
			t.Fatalf("%s debería estar en ok, dio %q", nombre, got)
		}
	}

	// Una sola publicación fallida rompe el SLI-2, cuyo objetivo es el 100 %:
	// el usuario transfirió el archivo entero y no quedó publicado.
	i.SubidasFallidas = 1
	if got := buscarIndicador(t, evaluar(nodoSano(), i), "SLI-2").Veredicto; got != vFallo {
		t.Fatalf("una publicación fallida debe ser fallo, dio %q", got)
	}
}

func TestPeorDeEscalaCorrectamente(t *testing.T) {
	casos := []struct {
		in   []veredicto
		peor veredicto
	}{
		{[]veredicto{vOK, vOK}, vOK},
		{[]veredicto{vOK, vDesconocido}, vDesconocido},
		{[]veredicto{vDesconocido, vAtencion}, vAtencion},
		{[]veredicto{vAtencion, vFallo, vOK}, vFallo},
	}
	for _, c := range casos {
		var is []indicador
		for _, v := range c.in {
			is = append(is, indicador{Veredicto: v})
		}
		if got := peorDe(is); got != c.peor {
			t.Fatalf("peorDe(%v) = %q, se esperaba %q", c.in, got, c.peor)
		}
	}
}

// El JSON debe decir «null» y no «0» en lo que no se pudo medir. Es la
// diferencia entre «0 °C» y «no lo sé», y sin ella el extremo no sirve.
func TestJSONDistingueNoMedidoDeCero(t *testing.T) {
	n := nodoSano()
	n.TemperaturaOK = false
	n.CPUOK = false
	n.Throttled = sistema.Throttled{}

	b, err := json.Marshal(comoJSON(n, Instantanea{}, nil))
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, campo := range []string{`"temperatura_c":null`, `"cpu_porcentaje":null`, `"throttled":null`} {
		if !strings.Contains(s, campo) {
			t.Fatalf("falta %s en el JSON:\n%s", campo, s)
		}
	}
}

func TestJSONFueraDeLinuxNoInventaVolumenes(t *testing.T) {
	n := sistema.Nodo{Datos: sistema.Volumen{Punto: "/srv/nas"}, Avisos: []string{"solo Linux"}}
	e := comoJSON(n, Instantanea{}, nil)
	if e.Nodo.Disponible {
		t.Fatal("no debería declararse disponible")
	}
	if e.Nodo.Datos.TotalBytes != nil {
		t.Fatal("un volumen no leído no puede llevar tamaño")
	}
	if len(e.Avisos) == 0 {
		t.Fatal("el motivo debe viajar en el JSON, no perderse")
	}
}

// ---------------------------------------------------------------------------
// De extremo a extremo
// ---------------------------------------------------------------------------

// Con sesión, /estado responde en las dos formas. Fuera de Linux el nodo no se
// puede medir (D-06), y lo que se comprueba aquí es justamente que eso NO
// rompe el extremo: informa de lo que sí sabe y declara lo que no.
func TestEstadoConSesionRespondeHTMLYJSON(t *testing.T) {
	s := servidorConAuth(t)
	h := s.Rutas()
	cookie := abrirSesionDePrueba(t, h)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/estado", nil)
	r.AddCookie(cookie)
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /estado -> %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("Content-Type = %q", ct)
	}
	// Y se mira el CUERPO, no solo el código. La plantilla se ejecuta después
	// de escribir la cabecera: si reventara a mitad, el error se registraría y
	// esta petición seguiría siendo un 200 con media página. Comprobar solo el
	// código sería aprobar sin verificar (00_RECTOR.md §12.5).
	//
	// El último trozo es la línea FINAL de la plantilla, y por eso no es un
	// encabezado: el 2026-08-04 este test se puso en rojo al renombrar «El
	// servicio» a «Servicio» por gusto estético. El ancla de «se renderizó
	// entero» no puede depender de una palabra que se cambia al retocar el
	// aspecto; sí puede depender de que exista el cierre de la página.
	// «Disco de datos» es un rótulo que el responsable pidió NO renombrar, así
	// que es un ancla estable; «</html>» es el cierre de la plantilla y prueba
	// que se renderizó entera. Las anclas anteriores —un encabezado y un
	// párrafo de ayuda— se cayeron dos veces al retocar el aspecto, que es
	// justo lo que este comentario lleva avisando desde el 2026-08-04.
	cuerpo := w.Body.String()
	for _, trozo := range []string{"Disco de datos", "Medio de arranque", "</html>"} {
		if !strings.Contains(cuerpo, trozo) {
			t.Fatalf("la página no contiene %q; ¿falló el render?\n%s", trozo, cuerpo)
		}
	}
	// El veredicto va también como texto, no solo como color: un semáforo que
	// solo distingue por color no lo lee quien no distingue esos colores.
	if !strings.Contains(cuerpo, "desconocido") && !strings.Contains(cuerpo, "ok") {
		t.Fatal("el veredicto de cada indicador debe aparecer escrito, no solo como clase CSS")
	}
	// Un panel de estado cacheado por el navegador mostraría cifras viejas,
	// que es la peor forma posible de fallar para un panel de estado.
	if cc := w.Header().Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Fatalf("Cache-Control = %q; /estado no puede cachearse", cc)
	}

	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest("GET", "/estado?formato=json", nil)
	r2.AddCookie(cookie)
	h.ServeHTTP(w2, r2)
	if w2.Code != http.StatusOK {
		t.Fatalf("GET /estado?formato=json -> %d", w2.Code)
	}
	var salida map[string]any
	if err := json.Unmarshal(w2.Body.Bytes(), &salida); err != nil {
		t.Fatalf("el JSON no es válido: %v\n%s", err, w2.Body.String())
	}
	if _, ok := salida["veredicto"]; !ok {
		t.Fatal("el JSON debe traer el veredicto global")
	}
	if _, ok := salida["indicadores"]; !ok {
		t.Fatal("el JSON debe traer los indicadores, no solo cifras crudas")
	}
}

// La negociación por Accept también, que es la forma correcta.
func TestEstadoNegociaPorAccept(t *testing.T) {
	s := servidorConAuth(t)
	h := s.Rutas()
	cookie := abrirSesionDePrueba(t, h)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/estado", nil)
	r.Header.Set("Accept", "application/json")
	r.AddCookie(cookie)
	h.ServeHTTP(w, r)
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("Content-Type = %q", ct)
	}
}

// Las peticiones a /estado se cuentan como cualquier otra: el envoltorio de
// registro las anota sin que el manejador tenga que acordarse.
func TestLosContadoresSeAlimentanSolos(t *testing.T) {
	s := servidorConAuth(t)
	h := s.Rutas()
	antes := s.contadores.instantanea().Peticiones

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil)) // 401, pero cuenta

	i := s.contadores.instantanea()
	if i.Peticiones != antes+1 {
		t.Fatalf("peticiones = %d, se esperaba %d", i.Peticiones, antes+1)
	}
	if i.ErroresCliente != 1 {
		t.Fatalf("un 401 debe contar como error de cliente, no de servidor: %+v", i)
	}
	if i.Listados != 1 {
		t.Fatalf("GET / es un listado a efectos del SLI-3: %+v", i)
	}
}

// Regresión de la SEGUNDA pasada sobre el propio código de la Fase 4.
//
// «bytes descargados» contaba el cuerpo de cualquier respuesta bajo
// /descargar/, incluidas las de error. Los tres intentos de salto de ruta que
// prueba 06_verificar_web.sh habrían sumado sus 400 a los bytes servidos, y el
// indicador habría dicho que el NAS sirvió contenido donde en realidad rechazó
// un ataque. Ahora exige además un 2xx.
func TestLosErroresNoCuentanComoBytesDescargados(t *testing.T) {
	c := nuevosContadores()

	c.anotarRespuesta(400, 512, time.Millisecond, rutaDescarga) // salto de ruta
	c.anotarRespuesta(404, 300, time.Millisecond, rutaDescarga) // no existe
	if got := c.instantanea().BytesDescargados; got != 0 {
		t.Fatalf("las respuestas de error sumaron %d bytes descargados", got)
	}

	c.anotarRespuesta(206, 1000, time.Millisecond, rutaDescarga) // rango, RF-10
	c.anotarRespuesta(200, 4096, time.Millisecond, rutaDescarga)
	if got := c.instantanea().BytesDescargados; got != 5096 {
		t.Fatalf("bytes descargados = %d, se esperaban 5096", got)
	}
}

func abrirSesionDePrueba(t *testing.T, h http.Handler) *http.Cookie {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/acceso",
		strings.NewReader(url.Values{
			"usuario": {autenticacion.NombreSuperusuario},
			"clave":   {claveDePrueba},
		}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.ServeHTTP(w, r)
	for _, c := range w.Result().Cookies() {
		if c.Name == nombreCookie {
			return c
		}
	}
	t.Fatal("no se pudo abrir sesión de prueba")
	return nil
}

// ---------------------------------------------------------------------------
// Alertas
// ---------------------------------------------------------------------------

// registroCapturado recoge las líneas emitidas para poder afirmar sobre ellas.
type registroCapturado struct{ lineas []slog.Record }

func (r *registroCapturado) Enabled(context.Context, slog.Level) bool { return true }
func (r *registroCapturado) Handle(_ context.Context, rec slog.Record) error {
	r.lineas = append(r.lineas, rec)
	return nil
}
func (r *registroCapturado) WithAttrs([]slog.Attr) slog.Handler { return r }
func (r *registroCapturado) WithGroup(string) slog.Handler      { return r }

func (r *registroCapturado) niveles() []slog.Level {
	var out []slog.Level
	for _, l := range r.lineas {
		out = append(out, l.Level)
	}
	return out
}

// Repetir la misma alerta cada cinco minutos son 288 líneas idénticas al día
// escritas en el medio de arranque, que por P9 es CONSUMIBLE. Se avisa del
// cambio, no del estado.
func TestSoloSeAnuncianLosCambios(t *testing.T) {
	cap := &registroCapturado{}
	s := servidorDePrueba()
	s.reg = slog.New(cap)

	malo := []indicador{{Nombre: "Disco de datos", Valor: "98 %", Veredicto: vFallo, Accion: "liberar"}}

	s.anunciar(malo)
	if len(cap.lineas) != 1 {
		t.Fatalf("la primera vez debe avisar una vez, avisó %d", len(cap.lineas))
	}
	for range 10 {
		s.anunciar(malo)
	}
	if len(cap.lineas) != 1 {
		t.Fatalf("repetir el mismo veredicto no debe volver a escribir; hay %d líneas", len(cap.lineas))
	}

	// Y la recuperación sí se anuncia: es la que cierra la alerta anterior.
	s.anunciar([]indicador{{Nombre: "Disco de datos", Valor: "40 %", Veredicto: vOK}})
	if len(cap.lineas) != 2 {
		t.Fatalf("la recuperación debe constar; hay %d líneas", len(cap.lineas))
	}
	if cap.lineas[1].Level != slog.LevelInfo {
		t.Fatalf("la recuperación es informativa, no un aviso: %v", cap.lineas[1].Level)
	}
}

// Al arrancar, casi todo está sin medir y los SLI no tienen muestras. Eso no
// es una novedad que anunciar. Un FALLO sí, aunque sea la primera pasada.
func TestElArranqueNoInundaElDiario(t *testing.T) {
	cap := &registroCapturado{}
	s := servidorDePrueba()
	s.reg = slog.New(cap)

	s.anunciar([]indicador{
		{Nombre: "SLI-1", Veredicto: vDesconocido},
		{Nombre: "SLI-2", Veredicto: vDesconocido},
		{Nombre: "RAM usada", Veredicto: vOK},
		{Nombre: "Medio de arranque", Veredicto: vFallo, Accion: "reconstruir"},
	})

	niveles := cap.niveles()
	if len(niveles) != 1 || niveles[0] != slog.LevelError {
		t.Fatalf("solo el fallo debía salir en la primera pasada, salieron %v", niveles)
	}
}

func TestDegradarseADesconocidoSiAvisa(t *testing.T) {
	cap := &registroCapturado{}
	s := servidorDePrueba()
	s.reg = slog.New(cap)

	s.anunciar([]indicador{{Nombre: "Limitación del SoC", Veredicto: vOK}})
	s.anunciar([]indicador{{Nombre: "Limitación del SoC", Veredicto: vDesconocido}})

	niveles := cap.niveles()
	if len(niveles) != 1 || niveles[0] != slog.LevelWarn {
		t.Fatalf("perder una medida que antes se tenía es un aviso; salieron %v", niveles)
	}
}

// NINGUNA FILA LLEVA GUION EN LA COLUMNA DE ESTADO.
//
// El responsable lo pidió dos veces —el 05/08/2026 la segunda, señalando que la
// primera no se había aplicado del todo— y por eso pasa a estar escrito aquí en
// vez de confiado a la memoria de quien retoque la plantilla.
//
// El razonamiento, para que no se «arregle» al revés: una fila de contexto no
// tiene veredicto porque no tiene UMBRAL contra el que aprobar o suspender —el
// uso de CPU es un número, no una alarma—, y rellenar ese hueco con un guion,
// con «N/A» o con «no aplica» es escribir algo donde no hay nada que decir. La
// pastilla se queda vacía y el CSS la esconde.
func TestNingunaFilaDeEstadoLlevaGuionEnLaColumnaDeEstado(t *testing.T) {
	s := servidorConAuth(t)
	h := s.Rutas()
	cookie := abrirSesionDePrueba(t, h)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/estado", nil)
	r.AddCookie(cookie)
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /estado -> %d", w.Code)
	}
	cuerpo := w.Body.String()

	// Se busca el guion DENTRO de una pastilla, no en toda la página: hay
	// valores que lo llevan legítimamente («0x0 — sin limitación»), y prohibir
	// el carácter entero habría convertido esta prueba en una molestia que el
	// siguiente que la vea en rojo desactiva.
	if strings.Contains(cuerpo, `class="pastilla">—`) {
		t.Fatal(`sigue habiendo pastillas con «—»; una fila sin umbral va vacía`)
	}

	// Y que la pastilla vacía EXISTA como elemento, que es lo que permite al
	// flujo en vivo escribir en ella sin crear ni destruir nodos.
	if !strings.Contains(cuerpo, `class="pastilla p"></span>`) {
		t.Fatal("no hay ninguna pastilla vacía; las filas de contexto deben " +
			"conservar el elemento aunque no tengan veredicto que enseñar")
	}
}
