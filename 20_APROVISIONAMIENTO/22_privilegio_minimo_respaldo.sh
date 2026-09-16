#!/bin/bash
# =============================================================================
#  22_privilegio_minimo_respaldo.sh
#
#  LAYER 4 of 30_CLIENTE_RESPALDO.md section 7: least privilege on the share.
#
#  THE PROBLEM IT SOLVES, measured on the node on 2026-09-02:
#
#    /etc/samba/smb.conf has ONE SINGLE share, [datos], with
#    "valid users = nas" and "path = /srv/nas/datos". The backup client signs in
#    with that credential, so TODAY IT CAN WRITE AND DELETE ACROSS THE WHOLE
#    NAS: everyone's home directories, _HISTORICO with its 141 GB, and the
#    phone backup.
#
#    Section 7 says it plainly: "automatic backup IS a privilege. If the
#    workstation can write and delete on the node unattended, anything that
#    controls the workstation can too." That is what is bounded here.
#
#  WHAT IT DOES: it adds a SECOND share, [respaldo], rooted at
#  01_BACKUP/EQUIPO-01 and reachable only by a user of its own. The client
#  moves to that one, and stops having reach over the rest of the tree.
#
#  ------------------------------------------------------------------------
#  THIS SCRIPT DOES NOT RUN ON ITS OWN, AND THE REASON IS NOT GENERIC CAUTION:
#
#    1. IT REQUIRES CREATING A USER WITH A PASSWORD, and that belongs to the
#       owner. This is not a new rule of this document: it is how it has always
#       been done in this project. An agent does not create credentials.
#
#    2. IT TOUCHES SAMBA ON A NAS USED DAILY. Reloading the service cuts open
#       sessions. It is done when the owner wants, not mid development session.
#
#    3. THE CHECK THAT REALLY MATTERS CANNOT BE DONE FROM THE NODE.
#       "smbclient from the node itself does not require signing" is the lesson
#       the ADR-0031/0038 note left written: a property that only shows up with
#       a real Windows client is NOT verified until it is run with a real
#       Windows client.
#
#  HOW IT IS USED, in this order:
#
#      # 1. The owner creates the user and its password, on the node:
#      sudo useradd -r -s /usr/sbin/nologin -M respaldo
#      sudo smbpasswd -a respaldo          # <- asks for the password, it is theirs
#
#      # 2. This script, dry run first:
#      sudo ./22_privilegio_minimo_respaldo.sh --simular
#
#      # 3. And after reading what it would do:
#      sudo ./22_privilegio_minimo_respaldo.sh --aplicar
#
#      # 4. On the workstation, save the credential and point the client at it:
#      cmdkey /add:192.168.1.38 /user:respaldo /pass
#      #    and change in 3-Config/respaldo.jsonc:
#      #      "unc": "\\\\192.168.1.38\\respaldo"
#      #      "raiz": ""            <- the share ALREADY roots at 01_BACKUP
#      #      "prefijoEquipo": "EQUIPO-01"
#
#      # 5. Check what really has to be checked, from Windows:
#      #      - that the client CAN STILL write in its own tree
#      #      - that it can NO LONGER reach \\192.168.1.38\datos\homeUsers
#
#  ROLLBACK: a dated copy of smb.conf is kept before touching it, and
#  --revertir restores it.
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
