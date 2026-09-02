# ---------------------------------------------------------------------------
#  tablero.ps1  -  El menu de teclas sobre la ventana de estado.
#
#  Contrato: 30_CLIENTE_RESPALDO.md seccion 10.1.
#
#  NINGUNA INTERFAZ ES EL UNICO CAMINO: el motor tiene que correr sin menu y
#  sin icono desde la tarea programada, o nada se podria automatizar (seccion 10).
#
#  Acciones: copiar al nodo, copiar al disco, verificar huellas, probar
#  restauracion, ajustes.
#
#  SUPERFICIE DE ATAQUE: ninguna nueva. Proceso local, sesion del usuario, sin
#  puerto ni servicio. Lo que pudiera ejecutar el menu ya podia ejecutar el
#  respaldo.
#
#  EL RIESGO REAL ES OTRO: que el menu permita debilitar la proteccion sin que
#  se note. Por eso el reparto NO es negociable:
#
#    DESDE EL MENU              SOLO EDITANDO respaldo.jsonc
#    hora de corrida            UMBRAL DEL FRENO
#    pausar raices              CENTINELAS
#    verbosidad                 POLITICA DE BORRADO
#    lanzar acciones            CLASES
#
#  Cada parametro de proteccion lleva encima un comentario explicando que pasa
#  si se cambia, y toda modificacion queda registrada y notificada.
#
#  Fase 0: esqueleto. Aqui no hay implementacion todavia.
# ---------------------------------------------------------------------------

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

# Pendiente de Fase 3.
