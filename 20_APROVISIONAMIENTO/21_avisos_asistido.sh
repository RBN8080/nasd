#!/bin/bash
# Assistant for writing /etc/nasd/avisos - ADR-0073 and ADR-0074.
#
#   20_avisos.sh                 installs the credential and verifies; THIS
#                                script only WRITES it
#   04_SEGURIDAD.md §6.septies   what leaves the node and what does not
#   P4                           secrets NEVER enter the repository
#
# WHY THIS SCRIPT EXISTS, AND IT IS NOT CONVENIENCE:
#
# 20_avisos.sh assumes /etc/nasd/avisos already exists, and writing it by hand
# means knowing how to pull the chat_id out of a JSON. That made the last step
# of the layer the only one not automated, and on top of that the one handling
# the secret - exactly the wrong way round to distribute the difficulty.
#
# THE SECRET IS TYPED HERE AND DOES NOT LEAVE HERE. The four ways a token
# escapes are closed on purpose:
#
#   1. The SCREEN         "read -rs": it is not shown while being typed.
#   2. The HISTORY        it is not an argument to a command, so .bash_history
#                         never sees it. Writing it with "sudo tee" would.
#   3. The PROCESS LIST   "curl -K -" reads the URL from standard input. The Bot
#                         API requires the token in the URL PATH, so a plain
#                         curl would leave it visible to any user on the node
#                         with "ps". It is the defect 11_ddns.sh still has with
#                         the DuckDNS token.
#   4. The JOURNAL        nothing this script prints carries the token, not even
#                         in error messages.
#
# Idempotent: if the file already exists, it asks before overwriting.
#
# Usage: sudo ./21_avisos_asistido.sh

set -euo pipefail

CONF=/etc/nasd/avisos
AQUI="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

rojo()  { printf '\033[31m%s\033[0m\n' "$*"; }
verde() { printf '\033[32m%s\033[0m\n' "$*"; }
aviso() { printf '\033[33m%s\033[0m\n' "$*"; }
gris()  { printf '\033[90m%s\033[0m\n' "$*"; }

[ "$(id -u)" -eq 0 ] || { rojo "Ejecute con sudo."; exit 1; }
[ -t 0 ] || { rojo "Este script pregunta cosas: ejecútelo en una terminal, no por tubería."; exit 1; }

command -v curl >/dev/null || { echo "Instalando curl..."; apt-get install -y -qq curl; }

echo "===== Asistente de avisos del NAS ====="
echo
gris "Lo que teclee aquí NO se muestra, NO entra en el historial de su shell y"
gris "NO aparece en la lista de procesos. Se escribe solo en $CONF (0600 root)."
echo

if [ -f "$CONF" ]; then
  aviso "$CONF YA EXISTE."
  read -rp "¿Sobrescribirlo? [s/N] " r
  case "$r" in
    [sS]) ;;
    *) echo "Sin cambios."; exit 0 ;;
  esac
  echo
fi

TMP=$(mktemp); trap 'rm -f "$TMP"' EXIT

# ---------------------------------------------------------------------------
# 1. El token, y comprobar que es de verdad ANTES de seguir
# ---------------------------------------------------------------------------
echo "--- 1 de 3: el bot ---"
gris "Es el token que le dio @BotFather. Tiene la forma 123456789:AA..."
printf 'Token del bot: '
read -rs TOKEN
echo
[ -n "$TOKEN" ] || { rojo "No tecleó nada."; exit 1; }

# getMe es la comprobación barata: dice si el token vale sin depender de que
# el usuario haya mandado ya un mensaje. Separar los dos fallos importa —
# «token mal» y «aún no le has escrito» se arreglan de formas distintas.
COD=$(curl -sS -K - -o "$TMP" -w '%{http_code}' <<FIN || true
url = "https://api.telegram.org/bot${TOKEN}/getMe"
max-time = 20
FIN
)
if [ "$COD" != "200" ]; then
  rojo "Telegram rechazó ese token (HTTP $COD)."
  gris "Vuelva a @BotFather, mande /mybots y copie el token entero, con los dos puntos."
  exit 1
fi
NOMBRE=$(grep -o '"username":"[^"]*"' "$TMP" | head -1 | cut -d'"' -f4)
verde "Token correcto. El bot es @${NOMBRE:-desconocido}."
echo

# ---------------------------------------------------------------------------
# 2. El chat_id, que lo saca el script y no el operador
# ---------------------------------------------------------------------------
echo "--- 2 de 3: a qué conversación escribe ---"
gris "Abra Telegram, entre a la conversación con @${NOMBRE:-su bot} y mándele"
gris "cualquier cosa (un «hola» vale). Es lo que autoriza al bot a escribirle."
read -rp "Pulse Intro cuando lo haya mandado... " _

CHAT=""
for intento in 1 2 3; do
  curl -sS -K - -o "$TMP" <<FIN || true
url = "https://api.telegram.org/bot${TOKEN}/getUpdates"
max-time = 20
FIN
  # Sin jq: se extrae el id del objeto "chat" de cada actualización. El último
  # es el más reciente, que es la conversación en la que acaba de escribir.
  CHAT=$(grep -oE '"chat":\{"id":-?[0-9]+' "$TMP" | grep -oE '\-?[0-9]+$' | tail -1 || true)
  [ -n "$CHAT" ] && break
  if [ "$intento" -lt 3 ]; then
    aviso "Todavía no veo ningún mensaje. Mándele uno y espere un momento."
    read -rp "Pulse Intro para reintentar... " _
  fi
done

if [ -z "$CHAT" ]; then
  rojo "No llegó ningún mensaje al bot."
  echo
  gris "Dos causas posibles, y se arreglan distinto:"
  gris "  - No le escribió al bot todavía, o le escribió a otro bot."
  gris "  - Le escribió hace más de 24 h: Telegram descarta las actualizaciones"
  gris "    viejas. Mándele otro mensaje y vuelva a ejecutar este script."
  exit 1
fi
verde "Conversación encontrada: chat_id $CHAT"
echo

# ---------------------------------------------------------------------------
# 3. El testigo externo — OPCIONAL, pero se dice qué se pierde sin él
# ---------------------------------------------------------------------------
echo "--- 3 de 3: el testigo externo (se puede dejar para después) ---"
gris "Telegram NO puede avisarle de que el NAS se apagó: es un buzón, recibe lo"
gris "que le mandan, y un chat callado es igual que una noche tranquila."
gris "El testigo es quien nota la AUSENCIA. Se crea gratis en healthchecks.io:"
gris "  1. Cree la cuenta y un check con Period = 5 minutes, Grace = 90 minutes."
gris "  2. Copie su «ping URL» (https://hc-ping.com/...)."
gris "  3. En Integrations → Telegram, pulse /start en @HealthchecksBot DESDE"
gris "     ESTA MISMA CONVERSACIÓN, para que el aviso llegue donde todo lo demás."
echo
gris "Si aún no lo tiene, deje esto vacío y pulse Intro: el resto queda montado"
gris "igual y podrá volver a ejecutar este script cuando lo cree."
printf 'Ping URL del testigo (opcional): '
read -r LATIDO
LATIDO="${LATIDO#"${LATIDO%%[![:space:]]*}"}"; LATIDO="${LATIDO%"${LATIDO##*[![:space:]]}"}"

if [ -n "$LATIDO" ]; then
  # https OBLIGATORIO: esta URL ES el secreto —quien la tenga puede falsificar
  # latidos y mantener el testigo en verde con el nodo muerto—, así que mandarla
  # en claro la regalaría a cualquiera en el camino. El binario aplica la misma
  # regla y rechazaría http:// al arrancar; se falla aquí para no descubrirlo
  # con el servicio ya parado.
  case "$LATIDO" in
    https://*) ;;
    *) rojo "El testigo debe ser una URL https://. Recibido algo que no lo es."; exit 1 ;;
  esac
  COD=$(curl -sS -K - -o /dev/null -w '%{http_code}' <<FIN || true
url = "${LATIDO}"
max-time = 20
FIN
)
  if [ "$COD" != "200" ]; then
    rojo "El testigo devolvió HTTP $COD: esa URL no responde como un check válido."
    exit 1
  fi
  verde "El testigo aceptó un latido de prueba."
else
  aviso "SIN TESTIGO: si el nodo se apaga o pierde la red, NADIE FUERA SE ENTERARÁ."
  aviso "Los avisos de actividad sí le llegarán; el de «el NAS dejó de reportar», no."
fi
echo

# ---------------------------------------------------------------------------
# 4. Escribir el archivo. umask ANTES de crearlo, no chmod después.
# ---------------------------------------------------------------------------
install -d -m 0755 /etc/nasd
UMASK_PREVIA=$(umask); umask 077
{
  echo "# Secretos de la capa de avisos — ADR-0073 y ADR-0074."
  echo "# Escrito por 21_avisos_asistido.sh. NO copiar a ningún otro sitio (P4)."
  echo "telegram_token = \"$TOKEN\""
  echo "telegram_chat  = \"$CHAT\""
  [ -n "$LATIDO" ] && echo "latido_url     = \"$LATIDO\""
} > "$CONF"
umask "$UMASK_PREVIA"
chown root:root "$CONF"
chmod 0600 "$CONF"
verde "$CONF escrito (0600 root:root)."
unset TOKEN
echo

# ---------------------------------------------------------------------------
# 5. Encadenar con la instalación de verdad, que es la que verifica por efecto
# ---------------------------------------------------------------------------
if [ -x "$AQUI/20_avisos.sh" ]; then
  echo "===== Encadenando con 20_avisos.sh ====="
  echo
  exec "$AQUI/20_avisos.sh"
fi
aviso "No encuentro 20_avisos.sh junto a este script. Ejecútelo a mano:"
aviso "  sudo ./20_avisos.sh"
