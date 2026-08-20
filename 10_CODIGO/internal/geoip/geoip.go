// Package geoip resuelve una dirección IP a su país y su operador.
//
// # PARA QUÉ, EN UNA FRASE
//
// «203.0.113.7» no le dice nada a nadie. «DIGITALOCEAN-ASN · DE» sí: quien
// mira el panel puede juzgar si un origen tiene sentido sin saber de redes.
// Es lo que el responsable pidió al ver la primera versión —«me faltará
// conocimiento técnico adicional para interpretar la información»— y por eso
// esta pieza existe.
//
// # DE DÓNDE SALEN LOS DATOS, Y POR QUÉ NO DE MAXMIND
//
// De IPtoASN (iptoasn.com), publicado bajo Public Domain Dedication and
// License v1.0, en TSV y con actualización horaria. **No exige cuenta, ni
// clave, ni aceptar un contrato.**
//
// Se estudió GeoLite2 de MaxMind, que es la opción habitual, y se descartó —
// NO por dinero, que es gratis, sino por tres cosas que en este proyecto
// pesan más:
//
//  1. Exige cuenta y una clave de licencia que CADUCA cada 90 días si no se
//     reconfirma. Una credencial que expira sola es un modo de fallo nuevo en
//     un servicio que hoy no tiene ninguno.
//  2. Su EULA obliga a borrar la base dentro de los 30 días de cada versión
//     nueva. Es una obligación contractual sobre un NAS doméstico.
//  3. Su formato .mmdb exige una biblioteca externa, y go.mod de este
//     proyecto dice literalmente «Sin dependencias externas — ADR-0013 (P8)».
//     La alternativa era escribir un intérprete de formato binario ajeno.
//
// El TSV de IPtoASN no tiene ninguno de los tres problemas y trae país Y
// operador en el mismo archivo.
//
// # POR QUÉ UN ARCHIVO DE ANCHO FIJO Y NO EL TSV DIRECTAMENTE
//
// Se consideró buscar por bisección sobre el propio TSV, que evitaría el paso
// de conversión. Se descartó: las líneas son de ancho variable, así que cada
// sonda obliga a rastrear hasta el siguiente salto de línea, y esa búsqueda
// tiene casos límite —la última línea del archivo, entre otros— fáciles de
// escribir mal y MUY difíciles de notar: devolvería el operador equivocado de
// vez en cuando, sin fallar.
//
// Con ancho fijo, el registro i está en `cabecera + i*tamañoRegistro`. La
// búsqueda es sort.Search sobre un índice, sin rastreo y sin casos límite.
// Se paga una conversión —«nasd --preparar-geoip»— a cambio de que la parte
// que corre a diario sea trivialmente correcta.
//
// # CUÁNTA MEMORIA CUESTA: NINGUNA
//
// No se carga nada. La búsqueda son ~20 lecturas de 16 bytes con ReadAt sobre
// un archivo abierto, que el caché de página del núcleo sirve de RAM sin que
// este proceso las cuente como suyas. En un nodo de 592 MB (RES-01) donde
// nasd ocupa 8 MB de RSS, cargar 47 MB de tablas habría sido la misma
// desproporción que D-05 rechazó con Docker.
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
