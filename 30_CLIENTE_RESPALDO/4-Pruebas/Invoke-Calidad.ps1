#Requires -Version 5.1
<#
    .SYNOPSIS
        Puerta de calidad estatica y pruebas del cliente de respaldo.
    .DESCRIPTION
        Ejecuta siempre las comprobaciones estructurales sin dependencias:
          1. Codificacion ASCII puro, finales LF y ausencia de BOM (.ps1/.psm1/.psd1).
          2. Sintaxis AST limpia en todos los archivos PowerShell.
          3. Integridad del manifiesto NasRespaldo.psd1, verbos aprobados y CmdletBinding.
          4. Invariante: ningun guion carga con punto a otro con param().
        Ademas ejecuta PSScriptAnalyzer y Pester 5 si estan instalados; cuando
        no lo estan lo informa explicitamente como OMITIDO sin fingir OK.
    .PARAMETER IncluirBateria
        Ejecuta tambien Invoke-Pruebas.ps1 al final.
    .EXAMPLE
        .\Invoke-Calidad.ps1
        .\Invoke-Calidad.ps1 -IncluirBateria
#>
[CmdletBinding()]
[OutputType([psobject])]
param(
    [switch] $IncluirBateria
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$raiz = Split-Path $PSScriptRoot -Parent
$detalles = New-Object System.Collections.Generic.List[psobject]
$fallos = 0
$omitidos = 0

function Add-Chequeo {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory)][string] $Nombre,
        [Parameter(Mandatory)][ValidateSet('OK', 'FALLO', 'OMITIDO')][string] $Estado,
        [string] $Detalle = ''
    )
    if ($Estado -eq 'FALLO') { $script:fallos++ }
    elseif ($Estado -eq 'OMITIDO') { $script:omitidos++ }
    $script:detalles.Add([pscustomobject]@{ Nombre = $Nombre; Estado = $Estado; Detalle = $Detalle })
}

$archivosPs = @(Get-ChildItem -LiteralPath $raiz -Recurse -File |
    Where-Object { $_.Extension -in @('.ps1', '.psm1', '.psd1') })

# 1. Codificacion ASCII puro, LF y sin BOM (ADR-0078).
$malCod = New-Object System.Collections.Generic.List[string]
foreach ($f in $archivosPs) {
    $bytes = [System.IO.File]::ReadAllBytes($f.FullName)
    $noAscii = @($bytes | Where-Object { $_ -gt 127 }).Count
    $cr = @($bytes | Where-Object { $_ -eq 13 }).Count
    if ($noAscii -gt 0 -or $cr -gt 0) {
        [void]$malCod.Add(('{0} (noAscii={1}, cr={2})' -f $f.Name, $noAscii, $cr))
    }
}
if ($malCod.Count -eq 0) {
    Add-Chequeo -Nombre 'Codificacion ASCII/LF sin BOM' -Estado 'OK' -Detalle ('{0} archivos verificados' -f $archivosPs.Count)
}
else {
    Add-Chequeo -Nombre 'Codificacion ASCII/LF sin BOM' -Estado 'FALLO' -Detalle ($malCod -join '; ')
}

# 2. Analisis sintactico AST.
$malAst = New-Object System.Collections.Generic.List[string]
foreach ($f in $archivosPs) {
    $errs = $null
    [void][System.Management.Automation.Language.Parser]::ParseFile($f.FullName, [ref]$null, [ref]$errs)
    if ($errs -and $errs.Count -gt 0) {
        [void]$malAst.Add(('{0} ({1} error/es)' -f $f.Name, $errs.Count))
    }
}
if ($malAst.Count -eq 0) {
    Add-Chequeo -Nombre 'Sintaxis PowerShell (AST)' -Estado 'OK' -Detalle ('{0} archivos sin errores' -f $archivosPs.Count)
}
else {
    Add-Chequeo -Nombre 'Sintaxis PowerShell (AST)' -Estado 'FALLO' -Detalle ($malAst -join '; ')
}

# 3. Manifiesto NasRespaldo, verbos aprobados y CmdletBinding.
$manifiesto = Join-Path $raiz '2-Nucleo\NasRespaldo\NasRespaldo.psd1'
try {
    $infoMod = Test-ModuleManifest -Path $manifiesto -ErrorAction Stop
    $aprobados = @((Get-Verb).Verb)
    $malVerbo = @($infoMod.ExportedFunctions.Keys | Where-Object { $aprobados -notcontains ($_ -split '-')[0] })
    $conComodin = ($infoMod.ExportedFunctions.Keys -contains '*')
    $sinCb = New-Object System.Collections.Generic.List[string]
    foreach ($pf in @(Get-ChildItem -LiteralPath (Join-Path $raiz '2-Nucleo\NasRespaldo\Publico') -Filter '*.ps1' -File)) {
        $astP = [System.Management.Automation.Language.Parser]::ParseFile($pf.FullName, [ref]$null, [ref]$null)
        foreach ($fn in $astP.FindAll({ param($n) $n -is [System.Management.Automation.Language.FunctionDefinitionAst] -and $n.Parent -is [System.Management.Automation.Language.NamedBlockAst] }, $false)) {
            $tieneCb = @($fn.Body.ParamBlock.Attributes | Where-Object { $_.TypeName.Name -match 'CmdletBinding' }).Count -gt 0
            if (-not $tieneCb) { [void]$sinCb.Add($fn.Name) }
        }
    }
    if ($conComodin -or $malVerbo.Count -gt 0 -or $sinCb.Count -gt 0) {
        Add-Chequeo -Nombre 'Modulo NasRespaldo y contrato de funciones' -Estado 'FALLO' `
            -Detalle ('comodin={0}, verbos={1}, sinCmdletBinding={2}' -f $conComodin, ($malVerbo -join ','), ($sinCb -join ','))
    }
    else {
        Add-Chequeo -Nombre 'Modulo NasRespaldo y contrato de funciones' -Estado 'OK' `
            -Detalle ('v{0}, {1} funciones exportadas, 100% verbos aprobados y CmdletBinding' -f $infoMod.Version, $infoMod.ExportedFunctions.Count)
    }
}
catch {
    Add-Chequeo -Nombre 'Modulo NasRespaldo y contrato de funciones' -Estado 'FALLO' -Detalle $_.Exception.Message
}

# 4. Invariante: ningun guion carga con punto a otro con param().
$conParam = @('respaldo.ps1', 'disco.ps1', 'semilla.ps1', 'verificar.ps1', 'laboratorio.ps1')
$prohibidas = New-Object System.Collections.Generic.List[string]
foreach ($carp in @('2-Nucleo', '1-Interfaz')) {
    foreach ($f in @(Get-ChildItem -LiteralPath (Join-Path $raiz $carp) -Filter '*.ps1' -File)) {
        $txt = Get-Content -LiteralPath $f.FullName -Raw
        foreach ($otro in $conParam) {
            if ($f.Name -eq $otro) { continue }
            if ($txt -match ('\.\s+["''][^"'']*' + [regex]::Escape($otro) + '["'']')) {
                [void]$prohibidas.Add(('{0} -> {1}' -f $f.Name, $otro))
            }
        }
    }
}
if ($prohibidas.Count -eq 0) {
    Add-Chequeo -Nombre 'Cero dot-source a guiones con param()' -Estado 'OK' -Detalle 'Ninguna carga prohibida'
}
else {
    Add-Chequeo -Nombre 'Cero dot-source a guiones con param()' -Estado 'FALLO' -Detalle ($prohibidas -join '; ')
}

# 5. PSScriptAnalyzer (si esta instalado).
$psa = Get-Module -ListAvailable -Name 'PSScriptAnalyzer' | Sort-Object Version -Descending | Select-Object -First 1
if ($psa) {
    Import-Module -Name 'PSScriptAnalyzer' -ErrorAction Stop
    $cfgPsa = Join-Path $PSScriptRoot 'PSScriptAnalyzerSettings.psd1'
    $hallazgos = @(Invoke-ScriptAnalyzer -Path $raiz -Recurse -Settings $cfgPsa)
    if ($hallazgos.Count -eq 0) {
        Add-Chequeo -Nombre 'PSScriptAnalyzer' -Estado 'OK' -Detalle ('v{0}: 0 hallazgos' -f $psa.Version)
    }
    else {
        Add-Chequeo -Nombre 'PSScriptAnalyzer' -Estado 'FALLO' -Detalle ('{0} hallazgo(s); primero: {1} ({2}:{3})' -f $hallazgos.Count, $hallazgos[0].RuleName, $hallazgos[0].ScriptName, $hallazgos[0].Line)
    }
}
else {
    Add-Chequeo -Nombre 'PSScriptAnalyzer' -Estado 'OMITIDO' -Detalle 'No instalado (Install-Module PSScriptAnalyzer -Scope CurrentUser)'
}

# 6. Pester 5 (si esta instalado >= 5.0).
$pester5 = Get-Module -ListAvailable -Name 'Pester' | Where-Object { $_.Version -ge [version]'5.0.0' } | Sort-Object Version -Descending | Select-Object -First 1
if ($pester5) {
    Import-Module -Name 'Pester' -MinimumVersion '5.0.0' -ErrorAction Stop
    $resP = Invoke-Pester -Path (Join-Path $PSScriptRoot 'NasRespaldo.Tests.ps1') -PassThru
    if ($resP.FailedCount -eq 0) {
        Add-Chequeo -Nombre 'Pester 5 (NasRespaldo.Tests.ps1)' -Estado 'OK' -Detalle ('v{0}: {1} pasan, 0 fallan' -f $pester5.Version, $resP.PassedCount)
    }
    else {
        Add-Chequeo -Nombre 'Pester 5 (NasRespaldo.Tests.ps1)' -Estado 'FALLO' -Detalle ('{0} fallan de {1}' -f $resP.FailedCount, $resP.TotalCount)
    }
}
else {
    Add-Chequeo -Nombre 'Pester 5 (NasRespaldo.Tests.ps1)' -Estado 'OMITIDO' -Detalle 'Requiere Pester >= 5.0 (Install-Module Pester -MinimumVersion 5.0 -Scope CurrentUser -Force)'
}

if ($IncluirBateria) {
    $bat = & (Join-Path $PSScriptRoot 'Invoke-Pruebas.ps1')
    if ($bat -and $bat.Fallan -eq 0) {
        Add-Chequeo -Nombre 'Bateria Invoke-Pruebas.ps1' -Estado 'OK' -Detalle ('{0}/{1} pasan' -f $bat.Pasan, $bat.Total)
    }
    else {
        Add-Chequeo -Nombre 'Bateria Invoke-Pruebas.ps1' -Estado 'FALLO' -Detalle ('{0} fallan' -f $(if ($bat) { $bat.Fallan } else { '?' }))
    }
}

foreach ($d in $detalles) {
    Write-Information ('{0,-7} {1} -- {2}' -f $d.Estado, $d.Nombre, $d.Detalle) -InformationAction Continue
}

return [pscustomobject]@{
    Correcto = ($fallos -eq 0)
    Fallos   = $fallos
    Omitidos = $omitidos
    Detalles = $detalles.ToArray()
}
