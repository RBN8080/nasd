// Package geoip resolves an IP address to its country and its network operator.
//
// # WHAT FOR, IN ONE SENTENCE
//
// "203.0.113.7" tells nobody anything. "DIGITALOCEAN-ASN - DE" does: whoever
// looks at the panel can judge whether an origin makes sense without knowing
// about networks. It is what the owner asked for on seeing the first version -
// "I would lack the additional technical knowledge to interpret the
// information" - and that is why this piece exists.
//
// # WHERE THE DATA COMES FROM, AND WHY NOT FROM MAXMIND
//
// From IPtoASN (iptoasn.com), published under the Public Domain Dedication and
// License v1.0, as TSV and updated hourly. **It requires no account, no key,
// and no contract to accept.**
//
// GeoLite2 from MaxMind, which is the usual option, was studied and discarded -
// NOT over money, since it is free, but over three things that weigh more in
// this project:
//
//  1. It requires an account and a licence key that EXPIRES every 90 days if
//     not reconfirmed. A credential that expires on its own is a new failure
//     mode in a service that has none today.
//  2. Its EULA obliges deleting the database within 30 days of each new
//     release. That is a contractual obligation on a home NAS.
//  3. Its .mmdb format requires an external library, and this project's go.mod
//     literally says "No external dependencies - ADR-0013 (P8)". The
//     alternative was writing an interpreter for somebody else's binary format.
//
// The IPtoASN TSV has none of the three problems and carries country AND
// operator in the same file.
//
// # WHY A FIXED-WIDTH FILE AND NOT THE TSV DIRECTLY
//
// Binary searching over the TSV itself was considered, which would avoid the
// conversion step. It was discarded: the lines are variable width, so every
// probe has to scan to the next newline, and that search has edge cases - the
// last line of the file, among others - that are easy to get wrong and VERY
// hard to notice: it would return the wrong operator now and then, without
// failing.
//
// With fixed width, record i sits at `header + i*recordSize`. The lookup is a
// sort.Search over an index, with no scanning and no edge cases. One conversion
// ("nasd --preparar-geoip") is paid so that the part running daily is trivially
// correct.
//
// # HOW MUCH MEMORY IT COSTS: NONE
//
// Nothing is loaded. A lookup is ~20 reads of 16 bytes with ReadAt over an open
// file, which the kernel page cache serves from RAM without this process
// counting them as its own. On a 592 MB node (RES-01) where nasd holds 8 MB of
// RSS, loading 47 MB of tables would have been the same disproportion that D-05
// rejected with Docker.
package geoip

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net/netip"
	"os"
	"sort"
	"time"
)

const (
	// magia identifica el archivo. Un archivo de otra cosa —o de una versión
	// futura con otro formato— se rechaza al abrir en vez de leerse como
	// basura y devolver operadores inventados.
	magia = "NASGEOIP"

	version = 1

	// Tamaños del registro. Se escriben aquí y se COMPRUEBAN contra la
	// cabecera al abrir: si un día cambian y alguien no regenera el archivo,
	// el desajuste se ve al arrancar y no en un dato torcido meses después.
	tamLo     = 16
	tamHi     = 16
	tamASN    = 4
	tamPais   = 2
	tamNombre = 48

	tamRegistro = tamLo + tamHi + tamASN + tamPais + tamNombre // 86
	tamCabecera = 16
)

// Info es lo que se sabe de una dirección.
type Info struct {
	// ASN es el número de sistema autónomo. Se conserva además del nombre
	// porque es el identificador estable: los nombres cambian de redacción
	// entre versiones de la base y el número no.
	ASN uint32
	// Pais es el código ISO 3166-1 alfa-2 («MX», «US», «DE»).
	//
	// SE ENSEÑA EL CÓDIGO Y NO EL NOMBRE TRADUCIDO, y es una decisión
	// consciente: traducirlo exige una tabla de 250 entradas que hay que
	// mantener, y lo que de verdad hace interpretable una fila es el OPERADOR
	// —«DIGITALOCEAN», «CHINANET»— que ya viene en el mismo registro. Si algún
	// día el responsable quiere los nombres en español, la tabla entra aquí y
	// no cambia nada más.
	Pais string
	// Nombre es el operador, tal como lo publica IPtoASN, recortado a
	// tamNombre bytes.
	Nombre string
}

// BaseDatos consulta el archivo preparado. Es de solo lectura y se puede
// usar desde varias goroutines a la vez: ReadAt no tiene posición que
// compartir, que es justo la razón de usarlo en vez de Seek + Read.
type BaseDatos struct {
	f io.ReaderAt
	c io.Closer
	n int
	// fecha es la de modificación del archivo, es decir cuándo se preparó.
	//
	// SE PUBLICA EN EL PANEL Y NO ES UN ADORNO: esta base se refresca una vez
	// al mes, así que puede ir por detrás de la realidad. Una base vieja que
	// no se anuncia es una afirmación que dejó de ser verdad sin avisar — el
	// modo de fallo que este proyecto persigue. Mismo criterio que el
	// «medido {{fecha}}» del uso de disco en /administracion.
	fecha time.Time
}

// Abrir carga la base. Que el archivo NO exista se distingue de que esté
// roto, porque significan cosas distintas para quien llama: lo primero es un
// nodo al que todavía no se le ha instalado la base —normal—, lo segundo es
// algo que hay que mirar.
func Abrir(ruta string) (*BaseDatos, error) {
	f, err := os.Open(ruta)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}

	var cab [tamCabecera]byte
	if _, err := f.ReadAt(cab[:], 0); err != nil {
		f.Close()
		return nil, fmt.Errorf("cabecera de %q ilegible: %w", ruta, err)
	}
	if string(cab[:8]) != magia {
		f.Close()
		return nil, fmt.Errorf("%q no es una base geoip de este proyecto", ruta)
	}
	if v := binary.BigEndian.Uint16(cab[8:10]); v != version {
		f.Close()
		return nil, fmt.Errorf("%q es de la versión %d y esta build lee la %d: regenérela", ruta, v, version)
	}
	if t := binary.BigEndian.Uint16(cab[10:12]); t != tamRegistro {
		f.Close()
		return nil, fmt.Errorf("%q trae registros de %d bytes y esta build espera %d: regenérela", ruta, t, tamRegistro)
	}
	n := int(binary.BigEndian.Uint32(cab[12:16]))

	// El recuento declarado tiene que cuadrar con el tamaño real. Si no, el
	// archivo se truncó a medio copiar y las búsquedas leerían basura —o
	// peor, leerían de más y devolverían un operador equivocado.
	esperado := int64(tamCabecera) + int64(n)*tamRegistro
	if info.Size() != esperado {
		f.Close()
		return nil, fmt.Errorf("%q declara %d registros (%d bytes) y mide %d: está incompleto",
			ruta, n, esperado, info.Size())
	}

	return &BaseDatos{f: f, c: f, n: n, fecha: info.ModTime()}, nil
}

// Cuantos son los rangos cargados. Lo usa el preparador para informar.
func (b *BaseDatos) Cuantos() int {
	if b == nil {
		return 0
	}
	return b.n
}

// Fecha dice cuándo se preparó la base. Cero si no hay base: la plantilla
// distingue «no se sabe» de una fecha real en vez de pintar el año 1.
func (b *BaseDatos) Fecha() time.Time {
	if b == nil {
		return time.Time{}
	}
	return b.fecha
}

func (b *BaseDatos) Cerrar() error {
	if b == nil || b.c == nil {
		return nil
	}
	return b.c.Close()
}

// ASN dice de qué operador es una dirección, y nada más.
//
// # POR QUÉ EXISTE HABIENDO Buscar
//
// Para que internal/seguridad pueda razonar por operador SIN conocer este
// paquete. Aquel declara la interfaz que necesita —DeQuienEs, una sola función—
// y este la satisface por tener el método: el dominio sigue sin importar un
// adaptador, que es lo que ADR-0014 protege.
//
// Devolver Info entera valdría igual y sería peor: obligaría a la interfaz del
// dominio a hablar de países y de nombres de operador que allí no pintan nada.
//
// Un ASN 0 se devuelve como «no se sabe», no como el operador cero: la base
// usa ese valor para los rangos que no tienen dueño asignado, y confundirlo con
// un operador metería en el mismo saco a todo lo desconocido.
func (b *BaseDatos) ASN(ip netip.Addr) (uint32, bool) {
	info, ok := b.Buscar(ip)
	if !ok || info.ASN == 0 {
		return 0, false
	}
	return info.ASN, true
}

// Buscar resuelve una dirección.
//
// EL RECEPTOR NULO ES VÁLIDO Y DEVUELVE «no se sabe». Es lo que permite que
// el panel funcione igual en un nodo sin base instalada, sin que cada sitio
// que consulta tenga que acordarse de comprobarlo (P5: lo que no se sabe se
// dice, no se rellena).
func (b *BaseDatos) Buscar(ip netip.Addr) (Info, bool) {
	if b == nil || !ip.IsValid() {
		return Info{}, false
	}
	// As16 normaliza: una IPv4 viaja como ::ffff:a.b.c.d, que es exactamente
	// como la escribió el preparador. Sin esto, las IPv4 no encontrarían nada
	// y el fallo sería silencioso —solo faltaría el dato—.
	objetivo := ip.As16()

	// Primer registro cuyo inicio SUPERA al objetivo; el candidato es el
	// anterior. Los rangos de IPtoASN no se solapan, así que «el último que
	// empieza antes o en el objetivo» es el único que puede contenerlo.
	i := sort.Search(b.n, func(i int) bool {
		lo, err := b.inicioDe(i)
		if err != nil {
			// Un error de lectura empuja la búsqueda hacia abajo en vez de
			// abortarla: devolver «no se sabe» es correcto y no rompe el
			// panel por un sector ilegible.
			return true
		}
		return bytes.Compare(lo[:], objetivo[:]) > 0
	})
	if i == 0 {
		return Info{}, false
	}

	var reg [tamRegistro]byte
	if _, err := b.f.ReadAt(reg[:], desplazamiento(i-1)); err != nil {
		return Info{}, false
	}
	// El rango empieza antes del objetivo por construcción de la búsqueda;
	// falta comprobar que también lo alcanza. Si no, el objetivo cae en un
	// hueco sin anunciar y la respuesta honesta es «no se sabe».
	if bytes.Compare(objetivo[:], reg[tamLo:tamLo+tamHi]) > 0 {
		return Info{}, false
	}
	return descomponer(reg), true
}

func desplazamiento(i int) int64 {
	return int64(tamCabecera) + int64(i)*tamRegistro
}

func (b *BaseDatos) inicioDe(i int) ([tamLo]byte, error) {
	var lo [tamLo]byte
	_, err := b.f.ReadAt(lo[:], desplazamiento(i))
	return lo, err
}

func descomponer(reg [tamRegistro]byte) Info {
	p := tamLo + tamHi
	info := Info{ASN: binary.BigEndian.Uint32(reg[p : p+tamASN])}
	p += tamASN
	info.Pais = string(bytes.TrimRight(reg[p:p+tamPais], "\x00"))
	p += tamPais
	info.Nombre = string(bytes.TrimRight(reg[p:p+tamNombre], "\x00"))
	return info
}

// Rango es un tramo anunciado de la base, con lo que se sabe de él.
//
// # POR QUÉ RANGO Y NO PREFIJO
//
// La base guarda tramos [Desde, Hasta] porque es como los publica IPtoASN, y
// convertirlos a prefijos CIDR exigiría partir cada uno en varios —un tramo
// arbitrario no es un prefijo— con un algoritmo que hay que escribir bien y
// que no aporta nada aquí: quien consume esto no configura nftables, comprueba
// si una dirección cae dentro. Y para eso, un tramo es MÁS exacto que un
// prefijo: no redondea.
//
// Un prefijo escrito a mano («2602:fa5d::/44») sigue sirviendo, porque un
// prefijo ES un tramo — netip.Prefix da sus dos extremos.
type Rango struct {
	Desde netip.Addr
	Hasta netip.Addr
	Info  Info
}

// Contiene dice si la dirección cae dentro del tramo.
func (r Rango) Contiene(ip netip.Addr) bool {
	if !r.Desde.IsValid() || !r.Hasta.IsValid() || !ip.IsValid() {
		return false
	}
	v := netip.AddrFrom16(ip.As16())
	return v.Compare(netip.AddrFrom16(r.Desde.As16())) >= 0 &&
		v.Compare(netip.AddrFrom16(r.Hasta.As16())) <= 0
}

// BuscarRango es Buscar devolviendo además los extremos del tramo.
//
// Existe porque el panel necesita PROPONER un alcance —«el rango de este
// operador»—, y para eso no basta con saber de quién es la dirección: hay que
// poder enseñar exactamente qué se va a bloquear antes de bloquearlo.
func (b *BaseDatos) BuscarRango(ip netip.Addr) (Rango, bool) {
	if b == nil || !ip.IsValid() {
		return Rango{}, false
	}
	objetivo := ip.As16()
	i := sort.Search(b.n, func(i int) bool {
		lo, err := b.inicioDe(i)
		if err != nil {
			return true
		}
		return bytes.Compare(lo[:], objetivo[:]) > 0
	})
	if i == 0 {
		return Rango{}, false
	}
	var reg [tamRegistro]byte
	if _, err := b.f.ReadAt(reg[:], desplazamiento(i-1)); err != nil {
		return Rango{}, false
	}
	if bytes.Compare(objetivo[:], reg[tamLo:tamLo+tamHi]) > 0 {
		return Rango{}, false
	}
	return rangoDe(reg), true
}

// RangosDe devuelve TODOS los tramos anunciados por un operador.
//
// # POR QUÉ UNA PASADA COMPLETA, Y POR QUÉ SE PUEDE PERMITIR
//
// El archivo está ordenado por dirección, no por operador, así que los tramos
// de un mismo AS están desperdigados: no hay búsqueda binaria posible y hay
// que mirarlos todos. Con ~600 000 registros de 86 bytes son unos 50 MB de
// lectura secuencial.
//
// Se paga UNA vez, cuando alguien pulsa «bloquear el operador» y hay que
// componerle la propuesta, y NUNCA al servir una petición. Es la misma razón
// por la que el país y el operador de cada fila se resuelven al pintar y no al
// anotar (ADR-0062).
//
// Se lee por bloques y no registro a registro: 600 000 ReadAt de 86 bytes
// serían 600 000 llamadas al sistema para lo mismo.
//
// LO QUE ESTO NO PUEDE SABER: si el operador anuncia mañana un tramo nuevo,
// aquí no está. Por eso quien lo use guarda la FECHA de la base junto al
// resultado — un alcance congelado que se sabe de cuándo es, en vez de uno
// que cambia solo sin que nadie lo decida.
func (b *BaseDatos) RangosDe(asn uint32) ([]Rango, error) {
	if b == nil || asn == 0 {
		return nil, nil
	}
	const porBloque = 512 // 512 × 86 B ≈ 44 KB por lectura
	var (
		out []Rango
		buf = make([]byte, porBloque*tamRegistro)
	)
	for i := 0; i < b.n; i += porBloque {
		cuantos := min(porBloque, b.n-i)
		trozo := buf[:cuantos*tamRegistro]
		if _, err := b.f.ReadAt(trozo, desplazamiento(i)); err != nil {
			return out, fmt.Errorf("recorrer la base en el registro %d: %w", i, err)
		}
		for j := range cuantos {
			var reg [tamRegistro]byte
			copy(reg[:], trozo[j*tamRegistro:])
			p := tamLo + tamHi
			if binary.BigEndian.Uint32(reg[p:p+tamASN]) != asn {
				continue
			}
			out = append(out, rangoDe(reg))
		}
	}
	return out, nil
}

func rangoDe(reg [tamRegistro]byte) Rango {
	var lo, hi [16]byte
	copy(lo[:], reg[:tamLo])
	copy(hi[:], reg[tamLo:tamLo+tamHi])
	return Rango{
		Desde: netip.AddrFrom16(lo).Unmap(),
		Hasta: netip.AddrFrom16(hi).Unmap(),
		Info:  descomponer(reg),
	}
}
