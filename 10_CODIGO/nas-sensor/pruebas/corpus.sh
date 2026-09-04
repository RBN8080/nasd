#!/bin/bash
# Corpus de paquetes hostiles para nas-sensor, construido AQUI con printf en
# vez de comprometido como binarios: son unas decenas de bytes cada uno y no
# hace falta versionar archivos ilegibles en un diff para algo que se
# reconstruye en microsegundos. Mismo criterio que el corpus de nas-miniatura.
#
# Uso:  bash corpus.sh <ruta-al-arnes>
#
# Sale con el numero de casos fallidos (0 = todo bien).
#
# HAY DOS CLASES DE COMPROBACION, Y LA SEGUNDA ES LA QUE DE VERDAD IMPORTA:
#
#   comprobar  el programa no revienta -- ni senal, ni cuelgue, ni texto de un
#              desinfectante. Es lo que cubre la entrada malformada.
#   esperar    ademas, el veredicto es EXACTAMENTE el que debe ser. Sin esto,
#              un analisis que devolviera «no es un toque» siempre pasaria el
#              corpus entero sin analizar nada.
#
# VARIOS CASOS DE «esperar» LOS PIDIO LA PRUEBA DE MUTACION, no la imaginacion:
# se rompio a proposito cada comprobacion de analisis.c y se anadio un caso por
# cada rotura que el corpus dejaba pasar. Ver ADR-0066.
set -u
BIN="${1:?uso: corpus.sh <ruta-al-arnes>}"
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

# --- Trozos reutilizables --------------------------------------------------
# Cabecera IPv4 de 20 bytes, protocolo TCP (06), origen 192.168.1.23.
IP4_TCP='\x45\x00\x00\x3c\x1c\x46\x40\x00\x40\x06\x00\x00\xc0\xa8\x01\x17\xcb\x00\x71\x07'
# La misma con protocolo ICMP (01).
IP4_ICMP='\x45\x00\x00\x3c\x1c\x46\x40\x00\x40\x01\x00\x00\xc0\xa8\x01\x17\xcb\x00\x71\x07'
# Las dos direcciones de una cabecera IPv6. Origen 2a06:4883:5000::65.
IP6_SRC='\x2a\x06\x48\x83\x50\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x65'
IP6_DST='\x28\x06\x02\xa0\x10\x1e\x3d\x82\x00\x00\x00\x00\x00\x00\x00\x38'
# TCP de 20 bytes al puerto 80. El byte 13 son las banderas.
TCP_SYN='\xd4\x31\x00\x50\x00\x00\x00\x00\x00\x00\x00\x00\x50\x02\x72\x10\x00\x00\x00\x00'
TCP_SYNACK='\xd4\x31\x00\x50\x00\x00\x00\x00\x00\x00\x00\x00\x50\x12\x72\x10\x00\x00\x00\x00'
TCP_ACK='\xd4\x31\x00\x50\x00\x00\x00\x00\x00\x00\x00\x00\x50\x10\x72\x10\x00\x00\x00\x00'

# ip6 <siguiente-encabezado> -- cabecera IPv6 de 40 bytes completa.
ip6() { printf "\x60\x00\x00\x00\x00\x14$1\x40${IP6_SRC}${IP6_DST}"; }

sano() { # nombre salida codigo
  local nombre="$1" salida="$2" ec="$3"
  if [ "$ec" != 0 ] && [ "$ec" != 1 ] && [ "$ec" != 2 ]; then
    echo "  FALLO $nombre -> codigo de salida $ec (se esperaba 0, 1 o 2)"
    return 1
  fi
  if echo "$salida" | grep -qE 'runtime error|SUMMARY: (Address|Undefined)Sanitizer'; then
    echo "  FALLO $nombre -> el desinfectante disparo:"
    echo "$salida" | sed 's/^/         /'
    return 1
  fi
  return 0
}

comprobar() { # nombre archivo familia
  local salida ec
  salida=$("$BIN" "$2" "$3" 2>&1); ec=$?
  if sano "$1" "$salida" "$ec"; then
    echo "  OK    $1 (salida=$ec)"
    ok=$((ok+1))
  else
    mal=$((mal+1))
  fi
}

esperar() { # nombre archivo familia texto-esperado
  local salida ec
  salida=$("$BIN" "$2" "$3" 2>&1); ec=$?
  if ! sano "$1" "$salida" "$ec"; then
    mal=$((mal+1)); return
  fi
  if [ "$salida" != "$4" ]; then
    echo "  FALLO $1"
    echo "          dijo        «$salida»"
    echo "          se esperaba «$4»"
    mal=$((mal+1)); return
  fi
  echo "  OK    $1"
  ok=$((ok+1))
}

echo "== Lo que SI es un toque =="
printf "${IP4_TCP}${TCP_SYN}" > "$DIR/syn4"
esperar "IPv4 SYN a un puerto cerrado" "$DIR/syn4" 4 "origen=192.168.1.23 puerto=80 tipo=syn"

printf "${IP4_ICMP}\x08\x00\x00\x00\x00\x00\x00\x00" > "$DIR/ping4"
esperar "IPv4 echo request" "$DIR/ping4" 4 "origen=192.168.1.23 puerto=0 tipo=ping"

ip6 '\x06' > "$DIR/syn6"; printf "${TCP_SYN}" >> "$DIR/syn6"
esperar "IPv6 SYN" "$DIR/syn6" 6 "origen=2a06:4883:5000:0:0:0:0:65 puerto=80 tipo=syn"

ip6 '\x3a' > "$DIR/ping6"; printf '\x80\x00\x00\x00\x00\x00\x00\x00' >> "$DIR/ping6"
esperar "IPv6 echo request" "$DIR/ping6" 6 "origen=2a06:4883:5000:0:0:0:0:65 puerto=0 tipo=ping"

echo
echo "== Lo que NO es un toque, y es la mitad que impide un falso positivo =="
printf "${IP4_TCP}${TCP_SYNACK}" > "$DIR/synack4"
esperar "SYN+ACK (respuesta a algo NUESTRO)" "$DIR/synack4" 4 "no-es-un-toque"

printf "${IP4_TCP}${TCP_ACK}" > "$DIR/ack4"
esperar "ACK suelto (trafico ya establecido)" "$DIR/ack4" 4 "no-es-un-toque"

printf "${IP4_ICMP}\x00\x00\x00\x00\x00\x00\x00\x00" > "$DIR/pong4"
esperar "IPv4 echo REPLY" "$DIR/pong4" 4 "no-es-un-toque"

# ICMPv6 QUE NO ES UN ECHO REQUEST. Estos dos importan mas de lo que parece: el
# descubrimiento de vecinos IPv6 es CONSTANTE en la LAN -- medido el 18/08 --, y
# si se dejara de mirar el tipo, cada anuncio de vecino de la casa se anotaria
# como un toque y el anillo se llenaria de ruido domestico.
ip6 '\x3a' > "$DIR/pong6"; printf '\x81\x00\x00\x00\x00\x00\x00\x00' >> "$DIR/pong6"
esperar "ICMPv6 echo REPLY" "$DIR/pong6" 6 "no-es-un-toque"

ip6 '\x3a' > "$DIR/vecino6"; printf '\x87\x00\x00\x00\x00\x00\x00\x00' >> "$DIR/vecino6"
esperar "ICMPv6 solicitud de vecino" "$DIR/vecino6" 6 "no-es-un-toque"

# Un fragmento que no es el primero no lleva cabecera de transporte: leer ahi
# las banderas seria interpretar datos como si fueran cabecera.
printf '\x45\x00\x00\x3c\x1c\x46\x00\x01\x40\x06\x00\x00\xc0\xa8\x01\x17\xcb\x00\x71\x07' > "$DIR/frag4"
printf "${TCP_SYN}" >> "$DIR/frag4"
esperar "fragmento con pinta de SYN" "$DIR/frag4" 4 "no-es-un-toque"

# Cabecera de extension: la cadena no se sigue a proposito. Perdida conocida.
ip6 '\x00' > "$DIR/ext6"; printf "${TCP_SYN}" >> "$DIR/ext6"
esperar "IPv6 con cabecera de extension" "$DIR/ext6" 6 "no-es-un-toque"

# EL NIBBLE DE VERSION, AISLADO. Aqui la IHL y todo lo demas son VALIDOS y lo
# unico malo es la version: si se deja de mirar, esto se anota como un toque.
# Los casos de basura de mas abajo NO lo cubrian -- morian antes por la IHL.
printf '\x55\x00\x00\x3c\x1c\x46\x40\x00\x40\x06\x00\x00\xc0\xa8\x01\x17\xcb\x00\x71\x07' > "$DIR/v5"
printf "${TCP_SYN}" >> "$DIR/v5"
esperar "IPv4 que dice ser version 5" "$DIR/v5" 4 "no-es-un-toque"

printf '\x50\x00\x00\x00\x00\x14\x06\x40' > "$DIR/v5_6"
printf "${IP6_SRC}${IP6_DST}${TCP_SYN}" >> "$DIR/v5_6"
esperar "IPv6 que dice ser version 5" "$DIR/v5_6" 6 "no-es-un-toque"

# La familia la pone el NUCLEO, no el paquete: un IPv4 anunciado como IPv6
# tiene que rechazarse por el nibble de version, no analizarse a medias.
printf "${IP4_TCP}${TCP_SYN}" > "$DIR/mentira"
esperar "IPv4 anunciado como IPv6" "$DIR/mentira" 6 "no-es-un-toque"

# UNA IHL ILEGAL QUE AUN ASI ALINEA UN FALSO SYN, y es el caso mas retorcido del
# corpus a proposito. La IHL dice 4 (16 bytes, por debajo del minimo de 20), y el
# paquete esta construido para que, si se aceptara esa longitud, la lectura
# desplazada cayera sobre un 0x02 -- es decir, el analisis INVENTARIA un SYN al
# puerto 28935, que no existe en ningun sitio del paquete.
#
# Lo pidio la prueba de mutacion: sin este caso se podia quitar la cota inferior
# de la IHL y el corpus entero seguia en verde, porque los demas paquetes cortos
# morian antes en u8_en. Un dato inventado es peor que un dato perdido.
printf '\x44\x00\x00\x3c\x1c\x46\x40\x00\x40\x06\x00\x00\xc0\xa8\x01\x17\xcb\x00\x71\x07' > "$DIR/ihl4"
printf '\xd4\x31\x00\x50\x00\x00\x00\x00\x00\x02\x00\x00\x50\x02\x72\x10\x00\x00\x00\x00' >> "$DIR/ihl4"
esperar "IHL ilegal que alinearia un falso SYN" "$DIR/ihl4" 4 "no-es-un-toque"

echo
echo "== Entrada malformada: aqui solo se exige NO REVENTAR =="
: > "$DIR/vacio"
comprobar "archivo vacio" "$DIR/vacio" 4
comprobar "archivo vacio, como IPv6" "$DIR/vacio" 6

printf '\x45' > "$DIR/un_byte"
comprobar "un solo byte" "$DIR/un_byte" 4

# IHL = 4 -> 16 bytes, por debajo del minimo del protocolo.
printf '\x44\x00\x00\x3c\x1c\x46\x40\x00\x40\x06\x00\x00\xc0\xa8\x01\x17' > "$DIR/ihl_corta"
comprobar "IPv4 con IHL menor que 5" "$DIR/ihl_corta" 4

# IHL = 15 -> 60 bytes declarados sobre un paquete de 20.
printf '\x4f\x00\x00\x3c\x1c\x46\x40\x00\x40\x06\x00\x00\xc0\xa8\x01\x17\xcb\x00\x71\x07' > "$DIR/ihl_larga"
comprobar "IPv4 con IHL mayor que el paquete" "$DIR/ihl_larga" 4

# Cabecera IP completa y TCP cortado a la mitad.
printf "${IP4_TCP}\xd4\x31\x00\x50\x00\x00" > "$DIR/tcp_corto"
comprobar "IPv4 con TCP truncado" "$DIR/tcp_corto" 4

printf '\x00\x00\x00\x00' > "$DIR/version_cero"
comprobar "nibble de version invalido" "$DIR/version_cero" 4
comprobar "nibble de version invalido, IPv6" "$DIR/version_cero" 6

ip6 '\x06' > "$DIR/ip6_solo"
comprobar "IPv6 sin nada detras" "$DIR/ip6_solo" 6

printf '\x60\x00\x00\x00\x00\x14\x06\x40\x2a\x06' > "$DIR/ip6_corta"
comprobar "IPv6 truncada a la mitad" "$DIR/ip6_corta" 6

printf 'esto no es un paquete, es texto plano y encima largo' > "$DIR/texto"
comprobar "texto plano" "$DIR/texto" 4
comprobar "texto plano, como IPv6" "$DIR/texto" 6

echo
echo "== El historial que el sensor relee al arrancar =="
# ESTA SECCION EXISTE POR UN DEFECTO MEDIDO, no por completitud: el anillo del
# sensor vivia SOLO en memoria, asi que el primer volcado tras arrancar pisaba
# el archivo con uno vacio -- en cada reinicio del nodo Y en cada despliegue.
# Al releerlo, estas lineas pasan a ser bytes que hay que analizar con la misma
# desconfianza que un paquete: un corte de luz pudo dejar el archivo a medias.
#
# LOS INSTANTES ESPERADOS SE CALCULARON CON date(1), NO con este codigo. Si
# salieran de la propia implementacion, la prueba solo diria que hace lo que
# hace.   date -u -d 2026-09-04T03:36:51Z +%s   ->   1788493011

printf '%s\n' \
  '# Historial de toques -- anillo de 2000, del mas antiguo al mas reciente.' \
  '# Un toque por linea: instante origen puerto tipo.' \
  '# total-visto: 62' \
  '2026-09-04T03:36:51Z 192.168.1.18 80 syn' \
  '2026-08-31T22:26:07Z 2a06:4884:1000::5 443 syn' > "$DIR/hist_ok"
esperar "historial completo" "--historial" "$DIR/hist_ok" \
"toque momento=1788493011 origen=192.168.1.18 puerto=80 tipo=syn
toque momento=1788215167 origen=2a06:4884:1000::5 puerto=443 tipo=syn
total=62 leidos=2"

# LA EPOCA, que es donde se rompe una cuenta de dias mal escrita: 1970-01-01
# tiene que dar exactamente 0. Sin este caso, un desfase constante de un dia
# pasaria desapercibido en todos los demas, que solo se comparan consigo mismos.
printf '%s\n' '1970-01-01T00:00:00Z 10.0.0.1 22 ping' > "$DIR/hist_epoca"
esperar "la epoca da cero" "--historial" "$DIR/hist_epoca" \
"toque momento=0 origen=10.0.0.1 puerto=22 tipo=ping
total=0 leidos=1"

printf '%s\n' '# total-visto: 62' > "$DIR/hist_solo_cab"
esperar "solo cabeceras" "--historial" "$DIR/hist_solo_cab" "total=62 leidos=0"

# UN CORTE DE LUZ A MEDIA LINEA. Lo de arriba se conserva y lo cortado se tira:
# es la diferencia entre perder una linea y perder el archivo entero, que es lo
# que veniamos haciendo en cada arranque.
printf '%s\n%s' '2026-09-04T03:36:51Z 192.168.1.18 80 syn' \
  '2026-09-04T03:37:0' > "$DIR/hist_cortado"
esperar "cortado a media linea" "--historial" "$DIR/hist_cortado" \
"toque momento=1788493011 origen=192.168.1.18 puerto=80 tipo=syn
total=0 leidos=1"

# UNA FECHA IMPOSIBLE SE RECHAZA, no se convierte. Febrero no tiene 31 dias, y
# un instante inventado se pintaria en el panel como si fuera cierto.
printf '%s\n' '2026-02-31T10:00:00Z 10.0.0.1 80 syn' > "$DIR/hist_feb31"
esperar "31 de febrero" "--historial" "$DIR/hist_feb31" "total=0 leidos=0"

printf '%s\n' '2026-09-04T03:36:51Z 10.0.0.1 70000 syn' > "$DIR/hist_puerto"
esperar "puerto fuera de rango" "--historial" "$DIR/hist_puerto" "total=0 leidos=0"

# Una direccion mas larga de lo que cabe NO se recorta: media direccion es un
# dato falso. Se descarta la linea entera.
printf '%s\n' "2026-09-04T03:36:51Z $(printf 'a%.0s' $(seq 1 50)) 80 syn" > "$DIR/hist_largo"
esperar "origen mas largo que el campo" "--historial" "$DIR/hist_largo" "total=0 leidos=0"

# La T y la Z son separadores, no adorno: si se dejaran de mirar, cualquier
# linea con numeros en los sitios correctos entraria como un toque.
printf '%s\n' '2026-09-04 03:36:51Z 10.0.0.1 80 syn' > "$DIR/hist_sinT"
esperar "instante sin la T" "--historial" "$DIR/hist_sinT" "total=0 leidos=0"

# Una cabecera ilegible no puede inventar una cuenta: mejor perder el total que
# publicar uno falso. Las lineas de debajo se siguen leyendo igual.
printf '%s\n' '# total-visto: 99999999999999999999999999' \
  '2026-09-04T03:36:51Z 10.0.0.1 80 syn' > "$DIR/hist_total_roto"
esperar "total-visto que no cabe" "--historial" "$DIR/hist_total_roto" \
"toque momento=1788493011 origen=10.0.0.1 puerto=80 tipo=syn
total=0 leidos=1"

echo
echo "== Historial malformado: aqui solo se exige NO REVENTAR =="
comprobar "historial que no existe" "--historial" "$DIR/no_existe_ninguno"
: > "$DIR/hist_vacio"
comprobar "historial vacio" "--historial" "$DIR/hist_vacio"
printf 'esto no es un historial, es texto plano y encima largo\n' > "$DIR/hist_texto"
comprobar "historial de texto plano" "--historial" "$DIR/hist_texto"
printf '2026-09-04T03:36:51Z\n' > "$DIR/hist_solo_fecha"
comprobar "solo el instante, sin nada mas" "--historial" "$DIR/hist_solo_fecha"
printf '2026-12-31T23:59:60Z 10.0.0.1 80 syn\n' > "$DIR/hist_intercalar"
comprobar "segundo intercalar" "--historial" "$DIR/hist_intercalar"
printf '\x00\x01\x02 binario crudo\n' > "$DIR/hist_binario"
comprobar "bytes binarios" "--historial" "$DIR/hist_binario"

echo
echo "RESULTADO: $ok correctas, $mal fallidas"
exit "$mal"
