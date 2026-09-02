#Requires -Version 5.1
<#
    .SYNOPSIS
        La semilla nivel 1: la lista de lo que habia, no los programas.

    .DESCRIPTION
        Contrato: 30_CLIENTE_RESPALDO.md seccion 5. Aplicacion directa de la
        seccion 3.2: "la fuente puede reconstruir esto sin mi?" Si -> no se
        respalda nada; se respalda la LISTA.

        Se REGENERA en cada corrida. Total: menos de 1 MB, frente a los cientos
        de GB que costaria guardar lo que esas listas describen.

        NIVELES (seccion 5.1). Esto es el NIVEL 1, que es lo que la Fase 2 pide:
          nada          ~3 dias
          1 inventario    ~6 h   <-- aqui
          2 entorno       ~3 h
          3 ejecutable    ~1 h   <-- Fase 4
        El salto de 3 h a 1 h NO viene de guardar mas: entre el 2 y el 3 no se
        guarda ni un dato nuevo. La diferencia es que la lista deja de leerse y
        empieza a ejecutarse.

        LAS LLAVES SSH NO ENTRAN AQUI, Y ES DELIBERADO. La seccion 5 las lista,
        pero su destino es `_CONFIGS`, que sigue SIN POBLAR y deshabilitada por
        ADR-0080: copiar llaves privadas a un recurso de red es una decision de
        seguridad distinta, y hasta que exista el contenedor cifrado no viajan.
        `.gitconfig` si entra: no es un secreto.

        HALLAZGO DEL 2026-08-27 que hace esto obligatorio: la sincronizacion de
        configuracion de VS Code esta APAGADA, asi que la fuente NO puede
        reponer la lista de extensiones. Sin semilla, esa lista no existe en
        ningun sitio.

    .PARAMETER Destino
        Carpeta donde escribir la semilla.

    .EXAMPLE
        .\semilla.ps1 -Destino '\\192.168.1.38\datos\01_BACKUP\EQUIPO-01\_SEMILLA'
#>
[CmdletBinding(SupportsShouldProcess)]
[OutputType([psobject])]
param(
    [Parameter(Mandatory)]
    [ValidateNotNullOrEmpty()]
    [string] $Destino
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

. "$PSScriptRoot\comun.ps1"

function Get-PiezaDeSemilla {
    <#
        .SYNOPSIS
            Ejecuta una pieza de la semilla sin dejar que su fallo tumbe el resto.
        .DESCRIPTION
            Una pieza que falla NO puede impedir que se guarden las demas:
            perder la lista de extensiones porque winget no respondio seria
            cambiar un problema pequeno por uno grande. Lo que falla se anota y
            se sigue, y el manifiesto lo dice.
        .PARAMETER Nombre
            Nombre del archivo que produce.
        .PARAMETER Bloque
            Lo que hay que ejecutar. Debe devolver texto.
    #>
    [CmdletBinding()]
    [OutputType([psobject])]
    param(
        [Parameter(Mandatory)][ValidateNotNullOrEmpty()][string] $Nombre,
        [Parameter(Mandatory)][scriptblock] $Bloque
    )
    try {
        $texto = & $Bloque
        if ($null -eq $texto) { $texto = '' }
        return [pscustomobject]@{
            Nombre = $Nombre
            Texto  = ($texto | Out-String)
            Error  = $null
        }
    }
    catch {
        Write-RegistroRespaldo -Nivel 'ATENCION' -Etapa 'semilla' -Mensaje "Pieza '$Nombre' fallo: $($_.Exception.Message)"
        return [pscustomobject]@{ Nombre = $Nombre; Texto = $null; Error = $_.Exception.Message }
    }
}

function New-SemillaNivel1 {
    <#
        .SYNOPSIS
            Escribe la semilla completa en la carpeta indicada.
        .PARAMETER Carpeta
            Destino.
    #>
    [CmdletBinding(SupportsShouldProcess)]
    [OutputType([psobject])]
    param(
        [Parameter(Mandatory)][ValidateNotNullOrEmpty()][string] $Carpeta
    )

    if (-not $PSCmdlet.ShouldProcess($Carpeta, 'Regenerar la semilla nivel 1')) { return $null }

    $piezas = @(
        Get-PiezaDeSemilla -Nombre 'programas-winget.json' -Bloque {
            $tmp = Join-Path $env:TEMP ('winget_{0}.json' -f [guid]::NewGuid().ToString('N'))
            try {
                & winget.exe export --output $tmp --accept-source-agreements --disable-interactivity 2>&1 | Out-Null
                if (Test-Path -LiteralPath $tmp) { Get-Content -LiteralPath $tmp -Raw -Encoding UTF8 }
                else { throw 'winget export no produjo archivo' }
            }
            finally { if (Test-Path -LiteralPath $tmp) { Remove-Item -LiteralPath $tmp -Force -ErrorAction SilentlyContinue } }
        }

        Get-PiezaDeSemilla -Nombre 'vscode-extensiones.txt' -Bloque {
            # Get-Command devuelve VARIAS coincidencias -aqui code.cmd y code- y
            # tomar .Source de la coleccion entera concatena las rutas en una
            # sola cadena que no existe. Hay que quedarse con la primera.
            $code = @(Get-Command code -CommandType Application -ErrorAction SilentlyContinue) |
                Select-Object -First 1
            if (-not $code) { throw 'VS Code no esta en el PATH' }
            $salida = & $code.Source --list-extensions 2>&1
            if ($LASTEXITCODE -ne 0) { throw "code --list-extensions devolvio $LASTEXITCODE" }
            $salida
        }

        Get-PiezaDeSemilla -Nombre 'vscode-settings.json' -Bloque {
            $p = Join-Path $env:APPDATA 'Code\User\settings.json'
            if (-not (Test-Path -LiteralPath $p)) { throw "No existe $p" }
            Get-Content -LiteralPath $p -Raw -Encoding UTF8
        }

        Get-PiezaDeSemilla -Nombre 'vscode-keybindings.json' -Bloque {
            $p = Join-Path $env:APPDATA 'Code\User\keybindings.json'
            if (-not (Test-Path -LiteralPath $p)) { throw "No existe $p" }
            Get-Content -LiteralPath $p -Raw -Encoding UTF8
        }

        Get-PiezaDeSemilla -Nombre 'tareas-programadas.csv' -Bloque {
            Get-ScheduledTask -ErrorAction Stop |
                Where-Object { $_.TaskPath -notlike '\Microsoft\*' } |
                Select-Object TaskName, TaskPath, State, Description |
                ConvertTo-Csv -NoTypeInformation
        }

        Get-PiezaDeSemilla -Nombre 'maquinas-virtuales-vmx.txt' -Bloque {
            # Solo la RUTA de los .vmx: la configuracion, no los 240 GB de discos.
            $donde = @('D:\', (Join-Path $env:USERPROFILE 'Documents\Virtual Machines')) |
                Where-Object { Test-Path -LiteralPath $_ }
            if (-not $donde) { throw 'Ninguna carpeta de maquinas virtuales encontrada' }
            Get-ChildItem -Path $donde -Filter '*.vmx' -Recurse -File -ErrorAction SilentlyContinue |
                Select-Object -ExpandProperty FullName
        }

        Get-PiezaDeSemilla -Nombre 'gitconfig.txt' -Bloque {
            $p = Join-Path $env:USERPROFILE '.gitconfig'
            if (-not (Test-Path -LiteralPath $p)) { throw "No existe $p" }
            Get-Content -LiteralPath $p -Raw -Encoding UTF8
        }
    )

    if (-not (Test-Path -LiteralPath $Carpeta)) {
        New-Item -ItemType Directory -Path $Carpeta -Force | Out-Null
    }

    $escritas = New-Object System.Collections.Generic.List[psobject]
    foreach ($p in $piezas) {
        if ($null -ne $p.Error) { continue }
        $ruta = Join-Path $Carpeta $p.Nombre
        Set-ContenidoAtomico -Ruta $ruta -Contenido $p.Texto -Confirm:$false
        $escritas.Add([pscustomobject]@{ Nombre = $p.Nombre; Bytes = (Get-Item -LiteralPath $ruta).Length })
    }

    # El manifiesto dice QUE hay y, sobre todo, QUE FALTA. Una semilla que calla
    # lo que no pudo guardar es peor que una que lo grita: se descubre el dia de
    # la restauracion, que es el peor dia posible para descubrir nada.
    $fallidas = @($piezas | Where-Object { $null -ne $_.Error })
    [int64]$bytes = 0
    foreach ($e in $escritas) { $bytes += $e.Bytes }

    $m = New-Object System.Text.StringBuilder
    [void]$m.AppendLine('SEMILLA NIVEL 1 - la lista de lo que habia, no los programas')
    [void]$m.AppendLine('Contrato: 30_CLIENTE_RESPALDO.md seccion 5')
    [void]$m.AppendLine(('Regenerada: {0}' -f (Get-Date -Format 's')))
    [void]$m.AppendLine(('Equipo    : {0}' -f $env:COMPUTERNAME))
    [void]$m.AppendLine(('Total     : {0} piezas, {1:N0} bytes' -f $escritas.Count, $bytes))
    [void]$m.AppendLine('')
    [void]$m.AppendLine('GUARDADO:')
    foreach ($e in $escritas) { [void]$m.AppendLine(('  {0,-30} {1,10:N0} B' -f $e.Nombre, $e.Bytes)) }
    if ($fallidas.Count -gt 0) {
        [void]$m.AppendLine('')
        [void]$m.AppendLine('NO SE PUDO GUARDAR -- no es un detalle, es un hueco de recuperacion:')
        foreach ($f in $fallidas) { [void]$m.AppendLine(('  {0,-30} {1}' -f $f.Nombre, $f.Error)) }
    }
    [void]$m.AppendLine('')
    [void]$m.AppendLine('FUERA A PROPOSITO: las llaves SSH. Su destino es _CONFIGS, que sigue sin')
    [void]$m.AppendLine('poblar y deshabilitada por ADR-0080. No viajan hasta que exista el')
    [void]$m.AppendLine('contenedor cifrado.')

    Set-ContenidoAtomico -Ruta (Join-Path $Carpeta 'MANIFIESTO.txt') -Contenido $m.ToString() -Confirm:$false

    Write-RegistroRespaldo -Etapa 'semilla' `
        -Mensaje ('Semilla regenerada: {0} piezas, {1:N0} bytes, {2} fallidas.' -f $escritas.Count, $bytes, $fallidas.Count)

    return [pscustomobject]@{
        Carpeta  = $Carpeta
        Escritas = $escritas.ToArray()
        Fallidas = $fallidas
        Bytes    = $bytes
    }
}

if ($MyInvocation.InvocationName -eq '.') {
    Write-Verbose 'semilla.ps1 cargado con punto: se exponen las funciones y no se genera nada.'
    return
}

New-SemillaNivel1 -Carpeta $Destino -Confirm:$false
