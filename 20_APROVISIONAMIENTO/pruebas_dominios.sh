#!/bin/bash
# Ensayo local; nunca lee configuracion de produccion ni llama a la red.
set -euo pipefail
cd -- "$(dirname -- "$0")"
# shellcheck source=dominios.sh
. ./dominios.sh
ensayo=$(mktemp -d)
trap 'rm -rf -- "$ensayo"' EXIT
printf 'DOMINIO=anterior\nTESTIGO=no-leer\n' > "$ensayo/legado"
[ "$(nas_fqdn web "$ensayo/nuevo" "$ensayo/legado")" = anterior.duckdns.org ]
printf 'FQDN_WEB=NAS.ejemplo.org\nFQDN_VPN=vpn.ejemplo.org\n' > "$ensayo/nuevo"
[ "$(nas_fqdn web "$ensayo/nuevo" "$ensayo/legado")" = nas.ejemplo.org ]
[ "$(nas_fqdn vpn "$ensayo/nuevo" "$ensayo/legado")" = vpn.ejemplo.org ]
for invalido in '' nas 'https://nas.ejemplo.org' 'nas.ejemplo.org:443' '*.ejemplo.org' '-nas.ejemplo.org' 'nas..org' 'nas.ejemplo.org;id'; do
  printf 'FQDN_WEB=%s\n' "$invalido" > "$ensayo/nuevo"
  if nas_fqdn web "$ensayo/nuevo" "$ensayo/legado" 2>/dev/null; then
    echo "Se acepto nombre invalido: $invalido" >&2; exit 1
  fi
done
printf 'FQDN_WEB=nas.ejemplo.org\n' > "$ensayo/nuevo"
if nas_fqdn vpn "$ensayo/nuevo" "$ensayo/legado" 2>/dev/null; then
  echo 'Una configuracion parcial recupero el dominio antiguo' >&2; exit 1
fi
echo 'OK: legado, web/VPN separados, normalizacion y 9 fallos de configuracion'
