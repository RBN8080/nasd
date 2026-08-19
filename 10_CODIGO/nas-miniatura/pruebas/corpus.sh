#!/bin/bash
# Corpus de entradas hostiles para nas-miniatura, construido AQUI en vez de
# comprometido como archivos binarios: los archivos de prueba son unos
# cuantos printf, y no hace falta versionar binarios ilegibles en un diff
# para algo que se reconstruye en microsegundos.
#
# Uso:  bash corpus.sh <ruta-al-binario>
#
# Sale con el numero de casos fallidos (0 = todo bien), estilo
# pruebas/humo.sh. Un "fallo" aqui es CUALQUIER cosa que no sea "el programa
# termino con 0, 1 o 2" -- una senal (segfault, abort de un desinfectante),
# un cuelgue, o texto de ASan/UBSan en la salida. NINGUN caso de este
# corpus tiene una miniatura de verdad que extraer: el resultado correcto
# de todos ellos es "no revientes", no "acierta el contenido".
set -u
BIN="${1:?uso: corpus.sh <ruta-al-binario>}"
DIR=$(mktemp -d)
trap 'rm -rf "$DIR"' EXIT

ok=0; mal=0

# EL BINARIO RECIEN COMPILADO NO SIEMPRE SE PUEDE EJECUTAR TODAVIA (Windows).
#
# Reproducido el 2026-08-19: de doce vueltas seguidas a «make verificar-c»,
# tres fallaron ENTERAS con codigo 126 -«existe pero no es ejecutable»- en
# todos los casos a la vez, y las otras nueve pasaron limpias. No es el codigo:
# es que el antivirus abre el .exe en cuanto zig lo escribe y lo retiene unas
# decimas. En este mismo arbol hay otra prueba del mismo bloqueo -Go deja un
# «.exe~» cuando no puede reemplazar el binario en uso-.
#
# IMPORTA porque «desplegar» depende de «verificar»: una puerta que se niega al
# azar ensena a repetirla hasta que pase, y asi es como un fallo de verdad
# acaba colandose. El fallo era seguro -126 nunca aprueba nada- pero el habito
# que crea no lo es.
#
# Se espera a que ARRANQUE, no un «sleep» fijo: sin argumentos el arnes sale
# con error de uso, y eso ya demuestra que el sistema lo deja correr.
esperar_ejecutable() {
  local intento
  for intento in 1 2 3 4 5 6 7 8 9 10; do
    "$BIN" >/dev/null 2>&1
    [ $? != 126 ] && return 0
    sleep 0.3
  done
  echo "FALLO: $BIN sigue sin poder ejecutarse (codigo 126) tras 3 segundos." >&2
  exit 1
}
esperar_ejecutable
comprobar() { # nombre archivo
  local nombre="$1" archivo="$2"
  local salida
  salida=$("$BIN" "$archivo" "$DIR/salida.jpg" 2>&1)
  local ec=$?
  if [ "$ec" != 0 ] && [ "$ec" != 1 ] && [ "$ec" != 2 ]; then
    echo "  FALLO $nombre -> codigo de salida $ec (se esperaba 0, 1 o 2)"
    mal=$((mal+1)); return
  fi
  if echo "$salida" | grep -qE 'ERROR|runtime error|SUMMARY: (Address|Undefined)Sanitizer'; then
    echo "  FALLO $nombre -> el desinfectante disparo:"
    echo "$salida" | sed 's/^/         /'
    mal=$((mal+1)); return
  fi
  echo "  OK    $nombre (salida=$ec)"
  ok=$((ok+1))
}

# 1. Vacio.
: > "$DIR/vacio.jpg"
comprobar "archivo vacio" "$DIR/vacio.jpg"

# 2. Solo el marcador SOI, nada mas.
printf '\xff\xd8' > "$DIR/solo_soi.jpg"
comprobar "solo SOI" "$DIR/solo_soi.jpg"

# 3. SOI seguido de basura sin forma de segmento.
printf '\xff\xd8\x00\x01\x02\x03' > "$DIR/basura_tras_soi.jpg"
comprobar "basura tras SOI" "$DIR/basura_tras_soi.jpg"

# 4. No es un JPEG en absoluto.
printf 'esto no es un jpeg, es texto plano' > "$DIR/no_es_jpeg.txt"
comprobar "no es JPEG" "$DIR/no_es_jpeg.txt"

# 5. APP1/Exif con longitud de segmento ilegal (<2).
printf '\xff\xd8\xff\xe1\x00\x00' > "$DIR/app1_longitud_cero.jpg"
comprobar "APP1 longitud cero" "$DIR/app1_longitud_cero.jpg"

# 6. APP1/Exif que declara una longitud mayor que el archivo entero.
printf '\xff\xd8\xff\xe1\xff\xffExif\x00\x00' > "$DIR/app1_longitud_excesiva.jpg"
comprobar "APP1 longitud excesiva" "$DIR/app1_longitud_excesiva.jpg"

# 7. Desplazamiento a IFD0 absurdamente grande (0xFFFFFFFF).
printf '\xff\xd8\xff\xe1\x00\x10Exif\x00\x00II\x2a\x00\xff\xff\xff\xff' > "$DIR/ifd0_offset_absurdo.jpg"
comprobar "offset de IFD0 absurdo" "$DIR/ifd0_offset_absurdo.jpg"

# 8. Marca de orden de bytes que no es ni "II" ni "MM".
printf '\xff\xd8\xff\xe1\x00\x10Exif\x00\x00XX\x2a\x00\x08\x00\x00\x00' > "$DIR/byte_order_invalido.jpg"
comprobar "orden de bytes invalido" "$DIR/byte_order_invalido.jpg"

# 9. EL CASO CLAVE: un IFD cuyo "siguiente IFD" apunta A SI MISMO. Prueba
# que la regla "la cadena de IFDs no se sigue de forma dinamica" (miniatura.c
# regla de seguridad #3) de verdad impide un bucle, y no solo lo supone.
printf '\xff\xd8\xff\xe1\x00\x16Exif\x00\x00II\x2a\x00\x08\x00\x00\x00\x00\x00\x08\x00\x00\x00' \
  > "$DIR/ifd_autorreferencia.jpg"
comprobar "IFD que se apunta a si mismo" "$DIR/ifd_autorreferencia.jpg"

# 10. Un IFD que declara 65535 entradas cuando el archivo no tiene datos ni
# para una decima parte. Prueba MAX_ENTRADAS_IFD y que cada lectura dentro
# del bucle siga acotada, no solo la primera.
{
  printf '\xff\xd8\xff\xe1\x00\x26Exif\x00\x00II\x2a\x00\x08\x00\x00\x00\xff\xff'
  for _ in $(seq 1 20); do printf '\x00'; done
} > "$DIR/ifd_entradas_gigantes.jpg"
comprobar "IFD con 65535 entradas declaradas" "$DIR/ifd_entradas_gigantes.jpg"

# 11. Puntero de miniatura que, sumado a su longitud, desborda -- prueba que
# la suma se hace en uint64_t y no en un tipo mas estrecho.
printf '\xff\xd8\xff\xe1\x00\x1cExif\x00\x00II\x2a\x00\x08\x00\x00\x00\x01\x00\x01\x02\x00\x04\x00\x00\x00\x01\xff\xff\xff\xff\x00\x00\x00\x00' \
  > "$DIR/offset_miniatura_desbordado.jpg"
comprobar "offset de miniatura desbordado" "$DIR/offset_miniatura_desbordado.jpg"

echo
echo "RESULTADO: $ok correctas, $mal fallidas"
exit "$mal"
