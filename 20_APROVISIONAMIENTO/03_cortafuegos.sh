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
PUERTO_WEB=80   # ADR-0032
PUERTO_WG=61820 # ADR-0043
WG_CONF=/etc/wireguard/wg0.conf

# El bloque de WireGuard se emite SOLO si el túnel está configurado. Un puerto
# abierto sin nada escuchando detrás es superficie regalada (P8) — es el mismo
# criterio con el que ADR-0032 retiró la regla del 8080. Así, este script sirve
# igual antes y después de la Fase 5 sin tener dos versiones.
if [ -f "$WG_CONF" ]; then
  BLOQUE_WG="
    # --- Fase 5, tunel WireGuard (ADR-0043) ---
    #
    # DESDE CUALQUIER ORIGEN, y no es un descuido: el responsable se conecta
    # desde redes que no se pueden enumerar de antemano. WireGuard no responde
    # NADA sin una clave valida —ni RST ni ICMP—, asi que para un rastreador
    # este puerto es indistinguible de uno cerrado.
    udp dport $PUERTO_WG accept

    # Lo que entra POR el tunel llega a la web y a SMB, y a nada mas.
    # ADR-0042: la Fase 5 da acceso completo a los dispositivos del responsable.
    iifname \"wg0\" tcp dport { $PUERTO_WEB, 445 } accept
    iifname \"wg0\" icmp type echo-request accept
"
  echo "WireGuard configurado: se incluyen las reglas del túnel (puerto $PUERTO_WG)."
else
  BLOQUE_WG=""
  echo "Sin $WG_CONF: NO se abre el puerto de WireGuard (P8)."
fi

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

    # ICMPv6 — OBLIGATORIO, no es diagnóstico. RFC 4890 §4.4.1.
    #
    # IPv6 no tiene ARP: la resolución de vecinos y el anuncio del router SON
    # ICMPv6. Con «policy drop» y sin estas reglas, el nodo descarta los
    # Router Advertisement y NUNCA obtiene dirección IPv6, aunque el router los
    # esté enviando correctamente cada pocos segundos.
    #
    # Eso fue exactamente lo que pasó: 06_ACCESO_REMOTO §8.4 registró que «la
    # LAN tiene IPv6 y la Pi no», y se persiguió durante dos sesiones en
    # accept_ra, en NetworkManager y en el router. La causa estaba aquí, en el
    # cortafuegos del propio nodo. Medido el 2026-08-01: con estas reglas
    # puestas, la Pi resuelve la puerta de enlace y sale a Internet por IPv6 en
    # menos de 3 segundos; sin ellas, jamás.
    #
    # No amplía superficie: son mensajes de control del protocolo. Los tipos de
    # descubrimiento van con hop limit 255, que por RFC 4861 §11.2 no puede
    # falsificarse desde fuera del enlace — un paquete remoto llega con menos.
    icmpv6 type { nd-router-advert, nd-router-solicit,
                  nd-neighbor-solicit, nd-neighbor-advert } accept

    # Errores de ICMPv6. Sin «packet-too-big» IPv6 se rompe en silencio: no hay
    # fragmentación en tránsito, así que el descubrimiento de MTU es la ÚNICA
    # forma de que una conexión con MTU menor funcione. Es el fallo clásico de
    # «la web carga y las descargas grandes se cuelgan».
    icmpv6 type { destination-unreachable, packet-too-big,
                  time-exceeded, parameter-problem } accept

    # Diagnóstico IPv6 dentro de la LAN, en paralelo a la regla IPv4 de arriba.
    icmpv6 type echo-request ip6 saddr fe80::/10 accept

    # SSH: sin esto el nodo se queda inaccesible. Charter §7.1.2.
    ip saddr $RED tcp dport 22 accept

    # SMB — D-01. Solo la LAN.
    ip saddr $RED tcp dport 445 accept

    # Web — ADR-0032. Solo la LAN.
    #
    # El 8080 se RETIRA a propósito: desde ADR-0032 no hay servicio detrás, y
    # una regla abierta sin nada escuchando es superficie regalada (P8).
    ip saddr $RED tcp dport $PUERTO_WEB accept
$BLOQUE_WG
    # Todo lo demás cae en silencio.
  }

  chain reenvio {
    # SIGUE EN DROP CON EL TUNEL PUESTO, y es deliberado: el diseño de la
    # Fase 5 hace que el cliente hable con 192.168.1.38, que es una dirección
    # LOCAL del nodo. El kernel la entrega localmente y no reenvía nada, así
    # que el túnel NO alcanza ningún otro equipo de la casa.
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
# LA CONFIRMACIÓN NO SE QUITA: aplicar una política DENY mal escrita deja el
# nodo inalcanzable y exige teclado y pantalla. Se añade UNA vía no interactiva,
# acotada a un caso concreto y con su motivo:
#
#   12_wireguard.sh vuelve a llamar a este script para que incluya el bloque
#   del túnel. En ese momento las reglas YA están aplicadas y en vigor, la del
#   22 entre ellas, y lo único que cambia es AÑADIR una apertura. Preguntar ahí
#   no protege de nada y, sin terminal, «read» leería EOF y cancelaría.
#
# Cualquier otro uso debe seguir confirmando a mano.
if [ "${1:-}" = "--si" ]; then
  echo "Modo no interactivo (--si): reaplicando reglas ya vigentes."
else
  rojo "AVISO: se va a aplicar una política DENY por defecto."
  echo "El puerto 22 queda abierto para $RED, así que no debería perder SSH."
  echo "Si se conecta desde FUERA de $RED, perderá el acceso ahora mismo."
  read -r -p "Escriba SI para aplicar: " R
  [ "$R" = "SI" ] || { echo "Cancelado. Las reglas quedan escritas en $REGLAS sin aplicar."; exit 1; }
fi

nft -f "$REGLAS"
systemctl enable nftables
verde "Reglas aplicadas y activadas al arranque."

echo
nft list ruleset | sed -n '1,40p'
echo
verde "Paso 3 completado."
echo "Siguiente: ./04_verificar.sh (no necesita sudo para casi nada)"
