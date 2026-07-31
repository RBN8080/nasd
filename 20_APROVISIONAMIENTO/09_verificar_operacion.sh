#!/bin/bash
# Fase 4, paso 2 — verificar la operación CONTRA EL NODO REAL.
#
# Comprueba lo que exige el charter §8: métricas, extremo de estado, diario
# rotado con límite, watchdog —de servicio y de hardware— y que los SLI de
# 05_OPERACION.md se estén midiendo de verdad.
#
# REGLA DE ESTE SCRIPT, y de todos los de este proyecto:
#   «no se pudo comprobar» NO es «correcto». Este proyecto lleva cuatro
#   defectos corregidos por confundir las dos cosas (00_RECTOR.md §12.5). Aquí
#   lo no verificable se reporta como AUSENTE y cuenta como fallo.
#
# RES-09: no ejecutar con VSCode Remote-SSH conectado.
#
# Uso: sudo ./09_verificar_operacion.sh

set -uo pipefail

# ADR-0032: puerto 80. La IP de la LAN, porque ADR-0018 enlaza ahí y NO a
# 127.0.0.1 — apuntar a localhost hacía que todos los curl dieran 000.
IP=$(hostname -I | awk '{print $1}')
BASE="http://$IP"
GALLETAS=$(mktemp)
ok=0; mal=0

si()    { printf '  \033[32mOK\033[0m    %s\n' "$1"; ok=$((ok+1)); }
no()    { printf '  \033[31mFALLO\033[0m %s\n' "$1"; mal=$((mal+1)); }
dato()  { printf '  \033[36mDATO\033[0m  %s\n' "$1"; }

# shellcheck disable=SC2329  # se invoca desde el trap de abajo, que shellcheck no sigue
limpiar() { rm -f "$GALLETAS"; }
trap limpiar EXIT

[ "$(id -u)" -eq 0 ] || { echo "Ejecute con sudo."; exit 1; }
systemctl is-active --quiet nasd || { echo "nasd no está activo."; exit 1; }

echo "===== Fase 4 · verificación de la operación ====="
echo

# ---------------------------------------------------------------------------
echo "-- Charter §8: diario estructurado y ROTADO con límite (RNF-13) --"

USO=$(journalctl --disk-usage 2>/dev/null | grep -oE '[0-9.]+[KMG]' | head -1)
TOPE=$(systemd-analyze cat-config systemd/journald.conf 2>/dev/null \
       | awk -F= '/^SystemMaxUse=/{v=$2} END{print v}')
ALMACEN=$(systemd-analyze cat-config systemd/journald.conf 2>/dev/null \
       | awk -F= '/^Storage=/{v=$2} END{print v}')
if [ -n "$TOPE" ]; then
  si "límite de tamaño configurado: SystemMaxUse=$TOPE"
else
  no "no hay SystemMaxUse: el diario NO está acotado. Ejecute 08_observabilidad.sh"
fi
dato "ocupación actual del diario: ${USO:-desconocida}"

# Lo que de verdad importa de RF-19: que el rastro de un borrado sobreviva a un
# reinicio. Raspberry Pi OS trae Storage=volatile y lo perdía TODO al arrancar.
if [ "$ALMACEN" = "persistent" ]; then
  si "diario PERSISTENTE: los borrados de RF-19 sobreviven a un reinicio"
else
  no "diario con Storage=${ALMACEN:-por omisión}: se PIERDE al reiniciar, y con él el rastro de los borrados (RF-19)"
fi

# Y que esté en el disco de datos y no en el medio de arranque (ADR-0037).
DESTINO=$(readlink -f /var/log/journal 2>/dev/null)
case "$DESTINO" in
  /srv/nas/*) si "el diario vive en el disco de datos: $DESTINO (no gasta el medio de arranque)" ;;
  "")         no "no existe /var/log/journal" ;;
  *)          no "el diario está en $DESTINO, no en el disco de datos (ADR-0037)" ;;
esac

# Un solo arranque en la lista significa que el diario no sobrevivió al último
# reinicio. Ese fue el síntoma que destapó el problema.
ARRANQUES=$(journalctl --list-boots 2>/dev/null | grep -cE '^ *-?[0-9]+ ')
dato "arranques conservados en el diario: ${ARRANQUES:-0}"

# Que la línea esté escrita no basta: journald tiene que haberla tomado. Este
# proyecto ya se encontró con «server smb encrypt», un parámetro que Samba
# ignoraba EN SILENCIO porque estaba mal escrito (rector v1.3.0).
# NO se usa «productor | grep -q»: grep -q sale al PRIMER acierto, el productor
# recibe SIGPIPE y muere con estado != 0, y «set -o pipefail» convierte el
# ACIERTO en fallo. Da un FALSO NEGATIVO que depende del tamaño de la salida:
# con pocas líneas cuela, con muchas miente. Aquí lo hizo: reportaba que los
# registros de nasd no eran JSON cuando 44 de las 50 líneas lo eran.
#
# Es el mismo defecto que este proyecto lleva persiguiendo (00_RECTOR.md §12.5),
# en su versión ruidosa: un verificador que grita sin motivo se acaba ignorando.
# Se cuenta primero y se decide después.
N=$(journalctl -u nasd -n 1 --no-pager -o json 2>/dev/null | grep -c '"MESSAGE"')
if [ "${N:-0}" -gt 0 ]; then
  si "el diario de nasd es legible y estructurado en JSON"
else
  no "no se pudo leer el diario de nasd en formato estructurado"
fi

# El registro del servicio va en JSON por stdout (RNF-13). Se comprueba que lo
# que llega a journald es JSON de verdad, no texto suelto.
N=$(journalctl -u nasd -n 50 --no-pager -o cat 2>/dev/null | grep -c '^{.*"level":')
if [ "${N:-0}" -gt 0 ]; then
  si "las líneas de nasd llegan como JSON con nivel: $N de las últimas 50 (RNF-13)"
else
  no "las líneas de nasd no parecen JSON estructurado"
fi

# ---------------------------------------------------------------------------
echo
echo "-- Charter §8: watchdog --"

WD_SERVICIO=$(systemctl show nasd -p WatchdogUSec --value)
if [ "${WD_SERVICIO:-0}" != "0" ] && [ -n "$WD_SERVICIO" ]; then
  si "watchdog de SERVICIO activo: nasd late cada ${WD_SERVICIO}"
else
  no "nasd sin WatchdogSec: nadie vigila que el servicio siga respondiendo"
fi

# OJO: este ya venia activo de fabrica a 1min en este systemd. ADR-0035 decia
# que «no existia» y era FALSO; ADR-0037 lo corrige. Aqui se comprueba que
# sigue en pie y que 08_observabilidad.sh lo apreto.
WD_HW=$(systemctl show -p RuntimeWatchdogUSec --value)
if [ "${WD_HW:-0}" != "0" ] && [ -n "$WD_HW" ]; then
  si "watchdog de HARDWARE activo: RuntimeWatchdogUSec=$WD_HW"
else
  no "sin watchdog de hardware. El charter §8 lo exige (bcm2835_wdt). Ejecute 08_observabilidad.sh"
fi
# NO se usa «A && B || C»: no es if-then-else, y en un script cuyo único
# trabajo es informar con verdad puede hacerle decir lo contrario de lo que
# ocurre. Ya salió una vez en 04_verificar.sh (rector v1.2.0).
if [ -e /dev/watchdog ]; then
  dato "/dev/watchdog presente"
else
  dato "/dev/watchdog AUSENTE"
fi

LIMITE=$(systemctl show nasd -p LogRateLimitBurst --value)
if [ "${LIMITE:-0}" -gt 1000 ]; then
  si "límite de tasa del diario propio de nasd: $LIMITE por intervalo"
else
  no "nasd con el límite de tasa por omisión: una ráfaga podría descartar un BORRADO (RF-19)"
fi

REINICIOS=$(systemctl show nasd -p NRestarts --value)
if [ "${REINICIOS:-0}" = "0" ]; then
  si "SLI-4: cero reinicios no planificados desde el último arranque"
else
  no "SLI-4 INCUMPLIDO: nasd se ha reiniciado $REINICIOS veces. Busque el motivo en el diario"
fi

# ---------------------------------------------------------------------------
echo
echo "-- RF-24: extremo de estado --"

# Primero SIN sesión. ADR-0034 lo pone detrás de la autenticación a propósito.
COD=$(curl -s -o /dev/null -w '%{http_code}' "$BASE/estado")
if [ "$COD" = "401" ] || [ "$COD" = "403" ]; then
  si "/estado sin sesión → $COD (ADR-0034: no se regala reconocimiento en la LAN)"
else
  no "/estado sin sesión devolvió $COD; debería ser 401 o 403"
fi

echo
echo "  Para el resto hace falta la contraseña de la WEB (la de D-14, no la de Samba)."
read -rs -p "  Contraseña de la web (Intro para saltar): " CLAVE; echo

if [ -z "$CLAVE" ]; then
  # Saltar NO es aprobar. Se cuentan como fallos porque quedan sin verificar.
  no "SIN VERIFICAR: /estado con sesión (no se dio contraseña)"
  no "SIN VERIFICAR: métricas del nodo (no se dio contraseña)"
else
  COD=$(curl -s -o /dev/null -w '%{http_code}' -c "$GALLETAS" \
        --data-urlencode "clave=$CLAVE" "$BASE/acceso")
  unset CLAVE
  if [ "$COD" != "303" ]; then
    no "no se pudo abrir sesión (HTTP $COD); el resto queda SIN VERIFICAR"
    no "SIN VERIFICAR: métricas del nodo"
  else
    JSON=$(curl -s -b "$GALLETAS" "$BASE/estado?formato=json")

    if [ "$(printf '%s' "$JSON" | grep -c '"veredicto"')" -gt 0 ]; then
      si "/estado con sesión devuelve JSON con veredicto global"
    else
      no "/estado no devolvió el JSON esperado"
    fi

    VEREDICTO=$(printf '%s' "$JSON" | grep -o '"veredicto": *"[a-z]*"' | head -1 | cut -d'"' -f4)
    dato "veredicto global del nodo: ${VEREDICTO:-desconocido}"

    # Métricas del charter §8, una a una. «null» significa NO MEDIDO, y aquí
    # eso cuenta como fallo: el charter las pide, no las sugiere.
    for CAMPO in temperatura_c cpu_porcentaje ram_total_bytes carga_1min; do
      VALOR=$(printf '%s' "$JSON" | grep -o "\"$CAMPO\": *[^,}]*" | head -1 | cut -d: -f2- | tr -d ' ')
      if [ -z "$VALOR" ] || [ "$VALOR" = "null" ]; then
        no "métrica $CAMPO NO MEDIDA (null)"
      else
        si "métrica $CAMPO = $VALOR"
      fi
    done

    # La limitación del SoC es el caso especial: puede no estar publicada en
    # sysfs de este kernel. Si falta, se comprueba a mano y se DICE, en vez de
    # dejar un hueco silencioso.
    TH=$(printf '%s' "$JSON" | grep -o '"throttled": *[^,}]*' | head -1 | cut -d: -f2- | tr -d ' "')
    if [ -n "$TH" ] && [ "$TH" != "null" ]; then
      si "métrica throttled leída de sysfs por el servicio: $TH"
    else
      no "throttled NO disponible para el servicio"
      dato "  comprobación manual: $(vcgencmd get_throttled 2>/dev/null || echo 'vcgencmd no disponible')"
      dato "  si a mano funciona y el servicio no lo ve, le falta a la unidad"
      dato "  SupplementaryGroups=video y DeviceAllow=/dev/vchiq (ADR-0036)."
      dato "  Grupos actuales: $(systemctl show nasd -p SupplementaryGroups --value)"
    fi

    # Salud del medio de arranque: la parte que depende de poder leer el
    # /proc/1/mounts desde dentro del espacio de nombres endurecido.
    RO=$(printf '%s' "$JSON" | grep -o '"solo_lectura": *[^,}]*' | head -1 | cut -d: -f2- | tr -d ' ')
    if [ "$RO" = "false" ]; then
      si "medio de arranque montado de lectura y escritura, comprobado"
    elif [ "$RO" = "true" ]; then
      no "MEDIO DE ARRANQUE EN SOLO LECTURA: síntoma de medio agonizante (P9)"
    else
      no "no se pudo comprobar el remontaje de solo lectura: ¿ilegible /proc/1/mounts?"
      dato "  el servicio lo declara como «no se sabe», que es lo correcto, pero"
      dato "  deja la salud del medio de arranque sin uno de sus dos síntomas"
    fi

    # Que los contadores se muevan, no solo que existan.
    PET=$(printf '%s' "$JSON" | grep -o '"peticiones": *[0-9]*' | head -1 | grep -o '[0-9]*$')
    if [ "${PET:-0}" -gt 0 ]; then
      si "los contadores de SLI se están alimentando: $PET peticiones anotadas"
    else
      no "los contadores están a cero tras haber pedido /estado: no se están alimentando"
    fi
  fi
fi

# ---------------------------------------------------------------------------
echo
echo "-- Salud del medio de arranque desde el propio nodo (P9) --"
RAIZ=$(findmnt -no SOURCE / 2>/dev/null)
CORTO=$(basename "${RAIZ:-desconocido}")
if [ -r "/sys/fs/ext4/$CORTO/lifetime_write_kbytes" ]; then
  KB=$(cat "/sys/fs/ext4/$CORTO/lifetime_write_kbytes")
  si "desgaste del medio de arranque medible: $((KB / 1024)) MB escritos desde el formateo"
else
  no "no se puede leer lifetime_write_kbytes de $CORTO: sin medida de desgaste"
fi
if [ -r "/sys/fs/ext4/$CORTO/errors_count" ]; then
  ERR=$(cat "/sys/fs/ext4/$CORTO/errors_count")
  if [ "$ERR" = "0" ]; then si "cero errores de ext4 en el medio de arranque"
  else no "ext4 ha registrado $ERR errores en $CORTO: pase «fsck -f» cuanto antes"; fi
else
  dato "errors_count no disponible en $CORTO"
fi

echo
echo "-- Endurecimiento (no debe haber empeorado con la Fase 4) --"
NOTA=$(systemd-analyze security nasd --no-pager 2>/dev/null | tail -1)
dato "${NOTA:-no disponible}"

echo
echo "===== RESULTADO: $ok correctas, $mal fallidas ====="
exit "$mal"
