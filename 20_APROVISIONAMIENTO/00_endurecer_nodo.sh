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

# --- §7.1.5 · política de actualización automática ---------------------------
#
# Hallazgo de 07_AUDITORIAS §5.1: unattended-upgrades instalaba las
# actualizaciones pero TODAS las líneas de reinicio estaban comentadas, así que
# lo que exige reinicio —kernel, glibc, ssh— quedaba instalado y NO en vigor.
# El charter §7.1.5 pide las actualizaciones «con ventana de reinicio
# definida», y sin ella el punto está a medias.
#
# REESCRITO EL 2026-08-15 (rector §7.nonies) POR DOS DEFECTOS MEDIDOS, no
# supuestos. Salieron al diagnosticar el reinicio del 15/08 04:00, que el
# responsable reportó como una recaída de P-11 y NO lo era:
#
#   1. EL NÚCLEO NUNCA SE ACTUALIZABA SOLO. 50unattended-upgrades permite
#      únicamente orígenes «origin=Debian», y el núcleo de esta Pi viene de
#      «Raspberry Pi Foundation» (archive.raspberrypi.com), fuera de la lista.
#      Medido: el 14/08 el sistema escribió «No packages found that can be
#      upgraded unattended» teniendo el núcleo 6.18.39 disponible delante.
#      Consecuencia: la ventana de reinicio no se estrenó hasta que el
#      responsable instaló ese núcleo A MANO. Desde el 01/08, cero parches de
#      núcleo aplicados por la vía automática.
#
#   2. EL ORDEN ESTABA INVERTIDO. Se actualizaba a las ~06:00 y se reiniciaba
#      a las 04:00, o sea 22 HORAS DESPUÉS: el núcleo parcheado pasaba casi un
#      día instalado sin correr, que es justo lo que §5.1 quería evitar.
#
# Por qué «now» y no una hora fija: con el reinicio encadenado al final de la
# instalación el hueco pasa de 22 h a minutos, y además se vuelve IMPOSIBLE que
# una corrida larga pille el reinicio a mitad de un dpkg. Medido para dimensionar
# el riesgo que se descarta: la corrida automática del 15/08 tardó 17 min 53 s y
# el apt upgrade manual del 14/08 —33 paquetes con núcleo, chromium y mesa— 31 min.
#
# Por qué Persistent=false SOLO en el temporizador de instalación, y es la
# lección de P-11: con Persistent=true una corrida perdida se ejecuta al
# siguiente arranque, y como «now» reinicia acto seguido, un arranque de tarde
# —P-11 los produce entre las 17:19 y las 20:25— encadenaría un segundo
# reinicio DENTRO de la franja de uso del responsable. Es exactamente la
# interferencia que él pidió evitar. El nodo es un NAS 24/7: llega a las 05:50
# todos los días. El refresco de índices sí conserva Persistent porque no
# reinicia nada.
#
# Por qué no da miedo: es una parada LIMPIA, no un corte. systemd detiene nasd,
# que cierra sus escrituras; y las subidas en curso son reanudables por el
# protocolo tus (ADR-0026), así que se retoman solas. NO es el escenario de
# S-01, que habla de un corte brusco.
echo
echo "-- §7.1.5 política de actualización automática --"
cat > /etc/apt/apt.conf.d/60-nas-reinicio <<'EOF'
// Generado por 20_APROVISIONAMIENTO/00_endurecer_nodo.sh — proyecto NAS.
// Charter §7.1.5: actualizaciones desatendidas CON ventana de reinicio.
// Rector §7.nonies (2026-08-15): decisión del responsable.

// El origen del núcleo. Sin esta línea unattended-upgrades solo mira los
// orígenes «Debian» y el núcleo de la Pi —que viene de archive.raspberrypi.com—
// no se actualiza nunca solo. Origins-Pattern es una LISTA: declararla aquí
// AÑADE a lo que trae 50unattended-upgrades, no lo reemplaza.
Unattended-Upgrade::Origins-Pattern {
        "origin=Raspberry Pi Foundation,codename=${distro_codename},label=Raspberry Pi Foundation";
};

Unattended-Upgrade::Automatic-Reboot "true";
Unattended-Upgrade::Automatic-Reboot-WithUsers "true";
// «now» = reiniciar al TERMINAR de instalar, no a una hora fija 22 h después.
Unattended-Upgrade::Automatic-Reboot-Time "now";
EOF

# Los horarios. Se fijan con drop-in y no editando las unidades del sistema,
# que son del paquete apt y se reescriben al actualizarlo. La línea vacía
# «OnCalendar=» es obligatoria: sin ella systemd SUMA el horario nuevo al del
# fabricante en vez de sustituirlo, y correrían los dos.
install -d /etc/systemd/system/apt-daily.timer.d
cat > /etc/systemd/system/apt-daily.timer.d/nas.conf <<'EOF'
# Generado por 20_APROVISIONAMIENTO/00_endurecer_nodo.sh — proyecto NAS.
# Refresco de índices a las 05:00, para que la instalación de las 05:50
# encuentre la lista fresca. De fábrica corría 6,18:00 con hasta 12 h de
# dispersión, así que su orden respecto a la instalación era aleatorio.
[Timer]
OnCalendar=
OnCalendar=*-*-* 05:00
RandomizedDelaySec=20m
EOF

install -d /etc/systemd/system/apt-daily-upgrade.timer.d
cat > /etc/systemd/system/apt-daily-upgrade.timer.d/nas.conf <<'EOF'
# Generado por 20_APROVISIONAMIENTO/00_endurecer_nodo.sh — proyecto NAS.
# Instalación a las 05:50 (hora elegida por el responsable) y, con
# Automatic-Reboot-Time "now", el reinicio va encadenado al final.
# Persistent=false a propósito: ver el comentario largo de arriba.
[Timer]
OnCalendar=
OnCalendar=*-*-* 05:50
RandomizedDelaySec=10m
Persistent=false
EOF

systemctl daemon-reload
systemctl restart apt-daily.timer apt-daily-upgrade.timer

if apt-config dump 2>/dev/null | grep -q 'Unattended-Upgrade::Automatic-Reboot "true"'; then
  verde "Reinicio automático encadenado a la instalación, en vigor."
else
  rojo "La ventana de reinicio NO quedó en vigor. Revise /etc/apt/apt.conf.d/."
fi

# Que el origen del núcleo esté ADMITIDO se comprueba, no se supone: es el
# defecto que estuvo dos semanas invisible precisamente por no comprobarlo.
if apt-config dump Unattended-Upgrade::Origins-Pattern 2>/dev/null |
     grep -q 'Raspberry Pi Foundation'; then
  verde "Origen del núcleo (Raspberry Pi Foundation) admitido."
else
  rojo "El origen del núcleo NO quedó admitido: el kernel seguirá sin actualizarse solo."
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

# Y la otra mitad del mismo punto, que faltaba y por eso el defecto duró dos
# semanas: la ventana de reinicio no sirve de nada si el núcleo ni siquiera es
# CANDIDATO a instalarse solo (rector §7.nonies).
if apt-config dump Unattended-Upgrade::Origins-Pattern 2>/dev/null |
     grep -q 'Raspberry Pi Foundation'; then R=ok; else R=no; fi
comprobar "§7.1.5 origen del núcleo admitido (si no, no hay nada que reiniciar)" "$R"

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
