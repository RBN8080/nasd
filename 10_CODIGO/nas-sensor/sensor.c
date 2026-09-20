// nas-sensor -- records who TOUCHES the node, even when the firewall drops it.
//
// WHY IT EXISTS: the security panel sees rejected HTTP requests (ADR-0061) and
// accepted TCP connections (ADR-0064). Both live INSIDE nasd, so they only
// exist if the packet reached port 80 or 443. Everything the firewall drops --
// a scanner sweeping closed ports -- nobody sees.
//
// WHY IT IS NOT AN nftables "counter", WHICH WAS THE PLAN (IDEAS §22.2).
// Measured against the node on 2026-08-18, and the three reasons are
// independent:
//
//   1. 15 boots in 14 days. A kernel counter dies on each one, because
//      nftables.service reloads a file starting with "flush ruleset". A week of
//      counting would have been "since the last power cut", with no warning
//      that it had restarted.
//   2. ~57,000 packets/day of household broadcast against 0 of IPv6 radiation
//      in the measured window. The figure driving the line of investigation
//      would have been a measurement of the devices in the living room.
//   3. Both DRIFTNET sweeps came in over 443, which is in "accept". A counter
//      on the "drop" path would NOT have seen the only real external traffic
//      this node has ever received.
//
// Besides, the owner asked that this not touch what already works. This program
// touches neither the firewall nor the nasd unit: it listens in parallel.
//
// WHAT IT RECORDS: see analisis.h. In short, a TOUCH is someone INITIATING
// something -- a TCP SYN without ACK, or an echo request. UDP is not recorded,
// and that is a DECLARED blind spot: without conntrack a probe cannot be told
// apart from the answer to a query the node itself made.
//
// WHAT THIS PROGRAM DOES NOT DECIDE, AND IT IS DELIBERATE
//
// It does not know what "the internet" is and does not need to. It records
// EVERYTHING it captures, wherever it comes from; what discards local traffic
// is nasd when painting, with seguridad.ClasificarRed -- the same function that
// already fixed two real defects (the household IPv6 counted as a stranger, and
// fe80::...%2 because of the zone). Having that logic in ONE place is worth
// more than saving lines in this file: here it would have to be reimplemented,
// and with it its history of defects. Accepted consequence: the file contains
// the odd local row.
//
// INTERFACE
//
//   nas-sensor <output-file>
//
//   Captures until SIGTERM or SIGINT. Flushes every 60 s and on exit.
//   Exit code: 0 if it finished cleanly, 2 if it could not work.
//
// To analyse a single packet by hand there is pruebas/arnes.c, which links the
// SAME analisis.c. There is no "--analizar" mode here because it would be that
// same code written twice.
//
// WHAT THIS PROGRAM DOES NOT DO: it does not write to the network, opens no
// socket beyond the capture one, executes nothing, allocates no dynamic memory
// at any point, and links no library beyond the C standard one.

// -std=c11 is STRICT ISO mode: without this macro, sigaction, gmtime_r and
// fileno stay hidden and the compiler treats them as implicit -- which with
// -Werror is an error, and without it would be a silent link-time failure. It
// goes before any include, which is the only thing that makes it effective.

#define _POSIX_C_SOURCE 200809L

#include "analisis.h"

#include <errno.h>
#include <linux/filter.h>
#include <linux/if_ether.h>
#include <linux/if_packet.h>
#include <netinet/in.h>
#include <poll.h>
#include <signal.h>
#include <stdint.h>
#include <stdio.h>
#include <string.h>
#include <sys/socket.h>
#include <time.h>
#include <unistd.h>

#define CAPACIDAD 2000u   // toques que caben, el mismo tope que los anillos de Go
#define PAQUETE_MAX 256u  // solo hacen falta cabeceras; el cuerpo no se lee
#define RUTA_MAX 512u
#define SEGUNDOS_VOLCADO 60

static struct toque anillo[CAPACIDAD];
static unsigned siguiente;

static unsigned poblados;

static unsigned long long total;

static volatile sig_atomic_t hay_que_salir;

static void al_recibir_senal(int s) {
  (void)s;
  hay_que_salir = 1;
}

static void anotar(const struct toque *t) {
  anillo[siguiente] = *t;
  siguiente = (siguiente + 1u) % CAPACIDAD;
  if (poblados < CAPACIDAD) {
    poblados++;
  }
  total++;
}

static int volcar(const char *ruta) {
  char temporal[RUTA_MAX];
  int escritos = snprintf(temporal, sizeof(temporal), "%s.tmp", ruta);
  if (escritos < 0 || (size_t)escritos >= sizeof(temporal)) {
    return -1;
  }

  FILE *f = fopen(temporal, "w");
  if (f == NULL) {
    return -1;
  }
  fprintf(f, "# Historial de toques -- anillo de %u, del mas antiguo al mas reciente.\n",
          CAPACIDAD);
  fprintf(f, "# Un toque por linea: instante origen puerto tipo.\n");
  fprintf(f, "# total-visto: %llu\n", total);

  unsigned cuantos = poblados;
  for (unsigned i = 0; i < cuantos; i++) {
    
    unsigned pos = (siguiente + CAPACIDAD - cuantos + i) % CAPACIDAD;
    const struct toque *t = &anillo[pos];
    time_t cuando = (time_t)t->momento;
    struct tm utc;
    char texto[32];
    if (gmtime_r(&cuando, &utc) == NULL ||
        strftime(texto, sizeof(texto), "%Y-%m-%dT%H:%M:%SZ", &utc) == 0) {
      continue;
    }
    fprintf(f, "%s %s %u %s\n", texto, t->origen, (unsigned)t->puerto,
            t->tipo == TIPO_PING ? "ping" : "syn");
  }

  if (fflush(f) != 0 || fsync(fileno(f)) != 0) {
    fclose(f);
    return -1;
  }
  if (fclose(f) != 0) {
    return -1;
  }
  return rename(temporal, ruta);
}

static void releer(const char *ruta) {
  FILE *f = fopen(ruta, "r");
  if (f == NULL) {
    return;
  }

  char linea[128];
  unsigned long long visto = 0;
  while (fgets(linea, sizeof(linea), f) != NULL) {
    if (linea[0] == '#') {
      leer_total(linea, &visto);
      continue;
    }
    struct toque t;
    if (leer_toque(linea, &t)) {
      anotar(&t);
    }
  }
  fclose(f);

  if (visto > total) {
    total = visto;
  }
}

static struct sock_filter programa_bpf[] = {
    {0x80, 0, 0, 0x00000000},  // A = longitud del paquete
    {0x35, 1, 0, 0x00000080},  // si A > 128 -> saltar a descartar
    {0x06, 0, 0, 0x00040000},  // aceptar
    {0x06, 0, 0, 0x00000000},  // descartar
};

static int abrir_captura(void) {

  int s = socket(AF_PACKET, SOCK_DGRAM, (int)htons(ETH_P_ALL));
  if (s < 0) {
    return -1;
  }
  struct sock_fprog fprog;
  fprog.len = (unsigned short)(sizeof(programa_bpf) / sizeof(programa_bpf[0]));
  fprog.filter = programa_bpf;
  if (setsockopt(s, SOL_SOCKET, SO_ATTACH_FILTER, &fprog, sizeof(fprog)) != 0) {
    close(s);
    return -1;
  }
  return s;
}

static int capturar(const char *ruta) {

  releer(ruta);

  int s = abrir_captura();
  if (s < 0) {
    fprintf(stderr, "nas-sensor: no se pudo abrir la captura: %s\n",
            strerror(errno));
    return 2;
  }

  time_t ultimo_volcado = time(NULL);
  uint8_t buf[PAQUETE_MAX];

  while (!hay_que_salir) {
    struct pollfd p;
    p.fd = s;
    p.events = POLLIN;
    p.revents = 0;
    int listo = poll(&p, 1, 1000);
    if (listo < 0 && errno != EINTR) {
      break;
    }
    if (listo > 0) {
      struct sockaddr_ll desde;
      socklen_t tam = sizeof(desde);
      memset(&desde, 0, sizeof(desde));
      ssize_t n = recvfrom(s, buf, sizeof(buf), MSG_TRUNC,
                           (struct sockaddr *)&desde, &tam);
      if (n > 0) {
        // MSG_TRUNC devuelve la longitud REAL, que puede pasar del bufer.
        size_t leidos = ((size_t)n > sizeof(buf)) ? sizeof(buf) : (size_t)n;
   
        tuvo el panel el 14/08, cuando una
        // senal salto sobre el propio nodo.
        if (desde.sll_pkttype != PACKET_OUTGOING) {
          struct toque t;
          if (analizar_paquete(buf, leidos, ntohs(desde.sll_protocol), &t)) {
            t.momento = (int64_t)time(NULL);
            anotar(&t);
          }
        }
      }
    }

    time_t ahora = time(NULL);
    if (ahora - ultimo_volcado >= SEGUNDOS_VOLCADO) {
      if (volcar(ruta) != 0) {
        fprintf(stderr, "nas-sensor: no se pudo volcar en %s: %s\n", ruta,
                strerror(errno));
      }
      ultimo_volcado = ahora;
    }
  }

  close(s);
  if (volcar(ruta) != 0) {
    fprintf(stderr, "nas-sensor: no se pudo volcar al salir: %s\n",
            strerror(errno));
    return 2;
  }
  return 0;
}

int main(int argc, char **argv) {
  if (argc != 2) {
    fprintf(stderr, "uso: nas-sensor <archivo-de-salida>\n");
    return 2;
  }
  if (strlen(argv[1]) + 5u >= RUTA_MAX) {
    fprintf(stderr, "nas-sensor: la ruta es demasiado larga\n");
    return 2;
  }

  struct sigaction sa;
  memset(&sa, 0, sizeof(sa));
  sa.sa_handler = al_recibir_senal;
  sigaction(SIGTERM, &sa, NULL);
  sigaction(SIGINT, &sa, NULL);

  return capturar(argv[1]);
}
