// arnes -- entrada portable para ejercitar analizar_paquete con desinfectantes.
//
// Enlaza el MISMO analisis.c que usa el sensor, pero sin socket ni cabeceras de
// Linux, asi que compila y corre en el PC del responsable -- que es Windows --
// con ASan y UBSan puestos. Sin esto, la comprobacion del corpus habria que
// hacerla en el nodo o en WSL, es decir: en la practica no se haria.
//
//   arnes <archivo-con-un-paquete> <4|6>
//
//   0  era un toque, y se imprime lo que se saco
//   1  no era un toque. Caso LEGITIMO, no un error
//   2  argumentos invalidos o el archivo no abre

#include "../analisis.h"

#include <stdio.h>
#include <string.h>

#define PAQUETE_MAX 256u

int main(int argc, char **argv) {
  if (argc != 3) {
    fprintf(stderr, "uso: arnes <archivo-con-un-paquete> <4|6>\n");
    return 2;
  }
  uint16_t familia;
  if (strcmp(argv[2], "4") == 0) {
    familia = FAMILIA_IPV4;
  } else if (strcmp(argv[2], "6") == 0) {
    familia = FAMILIA_IPV6;
  } else {
    fprintf(stderr, "arnes: la familia debe ser 4 o 6\n");
    return 2;
  }

  FILE *f = fopen(argv[1], "rb");
  if (f == NULL) {
    fprintf(stderr, "arnes: no se pudo abrir %s\n", argv[1]);
    return 2;
  }
  unsigned char buf[PAQUETE_MAX];
  size_t n = fread(buf, 1, sizeof(buf), f);
  fclose(f);

  struct toque t;
  if (!analizar_paquete(buf, n, familia, &t)) {
    printf("no-es-un-toque\n");
    return 1;
  }
  printf("origen=%s puerto=%u tipo=%s\n", t.origen, (unsigned)t.puerto,
         t.tipo == TIPO_PING ? "ping" : "syn");
  return 0;
}
