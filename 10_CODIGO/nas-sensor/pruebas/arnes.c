// arnes -- portable entry point to exercise analizar_paquete under sanitizers.
//
// It links the SAME analisis.c the sensor uses, but with no socket and no Linux
// headers, so it builds and runs on the owner's PC -- which is Windows -- with
// ASan and UBSan on. Without this, checking the corpus would have to happen on
// the node or in WSL, which means: in practice it would not happen.
//
//   arnes <file-with-one-packet> <4|6>
//
//   0  it was a touch, and what was extracted is printed
//   1  it was not a touch. LEGITIMATE case, not an error
//   2  invalid arguments, or the file does not open
//
//   arnes --historial <history-file>
//
//   Exercises leer_toque and leer_total over a whole file: prints one line per
//   touch that could be read and ends with "total=N leidos=M".
//   0 if the file opens, 2 if not.

#include "../analisis.h"

#include <stdio.h>
#include <string.h>

#define PAQUETE_MAX 256u
#define LINEA_MAX 128u

// historial recorre el archivo como lo hace el sensor al arrancar.
//
// EL BUCLE ESTA REPETIDO AQUI A PROPOSITO, y no es un descuido que haya que
// unificar: lo que hay que probar con desinfectantes es leer_toque y
// leer_total, que analizan bytes que un corte de luz pudo dejar a medias. El
// bucle de fgets del sensor no se puede compartir porque el suyo llama a
// anotar(), que toca el anillo estatico -- y traerse ese anillo aqui seria
// meter en analisis.c el estado que lo haria dejar de ser portable. Se comparte
// lo peligroso y se repiten seis lineas triviales, que es el mismo reparto que
// ya gobierna analisis.c frente a sensor.c.
static int historial(const char *ruta) {
  FILE *f = fopen(ruta, "r");
  if (f == NULL) {
    fprintf(stderr, "arnes: no se pudo abrir %s\n", ruta);
    return 2;
  }
  char linea[LINEA_MAX];
  unsigned long long total = 0;
  unsigned leidos = 0;
  while (fgets(linea, sizeof(linea), f) != NULL) {
    if (linea[0] == '#') {
      leer_total(linea, &total);
      continue;
    }
    struct toque t;
    if (leer_toque(linea, &t)) {
      printf("toque momento=%lld origen=%s puerto=%u tipo=%s\n",
             (long long)t.momento, t.origen, (unsigned)t.puerto,
             t.tipo == TIPO_PING ? "ping" : "syn");
      leidos++;
    }
  }
  fclose(f);
  printf("total=%llu leidos=%u\n", total, leidos);
  return 0;
}

int main(int argc, char **argv) {
  if (argc == 3 && strcmp(argv[1], "--historial") == 0) {
    return historial(argv[2]);
  }
  if (argc != 3) {
    fprintf(stderr, "uso: arnes <archivo-con-un-paquete> <4|6>\n");
    fprintf(stderr, "     arnes --historial <archivo-de-historial>\n");
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
