#Requires -Version 5.1
<#
    .SYNOPSIS
        El menu de teclas sobre la ventana de estado.

    .DESCRIPTION
        Contrato: 30_CLIENTE_RESPALDO.md seccion 10.1.

        NINGUNA INTERFAZ ES EL UNICO CAMINO: el motor tiene que correr sin menu y
        sin icono desde la tarea programada, o nada se podria automatizar
        (seccion 10). Este archivo NO contiene logica de respaldo: llama al
        motor, igual que lo llamaria la tarea.

        SUPERFICIE DE ATAQUE: ninguna nueva. Proceso local, sesion del usuario,
        sin puerto ni servicio. Lo que pudiera ejecutar el menu ya podia ejecutar
        el respaldo.

        EL RIESGO REAL ES OTRO: que el menu permita debilitar la proteccion sin
        que se note. Por eso el reparto NO es negociable, y este archivo lo
        respeta por omision -aqui no hay ninguna opcion que toque las cuatro de
        la derecha-:

          DESDE EL MENU              SOLO EDITANDO respaldo.jsonc
          hora de corrida            UMBRAL DEL FRENO
          pausar raices              CENTINELAS
          verbosidad                 POLITICA DE BORRADO
          lanzar acciones            CLASES

        La ventana ENSENA esos cuatro valores para que se puedan mirar, y dice
        donde se cambian. Ver no es poder cambiar.

    .PARAMETER RutaConfiguracion
        Configuracion a usar. Por omision la de 3-Config.

    .EXAMPLE
        .\tablero.ps1
#>
[CmdletBinding()]
[OutputType([void])]
param(
    [ValidateNotNullOrEmpty()]
    [string] $RutaConfiguracion
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$nucleo = Join-Path (Split-Path $PSScriptRoot -Parent) '2-Nucleo'
. "$nucleo\comun.ps1"

$script:NombreTarea = 'NasRespaldo-Diario'

function Show-Ventana {
    <#
        .SYNOPSIS
            Pinta la ventana de estado y devuelve el menu disponible.
        .PARAMETER Configuracion
            El objeto de configuracion completo.
    #>
    [CmdletBinding()]
    [OutputType([void])]
    param(
        [Parameter(Mandatory)][psobject] $Configuracion
    )

    $estado = Read-EstadoRespaldo
    $marca  = Get-MarcaDeCorrida
    $tarea  = Get-ScheduledTask -TaskName $script:NombreTarea -ErrorAction SilentlyContinue

    Write-Information '' -InformationAction Continue
    Write-Information '=====================================================================' -InformationAction Continue
    Write-Information '  RESPALDO DEL NAS - tablero' -InformationAction Continue
    Write-Information '=====================================================================' -InformationAction Continue
    Write-Information ('  Estado    : {0}' -f $estado['estado']) -InformationAction Continue
    Write-Information ('  Momento   : {0}' -f $estado['momento']) -InformationAction Continue
    Write-Information ('  Detalle   : {0}' -f $estado['detalle']) -InformationAction Continue
    if ($marca.Existe) {
        Write-Information ('  EN CURSO  : desde hace {0} min (pid {1}, proceso vivo: {2})' -f $marca.Minutos, $marca.Pid, $marca.ProcesoVivo) -InformationAction Continue
    }
    $textoTarea = if ($tarea) { $tarea.State } else { 'NO EXISTE - el respaldo no corre solo' }
    Write-Information ('  Tarea     : {0}' -f $textoTarea) -InformationAction Continue
    Write-Information '' -InformationAction Continue

    Write-Information '  --- Solo se cambian editando 3-Config/respaldo.jsonc (seccion 10.1) ---' -InformationAction Continue
    $minimo = if ($Configuracion.freno.PSObject.Properties.Name -contains 'minimoArchivosParaFrenar') { $Configuracion.freno.minimoArchivosParaFrenar } else { 0 }
    Write-Information ('  Freno     : {0} % y minimo de {1} archivos' -f $Configuracion.freno.umbralPorcentajeDeArchivosQueCambian, $minimo) -InformationAction Continue
    Write-Information ('  Centinelas: {0} declarados' -f @($Configuracion.centinelas).Count) -InformationAction Continue
    Write-Information ('  Clases    : {0} contenedores, {1} raices declaradas' -f @($Configuracion.contenedores).Count, @($Configuracion.raicesDeclaradas).Count) -InformationAction Continue
    Write-Information '' -InformationAction Continue

    Write-Information '  [1] Copiar al nodo                 [4] Ver el estado completo' -InformationAction Continue
    Write-Information '  [2] Simular (no copia nada)        [5] Regenerar la semilla' -InformationAction Continue
    Write-Information '  [3] Verificar contra el nodo       [6] Ver la tarea programada' -InformationAction Continue
    Write-Information '  [S] Salir' -InformationAction Continue
    Write-Information '' -InformationAction Continue
}

$parametros = @{}
if ($PSBoundParameters.ContainsKey('RutaConfiguracion')) { $parametros['Ruta'] = $RutaConfiguracion }
$configuracion = Get-ConfiguracionRespaldo @parametros
$rutaConfig = if ($PSBoundParameters.ContainsKey('RutaConfiguracion')) { $RutaConfiguracion } else { $null }

$seguir = $true
while ($seguir) {
    Show-Ventana -Configuracion $configuracion
    $tecla = Read-Host '  Elija'
    $comunes = @{}
    if ($rutaConfig) { $comunes['RutaConfiguracion'] = $rutaConfig }

    switch ($tecla.Trim().ToUpperInvariant()) {
        '1' {
            # AUTORIZAR EL FRENO NO ES UNA OPCION DEL MENU y es deliberado: si el
            # freno salta, quien lo autoriza tiene que verlo primero y volver a
            # lanzar con -AutorizarFreno desde la consola. Un menu que ofreciera
            # "copiar de todos modos" convertiria la defensa en un clic.
            & "$nucleo\respaldo.ps1" @comunes -Confirm:$false | Out-Null
        }
        '2' { & "$nucleo\respaldo.ps1" @comunes -SoloSimular -Confirm:$false | Out-Null }
        '3' { & "$nucleo\verificar.ps1" @comunes | Format-List | Out-String -Width 200 | Write-Information -InformationAction Continue }
        '4' { Read-EstadoRespaldo | Format-Table -AutoSize | Out-String | Write-Information -InformationAction Continue }
        '5' {
            $destino = '{0}\{1}\{2}\_SEMILLA' -f `
                $configuracion.destinos.nodo.unc.TrimEnd('\'),
                $configuracion.destinos.nodo.raiz,
                $configuracion.destinos.nodo.prefijoEquipo
            & "$nucleo\semilla.ps1" -Destino $destino -Confirm:$false | Format-List | Out-String | Write-Information -InformationAction Continue
        }
        '6' {
            $t = Get-ScheduledTask -TaskName $script:NombreTarea -ErrorAction SilentlyContinue
            if ($t) { $t | Get-ScheduledTaskInfo | Format-List | Out-String | Write-Information -InformationAction Continue }
            else { Write-Warning 'La tarea programada NO existe. El respaldo no corre solo.' }
        }
        'S' { $seguir = $false }
        default { Write-Warning 'Opcion no reconocida.' }
    }
}
