#!/bin/bash
# Fase 1, paso 2 — instalar y endurecer Samba.
#
#   D-08 / ADR-0008   un único usuario con credencial, sin recursos anónimos
#   ADR-0019          se comparte /srv/nas/datos, NO la raíz del volumen
#   ADR-0020          un único UID escribe en el almacén
#   RNF-09            SMB1 deshabilitado
#   04_SEGURIDAD §5   configuración de endurecimiento
#
# Uso: sudo ./02_instalar_samba.sh

set -euo pipefail

PUNTO=/srv/nas
USUARIO=nas
RED=192.168.1.0/24
RECURSO=datos

rojo()  { printf '\033[31m%s\033[0m\n' "$*"; }
verde() { printf '\033[32m%s\033[0m\n' "$*"; }

[ "$(id -u)" -eq 0 ] || { rojo "Ejecute con sudo."; exit 1; }
mountpoint -q "$PUNTO" || { rojo "$PUNTO no está montado. Ejecute antes 01_preparar_disco.sh"; exit 1; }
[ -d "$PUNTO/datos" ]  || { rojo "Falta $PUNTO/datos (ADR-0019)."; exit 1; }
id -u "$USUARIO" >/dev/null 2>&1 || { rojo "Falta el usuario '$USUARIO'."; exit 1; }

echo "== Instalando Samba =="
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y samba samba-common-bin

echo
echo "== Configurando =="
CONF=/etc/samba/smb.conf
[ -f "$CONF.original" ] || cp "$CONF" "$CONF.original"

cat > "$CONF" <<EOF
# Generado por 20_APROVISIONAMIENTO/02_instalar_samba.sh — proyecto NAS.
# P1: si un cambio no está en el script, no existe. NO editar a mano.

[global]
   workgroup = WORKGROUP
   server string = NAS Raspberry Pi
   log file = /var/log/samba/log.%m
   max log size = 1000
   logging = file

   # RNF-09 — SMB1 deshabilitado. Es el control que verifica nmap.
   server min protocol = SMB2_10
   client min protocol = SMB2_10

   # RN-03 / D-08 — sin recursos anónimos ni cuentas de invitado.
   security = user
   map to guest = Never
   restrict anonymous = 2
   guest account = nobody

   # RN-04 / RNF-07 — solo la LAN.
   hosts allow = $RED 127.0.0.1
   hosts deny = 0.0.0.0/0

   smb encryption = desired

   # ADR-0028 — los bloqueos de rango se proyectan a POSIX para que la web
   # pueda verlos. Es "cortesía", no garantía: un copiar y pegar del
   # Explorador usa share modes, que Samba gestiona en su base de datos
   # interna y NO llegan al kernel. La garantía de RF-22 la da el rename()
   # atómico, no esta línea.
   posix locking = yes

   # No se anuncia ni se sirve nada más que el recurso declarado (P8).
   load printers = no
   printing = bsd
   printcap name = /dev/null
   disable spoolss = yes

[$RECURSO]
   comment = Datos del NAS
   # ADR-0019: se comparte datos/, NO $PUNTO. Si se apuntara a la raíz del
   # volumen, estado/ quedaría expuesto y R3 se incumpliría el primer día.
   path = $PUNTO/datos
   browseable = yes
   read only = no
   guest ok = no
   valid users = $USUARIO

   # ADR-0020: un único UID escribe, venga por SMB o por la web. Un archivo
   # subido por una vía es indistinguible de uno subido por la otra (RF-21).
   force user = $USUARIO
   force group = $USUARIO
   create mask = 0600
   force create mode = 0600
   directory mask = 0700
   force directory mode = 0700

   # 04_SEGURIDAD §2 — un enlace dentro de datos/ apuntando a /etc
   # convertiría un control de rutas correcto en una fuga.
   follow symlinks = no
   wide links = no
EOF

echo "== Comprobando la sintaxis =="
testparm -s >/dev/null || { rojo "smb.conf tiene errores"; exit 1; }
verde "smb.conf válido."

echo
echo "== Contraseña de Samba para '$USUARIO' =="
echo "D-14: esta credencial es SOLO para SMB. La web tendrá la suya, distinta."
smbpasswd -a "$USUARIO"
smbpasswd -e "$USUARIO"

systemctl restart smbd nmbd
systemctl enable smbd nmbd

echo
verde "Paso 2 completado."
echo "Siguiente: sudo ./03_cortafuegos.sh"
