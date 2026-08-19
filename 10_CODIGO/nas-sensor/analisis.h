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

#endif
