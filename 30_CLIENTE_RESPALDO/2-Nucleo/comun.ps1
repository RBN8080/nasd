#Requires -Version 5.1
<#
    comun.ps1  -  Fachada ligera del modulo de nucleo NasRespaldo.

    Contrato: 30_CLIENTE_RESPALDO.md seccion 16.bis.

    Toda la implementacion vive ahora en el modulo con manifiesto
    2-Nucleo\NasRespaldo\ (NasRespaldo.psd1 / NasRespaldo.psm1 / Publico\*.ps1),
    separada por dominio y con exportacion explicita de funciones. Este archivo
    se conserva como punto de entrada retrocompatible para los guiones existentes
    que cargan `. "$PSScriptRoot\comun.ps1"` sin `param()`.

    Uso:
        . "$PSScriptRoot\comun.ps1"
#>

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

# Constantes compartidas en el ambito del llamador (p. ej. verificar.ps1).
$script:RobocopyMaximoCorrecto = 7
$script:DiasEntreMuestreos     = 7

$manifiestoNasRespaldo = Join-Path $PSScriptRoot 'NasRespaldo\NasRespaldo.psd1'
if (-not (Get-Module -Name 'NasRespaldo')) {
    Import-Module -Name $manifiestoNasRespaldo -Global -DisableNameChecking -Verbose:$false
}
