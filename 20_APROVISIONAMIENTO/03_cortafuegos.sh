#!/bin/bash
# Fase 1, paso 3 — abrir 445 y 8080 SOLO a la LAN.
#
#   Charter §7.1.3   política DENY por defecto; apertura justificada en ADR
#   ADR-0018         puerto web 8080
#   RN-04 / RNF-07   solo 192.168.1.0/24
#
# Uso: sudo ./03_cortafuegos.sh

set -euo pipefail

RED=192.168.1.0/24
PUERTO_WEB=8080

rojo()  { printf '\033[31m%s\033[0m\n' "$*"; }
verde() { printf '\033[32m%s\033[0m\n' "$*"; }

[ "$(id -u)" -eq 0 ] || { rojo "Ejecute con sudo."; exit 1; }
command -v nft >/dev/null || { echo "Instalando nftables..."; apt-get install -y nftables; }

REGLAS=/etc/nftables.conf
[ -f "$REGLAS.original" ] || cp "$REGLAS" "$REGLAS.original" 2>/dev/null || true

cat > "$REGLAS" <<EOF
#!/usr/sbin/nft -f
# Generado por 20_APROVISIONAMIENTO/03_cortafuegos.sh — proyecto NAS.
# Charter §7.1.3: política DENY por defecto. Cada apertura tiene su ADR.

flush ruleset

table inet filter {
  chain entrada {
    type filter hook input priority 0; policy drop;

    ct state established,related accept
    ct state invalid drop
    iif lo accept

    # Diagnóstico dentro de la LAN.
    ip saddr $RED icmp type echo-request accept

    # SSH: sin esto el nodo se queda inaccesible. Charter §7.1.2.
    ip saddr $RED tcp dport 22 accept

    # SMB — D-01. Solo la LAN.
    ip saddr $RED tcp dport 445 accept

    # Web — ADR-0018. Solo la LAN.
    ip saddr $RED tcp dport $PUERTO_WEB accept

    # Todo lo demás cae en silencio.
  }

  chain reenvio {
    type filter hook forward priority 0; policy drop;
  }

  chain salida {
    type filter hook output priority 0; policy accept;
  }
}
EOF

echo "== Comprobando la sintaxis antes de aplicar =="
nft -c -f "$REGLAS" || { rojo "Las reglas tienen errores; NO se aplican."; exit 1; }
verde "Sintaxis correcta."

echo
rojo "AVISO: se va a aplicar una política DENY por defecto."
echo "El puerto 22 queda abierto para $RED, así que no debería perder SSH."
echo "Si se conecta desde FUERA de $RED, perderá el acceso ahora mismo."
read -r -p "Escriba SI para aplicar: " R
[ "$R" = "SI" ] || { echo "Cancelado. Las reglas quedan escritas en $REGLAS sin aplicar."; exit 1; }

nft -f "$REGLAS"
systemctl enable nftables
verde "Reglas aplicadas y activadas al arranque."

echo
nft list ruleset | sed -n '1,40p'
echo
verde "Paso 3 completado."
echo "Siguiente: ./04_verificar.sh (no necesita sudo para casi nada)"
