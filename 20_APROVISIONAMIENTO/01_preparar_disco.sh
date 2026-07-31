#!/bin/bash
# Fase 1, paso 1 — formatear el disco en ext4 y montarlo por UUID.
#
#   D-07  / ADR-0007  el volumen de datos va en ext4
#   ADR-0019          disposición datos/ + estado/
#   ADR-0020          un único UID escribe en el almacén
#   RNF-10            montaje por UUID, jamás por /dev/sdX
#
# ############################################################################
# #  ESTE SCRIPT DESTRUYE TODO EL CONTENIDO DEL DISCO QUE SE LE INDIQUE.     #
# #  Léalo entero antes de ejecutarlo. Exige confirmación escrita.           #
# ############################################################################
#
# Uso:   sudo ./01_preparar_disco.sh /dev/sdb
#
# P1 del charter: este script ES la configuración. Si un cambio no está aquí,
# no existe y el nodo no es reconstruible.

set -euo pipefail

DISCO="${1:-}"
MODELO_ESPERADO="ST1000LM035"      # charter §3.6 [M]
PUNTO=/srv/nas
USUARIO=nas

rojo()  { printf '\033[31m%s\033[0m\n' "$*"; }
verde() { printf '\033[32m%s\033[0m\n' "$*"; }

[ "$(id -u)" -eq 0 ] || { rojo "Ejecute con sudo."; exit 1; }
[ -n "$DISCO" ]      || { rojo "Uso: sudo $0 /dev/sdX"; exit 1; }
[ -b "$DISCO" ]      || { rojo "$DISCO no es un dispositivo de bloques."; exit 1; }

# ---------------------------------------------------------------- seguridad
# P2: no se actúa sobre una suposición. Se comprueba QUÉ disco es.
echo "== Disco indicado =="
lsblk -o NAME,SIZE,MODEL,SERIAL,FSTYPE,MOUNTPOINT "$DISCO"
echo

MODELO=$(lsblk -ndo MODEL "$DISCO" | tr -d ' ')
if [ "$MODELO" != "$MODELO_ESPERADO" ]; then
  rojo "El modelo es '$MODELO' y se esperaba '$MODELO_ESPERADO' (charter §3.6)."
  rojo "Si de verdad quiere continuar, edite MODELO_ESPERADO en este script."
  exit 1
fi

# Un disco que es el medio de arranque no se toca jamás.
RAIZ_DEV=$(findmnt -no SOURCE / | sed 's/[0-9]*$//')
if [ "$DISCO" = "$RAIZ_DEV" ]; then
  rojo "ABORTADO: $DISCO es el disco del sistema."
  exit 1
fi

if mount | grep -q "^$DISCO"; then
  echo "Hay particiones montadas; se desmontan:"
  mount | grep "^$DISCO" | awk '{print $3}' | while read -r m; do
    echo "  umount $m"; umount "$m"
  done
fi

echo
rojo "SE VA A BORRAR TODO EL CONTENIDO DE $DISCO ($MODELO)."
rojo "Esta operación NO se puede deshacer y no hay copia (D-12: copia única)."
read -r -p "Escriba exactamente BORRAR para continuar: " RESPUESTA
[ "$RESPUESTA" = "BORRAR" ] || { echo "Cancelado."; exit 1; }

# ---------------------------------------------------------------- formateo
echo
echo "== Particionando y formateando =="
wipefs -a "$DISCO"
sgdisk --zap-all "$DISCO"
sgdisk --new=1:0:0 --typecode=1:8300 --change-name=1:nas-datos "$DISCO"
partprobe "$DISCO"; sleep 2

PART="${DISCO}1"
[ -b "$PART" ] || PART="${DISCO}p1"
[ -b "$PART" ] || { rojo "No aparece la partición de $DISCO"; exit 1; }

# -m 0: sin reserva del 5 % para root. Son ~46 GB en 931 GB, y este volumen
# es de datos, no del sistema: la reserva no aporta nada aquí.
mkfs.ext4 -L nas-datos -m 0 "$PART"

UUID=$(blkid -s UUID -o value "$PART")
[ -n "$UUID" ] || { rojo "No se pudo leer el UUID"; exit 1; }
verde "UUID del volumen: $UUID"

# ---------------------------------------------------------------- usuario
echo
echo "== Usuario de servicio (ADR-0020) =="
if ! id -u "$USUARIO" >/dev/null 2>&1; then
  # Sin shell y sin directorio propio: solo existe para poseer los datos.
  useradd --system --no-create-home --shell /usr/sbin/nologin "$USUARIO"
  verde "Creado el usuario del sistema '$USUARIO'."
else
  echo "El usuario '$USUARIO' ya existe."
fi

# ---------------------------------------------------------------- montaje
echo
echo "== Montaje por UUID (RNF-10) =="
mkdir -p "$PUNTO"

# El nombre de dispositivo cambia entre arranques; el UUID no.
LINEA="UUID=$UUID  $PUNTO  ext4  defaults,noatime,nofail  0  2"
if grep -q "$PUNTO" /etc/fstab; then
  echo "Ya hay una entrada para $PUNTO en /etc/fstab; se deja como está:"
  grep "$PUNTO" /etc/fstab
else
  cp /etc/fstab "/etc/fstab.bak.$(date +%Y%m%d%H%M%S)"
  echo "$LINEA" >> /etc/fstab
  verde "Añadido a /etc/fstab (copia de seguridad hecha)."
fi

systemctl daemon-reload
mount "$PUNTO"
mountpoint -q "$PUNTO" || { rojo "No se pudo montar $PUNTO"; exit 1; }

# ------------------------------------------------------- disposición ADR-0019
echo
echo "== Disposición del volumen (ADR-0019) =="
mkdir -p "$PUNTO/datos" "$PUNTO/estado/parciales" "$PUNTO/estado/miniaturas"

# datos/ es el ÚNICO subárbol que se comparte por Samba.
# estado/ NO se comparte: si se compartiera, el usuario podría borrar por SMB
# una subida en curso y romperla (regla R3 de 02_ARQUITECTURA.md §3.3).
chown -R "$USUARIO:$USUARIO" "$PUNTO"
chmod 0700 "$PUNTO/datos" "$PUNTO/estado"

echo
lsblk -o NAME,SIZE,FSTYPE,LABEL,MOUNTPOINT "$DISCO"
df -h "$PUNTO"
tree -L 2 "$PUNTO" 2>/dev/null || ls -la "$PUNTO"

echo
verde "Paso 1 completado."
echo "UUID para la documentación: $UUID"
echo "Siguiente: sudo ./02_instalar_samba.sh"
