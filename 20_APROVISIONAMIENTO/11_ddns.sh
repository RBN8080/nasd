#!/bin/bash
# Fase 5, paso 1 — nombre estable para una IP que cambia (DDNS).
#
#   06_ACCESO_REMOTO.md §4   la decisión y su motivo
#   ADR-0042                 la Fase 6 exige TLS; DuckDNS admite TXT
#   P4                       el testigo NUNCA entra en el repositorio
#   P8                       sin demonio: temporizador y curl
#
# POR QUÉ DUCKDNS Y NO OTRO, y el motivo determinante NO es el precio:
#
#   La Fase 6 necesita un certificado, y ADR-0042 prohíbe exponer nada sin
#   TLS. Con el método HTTP-01 haría falta el puerto 80 ABIERTO para poder
#   obtener el certificado — es decir, exponer sin TLS para conseguir TLS.
#   Círculo cerrado. DuckDNS admite registros TXT, así que el certificado
#   podrá pedirse por DNS-01 SIN ABRIR NI UN PUERTO. Se elige hoy para no
#   tener que rehacerlo mañana.
#
#   No-IP gratuito se descartó por exigir confirmar la cuenta cada 30 días:
#   un olvido y el acceso remoto muere sin aviso.
#
# EL TESTIGO NO SE PASA POR ARGUMENTO NI POR MENSAJE. Se escribe a mano en el
# nodo, una sola vez. Si el archivo no existe, este script explica cómo y para.
#
# Idempotente. P1: si un cambio no está en un script, no existe.
#
# Uso: sudo ./11_ddns.sh

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

# ip= vacío a propósito: DuckDNS toma la IP de origen de la petición, que es
# exactamente la IP pública de la casa. Descubrirla nosotros añadiría una
# dependencia de un tercero más para averiguar algo que el destinatario ya sabe.
RESP=$(curl -fsS --max-time 20 \
  "https://www.duckdns.org/update?domains=${DOMINIO}&token=${TESTIGO}&ip=" 2>&1)

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

echo "  IP pública real:        ${IP_REAL:-no se pudo leer}"
echo "  $DOMINIO.duckdns.org →  ${IP_NOMBRE:-no resuelve}"

if [ -n "$IP_REAL" ] && [ "$IP_REAL" = "$IP_NOMBRE" ]; then
  verde "El nombre resuelve a la IP pública correcta."
elif [ -z "$IP_NOMBRE" ]; then
  rojo "El nombre NO resuelve todavía. Puede tardar un minuto; reintente."
  exit 1
else
  # No se aborta: la caché del resolutor puede ir por detrás y no es un error.
  aviso "El nombre resuelve a otra IP. Puede ser caché de DNS; reintente en un minuto."
fi

echo
aviso "OJO — esto NO abre nada todavía:"
aviso "  el nombre ya apunta a su casa, pero el router sigue sin redirigir"
aviso "  ningún puerto. Sin el paso 12 y sin la redirección, desde fuera no"
aviso "  se llega a nada. Eso es correcto y es el orden previsto."

echo
verde "Paso completado."
echo "Siguiente: sudo ./12_wireguard.sh"
