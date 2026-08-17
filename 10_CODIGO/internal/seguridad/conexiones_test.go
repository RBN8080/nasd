package seguridad

import (
	"net/netip"
	"path/filepath"
	"testing"
	"time"
)

func conexionesDePrueba(t *testing.T) *Conexiones {
	t.Helper()
	c, err := CargarConexiones(filepath.Join(t.TempDir(), "conexiones"))
	if err != nil {
		t.Fatalf("CargarConexiones: %v", err)
	}
	return c
}

// LA PRUEBA QUE DEFINE LA PIEZA. El anillo existe para responder «quién de
// Internet ha tocado el nodo», así que anotar cualquier otra cosa lo rompe:
// el iPhone de casa abre varias conexiones por página y ahogaría en un rato
// lo único que hay que mirar.
func TestSoloSeAnotaLoQueVieneDeInternet(t *testing.T) {
	c := conexionesDePrueba(t)
	ahora := time.Now()

	casos := []struct {
		ip     string
		anota  bool
		porque string
	}{
		{"203.0.113.7", true, "Internet"},
		{"2a06:4883:5000::65", true, "Internet por IPv6 — el sondeo real del 16/08"},
		{"192.168.1.18", false, "la LAN"},
		{"10.77.0.3", false, "el túnel: llegar hasta ahí ya exigió la clave"},
		{"127.0.0.1", false, "el propio nodo"},
		{"::1", false, "el propio nodo por IPv6"},
	}
	for _, caso := range casos {
		ip := netip.MustParseAddr(caso.ip)
		if got := c.Anotar(ip, ahora); got != caso.anota {
			t.Errorf("Anotar(%s) = %v; se esperaba %v porque es %s",
				caso.ip, got, caso.anota, caso.porque)
		}
	}

	if n := c.Total(); n != 2 {
		t.Errorf("el anillo guardó %d conexiones; solo las 2 de Internet debían entrar", n)
	}
}

// Una IPv4 que llega por el socket IPv6 viene como ::ffff:a.b.c.d. Es el caso
// NORMAL de todo lo que entra por TLS, no una rareza: nasd escucha en [::]:443
// (web/tls.go). Sin desenvolverla, una dirección de casa se contaría como
// Internet.
func TestUnaIPv4MapeadaDeCasaNoCuentaComoInternet(t *testing.T) {
	c := conexionesDePrueba(t)
	if c.Anotar(netip.MustParseAddr("::ffff:192.168.1.18"), time.Now()) {
		t.Error("una IPv4 de la LAN mapeada en IPv6 se contó como Internet")
	}
}

// El IPv6 de casa tampoco, y esto no lo cubre ningún literal: se APRENDE al
// arrancar. Es el defecto que la primera versión del panel tuvo en producción
// —el iPhone del responsable clasificado como extraño— y aquí queda anclado.
func TestElIPv6DeCasaAprendidoNoEsInternet(t *testing.T) {
	previos := prefijosPropios
	t.Cleanup(func() { prefijosPropios = previos })
	AprenderRedesPropias([]netip.Addr{netip.MustParseAddr("3fff:2a0:101e:3d82::38")})

	c := conexionesDePrueba(t)
	if c.Anotar(netip.MustParseAddr("3fff:2a0:101e:3d82:3ce3:12c7:dfa8:a197"), time.Now()) {
		t.Error("el IPv6 de casa se contó como Internet")
	}
	if !c.Anotar(netip.MustParseAddr("2a06:4883:5000::65"), time.Now()) {
		t.Error("una dirección de fuera dejó de contarse como Internet")
	}
}

func TestElHistorialDeConexionesSobreviveAlReinicio(t *testing.T) {
	ruta := filepath.Join(t.TempDir(), "conexiones")
	momento := time.Now().Add(-time.Hour).Truncate(time.Second)

	c, err := CargarConexiones(ruta)
	if err != nil {
		t.Fatalf("CargarConexiones: %v", err)
	}
	c.Anotar(netip.MustParseAddr("2a06:4883:5000::65"), momento)
	if err := c.Volcar(); err != nil {
		t.Fatalf("Volcar: %v", err)
	}

	otro, err := CargarConexiones(ruta)
	if err != nil {
		t.Fatalf("recargar: %v", err)
	}
	leidas := otro.Desde(time.Time{})
	if len(leidas) != 1 {
		t.Fatalf("tras recargar hay %d conexiones; se esperaba 1", len(leidas))
	}
	if leidas[0].Origen.String() != "2a06:4883:5000::65" {
		t.Errorf("dirección leída %q", leidas[0].Origen)
	}
	if !leidas[0].Momento.Equal(momento) {
		t.Errorf("momento leído %v; se escribió %v", leidas[0].Momento, momento)
	}
}

// La ventana corta por tiempo, y el anillo está ordenado por naturaleza: la
// primera que cae fuera garantiza que las siguientes también.
func TestLaVentanaDeConexionesCortaPorTiempo(t *testing.T) {
	c := conexionesDePrueba(t)
	ahora := time.Now()
	c.Anotar(netip.MustParseAddr("203.0.113.1"), ahora.Add(-48*time.Hour))
	c.Anotar(netip.MustParseAddr("203.0.113.2"), ahora.Add(-30*time.Minute))

	recientes := c.Desde(ahora.Add(-time.Hour))
	if len(recientes) != 1 {
		t.Fatalf("en la última hora salen %d; se esperaba 1", len(recientes))
	}
	if recientes[0].Origen.String() != "203.0.113.2" {
		t.Errorf("la ventana devolvió %q", recientes[0].Origen)
	}
	if todas := c.Desde(time.Time{}); len(todas) != 2 {
		t.Errorf("sin límite salen %d; se esperaba 2", len(todas))
	}
}

// El anillo no puede crecer: el evento Capacidad+1 pisa al primero. La
// rotación es la ESTRUCTURA, no una tarea que pueda fallar.
func TestElAnilloDeConexionesNoCrecePorEncimaDeLaCapacidad(t *testing.T) {
	c := conexionesDePrueba(t)
	ahora := time.Now()
	for i := range Capacidad + 500 {
		c.Anotar(netip.AddrFrom4([4]byte{203, 0, byte(i / 256), byte(i % 256)}), ahora)
	}
	if n := len(c.Desde(time.Time{})); n != Capacidad {
		t.Errorf("el anillo conserva %d; el tope es %d", n, Capacidad)
	}
	// Y el total de SIEMPRE no se recorta: es lo que permite al panel decir
	// «se muestran 2000 de 2500» en vez de mentir por omisión.
	if c.Total() != int64(Capacidad+500) {
		t.Errorf("Total() = %d; se anotaron %d", c.Total(), Capacidad+500)
	}
}

func TestPorOrigenConectadoAgrupaYOrdena(t *testing.T) {
	base := time.Now()
	conexiones := []Conexion{
		{Momento: base.Add(2 * time.Minute), Origen: netip.MustParseAddr("203.0.113.7")},
		{Momento: base, Origen: netip.MustParseAddr("203.0.113.7")},
		{Momento: base.Add(time.Minute), Origen: netip.MustParseAddr("203.0.113.7")},
		{Momento: base, Origen: netip.MustParseAddr("198.51.100.4")},
	}

	out := PorOrigenConectado(conexiones)
	if len(out) != 2 {
		t.Fatalf("salieron %d orígenes; se esperaban 2", len(out))
	}
	// El más insistente primero: la pregunta es «quién ha estado tocando la
	// puerta», no «qué fue lo último».
	if out[0].IP.String() != "203.0.113.7" || out[0].Conexiones != 3 {
		t.Errorf("primero %v con %d conexiones", out[0].IP, out[0].Conexiones)
	}
	if !out[0].Primera.Equal(base) || !out[0].Ultima.Equal(base.Add(2*time.Minute)) {
		t.Errorf("primera=%v última=%v; el orden de llegada no las fijó bien",
			out[0].Primera, out[0].Ultima)
	}
}
