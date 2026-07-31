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

   # OJO: el parámetro es «server smb encrypt», NO «smb encryption».
   # Ese último NO EXISTE, y Samba lo ignora con un aviso fácil de pasar por
   # alto en la salida de testparm: el cifrado no se activa y nadie se entera.
   # Verificado contra Samba 4.22.10 en el nodo (2026-07-30).
   server smb encrypt = desired

   # ADR-0028 — los bloqueos de rango se proyectan a POSIX para que la web
   # pueda verlos. Es "cortesía", no garantía: un copiar y pegar del
   # Explorador usa share modes, que Samba gestiona en su base de datos
   # interna y NO llegan al kernel. La garantía de RF-22 la da el rename()
   # atómico, no esta línea.
   posix locking = yes

   # Clientes Apple (iOS y macOS) — línea base recomendada por el propio
   # proyecto Samba. NO es cosmético: sin estos módulos el iPhone puede
   # presentar el recurso como de SOLO LECTURA y ni siquiera intentar
   # escribir, que es justo lo observado el 2026-07-30.
   #
   # RF-03 exige que el iPhone suba una foto, así que esto es requisito,
   # no comodidad. «fruit» traduce los metadatos de Apple a atributos
   # extendidos en lugar de a archivos ._ sueltos por todo el árbol.
   # «fruit» es lo que arregla la escritura desde iOS. «catia» se RETIRÓ el
   # 2026-07-31: traduce los caracteres que Windows no admite a un área
   # privada de Unicode, y eso hacía que el nombre guardado en disco NO
   # coincidiera con el que ven los clientes SMB — un carácter invisible al
   # final. Rompía la fidelidad del nombre entre las tres vías, que vale más
   # que el caso raro de un nombre con : * ? " < > |
   vfs objects = fruit streams_xattr
   fruit:metadata = stream
   fruit:model = MacSamba
   fruit:posix_rename = yes
   fruit:veto_appledouble = no
   fruit:wipe_intentionally_left_blank_rfork = yes
   fruit:delete_empty_adfiles = yes
   ea support = yes

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

# P4 del charter: cero secretos en el repositorio. La contraseña NO se pasa
# por parámetro, ni por variable de entorno, ni queda en el historial del
# shell: se teclea aquí y solo aquí.
if pdbedit -L 2>/dev/null | cut -d: -f1 | grep -qx "$USUARIO"; then
  echo "El usuario '$USUARIO' ya existe en la base de Samba; no se toca la contraseña."
  echo "Para cambiarla: sudo smbpasswd '$USUARIO'"
elif [ -t 0 ]; then
  smbpasswd -a "$USUARIO"
  smbpasswd -e "$USUARIO"
else
  # Sin terminal interactiva no se puede pedir la contraseña. Se avisa de
  # forma ruidosa en lugar de continuar como si nada (P5).
  rojo "SIN TERMINAL INTERACTIVA: la contraseña de Samba NO se ha fijado."
  rojo "El recurso quedará inaccesible hasta que la establezca usted:"
  echo
  echo "    sudo smbpasswd -a $USUARIO && sudo smbpasswd -e $USUARIO"
  echo
  FALTA_CONTRASENA=1
fi

systemctl restart smbd nmbd
systemctl enable smbd nmbd

echo
if [ "${FALTA_CONTRASENA:-0}" = "1" ]; then
  verde "Paso 2 completado, PERO FALTA LA CONTRASEÑA (ver arriba)."
else
  verde "Paso 2 completado."
fi
echo "Siguiente: sudo ./03_cortafuegos.sh"
