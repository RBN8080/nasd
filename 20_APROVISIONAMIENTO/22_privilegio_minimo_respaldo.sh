#!/bin/bash
# =============================================================================
#  22_privilegio_minimo_respaldo.sh
#
#  CAPA 4 de 30_CLIENTE_RESPALDO.md seccion 7: privilegio minimo en el recurso.
#
#  EL PROBLEMA QUE RESUELVE, medido en el nodo el 2026-09-02:
#
#    /etc/samba/smb.conf tiene UNA SOLA comparticion, [datos], con
#    "valid users = nas" y "path = /srv/nas/datos". El cliente de respaldo entra
#    con esa credencial, asi que HOY PUEDE ESCRIBIR Y BORRAR EN TODO EL NAS:
#    homeUsers/ de la familia, _HISTORICO con sus 141 GB, y el respaldo del HTC.
#
#    La seccion 7 lo dice sin rodeos: "el respaldo automatico ES un privilegio.
#    Si el equipo puede escribir y borrar en el nodo sin intervencion, cualquier
#    cosa que controle el equipo tambien puede". Eso es lo que se acota aqui.
#
#  QUE HACE: anade una SEGUNDA comparticion, [respaldo], enraizada en
#  01_BACKUP/EQUIPO-01 y accesible solo por un usuario propio. El cliente pasa
#  a usar esa, y deja de tener alcance sobre el resto del arbol.
#
#  ------------------------------------------------------------------------
#  ESTE GUION NO SE EJECUTA SOLO, Y HAY UNA RAZON QUE NO ES PRUDENCIA GENERICA:
#
#    1. EXIGE DAR DE ALTA UN USUARIO CON CONTRASENA, y eso es del responsable.
#       No es una regla nueva de este documento: es como se ha hecho siempre en
#       este proyecto. Un agente no crea credenciales.
#
#    2. TOCA SAMBA EN UN NAS QUE LA FAMILIA USA A DIARIO. Recargar el servicio
#       corta las sesiones abiertas. Se hace cuando el responsable quiera, no a
#       mitad de una sesion de desarrollo.
#
#    3. LA COMPROBACION QUE DE VERDAD IMPORTA NO SE PUEDE HACER DESDE EL NODO.
#       "smbclient desde el propio nodo no exige firma" es la leccion que la
#       nota de ADR-0031/0038 dejo escrita: una propiedad que solo se manifiesta
#       con un cliente Windows real NO esta verificada hasta que se ejecuta con
#       un cliente Windows real.
#
#  COMO SE USA, en este orden:
#
#      # 1. El responsable crea el usuario y su contrasena, en el nodo:
#      sudo useradd -r -s /usr/sbin/nologin -M respaldo
#      sudo smbpasswd -a respaldo          # <- pide la contrasena, es suya
#
#      # 2. Este guion, en seco primero:
#      sudo ./22_privilegio_minimo_respaldo.sh --simular
#
#      # 3. Y despues de leer lo que haria:
#      sudo ./22_privilegio_minimo_respaldo.sh --aplicar
#
#      # 4. En el equipo, guardar la credencial y apuntar el cliente:
#      cmdkey /add:192.168.1.38 /user:respaldo /pass
#      #    y cambiar en 3-Config/respaldo.jsonc:
#      #      "unc": "\\\\192.168.1.38\\respaldo"
#      #      "raiz": ""            <- la comparticion YA enraiza en 01_BACKUP
#      #      "prefijoEquipo": "EQUIPO-01"
#
#      # 5. Comprobar lo que de verdad hay que comprobar, desde Windows:
#      #      - que el cliente SIGUE pudiendo escribir en su arbol
#      #      - que YA NO puede llegar a \\192.168.1.38\datos\homeUsers
#
#  REVERSION: se guarda una copia fechada de smb.conf antes de tocarlo, y
#  --revertir la restaura.
# =============================================================================

set -euo pipefail

CONF=/etc/samba/smb.conf
RAIZ=/srv/nas/datos/01_BACKUP/EQUIPO-01
USUARIO=respaldo
MARCA="# --- respaldo: capa 4, privilegio minimo (30_CLIENTE_RESPALDO seccion 7) ---"

uso() {
    sed -n '2,60p' "$0"
    exit 1
}

[ $# -eq 1 ] || uso
[ "$(id -u)" -eq 0 ] || { echo "Hay que ser root." >&2; exit 1; }

# Los seis valores de esta red — ADR-0095. Aquí solo hace falta NODO_IP, y no
# para configurar nada: para que las dos rutas UNC que este script imprime al
# final sean las de ESTE nodo. Son instrucciones que alguien teclea tal cual
# en otra máquina, así que una dirección equivocada ahí manda a comprobar el
# recurso de un equipo que no es.
AJUSTES=/etc/nas/ajustes.conf
[ -f "$AJUSTES" ] || { echo "Falta $AJUSTES. Copielo de ajustes.conf.ejemplo y editelo (ver LEEME.md)." >&2; exit 1; }
# shellcheck disable=SC1090  # ruta fija conocida, no una variable arbitraria
. "$AJUSTES"
: "${NODO_IP:?falta NODO_IP en $AJUSTES}"

case "$1" in
    --simular)  MODO=simular ;;
    --aplicar)  MODO=aplicar ;;
    --revertir) MODO=revertir ;;
    *) uso ;;
esac

if [ "$MODO" = revertir ]; then
    # find en vez de ls: shellcheck SC2012, y ademas ordena por fecha real.
    ULTIMA=$(find "$(dirname "$CONF")" -maxdepth 1 -name "$(basename "$CONF").antes-respaldo.*" -printf "%T@ %p
" 2>/dev/null | sort -rn | head -1 | cut -d" " -f2-)
    [ -n "$ULTIMA" ] || { echo "No hay copia que restaurar." >&2; exit 1; }
    cp -a "$ULTIMA" "$CONF"
    testparm -s >/dev/null
    systemctl reload smbd
    echo "Restaurado desde $ULTIMA y smbd recargado."
    exit 0
fi

# --- Comprobaciones previas. Todas abortan, ninguna arregla por su cuenta. ---

if ! id "$USUARIO" >/dev/null 2>&1; then
    echo "FALTA EL USUARIO '$USUARIO'." >&2
    echo "Lo crea el responsable, no este guion:" >&2
    echo "    sudo useradd -r -s /usr/sbin/nologin -M $USUARIO" >&2
    echo "    sudo smbpasswd -a $USUARIO" >&2
    exit 1
fi

if ! pdbedit -L 2>/dev/null | grep -q "^${USUARIO}:"; then
    echo "El usuario '$USUARIO' existe en el sistema pero NO en Samba." >&2
    echo "    sudo smbpasswd -a $USUARIO   # la contrasena es del responsable" >&2
    exit 1
fi

[ -d "$RAIZ" ] || { echo "No existe $RAIZ. El arbol del respaldo tiene que estar antes." >&2; exit 1; }

if grep -qF "$MARCA" "$CONF"; then
    echo "La comparticion [respaldo] ya esta declarada en $CONF. Nada que hacer."
    exit 0
fi

BLOQUE=$(cat <<FIN

$MARCA
# El cliente de respaldo entra por AQUI y no por [datos]. Su alcance es su
# propio arbol: no puede leer ni escribir homeUsers/ ni _HISTORICO, asi que un
# equipo comprometido no alcanza lo que no le corresponde.
[respaldo]
   comment = Solo el arbol de respaldo de EQUIPO-01
   path = $RAIZ
   browseable = no
   read only = no
   valid users = $USUARIO
   force user = nas
   force group = nas
   create mask = 0600
   directory mask = 0700
   # Sin papelera y sin ejecutables: esto es un destino de copia, no un disco
   # de trabajo. Menos superficie por lo mismo que el resto del proyecto.
   veto files = /*.exe/*.com/*.scr/*.pif/
   delete veto files = no
FIN
)

if [ "$MODO" = simular ]; then
    echo "=== SIMULACION: esto es lo que se ANADIRIA al final de $CONF ==="
    echo "$BLOQUE"
    echo
    echo "=== Y esto es lo que NO se toca ==="
    echo "  - la comparticion [datos] se queda EXACTAMENTE como esta"
    echo "  - no se borra ni se mueve ningun archivo"
    echo "  - no se recarga smbd"
    echo
    echo "Nada se ha modificado. Para aplicarlo: $0 --aplicar"
    exit 0
fi

# --- Aplicar -----------------------------------------------------------------

COPIA="${CONF}.antes-respaldo.$(date +%Y%m%d-%H%M%S)"
cp -a "$CONF" "$COPIA"
echo "Copia de seguridad: $COPIA"

printf '%s\n' "$BLOQUE" >> "$CONF"

# testparm ANTES de recargar: un smb.conf roto deja el NAS entero sin SMB, y la
# familia usa esto a diario.
if ! testparm -s >/dev/null 2>&1; then
    echo "testparm RECHAZO la configuracion. Restaurando y abortando." >&2
    cp -a "$COPIA" "$CONF"
    exit 1
fi

systemctl reload smbd
echo "Comparticion [respaldo] anadida y smbd recargado."
echo
echo "FALTA LO QUE NO SE PUEDE COMPROBAR DESDE AQUI, y es lo que de verdad importa:"
printf "%s
" "  desde Windows, comprobar que el cliente SI escribe en \\\\$NODO_IP\\respaldo"
printf "%s
" "  y que YA NO alcanza \\\\$NODO_IP\\datos\\homeUsers"
echo "  (smbclient desde el propio nodo no prueba esto: ver la nota de ADR-0031/0038)"
