#!/bin/bash
# Prueba de humo de extremo a extremo de nasd sobre Linux/ext4.
B=http://127.0.0.1:8099
ok=0; mal=0
comprobar() { # descripcion esperado obtenido
  if [ "$2" = "$3" ]; then echo "  OK   $1 -> $3"; ok=$((ok+1));
  else echo "  FALLO $1 -> $3 (se esperaba $2)"; mal=$((mal+1)); fi
}

rm -rf ~/volnas; mkdir -p ~/volnas
sleep 0.2
NASD_VOLUMEN=$HOME/volnas NASD_DIRECCION=127.0.0.1 NASD_PUERTO=8099 ~/nastest/nasd > ~/nastest/log.json 2>&1 &
PID=$!
sleep 1

echo "== RF-06/RF-07 listado =="
c=$(curl -s -o /dev/null -w '%{http_code}' $B/)
comprobar "GET / " 200 "$c"

echo "== RF-13 crear directorio =="
c=$(curl -s -o /dev/null -w '%{http_code}' -X POST -d 'destino=&nombre=fotos' $B/directorio)
comprobar "POST /directorio" 303 "$c"

echo "== RF-12 subida reanudable (tus), 3 MB en dos bloques =="
head -c 3000000 /dev/urandom > /tmp/prueba.bin
split -b 1500000 /tmp/prueba.bin /tmp/trozo.
LOC=$(curl -s -D- -o /dev/null -X POST \
  -H 'Upload-Length: 3000000' -H 'Nas-Destino: fotos' -H 'Nas-Nombre: prueba.bin' \
  $B/subidas | awk 'tolower($1)=="location:"{gsub(/\r/,"");print $2}')
echo "  Location: $LOC"
c=$(curl -s -o /dev/null -w '%{http_code}' -X PATCH -H 'Upload-Offset: 0' \
  --data-binary @/tmp/trozo.aa $B$LOC)
comprobar "PATCH bloque 1" 204 "$c"

off=$(curl -s -D- -o /dev/null --head $B$LOC | awk 'tolower($1)=="upload-offset:"{gsub(/\r/,"");print $2}')
comprobar "HEAD Upload-Offset tras bloque 1" 1500000 "$off"

c=$(curl -s -o /dev/null -w '%{http_code}' -X PATCH -H "Upload-Offset: $off" \
  --data-binary @/tmp/trozo.ab $B$LOC)
comprobar "PATCH bloque 2" 204 "$c"

a=$(md5sum /tmp/prueba.bin | cut -c1-32)
b=$(md5sum ~/volnas/datos/fotos/prueba.bin 2>/dev/null | cut -c1-32)
comprobar "suma de verificacion identica" "$a" "$b"

echo "== ADR-0027 desplazamiento incoherente =="
LOC2=$(curl -s -D- -o /dev/null -X POST -H 'Upload-Length: 10' -H 'Nas-Destino: fotos' \
  -H 'Nas-Nombre: otro.bin' $B/subidas | awk 'tolower($1)=="location:"{gsub(/\r/,"");print $2}')
c=$(curl -s -o /dev/null -w '%{http_code}' -X PATCH -H 'Upload-Offset: 999' -d 'x' $B$LOC2)
comprobar "PATCH con offset mentido" 409 "$c"

echo "== RF-23 no sobrescribir =="
c=$(curl -s -o /dev/null -w '%{http_code}' -X POST -H 'Upload-Length: 10' \
  -H 'Nas-Destino: fotos' -H 'Nas-Nombre: prueba.bin' $B/subidas)
comprobar "segunda subida al mismo nombre" 409 "$c"

echo "== RNF-06 salto de ruta (CWE-22) =="
for p in '/descargar/..%2f..%2fetc%2fpasswd' '/ver/..%2f..%2fetc' '/descargar/%2e%2e%2f%2e%2e%2fetc%2fpasswd'; do
  c=$(curl -s -o /dev/null -w '%{http_code}' "$B$p")
  comprobar "GET $p" 400 "$c"
done
c=$(curl -s -o /dev/null -w '%{http_code}' --path-as-is "$B/descargar/../../etc/passwd")
echo "  info  ruta con ../ sin codificar -> $c (ServeMux la limpia y redirige; no llega al manejador)"
d=$(curl -s --path-as-is -o /dev/null -w '%{redirect_url}' "$B/descargar/../../etc/passwd")
comprobar "destino de esa redireccion" "$B/etc/passwd" "$d"

echo "== enlace simbolico fuera del volumen (capa 2, os.Root) =="
ln -sf /etc/passwd ~/volnas/datos/fuga
c=$(curl -s -o /dev/null -w '%{http_code}' $B/descargar/fuga)
comprobar "GET /descargar/fuga (enlace a /etc/passwd)" 404 "$c"

echo "== RF-10 rangos, RFC 7233 =="
c=$(curl -s -o /dev/null -w '%{http_code}' -r 0-999 $B/descargar/fotos/prueba.bin)
comprobar "peticion de rango" 206 "$c"
n=$(curl -s -r 0-999 $B/descargar/fotos/prueba.bin | wc -c)
comprobar "bytes devueltos" 1000 "$n"

echo "== 04_SEGURIDAD §4 cabeceras de descarga =="
h=$(curl -s -D- -o /dev/null $B/descargar/fotos/prueba.bin | tr -d '\r')
echo "$h" | grep -qi 'X-Content-Type-Options: nosniff' && comprobar "nosniff" si si || comprobar "nosniff" si no
echo "$h" | grep -qi 'Content-Disposition: attachment' && comprobar "attachment" si si || comprobar "attachment" si no

echo "== ADR-0029 sin basura en parciales =="
# Queda 1 esperado: la subida de otro.bin sigue abierta y es reanudable (RF-12).
n=$(ls -1 ~/volnas/estado/parciales | wc -l)
comprobar "parciales abiertos" 1 "$n"

echo "== RNF-13 registro estructurado =="
head -1 ~/nastest/log.json | python3 -c 'import json,sys; json.loads(sys.stdin.read()); print("  OK   la primera linea es JSON valido")' || { echo "  FALLO registro no es JSON"; mal=$((mal+1)); }

kill $PID 2>/dev/null
echo
echo "RESULTADO: $ok correctas, $mal fallidas"
exit $mal
