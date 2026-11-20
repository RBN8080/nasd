#Requires -Version 5.1
<#
    NasRespaldo.psm1  -  Cargador del modulo de nucleo del cliente de respaldo.

    Separa el nucleo compartido en dominios cohesivos bajo Publico\ y controla
    las exportaciones explicitamente desde NasRespaldo.psd1 (guia oficial de
    modulos de script de Microsoft PowerShell y PoshCode Practice and Style Guide).
#>

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$script:RaizCliente            = Split-Path (Split-Path $PSScriptRoot -Parent) -Parent
$script:RobocopyMaximoCorrecto = 7
$script:DiasEntreMuestreos     = 7
$script:VmxSimulado            = $null

$carpetaPublico = Join-Path $PSScriptRoot 'Publico'
foreach ($archivo in @(Get-ChildItem -LiteralPath $carpetaPublico -Filter '*.ps1' -File | Sort-Object -Property Name)) {
    . $archivo.FullName
}
