#!/bin/bash
# External alerts and availability witness - ADR-0073 and ADR-0074.
#
#   05_OPERACION.md §1           the row "nobody finds out on their own" stops
#                                being true here
#   04_SEGURIDAD.md §6.septies   what leaves the node and what does not
#   P4                           secrets NEVER enter the repository
#   P8                           no new daemon: nasd emits the heartbeat
#
# WHAT THIS INSTALLS, AND WHY IT IS TWO THINGS AND NOT ONE:
#
#   1. THE CHANNEL - nasd sends green/amber/red to a Telegram chat.
#   2. THE WITNESS - nasd beats every 5 min to Healthchecks.io, and if it STOPS
#      beating it is @HealthchecksBot that writes the alert in THAT SAME chat.
#
# Telegram cannot be the witness, and that is not a limitation of this design:
# it is a mailbox, it receives what it is sent. There is nothing in it that says
# "this node was supposed to write to me every 5 minutes and it has been 95
# without doing so". If the node powers off it sends nothing, and a silent chat
# is indistinguishable from a quiet night. Without a witness, that alert simply
# does not exist.
#
# WHY A DROP-IN AND NOT A LINE IN 05_instalar_servicio.sh:
#
#   LoadCredential= with a file that DOES NOT EXIST makes the unit FAIL TO
#   START. Putting the line in the main script would leave any node it ran on,
#   without alerts configured first, WITHOUT SERVICE - trading a working NAS for
#   a notification channel. With the drop-in, the credential is declared ONLY
#   once it exists, and removing the layer is deleting one file.
#
# THE SECRETS ARE NOT PASSED AS ARGUMENTS OR IN A MESSAGE. They are written by
# hand on the node, once. If the file does not exist, this script explains how
# and stops.
#
# Idempotent. P1: if a change is not in a script, it does not exist.
#
# Usage: sudo ./20_avisos.sh

set -euo pipefail

CONF=/etc/nasd/avisos
DIR_DROPIN=/etc/systemd/system/nasd.service.d
DROPIN="$DIR_DROPIN/50-avisos.conf"

rojo()  { printf '\033[31m%s\033[0m\n' "$*"; }
verde() { printf '\033[32m%s\033[0m\n' "$*"; }
aviso() { printf '\033[33m%s\033[0m\n' "$*"; }
gris()  { printf '\033[90m%s\033[0m\n' "$*"; }

[ "$(id -u)" -eq 0 ] || { rojo "Ejecute con sudo."; exit 1; }

echo "===== Avisos externos y testigo de disponibilidad ====="
echo

# ---------------------------------------------------------------------------
# 1. Los secretos, que llegan de fuera de este script
# ---------------------------------------------------------------------------
if [ ! -f "$CONF" ]; then
  rojo "No existe $CONF."
  echo
  verde "LA VÍA CORTA: sudo ./21_avisos_asistido.sh"
  gris "  Pregunta el token sin mostrarlo, saca él solo el chat_id y escribe"
  gris "  este archivo. Luego encadena con este script. El testigo es opcional."
  echo
  aviso "O A MANO, si prefiere ver cada paso:"
  cat <<'AYUDA'

  A) EL CANAL (Telegram)
     1. En Telegram, hable con @BotFather y mande /newbot. Le dará un TOKEN.
     2. Abra una conversación con SU bot y mándele cualquier cosa.
     3. Averigüe el chat_id. Desde el propio nodo, SIN dejar el token en la
        lista de procesos (por eso va por la entrada estándar):

          curl -sS -K - <<'FIN' | head -c 400
          url = "https://api.telegram.org/botEL-TOKEN/getUpdates"
          FIN

        Busque  "chat":{"id":NNNNNNN  — ese número es el chat_id.

  B) EL TESTIGO (Healthchecks.io)
     4. Cree una cuenta gratuita en https://healthchecks.io
     5. Cree un check y fije:
             Period = 5 minutes
             Grace  = 90 minutes
        Los dos números están justificados en ADR-0074: 90 min cubre el peor
        corte de corriente medido en este nodo (60.1 min) más el arranque.
     6. Copie su «ping URL» (https://hc-ping.com/UUID).
     7. En Integrations, añada Telegram y pulse /start en @HealthchecksBot
        DESDE EL MISMO CHAT del paso A. Así el 🟠 llega donde todo lo demás.

  C) ESCRIBIR EL ARCHIVO, en el nodo:

       sudo install -d -m 0755 /etc/nasd
       sudo tee /etc/nasd/avisos >/dev/null <<'FIN'
       telegram_token = "EL-TOKEN-DE-BOTFATHER"
       telegram_chat  = "EL-CHAT-ID"
       latido_url     = "https://hc-ping.com/EL-UUID"
       FIN
       sudo chmod 0600 /etc/nasd/avisos

  8. Vuelva a ejecutar este script.

  LOS TRES SON SECRETOS Y NO SE MANDAN POR CHAT NI ENTRAN EN EL REPOSITORIO (P4).
  El que más daño hace si se filtra NO es el token: es latido_url. Quien la
  tenga puede falsificar latidos y mantener el testigo en VERDE con el nodo
  muerto — ver ADR-0074.

AYUDA
  exit 1
fi

chmod 0600 "$CONF"
chown root:root "$CONF"

# NO SE HACE «. "$CONF"»: este archivo no es shell, es el formato TOML-lite que
# lee config.LeerPares en el propio binario (10_CODIGO/internal/config/toml.go)
# — «clave = "valor"», con espacios alrededor del signo igual. Sourcearlo como
# bash rompía con «command not found» en la primera línea, porque los espacios
# no son asignación válida de shell. Es distinto del formato de 11_ddns.sh
# (DOMINIO=valor, sin espacios, ese sí pensado para sourcearse) y no deben
# confundirse: aquí se extrae con la misma regla que aplica el lector real.
valor_de() {
  sed -n "s/^${1}[[:space:]]*=[[:space:]]*\"\\(.*\\)\"[[:space:]]*\$/\\1/p" "$CONF" | tail -1
}
telegram_token=$(valor_de telegram_token)
telegram_chat=$(valor_de telegram_chat)
latido_url=$(valor_de latido_url)
: "${telegram_token:?falta telegram_token en $CONF}"
: "${telegram_chat:?falta telegram_chat en $CONF}"
# EL TESTIGO ES OPCIONAL Y EL CANAL NO, igual que en el binario: main.go exige
# token y chat JUNTOS —media configuración arranca y parece funcionar, que es la
# forma más cara de descubrir un error— pero trata latido_url por separado.
# Este script exigía los tres hasta el 2026-08-25, y era MÁS ESTRICTO QUE EL
# CÓDIGO: dejaba sin canal a quien tuviera el bot y todavía no el check, que es
# exactamente el orden en que se consiguen las dos cosas.
latido_url="${latido_url:-}"
verde "Secretos leídos de $CONF (0600 root:root)."

command -v curl >/dev/null || { echo "Instalando curl..."; apt-get install -y -qq curl; }

# ---------------------------------------------------------------------------
# 2. La credencial, por el mecanismo de P4
# ---------------------------------------------------------------------------
install -d -m 0755 "$DIR_DROPIN"
cat > "$DROPIN" <<EOF
# Generado por 20_APROVISIONAMIENTO/20_avisos.sh — proyecto NAS. NO editar (P1).
#
# systemd copia el archivo a un tmpfs privado del servicio, legible solo por él,
# y publica la ruta en \$CREDENTIALS_DIRECTORY. Es el MISMO mecanismo que la
# credencial de la web (ADR-0021), y no otro: dos formas de entregar secretos al
# mismo proceso serían dos sitios donde equivocarse.
#
# PARA RETIRAR LA CAPA DE AVISOS: borre este archivo y «systemctl daemon-reload
# && systemctl restart nasd». El nodo vuelve a funcionar exactamente igual, solo
# que sin avisar por fuera.
[Service]
LoadCredential=avisos:$CONF
EOF
verde "$DROPIN escrito."

systemctl daemon-reload
systemctl restart nasd
sleep 3
systemctl is-active --quiet nasd || {
  rojo "nasd NO arrancó tras añadir la credencial. Diagnóstico:"
  systemctl status nasd --no-pager -l | tail -20
  exit 1
}
verde "nasd reiniciado y activo."

# ---------------------------------------------------------------------------
# 3. Comprobar el EFECTO, no el archivo
# ---------------------------------------------------------------------------
echo
echo "== Comprobación =="

# EL TOKEN VA POR LA ENTRADA ESTÁNDAR, NUNCA EN LA LÍNEA DE ÓRDENES.
#
# La Bot API obliga a llevarlo en la RUTA de la URL, así que un «curl "https://
# api.telegram.org/bot$TOKEN/..."» lo dejaría en el argv, visible con «ps» para
# cualquier usuario del nodo. Es el defecto que 11_ddns.sh tiene con el testigo
# de DuckDNS, y el que el commit del 24/08 corrigió en el verificador de la web.
#
# «curl -K -» lee su configuración de la entrada estándar: la URL entra por una
# tubería y no por un argumento.
COD=$(curl -sS -K - -o /tmp/nas-avisos-resp.$$ -w '%{http_code}' <<FIN || true
url = "https://api.telegram.org/bot${telegram_token}/sendMessage"
data-urlencode = "chat_id=${telegram_chat}"
data-urlencode = "text=NAS · prueba de canal. Si lee esto, los avisos llegan."
max-time = 20
FIN
)
if [ "$COD" = "200" ]; then
  verde "Telegram aceptó el mensaje de prueba: mire su teléfono."
else
  rojo "Telegram devolvió HTTP $COD."
  # La respuesta puede traer la descripción del error, y NO lleva el token.
  head -c 300 "/tmp/nas-avisos-resp.$$" 2>/dev/null || true
  echo
  rm -f "/tmp/nas-avisos-resp.$$"
  exit 1
fi
rm -f "/tmp/nas-avisos-resp.$$"

# El latido, igual: la URL lleva el UUID, que es secreto.
if [ -n "$latido_url" ]; then
  COD=$(curl -sS -K - -o /dev/null -w '%{http_code}' <<FIN || true
url = "${latido_url}"
max-time = 20
FIN
)
  if [ "$COD" = "200" ]; then
    verde "El testigo externo aceptó el latido: el check debe estar en «up»."
  else
    rojo "El testigo devolvió HTTP $COD. Revise latido_url en $CONF."
    exit 1
  fi
else
  echo
  aviso "SIN TESTIGO EXTERNO — el canal queda montado, pero falta la otra mitad."
  aviso "  Telegram NO puede avisarle de que este nodo se apagó: es un buzón, y un"
  aviso "  chat callado es indistinguible de una noche tranquila. Mientras no haya"
  aviso "  testigo, la ausencia del NAS NO LA DETECTA NADIE."
  aviso "  Se resuelve con 21_avisos_asistido.sh cuando cree el check."
fi

# Y LO QUE DE VERDAD IMPORTA: que sea NASD quien late, no este script.
#
# Los dos curl de arriba demuestran que los SECRETOS valen, no que el servicio
# los esté usando. Es la diferencia entre comprobar el archivo y comprobar el
# efecto, y sin esta espera el script se daría por bueno con un nasd que no late.
if [ -n "$latido_url" ]; then
  echo
  echo "Esperando al primer latido del propio servicio (hasta 90 s)..."
  if timeout 90 journalctl -u nasd -f -n 0 -o cat 2>/dev/null \
     | grep -q -m1 'latido al testigo externo activo'; then
    verde "nasd emite el latido por su cuenta."
  else
    # No se aborta: la línea se escribe AL ARRANCAR, así que si el servicio
    # llevaba rato arriba ya pasó. Se dice cómo comprobarlo en vez de fingir.
    aviso "No se vio la línea de arranque (puede haber pasado ya). Compruebe con:"
    aviso "  journalctl -u nasd --since '10 min ago' -o cat | grep -i latido"
  fi

  echo
  aviso "LO QUE ESTE MONTAJE NO DEMUESTRA, y conviene saberlo:"
  aviso "  El latido dice «el proceso sigue mandando señal». NO es una prueba de"
  aviso "  integridad: alguien con privilegios en el nodo podría seguir emitiéndolo"
  aviso "  con el servicio comprometido debajo. El testigo detecta AUSENCIA, no"
  aviso "  compromiso — ADR-0074."
  echo
  aviso "LA ÚNICA PRUEBA FALSABLE DEL TESTIGO exige esperar:"
  aviso "  sudo systemctl stop nasd   → a los ~95 min debe llegarle el 🟠 por Telegram"
  aviso "  sudo systemctl start nasd  → y a continuación el aviso de recuperación"
fi

echo
verde "Paso completado."
