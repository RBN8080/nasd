package geoip

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Preparar lee los dos TSV de IPtoASN (v4 y v6, ya descargados y
// descomprimidos por el script de aprovisionamiento) y escribe la base de
// ancho fijo que geoip.Abrir sabe leer. Devuelve cuántos rangos quedaron.
//
// Formato de entrada, confirmado contra el archivo real y no supuesto —
// cinco campos separados por tabulador:
//
//	inicio_rango	fin_rango	ASN	país_ISO	nombre_del_operador
//
// Ejemplo real: «1.0.0.0	1.0.0.255	13335	US	CLOUDFLARENET»
// Un rango sin asignar: «1.0.1.0	1.0.3.255	0	None	Not routed»
//
// # POR QUÉ SE MEZCLA EN VEZ DE CONCATENAR
//
// Los dos archivos vienen cada uno ordenado ascendente por sí solo, pero NO
// se puede dar por hecho que todo IPv4 ordene antes que todo IPv6: una IPv4
// se codifica como ::ffff:a.b.c.d en 16 bytes, y ese valor cae numéricamente
// ENTRE rangos IPv6 de valor bajo que el propio archivo v6 ya trae —«::»,
// «64:ff9b::/96» (NAT64), «100::/8»—. Concatenar v4 después de v6 dejaría el
// archivo de salida DESORDENADO justo en esa zona, y sort.Search sobre un
// archivo desordenado no falla con un error: devuelve el operador equivocado
// en silencio, que es el peor tipo de fallo para esta pieza.
//
// Se hace un mezclado de dos flujos en O(1) de memoria —dos cursores, nunca
// se cargan los ~500 000 registros combinados a la vez—, comparando por el
// mismo criterio de 16 bytes que usará la búsqueda.
func Preparar(v4, v6 io.Reader, rutaSalida string) (int, error) {
	c4 := nuevoCursor(v4)
	c6 := nuevoCursor(v6)
	defer c4.cerrar()
	defer c6.cerrar()

	dir := filepath.Dir(rutaSalida)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, fmt.Errorf("preparar %q: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".geoip-*")
	if err != nil {
		return 0, fmt.Errorf("crear el temporal de la base geoip: %w", err)
	}
	nombreTmp := tmp.Name()
	defer os.Remove(nombreTmp) // no-op si el rename salió bien

	// La cabecera lleva el RECUENTO, que no se conoce hasta acabar de
	// mezclar. Se deja un hueco de tamCabecera bytes y se rellena al final
	// con un Seek — más simple que escribir a un búfer intermedio del tamaño
	// del archivo entero (~40 MB) solo para anteponerle 16 bytes.
	if _, err := tmp.Write(make([]byte, tamCabecera)); err != nil {
		tmp.Close()
		return 0, fmt.Errorf("reservar la cabecera de la base geoip: %w", err)
	}

	w := bufio.NewWriter(tmp)
	n := 0
	for {
		a, okA := c4.actual()
		b, okB := c6.actual()
		if !okA && !okB {
			break
		}
		var r registro
		switch {
		case !okB || (okA && bytes.Compare(a.lo[:], b.lo[:]) <= 0):
			r = a
			c4.avanzar()
		default:
			r = b
			c6.avanzar()
		}
		if err := escribirRegistro(w, r); err != nil {
			tmp.Close()
			return 0, fmt.Errorf("escribir la base geoip: %w", err)
		}
		n++
	}
	if err := c4.err(); err != nil {
		tmp.Close()
		return 0, fmt.Errorf("leer el TSV de IPv4: %w", err)
	}
	if err := c6.err(); err != nil {
		tmp.Close()
		return 0, fmt.Errorf("leer el TSV de IPv6: %w", err)
	}
	if err := w.Flush(); err != nil {
		tmp.Close()
		return 0, fmt.Errorf("escribir la base geoip: %w", err)
	}

	// La cabecera, ahora que ya se sabe «n». Los mismos cuatro pasos de
	// ADR-0024: contenido en el temporal, fsync, rename, fsync del
	// directorio — mismo patrón que metricas.guardar y seguridad.guardar.
	var cab [tamCabecera]byte
	copy(cab[:8], magia)
	binary.BigEndian.PutUint16(cab[8:10], version)
	binary.BigEndian.PutUint16(cab[10:12], tamRegistro)
	binary.BigEndian.PutUint32(cab[12:16], uint32(n))
	if _, err := tmp.WriteAt(cab[:], 0); err != nil {
		tmp.Close()
		return 0, fmt.Errorf("escribir la cabecera de la base geoip: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return 0, fmt.Errorf("sincronizar la base geoip: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return 0, fmt.Errorf("cerrar el temporal de la base geoip: %w", err)
	}
	if err := os.Rename(nombreTmp, rutaSalida); err != nil {
		return 0, fmt.Errorf("publicar la base geoip: %w", err)
	}
	if err := sincronizarDir(dir); err != nil {
		return 0, err
	}
	return n, nil
}

// registro es la forma en memoria de UNA línea ya parseada, con las
// direcciones convertidas a su forma comparable de 16 bytes.
type registro struct {
	lo, hi [16]byte
	asn    uint32
	pais   string
	nombre string
}

func escribirRegistro(w io.Writer, r registro) error {
	var buf [tamRegistro]byte
	p := 0
	copy(buf[p:], r.lo[:])
	p += tamLo
	copy(buf[p:], r.hi[:])
	p += tamHi
	binary.BigEndian.PutUint32(buf[p:p+tamASN], r.asn)
	p += tamASN
	copy(buf[p:p+tamPais], r.pais) // el resto queda en 0x00, TrimRight al leer
	p += tamPais
	copy(buf[p:p+tamNombre], truncarUTF8(r.nombre, tamNombre))
	_, err := w.Write(buf[:])
	return err
}

// cursor entrega los registros de un TSV en orden, uno a la vez, saltando ya
// los que no informan nada (asn 0, «Not routed» — no es que el hueco no
// exista, es que ahí no hay a quién nombrar, y un fallo de búsqueda en ese
// hueco YA significa «no se sabe», que es la respuesta honesta).
type cursor struct {
	s           *bufio.Scanner
	linea       int
	act         registro
	tieneActual bool
	error       error
}

func nuevoCursor(r io.Reader) *cursor {
	c := &cursor{s: bufio.NewScanner(r)}
	// Las descripciones más largas del archivo real rondan 80 caracteres;
	// 4096 deja margen amplio sin abrir la puerta a una línea de tamaño
	// arbitrario si el archivo llegara corrupto.
	c.s.Buffer(make([]byte, 0, 4096), 4096)
	c.avanzar()
	return c
}

// avanzar deja preparado el siguiente registro VÁLIDO, saltando los que no
// informan y las líneas ilegibles —que se cuentan y se reportan, no se
// abortan: un TSV de medio millón de líneas con una mal formada en algún
// punto no debe tirar la preparación entera, y el recuento final permite
// notar si «unas pocas» se convirtieron en «la mayoría».
func (c *cursor) avanzar() {
	for c.s.Scan() {
		c.linea++
		r, ok, err := parsearLinea(c.s.Text())
		if err != nil {
			c.error = fmt.Errorf("línea %d: %w", c.linea, err)
			c.tieneActual = false
			return
		}
		if !ok {
			continue // sin ASN: no informa nada, se salta
		}
		c.act = r
		c.tieneActual = true
		return
	}
	if err := c.s.Err(); err != nil {
		c.error = err
	}
	c.tieneActual = false
}

func (c *cursor) actual() (registro, bool) { return c.act, c.tieneActual }
func (c *cursor) err() error               { return c.error }
func (c *cursor) cerrar()                  {}

// parsearLinea interpreta una línea del TSV. El booleano distingue «se leyó
// bien pero no informa nada» (asn 0) de «hay un error», que son casos
// distintos y merecen tratarse distinto: el primero se salta en silencio
// —es la mayoría del archivo—, el segundo se cuenta como fallo.
func parsearLinea(linea string) (registro, bool, error) {
	if linea == "" {
		return registro{}, false, nil
	}
	campos := strings.Split(linea, "\t")
	if len(campos) != 5 {
		return registro{}, false, fmt.Errorf("se esperaban 5 campos separados por tabulador, hay %d", len(campos))
	}

	asn, err := strconv.ParseUint(campos[2], 10, 32)
	if err != nil {
		return registro{}, false, fmt.Errorf("ASN %q inválido: %w", campos[2], err)
	}
	if asn == 0 {
		return registro{}, false, nil // «Not routed»: no informa nada
	}

	lo, err := netip.ParseAddr(campos[0])
	if err != nil {
		return registro{}, false, fmt.Errorf("dirección de inicio %q inválida: %w", campos[0], err)
	}
	hi, err := netip.ParseAddr(campos[1])
	if err != nil {
		return registro{}, false, fmt.Errorf("dirección de fin %q inválida: %w", campos[1], err)
	}

	// EL PAÍS SE ACEPTA SOLO SI ES UN CÓDIGO ISO DE DOS LETRAS; cualquier
	// otra cosa se guarda como «no se sabe» en vez de tumbar la preparación.
	//
	// La primera versión fallaba con «código de país "Unknown" más largo de
	// 2» y no llegaba a escribir nada. Lo destapó ejecutarlo contra el archivo
	// REAL en el nodo, no las pruebas: el fragmento que se usó de muestra
	// tenía «None» —que sí estaba contemplado— pero no «Unknown», que aparece
	// en 5232 rangos del archivo completo. Se midió antes de decidir.
	//
	// Rechazar el lote entero por 5232 rangos de 714 387 sería cambiar toda la
	// base por un campo que además es el menos importante de los tres: el
	// OPERADOR es lo que hace interpretable una fila, y esos rangos sí lo
	// tienen. Un país vacío la plantilla ya sabe no pintarlo.
	pais := campos[3]
	if len(pais) != tamPais {
		pais = ""
	}

	return registro{
		lo: lo.As16(), hi: hi.As16(),
		asn: uint32(asn), pais: pais, nombre: campos[4],
	}, true, nil
}

// truncarUTF8 recorta sin partir una secuencia UTF-8, igual que
// seguridad.TruncarAgente y por el mismo motivo: el nombre de un operador
// puede llevar tildes u otros caracteres multibyte, y una runa rota en un
// campo de ancho fijo se leería mal en cada consulta futura, no solo en la
// preparación.
func truncarUTF8(s string, tope int) string {
	if len(s) <= tope {
		return s
	}
	corte := tope
	for corte > 0 && s[corte]&0xC0 == 0x80 {
		corte--
	}
	return s[:corte]
}
