#Requires -Version 5.1
<#
    .SYNOPSIS
        El menu de teclas sobre la ventana de estado.

    .DESCRIPTION
        Contrato: 30_CLIENTE_RESPALDO.md seccion 10.1.

        NINGUNA INTERFAZ ES EL UNICO CAMINO: el motor tiene que correr sin menu
        y sin icono desde la tarea programada, o nada se podria automatizar
        (seccion 10). Este archivo NO contiene logica de respaldo: llama al
        motor, igual que lo llamaria la tarea.

        SUPERFICIE DE ATAQUE: ninguna nueva. Proceso local, sesion del usuario,
        sin puerto ni servicio. Lo que pudiera ejecutar el menu ya podia
        ejecutar el respaldo.

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

        EL ESTADO LO RESUELVE EL INDICADOR, NO ESTE ARCHIVO, y es a proposito:
        si el tablero calculara el suyo, la barra y la ventana podrian contar
        historias distintas del mismo momento y no habria forma de saber cual
        creer. Se invoca con -UnaSolaLectura, SIN CARGARLO CON PUNTO: indicador
        .ps1 tiene bloque param(), y cargar con punto un guion con param()
        reinicia esas variables en quien lo carga. Asi se convirtio una
        simulacion en una copia real el 2026-09-02 (seccion 12.undecies).

        EL DIBUJO VIVE EN estilo.ps1. Aqui solo se decide QUE se ensena; COMO se
        ve es de la otra capa, para que cambiar el aspecto no obligue a tocar el
        archivo que lanza copias.

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
. "$PSScriptRoot\estilo.ps1"

$script:NombreTarea    = 'NasRespaldo-Diario'
$script:NombreIndicador = 'NasRespaldo-Indicador'

$script:Capacidades = Initialize-Consola
$script:Paleta      = Get-Paleta -Capacidades $script:Capacidades
$script:Trazo       = Get-TrazoDeMarco -Unicode $script:Capacidades.Unicode

function Get-EstadoDelIndicador {
    <#
        .SYNOPSIS
            Pregunta al indicador en que estado esta. Nunca lanza.
        .DESCRIPTION
            Se llama al guion, no se carga con punto. Si algo falla se devuelve
            SinDatos: una ventana que no abre porque el estado no se pudo leer
            es peor que una ventana que dice que no lo sabe.
    #>
    [CmdletBinding()]
    [OutputType([psobject])]
    param()

    try {
        $v = & "$PSScriptRoot\indicador.ps1" -UnaSolaLectura
        if ($v) { return $v }
    }
    catch {
        Write-Verbose "El indicador no pudo resolver el estado: $($_.Exception.Message)"
    }
    return [pscustomobject]@{ Estado = 'SinDatos'; Detalle = 'No se pudo leer el estado'; Fuente = 'error' }
}

function Write-Campo {
    <#
        .SYNOPSIS
            Escribe una fila etiqueta/valor dentro del marco.
        .PARAMETER Etiqueta
            Nombre del campo.
        .PARAMETER Valor
            Contenido.
        .PARAMETER Color
            Codigo de color para el valor. Vacio lo deja sin colorear.
    #>
    [CmdletBinding()]
    [OutputType([void])]
    param(
        [Parameter(Mandatory)][string] $Etiqueta,
        [Parameter(Mandatory)][AllowEmptyString()][string] $Valor,
        [string] $Color = ''
    )

    $pintado = if ($Color) { '{0}{1}{2}' -f $Color, $Valor, $script:Paleta.Fin } else { $Valor }
    $campo = Format-Campo -Etiqueta $Etiqueta -Valor $pintado -Paleta $script:Paleta -VisibleValor $Valor.Length
    Write-Information (Format-LineaDeTablero -Trazo $script:Trazo -Paleta $script:Paleta `
            -Texto $campo.Texto -Visible $campo.Visible) -InformationAction Continue
}

function Write-Separador {
    <#
        .SYNOPSIS
            Una linea del marco con su titulo incrustado.
        .PARAMETER Titulo
            Nombre del bloque.
        .PARAMETER Posicion
            Superior, Union o Inferior.
        .PARAMETER Derecha
            Texto pegado al extremo derecho.
    #>
    [CmdletBinding()]
    [OutputType([void])]
    param(
        [string] $Titulo = '',
        [ValidateSet('Superior', 'Union', 'Inferior')][string] $Posicion = 'Union',
        [string] $Derecha = ''
    )
    Write-Information (Format-LineaDeMarco -Trazo $script:Trazo -Paleta $script:Paleta `
            -Titulo $Titulo -Posicion $Posicion -Derecha $Derecha) -InformationAction Continue
}

function Show-Ventana {
    <#
        .SYNOPSIS
            Pinta la ventana de estado y el menu.
        .PARAMETER Configuracion
            El objeto de configuracion completo.
    #>
    [CmdletBinding()]
    [OutputType([void])]
    param(
        [Parameter(Mandatory)][psobject] $Configuracion
    )

    $v          = Get-EstadoDelIndicador
    $estado     = Read-EstadoRespaldo
    $simbolo    = Get-Simbolo -Estado $v.Estado -Unicode $script:Capacidades.Unicode
    $colorEstado = Get-ColorDeEstado -Estado $v.Estado -Paleta $script:Paleta
    $p          = $script:Paleta

    Write-Information '' -InformationAction Continue
    Write-Separador -Titulo 'Respaldo del NAS' -Posicion 'Superior' -Derecha (Get-Date -Format 'dd/MM HH:mm')
    Write-Information (Format-LineaDeTablero -Trazo $script:Trazo -Paleta $p) -InformationAction Continue

    # EL SIMBOLO VA ANTES QUE EL COLOR, no al reves: en una terminal sin color
    # la linea tiene que seguir diciendo lo mismo (seccion 10.2).
    # Get-Simbolo ya trae su propio espacio a cada lado, y su ancho cambia entre
    # Unicode y ASCII: no se le suman espacios aqui o la sangria bailaria segun
    # la terminal.
    $titular = '  {0}{1}{2}{3}' -f $colorEstado, $simbolo, $v.Estado.ToUpperInvariant(), $p.Fin
    Write-Information (Format-LineaDeTablero -Trazo $script:Trazo -Paleta $p `
            -Texto $titular -Visible (2 + $simbolo.Length + $v.Estado.Length)) -InformationAction Continue

    $detalle = '      {0}' -f $v.Detalle
    Write-Information (Format-LineaDeTablero -Trazo $script:Trazo -Paleta $p `
            -Texto $detalle -Visible $detalle.Length) -InformationAction Continue

    $momento = '' + $estado['momento']
    [datetime] $cuando = [datetime]::MinValue
    if ([datetime]::TryParse($momento, [ref] $cuando)) {
        $horas = ((Get-Date) - $cuando).TotalHours
        $linea = '      ultima corrida buena  {0:dd/MM HH:mm}  (hace {1:N0} h)' -f $cuando, $horas
        Write-Information (Format-LineaDeTablero -Trazo $script:Trazo -Paleta $p `
                -Texto ('{0}{1}{2}' -f $p.Tenue, $linea, $p.Fin) -Visible $linea.Length) -InformationAction Continue
    }
    Write-Information (Format-LineaDeTablero -Trazo $script:Trazo -Paleta $p) -InformationAction Continue

    # --- Destinos ---------------------------------------------------------
    Write-Separador -Titulo 'Destinos'
    $unc = $Configuracion.destinos.nodo.unc
    # Test-Path directo y no Test-DestinoNodo: pintar la ventana no debe
    # escribir lineas de ERROR en el registro de operacion.
    $nodoVivo = Test-Path -LiteralPath $unc -ErrorAction SilentlyContinue
    if ($nodoVivo) { Write-Campo -Etiqueta 'Nodo' -Valor ('{0}  responde' -f $unc) -Color $p.Verde }
    else           { Write-Campo -Etiqueta 'Nodo' -Valor ('{0}  NO RESPONDE' -f $unc) -Color $p.Rojo }

    $disco = Get-DiscoFrio -Configuracion $Configuracion 2>$null
    if ($disco) {
        $marcaDisco = Get-Item -LiteralPath $disco.Centinela -ErrorAction SilentlyContinue
        $texto = '{0}  serie {1}' -f $disco.Raiz, $disco.Serie
        if ($marcaDisco) {
            $dias = [int]((Get-Date) - $marcaDisco.LastWriteTime).TotalDays
            $texto += '  escrito hace {0} d' -f $dias
        }
        Write-Campo -Etiqueta 'Disco frio' -Valor $texto -Color $p.Verde
    }
    else {
        # El disco se conecta A PETICION (seccion 6.3): no hay calendario y es
        # deliberado. Que no este no es una falla, asi que no se pinta en rojo.
        Write-Campo -Etiqueta 'Disco frio' -Valor 'no conectado' -Color $p.Gris
    }

    # --- Automatismo ------------------------------------------------------
    Write-Separador -Titulo 'Automatismo'
    foreach ($par in @(
            @{ Eti = 'Motor'; Tarea = $script:NombreTarea;     Falta = 'NO EXISTE - el respaldo no corre solo' },
            @{ Eti = 'Icono'; Tarea = $script:NombreIndicador; Falta = 'NO EXISTE - el icono no arranca solo' })) {
        $t = Get-ScheduledTask -TaskName $par.Tarea -ErrorAction SilentlyContinue
        if (-not $t) {
            Write-Campo -Etiqueta $par.Eti -Valor $par.Falta -Color $p.Rojo
        }
        elseif ($t.State -eq 'Disabled') {
            Write-Campo -Etiqueta $par.Eti -Valor ('{0}  DESHABILITADA' -f $par.Tarea) -Color $p.Ambar
        }
        else {
            Write-Campo -Etiqueta $par.Eti -Valor ('{0}  {1}' -f $par.Tarea, $t.State) -Color $p.Verde
        }
    }

    # --- Protecciones -----------------------------------------------------
    # El recordatorio va por la derecha y NO dentro del titulo: el titulo se
    # escribe en mayusculas, y una ruta de archivo en mayusculas deja de ser
    # una ruta que alguien pueda copiar.
    Write-Separador -Titulo 'Protecciones' -Derecha 'se cambian en 3-Config/respaldo.jsonc'
    $minimo = if ($Configuracion.freno.PSObject.Properties.Name -contains 'minimoArchivosParaFrenar') {
        $Configuracion.freno.minimoArchivosParaFrenar
    } else { 0 }
    Write-Campo -Etiqueta 'Freno' -Valor ('{0} % y minimo de {1} archivos' -f $Configuracion.freno.umbralPorcentajeDeArchivosQueCambian, $minimo)
    Write-Campo -Etiqueta 'Centinelas' -Valor ('{0} declarados' -f @($Configuracion.centinelas).Count)
    Write-Campo -Etiqueta 'Clases' -Valor ('{0} contenedores, {1} raices declaradas' -f @($Configuracion.contenedores).Count, @($Configuracion.raicesDeclaradas).Count)
    Write-Campo -Etiqueta 'Borrado' -Valor 'clase A nunca borra  -  clase B espeja'
    Write-Separador -Posicion 'Inferior'

    # --- Menu -------------------------------------------------------------
    Write-Information '' -InformationAction Continue
    Write-Information ('   {0}COPIAR{1}                  {0}COMPROBAR{1}                 {0}SISTEMA{1}' -f $p.Fuerte, $p.Fin) -InformationAction Continue
    Write-Information '   [1] al nodo             [3] verificar el nodo     [5] semilla' -InformationAction Continue
    Write-Information '   [2] simular             [4] estado completo       [6] tareas' -InformationAction Continue
    Write-Information '   [7] al disco frio       [9] revisar el disco      [S] salir' -InformationAction Continue
    Write-Information '   [8] simular el disco' -InformationAction Continue
    Write-Information '' -InformationAction Continue
}

function Show-RevisionDelDisco {
    <#
        .SYNOPSIS
            Ensena lo que la verificacion encontro en el disco frio.
        .DESCRIPTION
            NO REPARA. Ensena y dice como se repara, porque reparar pisa
            archivos y ese gesto es de una persona (seccion 10.1: el menu lanza
            acciones, no relaja protecciones).
        .PARAMETER Revision
            Lo que devuelve Test-DiscoFrioEnVuelo.
    #>
    [CmdletBinding()]
    [OutputType([void])]
    param(
        [Parameter(Mandatory)][AllowNull()][psobject] $Revision
    )

    $p = $script:Paleta
    if (-not $Revision) {
        Write-Warning 'El disco frio no esta conectado: no se reviso nada.'
        return
    }

    Write-Information ('  Raices revisadas: {0}' -f @($Revision.Revisadas).Count) -InformationAction Continue
    foreach ($s in @($Revision.Saltadas)) {
        Write-Warning ('NO REVISADO ({0}): {1}' -f $s.Lado, $s.Motivo)
    }

    $rotos = @($Revision.EnVuelo)
    if ($rotos.Count -eq 0 -and @($Revision.Saltadas).Count -eq 0) {
        Write-Information ('  {0}Sin archivos a medias. El disco esta cuadrado.{1}' -f $p.Verde, $p.Fin) -InformationAction Continue
        return
    }
    if ($rotos.Count -eq 0) {
        Write-Warning 'Sin hallazgos EN LO QUE SE PUDO MIRAR. No es lo mismo que "limpio".'
        return
    }

    Write-Information ('  {0}{1} archivo(s) a medias de una copia cortada.{2}' -f $p.Rojo, $rotos.Count, $p.Fin) -InformationAction Continue
    $rotos | Select-Object Motivo, TamanoOrigen, TamanoDestino, Relativa |
        Format-Table -AutoSize | Out-String -Width 200 | Write-Information -InformationAction Continue
    Write-Information '  Se reparan con Repair-ArchivosEnVuelo (2-Nucleo/comun.ps1): sobrescribe, nunca borra.' -InformationAction Continue
}

$parametros = @{}
if ($PSBoundParameters.ContainsKey('RutaConfiguracion')) { $parametros['Ruta'] = $RutaConfiguracion }
$configuracion = Get-ConfiguracionRespaldo @parametros
$rutaConfig = if ($PSBoundParameters.ContainsKey('RutaConfiguracion')) { $RutaConfiguracion } else { $null }

$seguir = $true
while ($seguir) {
    Show-Ventana -Configuracion $configuracion
    $tecla = Read-Host '   Elija'
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
            foreach ($n in @($script:NombreTarea, $script:NombreIndicador)) {
                $t = Get-ScheduledTask -TaskName $n -ErrorAction SilentlyContinue
                if ($t) { $t | Get-ScheduledTaskInfo | Format-List | Out-String | Write-Information -InformationAction Continue }
                else { Write-Warning "La tarea '$n' NO existe." }
            }
        }
        '7' {
            # Las DOS pasadas. Si el nodo no responde corre solo la del equipo y
            # la copia queda declarada INCOMPLETA: nunca se dice "al dia" a una
            # copia fria a la que le falto la mitad (seccion 6.2).
            $rd = & "$nucleo\disco.ps1" @comunes -Confirm:$false
            if ($rd.Abortada) { Write-Warning $rd.Motivo }
            elseif (-not $rd.Completa) {
                Write-Warning 'COPIA FRIA INCOMPLETA: corrio solo la pasada equipo -> disco. Lo que solo vive en el nodo NO llego.'
            }
            else { Write-Information '   Copia fria COMPLETA: las dos pasadas corrieron.' -InformationAction Continue }
        }
        '8' { & "$nucleo\disco.ps1" @comunes -SoloSimular -Confirm:$false | Format-List | Out-String -Width 200 | Write-Information -InformationAction Continue }
        '9' {
            # Busca lo que una copia cortada dejo a medias. Existe porque un
            # apagon el 2026-09-02 dejo ocho archivos con el tamano correcto y
            # el contenido distinto, y /XO los habria saltado para siempre.
            Write-Information '   Revisando el disco (recorre las dos rutas: puede tardar)...' -InformationAction Continue
            $r = & "$nucleo\verificar.ps1" @comunes -RevisarDisco -TamanoMuestra 0
            Show-RevisionDelDisco -Revision $r.DiscoEnVuelo
        }
        'S' { $seguir = $false }
        default { Write-Warning 'Opcion no reconocida.' }
    }
}
