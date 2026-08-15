package geoip

import (
	"bytes"
	"fmt"
	"math/rand/v2"
	"net/netip"
	"path/filepath"
	"strings"
	"testing"
)

// LA PRUEBA QUE HACE SEGURO TODO ESTE PAQUETE.
//
// La búsqueda binaria es el único punto del panel de seguridad donde un
// defecto NO se nota: un error de límites no revienta ni registra nada, solo
// devuelve el operador equivocado de vez en cuando. Y el panel existe
// precisamente para que el responsable pueda juzgar un origen, así que un
// dato torcido es peor que no tener dato.
//
// Se compara `Buscar` contra un recorrido lineal sobre EXACTAMENTE los mismos
// datos, para miles de objetivos elegidos al azar y —lo que más importa— para
// las fronteras: el primer y el último byte de cada rango, el byte anterior al
// inicio y el siguiente al final. Los errores de este tipo de código viven en
// esas cuatro posiciones, no en el medio de un rango.
//
// Si alguna vez discrepan, la prueba falla y dice con qué dirección.
func TestLaBusquedaBinariaCoincideConLaFuerzaBruta(t *testing.T) {
	// Un conjunto sintético con formas incómodas a propósito: rangos de un
	// solo elemento, rangos enormes, huecos entre medias, y las dos familias
	// mezcladas para ejercitar el orden combinado de 16 bytes.
	lineas := []string{
		"1.0.0.0\t1.0.0.0\t1\tAA\tuno solo",       // rango de UN elemento
		"1.0.0.2\t1.0.0.3\t2\tBB\tdos",            // hueco antes: falta 1.0.0.1
		"10.0.0.0\t10.255.255.255\t3\tCC\tenorme", // rango de 16 millones
		"192.0.2.0\t192.0.2.255\t4\tDD\tdocumentacion",
		"203.0.113.128\t203.0.113.255\t5\tEE\tmitad alta",
		"255.255.255.0\t255.255.255.255\t6\tFF\tel final de IPv4",
	}
	lineasV6 := []string{
		"::\t::\t7\tGG\tla direccion cero",
		"2001:db8::\t2001:db8::ffff\t8\tHH\tdocumentacion v6",
		"3fff:2a0::\t3fff:2a0:ffff:ffff:ffff:ffff:ffff:ffff\t9\tII\tel prefijo de casa",
		"ffff::\tffff::ffff\t10\tJJ\tel final de IPv6",
	}

	ruta := filepath.Join(t.TempDir(), "geoip")
	if _, err := Preparar(
		strings.NewReader(strings.Join(lineas, "\n")+"\n"),
		strings.NewReader(strings.Join(lineasV6, "\n")+"\n"),
		ruta,
	); err != nil {
		t.Fatalf("Preparar: %v", err)
	}
	b, err := Abrir(ruta)
	if err != nil {
		t.Fatalf("Abrir: %v", err)
	}
	defer b.Cerrar()

	// La referencia: los mismos rangos, en memoria, buscados uno a uno.
	referencia := parsearTodo(t, append(append([]string{}, lineas...), lineasV6...))
	if b.Cuantos() != len(referencia) {
		t.Fatalf("la base trae %d registros y la referencia %d", b.Cuantos(), len(referencia))
	}

	comparar := func(t *testing.T, ip netip.Addr) {
		t.Helper()
		quiero, hayQuiero := fuerzaBruta(referencia, ip)
		got, hayGot := b.Buscar(ip)
		if hayGot != hayQuiero {
			t.Fatalf("%s: Buscar dice encontrado=%v y la fuerza bruta dice %v", ip, hayGot, hayQuiero)
		}
		if hayQuiero && (got.ASN != quiero.asn || got.Pais != quiero.pais) {
			t.Fatalf("%s: Buscar devuelve AS%d/%s y la fuerza bruta AS%d/%s",
				ip, got.ASN, got.Pais, quiero.asn, quiero.pais)
		}
	}

	// 1. LAS FRONTERAS, que es donde viven los errores de límites.
	t.Run("fronteras", func(t *testing.T) {
		for _, r := range referencia {
			lo := netip.AddrFrom16(r.lo).Unmap()
			hi := netip.AddrFrom16(r.hi).Unmap()
			for _, ip := range []netip.Addr{lo, hi, lo.Prev(), hi.Next()} {
				if ip.IsValid() {
					comparar(t, ip)
				}
			}
		}
	})

	// 2. AL AZAR, con semilla fija para que un fallo sea reproducible: una
	// prueba de propiedades que no se puede repetir es una anécdota.
	t.Run("al azar", func(t *testing.T) {
		g := rand.New(rand.NewPCG(1, 2))
		for range 5000 {
			var b16 [16]byte
			for i := range b16 {
				b16[i] = byte(g.UintN(256))
			}
			// La mitad como IPv4, la mitad como IPv6, para cubrir las dos
			// familias y el punto donde se tocan en el orden combinado.
			ip := netip.AddrFrom16(b16)
			if g.UintN(2) == 0 {
				ip = netip.AddrFrom4([4]byte{b16[0], b16[1], b16[2], b16[3]})
			}
			comparar(t, ip)
		}
	})

	// 3. LOS EXTREMOS ABSOLUTOS del espacio de direcciones.
	t.Run("extremos", func(t *testing.T) {
		for _, s := range []string{
			"0.0.0.0", "255.255.255.255",
			"::", "ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff",
		} {
			comparar(t, netip.MustParseAddr(s))
		}
	})
}

// fuerzaBruta es la implementación obviamente correcta: recorre TODOS los
// rangos y devuelve el que contenga la dirección. Es lenta y no importa —
// existe para no tener que creerse la rápida.
func fuerzaBruta(rs []registro, ip netip.Addr) (registro, bool) {
	objetivo := ip.As16()
	for _, r := range rs {
		if bytes.Compare(objetivo[:], r.lo[:]) >= 0 && bytes.Compare(objetivo[:], r.hi[:]) <= 0 {
			return r, true
		}
	}
	return registro{}, false
}

func parsearTodo(t *testing.T, lineas []string) []registro {
	t.Helper()
	var out []registro
	for _, l := range lineas {
		r, ok, err := parsearLinea(l)
		if err != nil {
			t.Fatalf("parsearLinea(%q): %v", l, err)
		}
		if ok {
			out = append(out, r)
		}
	}
	return out
}

// TestElMezcladoDejaElArchivoOrdenado.
//
// Es la propiedad de la que DEPENDE la búsqueda binaria entera, y por eso se
// comprueba aparte en vez de confiarla al mezclado: sobre un archivo
// desordenado, sort.Search no falla con un error — devuelve el registro
// equivocado en silencio.
//
// El caso que lo hace necesario no es teórico: una IPv4 se codifica como
// ::ffff:a.b.c.d, y ese valor cae numéricamente ENTRE rangos IPv6 de valor
// bajo que el archivo v6 real ya trae («::», «64:ff9b::/96» de NAT64,
// «100::/8»). Concatenar en vez de mezclar rompería el orden justo ahí.
func TestElMezcladoDejaElArchivoOrdenado(t *testing.T) {
	// v6 con rangos POR DEBAJO y POR ENCIMA del bloque ::ffff:0:0/96 donde
	// viven todas las IPv4: si se concatenara, el orden se rompería.
	v6 := "100::\t100::ffff\t1\tAA\tpor debajo de las IPv4 mapeadas\n" +
		"2001:db8::\t2001:db8::ffff\t2\tBB\tmuy por encima\n"
	v4 := "8.8.8.0\t8.8.8.255\t3\tCC\ten medio de los dos\n" +
		"9.9.9.0\t9.9.9.255\t4\tDD\ttambien en medio\n"

	ruta := filepath.Join(t.TempDir(), "geoip")
	n, err := Preparar(strings.NewReader(v4), strings.NewReader(v6), ruta)
	if err != nil {
		t.Fatalf("Preparar: %v", err)
	}
	if n != 4 {
		t.Fatalf("se escribieron %d registros y hay 4 con ASN", n)
	}

	b, err := Abrir(ruta)
	if err != nil {
		t.Fatalf("Abrir: %v", err)
	}
	defer b.Cerrar()

	// Se recorre el archivo entero comprobando que cada inicio es >= al
	// anterior. Si el mezclado se sustituyera por una concatenación, esto
	// falla — que es exactamente lo que tiene que pasar.
	var previo [16]byte
	for i := range b.Cuantos() {
		lo, err := b.inicioDe(i)
		if err != nil {
			t.Fatalf("leyendo el registro %d: %v", i, err)
		}
		if i > 0 && bytes.Compare(lo[:], previo[:]) < 0 {
			t.Fatalf("el registro %d (%v) va antes que el %d (%v): el archivo NO está ordenado",
				i, netip.AddrFrom16(lo), i-1, netip.AddrFrom16(previo))
		}
		previo = lo
	}

	// Y las cuatro se encuentran, que es la comprobación de que ordenar bien
	// no se logró a costa de perder registros.
	for _, s := range []string{"100::1", "8.8.8.8", "9.9.9.9", "2001:db8::1"} {
		if _, ok := b.Buscar(netip.MustParseAddr(s)); !ok {
			t.Errorf("%s no se encontró tras el mezclado", s)
		}
	}
}

// TestUnNombreLargoNoRompeElRegistroSiguiente: el nombre va en un campo de
// ancho fijo, así que un operador con descripción larga tiene que recortarse
// —y no desbordar sobre el registro de al lado, que lo dejaría ilegible—.
func TestUnNombreLargoNoRompeElRegistroSiguiente(t *testing.T) {
	largo := strings.Repeat("Ñ", 200) // multibyte a propósito: 400 bytes
	v4 := fmt.Sprintf("1.0.0.0\t1.0.0.255\t1\tAA\t%s\n", largo) +
		"2.0.0.0\t2.0.0.255\t2\tBB\tel siguiente, intacto\n"

	ruta := filepath.Join(t.TempDir(), "geoip")
	if _, err := Preparar(strings.NewReader(v4), strings.NewReader(""), ruta); err != nil {
		t.Fatalf("Preparar: %v", err)
	}
	b, err := Abrir(ruta)
	if err != nil {
		t.Fatalf("Abrir: %v", err)
	}
	defer b.Cerrar()

	primero, ok := b.Buscar(netip.MustParseAddr("1.0.0.1"))
	if !ok {
		t.Fatal("no se encontró el primer rango")
	}
	if len(primero.Nombre) > tamNombre {
		t.Errorf("el nombre guardado mide %d bytes y el tope es %d", len(primero.Nombre), tamNombre)
	}
	if strings.ContainsRune(primero.Nombre, '�') {
		t.Error("el recorte partió una secuencia UTF-8 por la mitad")
	}

	// LO QUE DE VERDAD PRUEBA ESTO: el registro de al lado sigue entero.
	segundo, ok := b.Buscar(netip.MustParseAddr("2.0.0.1"))
	if !ok {
		t.Fatal("el registro siguiente desapareció: el nombre largo lo pisó")
	}
	if segundo.ASN != 2 || segundo.Nombre != "el siguiente, intacto" {
		t.Fatalf("el registro siguiente salió corrupto: %+v", segundo)
	}
}
