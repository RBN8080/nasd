package web

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"nasd/internal/autenticacion"
	"nasd/internal/seguridad"
)

// Pruebas de la indistinguibilidad preautenticación — ADR-0072, RF-38.
//
// Lo que se comprueba aquí NO es HTML: es una PROPIEDAD sobre las respuestas.
// Que la página se vea bien lo miran otras pruebas; estas miran que dos
// peticiones distintas sean indistinguibles desde fuera y distinguibles desde
// dentro, que es lo contrario de lo que suele pedírsele a un servidor.

// respuestaComparable es lo que un cliente puede observar de una respuesta, sin
// los campos que varían con el tiempo y no con la ruta.
//
// # POR QUÉ SE QUITA Date Y NO SE EXIGE IGUALDAD BYTE A BYTE
//
// Porque Date lo pone el transporte y cambia con el segundo, no con la ruta.
// Lo que ADR-0072 exige no es que dos respuestas separadas en el tiempo sean
// idénticas: es que LA EXISTENCIA DE UNA RUTA no produzca ninguna diferencia
// observable. Comparar Date sería comparar el reloj.
type respuestaComparable struct {
	estado    int
	cabeceras string
	cuerpo    string
}

func observar(t *testing.T, s *Servidor, metodo, ruta string, cookies ...*http.Cookie) respuestaComparable {
	t.Helper()
	w := pedir(t, s, metodo, ruta, cookies...)

	var lineas []string
	for k, vs := range w.Result().Header {
		if k == "Date" {
			continue
		}
		lineas = append(lineas, k+": "+strings.Join(vs, ", "))
	}
	sort.Strings(lineas)
	return respuestaComparable{
		estado:    w.Code,
		cabeceras: strings.Join(lineas, "\n"),
		cuerpo:    w.Body.String(),
	}
}

// O1, O2, O3 y O9 en una sola tabla, porque son la misma afirmación mirada
// desde sitios distintos: para un desconocido, TODAS estas peticiones son la
// misma respuesta.
//
// Las siete rutas están elegidas a mano y cubren las cinco clases que un
// atacante usaría para separar el mapa: una ruta del superusuario, una normal,
// la raíz, dos sondeos de software que este NAS no ejecuta, un asset que
// existe pero no es público, y una ruta inventada.
func TestParaUnDesconocidoTodasLasRutasSonLaMismaRespuesta(t *testing.T) {
	s := servidorConAuth(t)

	rutas := []string{
		"/estado",            // existe, es del superusuario
		"/administracion",    // existe, es del superusuario
		"/seguridad",         // existe, es del superusuario
		"/ver/fotos",         // existe, es de cualquiera
		"/",                  // existe, es la raíz
		"/.git/config",       // no existe; software ajeno
		"/.env",              // no existe; software ajeno
		"/wp-login.php",      // no existe; software ajeno
		"/estatico/menus.js", // EXISTE como archivo, y no es público
		"/esto-no-existe-123",
	}

	patron := observar(t, s, "GET", rutas[0])
	if patron.estado != http.StatusNotFound {
		t.Fatalf("la respuesta opaca es %d y ADR-0072 exige 404", patron.estado)
	}
	for _, ruta := range rutas[1:] {
		got := observar(t, s, "GET", ruta)
		if got.estado != patron.estado {
			t.Errorf("GET %s -> %d; GET %s -> %d: el estado enumera",
				ruta, got.estado, rutas[0], patron.estado)
		}
		if got.cabeceras != patron.cabeceras {
			t.Errorf("GET %s trae otras cabeceras que GET %s:\n%s\n---\n%s",
				ruta, rutas[0], got.cabeceras, patron.cabeceras)
		}
		if got.cuerpo != patron.cuerpo {
			t.Errorf("GET %s trae otro cuerpo que GET %s (%d bytes contra %d)",
				ruta, rutas[0], len(got.cuerpo), len(patron.cuerpo))
		}
	}

	// Y LO QUE NO PUEDE LLEVAR NINGUNA. Cada una de estas cabeceras es, por sí
	// sola, una forma de contestar la pregunta que el 404 viene a no contestar.
	for _, cab := range []string{"Allow", "Location", "WWW-Authenticate", "Set-Cookie"} {
		if strings.Contains(patron.cabeceras, cab+":") {
			t.Errorf("la respuesta opaca lleva %s y eso dice algo de la ruta:\n%s",
				cab, patron.cabeceras)
		}
	}
	// Content-Length declarado: si no lo estuviera, la longitud saldría del
	// troceado y podría depender de lo que se escribiera.
	if !strings.Contains(patron.cabeceras, "Content-Length:") {
		t.Error("la respuesta opaca no declara Content-Length")
	}
}

// O1, O2, O4 — LA OTRA MITAD, y la que da sentido a todo lo anterior: por
// dentro, esas mismas peticiones NO son la misma cosa.
//
// Si esta prueba se pudiera borrar sin que fallara nada, ADR-0072 habría
// cambiado un panel informativo por uno que solo sabe decir «404».
func TestEl404OpacoNoBorraLaVerdadInterior(t *testing.T) {
	s := servidorConAuth(t)

	// Una sesión de verdad, abierta y vencida por este proceso: es el único
	// caso en el que «caducada» se puede demostrar.
	sVencida := servidorConAuth(t)
	sVencida.sesiones = autenticacion.NuevasSesiones(-time.Hour, 0)
	vencido, err := sVencida.sesiones.Abrir(autenticacion.NombreSuperusuario)
	if err != nil {
		t.Fatal(err)
	}

	casos := []struct {
		nombre   string
		servidor *Servidor
		metodo   string
		ruta     string
		cookies  []*http.Cookie
		esperado seguridad.Motivo
	}{
		{"ruta protegida conocida", s, "GET", "/estado", nil, seguridad.SinSesion},
		{"ruta inexistente", s, "GET", "/.git/config", nil, seguridad.RutaInexistente},
		{"ruta inventada", s, "GET", "/esto-no-existe-123", nil, seguridad.RutaInexistente},
		{"método equivocado sobre ruta real", s, "POST", "/estado", nil, seguridad.MetodoNoPermitido},
		{"cookie que el servidor nunca emitió", s, "GET", "/estado",
			[]*http.Cookie{{Name: nombreCookie, Value: "inventada"}}, seguridad.SesionInvalida},
		{"sesión abierta por este proceso y vencida", sVencida, "GET", "/estado",
			[]*http.Cookie{{Name: nombreCookie, Value: vencido}}, seguridad.SesionCaducada},
	}
	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			w := pedir(t, c.servidor, c.metodo, c.ruta, c.cookies...)
			if w.Code != http.StatusNotFound {
				t.Fatalf("%s %s -> %d; hacia fuera todo es el 404 opaco", c.metodo, c.ruta, w.Code)
			}
			if got := ultimoEvento(t, c.servidor).Motivo; got != c.esperado {
				t.Errorf("hacia dentro se anotó %q; se esperaba %q", got.Etiqueta(), c.esperado.Etiqueta())
			}
		})
	}
}

// O5 — LOS MÉTODOS NO DIBUJAN LA TABLA DE RUTAS.
//
// Antes de ADR-0072, «POST /estado» salía del mux con un 405 y una cabecera
// «Allow: GET, HEAD»: en una sola petición, la confirmación de que /estado
// existe Y la lista de lo que acepta. Medido contra un net/http real el
// 2026-08-24.
func TestLosMetodosNoRevelanLaTablaDeRutas(t *testing.T) {
	s := servidorConAuth(t)

	for _, metodo := range []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS", "HEAD"} {
		real := observar(t, s, metodo, "/estado")
		inventada := observar(t, s, metodo, "/no-existe-en-absoluto")

		if real.estado != http.StatusNotFound {
			t.Errorf("%s /estado -> %d; se esperaba el 404 opaco", metodo, real.estado)
		}
		if real.estado != inventada.estado || real.cabeceras != inventada.cabeceras {
			t.Errorf("%s distingue /estado de una ruta inventada:\n%s\n---\n%s",
				metodo, real.cabeceras, inventada.cabeceras)
		}
		if strings.Contains(real.cabeceras, "Allow:") {
			t.Errorf("%s /estado devolvió Allow: eso es la tabla de métodos", metodo)
		}
	}
}

// O10, O11 — CANONICALIZACIÓN Y REDIRECCIONES.
//
// http.ServeMux limpia rutas y redirige por su cuenta, y esas redirecciones
// llevaban Location. Medido el 2026-08-24 sobre la forma anterior:
//
//	GET //estado        ->  307 Location: /estado
//	GET /foo/../estado  ->  307 Location: /estado
//	GET /ver            ->  307 Location: /ver/     (¡confirma que /ver existe!)
//	GET /estatico       ->  307 Location: /estatico/
//
// Ninguna de las cuatro pasaba por la guarda de sesión.
func TestNingunaCanonicalizacionDeRutaDelataUnaRutaProtegida(t *testing.T) {
	s := servidorConAuth(t)

	// Cada par es «una variante rara que apunta a algo real» contra «la misma
	// variante rara apuntando a algo que no existe». Si las dos son iguales, la
	// variante no sirve para enumerar.
	pares := []struct{ real, falsa string }{
		{"//estado", "//no-existe"},
		{"/estado/", "/no-existe/"},
		{"/foo/../estado", "/foo/../no-existe"},
		{"/%2e%2e/estado", "/%2e%2e/no-existe"},
		{"/%2e/estado", "/%2e/no-existe"},
		{"/ver", "/vvv"},
		{"/ver/", "/vvv/"},
		{"/estatico", "/estatiquito"},
		{"/estatico/", "/estatiquito/"},
		{"/administracion/baja/pepe", "/administracion/baju/pepe"},
	}
	for _, p := range pares {
		real := observar(t, s, "GET", p.real)
		falsa := observar(t, s, "GET", p.falsa)
		if real.estado != http.StatusNotFound {
			t.Errorf("GET %s -> %d; se esperaba el 404 opaco", p.real, real.estado)
		}
		if real.estado != falsa.estado || real.cabeceras != falsa.cabeceras || real.cuerpo != falsa.cuerpo {
			t.Errorf("%s y %s se distinguen desde fuera:\n%d %s\n---\n%d %s",
				p.real, p.falsa, real.estado, real.cabeceras, falsa.estado, falsa.cabeceras)
		}
	}
}

// O12 — OPTIONS, incluido el «OPTIONS *» que net/http contestaba ÉL SOLO.
//
// Con DisableGeneralOptionsHandler sin poner, net/http respondía 200 y
// Content-Length: 0 antes del manejador: fuera de la puerta, fuera de las
// cabeceras de seguridad y fuera del registro. No delataba una ruta —«*» no es
// una ruta— pero sí era una respuesta del servidor que nadie veía ni gobernaba.
func TestOptionsPasaPorLaPuertaComoTodoLoDemas(t *testing.T) {
	s := servidorConAuth(t)
	srv := s.HTTPServer("127.0.0.1", 0)
	if !srv.DisableGeneralOptionsHandler {
		t.Fatal("el OPTIONS general de net/http sigue encendido: contesta antes de la puerta")
	}

	for _, ruta := range []string{"/estado", "/administracion", "/no-existe"} {
		if w := pedir(t, s, "OPTIONS", ruta); w.Code != http.StatusNotFound {
			t.Errorf("OPTIONS %s -> %d; se esperaba el 404 opaco", ruta, w.Code)
		}
	}

	// «OPTIONS *» no es una ruta y httptest.NewRequest no lo construye: se
	// arma a mano, igual que lo mandaría un cliente.
	r := httptest.NewRequest("OPTIONS", "http://nas/", nil)
	r.RequestURI = "*"
	r.URL.Path = "*"
	w := httptest.NewRecorder()
	s.Rutas().ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Errorf("OPTIONS * -> %d; se esperaba el 404 opaco", w.Code)
	}
	// Y con las cabeceras globales puestas, que es la mitad del motivo por el
	// que se apagó el manejador automático.
	if w.Header().Get("Content-Security-Policy") == "" {
		t.Error("OPTIONS * salió sin CSP")
	}
}

// O6 — DESPUÉS DE AUTENTICARSE, HTTP VUELVE A SER HTTP.
//
// La opacidad es EXCLUSIVAMENTE preautenticación. Convertir también el interior
// en errores falsos habría cambiado un servidor que oculta su mapa por uno que
// miente a su dueño, y con él se administra un disco.
func TestConSesionVuelveLaSemanticaRealDeHTTP(t *testing.T) {
	s := servidorConAuth(t)
	cookie, _ := sesionAbierta(t, s)

	casos := []struct {
		nombre   string
		metodo   string
		ruta     string
		esperado int
	}{
		// Ruta válida y método válido: se atiende de verdad.
		{"ruta y método correctos", "GET", "/estado", http.StatusOK},
		// Ruta inexistente: 404 DE VERDAD, no opaco.
		{"ruta inexistente", "GET", "/esto-no-existe-123", http.StatusNotFound},
		// Ruta existente, método que no es: 405, con su Allow y todo.
		{"método no permitido", "PUT", "/estado", http.StatusMethodNotAllowed},
	}
	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			w := pedir(t, s, c.metodo, c.ruta, cookie)
			if w.Code != c.esperado {
				t.Fatalf("%s %s con sesión -> %d; se esperaba %d", c.metodo, c.ruta, w.Code, c.esperado)
			}
		})
	}

	// Y el 403 de «no te toca», que es la respuesta correcta cuando quien pide
	// ya está identificado: fingir un 404 aquí convertiría una regla en una
	// avería. Se usa un usuario normal contra una ruta del superusuario.
	normal := sesionAbiertaPara(t, s, "ana")
	if w := pedir(t, s, "GET", "/estado", normal); w.Code != http.StatusForbidden {
		t.Errorf("un usuario normal en /estado -> %d; se esperaba 403", w.Code)
	}
}

// O7 — /salir DEJA DE DELATARSE.
//
// Era público, así que sin sesión respondía 303 hacia /acceso mientras todo lo
// demás respondía 401: la única ruta del servidor que se podía confirmar sin
// tener nada. Cerrar una sesión que no existe no es una operación que nadie
// necesite.
func TestSalirNoSeDelataSinSesion(t *testing.T) {
	s := servidorConAuth(t)

	salir := observar(t, s, "POST", "/salir")
	inventada := observar(t, s, "POST", "/salir-que-no-existe")
	if salir.estado != http.StatusNotFound {
		t.Errorf("POST /salir sin sesión -> %d; se esperaba el 404 opaco", salir.estado)
	}
	if salir.estado != inventada.estado || salir.cabeceras != inventada.cabeceras {
		t.Errorf("/salir se distingue de una ruta inventada:\n%s\n---\n%s",
			salir.cabeceras, inventada.cabeceras)
	}

	// Y CON SESIÓN SIGUE FUNCIONANDO IGUAL, que es la mitad que importa: esto
	// no es quitar una función, es dejar de anunciarla.
	cookie, _ := sesionAbierta(t, s)
	w := pedir(t, s, "POST", "/salir", cookie)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("POST /salir con sesión -> %d; se esperaba 303", w.Code)
	}
	if s.sesiones.Valida(cookie.Value) {
		t.Error("la sesión seguía viva después de salir")
	}
}

// O8 — SOLO LOS ASSETS DEL FORMULARIO SON PÚBLICOS, y la lista se DERIVA de la
// plantilla en vez de escribirse dos veces.
//
// # POR QUÉ SE LEE LA PLANTILLA Y NO SE COPIAN LOS NOMBRES
//
// Porque una lista escrita a mano aquí y otra en estatico.go es exactamente la
// clase de par que envejece por separado: el día que el formulario necesite un
// archivo nuevo, la lista de producción se quedaría corta y esta prueba
// seguiría pasando. Leyendo la plantilla, quien cambie el formulario se entera
// aquí y en el mismo commit.
func TestSoloSonPublicosLosAssetsQueElFormularioNecesita(t *testing.T) {
	s := servidorConAuth(t)

	// Lo que acceso.html referencia DE VERDAD, cabeza incluida. El mismo
	// criterio de captura que TestLasPlantillasSoloPidenRecursosQueExisten.
	ref := regexp.MustCompile(`(?:href|src)="(/estatico/[^"]+)"`)
	var fuente strings.Builder
	for _, nombre := range []string{"acceso.html", "cabeza"} {
		p := s.plantillas.Lookup(nombre)
		if p == nil {
			t.Fatalf("la plantilla %q no existe; ¿se renombró?", nombre)
		}
		fuente.WriteString(p.Tree.Root.String())
	}
	necesarios := map[string]bool{}
	for _, m := range ref.FindAllStringSubmatch(fuente.String(), -1) {
		necesarios[m[1]] = true
	}
	if len(necesarios) == 0 {
		t.Fatal("el formulario de acceso no pidió ningún recurso; ¿cambió la forma de referenciarlos?")
	}

	declarados := map[string]bool{}
	for _, a := range assetsPublicos {
		declarados[a] = true
	}
	for a := range necesarios {
		if !declarados[a] {
			t.Errorf("%s lo necesita el formulario y NO está en assetsPublicos: la página de acceso saldrá rota", a)
		}
	}
	for a := range declarados {
		if !necesarios[a] {
			t.Errorf("%s es público y el formulario NO lo necesita: superficie regalada", a)
		}
	}

	// Y ahora la propiedad de verdad, contra el servidor: los públicos se
	// sirven sin cookie y CUALQUIER OTRO se comporta como una ruta inventada.
	for a := range declarados {
		if w := pedir(t, s, "GET", a); w.Code != http.StatusOK {
			t.Errorf("GET %s sin sesión -> %d; el formulario no podría dibujarse", a, w.Code)
		}
	}
	inventada := observar(t, s, "GET", "/estatico/no-existe-jamas.js")
	for _, a := range []string{
		"/estatico/subida.js", "/estatico/cuentas.js", "/estatico/estado.js",
		"/estatico/menus.js", "/estatico/visor.js",
	} {
		got := observar(t, s, "GET", a)
		if got.estado != http.StatusNotFound {
			t.Errorf("GET %s sin sesión -> %d: es huella del programa regalada", a, got.estado)
		}
		if got.cabeceras != inventada.cabeceras || got.cuerpo != inventada.cuerpo {
			t.Errorf("%s se distingue de un estático inventado: sirve para fingerprinting", a)
		}
		// Y el ETag es la vía más fina: confirmaría el archivo sin cuerpo.
		if got.cabeceras != "" && strings.Contains(got.cabeceras, "Etag:") {
			t.Errorf("GET %s sin sesión devolvió ETag", a)
		}
	}
}

// EL SONDEO DEL ROUTER NO PUEDE EJECUTAR NADA, y esto no se argumenta: se
// comprueba.
//
// resolverRuta le pregunta al mux qué estado daría, ejecutándolo contra un
// sumidero. Toda la seguridad de esa maniobra depende de una afirmación sobre
// net/http: cuando Handler() devuelve patrón VACÍO, el manejador que devuelve
// es del propio mux —404 o 405— y nunca uno de la aplicación. Aquí se registran
// manejadores que ENTRAN EN PÁNICO: si alguno se ejecutara, la prueba revienta.
func TestElSondeoDelRouterNoEjecutaNingunManejador(t *testing.T) {
	explota := func(w http.ResponseWriter, r *http.Request) {
		panic("el sondeo ejecutó un manejador de la aplicación: " + r.Method + " " + r.URL.Path)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", explota)
	mux.HandleFunc("GET /estado", explota)
	mux.HandleFunc("POST /subidas", explota)
	mux.HandleFunc("GET /ver/{ruta...}", explota)

	casos := []struct {
		metodo, ruta string
		esperado     seguridad.Motivo
	}{
		{"POST", "/estado", seguridad.MetodoNoPermitido},
		{"PUT", "/estado", seguridad.MetodoNoPermitido},
		{"DELETE", "/estado", seguridad.MetodoNoPermitido},
		{"OPTIONS", "/estado", seguridad.MetodoNoPermitido},
		{"GET", "/subidas", seguridad.MetodoNoPermitido},
		{"POST", "/", seguridad.MetodoNoPermitido},
		{"GET", "/.git/config", seguridad.RutaInexistente},
		{"GET", "/no-existe", seguridad.RutaInexistente},
		{"GET", "/estado/", seguridad.RutaInexistente},
		{"POST", "/no-existe", seguridad.RutaInexistente},
		{"GET", "//no-existe", seguridad.RutaInexistente},
	}
	for _, c := range casos {
		r := httptest.NewRequest(c.metodo, c.ruta, nil)
		motivo, existe := resolverRuta(mux, r)
		if existe {
			t.Errorf("%s %s: resolverRuta dice que se puede atender", c.metodo, c.ruta)
			continue
		}
		if motivo != c.esperado {
			t.Errorf("%s %s -> %q; se esperaba %q",
				c.metodo, c.ruta, motivo.Etiqueta(), c.esperado.Etiqueta())
		}
	}

	// Y las que SÍ existen deben decir que existen, sin sondear nada.
	for _, c := range []struct{ metodo, ruta string }{
		{"GET", "/estado"}, {"GET", "/"}, {"POST", "/subidas"}, {"GET", "/ver/a/b"},
	} {
		r := httptest.NewRequest(c.metodo, c.ruta, nil)
		if _, existe := resolverRuta(mux, r); !existe {
			t.Errorf("%s %s: resolverRuta no reconoce una ruta registrada", c.metodo, c.ruta)
		}
	}
}

// El sondeo tampoco puede dejar rastro en la petición que se va a atender de
// verdad: ServeMux.ServeHTTP escribe en r.Pattern, y el sondeo se hace sobre
// una copia justamente por eso.
func TestElSondeoDelRouterNoEnsuciaLaPeticion(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /estado", func(http.ResponseWriter, *http.Request) {})

	r := httptest.NewRequest("POST", "/estado", nil)
	r.Pattern = "intacto"
	resolverRuta(mux, r)
	if r.Pattern != "intacto" {
		t.Errorf("el sondeo pisó r.Pattern: %q", r.Pattern)
	}
}

// LAS CABECERAS DE SEGURIDAD TAMBIÉN SALEN POR EL 404 OPACO.
//
// Una vía de error que se escapara sin CSP sería el agujero abierto por la
// puerta de al lado que D-21 obliga a comprobar vía por vía en lugar de darlo
// por hecho.
func TestElOpacoLlevaLasCabecerasDeSeguridad(t *testing.T) {
	s := servidorConAuth(t)
	w := pedir(t, s, "GET", "/estado")

	for cab, esperado := range map[string]string{
		"Content-Security-Policy": cspBase("'none'"),
		"X-Frame-Options":         "DENY",
		"Referrer-Policy":         "same-origin",
		"X-Content-Type-Options":  "nosniff",
	} {
		if got := w.Header().Get(cab); got != esperado {
			t.Errorf("%s = %q; se esperaba %q", cab, got, esperado)
		}
	}
}

// EL 404 OPACO SIGUE ALIMENTANDO LAS SEÑALES, que es lo que separa esta
// decisión de «apagar el panel para no dar información».
func TestElOpacoSigueAlimentandoLasSenales(t *testing.T) {
	s := servidorConAuth(t)

	// Software ajeno: una sola ruta basta.
	pedir(t, s, "GET", "/wp-login.php")
	// Exploración: hacen falta umbralExploracion rutas inexistentes DISTINTAS.
	for _, ruta := range []string{
		"/.env", "/.git/config", "/phpmyadmin/", "/admin.php", "/config.json",
		"/vendor/x", "/backup.zip", "/server-status", "/.aws/credentials",
	} {
		pedir(t, s, "GET", ruta)
	}

	origenes := seguridad.PorOrigen(s.seguridad.Desde(time.Time{}))
	if len(origenes) == 0 {
		t.Fatal("no se agregó ningún origen: el 404 opaco dejó de anotarse")
	}
	var senales []seguridad.Senal
	for _, o := range origenes {
		senales = append(senales, o.Senales...)
	}
	tiene := func(x seguridad.Senal) bool {
		for _, s := range senales {
			if s == x {
				return true
			}
		}
		return false
	}
	if !tiene(seguridad.SenalSoftwareAjeno) {
		t.Error("el 404 opaco dejó de alimentar SenalSoftwareAjeno")
	}
	if !tiene(seguridad.SenalExploracion) {
		t.Error("el 404 opaco dejó de alimentar SenalExploracion")
	}
}

// Y EL MÉTODO EQUIVOCADO NO INFLA LA EXPLORACIÓN.
//
// SenalExploracion cuenta RUTAS INEXISTENTES distintas. Si «POST /estado» se
// siguiera anotando como ruta inexistente —que es lo que hacía antes de
// ADR-0072, porque Handler() devuelve patrón vacío también ahí— bastarían ocho
// métodos raros sobre rutas REALES para disparar la señal. Eso es una alarma
// sobre un hecho que no es exploración.
func TestElMetodoEquivocadoNoCuentaComoExploracion(t *testing.T) {
	s := servidorConAuth(t)

	for _, m := range []string{"POST", "PUT", "PATCH", "DELETE", "OPTIONS"} {
		pedir(t, s, m, "/estado")
		pedir(t, s, m, "/seguridad")
	}
	for _, o := range seguridad.PorOrigen(s.seguridad.Desde(time.Time{})) {
		if o.RutasInexistentes != 0 {
			t.Errorf("diez métodos equivocados sobre dos rutas REALES contaron como %d rutas inexistentes",
				o.RutasInexistentes)
		}
		for _, sn := range o.Senales {
			if sn == seguridad.SenalExploracion {
				t.Error("métodos equivocados sobre rutas reales dispararon «exploración»")
			}
		}
	}
}
