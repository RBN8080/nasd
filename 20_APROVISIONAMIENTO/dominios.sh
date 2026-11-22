#!/bin/bash
# Biblioteca sin secretos ni efectos. Sin /etc/nas/dominios.conf no hay nombre:
# falla, no adivina.
nas_fqdn() {
  local uso=${1:?web o vpn} conf=${2:-/etc/nas/dominios.conf} clave nombre
  case "$uso" in
    web) clave=FQDN_WEB ;;
    vpn) clave=FQDN_VPN ;;
    *) echo 'Uso de dominio desconocido' >&2; return 1 ;;
  esac
  [ -f "$conf" ] || { echo "Falta $conf" >&2; return 1; }
  nombre=$(sed -n "s/^${clave}=//p" "$conf") || return 1
  # Nombres DNS completos ASCII, sin puertos, URL, comodines o metacaracteres.
  if [ "${#nombre}" -gt 253 ] ||
     [[ ! "$nombre" =~ ^([a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?\.)+[a-zA-Z]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$ ]]; then
    echo "Falta un FQDN valido para $uso" >&2
    return 1
  fi
  printf '%s\n' "${nombre,,}"
}
