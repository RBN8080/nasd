#!/bin/bash
# Fase 4, paso 1 — diario persistente y acotado, y watchdog de hardware.
#
#   Charter §8   «Logs: journald con límite de tamaño (P9). Sin logs no rotados»
#   Charter §8   «Salud: endpoint de estado y watchdog de hardware (bcm2835_wdt)»
#   RNF-13       registros estructurados y ROTADOS
#   ADR-0037     las decisiones de este script; supersede a ADR-0035
#
# DOS COSAS QUE SE MIDIERON EN EL NODO Y CAMBIARON EL PLAN:
#
#  1. El diario era VOLÁTIL. Raspberry Pi OS trae
#     /usr/lib/systemd/journald.conf.d/40-rpi-volatile-storage.conf, así que
#     el diario vivía en /run —es decir, en RAM— y **se perdía en cada
#     reinicio**. Con ello se perdían los BORRADOS de RF-19, que sin papelera
#     (D-15) ni segunda copia (D-12) son el único rastro de lo destruido.
#     Comprobado: «journalctl --list-boots» mostraba UN solo arranque.
#
#  2. El watchdog de hardware YA ESTABA ACTIVO, a 1min, por omisión de este
#     systemd. ADR-0035 afirmaba que «no existía» y era FALSO. Este script no
#     lo activa: lo APRIETA a 30s y comprueba que sigue en pie.
#
#  3. AÑADIDO tras el primer reinicio real (ADR-0039). Poner Storage=persistent
#     NO bastaba: en el arranque, systemd-journal-flush corría 4 SEGUNDOS ANTES
#     de montar /srv/nas, así que el enlace /var/log/journal colgaba en el vacío
#     y journald se quedaba en /run —RAM— TODO el arranque. El diario sobrevivía
#     una vez, el del aprovisionamiento, y a partir de ahí cada reinicio se
#     llevaba el arranque entero. Se ordena el volcado DESPUÉS del montaje.
#
# Idempotente: se puede volver a ejecutar. P1: si un cambio no está en un
# script, no existe.
#
# Uso: sudo ./08_observabilidad.sh

set -euo pipefail

CONF_DIARIO=/etc/systemd/journald.conf.d/50-nas.conf
CONF_SYSLOG=/etc/systemd/journald.conf.d/syslog.conf
CONF_WATCHDOG=/etc/systemd/system.conf.d/nas-watchdog.conf
DIR_VOLCADO=/etc/systemd/system/systemd-journal-flush.service.d
CONF_VOLCADO="$DIR_VOLCADO/50-nas-espera-al-disco.conf"

# El diario va al DISCO DE DATOS, no al medio de arranque. Decisión del
# responsable (ADR-0037): conserva la auditoría de borrados sin gastar el medio
# consumible. Coste aceptado: el plato no llega a pararse.
#
# Dentro de estado/ y NO de datos/: ADR-0019 comparte por Samba únicamente
# datos/, y 04_SEGURIDAD §6 advierte de que el diario contiene nombres de
# archivo personales. Publicarlo por SMB sería regalarlo.
PUNTO=/srv/nas
DIARIO="$PUNTO/estado/diario"
ENLACE=/var/log/journal

MAX_DIARIO=200M
GUARDAR_LIBRE=1G
MAX_ARCHIVO=20M
RETENCION=3month

WATCHDOG_RUNTIME=30s
WATCHDOG_REINICIO=2min

rojo()  { printf '\033[31m%s\033[0m\n' "$*"; }
verde() { printf '\033[32m%s\033[0m\n' "$*"; }
aviso() { printf '\033[33m%s\033[0m\n' "$*"; }

[ "$(id -u)" -eq 0 ] || { rojo "Ejecute con sudo."; exit 1; }
mountpoint -q "$PUNTO" || { rojo "$PUNTO no está montado. Ejecute antes 01_preparar_disco.sh"; exit 1; }

echo "===== Fase 4 · observabilidad ====="
echo

# ---------------------------------------------------------------------------
# 1. El diario deja de ser volátil y se muda al disco de datos
# ---------------------------------------------------------------------------
echo "== 1. Diario persistente y acotado (RNF-13) =="

install -d -o root -g systemd-journal -m 2755 "$DIARIO"
verde "$DIARIO preparado (2755 root:systemd-journal, como espera journald)."

# journald solo mira /var/log/journal. Se enlaza ahí en vez de moverlo, para
# no pelearse con la ruta que systemd lleva compilada.
if [ -L "$ENLACE" ]; then
  ACTUAL=$(readlink -f "$ENLACE")
  if [ "$ACTUAL" != "$DIARIO" ]; then
    rojo "$ENLACE ya es un enlace y apunta a $ACTUAL, no a $DIARIO."
    rojo "No se toca: revíselo a mano antes de seguir."
    exit 1
  fi
  verde "$ENLACE ya apunta a $DIARIO."
elif [ -d "$ENLACE" ]; then
  # El directorio real de la distribución. Si tiene contenido, se conserva:
  # borrar diarios antiguos sería destruir justo lo que este script protege.
  if [ -n "$(ls -A "$ENLACE" 2>/dev/null)" ]; then
    aviso "$ENLACE tiene contenido; se conserva en $ENLACE.anterior"
    mv "$ENLACE" "$ENLACE.anterior"
  else
    rmdir "$ENLACE"
  fi
  ln -s "$DIARIO" "$ENLACE"
  verde "$ENLACE -> $DIARIO"
else
  ln -s "$DIARIO" "$ENLACE"
  verde "$ENLACE -> $DIARIO"
fi

mkdir -p "$(dirname "$CONF_DIARIO")"
# El 50 gana al 40 de la distribución: los drop-in se aplican por orden.
cat > "$CONF_DIARIO" <<EOF
# Generado por 20_APROVISIONAMIENTO/08_observabilidad.sh — proyecto NAS.
# P1: si un cambio no está en el script, no existe. NO editar a mano.
#
# ADR-0037. El número 50 es deliberado: tiene que ganar al
# 40-rpi-volatile-storage.conf que trae Raspberry Pi OS, que pone el diario en
# RAM y lo pierde en cada reinicio.

[Journal]
# PERSISTENTE. Sin esto, los BORRADOS de RF-19 desaparecen al reiniciar, y con
# D-15 (sin papelera) y D-12 (copia única) son el único rastro de lo destruido.
# El diario vive en $DIARIO por el enlace de /var/log/journal: en el disco de
# DATOS, no en el medio de arranque, que por P9 es consumible.
Storage=persistent

Compress=yes
SystemMaxUse=$MAX_DIARIO
SystemKeepFree=$GUARDAR_LIBRE
SystemMaxFileSize=$MAX_ARCHIVO
MaxRetentionSec=$RETENCION

# Hasta que /srv/nas esté montado, journald escribe en /run, que es RAM.
# RES-01 son 592 MB: esta es la cota de esa ventana.
RuntimeMaxUse=32M

# Sin reenvío a syslog. Medido: rsyslog NO está instalado en este nodo, así que
# journald estaba haciendo un trabajo cuyo destinatario no existe.
ForwardToSyslog=no
EOF
verde "$CONF_DIARIO escrito."

# El 50 gana al 40 de la distribución, pero NO a «syslog.conf»: los drop-in se
# ordenan por NOMBRE DE ARCHIVO, y «syslog» va después de «50» en ese orden. Así
# que /usr/lib/systemd/journald.conf.d/syslog.conf reponía ForwardToSyslog=yes y
# la línea razonada de arriba no surtía efecto. Un archivo del MISMO NOMBRE en
# /etc sustituye al del proveedor: es la forma que systemd da para esto.
cat > "$CONF_SYSLOG" <<EOF
# Generado por 20_APROVISIONAMIENTO/08_observabilidad.sh — proyecto NAS.
# P1: si un cambio no está en el script, no existe. NO editar a mano.
#
# ADR-0039. Este archivo existe SOLO para anular al del proveedor
# (/usr/lib/systemd/journald.conf.d/syslog.conf), que repone ForwardToSyslog=yes
# y ordena DESPUÉS de 50-nas.conf. Deliberadamente no fija nada: al vaciarlo,
# quien decide vuelve a ser 50-nas.conf.
EOF
verde "$CONF_SYSLOG escrito (anula el del proveedor)."

# ---------------------------------------------------------------------------
# 1.bis  El volcado a disco tiene que esperar al montaje (ADR-0039)
# ---------------------------------------------------------------------------
# MEDIDO en el primer reinicio real del nodo:
#
#   15:45:18  systemd-journal-flush.service   <- vuelca aquí
#   15:45:22  srv-nas.mount                   <- el disco monta 4 s DESPUÉS
#
# Dos causas independientes, y CADA UNA bastaría por sí sola:
#
#   a) systemd-journal-flush trae RequiresMountsFor=/var/log/journal, que
#      resuelve a «-.mount» —la raíz— y NO a srv-nas.mount: /var/log/journal es
#      un ENLACE, y systemd resuelve los montajes del PREFIJO DE LA RUTA, no el
#      destino del enlace. La dependencia que parece existir mira a otro disco.
#   b) el «nofail» del fstab quita la ordenación Before=local-fs.target del
#      montaje. Medido: srv-nas.mount solo declara Before=umount.target.
#
# Se arregla (b) sin tocar el fstab, porque «nofail» es justo lo que impide que
# un disco ausente cuelgue el arranque de un nodo sin teclado. Se ordena, no se
# exige: si el disco no está, el trabajo de montaje falla, el After= se da por
# cumplido y el arranque sigue — journald degrada a /run como antes.
UNIDAD_MONTAJE=$(systemd-escape -p --suffix=mount "$PUNTO")
mkdir -p "$DIR_VOLCADO"
cat > "$CONF_VOLCADO" <<EOF
# Generado por 20_APROVISIONAMIENTO/08_observabilidad.sh — proyecto NAS.
# P1: si un cambio no está en el script, no existe. NO editar a mano.
#
# ADR-0039. Sin esto, journald vuelca antes de que $PUNTO esté montado, no
# vuelve a intentarlo, y el diario del arranque entero se queda en RAM.
#
# ORDENACIÓN, NO EXIGENCIA: nada de Requires= ni RequiresMountsFor=. Si el disco
# falta, el arranque debe seguir (el fstab lleva nofail a propósito).

[Unit]
After=$UNIDAD_MONTAJE
EOF
verde "$CONF_VOLCADO escrito (volcado tras $UNIDAD_MONTAJE)."
systemctl daemon-reload

systemctl restart systemd-journald
# Empuja lo que haya en /run hacia el diario persistente.
journalctl --flush >/dev/null 2>&1 || true
journalctl --vacuum-size="$MAX_DIARIO" >/dev/null 2>&1 || true

# Comprobar el EFECTO, no el archivo. Precedente: «server smb encrypt», un
# parámetro mal escrito que Samba ignoró en silencio toda la Fase 1.
sleep 1
# NADA de «productor | grep -q» bajo pipefail: grep -q sale al primer acierto,
# el productor recibe SIGPIPE y muere con estado != 0, y pipefail convierte el
# ACIERTO en fallo. Es un falso negativo que depende del tamaño de la salida —
# con pocas líneas cuela y con muchas miente—, y en 09_verificar_operacion.sh
# ya hizo reportar que los registros de nasd no eran JSON cuando sí lo eran.
# OJO CON LO QUE SE PREGUNTA. La versión anterior contaba las apariciones de
# /var/log/journal en «journalctl --header», y eso da VERDE con el diario en RAM:
# tras un reinicio, --header lista LOS DOS archivos, el de /run que está vivo y
# el del disco que solo se lee. Contaba el que se lee y no el que se escribe.
# Es ADR-0038 otra vez: el verificador no se parecía a lo que se quería saber.
#
# La pregunta correcta es dónde está el diario del sistema que journald usa
# AHORA. Si systemd-journal-flush volcó bien, /run/log/journal deja de existir.
MAQUINA=$(cat /etc/machine-id)
if [ -e "/run/log/journal/$MAQUINA/system.journal" ]; then
  rojo "journald SIGUE escribiendo en RAM (/run/log/journal). El volcado no ocurrió."
  rojo "Diagnóstico:  journalctl --header | grep 'File path'"
  exit 1
fi
if [ ! -e "$DIARIO/$MAQUINA/system.journal" ]; then
  rojo "No hay diario del sistema en $DIARIO. El archivo está escrito y NO surte efecto."
  rojo "Diagnóstico:  journalctl --header | grep 'File path'"
  exit 1
fi
verde "journald escribe en $DIARIO y /run/log/journal ha desaparecido."

# ---------------------------------------------------------------------------
# 2. Watchdog de hardware — ya existía; aquí se aprieta y se comprueba
# ---------------------------------------------------------------------------
echo
echo "== 2. Watchdog de hardware (charter §8) =="

# Son DOS cosas distintas y confundirlas deja el nodo peor vigilado de lo que
# parece:
#
#   WatchdogSec= en nasd.service   systemd vigila a NASD. Si el servicio deja
#                                  de latir, systemd lo reinicia.
#   RuntimeWatchdogSec= aquí       el SoC vigila a SYSTEMD. Si se cuelga el
#                                  kernel o PID 1, la placa se reinicia sola.
#
# MEDIDO: el segundo YA estaba a 1min por omisión de este systemd. Se aprieta
# a 30s para que un cuelgue se resuelva antes, no para «activarlo».
if [ ! -e /dev/watchdog ]; then
  rojo "No hay /dev/watchdog: el módulo bcm2835_wdt no está cargado."
  exit 1
fi
ANTES=$(systemctl show -p RuntimeWatchdogUSec --value)
verde "/dev/watchdog presente. Valor actual: ${ANTES:-0}"

mkdir -p "$(dirname "$CONF_WATCHDOG")"
cat > "$CONF_WATCHDOG" <<EOF
# Generado por 20_APROVISIONAMIENTO/08_observabilidad.sh — proyecto NAS.
# ADR-0037. NO editar a mano (P1).
#
# El SoC reinicia la placa si systemd deja de alimentar /dev/watchdog.
# Este systemd ya traía 1min por omisión; se aprieta a $WATCHDOG_RUNTIME.

[Manager]
RuntimeWatchdogSec=$WATCHDOG_RUNTIME
RebootWatchdogSec=$WATCHDOG_REINICIO
EOF
verde "$CONF_WATCHDOG escrito."

# daemon-reexec y no daemon-reload: RuntimeWatchdogSec la lee el MANAGER al
# arrancar, y un reload no la aplica. Es la clase de detalle que deja un
# archivo escrito, correcto y sin ningún efecto.
systemctl daemon-reexec

APLICADO=$(systemctl show -p RuntimeWatchdogUSec --value)
if [ "$APLICADO" = "0" ] || [ -z "$APLICADO" ]; then
  rojo "systemd NO ha tomado el watchdog (RuntimeWatchdogUSec=$APLICADO)."
  exit 1
fi
verde "Watchdog de hardware: $ANTES -> $APLICADO"

echo
aviso "CONSECUENCIA QUE HAY QUE SABER, no un detalle:"
aviso "  el watchdog cambia «nodo colgado para siempre» por «reinicio en seco»."
aviso "  Un reinicio en seco a mitad de una escritura es EXACTAMENTE el"
aviso "  escenario del supuesto S-01 (03_ESTUDIO_TECNICO §10), que sigue SIN"
aviso "  MEDIR. Ya era así antes de este script —el watchdog llevaba puesto"
aviso "  1min—; apretarlo a 30s solo hace que ocurra antes."

# ---------------------------------------------------------------------------
# 3. Unidad de nasd
# ---------------------------------------------------------------------------
echo
echo "== 3. Unidad de nasd =="
if [ -f /etc/systemd/system/nasd.service ]; then
  systemctl daemon-reload
  RATE=$(systemctl show nasd -p LogRateLimitBurst --value)
  GRUPOS=$(systemctl show nasd -p SupplementaryGroups --value)
  if [ "${RATE:-0}" -gt 1000 ]; then
    verde "Límite de tasa propio: LogRateLimitBurst=$RATE"
  else
    aviso "Todavía con el límite por omisión ($RATE). Ejecute 05_instalar_servicio.sh."
  fi
  if [ "$(printf '%s' "$GRUPOS" | grep -c video)" -gt 0 ]; then
    verde "Grupo video presente: el servicio puede leer la limitación del SoC (ADR-0036)."
  else
    aviso "Sin SupplementaryGroups=video: RNF-11 no se medirá desde el servicio."
    aviso "Ejecute 05_instalar_servicio.sh para reescribir la unidad."
  fi
else
  aviso "nasd.service no está instalado todavía."
fi

# ---------------------------------------------------------------------------
# 4. Marca de arranque en ramoops — el discriminador de P-11
# ---------------------------------------------------------------------------
#
# QUÉ PREGUNTA RESPONDE, Y POR QUÉ HOY NO SE PUEDE RESPONDER:
#
# P-11 se cerró el 2026-08-16 con la causa determinada —a la Pi se le va la
# corriente— pero UNA ambigüedad quedó viva: `ramoops` no distingue un corte
# de corriente de un cuelgue mudo. En los dos casos el núcleo no llega a
# escribir nada, el búfer de consola queda vacío, y `systemd-pstore` se salta
# con «ConditionDirectoryNotEmpty». Un búfer vacío no dice nada.
#
# La marca lo convierte en respuesta binaria. Tras el siguiente corte:
#
#   hay captura en /var/lib/systemd/pstore/  -> la RAM SOBREVIVIÓ  -> se colgó
#   no hay nada                              -> la RAM se perdió   -> corriente
#
# ES UNA SOLA LÍNEA POR ARRANQUE, no un latido periódico. Un latido cada 30 s
# añadiría 2880 líneas al diario cada día para decir algo que el diario ya
# dice: `nas-vigilar-disco` escribe cada ~65 s, así que el instante del corte
# ya está acotado a un minuto sin instrumentar nada.
#
# VA DESPUÉS de systemd-pstore a propósito: ese servicio MUEVE la captura
# anterior y al desenlazarla BORRA la zona. Escribir antes sería escribir en
# algo que se va a borrar.
CONF_CONSOLA=/etc/sysctl.d/99-nas-consola.conf
MARCA_SH=/usr/local/sbin/nas-marca-arranque.sh
MARCA_UNIDAD=/etc/systemd/system/nas-marca-arranque.service

echo
echo "== 4. Marca de arranque en ramoops (discriminador de P-11) =="

# El nivel de consola por omisión es 4: solo pasan los niveles 0-3, es decir
# de «emerg» a «err». Sin subirlo, la marca tendría que emitirse como ERROR
# para llegar a ramoops, y una línea rutinaria disfrazada de error ensucia
# justo la herramienta con la que se buscan los errores de verdad.
#
# Se sube a 7 y no se toca nada más. En este nodo NO cuesta rendimiento:
# `8250.nr_uarts=0` deja la UART apagada y /proc/consoles solo lista `tty1`
# (sin pantalla) y `ramoops-1`. Y de propina, cualquier mensaje que el núcleo
# sí llegue a emitir antes de un cuelgue queda capturado — hoy se perdería.
cat > "$CONF_CONSOLA" <<'EOF'
# Nivel de consola a 7 para que los mensajes del núcleo lleguen a ramoops.
# Dueño: 20_APROVISIONAMIENTO/08_observabilidad.sh. 00_RECTOR.md §7.sexies.
kernel.printk = 7 4 1 7
EOF
sysctl -q -p "$CONF_CONSOLA"

cat > "$MARCA_SH" <<'EOF'
#!/bin/sh
# Marca el búfer de consola de ramoops para que NO quede vacío.
#
# Un búfer vacío tras un corte es ambiguo: lo producen igual un corte de
# corriente y un cuelgue mudo. Con esta línea dentro, deja de serlo — si
# reaparece en /var/lib/systemd/pstore/ tras el siguiente corte, la RAM
# sobrevivió y el nodo se colgó; si no aparece nada, la RAM perdió la
# corriente. 00_RECTOR.md §7.sexies.
set -eu
printf '<6>nas: marca de arranque %s\n' "$(date -Is)" > /dev/kmsg
EOF
chmod 0755 "$MARCA_SH"

cat > "$MARCA_UNIDAD" <<'EOF'
[Unit]
Description=Marcar el buffer de ramoops para distinguir un corte de un cuelgue
Documentation=file:///home/usuario/nas-aprovisionamiento/08_observabilidad.sh
# DESPUES de systemd-pstore: ese servicio mueve la captura anterior y al
# desenlazarla BORRA la zona. Escribir antes seria escribir en algo que se
# va a borrar. Un systemd-pstore SALTADO tambien ordena, asi que esto vale
# igual el dia que no haya captura previa que archivar.
After=systemd-pstore.service

[Service]
Type=oneshot
ExecStart=/usr/local/sbin/nas-marca-arranque.sh
RemainAfterExit=yes

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable --now nas-marca-arranque.service >/dev/null 2>&1 || true

# COMPROBADO POR SU EFECTO, no por lo que diga el script: que el nivel esté
# puesto, que ramoops siga siendo una consola registrada —sin eso la marca no
# llega a ninguna parte— y que la línea exista de verdad en el anillo del
# núcleo.
NIVEL=$(cut -f1 /proc/sys/kernel/printk)
if [ "$NIVEL" -ge 7 ]; then
  verde "Nivel de consola: $NIVEL (la marca llega a ramoops)"
else
  rojo "Nivel de consola es $NIVEL: la marca NO llegaría a ramoops."
  exit 1
fi
if grep -q ramoops /proc/consoles; then
  verde "ramoops sigue registrado como consola"
else
  rojo "ramoops NO está registrado como consola: falta dtoverlay=ramoops en config.txt."
  exit 1
fi
if dmesg | grep -q "nas: marca de arranque"; then
  verde "Marca escrita y presente en el anillo del núcleo"
else
  rojo "La marca no aparece en dmesg."
  exit 1
fi

echo
aviso "LO QUE ESTA MARCA NO HACE: no evita ningún corte ni avisa de ninguno."
aviso "  Solo hace legible el SIGUIENTE. La causa de P-11 ya está determinada"
aviso "  (§7.sexies) y lo que queda es una acción física del responsable."

echo
verde "Paso completado."
echo "Siguiente: sudo ./09_verificar_operacion.sh"
