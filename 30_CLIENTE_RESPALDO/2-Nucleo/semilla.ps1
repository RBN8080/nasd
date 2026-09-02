# ---------------------------------------------------------------------------
#  semilla.ps1  -  En vez de copiar programas, copiar la lista de lo que habia.
#
#  Contrato: 30_CLIENTE_RESPALDO.md seccion 5. Aplicacion directa de seccion 3.2:
#  "la fuente puede reconstruir esto sin mi?" Si -> no se respalda nada.
#
#  Se REGENERA en cada corrida. Total: menos de 1 MB.
#    winget export                      lista de programas instalados  < 200 KB
#    extensiones de VS Code (son 55)                                   <   5 KB
#    ajustes y atajos de VS Code                                       < 100 KB
#    .vmx de las maquinas virtuales     en vez de 240 GB de discos     <   1 MB
#    las 18 tareas programadas                                         < 500 KB
#    .gitconfig y llaves SSH                                           <  20 KB
#
#  HALLAZGO DEL 2026-08-27: la sincronizacion de configuracion de VS Code esta
#  APAGADA. Por seccion 3.2 eso significa que la fuente NO puede reponer la lista de
#  55 extensiones, asi que la semilla es obligatoria para ese caso.
#
#  Las llaves SSH van a _CONFIGS, que esta SIN POBLAR a proposito (seccion 8) y sigue
#  deshabilitada en respaldo.jsonc hasta que el ADR de secretos lo resuelva.
#
#  NIVELES (seccion 5.1). Arrancar en nivel 1 (Fase 2), llegar a nivel 3 (Fase 4):
#    nada       ~3 dias   |  1 inventario  ~6 h  |  2 entorno ~3 h  |  3 ejecutable ~1 h
#  El salto de 3 h a 1 h NO viene de guardar mas: entre el 2 y el 3 no se
#  guarda ni un dato nuevo. La diferencia es que la lista deja de leerse y
#  empieza a ejecutarse.
#
#  Fase 0: esqueleto. Aqui no hay implementacion todavia.
# ---------------------------------------------------------------------------

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

# Pendiente de Fase 2.
