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
            Se adapta dinamicamente al ancho visible de la terminal usando
            Get-GeometriaDeConsola (Compacto / Estandar / Amplio). Todos los
            paneles, tablas, reglas y rejillas comparten el mismo ancho exacto.
        .PARAMETER Datos
            Lo que devuelve Get-DatosDeVentana.
    #>
    [CmdletBinding()]
    [OutputType([void])]
    param(
        [Parameter(Mandatory)][psobject] $Datos
    )

    $c = $script:ColoresPanel

    # EL TONO DEL VEREDICTO LO DECIDE Get-ColorDeEstado (estilo.ps1).
    $tono = Get-ColorDeEstado -Estado $Datos.Estado -Paleta @{ Verde = 'Verde'; Ambar = 'Ambar'; Rojo = 'Rojo'; Gris = 'Gris' }
    $simbolo = (Get-Simbolo -Estado $Datos.Estado -Unicode $true).Trim()
    $etiquetaEstado = Get-EtiquetaDeEstado -Estado $Datos.Estado

    $consola = [Spectre.Console.AnsiConsole]::Profile.Width
    $geo = Get-GeometriaDeConsola -AnchoConsola $consola
    $ancho = [Math]::Min($consola, $geo.AnchoPanel)

    # --- Cabecera: el marco entero lleva el color del veredicto --------------
    $horaAct = if ($Datos.PSObject.Properties.Name -contains 'Actualizado' -and $Datos.Actualizado) {
        '' + $Datos.Actualizado
    }
    else { (Get-Date).ToString('HH:mm:ss') }
    $cuerpo = '{0} {1}   {2}' -f (Format-Marcado -Texto $simbolo -Tono $tono),
        (Format-Marcado -Texto $etiquetaEstado -Tono $tono -Negrita),
        (Format-Marcado -Texto ('Actualizado ' + $horaAct) -Tono 'Tenue')
    if ($Datos.Detalle) { $cuerpo += "`n" + (Format-Marcado -Texto $Datos.Detalle -Tono 'Tenue') }
    $sepMedio = [string][char] 0x00B7
    $cabecera = Format-Marcado -Texto (' CONSOLA DE RESPALDO {0} {1} ' -f $sepMedio, $Datos.Equipo) -Tono 'Titulo' -Negrita
    Format-SpectrePanel -Data $cuerpo -Header $cabecera -Border 'Rounded' -Color $c[$tono] -Width $ancho |
        Out-SpectreHost

    # --- Los destinos: nodo SMB, disco frio y laboratorio VM -----------------
    $destinos = @($Datos.Nodo, $Datos.Disco)
    if ($Datos.PSObject.Properties.Name -contains 'Laboratorio' -and $Datos.Laboratorio) { $destinos += $Datos.Laboratorio }
    $filasDestino = foreach ($destino in $destinos) {
        New-SpectreGridRow -Data @(
            (Format-Marcado -Texto $destino.Nombre -Tono 'Etiqueta' -Negrita),
            (Format-Marcado -Texto $destino.Situacion -Tono $destino.TonoSituacion),
            (Format-Marcado -Texto $destino.Libres -Tono 'Tenue'),
            (Format-Marcado -Texto ('ult. sync: {0}' -f $destino.UltimaCopia) -Tono $destino.TonoCopia))
    }
    Format-SpectreGrid -Data @($filasDestino) -Padding 2 -Width $ancho | Out-SpectreHost

    # --- Barra viva de operacion en curso (PROGRESO.json) --------------------
    if ($Datos.PSObject.Properties.Name -contains 'ProgresoActivo' -and $Datos.ProgresoActivo -and $Datos.ProgresoActivo.Activo) {
        $prog = $Datos.ProgresoActivo
        $barra = Format-BarraDeProgreso -Porcentaje $prog.Porcentaje -Ancho 16 -Unicode $true
        $resProg = Format-ProgresoDeOperacion -Progreso $prog
        $lineaProg = '{0} {1}  {2}' -f
            (Format-Marcado -Texto 'EN CURSO' -Tono 'Verde' -Negrita),
            (Format-Marcado -Texto $barra -Tono 'Verde'),
            (Format-Marcado -Texto $resProg -Tono 'Valor')
        Write-SpectreHost $lineaProg
    }
    Write-SpectreHost ' '

    # --- La tabla por volumen / raiz -----------------------------------------
    $argsTabla = @{ Filas = @($Datos.Filas); HorasParaAmbar = $Datos.HorasVerde }
    if ($Datos.PSObject.Properties.Name -contains 'RaicesActuales' -and @($Datos.RaicesActuales).Count -gt 0) {
        $argsTabla['RaicesActuales'] = @($Datos.RaicesActuales)
    }
    if ($Datos.PSObject.Properties.Name -contains 'Centinelas' -and @($Datos.Centinelas).Count -gt 0) {
        $argsTabla['Centinelas'] = @($Datos.Centinelas)
    }
    $tabla = @(Get-FilasDeTabla @argsTabla)
    if ($tabla.Count -eq 0) {
        Write-SpectreHost (Format-Marcado -Texto 'Sin registro de volumenes todavia. Ejecute [A] Respaldo al nodo SMB.' -Tono 'Tenue')
    }
    else {
        $objetos = foreach ($m in $tabla) {
            [pscustomobject]@{
                'VOLUMEN'    = (Format-Marcado -Texto (Limit-Texto -Texto $m.Etiqueta -Maximo $geo.ColRaiz) -Tono 'Valor')
                'POL'        = (Format-Marcado -Texto $m.Clase -Tono 'Tenue')
                'DELTA'      = (Format-Marcado -Texto ('{0} arch.' -f $m.Movidos) -Tono 'Tenue')
                'NODO SMB'   = (Format-Marcado -Texto $m.Nodo.Texto -Tono $m.Nodo.Tono)
                'DISCO FRIO' = (Format-Marcado -Texto $m.Disco.Texto -Tono $m.Disco.Tono)
            }
        }
        Format-SpectreTable -Data @($objetos) -AllowMarkup -Border 'Rounded' -Color $c.Marco `
            -HeaderColor $c.Etiqueta -Width $ancho | Out-SpectreHost
    }
    Write-SpectreHost ' '

    # --- Panel de control (semantica estricta, nunca miente) -----------------
    $tieneNuevos = ($Datos.PSObject.Properties.Name -contains 'Proteccion') -and ($null -ne $Datos.Proteccion)
    $separador = ' {0} ' -f (Format-Marcado -Texto $sepMedio -Tono 'Tenue')
    $filasSumario = New-Object System.Collections.Generic.List[object]

    if ($tieneNuevos) {
        $bloques = @(
            @{ Etiqueta = 'PROTECCION'; Partes = @($Datos.Proteccion.Partes); Tono = $Datos.Proteccion.Tono; Regla = [bool]$Datos.Proteccion.Regla },
            @{ Etiqueta = 'INTEGRIDAD'; Partes = @($Datos.Integridad.Partes); Tono = $Datos.Integridad.Tono; Regla = [bool]$Datos.Integridad.Regla },
            @{ Etiqueta = 'SERVICIO';   Partes = @($Datos.Servicio.Partes);   Tono = $Datos.Servicio.Tono;   Regla = [bool]$Datos.Servicio.Regla }
        )
        foreach ($s in $bloques) {
            $utiles = @($s.Partes | Where-Object { -not [string]::IsNullOrWhiteSpace($_) })
            if ($utiles.Count -eq 0) { continue }
            $tonoUso = if ($c.ContainsKey($s.Tono)) { $s.Tono } else { 'Valor' }
            $rotulo = Format-Marcado -Texto $s.Etiqueta -Tono 'Etiqueta'
            if ($s.Regla) {
                $rotulo = Format-Marcado -Texto (([string][char] 0x258C) + ' ' + $s.Etiqueta) -Tono $tonoUso -Negrita
            }
            $texto = ($utiles | ForEach-Object { Format-Marcado -Texto $_ -Tono $tonoUso }) -join $separador
            $filasSumario.Add((New-SpectreGridRow -Data @($rotulo, $texto)))
        }

        if ($Datos.PSObject.Properties.Name -contains 'Alertas' -and @($Datos.Alertas).Count -gt 0) {
            $alertasVis = @($Datos.Alertas | Select-Object -First 3)
            $hayCritica = @($alertasVis | Where-Object { $_.Nivel -eq 'Critico' }).Count -gt 0
            $tonoAlerta = if ($hayCritica) { 'Rojo' } else { 'Ambar' }
            $partesAl = New-Object System.Collections.Generic.List[string]
            foreach ($al in $alertasVis) {
                $tAl = if ($al.Nivel -eq 'Critico') { 'Rojo' } else { 'Ambar' }
                $pref = if ($al.Opcion) { '[{0}] ' -f $al.Opcion } else { '' }
                [void]$partesAl.Add((Format-Marcado -Texto ($pref + $al.Mensaje) -Tono $tAl))
            }
            $extraAl = @($Datos.Alertas).Count - $alertasVis.Count
            if ($extraAl -gt 0) {
                [void]$partesAl.Add((Format-Marcado -Texto ('+{0} mas en [F] Diagnostico' -f $extraAl) -Tono 'Tenue'))
            }
            $rotAl = Format-Marcado -Texto (([string][char] 0x258C) + ' ALERTAS') -Tono $tonoAlerta -Negrita
            $filasSumario.Add((New-SpectreGridRow -Data @($rotAl, ($partesAl.ToArray() -join $separador))))
        }
    }
    else {
        # Respaldo de compatibilidad con objetos de prueba legados.
        $tonoComprobadoPanel = if ($Datos.PSObject.Properties.Name -contains 'TonoComprobado' -and $c.ContainsKey($Datos.TonoComprobado)) {
            $Datos.TonoComprobado
        }
        else { 'Valor' }
        foreach ($s in @(
                @{ Etiqueta = 'PROTECCION'; Partes = $Datos.Seguridad;   Tono = 'Valor';               Regla = $false },
                @{ Etiqueta = 'INTEGRIDAD'; Partes = $Datos.Comprobado;  Tono = $tonoComprobadoPanel;  Regla = ($tonoComprobadoPanel -eq 'Rojo') },
                @{ Etiqueta = 'SERVICIO';   Partes = $Datos.Automatismo; Tono = 'Valor';               Regla = $false },
                @{ Etiqueta = 'ALERTAS';    Partes = $Datos.Pendiente;   Tono = 'Ambar';               Regla = $true })) {
            $utiles = @($s.Partes | Where-Object { -not [string]::IsNullOrWhiteSpace($_) })
            if ($utiles.Count -eq 0) { continue }
            $rotulo = Format-Marcado -Texto $s.Etiqueta -Tono 'Etiqueta'
            if ($s.Regla) {
                $rotulo = Format-Marcado -Texto (([string][char] 0x258C) + ' ' + $s.Etiqueta) -Tono $s.Tono
            }
            $texto = ($utiles | ForEach-Object { Format-Marcado -Texto $_ -Tono $s.Tono }) -join $separador
            $filasSumario.Add((New-SpectreGridRow -Data @($rotulo, $texto)))
        }
    }
    if ($filasSumario.Count -gt 0) {
        Format-SpectreGrid -Data $filasSumario.ToArray() -Padding 2 -Width $ancho | Out-SpectreHost
    }

    # --- Menu consolidado [A]-[F] --------------------------------------------
    $menu = Get-MenuDelTablero
    Write-SpectreHost ' '
    Write-SpectreRule -LineColor $c.Marco -Width $ancho
    $filasMenu = New-Object System.Collections.Generic.List[object]
    if ($geo.ColumnasMenu -eq 1) {
        $filasMenu.Add((New-SpectreGridRow -Data @((Format-Marcado -Texto 'OPERACIONES Y SISTEMA' -Tono 'Titulo' -Negrita))))
        foreach ($item in $menu.Items) {
            $filasMenu.Add((New-SpectreGridRow -Data @((Format-Marcado -Texto ('[{0}] {1}' -f $item.Tecla, $item.Etiqueta) -Tono 'Valor'))))
        }
    }
    else {
        $filasMenu.Add((New-SpectreGridRow -Data @($menu.Columnas | ForEach-Object { Format-Marcado -Texto $_ -Tono 'Titulo' -Negrita })))
        foreach ($fila in $menu.Filas) {
            $filasMenu.Add((New-SpectreGridRow -Data @($fila | ForEach-Object { Format-Marcado -Texto $_ -Tono 'Valor' })))
        }
    }
    Format-SpectreGrid -Data $filasMenu.ToArray() -Padding 2 -Width $ancho | Out-SpectreHost
    Write-SpectreHost (Format-Marcado -Texto $menu.Nota -Tono 'Tenue')
    Write-SpectreHost ' '
}
