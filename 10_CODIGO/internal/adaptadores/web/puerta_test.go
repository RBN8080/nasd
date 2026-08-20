package web

import (
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"nasd/internal/seguridad"
)

// LA PUERTA — el orden de dos líneas es toda la decisión de este trabajo.
//
// Bloquear en el cortafuegos habría dejado al bloqueado INVISIBLE: los anillos
// viven dentro de este proceso y un paquete que descarta el núcleo no llega
// nunca. Con dos únicos orígenes externos en 19 días, eso era quedarse ciego
// sobre lo único que este nodo ha visto.
//
// Por eso se anota PRIMERO y se cuelga DESPUÉS, y por eso esto se prueba
// contra un servidor de verdad y no llamando al método a mano: lo que hay que
// demostrar es que las dos cosas pasan, y en ese orden.

// apartar mete una dirección en cuarentena por la vía normal —conducta
// observada— y no tocando la estructura por dentro. Si algún día cambia lo que
// hace falta para que a alguien lo aparten, estas pruebas se enteran.
func apartar(t *testing.T, s *Servidor, ip string) netip.Addr {
	t.Helper()
	dir := netip.MustParseAddr(ip)
	var eventos []seguridad.Evento
	for i := range 10 {
		eventos = append(eventos, seguridad.Evento{
			Momento: time.Now(),
			Origen:  dir,
			Red:     seguridad.ClasificarRed(dir),
			Metodo:  "GET",
			Ruta:    "/inventada-" + strconv.Itoa(i),
			Estado:  404,
			Motivo:  seguridad.RutaInexistente,
		})
	}
	nuevos := s.cuarentena.Evaluar(seguridad.PorOrigen(eventos), time.Now())
	if len(nuevos) != 1 {
		t.Fatalf("no se pudo apartar a %s: %d apartados nuevos", ip, len(nuevos))
	}
	return dir
}

func TestAlApartadoSeLeAnotaLaConexionYDespuesSeLeCuelga(t *testing.T) {
	s := servidorConAuth(t)
	dir := apartar(t, s, "203.0.113.7")

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("abrir el listener de prueba: %v", err)
	}
	remoto := &net.TCPAddr{IP: net.ParseIP(dir.String()), Port: 44001}
	srv := s.HTTPServer("127.0.0.1", 0)
	go srv.Serve(escuchaConOrigen{Listener: ln, remoto: remoto})
	t.Cleanup(func() { srv.Close() })

	c, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("conectar: %v", err)
	}
	defer c.Close()

	// Se pide algo perfectamente válido. Da igual: no debe contestar.
	_, _ = c.Write([]byte("GET /acceso HTTP/1.1\r\nHost: nas\r\n\r\n"))
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err := c.Read(make([]byte, 64))
	if err == nil {
		t.Fatalf("el apartado recibió %d bytes de respuesta; se le tenía que colgar", n)
	}
	var vencido net.Error
	if errors.As(err, &vencido) && vencido.Timeout() {
		t.Fatalf("la conexión del apartado siguió abierta hasta agotar el plazo: %v", err)
	}
	if !errors.Is(err, io.EOF) && !strings.Contains(err.Error(), "reset") &&
		!strings.Contains(err.Error(), "forcibly closed") {
		t.Fatalf("se esperaba una conexión cerrada; llegó: %v", err)
	}

	// Y LA OTRA MITAD, QUE ES LA QUE JUSTIFICA HACERLO AQUÍ Y NO EN NFTABLES:
	// se le sigue viendo llegar.
	//
	// Se mira el ANILLO DE CONEXIONES y no el cuerpo del panel, y la
	// diferencia importa: el panel enseñaría igual esta dirección por estar en
	// la tabla de apartados, así que buscarla en el HTML pasaría aunque la
	// conexión no se hubiera anotado nunca. Esta comprobación solo puede estar
	// en verde si Anotar corrió ANTES del cierre.
	esperarA(t, func() bool { return s.conexiones.Total() > 0 },
		"la conexión del apartado se cerró SIN anotarse: se le colgó a ciegas")

	esperarA(t, func() bool {
		v := s.cuarentena.Vigentes(time.Now())
		return len(v) == 1 && v[0].Frenados > 0
	}, "no se contó el frenado: la conexión se cerró sin quedar registrada")

	cuerpo := panelSeguridad(t, s, "")
	if !strings.Contains(cuerpo, dir.String()) {
		t.Errorf("al apartado se le colgó y además desapareció del panel:\n%s", cuerpo)
	}
}

// El panel enseña lo apartado con lo único que permite retirarlo: por qué está
// y cuánto ha frenado desde que está.
func TestElPanelEnsenaLoApartadoYDejaSoltarlo(t *testing.T) {
	s := servidorConAuth(t)
	dir := apartar(t, s, "203.0.113.7")

	cuerpo := panelSeguridad(t, s, "")
	for _, quiero := range []string{
		dir.String(),
		seguridad.SenalExploracion.Etiqueta(),
		"Apartados por conducta",
		`action="/seguridad/soltar"`,
	} {
		if !strings.Contains(cuerpo, quiero) {
			t.Errorf("el panel no enseña %q", quiero)
		}
	}

	// Y soltarlo funciona de verdad, no solo en la pantalla.
	cookie, csrf := sesionAbierta(t, s)
	r := desdeCasa(httptest.NewRequest(http.MethodPost, "/seguridad/soltar",
		strings.NewReader(url.Values{"csrf": {csrf}, "ip": {dir.String()}}.Encode())))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	s.Rutas().ServeHTTP(w, r)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("soltar -> %d; se esperaba 303", w.Code)
	}
	if len(s.cuarentena.Vigentes(time.Now())) != 0 {
		t.Error("el panel dijo que lo soltó y sigue apartado")
	}
}

// Soltar CAMBIA algo, así que exige testigo como todo lo demás que cambia.
func TestSoltarExigeTestigoCSRF(t *testing.T) {
	s := servidorConAuth(t)
	dir := apartar(t, s, "203.0.113.7")
	cookie, _ := sesionAbierta(t, s)

	if got := postCon(t, s.Rutas(), cookie, "/seguridad/soltar",
		url.Values{"ip": {dir.String()}}); got != http.StatusForbidden {
		t.Errorf("soltar sin CSRF -> %d; se esperaba 403", got)
	}
	if len(s.cuarentena.Vigentes(time.Now())) != 1 {
		t.Error("una petición sin testigo llegó a soltar al apartado")
	}
}

// LA MARCA DE LA BARRA — el único sitio donde todo esto se ve sin ir a
// buscarlo. Se comprueba en el LISTADO, que es la página que el responsable
// abre de verdad; que aparezca en el panel de seguridad no serviría de nada,
// porque para verla ahí ya habría tenido que ir.
func TestLaBarraEnsenaLaMarcaYMirarLaApaga(t *testing.T) {
	s := servidorConAuth(t)
	apartar(t, s, "203.0.113.7")
	s.novedades.Recalcular(nil, s.cuarentena.Vigentes(time.Now()), nil)

	cookie := superusuarioEn(t, s)
	conMarca := pedirDesde(t, s.Rutas(), http.MethodGet, "/", "192.168.1.18:5000", cookie).Body.String()
	if !strings.Contains(conMarca, `Seguridad <span class="pastilla">1</span>`) {
		t.Errorf("la barra no enseña la marca con una cuarentena recién disparada:\n%s", conMarca)
	}

	// Mirar el panel es lo que significa «ya lo he visto».
	panelSeguridad(t, s, "")
	if n := s.novedades.Cuantas(); n != 0 {
		t.Errorf("tras mirar el panel la marca sigue en %d", n)
	}
	sinMarca := pedirDesde(t, s.Rutas(), http.MethodGet, "/", "192.168.1.18:5000", cookie).Body.String()
	if strings.Contains(sinMarca, `class="pastilla">1<`) {
		t.Error("la marca sigue pintada después de mirar el panel")
	}
	// Y «Seguridad» sigue estando: lo que desaparece es la pastilla, no el
	// botón.
	if !strings.Contains(sinMarca, `href="/seguridad">Seguridad`) {
		t.Error("desapareció el botón entero en vez de solo la marca")
	}
}
