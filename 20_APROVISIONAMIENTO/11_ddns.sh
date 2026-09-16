#!/bin/bash
# Phase 5, step 1 - a stable name for an address that changes (DDNS).
#
#   06_ACCESO_REMOTO.md §4   the decision and its reason
#   ADR-0042                 phase 6 requires TLS; DuckDNS supports TXT
#   P4                       the token NEVER enters the repository
#   P8                       no daemon: a timer and curl
#
# WHY DUCKDNS AND NOT ANOTHER, and the deciding reason is NOT the price:
#
#   Phase 6 needs a certificate, and ADR-0042 forbids exposing anything without
#   TLS. With the HTTP-01 method, port 80 would have to be OPEN in order to get
#   the certificate - that is, expose without TLS in order to obtain TLS. A
#   closed circle. DuckDNS supports TXT records, so the certificate can be
#   requested over DNS-01 WITHOUT OPENING A SINGLE PORT. It is chosen today so
#   it does not have to be redone tomorrow.
#
#   Free No-IP was discarded for requiring the account to be reconfirmed every
#   30 days: one lapse and remote access dies with no warning.
#
# THE TOKEN IS NOT PASSED AS AN ARGUMENT OR IN A MESSAGE. It is written by hand
# on the node, once. If the file does not exist, this script explains how and
# stops.
#
# Idempotent. P1: if a change is not in a script, it does not exist.
#
# Usage: sudo ./11_ddns.sh

set -euo pipefail

CONF=/etc/nas/ddns.conf
ACTUALIZADOR=/usr/local/sbin/nas-ddns.sh
UNIDAD=/etc/systemd/system/nas-ddns.service
TEMPORIZADOR=/etc/systemd/system/nas-ddns.timer
INTERVALO=5min

rojo()  { printf '\033[31m%s\033[0m\n' "$*"; }
verde() { printf '\033[32m%s\033[0m\n' "$*"; }
aviso() { printf '\033[33m%s\033[0m\n' "$*"; }

[ "$(id -u)" -eq 0 ] || { rojo "Ejecute con sudo."; exit 1; }

echo "===== Fase 5 · DDNS ====="
echo

# ---------------------------------------------------------------------------
# 1. El testigo, que llega de fuera de este script
# ---------------------------------------------------------------------------
if [ ! -f "$CONF" ]; then
  rojo "No existe $CONF."
  echo
  aviso "QUÉ HACER, una sola vez:"
  cat <<'AYUDA'

  1. Entre en https://www.duckdns.org y acceda con Google o GitHub.
  2. Cree un subdominio. Quedará como  ALGO.duckdns.org
  3. Copie el "token" que aparece arriba de la página.
  4. En el nodo, escriba EXACTAMENTE esto (sustituyendo lo que corresponda):

       sudo install -d -m 0755 /etc/nas
       sudo tee /etc/nas/ddns.conf >/dev/null <<'FIN'
       DOMINIO=ALGO
       TESTIGO=el-token-que-copio
       FIN
       sudo chmod 0600 /etc/nas/ddns.conf

  5. Vuelva a ejecutar este script.

  EL TESTIGO NO SE ENVÍA POR CHAT NI SE GUARDA EN EL REPOSITORIO (P4).
  Es una credencial: quien la tenga puede apuntar su dominio a donde quiera.

AYUDA
  exit 1
fi

# Los seis valores de esta red — ADR-0095. De aquí salen la interfaz y, por
# derivación, el sufijo de la dirección IPv6 fija.
AJUSTES=/etc/nas/ajustes.conf
[ -f "$AJUSTES" ] || { rojo "Falta $AJUSTES. Cópielo de ajustes.conf.ejemplo y edítelo (ver LEEME.md)."; exit 1; }
# shellcheck disable=SC1090  # ruta fija conocida, no una variable arbitraria
. "$AJUSTES"
: "${INTERFAZ:?falta INTERFAZ en $AJUSTES}"
: "${NODO_IP:?falta NODO_IP en $AJUSTES}"
SUFIJO="${NODO_IP##*.}"

chmod 0600 "$CONF"
chown root:root "$CONF"
# shellcheck disable=SC1090  # ruta fija conocida, no una variable arbitraria
. "$CONF"
: "${DOMINIO:?falta DOMINIO en $CONF}"
: "${TESTIGO:?falta TESTIGO en $CONF}"
verde "Configuración leída: $DOMINIO.duckdns.org"

command -v curl >/dev/null || { echo "Instalando curl..."; apt-get install -y -qq curl; }

# ---------------------------------------------------------------------------
# 2. El actualizador
# ---------------------------------------------------------------------------
cat > "$ACTUALIZADOR" <<'EOF'
#!/bin/bash
# Generado por 20_APROVISIONAMIENTO/11_ddns.sh — proyecto NAS.
# P1: si un cambio no está en el script, no existe. NO editar a mano.
set -uo pipefail

CONF=/etc/nas/ddns.conf
# shellcheck disable=SC1090
. "$CONF"

# Los ajustes del nodo (ADR-0095). Se leen AQUÍ, en cada ejecución, y no se
# incrustan al generar este archivo: así cambiar la interfaz o la dirección en
# /etc/nas/ajustes.conf llega al temporizador sin volver a ejecutar 11_ddns.sh.
AJUSTES=/etc/nas/ajustes.conf
# shellcheck disable=SC1090
. "$AJUSTES"
SUFIJO="${NODO_IP##*.}"

# DuckDNS solo autodetecta IPv4. Cuando existe la dirección fija de la Fase 6,
# se envían LOS DOS valores explícitos: su API deja de autodetectar «ip» al
# recibir «ipv6». Así el AAAA no conserva una SLAAC que el router no autoriza.
IPV6_FIJA=$(ip -6 -o addr show dev "$INTERFAZ" scope global 2>/dev/null \
  | awk -v suf="::$SUFIJO/64" 'substr($4, length($4)-length(suf)+1) == suf {sub(/\/.*/, "", $4); print $4; exit}')

if [ -n "$IPV6_FIJA" ]; then
  IPV4_PUBLICA=$(curl -4 -fsS --max-time 15 https://ifconfig.me 2>/dev/null || echo "")
  [ -n "$IPV4_PUBLICA" ] || {
    logger -t nas-ddns -p user.err "No se pudo medir la IPv4 pública; no se actualizó ${DOMINIO}"
    exit 1
  }
  RESP=$(curl -fsS --max-time 20 \
    "https://www.duckdns.org/update?domains=${DOMINIO}&token=${TESTIGO}&ip=${IPV4_PUBLICA}&ipv6=${IPV6_FIJA}" 2>&1)
else
  RESP=$(curl -fsS --max-time 20 \
    "https://www.duckdns.org/update?domains=${DOMINIO}&token=${TESTIGO}&ip=" 2>&1)
fi

if [ "$RESP" = "OK" ]; then
  exit 0
fi

# Solo se registra el FALLO. Un acierto cada 5 minutos serían 288 líneas
# idénticas al día sobre un plato de disco (ADR-0037) que no aportan nada.
# Mismo criterio que las alertas de 05_OPERACION.md: se avisa al cambiar.
logger -t nas-ddns -p user.err "DuckDNS no aceptó la actualización de ${DOMINIO}: ${RESP}"
exit 1
EOF
chmod 0700 "$ACTUALIZADOR"
verde "$ACTUALIZADOR escrito."

# ---------------------------------------------------------------------------
# 3. Temporizador
# ---------------------------------------------------------------------------
cat > "$UNIDAD" <<EOF
# Generado por 20_APROVISIONAMIENTO/11_ddns.sh — proyecto NAS. NO editar (P1).
[Unit]
Description=Actualizar el nombre DuckDNS del NAS
After=network-online.target
Wants=network-online.target

[Service]
Type=oneshot
ExecStart=$ACTUALIZADOR
EOF

cat > "$TEMPORIZADOR" <<EOF
# Generado por 20_APROVISIONAMIENTO/11_ddns.sh — proyecto NAS. NO editar (P1).
#
# Cada $INTERVALO. La IP doméstica cambia pocas veces al mes, pero cuando
# cambia el acceso remoto queda roto hasta la siguiente actualización: ese
# intervalo es el tiempo máximo de corte, y es lo que se está eligiendo aquí.
[Unit]
Description=Actualizar DuckDNS cada $INTERVALO

[Timer]
OnBootSec=1min
OnUnitActiveSec=$INTERVALO
AccuracySec=30s

[Install]
WantedBy=timers.target
EOF
verde "Unidad y temporizador escritos."

systemctl daemon-reload
systemctl enable --now nas-ddns.timer >/dev/null 2>&1

# ---------------------------------------------------------------------------
# 4. Comprobar el EFECTO, no el archivo
# ---------------------------------------------------------------------------
echo
echo "== Comprobación =="

"$ACTUALIZADOR" || { rojo "La actualización falló. Revise DOMINIO y TESTIGO en $CONF."; exit 1; }
verde "DuckDNS aceptó la actualización."

# LO QUE DE VERDAD IMPORTA: que el nombre resuelva a la IP pública REAL. Que
# DuckDNS diga OK solo significa que aceptó la petición.
sleep 3
IP_REAL=$(curl -4 -fsS --max-time 15 https://ifconfig.me 2>/dev/null || echo "")
IP_NOMBRE=$(getent ahostsv4 "$DOMINIO.duckdns.org" 2>/dev/null | awk 'NR==1{print $1}')
IPV6_FIJA=$(ip -6 -o addr show dev "$INTERFAZ" scope global 2>/dev/null \
  | awk -v suf="::$SUFIJO/64" 'substr($4, length($4)-length(suf)+1) == suf {sub(/\/.*/, "", $4); print $4; exit}')
IPV6_NOMBRE=$(getent ahostsv6 "$DOMINIO.duckdns.org" 2>/dev/null | awk 'NR==1{print $1}')

echo "  IP pública real:        ${IP_REAL:-no se pudo leer}"
echo "  $DOMINIO.duckdns.org →  ${IP_NOMBRE:-no resuelve}"
if [ -n "$IPV6_FIJA" ]; then
  echo "  IPv6 fija del nodo:     $IPV6_FIJA"
  echo "  AAAA publicado:         ${IPV6_NOMBRE:-no resuelve}"
fi

if [ -n "$IP_REAL" ] && [ "$IP_REAL" = "$IP_NOMBRE" ]; then
  verde "El nombre resuelve a la IP pública correcta."
elif [ -z "$IP_NOMBRE" ]; then
  rojo "El nombre NO resuelve todavía. Puede tardar un minuto; reintente."
  exit 1
else
  # No se aborta: la caché del resolutor puede ir por detrás y no es un error.
  aviso "El nombre resuelve a otra IP. Puede ser caché de DNS; reintente en un minuto."
fi

if [ -n "$IPV6_FIJA" ] && [ "$IPV6_FIJA" = "$IPV6_NOMBRE" ]; then
  verde "El AAAA resuelve a la IPv6 fija autorizada en el router."
elif [ -n "$IPV6_FIJA" ] && [ -z "$IPV6_NOMBRE" ]; then
  rojo "El nombre no tiene AAAA, aunque el nodo sí tiene la IPv6 fija $IPV6_FIJA."
  exit 1
elif [ -n "$IPV6_FIJA" ]; then
  aviso "El AAAA aún apunta a $IPV6_NOMBRE; puede ser caché de DNS. Reintente en un minuto."
fi

echo
aviso "OJO — el DDNS no abre puertos ni modifica el router:"
aviso "  IPv4 sigue dependiendo de la red del operador; para IPv6, el router"
aviso "  debe permitir solo TCP/443 hacia la dirección fija terminada en ::$SUFIJO."
aviso "  No use DMZ: D-22 exige una regla mínima y específica."

echo
verde "Paso completado."
echo "Siguiente: sudo ./12_wireguard.sh"
