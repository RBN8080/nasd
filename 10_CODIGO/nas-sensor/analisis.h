// analisis -- decidir si unos bytes son un TOQUE, y sacar de quien.
//
// ESTA MITAD ESTA SEPARADA DEL SENSOR A PROPOSITO, y no es una manía de
// organizacion: es lo que hace que la parte hostil se pueda probar.
//
// sensor.c abre un socket AF_PACKET y necesita cabeceras de Linux, asi que en
// el PC del responsable -- que es Windows -- no compila, y por tanto tampoco
// se puede ejecutar con ASan y UBSan. Este archivo no incluye nada mas que
// stdint y string: compila y corre en cualquier sitio, con desinfectantes, y
// es justo donde vive el codigo que analiza bytes que escribio un extrano.
//
// Sin esta separacion, la comprobacion con desinfectantes del corpus habria
// que hacerla en el nodo o en WSL -- es decir, en la practica no se haria.

#ifndef ANALISIS_H
#define ANALISIS_H

#include <stdint.h>
#include <stddef.h>

#define DIR_MAX 46u  // lo que ocupa la forma larga de una IPv6, con su nulo

#define TIPO_SYN 1
#define TIPO_PING 2

// Los dos valores de EtherType que interesan. Se definen AQUI y no se toman de
// <linux/if_ether.h> para que este archivo no dependa de Linux: son numeros
// fijos de la IANA, no una particularidad del nucleo.
#define FAMILIA_IPV4 0x0800u
#define FAMILIA_IPV6 0x86DDu

struct toque {
  int64_t momento;      // segundos desde la epoca; lo pone quien captura
  char origen[DIR_MAX]; // ya formateada, para no reformatearla al volcar
  uint16_t puerto;      // 0 en un ping: no hay puerto, y el panel lo dice
  uint8_t tipo;         // TIPO_SYN o TIPO_PING
};

// analizar_paquete devuelve 1 si los bytes son un toque y rellena *t.
//
// familia debe venir del NUCLEO (sll_protocol), nunca deducirse del contenido:
// es el unico dato de esta funcion que no escribio quien envio el paquete.
//
// Devuelve 0 para todo lo demas. Un paquete truncado, con una cabecera
// imposible, fragmentado o de un protocolo que no miramos simplemente NO es un
// toque: no es un error y no hay ningun camino que lo trate como tal.
int analizar_paquete(const uint8_t *b, size_t n, uint16_t familia,
                     struct toque *t);

// leer_toque analiza UNA linea del historial y devuelve 1 si describe un toque.
//
// POR QUE ESTA AQUI Y NO EN sensor.c, que es quien abre el archivo: por el
// mismo motivo que analizar_paquete. El anillo del sensor vivia SOLO en
// memoria, asi que cada arranque -- y cada «make desplegar», que reinicia el
// servicio -- empezaba en blanco y a los 60 s pisaba el archivo. Releerlo
// obliga a analizar bytes de un archivo que un corte de luz pudo dejar a
// medias, y eso es exactamente lo que esta mitad existe para poder probar con
// desinfectantes. Las cinco reglas de analisis.c se aplican igual.
//
// Devuelve 0 para una cabecera, una linea vacia, truncada, con un campo fuera
// de rango o con una fecha imposible. Una linea ilegible NO es un error: se
// salta, y el resto del historial se conserva.
int leer_toque(const char *linea, struct toque *t);

// leer_total saca la cuenta de la cabecera «# total-visto: N».
//
// Se relee aparte de las lineas porque cuenta algo distinto: los toques
// CAPTURADOS desde siempre, que son mas de los que caben en el anillo. Es la
// unica forma de comprobar que la captura funciona sin trafico de Internet, y
// se ponia a cero en cada arranque junto con todo lo demas.
//
// Devuelve 1 si la linea era esa cabecera y el numero cabe; 0 en lo demas.
int leer_total(const char *linea, unsigned long long *total);

#endif
