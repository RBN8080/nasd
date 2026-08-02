#!/bin/bash
# Paso 0 — endurecimiento del nodo exigido por el charter §7.1.
#
# «Obligatorio antes de exponer el nodo». Los seis puntos se auditaron contra
# el nodo el 2026-07-31 y cinco ya se cumplían; este script cierra el que
# faltaba y comprueba los demás en vez de suponerlos.
#
# Existe porque P1 lo exige: retirar el escritorio se hizo con un comando
# suelto, y lo que no está en un script no existe.
#
# Uso: sudo ./00_endurecer_nodo.sh

set -euo pipefail

rojo()  { printf '\033[31m%s\033[0m\n' "$*"; }
verde() { printf '\033[32m%s\033[0m\n' "$*"; }
aviso() { printf '\033[33m%s\033[0m\n' "$*"; }

[ "$(id -u)" -eq 0 ] || { rojo "Ejecute con sudo."; exit 1; }

echo "===== Charter §7.1 · obligatorio antes de exponer el nodo ====="
echo

# --- §7.1.4 · superficie mínima ---------------------------------------------
#
# El charter §7.3 concedió una excepción al escritorio —194 MB, aceptados a
# cambio de poder usar la máquina con pantalla— pero la concedió cuando el
# nodo era SOLO LAN. La Fase 5 lo hará alcanzable desde fuera, y §7.1.4 no
# admite escritorio antes de exponerlo.
echo "-- §7.1.4 superficie mínima --"
ACTUAL=$(systemctl get-default)
if [ "$ACTUAL" = "multi-user.target" ]; then
  verde "Ya sin escritorio: $ACTUAL"
else
  systemctl set-default multi-user.target >/dev/null
  verde "Escritorio retirado: $ACTUAL -> multi-user.target"
  aviso "Se aplica en el próximo reinicio."
fi

# SERVICIOS QUE NADIE PIDIÓ. Hallazgo de 07_AUDITORIAS §5.2 (auditoría 1):
# corrían 41.5 MB en demonios que ninguna decisión del proyecto justifica, en
# un nodo cuyo presupuesto real son 656 MB. §7.1.4 dice «sin servicios no
# requeridos» y estos tres no tienen defensa posible:
#
#   bluetooth       un NAS no usa Bluetooth
#   wpa_supplicant  el nodo es cableado; wlan0 está documentado como inutilizable
#   avahi-daemon    mDNS, y además abría 5353/udp en 0.0.0.0
#
# NO se toca nmbd, que sí tiene defensa: lo levanta 02_instalar_samba.sh a
# propósito y quitarlo haría que el NAS dejara de aparecer al explorar la red
# desde Windows. Eso es una decisión de producto, no de endurecimiento.
#
# Reversible con: sudo systemctl enable --now <servicio>
for S in bluetooth wpa_supplicant avahi-daemon; do
  if systemctl list-unit-files "$S.service" --no-legend 2>/dev/null | grep -q .; then
    systemctl disable --now "$S" >/dev/null 2>&1 || true
    if systemctl is-active --quiet "$S" 2>/dev/null; then
      rojo "  $S sigue activo pese a haberlo detenido"
    else
      verde "  $S detenido y deshabilitado (P8)"
    fi
  fi
done

# --- §7.1.5 · ventana de reinicio -------------------------------------------
#
# Hallazgo de 07_AUDITORIAS §5.1: unattended-upgrades instalaba las
# actualizaciones pero TODAS las líneas de reinicio estaban comentadas, así que
# lo que exige reinicio —kernel, glibc, ssh— quedaba instalado y NO en vigor.
# El charter §7.1.5 pide las actualizaciones «con ventana de reinicio
# definida», y sin ella el punto está a medias.
#
# Por qué a las 04:00 y por qué no da miedo: es una parada LIMPIA, no un corte.
# systemd detiene nasd, que cierra sus escrituras; y las subidas en curso son
# reanudables por el protocolo tus (ADR-0026), así que se retoman solas. NO es
# el escenario de S-01, que habla de un corte brusco.
echo
echo "-- §7.1.5 ventana de reinicio --"
cat > /etc/apt/apt.conf.d/60-nas-reinicio <<'EOF'
// Generado por 20_APROVISIONAMIENTO/00_endurecer_nodo.sh — proyecto NAS.
// Charter §7.1.5: actualizaciones desatendidas CON ventana de reinicio.
Unattended-Upgrade::Automatic-Reboot "true";
Unattended-Upgrade::Automatic-Reboot-WithUsers "true";
Unattended-Upgrade::Automatic-Reboot-Time "04:00";
EOF
if apt-config dump 2>/dev/null | grep -q 'Unattended-Upgrade::Automatic-Reboot "true"'; then
  verde "Reinicio automático a las 04:00 en vigor."
else
  rojo "La ventana de reinicio NO quedó en vigor. Revise /etc/apt/apt.conf.d/."
fi

# --- Los otros cinco: se COMPRUEBAN, no se suponen ---------------------------
echo
echo "-- Comprobación de los otros cinco puntos --"
fallos=0
comprobar() {
  if [ "$2" = "ok" ]; then verde "  OK    $1"; else rojo "  FALLO $1"; fallos=$((fallos+1)); fi
}

if getent passwd pi >/dev/null 2>&1; then R=no; else R=ok; fi
comprobar "§7.1.1 sin usuario 'pi' con contraseña conocida" "$R"

SSHD=$(sshd -T 2>/dev/null)
case "$SSHD" in
  *"passwordauthentication no"*) R=ok ;;
  *) R=no ;;
esac
comprobar "§7.1.2 SSH sin autenticación por contraseña" "$R"

if [ "$(nft list ruleset 2>/dev/null | grep -c 'policy drop')" -gt 0 ]; then R=ok; else R=no; fi
comprobar "§7.1.3 cortafuegos con política DENY" "$R"

if [ "$(systemctl is-enabled unattended-upgrades 2>/dev/null)" = "enabled" ]; then R=ok; else R=no; fi
comprobar "§7.1.5 actualizaciones de seguridad desatendidas" "$R"

# El charter pide las actualizaciones Y la ventana. Comprobarlas por separado
# es lo que reveló que el punto llevaba meses a medias (07_AUDITORIAS §5.1).
if apt-config dump 2>/dev/null | grep -q 'Unattended-Upgrade::Automatic-Reboot "true"'; then R=ok; else R=no; fi
comprobar "§7.1.5 ventana de reinicio definida (si no, el kernel no se aplica)" "$R"

# §4.1: el nodo no tiene RTC, así que la hora depende de NTP y sin ella no hay
# operación TLS válida — que es exactamente lo que la Fase 5 va a necesitar.
if [ "$(timedatectl show -p NTPSynchronized --value)" = "yes" ]; then R=ok; else R=no; fi
comprobar "§7.1.6 hora sincronizada por NTP (no hay RTC)" "$R"

echo
if [ "$fallos" -eq 0 ]; then
  verde "Charter §7.1 satisfecho."
else
  rojo "$fallos punto(s) del charter §7.1 sin cumplir. NO exponer el nodo así."
  exit 1
fi
