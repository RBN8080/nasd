package web

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"nasd/internal/autenticacion"
	"nasd/internal/geoip"
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

	// SIN PARÁMETROS EL PANEL ABRE EN INTERNET, no en «todo». Encargo del
	// responsable del 2026-08-16; esta prueba afirmaba lo contrario y es la
	// que destapó el cambio de contrato, que es justo para lo que está.
	porOmision := panelSeguridad(t, s, "")
	if !strings.Contains(porOmision, "203.0.113.7") {
		t.Error("el panel por omisión perdió el origen de Internet")
	}
	if strings.Contains(porOmision, "10.77.0.3") {
		t.Error("el panel por omisión enseñó el túnel; debe abrir solo con Internet")
	}

	// «Cualquier origen» —red= vacío, que es lo que manda el <select>— sí
	// enseña los dos. Es la mitad que hace que la LAN y el túnel queden en
	// segundo lugar y no fuera.
	todo := panelSeguridad(t, s, "?red=")
	if !strings.Contains(todo, "203.0.113.7") || !strings.Contains(todo, "10.77.0.3") {
		t.Fatal("con «cualquier origen» deberían salir los dos")
	}

	// Y pedir Internet explícitamente da lo mismo que no pedir nada.
	soloFuera := panelSeguridad(t, s, "?red=internet")
	if !strings.Contains(soloFuera, "203.0.113.7") {
		t.Error("el filtro por Internet perdió el origen externo")
	}
	if strings.Contains(soloFuera, "10.77.0.3") {
		t.Error("el filtro por Internet dejó pasar un origen del túnel")
	}

	// EL FILTRO POR RUTA SE PROBABA AQUÍ Y YA NO EXISTE (ADR-0065): el
	// responsable declaró que ni esa caja ni la de dirección le servían.
	// Quedan los filtros que sí se usan, y son los que esta prueba defiende.
}

// Un filtro con basura no puede reventar la página ni colar HTML: lo que
// llega por la URL lo escribe quien pide, y vuelve escrito en el formulario.
func TestUnFiltroConBasuraNoRompeNiInyecta(t *testing.T) {
	s := servidorConAuth(t)
	// «gravedad» e «ip» YA NO SON PARÁMETROS DE ESTA PÁGINA (ADR-0065), y se
	// dejan en la URL a propósito: la prueba pasa a defender algo más fuerte
	// que antes —que un parámetro retirado no se cuela por ningún resquicio—.
	// Desde la retirada de las dos cajas de texto, la página no devuelve NADA
	// de lo que llega por la URL: el rótulo del filtro activo se compone de las
	// etiquetas propias del programa, no de lo que teclee quien pide.
	cuerpo := panelSeguridad(t, s,
		"?horas=no-es-un-numero&motivo=inventado&gravedad=x&red=y&ip=%3Cscript%3Ealert(1)%3C/script%3E")
	if strings.Contains(cuerpo, "<script>alert(1)</script>") {
		t.Fatal("un parámetro retirado se devolvió sin escapar")
	}
	if strings.Contains(cuerpo, "alert(1)") {
		t.Fatal("un parámetro retirado se sigue devolviendo a la página")
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
	// SE COMPRUEBAN LAS DOS CIFRAS Y NO UNA FRASE, y es la corrección que trajo
	// ADR-0065: el texto anterior decía «los N más recientes de M» con N = los
	// eventos DE LA VENTANA y no los que caben en el anillo, así que con
	// «?horas=1» llegaba a afirmar «se conservan los 3 más recientes de 5000».
	// Lo que la prueba defiende es que la cifra publicada sea la real.
	if !strings.Contains(cuerpo, strconv.Itoa(seguridad.Capacidad)) {
		t.Error("con el anillo lleno, el panel no dice cuántos rechazos conserva")
	}
	if !strings.Contains(cuerpo, strconv.Itoa(seguridad.Capacidad+10)) {
		t.Error("con el anillo lleno, el panel no dice cuántos se han visto en total")
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

// --- Etapa 4: país y operador ------------------------------------------------

// El panel funciona SIN base instalada. Es la razón de que GeoIP sea la única
// dependencia opcional de web.Opciones: un nodo al que no se le ha ejecutado
// 18_geoip.sh todavía no debe quedarse sin panel de seguridad.
func TestElPanelFuncionaSinBaseDePaisYOperador(t *testing.T) {
	s := servidorConAuth(t) // se construye sin GeoIP
	if s.geo != nil {
		t.Fatal("este servidor de prueba no debería tener base")
	}
	pedir(t, s, "GET", "/wp-login.php")

	cuerpo := panelSeguridad(t, s, "")
	// Sin base NO se pinta la columna: una columna vacía se leería como «no
	// se sabe de nadie» cuando en realidad es «no se ha instalado la base».
	if strings.Contains(cuerpo, "<th>Operador</th>") {
		t.Error("sin base instalada no debe aparecer la columna de operador")
	}
	if !strings.Contains(cuerpo, "Orígenes") {
		t.Error("el panel no se renderizó entero sin la base")
	}
}

// Y con base, el origen de Internet sale resuelto — mientras que los de casa
// NO se resuelven a propósito: una IP privada no tiene operador, y enseñar
// «el operador» junto a los aparatos del responsable sería ruido en la única tabla
// que existe para mirar hacia fuera.
func TestSoloLosOrigenesDeInternetSeResuelven(t *testing.T) {
	s := servidorConAuth(t)
	s.geo = baseGeoDePrueba(t)
	t.Cleanup(func() { s.geo.Cerrar() })

	deFuera := httptest.NewRequest("GET", "/wp-login.php", nil)
	deFuera.RemoteAddr = "8.8.8.8:44001"
	s.Rutas().ServeHTTP(httptest.NewRecorder(), deFuera)

	deCasa := httptest.NewRequest("GET", "/loquesea", nil)
	deCasa.RemoteAddr = "192.168.1.18:5000"
	s.Rutas().ServeHTTP(httptest.NewRecorder(), deCasa)

	cuerpo := panelSeguridad(t, s, "")
	if !strings.Contains(cuerpo, "<th>Operador</th>") {
		t.Fatal("con base instalada debe aparecer la columna de operador")
	}
	if !strings.Contains(cuerpo, "GOOGLE") {
		t.Error("el origen de Internet no se resolvió a su operador")
	}
	if !strings.Contains(cuerpo, "AS15169") {
		t.Error("falta el número de AS, que es el identificador estable")
	}
}

// baseGeoDePrueba arma una base mínima con los mismos fragmentos reales que
// usa el paquete geoip, para no inventar datos.
func baseGeoDePrueba(t *testing.T) *geoip.BaseDatos {
	t.Helper()
	ruta := filepath.Join(t.TempDir(), "geoip")
	v4 := "8.8.8.0\t8.8.8.255\t15169\tUS\tGOOGLE\n" +
		"198.51.100.0\t198.51.100.255\t64496\tMX\tOperador Domestico, S.A. de C.V.\n"
	if _, err := geoip.Preparar(strings.NewReader(v4), strings.NewReader(""), ruta); err != nil {
		t.Fatalf("Preparar: %v", err)
	}
	b, err := geoip.Abrir(ruta)
	if err != nil {
		t.Fatalf("Abrir: %v", err)
	}
	return b
}

// EL PANEL SE RENDERIZA ENTERO, y esta prueba existe por un defecto propio
// que costó un rato encontrar: un comentario HTML dentro de seguridad.html
// citaba la función «fecha» entre llaves dobles para explicar un criterio.
// Go analiza las acciones ANTES de tratar el HTML, así que aquello no era
// texto: era una llamada con cero argumentos.
//
// El síntoma es el peligroso: ExecuteTemplate falla A MITAD de escribir, la
// cabecera 200 ya se envió, y el panel devuelve una página cortada con código
// de éxito. Comprobar «responde 200» no detecta nada.
//
// Se ancla a la ÚLTIMA línea de la plantilla, siguiendo la lección que la
// v1.39.0 dejó escrita al descubrir el mismo tipo de agujero en /estado: un
// ancla de «se renderizó entero» no puede depender de una palabra que se
// cambia por gusto estético.
func TestElPanelDeSeguridadSeRenderizaEntero(t *testing.T) {
	s := servidorConAuth(t)
	s.geo = baseGeoDePrueba(t) // con base: ejercita las ramas de la columna
	t.Cleanup(func() { s.geo.Cerrar() })

	deFuera := httptest.NewRequest("GET", "/wp-login.php", nil)
	deFuera.RemoteAddr = "8.8.8.8:44001"
	s.Rutas().ServeHTTP(httptest.NewRecorder(), deFuera)

	cuerpo := panelSeguridad(t, s, "")
	if !strings.HasSuffix(strings.TrimSpace(cuerpo), "</html>") {
		t.Fatalf("la página no llega a su última línea: se cortó el render.\nCola: %q",
			cuerpo[max(0, len(cuerpo)-300):])
	}
	// Y las cuatro secciones, para que «entero» signifique algo más que que
	// la etiqueta de cierre llegó.
	for _, seccion := range []string{"Resumen", "Filtros", "Orígenes", "Cronología"} {
		if !strings.Contains(cuerpo, "<h2>"+seccion+"</h2>") {
			t.Errorf("falta la sección %q", seccion)
		}
	}
}

// El rótulo del filtro activo SUSTITUYE a la prosa que explicaba que la página
// abre filtrada a Internet (ADR-0065), así que tiene que decir la verdad y no
// puede quedarse en blanco: si se vaciara, esa información se perdería sin que
// nada fallara a gritos, que es el modo de fallo que este proyecto persigue.
//
// Se comprueba contra el TEXTO COMPUESTO y no contra los campos, porque el
// defecto que importa es el de la pantalla: un rótulo que dice «Internet»
// mientras el desplegable enseña otra cosa.
func TestElRotuloDelFiltroDiceLoQueSeEstaMirando(t *testing.T) {
	s := servidorConAuth(t)

	if cuerpo := panelSeguridad(t, s, ""); !strings.Contains(cuerpo, "Internet · 24 horas") {
		t.Error("el rótulo no anuncia el filtro por omisión")
	}
	if cuerpo := panelSeguridad(t, s, "?red=&horas=1"); !strings.Contains(cuerpo, "Cualquier origen · 1 hora") {
		t.Error("el rótulo no siguió al desplegable")
	}
	// El motivo aparece SOLO cuando se ha elegido uno: «Todos los motivos» es
	// la ausencia de filtro, y anunciarla en cada carga sería el mismo ruido
	// que este trabajo viene a quitar.
	conMotivo := panelSeguridad(t, s, "?motivo=credencial_incorrecta")
	if !strings.Contains(conMotivo, "Internet · 24 horas · Credencial incorrecta") {
		t.Error("el rótulo no anuncia el motivo elegido")
	}
	if strings.Contains(panelSeguridad(t, s, ""), "Todos los motivos ·") {
		t.Error("el rótulo anuncia la ausencia de filtro como si fuera un filtro")
	}
}

// La columna «Procedencia» solo se enseña cuando puede decir algo distinto en
// cada fila. Con el filtro por omisión decía «Internet» en TODAS, que es una
// columna constante: ocupa ancho y no informa (ADR-0065).
//
// No se retiró del todo porque en cuanto se mezclan redes vuelve a ser la que
// distingue el móvil de casa de un extraño.
func TestLaProcedenciaSoloApareceCuandoDistingue(t *testing.T) {
	s := servidorConAuth(t)

	deFuera := httptest.NewRequest("GET", "/sondeo", nil)
	deFuera.RemoteAddr = "203.0.113.7:44001"
	s.Rutas().ServeHTTP(httptest.NewRecorder(), deFuera)

	if strings.Contains(panelSeguridad(t, s, ""), "<th>Procedencia</th>") {
		t.Error("con el filtro por omisión la procedencia es constante y no debe enseñarse")
	}
	if !strings.Contains(panelSeguridad(t, s, "?red="), "<th>Procedencia</th>") {
		t.Error("con «cualquier origen» la procedencia distingue y tiene que estar")
	}
}

// «Sondeo de software que aquí no existe» INFORMA PERO NO ALARMA.
//
// Dispara con UNA sola ruta ajena y su propia explicación termina diciendo que
// no va dirigida a este nodo: una alerta que se desmiente sola gasta la
// credibilidad de las otras dos. Sigue apareciendo —RF-29(7) exige que la
// inferencia esté con su explicación— y pierde el énfasis (ADR-0065).
func TestLaSenalDeSoftwareAjenoInformaPeroNoAlarma(t *testing.T) {
	s := servidorConAuth(t)

	deFuera := httptest.NewRequest("GET", "/wp-login.php", nil)
	deFuera.RemoteAddr = "203.0.113.7:44001"
	s.Rutas().ServeHTTP(httptest.NewRecorder(), deFuera)

	cuerpo := panelSeguridad(t, s, "")
	if !strings.Contains(cuerpo, seguridad.SenalSoftwareAjeno.Etiqueta()) {
		t.Fatal("la señal desapareció; solo tenía que perder el énfasis")
	}
	if strings.Contains(cuerpo, `class="senal senal-aviso"`) {
		t.Error("la señal de software ajeno se sigue pintando como alerta")
	}
	// Y la mitad que impide que esta prueba se cumpla sola: las otras dos SÍ
	// alarman. Sin esto, borrar la clase del CSS entero pasaría la prueba.
	if !seguridad.SenalExploracion.Destacar() || !seguridad.SenalFuerzaBruta.Destacar() {
		t.Error("las señales que sí alarman perdieron su énfasis")
	}
}

// «Primera» y «Última» eran dos columnas y son un intervalo: se fundieron en
// una (ADR-0065). Y cuando los dos extremos caen en el mismo minuto se escribe
// UNO SOLO.
//
// No es cosmético, y por eso tiene prueba: un sondeo entero cabe en un minuto
// —los veinte de DRIFTNET tardaron cinco—, así que el caso CORRIENTE es el de
// los dos extremos iguales, y pintar la misma fecha dos veces con una flecha
// en medio es repetir un dato para no decir nada.
func TestElIntervaloNoRepiteLaMismaFechaDosVeces(t *testing.T) {
	s := servidorConAuth(t)
	base := time.Date(2026, 8, 16, 11, 49, 0, 0, time.Local)
	anota := func(cuando time.Time) {
		s.seguridad.Anotar(seguridad.Evento{
			Momento: cuando,
			Origen:  netip.MustParseAddr("203.0.113.7"),
			Metodo:  "GET", Ruta: "/", Estado: 401, Motivo: seguridad.SinSesion,
		})
	}

	// «horas=0» es «todo lo guardado». Va A PROPÓSITO y no la ventana por
	// omisión: con una fecha fija de hace días, las 24 horas dejarían la tabla
	// VACÍA y la primera comprobación se cumpliría sin haber probado nada —
	// que es justo lo que pasó al escribir esta prueba.
	anota(base)
	anota(base.Add(30 * time.Second)) // el mismo minuto
	primera := panelSeguridad(t, s, "?horas=0")
	if !strings.Contains(primera, "2026-08-16 11:49") {
		t.Fatal("la fila no llegó a la tabla; la comprobación de abajo no probaría nada")
	}
	if strings.Contains(primera, "→") {
		t.Error("con los dos extremos en el mismo minuto se pintó un intervalo")
	}

	// Y la mitad que impide que la prueba se cumpla sola borrando la flecha:
	// cuando de verdad hay recorrido, se dice.
	anota(base.Add(5 * time.Minute))
	if cuerpo := panelSeguridad(t, s, "?horas=0"); !strings.Contains(cuerpo, "2026-08-16 11:49 → 2026-08-16 11:54") {
		t.Error("con recorrido real el intervalo no se enseña entero")
	}
}

// EL CASO QUE ANTES ERA INVISIBLE, y es el motivo entero de ADR-0066: alguien
// recorre puertos cerrados y se va sin llegar a hablar. El cortafuegos lo tira,
// así que nunca hubo conexión ni petición, y hasta el sensor no lo veía nadie.
//
// Se comprueba en la MISMA fila que las otras dos capas: la escalera
// paquete → conexión → rechazo es lo que hace legible la historia del 16/08.
func TestUnEscaneoAPuertosCerradosApareceAunqueNoHablara(t *testing.T) {
	s := servidorConAuth(t)
	s.rutaToques = filepath.Join(t.TempDir(), "toques")
	ahora := time.Now().UTC().Format(time.RFC3339)
	cuerpo := "# total-visto: 913\n" +
		ahora + " 203.0.113.7 23 syn\n" +
		ahora + " 203.0.113.7 2323 syn\n" +
		ahora + " 192.168.1.23 445 syn\n" // de casa: NO debe salir
	if err := os.WriteFile(s.rutaToques, []byte(cuerpo), 0o644); err != nil {
		t.Fatal(err)
	}

	panel := panelSeguridad(t, s, "")
	if !strings.Contains(panel, "203.0.113.7") {
		t.Fatal("el origen que solo envió paquetes no aparece: sigue siendo invisible")
	}
	if strings.Contains(panel, "192.168.1.23") {
		t.Error("un toque de la LAN se coló en la tabla de Internet")
	}
	if !strings.Contains(panel, "<th>Paquetes</th>") {
		t.Error("con sensor instalado debe existir la columna de paquetes")
	}
	// Los puertos distintos son lo que separa «un cliente reintentando» de
	// «alguien recorriendo el nodo».
	if !strings.Contains(panel, "puertos: 23 2323") {
		t.Error("no se enseñan los puertos distintos que se tocaron")
	}
	// Y el total sobrevive al reinicio porque viaja en el archivo, no en
	// memoria — que es justo lo que los otros dos anillos NO hacen.
	if !strings.Contains(panel, "913") {
		t.Error("no se publica el total de paquetes vistos desde siempre")
	}
}

// Sin sensor instalado la columna NO se pinta a cero: se esconde. Una columna
// de ceros se leería como «nadie me toca» cuando significa «no lo estoy
// mirando» — mismo criterio que la columna de operador sin base de geoip.
func TestSinSensorLaColumnaDePaquetesNoSePinta(t *testing.T) {
	s := servidorConAuth(t)
	s.rutaToques = filepath.Join(t.TempDir(), "no-existe")

	panel := panelSeguridad(t, s, "")
	if strings.Contains(panel, "<th>Paquetes</th>") {
		t.Error("sin sensor no debe haber columna de paquetes")
	}
	if !strings.Contains(panel, "<h2>Orígenes</h2>") {
		t.Error("sin sensor el resto de la página tiene que seguir entera")
	}
}

// Las dos capas de Internet no aplican cuando se miran otras redes, así que sus
// columnas desaparecen en vez de salir vacías. Mismo patrón que «Procedencia».
func TestLasColumnasDeInternetSoloSalenConElFiltroEnInternet(t *testing.T) {
	s := servidorConAuth(t)
	s.rutaToques = filepath.Join(t.TempDir(), "toques")
	ahora := time.Now().UTC().Format(time.RFC3339)
	if err := os.WriteFile(s.rutaToques,
		[]byte("# total-visto: 1\n"+ahora+" 203.0.113.7 23 syn\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if p := panelSeguridad(t, s, ""); !strings.Contains(p, "<th>Conex.</th>") {
		t.Error("con el filtro en Internet deben verse las conexiones")
	}
	todo := panelSeguridad(t, s, "?red=")
	if strings.Contains(todo, "<th>Paquetes</th>") || strings.Contains(todo, "<th>Conex.</th>") {
		t.Error("con «cualquier origen» esas dos capas no aplican y no deben pintarse")
	}
	if !strings.Contains(todo, "<th>Procedencia</th>") {
		t.Error("con «cualquier origen» sí debe verse la procedencia")
	}
}

// Un filtro que deja pasar justo lo que NO cumple es peor que no tenerlo.
//
// Las capas de paquetes y conexiones no saben de motivos —un paquete no tiene
// ninguno—, así que al fundir las tablas apareció este defecto: filtrar por
// «Credencial incorrecta» seguía enseñando cada dirección que solo había
// enviado paquetes, con un 0 en la columna de rechazos.
func TestElFiltroDeMotivoGobiernaLaTablaEntera(t *testing.T) {
	s := servidorConAuth(t)
	s.rutaToques = filepath.Join(t.TempDir(), "toques")
	ahora := time.Now().UTC().Format(time.RFC3339)
	if err := os.WriteFile(s.rutaToques,
		[]byte("# total-visto: 1\n"+ahora+" 203.0.113.7 23 syn\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Sin filtro de motivo, quien solo envió paquetes tiene que verse: es el
	// caso que ADR-0066 vino a hacer visible.
	if p := panelSeguridad(t, s, ""); !strings.Contains(p, "203.0.113.7") {
		t.Fatal("sin filtro de motivo debe verse quien solo envió paquetes")
	}
	// Con un motivo elegido, ya no: esa dirección no produjo ese motivo.
	conMotivo := panelSeguridad(t, s, "?motivo=credencial_incorrecta")
	if strings.Contains(conMotivo, "203.0.113.7") {
		t.Error("el filtro de motivo dejó pasar una dirección sin ningún rechazo")
	}
}

// EL MISMO INSTANTE TIENE QUE PINTARSE CON LA MISMA HORA, venga de la capa que
// venga. Los dos anillos guardan la hora con desfase local —nacen de time.Now()
// dentro de nasd— y el sensor la guarda en UTC, porque lo escribe otro programa.
//
// Sin convertir, la fila del primer origen de Internet real (18/08, 20:23) se
// pintaba «2026-08-19 02:23 → 2026-08-18 20:23»: un intervalo corriendo HACIA
// ATRÁS, con el SYN dos segundos DESPUÉS de la petición que provocó.
//
// No lo cazó ninguna prueba porque hasta entonces ninguna fila había tenido
// datos de las dos procedencias a la vez. Esta la fija.
func TestElIntervaloNoMezclaZonasHorarias(t *testing.T) {
	s := servidorConAuth(t)
	s.rutaToques = filepath.Join(t.TempDir(), "toques")

	// El SYN, en UTC, como lo escribe el sensor.
	ahora := time.Now().UTC()
	if err := os.WriteFile(s.rutaToques,
		[]byte("# total-visto: 1\n"+ahora.Format(time.RFC3339)+" 203.0.113.7 443 syn\n"),
		0o644); err != nil {
		t.Fatal(err)
	}
	// El rechazo, dos segundos después y en hora local, como lo escribe nasd.
	s.seguridad.Anotar(seguridad.Evento{
		Momento: ahora.Add(2 * time.Second).Local(),
		Origen:  netip.MustParseAddr("203.0.113.7"),
		Metodo:  "GET", Ruta: "/", Estado: 401, Motivo: seguridad.SinSesion,
	})

	cuerpo := panelSeguridad(t, s, "")
	local := ahora.Local().Format("2006-01-02 15:04")
	utc := ahora.Format("2006-01-02 15:04")
	if !strings.Contains(cuerpo, local) {
		t.Errorf("la fila no se pinta en hora local (%s)", local)
	}
	if local != utc && strings.Contains(cuerpo, utc) {
		t.Errorf("se coló la hora en UTC (%s): el mismo instante sale con dos horas", utc)
	}
}
