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
echo "RESULTADO: $ok correctas, $mal fallidas"
exit "$mal"
