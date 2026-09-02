# ---------------------------------------------------------------------------
#  notificar.ps1  -  LA COSTURA con el nodo. seccion 11 lo nombra por su nombre y dice
#  que es el UNICO archivo que cambia si el transporte cambia.
#
#  Contrato: 30_CLIENTE_RESPALDO.md seccion 11.
#
#  NO SE CONSTRUYE UN NOTIFICADOR NUEVO. 10_CODIGO/internal/aviso ya existe con
#  severidades, politica, agrupacion por situacion y pruebas, y ADR-0073 /
#  ADR-0074 ya establecieron Telegram y el testigo externo.
#
#  EL CLIENTE SOLO EMITE EL EVENTO; EL NODO DECIDE QUE HACER CON EL.
#
#    OK              silencioso
#    ATENCION        entra al resumen agrupado
#    FRENO / ERROR   avisa siempre
#
#  REGLA DE RUIDO: el exito diario NO notifica. Una alerta que llega todos los
#  dias deja de leerse justo antes del dia que importaba.
#
#  LO QUE HAY QUE LEER ANTES DE ESCRIBIR ESTE ARCHIVO, y seccion 16.bis lo midio:
#  internal/aviso NO es un notificador generico, es EL EVALUADOR DE SITUACIONES
#  DEL NODO - 3 de sus 15 archivos tocan Linux (evaluar.go, situacion.go).
#  De leer situacion.go sale si su catalogo es extensible o si asume las
#  situaciones del nodo, y por tanto si el cliente se cuelga de el o solo emite
#  y el nodo evalua. Es lo unico del contrato definido como interfaz y no como
#  implementacion.
#
#  Fase 0: esqueleto. Aqui no hay implementacion todavia.
# ---------------------------------------------------------------------------

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

# Pendiente de Fase 3.
