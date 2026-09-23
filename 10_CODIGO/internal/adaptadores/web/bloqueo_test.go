package web

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nasd/internal/geoip"
	"nasd/internal/seguridad"
)

// EL BLOQUEO MANUAL
//
// Los datos de la base son los REALES que el responsable midió el 19/08
// (IDEAS §28.2): AS44382 no anuncia un bloque sino dos, separados por tramos
// que no son suyos. Con datos inventados esta batería no diría nada del
// problema que existe.
const v6DePrueba = "::\t::1\t0\tNone\tNot routed\n" +
	"2602:fa5d::\t2602:fa5d:9:ffff:ffff:ffff:ffff:ffff\t44382\tUS\tWHITELABEL\n" +
	"2602:fa5d:a::\t2602:fa5d:f:ffff:ffff:ffff:ffff:ffff\t0\tNone\tNot routed\n" +
	"2602:fa5d:10::\t2602:fa5d:10:ffff:ffff:ffff:ffff:ffff\t44382\tUS\tWHITELABEL\n"

// conGeo le pone al servidor de prueba una base de operadores de verdad.
func conGeo(t *testing.T, s *Servidor) *Servidor {
	t.Helper()
	ruta := filepath.Join(t.TempDir(), "geoip")
	if _, err := geoip.Preparar(strings.NewReader(""), strings.NewReader(v6DePrueba), ruta); err != nil {
		t.Fatalf("geoip.Preparar: %v", err)
	}
	b, err := geoip.Abrir(ruta)
	if err != nil {
		t.Fatalf("geoip.Abrir: %v", err)
	}
	t.Cleanup(func() { b.Cerrar() })
	s.geo = b
	return s
}

func pantallaDeBloqueo(t *testing.T, s *Servidor, ip string) string {
	t.Helper()
	cookie, _ := sesionAbierta(t, s)
	r := desdeCasa(httptest.NewRequest(http.MethodGet, "/seguridad/bloquear?ip="+ip, nil))
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	s.Rutas().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /seguridad/bloquear -> %d", w.Code)
	}
	return w.Body.String()
}

// LA PANTALLA ENSEÑA LOS TRES ALCANCES, Y EL NÚMERO QUE DESTAPA AL INSUFICIENTE.
//
// «11 tramos» frente a «1» es lo único que distingue un bloqueo que funciona de
// uno que lo parece. Aquí son 2 y 1, con la base recortada, pero es el mismo
// hecho: el operador abarca más de un tramo.
func TestLaPantallaDeBloqueoEnsenaLosTresAlcances(t *testing.T) {
	s := conGeo(t, servidorConAuth(t))
	cuerpo := pantallaDeBloqueo(t, s, "2602:fa5d::8b")

	for _, quiero := range []string{
		"Dirección", "Rango", "Operador",
		"2602:fa5d::8b", // el alcance más estrecho
		"2602:fa5d:: – 2602:fa5d:9:ffff:ffff:ffff:ffff:ffff", // el tramo anunciado
		"AS44382 · WHITELABEL · US",                          // el operador entero
	} {
		if !strings.Contains(cuerpo, quiero) {
			t.Errorf("la pantalla de bloqueo no enseña %q", quiero)
		}
	}
	// Y dice lo que NO puede saber, que es la mitad honesta de la pantalla.
	if !strings.Contains(cuerpo, "no puede distinguir cuáles de esas conexiones eran suyas") {
		t.Error("la pantalla no advierte de lo que el nodo no puede saber")
	}
}

// SIN BASE DE OPERADORES no se inventa un alcance: se dice por qué no se puede.
func TestSinBaseSoloSePuedeBloquearLaDireccion(t *testing.T) {
	s := servidorConAuth(t) // sin geo
	cuerpo := pantallaDeBloqueo(t, s, "2602:fa5d::8b")

	if !strings.Contains(cuerpo, "2602:fa5d::8b") {
		t.Error("sin base debería seguir pudiéndose bloquear la dirección suelta")
	}
	if !strings.Contains(cuerpo, "base de operadores") {
		t.Errorf("no explica por qué no hay rango ni operador:\n%s", cuerpo)
	}
}

// LA SIMULACIÓN SALE DEL HISTORIAL REAL, no de un número inventado.
func TestLaPantallaCuentaLoQueEsaReglaHabriaFrenado(t *testing.T) {
	s := conGeo(t, servidorConAuth(t))

	// Tres rechazos desde DOS direcciones del mismo operador: la regla de
	// «dirección» solo habría frenado los de una; la de «operador», los tres.
	for _, ip := range []string{"2602:fa5d::8b", "2602:fa5d::8b", "2602:fa5d:10::1"} {
		r := httptest.NewRequest(http.MethodGet, "/wp-login.php", nil)
		r.RemoteAddr = "[" + ip + "]:44001"
		s.Rutas().ServeHTTP(httptest.NewRecorder(), r)
	}

	cuerpo := pantallaDeBloqueo(t, s, "2602:fa5d::8b")
	// La fila del operador tiene que contar más que la de la dirección. Se
	// comprueba por el contenido de las celdas y no por su posición: lo que
	// importa es que las dos cifras existan y sean distintas.
	if !strings.Contains(cuerpo, ">2<") || !strings.Contains(cuerpo, ">3<") {
		t.Errorf("la simulación no distingue el alcance estrecho del ancho:\n%s", cuerpo)
	}
}

func bloquearCon(t *testing.T, s *Servidor, campos url.Values) *httptest.ResponseRecorder {
	t.Helper()
	cookie, csrf := sesionAbierta(t, s)
	campos.Set("csrf", csrf)
	r := desdeCasa(httptest.NewRequest(http.MethodPost, "/seguridad/bloquear",
		strings.NewReader(campos.Encode())))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	s.Rutas().ServeHTTP(w, r)
	return w
}

// EL CAMINO COMPLETO: se bloquea el operador entero y la puerta se cierra para
// una dirección de su SEGUNDO tramo — la que un /48 habría dejado pasar.
func TestBloquearElOperadorCubreTodosSusTramos(t *testing.T) {
	s := conGeo(t, servidorConAuth(t))

	w := bloquearCon(t, s, url.Values{
		"ip":      {"2602:fa5d::8b"},
		"alcance": {"operador"},
		"motivo":  {"reputación del ASN; no conducta observada aquí"},
		"dias":    {"30"},
	})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("bloquear -> %d; se esperaba 303. Cuerpo: %s", w.Code, w.Body.String())
	}

	v := s.lista.Vigentes(time.Now())
	if len(v) != 1 {
		t.Fatalf("entradas en la lista = %d; se esperaba 1", len(v))
	}
	if len(v[0].Tramos) != 2 {
		t.Errorf("la entrada cubre %d tramos; el operador tiene 2", len(v[0].Tramos))
	}
	if v[0].Motivo == "" || v[0].Autor == "" {
		t.Error("la entrada se guardó sin motivo o sin autor")
	}
	if v[0].Caduca.IsZero() {
		t.Error("se pidió que caducara en 30 días y quedó permanente")
	}

	// La comprobación que importa: la dirección del SEGUNDO tramo, la que el
	// /48 de la ficha de partida habría dejado pasar.
	if _, ok := s.lista.Cubre(netip.MustParseAddr("2602:fa5d:10::1"), time.Now()); !ok {
		t.Error("el segundo tramo del operador no quedó cubierto")
	}
}

// SIN MOTIVO NO SE BLOQUEA, y la pantalla lo dice en vez de tragárselo.
func TestBloquearSinMotivoVuelveConElAviso(t *testing.T) {
	s := conGeo(t, servidorConAuth(t))

	w := bloquearCon(t, s, url.Values{
		"ip":      {"2602:fa5d::8b"},
		"alcance": {"direccion"},
		"motivo":  {"   "},
	})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("-> %d; se esperaba volver a la pantalla", w.Code)
	}
	destino := w.Header().Get("Location")
	if !strings.Contains(destino, "aviso=") {
		t.Errorf("vuelve sin decir por qué: %q", destino)
	}
	if len(s.lista.Vigentes(time.Now())) != 0 {
		t.Error("guardó un bloqueo sin motivo")
	}
}

// LA BARANDILLA, COMPROBADA POR LA VÍA DE VERDAD: por el formulario, no
// llamando a la lista a mano.
func TestElPanelSeNiegaABloquearLaRedDesdeLaQueSeMira(t *testing.T) {
	s := conGeo(t, servidorConAuth(t))
	cookie, csrf := sesionAbierta(t, s)

	// Se pide desde una dirección del propio operador que se quiere bloquear.
	r := httptest.NewRequest(http.MethodPost, "/seguridad/bloquear",
		strings.NewReader(url.Values{
			"csrf": {csrf}, "ip": {"2602:fa5d::8b"},
			"alcance": {"operador"}, "motivo": {"da igual"},
		}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.RemoteAddr = "[2602:fa5d:1::99]:44001"
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	s.Rutas().ServeHTTP(w, r)

	if len(s.lista.Vigentes(time.Now())) != 0 {
		t.Fatal("se dejó bloquear la red desde la que se estaba mirando")
	}
	if !strings.Contains(w.Header().Get("Location"), "aviso=") {
		t.Error("se rechazó sin explicar por qué")
	}
}

// El panel enseña lo puesto, con su motivo, y deja retirarlo.
func TestElPanelEnsenaLosBloqueosYDejaRetirarlos(t *testing.T) {
	s := conGeo(t, servidorConAuth(t))
	if w := bloquearCon(t, s, url.Values{
		"ip": {"2602:fa5d::8b"}, "alcance": {"rango"},
		"motivo": {"barrido medido el 16/08"}, "dias": {"30"},
	}); w.Code != http.StatusSeeOther {
		t.Fatalf("bloquear -> %d", w.Code)
	}

	cuerpo := panelSeguridad(t, s, "")
	for _, quiero := range []string{"Bloqueos puestos", "barrido medido el 16/08", "Retirar"} {
		if !strings.Contains(cuerpo, quiero) {
			t.Errorf("el panel no enseña %q", quiero)
		}
	}

	id := s.lista.Vigentes(time.Now())[0].ID
	cookie, csrf := sesionAbierta(t, s)
	r := desdeCasa(httptest.NewRequest(http.MethodPost, "/seguridad/retirar",
		strings.NewReader(url.Values{"csrf": {csrf}, "id": {id}}.Encode())))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	s.Rutas().ServeHTTP(w, r)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("retirar -> %d", w.Code)
	}
	if len(s.lista.Vigentes(time.Now())) != 0 {
		t.Error("el panel dijo que lo retiró y sigue puesto")
	}
}

// A la casa no se le ofrece el botón. La barandilla del servidor ya lo
// impediría; esto evita ofrecer algo que se va a rechazar.
func TestElPanelNoOfreceBloquearLoQueVieneDeCasa(t *testing.T) {
	s := servidorConAuth(t)

	for _, ip := range []string{"192.168.1.18", "203.0.113.7"} {
		r := httptest.NewRequest(http.MethodGet, "/inventada", nil)
		r.RemoteAddr = ip + ":5000"
		if strings.Contains(ip, ":") {
			r.RemoteAddr = "[" + ip + "]:5000"
		}
		s.Rutas().ServeHTTP(httptest.NewRecorder(), r)
	}

	cuerpo := panelSeguridad(t, s, "?red=")
	if !seOfreceBloquear(cuerpo, "203.0.113.7") {
		t.Error("no se ofrece bloquear a un origen de Internet")
	}
	if seOfreceBloquear(cuerpo, "192.168.1.18") {
		t.Error("se ofrece bloquear a un equipo de casa")
	}
}

// seOfreceBloquear dice si el panel ofrece la acción de bloqueo para esa
// dirección.
//
// EXISTE PARA NO ATARSE A LA FORMA DEL CONTROL. Esto se comprobaba buscando
// «/seguridad/bloquear?ip=X» en el HTML, y el 24/08/2026 la acción pasó de
// enlace a formulario GET —mismo método, mismo destino, botón nativo en vez de
// enlace azul entre botones—. La prueba se puso roja sin que cambiara nada de
// lo que afirma: quién puede bloquearse y quién no. Se pregunta por el par
// destino + dirección, que es lo único que esa afirmación necesita.
func seOfreceBloquear(cuerpo, ip string) bool {
	const destino = `action="/seguridad/bloquear"`
	for resto := cuerpo; ; {
		_, tras, hay := strings.Cut(resto, destino)
		if !hay {
			return false
		}
		formulario, _, _ := strings.Cut(tras, "</form>")
		if strings.Contains(formulario, `value="`+ip+`"`) {
			return true
		}
		resto = tras
	}
}

// Un bloqueo puesto cierra la puerta de verdad, igual que la cuarentena, y por
// el mismo sitio.
func TestUnBloqueoManualCierraLaPuerta(t *testing.T) {
	s := conGeo(t, servidorConAuth(t))
	if _, err := s.lista.Anadir(seguridad.Entrada{
		Alcance:  seguridad.AlcanceRango,
		Etiqueta: "prueba",
		Tramos:   []seguridad.Tramo{seguridad.TramoDe(netip.MustParsePrefix("2602:fa5d::/44"))},
		Motivo:   "prueba",
		Autor:    "raiz",
	}, netip.Addr{}, time.Now()); err != nil {
		t.Fatalf("Anadir: %v", err)
	}

	ahora := time.Now()
	id, ok := s.lista.Cubre(netip.MustParseAddr("2602:fa5d:5::dead"), ahora)
	if !ok {
		t.Fatal("la lista no bloquea a quien cae dentro")
	}
	// Y COMPROBAR NO ES FRENAR: hasta aquí el contador tiene que seguir a cero.
	// Antes no podía: la misma llamada que decidía era la que sumaba.
	if n := s.lista.Vigentes(ahora)[0].Frenados; n != 0 {
		t.Fatalf("consultar la política ya contó %d frenados", n)
	}
	// El contador sube al CERRAR, que es lo que permite retirarla algún día
	// con un dato en vez de con una corazonada.
	s.lista.AnotarCierre(id, ahora)
	if s.lista.Vigentes(ahora)[0].Frenados != 1 {
		t.Error("se cerró una conexión y no se contó")
	}
}
