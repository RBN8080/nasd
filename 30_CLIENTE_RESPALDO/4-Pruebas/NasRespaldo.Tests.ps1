#Requires -Version 5.1
<#
    .SYNOPSIS
        Suite Pester 5 del modulo NasRespaldo, progreso atomico y consola TUI.
    .DESCRIPTION
        Convive con Invoke-Pruebas.ps1 (bateria de integracion sin dependencias).
        Requiere Pester >= 5.0:
            Install-Module Pester -MinimumVersion 5.0 -Scope CurrentUser -Force
            Invoke-Pester -Path .\4-Pruebas\NasRespaldo.Tests.ps1 -Output Detailed
#>

BeforeAll {
    $script:RaizCliente = Split-Path $PSScriptRoot -Parent
    $script:RutaManifiesto = Join-Path $script:RaizCliente '2-Nucleo\NasRespaldo\NasRespaldo.psd1'
    Import-Module -Name $script:RutaManifiesto -Force -DisableNameChecking
    . (Join-Path $script:RaizCliente '1-Interfaz\tablero.ps1')
}

Describe 'Modulo NasRespaldo' {
    It 'valida su manifiesto sin comodines en FunctionsToExport' {
        $m = Test-ModuleManifest -Path $script:RutaManifiesto -ErrorAction Stop
        $m.Version.ToString() | Should -Be '1.0.0'
        $m.ExportedFunctions.Count | Should -Be 66
        ($m.ExportedFunctions.Keys -contains '*') | Should -BeFalse
    }

    It 'exporta solo verbos aprobados de PowerShell' {
        $aprobados = @((Get-Verb).Verb)
        $mod = Get-Module -Name 'NasRespaldo'
        $noAprobados = @($mod.ExportedFunctions.Keys | Where-Object { $aprobados -notcontains ($_ -split '-')[0] })
        $noAprobados.Count | Should -Be 0
    }
}

Describe 'Contrato unificado de progreso atomico (PROGRESO.json)' {
    BeforeEach {
        $script:TempProg = Join-Path ([System.IO.Path]::GetTempPath()) ('nas_pester_' + [guid]::NewGuid().ToString('N'))
        New-Item -ItemType Directory -Path $script:TempProg -Force | Out-Null
    }
    AfterEach {
        if (Test-Path -LiteralPath $script:TempProg) {
            Remove-Item -LiteralPath $script:TempProg -Recurse -Force -ErrorAction SilentlyContinue
        }
    }

    It 'publica, lee y formatea avance activo con porcentaje y ETA' {
        Write-ProgresoDeOperacion -Operacion 'disco' -Fase 'pasada-equipo' -Objetivo 'Disco frio' `
            -Elemento '01_DOCS' -Hechos 50MB -Totales 100MB -Unidad 'bytes' -TasaMBs 25.0 `
            -Carpeta $script:TempProg -Confirm:$false
        $p = Read-ProgresoDeOperacion -Carpeta $script:TempProg
        $p.Activo | Should -BeTrue
        $p.Porcentaje | Should -Be 50
        $p.EtaSegundos | Should -Be 2
        (Format-ProgresoDeOperacion -Progreso $p) | Should -Match '50%'
        Remove-ProgresoDeOperacion -Carpeta $script:TempProg -Operacion 'disco' -Confirm:$false
        (Test-Path -LiteralPath (Join-Path $script:TempProg 'PROGRESO.json')) | Should -BeFalse
    }
}

Describe 'Consola TUI adaptable, semantica estricta y menu [1]-[6]' {
    It 'calcula perfiles Compacto, Estandar y Amplio sin desbordar el ancho' {
        foreach ($caso in @(
                @{ W = 72;  Perfil = 'Compacto'; Cols = 1 }
                @{ W = 100; Perfil = 'Estandar'; Cols = 2 }
                @{ W = 140; Perfil = 'Amplio';   Cols = 2 })) {
            $g = Get-GeometriaDeConsola -AnchoConsola $caso.W
            $g.Perfil | Should -Be $caso.Perfil
            $g.ColumnasMenu | Should -Be $caso.Cols
            ($g.ColRaiz + $g.ColPol + $g.ColDelta + $g.ColNodo + $g.ColDisco) | Should -BeLessOrEqual $g.Ancho
        }
    }

    It 'nunca pinta INTEGRIDAD en verde si falta el ensayo DR' {
        $hoy = (Get-Date).AddHours(-1).ToString('s')
        $r = Resolve-EstadoDeIntegridad -Estado @{
            huellas_estado = 'OK'; huellas_momento = $hoy
            muestreo_estado = 'OK'; muestreo_momento = $hoy
            semilla_estado = 'OK'; semilla_momento = $hoy
        }
        $r.Tono | Should -Be 'Ambar'
    }

    It 'escala el veredicto global a Falla si proteccion en vivo falla' {
        $protRojo = [pscustomobject]@{ Tono = 'Rojo'; Regla = $true; Partes = @('fallo'); Motivo = 'centinela alterado' }
        $v = Resolve-VeredictoDeConsola -EstadoIndicador 'Protegido' -DetalleIndicador 'ok' -Proteccion $protRojo
        $v.Estado | Should -Be 'Falla'
    }

    It 'expone menu secuencial numerico [1]-[6] sin celdas vacias ni sangrias manuales' {
        $m = Get-MenuDelTablero
        ((@($m.Items | ForEach-Object { $_.Tecla })) -join '') | Should -Be '123456'
        $celdas = @($m.Filas | ForEach-Object { $_ })
        @($celdas | Where-Object { [string]::IsNullOrWhiteSpace($_) }).Count | Should -Be 0
        @($celdas | Where-Object { $_ -match '\]\s{2,}' }).Count | Should -Be 0
    }
}
