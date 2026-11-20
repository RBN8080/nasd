@{
    # Perfil de analisis estatico para 30_CLIENTE_RESPALDO (Microsoft PSScriptAnalyzer).
    # Compatible con Windows PowerShell 5.1 (motor) y PowerShell 7.4+ (panel Spectre).
    Severity     = @('Error', 'Warning')

    ExcludeRules = @(
        # ADR-0078: los archivos PowerShell del repositorio son ASCII puro con LF sin BOM.
        'PSUseBOMForUnicodeEncodedFile',
        # La interfaz TUI (1-Interfaz) e Instalar.ps1 usan salida directa de consola a proposito.
        'PSAvoidUsingWriteHost'
    )

    Rules        = @{
        PSUseCompatibleSyntax = @{
            Enable         = $true
            TargetVersions = @('5.1', '7.4')
        }
    }
}
