package aviso

import (
	"net/netip"
	"path/filepath"
	"testing"
	"time"

	"nasd/internal/seguridad"
)

// Constructores de prueba. Existen para que cada prueba diga lo que está
// comprobando y no cincuenta líneas de andamiaje: la diferencia entre «este
// origen explora» y un literal de doce campos es la que decide si alguien
// entiende el fallo dentro de seis meses.

func registroDePrueba(t *testing.T) *Registro {
	t.Helper()
	r, err := CargarRegistro(filepath.Join(t.TempDir(), "avisos"))
	if err != nil {
		t.Fatalf("CargarRegistro: %v", err)
	}
	return r
}

// origen construye un seguridad.Origen de Internet con lo mínimo coherente.
func origen(ip string, eventos int, cuando time.Time) seguridad.Origen {
	return seguridad.Origen{
		IP:        netip.MustParseAddr(ip),
		Red:       seguridad.RedInternet,
		Eventos:   eventos,
		Primera:   cuando.Add(-2 * time.Minute),
		Ultima:    cuando,
		PorMotivo: map[seguridad.Motivo]int{},
	}
}

// explorador es un origen que dispara SenalExploracion.
func explorador(ip string, rutas int, cuando time.Time) OrigenVisto {
	o := origen(ip, rutas, cuando)
	o.Senales = []seguridad.Senal{seguridad.SenalExploracion}
	o.RutasInexistentes = rutas
	o.PorMotivo[seguridad.RutaInexistente] = rutas
	o.Evidencia = []seguridad.Sonda{
		{Metodo: "GET", Ruta: "/wp-login.php", Estado: 404, Veces: 3},
		{Metodo: "GET", Ruta: "/.env", Estado: 404, Veces: 2},
	}
	return OrigenVisto{Origen: o, Geo: "GB · AS211298 DRIFTNET"}
}

// fuerzaBruta es un origen que dispara SenalFuerzaBruta.
func fuerzaBruta(ip string, intentos int, cuando time.Time) OrigenVisto {
	o := origen(ip, intentos, cuando)
	o.Senales = []seguridad.Senal{seguridad.SenalFuerzaBruta}
	o.PorMotivo[seguridad.CredencialIncorrecta] = intentos
	return OrigenVisto{Origen: o}
}

// rutinario es un origen que NO cuenta ninguna historia: pidió y se le negó.
func rutinario(ip string, cuando time.Time) OrigenVisto {
	o := origen(ip, 3, cuando)
	o.PorMotivo[seguridad.SinSesion] = 3
	return OrigenVisto{Origen: o}
}

func hallazgo(ruta string, cuando time.Time) seguridad.Hallazgo {
	return seguridad.Hallazgo{
		Clase: seguridad.RutaAjenaAtendida, Metodo: "GET", Ruta: ruta,
		Estado: 200, Veces: 1, Primera: cuando, Ultima: cuando,
		UltimoOrigen: netip.MustParseAddr("203.0.113.9"),
	}
}

// anomalo es un origen con un rechazo que el servidor NO supo clasificar. Es la
// entrada de ClaseAnomalia, que es la representación de «esto es nuevo y no
// tenemos detector».
func anomalo(ip string, cuando time.Time) OrigenVisto {
	o := origen(ip, 3, cuando)
	o.PorMotivo[seguridad.MotivoDesconocido] = 1
	return OrigenVisto{Origen: o}
}

// apartadoDe es la respuesta del nodo colgada de un origen.
func apartadoDe(ip string, desde time.Time) *seguridad.Apartado {
	return &seguridad.Apartado{
		IP:    netip.MustParseAddr(ip),
		Senal: seguridad.SenalExploracion,
		Desde: desde,
		Hasta: desde.Add(seguridad.DuracionCuarentena),
	}
}

// direccion produce direcciones distintas y válidas para las pruebas de tope.
//
// Recorre 203.0.113.0/24 y sigue por 198.51.100.0/24 —los dos rangos que RFC
// 5737 reserva para documentación— porque las pruebas de tope necesitan más de
// 254 direcciones distintas y una IP inventada fuera de esos rangos podría
// existir de verdad.
func direccion(i int) string {
	if i < 256 {
		return netip.AddrFrom4([4]byte{203, 0, 113, byte(i)}).String()
	}
	if i < 512 {
		return netip.AddrFrom4([4]byte{198, 51, 100, byte(i - 256)}).String()
	}
	return netip.AddrFrom4([4]byte{192, 0, 2, byte(i - 512)}).String()
}

// severidadDe resume la severidad de una lista para los mensajes de error.
func severidadDe(ss []Situacion) string {
	if len(ss) == 0 {
		return "(ninguna)"
	}
	return ss[0].Severidad.String()
}
