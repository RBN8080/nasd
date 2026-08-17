package web

import (
	"net"
	"strings"
	"testing"
	"time"
)

// LA PRUEBA QUE REPRODUCE EL DEFECTO DE PRODUCCIÓN DEL 2026-08-16.
//
// Aquel día veinte direcciones de DRIFTNET recorrieron el 443 y el panel
// enseñó UNA, porque diecinueve murieron en el saludo TLS sin llegar a hacer
// una petición HTTP — y el historial de rechazos solo se escribe desde el
// middleware HTTP.
//
// Aquí se abre una conexión REAL contra un servidor REAL construido con
// HTTPServer, y se cierra SIN ENVIAR NADA. No hay petición, así que no hay
// rechazo que anotar; si aun así la conexión aparece en el panel, el hueco
// está cerrado.
//
// Es deliberadamente una prueba de integración y no una llamada directa a
// anotarConexion: lo que falló en producción no fue la lógica de anotar, fue
// que NADA la llamaba. Una prueba que invoque el método a mano pasaría aunque
// el ConnState de HTTPServer se borrara mañana.
func TestUnaConexionQueNuncaPideNadaAcabaEnElPanel(t *testing.T) {
	s := servidorConAuth(t)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("abrir el listener de prueba: %v", err)
	}
	// El listener finge que quien entra viene de Internet. Hace falta porque
	// el anillo descarta a propósito el bucle local, y 127.0.0.1 es «el propio
	// nodo»: sin esto la prueba comprobaría el descarte, no la anotación.
	remoto := &net.TCPAddr{IP: net.ParseIP("203.0.113.7"), Port: 44001}

	srv := s.HTTPServer("127.0.0.1", 0)
	go srv.Serve(escuchaConOrigen{Listener: ln, remoto: remoto})
	t.Cleanup(func() { srv.Close() })

	c, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("conectar: %v", err)
	}
	c.Close() // sin enviar un solo byte

	// ConnState corre en el bucle de aceptación, que es otra goroutine: se
	// espera a que llegue en vez de dar por hecho que ya pasó.
	esperarA(t, func() bool { return s.conexiones.Total() == 1 },
		"la conexión no se anotó")

	cuerpo := panelSeguridad(t, s, "")
	if !strings.Contains(cuerpo, "203.0.113.7") {
		t.Error("el panel no enseña una conexión de Internet que no llegó a pedir nada")
	}
	// Y la mitad que prueba que no se inventa nada: no hubo petición, así que
	// el historial de RECHAZOS tiene que seguir vacío. Las dos categorías se
	// mantienen separadas, que es la razón de que esto sea un anillo aparte.
	if n := s.seguridad.Total(); n != 0 {
		t.Errorf("una conexión sin petición produjo %d rechazos; no debía producir ninguno", n)
	}
}

// La otra mitad del filtro, y la que evita que el panel se llene de ruido: lo
// de casa NO entra en la tabla de conexiones aunque pase por el mismo hook.
func TestLasConexionesDeCasaNoEntranEnElPanel(t *testing.T) {
	s := servidorConAuth(t)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("abrir el listener de prueba: %v", err)
	}
	remoto := &net.TCPAddr{IP: net.ParseIP("192.168.1.18"), Port: 51000}

	srv := s.HTTPServer("127.0.0.1", 0)
	go srv.Serve(escuchaConOrigen{Listener: ln, remoto: remoto})
	t.Cleanup(func() { srv.Close() })

	c, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("conectar: %v", err)
	}
	c.Close()

	// Se espera un poco a que el bucle de aceptación haya podido anotarla, y
	// se comprueba que NO lo hizo. Sin esta espera la prueba pasaría siempre,
	// incluso con el filtro roto.
	time.Sleep(100 * time.Millisecond)
	if n := s.conexiones.Total(); n != 0 {
		t.Errorf("se anotaron %d conexiones de la LAN; no debía entrar ninguna", n)
	}
}

// HTTPServer tiene que enganchar ConnState en TODOS los servidores que
// construye, no solo en el de TLS: es el mismo argumento de D-21 —una vía
// verificada y la otra no es exactamente cómo se cuelan los huecos—.
func TestHTTPServerSiempreEnganchaConnState(t *testing.T) {
	s := servidorConAuth(t)
	if s.HTTPServer("127.0.0.1", 80).ConnState == nil {
		t.Error("el servidor HTTP se construyó sin ConnState")
	}
	if s.HTTPServer("::", 443).ConnState == nil {
		t.Error("el servidor TLS se construyó sin ConnState")
	}
}

// escuchaConOrigen envuelve un listener real y le cambia la dirección remota
// a lo que entra. Es lo que permite probar la clasificación por red contra un
// servidor de verdad sin necesitar dos máquinas.
type escuchaConOrigen struct {
	net.Listener
	remoto net.Addr
}

func (l escuchaConOrigen) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return conConOrigen{Conn: c, remoto: l.remoto}, nil
}

type conConOrigen struct {
	net.Conn
	remoto net.Addr
}

func (c conConOrigen) RemoteAddr() net.Addr { return c.remoto }

// esperarA sondea hasta que se cumpla la condición o se agote el plazo. Un
// sleep fijo sería más corto de escribir y una prueba intermitente: en un A53
// cargado el bucle de aceptación puede tardar más que en el host.
func esperarA(t *testing.T, condicion func() bool, mensaje string) {
	t.Helper()
	limite := time.Now().Add(2 * time.Second)
	for time.Now().Before(limite) {
		if condicion() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal(mensaje)
}
