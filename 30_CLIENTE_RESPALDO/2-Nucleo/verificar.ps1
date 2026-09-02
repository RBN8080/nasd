# ---------------------------------------------------------------------------
#  verificar.ps1  -  Comprueba. Y no afirma lo que no comprobo.
#
#  Contrato: 30_CLIENTE_RESPALDO.md seccion 9.
#
#  TRES NIVELES DE CERTEZA QUE NO DEBEN CONFUNDIRSE:
#    "Corrio"                  el codigo de salida            costo nulo
#    "Coincide"                comparacion en seco con /L     costo segundos
#    "El contenido es identico" huella criptografica          costo alto
#                                                             -> por MUESTREO semanal
#
#  estado.cmd no lee un registro viejo: hace la comparacion real contra los dos
#  destinos EN ESE MOMENTO, y por eso puede afirmar lo que afirma.
#
#  DOS TRAMPAS MEDIDAS EL 2026-09-02, y las dos costaron tiempo real:
#
#  1. NORMALIZAR LOS DOS LADOS ANTES DE COMPARAR. PowerShell escribe el MD5 en
#     MAYUSCULAS y md5sum en minusculas. Salieron "27 508 diferencias" que eran
#     cero. Paso DOS VECES: la segunda, un tr 'A-F' 'a-f' que solo bajaba los
#     digitos hex y dejaba DJI como dJI. Un cotejo que sale 100 % distinto casi
#     nunca es un desastre: casi siempre es formato.
#
#  2. EN EL NODO, POR LOTES. Un proceso md5sum por archivo proyectaba 49 min
#     para 27 508 archivos; por lotes de 500 fueron minutos. Sobre un Pi, el
#     coste de ARRANCAR PROCESOS domina cuando los archivos son pequenos.
#
#  Y una velocidad que no cuadra con lo medido es un fallo hasta que se
#  demuestre lo contrario (seccion 12.septies). Techo medido PC -> nodo:
#     bloque grande     13.4 MB/s   - el techo es el Pi 3B+, no SMB
#     archivos pequenos 60.2 arch/s - manda el conteo, no los bytes
#
#  Fase 0: esqueleto. Aqui no hay implementacion todavia.
# ---------------------------------------------------------------------------

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

# Pendiente de Fase 2.
