// analisis -- see analisis.h for why this half is kept separate.
//
// SECURITY -- READ BEFORE TOUCHING THIS FILE
//
// This parses bytes written by whoever sent the packet, in a process running
// with CAP_NET_RAW. Every offset comes from a field a stranger put there.
// Rules, without exception, and they are the same ones that govern
// nas-miniatura:
//
//   1. A SINGLE POINT reads raw bytes: u8_en(). Every multi-byte read is built
//      on top of it. Nowhere else in this file indexes b[i].
//   2. Zero dynamic allocation. There is no malloc, calloc or realloc, and
//      there must not be: every buffer is of fixed compile-time size.
//   3. Zero recursion.
//   4. The IP header length is written by the sender: it is checked against the
//      protocol minimum AND against what was actually received BEFORE being
//      used as an offset.
//   5. Only headers are read. The packet body is never touched.

#include "analisis.h"

#include <string.h>

// u8_en es el UNICO sitio que lee un byte del paquete. Si el desplazamiento
// cae fuera de lo recibido marca el fallo y devuelve 0; quien llama comprueba
// *ok, nunca el valor devuelto.
static uint8_t u8_en(const uint8_t *b, size_t n, size_t i, int *ok) {
  if (i >= n) {
    *ok = 0;
    return 0;
  }
  return b[i];
}

static uint16_t u16_en(const uint8_t *b, size_t n, size_t i, int *ok) {
  uint16_t alto = u8_en(b, n, i, ok);
  uint16_t bajo = u8_en(b, n, i + 1u, ok);
  return (uint16_t)((alto << 8) | bajo);
}

// escribir_hex4 pone un grupo IPv6 en minusculas y sin ceros a la izquierda.
// Devuelve cuantos caracteres escribio.
static size_t escribir_hex4(char *dst, uint16_t v) {
  static const char digitos[] = "0123456789abcdef";
  char tmp[4];
  size_t n = 0;
  if (v == 0) {
    dst[0] = '0';
    return 1;
  }
  while (v != 0 && n < 4) {
    tmp[n++] = digitos[v & 0x0fu];
    v = (uint16_t)(v >> 4);
  }
  for (size_t i = 0; i < n; i++) {
    dst[i] = tmp[n - 1u - i];
  }
  return n;
}

static size_t escribir_u8(char *dst, uint8_t v) {
  size_t n = 0;
  if (v >= 100u) {
    dst[n++] = (char)('0' + v / 100u);
  }
  if (v >= 10u) {
    dst[n++] = (char)('0' + (v / 10u) % 10u);
  }
  dst[n++] = (char)('0' + v % 10u);
  return n;
}

static int formatear_ipv4(const uint8_t *b, size_t n, size_t base, char *dst) {
  int ok = 1;
  uint8_t o[4];
  for (size_t i = 0; i < 4u; i++) {
    o[i] = u8_en(b, n, base + i, &ok);
  }
  if (!ok) {
    return 0;
  }
  size_t p = 0;
  for (size_t i = 0; i < 4u; i++) {
    if (i > 0) {
      dst[p++] = '.';
    }
    p += escribir_u8(dst + p, o[i]);
  }
  dst[p] = '\0';
  return 1;
}

static int formatear_ipv6(const uint8_t *b, size_t n, size_t base, char *dst) {
  int ok = 1;
  uint16_t g[8];
  for (size_t i = 0; i < 8u; i++) {
    g[i] = u16_en(b, n, base + i * 2u, &ok);
  }
  if (!ok) {
    return 0;
  }
  size_t p = 0;
  for (size_t i = 0; i < 8u; i++) {
    if (i > 0) {
      dst[p++] = ':';
    }
    p += escribir_hex4(dst + p, g[i]);
  }
  dst[p] = '\0';
  return 1;
}

// toque_tcp mira las banderas y saca el puerto destino.
//
// SYN PUESTO Y ACK QUITADO es la definicion entera de «alguien INICIA algo
// contra mi». Un SYN+ACK es la respuesta a algo que pedimos nosotros y un ACK
// suelto es trafico ya establecido: ninguno de los dos es un toque, y anotarlos
// llenaria el anillo de nuestro propio trafico.
static int toque_tcp(const uint8_t *b, size_t n, size_t base, struct toque *t) {
  int ok = 1;
  uint16_t puerto = u16_en(b, n, base + 2u, &ok);
  uint8_t banderas = u8_en(b, n, base + 13u, &ok);
  if (!ok || (banderas & 0x12u) != 0x02u) {
    return 0;
  }
  t->puerto = puerto;
  t->tipo = TIPO_SYN;
  return 1;
}

int analizar_paquete(const uint8_t *b, size_t n, uint16_t familia,
                     struct toque *t) {
  int ok = 1;
  memset(t, 0, sizeof(*t));

  if (familia == FAMILIA_IPV4) {
    uint8_t v_ihl = u8_en(b, n, 0, &ok);
    if (!ok || (v_ihl >> 4) != 4u) {
      return 0;
    }

    size_t ihl = (size_t)(v_ihl & 0x0fu) * 4u;
    if (ihl < 20u || ihl > n) {
      return 0;
    }
    // Un fragmento que no es el primero NO lleva cabecera de transporte: leer
    // ahi las banderas de TCP seria interpretar datos como si fueran cabecera.
    uint16_t frag = u16_en(b, n, 6u, &ok);
    if (!ok || (frag & 0x1fffu) != 0u) {
      return 0;
    }
    uint8_t protocolo = u8_en(b, n, 9u, &ok);
    if (!ok || !formatear_ipv4(b, n, 12u, t->origen)) {
      return 0;
    }
    if (protocolo == 6u) {
      return toque_tcp(b, n, ihl, t);
    }
    if (protocolo == 1u) {
      uint8_t tipo = u8_en(b, n, ihl, &ok);
      if (!ok || tipo != 8u) {  // 8 = echo request
        return 0;
      }
      t->tipo = TIPO_PING;
      return 1;
    }
    return 0;
  }

  if (familia == FAMILIA_IPV6) {
    uint8_t version = u8_en(b, n, 0, &ok);
    if (!ok || (version >> 4) != 6u) {
      return 0;
    }
    uint8_t siguiente_cab = u8_en(b, n, 6u, &ok);
    if (!ok || !formatear_ipv6(b, n, 8u, t->origen)) {
      return 0;
    }
    // LA CADENA DE CABECERAS DE EXTENSION NO SE SIGUE, y es la misma decision
    // que nas-miniatura tomo con la cadena de IFDs: recorrerla exige un bucle
    // sobre longitudes que escribe el emisor. Un paquete con extensiones no se
    // anota. Es una PERDIDA conocida, nunca un dato falso, y un escaner tipico
    // no las usa.
    if (siguiente_cab == 6u) {
      return toque_tcp(b, n, 40u, t);
    }
    if (siguiente_cab == 58u) {
      uint8_t tipo = u8_en(b, n, 40u, &ok);
      if (!ok || tipo != 128u) {  // 128 = echo request de ICMPv6
        return 0;
      }
      t->tipo = TIPO_PING;
      return 1;
    }
    return 0;
  }

  return 0;
}

#include <limits.h>

// LARGO_INSTANTE es lo que ocupa «2026-09-04T03:36:51Z», que es lo que escribe
// strftime con «%Y-%m-%dT%H:%M:%SZ» en volcar().
#define LARGO_INSTANTE 20u

// digitos lee EXACTAMENTE cuantos digitos decimales y los deja en *out.
//
// No puede pasarse del final de la cadena: el nulo no esta entre '0' y '9', asi
// que corta el bucle igual que cualquier otro caracter que no toque.
static int digitos(const char *s, size_t cuantos, unsigned *out) {
  unsigned v = 0;
  for (size_t i = 0; i < cuantos; i++) {
    char c = s[i];
    if (c < '0' || c > '9') {
      return 0;
    }
    v = v * 10u + (unsigned)(c - '0');
  }
  *out = v;
  return 1;
}

static unsigned dias_del_mes(unsigned a, unsigned m) {
  static const unsigned tabla[12] = {31u, 28u, 31u, 30u, 31u, 30u,
                                     31u, 31u, 30u, 31u, 30u, 31u};
  if (m == 2u && ((a % 4u == 0u && a % 100u != 0u) || a % 400u == 0u)) {
    return 29u;
  }
  return tabla[m - 1u];
}

static int64_t dias_desde_epoca(unsigned a, unsigned m, unsigned d) {
  int64_t y = (int64_t)a - (m <= 2u ? 1 : 0);
  int64_t era = (y >= 0 ? y : y - 399) / 400;
  int64_t yoe = y - era * 400;                        // [0, 399]
  int64_t mp = (int64_t)((m + 9u) % 12u);             // marzo = 0
  int64_t doy = (153 * mp + 2) / 5 + (int64_t)d - 1;  // [0, 365]
  int64_t doe = yoe * 365 + yoe / 4 - yoe / 100 + doy;
  return era * 146097 + doe - 719468;
}

// fin_de_campo acepta el nulo y el salto de linea en sus dos formas: el archivo
// lo escribe este programa con «\n», pero puede haber pasado por un editor.
static int fin_de_campo(char c) { return c == '\0' || c == '\n' || c == '\r'; }

int leer_toque(const char *linea, struct toque *t) {
  if (linea == NULL || t == NULL || linea[0] == '#') {
    return 0;
  }

  unsigned a, m, d, hh, mm, ss;
  if (!digitos(linea, 4u, &a) || linea[4] != '-' ||
      !digitos(linea + 5u, 2u, &m) || linea[7] != '-' ||
      !digitos(linea + 8u, 2u, &d) || linea[10] != 'T' ||
      !digitos(linea + 11u, 2u, &hh) || linea[13] != ':' ||
      !digitos(linea + 14u, 2u, &mm) || linea[16] != ':' ||
      !digitos(linea + 17u, 2u, &ss) || linea[19] != 'Z' ||
      linea[LARGO_INSTANTE] != ' ') {
    return 0;
  }
  // Se rechaza una fecha imposible en vez de convertirla: «2026-02-31» daria un
  // instante que no corresponde a ningun dia, y el panel lo pintaria como si
  // fuera cierto. El segundo 60 SI se acepta -- existe, es el intercalar.
  if (a < 1970u || m < 1u || m > 12u || d < 1u || d > dias_del_mes(a, m) ||
      hh > 23u || mm > 59u || ss > 60u) {
    return 0;
  }

  // El origen: hasta el siguiente espacio, y nunca mas de lo que cabe en el
  // campo. Una direccion mas larga que DIR_MAX no se recorta -- se descarta la
  // linea entera: media direccion es un dato falso, y eso no se hace.
  const char *p = linea + LARGO_INSTANTE + 1u;
  size_t i = 0;
  while (p[i] != '\0' && p[i] != ' ' && i < DIR_MAX - 1u) {
    i++;
  }
  if (i == 0u || p[i] != ' ') {
    return 0;
  }
  memcpy(t->origen, p, i);
  t->origen[i] = '\0';

  p += i + 1u;
  unsigned puerto = 0;
  i = 0;
  while (p[i] >= '0' && p[i] <= '9' && i < 5u) {
    puerto = puerto * 10u + (unsigned)(p[i] - '0');
    i++;
  }
  if (i == 0u || p[i] != ' ' || puerto > 65535u) {
    return 0;
  }

  p += i + 1u;
  if (strncmp(p, "syn", 3u) == 0 && fin_de_campo(p[3])) {
    t->tipo = TIPO_SYN;
  } else if (strncmp(p, "ping", 4u) == 0 && fin_de_campo(p[4])) {
    t->tipo = TIPO_PING;
  } else {
    return 0;
  }

  t->momento = dias_desde_epoca(a, m, d) * 86400 + (int64_t)hh * 3600 +
               (int64_t)mm * 60 + (int64_t)ss;
  t->puerto = (uint16_t)puerto;
  return 1;
}

int leer_total(const char *linea, unsigned long long *total) {
  static const char marca[] = "# total-visto: ";
  const size_t largo = sizeof(marca) - 1u;
  if (linea == NULL || total == NULL || strncmp(linea, marca, largo) != 0) {
    return 0;
  }
  const char *p = linea + largo;
  unsigned long long v = 0;
  size_t i = 0;
  while (p[i] >= '0' && p[i] <= '9') {
    unsigned d = (unsigned)(p[i] - '0');
    // Se comprueba ANTES de multiplicar. Un desbordamiento de unsigned long
    // long no es comportamiento indefinido, pero daria una cuenta falsa, y una
    // cifra que dice ser «todo lo visto» tiene que ser cierta o no estar.
    if (v > (ULLONG_MAX - d) / 10ULL) {
      return 0;
    }
    v = v * 10ULL + d;
    i++;
  }
  if (i == 0u) {
    return 0;
  }
  *total = v;
  return 1;
}
