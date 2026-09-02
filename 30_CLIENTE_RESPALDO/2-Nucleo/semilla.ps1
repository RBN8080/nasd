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

function Get-GuionDeRestauracion {
    <#
        .SYNOPSIS
            Devuelve el texto de RESTAURAR.ps1, que viaja DENTRO de la semilla.
        .DESCRIPTION
            Nivel 3 de la seccion 5.1. Tiene que ser AUTOSUFICIENTE: se ejecuta
            en una maquina limpia que no tiene este repositorio, ni el motor, ni
            nada. Por eso es un texto literal y no una referencia.

            Se cronometra contra el objetivo de ~1 h del nivel 3, que es el
            criterio 11 de la seccion 15.
    #>
    [CmdletBinding()]
    [OutputType([string])]
    param()

    # Comilla simple: NADA de aqui se expande al generarlo. El guion se escribe
    # tal cual y resuelve sus variables cuando se EJECUTA, en la maquina nueva.
    return @'
#Requires -Version 5.1
<#
    .SYNOPSIS
        Restaura el entorno de trabajo desde esta semilla. Nivel 3.

    .DESCRIPTION
        Este archivo viaja DENTRO de la semilla y es autosuficiente: no necesita
        el repositorio, ni el motor de respaldo, ni nada mas que Windows.

        LO QUE HACE Y LO QUE NO. Restaura el ENTORNO -programas, editor, tareas,
        identidad de git-, no los DATOS. Los datos estan en el nodo y en el
        disco frio, y se traen copiandolos: no hace falta un guion para eso, y
        un guion que moviera datos el dia peor seria una pieza mas que puede
        fallar.

        SE CRONOMETRA. El objetivo del nivel 3 es ~1 hora contra las ~3 horas
        del nivel 2 y los ~3 dias de no tener nada. Este guion mide cada paso y
        escribe el total, porque la seccion 5.1 pide tiempo MEDIDO, no estimado.

    .PARAMETER Semilla
        Carpeta de la semilla. Por omision, la de este archivo.

    .PARAMETER SoloSimular
        Ensena lo que haria y no instala nada.

    .EXAMPLE
        .\RESTAURAR.ps1 -SoloSimular
        .\RESTAURAR.ps1
#>
[CmdletBinding(SupportsShouldProcess)]
param(
    [string] $Semilla = $PSScriptRoot,
    [switch] $SoloSimular
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$pasos = New-Object System.Collections.Generic.List[psobject]
$relojTotal = [System.Diagnostics.Stopwatch]::StartNew()

function Invoke-Paso {
    param(
        [Parameter(Mandatory)][string] $Nombre,
        [Parameter(Mandatory)][string] $Archivo,
        [Parameter(Mandatory)][scriptblock] $Bloque
    )
    $ruta = Join-Path $Semilla $Archivo
    if (-not (Test-Path -LiteralPath $ruta -PathType Leaf)) {
        Write-Warning "$Nombre : falta '$Archivo' en la semilla. Se salta."
        $script:pasos.Add([pscustomobject]@{ Paso = $Nombre; Segundos = 0; Estado = 'FALTA EN LA SEMILLA' })
        return
    }
    if ($SoloSimular) {
        Write-Host "[simulacion] $Nombre  <- $Archivo"
        $script:pasos.Add([pscustomobject]@{ Paso = $Nombre; Segundos = 0; Estado = 'simulado' })
        return
    }
    $reloj = [System.Diagnostics.Stopwatch]::StartNew()
    try {
        & $Bloque $ruta
        $reloj.Stop()
        $script:pasos.Add([pscustomobject]@{ Paso = $Nombre; Segundos = [math]::Round($reloj.Elapsed.TotalSeconds, 1); Estado = 'OK' })
        Write-Host ("OK  {0}  ({1:N1} s)" -f $Nombre, $reloj.Elapsed.TotalSeconds)
    }
    catch {
        $reloj.Stop()
        # Un paso que falla NO detiene la restauracion: el dia que esto se use
        # de verdad, quedarse a medias por una pieza es el peor resultado.
        $script:pasos.Add([pscustomobject]@{ Paso = $Nombre; Segundos = [math]::Round($reloj.Elapsed.TotalSeconds, 1); Estado = "FALLO: $($_.Exception.Message)" })
        Write-Warning "$Nombre fallo: $($_.Exception.Message)"
    }
}

Write-Host ''
Write-Host '== RESTAURACION DESDE LA SEMILLA - nivel 3 =='
Write-Host "   semilla: $Semilla"
Write-Host ''

Invoke-Paso -Nombre 'Programas (winget import)' -Archivo 'programas-winget.json' -Bloque {
    param($ruta)
    & winget.exe import --import-file $ruta --accept-package-agreements --accept-source-agreements --ignore-versions --disable-interactivity
    # winget devuelve distinto de cero si algun paquete ya estaba o no se
    # encontro. Eso no es un fallo de la restauracion: se anota y se sigue.
    if ($LASTEXITCODE -ne 0) { Write-Warning "winget import devolvio $LASTEXITCODE (normal si algo ya estaba instalado)" }
}

Invoke-Paso -Nombre 'Extensiones de VS Code' -Archivo 'vscode-extensiones.txt' -Bloque {
    param($ruta)
    $code = @(Get-Command code -CommandType Application -ErrorAction SilentlyContinue) | Select-Object -First 1
    if (-not $code) { throw 'VS Code todavia no esta en el PATH. Reabra la consola y repita solo este paso.' }
    $n = 0
    foreach ($ext in (Get-Content -LiteralPath $ruta -Encoding UTF8)) {
        if ([string]::IsNullOrWhiteSpace($ext)) { continue }
        & $code.Source --install-extension $ext.Trim() --force | Out-Null
        $n++
    }
    Write-Host "    $n extensiones"
}

Invoke-Paso -Nombre 'Ajustes de VS Code' -Archivo 'vscode-settings.json' -Bloque {
    param($ruta)
    $destino = Join-Path $env:APPDATA 'Code\User'
    if (-not (Test-Path -LiteralPath $destino)) { New-Item -ItemType Directory -Path $destino -Force | Out-Null }
    Copy-Item -LiteralPath $ruta -Destination (Join-Path $destino 'settings.json') -Force
}

Invoke-Paso -Nombre 'Atajos de VS Code' -Archivo 'vscode-keybindings.json' -Bloque {
    param($ruta)
    $destino = Join-Path $env:APPDATA 'Code\User'
    if (-not (Test-Path -LiteralPath $destino)) { New-Item -ItemType Directory -Path $destino -Force | Out-Null }
    Copy-Item -LiteralPath $ruta -Destination (Join-Path $destino 'keybindings.json') -Force
}

Invoke-Paso -Nombre 'Identidad de git' -Archivo 'gitconfig.txt' -Bloque {
    param($ruta)
    Copy-Item -LiteralPath $ruta -Destination (Join-Path $env:USERPROFILE '.gitconfig') -Force
}

# LAS TAREAS PROGRAMADAS NO SE REARMAN SOLAS, Y ES DELIBERADO. El CSV dice
# CUALES habia; volver a crearlas exige saber que ejecuta cada una, y ese dato
# no cabe en un inventario. Re-registrar a ciegas tareas que apuntan a rutas que
# quiza ya no existen es peor que no hacerlo.
$csvTareas = Join-Path $Semilla 'tareas-programadas.csv'
if (Test-Path -LiteralPath $csvTareas) {
    Write-Host ''
    Write-Host 'TAREAS PROGRAMADAS QUE HABIA -- se rearman a mano, ver RECREAR.md:'
    Import-Csv -LiteralPath $csvTareas | ForEach-Object { Write-Host ("    {0}{1}" -f $_.TaskPath, $_.TaskName) }
}

$relojTotal.Stop()
Write-Host ''
Write-Host '== RESUMEN =='
$pasos | Format-Table Paso, Segundos, Estado -AutoSize
Write-Host ("TIEMPO TOTAL MEDIDO: {0:N1} min" -f $relojTotal.Elapsed.TotalMinutes)
Write-Host 'Objetivo del nivel 3 (seccion 5.1): ~1 hora hasta volver a trabajar.'
Write-Host ''
Write-Host 'LO QUE FALTA Y NO LO HACE ESTE GUION:'
Write-Host '  - Traer los DATOS del nodo o del disco frio (son copias, no instalacion)'
Write-Host '  - Las llaves SSH: viajan aparte, cifradas (ADR-0080)'
Write-Host '  - Rearmar las tareas programadas listadas arriba'
Write-Host '  - Lo de clase C: ver RECREAR.md'
'@
}

function Get-TextoRecrear {
    <#
        .SYNOPSIS
            Devuelve RECREAR.md, leyendo su plantilla de 3-Config.
        .DESCRIPTION
            La seccion 8 lo declara dentro de `_SISTEMA/`. Aqui viaja con la
            semilla, que es donde de verdad hace falta el dia de la restauracion.

            EL TEXTO VIVE EN UNA PLANTILLA .md Y NO AQUI DENTRO, y no es un
            capricho: los .ps1 de este cliente se escriben SIN ACENTOS porque
            PowerShell 5.1 lee un .ps1 sin BOM como ANSI y los rompe (ADR-0078).
            Un documento que se lee el peor dia del ano no puede estar escrito
            sin acentos por una limitacion del interprete, asi que se separa:
            el guion queda ASCII y el documento conserva su ortografia.
        .PARAMETER Plantilla
            Ruta de la plantilla. Por omision la de 3-Config.
    #>
    [CmdletBinding()]
    [OutputType([string])]
    param(
        [ValidateNotNullOrEmpty()]
        [string] $Plantilla = (Join-Path (Split-Path $PSScriptRoot -Parent) '3-Config\RECREAR.plantilla.md')
    )

    if (-not (Test-Path -LiteralPath $Plantilla -PathType Leaf)) {
        return "# RECREAR`
`
No se encontro la plantilla en $Plantilla. La semilla se genero sin este documento."
    }
    $texto = Get-Content -LiteralPath $Plantilla -Raw -Encoding UTF8
    $texto = $texto.Replace('{{FECHA}}', (Get-Date -Format 's'))
    $texto = $texto.Replace('{{EQUIPO}}', $env:COMPUTERNAME)
    return $texto
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

    # NIVEL 3: la lista deja de leerse y empieza a ejecutarse (seccion 5.1).
    # Entre el nivel 2 y el 3 NO se guarda ni un dato nuevo. La diferencia es
    # que una lista ahorra RECORDAR y un ejecutable ahorra TECLEAR, que es donde
    # se van las horas. Por eso RESTAURAR.ps1 viaja DENTRO de la semilla: tiene
    # que funcionar en una maquina limpia que no ha visto este repositorio.
    Set-ContenidoAtomico -Ruta (Join-Path $Carpeta 'RESTAURAR.ps1') -Contenido (Get-GuionDeRestauracion) -Confirm:$false
    Set-ContenidoAtomico -Ruta (Join-Path $Carpeta 'RECREAR.md') -Contenido (Get-TextoRecrear) -Confirm:$false

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
