#!/bin/bash
# Phase 6, step 1 - a TLS certificate over DNS-01, without opening any port.
#
#   ADR-0046   TLS terminates inside nasd, not in a proxy
#   ADR-0042   BINDING RULE: nothing is exposed to the internet without TLS
#   ADR-0048   443 is opened only AFTERWARDS, and only if this certificate exists
#   P4         the DuckDNS token does not enter the repository
#
# WHY DNS-01 AND NOT HTTP-01, which is the important question:
#
#   HTTP-01 requires Let's Encrypt to reach port 80 from the internet in order
#   to validate. That would force opening the port BEFORE having a certificate,
#   which is exactly the circle ADR-0042 forbids: "nothing is exposed without
#   TLS, not even for a test". DNS-01 validates by placing a TXT record, so the
#   certificate is issued with the node still closed.
#
#   That is the real reason v1.25.0 chose DuckDNS: it supports TXT. It was not
#   the price.
#
# And because the node sits behind CGNAT (ADR-0044) this is even more necessary:
# HTTP-01 would not work even if we wanted it.
#
# Usage: sudo ./15_tls.sh

set -euo pipefail

CONF_DDNS=/etc/nas/ddns.conf
DIR_TLS=/etc/nas/tls
GANCHOS=/usr/local/sbin
CORREO_ACME=""   # opcional; sin él Let's Encrypt no avisa de caducidad

rojo()  { printf '\033[31m%s\033[0m\n' "$*"; }
verde() { printf '\033[32m%s\033[0m\n' "$*"; }
aviso() { printf '\033[33m%s\033[0m\n' "$*"; }

[ "$(id -u)" -eq 0 ] || { rojo "Ejecute con sudo."; exit 1; }

echo "===== Fase 6 · certificado TLS ====="
echo

# --- 1. Configuración -------------------------------------------------------
if [ ! -f "$CONF_DDNS" ]; then
  rojo "Falta $CONF_DDNS. Ejecute antes ./11_ddns.sh"
  exit 1
fi
# shellcheck source=/dev/null
. "$CONF_DDNS"
: "${DOMINIO:?falta DOMINIO en $CONF_DDNS}"
: "${TESTIGO:?falta TESTIGO en $CONF_DDNS}"
NOMBRE="$DOMINIO.duckdns.org"
verde "Dominio: $NOMBRE"

# El nombre tiene que resolver ANTES de pedir nada: si no, Let's Encrypt
# fallará la validación y habremos gastado un intento contra su límite de
# peticiones, que es de 5 fallos por hora y cuenta.
if ! getent hosts "$NOMBRE" >/dev/null 2>&1; then
  rojo "$NOMBRE no resuelve. Ejecute ./11_ddns.sh y compruébelo antes."
  exit 1
fi

# --- 2. certbot -------------------------------------------------------------
#
# Del repositorio de la distribución, no de un instalador externo: es lo que
# el charter §6.2 espera y lo que las actualizaciones desatendidas parchean.
# NO hay demonio: certbot corre por temporizador, igual que 11_ddns.sh.
if ! command -v certbot >/dev/null 2>&1; then
  echo "Instalando certbot..."
  export DEBIAN_FRONTEND=noninteractive
  apt-get update -qq
  apt-get install -y -qq certbot
fi
verde "certbot presente: $(certbot --version 2>&1 | head -1)"

# --- 3. Ganchos de validación ----------------------------------------------
#
# DuckDNS actualiza el TXT con una llamada. El gancho NO lleva el testigo
# dentro: lo lee de /etc/nas/ddns.conf, que es 0600 root (P4).
install -d -m 0755 "$GANCHOS"

cat > "$GANCHOS/nas-tls-auth.sh" <<'EOF'
#!/bin/sh
# Gancho de validación DNS-01. Lo invoca certbot con CERTBOT_VALIDATION.
set -eu
. /etc/nas/ddns.conf
RESP=$(curl -fsS --max-time 20 \
  "https://www.duckdns.org/update?domains=${DOMINIO}&token=${TESTIGO}&txt=${CERTBOT_VALIDATION}")
[ "$RESP" = "OK" ] || { echo "DuckDNS no aceptó el TXT: $RESP" >&2; exit 1; }
# Sin esto la validación falla la primera vez y funciona la segunda, que es el
# peor modo de fallo posible: intermitente y con el límite de Let's Encrypt
# consumiéndose. 30 s cubre la propagación de DuckDNS con holgura.
sleep 30
EOF

cat > "$GANCHOS/nas-tls-limpiar.sh" <<'EOF'
#!/bin/sh
# Retira el TXT tras validar. Un TXT olvidado no es un agujero, pero deja
# rastro público de cómo se valida este dominio.
set -eu
. /etc/nas/ddns.conf
curl -fsS --max-time 20 \
  "https://www.duckdns.org/update?domains=${DOMINIO}&token=${TESTIGO}&txt=removed&clear=true" >/dev/null || true
EOF

chmod 0700 "$GANCHOS/nas-tls-auth.sh" "$GANCHOS/nas-tls-limpiar.sh"
verde "Ganchos de validación escritos (0700 root: llevan acceso al testigo)."

# --- 4. Emisión -------------------------------------------------------------
echo
echo "== Solicitando el certificado a Let's Encrypt =="
aviso "Tarda ~40 s: hay una espera deliberada para que el TXT se propague."

# Array y no cadena: «--email x» son DOS argumentos, y entrecomillar la cadena
# los uniría en uno solo. Es el mismo tipo de defecto que shellcheck cazó en
# 12_wireguard.sh, y por eso el utillaje de ADR-0013 §4 es vinculante.
if [ -n "$CORREO_ACME" ]; then
  ARGS_CORREO=(--email "$CORREO_ACME")
else
  ARGS_CORREO=(--register-unsafely-without-email)
fi

if certbot certonly \
    --manual \
    --preferred-challenges dns \
    --manual-auth-hook "$GANCHOS/nas-tls-auth.sh" \
    --manual-cleanup-hook "$GANCHOS/nas-tls-limpiar.sh" \
    --non-interactive --agree-tos "${ARGS_CORREO[@]}" \
    --keep-until-expiring \
    -d "$NOMBRE"; then
  verde "Certificado emitido para $NOMBRE."
else
  rojo "certbot falló. NO se abre ningún puerto (ADR-0042)."
  rojo "Revise el registro en /var/log/letsencrypt/letsencrypt.log"
  exit 1
fi

# --- 5. Copia legible por nasd ---------------------------------------------
#
# nasd corre como el usuario «nas» y NO debe leer /etc/letsencrypt, que es de
# root y contiene las claves de todos los certificados. Se le deja una copia
# acotada: solo este par, y la clave legible solo por su grupo.
install -d -m 0755 "$DIR_TLS"
install -m 0644 "/etc/letsencrypt/live/$NOMBRE/fullchain.pem" "$DIR_TLS/fullchain.pem"
install -m 0640 -g nas "/etc/letsencrypt/live/$NOMBRE/privkey.pem" "$DIR_TLS/privkey.pem"
verde "Par copiado a $DIR_TLS (la clave, solo para el grupo «nas»)."

# --- 6. Renovación automática ----------------------------------------------
#
# certbot trae su propio temporizador. Lo que le falta es rehacer la copia de
# arriba, porque si no nasd seguiría sirviendo el certificado viejo hasta que
# caducara — un fallo silencioso a 90 días vista.
install -d -m 0755 /etc/letsencrypt/renewal-hooks/deploy
cat > /etc/letsencrypt/renewal-hooks/deploy/nas-copiar.sh <<EOF
#!/bin/sh
# Tras cada renovación, repone la copia que lee nasd. Sin reiniciar el
# servicio: el CargadorCert de ADR-0046 detecta el cambio de fecha y recarga
# en caliente, así que las subidas en curso no se cortan.
set -eu
install -m 0644 "/etc/letsencrypt/live/$NOMBRE/fullchain.pem" "$DIR_TLS/fullchain.pem"
install -m 0640 -g nas "/etc/letsencrypt/live/$NOMBRE/privkey.pem" "$DIR_TLS/privkey.pem"
EOF
chmod 0755 /etc/letsencrypt/renewal-hooks/deploy/nas-copiar.sh

systemctl enable --now certbot.timer >/dev/null 2>&1 || true
if systemctl is-active --quiet certbot.timer; then
  verde "Renovación automática activa (certbot.timer)."
else
  rojo "certbot.timer NO está activo: el certificado caducará en 90 días sin avisar."
fi

# --- 7. Comprobación --------------------------------------------------------
echo
echo "== Comprobación =="
FIN=$(openssl x509 -in "$DIR_TLS/fullchain.pem" -noout -enddate | cut -d= -f2)
DIAS=$(( ( $(date -d "$FIN" +%s) - $(date +%s) ) / 86400 ))
CN=$(openssl x509 -in "$DIR_TLS/fullchain.pem" -noout -subject | sed 's/.*CN *= *//')
echo "  nombre     : $CN"
echo "  caduca     : $FIN  ($DIAS días)"
if [ "$CN" = "$NOMBRE" ] && [ "$DIAS" -gt 0 ]; then
  verde "Certificado válido y en su sitio."
else
  rojo "El certificado no cuadra con $NOMBRE o ya caducó."
  exit 1
fi

echo
verde "Paso completado."
echo "Siguiente:"
echo "  1. sudo ./05_instalar_servicio.sh   (nasd.toml con las rutas del certificado)"
echo "  2. sudo ./03_cortafuegos.sh         (abre el 443 SOLO si el certificado existe)"
