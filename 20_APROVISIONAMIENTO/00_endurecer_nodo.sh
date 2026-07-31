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
