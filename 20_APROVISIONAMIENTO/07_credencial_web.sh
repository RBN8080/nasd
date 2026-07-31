#!/bin/bash
# Fase 3, paso 1 — fijar la credencial de la web.
#
#   RF-15            la web exige autenticación
#   D-14 / ADR-0021  credencial PROPIA, distinta de la de Samba
#   P4               cero secretos en el repositorio
#
# La contraseña se teclea aquí y no viaja por ningún otro sitio: no se pasa
# como argumento —quedaría en el historial y en la lista de procesos— ni se
# escribe en el TOML versionado. Solo se guarda su derivación PBKDF2.
#
# Uso: sudo ./07_credencial_web.sh

set -euo pipefail

DESTINO=/etc/nasd/credencial
BINARIO=/usr/local/bin/nasd

rojo()  { printf '\033[31m%s\033[0m\n' "$*"; }
verde() { printf '\033[32m%s\033[0m\n' "$*"; }

[ "$(id -u)" -eq 0 ] || { rojo "Ejecute con sudo."; exit 1; }
[ -x "$BINARIO" ]    || { rojo "Falta $BINARIO. Ejecute antes 05_instalar_servicio.sh"; exit 1; }

echo "== Credencial de la WEB =="
echo "Es distinta de la de Samba (D-14). Mínimo 12 caracteres."
echo

# read -s: el intérprete no muestra lo tecleado ni lo guarda en el historial.
read -rs -p "Contraseña: " CLAVE; echo
read -rs -p "Repítala:   " CLAVE2; echo

if [ "$CLAVE" != "$CLAVE2" ]; then
  rojo "No coinciden. No se ha cambiado nada."
  exit 1
fi
if [ "${#CLAVE}" -lt 12 ]; then
  rojo "Demasiado corta: mínimo 12 caracteres. No se ha cambiado nada."
  exit 1
fi

mkdir -p "$(dirname "$DESTINO")"
if [ -f "$DESTINO" ]; then
  cp "$DESTINO" "$DESTINO.bak.$(date +%Y%m%d%H%M%S)"
  echo "Credencial anterior respaldada."
fi

# La derivación tarda unos segundos a propósito: 600 000 iteraciones de
# PBKDF2, medidas en 3.2 s sobre este nodo.
echo "Derivando (tarda unos segundos)..."
printf '%s' "$CLAVE" | "$BINARIO" --generar-credencial > "$DESTINO.tmp"
unset CLAVE CLAVE2

chmod 0400 "$DESTINO.tmp"
chown root:root "$DESTINO.tmp"
mv "$DESTINO.tmp" "$DESTINO"

verde "Credencial guardada en $DESTINO (solo root puede leerla)."
echo "El servicio la recibe por LoadCredential= de systemd, en un tmpfs privado."
echo
echo "Siguiente: sudo systemctl restart nasd"
