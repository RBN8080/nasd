#!/usr/bin/env bash
# Desinfectantes de C dentro de WSL — 2026-08-19.
#
# # POR QUE YA NO SE EJECUTAN EN WINDOWS
#
# Smart App Control esta ACTIVO Y FORZADO en este PC
# (HKLM\SYSTEM\CurrentControlSet\Control\CI\Policy
#  \VerifiedAndReputablePolicyState = 1) y se niega a ejecutar binarios sin
# firmar ni reputacion. El binario que zig compila con -fsanitize es
# exactamente eso, y el rechazo es ESTABLE, no intermitente: cinco vueltas,
# cinco bloqueos con codigo 126. Tampoco depende de donde este el archivo — se
# probo copiandolo fuera del repositorio, a %TEMP%, y bloqueo igual.
#
# Esto NO es el mismo defecto que corpus.sh documenta el 19/08. Aquel era el
# antivirus reteniendo un archivo recien escrito unas decimas, y se cura
# esperando. Este no se cura esperando: hay que no pedirle a Windows que
# ejecute el binario.
#
# # POR QUE WSL Y NO OTRA COSA
#
# Porque el binario pasa a ser de Linux y Smart App Control no lo mira. El
# corpus se ejecuta ENTERO y con los mismos desinfectantes — medido el
# 2026-08-19: 11 casos de nas-miniatura y 27 de nas-sensor, cero fallos.
#
# Y porque no cuesta nada nuevo: analisis.c y miniatura.c se escribieron ya sin
# incluir NADA de Linux, precisamente para que su correccion fuera comprobable
# fuera del nodo (ver la nota de SRC_SENSOR en el Makefile).
#
# CAMBIA EL COMPILADOR, de zig a gcc, y eso es una GANANCIA de la que conviene
# no presumir demasiado: dos compiladores distintos sobre el mismo -Wall -Wextra
# -Werror encuentran cosas distintas. Lo que se pierde es que zig ya no
# comprueba en cada vuelta que el codigo sigue compilando con el; eso lo sigue
# haciendo «make compilar», que cruza a aarch64 con zig y esta en el camino de
# «desplegar».
#
# La alternativa de raiz —apagar Smart App Control— es una decision del
# responsable sobre su equipo: en Windows 11 solo se puede apagar, y volver a
# encenderlo exige reinstalar el sistema.
set -euo pipefail

DISTRO=${DISTRO:-Ubuntu}
BANDERAS="-std=c11 -Wall -Wextra -Werror -O0 -g -fsanitize=address,undefined"

# «pwd -W» da la ruta en forma de Windows, que es lo unico que wslpath entiende.
# El «tr» quita el retorno de carro que deja la tuberia entre los dos mundos.
ruta_win=$(pwd -W)
ruta_wsl=$(wsl.exe -d "$DISTRO" -- wslpath "$ruta_win" | tr -d '\r')

if [ -z "$ruta_wsl" ]; then
  echo "FALLO: WSL no supo traducir la ruta $ruta_win" >&2
  exit 1
fi

echo ">> nas-miniatura y nas-sensor: desinfectantes dentro de WSL ($DISTRO)"

# Los binarios se dejan en /tmp DE WSL y no en el arbol: aqui no pintan nada, y
# «desplegar» exige el repositorio limpio (D-24).
wsl.exe -d "$DISTRO" -- bash -lc "
  set -euo pipefail
  cd '$ruta_wsl'

  gcc $BANDERAS -o /tmp/nas_miniatura_verificar nas-miniatura/miniatura.c
  bash nas-miniatura/pruebas/corpus.sh /tmp/nas_miniatura_verificar

  gcc $BANDERAS -o /tmp/nas_sensor_verificar nas-sensor/analisis.c nas-sensor/pruebas/arnes.c
  bash nas-sensor/pruebas/corpus.sh /tmp/nas_sensor_verificar

  rm -f /tmp/nas_miniatura_verificar /tmp/nas_sensor_verificar
"
