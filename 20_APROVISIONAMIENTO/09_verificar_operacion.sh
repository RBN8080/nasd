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

# Storage=persistent es CONDICIÓN NECESARIA Y NO SUFICIENTE, y confundir las dos
# cosas es exactamente lo que dejó pasar el defecto de ADR-0039. Se dice así.
if [ "$ALMACEN" = "persistent" ]; then
  si "diario configurado como PERSISTENTE (necesario; la prueba está más abajo)"
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

# ---------------------------------------------------------------------------
# LA COMPROBACIÓN QUE FALTABA (ADR-0039), y la única que habría detectado el
# defecto. Las TRES de arriba daban OK con el diario entero en RAM:
#
#   - Storage=persistent estaba escrito y journald lo había leído;
#   - el enlace apuntaba al disco de datos;
#   - y --list-boots contaba arranques, porque LEE el archivo del disco.
#
# Ninguna preguntaba dónde se ESCRIBE ahora. Si systemd-journal-flush volcó bien,
# /run/log/journal deja de existir; si sigue ahí, journald está en RAM y este
# arranque se perderá entero en el próximo reinicio.
MAQUINA=$(cat /etc/machine-id 2>/dev/null)
if [ -n "$MAQUINA" ] && [ -e "/run/log/journal/$MAQUINA/system.journal" ]; then
  no "journald ESCRIBE EN RAM (/run/log/journal): este arranque se perderá al reiniciar, con los borrados de RF-19 dentro"
elif [ -n "$MAQUINA" ] && [ -e "$DESTINO/$MAQUINA/system.journal" ]; then
  si "journald ESCRIBE en el diario persistente y /run/log/journal ha desaparecido"
else
  no "no se pudo determinar dónde escribe journald: se cuenta como fallo (regla de este script)"
fi

# La causa era de ORDEN: el volcado corría antes de montar el disco. Se comprueba
# la ordenación además del efecto, porque el efecto de arriba también sale bien
# si alguien volcó a mano tras arrancar — y eso no sobrevive al siguiente inicio.
UNIDAD_MONTAJE=$(systemd-escape -p --suffix=mount /srv/nas)
N=$(systemctl show systemd-journal-flush.service -p After --value 2>/dev/null | tr ' ' '\n' | grep -c "^$UNIDAD_MONTAJE$")
if [ "${N:-0}" -gt 0 ]; then
  si "el volcado del diario está ordenado tras $UNIDAD_MONTAJE (ADR-0039)"
else
  no "systemd-journal-flush NO espera a $UNIDAD_MONTAJE: volcará antes de montar y el diario quedará en RAM. Ejecute 08_observabilidad.sh"
fi

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
echo "  Para el resto hace falta la credencial única de WEB y Samba (ADR-0047)."
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
echo "-- Resiliencia del disco de datos (ADR-0040) --"

# El 2026-07-31 el puente USB-SATA dejó de responder, el kernel descartó el
# disco a los 30 s y ext4 se apagó. Se comprueba que las tres mitigaciones
# siguen puestas, porque ninguna es visible en el uso diario: un nodo con esto
# desactivado se comporta EXACTAMENTE igual hasta el día que vuelve a fallar.
PART=$(findmnt -no SOURCE /srv/nas 2>/dev/null)
DISCO_DATOS=$(lsblk -no PKNAME "$PART" 2>/dev/null)

PLAZO=$(cat "/sys/block/$DISCO_DATOS/device/timeout" 2>/dev/null)
if [ "${PLAZO:-0}" -ge 180 ]; then
  si "plazo del kernel antes de descartar el disco: ${PLAZO}s (era 30s el día del incidente)"
else
  no "plazo del kernel en ${PLAZO:-desconocido}s: una respuesta lenta del puente USB tirará el disco. Ejecute 10_resiliencia_disco.sh"
fi

COMPORTA=$(dumpe2fs -h "$PART" 2>/dev/null | sed -n 's/^Errors behavior: *//p')
if [ "$COMPORTA" = "Remount read-only" ]; then
  si "ante error de E/S el volumen se remonta en solo-lectura, no sigue escribiendo"
else
  no "comportamiento ante error = «${COMPORTA:-desconocido}»: seguiría escribiendo sobre un volumen que ya falló, y con D-12 no hay segunda copia"
fi

# OJO: ext4 solo imprime «errors=» cuando DIFIERE de su valor por omisión, que
# es remount-ro. La señal buena es la AUSENCIA de errors=continue, no la
# presencia de errors=remount-ro. Comprobar la presencia haría fallar a un nodo
# bien configurado.
OPC_NAS=$(findmnt -no OPTIONS /srv/nas 2>/dev/null)
case "$OPC_NAS" in
  *errors=continue*) no "el montaje en vigor sigue en «continue»: $OPC_NAS" ;;
  "")                no "no se pudieron leer las opciones de /srv/nas" ;;
  *)                 si "montaje en vigor sin «continue»: $OPC_NAS" ;;
esac

if systemctl is-active --quiet nas-vigilar-disco.timer 2>/dev/null; then
  si "vigilante de montaje activo: recupera el disco solo si vuelve a caerse"
else
  no "vigilante de montaje INACTIVO: una caída del bus dejaría el NAS inaccesible hasta repararlo a mano. Ejecute 10_resiliencia_disco.sh"
fi

# smartctl es la herramienta que faltaba el día del incidente. smartd, en cambio,
# no debe correr: es un demonio que nadie decidió en un nodo de 592 MB (P8).
if command -v smartctl >/dev/null 2>&1; then
  SALUD=$(smartctl -d sat -H "$PART" 2>/dev/null | sed -n 's/^SMART overall-health self-assessment test result: *//p')
  case "$SALUD" in
    PASSED) si "SMART del disco de datos: PASSED" ;;
    "")     dato "SMART no legible a través del puente USB" ;;
    *)      no "SMART del disco de datos: $SALUD — RESPALDE AHORA" ;;
  esac
else
  no "smartctl ausente: no se puede saber si el disco se está muriendo. Ejecute 10_resiliencia_disco.sh"
fi

echo
echo "-- Fase 5: acceso remoto por WireGuard (ADR-0043) --"

# Solo se comprueba si el tunel esta instalado. Antes de la Fase 5 este bloque
# no aplica y callarse es lo correcto: un FALLO por algo que aun no existe
# entrena a ignorar los fallos, que es peor que no comprobar.
if [ ! -f /etc/wireguard/wg0.conf ]; then
  dato "túnel no instalado: bloque omitido (correcto antes de la Fase 5)"
else
  if ip link show wg0 >/dev/null 2>&1; then
    si "interfaz wg0 levantada"
  else
    no "wg0 no existe: el acceso remoto está caído"
  fi

  if systemctl is-enabled --quiet wg-quick@wg0 2>/dev/null; then
    si "el túnel se levanta solo al arrancar"
  else
    no "wg-quick@wg0 no habilitado: no sobrevivirá a un reinicio"
  fi

  N_PUERTO=$(ss -ulnp 2>/dev/null | grep -c ':61820 ')
  if [ "${N_PUERTO:-0}" -gt 0 ]; then
    si "escuchando en 61820/udp"
  else
    no "nada escucha en 61820/udp"
  fi

  REGLAS_WG=$(nft list ruleset 2>/dev/null)
  N_WG=$(printf '%s' "$REGLAS_WG" | grep -c 'udp dport 61820 accept')
  if [ "${N_WG:-0}" -gt 0 ]; then
    si "el cortafuegos acepta 61820/udp"
  else
    no "el cortafuegos NO acepta 61820/udp: nadie podrá entrar"
  fi

  # Que forward siga cerrado es la prueba de que el tunel NO alcanza el resto
  # de la casa. Si esto se rompe, el radio de una clave perdida crece de un
  # servidor a la red entera.
  N_FWD5=$(printf '%s' "$REGLAS_WG" | grep -A1 'hook forward' | grep -c 'policy drop')
  if [ "${N_FWD5:-0}" -gt 0 ]; then
    si "forward en policy drop: el túnel llega al NAS y a nada más"
  else
    no "forward ya NO está en drop: el túnel podría alcanzar otros equipos de la casa"
  fi

  # LO QUE DE VERDAD IMPORTA: que ALGUIEN haya conectado alguna vez. Que la
  # interfaz exista no prueba que sea alcanzable desde fuera — es justo el
  # error que costó la noche del 2026-07-31, cuando se dio por imposible algo
  # que ya estaba funcionando.
  N_PARES=$(wg show wg0 peers 2>/dev/null | grep -c .)
  N_SALUDOS=$(wg show wg0 latest-handshakes 2>/dev/null | awk '$2>0' | grep -c .)
  dato "dispositivos dados de alta: ${N_PARES:-0}"
  if [ "${N_SALUDOS:-0}" -gt 0 ]; then
    si "$N_SALUDOS de ${N_PARES:-0} han conectado alguna vez: el túnel es alcanzable de verdad"
  else
    no "NINGÚN dispositivo ha conectado nunca: la interfaz existe pero no está demostrado que se llegue desde fuera"
  fi

  # El extremo de los perfiles: IP fija es deuda conocida, no un fallo.
  EXTREMO_PERFIL=$(grep -h '^Endpoint' /etc/wireguard/clientes/*.conf 2>/dev/null | head -1 | sed 's/.*= *//')
  case "$EXTREMO_PERFIL" in
    *duckdns.org*|*[a-z].[a-z]*) si "los perfiles usan un NOMBRE: sobreviven a un cambio de IP" ;;
    "")                          dato "no se pudieron leer los perfiles de cliente" ;;
    *)                           dato "DEUDA: los perfiles apuntan a la IP $EXTREMO_PERFIL, no a un nombre. Cuando el proveedor la cambie, el acceso remoto morirá EN SILENCIO. Se salda con 11_ddns.sh" ;;
  esac

  # D-21 — SAMBA POR EL TÚNEL. Esta comprobación existe porque su ausencia
  # costó una sesión entera el 2026-08-01: el cortafuegos aceptaba el 445 por
  # wg0, pero smb.conf filtraba por origen con «hosts allow» y solo tenía la
  # LAN, asi que Samba rechazaba la sesión DESPUÉS de que el cortafuegos la
  # dejara pasar. La web funcionaba y SMB no, y el síntoma parecía de red.
  # Ninguna comprobación anterior lo habría visto.
  if testparm -s 2>/dev/null | grep -q 'hosts allow.*10\.77\.0\.0/24'; then
    si "Samba acepta el origen del túnel: SMB funciona desde fuera de casa"
  else
    no "Samba NO acepta 10.77.0.0/24 en hosts allow: la web irá por el túnel y SMB no (D-21)"
  fi

  # EL MANTENEDOR DE NAT. Sin él, el nodo queda alcanzable solo mientras haya
  # tráfico: caducan la asociación del CGNAT, el NAT del router y la entrada
  # ARP del router para este nodo, y el acceso remoto muere EN SILENCIO — que
  # es exactamente lo que pasaba antes de existir. Ver 06_ACCESO_REMOTO §10.
  if systemctl is-active --quiet nas-mantener-nat 2>/dev/null; then
    si "el mantenedor de NAT está vivo: la ruta de entrada no caduca"
  else
    no "nas-mantener-nat CAÍDO: el acceso remoto dejará de funcionar en minutos, sin aviso"
  fi
  if systemctl is-enabled --quiet nas-mantener-nat 2>/dev/null; then
    si "el mantenedor sobrevive al reinicio"
  else
    no "nas-mantener-nat no habilitado: tras un reinicio el acceso remoto no volverá"
  fi

  # DDNS. Que el temporizador exista no basta: lo que importa es que el nombre
  # resuelva a la IP que el mundo ve de verdad. Se usa un servicio de eco HTTP
  # porque aquí la pregunta es «qué IP ve Internet», que es justo para lo que
  # sirve; NO se usa para deducir si hay CGNAT, que es el error registrado en
  # el rector §11 v1.28.0.
  if systemctl is-active --quiet nas-ddns.timer 2>/dev/null; then
    si "el temporizador de DDNS está activo"
    DOM=$(sed -n 's/^DOMINIO=//p' /etc/nas/ddns.conf 2>/dev/null)
    if [ -n "$DOM" ]; then
      IP_VE_INTERNET=$(curl -4 -fsS --max-time 10 https://ifconfig.me 2>/dev/null || echo "")
      IP_DEL_NOMBRE=$(getent ahostsv4 "$DOM.duckdns.org" 2>/dev/null | awk 'NR==1{print $1}')
      if [ -n "$IP_VE_INTERNET" ] && [ "$IP_VE_INTERNET" = "$IP_DEL_NOMBRE" ]; then
        si "$DOM.duckdns.org resuelve a la IP pública real ($IP_DEL_NOMBRE)"
      elif [ -z "$IP_VE_INTERNET" ]; then
        dato "no se pudo medir la IP pública; el nombre resuelve a ${IP_DEL_NOMBRE:-nada}"
      else
        no "$DOM.duckdns.org apunta a ${IP_DEL_NOMBRE:-nada} y la IP real es $IP_VE_INTERNET: el acceso remoto está roto"
      fi

      # Fase 6: el router autoriza la dirección fija terminada en ::38. Si el
      # AAAA conserva una SLAAC anterior, el cliente llama a otro destino y la
      # captura del nodo autorizado queda en cero.
      IPV6_FIJA=$(ip -6 -o addr show dev eth0 scope global 2>/dev/null \
        | awk '$4 ~ /::38\/64$/ {sub(/\/.*/, "", $4); print $4; exit}')
      IPV6_DEL_NOMBRE=$(getent ahostsv6 "$DOM.duckdns.org" 2>/dev/null | awk 'NR==1{print $1}')
      if [ -n "$IPV6_FIJA" ] && [ "$IPV6_FIJA" = "$IPV6_DEL_NOMBRE" ]; then
        si "$DOM.duckdns.org publica la IPv6 fija autorizada en el router ($IPV6_FIJA)"
      elif [ -z "$IPV6_FIJA" ]; then
        no "el nodo no tiene la IPv6 fija ::38 que debe autorizar el router"
      else
        no "$DOM.duckdns.org publica ${IPV6_DEL_NOMBRE:-ningún AAAA}, pero el router autoriza $IPV6_FIJA"
      fi
    fi
  else
    no "el temporizador de DDNS no está activo: cuando cambie la IP, el acceso morirá en silencio"
  fi
fi

echo
echo "-- Fase 6: TLS y web expuesta (ADR-0046, ADR-0048) --"
CERT=/etc/nas/tls/fullchain.pem
if [ ! -f "$CERT" ]; then
  dato "sin certificado: la web va solo por HTTP en la LAN (correcto antes de la Fase 6)"
else
  # LO QUE MÁS IMPORTA Y LO QUE FALLA EN SILENCIO. Un certificado caducado
  # deja la web inaccesible desde fuera sin que nada avise, y la renovación
  # ocurre sola: si se rompe, nadie se entera hasta el día 90.
  FIN=$(openssl x509 -in "$CERT" -noout -enddate 2>/dev/null | cut -d= -f2)
  if [ -n "$FIN" ]; then
    DIAS=$(( ( $(date -d "$FIN" +%s) - $(date +%s) ) / 86400 ))
    if [ "$DIAS" -gt 21 ]; then
      si "certificado válido, le quedan $DIAS días"
    elif [ "$DIAS" -gt 0 ]; then
      no "al certificado le quedan $DIAS días y la renovación debería haber ocurrido ya: revise certbot.timer"
    else
      no "CERTIFICADO CADUCADO: la web no es accesible desde fuera"
    fi
  else
    no "no se pudo leer la fecha del certificado $CERT"
  fi

  if systemctl is-active --quiet certbot.timer 2>/dev/null; then
    si "la renovación automática está activa (certbot.timer)"
  else
    no "certbot.timer parado: el certificado caducará sin avisar"
  fi

  # El gancho que repone la copia que lee nasd. Sin él, certbot renovaría en
  # /etc/letsencrypt y nasd seguiría sirviendo el viejo hasta caducar — un
  # fallo silencioso a 90 días vista.
  if [ -x /etc/letsencrypt/renewal-hooks/deploy/nas-copiar.sh ]; then
    si "el gancho de despliegue repone el certificado que lee nasd"
  else
    no "falta el gancho de renovación: nasd se quedaría con el certificado viejo"
  fi

  if ss -tlnp 2>/dev/null | grep -q ':443 '; then
    si "nasd escucha en 443"
  else
    no "nada escucha en 443: la web expuesta no funciona"
  fi

  if nft list ruleset 2>/dev/null | grep -q 'tcp dport 443 accept'; then
    si "el cortafuegos acepta 443/tcp (ADR-0048)"
  else
    no "el cortafuegos NO acepta 443/tcp"
  fi

  # La prueba que se parece a la pregunta: no basta con que el puerto esté
  # abierto, hay que ver que el TLS negocia y que el certificado valida contra
  # el almacén del sistema. Se fuerza la resolución al nodo para no depender
  # de que la entrada desde Internet funcione (ADR-0044: hoy no funciona).
  DOM_TLS=$(sed -n 's/^DOMINIO=//p' /etc/nas/ddns.conf 2>/dev/null)
  if [ -n "$DOM_TLS" ]; then
    COD=$(curl -sS -o /dev/null -w '%{http_code}:%{ssl_verify_result}' \
      --resolve "$DOM_TLS.duckdns.org:443:127.0.0.1" \
      "https://$DOM_TLS.duckdns.org/" 2>/dev/null || echo "fallo:x")
    case "$COD" in
      401:0|200:0) si "TLS negocia y el certificado VALIDA (${COD%%:*})" ;;
      *:0)         dato "TLS válido, respuesta inesperada: ${COD%%:*}" ;;
      *)           no "TLS no negocia o el certificado no valida ($COD)" ;;
    esac
  fi
fi

echo
# ICMPv6 EN EL CORTAFUEGOS — no es diagnóstico, es obligatorio (RFC 4890).
# IPv6 no tiene ARP: el descubrimiento de vecinos y los anuncios del router SON
# ICMPv6. Con «policy drop» y sin estas reglas el nodo descarta los anuncios y
# nunca obtiene dirección IPv6, aunque el router los esté enviando bien. Se
# persiguió dos sesiones en accept_ra, en NetworkManager y en el router; la
# causa estaba en el propio cortafuegos.
if nft list ruleset 2>/dev/null | grep -q 'nd-router-advert'; then
  si "el cortafuegos admite el descubrimiento de vecinos IPv6 (RFC 4890)"
else
  no "faltan las reglas ICMPv6: el nodo NUNCA obtendrá dirección IPv6 y nadie dirá por qué"
fi

echo
echo "-- Recuperación: identidad del nodo (ADR-0050) --"

# Los scripts de aprovisionamiento reconstruyen la CONFIGURACIÓN, no la
# IDENTIDAD. Sin este respaldo, perder la placa obliga a dar de alta otra vez
# los tres dispositivos de WireGuard, uno por uno y con un QR nuevo.
RESPALDO=/srv/nas/identidad/identidad.tar.gz
if [ ! -f "$RESPALDO" ]; then
  no "no hay respaldo de la identidad: si muere la placa hay que reconfigurar cada dispositivo a mano"
elif ! tar tzf "$RESPALDO" >/dev/null 2>&1; then
  # Que exista no basta. Un tar ilegible es un respaldo que no existe y lo
  # parece hasta el día que hace falta — el mismo error que la captura sin -U.
  no "el respaldo de la identidad EXISTE pero no se puede leer: no sirve para nada"
elif ! tar tzf "$RESPALDO" 2>/dev/null | grep -q 'etc/wireguard/wg0.conf'; then
  no "el respaldo no contiene la clave del servidor WireGuard, que es lo único irreemplazable"
else
  # Comparar contra lo que dice proteger, no contra el calendario: un respaldo
  # de hace tres meses vale si la identidad no ha cambiado desde entonces.
  MAS_NUEVO=$(find /etc/wireguard /etc/nasd/credencial /etc/nas/ddns.conf \
                -newer "$RESPALDO" 2>/dev/null | head -1)
  if [ -n "$MAS_NUEVO" ]; then
    no "la identidad cambió DESPUÉS del último respaldo ($MAS_NUEVO): ejecute 17_respaldar_identidad.sh"
  else
    si "la identidad del nodo está respaldada y al día ($(stat -c %y "$RESPALDO" | cut -d' ' -f1))"
  fi
fi

if systemctl is-enabled nas-respaldo-identidad.timer >/dev/null 2>&1; then
  si "el respaldo de la identidad se repite solo: no depende de acordarse"
else
  no "el temporizador de respaldo no está habilitado: la copia envejecerá en silencio"
fi

echo
echo "-- Endurecimiento (no debe haber empeorado con la Fase 4) --"
NOTA=$(systemd-analyze security nasd --no-pager 2>/dev/null | tail -1)
dato "${NOTA:-no disponible}"

echo
echo "===== RESULTADO: $ok correctas, $mal fallidas ====="
exit "$mal"
