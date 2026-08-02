#!/bin/bash
# Fase 3, paso 1 — fijar LA credencial del NAS. Web y SMB, la misma.
#
#   RF-15     la web exige autenticación
#   ADR-0047  UNA SOLA contraseña — supersede a ADR-0021 y relaja D-14
#   ADR-0046  con TLS desaparece el motivo por el que estaban separadas
#   P4        cero secretos en el repositorio
#
# La contraseña se teclea aquí y no viaja por ningún otro sitio: no se pasa
# como argumento —quedaría en el historial y en la lista de procesos— ni se
# escribe en el TOML versionado. Solo se guardan sus derivaciones.
#
# Uso: sudo ./07_credencial_web.sh

set -euo pipefail

DESTINO=/etc/nasd/credencial
BINARIO=/usr/local/bin/nasd
USUARIO_SMB=nas   # D-08: un solo usuario

rojo()  { printf '\033[31m%s\033[0m\n' "$*"; }
verde() { printf '\033[32m%s\033[0m\n' "$*"; }
aviso() { printf '\033[33m%s\033[0m\n' "$*"; }

[ "$(id -u)" -eq 0 ] || { rojo "Ejecute con sudo."; exit 1; }
[ -x "$BINARIO" ]    || { rojo "Falta $BINARIO. Ejecute antes 05_instalar_servicio.sh"; exit 1; }

echo "== Credencial del NAS =="
echo "ADR-0047: UNA SOLA contraseña para la web y para SMB."
echo "Mínimo 14 caracteres."
echo
echo "Por qué 14 y no 12: con ADR-0046 la web queda expuesta a Internet, y"
echo "con ADR-0047 esta misma contraseña abre además el recurso de red."
echo "Si vale por las dos cosas, tiene que valer el doble."
echo

# read -s: el intérprete no muestra lo tecleado ni lo guarda en el historial.
read -rs -p "Contraseña: " CLAVE; echo
read -rs -p "Repítala:   " CLAVE2; echo

if [ "$CLAVE" != "$CLAVE2" ]; then
  rojo "No coinciden. No se ha cambiado nada."
  exit 1
fi
if [ "${#CLAVE}" -lt 14 ]; then
  rojo "Demasiado corta: mínimo 14 caracteres (ADR-0047). No se ha cambiado nada."
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

chmod 0400 "$DESTINO.tmp"
chown root:root "$DESTINO.tmp"
mv "$DESTINO.tmp" "$DESTINO"

# LA MISMA contraseña en Samba — ADR-0047, que supersede a ADR-0021.
#
# Se unifica el VALOR que se teclea, no el almacenamiento: la web sigue
# guardando su derivación PBKDF2 y Samba su hash NT, en bases distintas. Que
# sean dos almacenes no es un defecto, es lo que impide que romper uno entregue
# el otro.
#
# smbpasswd -s lee de la entrada estándar, así que la contraseña NO pasa por
# argumentos ni queda en la lista de procesos (P4).
if command -v smbpasswd >/dev/null 2>&1 && pdbedit -L 2>/dev/null | cut -d: -f1 | grep -qx "$USUARIO_SMB"; then
  if printf '%s\n%s\n' "$CLAVE" "$CLAVE" | smbpasswd -s "$USUARIO_SMB" >/dev/null 2>&1; then
    verde "Contraseña de Samba actualizada al mismo valor (ADR-0047)."
  else
    rojo "NO se pudo cambiar la contraseña de Samba. Quedan DESINCRONIZADAS."
    rojo "Arréglelo con: sudo smbpasswd $USUARIO_SMB"
  fi
else
  aviso "Samba no está instalado o el usuario '$USUARIO_SMB' no existe: solo se fijó la de la web."
fi
unset CLAVE CLAVE2

verde "Credencial guardada en $DESTINO (solo root puede leerla)."
echo "El servicio la recibe por LoadCredential= de systemd, en un tmpfs privado."
echo
echo "Siguiente: sudo systemctl restart nasd"
