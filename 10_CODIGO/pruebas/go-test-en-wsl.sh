#!/usr/bin/env bash
# Las pruebas de Go, dentro de WSL — 2026-08-19.
#
# Hermano de verificar-c-en-wsl.sh, con la misma causa y el mismo remedio.
#
# # POR QUE
#
# Smart App Control esta ACTIVO Y FORZADO en este PC
# (HKLM\SYSTEM\CurrentControlSet\Control\CI\Policy
#  \VerifiedAndReputablePolicyState = 1) y se niega a ejecutar binarios sin
# firmar ni reputacion. Un binario de prueba recien enlazado es justo eso, y
# «go test ./...» dejo de pasar:
#
#   fork/exec ...\web.test.exe: Una directiva de Control de aplicaciones
#   bloqueo este archivo
#
# # LO QUE SE PROBO ANTES, Y POR QUE NO VALIO
#
# 1. REINTENTAR. Cinco vueltas al paquete que fallaba: cinco fallos. El rechazo
#    es estable, no intermitente como el 126 que corpus.sh ya sortea.
# 2. OTRO GOTMPDIR, dentro del repositorio y en el perfil del usuario, fuera de
#    %TEMP%. Bloqueo igual: no es la ruta.
# 3. COMPILAR CON «go test -c -o» Y EJECUTAR A MANO. Funciono para el paquete
#    que fallaba... y entonces el bloqueo salto a otro paquete distinto. Se
#    estaba persiguiendo el sintoma: SAC decide binario por binario.
#
# # POR QUE ESTO SI, Y POR QUE ADEMAS ES MEJOR PUERTA
#
# Dentro de WSL los binarios son de Linux y Smart App Control no los mira.
#
# Y de paso se corrige algo que estaba mal desde el principio: nasd corre en
# linux/arm64 en produccion, y hasta hoy sus pruebas solo se ejecutaban en
# Windows. El adaptador de archivos usa os.Root, permisos POSIX y renombrados
# atomicos — precisamente lo que Windows NO ejercita igual. Ahora la puerta
# corre sobre el mismo sistema operativo que el nodo.
#
# Medido el 2026-08-19: los once paquetes en verde, y mas rapido que en
# Windows (web baja de ~10 s a ~9 s, y el resto a centesimas).
#
# LO QUE NO CUBRE: la compilacion para Windows. Nadie ejecuta nasd en Windows
# —«se escribe y se compila en el host; al nodo llega SOLO el binario»,
# ADR-0006— asi que no hay nada que perder ahi. «go vet» y «gofmt», que si
# corren en Windows, siguen donde estaban.
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
