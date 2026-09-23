#Requires -Version 5.1
<#
    panel.ps1  -  La ventana de estado pintada con Spectre, cuando se puede.

    ADR-0097. Solo el tablero lo carga, y solo pinta si Initialize-PanelRico dice
    que si: PowerShell 7.4 o mas y el modulo PwshSpectreConsole. Si no, el
    tablero pinta la ventana de siempre y esto no cambia nada.

    NO DECIDE NADA. Que se ensena y de que tono lo deciden Get-DatosDeVentana y
    Get-FilasDeTabla (tablero.ps1). Aqui cada tono se traduce a su color, y los
    colores salen de Get-Paleta (estilo.ps1): son los del panel del nodo.

    SE ESCRIBE ANALIZABLE POR 5.1 Y EN ASCII (ADR-0078 punto 5): la bateria de
    pruebas corre en 5.1 y carga este archivo.

    Uso: se carga con punto desde el tablero.
        . "$PSScriptRoot\panel.ps1"
#>

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

function Initialize-PanelRico {
    <#
        .SYNOPSIS
            Dice si se puede pintar con Spectre, y lo deja listo si se puede.
        .DESCRIPTION
            Nunca lanza. Sin PowerShell 7.4, o si el modulo no carga, devuelve
            falso y el tablero pinta la ventana de siempre: un modulo de galeria
            que se actualiza y se rompe no puede dejar al tablero sin ventana.
    #>
    [CmdletBinding()]
    [OutputType([bool])]
    param()

    if ($PSVersionTable.PSVersion -lt [version]'7.4') { return $false }
    try {
        Import-Module PwshSpectreConsole -ErrorAction Stop -Verbose:$false
    }
    catch {
        Write-Verbose "Sin panel rico: $($_.Exception.Message)"
        return $false
    }
    $script:ColoresPanel = Get-ColoresDelPanel
    return $true
}

function Get-ColoresDelPanel {
    <#
        .SYNOPSIS
            Los colores de Get-Paleta en la forma que entiende Spectre: #rrggbb.
        .DESCRIPTION
            SE DERIVAN, NO SE COPIAN. Get-Paleta ya lleva los colores del panel
            del nodo, y una prueba los compara con estilo.css. Escribirlos otra
            vez aqui seria la segunda copia, la que un dia se separa.
    #>
    [CmdletBinding()]
    [OutputType([hashtable])]
    param()

    $p = Get-Paleta -Capacidades ([pscustomobject]@{ Color = $true; Unicode = $true })
    $colores = @{}
    foreach ($tono in @('Marco', 'Titulo', 'Etiqueta', 'Valor', 'Tenue', 'Verde', 'Ambar', 'Rojo', 'Gris')) {
        if ($p[$tono] -match '38;2;(\d+);(\d+);(\d+)') {
            $colores[$tono] = '#{0:x2}{1:x2}{2:x2}' -f [int]$Matches[1], [int]$Matches[2], [int]$Matches[3]
        }
    }
    return $colores
}

function Format-Marcado {
    <#
        .SYNOPSIS
            Texto escapado y pintado con un tono, listo para el marcado de Spectre.
        .DESCRIPTION
            EL ESCAPE VA SIEMPRE. Las rutas, los detalles de ESTADO.txt y el
            propio menu -"[1] al nodo"- llevan corchetes, y Spectre los leeria
            como marcado: en el mejor caso se pierde texto, en el peor lanza.
        .PARAMETER Texto
            Lo que se ensena, tal cual.
        .PARAMETER Tono
            Una clave de Get-ColoresDelPanel.
        .PARAMETER Negrita
            Para rotulos.
    #>
    [CmdletBinding()]
    [OutputType([string])]
    param(
        [AllowEmptyString()][string] $Texto = '',
        [ValidateNotNullOrEmpty()][string] $Tono = 'Valor',
        [switch] $Negrita
    )

    if ([string]::IsNullOrEmpty($Texto)) { return '' }
    $estilo = $script:ColoresPanel[$Tono]
    if ($Negrita) { $estilo = 'bold ' + $estilo }
    return '[{0}]{1}[/]' -f $estilo, (Get-SpectreEscapedText -Text $Texto)
}

function Show-VentanaRica {
    <#
        .SYNOPSIS
            La ventana de estado y el menu, con marco, tabla y color por estado.
        .DESCRIPTION
            La maqueta de Show-Ventana -cabecera con veredicto, los dos destinos,
            la tabla por raiz, los sumarios y el menu- con lo que Spectre anade:
            el marco toma el color del veredicto, la tabla lleva bordes y todo
            se ajusta al ancho de la ventana.
        .PARAMETER Datos
            Lo que devuelve Get-DatosDeVentana.
    #>
    [CmdletBinding()]
    [OutputType([void])]
    param(
        [Parameter(Mandatory)][psobject] $Datos
    )

    $c = $script:ColoresPanel

    # EL TONO DEL VEREDICTO LO DECIDE Get-ColorDeEstado (estilo.ps1). Se le pasa
    # una "paleta" de nombres, asi la regla estado -> color sigue en un sitio.
    $tono = Get-ColorDeEstado -Estado $Datos.Estado -Paleta @{ Verde = 'Verde'; Ambar = 'Ambar'; Rojo = 'Rojo'; Gris = 'Gris' }
    $simbolo = (Get-Simbolo -Estado $Datos.Estado -Unicode $true).Trim()

    # TODOS LOS BLOQUES CON EL MISMO ANCHO, para que los marcos se alineen, y
    # con SUELO DE 92: por debajo, Spectre reparte el sitio de la tabla y parte
    # en tres renglones la columna de la clase (medido pintandola a 80). El
    # techo es la consola que mide Spectre: un ancho mayor lo rechaza y lanza.
    $consola = [Spectre.Console.AnsiConsole]::Profile.Width
    $ancho = [Math]::Min($consola, [Math]::Max(92, [Math]::Min(110, $consola - 2)))

    # --- Cabecera: el marco entero lleva el color del veredicto --------------
    $cuerpo = '{0} {1}' -f (Format-Marcado -Texto $simbolo -Tono $tono),
        (Format-Marcado -Texto $Datos.Estado.ToUpperInvariant() -Tono $tono -Negrita)
    if ($Datos.Detalle) { $cuerpo += "`n" + (Format-Marcado -Texto $Datos.Detalle -Tono 'Tenue') }
    $cabecera = Format-Marcado -Texto (' RESPALDO {0} ' -f $Datos.Equipo) -Tono 'Titulo' -Negrita
    Format-SpectrePanel -Data $cuerpo -Header $cabecera -Border 'Rounded' -Color $c[$tono] -Width $ancho |
        Out-SpectreHost

    # --- Los dos destinos ---------------------------------------------------
    $filasDestino = foreach ($destino in @($Datos.Nodo, $Datos.Disco)) {
        New-SpectreGridRow -Data @(
            (Format-Marcado -Texto $destino.Nombre -Tono 'Etiqueta'),
            (Format-Marcado -Texto $destino.Situacion -Tono $destino.TonoSituacion),
            (Format-Marcado -Texto $destino.Libres -Tono 'Tenue'),
            (Format-Marcado -Texto ('ult. copia {0}' -f $destino.UltimaCopia) -Tono $destino.TonoCopia))
    }
    Format-SpectreGrid -Data @($filasDestino) -Padding 3 | Out-SpectreHost
    Write-SpectreHost ' '

    # --- La tabla por raiz --------------------------------------------------
    $tabla = @(Get-FilasDeTabla -Filas @($Datos.Filas) -HorasParaAmbar $Datos.HorasVerde)
    if ($tabla.Count -eq 0) {
        Write-SpectreHost (Format-Marcado -Texto 'Todavia no hay tabla: la escribe el motor al copiar. Corre [1].' -Tono 'Tenue')
    }
    else {
        $objetos = foreach ($m in $tabla) {
            [pscustomobject]@{
                'RAIZ'       = (Format-Marcado -Texto $m.Etiqueta -Tono 'Valor')
                'CL'         = (Format-Marcado -Texto $m.Clase -Tono 'Tenue')
                'COPIADO'    = (Format-Marcado -Texto ('{0} arch.' -f $m.Movidos) -Tono 'Tenue')
                'NODO'       = (Format-Marcado -Texto $m.Nodo.Texto -Tono $m.Nodo.Tono)
                'DISCO FRIO' = (Format-Marcado -Texto $m.Disco.Texto -Tono $m.Disco.Tono)
            }
        }
        Format-SpectreTable -Data @($objetos) -AllowMarkup -Border 'Rounded' -Color $c.Marco `
            -HeaderColor $c.Etiqueta -Width $ancho | Out-SpectreHost
    }
    Write-SpectreHost ' '

    # --- Los sumarios -------------------------------------------------------
    # PENDIENTE solo aparece si hay algo, y es la unica que lleva la regla
    # lateral del aviso: una marca que esta siempre no marca nada.
    $separador = ' {0} ' -f (Format-Marcado -Texto ([string][char] 0x00B7) -Tono 'Tenue')
    $filasSumario = New-Object System.Collections.Generic.List[object]
    foreach ($s in @(
            @{ Etiqueta = 'SEGURIDAD';   Partes = $Datos.Seguridad;   Tono = 'Valor' },
            @{ Etiqueta = 'COMPROBADO';  Partes = $Datos.Comprobado;  Tono = 'Valor' },
            @{ Etiqueta = 'AUTOMATISMO'; Partes = $Datos.Automatismo; Tono = 'Valor' },
            @{ Etiqueta = 'PENDIENTE';   Partes = $Datos.Pendiente;   Tono = 'Ambar' })) {
        $utiles = @($s.Partes | Where-Object { -not [string]::IsNullOrWhiteSpace($_) })
        if ($utiles.Count -eq 0) { continue }
        $rotulo = Format-Marcado -Texto $s.Etiqueta -Tono 'Etiqueta'
        if ($s.Tono -eq 'Ambar') {
            $rotulo = Format-Marcado -Texto (([string][char] 0x258C) + ' ' + $s.Etiqueta) -Tono 'Ambar'
        }
        $texto = ($utiles | ForEach-Object { Format-Marcado -Texto $_ -Tono $s.Tono }) -join $separador
        $filasSumario.Add((New-SpectreGridRow -Data @($rotulo, $texto)))
    }
    if ($filasSumario.Count -gt 0) {
        Format-SpectreGrid -Data $filasSumario.ToArray() -Padding 2 -Width $ancho | Out-SpectreHost
    }

    # --- Menu ----------------------------------------------------------------
    $menu = Get-MenuDelTablero
    Write-SpectreHost ' '
    Write-SpectreRule -LineColor $c.Marco -Width $ancho
    $filasMenu = New-Object System.Collections.Generic.List[object]
    $filasMenu.Add((New-SpectreGridRow -Data @($menu.Columnas | ForEach-Object { Format-Marcado -Texto $_ -Tono 'Titulo' -Negrita })))
    foreach ($fila in $menu.Filas) {
        $filasMenu.Add((New-SpectreGridRow -Data @($fila | ForEach-Object { Format-Marcado -Texto $_ -Tono 'Valor' })))
    }
    Format-SpectreGrid -Data $filasMenu.ToArray() -Padding 4 | Out-SpectreHost
    Write-SpectreHost (Format-Marcado -Texto $menu.Nota -Tono 'Tenue')
    Write-SpectreHost ' '
}
