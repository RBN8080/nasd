#!/bin/bash
# Fase 4, paso 1 — rotación del diario con límite y watchdog de hardware.
#
#   Charter §8   «Logs: journald con límite de tamaño (P9). Sin logs no rotados»
#   Charter §8   «Salud: endpoint de estado y watchdog de hardware (bcm2835_wdt)»
#   RNF-13       registros estructurados y ROTADOS
#   ADR-0035     las dos decisiones de este script, con su razonamiento
#
# Idempotente: se puede volver a ejecutar sin efectos raros. P1: si un cambio
# no está en un script, no existe.
#
# Uso: sudo ./08_observabilidad.sh

set -euo pipefail

CONF_DIARIO=/etc/systemd/journald.conf.d/nas.conf
CONF_WATCHDOG=/etc/systemd/system.conf.d/nas-watchdog.conf

# Tope del diario. El medio de arranque es CONSUMIBLE (P9) y es donde vive
# /var/log/journal: el límite acota a la vez el espacio y el desgaste.
#
# 200 MB comprimidos son varios meses del ritmo real de este producto —una
# línea por petición, y las peticiones las hace una persona—. Se prefiere
# holgura en el histórico porque los BORRADOS de RF-19 solo existen aquí:
# sin papelera (D-15) ni segunda copia (D-12), el diario es lo único que dice
# qué había en un archivo que ya no está.
MAX_DIARIO=200M
GUARDAR_LIBRE=500M
MAX_ARCHIVO=20M
RETENCION=3month

# Plazo del watchdog de HARDWARE, el que vigila a systemd (PID 1). Distinto
# del WatchdogSec=30s de nasd.service, que vigila al servicio.
WATCHDOG_RUNTIME=30s
WATCHDOG_REINICIO=2min

rojo()  { printf '\033[31m%s\033[0m\n' "$*"; }
verde() { printf '\033[32m%s\033[0m\n' "$*"; }
aviso() { printf '\033[33m%s\033[0m\n' "$*"; }

[ "$(id -u)" -eq 0 ] || { rojo "Ejecute con sudo."; exit 1; }

echo "===== Fase 4 · observabilidad ====="
echo

# ---------------------------------------------------------------------------
# 1. Rotación del diario con límite
# ---------------------------------------------------------------------------
echo "== 1. Diario con límite de tamaño (RNF-13) =="

mkdir -p "$(dirname "$CONF_DIARIO")"
cat > "$CONF_DIARIO" <<EOF
# Generado por 20_APROVISIONAMIENTO/08_observabilidad.sh — proyecto NAS.
# P1: si un cambio no está en el script, no existe. NO editar a mano.
#
# ADR-0035. El charter §8 exige diario rotado con límite, y P9 recuerda que
# el medio de arranque es consumible: aquí se acotan las dos cosas a la vez.

[Journal]
# PERSISTENTE y no volátil, a conciencia. Volatile ahorraría escrituras en el
# medio de arranque, pero perdería el diario en cada reinicio — y con él los
# BORRADOS de RF-19, que sin papelera son el único rastro de lo destruido.
# Se paga el desgaste y se acota con el tope de abajo.
Storage=persistent

# Comprimir reduce lo que de verdad se escribe en el medio consumible.
Compress=yes

SystemMaxUse=$MAX_DIARIO
SystemKeepFree=$GUARDAR_LIBRE
SystemMaxFileSize=$MAX_ARCHIVO
MaxRetentionSec=$RETENCION

# El diario de /run (volátil) no debe comerse la RAM: RES-01 son 592 MB.
RuntimeMaxUse=32M

# Sin reenvío a syslog: duplicaría cada línea en /var/log/*, es decir, el
# DOBLE de escrituras sobre el medio de arranque a cambio de nada.
ForwardToSyslog=no
EOF
verde "$CONF_DIARIO escrito."

systemctl restart systemd-journald
journalctl --vacuum-size="$MAX_DIARIO" >/dev/null 2>&1 || true
verde "journald reiniciado y diario recortado al tope."

if dpkg -l rsyslog 2>/dev/null | grep -q '^ii'; then
  aviso "rsyslog está instalado: seguirá escribiendo su propia copia en /var/log/."
  aviso "ForwardToSyslog=no corta el grifo desde journald, pero si nadie usa"
  aviso "rsyslog en este nodo, desinstalarlo ahorra escrituras (P9)."
fi

# ---------------------------------------------------------------------------
# 2. Watchdog de hardware
# ---------------------------------------------------------------------------
echo
echo "== 2. Watchdog de hardware (charter §8) =="

# Son DOS cosas distintas y confundirlas deja el nodo peor vigilado de lo que
# parece:
#
#   WatchdogSec= en nasd.service   systemd vigila a NASD. Si el servicio deja
#                                  de latir, systemd lo reinicia. YA EXISTE.
#   RuntimeWatchdogSec= aquí       el SoC vigila a SYSTEMD. Si se cuelga el
#                                  kernel o PID 1, la placa se reinicia sola.
#                                  Es lo que pide el charter y NO existía.
if [ ! -e /dev/watchdog ]; then
  rojo "No hay /dev/watchdog: el módulo bcm2835_wdt no está cargado."
  rojo "Sin él, RuntimeWatchdogSec no vigila nada. NO se configura a medias."
  exit 1
fi
verde "/dev/watchdog presente."

mkdir -p "$(dirname "$CONF_WATCHDOG")"
cat > "$CONF_WATCHDOG" <<EOF
# Generado por 20_APROVISIONAMIENTO/08_observabilidad.sh — proyecto NAS.
# ADR-0035. NO editar a mano (P1).
#
# El SoC reinicia la placa si systemd deja de alimentar /dev/watchdog.

[Manager]
RuntimeWatchdogSec=$WATCHDOG_RUNTIME
RebootWatchdogSec=$WATCHDOG_REINICIO
EOF
verde "$CONF_WATCHDOG escrito."

# daemon-reexec y no daemon-reload: RuntimeWatchdogSec la lee el MANAGER al
# arrancar, y un simple reload no la aplica. Es la clase de detalle que deja
# un archivo escrito, bonito y sin efecto.
systemctl daemon-reexec

APLICADO=$(systemctl show -p RuntimeWatchdogUSec --value)
if [ "$APLICADO" = "0" ] || [ -z "$APLICADO" ]; then
  rojo "systemd NO ha tomado el watchdog (RuntimeWatchdogUSec=$APLICADO)."
  rojo "El archivo está escrito pero no surte efecto. NO se da por bueno."
  exit 1
fi
verde "Watchdog de hardware activo: RuntimeWatchdogUSec=$APLICADO"

echo
aviso "CONSECUENCIA QUE HAY QUE SABER, no un detalle:"
aviso "  el watchdog cambia «nodo colgado para siempre» por «reinicio en seco»."
aviso "  Un reinicio en seco a mitad de una escritura es EXACTAMENTE el"
aviso "  escenario del supuesto S-01 (03_ESTUDIO_TECNICO §10), que sigue SIN"
aviso "  MEDIR: la atomicidad de ADR-0024 debería protegerlo, y nadie lo ha"
aviso "  comprobado cortando la corriente de verdad."

# ---------------------------------------------------------------------------
# 3. Recargar la unidad de nasd, que ahora trae límite de tasa propio
# ---------------------------------------------------------------------------
echo
echo "== 3. Unidad de nasd =="
if [ -f /etc/systemd/system/nasd.service ]; then
  systemctl daemon-reload
  RATE=$(systemctl show nasd -p LogRateLimitBurst --value)
  if [ "${RATE:-0}" -gt 1000 ]; then
    verde "nasd con límite de tasa propio: LogRateLimitBurst=$RATE"
  else
    aviso "nasd todavía con el límite por omisión (LogRateLimitBurst=$RATE)."
    aviso "Vuelva a ejecutar 05_instalar_servicio.sh para reescribir la unidad."
  fi
else
  aviso "nasd.service no está instalado todavía."
fi

echo
verde "Paso completado."
echo "Siguiente: sudo ./09_verificar_operacion.sh"
