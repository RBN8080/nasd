// nas-sensor -- anota quien TOCA el nodo, aunque el cortafuegos lo descarte.
//
// POR QUE EXISTE: el panel de seguridad ve las peticiones HTTP rechazadas
// (ADR-0061) y las conexiones TCP aceptadas (ADR-0064). Las dos viven DENTRO
// de nasd, asi que solo existen si el paquete llego hasta el 80 o el 443. Todo
// lo que el cortafuegos tira -- un escaner recorriendo puertos cerrados -- no
// lo ve nadie.
//
// POR QUE NO ES UN «counter» EN nftables, QUE ERA EL PLAN (IDEAS §22.2).
// Medido contra el nodo el 2026-08-18, y las tres razones son independientes:
//
//   1. 15 arranques en 14 dias. Un contador del nucleo muere en cada uno,
//      porque nftables.service recarga un archivo que empieza por «flush
//      ruleset». Una semana de cuenta seria «desde el ultimo corte de luz», y
//      sin avisar de que se reinicio.
//   2. ~57 000 paquetes/dia de difusion de la casa contra 0 de radiacion IPv6
//      en la ventana medida. La cifra que decidia la linea de investigacion
//      habria sido una medicion de los cacharros del salon.
//   3. Los dos barridos de DRIFTNET entraron por el 443, que esta en «accept».
//      Un contador en el camino del «drop» NO habria visto el unico trafico
//      externo real que este nodo ha recibido.
//
// Ademas, el responsable pidio que esto no tocara lo que ya funciona. Este
// programa no toca el cortafuegos ni la unidad de nasd: escucha en paralelo.
//
// QUE ANOTA: ver analisis.h. Resumido, un TOQUE es alguien INICIANDO algo --
// un TCP SYN sin ACK, o un echo request. UDP no se anota, y es un punto ciego
// DECLARADO: sin conntrack no se distingue un sondeo de la respuesta a una
// consulta que hizo el propio nodo.
//
// QUE NO DECIDE ESTE PROGRAMA, Y ES DELIBERADO
//
// No sabe que es «Internet» y no le hace falta. Anota TODO lo que captura,
// venga de donde venga; quien descarta lo de casa es nasd al pintar, con
// seguridad.ClasificarRed -- la misma funcion que ya corrigio dos fallos
// reales (el IPv6 de casa contado como extrano, y fe80::…%2 por culpa de la
// zona). Tener esa logica en UN solo sitio vale mas que ahorrar lineas en el
// archivo: aqui habria que reimplementarla, y con ella su historial de fallos.
// Consecuencia asumida: el archivo contiene alguna fila de casa.
//
// INTERFAZ
//
//   nas-sensor <archivo-de-salida>
//
//   Captura hasta recibir SIGTERM o SIGINT. Vuelca cada 60 s y al salir.
//   Codigo de salida: 0 si termino bien, 2 si no pudo trabajar.
//
// Para analizar un paquete suelto a mano esta pruebas/arnes.c, que enlaza el
// MISMO analisis.c. Aqui no hay un modo «--analizar» porque seria ese mismo
// codigo escrito dos veces.
//
// LO QUE ESTE PROGRAMA NO HACE: no escribe en la red, no abre mas socket que
// el de captura, no ejecuta nada, no reserva memoria dinamica en ningun
// momento, y no enlaza ninguna libreria mas alla de la estandar de C.

// -std=c11 es modo ISO ESTRICTO: sin esta macro, sigaction, gmtime_r y fileno
// quedan escondidas y el compilador las da por implicitas -- que con -Werror es
// un error, y sin el seria un fallo silencioso en tiempo de enlazado. Va antes
// de cualquier include, que es lo unico que la hace efectiva.
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

// EL ANILLO ES ESTATICO Y DE TAMANO FIJO. Un escaner de 65 535 puertos lo llena
// y sigue costando exactamente lo mismo, porque no hay nada que reservar.
static struct toque anillo[CAPACIDAD];
static unsigned siguiente;

// total son los toques CAPTURADOS desde siempre, antes de que nadie clasifique
// nada. Se publica en la cabecera del archivo por dos motivos: un anillo lleno
// no debe leerse como «esto es todo lo que ha pasado», y es la unica forma de
// comprobar que la captura funciona SIN necesitar trafico de Internet -- un SYN
// desde la LAN lo incrementa aunque el panel despues no lo ensene.
static unsigned long long total;

static volatile sig_atomic_t hay_que_salir;

static void al_recibir_senal(int s) {
  (void)s;
  hay_que_salir = 1;
}

static void anotar(const struct toque *t) {
  anillo[siguiente] = *t;
  siguiente = (siguiente + 1u) % CAPACIDAD;
  total++;
}

// volcar escribe el anillo entero, del mas antiguo al mas reciente.
//
// ESCRITURA ATOMICA (ADR-0024): temporal en el MISMO directorio y rename. Un
// corte de luz a media escritura -- que en este nodo pasa casi a diario -- deja
// el archivo anterior intacto, nunca uno a medias.
//
// TEXTO PLANO Y NO JSON, a proposito: quien escribe es C sin librerias, y
// componer JSON a mano es un riesgo gratuito cuando los cuatro campos no pueden
// contener un espacio -- la direccion la produce analisis.c, el puerto es un
// entero y el tipo es uno de dos literales. Ademas asi se lee con «cat» en el
// nodo, que importa en operacion.
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

  unsigned cuantos =
      (total < (unsigned long long)CAPACIDAD) ? (unsigned)total : CAPACIDAD;
  for (unsigned i = 0; i < cuantos; i++) {
    // «siguiente» apunta al hueco que viene, asi que el mas antiguo esta
    // justo ahi en cuanto el anillo ha dado la vuelta.
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

// EL FILTRO DEL NUCLEO ES UNA OPTIMIZACION, NO UN CONTROL. Su unico trabajo es
// no despertar al espacio de usuario por cada paquete: una copia por SMB son
// decenas de miles por segundo en este nodo, y todos son de datos.
//
// Se filtra POR LONGITUD y por nada mas. Un SYN con opciones son ~80 bytes, y
// con IPv6 ~100; un paquete de datos ronda los 1500. Cuatro instrucciones que
// se leen de un vistazo valen mas aqui que un programa preciso de veinte cuyos
// saltos relativos no se pueden comprobar sin ejecutarlo.
//
// La asimetria es deliberada y es lo que lo hace seguro: analizar_paquete
// vuelve a comprobarlo TODO en el espacio de usuario, asi que un fallo de este
// filtro solo puede PERDER un paquete, jamas inventar un dato falso.
//
// Limite conocido: un ping con carga grande («ping -s») pasa de 128 bytes y no
// se anota. Un ping gigante no es un escaneo.
static struct sock_filter programa_bpf[] = {
    {0x80, 0, 0, 0x00000000},  // A = longitud del paquete
    {0x35, 1, 0, 0x00000080},  // si A > 128 -> saltar a descartar
    {0x06, 0, 0, 0x00040000},  // aceptar
    {0x06, 0, 0, 0x00000000},  // descartar
};

static int abrir_captura(void) {
  // SOCK_DGRAM y no SOCK_RAW: el nucleo entrega el paquete ya SIN cabecera de
  // enlace, asi que desaparece toda una capa de analisis -- Ethernet, VLAN -- y
  // con ella su clase entera de fallos. sll_protocol dice de que familia es.
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
        // LO SALIENTE NO SE ANOTA. Sin esto anotariamos nuestras propias
        // respuestas y el nodo saldria tocandose a si mismo -- que es
        // exactamente el defecto que ya tuvo el panel el 14/08, cuando una
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
