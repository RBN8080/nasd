#!/bin/bash
# Fase 2, paso 2 — verificar el servicio web CONTRA EL NODO REAL.
#
# Lo que se probó en Linux de escritorio no vale aquí: RNF-01 existe por los
# 592 MB del nodo (RES-01), y solo se demuestra en el nodo.
#
# RES-09: no ejecutar con VSCode Remote-SSH conectado.
#
# Uso: sudo ./06_verificar_web.sh

set -uo pipefail

# ADR-0018: el servicio se enlaza a la IP de la LAN, NO a 0.0.0.0 ni a
# localhost. Apuntar a 127.0.0.1 aquí hacía que TODOS los curl devolvieran
# 000 —sin conectar— y el script lo interpretaba como fallos del servicio.
BASE="http://$(hostname -I | awk '{print $1}'):8080"
PUNTO=/srv/nas
TRABAJO=/srv/nas/estado/.verificacion
ok=0; mal=0

si()   { printf '  \033[32mOK\033[0m    %s\n' "$1"; ok=$((ok+1)); }
no()   { printf '  \033[31mFALLO\033[0m %s\n' "$1"; mal=$((mal+1)); }
dato() { printf '  \033[36mDATO\033[0m  %s\n' "$1"; }

[ "$(id -u)" -eq 0 ] || { echo "Ejecute con sudo."; exit 1; }
systemctl is-active --quiet nasd || { echo "nasd no está activo."; exit 1; }

PID=$(systemctl show nasd -p MainPID --value)
rss() { awk '/VmRSS/{print $2}' "/proc/$PID/status" 2>/dev/null || echo 0; }

limpiar() {
  rm -rf "$TRABAJO"
  find "$PUNTO/datos" -mindepth 1 -name 'v_*' -exec rm -rf {} + 2>/dev/null
  find "$PUNTO/estado/parciales" -mindepth 1 -delete 2>/dev/null
}
trap limpiar EXIT
limpiar
mkdir -p "$TRABAJO"

echo "===== Fase 2 · verificación contra el nodo ====="
echo
dato "RSS en reposo: $(( $(rss) / 1024 )) MB   (RNF-02)"
echo

# Material de prueba: 10 MB aleatorios que se concatenan. Evita /dev/zero,
# donde un fallo que escribiera ceros pasaría inadvertido.
head -c 10485760 /dev/urandom > "$TRABAJO/base"

echo "-- RNF-01: el pico de memoria NO debe crecer con el tamaño del archivo --"
echo "   (criterio vinculante de 01_REQUISITOS; la cifra es solo referencia)"
PICOS=""
SUBIDAS_OK=0
for MB in 100 400 800; do
  N=$((MB / 10))
  for _ in $(seq 1 $N); do cat "$TRABAJO/base"; done > "$TRABAJO/f_$MB"
  ORIG=$(md5sum "$TRABAJO/f_$MB" | cut -c1-32)

  # Muestrea el RSS cada 0.2 s mientras dura la subida.
  PICO=0
  ( while :; do rss; sleep 0.2; done > "$TRABAJO/rss_$MB" ) &
  VIGIA=$!

  T0=$(date +%s.%N)
  COD=$(curl -s -o /dev/null -w '%{http_code}' -X POST \
        -F "destino=" -F "archivo=@$TRABAJO/f_$MB;filename=v_$MB.bin" "$BASE/subir")
  T1=$(date +%s.%N)
  kill $VIGIA 2>/dev/null; wait $VIGIA 2>/dev/null

  PICO=$(sort -n "$TRABAJO/rss_$MB" | tail -1)
  PICO_MB=$((PICO / 1024))
  SEG=$(awk -v a="$T0" -v b="$T1" 'BEGIN{printf "%.1f", b-a}')
  VEL=$(awk -v m="$MB" -v s="$SEG" 'BEGIN{printf "%.1f", m/s}')

  DEST=$(md5sum "$PUNTO/datos/v_$MB.bin" 2>/dev/null | cut -c1-32)
  if [ "$ORIG" = "$DEST" ]; then
    dato "${MB} MB → HTTP $COD · pico RSS ${PICO_MB} MB · ${SEG}s · ${VEL} MB/s · suma OK"
    SUBIDAS_OK=$((SUBIDAS_OK+1))
  else
    no "${MB} MB: la suma de verificación NO coincide (HTTP $COD)"
  fi
  PICOS="$PICOS $PICO_MB"
  rm -f "$TRABAJO/f_$MB"
done

# El criterio: el pico del archivo grande no puede ser proporcional al tamaño.
P1=$(echo "$PICOS" | awk '{print $1}')
P3=$(echo "$PICOS" | awk '{print $3}')
echo
# Sin subidas correctas NO hay nada que concluir. Dar OK aquí seria decir
# «verificado» cuando en realidad es «no se pudo verificar», que es el peor
# defecto posible en un verificador: se le cree.
if [ "$SUBIDAS_OK" -lt 3 ]; then
  no "RNF-01 NO SE PUDO VERIFICAR: solo $SUBIDAS_OK de 3 subidas funcionaron"
elif [ "$P3" -lt $((P1 * 2)) ]; then
  si "el pico NO crece con el tamaño: ${P1} MB con 100 MB, ${P3} MB con 800 MB"
else
  no "el pico CRECE con el tamaño (${P1} → ${P3} MB): hay buffer completo"
fi

echo
echo "-- RF-23: no sobrescribir sin decisión explícita --"
COD=$(curl -s -o /dev/null -w '%{http_code}' -X POST \
      -F "destino=" -F "archivo=@$TRABAJO/base;filename=v_100.bin" "$BASE/subir")
if [ "$COD" = "409" ]; then si "segunda subida al mismo nombre → 409"; else no "se esperaba 409, llegó $COD"; fi

echo
echo "-- RF-09 y RF-10: descarga completa y por rangos --"
COD=$(curl -s -o /dev/null -w '%{http_code}' -r 0-999 "$BASE/descargar/v_100.bin")
N=$(curl -s -r 0-999 "$BASE/descargar/v_100.bin" | wc -c)
if [ "$COD" = "206" ] && [ "$N" = "1000" ]; then si "rango → 206 con 1000 bytes exactos"; else no "rango: HTTP $COD, $N bytes"; fi

echo
echo "-- RNF-06: contención de rutas (CWE-22) --"
FUGAS=0
for P in '..%2f..%2fetc%2fpasswd' '%2e%2e%2f%2e%2e%2fetc%2fpasswd' '..%5c..%5cetc'; do
  C=$(curl -s -o /dev/null -w '%{http_code}' "$BASE/descargar/$P")
  [ "$C" = "400" ] || { FUGAS=$((FUGAS+1)); dato "  $P devolvió $C"; }
done
if [ "$FUGAS" = "0" ]; then si "las tres formas de salto de ruta rechazadas con 400"; else no "$FUGAS variantes no devolvieron 400"; fi

echo
echo "-- RNF-04: listado de directorios grandes --"
mkdir -p "$PUNTO/datos/v_muchos"
for i in $(seq 1 3000); do : > "$PUNTO/datos/v_muchos/a_$i.txt"; done
chown -R nas:nas "$PUNTO/datos/v_muchos"
L0=$(date +%s.%N)
CL=$(curl -s -o /dev/null -w '%{http_code}' "$BASE/ver/v_muchos")
L1=$(date +%s.%N)
T=$(awk -v a="$L0" -v b="$L1" 'BEGIN{printf "%.2f", b-a}')
if [ "$CL" = "200" ]; then
  si "listado de 3000 entradas servido en ${T}s (RNF-04)"
else
  no "el listado de 3000 entradas devolvió $CL"
fi
RSS_POST=$(( $(rss) / 1024 ))
dato "RSS tras el listado grande: ${RSS_POST} MB"

echo
echo "-- RNF-05 / ADR-0029: sin residuos --"
N=$(find "$PUNTO/estado/parciales" -mindepth 1 2>/dev/null | wc -l)
if [ "$N" = "0" ]; then si "estado/parciales/ vacío tras todas las subidas"; else no "quedaron $N residuos"; fi

echo
echo "-- ADR-0020: propiedad de lo escrito por la web --"
N=$(find "$PUNTO/datos" -name 'v_*' ! -user nas 2>/dev/null | wc -l)
if [ "$N" = "0" ]; then si "todo lo escrito por la web pertenece a nas"; else no "$N archivos con otro dueño"; fi

echo
echo "-- RNF-11: control térmico --"
# RNF-11 tiene criterio propio: NO deben encenderse bits nuevos respecto a
# 0x80000 [M]. Imprimirlo como dato y no comprobarlo era dejar pasar un
# incumplimiento con un «8 de 8» en pantalla.
#
#   bit 19 (0x80000) — el límite blando YA se alcanzó alguna vez (estado de
#                      partida documentado en el charter §10.3)
#   bit  3 (0x8)     — el límite blando está ACTIVO AHORA: se está degradando
TH=$(vcgencmd get_throttled 2>/dev/null | cut -d= -f2)
TEMP=$(vcgencmd measure_temp 2>/dev/null | cut -d= -f2)
dato "throttled=$TH · temp=$TEMP"
if [ -z "$TH" ]; then
  no "RNF-11 NO SE PUDO VERIFICAR: vcgencmd no disponible"
elif [ "$TH" = "0x80000" ]; then
  si "RNF-11: sin bits nuevos respecto a 0x80000"
else
  no "RNF-11 INCUMPLIDO: throttled=$TH, hay bits nuevos sobre 0x80000 (a $TEMP)"
  dato "  el nodo está degradando por temperatura bajo esta carga"
  dato "  charter §10.3 ya listaba el disipador como riesgo abierto"
fi

echo
echo "-- RNF-14: el servicio sigue en pie --"
if systemctl is-active --quiet nasd; then si "nasd activo tras toda la carga"; else no "nasd se cayó"; fi
dato "RSS final: $(( $(rss) / 1024 )) MB · memoria del nodo: $(free -m | awk '/^Mem:/{print $7" MB disponibles"}')"

echo
echo "===== RESULTADO: $ok correctas, $mal fallidas ====="
exit "$mal"
