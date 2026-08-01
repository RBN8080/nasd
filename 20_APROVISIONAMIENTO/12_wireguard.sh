#!/bin/bash
# Fase 5, paso 2 — el túnel WireGuard.
#
#   06_ACCESO_REMOTO.md §3   el diseño y por qué el túnel llega al NAS y no a la red
#   ADR-0042                 la Fase 5 da acceso completo: web Y SMB
#   ADR-0043                 puerto 61820/udp, con su justificación
#   P4                       las claves NUNCA entran en el repositorio
#
# LA DECISIÓN QUE SOSTIENE TODO ESTO, y conviene entenderla antes de tocar nada:
#
#   nasd enlaza SOLO a 192.168.1.38:80 (medido; ADR-0018 fija la dirección).
#   Si el cliente hablara con la IP del túnel, NO habría web.
#
#   La salida NO es enlazar nasd a 0.0.0.0 —eso supersedería ADR-0018— ni
#   encaminar la red doméstica —eso exige reenvío y NAT—. Es que el cliente
#   encamine 192.168.1.38/32 POR EL TÚNEL: el paquete entra por wg0 con destino
#   a una dirección LOCAL del propio nodo, el kernel lo entrega localmente y
#   nasd responde donde ya escucha.
#
#   Consecuencia: cero cambios en nasd, cero en Samba, sin reenvío, sin NAT,
#   y el túnel NO alcanza ningún otro equipo de la casa.
#
# IDEMPOTENTE, Y AQUÍ IMPORTA MÁS QUE DE COSTUMBRE: si las claves ya existen NO
# se regeneran. Regenerarlas dejaría fuera a todos los dispositivos ya
# configurados sin decir nada — el mismo cuidado que 02_instalar_samba.sh tiene
# con la contraseña.
#
# Uso: sudo ./12_wireguard.sh

set -euo pipefail

PUERTO=61820                 # ADR-0043
RED_TUNEL=10.77.0
NODO_TUNEL="$RED_TUNEL.1"
DIR=/etc/wireguard
CLIENTES="$DIR/clientes"
DDNS_CONF=/etc/nas/ddns.conf
NAS_IP=192.168.1.38          # ADR-0018: donde escucha nasd de verdad

# A-4, decidido por el responsable: los tres dispositivos.
DISPOSITIVOS=(pc iphone tableta)

rojo()  { printf '\033[31m%s\033[0m\n' "$*"; }
verde() { printf '\033[32m%s\033[0m\n' "$*"; }
aviso() { printf '\033[33m%s\033[0m\n' "$*"; }

[ "$(id -u)" -eq 0 ] || { rojo "Ejecute con sudo."; exit 1; }

echo "===== Fase 5 · túnel WireGuard ====="
echo

# ---------------------------------------------------------------------------
# 0. Requisitos
# ---------------------------------------------------------------------------
# EL EXTREMO PÚBLICO: nombre DDNS si existe, IP directa si no.
#
# Darse de alta en DuckDNS exige un formulario de navegador con OAuth, así que
# no es automatizable: lo hace el responsable. Para no bloquear el resto de la
# fase por eso, se admite arrancar con la IP pública directa —el túnel funciona
# igual— y sustituirla después. Es PROVISIONAL y el script lo grita: con IP
# dinámica, el día que el proveedor la cambie el acceso remoto muere en silencio.
PROVISIONAL=0
if [ -f "$DDNS_CONF" ]; then
  # shellcheck disable=SC1090
  . "$DDNS_CONF"
  : "${DOMINIO:?falta DOMINIO en $DDNS_CONF}"
  EXTREMO="$DOMINIO.duckdns.org:$PUERTO"
  verde "Extremo público del túnel: $EXTREMO"
else
  IP_PUBLICA=$(curl -4 -fsS --max-time 15 https://ifconfig.me 2>/dev/null || echo "")
  [ -n "$IP_PUBLICA" ] || { rojo "Sin $DDNS_CONF y no se pudo averiguar la IP pública. Ejecute ./11_ddns.sh"; exit 1; }
  EXTREMO="$IP_PUBLICA:$PUERTO"
  PROVISIONAL=1
  aviso "Sin $DDNS_CONF: se usa la IP directa $IP_PUBLICA (PROVISIONAL)."
fi

if ! command -v wg >/dev/null 2>&1; then
  echo "Instalando wireguard-tools..."
  DEBIAN_FRONTEND=noninteractive apt-get install -y -qq wireguard-tools >/dev/null
fi
command -v qrencode >/dev/null 2>&1 || {
  echo "Instalando qrencode (para configurar los móviles sin teclear claves)..."
  DEBIAN_FRONTEND=noninteractive apt-get install -y -qq qrencode >/dev/null
}

# El módulo se comprueba con RUTA ABSOLUTA. En este proyecto «no encontrado» ya
# ha significado tres veces «no está en el PATH»: charter §3.3, §3.4 y el propio
# modinfo de wireguard el 2026-07-31, que casi hace descartar esta fase entera.
/usr/sbin/modprobe wireguard 2>/dev/null || true
if ! /usr/sbin/modinfo wireguard >/dev/null 2>&1; then
  rojo "El kernel no trae WireGuard. Sin él esta fase no es posible."
  exit 1
fi
verde "Módulo wireguard disponible."

install -d -m 0700 -o root -g root "$DIR" "$CLIENTES"

# ---------------------------------------------------------------------------
# 1. Claves del nodo — se generan UNA vez
# ---------------------------------------------------------------------------
echo
echo "== 1. Claves del nodo =="
if [ -f "$DIR/nodo.key" ]; then
  verde "Ya existen; NO se regeneran (rehacerlas expulsaría a los dispositivos ya configurados)."
else
  umask 077
  wg genkey > "$DIR/nodo.key"
  wg pubkey < "$DIR/nodo.key" > "$DIR/nodo.pub"
  verde "Generadas."
fi
chmod 0600 "$DIR/nodo.key"
NODO_PUB=$(cat "$DIR/nodo.pub")

# ---------------------------------------------------------------------------
# 2. Un par de claves por dispositivo
# ---------------------------------------------------------------------------
echo
echo "== 2. Dispositivos (A-4: ${DISPOSITIVOS[*]}) =="
N=1
PARES=""
for D in "${DISPOSITIVOS[@]}"; do
  N=$((N+1))
  IP_D="$RED_TUNEL.$N"
  if [ ! -f "$CLIENTES/$D.key" ]; then
    umask 077
    wg genkey > "$CLIENTES/$D.key"
    wg pubkey < "$CLIENTES/$D.key" > "$CLIENTES/$D.pub"
    verde "  $D → $IP_D  (claves nuevas)"
  else
    verde "  $D → $IP_D  (ya tenía claves; se conservan)"
  fi
  chmod 0600 "$CLIENTES/$D.key"

  # AllowedIPs del NODO hacia el par: SOLO su dirección. Es el control
  # antisuplantación de WireGuard — un par no puede presentarse con otra IP.
  PARES="$PARES
[Peer]
# $D
PublicKey = $(cat "$CLIENTES/$D.pub")
AllowedIPs = $IP_D/32
"

  # Configuración del cliente. AllowedIPs = SOLO el NAS (A-3): el resto del
  # tráfico del teléfono sigue su camino normal y no pasa por casa.
  cat > "$CLIENTES/$D.conf" <<EOF
# NAS — perfil de $D. Generado por 12_wireguard.sh. NO compartir: lleva la clave privada.
[Interface]
PrivateKey = $(cat "$CLIENTES/$D.key")
Address = $IP_D/24

[Peer]
PublicKey = $NODO_PUB
Endpoint = $EXTREMO
# SOLO el NAS viaja por el tunel (A-3). Ni la red de casa ni el resto de
# Internet: asi el tunel no estrangula la navegacion del telefono con la
# subida domestica, medida en 12 MB/s (RNF-03).
AllowedIPs = $NAS_IP/32
# El movil cambia de red y de NAT constantemente; sin esto el tunel se cae
# en silencio al dormirse el telefono.
PersistentKeepalive = 25
EOF
  chmod 0600 "$CLIENTES/$D.conf"
done

# ---------------------------------------------------------------------------
# 3. Configuración del nodo
# ---------------------------------------------------------------------------
echo
echo "== 3. Interfaz wg0 =="
umask 077
cat > "$DIR/wg0.conf" <<EOF
# Generado por 20_APROVISIONAMIENTO/12_wireguard.sh — proyecto NAS.
# P1: si un cambio no está en el script, no existe. NO editar a mano.
# ADR-0043 (puerto) · ADR-0042 (alcance) · 06_ACCESO_REMOTO.md §3
#
# Sin PostUp ni NAT a propósito: el túnel NO encamina nada. El cliente habla
# con $NAS_IP, que es una dirección LOCAL de este nodo, así que el kernel la
# entrega localmente y «forward» sigue en policy drop.

[Interface]
Address = $NODO_TUNEL/24
ListenPort = $PUERTO
PrivateKey = $(cat "$DIR/nodo.key")
$PARES
EOF
chmod 0600 "$DIR/wg0.conf"
verde "$DIR/wg0.conf escrito con ${#DISPOSITIVOS[@]} dispositivos."

systemctl enable --now "wg-quick@wg0" >/dev/null 2>&1 || {
  rojo "wg-quick@wg0 no arrancó. Diagnóstico: journalctl -u wg-quick@wg0 -n 30"
  exit 1
}
systemctl restart "wg-quick@wg0"

# ---------------------------------------------------------------------------
# 4. Cortafuegos — lo aplica su dueño, no este script
# ---------------------------------------------------------------------------
echo
echo "== 4. Cortafuegos =="
# 03_cortafuegos.sh es el dueño ÚNICO de /etc/nftables.conf: lo reescribe
# entero. Si este script añadiera reglas por su cuenta, la próxima ejecución
# de aquel las borraría sin avisar. Aquel ya sabe incluir el bloque del túnel
# cuando existe wg0.conf, que a estas alturas ya existe.
DONDE=$(dirname "$0")
[ -x "$DONDE/03_cortafuegos.sh" ] || { rojo "No encuentro 03_cortafuegos.sh junto a este script."; exit 1; }
"$DONDE/03_cortafuegos.sh" --si >/dev/null || { rojo "03_cortafuegos.sh falló."; exit 1; }
verde "Reglas reaplicadas por su dueño."

# ---------------------------------------------------------------------------
# 5. Comprobar el EFECTO
# ---------------------------------------------------------------------------
echo
echo "== 5. Comprobación =="
ok=0; mal=0
si() { printf '  \033[32mOK\033[0m    %s\n' "$1"; ok=$((ok+1)); }
no() { printf '  \033[31mFALLO\033[0m %s\n' "$1"; mal=$((mal+1)); }

# NADA de «A && B || C»: NO es if-then-else — C se ejecuta también si A acierta
# y B falla. La v1.2.0 lo eliminó de 04_verificar.sh por este motivo exacto, y
# en un script cuyo único trabajo es informar con verdad, puede hacerle reportar
# lo contrario de lo que ocurre. Se cuenta primero y se decide después.

if ip link show wg0 >/dev/null 2>&1; then
  si "interfaz wg0 levantada"
else
  no "no existe wg0"
fi

# grep -c no sale al primer acierto, así que aquí no hay el problema de SIGPIPE
# + pipefail que este proyecto ya sufrió con «grep -q» (rector v1.16.0).
PARES_VIVOS=$(wg show wg0 peers 2>/dev/null | grep -c .)
if [ "${PARES_VIVOS:-0}" -eq "${#DISPOSITIVOS[@]}" ]; then
  si "los ${#DISPOSITIVOS[@]} dispositivos están dados de alta"
else
  no "wg reporta ${PARES_VIVOS:-0} pares y se esperaban ${#DISPOSITIVOS[@]}"
fi

N_ESCUCHA=$(ss -ulnp 2>/dev/null | grep -c ":$PUERTO ")
if [ "${N_ESCUCHA:-0}" -gt 0 ]; then
  si "escuchando en $PUERTO/udp (ADR-0043)"
else
  no "nada escucha en $PUERTO/udp"
fi

REGLAS_AHORA=$(nft list ruleset 2>/dev/null)
N_REGLA=$(printf '%s' "$REGLAS_AHORA" | grep -c "udp dport $PUERTO accept")
if [ "${N_REGLA:-0}" -gt 0 ]; then
  si "el cortafuegos acepta $PUERTO/udp"
else
  no "el cortafuegos NO acepta $PUERTO/udp"
fi

# Que «forward» siga cerrado es la prueba de que el túnel no encamina nada.
N_FWD=$(printf '%s' "$REGLAS_AHORA" | grep -A1 'hook forward' | grep -c 'policy drop')
if [ "${N_FWD:-0}" -gt 0 ]; then
  si "forward sigue en policy drop: el túnel no alcanza el resto de la casa"
else
  no "forward ya NO está en drop — el túnel podría llegar a otros equipos"
fi

if systemctl is-enabled --quiet "wg-quick@wg0"; then
  si "el túnel se levanta solo al arrancar"
else
  no "wg-quick@wg0 no está habilitado: no sobrevivirá a un reinicio"
fi

echo
echo "  ===== $ok correctas, $mal fallidas ====="
[ "$mal" -eq 0 ] || exit 1

# ---------------------------------------------------------------------------
# 6. Lo que falta, y no lo puede hacer este script
# ---------------------------------------------------------------------------
if [ "$PROVISIONAL" -eq 1 ]; then
  echo
  rojo "EXTREMO PROVISIONAL — esto caduca solo:"
  rojo "  los perfiles apuntan a la IP $EXTREMO, no a un nombre."
  rojo "  La IP es DINÁMICA: el día que el proveedor la cambie, el acceso"
  rojo "  remoto dejará de funcionar SIN NINGÚN AVISO y los perfiles habrá"
  rojo "  que regenerarlos uno a uno."
  rojo "  Se arregla con ./11_ddns.sh y volviendo a ejecutar este script."
fi

echo
aviso "FALTAN DOS COSAS Y NINGUNA ES AUTOMATIZABLE:"
aviso ""
aviso "  1. REDIRIGIR EL PUERTO EN EL ROUTER"
aviso "       protocolo UDP · puerto externo $PUERTO · interno $PUERTO"
aviso "       destino 192.168.1.38"
aviso "     Es una pantalla web de un aparato ajeno: rompe P1 y no tiene arreglo."
aviso ""
aviso "  2. CONFIGURAR CADA DISPOSITIVO"
aviso "     Móviles — mostrar el QR y escanearlo desde la app WireGuard:"
for D in "${DISPOSITIVOS[@]}"; do
  aviso "       sudo qrencode -t ansiutf8 < $CLIENTES/$D.conf"
done
aviso "     PC — copiar el perfil e importarlo en la aplicación de WireGuard:"
aviso "       sudo cat $CLIENTES/pc.conf"
aviso ""
aviso "LOS PERFILES LLEVAN CLAVE PRIVADA: no se envían por chat ni por correo,"
aviso "y NUNCA entran en el repositorio (P4)."
aviso ""
aviso "LA PRUEBA DE VERDAD: con los DATOS MÓVILES del teléfono y el WiFi"
aviso "APAGADO, abrir http://192.168.1.38 y \\\\192.168.1.38\\datos."
aviso "Probarlo desde casa NO demuestra nada."

echo
verde "Paso completado."
