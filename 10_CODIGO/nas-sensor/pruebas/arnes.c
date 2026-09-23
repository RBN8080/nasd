#include "../analisis.h"

#include <stdio.h>
#include <string.h>

#define PAQUETE_MAX 256u
#define LINEA_MAX 128u

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
