@{
    RootModule        = 'NasRespaldo.psm1'
    ModuleVersion     = '1.0.0'
    GUID              = '7f4d2e9a-3c6b-4b8e-9a12-5d8f0c3e7b11'
    Author            = 'RBN8080'
    Description       = 'Nucleo compartido del cliente de respaldo (configuracion, registro, estado, progreso, robocopy, cadencia, centinelas, disco frio y laboratorio VM).'
    PowerShellVersion = '5.1'

    FunctionsToExport = @(
        'ConvertFrom-Jsonc',
        'Get-ConfiguracionRespaldo',
        'Set-ContenidoAtomico',
        'Hide-VentanaDeConsola',
        'Write-LineaDeRegistro',
        'Get-CarpetaDeRegistro',
        'Remove-RegistrosAntiguos',
        'Write-RegistroRespaldo',
        'Test-RutaUnc',
        'Test-DestinoNodo',
        'Get-FirmaDeDisco',
        'Get-DiscoFrio',
        'Get-AutomatismoDeDiscoFrio',
        'Test-DebeDispararDiscoFrio',
        'Format-NotificacionDeDiscoFrio',
        'Invoke-Robocopy',
        'Get-RutaEnDestino',
        'Test-LineaDeRobocopyVacia',
        'Get-EspacioLibre',
        'Test-RastroDeCorridaProgramada',
        'Test-MotorIntacto',
        'Get-CarpetaDeEstado',
        'Write-EstadoRespaldo',
        'Read-EstadoRespaldo',
        'Enter-MarcaDeCorrida',
        'Exit-MarcaDeCorrida',
        'Get-MarcaDeCorrida',
        'Write-EstadoDelDisco',
        'Write-EstadoDelLaboratorio',
        'Resolve-EstadoVigente',
        'Publish-EstadoAlNodo',
        'Write-EstadoPorRaiz',
        'Read-EstadoPorRaiz',
        'Write-EstadoDeComprobacion',
        'Get-CadenciaDeCorrida',
        'Set-HorarioDeRespaldo',
        'Get-VentanaDeCorrida',
        'Get-ProximaVentanaDeCorrida',
        'Test-Centinela',
        'Set-RutaDeCentinelaEnConfig',
        'Move-RaizRenombradaEnDestino',
        'New-Centinela',
        'Get-HuellaDeArchivo',
        'Get-ArchivosEnVuelo',
        'Test-HuellaEnVuelo',
        'Repair-ArchivosEnVuelo',
        'Get-FichaDelLaboratorio',
        'Test-ExcluidoDelLaboratorio',
        'Get-InventarioDelLaboratorio',
        'Get-VmAbiertaDelLaboratorio',
        'Test-VmxEnMarcha',
        'Set-VmxSimulado',
        'Write-EstadoDelLaboratorioPorVm',
        'Read-EstadoDelLaboratorioPorVm',
        'Format-BytesDelLaboratorio',
        'Get-ResumenDeVmsDelLaboratorio',
        'Get-FilasDelLaboratorio',
        'Get-ResumenDelLaboratorio',
        'Read-LecturaDelLaboratorio',
        'Write-ProgresoDeOperacion',
        'Read-ProgresoDeOperacion',
        'Remove-ProgresoDeOperacion',
        'Format-ProgresoDeOperacion',
        'Write-ProgresoDelLaboratorio',
        'Read-ProgresoDelLaboratorio',
        'Remove-ProgresoDelLaboratorio'
    )

    CmdletsToExport   = @()
    VariablesToExport = @()
    AliasesToExport   = @()
}
