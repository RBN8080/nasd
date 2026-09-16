#!/bin/bash
# Backup of the node's identity - ADR-0050.
#
#   D-04   system and data live on different media, on purpose
#   P9     the charter assumes the medium WILL fail, not that it may
#   P8     no daemon: a systemd timer, like the DDNS one of ADR-0043
#
# WHAT PROBLEM IT SOLVES, and it is not the obvious one:
#
#   If the board dies, the PHOTOS survive - they are on the USB disk, separated
#   by D-04. What does NOT survive is the identity: the WireGuard server private
#   key lives only on the boot medium, and losing it forces enrolling all three
#   devices AGAIN, scanning a new QR on each. The scripts in
#   20_APROVISIONAMIENTO/ rebuild the configuration; they do NOT rebuild the
#   identity.
#
# THE TLS PRIVATE KEY IS NOT BACKED UP, AND THAT IS DELIBERATE: it is reissued
# with 15_tls.sh over DNS-01 in minutes, and it is the ONLY element whose theft
# allows impersonating the site. Excluding it costs nothing in recovery and
# reduces the damage if the copy is lost.
#
# The destination is the DATA DISK because that is exactly what survives the
# board, and OUTSIDE datos/ because that is what Samba publishes.
#
# Idempotent. Usage:  sudo ./17_respaldar_identidad.sh
#                     sudo ./17_respaldar_identidad.sh --solo-copia   (timer)

set -euo pipefail

DESTINO=/srv/nas/identidad
ARCHIVO="$DESTINO/identidad.tar.gz"
SERVICIO=/etc/systemd/system/nas-respaldo-identidad.service
TEMPORIZADOR=/etc/systemd/system/nas-respaldo-identidad.timer
EJECUTABLE=/usr/local/sbin/nas-respaldar-identidad.sh
INTERVALO=weekly

rojo()  { printf '\033[31m%s\033[0m\n' "$*"; }
verde() { printf '\033[32m%s\033[0m\n' "$*"; }
aviso() { printf '\033[33m%s\033[0m\n' "$*"; }

[ "$(id -u)" -eq 0 ] || { rojo "Ejecute con sudo."; exit 1; }

# ---------------------------------------------------------------------------
# La copia. Es lo único que hace el temporizador.
# ---------------------------------------------------------------------------
hacer_copia() {
  # P5: sin volumen montado no se respalda nada. Escribir en el punto de
  # montaje vacío crearía una copia en la tarjeta de arranque —el medio que
  # este respaldo existe para sobrevivir— y nadie lo notaría.
  if ! mountpoint -q /srv/nas; then
    logger -t nas-respaldo -p user.err "El volumen /srv/nas NO está montado: no se respalda"
    rojo "El volumen /srv/nas no está montado. No se ha respaldado nada."
    return 1
  fi

  install -d -m 0700 -o root -g root "$DESTINO"

  # Lo que se guarda. Ausente = se omite sin fallar: 15_tls.sh puede no haber
  # corrido todavía, y eso no debe impedir respaldar WireGuard.
  local piezas=()
  for p in /etc/wireguard /etc/nasd/credencial /etc/nasd/nasd.toml /etc/nas/ddns.conf; do
    [ -e "$p" ] && piezas+=("$p")
  done
  [ ${#piezas[@]} -gt 0 ] || { rojo "No hay nada que respaldar."; return 1; }

  # Escritura atómica y UNA copia anterior — misma secuencia que ADR-0024.
  # Sin esto, el temporizador semanal sobrescribiría el respaldo bueno con uno
  # corrupto y el fallo se descubriría el día de la restauración.
  tar czf "$ARCHIVO.tmp" --absolute-names "${piezas[@]}" 2>/dev/null
  chmod 0600 "$ARCHIVO.tmp"
  [ -f "$ARCHIVO" ] && cp -p "$ARCHIVO" "$ARCHIVO.anterior"
  mv "$ARCHIVO.tmp" "$ARCHIVO"
  sync

  # Comprobar el EFECTO, no la ausencia de error: un tar que no se puede leer
  # es un respaldo que no existe, y lo parece hasta el día que hace falta.
  tar tzf "$ARCHIVO" >/dev/null 2>&1 || {
    logger -t nas-respaldo -p user.err "El respaldo se escribió pero NO se puede leer"
    rojo "El archivo se escribió pero no se puede leer. Respaldo NO válido."
    return 1
  }
  return 0
}

if [ "${1:-}" = "--solo-copia" ]; then
  hacer_copia
  exit $?
fi

echo "===== Respaldo de la identidad del nodo (ADR-0050) ====="
echo

hacer_copia || exit 1
verde "Copia hecha: $ARCHIVO ($(stat -c %s "$ARCHIVO") bytes)"
echo "Contiene:"
tar tzf "$ARCHIVO" | sed 's/^/  /'

# ---------------------------------------------------------------------------
# El temporizador. La identidad cambia dos o tres veces al año, pero cambia
# justo cuando uno está ocupado en otra cosa: dar de alta un dispositivo y
# olvidar el respaldo es el escenario realista, no la excepción.
# ---------------------------------------------------------------------------
# El ejecutable vive fuera del directorio del responsable: el temporizador no
# debe depender de que ~/nas-aprovisionamiento/ siga existiendo dentro de un
# año. Se copia este mismo archivo, resolviendo su ruta real y no $0, que
# depende de cómo se haya invocado.
ORIGEN=$(readlink -f "${BASH_SOURCE[0]}")
install -m 0700 -o root -g root "$ORIGEN" "$EJECUTABLE"

cat > "$SERVICIO" <<EOF
# Generado por 20_APROVISIONAMIENTO/17_respaldar_identidad.sh. NO editar (P1).
[Unit]
Description=Respaldar la identidad del nodo en el disco de datos (ADR-0050)
# El disco debe estar montado; si no, la copia acabaría en la tarjeta de
# arranque, que es justo el medio del que se está protegiendo.
RequiresMountsFor=/srv/nas

[Service]
Type=oneshot
ExecStart=$EJECUTABLE --solo-copia
EOF

cat > "$TEMPORIZADOR" <<EOF
# Generado por 20_APROVISIONAMIENTO/17_respaldar_identidad.sh. NO editar (P1).
[Unit]
Description=Respaldar la identidad del nodo, $INTERVALO

[Timer]
OnCalendar=$INTERVALO
# Si el nodo estaba apagado a la hora prevista, se ejecuta al arrancar. Sin
# esto, un nodo que se apaga los fines de semana no se respaldaría nunca.
Persistent=true
AccuracySec=1h

[Install]
WantedBy=timers.target
EOF

systemctl daemon-reload
systemctl enable --now nas-respaldo-identidad.timer >/dev/null 2>&1

echo
verde "Temporizador activo: se repite $INTERVALO."
echo
aviso "CÓMO SE RESTAURA, que es lo que de verdad importa:"
cat <<'AYUDA'

  Con la placa nueva y el disco viejo montado en /srv/nas:

      sudo tar xzf /srv/nas/identidad/identidad.tar.gz -C /
      sudo systemctl restart nasd wg-quick@wg0 nas-ddns.timer
      sudo ./15_tls.sh          # el certificado SÍ se reemite, no se respalda

  Los tres dispositivos siguen conectando sin tocarlos: sus perfiles apuntan
  al NOMBRE de DuckDNS, no a una IP, y la clave del servidor es la de antes.

AYUDA
aviso "LO QUE ESTO NO CUBRE, y conviene saberlo:"
echo "  Si se pierden la placa Y el disco a la vez, no queda nada — ni esto"
echo "  ni las fotos (D-12, copia única). Para eso haría falta una copia"
echo "  FUERA del nodo, y esa la tiene que hacer el responsable a mano."
