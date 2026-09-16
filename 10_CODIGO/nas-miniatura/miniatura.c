// nas-miniatura -- extracts the embedded EXIF thumbnail of a JPEG and its
// basic metadata, without decoding the image.
//
// WHY IT EXISTS: 10_CODIGO/cmd/nasd is Go with no dependencies (ADR-0013,
// ADR-0017) and the service runs with MemoryMax=192M
// (20_APROVISIONAMIENTO/05_instalar_servicio.sh) -- that ceiling belongs to the
// WHOLE cgroup, children included, with GOMEMLIMIT=160MiB for nasd itself. That
// leaves ~32 MB for a subprocess. Decoding a 12 MP iPhone photo whole to build
// a thumbnail takes ~36 MB of working memory: it does not fit.
//
// iPhone photos ALREADY CARRY a 160x120 thumbnail inside the EXIF block --
// measured on the node on 2026-08-15 over a real photo: bytes 3160-12862 of the
// file. Extracting it is reading and copying a few thousand bytes; it decodes
// nothing. That is all this program does.
//
// INTERFACE
//
//   nas-miniatura <input-file> <output-file>
//
//   Standard output: the EXIF metadata that was found, one per line,
//   "key=value". No field is mandatory; only what was really in the file is
//   printed:
//
//     fecha=2025-04-02T14:33:07   (DateTimeOriginal, 0x9003, as ISO 8601)
//     orientacion=1                (0x0112, raw value 1-8)
//     marca=Apple                  (0x010F, Make)
//     modelo=Modelo X1             (0x0110, Model)
//
//   Exit code:
//     0  the thumbnail was written to <output-file>
//     1  no usable EXIF thumbnail -- valid JPEG, LEGITIMATE case, not an
//        error. The caller falls back to its own generic icon
//     2  the work could not be completed: invalid arguments, the input file
//        does not open, it is not a JPEG (does not start with FFD8), or
//        writing the output failed
//
// SECURITY -- READ BEFORE TOUCHING THIS FILE
//
// This program processes files that arrive from torrents and phones, on a node
// with surface exposed to the internet (06_ACCESO_REMOTO.md). Every offset
// inside the file is a number written by whoever produced THAT file, never
// trusted data. Rules, without exception:
//
//   1. A SINGLE POINT reads raw bytes from the buffer: u8_en(). Every
//      multi-byte read (u16_en/u32_en) is built ON TOP of u8_en; nowhere else
//      in the file indexes buf[i] directly. A new field is composed from these
//      three functions, not by adding a fourth accessor.
//   2. Loops over the entries of an IFD are bounded by MAX_ENTRADAS_IFD, a
//      FIXED compile-time cap, regardless of what the file itself says.
//   3. The IFD chain is NOT followed dynamically. analizar_ifd() is called
//      EXACTLY THREE TIMES, written out by hand in main(): IFD0, the Exif
//      sub-IFD if IFD0 says it exists, and IFD1 if IFD0 points at one. A file
//      whose "next IFD" points at itself cannot produce a loop, because there
//      is no loop following that chain.
//   4. Zero recursion in the whole file.
//   5. Every sum of 32-bit file offsets is done in uint64_t before comparing it
//      with the available size, so that no offset plus a base can overflow a
//      narrower type.
//   6. The thumbnail is capped at MINIATURA_MAX (256 KiB); if the file declares
//      more, it is rejected without reading it.
//   7. Before writing it, it is checked to start with FFD8 and end with FFD9 --
//      what comes out of here must at least have the shape of a JPEG.
//
// What this program does NOT do, on purpose: it decodes not one pixel, it
// allocates no memory proportional to what the file says (every buffer is of
// FIXED size, known at compile time), and it links no library beyond the C
// standard library.

#include <stdint.h>
#include <stdio.h>
#include <string.h>

#define CABECERA_MAX (128u * 1024u)   // techo de lectura del encabezado
#define MINIATURA_MAX (256u * 1024u)  // techo de tamaño de la miniatura
#define MAX_ENTRADAS_IFD 512          // ninguna camara real pasa de unas decenas
#define ASCII_MAX 64                  // marca/modelo/fecha se truncan aqui

static uint8_t cabecera[CABECERA_MAX];
static uint8_t miniatura[MINIATURA_MAX];

// --- el unico punto de acceso a bytes crudos, y lo que se construye encima ---

static int u8_en(const uint8_t *buf, size_t len, size_t off, uint8_t *salida) {
    if (off >= len) return 0;
    *salida = buf[off];
    return 1;
}

static int u16_en(const uint8_t *buf, size_t len, size_t off, int mayor_primero,
                   uint16_t *salida) {
    uint8_t a, b;
    if (!u8_en(buf, len, off, &a) || !u8_en(buf, len, off + 1, &b)) return 0;
    *salida = mayor_primero ? (uint16_t)((uint16_t)a << 8 | b)
                             : (uint16_t)((uint16_t)b << 8 | a);
    return 1;
}

static int u32_en(const uint8_t *buf, size_t len, size_t off, int mayor_primero,
                   uint32_t *salida) {
    uint8_t a, b, c, d;
    if (!u8_en(buf, len, off, &a) || !u8_en(buf, len, off + 1, &b) ||
        !u8_en(buf, len, off + 2, &c) || !u8_en(buf, len, off + 3, &d))
        return 0;
    if (mayor_primero)
        *salida = ((uint32_t)a << 24) | ((uint32_t)b << 16) | ((uint32_t)c << 8) | d;
    else
        *salida = ((uint32_t)d << 24) | ((uint32_t)c << 16) | ((uint32_t)b << 8) | a;
    return 1;
}

// --- los datos que interesan, acumulados a lo largo de hasta tres IFDs ---

typedef struct {
    char marca[ASCII_MAX];
    int tiene_marca;
    char modelo[ASCII_MAX];
    int tiene_modelo;
    uint16_t orientacion;
    int tiene_orientacion;
    uint32_t exif_subifd_rel;  // 0x8769, relativo al inicio del bloque TIFF
    int tiene_exif_subifd;
    char fecha_original[ASCII_MAX];  // formato EXIF crudo "YYYY:MM:DD HH:MM:SS"
    int tiene_fecha_original;
    uint32_t miniatura_off_rel;  // 0x0201, relativo al inicio del bloque TIFF
    int tiene_miniatura_off;
    uint32_t miniatura_len;  // 0x0202
    int tiene_miniatura_len;
} datos_exif;

// Copia una cadena TIFF (tipo ASCII, 2) de como maximo ASCII_MAX-1 bytes en
// 'destino'. 'off_campo' es donde empiezan los 4 bytes de "valor u offset"
// de la entrada. Si count<=4 la cadena vive DENTRO de esos 4 bytes, en el
// mismo orden en que aparecen en el archivo -- el orden de bytes del TIFF
// (mayor_primero) no aplica aqui, solo afecta a NUMEROS multibyte, no a
// secuencias de caracteres. Si count>4, esos 4 bytes son un desplazamiento
// (ese si, un numero) hacia donde vive la cadena de verdad.
static int copiar_ascii(const uint8_t *buf, size_t len, int mayor_primero,
                         size_t tiff_base, uint32_t count, size_t off_campo,
                         char *destino) {
    if (count == 0) return 0;
    size_t n = count;
    if (n > ASCII_MAX - 1) n = ASCII_MAX - 1;  // se trunca, nunca se rechaza

    size_t origen;
    if (count <= 4) {
        origen = off_campo;
    } else {
        uint32_t off_rel;
        if (!u32_en(buf, len, off_campo, mayor_primero, &off_rel)) return 0;
        uint64_t abs = (uint64_t)tiff_base + (uint64_t)off_rel;
        if (abs > (uint64_t)len) return 0;
        origen = (size_t)abs;
    }

    size_t i;
    for (i = 0; i < n; i++) {
        uint8_t b;
        if (!u8_en(buf, len, origen + i, &b)) return 0;
        if (b == 0) break;  // corta en el primer NUL si llega antes de 'n'
        destino[i] = (char)b;
    }
    destino[i] = '\0';
    return 1;
}

// Lee un IFD completo situado en 'ifd_off_rel', relativo al inicio del
// bloque TIFF ('tiff_base', un desplazamiento ABSOLUTO dentro de 'buf').
// Rellena en 'datos' los campos reconocidos que encuentre. Devuelve en
// '*siguiente_rel' el desplazamiente del siguiente IFD en la cadena (0 si
// no hay) -- el LLAMADOR decide si lo sigue; esta funcion nunca se llama a
// si misma, y main() la invoca a mano un maximo de tres veces (regla 3).
static void analizar_ifd(const uint8_t *buf, size_t len, int mayor_primero,
                          size_t tiff_base, uint32_t ifd_off_rel, datos_exif *datos,
                          uint32_t *siguiente_rel) {
    *siguiente_rel = 0;

    uint64_t ifd_abs64 = (uint64_t)tiff_base + (uint64_t)ifd_off_rel;
    if (ifd_abs64 > (uint64_t)len) return;
    size_t ifd_abs = (size_t)ifd_abs64;

    uint16_t n_entradas;
    if (!u16_en(buf, len, ifd_abs, mayor_primero, &n_entradas)) return;
    if (n_entradas > MAX_ENTRADAS_IFD) n_entradas = MAX_ENTRADAS_IFD;  // se acota

    size_t pos = ifd_abs + 2;
    uint16_t i;
    for (i = 0; i < n_entradas; i++) {
        uint16_t tag, tipo;
        uint32_t contador;
        if (!u16_en(buf, len, pos, mayor_primero, &tag)) return;
        if (!u16_en(buf, len, pos + 2, mayor_primero, &tipo)) return;
        if (!u32_en(buf, len, pos + 4, mayor_primero, &contador)) return;
        size_t off_campo = pos + 8;  // los 4 bytes de "valor u offset"

        switch (tag) {
            case 0x010F:  // Make
                if (tipo == 2 && !datos->tiene_marca)
                    datos->tiene_marca = copiar_ascii(buf, len, mayor_primero, tiff_base,
                                                       contador, off_campo, datos->marca);
                break;
            case 0x0110:  // Model
                if (tipo == 2 && !datos->tiene_modelo)
                    datos->tiene_modelo = copiar_ascii(buf, len, mayor_primero, tiff_base,
                                                        contador, off_campo, datos->modelo);
                break;
            case 0x0112: {  // Orientation, SHORT inline
                if (tipo == 3 && !datos->tiene_orientacion) {
                    uint16_t v;
                    if (u16_en(buf, len, off_campo, mayor_primero, &v)) {
                        datos->orientacion = v;
                        datos->tiene_orientacion = 1;
                    }
                }
                break;
            }
            case 0x8769: {  // ExifIFDPointer, LONG inline
                if (tipo == 4 && contador == 1 && !datos->tiene_exif_subifd) {
                    uint32_t v;
                    if (u32_en(buf, len, off_campo, mayor_primero, &v)) {
                        datos->exif_subifd_rel = v;
                        datos->tiene_exif_subifd = 1;
                    }
                }
                break;
            }
            case 0x9003:  // DateTimeOriginal
                if (tipo == 2 && !datos->tiene_fecha_original)
                    datos->tiene_fecha_original =
                        copiar_ascii(buf, len, mayor_primero, tiff_base, contador,
                                     off_campo, datos->fecha_original);
                break;
            case 0x0201: {  // JPEGInterchangeFormat: desplazamiento de la miniatura
                if (tipo == 4 && contador == 1 && !datos->tiene_miniatura_off) {
                    uint32_t v;
                    if (u32_en(buf, len, off_campo, mayor_primero, &v)) {
                        datos->miniatura_off_rel = v;
                        datos->tiene_miniatura_off = 1;
                    }
                }
                break;
            }
            case 0x0202: {  // JPEGInterchangeFormatLength: su tamaño
                if (tipo == 4 && contador == 1 && !datos->tiene_miniatura_len) {
                    uint32_t v;
                    if (u32_en(buf, len, off_campo, mayor_primero, &v)) {
                        datos->miniatura_len = v;
                        datos->tiene_miniatura_len = 1;
                    }
                }
                break;
            }
            default:
                break;
        }
        pos += 12;
    }

    uint32_t sig;
    if (u32_en(buf, len, pos, mayor_primero, &sig)) *siguiente_rel = sig;
}

// Recorre los segmentos JPEG desde justo despues del SOI buscando un APP1
// que contenga un bloque Exif. La longitud de un segmento JPEG SIEMPRE va
// en big-endian -- es una regla del formato JPEG en si, independiente del
// orden de bytes que el TIFF de dentro declare mas adelante.
//
// Devuelve 1 y, en '*tiff_base', el desplazamiento ABSOLUTO donde arranca
// la cabecera TIFF (justo tras "Exif\0\0") si lo encuentra. Devuelve 0 si
// el archivo no tiene Exif (JPEG valido sin metadatos: no es un error) o si
// la estructura de segmentos se sale de 'len' antes de encontrarlo (tampoco
// es un error: simplemente no se pudo seguir leyendo).
static int buscar_app1_exif(const uint8_t *buf, size_t len, size_t *tiff_base) {
    size_t pos = 2;  // justo despues de FFD8
    for (;;) {
        uint8_t marcador_ff, marcador;
        if (!u8_en(buf, len, pos, &marcador_ff)) return 0;
        if (marcador_ff != 0xFF) return 0;  // estructura de segmentos rota
        if (!u8_en(buf, len, pos + 1, &marcador)) return 0;
        pos += 2;

        if (marcador == 0xD9) return 0;  // EOI: fin de imagen, sin Exif
        if (marcador == 0xDA) return 0;  // SOS: empiezan los datos de la imagen
        if (marcador == 0x01 || (marcador >= 0xD0 && marcador <= 0xD7))
            continue;  // TEM / RSTn: sin campo de longitud, sin datos

        uint16_t seglen;
        if (!u16_en(buf, len, pos, 1, &seglen)) return 0;
        if (seglen < 2) return 0;  // ilegal: no podria ni contenerse a si misma

        if (marcador == 0xE1) {  // APP1: puede ser Exif, o puede ser otra cosa (XMP...)
            size_t datos_off = pos + 2;
            uint8_t firma[6];
            int hay_firma = 1;
            for (int j = 0; j < 6; j++) {
                if (!u8_en(buf, len, datos_off + (size_t)j, &firma[j])) {
                    hay_firma = 0;
                    break;
                }
            }
            if (hay_firma && memcmp(firma, "Exif\0\0", 6) == 0) {
                *tiff_base = datos_off + 6;
                return 1;
            }
        }

        // pos y seglen caben ambos en ~196 KB (CABECERA_MAX + 65535): no
        // hace falta aritmetica de 64 bits aqui, a diferencia de los
        // desplazamientos EXIF, que si vienen del archivo en 32 bits.
        pos += seglen;
    }
}

// Convierte "YYYY:MM:DD HH:MM:SS" (formato EXIF, 19 caracteres exactos) a
// "YYYY-MM-DDTHH:MM:SS" (ISO 8601). No corrige ni adivina: si la cadena no
// tiene exactamente esta forma, se rechaza -- una fecha mal formada no
// merece arriesgarse a malinterpretarla.
static int convertir_fecha(const char *cruda, char *salida /* min. 20 bytes */) {
    size_t n = strlen(cruda);
    if (n != 19) return 0;
    if (cruda[4] != ':' || cruda[7] != ':' || cruda[10] != ' ' || cruda[13] != ':' ||
        cruda[16] != ':')
        return 0;
    for (size_t i = 0; i < 19; i++) {
        if (i == 4 || i == 7 || i == 10 || i == 13 || i == 16) continue;
        if (cruda[i] < '0' || cruda[i] > '9') return 0;
    }
    memcpy(salida, cruda, 19);
    salida[4] = '-';
    salida[7] = '-';
    salida[10] = 'T';
    salida[19] = '\0';
    return 1;
}

int main(int argc, char **argv) {
    if (argc != 3) {
        fprintf(stderr, "uso: %s <archivo-entrada> <archivo-salida>\n",
                argc > 0 ? argv[0] : "nas-miniatura");
        return 2;
    }
    const char *ruta_entrada = argv[1];
    const char *ruta_salida = argv[2];

    FILE *f = fopen(ruta_entrada, "rb");
    if (!f) {
        fprintf(stderr, "no se pudo abrir %s\n", ruta_entrada);
        return 2;
    }

    if (fseek(f, 0, SEEK_END) != 0) {
        fclose(f);
        return 2;
    }
    long tam_l = ftell(f);
    if (tam_l < 0) {
        fclose(f);
        return 2;
    }
    // 'long' basta aqui: este programa procesa fotos, no video, y en el
    // destino de produccion (linux/arm64) 'long' ya es de 64 bits. En una
    // compilacion nativa de desarrollo en Windows (32 bits) el limite
    // teorico serian ~2 GB, muy por encima de cualquier foto real.
    uint64_t tam_archivo = (uint64_t)tam_l;
    if (fseek(f, 0, SEEK_SET) != 0) {
        fclose(f);
        return 2;
    }

    size_t len = fread(cabecera, 1, CABECERA_MAX, f);
    if (len < 4 || cabecera[0] != 0xFF || cabecera[1] != 0xD8) {
        fclose(f);
        fprintf(stderr, "no es un JPEG (falta SOI)\n");
        return 2;
    }

    size_t tiff_base;
    if (!buscar_app1_exif(cabecera, len, &tiff_base)) {
        fclose(f);
        return 1;  // JPEG valido, sin Exif aprovechable: no es un error
    }

    uint8_t b0, b1;
    if (!u8_en(cabecera, len, tiff_base, &b0) ||
        !u8_en(cabecera, len, tiff_base + 1, &b1)) {
        fclose(f);
        return 1;
    }
    int mayor_primero;
    if (b0 == 'I' && b1 == 'I')
        mayor_primero = 0;
    else if (b0 == 'M' && b1 == 'M')
        mayor_primero = 1;
    else {
        fclose(f);
        return 1;  // cabecera TIFF ilegible: no es Exif de verdad
    }

    uint16_t magia;
    if (!u16_en(cabecera, len, tiff_base + 2, mayor_primero, &magia) || magia != 42) {
        fclose(f);
        return 1;
    }
    uint32_t ifd0_off;
    if (!u32_en(cabecera, len, tiff_base + 4, mayor_primero, &ifd0_off)) {
        fclose(f);
        return 1;
    }

    datos_exif datos;
    memset(&datos, 0, sizeof(datos));
    uint32_t siguiente = 0;

    // Llamada 1 de 3, EXPLICITA: IFD0 -- marca, modelo, orientacion, y los
    // punteros a las otras dos zonas (sub-IFD de Exif e IFD1 de miniatura).
    analizar_ifd(cabecera, len, mayor_primero, tiff_base, ifd0_off, &datos, &siguiente);
    uint32_t ifd1_off = siguiente;

    // Llamada 2 de 3: el sub-IFD de Exif, SOLO si IFD0 dijo que existe.
    if (datos.tiene_exif_subifd) {
        uint32_t descartar;
        analizar_ifd(cabecera, len, mayor_primero, tiff_base, datos.exif_subifd_rel,
                     &datos, &descartar);
    }

    // Llamada 3 de 3: IFD1 -- ahi viven los punteros a la miniatura.
    if (ifd1_off != 0) {
        uint32_t descartar;
        analizar_ifd(cabecera, len, mayor_primero, tiff_base, ifd1_off, &datos, &descartar);
    }

    if (datos.tiene_fecha_original) {
        char iso[20];
        if (convertir_fecha(datos.fecha_original, iso)) printf("fecha=%s\n", iso);
    }
    if (datos.tiene_orientacion) printf("orientacion=%u\n", (unsigned)datos.orientacion);
    if (datos.tiene_marca) printf("marca=%s\n", datos.marca);
    if (datos.tiene_modelo) printf("modelo=%s\n", datos.modelo);

    int tiene_miniatura_ptr = datos.tiene_miniatura_off && datos.tiene_miniatura_len;
    if (!tiene_miniatura_ptr || datos.miniatura_len == 0) {
        fclose(f);
        return 1;  // hay Exif, pero sin miniatura: legitimo
    }
    if (datos.miniatura_len > MINIATURA_MAX) {
        fclose(f);
        return 1;  // se declara mas grande de lo razonable: no se confia
    }

    uint64_t inicio64 = (uint64_t)tiff_base + (uint64_t)datos.miniatura_off_rel;
    uint64_t fin64 = inicio64 + (uint64_t)datos.miniatura_len;
    if (fin64 < inicio64 || fin64 > tam_archivo) {  // fin64<inicio64: desbordo
        fclose(f);
        return 1;
    }

    size_t n;
    if (fin64 <= (uint64_t)len) {
        // La miniatura cae entera dentro de lo que ya se leyo.
        memcpy(miniatura, cabecera + (size_t)inicio64, (size_t)datos.miniatura_len);
        n = (size_t)datos.miniatura_len;
        fclose(f);
    } else {
        // Cae mas alla del encabezado leido (raro, pero no se descarta sin
        // comprobarlo): se relee esa region concreta directamente del
        // archivo, acotada al mismo MINIATURA_MAX de siempre.
        if (fseek(f, (long)inicio64, SEEK_SET) != 0) {
            fclose(f);
            return 1;
        }
        n = fread(miniatura, 1, (size_t)datos.miniatura_len, f);
        fclose(f);
        if (n != (size_t)datos.miniatura_len) return 1;
    }

    if (n < 4 || miniatura[0] != 0xFF || miniatura[1] != 0xD8 || miniatura[n - 2] != 0xFF ||
        miniatura[n - 1] != 0xD9) {
        return 1;  // lo que hay ahi no tiene forma de JPEG: no se confia
    }

    FILE *out = fopen(ruta_salida, "wb");
    if (!out) return 2;
    size_t escrito = fwrite(miniatura, 1, n, out);
    int cerro_bien = (fclose(out) == 0);
    if (escrito != n || !cerro_bien) return 2;

    return 0;
}
