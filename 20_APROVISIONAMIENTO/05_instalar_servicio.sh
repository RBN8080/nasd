#!/bin/bash
# Phase 2, step 1 - install the nasd web service on the node.
#
#   D-05 / ADR-0005   no Docker: one binary and one systemd unit
#   D-06 / ADR-0006   the binary is built on the host and arrives by scp
#   ADR-0032          it listens on port 80, so the address alone is enough
#   ADR-0018          it binds to the LAN address, not to 0.0.0.0
#   ADR-0019          volume at /srv/nas
#   ADR-0020          it runs as the nas user, the same one that writes over SMB
#
# IT COMPILES NOTHING HERE: the binary must have been copied to /tmp/nasd
# beforehand, from the host with "make desplegar" or scp. Building on the node
# would compete with the very service being tested.
#
# ADR-0096 relaxes that for the FIRST install only: instalar.sh will build on
# the node if it can, because at that moment nasd is not running yet and there
# is nothing to compete with. For an existing node, "make desplegar" still rules.
#
# Usage: sudo ./05_instalar_servicio.sh [binary-path]

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
aviso() { printf '\033[33m%s\033[0m\n' "$*"; }

[ "$(id -u)" -eq 0 ] || { rojo "Ejecute con sudo."; exit 1; }

# Los seis valores de esta red — ADR-0095. De aquí salen las dos redes que
# nasd cuenta como «de casa», que es lo que decide quién puede borrar, mover y
# dar de alta desde dentro.
AJUSTES=/etc/nas/ajustes.conf
[ -f "$AJUSTES" ] || { rojo "Falta $AJUSTES. Cópielo de ajustes.conf.ejemplo y edítelo (ver LEEME.md)."; exit 1; }
# shellcheck disable=SC1090  # ruta fija conocida, no una variable arbitraria
. "$AJUSTES"
: "${RED:?falta RED en $AJUSTES}"
: "${RED_TUNEL:?falta RED_TUNEL en $AJUSTES}"
: "${NODO_IP:?falta NODO_IP en $AJUSTES}"

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
#
# LA ARQUITECTURA SE PREGUNTA, NO SE ESCRIBE. Hasta el 2026-09-15 esta
# comprobación exigía «aarch64» literal, y el comentario de encima ya decía
# «ESTA arquitectura»: el código nombraba una sola. Consecuencia medida al
# abrir la Fase 8 — en cualquier nodo que no fuera una Raspberry Pi de 64
# bits, el paso rechazaba el binario que el propio nodo acababa de compilar.
#
# «file» describe la máquina con otras palabras que «uname», así que hace
# falta la tabla: no es un «por si acaso», es la traducción entre dos
# vocabularios. Y una arquitectura que no esté en la tabla DETIENE la
# instalación en vez de dejarla pasar: no poder comprobar no es haber
# comprobado (misma regla que 09_verificar_operacion.sh §7).
if [ "$INSTALAR_BINARIO" = "1" ]; then
  ARCO=$(file -b "$ORIGEN" 2>/dev/null || echo desconocido)
  MAQUINA=$(uname -m)
  case "$MAQUINA" in
    aarch64)       ESPERADO="aarch64"   ;;
    x86_64)        ESPERADO="x86-64"    ;;
    armv6l|armv7l) ESPERADO="ARM, EABI" ;;
    *)
      rojo "No sé cómo describe «file» un binario de $MAQUINA, así que NO se puede comprobar."
      echo "       Instalar un binario sin comprobar su arquitectura es justo el fallo que"
      echo "       esta línea existe para impedir, así que se detiene aquí."
      echo "       El binario que se iba a instalar es:  $ARCO"
      exit 1
      ;;
  esac
  case "$ARCO" in
    *"$ESPERADO"*) ;;
    *)
      rojo "El binario no es para esta máquina ($MAQUINA): $ARCO"
      echo "       Se esperaba encontrar «$ESPERADO» en esa descripción."
      echo "       Compílelo para $MAQUINA y vuelva a copiarlo a $ORIGEN."
      exit 1
      ;;
  esac
fi

# La IP de la LAN, para ADR-0018: se enlaza a la interfaz declarada, no a
# 0.0.0.0. El cortafuegos es el segundo control, no el único.
IP=$(hostname -I | awk '{print $1}')

# LA DIRECCIÓN SE MIDE, PERO NODO_IP TIENE QUE COINCIDIR, y por eso se avisa
# en vez de elegir una en silencio: 12_wireguard.sh reparte perfiles que
# encaminan NODO_IP concretamente, así que si el nodo escucha en otra, el
# túnel se levanta y la web no aparece — sin que nada falle.
if [ "$IP" != "$NODO_IP" ]; then
  aviso "La dirección real del nodo es $IP y $AJUSTES dice $NODO_IP."
  aviso "Se usará $IP para escuchar, pero los perfiles de WireGuard apuntarán a $NODO_IP."
  aviso "Fije la IP en el router o corrija NODO_IP antes de repartir perfiles."
fi
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

# Qué cuenta como «de casa» — ADR-0095. Salen de /etc/nas/ajustes.conf, que es
# el mismo archivo del que 02_instalar_samba.sh y 03_cortafuegos.sh sacan la
# suya: una red declarada una vez, no tres copias que hay que recordar.
#
# Se escriben aquí aunque el binario sepa derivar la LAN del /24 de
# «direccion»: la derivación es la red de seguridad para un nodo que ya
# existía, no la forma de configurar uno nuevo.
lan = "$RED"
tunel = "$RED_TUNEL"

[sesion]
# Tope deslizante de inactividad — ADR-0059, con el valor de ADR-0082. NO es
# plazos.inactividad_s de abajo: aquel es el plazo de E/S de ADR-0026 dentro
# de UNA petición; este es cuánto puede estar la sesión entera sin actividad
# antes de caducar. 12 h: los 5 min originales echaban fuera a quien mira el
# panel en vivo, porque un flujo SSE no cuenta como actividad.
inactividad_s = 43200

[plazos]
inactividad_s = 60
EOF
  chmod 0644 "$CONFIG"
  verde "$CONFIG creado."
fi

# --- Fase 6 · bloque TLS (ADR-0046) -----------------------------------------
#
# Se añade SOLO si el certificado existe, y solo si no estaba ya. Mismo
# criterio que el 443 en 03_cortafuegos.sh: sin certificado, nasd sirve solo
# HTTP, que es el estado correcto y no un fallo.
#
# Va aquí y no a mano porque este script es el dueño del TOML. Editarlo por
# fuera es lo que ADR-0040 llamó doble propiedad, y ya costó una vez.
CERT_TLS=/etc/nas/tls/fullchain.pem
CLAVE_TLS=/etc/nas/tls/privkey.pem
if [ -f "$CERT_TLS" ] && [ -f "$CLAVE_TLS" ]; then
  if grep -q '^\[tls\]' "$CONFIG"; then
    echo "El bloque [tls] ya está en $CONFIG; no se toca."
  else
    cp "$CONFIG" "$CONFIG.bak.$(date +%Y%m%d%H%M%S)"
    # direccion_tls va dentro de [red], que ya existe; el resto en su sección.
    sed -i "/^puerto = /a\\
\\
# Fase 6 (ADR-0048): el 443 es la superficie que se abre a proposito.\\
# El 80 se queda en la IP de la LAN, como estaba.\\
direccion_tls = \"::\"\\
puerto_tls = 443" "$CONFIG"
    cat >> "$CONFIG" <<EOF

[tls]
# Emitidos por 15_tls.sh mediante DNS-01, sin abrir ningun puerto.
# nasd los relee cuando cambian de fecha: la renovacion no exige reiniciar.
certificado = "$CERT_TLS"
clave = "$CLAVE_TLS"
EOF
    verde "Bloque TLS añadido a $CONFIG (ADR-0046)."
  fi
else
  aviso "Sin certificado en $CERT_TLS: nasd servirá solo HTTP. Ejecute ./15_tls.sh"
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
# AF_INET6 hace falta desde la Fase 6, y su ausencia era un candado INVISIBLE:
# con «direccion_tls = "::"» el enlace parece correcto y el puerto aparece
# abierto, pero systemd le prohibe al proceso crear sockets IPv6 y no entra
# nadie. Como la unica via de entrada viable desde Internet es IPv6 —el CGNAT
# complica la IPv4 (ADR-0044)—, sin esta linea la Fase 6 esta muerta sin que
# nada lo delate.
RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX

# ADR-0032 — puerto 80 sin elevar el proceso.
#
# Enlazar un puerto <1024 exige CAP_NET_BIND_SERVICE. systemd se la concede
# al proceso, que SIGUE siendo el usuario nas sin shell. El bounding set la
# deja como UNICA capacidad posible: no puede adquirir ninguna otra.
AmbientCapabilities=CAP_NET_BIND_SERVICE
CapabilityBoundingSet=CAP_NET_BIND_SERVICE

# ADR-0036 — acceso al firmware para leer la limitacion del SoC (RNF-11).
#
# MEDIDO con systemd-run, no supuesto: este kernel no publica get_throttled en
# sysfs, y vcgencmd abre /dev/vcio_gencmd (0660 root:video) — comprobado con
# strace—, no /dev/vcio ni /dev/vchiq.
#
# Las TRES lineas hacen falta y cada una hace algo distinto:
#   SupplementaryGroups  permiso de grupo sobre el nodo
#   DeviceAllow          abre la politica de cgroup que PrivateDevices cierra
#   BindPaths            HACE APARECER el nodo en el /dev privado; DeviceAllow
#                        por si solo NO lo crea (probado)
#
# PrivateDevices=yes SE MANTIENE: el /dev del servicio queda con los nodos
# minimos mas vcio_gencmd y nada mas.
SupplementaryGroups=video
DeviceAllow=/dev/vcio_gencmd rw
BindPaths=/dev/vcio_gencmd

# Tercera capa de contención de rutas (04_SEGURIDAD §2).
ReadWritePaths=/srv/nas

# ADR-0055 — el registro de cuentas vive en /var/lib/nasd.
#
# systemd crea el directorio con el dueño del servicio ANTES de arrancar, lo
# anade a ReadWritePaths por su cuenta (hace falta: ProtectSystem=strict deja
# todo lo demas de solo lectura) y publica la ruta en $STATE_DIRECTORY.
#
# NO va en /srv/nas, y la diferencia importa: alli lo veria SMB, y ese disco
# esta pensado para sobrevivir a la placa y viajar — justo lo que no se quiere
# de unas credenciales.
#
# 0700: el registro no es asunto de nadie mas que del servicio.
StateDirectory=nasd
StateDirectoryMode=0700

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

# Fase 4 / ADR-0035 — límite de tasa del diario POR UNIDAD.
#
# journald descarta mensajes cuando una unidad supera su cupo, y lo hace en
# silencio. Con el valor por omisión, una ráfaga de peticiones —el servicio
# anota una línea por cada una— podría llevarse por delante justo lo que no
# puede perderse: los BORRADOS de RF-19, único rastro de lo destruido con
# D-15 (sin papelera) y D-12 (copia única).
#
# No se pone a 0 —sería una puerta para llenar el medio de arranque, que por
# P9 es consumible—: el tamaño total lo acota SystemMaxUse en journald.conf.
LogRateLimitIntervalSec=30s
LogRateLimitBurst=20000

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
