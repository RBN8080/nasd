package seguridad

import (
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// archivoDeToques deja un historial como el que escribe nas-sensor.
func archivoDeToques(t *testing.T, lineas ...string) string {
	t.Helper()
	ruta := filepath.Join(t.TempDir(), "toques")
	cuerpo := "# Historial de toques -- anillo de 2000, del mas antiguo al mas reciente.\n" +
		"# Un toque por linea: instante origen puerto tipo.\n" +
		"# total-visto: 4821\n" +
		strings.Join(lineas, "\n") + "\n"
	if err := os.WriteFile(ruta, []byte(cuerpo), 0o644); err != nil {
		t.Fatal(err)
	}
	return ruta
}

// LA PRUEBA QUE MÁS IMPORTA DE ESTE ARCHIVO. El sensor anota todo lo que
// captura y no sabe qué es Internet; si el descarte de aquí fallara, el panel
// enseñaría el móvil del responsable, su PC y cada aparato de la casa como si
// fueran extraños tocando el nodo.
//
// Es el mismo defecto que este panel YA cometió dos veces: el IPv6 de casa
// contado como Internet, y una señal disparando sobre el propio nodo.
func TestSoloSeLeenLosToquesDeInternet(t *testing.T) {
	AprenderRedesPropias(nil) // sin /64 propio aprendido: el caso más simple
	ahora := time.Now().UTC().Truncate(time.Second)
	f := ahora.Format(time.RFC3339)

	ruta := archivoDeToques(t,
		f+" 192.168.1.23 445 syn", // la LAN
		f+" 10.77.0.3 80 syn",     // el túnel
		f+" 127.0.0.1 80 syn",     // el propio nodo
		f+" fe80::1 22 syn",       // local del enlace
		f+" 203.0.113.7 23 syn",   // Internet
		f+" 2a06:4883:5000::65 0 ping",
	)

	h, err := LeerToques(ruta, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if h.Total != 4821 {
		t.Errorf("total capturado = %d, se esperaba 4821 (la cabecera)", h.Total)
	}
	if len(h.Toques) != 2 {
		t.Fatalf("salieron %d toques, se esperaban 2 (solo los de Internet): %+v",
			len(h.Toques), h.Toques)
	}
	for _, q := range h.Toques {
		if !ClasificarRed(q.Origen).DeFuera() {
			t.Errorf("%v no es de Internet y se coló", q.Origen)
		}
	}
}

// El /64 de casa se aprende al arrancar y puede rotar. Un aparato del
// responsable hablando IPv6 nativo NO es un extraño, y esta prueba lo fija:
// es literalmente el fallo que tuvo la primera versión del panel.
func TestElIPv6DeCasaNoEsUnToqueDeInternet(t *testing.T) {
	casa := netip.MustParseAddr("3fff:2a0:101e:3d82:1111:2222:3333:4444")
	AprenderRedesPropias([]netip.Addr{netip.MustParseAddr("3fff:2a0:101e:3d82::38")})
	t.Cleanup(func() { AprenderRedesPropias(nil) })

	f := time.Now().UTC().Format(time.RFC3339)
	ruta := archivoDeToques(t,
		f+" "+casa.String()+" 443 syn",
		f+" 2a06:4883:5000::65 443 syn",
	)

	h, err := LeerToques(ruta, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(h.Toques) != 1 {
		t.Fatalf("salieron %d toques, se esperaba 1: %+v", len(h.Toques), h.Toques)
	}
	if h.Toques[0].Origen == casa {
		t.Error("el IPv6 de casa se contó como Internet, otra vez")
	}
}

// Sin sensor instalado el archivo no existe. Eso NO es un error: es un nodo al
// que todavía no se le ha puesto esta pieza, y el panel tiene que poder
// esconder las columnas en vez de pintar ceros.
func TestSinSensorInstaladoNoEsUnError(t *testing.T) {
	h, err := LeerToques(filepath.Join(t.TempDir(), "no-existe"), time.Time{})
	if err != nil {
		t.Fatalf("un archivo ausente no debe ser un error: %v", err)
	}
	if len(h.Toques) != 0 || h.Total != 0 {
		t.Errorf("sin archivo deberían salir cero toques, salieron %d (total %d)",
			len(h.Toques), h.Total)
	}
}

func TestLaVentanaAcotaLosToques(t *testing.T) {
	AprenderRedesPropias(nil)
	ahora := time.Now().UTC()
	ruta := archivoDeToques(t,
		ahora.Add(-48*time.Hour).Format(time.RFC3339)+" 203.0.113.7 23 syn",
		ahora.Add(-30*time.Minute).Format(time.RFC3339)+" 203.0.113.8 24 syn",
	)

	h, err := LeerToques(ruta, ahora.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(h.Toques) != 1 {
		t.Fatalf("con ventana de una hora salieron %d toques, se esperaba 1", len(h.Toques))
	}
	if h.Toques[0].Origen.String() != "203.0.113.8" {
		t.Errorf("sobrevivió el toque equivocado: %v", h.Toques[0].Origen)
	}
}

// Una línea corrupta NO se ignora en silencio. El archivo lo escribe OTRO
// programa: si lo que llega no se entiende, es mejor que el panel esconda la
// columna a que enseñe una cifra construida sobre lo que sí se pudo leer.
func TestUnaLineaIlegibleSeDenuncia(t *testing.T) {
	AprenderRedesPropias(nil)
	f := time.Now().UTC().Format(time.RFC3339)
	for _, caso := range []struct{ nombre, linea string }{
		{"campos de menos", f + " 203.0.113.7 23"},
		{"dirección ilegible", f + " no-es-una-ip 23 syn"},
		{"puerto fuera de rango", f + " 203.0.113.7 70000 syn"},
		{"tipo inventado", f + " 203.0.113.7 23 baile"},
		{"instante ilegible", "ayer 203.0.113.7 23 syn"},
	} {
		t.Run(caso.nombre, func(t *testing.T) {
			if _, err := LeerToques(archivoDeToques(t, caso.linea), time.Time{}); err == nil {
				t.Error("una línea corrupta pasó sin denunciarse")
			}
		})
	}
}

func TestPorOrigenTocadoAgrupaYOrdena(t *testing.T) {
	base := time.Date(2026, 8, 16, 11, 49, 0, 0, time.UTC)
	ip := func(s string) netip.Addr { return netip.MustParseAddr(s) }
	toques := []Toque{
		{Momento: base, Origen: ip("203.0.113.7"), Puerto: 23, Tipo: "syn"},
		{Momento: base.Add(time.Minute), Origen: ip("203.0.113.7"), Puerto: 2323, Tipo: "syn"},
		{Momento: base.Add(2 * time.Minute), Origen: ip("203.0.113.7"), Puerto: 23, Tipo: "syn"},
		{Momento: base.Add(time.Minute), Origen: ip("203.0.113.9"), Puerto: 0, Tipo: "ping"},
	}

	out := PorOrigenTocado(toques)
	if len(out) != 2 {
		t.Fatalf("salieron %d orígenes, se esperaban 2", len(out))
	}
	// El más insistente primero: es la pregunta que contesta esta tabla.
	if out[0].IP.String() != "203.0.113.7" || out[0].Toques != 3 {
		t.Fatalf("el primero es %v con %d toques", out[0].IP, out[0].Toques)
	}
	// Puertos DISTINTOS y ordenados: 23 aparece dos veces y cuenta una.
	if len(out[0].Puertos) != 2 || out[0].Puertos[0] != 23 || out[0].Puertos[1] != 2323 {
		t.Errorf("puertos = %v, se esperaba [23 2323]", out[0].Puertos)
	}
	if !out[0].Primera.Equal(base) || !out[0].Ultima.Equal(base.Add(2*time.Minute)) {
		t.Errorf("el intervalo salió %v → %v", out[0].Primera, out[0].Ultima)
	}
	// El puerto 0 de un ping NO se lista como si fuera un puerto.
	if len(out[1].Puertos) != 0 {
		t.Errorf("un ping no tiene puerto y se listó: %v", out[1].Puertos)
	}
}

// Un escaneo completo son 65 535 puertos. La CUENTA tiene que seguir siendo
// exacta aunque la lista se acote: si se acotaran las dos, el panel diría que
// alguien tocó doce puertos cuando recorrió el nodo entero.
func TestUnEscaneoNoFalseaLaCuentaAunqueAcorteLosPuertos(t *testing.T) {
	base := time.Now()
	ip := netip.MustParseAddr("203.0.113.7")
	var toques []Toque
	for p := 1; p <= 500; p++ {
		toques = append(toques, Toque{
			Momento: base.Add(time.Duration(p) * time.Millisecond),
			Origen:  ip, Puerto: uint16(p), Tipo: "syn",
		})
	}

	out := PorOrigenTocado(toques)
	if len(out) != 1 {
		t.Fatalf("salieron %d orígenes, se esperaba 1", len(out))
	}
	if out[0].Toques != 500 {
		t.Errorf("la cuenta se falseó: %d toques, se esperaban 500", out[0].Toques)
	}
	if len(out[0].Puertos) != topePuertos {
		t.Errorf("la lista de puertos no se acotó: %d, tope %d",
			len(out[0].Puertos), topePuertos)
	}
}
