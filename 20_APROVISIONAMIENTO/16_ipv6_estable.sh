#!/bin/bash
# Phase 6, step 2 - a fixed, predictable IPv6 address for the node.
#
#   ADR-0048        443 is published; with CGNAT (ADR-0044) the viable path is IPv6
#   charter §7.1.3  the opening is done by the router, pointing at THIS address
#
# WHY A FIXED ONE IS NEEDED, and why it was removed before:
#
#   On 2026-08-01 a static one was set, it was found that the real cause of "the
#   node does not take IPv6" was the node's own firewall, and it was REMOVED for
#   being unused: configuration without a purpose. That was correct with what
#   was known then.
#
#   The premise changed hours later: the router allows opening IPv6 inbound
#   pointing at ONE SPECIFIC HOST, and that screen stores a literal address.
#   With SLAAC the address depends on the prefix the ISP delegates, so the day
#   the operator changes it the router rule would point at nobody and access
#   would die IN SILENCE - the same failure mode DDNS solves for IPv4.
#
#   The suffix is derived from the last octet of the node's IPv4 address
#   (NODO_IP in /etc/nas/ajustes.conf, ADR-0095), so that the IPv6 address ends
#   in a number already in the operator's head.
#
# DECLARED LIMIT: if the ISP changes the PREFIX, this address changes too and
# two things must be repeated - this script and the router field. That is not
# avoidable without a stable delegated prefix, which the ISP does not guarantee.
#
# Usage: sudo ./16_ipv6_estable.sh

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
