#Requires -Version 5.1

# Modulo NasRespaldo - dominio: Progreso.ps1

function Write-ProgresoDeOperacion {
    <#
        .SYNOPSIS
            Publica telemetria atomica en PROGRESO.json para operaciones largas.
        .DESCRIPTION
            Contrato unificado de avance (Release It!: transparencia operativa
            en procesos de larga duracion). Lo consumen el tablero en vivo y el
            indicador de bandeja. Nunca lanza: la telemetria no puede abortar un
            respaldo ni una verificacion.
    #>
    [CmdletBinding(SupportsShouldProcess)]
    [OutputType([void])]
    param(
        [Parameter(Mandatory)]
        [ValidateSet('nodo', 'disco', 'verificacion', 'laboratorio', 'laboratorio-frio', 'validacion-profunda')]
        [string] $Operacion,

        [Parameter(Mandatory)]
        [ValidateNotNullOrEmpty()]
        [string] $Fase,

        [AllowEmptyString()][string] $Objetivo = '',
        [AllowEmptyString()][string] $Elemento = '',
        [long] $Hechos = 0,
        [long] $Totales = 0,

        [ValidateSet('bytes', 'archivos', 'raices', 'bloques', 'pasos')]
        [string] $Unidad = 'pasos',

        [double] $TasaMBs = 0,
        [int] $EtaSegundos = -1,
        [AllowEmptyString()][string] $Detalle = '',
        [AllowEmptyString()][string] $InicioUtc = '',

        [ValidateNotNullOrEmpty()]
        [string] $Carpeta = (Get-CarpetaDeEstado)
    )

    $ruta = Join-Path $Carpeta 'PROGRESO.json'
    if (-not $PSCmdlet.ShouldProcess($ruta, 'Actualizar progreso de operacion')) { return }

    $ahoraUtc = [datetime]::UtcNow.ToString('o')
    $inicioEfectivo = $InicioUtc
    if ([string]::IsNullOrWhiteSpace($inicioEfectivo)) {
        try {
            if (Test-Path -LiteralPath $ruta -PathType Leaf) {
                $prev = Get-Content -LiteralPath $ruta -Raw -Encoding UTF8 -ErrorAction Stop | ConvertFrom-Json
                if ($prev -and ('' + $prev.Operacion) -eq $Operacion -and ([int]$prev.Pid) -eq $PID -and $prev.InicioUtc) {
                    $inicioEfectivo = '' + $prev.InicioUtc
                }
            }
        }
        catch { Write-Verbose "No se pudo heredar InicioUtc de PROGRESO.json: $($_.Exception.Message)" }
        if ([string]::IsNullOrWhiteSpace($inicioEfectivo)) { $inicioEfectivo = $ahoraUtc }
    }

    $pct = if ($Totales -gt 0) {
        [int][math]::Max(0, [math]::Min(100, [math]::Floor(100.0 * $Hechos / $Totales)))
    }
    else { 0 }

    $eta = $EtaSegundos
    if ($eta -lt 0 -and $Unidad -eq 'bytes' -and $TasaMBs -gt 0.05 -and $Totales -gt $Hechos) {
        $eta = [int][math]::Ceiling((($Totales - $Hechos) / 1MB) / $TasaMBs)
    }

    $registro = [ordered]@{
        Version        = 1
        Operacion      = $Operacion
        Fase           = $Fase
        Objetivo       = ($Objetivo -replace '[\r\n]+', ' ')
        Elemento       = ($Elemento -replace '[\r\n]+', ' ')
        Hechos         = [long]$Hechos
        Totales        = [long]$Totales
        Unidad         = $Unidad
        Porcentaje     = [int]$pct
        TasaMBs        = [math]::Round([double]$TasaMBs, 1)
        EtaSegundos    = [int]$eta
        Detalle        = ($Detalle -replace '[\r\n]+', ' ')
        Pid            = [int]$PID
        InicioUtc      = $inicioEfectivo
        ActualizadoUtc = $ahoraUtc
    }
    try {
        $json = $registro | ConvertTo-Json -Depth 3
        Set-ContenidoAtomico -Ruta $ruta -Contenido $json -Confirm:$false
    }
    catch {
        Write-Verbose "No se pudo actualizar PROGRESO.json: $($_.Exception.Message)"
    }
}

function Read-ProgresoDeOperacion {
    <#
        .SYNOPSIS
            Lee PROGRESO.json (o LABORATORIO.progreso como respaldo) y valida si sigue activo.
        .DESCRIPTION
            Nunca lanza. Valida tanto la frescura de la marca de tiempo como que
            el PID escritor siga vivo en el sistema, evitando mostrar como activa
            una operacion que murio antes de retirar su archivo.
    #>
    [CmdletBinding()]
    [OutputType([psobject])]
    param(
        [ValidateNotNullOrEmpty()]
        [string] $Carpeta = (Get-CarpetaDeEstado),

        [ValidateRange(1, 120)]
        [int] $MaximoMinutos = 15
    )

    $ruta = Join-Path $Carpeta 'PROGRESO.json'
    if (Test-Path -LiteralPath $ruta -PathType Leaf) {
        try {
            $j = Get-Content -LiteralPath $ruta -Raw -Encoding UTF8 -ErrorAction Stop | ConvertFrom-Json
            if ($j -and ($j.PSObject.Properties.Name -contains 'Operacion') -and -not [string]::IsNullOrWhiteSpace('' + $j.Operacion)) {
                $ahora = [datetime]::UtcNow
                [datetime] $act = [datetime]::MinValue
                $edadSeg = [int]::MaxValue
                $reciente = $false
                $actTxt = if ($j.PSObject.Properties.Name -contains 'ActualizadoUtc') { '' + $j.ActualizadoUtc } else { '' }
                if ($actTxt -and [datetime]::TryParse($actTxt, [System.Globalization.CultureInfo]::InvariantCulture,
                        ([System.Globalization.DateTimeStyles]::AssumeUniversal -bor [System.Globalization.DateTimeStyles]::AdjustToUniversal), [ref]$act)) {
                    $edadSeg = [int][math]::Max(0, [math]::Floor(($ahora - $act).TotalSeconds))
                    $reciente = ($edadSeg -le ($MaximoMinutos * 60))
                }
                [int] $pidEsc = 0
                if ($j.PSObject.Properties.Name -contains 'Pid' -and $null -ne $j.Pid) { $pidEsc = [int]$j.Pid }
                $vivo = ($pidEsc -gt 0) -and ($null -ne (Get-Process -Id $pidEsc -ErrorAction SilentlyContinue))
                $tasa = 0.0
                if ($j.PSObject.Properties.Name -contains 'TasaMBs' -and $null -ne $j.TasaMBs) { $tasa = [double]$j.TasaMBs }
                $etaSeg = -1
                if ($j.PSObject.Properties.Name -contains 'EtaSegundos' -and $null -ne $j.EtaSegundos) { $etaSeg = [int]$j.EtaSegundos }

                return [pscustomobject]@{
                    Operacion      = '' + $j.Operacion
                    Fase           = $(if ($j.PSObject.Properties.Name -contains 'Fase') { '' + $j.Fase } else { '' })
                    Objetivo       = $(if ($j.PSObject.Properties.Name -contains 'Objetivo') { '' + $j.Objetivo } else { '' })
                    Elemento       = $(if ($j.PSObject.Properties.Name -contains 'Elemento') { '' + $j.Elemento } else { '' })
                    Hechos         = $(if ($j.PSObject.Properties.Name -contains 'Hechos' -and $null -ne $j.Hechos) { [long]$j.Hechos } else { [long]0 })
                    Totales        = $(if ($j.PSObject.Properties.Name -contains 'Totales' -and $null -ne $j.Totales) { [long]$j.Totales } else { [long]0 })
                    Unidad         = $(if ($j.PSObject.Properties.Name -contains 'Unidad') { '' + $j.Unidad } else { 'pasos' })
                    Porcentaje     = $(if ($j.PSObject.Properties.Name -contains 'Porcentaje' -and $null -ne $j.Porcentaje) { [int]$j.Porcentaje } else { 0 })
                    TasaMBs        = $tasa
                    EtaSegundos    = $etaSeg
                    Detalle        = $(if ($j.PSObject.Properties.Name -contains 'Detalle') { '' + $j.Detalle } else { '' })
                    Pid            = $pidEsc
                    InicioUtc      = $(if ($j.PSObject.Properties.Name -contains 'InicioUtc') { '' + $j.InicioUtc } else { '' })
                    ActualizadoUtc = $actTxt
                    EdadSegundos   = $edadSeg
                    Reciente       = $reciente
                    ProcesoVivo    = $vivo
                    Activo         = ($reciente -and $vivo)
                    Fuente         = 'progreso-json'
                }
            }
        }
        catch {
            Write-Verbose "PROGRESO.json ilegible: $($_.Exception.Message)"
        }
    }

    # Respaldo hacia atras con LABORATORIO.progreso si existe y es reciente.
    $lab = Read-ProgresoDelLaboratorio -Carpeta $Carpeta -MaximoMinutos $MaximoMinutos
    if ($lab -and $lab.Reciente) {
        [double] $mbs = 0.0
        [void][double]::TryParse(('' + $lab.MbPorSegundo), [System.Globalization.NumberStyles]::Float, [System.Globalization.CultureInfo]::InvariantCulture, [ref]$mbs)
        $etaSegLab = if ($lab.EtaMinutos -ge 0) { $lab.EtaMinutos * 60 } else { -1 }
        return [pscustomobject]@{
            Operacion      = 'laboratorio'
            Fase           = '' + $lab.Fase
            Objetivo       = '' + $lab.Vm
            Elemento       = '' + $lab.Archivo
            Hechos         = [long]$lab.Hechos
            Totales        = [long]$lab.Totales
            Unidad         = 'bytes'
            Porcentaje     = [int]$lab.Porcentaje
            TasaMBs        = $mbs
            EtaSegundos    = $etaSegLab
            Detalle        = ''
            Pid            = 0
            InicioUtc      = ''
            ActualizadoUtc = '' + $lab.Momento
            EdadSegundos   = 0
            Reciente       = $true
            ProcesoVivo    = $true
            Activo         = $true
            Fuente         = 'laboratorio-legado'
        }
    }
    return $null
}

function Remove-ProgresoDeOperacion {
    <#
        .SYNOPSIS
            Retira PROGRESO.json al finalizar una operacion. Nunca lanza.
        .DESCRIPTION
            Si se especifica -Operacion, solo retira el archivo cuando pertenece
            a esa operacion (o al PID actual), evitando que una verificacion o
            corrida secundaria borre la telemetria de otro proceso activo.
    #>
    [CmdletBinding(SupportsShouldProcess)]
    [OutputType([void])]
    param(
        [ValidateNotNullOrEmpty()]
        [string] $Carpeta = (Get-CarpetaDeEstado),

        [AllowEmptyString()]
        [string] $Operacion = '',

        [switch] $Forzar
    )

    $ruta = Join-Path $Carpeta 'PROGRESO.json'
    if (-not (Test-Path -LiteralPath $ruta -PathType Leaf)) { return }
    if (-not $Forzar -and -not [string]::IsNullOrWhiteSpace($Operacion)) {
        try {
            $j = Get-Content -LiteralPath $ruta -Raw -Encoding UTF8 -ErrorAction Stop | ConvertFrom-Json
            if ($j -and ($j.PSObject.Properties.Name -contains 'Operacion') -and ('' + $j.Operacion) -ne $Operacion) {
                $pidAjeno = if ($j.PSObject.Properties.Name -contains 'Pid' -and $null -ne $j.Pid) { [int]$j.Pid } else { 0 }
                if ($pidAjeno -gt 0 -and $pidAjeno -ne $PID -and $null -ne (Get-Process -Id $pidAjeno -ErrorAction SilentlyContinue)) {
                    return
                }
            }
        }
        catch { Write-Verbose "Limpiando PROGRESO.json no interpretable: $($_.Exception.Message)" }
    }
    if (-not $PSCmdlet.ShouldProcess($ruta, 'Retirar progreso de operacion')) { return }
    Remove-Item -LiteralPath $ruta -Force -ErrorAction SilentlyContinue
}

function Format-ProgresoDeOperacion {
    <#
        .SYNOPSIS
            Resume un objeto de Read-ProgresoDeOperacion en una linea concisa.
    #>
    [CmdletBinding()]
    [OutputType([string])]
    param(
        [Parameter(Mandatory)][psobject] $Progreso
    )

    $partes = New-Object System.Collections.Generic.List[string]
    if ($Progreso.Totales -gt 0) {
        if ($Progreso.Unidad -eq 'bytes') {
            [void]$partes.Add(('{0}%' -f $Progreso.Porcentaje))
        }
        else {
            [void]$partes.Add(('{0}/{1} ({2}%)' -f $Progreso.Hechos, $Progreso.Totales, $Progreso.Porcentaje))
        }
    }
    elseif ($Progreso.Fase) {
        [void]$partes.Add('' + $Progreso.Fase)
    }
    if ($Progreso.TasaMBs -gt 0.05) {
        [void]$partes.Add(([string]::Format([System.Globalization.CultureInfo]::InvariantCulture, '{0:0.0} MB/s', $Progreso.TasaMBs)))
    }
    if ($Progreso.EtaSegundos -gt 0) {
        if ($Progreso.EtaSegundos -lt 90) { [void]$partes.Add(('ETA {0}s' -f $Progreso.EtaSegundos)) }
        elseif ($Progreso.EtaSegundos -lt 5400) { [void]$partes.Add(('ETA {0}m' -f [int][math]::Ceiling($Progreso.EtaSegundos / 60.0))) }
        else { [void]$partes.Add(([string]::Format([System.Globalization.CultureInfo]::InvariantCulture, 'ETA {0:0.0}h', ($Progreso.EtaSegundos / 3600.0)))) }
    }
    $etiqueta = if ($Progreso.Objetivo) { '' + $Progreso.Objetivo } elseif ($Progreso.Elemento) { '' + $Progreso.Elemento } else { '' }
    if ($etiqueta) { [void]$partes.Add($etiqueta) }
    return ($partes -join ' | ')
}

function Write-ProgresoDelLaboratorio {
    <#
        .SYNOPSIS
            Publica el avance en vivo de [G] en LABORATORIO.progreso y PROGRESO.json.
        .DESCRIPTION
            Archivo atomico ligero que leen otra instancia del tablero y el
            indicador de bandeja mientras [G] o GuardarFrio envian bloques.
            Nunca lanza si no puede escribir: el avance es telemetria, no guarda.
    #>
    [CmdletBinding(SupportsShouldProcess)]
    [OutputType([void])]
    param(
        [Parameter(Mandatory)][ValidateNotNullOrEmpty()][string] $Vm,
        [ValidateNotNull()][string] $Archivo = '',
        [ValidateSet('hasheando', 'escribiendo', 'verificando', 'copiando')][string] $Fase = 'escribiendo',
        [long] $Hechos = 0,
        [long] $Totales = 0,
        [double] $MbPorSegundo = 0,
        [int] $EtaMinutos = -1,
        [ValidateNotNullOrEmpty()][string] $Carpeta = (Get-CarpetaDeEstado),
        [ValidateSet('laboratorio', 'laboratorio-frio')][string] $Operacion = 'laboratorio'
    )

    $ruta = Join-Path $Carpeta 'LABORATORIO.progreso'
    if (-not $PSCmdlet.ShouldProcess($ruta, 'Actualizar progreso del laboratorio')) { return }

    $pct = if ($Totales -gt 0) { [int][math]::Min(100, [math]::Floor(100.0 * $Hechos / $Totales)) } else { 0 }
    $vel = [string]::Format([System.Globalization.CultureInfo]::InvariantCulture, '{0:0.0}', $MbPorSegundo)
    $sb = New-Object System.Text.StringBuilder
    [void]$sb.AppendLine(('vm={0}' -f ($Vm -replace '[\r\n]+', ' ')))
    [void]$sb.AppendLine(('archivo={0}' -f ($Archivo -replace '[\r\n]+', ' ')))
    [void]$sb.AppendLine(('fase={0}' -f $Fase))
    [void]$sb.AppendLine(('hechos={0}' -f $Hechos))
    [void]$sb.AppendLine(('totales={0}' -f $Totales))
    [void]$sb.AppendLine(('porcentaje={0}' -f $pct))
    [void]$sb.AppendLine(('mb_s={0}' -f $vel))
    [void]$sb.AppendLine(('eta_min={0}' -f $EtaMinutos))
    [void]$sb.AppendLine(('momento={0}' -f (Get-Date -Format 's')))
    try { Set-ContenidoAtomico -Ruta $ruta -Contenido $sb.ToString() -Confirm:$false }
    catch { Write-Verbose "No se pudo actualizar progreso del laboratorio: $($_.Exception.Message)" }

    $etaSeg = if ($EtaMinutos -ge 0) { $EtaMinutos * 60 } else { -1 }
    Write-ProgresoDeOperacion -Operacion $Operacion -Fase $Fase -Objetivo $Vm -Elemento $Archivo `
        -Hechos $Hechos -Totales $Totales -Unidad 'bytes' -TasaMBs $MbPorSegundo -EtaSegundos $etaSeg `
        -Carpeta $Carpeta -Confirm:$false
}

function Read-ProgresoDelLaboratorio {
    <#
        .SYNOPSIS
            Lee LABORATORIO.progreso si existe y es legible. Nunca lanza.
    #>
    [CmdletBinding()]
    [OutputType([psobject])]
    param(
        [ValidateNotNullOrEmpty()][string] $Carpeta = (Get-CarpetaDeEstado),
        [ValidateRange(1, 120)][int] $MaximoMinutos = 15
    )

    $ruta = Join-Path $Carpeta 'LABORATORIO.progreso'
    try {
        if (-not (Test-Path -LiteralPath $ruta -PathType Leaf)) { return $null }
        $m = @{}
        foreach ($linea in (Get-Content -LiteralPath $ruta -Encoding UTF8 -ErrorAction Stop)) {
            if ($linea -match '^\s*#' -or $linea -notmatch '=') { continue }
            $par = $linea -split '=', 2
            $m[$par[0].Trim()] = $par[1].Trim()
        }
        if (-not $m.ContainsKey('vm') -or [string]::IsNullOrWhiteSpace($m['vm'])) { return $null }
        [datetime] $t = [datetime]::MinValue
        $reciente = $false
        if ($m.ContainsKey('momento') -and [datetime]::TryParse($m['momento'], [ref]$t)) {
            $reciente = (((Get-Date) - $t).TotalMinutes -le $MaximoMinutos)
        }
        [int] $pct = 0; [void][int]::TryParse(('' + $m['porcentaje']), [ref]$pct)
        [long] $hechos = 0; [void][long]::TryParse(('' + $m['hechos']), [ref]$hechos)
        [long] $totales = 0; [void][long]::TryParse(('' + $m['totales']), [ref]$totales)
        [int] $eta = -1; [void][int]::TryParse(('' + $m['eta_min']), [ref]$eta)
        return [pscustomobject]@{
            Vm           = '' + $m['vm']
            Archivo      = '' + $m['archivo']
            Fase         = '' + $m['fase']
            Hechos       = $hechos
            Totales      = $totales
            Porcentaje   = $pct
            MbPorSegundo = '' + $m['mb_s']
            EtaMinutos   = $eta
            Momento      = '' + $m['momento']
            Reciente     = $reciente
        }
    }
    catch {
        return $null
    }
}

function Remove-ProgresoDelLaboratorio {
    <#
        .SYNOPSIS
            Retira LABORATORIO.progreso y su entrada en PROGRESO.json al terminar. Nunca lanza.
    #>
    [CmdletBinding(SupportsShouldProcess)]
    [OutputType([void])]
    param(
        [ValidateNotNullOrEmpty()][string] $Carpeta = (Get-CarpetaDeEstado),
        [ValidateSet('laboratorio', 'laboratorio-frio')][string] $Operacion = 'laboratorio'
    )
    $ruta = Join-Path $Carpeta 'LABORATORIO.progreso'
    if (Test-Path -LiteralPath $ruta -PathType Leaf) {
        if ($PSCmdlet.ShouldProcess($ruta, 'Retirar progreso del laboratorio')) {
            Remove-Item -LiteralPath $ruta -Force -ErrorAction SilentlyContinue
        }
    }
    Remove-ProgresoDeOperacion -Carpeta $Carpeta -Operacion $Operacion -Confirm:$false
}
