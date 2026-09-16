#!/bin/bash
# Maintenance phase - installs nas-sensor, the passive touch sensor.
#
#   RF-33 / ADR-0066   the touch as a fact of its own
#   Charter §7.1.3     NOT touched: this opens no port
#
# WHAT IT DOES AND WHAT IT DOES NOT TOUCH, which is half of the owner's brief:
#
#   It does NOT touch 03_cortafuegos.sh. The sensor listens alongside the
#   firewall, not inside it. The nftables rule that was once proposed was
#   discarded with measurement - see the ADR - not deferred.
#
#   It does NOT touch nasd.service. Its hardening stays exactly as it is. nasd
#   only READS the file this service leaves behind, and it can do so with no new
#   permissions because ProtectSystem=strict leaves the system read-only, not
#   unreachable.
#
# Usage: sudo ./19_sensor.sh

set -euo pipefail

USUARIO=nassensor
UNIDAD=/etc/systemd/system/nas-sensor.service
BINARIO=/usr/local/bin/nas-sensor
SALIDA=/var/lib/nas-sensor/toques

rojo()  { printf '\033[31m%s\033[0m\n' "$*"; }
verde() { printf '\033[32m%s\033[0m\n' "$*"; }

[ "$(id -u)" -eq 0 ] || { rojo "Ejecute con sudo."; exit 1; }
[ -x "$BINARIO" ] || {
  rojo "No está $BINARIO."
  echo "Despliéguelo antes desde el host:  make desplegar"
  exit 1
}
getent group nas >/dev/null || { rojo "No existe el grupo «nas» (¿se instaló nasd?)."; exit 1; }

# USUARIO PROPIO, SEPARADO DEL DE nasd, y no es ceremonia: este proceso lleva
# CAP_NET_RAW y nasd no. Compartir usuario significaría que comprometer el
# sensor da acceso de escritura a los archivos de estado del NAS.
#
# El GRUPO sí es compartido: es lo que deja a nasd leer la salida sin tocar su
# propia unidad. La dirección del permiso importa — el sensor escribe, nasd lee.
if ! id -u "$USUARIO" >/dev/null 2>&1; then
  useradd --system --no-create-home --shell /usr/sbin/nologin --gid nas "$USUARIO"
  echo "Usuario de sistema «$USUARIO» creado."
else
  echo "El usuario «$USUARIO» ya existe."
fi

cat > "$UNIDAD" <<EOF
[Unit]
# Generado por 20_APROVISIONAMIENTO/19_sensor.sh — proyecto NAS.
Description=Sensor pasivo de toques del NAS (RF-33, ADR-0066)
Documentation=file:///home/usuario/01_NAS/ADR/0066_el_sensor_de_toques.md
After=network-online.target
Wants=network-online.target

[Service]
Type=exec
User=$USUARIO
Group=nas
ExecStart=$BINARIO $SALIDA
Restart=on-failure
RestartSec=5

# LA UNICA CAPACIDAD QUE NECESITA, y ninguna mas. CAP_NET_RAW es lo que permite
# abrir un socket AF_PACKET; sin ella el programa no arranca y con ella no puede
# hacer nada mas que mirar. No lleva CAP_NET_ADMIN: no configura la red.
AmbientCapabilities=CAP_NET_RAW
CapabilityBoundingSet=CAP_NET_RAW

# AF_PACKET Y NADA MAS. En particular NO lleva AF_NETLINK, y eso es deliberado:
# el sensor no consulta interfaces ni rutas. Es la misma leccion que obligo a
# leer /proc/net/if_inet6 en vez de usar net.Interfaces() en nasd, cuando
# aquello funcionaba por SSH y fallaba dentro del servicio.
RestrictAddressFamilies=AF_PACKET

# El directorio de salida. Modo 0750 con grupo «nas»: el sensor escribe, nasd
# lee, y nadie mas entra. UMask 0027 para que el archivo salga 0640.
StateDirectory=nas-sensor
StateDirectoryMode=0750
UMask=0027

ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
PrivateDevices=yes
NoNewPrivileges=yes
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectControlGroups=yes
RestrictNamespaces=yes
RestrictRealtime=yes
RestrictSUIDSGID=yes
LockPersonality=yes
MemoryDenyWriteExecute=yes
SystemCallArchitectures=native

# NO SE PONE MemoryMax, y conviene decir por que en vez de dejar el hueco:
# cgroup_disable=memory viene en la linea de arranque del nucleo de fabrica en
# Raspberry Pi OS, asi que ese techo NUNCA ha estado en vigor en este nodo
# (medido el 2026-08-15). Aqui el techo real es que el programa no reserva
# memoria dinamica en ningun momento: el anillo son ~128 KB estaticos.

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable --now nas-sensor.service

echo
echo "== Estado =="
systemctl is-active nas-sensor.service || true
echo
echo "== Endurecimiento (menor es mejor) =="
systemd-analyze security nas-sensor.service 2>/dev/null | tail -3 || \
  echo "systemd-analyze no disponible"

echo
verde "nas-sensor instalado y arrancado."
echo "Escribe en: $SALIDA"
echo
echo "COMPRUEBE QUE LA CAPTURA FUNCIONA SIN NECESITAR TRAFICO DE INTERNET:"
echo "  1. Espere un minuto (vuelca cada 60 s) y mire la cabecera:"
echo "       head -3 $SALIDA"
echo "  2. «total-visto» cuenta TODO lo capturado, tambien lo de casa, asi que"
echo "     un acceso al NAS desde la LAN tiene que subirlo. Si sube, la captura"
echo "     funciona; que el panel no lo ensene es lo correcto — filtra a"
echo "     Internet al pintar."
echo
echo "Y LA PRUEBA QUE DE VERDAD IMPORTA, desde el movil con DATOS MOVILES:"
echo "  tocar un puerto cerrado del IPv6 publico del nodo. Debe aparecer una"
echo "  fila en /seguridad con paquetes >= 1 y conexiones 0."
