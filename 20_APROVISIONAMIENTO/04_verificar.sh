#!/bin/bash
# Fase 1, paso 4 — verificar lo hecho y MEDIR los supuestos abiertos.
#
# P2 del charter: la evidencia precede a la conclusión. Este script no
# "comprueba que todo está bien": recoge datos, incluidos los de los supuestos
# S-01 a S-04 de 03_ESTUDIO_TECNICO.md §10, que están sin verificar.
#
# RES-09: NO ejecute esto con VSCode Remote-SSH conectado. Consume 200–400 MB
# de los 592 disponibles e invalida cualquier medición.
#
# Uso: ./04_verificar.sh   (algunas comprobaciones piden sudo)

PUNTO=/srv/nas
USUARIO=nas
ok=0; mal=0; info=0

si()   { printf '  \033[32mOK\033[0m    %s\n' "$1"; ok=$((ok+1)); }
no()   { printf '  \033[31mFALLO\033[0m %s\n' "$1"; mal=$((mal+1)); }
dato() { printf '  \033[36mDATO\033[0m  %s\n' "$1"; info=$((info+1)); }

echo "===== Fase 1 · verificación ====="
echo

echo "-- RES-09: entorno de medición --"
if pgrep -f 'vscode-server' >/dev/null; then
  no "hay un servidor de VSCode corriendo: las mediciones NO son válidas"
else
  si "sin VSCode Remote-SSH conectado"
fi
dato "memoria: $(free -m | awk '/^Mem:/{print $7" MB disponibles de "$2" MB"}')"
dato "throttled: $(vcgencmd get_throttled 2>/dev/null || echo 'no disponible')"
dato "temperatura: $(vcgencmd measure_temp 2>/dev/null || echo 'no disponible')"

echo
echo "-- D-07 / RNF-10: volumen --"
if mountpoint -q "$PUNTO"; then si "$PUNTO montado"; else no "$PUNTO NO montado"; fi
if [ "$(findmnt -no FSTYPE "$PUNTO" 2>/dev/null)" = "ext4" ]; then si "es ext4 (D-07)"; else no "no es ext4"; fi
if grep -q "^UUID=" /etc/fstab; then si "fstab usa UUID, no /dev/sdX (RNF-10)"; else no "fstab NO usa UUID"; fi

echo
echo "-- ADR-0019: disposición --"
if [ -d "$PUNTO/datos" ]; then si "existe datos/"; else no "falta datos/"; fi
if [ -d "$PUNTO/estado/parciales" ]; then si "existe estado/parciales/"; else no "falta estado/parciales/"; fi
RUTA_COMPARTIDA=$(testparm -s --parameter-name=path --section-name=datos 2>/dev/null | tr -d ' ')
if [ "$RUTA_COMPARTIDA" = "$PUNTO/datos" ]; then
  si "Samba comparte datos/ y NO la raíz del volumen"
else
  no "Samba comparte '$RUTA_COMPARTIDA'; debe ser $PUNTO/datos (regla R3)"
fi

echo
echo "-- ADR-0020: propiedad --"
DUENO=$(stat -c '%U' "$PUNTO/datos")
if [ "$DUENO" = "$USUARIO" ]; then si "datos/ pertenece a $USUARIO"; else no "datos/ pertenece a $DUENO"; fi

echo
echo "-- RNF-09: SMB1 y sesiones anónimas --"
if testparm -s --parameter-name='server min protocol' 2>/dev/null | grep -qi 'SMB2'; then
  si "server min protocol = SMB2 o superior"
else
  no "SMB1 podría estar activo"
fi
# Lo que hay que comprobar NO es si el login anónimo se rechaza —Samba lo
# permite a nivel de protocolo aunque «restrict anonymous = 2»— sino si un
# cliente anónimo puede ACCEDER AL RECURSO. Eso es lo que dice RNF-09.
#
# Y si la herramienta no está, hay que decirlo: un script de verificación que
# confunde «control verificado» con «no se pudo verificar» es peor que no
# tenerlo, porque se le cree.
if ! command -v smbclient >/dev/null 2>&1; then
  no "NO SE PUDO VERIFICAR: falta smbclient. Instale: sudo apt install smbclient"
elif smbclient //localhost/datos -N -c ls 2>&1 | grep -qi 'NT_STATUS_ACCESS_DENIED\|NT_STATUS_LOGON_FAILURE\|NT_STATUS_CONNECTION_DISCONNECTED'; then
  si "un cliente anónimo NO puede acceder al recurso"
else
  no "UN CLIENTE ANÓNIMO ACCEDIÓ AL RECURSO: revise valid users / map to guest"
fi

echo
echo "-- RNF-07: cortafuegos --"
if command -v nft >/dev/null && sudo -n nft list ruleset 2>/dev/null | grep -q 'policy drop'; then
  si "política DENY por defecto"
  sudo -n nft list ruleset | grep -E 'dport (22|445|8080)' | sed 's/^/        /'
else
  dato "no se pudo leer el ruleset sin contraseña; compruebe con: sudo nft list ruleset"
fi

echo
echo "===== MEDICIONES DE SUPUESTOS (03_ESTUDIO_TECNICO.md §10) ====="

echo
echo "-- S-02: coste de fsync en este disco --"
if [ -w "$PUNTO/estado" ] && command -v python3 >/dev/null 2>&1; then
  # fsync PURO sobre un descriptor ya abierto: sin incluir open ni truncate,
  # que falsearían la cifra a la baja.
  MS=$(python3 -c '
import os, time
f = os.open("'"$PUNTO"'/estado/.fsync_prueba", os.O_CREAT | os.O_WRONLY, 0o600)
t0 = time.perf_counter()
for _ in range(30):
    os.write(f, b"x"); os.fsync(f)
print("%.1f" % ((time.perf_counter() - t0) * 1000 / 30))
os.close(f)
')
  rm -f "$PUNTO/estado/.fsync_prueba"
  dato "coste de fsync: ${MS} ms  (ADR-0024 estimaba 10-15 ms [R] — MEDIDO MUY SUPERIOR)"
else
  dato "no se pudo medir S-02 (hace falta permiso de escritura y python3)"
fi

echo
echo "-- S-03: ¿toma el Explorador de Windows bloqueos POSIX? --"
echo "     Esta es la medición que decide si la capa 3 de ADR-0028 sirve de algo."
printf '     HÁGALA A MANO: copie un archivo GRANDE desde Windows a \\\\%s\\datos\n' \
  "$(hostname -I | awk '{print $1}')"
echo "     y, MIENTRAS se copia, ejecute en otra terminal:"
echo
echo "         watch -n1 'cat /proc/locks | grep -v \"^$\"'"
echo
echo "     Si NO aparece ningún bloqueo sobre el archivo, S-03 queda confirmado:"
echo "     ninguna cerradura detectaría el conflicto, y RF-22 descansa solo en"
echo "     el rename() atómico y en no sobrescribir (RF-23)."
dato "bloqueos POSIX ahora mismo: $(wc -l < /proc/locks) líneas en /proc/locks"

echo
echo "-- S-04: versión de Samba y kernel share modes --"
dato "$(smbd --version 2>/dev/null || echo 'smbd no disponible')"
if testparm -s --parameter-name='kernel share modes' >/dev/null 2>&1; then
  dato "el parámetro 'kernel share modes' EXISTE en esta versión"
else
  si "'kernel share modes' no existe (retirado en 4.14) — coincide con ADR-0028"
fi

echo
echo "-- S-01: durabilidad de rename()+fsync --"
echo "     NO SE PUEDE AUTOMATIZAR. Es la medición más importante que queda:"
echo "     de ella depende el capítulo entero de atomicidad (ADR-0024)."
echo "     Procedimiento: iniciar una subida grande y CORTAR LA ALIMENTACIÓN"
echo "     de la Pi a mitad. Al arrancar, en datos/ debe estar el archivo"
echo "     completo o no estar. Nunca a medias."

echo
echo "===== RESULTADO: $ok correctas, $mal fallidas, $info datos ====="
exit $mal
