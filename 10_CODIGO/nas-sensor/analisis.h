#ifndef ANALISIS_H
#define ANALISIS_H

#include <stdint.h>
#include <stddef.h>

#define DIR_MAX 46u  // lo que ocupa la forma larga de una IPv6, con su nulo

#define TIPO_SYN 1
#define TIPO_PING 2

#define FAMILIA_IPV4 0x0800u
#define FAMILIA_IPV6 0x86DDu

struct toque {
  int64_t momento;      // segundos desde la epoca; lo pone quien captura
  char origen[DIR_MAX]; // ya formateada, para no reformatearla al volcar
  uint16_t puerto;      // 0 en un ping: no hay puerto, y el panel lo dice
  uint8_t tipo;         // TIPO_SYN o TIPO_PING
};

int analizar_paquete(const uint8_t *b, size_t n, uint16_t familia,
                     struct toque *t);
// salta, y el resto del historial se conserva.
int leer_toque(const char *linea, struct toque *t);

int leer_total(const char *linea, unsigned long long *total);

// direccion_privada dice si un origen YA FORMATEADO es de un rango que nunca
// llega por Internet: RFC 1918, bucle local y enlace local en IPv4; bucle
// local, enlace local (fe80::/10) y ULA (fc00::/7) en IPv6. Es un hecho del
// protocolo, no de esta casa: el IPv6 publico de casa NO cuenta aqui, porque
// ese prefijo solo lo conoce nasd. Decide el anillo del toque (ADR-0101).
// Devuelve 1 si es privada; 0 si no, o si el texto no se reconoce.
int direccion_privada(const char *origen);

#endif
