#!/bin/bash
# Preparacion de produccion. SOLO ejecutar tras autorizar la ventana del plan.
# No modifica DNS, emite certificados ni cambia el nombre activo por si solo.
set -euo pipefail
cd -- "$(dirname -- "$0")"
[ "$(id -u)" -eq 0 ] || { echo 'Ejecute con sudo' >&2; exit 1; }
case "${1:-}" in
  --instalar)
    # dig permite comprobar respuestas autoritativas (AA) y propagacion sin
    # anadir bibliotecas Python ni depender de resolutores recursivos externos.
    command -v dig >/dev/null || { echo 'Falta dig: instalar bind9-dnsutils en la ventana autorizada' >&2; exit 1; }
    command -v certbot >/dev/null
    install -d -m 0755 /usr/local/lib/nas-njalla
    install -d -m 0700 -o root -g root /etc/nas/njalla
    for pieza in token_njalla.py njalla_registros.py nas_dominio.py nas_tls.py; do
      install -m 0644 "$pieza" "/usr/local/lib/nas-njalla/$pieza"
    done
    if [ ! -f /etc/nas/njalla/dominio.toml ]; then
      install -m 0600 dominio.toml.ejemplo /etc/nas/njalla/dominio.toml
    fi
    cat > /usr/local/sbin/nas-njalla <<'EOF'
#!/bin/sh
exec /usr/bin/python3 -B /usr/local/lib/nas-njalla/nas_dominio.py "$@"
EOF
    cat > /usr/local/sbin/nas-tls-njalla <<'EOF'
#!/bin/sh
exec /usr/bin/python3 -B /usr/local/lib/nas-njalla/nas_tls.py "$@"
EOF
    chmod 0755 /usr/local/sbin/nas-njalla /usr/local/sbin/nas-tls-njalla
    cat > /etc/systemd/system/nas-njalla.service <<'EOF'
[Unit]
Description=Verificar y actualizar registros DNS especificos del NAS en Njalla
After=network-online.target
Wants=network-online.target
[Service]
Type=oneshot
ExecStart=/usr/local/sbin/nas-njalla ddns
TimeoutStartSec=240
ProtectSystem=strict
ReadWritePaths=-/run/nas-njalla
RuntimeDirectory=nas-njalla
RuntimeDirectoryMode=0700
RuntimeDirectoryPreserve=yes
PrivateTmp=yes
NoNewPrivileges=yes
ProtectHome=yes
UMask=0077
EOF
    cat > /etc/systemd/system/nas-njalla.timer <<'EOF'
[Unit]
Description=Comprobar AAAA del NAS en Njalla cada 5 minutos
[Timer]
OnBootSec=1min
OnUnitActiveSec=5min
AccuracySec=30s
[Install]
WantedBy=timers.target
EOF
    systemctl daemon-reload
    echo 'Instalado sin activar timer/ganchos. Completar dominio.toml y seguir el plan.'
    ;;
  --ganchos)
    # La ventana excluye tambien ejecuciones manuales de Certbot. Detener el
    # timer no mata una renovacion en curso: se rechaza mientras siga activa.
    if systemctl is-active --quiet certbot.service; then
      echo 'Esperar a que termine Certbot antes de sustituir el gancho' >&2; exit 1
    fi
    /usr/local/sbin/nas-njalla verificar
    respaldo=$(mktemp -d /etc/nas/njalla/reversion.XXXXXXXX)
    for pieza in /etc/letsencrypt/renewal-hooks/deploy/nas-copiar.sh /etc/nas/tls-lineage /etc/nas/dominios.conf; do
      if [ -f "$pieza" ]; then cp -p "$pieza" "$respaldo/$(basename "$pieza")"; fi
    done
    cp -a /etc/nas/tls "$respaldo/tls"
    # La copia raiz privada contiene tambien la clave para revertir la ventana;
    # no se incluye en el respaldo rutinario de identidad ni en el repositorio.
    if [ ! -f /etc/nas/tls-lineage ]; then
      printf '%s\n' /etc/letsencrypt/live/nas.example.org > /etc/nas/tls-lineage
      chmod 0600 /etc/nas/tls-lineage
    fi
    cat > /etc/letsencrypt/renewal-hooks/deploy/nas-copiar.sh <<'EOF'
#!/bin/sh
exec /usr/local/sbin/nas-tls-njalla desplegar
EOF
    chmod 0755 /etc/letsencrypt/renewal-hooks/deploy/nas-copiar.sh
    printf 'Respaldo de ventana: %s\n' "$respaldo"
    echo 'Gancho protegido; linaje seleccionado sin cambiar. Emitir/verificar antes de activar.'
    ;;
  *) echo 'Uso: sudo ./23_dominio_njalla.sh --instalar | --ganchos (ventana autorizada)' >&2; exit 1 ;;
esac
