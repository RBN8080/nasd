#Requires -Version 5.1

# Modulo NasRespaldo - dominio: Laboratorio.ps1

function Get-FichaDelLaboratorio {
    <#
        .SYNOPSIS
            El bloque `laboratorio` de la configuracion, validado. $null si no hay.
        .DESCRIPTION
            Sin bloque no hay laboratorio y no es un error: el tablero se pinta
            igual. Con bloque, cada clave de la que depende escribir en el nodo
            se exige aqui, y no a mitad de un guardado.
        .PARAMETER Configuracion
            El objeto de configuracion completo.
    #>
    [CmdletBinding()]
    [OutputType([psobject])]
    param(
        [Parameter(Mandatory)][psobject] $Configuracion
    )

    if ($Configuracion.PSObject.Properties.Name -notcontains 'laboratorio') { return $null }
    $l = $Configuracion.laboratorio
    $nombres = @($l.PSObject.Properties.Name)
    $faltan = @(@('origen', 'destino', 'destinoEnLaPi', 'umbralDeltaBytes', 'bloqueBytes') |
            Where-Object { $nombres -notcontains $_ })
    if ($faltan.Count -gt 0) { throw "El bloque laboratorio no declara: $($faltan -join ', ')" }

    # LA MISMA GUARDA QUE EL NODO (seccion 12.septies, tropiezo 1). Aqui pesa
    # mas: el laboratorio es un espejo, y un destino mal escrito no recibiria
    # una copia de mas sino que se quedaria sin la buena.
    if (-not (Test-RutaUnc -Ruta ('' + $l.destino))) { throw "laboratorio.destino no es una ruta UNC: $($l.destino)" }

    $bloque = [int]$l.bloqueBytes
    if ($bloque -lt 4096 -or $bloque -gt 64MB) { throw "laboratorio.bloqueBytes fuera de rango: $bloque" }

    $opcional = {
        param($Nombre, $PorOmision)
        if ($nombres -contains $Nombre) { return $l.$Nombre }
        return $PorOmision
    }

    # LA COPIA FRIA DEL LABORATORIO (ADR-0104, ampliacion). Peso equivalente al
    # del nodo: la ruta es relativa a la raiz del disco, nunca una letra. Por
    # omision espeja la del nodo: <prefijoEquipo>\D\VMware_LAB.
    $frio = [pscustomobject]@{ Habilitado = $false; Relativa = '' }
    if ($nombres -contains 'discoFrio') {
        $lf = $l.discoFrio
        $nf = @($lf.PSObject.Properties.Name)
        $habilitado = $true
        if ($nf -contains 'habilitado') { $habilitado = [bool]$lf.habilitado }
        $relativa = ''
        if ($nf -contains 'destino') { $relativa = ('' + $lf.destino).Trim('\') }
        if (-not $relativa) {
            $prefijo = 'EQUIPO-01'
            if ($Configuracion.PSObject.Properties.Name -contains 'destinos' -and
                $Configuracion.destinos.PSObject.Properties.Name -contains 'discoFrio' -and
                $Configuracion.destinos.discoFrio.PSObject.Properties.Name -contains 'prefijoEquipo') {
                $prefijo = '' + $Configuracion.destinos.discoFrio.prefijoEquipo
            }
            $relativa = $prefijo + '\D\VMware_LAB'
        }
        # Una ruta relativa de verdad: con letra, UNC o ".." escribiria donde no toca.
        if ($relativa -match '^[A-Za-z]:' -or $relativa.StartsWith('\\') -or $relativa -match '(^|\\)\.\.(\\|$)') {
            throw "laboratorio.discoFrio.destino debe ser relativo a la raiz del disco: $relativa"
        }
        $frio = [pscustomobject]@{ Habilitado = $habilitado; Relativa = $relativa }
    }

    return [pscustomobject]@{
        Frio            = $frio
        Origen          = ('' + $l.origen).TrimEnd('\')
        Destino         = ('' + $l.destino).TrimEnd('\')
        DestinoEnLaPi   = ('' + $l.destinoEnLaPi).TrimEnd('/')
        Ssh             = '' + (& $opcional 'ssh' 'rpi')
        UsuarioEnLaPi   = '' + (& $opcional 'usuarioEnLaPi' 'nas')
        Umbral          = [long]$l.umbralDeltaBytes
        Bloque          = $bloque
        Vmrun           = '' + (& $opcional 'vmrun' '')
        # 'pi' en la vida real. 'local' coteja leyendo el destino desde este
        # equipo: lo usa la bateria, que no tiene una Pi a mano.
        Verificador     = '' + (& $opcional 'verificador' 'pi')
        ExcluirArchivos = @(& $opcional 'excluirArchivos' @('*.lck', '*.vmem', '*.vmss', '*.log', '*.scoreboard', '_SHA256SUMS.txt', 'ULTIMO_GUARDADO.txt'))
        ExcluirCarpetas = @(& $opcional 'excluirCarpetas' @('_POR_BORRAR', 'registros', 'caches', '*.lck'))
    }
}

function Test-ExcluidoDelLaboratorio {
    <#
        .SYNOPSIS
            Dice si un archivo del laboratorio se queda fuera del guardado.
        .DESCRIPTION
            Las mismas exclusiones que el guion v1, para que el motor y la
            herramienta de emergencia guarden exactamente lo mismo: los cerrojos
            y la memoria de una VM viva, los registros, y las carpetas de
            trabajo de las propias herramientas.
        .PARAMETER Relativa
            Ruta relativa a la raiz del laboratorio.
        .PARAMETER Ficha
            Lo que devuelve Get-FichaDelLaboratorio.
    #>
    [CmdletBinding()]
    [OutputType([bool])]
    param(
        [Parameter(Mandatory)][ValidateNotNullOrEmpty()][string] $Relativa,
        [Parameter(Mandatory)][psobject] $Ficha
    )

    $tramos = $Relativa.Split('\')
    $nombre = $tramos[-1]
    foreach ($p in $Ficha.ExcluirArchivos) { if ($nombre -like $p) { return $true } }
    for ($i = 0; $i -lt $tramos.Count - 1; $i++) {
        foreach ($p in $Ficha.ExcluirCarpetas) { if ($tramos[$i] -like $p) { return $true } }
    }
    return $false
}

function Get-InventarioDelLaboratorio {
    <#
        .SYNOPSIS
            Los archivos del laboratorio, con su VM y si viajan por bloques.
        .DESCRIPTION
            UNA VM ES UNA CARPETA DE PRIMER NIVEL; lo que cuelga de la raiz va en
            la VM ''. Un archivo es GRANDE si pasa del umbral: esos no los copia
            robocopy, viajan por bloques (ADR-0104).
        .PARAMETER Ficha
            Lo que devuelve Get-FichaDelLaboratorio.
        .PARAMETER Raiz
            Donde mirar. Por omision el origen; el motor la usa tambien sobre el
            destino.
    #>
    [CmdletBinding()]
    [OutputType([psobject[]])]
    param(
        [Parameter(Mandatory)][psobject] $Ficha,
        [ValidateNotNullOrEmpty()][string] $Raiz = $Ficha.Origen
    )

    $raizNorm = $Raiz.TrimEnd('\')
    $lista = New-Object System.Collections.Generic.List[psobject]
    foreach ($f in @(Get-ChildItem -LiteralPath $raizNorm -Recurse -File -Force -ErrorAction Stop)) {
        $rel = $f.FullName.Substring($raizNorm.Length + 1)
        if (Test-ExcluidoDelLaboratorio -Relativa $rel -Ficha $Ficha) { continue }
        $vm = ''
        if ($rel.Contains('\')) { $vm = $rel.Split('\')[0] }
        $lista.Add([pscustomobject]@{
                Relativa = $rel
                Vm       = $vm
                Ruta     = $f.FullName
                Tamano   = [long]$f.Length
                FechaUtc = $f.LastWriteTimeUtc
                Grande   = ($f.Length -gt $Ficha.Umbral)
            })
    }
    return $lista.ToArray()
}

function Get-VmAbiertaDelLaboratorio {
    <#
        .SYNOPSIS
            Las VMs con algo abierto: cerrojo .lck o memoria suspendida .vmss.
        .DESCRIPTION
            NO HACE FALTA VMWARE PARA SABERLO. Una VM encendida o suspendida
            deja en su carpeta un cerrojo (.lck) o su memoria (.vmss), y los dos
            se ven con un listado. `vmrun list` es la segunda opinion, y la pide
            el motor antes de escribir.
        .PARAMETER Ficha
            Lo que devuelve Get-FichaDelLaboratorio.
    #>
    [CmdletBinding()]
    [OutputType([string[]])]
    param(
        [Parameter(Mandatory)][psobject] $Ficha
    )

    # UN .lck SIN PROCESO vmware-vmx ES UN CANDADO HUERFANO (VMware o el equipo
    # cerrados a la fuerza): la VM esta apagada y VMware lo ignora. Solo cuenta
    # si hay alguna VM corriendo. El .vmss (suspendida) cuenta siempre.
    $hayVmx = Test-VmxEnMarcha
    $raiz = $Ficha.Origen.TrimEnd('\')
    $abiertas = New-Object System.Collections.Generic.List[string]
    foreach ($e in @(Get-ChildItem -LiteralPath $raiz -Recurse -Force -ErrorAction SilentlyContinue |
                Where-Object { $_.Name -like '*.lck' -or $_.Name -like '*.vmss' })) {
        if ($e.Name -like '*.lck' -and -not $hayVmx) { continue }
        $rel = $e.FullName.Substring($raiz.Length + 1)
        $vm = $rel.Split('\')[0]
        if ($vm -like '_POR_BORRAR*' -or $vm -eq '_lab') { continue }
        if (-not $abiertas.Contains($vm)) { $abiertas.Add($vm) }
    }
    return $abiertas.ToArray()
}

function Set-VmxSimulado {
    <#
        .SYNOPSIS
            Fija o limpia la simulacion de proceso vmware-vmx para pruebas unitarias.
    #>
    [CmdletBinding(SupportsShouldProcess)]
    [OutputType([void])]
    param(
        [AllowNull()]
        [System.Nullable[bool]] $Valor = $null
    )
    if (-not $PSCmdlet.ShouldProcess('NasRespaldo.VmxSimulado', 'Ajustar simulacion VMX')) { return }
    $script:VmxSimulado = $Valor
}

function Test-VmxEnMarcha {
    <#
        .SYNOPSIS
            Si hay alguna VM de VMware corriendo en este equipo.
        .DESCRIPTION
            Una VM encendida es un proceso vmware-vmx. Set-VmxSimulado (solo la
            bateria de pruebas) fuerza la respuesta de forma controlada.
    #>
    [CmdletBinding()]
    [OutputType([bool])]
    param()
    if ($null -ne $script:VmxSimulado) { return [bool]$script:VmxSimulado }
    $globalSim = Get-Variable -Name 'VmxSimulado' -Scope Global -ValueOnly -ErrorAction SilentlyContinue
    if ($null -ne $globalSim) { return [bool]$globalSim }
    return (@(Get-Process -Name 'vmware-vmx*' -ErrorAction SilentlyContinue).Count -gt 0)
}

function Write-EstadoDelLaboratorioPorVm {
    <#
        .SYNOPSIS
            Anota en LABORATORIO.tsv como quedo cada VM. Solo cambia lo que se da.
        .DESCRIPTION
            ES EL RAICES.tsv DEL LABORATORIO, por la misma razon: el tablero no
            puede releer el nodo para pintarse, asi que lo anota quien lo midio.
            Un guardado escribe guardado, enviado y estado; una lectura en la Pi
            escribe lectura. Cada uno conserva lo del otro.
        .PARAMETER Filas
            Objetos con Vm y los campos que cambian: Bytes, Guardado, Enviado,
            Estado, Detalle, Lectura.
        .PARAMETER Vigentes
            Si se da, se borran las VMs que ya no estan en D:.
        .PARAMETER Carpeta
            Donde vive el estado.
    #>
    [CmdletBinding(SupportsShouldProcess)]
    [OutputType([void])]
    param(
        [Parameter(Mandatory)][AllowEmptyCollection()][psobject[]] $Filas,
        [string[]] $Vigentes,
        [ValidateNotNullOrEmpty()][string] $Carpeta = (Get-CarpetaDeEstado)
    )

    $campos = @('Bytes', 'Guardado', 'Enviado', 'Estado', 'Detalle', 'Lectura')
    $mapa = Read-EstadoDelLaboratorioPorVm -Carpeta $Carpeta
    foreach ($f in $Filas) {
        if ($null -eq $f) { continue }
        $vm = '' + $f.Vm
        if (-not $mapa.ContainsKey($vm)) {
            $mapa[$vm] = [pscustomobject]@{ Vm = $vm; Bytes = [long]0; Guardado = ''; Enviado = [long]-1; Estado = ''; Detalle = ''; Lectura = '' }
        }
        foreach ($c in $campos) {
            if ($f.PSObject.Properties.Name -contains $c -and $null -ne $f.$c) { $mapa[$vm].$c = $f.$c }
        }
    }
    if ($PSBoundParameters.ContainsKey('Vigentes')) {
        foreach ($vm in @($mapa.Keys)) { if ($Vigentes -notcontains $vm) { $mapa.Remove($vm) } }
    }

    $ruta = Join-Path $Carpeta 'LABORATORIO.tsv'
    if (-not $PSCmdlet.ShouldProcess($ruta, "Anotar $($mapa.Count) VMs del laboratorio")) { return }

    $sb = New-Object System.Text.StringBuilder
    [void]$sb.AppendLine('# LABORATORIO.tsv - una linea por VM de D:. Lo pinta el tablero (ADR-0104).')
    [void]$sb.AppendLine("# vm`tbytes`tguardado`tenviado`testado`tdetalle`tlectura")
    foreach ($vm in ($mapa.Keys | Sort-Object)) {
        $r = $mapa[$vm]
        $detalle = ('' + $r.Detalle) -replace '[\t\r\n]+', ' '
        [void]$sb.AppendLine(($r.Vm, $r.Bytes, $r.Guardado, $r.Enviado, $r.Estado, $detalle, $r.Lectura) -join "`t")
    }
    Set-ContenidoAtomico -Ruta $ruta -Contenido $sb.ToString() -Confirm:$false
}

function Read-EstadoDelLaboratorioPorVm {
    <#
        .SYNOPSIS
            Lee LABORATORIO.tsv como un mapa VM -> fila. Nunca lanza.
        .PARAMETER Carpeta
            Donde vive el estado.
    #>
    [CmdletBinding()]
    [OutputType([hashtable])]
    param(
        [ValidateNotNullOrEmpty()][string] $Carpeta = (Get-CarpetaDeEstado)
    )

    $mapa = @{}
    $ruta = Join-Path $Carpeta 'LABORATORIO.tsv'
    try {
        if (-not (Test-Path -LiteralPath $ruta -PathType Leaf)) { return $mapa }
        foreach ($linea in (Get-Content -LiteralPath $ruta -Encoding UTF8 -ErrorAction Stop)) {
            if ($linea -match '^\s*#' -or [string]::IsNullOrWhiteSpace($linea)) { continue }
            $c = $linea -split "`t"
            if ($c.Count -lt 7) { continue }
            $mapa[$c[0]] = [pscustomobject]@{
                Vm = $c[0]; Bytes = [long]$c[1]; Guardado = $c[2]; Enviado = [long]$c[3]
                Estado = $c[4]; Detalle = $c[5]; Lectura = $c[6]
            }
        }
    }
    catch {
        return @{}
    }
    return $mapa
}

function Format-BytesDelLaboratorio {
    <#
        .SYNOPSIS
            Un tamano en la unidad que se lee de un vistazo.
        .PARAMETER Bytes
            Bytes; negativo o $null es "no se sabe".
    #>
    [CmdletBinding()]
    [OutputType([string])]
    param(
        [AllowNull()][System.Nullable[long]] $Bytes
    )
    if ($null -eq $Bytes -or $Bytes -lt 0) { return '--' }
    if ($Bytes -ge 1GB) { return '{0:N1} GB' -f ($Bytes / 1GB) }
    if ($Bytes -ge 1MB) { return '{0:N0} MiB' -f ($Bytes / 1MB) }
    return '{0:N0} KiB' -f ($Bytes / 1KB)
}

function Get-ResumenDeVmsDelLaboratorio {
    <#
        .SYNOPSIS
            Una entrada por VM: cuanto ocupa y cuando cambio por ultima vez.
        .DESCRIPTION
            SOLO LISTA D:, que es un NVMe local y son unos 500 archivos: se mide
            en milisegundos. El nodo no se toca.
        .PARAMETER Inventario
            Lo que devuelve Get-InventarioDelLaboratorio.
    #>
    [CmdletBinding()]
    [OutputType([psobject[]])]
    param(
        [Parameter(Mandatory)][AllowEmptyCollection()][psobject[]] $Inventario
    )

    $vms = New-Object System.Collections.Generic.List[psobject]
    foreach ($g in @($Inventario | Group-Object -Property Vm)) {
        $ultimo = $g.Group | Sort-Object -Property FechaUtc -Descending | Select-Object -First 1
        $vms.Add([pscustomobject]@{
                Vm     = '' + $g.Name
                Bytes  = [long](($g.Group | Measure-Object -Property Tamano -Sum).Sum)
                Cambio = $ultimo.FechaUtc.ToLocalTime()
            })
    }
    return $vms.ToArray()
}

function Get-FilasDelLaboratorio {
    <#
        .SYNOPSIS
            La tabla por VM del tablero, ya decidida. Funcion pura.
        .DESCRIPTION
            "DESDE ENTONCES" ES LA PREGUNTA QUE IMPORTA: si lo que hay en D: es
            lo que hay en el nodo. Se contesta con la fecha del ultimo cambio en
            D: contra la del ultimo guardado de esa VM -o, si el motor aun no la
            guardo nunca, la del ultimo guardado entero del laboratorio-. No
            lee el nodo para eso.

            De peor a mejor:
              el ultimo guardado de la VM fallo      FALLO            rojo
              pendiente de leer en la Pi ([T])       pendiente [T]    ambar
              no esta en el nodo                     nueva            ambar
              nunca se guardo                        sin guardar      ambar
              cambio despues del guardado            cambiada dd/mm   ambar
              igual que en el guardado               al dia           verde
            Una VM abierta lo dice detras: asi no se puede guardar.
        .PARAMETER Vms
            Lo que devuelve Get-ResumenDeVmsDelLaboratorio.
        .PARAMETER PorVm
            Lo que devuelve Read-EstadoDelLaboratorioPorVm.
        .PARAMETER EnNodo
            Mapa VM -> bytes en el nodo, o $null si el nodo no se leyo.
        .PARAMETER Base
            El momento del ultimo guardado entero (laboratorio_copia).
        .PARAMETER Abiertas
            Lo que devuelve Get-VmAbiertaDelLaboratorio.
    #>
    [CmdletBinding()]
    [OutputType([psobject[]])]
    param(
        [Parameter(Mandatory)][AllowEmptyCollection()][psobject[]] $Vms,
        [Parameter(Mandatory)][hashtable] $PorVm,
        [AllowNull()][hashtable] $EnNodo,
        [AllowEmptyString()][AllowNull()][string] $Base = '',
        [AllowEmptyCollection()][string[]] $Abiertas = @()
    )

    $filas = New-Object System.Collections.Generic.List[psobject]
    foreach ($v in @($Vms | Sort-Object -Property Bytes -Descending)) {
        $vm = '' + $v.Vm
        $fila = $null
        if ($PorVm.ContainsKey($vm)) { $fila = $PorVm[$vm] }

        $guardado = '' + $Base
        if ($fila -and $fila.Guardado) { $guardado = '' + $fila.Guardado }
        [datetime] $tGuardado = [datetime]::MinValue
        $hayGuardado = (-not [string]::IsNullOrWhiteSpace($guardado)) -and [datetime]::TryParse($guardado, [ref] $tGuardado)

        $textoNodo = '?'
        $estaEnNodo = $true
        if ($null -ne $EnNodo) {
            if ($EnNodo.ContainsKey($vm)) { $textoNodo = Format-BytesDelLaboratorio -Bytes ([long]$EnNodo[$vm]) }
            else { $textoNodo = '--'; $estaEnNodo = $false }
        }

        if ($fila -and $fila.Estado -eq 'Falla') { $desde = 'FALLO'; $tono = 'Rojo' }
        elseif ($fila -and $fila.Estado -eq 'Pendiente') { $desde = 'pendiente [T]'; $tono = 'Ambar' }
        elseif (-not $estaEnNodo) { $desde = 'nueva'; $tono = 'Ambar' }
        elseif (-not $hayGuardado) { $desde = 'sin guardar'; $tono = 'Ambar' }
        elseif ($v.Cambio -gt $tGuardado.AddSeconds(2)) { $desde = 'cambiada {0:dd/MM}' -f $v.Cambio; $tono = 'Ambar' }
        else { $desde = 'al dia'; $tono = 'Verde' }
        if ($Abiertas -contains $vm) { $desde += ', abierta' }

        $enviado = '--'
        if ($fila -and $fila.Enviado -ge 0) { $enviado = Format-BytesDelLaboratorio -Bytes ([long]$fila.Enviado) }
        $lectura = '--'
        [datetime] $tLectura = [datetime]::MinValue
        if ($fila -and $fila.Lectura -and [datetime]::TryParse(('' + $fila.Lectura), [ref] $tLectura)) {
            $lectura = '{0:dd/MM HH:mm}' -f $tLectura
        }
        $guardada = '--'
        if ($hayGuardado) { $guardada = '{0:dd/MM HH:mm}' -f $tGuardado }
        $detalle = ''
        if ($fila) { $detalle = '' + $fila.Detalle }

        $filas.Add([pscustomobject]@{
                Vm       = $vm
                EnD      = Format-BytesDelLaboratorio -Bytes ([long]$v.Bytes)
                EnNodo   = $textoNodo
                Guardada = $guardada
                Desde    = $desde
                Tono     = $tono
                Enviado  = $enviado
                Lectura  = $lectura
                Detalle  = $detalle
            })
    }
    return $filas.ToArray()
}

function Get-ResumenDelLaboratorio {
    <#
        .SYNOPSIS
            Cuantas VMs no estan al dia en el nodo, para la linea PENDIENTE.
        .DESCRIPTION
            Nunca lanza: sin bloque laboratorio, sin D: o sin poder listar,
            devuelve $null y el tablero se pinta igual.
        .PARAMETER Configuracion
            El objeto de configuracion completo.
        .PARAMETER Estado
            Lo que devuelve Read-EstadoRespaldo.
    #>
    [CmdletBinding()]
    [OutputType([psobject])]
    param(
        [Parameter(Mandatory)][psobject] $Configuracion,
        [Parameter(Mandatory)][hashtable] $Estado
    )

    try {
        $ficha = Get-FichaDelLaboratorio -Configuracion $Configuracion
        if (-not $ficha -or -not (Test-Path -LiteralPath $ficha.Origen -PathType Container)) { return $null }
        $vms = @(Get-ResumenDeVmsDelLaboratorio -Inventario @(Get-InventarioDelLaboratorio -Ficha $ficha))
        $porVm = Read-EstadoDelLaboratorioPorVm -Carpeta (Get-CarpetaDeEstado -Configuracion $Configuracion)
        $filas = @(Get-FilasDelLaboratorio -Vms $vms -PorVm $porVm -EnNodo $null -Base ('' + $Estado['laboratorio_copia']))
        return [pscustomobject]@{
            Vms       = $filas.Count
            NoAlDia   = @($filas | Where-Object { $_.Desde -notlike 'al dia*' }).Count
        }
    }
    catch {
        Write-Verbose "Sin resumen del laboratorio: $($_.Exception.Message)"
        return $null
    }
}

function Read-LecturaDelLaboratorio {
    <#
        .SYNOPSIS
            Lee laboratorio\lectura.json si hay una lectura [T] en curso. Nunca lanza.
        .DESCRIPTION
            Solo lectura local en disco (< 1 ms, sin SSH) para que el tablero
            sepa de inmediato si la Pi tiene una lectura lanzada sin recoger y
            no pinte un estado enganoso en LAB VMWARE.
        .PARAMETER Carpeta
            Carpeta de estado del cliente.
    #>
    [CmdletBinding()]
    [OutputType([psobject])]
    param(
        [ValidateNotNullOrEmpty()][string] $Carpeta = (Get-CarpetaDeEstado)
    )

    $ruta = Join-Path $Carpeta 'laboratorio\lectura.json'
    try {
        if (-not (Test-Path -LiteralPath $ruta -PathType Leaf)) { return $null }
        $t = Get-Content -LiteralPath $ruta -Raw -Encoding UTF8 -ErrorAction Stop | ConvertFrom-Json
        if (-not $t) { return $null }
        $inicio = ''
        $minutos = 0
        if ($t.PSObject.Properties.Name -contains 'Inicio' -and $null -ne $t.Inicio) {
            if ($t.Inicio -is [datetime]) {
                $dt = [datetime]$t.Inicio
                $inicio = $dt.ToString('s')
                $minutos = [Math]::Max(0, [int][Math]::Floor(((Get-Date) - $dt).TotalMinutes))
            }
            else {
                $inicio = '' + $t.Inicio
                [datetime] $dt = [datetime]::MinValue
                if ($inicio -and (
                        [datetime]::TryParse($inicio, [System.Globalization.CultureInfo]::InvariantCulture, [System.Globalization.DateTimeStyles]::AssumeLocal, [ref]$dt) -or
                        [datetime]::TryParse($inicio, [ref]$dt))) {
                    $minutos = [Math]::Max(0, [int][Math]::Floor(((Get-Date) - $dt).TotalMinutes))
                }
            }
        }
        $archivos = @()
        if ($t.PSObject.Properties.Name -contains 'Archivos' -and $null -ne $t.Archivos) {
            $archivos = @($t.Archivos)
        }
        $alcance = 'completo'
        if ($t.PSObject.Properties.Name -contains 'Alcance' -and -not [string]::IsNullOrWhiteSpace('' + $t.Alcance)) {
            $alcance = '' + $t.Alcance
        }
        $bloque = 0
        if ($t.PSObject.Properties.Name -contains 'Bloque' -and $null -ne $t.Bloque) {
            $bloque = [int]$t.Bloque
        }
        return [pscustomobject]@{
            Ruta          = $ruta
            Inicio        = $inicio
            Minutos       = $minutos
            TotalArchivos = $archivos.Count
            Alcance       = $alcance
            Bloque        = $bloque
            Archivos      = $archivos
        }
    }
    catch {
        return $null
    }
}
