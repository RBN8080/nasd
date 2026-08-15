package web

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"nasd/internal/autenticacion"
	"nasd/internal/seguridad"
)

// pedir lanza una petición contra el servidor y devuelve la respuesta, con la
// dirección de origen fijada — httptest.NewRequest pone 192.0.2.1:1234 por
// omisión, que es la red de documentación (RFC 5737) y cae en «internet».
func pedir(t *testing.T, s *Servidor, metodo, ruta string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(metodo, ruta, nil)
	for _, c := range cookies {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	s.Rutas().ServeHTTP(w, r)
	return w
}

// ultimoEvento devuelve el rechazo más reciente. Falla si no hay ninguno:
// «no se anotó nada» es un fallo tan real como «se anotó lo que no era».
func ultimoEvento(t *testing.T, s *Servidor) seguridad.Evento {
	t.Helper()
	ev := s.seguridad.Desde(time.Time{})
	if len(ev) == 0 {
		t.Fatal("no se anotó ningún evento de seguridad")
	}
	return ev[0]
}

// EL HALLAZGO QUE ORIGINÓ TODO ESTO, convertido en prueba.
//
// Antes, una petición a /wp-login.php sin sesión y la primera visita del día
// del responsable producían el MISMO 401, indistinguible, porque el comodín
// «/» de Rutas() va envuelto en exigirSesion y corta antes de que el mux
// interno pueda devolver un 404. Las dos caían en el contador único de
// «rechazadas» de /estado y ahí se acababa la información.
func TestUnSondeoNoSeConfundeConLaPrimeraVisita(t *testing.T) {
	s := servidorConAuth(t)

	// Lo que hace un escáner de Internet.
	if w := pedir(t, s, "GET", "/wp-login.php"); w.Code != http.StatusUnauthorized {
		t.Fatalf("/wp-login.php -> %d; se esperaba 401", w.Code)
	}
	if got := ultimoEvento(t, s).Motivo; got != seguridad.RutaInexistente {
		t.Errorf("un sondeo a una ruta que no existe se anotó como %q", got.Etiqueta())
	}

	// Lo que hace el responsable al abrir el NAS sin cookie. MISMA respuesta,
	// motivo distinto.
	if w := pedir(t, s, "GET", "/"); w.Code != http.StatusUnauthorized {
		t.Fatalf("/ -> %d; se esperaba 401", w.Code)
	}
	if got := ultimoEvento(t, s).Motivo; got != seguridad.SinSesion {
		t.Errorf("la primera visita a una ruta que SÍ existe se anotó como %q", got.Etiqueta())
	}
}

// Y la otra mitad de esa decisión, que es la que protege el servidor: lo que
// cambia es lo que se APUNTA, nunca lo que se CONTESTA. Si el sondeo recibiera
// un 404 y la ruta real un 401, cualquiera podría enumerar la tabla de rutas
// del NAS a base de comparar respuestas.
func TestClasificarNoDelataQueRutasExisten(t *testing.T) {
	s := servidorConAuth(t)
	existe := pedir(t, s, "GET", "/estado")
	noExiste := pedir(t, s, "GET", "/no-existe-esta-ruta")

	if existe.Code != noExiste.Code {
		t.Fatalf("una ruta real responde %d y una inventada %d: eso las enumera",
			existe.Code, noExiste.Code)
	}
	if existe.Body.String() != noExiste.Body.String() {
		t.Error("los cuerpos difieren entre una ruta real y una inventada: eso las enumera")
	}
}

// «Sin sesión» y «sesión caducada» responden igual y significan lo contrario:
// una es un desconocido, la otra alguien que estuvo dentro y se le acabó el
// plazo (ADR-0059). Con un solo contador de 4xx eran el mismo número.
func TestUnaSesionCaducadaNoSeConfundeConUnDesconocido(t *testing.T) {
	s := servidorConAuth(t)
	// Una cookie con un testigo que el servidor no conoce: es exactamente lo
	// que queda en el navegador cuando la sesión se purga.
	vieja := &http.Cookie{Name: nombreCookie, Value: "testigo-que-ya-no-vale"}

	if w := pedir(t, s, "GET", "/", vieja); w.Code != http.StatusUnauthorized {
		t.Fatalf("con cookie caducada -> %d; se esperaba 401", w.Code)
	}
	if got := ultimoEvento(t, s).Motivo; got != seguridad.SesionCaducada {
		t.Errorf("una cookie que ya no vale se anotó como %q", got.Etiqueta())
	}
}

// LA PRUEBA QUE PROTEGE UN SECRETO, y existe por el defecto que 04_SEGURIDAD
// §6 ya documentó para el diario: con dos casillas, tarde o temprano alguien
// teclea su contraseña en la del nombre. Este archivo se guarda en disco, así
// que anotar lo tecleado a ciegas metería esa contraseña en /var/lib.
//
// La regla es: el nombre SOLO si la cuenta existe.
func TestElNombreTecleadoNoLlegaAlHistorialSiLaCuentaNoExiste(t *testing.T) {
	s := servidorConAuth(t)

	// Alguien teclea su contraseña en la casilla del nombre.
	posibleSecreto := "mi-contraseña-de-verdad"
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/acceso", strings.NewReader(url.Values{
		"usuario": {posibleSecreto},
		"clave":   {"lo-que-sea"},
	}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	s.Rutas().ServeHTTP(w, r)

	e := ultimoEvento(t, s)
	if e.Motivo != seguridad.CredencialIncorrecta {
		t.Fatalf("un acceso fallido se anotó como %q", e.Motivo.Etiqueta())
	}
	if e.Cuenta != "" {
		t.Fatalf("se anotó %q como cuenta y esa cuenta NO existe: puede ser una contraseña", e.Cuenta)
	}
	// Y por si alguien «mejorara» esto guardando el campo en otro sitio.
	for _, campo := range []string{e.Cuenta, e.Ruta, e.Agente} {
		if strings.Contains(campo, posibleSecreto) {
			t.Fatalf("lo tecleado en la casilla del nombre acabó en el historial: %q", campo)
		}
	}
}

// Y el complemento: cuando la cuenta SÍ existe, el nombre sirve y se anota —
// si no, no habría forma de ver contra qué cuenta se está insistiendo.
func TestSiLaCuentaExisteSuNombreSiSeAnota(t *testing.T) {
	s := servidorConAuth(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/acceso", strings.NewReader(url.Values{
		"usuario": {autenticacion.NombreSuperusuario},
		"clave":   {"esta-no-es-la-contraseña"},
	}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	s.Rutas().ServeHTTP(w, r)

	if got := ultimoEvento(t, s).Cuenta; got != autenticacion.NombreSuperusuario {
		t.Errorf("cuenta anotada = %q, se esperaba %q", got, autenticacion.NombreSuperusuario)
	}
}

// NADA DE LO PROHIBIDO POR 04_SEGURIDAD §6 PUEDE ACABAR EN EL HISTORIAL.
// Esta prueba existe porque el evento se compone a partir de la petición, y
// añadir un campo nuevo «que viene bien» es fácil; que este archivo se guarda
// en disco, no tanto de recordar.
func TestElHistorialNoGuardaCookiesNiAutorizacion(t *testing.T) {
	s := servidorConAuth(t)

	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer un-testigo-secretisimo")
	r.Header.Set("Cookie", nombreCookie+"=galleta-secreta")
	r.Header.Set("User-Agent", "Mozilla/5.0 (prueba)")
	s.Rutas().ServeHTTP(httptest.NewRecorder(), r)

	e := ultimoEvento(t, s)
	todo := e.Ruta + "|" + e.Agente + "|" + e.Cuenta
	for _, prohibido := range []string{"un-testigo-secretisimo", "galleta-secreta", "Bearer"} {
		if strings.Contains(todo, prohibido) {
			t.Fatalf("el historial contiene %q, que 04_SEGURIDAD §6 prohíbe: %q", prohibido, todo)
		}
	}
	// El User-Agent sí, que es lo único que se extrae de las cabeceras.
	if e.Agente != "Mozilla/5.0 (prueba)" {
		t.Errorf("agente = %q; se esperaba el User-Agent", e.Agente)
	}
}

// Un usuario normal que pulsa algo del superusuario: 403 y motivo propio. No
// es lo mismo que un desconocido, y con el contador único era el mismo número.
func TestUnUsuarioNormalEnRutaDeAdministracionSeAnotaAparte(t *testing.T) {
	s := servidorParaMetricas(t, map[string]int64{"juan": 1})

	// Sesión de una cuenta normal, no del superusuario. Se reutilizan los
	// ayudantes que ya tiene acceso_test.go en vez de rehacer el inicio de
	// sesión a mano aquí.
	cookie := cookieLlamada(entrar(t, s.Rutas(), "juan", claveDeJuan), nombreCookie)
	if cookie == nil {
		t.Fatal("no se abrió la sesión de la cuenta normal")
	}

	if got := pedir(t, s, "GET", "/estado", cookie); got.Code != http.StatusForbidden {
		t.Fatalf("/estado con cuenta normal -> %d; se esperaba 403", got.Code)
	}
	e := ultimoEvento(t, s)
	if e.Motivo != seguridad.PermisoInsuficiente {
		t.Errorf("motivo = %q; se esperaba permiso insuficiente", e.Motivo.Etiqueta())
	}
	// Aquí la cuenta SÍ se anota: la sesión ya está autenticada, así que el
	// nombre es uno real del registro y no algo tecleado.
	if e.Cuenta != "juan" {
		t.Errorf("cuenta = %q; se esperaba «juan»", e.Cuenta)
	}
}

// El historial y el contador de /estado deben contar LO MISMO: este historial
// existe para explicar ese número, y si contaran cosas distintas el panel se
// contradiría con la página de estado — el defecto de documentación que este
// proyecto persigue, ahora en dos pantallas.
func TestElHistorialCuentaLoMismoQueElContadorDeRechazadas(t *testing.T) {
	s := servidorConAuth(t)

	pedir(t, s, "GET", "/")                    // 401
	pedir(t, s, "GET", "/wp-login.php")        // 401
	pedir(t, s, "GET", "/acceso")              // 200, no cuenta
	pedir(t, s, "GET", "/estatico/estilo.css") // 200, no cuenta

	inst := s.contadores.instantanea()
	anotados := int64(len(s.seguridad.Desde(time.Time{})))
	if anotados != inst.ErroresCliente {
		t.Fatalf("el historial tiene %d eventos y el contador dice %d rechazadas",
			anotados, inst.ErroresCliente)
	}
}

// Un 4xx que nadie clasifique se anota igualmente, como «desconocido». Es
// deliberado: un punto ciego VISIBLE se corrige, uno silencioso no.
func TestUnRechazoSinClasificarApareceComoDesconocido(t *testing.T) {
	s := servidorConAuth(t)
	// El servidor de archivos estáticos queda FUERA de exigirSesion (para que
	// el formulario de acceso pueda dibujarse), así que un 404 suyo no pasa
	// por ninguna de las guardas que clasifican.
	if w := pedir(t, s, "GET", "/estatico/no-existe.css"); w.Code != http.StatusNotFound {
		t.Fatalf("estático inexistente -> %d; se esperaba 404", w.Code)
	}
	if got := ultimoEvento(t, s).Motivo; got != seguridad.MotivoDesconocido {
		t.Errorf("motivo = %q; se esperaba «desconocido», que es lo que hace visible el punto ciego", got)
	}
}

// El origen viaja en el evento y se clasifica por red. Es lo que sustituye a
// X-Forwarded-For: sale del socket, así que no se puede falsificar.
func TestElOrigenSeGuardaYSeClasificaPorRed(t *testing.T) {
	s := servidorConAuth(t)

	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.77.0.3:51820" // el iPhone por el túnel
	// Una cabecera de proxy que NO debe creerse: no hay proxy delante.
	r.Header.Set("X-Forwarded-For", "203.0.113.99")
	s.Rutas().ServeHTTP(httptest.NewRecorder(), r)

	e := ultimoEvento(t, s)
	if e.Origen.String() != "10.77.0.3" {
		t.Fatalf("origen = %q; se esperaba la dirección del socket, no la cabecera", e.Origen)
	}
	if e.Red != seguridad.RedTunel {
		t.Errorf("red = %q; se esperaba el túnel", e.Red)
	}
}

// --- El panel /seguridad, etapa 2 -------------------------------------------

// verSeguridad como superusuario, con los parámetros de filtro ya puestos.
func panelSeguridad(t *testing.T, s *Servidor, consulta string) string {
	t.Helper()
	cookie, _ := sesionAbierta(t, s)
	r := httptest.NewRequest(http.MethodGet, "/seguridad"+consulta, nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	s.Rutas().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /seguridad%s -> %d; se esperaba 200", consulta, w.Code)
	}
	return w.Body.String()
}

// El panel enseña el rechazo con TODO lo que se supo de él, no solo una cifra
// — que es el defecto del contador único que esto viene a sustituir.
func TestElPanelEnsenaElDetalleYNoSoloUnaCifra(t *testing.T) {
	s := servidorConAuth(t)
	r := httptest.NewRequest("GET", "/wp-login.php", nil)
	r.RemoteAddr = "203.0.113.7:44001"
	r.Header.Set("User-Agent", "zgrab/0.x")
	s.Rutas().ServeHTTP(httptest.NewRecorder(), r)

	cuerpo := panelSeguridad(t, s, "")
	for _, quiero := range []string{
		"203.0.113.7",      // la dirección de origen
		"/wp-login.php",    // la ruta pedida
		"zgrab/0.x",        // el cliente
		"Ruta inexistente", // el motivo, con palabras
		"Internet",         // la procedencia
	} {
		if !strings.Contains(cuerpo, quiero) {
			t.Errorf("el panel no muestra %q", quiero)
		}
	}
}

// LA PRUEBA QUE HACE HONESTO EL PANEL: una señal nunca aparece sola, siempre
// con aquello con lo que se puede confundir. Sin esta mitad, una sospecha
// derivada se lee como un veredicto — y el responsable pidió expresamente lo
// contrario.
func TestUnaSenalNuncaAparecSinSuFalsoPositivo(t *testing.T) {
	s := servidorConAuth(t)
	// Un escáner de verdad: rutas distintas que no existen, desde fuera.
	for i := range 12 {
		r := httptest.NewRequest("GET", "/inventada-"+strconv.Itoa(i), nil)
		r.RemoteAddr = "203.0.113.7:44001"
		s.Rutas().ServeHTTP(httptest.NewRecorder(), r)
	}

	cuerpo := panelSeguridad(t, s, "")
	if !strings.Contains(cuerpo, seguridad.SenalExploracion.Etiqueta()) {
		t.Fatal("el escáner no levantó la señal en el panel")
	}
	// Y su explicación, que incluye con qué se confunde.
	if !strings.Contains(cuerpo, "También lo produce") {
		t.Error("la señal se muestra sin decir con qué se puede confundir")
	}
	// Y la página empieza diciendo qué NO es esto. El aviso va primero a
	// propósito: sin él, un lector razonable daría por hecho que una tabla
	// titulada «Seguridad» lista ataques.
	if !strings.Contains(cuerpo, "no ataques") {
		t.Error("falta el aviso de que esto registra rechazos y no ataques")
	}
}

// Los filtros viajan por la URL —igual que el orden del listado (P-3)— para
// que un filtro concreto se pueda guardar en marcadores, y para que la página
// no guarde estado de nadie (regla R1 de ADR-0015).
func TestLosFiltrosDelPanelAcotanDeVerdad(t *testing.T) {
	s := servidorConAuth(t)
	desdeFuera := httptest.NewRequest("GET", "/sondeo-de-fuera", nil)
	desdeFuera.RemoteAddr = "203.0.113.7:44001"
	s.Rutas().ServeHTTP(httptest.NewRecorder(), desdeFuera)

	delTunel := httptest.NewRequest("GET", "/", nil)
	delTunel.RemoteAddr = "10.77.0.3:51820"
	s.Rutas().ServeHTTP(httptest.NewRecorder(), delTunel)

	// Sin filtro salen los dos.
	todo := panelSeguridad(t, s, "")
	if !strings.Contains(todo, "203.0.113.7") || !strings.Contains(todo, "10.77.0.3") {
		t.Fatal("sin filtro deberían salir los dos orígenes")
	}

	// Filtrando por Internet, el del túnel desaparece.
	soloFuera := panelSeguridad(t, s, "?red=internet")
	if !strings.Contains(soloFuera, "203.0.113.7") {
		t.Error("el filtro por Internet perdió el origen externo")
	}
	if strings.Contains(soloFuera, "10.77.0.3") {
		t.Error("el filtro por Internet dejó pasar un origen del túnel")
	}

	// Y por ruta.
	porRuta := panelSeguridad(t, s, "?ruta=sondeo")
	if strings.Contains(porRuta, "10.77.0.3") {
		t.Error("el filtro por ruta dejó pasar lo que no la contiene")
	}
}

// Un filtro con basura no puede reventar la página ni colar HTML: lo que
// llega por la URL lo escribe quien pide, y vuelve escrito en el formulario.
func TestUnFiltroConBasuraNoRompeNiInyecta(t *testing.T) {
	s := servidorConAuth(t)
	cuerpo := panelSeguridad(t, s,
		"?horas=no-es-un-numero&motivo=inventado&gravedad=x&red=y&ip=%3Cscript%3Ealert(1)%3C/script%3E")
	if strings.Contains(cuerpo, "<script>alert(1)</script>") {
		t.Fatal("lo tecleado en el filtro se devolvió sin escapar")
	}
	// Y con parámetros ilegibles se cae a la ventana por omisión en vez de
	// vaciar la página o fallar.
	if !strings.Contains(cuerpo, "Resumen") {
		t.Error("con parámetros inválidos la página no se renderizó entera")
	}
}

// El panel no puede dejar creer que el anillo es toda la historia.
func TestElPanelDiceQueEstaRecortando(t *testing.T) {
	s := servidorConAuth(t)
	// Se llena el anillo por debajo, sin pasar por HTTP: 2000 peticiones
	// reales tardarían demasiado para lo que aquí se comprueba.
	base := time.Now()
	for i := range seguridad.Capacidad + 10 {
		s.seguridad.Anotar(seguridad.Evento{
			Momento: base.Add(time.Duration(i) * time.Millisecond),
			Origen:  netip.MustParseAddr("203.0.113.7"),
			Metodo:  "GET", Ruta: "/", Estado: 401, Motivo: seguridad.SinSesion,
		})
	}
	cuerpo := panelSeguridad(t, s, "")
	if !strings.Contains(cuerpo, "más recientes de") {
		t.Error("con el anillo lleno, el panel no avisa de que está recortando")
	}
}

// EL DEFECTO DEL FAVICON, visto en la primera captura del panel en producción
// y convertido en prueba.
//
// /favicon.ico lo pide TODO navegador, siempre. Sin sesión lo clasificaba
// clasificarNegativa como «ruta inexistente»; con sesión llegaba al mux, que
// responde 404 por su cuenta sin pasar por fallo(), y nadie lo marcaba: salía
// como «Motivo desconocido». La MISMA petición, dos clasificaciones distintas
// según si habías entrado — y una de ellas sin nombre, a perpetuidad, porque
// el navegador nunca deja de pedirlo.
func TestLaMismaRutaInexistenteSeClasificaIgualConSesionYSinElla(t *testing.T) {
	s := servidorConAuth(t)

	// Sin sesión.
	if w := pedir(t, s, "GET", "/favicon.ico"); w.Code != http.StatusUnauthorized {
		t.Fatalf("sin sesión -> %d; se esperaba 401", w.Code)
	}
	sinSesion := ultimoEvento(t, s).Motivo

	// Con sesión.
	cookie, _ := sesionAbierta(t, s)
	if w := pedir(t, s, "GET", "/favicon.ico", cookie); w.Code != http.StatusNotFound {
		t.Fatalf("con sesión -> %d; se esperaba 404", w.Code)
	}
	conSesion := ultimoEvento(t, s).Motivo

	if sinSesion != conSesion {
		t.Fatalf("la misma ruta se clasificó como %q sin sesión y %q con ella",
			sinSesion.Etiqueta(), conSesion.Etiqueta())
	}
	if conSesion != seguridad.RutaInexistente {
		t.Fatalf("motivo = %q; se esperaba «ruta inexistente»", conSesion.Etiqueta())
	}
}
