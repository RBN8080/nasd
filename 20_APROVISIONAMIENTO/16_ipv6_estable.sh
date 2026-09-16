#!/bin/bash
# Fase 6, paso 2 — dirección IPv6 fija y previsible para el nodo.
#
#   ADR-0048   el 443 se publica; con CGNAT (ADR-0044) la via viable es IPv6
#   charter §7.1.3  la apertura la hace el router, apuntando a ESTA direccion
#
# POR QUE HACE FALTA UNA FIJA, y por que se retiro antes:
#
#   El 2026-08-01 se puso una estatica, se comprobo que la causa real de «la
#   Pi no toma IPv6» era el cortafuegos del propio nodo, y se RETIRO por no
#   usarse: era configuracion sin proposito. Fue correcto con lo que se sabia.
#
#   La premisa cambio horas despues: el router permite abrir la entrada IPv6
#   apuntando a UN HOST CONCRETO, y esa pantalla guarda una direccion literal.
#   Con SLAAC la direccion depende del prefijo que delegue el proveedor, asi
#   que el dia que el operador lo cambie la regla del router apuntaria a nadie y el
#   acceso moriria EN SILENCIO — el mismo modo de fallo que el DDNS resuelve
#   para IPv4.
#
#   Se elige el sufijo ::38 para que coincida con el ultimo octeto de la IPv4
#   del nodo (192.168.1.38): un numero que ya esta en la cabeza de quien opera.
#
# LIMITE DECLARADO: si el proveedor cambia el PREFIJO, esta direccion cambia
# igual y hay que repetir dos cosas — este script y el campo del router. No es
# evitable sin un prefijo delegado estable, que el operador no garantiza.
#
# Uso: sudo ./16_ipv6_estable.sh

set -euo pipefail

rojo()  { printf '\033[31m%s\033[0m\n' "$*"; }
verde() { printf '\033[32m%s\033[0m\n' "$*"; }
aviso() { printf '\033[33m%s\033[0m\n' "$*"; }

# Los seis valores de esta red — ADR-0095.
AJUSTES=/etc/nas/ajustes.conf
[ -f "$AJUSTES" ] || { rojo "Falta $AJUSTES. Copielo de ajustes.conf.ejemplo y editelo (ver LEEME.md)."; exit 1; }
# shellcheck disable=SC1090  # ruta fija conocida, no una variable arbitraria
. "$AJUSTES"
: "${NODO_IP:?falta NODO_IP en $AJUSTES}"
: "${INTERFAZ:?falta INTERFAZ en $AJUSTES}"

IFAZ="$INTERFAZ"

# EL SUFIJO NO ES UN AJUSTE: SE DERIVA. Es el ultimo octeto de NODO_IP, y lo
# unico que se le pide es que la direccion IPv6 termine en el mismo numero que
# la IPv4 — asi quien lee «...::38» reconoce el mismo nodo que «192.168.1.38».
# Escribirlo aparte creaba un septimo valor cuyo unico trabajo era coincidir
# con otro, que es justo el acoplamiento que ADR-0095 quita.
SUFIJO="${NODO_IP##*.}"

[ "$(id -u)" -eq 0 ] || { rojo "Ejecute con sudo."; exit 1; }

echo "===== Fase 6 · direccion IPv6 fija ====="
echo

# El prefijo NO se escribe a mano: se aprende del anuncio del router, que es la
# unica fuente que sabe cual esta delegado ahora mismo.
PREFIJO=$(ip -6 addr show dev "$IFAZ" scope global 2>/dev/null \
          | awk '/inet6/{print $2}' | head -1 | cut -d: -f1-4)

if [ -z "$PREFIJO" ]; then
  rojo "El nodo no tiene IPv6 global: no se puede deducir el prefijo."
  echo
  echo "Causa mas probable, y ya paso una vez: el cortafuegos descarta los"
  echo "anuncios del router. IPv6 no tiene ARP — el descubrimiento de vecinos"
  echo "ES ICMPv6, y sin sus reglas el nodo nunca obtiene direccion."
  echo "Ejecute ./03_cortafuegos.sh y vuelva a intentarlo."
  exit 1
fi

DIRECCION="$PREFIJO::$SUFIJO"
verde "Prefijo delegado ahora: $PREFIJO::/64"
verde "Direccion fija a fijar: $DIRECCION"

CONEXION=$(nmcli -t -f NAME,DEVICE connection show --active | grep ":$IFAZ\$" | cut -d: -f1)
[ -n "$CONEXION" ] || { rojo "No hay conexion activa de NetworkManager en $IFAZ."; exit 1; }

# method=auto y NO manual: se conserva la ruta por defecto y el DNS que anuncia
# el router, y solo se ANADE una direccion previsible. Con «manual» habria que
# fijar tambien la puerta de enlace, que es un fe80:: que puede cambiar.
nmcli connection modify "$CONEXION" \
  ipv6.method auto \
  +ipv6.addresses "$DIRECCION/64" >/dev/null

nmcli device reapply "$IFAZ" >/dev/null 2>&1 || true
sleep 6

echo
echo "== Comprobacion =="
if ip -6 addr show dev "$IFAZ" | grep -q "${DIRECCION}/64"; then
  verde "El nodo responde en $DIRECCION"
else
  rojo "La direccion NO quedo puesta. Revise: nmcli connection show \"$CONEXION\""
  exit 1
fi

if ping6 -c 2 -W 4 -I "$DIRECCION" 2606:4700:4700::1111 >/dev/null 2>&1; then
  verde "Sale a Internet por IPv6 usando esa direccion."
else
  aviso "No sale a Internet con esa direccion de origen. Puede ser normal si el"
  aviso "proveedor filtra origenes no anunciados; la ENTRADA puede funcionar igual."
fi

echo
verde "Paso completado."
echo
aviso "AHORA, EN EL ROUTER — es lo unico que no puede hacer un script:"
echo "  Security -> DMZ -> DMZ-IPv6"
echo "    DMZ:      On"
echo "    LAN Host: $DIRECCION"
echo "    Apply"
echo
echo "Eso NO redirige nada: en IPv6 no hay NAT. Lo que hace es abrir el"
echo "cortafuegos IPv6 del router para ese host. ZTE lo llama DMZ por analogia."
echo
echo "Despues, la prueba que decide (08_WEB_EXPUESTA.md §4):"
echo "  iPhone -> Ajustes -> Wi-Fi -> DESACTIVAR, con datos moviles"
echo "  abrir  -> https://$(sed -n 's/^DOMINIO=//p' /etc/nas/ddns.conf 2>/dev/null).duckdns.org"
