#!/bin/bash
# Fase 2, paso 1 — instalar el servicio web nasd en el nodo.
#
#   D-05 / ADR-0005   sin Docker: un binario y una unidad systemd
#   D-06 / ADR-0006   el binario se compila en el host y llega por scp
#   ADR-0018          escucha en la IP de la LAN, puerto 8080
#   ADR-0019          volumen en /srv/nas
#   ADR-0020          corre como el usuario nas, el mismo que escribe por SMB
#
# NO compila nada aquí: el binario debe haberse copiado antes a /tmp/nasd
# desde el host con «make desplegar» o scp. Compilar en la Pi competiría con
# el propio servicio que se está probando.
#
# Uso: sudo ./05_instalar_servicio.sh [ruta-del-binario]

set -euo pipefail

ORIGEN="${1:-/tmp/nasd}"
DESTINO=/usr/local/bin/nasd
UNIDAD=/etc/systemd/system/nasd.service
CONFIG_DIR=/etc/nasd
CONFIG="$CONFIG_DIR/nasd.toml"
PUNTO=/srv/nas
USUARIO=nas
PUERTO=80   # ADR-0032: se entra con la IP a secas, sin puerto

rojo()  { printf '\033[31m%s\033[0m\n' "$*"; }
verde() { printf '\033[32m%s\033[0m\n' "$*"; }

[ "$(id -u)" -eq 0 ] || { rojo "Ejecute con sudo."; exit 1; }
# El binario nuevo es OPCIONAL: este script también sirve para actualizar la
# unidad o la configuración sobre una instalación que ya funciona. Exigirlo
# siempre lo volvía inútil justo en ese caso —el mismo error que ya se
# corrigió con la configuración—.
if [ -f "$ORIGEN" ]; then
  INSTALAR_BINARIO=1
elif [ -x "$DESTINO" ]; then
  INSTALAR_BINARIO=0
  echo "No hay binario nuevo en $ORIGEN; se conserva el instalado en $DESTINO."
else
  rojo "No hay binario ni en $ORIGEN ni en $DESTINO. Cópielo desde el host."
  exit 1
fi
mountpoint -q "$PUNTO" || { rojo "$PUNTO no está montado. Ejecute antes 01_preparar_disco.sh"; exit 1; }
id -u "$USUARIO" >/dev/null 2>&1 || { rojo "Falta el usuario '$USUARIO'."; exit 1; }

# P2: comprobar que el binario es para ESTA arquitectura antes de instalarlo.
if [ "$INSTALAR_BINARIO" = "1" ]; then
  ARCO=$(file -b "$ORIGEN" 2>/dev/null || echo desconocido)
  case "$ARCO" in
    *aarch64*) ;;
    *) rojo "El binario no parece ARM64: $ARCO"; exit 1 ;;
  esac
fi

# La IP de la LAN, para ADR-0018: se enlaza a la interfaz declarada, no a
# 0.0.0.0. El cortafuegos es el segundo control, no el único.
IP=$(hostname -I | awk '{print $1}')
[ -n "$IP" ] || { rojo "No se pudo determinar la IP de la LAN."; exit 1; }

if [ "$INSTALAR_BINARIO" = "1" ]; then
  echo "== Instalando el binario =="
  install -m 0755 -o root -g root "$ORIGEN" "$DESTINO"
  verde "$DESTINO instalado."
fi

echo
echo "== Configuración (P4: sin secretos) =="
mkdir -p "$CONFIG_DIR"
if [ -f "$CONFIG" ]; then
  # La IP puede haber cambiado: al mover el nodo de sitio, al cambiar de
  # router, o si se pierde la reserva DHCP. ADR-0018 enlaza el servicio a la
  # IP declarada, NO a 0.0.0.0, asi que una IP obsoleta impide arrancar.
  #
  # Dejarlo en «no se toca» convertia este script en inutil justo cuando mas
  # falta hace. Se detecta y se corrige, avisando.
  ANTERIOR=$(awk -F\" '/^direccion/{print $2}' "$CONFIG")
  if [ "$ANTERIOR" != "$IP" ]; then
    rojo "La IP del nodo cambio: $ANTERIOR -> $IP"
    cp "$CONFIG" "$CONFIG.bak.$(date +%Y%m%d%H%M%S)"
    sed -i "s/^direccion = .*/direccion = \"$IP\"/" "$CONFIG"
    verde "Configuracion actualizada a $IP (copia de seguridad hecha)."
  else
    echo "Ya existe $CONFIG y la IP coincide ($IP); no se toca."
  fi
else
  cat > "$CONFIG" <<EOF
# Generado por 20_APROVISIONAMIENTO/05_instalar_servicio.sh
# P1: si un cambio no está en el script, no existe. NO editar a mano.
volumen = "$PUNTO"

[red]
direccion = "$IP"
puerto = $PUERTO

[plazos]
inactividad_s = 60
EOF
  chmod 0644 "$CONFIG"
  verde "$CONFIG creado."
fi

echo
echo "== Unidad systemd =="
cat > "$UNIDAD" <<'EOF'
[Unit]
Description=nasd — servidor de archivos del proyecto NAS
RequiresMountsFor=/srv/nas
After=network-online.target
Wants=network-online.target

[Service]
Type=notify
NotifyAccess=main
ExecStart=/usr/local/bin/nasd --config /etc/nasd/nasd.toml
Restart=on-failure
RestartSec=5s
WatchdogSec=30s

# ADR-0020: el mismo UID que escribe por SMB.
User=nas
Group=nas

# Charter §7 — endurecimiento.
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
RestrictAddressFamilies=AF_INET AF_UNIX

# ADR-0032 — puerto 80 sin elevar el proceso.
#
# Enlazar un puerto <1024 exige CAP_NET_BIND_SERVICE. systemd se la concede
# al proceso, que SIGUE siendo el usuario nas sin shell. El bounding set la
# deja como UNICA capacidad posible: no puede adquirir ninguna otra.
AmbientCapabilities=CAP_NET_BIND_SERVICE
CapabilityBoundingSet=CAP_NET_BIND_SERVICE

# Tercera capa de contención de rutas (04_SEGURIDAD §2).
ReadWritePaths=/srv/nas

# RES-01 — valores PROVISIONALES: se fijan con la medición de RNF-01.
MemoryMax=192M
Environment=GOMEMLIMIT=160MiB

# P4 — el secreto NO vive en el repositorio ni en el TOML. systemd copia el
# archivo a un tmpfs privado del servicio, legible solo por él, y expone su
# ruta en $CREDENTIALS_DIRECTORY. RF-15 y D-14 / ADR-0021.
#
# Sin esto el servicio NO ARRANCA: no existe un modo sin autenticar.
LoadCredential=web:/etc/nasd/credencial

StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
EOF
verde "$UNIDAD escrita."

echo
echo "== Credencial de la web (RF-15) =="
if [ -f /etc/nasd/credencial ]; then
  verde "Presente. Se entrega por LoadCredential= en un tmpfs privado."
else
  rojo "FALTA /etc/nasd/credencial. El servicio NO arrancará: no existe modo sin autenticar."
  rojo "Ejecute antes: sudo ./07_credencial_web.sh"
  exit 1
fi

echo
echo "== Arrancando =="
systemctl daemon-reload
systemctl enable nasd >/dev/null 2>&1
systemctl restart nasd
sleep 3

if systemctl is-active --quiet nasd; then
  verde "nasd ACTIVO."
else
  rojo "nasd NO arrancó. Últimas líneas del registro:"
  journalctl -u nasd -n 20 --no-pager -o cat
  exit 1
fi

echo
echo "== Comprobación =="
systemctl show nasd -p MainPID -p MemoryCurrent --value | paste -sd' ' | xargs echo "PID y memoria:"
ss -tlnp 2>/dev/null | grep ":$PUERTO" | head -2
echo
verde "Servicio instalado. Abra en el navegador:  http://$IP:$PUERTO"
