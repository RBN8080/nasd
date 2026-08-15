package geoip

import (
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Fragmentos REALES del TSV de IPtoASN, descargados y copiados a mano el
// 14/08/2026 — no inventados. Incluyen las cuatro anclas de verificación del
// plan (que no necesitan esperar tráfico de atacantes: se comprueban contra
// respuestas ya conocidas por otra vía) y un rango sin asignar de cada
// familia, para probar que se descartan.
const v4Real = "1.0.0.0\t1.0.0.255\t13335\tUS\tCLOUDFLARENET\n" +
	"1.0.1.0\t1.0.3.255\t0\tNone\tNot routed\n" +
	"1.1.1.0\t1.1.1.255\t13335\tUS\tCLOUDFLARENET\n" +
	"8.8.4.0\t8.8.4.255\t15169\tUS\tGOOGLE\n" +
	"8.8.8.0\t8.8.8.255\t15169\tUS\tGOOGLE\n" +
	"198.51.100.0\t198.51.100.255\t64496\tMX\tOperador Domestico, S.A. de C.V.\n"

// El rango que de verdad contiene 3fff:2a0:101e:3d82::38 —el AAAA del propio
// nodo (06_ACCESO_REMOTO §8.4)— es 3fff:2a0:df1:: – 3fff:2a0:1207:ffff:…,
// NO el que empieza en 3fff:2a0:14f1::, que queda por ENCIMA. Verificado
// contra el archivo real antes de escribir la prueba: un ancla mal calculada
// no comprobaría nada.
const v6Real = "::\t::1\t0\tNone\tNot routed\n" +
	"64:ff9b::1:0:0\t100::ffff:ffff:ffff:ffff\t0\tNone\tNot routed\n" +
	"3fff:2a0:df1::\t3fff:2a0:1207:ffff:ffff:ffff:ffff:ffff\t64496\tMX\tOperador Domestico, S.A. de C.V.\n"

// prepararDePrueba construye una base a partir de dos TSV (texto) en un
// archivo temporal y la abre. t.Cleanup se encarga de cerrarla.
func prepararDePrueba(t *testing.T, v4, v6 string) *BaseDatos {
	t.Helper()
	ruta := filepath.Join(t.TempDir(), "geoip")
	if _, err := Preparar(strings.NewReader(v4), strings.NewReader(v6), ruta); err != nil {
		t.Fatalf("Preparar: %v", err)
	}
	b, err := Abrir(ruta)
	if err != nil {
		t.Fatalf("Abrir: %v", err)
	}
	t.Cleanup(func() { b.Cerrar() })
	return b
}

// TestLasCuatroAnclasResuelvenBien es la verificación del plan que NO
// necesita esperar tráfico externo: cuatro direcciones cuya respuesta ya se
// conoce por otra vía —la propia IP pública del nodo (ADR-0044), su AAAA, y
// dos resolutores DNS públicos y estables—.
func TestLasCuatroAnclasResuelvenBien(t *testing.T) {
	b := prepararDePrueba(t, v4Real, v6Real)

	casos := []struct {
		nombre         string
		ip             string
		asn            uint32
		pais           string
		nombreContiene string
	}{
		{"IP pública del nodo (el operador)", "198.51.100.0", 64496, "MX", "Operador Domestico"},
		{"IPv6 del nodo (el operador)", "3fff:2a0:101e:3d82::38", 64496, "MX", "Operador Domestico"},
		{"Google DNS", "8.8.8.8", 15169, "US", "GOOGLE"},
		{"Cloudflare DNS", "1.1.1.1", 13335, "US", "CLOUDFLARENET"},
	}
	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			info, ok := b.Buscar(netip.MustParseAddr(c.ip))
			if !ok {
				t.Fatalf("%s (%s): no se encontró ningún registro", c.nombre, c.ip)
			}
			if info.ASN != c.asn {
				t.Errorf("ASN = %d, se esperaba %d", info.ASN, c.asn)
			}
			if info.Pais != c.pais {
				t.Errorf("país = %q, se esperaba %q", info.Pais, c.pais)
			}
			if !strings.Contains(info.Nombre, c.nombreContiene) {
				t.Errorf("nombre = %q, se esperaba que contuviera %q", info.Nombre, c.nombreContiene)
			}
		})
	}
}

// TestLosRangosSinAsignarSeDescartan: «Not routed» (asn 0) no aparece en la
// base. Buscar en ese hueco debe devolver «no se sabe», que es honesto —y NO
// el registro vecino por error de límites.
func TestLosRangosSinAsignarSeDescartan(t *testing.T) {
	b := prepararDePrueba(t, v4Real, v6Real)

	// v4: 6 líneas − 1 «Not routed» = 5. v6: 3 líneas − 2 «Not routed» = 1.
	const utiles = 6
	if got := b.Cuantos(); got != utiles {
		t.Fatalf("Cuantos() = %d, se esperaban %d con los «Not routed» descartados", got, utiles)
	}
	// 1.0.2.0 cae DENTRO del hueco 1.0.1.0–1.0.3.255, que es «Not routed».
	if _, ok := b.Buscar(netip.MustParseAddr("1.0.2.0")); ok {
		t.Error("un rango «Not routed» no debería resolver a ningún operador")
	}
	if _, ok := b.Buscar(netip.MustParseAddr("64:ff9b::1:2:3")); ok {
		t.Error("el hueco NAT64 «Not routed» no debería resolver a ningún operador")
	}
}

// TestUnaDireccionSinCobertura es el caso más común de todos en la práctica:
// una IP que no cae en ningún rango del fragmento cargado.
func TestUnaDireccionSinCobertura(t *testing.T) {
	b := prepararDePrueba(t, v4Real, v6Real)
	if _, ok := b.Buscar(netip.MustParseAddr("203.0.113.7")); ok {
		t.Error("una dirección fuera de cualquier rango cargado no debería resolver")
	}
}

// TestElReceptorNuloEsValido: sin base instalada, el panel sigue funcionando.
// Es lo que hace posible que un nodo recién actualizado no se rompa mientras
// nadie ha ejecutado --preparar-geoip todavía (P5: se dice, no se rellena).
func TestElReceptorNuloEsValido(t *testing.T) {
	var b *BaseDatos
	if _, ok := b.Buscar(netip.MustParseAddr("8.8.8.8")); ok {
		t.Fatal("un receptor nulo no puede devolver un resultado")
	}
	if got := b.Cuantos(); got != 0 {
		t.Errorf("Cuantos() sobre nil = %d, se esperaba 0", got)
	}
	if err := b.Cerrar(); err != nil {
		t.Errorf("Cerrar() sobre nil no debe fallar: %v", err)
	}
}

// TestAbrirUnArchivoQueNoExiste: se distingue de un archivo roto porque
// significan cosas distintas para quien llama — un nodo recién instalado sin
// base todavía es normal, un archivo roto merece mirarse (ver main.go).
func TestAbrirUnArchivoQueNoExiste(t *testing.T) {
	_, err := Abrir(filepath.Join(t.TempDir(), "no-existe"))
	if !os.IsNotExist(err) {
		t.Fatalf("err = %v, se esperaba os.IsNotExist", err)
	}
}

// TestAbrirRechazaLoQueNoEsUnaBaseGeoip: un archivo cualquiera —o de una
// versión futura de este mismo formato— no se lee como si fuera basura
// válida y no devuelve operadores inventados. Se rechaza al abrir.
func TestAbrirRechazaLoQueNoEsUnaBaseGeoip(t *testing.T) {
	casos := map[string][]byte{
		"archivo cualquiera":          []byte("esto no es una base geoip en absoluto"),
		"cabecera vacía":              make([]byte, tamCabecera),
		"versión futura desconocida":  cabeceraCon(t, magia, version+1, tamRegistro, 0),
		"tamaño de registro distinto": cabeceraCon(t, magia, version, tamRegistro+1, 0),
	}
	for nombre, contenido := range casos {
		t.Run(nombre, func(t *testing.T) {
			ruta := filepath.Join(t.TempDir(), "geoip")
			if err := os.WriteFile(ruta, contenido, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Abrir(ruta); err == nil {
				t.Error("se esperaba un error y no lo hubo")
			}
		})
	}
}

func cabeceraCon(t *testing.T, magia string, version, tamRegistro uint16, n uint32) []byte {
	t.Helper()
	b := make([]byte, tamCabecera)
	copy(b[:8], magia)
	b[8], b[9] = byte(version>>8), byte(version)
	b[10], b[11] = byte(tamRegistro>>8), byte(tamRegistro)
	b[12], b[13], b[14], b[15] = byte(n>>24), byte(n>>16), byte(n>>8), byte(n)
	return b
}

// TestUnArchivoTruncadoSeRechaza: la cabecera declara N registros pero el
// archivo mide menos. Sin esta comprobación, una copia interrumpida a medias
// —el temporizador mensual cortado por un reinicio, por ejemplo— se leería
// como una base válida con las últimas búsquedas devolviendo basura.
func TestUnArchivoTruncadoSeRechaza(t *testing.T) {
	ruta := filepath.Join(t.TempDir(), "geoip")
	// Declara 10 registros mientras el archivo solo trae la cabecera.
	if err := os.WriteFile(ruta, cabeceraCon(t, magia, version, tamRegistro, 10), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Abrir(ruta); err == nil {
		t.Fatal("un archivo truncado debe rechazarse, no leerse a medias")
	}
}

// TestUnPaisNoISOSeGuardaComoDesconocido.
//
// EL DEFECTO QUE SOLO ENSEÑÓ EL ARCHIVO COMPLETO. La primera versión
// rechazaba cualquier país que no cupiera en dos bytes y abortaba la
// preparación entera con «código de país "Unknown" más largo de 2». El
// fragmento de muestra usado en las pruebas traía «None» —contemplado— pero
// no «Unknown», que aparece en 5232 de los 714 387 rangos del archivo real.
//
// Tumbar la base entera por eso sería cambiar 714 387 rangos por el campo
// menos importante de los tres: esos rangos SÍ tienen operador, que es lo
// que de verdad hace interpretable una fila.
func TestUnPaisNoISOSeGuardaComoDesconocido(t *testing.T) {
	v4 := "1.0.0.0\t1.0.0.255\t111\tUnknown\tOperador con pais raro\n" +
		"2.0.0.0\t2.0.0.255\t222\tNone\tOperador sin pais\n" +
		"3.0.0.0\t3.0.0.255\t333\tUS\tOperador normal\n"
	b := prepararDePrueba(t, v4, "")

	if got := b.Cuantos(); got != 3 {
		t.Fatalf("Cuantos() = %d: un país raro no debe descartar el rango, solo su país", got)
	}
	casos := []struct {
		ip, pais string
		asn      uint32
	}{
		{"1.0.0.1", "", 111},
		{"2.0.0.1", "", 222},
		{"3.0.0.1", "US", 333},
	}
	for _, c := range casos {
		info, ok := b.Buscar(netip.MustParseAddr(c.ip))
		if !ok {
			t.Fatalf("%s no resuelve", c.ip)
		}
		if info.Pais != c.pais {
			t.Errorf("%s: país = %q, se esperaba %q", c.ip, info.Pais, c.pais)
		}
		// Lo que importa: el OPERADOR se conserva aunque el país no.
		if info.ASN != c.asn || info.Nombre == "" {
			t.Errorf("%s: se perdió el operador (AS%d %q)", c.ip, info.ASN, info.Nombre)
		}
	}
}
