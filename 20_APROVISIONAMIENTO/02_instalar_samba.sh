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
RED_TUNEL=10.77.0.0/24   # debe coincidir con RED_TUNEL de 12_wireguard.sh
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

   # RN-04 / RNF-07 — solo la LAN y el túnel de la Fase 5.
   #
   # El túnel NO estaba aquí y por eso SMB no funcionaba desde fuera de casa,
   # medido el 2026-08-01 con el túnel vivo: el cortafuegos ya dejaba pasar el
   # 445 por wg0 (regla iifname "wg0" de 03_cortafuegos.sh), pero el cliente
   # remoto llega con origen 10.77.0.x y Samba rechazaba la sesión él mismo.
   # La web sí funcionaba porque nasd no filtra por origen. Refuta el supuesto
   # de 06_ACCESO_REMOTO §3.2 de que la Fase 5 costaba "cero cambios en Samba".
   #
   # Va sin condicionar a que exista el túnel, al contrario que las reglas del
   # cortafuegos: aquí no hay superficie que regalar. Sin wg0 ningún paquete
   # puede llegar con origen 10.77.0.x — las reglas de entrada solo aceptan el
   # 445 desde 192.168.1.0/24 o por wg0, así que un origen falsificado desde la
   # LAN no encaja en ninguna. Condicionarlo obligaría a reejecutar este script
   # después de 12_wireguard.sh, que es la doble propiedad que costó ADR-0040.
   hosts allow = $RED $RED_TUNEL 127.0.0.1
   hosts deny = 0.0.0.0/0

   # CIFRADO DESACTIVADO — D-18 / ADR-0031, decisión del responsable.
   #
   # Medido con el mismo archivo de 5 GB en el nodo real: con AES-128-GCM,
   # 9.7 MB/s; sin cifrar, ~20 MB/s. El A53 del 3B+ NO tiene aceleración
   # criptográfica (charter §3.2), así que cifrar cuesta la mitad del ancho
   # de banda y no es un parámetro afinable.
   #
   # Lo que se pierde: el CONTENIDO viaja en claro por la LAN.
   # Lo que NO se pierde: la autenticación sigue siendo NTLMv2 —la contraseña
   # no viaja en claro— y RNF-09 sigue intacto.
   #
   # Detonante para revertirlo: que la LAN deje de ser solo de equipos de
   # confianza. Volver a activarlo es poner «desired» y reiniciar smbd.
   #
   # (El parámetro se llama «server smb encrypt». «smb encryption» NO existe
   #  y Samba lo ignora en silencio; ese error ya nos costó una tarde.)
   server smb encrypt = off

   # FIRMA SMB — OBLIGATORIA. Sin esta línea, Windows 11 no entra.
   #
   # No es endurecimiento opcional: es la consecuencia no prevista de D-18.
   # Hasta el 30/07 la sesión iba cifrada (SMB3_11 con AES-128-GCM, medido), y
   # el cifrado SMB3 lleva integridad implícita. Al desactivar el cifrado por
   # rendimiento, el recurso se quedó SIN NINGÚN mecanismo de integridad,
   # porque «server signing = default» en un servidor autónomo NO firma.
   #
   # Windows 11 exige firma en el cliente (RequireSecuritySignature, medido en
   # el host). El resultado era un fallo desconcertante: la contraseña se
   # aceptaba y acto seguido el servidor cerraba la conexión — error 64,
   # «el nombre de red ya no está disponible», que parece un problema de red.
   #
   # MS-SMB2 §3.3.5.2.4: el servidor debe rechazar la sesión si el cliente pide
   # firma y el servidor no la ofrece. Referencia: Microsoft, [MS-SMB2] Server
   # Message Block (SMB) Protocol Versions 2 and 3, y el endurecimiento por
   # omisión introducido en Windows 11 24H2.
   #
   # Coste: HMAC por paquete en un A53 sin aceleración criptográfica. Es menor
   # que cifrar —solo firma la cabecera y el resumen, no el contenido— pero no
   # es cero y NO está medido todavía.
   server signing = mandatory

   # SMB MULTICANAL DESACTIVADO. Sin esta línea, Windows no monta el recurso.
   #
   # Samba lo trae activo por omisión. Al recibir el tree connect, responde al
   # FSCTL_QUERY_NETWORK_INTERFACE_INFO anunciando SUS interfaces, y el cliente
   # abre canales adicionales por ellas ([MS-SMB2] §3.2.4.1.8). Este nodo expone
   # eth0 y wlan0; wlan0 no es utilizable, así que Windows esperaba ~10 s y
   # cerraba la conexión ENTERA con un RST. El síntoma era desconcertante:
   # autenticación correcta, tree connect correcto, y acto seguido error 64
   # «el nombre de red ya no está disponible». Capturado con tcpdump: todos los
   # estados SMB2 en 0x00000000 y el RST viniendo del cliente, no del servidor.
   #
   # No se pierde nada al apagarlo: multicanal agrega ancho de banda usando
   # varias rutas de red, y aquí hay UNA sola NIC utilizable, colgada del bus
   # USB 2.0 que ya comparte con el disco (RES-02). No hay segundo camino.
   #
   # Por qué no se vio antes: hasta D-18 la sesión iba cifrada, y smbclient
   # —con el que se probaba desde el nodo— no negocia multicanal.
   server multi channel support = no

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
# NO se usa «... | grep -qx»: grep -q sale al primer acierto, el productor
# recibe SIGPIPE, y «set -o pipefail» convierte el ACIERTO en fallo. Ese falso
# negativo se llevaría por delante justo la idempotencia que se añadió aquí a
# propósito (rector v1.4.0): volvería a pedir y cambiar la contraseña de Samba
# de un usuario que ya existe.
if [ "$(pdbedit -L 2>/dev/null | cut -d: -f1 | grep -cx "$USUARIO")" -gt 0 ]; then
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
