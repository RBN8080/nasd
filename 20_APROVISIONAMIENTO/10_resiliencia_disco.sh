#!/bin/bash
# Fase 4, paso 3 — resiliencia del disco de datos frente a caídas del bus USB.
#
#   ADR-0040     las decisiones de este script
#   Charter §3.6 el bus USB 2.0 y la alimentación compartida del 3B+
#   Charter P9   el medio FALLARÁ, no «puede fallar»
#   RNF-10       montaje por UUID — lo que permitió recuperar el incidente
#   D-12         copia única: no hay de dónde recuperar lo que se pierda aquí
#
# EL INCIDENTE QUE LO ORIGINA — 2026-07-31 17:12:31, medido, no supuesto:
#
#   17:12:31  el puente USB-SATA deja de responder
#             (Synchronize Cache failed, hostbyte=0x01 = DID_NO_CONNECT)
#   17:12:31  JBD2: I/O error -> diario abortado -> EXT4 remonta solo-lectura
#             -> «shut down requested (2)»: toda E/S devuelve EIO
#   17:12:34  el disco REAPARECE, pero como /dev/sdc (era /dev/sdb)
#
# El montaje seguía apuntando a sdb1, que ya no existía. Windows devolvía
# 0x8007045D (ERROR_IO_DEVICE), nasd moría y la web no respondía: UN SOLO
# fallo explicaba los tres síntomas.
#
# LO QUE SE DESCARTÓ MIDIENDO, para que nadie lo vuelva a proponer:
#   - subtensión del SoC:  throttled=0x80000, bits 0 y 16 LIMPIOS
#   - autosuspend de USB:  el dispositivo tenía power/control = on
#   - cable interno SATA:  UDMA_CRC_Error_Count = 0
#   - disco muriéndose:    0 sectores reasignados, 0 pendientes, SMART PASSED
#   - UAS:                 el kernel YA usaba usb-storage, no UAS
#
# LO QUE NO SE PUEDE DESCARTAR y este script NO arregla: la alimentación del
# raíl USB de 5 V. get_throttled mide el SoC, NO lo que llega al disco. El
# charter §3.6 ya recomendaba hub o carcasa alimentada. Esto es hardware y el
# responsable decide; aquí solo se reduce la probabilidad y se acota el daño.
#
# Idempotente. P1: si un cambio no está en un script, no existe.
#
# Uso: sudo ./10_resiliencia_disco.sh

set -euo pipefail

PUNTO=/srv/nas
TIMEOUT_NUEVO=180
# El 99 es DELIBERADO y costó una prueba fallida: las reglas de udev se aplican
# por NOMBRE DE ARCHIVO, y ID_SERIAL_SHORT la define 60-persistent-storage.rules.
# Con un 60- propio la regla se evaluaba ANTES de que la variable existiera, no
# casaba nunca, y no había ningún error: silencio y cero efecto.
REGLA=/etc/udev/rules.d/99-nas-disco.rules
REGLA_ANTIGUA=/etc/udev/rules.d/60-nas-disco.rules
VIGILANTE=/usr/local/sbin/nas-vigilar-disco.sh
UNIDAD=/etc/systemd/system/nas-vigilar-disco.service
TEMPORIZADOR=/etc/systemd/system/nas-vigilar-disco.timer

rojo()  { printf '\033[31m%s\033[0m\n' "$*"; }
verde() { printf '\033[32m%s\033[0m\n' "$*"; }
aviso() { printf '\033[33m%s\033[0m\n' "$*"; }

[ "$(id -u)" -eq 0 ] || { rojo "Ejecute con sudo."; exit 1; }
mountpoint -q "$PUNTO" || { rojo "$PUNTO no está montado. Ejecute antes 01_preparar_disco.sh"; exit 1; }

echo "===== Fase 4 · resiliencia del disco de datos ====="
echo

# El dispositivo NO se codifica: se descubre desde el punto de montaje. Todo lo
# que este proyecto ha aprendido sobre nombres de dispositivo cabe en una frase:
# sdb se convirtió en sdc sin avisar (RNF-10 existe por esto).
PARTICION=$(findmnt -no SOURCE "$PUNTO")
DISCO=/dev/$(lsblk -no PKNAME "$PARTICION")
SERIE=$(udevadm info -q property -n "$DISCO" | sed -n 's/^ID_SERIAL_SHORT=//p')
[ -n "$SERIE" ] || { rojo "No se pudo leer la serie de $DISCO. Sin ella la regla udev sería frágil."; exit 1; }
verde "Disco de datos: $DISCO (partición $PARTICION, serie $SERIE)"

# ---------------------------------------------------------------------------
# 1. Saldar la deuda de P1: smartmontools se instaló A MANO durante el incidente
# ---------------------------------------------------------------------------
echo
echo "== 1. Utillaje de diagnóstico (deuda de P1 del 2026-07-31) =="

if ! command -v smartctl >/dev/null 2>&1; then
  DEBIAN_FRONTEND=noninteractive apt-get install -y -qq smartmontools >/dev/null
  verde "smartmontools instalado."
else
  verde "smartmontools ya presente."
fi

# El paquete activa smartd por su cuenta. NO se queda: es un demonio que nadie
# decidió, en un nodo de 592 MB (RES-01), y duplica lo que ADR-0034 ya resuelve
# leyendo de /proc y /sys desde nasd. P8: lo que no aporta, no corre.
# Se conserva la HERRAMIENTA smartctl, que es lo que hacía falta para diagnosticar.
# LA UNIDAD SE LLAMA «smartmontools», NO «smartd», y esa diferencia hacía que
# este bloque mintiera. Detectado en la auditoría del 2026-08-01:
#
#   systemctl is-enabled smartd  ->  not-found
#   systemctl is-active  smartd  ->  inactive
#
# Las dos comprobaciones daban falso, el script entraba en el «else» e imprimía
# «smartd no está corriendo (correcto)» mientras smartmontools.service llevaba
# activo desde el 2026-07-31 17:56. Y el «disable --now smartd» tampoco podía
# hacer nada, porque esa unidad no existe aquí.
#
# Es el defecto de ADR-0038 otra vez: el verificador no se parecía a la
# pregunta. Se comprueba el nombre REAL y se falla ruidosamente (P5) si queda
# corriendo, en vez de dar por bueno lo que no se ha mirado.
UNIDAD_SMART=""
for U in smartmontools smartd; do
  if systemctl list-unit-files "$U.service" --no-legend 2>/dev/null | grep -q .; then
    UNIDAD_SMART="$U"
    break
  fi
done

if [ -z "$UNIDAD_SMART" ]; then
  verde "no hay unidad de smartd en este sistema (correcto)."
else
  systemctl disable --now "$UNIDAD_SMART" >/dev/null 2>&1 || true
  if systemctl is-active --quiet "$UNIDAD_SMART" 2>/dev/null; then
    rojo "$UNIDAD_SMART SIGUE ACTIVO pese a haberlo detenido. Revíselo a mano."
  else
    verde "$UNIDAD_SMART detenido y deshabilitado (P8). smartctl se conserva."
  fi
fi

# ---------------------------------------------------------------------------
# 2. El kernel deja de rendirse a los 30 segundos
# ---------------------------------------------------------------------------
echo
echo "== 2. Plazo del kernel antes de descartar el disco =="

# MEDIDO: el timeout estaba en 30 s. Cuando el puente tardó más que eso, el
# kernel no esperó: descartó el dispositivo y lo re-enumeró como sdc.
#
# QUÉ ARREGLA ESTO Y QUÉ NO, sin adornos:
#   SÍ  — el caso «el puente responde tarde»: un disco de 2.5" saliendo de
#         reposo tarda entre 5 y 15 s, y un puente atascado puede tardar más.
#         Con 180 s el kernel espera en vez de rendirse.
#   NO  — el caso «el puente se desconecta de verdad». Para eso está el punto 4.
#
# La regla se ancla a la SERIE del disco, no a sda/sdb/sdc, que es exactamente
# el nombre que cambió durante el incidente.
#
# POR QUÉ RUN+= Y NO ATTR{device/timeout}, que sería lo natural: MEDIDO al
# escribir este script — /sys/block/sdX/device es un ENLACE SIMBÓLICO al
# dispositivo SCSI, y ATTR{} de udev NO escribe a través de enlaces. La regla se
# evalúa, la serie coincide, y la escritura no llega: verde en el archivo y cero
# efecto en el sistema. Es el mismo defecto que «server smb encrypt» de la
# Fase 1, y se encontró porque este script comprueba el efecto y no el archivo.
cat > "$REGLA" <<EOF
# Generado por 20_APROVISIONAMIENTO/10_resiliencia_disco.sh — proyecto NAS.
# P1: si un cambio no está en el script, no existe. NO editar a mano.
#
# ADR-0040. El 2026-07-31 el kernel descartó este disco tras 30 s sin respuesta
# del puente USB-SATA, y el sistema de archivos se apagó. Con $TIMEOUT_NUEVO s espera en
# lugar de rendirse. Anclado a la SERIE porque el nombre del dispositivo CAMBIÓ
# durante el incidente: sdb pasó a sdc.
#
# RUN+= en vez de ATTR{}: 'device' es un enlace simbólico y ATTR{} no lo sigue.
# El 99 del nombre: ID_SERIAL_SHORT la define 60-persistent-storage.rules, y las
# reglas se aplican por nombre de archivo. Con un 60- propio no casaba nunca.
ACTION=="add|change", SUBSYSTEM=="block", KERNEL=="sd[a-z]", ENV{ID_SERIAL_SHORT}=="$SERIE", RUN+="/bin/sh -c 'echo $TIMEOUT_NUEVO > /sys\$devpath/device/timeout'"
EOF
verde "$REGLA escrito."
rm -f "$REGLA_ANTIGUA"

udevadm control --reload-rules

# SE COMPRUEBA LA REGLA, NO EL VALOR. Y la diferencia no es teórica: la primera
# versión de este script escribía el 180 a mano y LUEGO leía el sysfs, así que
# daba verde con la regla rota — la regla no casaba y el verificador no podía
# saberlo, porque estaba leyendo la escritura del propio script.
#
# Aquí se fuerza un valor DELIBERADAMENTE MALO, se dispara el evento, y solo si
# vuelve a 180 es que la regla funciona. Es la única forma de saber si el plazo
# seguirá puesto cuando el disco se re-enumere, que es cuando hace falta.
NOMBRE=$(basename "$DISCO")
RUTA_TIMEOUT="/sys/block/$NOMBRE/device/timeout"
echo 30 > "$RUTA_TIMEOUT"
udevadm trigger --action=change "/sys/block/$NOMBRE"
udevadm settle
sleep 2

APLICADO=$(cat "$RUTA_TIMEOUT" 2>/dev/null || echo 0)
if [ "$APLICADO" = "$TIMEOUT_NUEVO" ]; then
  verde "Plazo del kernel: la REGLA lo repone a ${APLICADO} s tras un evento del dispositivo."
else
  rojo "La regla NO surte efecto: tras el evento, timeout = ${APLICADO} s (se esperaba $TIMEOUT_NUEVO)."
  rojo "Causa típica: el nombre del archivo ordena antes que 60-persistent-storage.rules,"
  rojo "que es quien define ID_SERIAL_SHORT. Diagnóstico:  udevadm test --action=change /sys/block/$NOMBRE"
  echo "$TIMEOUT_NUEVO" > "$RUTA_TIMEOUT"   # no dejar el nodo peor de como estaba
  exit 1
fi

# ---------------------------------------------------------------------------
# 3. El fallo deja de ser accidentalmente correcto
# ---------------------------------------------------------------------------
echo
echo "== 3. Comportamiento ante error, escrito en el superbloque =="

# MEDIDO durante el incidente: el superbloque decía «Errors behavior: Continue»
# y aun así ext4 remontó en solo-lectura, porque el diario abortado no le dejó
# otra salida. Es decir: HIZO LO CORRECTO POR ACCIDENTE.
#
# «Continue» ante un error de E/S significa seguir escribiendo sobre un volumen
# que ya falló. Con D-12 —copia única— eso es la peor opción posible: convierte
# un fallo recuperable en corrupción silenciosa.
#
# Se escribe en el SUPERBLOQUE y no en el fstab a propósito: así es verificable
# con dumpe2fs y se aplica aunque alguien monte a mano.
ACTUAL=$(dumpe2fs -h "$PARTICION" 2>/dev/null | sed -n 's/^Errors behavior: *//p')
if [ "$ACTUAL" = "Remount read-only" ]; then
  verde "Superbloque: ya estaba en «Remount read-only»."
else
  tune2fs -e remount-ro "$PARTICION" >/dev/null
  NUEVO=$(dumpe2fs -h "$PARTICION" 2>/dev/null | sed -n 's/^Errors behavior: *//p')
  [ "$NUEVO" = "Remount read-only" ] || { rojo "tune2fs no surtió efecto: sigue en «$NUEVO»."; exit 1; }
  verde "Superbloque: «$ACTUAL» -> «$NUEVO»"
fi

# Y TAMBIÉN EN EL FSTAB, y no por redundancia: MEDIDO tras poner el superbloque,
# el montaje seguía anunciando «errors=continue» en sus opciones. Sea porque el
# valor del superbloque no se refleja ahí o porque no se aplica, el resultado es
# el mismo: no se puede comprobar, y en este proyecto lo no verificable se trata
# como ausente. Explícito en el fstab no admite interpretación —y además es la
# cadena que lee el vigilante del punto 4—.
#
# La línea la crea 01_preparar_disco.sh, que ya la escribe con esta opción; aquí
# se corrige el nodo que YA existe, donde aquella línea se escribió sin ella.
if grep -q "^[^#]*$PUNTO.*errors=remount-ro" /etc/fstab; then
  verde "fstab: ya lleva errors=remount-ro."
else
  cp /etc/fstab "/etc/fstab.bak.$(date +%Y%m%d%H%M%S)"
  sed -i "\|[[:space:]]${PUNTO}[[:space:]]|s/nofail/nofail,errors=remount-ro/" /etc/fstab
  grep -q "^[^#]*$PUNTO.*errors=remount-ro" /etc/fstab || { rojo "No se pudo añadir errors=remount-ro al fstab."; exit 1; }
  verde "fstab: añadido errors=remount-ro (copia de seguridad hecha)."
fi

mount -o remount "$PUNTO"
OPC=$(findmnt -no OPTIONS "$PUNTO")

# CÓMO SE LEE ESTO, porque la lectura ingenua da un falso negativo y costó una
# pasada: ext4 solo imprime «errors=» en las opciones cuando DIFIERE de su valor
# por omisión, y ese valor por omisión es justamente remount-ro. Por eso antes se
# veía «errors=continue» —era la excepción— y ahora no se ve nada.
#
# La señal correcta es la AUSENCIA de errors=continue, no la presencia de
# errors=remount-ro. Buscar la presencia habría hecho fallar un nodo bien
# configurado, que es peor que no comprobar: un verificador que miente en rojo
# se acaba desactivando.
case "$OPC" in
  *errors=continue*)
    rojo "El montaje SIGUE en «continue»: $OPC"
    exit 1 ;;
  *)
    verde "Montaje en vigor: $OPC (sin «errors=» = valor por omisión = remount-ro)" ;;
esac

# ---------------------------------------------------------------------------
# 4. Recuperación automática cuando el disco se cae de todas formas
# ---------------------------------------------------------------------------
echo
echo "== 4. Vigilante de montaje =="

# Los puntos 2 y 3 reducen la PROBABILIDAD y acotan el DAÑO. Ninguno impide que
# el puente se desconecte. Esto atiende lo que el responsable pidió: que el
# incidente no vuelva a dejar el NAS inaccesible hasta que alguien lo repare a
# mano por SSH.
#
# LO QUE HACE:  detecta el montaje apagado, para los servicios, desmonta,
#               vuelve a montar POR UUID (que es lo que encuentra el nombre
#               nuevo del dispositivo) y levanta los servicios.
# LO QUE NO HACE, deliberadamente: NO ejecuta e2fsck. Reparar un sistema de
#               archivos sin supervisión, sobre la ÚNICA copia que existe
#               (D-12) y sin papelera (D-15), puede destruir más de lo que
#               salva. Si el montaje falla, PARA y grita. Eso lo decide una
#               persona, no un temporizador.
cat > "$VIGILANTE" <<'EOF'
#!/bin/bash
# Generado por 20_APROVISIONAMIENTO/10_resiliencia_disco.sh — proyecto NAS.
# P1: si un cambio no está en el script, no existe. NO editar a mano.
# ADR-0040. Recupera /srv/nas tras una caída del bus USB.
set -uo pipefail

PUNTO=/srv/nas
etiqueta() { logger -t nas-vigilar-disco -p "$1" "$2"; }

mountpoint -q "$PUNTO" || exit 0

OPCIONES=$(findmnt -no OPTIONS "$PUNTO" 2>/dev/null || echo "")
ROTO=0

# (a) Banderas de apagado de ext4. Son las que se vieron el 2026-07-31:
#     rw,noatime,emergency_ro,shutdown
case "$OPCIONES" in
  *shutdown*|*emergency_ro*) ROTO=1 ;;
esac

# (b) Montado en SOLO-LECTURA sin pedirlo. Este caso NO existía antes de que
#     ADR-0040 pusiera errors=remount-ro: con «continue» el volumen se apagaba y
#     saltaba (a); ahora puede quedarse en ro limpio, sin ninguna bandera, y (a)
#     no lo vería. La mitigación abrió un hueco en la detección, y este es el
#     parche de ese hueco.
#
#     Se compara el PRIMER campo, que en las opciones VFS siempre es rw o ro.
#     Buscar «ro» suelto sería un defecto: «errors=remount-ro» contiene «ro».
MODO=${OPCIONES%%,*}
[ "$MODO" = "ro" ] && ROTO=1

[ "$ROTO" -eq 0 ] && exit 0

etiqueta user.err "$PUNTO INUTILIZABLE (opciones: $OPCIONES). Intentando recuperar."

systemctl stop nasd smbd 2>/dev/null
umount -l "$PUNTO" 2>/dev/null

# Por UUID desde el fstab: es lo único que encuentra el disco cuando el kernel
# le ha cambiado el nombre (RNF-10). Fue lo que salvó el incidente original.
if ! mount "$PUNTO" 2>/dev/null; then
  etiqueta user.crit "NO se pudo remontar $PUNTO. Servicios DETENIDOS a propósito. Intervención manual: revise 'dmesg -T' y ejecute e2fsck a mano."
  exit 1
fi

# El diario de systemd vive en el disco (ADR-0037); journald conserva el
# descriptor viejo hasta que se reinicia.
systemctl restart systemd-journald 2>/dev/null
journalctl --flush >/dev/null 2>&1

systemctl start nasd smbd 2>/dev/null
etiqueta user.warning "$PUNTO RECUPERADO desde $(findmnt -no SOURCE "$PUNTO"). Servicios levantados. REVISE LA CAUSA: esto no es normal."
EOF
chmod 755 "$VIGILANTE"
verde "$VIGILANTE escrito."

cat > "$UNIDAD" <<EOF
# Generado por 20_APROVISIONAMIENTO/10_resiliencia_disco.sh — proyecto NAS.
# ADR-0040. NO editar a mano (P1).
[Unit]
Description=Vigilar y recuperar el montaje del disco de datos del NAS
ConditionPathIsMountPoint=$PUNTO

[Service]
Type=oneshot
ExecStart=$VIGILANTE
EOF

cat > "$TEMPORIZADOR" <<'EOF'
# Generado por 20_APROVISIONAMIENTO/10_resiliencia_disco.sh — proyecto NAS.
# ADR-0040. NO editar a mano (P1).
#
# Cada 60 s. No se afina más: comprobar el montaje cuesta una lectura de
# /proc/self/mountinfo, y bajar el intervalo solo adelantaría la recuperación
# unos segundos a cambio de despertar el planificador el triple de veces.
[Unit]
Description=Comprobar cada minuto que el disco de datos del NAS sigue usable

[Timer]
OnBootSec=2min
OnUnitActiveSec=60s
AccuracySec=10s

[Install]
WantedBy=timers.target
EOF
verde "$UNIDAD y $TEMPORIZADOR escritos."

systemctl daemon-reload
systemctl enable --now nas-vigilar-disco.timer >/dev/null 2>&1

if systemctl is-active --quiet nas-vigilar-disco.timer; then
  PROXIMO=$(systemctl show nas-vigilar-disco.timer -p NextElapseUSecRealtime --value)
  verde "Vigilante activo. Próxima comprobación: ${PROXIMO:-en breve}"
else
  rojo "El temporizador NO quedó activo."
  exit 1
fi

echo
aviso "LO QUE ESTE SCRIPT NO ARREGLA, y conviene no olvidarlo:"
aviso "  la alimentación del raíl USB de 5 V. get_throttled mide el SoC, no lo"
aviso "  que llega al disco. Si la causa era esa, esto reduce el daño pero NO"
aviso "  evita la caída. El charter §3.6 ya recomendaba hub o carcasa"
aviso "  alimentada; es hardware y lo decide el responsable."

echo
verde "Paso completado."
echo "Siguiente: sudo ./09_verificar_operacion.sh"
