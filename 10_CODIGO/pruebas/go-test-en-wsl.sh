#!/usr/bin/env bash

set -euo pipefail

DISTRO=${DISTRO:-Ubuntu}

# «pwd -W» da la ruta en forma de Windows, que es lo unico que wslpath entiende.
# El «tr» quita el retorno de carro que deja la tuberia entre los dos mundos.
ruta_win=$(pwd -W)
ruta_wsl=$(wsl.exe -d "$DISTRO" -- wslpath "$ruta_win" | tr -d '\r')

if [ -z "$ruta_wsl" ]; then
  echo "FALLO: WSL no supo traducir la ruta $ruta_win" >&2
  exit 1
fi

echo ">> pruebas de Go dentro de WSL ($DISTRO)"
wsl.exe -d "$DISTRO" -- bash -lc "cd '$ruta_wsl' && go test $*"
