// analisis -- ver analisis.h para por que esta mitad va separada.
//
// SEGURIDAD -- LEASE ANTES DE TOCAR ESTE ARCHIVO
//
// Esto analiza bytes que escribio quien envio el paquete, en un proceso que
// corre con CAP_NET_RAW. Todo desplazamiento sale de un campo que puso un
// extrano. Reglas, sin excepcion, y son las mismas que gobiernan
// nas-miniatura:
//
//   1. Un UNICO PUNTO lee bytes crudos: u8_en(). Toda lectura multi-byte esta
//      construida encima. Ningun otro sitio de este archivo hace b[i].
//   2. Cero asignacion dinamica. No hay malloc, calloc ni realloc, y no debe
//      haberlos: todos los buferes son de tamano fijo en tiempo de compilacion.
//   3. Cero recursion.
//   4. La longitud de cabecera IP la escribe el emisor: se comprueba contra el
//      minimo del protocolo Y contra lo que de verdad se recibio ANTES de
//      usarla como desplazamiento.
//   5. Solo se leen cabeceras. El cuerpo del paquete no se toca nunca.

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

// Las dos funciones de formato se escriben a mano en vez de usar inet_ntop
// POR PORTABILIDAD, que es lo que permite ejercitar este archivo con
// desinfectantes en el PC del responsable: inet_ntop vive en <arpa/inet.h> y
// ahi no existe.
//
// La forma IPv6 sale LARGA, sin la abreviatura "::". Es deliberado: quien la
// lee es netip.ParseAddr en Go, que la acepta igual, y ahorrarse las reglas de
// compresion ahorra tambien sus casos borde. El panel la ensena ya canonica,
// porque netip.Addr.String() la vuelve a componer al pintar.

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
    // LA LONGITUD DE CABECERA LA ESCRIBE EL EMISOR. Se comprueba contra el
    // minimo del protocolo Y contra lo recibido antes de usarla para nada.
    //
    // «ihl > n» es DEFENSA EN PROFUNDIDAD, y conviene decirlo en vez de
    // presumir: es demostrablemente redundante con u8_en, porque ihl > n
    // implica ihl+13 > n y el accesor acotado ya rechaza esa lectura. La
    // prueba de mutacion lo confirma -- quitarlo no rompe ni un caso del
    // corpus. Se queda porque rechazar temprano y de forma explicita vale mas
    // que ahorrar una comparacion en un analizador de entrada hostil, pero
    // quien lo lea debe saber que el guardia de verdad es u8_en.
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
