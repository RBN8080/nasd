#Requires -Version 5.1
<#
    .SYNOPSIS
        El icono de la barra de tareas. Solo lee y pinta.

    .DESCRIPTION
        Contrato: 30_CLIENTE_RESPALDO.md seccion 10.2.

        SOLO LEE ESTADO.txt Y PINTA. NO EJECUTA EL RESPALDO. Cerrarlo no detiene
        nada. Y por eso ESTADO.txt se escribe de forma atomica: este proceso lo
        esta leyendo al mismo tiempo que el motor lo reescribe.

        CUADRO OSCURO CON NUCLEO QUE CAMBIA COLOR **Y** FORMA. La forma es
        deliberada: el color solo no basta (WCAG 2.2 1.4.1), y este repositorio
        ya tiene registrada esa misma preocupacion para el semaforo de la web.

          Protegido            circulo verde      fijo
          Copiando             circulo verde      pulso suave
          Requiere atencion    triangulo ambar    FIJO
          Falla                rombo rojo         PARPADEA
          Sin datos            circulo gris       fijo

        SOLO EL ROJO PARPADEA. Si el verde parpadeara cada vez que trabaja -y va
        a trabajar a diario- en dos semanas se deja de registrar el parpadeo, y
        entonces tampoco se registra el dia que sea rojo. El movimiento es un
        recurso que se gasta (ISA-18.2, fatiga de alarmas).

        DETECCION DE MOTOR CAIDO - la tabla de la seccion 10.2, entera:
          marca presente y reciente            copiando ahora          Copiando
          MARCA PRESENTE PERO VIEJA            arranco y nunca termino FALLA
          sin marca, dentro de lo esperado     termino bien            Protegido
          sin marca, ya paso la hora           no corrio               Atencion
          la tarea programada no existe o esta deshabilitada           FALLA

        PUNTO CIEGO DECLARADO: si el indicador muere no hay icono, y un icono
        ausente se parece a "todo bien". Se mitiga relanzandolo desde la tarea,
        pero EL SILENCIO DEL ICONO NUNCA ES PRUEBA DE NADA. La prueba esta en el
        tablero.

        EL ICONO ES NATIVO en el .NET que Windows ya trae: sin dependencias, sin
        tocar ADR-0013. Ese era el unico punto que podia presionar una decision
        ya tomada del repositorio, y en PowerShell desaparece (seccion 16.bis).

    .PARAMETER SegundosEntreLecturas
        Cada cuanto se relee ESTADO.txt.

    .PARAMETER HorasSinCorrerParaAvisar
        A partir de cuantas horas sin una corrida buena el icono pasa a Atencion.

    .PARAMETER UnaSolaLectura
        No abre icono: lee el estado, lo devuelve y sale. Es como lo prueban las
        pruebas, que no pueden abrir una barra de tareas.

    .EXAMPLE
        .\indicador.ps1
        .\indicador.ps1 -UnaSolaLectura
#>
[CmdletBinding()]
[OutputType([psobject])]
param(
    [ValidateRange(1, 3600)][int] $SegundosEntreLecturas = 10,
    [ValidateRange(1, 720)][int]  $HorasSinCorrerParaAvisar = 30,

    # Parametrizado y no fijo: una prueba que dependa de si la tarea REAL existe
    # en esta maquina es una prueba que pasa o falla segun el dia. Medido el
    # 2026-09-02, cuando registrar la tarea de verdad rompio su propia prueba.
    [ValidateNotNullOrEmpty()]
    [string] $NombreTarea = 'NasRespaldo-Diario',

    [ValidateNotNullOrEmpty()]
    [string] $CarpetaEstado = (Join-Path $env:LOCALAPPDATA 'NasRespaldo\estado'),

    [switch] $UnaSolaLectura
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

. "$PSScriptRoot\..\2-Nucleo\comun.ps1"

$script:NombreTarea = $NombreTarea
$script:CarpetaEstado = $CarpetaEstado

function Get-EstadoParaElIcono {
    <#
        .SYNOPSIS
            Resuelve los cinco estados de la seccion 10.2. No lanza nunca.
        .DESCRIPTION
            El orden de las preguntas ES la tabla del contrato, y esta ordenado
            de peor a mejor a proposito: lo que descubre una FALLA gana sobre lo
            que descubre normalidad. Un motor caido con la tarea deshabilitada
            tiene que salir rojo, no ambar.
        .PARAMETER HorasParaAvisar
            Cuantas horas sin corrida buena antes de pasar a Atencion.
    #>
    [CmdletBinding()]
    [OutputType([psobject])]
    param(
        [ValidateRange(1, 720)][int] $HorasParaAvisar = 30
    )

    try {
        $marca  = Get-MarcaDeCorrida -Carpeta $script:CarpetaEstado
        $estado = Read-EstadoRespaldo -Carpeta $script:CarpetaEstado

        # 1. La tarea programada no existe o esta deshabilitada -> alguien la quito.
        $tarea = Get-ScheduledTask -TaskName $script:NombreTarea -ErrorAction SilentlyContinue
        if (-not $tarea) {
            return [pscustomobject]@{ Estado = 'Falla'; Detalle = 'La tarea programada no existe: el respaldo no corre solo'; Fuente = 'tarea' }
        }
        if ($tarea.State -eq 'Disabled') {
            return [pscustomobject]@{ Estado = 'Falla'; Detalle = 'La tarea programada esta DESHABILITADA'; Fuente = 'tarea' }
        }

        # 2. Marca presente pero vieja, o con su proceso muerto -> arranco y
        #    nunca termino. Es FALLA, no "copiando".
        if ($marca.Existe -and $marca.Vieja) {
            $porque = if (-not $marca.ProcesoVivo) { 'su proceso ya no existe' } else { "lleva $($marca.Minutos) min" }
            return [pscustomobject]@{ Estado = 'Falla'; Detalle = "Una corrida arranco y nunca termino ($porque)"; Fuente = 'marca' }
        }

        # 3. Marca presente y viva -> copiando ahora.
        if ($marca.Existe) {
            return [pscustomobject]@{ Estado = 'Copiando'; Detalle = "Copiando desde hace $($marca.Minutos) min"; Fuente = 'marca' }
        }

        # 4. Sin marca: manda lo que dejo escrito la ultima corrida.
        $ultimo = '' + $estado['estado']
        if ($ultimo -eq 'SinDatos') {
            return [pscustomobject]@{ Estado = 'SinDatos'; Detalle = '' + $estado['detalle']; Fuente = 'estado' }
        }
        if ($ultimo -in @('Falla', 'Atencion')) {
            return [pscustomobject]@{ Estado = $ultimo; Detalle = '' + $estado['detalle']; Fuente = 'estado' }
        }

        # 5. La ultima fue buena: solo queda preguntar si fue hace demasiado.
        # Tipada antes de TryParse: con $null, PowerShell no resuelve el [ref].
        [datetime] $momento = [datetime]::MinValue
        $hayMomento = $false
        if ($estado.ContainsKey('momento')) {
            $hayMomento = [datetime]::TryParse(('' + $estado['momento']), [ref]$momento)
        }
        if ($hayMomento) {
            $horas = ((Get-Date) - $momento).TotalHours
            if ($horas -gt $HorasParaAvisar) {
                return [pscustomobject]@{
                    Estado  = 'Atencion'
                    Detalle = ('La ultima corrida buena fue hace {0:N0} h' -f $horas)
                    Fuente  = 'antiguedad'
                }
            }
        }
        return [pscustomobject]@{ Estado = 'Protegido'; Detalle = '' + $estado['detalle']; Fuente = 'estado' }
    }
    catch {
        # Nunca lanzar: una excepcion aqui mata el icono, y un icono ausente se
        # parece a "todo bien". Prefiero un icono gris que ninguno.
        return [pscustomobject]@{ Estado = 'SinDatos'; Detalle = "No se pudo leer el estado: $($_.Exception.Message)"; Fuente = 'error' }
    }
}

function New-IconoDeEstado {
    <#
        .SYNOPSIS
            Dibuja el icono: cuadro oscuro y nucleo con COLOR Y FORMA.
        .PARAMETER Estado
            Uno de los cinco.
        .PARAMETER Apagado
            Para el parpadeo: dibuja el nucleo tenue.
    #>
    [Diagnostics.CodeAnalysis.SuppressMessageAttribute(
        'PSUseShouldProcessForStateChangingFunctions', '',
        Justification = 'Construye un objeto Icon en memoria y lo devuelve. No toca disco, registro ni ningun estado del sistema: el verbo New- se usa aqui como en New-Object o New-TimeSpan. Anadir ShouldProcess a algo que se llama en cada tick del temporizador solo anadiria ruido.')]
    [CmdletBinding()]
    [OutputType([System.Drawing.Icon])]
    param(
        [Parameter(Mandatory)]
        [ValidateSet('Protegido', 'Copiando', 'Atencion', 'Falla', 'SinDatos')]
        [string] $Estado,
        [switch] $Apagado
    )

    $lado = 16
    $mapa = New-Object System.Drawing.Bitmap $lado, $lado
    $g = [System.Drawing.Graphics]::FromImage($mapa)
    try {
        $g.SmoothingMode = [System.Drawing.Drawing2D.SmoothingMode]::AntiAlias
        $fondo = New-Object System.Drawing.SolidBrush ([System.Drawing.Color]::FromArgb(255, 22, 26, 32))
        $g.FillRectangle($fondo, 0, 0, $lado, $lado)
        $fondo.Dispose()

        $color = switch ($Estado) {
            'Protegido' { [System.Drawing.Color]::FromArgb(255,  64, 200, 120) }
            'Copiando'  { [System.Drawing.Color]::FromArgb(255,  64, 200, 120) }
            'Atencion'  { [System.Drawing.Color]::FromArgb(255, 230, 170,  40) }
            'Falla'     { [System.Drawing.Color]::FromArgb(255, 225,  70,  70) }
            default     { [System.Drawing.Color]::FromArgb(255, 140, 145, 155) }
        }
        if ($Apagado) { $color = [System.Drawing.Color]::FromArgb(70, $color.R, $color.G, $color.B) }
        $pincel = New-Object System.Drawing.SolidBrush $color
        try {
            switch ($Estado) {
                # Triangulo: la forma de "mira esto", distinta del circulo aunque
                # se vea en blanco y negro (WCAG 2.2 1.4.1).
                'Atencion' {
                    $puntos = @(
                        (New-Object System.Drawing.Point 8, 3),
                        (New-Object System.Drawing.Point 14, 13),
                        (New-Object System.Drawing.Point 2, 13)
                    )
                    $g.FillPolygon($pincel, $puntos)
                }
                # Rombo: la forma de "esto esta roto".
                'Falla' {
                    $puntos = @(
                        (New-Object System.Drawing.Point 8, 2),
                        (New-Object System.Drawing.Point 14, 8),
                        (New-Object System.Drawing.Point 8, 14),
                        (New-Object System.Drawing.Point 2, 8)
                    )
                    $g.FillPolygon($pincel, $puntos)
                }
                default { $g.FillEllipse($pincel, 3, 3, 10, 10) }
            }
        }
        finally { $pincel.Dispose() }

        $manejador = $mapa.GetHicon()
        return [System.Drawing.Icon]::FromHandle($manejador)
    }
    finally {
        $g.Dispose()
        $mapa.Dispose()
    }
}

if ($UnaSolaLectura) {
    return (Get-EstadoParaElIcono -HorasParaAvisar $HorasSinCorrerParaAvisar)
}

# --- El icono de verdad -----------------------------------------------------
Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName System.Drawing

$icono = New-Object System.Windows.Forms.NotifyIcon
$icono.Visible = $true
$menu = New-Object System.Windows.Forms.ContextMenuStrip
$icono.ContextMenuStrip = $menu

$abrirTablero = $menu.Items.Add('Abrir el tablero')
$abrirTablero.Add_Click({
        Start-Process powershell.exe -ArgumentList @(
            '-NoExit', '-ExecutionPolicy', 'Bypass', '-File', "$PSScriptRoot\tablero.ps1")
    })
[void]$menu.Items.Add('-')
$salir = $menu.Items.Add('Cerrar el indicador (no detiene el respaldo)')

$temporizador = New-Object System.Windows.Forms.Timer
$temporizador.Interval = $SegundosEntreLecturas * 1000
$script:fase = $false
$script:ultimo = ''

$refrescar = {
    $v = Get-EstadoParaElIcono -HorasParaAvisar $HorasSinCorrerParaAvisar
    # SOLO EL ROJO PARPADEA, y el verde de "copiando" hace un pulso suave.
    $alterna = ($v.Estado -eq 'Falla')
    $script:fase = if ($alterna) { -not $script:fase } else { $false }
    $anterior = $icono.Icon
    $icono.Icon = New-IconoDeEstado -Estado $v.Estado -Apagado:$script:fase
    if ($anterior) { $anterior.Dispose() }
    $texto = '{0} - {1}' -f $v.Estado, $v.Detalle
    if ($texto.Length -gt 63) { $texto = $texto.Substring(0, 60) + '...' }
    $icono.Text = $texto
    $script:ultimo = $v.Estado
}

$temporizador.Add_Tick($refrescar)
$salir.Add_Click({
        $temporizador.Stop()
        $icono.Visible = $false
        $icono.Dispose()
        [System.Windows.Forms.Application]::Exit()
    })

& $refrescar
$temporizador.Start()
[System.Windows.Forms.Application]::Run()
