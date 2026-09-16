#!/bin/bash
# instalar.sh - bring the whole node up from a clone of the repository. ADR-0095.
#
# THIS SCRIPT INSTALLS NOTHING BY ITSELF. It calls the numbered scripts in this
# folder, in order, and stops the moment one fails. Everything it really does
# still lives in them, which is where P1 requires it: "if a change is not in a
# script, it does not exist".
#
# WHAT IT ADDS, AND IT IS WHAT WAS MISSING:
#
#   1. THE ORDER. Until now it lived in the numeric prefixes and in twenty error
#      messages scattered about ("run 01_preparar_disco.sh first"). Whoever
#      rebuilt the node had to read whole headers to discover, for instance,
#      that 07 goes before 06, or that 15 forces repeating 05 and 03.
#   2. THE PRE-FLIGHT CHECK. Before touching anything it looks at the conditions
#      that, on failing halfway, would leave the node with the disk already
#      formatted and the service half installed. They ALL fail at once, not one
#      per attempt.
#   3. THE BINARY. 05_instalar_servicio.sh expects it at /tmp/nasd and does not
#      say how to put it there. Here it is built if the node can, and if not it
#      stops and writes the exact command.
#
# Usage:
#   sudo ./instalar.sh                the home NAS: disk, Samba, firewall,
#                                     service, credential, journal, resilience
#   sudo ./instalar.sh --remoto       access from outside: DDNS, WireGuard, NAT
#                                     and TLS. Requires an existing DuckDNS account
#   sudo ./instalar.sh --desde 08     resume at that step, without repeating the
#                                     previous ones
#
# BEFORE THE FIRST TIME, once:
#   sudo install -d -m 0755 /etc/nas
#   sudo cp ajustes.conf.ejemplo /etc/nas/ajustes.conf
#   sudo nano /etc/nas/ajustes.conf

set -uo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")" || exit 1
AQUI="$(pwd)"
AJUSTES=/etc/nas/ajustes.conf
BINARIO=/tmp/nasd

rojo()  { printf '\033[31m%s\033[0m\n' "$*"; }
verde() { printf '\033[32m%s\033[0m\n' "$*"; }
aviso() { printf '\033[33m%s\033[0m\n' "$*"; }

# El formato de error del proyecto: el hecho en la primera linea, y debajo la
# consecuencia y la accion con siete espacios de sangria. El precedente exacto
# es «verificar-limpio» del Makefile.
error() {
  rojo "ERROR: $1"
  shift
  local l
  for l in "$@"; do printf '       %s\n' "$l"; done
}

paso() {
  echo
  printf '\033[36m===== %s =====\033[0m\n' "$*"
}

# ---------------------------------------------------------------------------
#  LAS DOS LISTAS
# ---------------------------------------------------------------------------
#
# Formato de cada entrada:  prefijo|tipo|orden completa
#
# «tipo» separa lo que INSTALA de lo que VERIFICA, y esa distincion no es
# cosmetica: un instalador que devuelve algo distinto de cero ha fallado y la
# instalacion se detiene; un verificador devuelve cuantos fallos conto, que se
# suman y se informan al final sin detener nada.
#
# LA ORDEN VA COMPLETA, con sus argumentos, y por eso no hay ni una rama que
# decida nada: 03 aparece dos veces, una sin «--si» y otra con el, porque son
# dos invocaciones distintas y no dos modos de la misma.

BASE=(
  "00|instalar|./00_endurecer_nodo.sh"
  "01|instalar|./01_preparar_disco.sh"
  "02|instalar|./02_instalar_samba.sh"
  # SIN «--si», Y ES DELIBERADO. 03_cortafuegos.sh solo autoriza el modo no
  # interactivo para reaplicar reglas YA vigentes; esta es la primera vez que
  # se aplica una politica DENY, que es el momento exacto en que se puede
  # perder SSH. Esa pregunta se respeta.
  "03|instalar|./03_cortafuegos.sh"
  "04|verificar|./04_verificar.sh"
  "05|instalar|./05_instalar_servicio.sh"
  # 07 ANTES QUE 06, aunque el numero diga lo contrario: 06_verificar_web.sh
  # prueba la web autenticada, y sin la credencial de 07 no hay con que entrar.
  "07|instalar|./07_credencial_web.sh"
  "06|verificar|./06_verificar_web.sh"
  "08|instalar|./08_observabilidad.sh"
  "10|instalar|./10_resiliencia_disco.sh"
  "17|instalar|./17_respaldar_identidad.sh"
  "09|verificar|./09_verificar_operacion.sh"
)

REMOTO=(
  "11|instalar|./11_ddns.sh"
  "12|instalar|./12_wireguard.sh"
  "14|instalar|./14_mantener_nat.sh"
  "15|instalar|./15_tls.sh"
  # 05 Y 03 OTRA VEZ, y no es un error de copiar y pegar: 15_tls.sh termina
  # pidiendo exactamente esto. Antes del certificado, 05 escribe un TOML sin
  # rutas de TLS y 03 no abre el 443 —los dos lo dicen en sus avisos—; despues
  # del certificado hay que volver a pasar para que lo recojan.
  #
  # Se repiten en vez de detectarse porque los dos son idempotentes por
  # construccion: 03 reescribe las reglas enteras y 05 solo reinstala el
  # binario si hay uno nuevo. Ejecutarlos dos veces cuesta segundos; una rama
  # que decidiera el orden costaria una prueba que nadie va a escribir.
  "05|instalar|./05_instalar_servicio.sh"
  "03|instalar|./03_cortafuegos.sh --si"
  # LA IDENTIDAD SE RESPALDA OTRA VEZ, y esto SALDA un fallo real: 09 exige que
  # el respaldo contenga /etc/wireguard/wg0.conf, que antes de 12 no existia.
  "17|instalar|./17_respaldar_identidad.sh"
  "09|verificar|./09_verificar_operacion.sh"
)

# ---------------------------------------------------------------------------
#  ARGUMENTOS
# ---------------------------------------------------------------------------

LISTA_NOMBRE="BASE"
BANDERA=""          # lo que hay que repetir en el mensaje de retome
DESDE=""

while [ $# -gt 0 ]; do
  case "$1" in
    --remoto) LISTA_NOMBRE="REMOTO"; BANDERA=" --remoto"; shift ;;
    --desde)
      DESDE="${2:-}"
      [ -n "$DESDE" ] || { error "«--desde» necesita el prefijo del paso." "Por ejemplo:  sudo ./instalar.sh --desde 08"; exit 1; }
      shift 2
      ;;
    -h|--ayuda|--help)
      # SE IMPRIME LA CABECERA ENTERA, sea cual sea su largo. Antes iba un
      # rango fijo de lineas y se quedo corto en cuanto la cabecera crecio: la
      # ayuda salia cortada a media frase y nada lo delataba.
      awk 'NR>1 && /^#/ {sub(/^# ?/, ""); print; next} NR>1 {exit}' "$0"
      exit 0
      ;;
    *)
      error "no reconozco «$1»." \
            "Las unicas banderas son «--remoto» y «--desde NN»." \
            "Vea:  ./instalar.sh --ayuda"
      exit 1
      ;;
  esac
done

if [ "$LISTA_NOMBRE" = "REMOTO" ]; then
  LISTA=("${REMOTO[@]}")
else
  LISTA=("${BASE[@]}")
fi

PREFIJOS=""
for e in "${LISTA[@]}"; do PREFIJOS="$PREFIJOS ${e%%|*}"; done

if [ -n "$DESDE" ]; then
  case " $PREFIJOS " in
    *" $DESDE "*) ;;
    *)
      error "no hay ningun paso «$DESDE» en esta lista." \
            "Los pasos de esta lista son:$PREFIJOS" \
            "(No hay ningun 13_: ese numero nunca se uso. No falta nada.)"
      exit 1
      ;;
  esac
fi

# ---------------------------------------------------------------------------
#  COMPROBACION PREVIA — nada de esto modifica el nodo
# ---------------------------------------------------------------------------
#
# SE ACUMULAN TODOS LOS FALLOS EN VEZ DE CORTAR EN EL PRIMERO. Quien se
# equivoca escribiendo seis valores a mano suele equivocarse en dos, y
# descubrirlos de uno en uno son dos viajes al nodo por nada.

paso "Comprobacion previa (no se toca nada del nodo)"

fallos=0
mal() { error "$@"; echo; fallos=$((fallos + 1)); }

# --- quien ejecuta ---
if [ "$(id -u)" -ne 0 ]; then
  mal "instalar.sh no se esta ejecutando como root." \
      "Todos los pasos escriben en /etc, en /srv y en systemd: ninguno podria" \
      "hacer nada." \
      "Ejecute:  sudo ./instalar.sh"
fi

# --- que los scripts esten y se puedan ejecutar ---
for e in "${LISTA[@]}"; do
  resto="${e#*|}"
  orden="${resto#*|}"
  guion="${orden%% *}"
  if [ ! -f "$guion" ]; then
    mal "falta $guion en $AQUI." \
        "La lista de instalacion lo nombra y no esta: el clon esta incompleto." \
        "Clone el repositorio entero, no solo esta carpeta."
  elif [ ! -x "$guion" ]; then
    mal "$guion no tiene permiso de ejecucion." \
        "Pasa al copiar el repositorio desde Windows o desde un ZIP: se pierde" \
        "el bit de ejecucion y la instalacion se cortaria a mitad." \
        "Arreglelo:  chmod +x $AQUI/*.sh"
  fi
done

# --- los seis ajustes ---
if [ ! -f "$AJUSTES" ]; then
  mal "no existe $AJUSTES." \
      "Sin el no se sabe ni que disco formatear ni cual es la LAN de esta casa," \
      "y ningun script lo adivina." \
      "Copielo y editelo, una sola vez:" \
      "  sudo install -d -m 0755 /etc/nas" \
      "  sudo cp $AQUI/ajustes.conf.ejemplo $AJUSTES" \
      "  sudo nano $AJUSTES"
else
  # shellcheck disable=SC1090  # ruta fija conocida, no una variable arbitraria
  . "$AJUSTES"
  for v in RED NODO_IP INTERFAZ DISCO DISCO_MODELO RED_TUNEL; do
    if [ -z "${!v:-}" ]; then
      mal "falta $v en $AJUSTES." \
          "Los seis valores son obligatorios y ninguno se deduce solo." \
          "Editelo:  sudo nano $AJUSTES"
    fi
  done
fi

# Lo que sigue solo tiene sentido con los ajustes leidos.
if [ -n "${DISCO:-}" ] && [ -n "${INTERFAZ:-}" ] && [ -n "${NODO_IP:-}" ]; then

  # --- el disco, que es lo unico que destruye datos ---
  #
  # Solo se mira si 01 esta en lo que se va a ejecutar: con «--desde 02» el
  # disco ya esta preparado y exigir que el modelo cuadre seria pedir que siga
  # enchufado el mismo dispositivo que se formateo hace meses.
  case " $PREFIJOS " in *" 01 "*) MIRAR_DISCO=1 ;; *) MIRAR_DISCO=0 ;; esac
  if [ -n "$DESDE" ] && [ "$DESDE" != "01" ] && [ "$DESDE" != "00" ]; then MIRAR_DISCO=0; fi

  if [ "$MIRAR_DISCO" = "1" ]; then
    if [ ! -b "$DISCO" ]; then
      mal "DISCO=$DISCO no es un dispositivo de bloques." \
          "01_preparar_disco.sh abortaria, pero DESPUES de que 00 ya hubiera" \
          "retirado el escritorio y varios servicios de este nodo." \
          "Mire cual es de verdad y corrija DISCO en $AJUSTES:"
      lsblk -o NAME,SIZE,MODEL,SERIAL,TRAN 2>/dev/null | sed 's/^/       /'
      echo
    else
      # El disco del que arranca el sistema. Se compara por el dispositivo
      # padre: la raiz vive en una particion (/dev/sda2) y aqui se nombra el
      # disco entero (/dev/sda).
      RAIZ_DEV=$(findmnt -no SOURCE / 2>/dev/null)
      RAIZ_DISCO=$(lsblk -nso PKNAME "$RAIZ_DEV" 2>/dev/null | tail -1)
      if [ -n "$RAIZ_DISCO" ] && [ "/dev/$RAIZ_DISCO" = "$DISCO" ]; then
        mal "DISCO=$DISCO es el disco desde el que arranca este nodo." \
            "Formatearlo destruiria el sistema, no el volumen de datos." \
            "Corrija DISCO en $AJUSTES."
      fi

      MODELO=$(lsblk -ndo MODEL "$DISCO" 2>/dev/null | tr -d ' ')
      case "$MODELO" in
        "$DISCO_MODELO"*) ;;
        *)
          mal "$DISCO es un «${MODELO:-modelo desconocido}» y DISCO_MODELO dice «$DISCO_MODELO»." \
              "O hay enchufado otro disco, o DISCO apunta al que no es. En los dos" \
              "casos, seguir borraria el contenido equivocado." \
              "Corrija DISCO o DISCO_MODELO en $AJUSTES."
          ;;
      esac
    fi
  fi

  # --- la interfaz ---
  if [ ! -d "/sys/class/net/$INTERFAZ" ]; then
    mal "no existe la interfaz de red «$INTERFAZ»." \
        "11_ddns.sh y 16_ipv6_estable.sh la leen por su nombre y, si no esta, NO" \
        "fallan: devuelven vacio, y el acceso remoto muere en silencio." \
        "Las que hay en este nodo son: $(find /sys/class/net -mindepth 1 -maxdepth 1 -printf '%f ' 2>/dev/null)" \
        "Corrija INTERFAZ en $AJUSTES.  (En Pi OS Bookworm suele ser end0.)"
  fi

  # --- la direccion ---
  IP_REAL=$(hostname -I 2>/dev/null | awk '{print $1}')
  if [ -n "$IP_REAL" ] && [ "$IP_REAL" != "$NODO_IP" ]; then
    mal "la direccion de este nodo es $IP_REAL y NODO_IP dice $NODO_IP." \
        "05 enlaza nasd a la direccion real, pero 12 reparte perfiles de" \
        "WireGuard que encaminan NODO_IP: el tunel se levantaria y la web no" \
        "apareceria, sin que nada fallara." \
        "Fije la IP en el router, o corrija NODO_IP en $AJUSTES."
  fi

  # --- las dos redes ---
  case "$RED" in */*) ;; *) mal "RED=$RED no lleva mascara." "Se escribe en CIDR, por ejemplo 192.168.1.0/24." "Corrijalo en $AJUSTES." ;; esac
  case "$RED_TUNEL" in
    */24) ;;
    *) mal "RED_TUNEL=$RED_TUNEL no es un /24." \
           "12_wireguard.sh reparte direcciones por el ultimo octeto y escribe" \
           "«Address = ...1/24»: con otra mascara el tunel no encamina lo que dice." \
           "Corrijalo en $AJUSTES." ;;
  esac
fi

# --- red para apt ---
#
# LIMITE CONOCIDO, dicho aqui y no escondido: esto comprueba que el DNS
# resuelve, NO que el repositorio responda ni que haya paquetes. Un proxy o un
# espejo caido pasan esta puerta. Es el lado barato por el que conviene
# equivocarse: detecta el caso comun —cable suelto, Wi-Fi sin configurar— antes
# de formatear el disco, y no promete mas de lo que mira.
if ! getent hosts deb.debian.org >/dev/null 2>&1; then
  mal "no se resuelve deb.debian.org." \
      "Los pasos 02, 03 y 10 instalan paquetes con apt-get y fallarian a mitad," \
      "con el disco ya formateado." \
      "Conecte la red, revise «ip a» y /etc/resolv.conf, y repita." \
      "LIMITE: esto comprueba el DNS, no que el repositorio responda."
fi

# --- medicion limpia: AVISO, no fallo ---
if pgrep -f 'vscode-server' >/dev/null 2>&1; then
  aviso "AVISO: hay un servidor de VSCode corriendo en este nodo."
  printf '       %s\n' \
    "04 y 09 declaran sus mediciones invalidas con el conectado (RES-09) y le" \
    "marcaran fallos que no son de la instalacion." \
    "Cierrelo y entre por ssh a secas si quiere un resultado limpio."
  echo
fi

# ---------------------------------------------------------------------------
#  EL BINARIO — segunda tanda, la unica que tarda
# ---------------------------------------------------------------------------
#
# Va DESPUES de las comprobaciones baratas para no hacer esperar a quien tiene
# un valor mal escrito, y ANTES de ejecutar nada para que «no hay binario» no
# se descubra con el disco ya formateado.

necesita_binario() { case " $PREFIJOS " in *" 05 "*) return 0 ;; *) return 1 ;; esac; }

if [ "$fallos" -eq 0 ] && necesita_binario; then
  paso "El binario de nasd"

  if [ -f "$BINARIO" ]; then
    echo "Ya hay un binario en $BINARIO; se usa ese."
    echo "(Es lo que 05_instalar_servicio.sh lee por omision.)"

  elif command -v go >/dev/null 2>&1 && [ -f "$AQUI/../10_CODIGO/go.mod" ]; then
    echo "Compilando nasd en este nodo con $(go version)..."
    # Sin GOOS ni GOARCH: se compila para esta misma maquina, que es donde va a
    # correr. Sin CGO para que el binario no dependa de nada del sistema.
    #
    # SOLO nasd. Los dos auxiliares en C —nas-miniatura y nas-sensor— se quedan
    # fuera a proposito: exigirian una cadena de compilacion de C que
    # 00_endurecer_nodo.sh acaba de retirar por superficie minima, y sin ellos
    # el NAS funciona igual (las miniaturas degradan a 404, ADR-0063, y el
    # sensor es una unidad aparte que instala 19_sensor.sh).
    if ( cd "$AQUI/../10_CODIGO" && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o "$BINARIO" ./cmd/nasd ); then
      verde "Compilado: $BINARIO"
      ls -lh "$BINARIO"
    else
      echo
      error "«go build» fallo en este nodo." \
            "Causas vistas: el go.mod exige Go 1.25 y el del sistema es mas viejo," \
            "o no hay memoria suficiente para compilar." \
            "No se ha tocado nada del nodo." \
            "Compilelo en su PC y enviemelo:" \
            "  cd 10_CODIGO" \
            "  CGO_ENABLED=0 GOOS=linux GOARCH=$(uname -m | sed 's/aarch64/arm64/;s/x86_64/amd64/') go build -trimpath -ldflags=\"-s -w\" -o nasd ./cmd/nasd" \
            "  scp nasd USUARIO@$IP_REAL:$BINARIO" \
            "Despues vuelva a ejecutar:  sudo ./instalar.sh"
      fallos=$((fallos + 1))
    fi

  elif [ ! -f "$AQUI/../10_CODIGO/go.mod" ]; then
    error "no encuentro ../10_CODIGO/go.mod junto a esta carpeta." \
          "Se copio solo 20_APROVISIONAMIENTO: no hay fuentes que compilar." \
          "O clone el repositorio entero, o envie el binario ya compilado a $BINARIO."
    fallos=$((fallos + 1))

  else
    error "no hay binario en $BINARIO y este nodo no tiene «go» instalado." \
          "Sin binario no hay servicio web, y el resto de la instalacion no sirve" \
          "de nada." \
          "NO SE HA TOCADO NADA DEL NODO." \
          "Compilelo en su PC y enviemelo:" \
          "  cd 10_CODIGO" \
          "  CGO_ENABLED=0 GOOS=linux GOARCH=$(uname -m | sed 's/aarch64/arm64/;s/x86_64/amd64/') go build -trimpath -ldflags=\"-s -w\" -o nasd ./cmd/nasd" \
          "  scp nasd USUARIO@$IP_REAL:$BINARIO" \
          "Despues vuelva a ejecutar:  sudo ./instalar.sh"
    fallos=$((fallos + 1))
  fi
fi

if [ "$fallos" -gt 0 ]; then
  echo
  rojo "===== LA COMPROBACION PREVIA ENCONTRO $fallos PROBLEMAS ====="
  echo
  echo "  No se ha ejecutado ningun paso y el nodo esta como estaba."
  echo "  Arregle lo de arriba y vuelva a lanzar:  sudo ./instalar.sh"
  exit 1
fi

verde "Comprobacion previa: todo en orden."

# ---------------------------------------------------------------------------
#  LA INSTALACION
# ---------------------------------------------------------------------------

total=0
saltados=0
fallos_verificacion=0
empezar=0
[ -z "$DESDE" ] && empezar=1

for e in "${LISTA[@]}"; do
  prefijo="${e%%|*}"
  resto="${e#*|}"
  tipo="${resto%%|*}"
  orden="${resto#*|}"

  if [ "$empezar" -eq 0 ]; then
    if [ "$prefijo" = "$DESDE" ]; then
      empezar=1
    else
      saltados=$((saltados + 1))
      continue
    fi
  fi

  paso "Paso $prefijo · $orden"
  # shellcheck disable=SC2086  # la orden lleva sus argumentos y se quiere dividir
  $orden
  codigo=$?
  total=$((total + 1))

  if [ "$tipo" = "verificar" ]; then
    # SU CODIGO DE SALIDA ES SU NUMERO DE FALLOS, no un error del paso. Tratarlo
    # como un fallo de instalacion detendria el proceso ante el primer fallo
    # esperado —y hay uno esperado en un nodo solo-LAN, ver el cierre—.
    if [ "$codigo" -gt 0 ]; then
      aviso "$orden conto $codigo fallos. Se anotan y se sigue."
      fallos_verificacion=$((fallos_verificacion + codigo))
    else
      verde "$orden: sin fallos."
    fi
  elif [ "$codigo" -ne 0 ]; then
    echo
    error "$orden termino con codigo $codigo." \
          "Los $((total - 1)) pasos anteriores SI se aplicaron: el nodo esta a medias." \
          "Lea el error de arriba, arreglelo, y retome donde se quedo:" \
          "  sudo ./instalar.sh$BANDERA --desde $prefijo" \
          "(Si lo que hizo fue cancelar a proposito, no hay nada que arreglar:" \
          " el nodo se quedo justo antes de ese paso.)"
    exit 1
  else
    verde "$orden: hecho."
  fi
done

# ---------------------------------------------------------------------------
#  EL CIERRE
# ---------------------------------------------------------------------------

echo
if [ "$fallos_verificacion" -eq 0 ]; then
  verde "===== INSTALACION COMPLETA: $total pasos, sin fallos ====="
else
  aviso "===== INSTALACION COMPLETA: $total pasos, $fallos_verificacion fallos de verificacion ====="
fi
[ "$saltados" -gt 0 ] && echo "  ($saltados pasos saltados por --desde $DESDE)"
echo
echo "  Abra en el navegador:  http://$NODO_IP"
printf '%s\n' "  Recurso de red:        \\\\$NODO_IP\\datos   (usuario nas, la contrasena del paso 07)"

if [ "$LISTA_NOMBRE" = "BASE" ] && [ "$fallos_verificacion" -gt 0 ]; then
  echo
  aviso "  UN FALLO DE 09 ES ESPERADO EN UN NODO SOLO-LAN:"
  printf '       %s\n' \
    "el respaldo de identidad no contiene /etc/wireguard/wg0.conf porque el" \
    "tunel todavia no existe. No es un defecto de la instalacion." \
    "Se salda entero al instalar el acceso remoto."
fi

if [ "$LISTA_NOMBRE" = "BASE" ]; then
  echo
  echo "  LO QUE ESTA INSTALACION NO HACE, y se pide aparte:"
  printf '    %s\n' \
    "sudo ./instalar.sh --remoto   DDNS, WireGuard, NAT y TLS (exige cuenta DuckDNS)" \
    "sudo ./16_ipv6_estable.sh     IPv6 fija: termina pidiendo un campo en el router" \
    "sudo ./18_geoip.sh            pais y operador en el panel de seguridad" \
    "sudo ./19_sensor.sh           sensor de toques: su binario llega del PC" \
    "sudo ./20_avisos.sh           avisos a Telegram: exige testigo y chat" \
    "sudo ./22_privilegio_minimo_respaldo.sh   exige crear el usuario 'respaldo' a mano"
fi

echo
# El numero significa algo, como en el resto de los verificadores del proyecto.
exit "$fallos_verificacion"
