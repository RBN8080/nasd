#!/bin/bash
# Security panel, stage 4 - the country and operator database.
#
#   00_RECTOR.md §7        the security panel and its stages
#   internal/geoip         the reader and the format; the reasoning is all there
#   ADR-0019 / ADR-0037    why this lives in estado/ and not in datos/
#   P9                     why it does NOT live on the boot medium
#
# WHAT IT SOLVES. The panel showed "203.0.113.7", which tells nobody anything.
# With this it shows "DIGITALOCEAN-ASN - DE", which can be judged without
# knowing about networks. It was the owner's explicit request on seeing the
# first version.
#
# WHY IPtoASN AND NOT MAXMIND. IPtoASN is published in the public domain (PDDL
# v1.0), as TSV, with no account and no key. GeoLite2 is free TOO, but it
# requires an account, a licence key that EXPIRES every 90 days, obliges by EULA
# to delete the database within 30 days of each new release, and its .mmdb
# format needs an external library that go.mod forbids in writing. Three new
# problems in exchange for nothing.
#
# IDEMPOTENT: it can be run as many times as wanted. Each run replaces the whole
# database atomically; if something fails halfway, the previous database stays
# in place and the service never notices.
#
# Usage: sudo ./18_geoip.sh

set -euo pipefail

FUENTE=https://iptoasn.com/data
VOLUMEN=/srv/nas
DESTINO="$VOLUMEN/estado/geoip"
NASD=/usr/local/bin/nasd
USUARIO_SERVICIO=nas

rojo()  { printf '\033[31m%s\033[0m\n' "$*"; }
verde() { printf '\033[32m%s\033[0m\n' "$*"; }
aviso() { printf '\033[33m%s\033[0m\n' "$*"; }

[ "$(id -u)" -eq 0 ] || { rojo "Ejecute con sudo."; exit 1; }

echo "===== Base de país y operador para el panel de seguridad ====="
echo

# ---------------------------------------------------------------------------
# 0. Requisitos
# ---------------------------------------------------------------------------
[ -x "$NASD" ] || { rojo "No encuentro $NASD. Despliegue el binario primero."; exit 1; }
mountpoint -q "$VOLUMEN" || { rojo "$VOLUMEN no está montado: la base vive en el disco de datos."; exit 1; }

# ---------------------------------------------------------------------------
# 1. Descargar y preparar
# ---------------------------------------------------------------------------
# Todo el trabajo sucio en un temporal que se limpia SIEMPRE, incluso si algo
# falla: son ~50 MB de TSV descomprimido que no tienen por qué sobrevivir.
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

echo "== 1. Descargando de $FUENTE =="
for f in ip2asn-v4 ip2asn-v6; do
  # --fail para que un 404 o un 500 sean un error de verdad y no un archivo
  # de cero bytes que luego el preparador rechazaría con un mensaje peor.
  curl -fsS --max-time 300 "$FUENTE/$f.tsv.gz" -o "$TMP/$f.tsv.gz" \
    || { rojo "No se pudo descargar $f.tsv.gz"; exit 1; }
  gunzip "$TMP/$f.tsv.gz"
  verde "  $f.tsv — $(wc -l < "$TMP/$f.tsv") líneas"
done

echo
echo "== 2. Preparando la base =="
# El preparador se autocomprueba contra cuatro direcciones de respuesta
# conocida y las imprime; si alguna no resuelve, sale con error y este script
# se detiene por el «set -e». Ver prepararBaseGeoIP en cmd/nasd/main.go.
install -d -m 0755 -o "$USUARIO_SERVICIO" -g "$USUARIO_SERVICIO" "$VOLUMEN/estado"
"$NASD" --preparar-geoip "$DESTINO" "$TMP/ip2asn-v4.tsv" "$TMP/ip2asn-v6.tsv"

# EL DUEÑO, Y ESTA LÍNEA NO ES CEREMONIA. Este script corre como root y el
# servicio corre como «nas». Un archivo que quede de root con permisos
# restrictivos NO lo puede leer nasd, y el síntoma sería «no aparece el país»
# sin ningún error en ninguna parte — exactamente la trampa que ADR-0055
# documentó con el registro de usuarios y que costó un diagnóstico entero.
chown "$USUARIO_SERVICIO:$USUARIO_SERVICIO" "$DESTINO"
chmod 0644 "$DESTINO"

# ---------------------------------------------------------------------------
# 3. El temporizador — decidido por el responsable el 2026-08-14
# ---------------------------------------------------------------------------
echo
echo "== 3. Refresco mensual =="
# Mensual: las asignaciones de ASN cambian despacio. Persistent=true para que
# un refresco que caiga con el nodo apagado se ejecute al arrancar en vez de
# saltarse el mes — con P-11 (el nodo se reinicia solo) eso no es hipotético.
# RandomizedDelaySec reparte la carga para no golpear la fuente a la misma
# hora que todo el mundo.
cat > /etc/systemd/system/nas-geoip.service <<EOF
[Unit]
Description=Refresca la base de pais y operador del panel de seguridad
Documentation=file://$VOLUMEN/estado/geoip
After=network-online.target srv-nas.mount
Requires=srv-nas.mount
Wants=network-online.target

[Service]
Type=oneshot
ExecStart=$(readlink -f "$0")
EOF

cat > /etc/systemd/system/nas-geoip.timer <<'EOF'
[Unit]
Description=Refresco mensual de la base de pais y operador

[Timer]
OnCalendar=monthly
Persistent=true
RandomizedDelaySec=6h

[Install]
WantedBy=timers.target
EOF

systemctl daemon-reload
systemctl enable --now nas-geoip.timer >/dev/null 2>&1 || {
  rojo "nas-geoip.timer no se pudo habilitar."; exit 1; }
verde "Temporizador mensual activo."

# ---------------------------------------------------------------------------
# 4. Comprobar el EFECTO
# ---------------------------------------------------------------------------
echo
echo "== 4. Comprobación =="
ok=0; mal=0
si() { printf '  \033[32mOK\033[0m    %s\n' "$1"; ok=$((ok+1)); }
no() { printf '  \033[31mFALLO\033[0m %s\n' "$1"; mal=$((mal+1)); }

# Nada de «A && B || C»: NO es if-then-else, y este proyecto ya lo eliminó de
# 04_verificar.sh por hacer reportar lo contrario de lo que ocurre.
if [ -s "$DESTINO" ]; then
  si "la base existe y no está vacía ($(du -h "$DESTINO" | cut -f1))"
else
  no "la base no existe o está vacía"
fi

DUENO=$(stat -c '%U' "$DESTINO" 2>/dev/null || echo "?")
if [ "$DUENO" = "$USUARIO_SERVICIO" ]; then
  si "el dueño es $USUARIO_SERVICIO: el servicio puede leerla"
else
  no "el dueño es $DUENO y el servicio corre como $USUARIO_SERVICIO: no la leerá"
fi

# La prueba que de verdad importa: que el SERVICIO la vea. Se lee del propio
# diario tras reiniciarlo, en vez de suponerlo desde aquí.
systemctl restart nasd
sleep 2
if journalctl -u nasd --since "-1 min" -o cat 2>/dev/null | grep -q "base de país/operador cargada"; then
  si "nasd la cargó al arrancar"
else
  no "nasd NO la cargó: revise 'journalctl -u nasd -n 30'"
fi

if systemctl is-enabled --quiet nas-geoip.timer; then
  si "el refresco mensual sobrevivirá a un reinicio"
else
  no "nas-geoip.timer no está habilitado"
fi

echo
echo "  ===== $ok correctas, $mal fallidas ====="
[ "$mal" -eq 0 ] || exit 1

echo
aviso "LO QUE ESTO NO CUBRE, y conviene tenerlo escrito:"
aviso "  El país y el operador salen de una base que se actualiza una vez al"
aviso "  mes, así que pueden ir por detrás de la realidad. El panel muestra"
aviso "  siempre la FECHA de la base para que se vea."
aviso "  Y solo se resuelven los orígenes de INTERNET: una dirección de la LAN"
aviso "  o del túnel no tiene operador que buscar."
echo
verde "Paso completado."
