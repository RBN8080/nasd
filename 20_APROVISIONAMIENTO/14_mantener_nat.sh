#!/bin/bash
# Phase 5, step 8 - keep the tunnel's inbound path alive.
#
#   06_ACCESO_REMOTO §10   why it is needed, with the measurement behind it
#   ADR-0057               port 443/udp (supersedes ADR-0043)
#   Charter §7.1.3         it opens no port: it only emits outbound traffic
#
# Installs 14_mantener_nat.py as a systemd service. Idempotent.
#
# Usage: sudo ./14_mantener_nat.sh

set -euo pipefail

USUARIO=nas
DESTINO=/usr/local/lib/nas
UNIDAD=/etc/systemd/system/nas-mantener-nat.service
ORIGEN="$(dirname "$0")/14_mantener_nat.py"
WG_CONF=/etc/wireguard/wg0.conf

rojo()  { printf '\033[31m%s\033[0m\n' "$*"; }
verde() { printf '\033[32m%s\033[0m\n' "$*"; }

[ "$(id -u)" -eq 0 ] || { rojo "Ejecute con sudo."; exit 1; }
[ -f "$ORIGEN" ]     || { rojo "No encuentro $ORIGEN"; exit 1; }
id -u "$USUARIO" >/dev/null 2>&1 || { rojo "Falta el usuario '$USUARIO'."; exit 1; }

# Mismo criterio que 03_cortafuegos.sh: sin túnel configurado esto no pinta
# nada, y un servicio emitiendo tráfico sin motivo es ruido (P8).
if [ ! -f "$WG_CONF" ]; then
  rojo "No existe $WG_CONF: el túnel no está configurado."
  echo "Ejecute antes 12_wireguard.sh. No se instala nada."
  exit 1
fi

echo "== Instalando el mantenedor =="
install -d -m 0755 "$DESTINO"
install -m 0755 "$ORIGEN" "$DESTINO/mantener_nat.py"

cat > "$UNIDAD" <<EOF
[Unit]
# Mantiene viva la asociación de entrada del túnel WireGuard. Sin esto, el
# CGNAT del operador, el NAT del router y la entrada ARP del router para este
# nodo caducan por inactividad, y el túnel deja de ser alcanzable desde fuera
# sin que nada informe del fallo. Medido el 2026-08-01: ver 06_ACCESO_REMOTO §10.
Description=NAS - mantener viva la ruta de entrada de WireGuard
Documentation=file://$DESTINO/mantener_nat.py
After=network-online.target wg-quick@wg0.service
Wants=network-online.target
# Si el túnel no está levantado no hay nada que mantener.
BindsTo=wg-quick@wg0.service

[Service]
Type=simple
ExecStart=/usr/bin/python3 $DESTINO/mantener_nat.py --demonio
User=$USUARIO
Group=$USUARIO

# CAP_NET_RAW es la ÚNICA capacidad, y hace falta por un motivo concreto: el
# puerto del túnel lo tiene WireGuard, así que un socket normal no puede enlazarlo.
# Uno en crudo sí puede EMITIR con ese puerto de origen, que es lo único que se
# necesita. El proceso NO escucha nada.
AmbientCapabilities=CAP_NET_RAW
CapabilityBoundingSet=CAP_NET_RAW
NoNewPrivileges=yes

# Endurecimiento — mismo criterio que nasd.service.
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
PrivateDevices=yes
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectControlGroups=yes
RestrictAddressFamilies=AF_INET AF_INET6 AF_PACKET
RestrictNamespaces=yes
LockPersonality=yes
MemoryDenyWriteExecute=yes
SystemCallArchitectures=native

Restart=always
RestartSec=10
# No inunda el diario: una línea por refresco serían 4300 al día.
StandardOutput=null
StandardError=journal

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable nas-mantener-nat.service
systemctl restart nas-mantener-nat.service
sleep 3

if systemctl is-active --quiet nas-mantener-nat.service; then
  verde "Paso 8 completado: el mantenedor está activo y sobrevive al reinicio."
else
  rojo "El servicio NO arrancó. Diagnóstico:"
  systemctl status nas-mantener-nat.service --no-pager -l | tail -20
  exit 1
fi
