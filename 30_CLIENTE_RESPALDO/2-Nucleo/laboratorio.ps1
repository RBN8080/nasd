#Requires -Version 5.1
<#
    .SYNOPSIS
        El laboratorio de D: al nodo, por bloques. Solo viaja lo que cambio.

    .DESCRIPTION
        Contrato: 30_CLIENTE_RESPALDO.md seccion 5.2. Decision: ADR-0104.

        LA RESTRICCION QUE MANDA: UNA VM QUE YA ESTA EN EL NODO NO SE VUELVE A
        SUBIR ENTERA. El guion v1 (D:\VMware_LAB\_lab\GUARDAR_EN_NAS.ps1) copia
        con robocopy, y robocopy recopia el .vmdk entero en cuanto cambia un
        byte: el guardado del 2026-10-10 fueron 7.1 h con las VMs apagadas. Aqui
        cada disco virtual grande se parte en bloques de 1 MiB, y solo viajan
        los bloques cuya huella cambio.

        EL MANIFIESTO ES EL ESTADO DEL NODO. Por cada archivo grande se guarda en
        este equipo la lista de huellas de sus bloques TAL COMO ESTAN EN EL NODO.
        Para saber que cambio no se relee el nodo -la Pi lee a 23 MB/s-: se
        compara D:, que es un NVMe local, contra el manifiesto. Despues la Pi
        relee SOLO los bloques escritos y confirma que son los de D:. Si los no
        tocados ya estaban bien, el archivo entero lo esta.

        ESO EXIGE QUE EL MANIFIESTO NO MIENTA, y hay tres guardas:
          - Un manifiesto se crea leyendo el NODO en la Pi (-Accion Leer), no D:.
            D: cambia despues de cada guardado; el nodo no.
          - Antes de usarlo se mira que el archivo del nodo tenga el tamano y la
            fecha que el manifiesto dice. Si alguien lo toco por fuera -el v1 de
            emergencia, por ejemplo-, la VM queda pendiente de leer y NO se
            escribe en ella.
          - Si la Pi no confirma un bloque, el manifiesto no se actualiza.
        La lectura completa en la Pi (-Accion Leer) es tambien el cotejo contra
        un disco que se degrada en lo que nadie toca. Se pasa a mano.

        LAS VMS VAN APAGADAS TODO EL GUARDADO, sin foto VSS ni administrador
        (ADR-0104). Con el delta son minutos: leer en local lo que cambio y
        enviar esos bloques.

    .PARAMETER RutaConfiguracion
        Configuracion a usar. Por omision la de 3-Config.

    .PARAMETER Accion
        Ver      la tabla por VM, sin escribir nada.
        Previa   cuanto enviaria cada VM, sin escribir nada.
        Guardar  envia lo que cambio.
        Leer     la lectura completa en la Pi: la lanza, dice como va o la recoge.

    .EXAMPLE
        .\laboratorio.ps1 -Accion Previa
        .\laboratorio.ps1 -Accion Guardar
#>
[CmdletBinding(SupportsShouldProcess, ConfirmImpact = 'High')]
[OutputType([psobject])]
param(
    [ValidateNotNullOrEmpty()]
    [string] $RutaConfiguracion,

    [ValidateSet('Ver', 'Previa', 'Guardar', 'Leer')]
    [string] $Accion = 'Ver'
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

. "$PSScriptRoot\comun.ps1"

# Donde la Pi guarda blkhash.py y una lectura completa en curso. /tmp porque es
# trabajo de paso: si la Pi se reinicia a media lectura, se vuelve a lanzar.
$script:BlkhashEnLaPi = '/tmp/nas-blkhash.py'
$script:LecturaEnLaPi = '/tmp/nas-lab-lectura'
# Margen libre que se deja en el nodo, como el v1.
$script:MargenEnNodo = 20GB
# Tolerancia de fechas: la misma que /FFT.
$script:Tolerancia = [TimeSpan]::FromSeconds(2).Ticks

# ---------------------------------------------------------------------------
#  Huellas, manifiestos y bloques
# ---------------------------------------------------------------------------

function Get-HuellasPorBloque {
    <#
        .SYNOPSIS
            SHA256 de cada bloque de un archivo, en minusculas. Lo mismo que
            blkhash.py full.
        .DESCRIPTION
            SHA256Cng y no SHA256.Create(): en .NET Framework el segundo da la
            implementacion administrada. Medido el 2026-10-18 en este equipo:
            ~1.5 GB/s con la cache caliente y ~630 MB/s leyendo el NVMe.
        .PARAMETER Ruta
            El archivo.
        .PARAMETER Bloque
            Tamano del bloque en bytes.
    #>
    [CmdletBinding()]
    [OutputType([psobject])]
    param(
        [Parameter(Mandatory)][ValidateNotNullOrEmpty()][string] $Ruta,
        [Parameter(Mandatory)][ValidateRange(4096, 67108864)][int] $Bloque
    )

    $sha = New-Object System.Security.Cryptography.SHA256Cng
    $fs = New-Object System.IO.FileStream($Ruta, [System.IO.FileMode]::Open, [System.IO.FileAccess]::Read,
        [System.IO.FileShare]::ReadWrite, 1048576, [System.IO.FileOptions]::SequentialScan)
    $huellas = New-Object System.Collections.Generic.List[string]
    try {
        $tamano = $fs.Length
        $buf = New-Object byte[] $Bloque
        while ($true) {
            $leido = 0
            while ($leido -lt $Bloque) {
                $r = $fs.Read($buf, $leido, $Bloque - $leido)
                if ($r -le 0) { break }
                $leido += $r
            }
            if ($leido -eq 0) { break }
            $huellas.Add(([System.BitConverter]::ToString($sha.ComputeHash($buf, 0, $leido))).Replace('-', '').ToLowerInvariant())
            if ($leido -lt $Bloque) { break }
        }
    }
    finally {
        $fs.Dispose()
        $sha.Dispose()
    }
    return [pscustomobject]@{ Tamano = [long]$tamano; Bloque = $Bloque; Huellas = $huellas.ToArray() }
}

function Get-RutaDelManifiesto {
    <#
        .SYNOPSIS
            Donde vive el manifiesto de un archivo: en la carpeta de estado, no en D:.
        .PARAMETER Carpeta
            La carpeta de estado.
        .PARAMETER Relativa
            Ruta del archivo relativa a la raiz del laboratorio.
    #>
    [CmdletBinding()]
    [OutputType([string])]
    param(
        [Parameter(Mandatory)][ValidateNotNullOrEmpty()][string] $Carpeta,
        [Parameter(Mandatory)][ValidateNotNullOrEmpty()][string] $Relativa
    )
    return (Join-Path (Join-Path $Carpeta 'laboratorio\bloques') ($Relativa + '.blk'))
}

function Read-ManifiestoDeBloques {
    <#
        .SYNOPSIS
            Lee un manifiesto. $null si no hay.
        .DESCRIPTION
            Cabecera "BS <bloque> SIZE <tamano> MTIME <ticks UTC>". MTIME es la
            fecha que el archivo tiene EN EL NODO; sin ella (formato del v2) el
            manifiesto no se puede contrastar con el nodo y se trata como 0.
        .PARAMETER Ruta
            El archivo .blk.
    #>
    [CmdletBinding()]
    [OutputType([psobject])]
    param(
        [Parameter(Mandatory)][ValidateNotNullOrEmpty()][string] $Ruta
    )

    if (-not (Test-Path -LiteralPath $Ruta -PathType Leaf)) { return $null }
    $lineas = [System.IO.File]::ReadAllLines($Ruta)
    $cab = $lineas[0] -split '\s+'
    $campos = @{}
    for ($i = 0; $i + 1 -lt $cab.Count; $i += 2) { $campos[$cab[$i]] = $cab[$i + 1] }
    $fecha = [long]0
    if ($campos.ContainsKey('MTIME')) { $fecha = [long]$campos['MTIME'] }
    return [pscustomobject]@{
        Bloque        = [int]$campos['BS']
        Tamano        = [long]$campos['SIZE']
        FechaUtcTicks = $fecha
        Huellas       = @($lineas | Select-Object -Skip 1 | Where-Object { $_ })
    }
}

function Write-ManifiestoDeBloques {
    <#
        .SYNOPSIS
            Escribe un manifiesto de forma atomica.
        .PARAMETER Ruta
            El archivo .blk.
        .PARAMETER Huellas
            Lo que devuelve Get-HuellasPorBloque.
        .PARAMETER FechaUtc
            La fecha del archivo en el nodo.
    #>
    [CmdletBinding(SupportsShouldProcess)]
    [OutputType([void])]
    param(
        [Parameter(Mandatory)][ValidateNotNullOrEmpty()][string] $Ruta,
        [Parameter(Mandatory)][psobject] $Huellas,
        [Parameter(Mandatory)][datetime] $FechaUtc
    )

    if (-not $PSCmdlet.ShouldProcess($Ruta, 'Escribir manifiesto')) { return }
    $dir = Split-Path -Path $Ruta -Parent
    if (-not (Test-Path -LiteralPath $dir)) { New-Item -ItemType Directory -Path $dir -Force | Out-Null }
    $sb = New-Object System.Text.StringBuilder
    [void]$sb.Append(('BS {0} SIZE {1} MTIME {2}' -f $Huellas.Bloque, $Huellas.Tamano, $FechaUtc.ToUniversalTime().Ticks)).Append("`n")
    foreach ($h in $Huellas.Huellas) { [void]$sb.Append($h).Append("`n") }
    Set-ContenidoAtomico -Ruta $Ruta -Contenido $sb.ToString() -Confirm:$false
}

function Get-IndicesCambiados {
    <#
        .SYNOPSIS
            Los bloques del origen que no son los del manifiesto.
        .DESCRIPTION
            Si el archivo crecio, los bloques nuevos cuentan. Si encogio, la
            cola sobrante no genera indices: la resuelve el ajuste de tamano.
        .PARAMETER Actual
            Lo que devuelve Get-HuellasPorBloque sobre el origen.
        .PARAMETER Previo
            El manifiesto.
    #>
    [CmdletBinding()]
    [OutputType([int[]])]
    param(
        [Parameter(Mandatory)][psobject] $Actual,
        [Parameter(Mandatory)][psobject] $Previo
    )

    $a = @($Actual.Huellas)
    $p = @($Previo.Huellas)
    $cambios = New-Object System.Collections.Generic.List[int]
    for ($i = 0; $i -lt $a.Count; $i++) {
        if ($i -ge $p.Count -or $a[$i] -ne $p[$i]) { $cambios.Add($i) }
    }
    return , $cambios.ToArray()
}

function Get-SumaDe {
    <#
        .SYNOPSIS
            Suma una propiedad de una lista, que puede estar vacia.
        .DESCRIPTION
            NO ES Measure-Object, Y POR UNA RAZON MEDIDA. En Windows PowerShell
            5.1, Measure-Object -Sum sobre una entrada vacia no devuelve nada, y
            leer .Sum de nada lanza con StrictMode. Una VM sin discos grandes
            -_docs, por ejemplo- tumbaba el guardado entero (2026-10-18).
        .PARAMETER Objetos
            La lista.
        .PARAMETER Propiedad
            Que sumar.
    #>
    [CmdletBinding()]
    [OutputType([long])]
    param(
        [Parameter(Mandatory)][AllowEmptyCollection()][AllowNull()][psobject[]] $Objetos,
        [Parameter(Mandatory)][ValidateNotNullOrEmpty()][string] $Propiedad
    )
    $total = [long]0
    foreach ($o in @($Objetos)) {
        if ($null -ne $o -and $null -ne $o.$Propiedad) { $total += [long]$o.$Propiedad }
    }
    return $total
}

function Get-BytesDeIndices {
    <#
        .SYNOPSIS
            Cuantos bytes ocupan esos bloques en un archivo de ese tamano.
        .PARAMETER Indices
            Los bloques.
        .PARAMETER Bloque
            Tamano del bloque.
        .PARAMETER Tamano
            Tamano del archivo: el ultimo bloque puede ser corto.
    #>
    [CmdletBinding()]
    [OutputType([long])]
    param(
        [Parameter(Mandatory)][AllowEmptyCollection()][int[]] $Indices,
        [Parameter(Mandatory)][int] $Bloque,
        [Parameter(Mandatory)][long] $Tamano
    )
    $total = [long]0
    foreach ($i in $Indices) {
        $resto = $Tamano - ([long]$i * $Bloque)
        if ($resto -gt 0) { $total += [Math]::Min([long]$Bloque, $resto) }
    }
    return $total
}

function Write-BloquesEnSuSitio {
    <#
        .SYNOPSIS
            Copia esos bloques del origen al destino, en su sitio. Ajusta tamano y fecha.
        .DESCRIPTION
            ABRE SIN TRUNCAR. Es lo que robocopy no sabe hacer: abrir el destino
            y escribir solo los bloques que cambiaron. Si el destino no existe se
            crea, y entonces los indices son todos.
        .PARAMETER Origen
            El archivo en D:.
        .PARAMETER Destino
            El archivo en el nodo.
        .PARAMETER Bloque
            Tamano del bloque.
        .PARAMETER Indices
            Los bloques a escribir.
        .PARAMETER Tamano
            Tamano final del destino.
        .PARAMETER FechaUtc
            Fecha final del destino: la del origen.
    #>
    [CmdletBinding(SupportsShouldProcess)]
    [OutputType([long])]
    param(
        [Parameter(Mandatory)][ValidateNotNullOrEmpty()][string] $Origen,
        [Parameter(Mandatory)][ValidateNotNullOrEmpty()][string] $Destino,
        [Parameter(Mandatory)][int] $Bloque,
        [Parameter(Mandatory)][AllowEmptyCollection()][int[]] $Indices,
        [Parameter(Mandatory)][long] $Tamano,
        [Parameter(Mandatory)][datetime] $FechaUtc
    )

    if (-not $PSCmdlet.ShouldProcess($Destino, "Escribir $(@($Indices).Count) bloques")) { return [long]0 }
    $dir = Split-Path -Path $Destino -Parent
    if (-not (Test-Path -LiteralPath $dir)) { New-Item -ItemType Directory -Path $dir -Force | Out-Null }

    $escritos = [long]0
    $o = New-Object System.IO.FileStream($Origen, [System.IO.FileMode]::Open, [System.IO.FileAccess]::Read, [System.IO.FileShare]::ReadWrite)
    try {
        $d = New-Object System.IO.FileStream($Destino, [System.IO.FileMode]::OpenOrCreate, [System.IO.FileAccess]::ReadWrite, [System.IO.FileShare]::None)
        try {
            if ($d.Length -ne $Tamano) { $d.SetLength($Tamano) }
            $buf = New-Object byte[] $Bloque
            $n = 0
            foreach ($i in $Indices) {
                $off = [long]$i * $Bloque
                $largo = [int][Math]::Min([long]$Bloque, $Tamano - $off)
                if ($largo -le 0) { continue }
                $o.Position = $off
                $leido = 0
                while ($leido -lt $largo) {
                    $r = $o.Read($buf, $leido, $largo - $leido)
                    if ($r -le 0) { throw "El origen se acabo antes de tiempo en el bloque $i de $Origen" }
                    $leido += $r
                }
                $d.Position = $off
                $d.Write($buf, 0, $largo)
                $escritos += $largo
                $n++
                if ($n % 64 -eq 0) {
                    Write-Progress -Activity "Enviando $(Split-Path $Destino -Leaf)" -Status ('{0:N0} de {1:N0} MiB' -f ($escritos / 1MB), ((@($Indices).Count * [long]$Bloque) / 1MB)) `
                        -PercentComplete ([Math]::Min(100, [int](100 * $n / @($Indices).Count)))
                }
            }
            $d.Flush()
        }
        finally { $d.Dispose() }
    }
    finally { $o.Dispose() }
    Write-Progress -Activity "Enviando $(Split-Path $Destino -Leaf)" -Completed
    [System.IO.File]::SetLastWriteTimeUtc($Destino, $FechaUtc)
    return $escritos
}

function Compare-HuellasLeidas {
    <#
        .SYNOPSIS
            Lo que el destino dice tener contra lo que se le envio. Funcion pura.
        .PARAMETER Esperadas
            Todas las huellas del origen.
        .PARAMETER Indices
            Los bloques que se escribieron.
        .PARAMETER Leidas
            Mapa indice -> huella, leido del destino.
        .PARAMETER Tamano
            Tamano del origen.
        .PARAMETER TamanoLeido
            Tamano del destino.
    #>
    [CmdletBinding()]
    [OutputType([psobject])]
    param(
        [Parameter(Mandatory)][AllowEmptyCollection()][string[]] $Esperadas,
        [Parameter(Mandatory)][AllowEmptyCollection()][int[]] $Indices,
        [Parameter(Mandatory)][hashtable] $Leidas,
        [Parameter(Mandatory)][long] $Tamano,
        [Parameter(Mandatory)][long] $TamanoLeido
    )
    $malos = 0
    foreach ($i in $Indices) {
        if (-not $Leidas.ContainsKey($i) -or $Leidas[$i] -ne $Esperadas[$i]) { $malos++ }
    }
    return [pscustomobject]@{
        Correcto = ($malos -eq 0 -and $Tamano -eq $TamanoLeido)
        Malos    = $malos
    }
}

# ---------------------------------------------------------------------------
#  La Pi
# ---------------------------------------------------------------------------

function ConvertTo-ArgumentoSh {
    <#
        .SYNOPSIS
            Una cadena entre comillas simples para sh, escapando las suyas.
        .PARAMETER Texto
            Lo que se quiere pasar tal cual.
    #>
    [CmdletBinding()]
    [OutputType([string])]
    param([Parameter(Mandatory)][AllowEmptyString()][string] $Texto)
    return "'" + $Texto.Replace("'", "'\''") + "'"
}

function Invoke-EnLaPi {
    <#
        .SYNOPSIS
            Una orden en la Pi por ssh. Devuelve sus lineas; lanza si falla.
        .PARAMETER Ficha
            Lo que devuelve Get-FichaDelLaboratorio.
        .PARAMETER Orden
            La orden, para sh.
        .PARAMETER Entrada
            Lineas para su entrada estandar. NUNCA vacia: sin entrada, ssh se
            quedaria leyendo la consola.
    #>
    [CmdletBinding()]
    [OutputType([string[]])]
    param(
        [Parameter(Mandatory)][psobject] $Ficha,
        [Parameter(Mandatory)][ValidateNotNullOrEmpty()][string] $Orden,
        [string[]] $Entrada
    )

    $argumentos = @('-o', 'BatchMode=yes', '-o', 'ConnectTimeout=15', $Ficha.Ssh, $Orden)
    $OutputEncoding = New-Object System.Text.UTF8Encoding $false
    if ($PSBoundParameters.ContainsKey('Entrada')) {
        if (@($Entrada).Count -eq 0) { throw 'Invoke-EnLaPi: entrada vacia.' }
        $salida = $Entrada | & ssh.exe @argumentos
    }
    else {
        $salida = & ssh.exe @argumentos
    }
    if ($LASTEXITCODE -ne 0) { throw "La Pi devolvio $LASTEXITCODE en: $Orden" }
    return @($salida)
}

function Publish-BlkhashEnLaPi {
    <#
        .SYNOPSIS
            Sube blkhash.py a la Pi y comprueba que llego entero.
        .DESCRIPTION
            Se sube en cada corrida y se compara su huella: la Pi no tiene que
            guardar nada nuestro, y lo que corre alli es exactamente lo que hay
            en este repositorio.
        .PARAMETER Ficha
            Lo que devuelve Get-FichaDelLaboratorio.
    #>
    [CmdletBinding()]
    [OutputType([string])]
    param([Parameter(Mandatory)][psobject] $Ficha)

    $local = Join-Path $PSScriptRoot 'blkhash.py'
    & scp.exe -q -o BatchMode=yes $local ('{0}:{1}' -f $Ficha.Ssh, $script:BlkhashEnLaPi)
    if ($LASTEXITCODE -ne 0) { throw 'No se pudo subir blkhash.py a la Pi.' }
    $esperada = (Get-FileHash -LiteralPath $local -Algorithm SHA256).Hash.ToLowerInvariant()
    $vista = @(Invoke-EnLaPi -Ficha $Ficha -Orden ('sha256sum ' + $script:BlkhashEnLaPi))
    if ($vista.Count -eq 0 -or -not $vista[0].StartsWith($esperada)) { throw 'blkhash.py llego distinto a la Pi.' }
    return $script:BlkhashEnLaPi
}

function Get-RutaEnLaPi {
    <#
        .SYNOPSIS
            La ruta de un archivo del laboratorio en el disco del nodo.
        .PARAMETER Ficha
            Lo que devuelve Get-FichaDelLaboratorio.
        .PARAMETER Relativa
            Ruta relativa a la raiz del laboratorio.
    #>
    [CmdletBinding()]
    [OutputType([string])]
    param(
        [Parameter(Mandatory)][psobject] $Ficha,
        [Parameter(Mandatory)][ValidateNotNullOrEmpty()][string] $Relativa
    )
    return ($Ficha.DestinoEnLaPi + '/' + $Relativa.Replace('\', '/'))
}

function Get-HuellasEnDestino {
    <#
        .SYNOPSIS
            Lo que el nodo tiene en esos bloques, leido EN LA PI.
        .DESCRIPTION
            La Pi lee de su disco con la cache tirada: es la unica forma de que
            "esta en el nodo" signifique "esta en el disco del nodo". Con el
            verificador 'local' (la bateria) se lee el destino desde este equipo.
        .PARAMETER Ficha
            Lo que devuelve Get-FichaDelLaboratorio.
        .PARAMETER Relativa
            Ruta relativa a la raiz del laboratorio.
        .PARAMETER Indices
            Los bloques a leer.
    #>
    [CmdletBinding()]
    [OutputType([psobject])]
    param(
        [Parameter(Mandatory)][psobject] $Ficha,
        [Parameter(Mandatory)][ValidateNotNullOrEmpty()][string] $Relativa,
        [Parameter(Mandatory)][AllowEmptyCollection()][int[]] $Indices
    )

    $mapa = @{}
    $enNodo = Join-Path $Ficha.Destino $Relativa
    if (@($Indices).Count -eq 0) {
        return [pscustomobject]@{ Tamano = [long](Get-Item -LiteralPath $enNodo -Force).Length; Mapa = $mapa }
    }

    if ($Ficha.Verificador -eq 'local') {
        $todo = Get-HuellasPorBloque -Ruta $enNodo -Bloque $Ficha.Bloque
        foreach ($i in $Indices) {
            if ($i -lt $todo.Huellas.Count) { $mapa[$i] = $todo.Huellas[$i] } else { $mapa[$i] = '-' }
        }
        return [pscustomobject]@{ Tamano = $todo.Tamano; Mapa = $mapa }
    }

    $orden = 'sudo -n -u {0} python3 {1} ranges {2} {3}' -f $Ficha.UsuarioEnLaPi, $script:BlkhashEnLaPi,
        (ConvertTo-ArgumentoSh -Texto (Get-RutaEnLaPi -Ficha $Ficha -Relativa $Relativa)), $Ficha.Bloque
    $salida = @(Invoke-EnLaPi -Ficha $Ficha -Orden $orden -Entrada @($Indices | ForEach-Object { '' + $_ }))
    if ($salida.Count -lt 1 -or $salida[0] -notmatch '^SIZE (\d+)$') { throw "La Pi no devolvio el tamano de $Relativa." }
    $tamano = [long]$Matches[1]
    for ($k = 0; $k -lt @($Indices).Count; $k++) {
        $h = '-'
        if ($k + 1 -lt $salida.Count) { $h = $salida[$k + 1].Trim() }
        $mapa[$Indices[$k]] = $h
    }
    return [pscustomobject]@{ Tamano = $tamano; Mapa = $mapa }
}

# ---------------------------------------------------------------------------
#  Guardar
# ---------------------------------------------------------------------------

function Test-LaboratorioQuieto {
    <#
        .SYNOPSIS
            Dice si se puede leer D: con las VMs paradas. Si no, por que.
        .PARAMETER Ficha
            Lo que devuelve Get-FichaDelLaboratorio.
    #>
    [CmdletBinding()]
    [OutputType([psobject])]
    param([Parameter(Mandatory)][psobject] $Ficha)

    $abiertas = @(Get-VmAbiertaDelLaboratorio -Ficha $Ficha)
    if ($abiertas.Count -gt 0) {
        return [pscustomobject]@{ Quieto = $false; Motivo = ('VMs encendidas o suspendidas: {0}. Apagalas (no suspender) y vuelve a pulsar.' -f ($abiertas -join ', ')) }
    }
    if ($Ficha.Vmrun -and (Test-Path -LiteralPath $Ficha.Vmrun -PathType Leaf)) {
        $lista = @(& $Ficha.Vmrun list)
        if (-not ($lista -match '^Total running VMs: 0$')) {
            return [pscustomobject]@{ Quieto = $false; Motivo = 'vmrun dice que hay VMs encendidas. Apagalas y vuelve a pulsar.' }
        }
    }
    return [pscustomobject]@{ Quieto = $true; Motivo = '' }
}

function Invoke-ArchivoGrande {
    <#
        .SYNOPSIS
            Un disco virtual grande al nodo: solo sus bloques cambiados.
        .PARAMETER Ficha
            Lo que devuelve Get-FichaDelLaboratorio.
        .PARAMETER Archivo
            Una entrada de Get-InventarioDelLaboratorio.
        .PARAMETER Carpeta
            La carpeta de estado.
        .PARAMETER Simular
            Mide y no escribe.
    #>
    [CmdletBinding(SupportsShouldProcess)]
    [OutputType([psobject])]
    param(
        [Parameter(Mandatory)][psobject] $Ficha,
        [Parameter(Mandatory)][psobject] $Archivo,
        [Parameter(Mandatory)][string] $Carpeta,
        [switch] $Simular
    )

    $rel = $Archivo.Relativa
    $enNodo = Join-Path $Ficha.Destino $rel
    $rutaManifiesto = Get-RutaDelManifiesto -Carpeta $Carpeta -Relativa $rel
    $manifiesto = Read-ManifiestoDeBloques -Ruta $rutaManifiesto
    $salida = {
        param($Estado, $Enviado, $Motivo)
        [pscustomobject]@{ Relativa = $rel; Vm = $Archivo.Vm; Estado = $Estado; Enviado = [long]$Enviado; Motivo = $Motivo }
    }

    if ($manifiesto) {
        # EL MANIFIESTO TIENE QUE SER EL NODO, o no se escribe nada.
        $n = Get-Item -LiteralPath $enNodo -Force -ErrorAction SilentlyContinue
        if (-not $n -or $n.Length -ne $manifiesto.Tamano -or
            [Math]::Abs($n.LastWriteTimeUtc.Ticks - $manifiesto.FechaUtcTicks) -gt $script:Tolerancia) {
            return (& $salida 'Pendiente' 0 'la copia del nodo no es la de su manifiesto: pulsa [T]')
        }
        if ($Archivo.Tamano -eq $manifiesto.Tamano -and
            [Math]::Abs($Archivo.FechaUtc.Ticks - $manifiesto.FechaUtcTicks) -le $script:Tolerancia) {
            return (& $salida 'AlDia' 0 '')
        }
        Write-Information ('   {0}: buscando lo que cambio ({1})...' -f $rel, (Format-BytesDelLaboratorio -Bytes $Archivo.Tamano)) -InformationAction Continue
        $actual = Get-HuellasPorBloque -Ruta $Archivo.Ruta -Bloque $Ficha.Bloque
        $indices = Get-IndicesCambiados -Actual $actual -Previo $manifiesto
    }
    else {
        # SIN MANIFIESTO Y YA EN EL NODO: no se sabe que tiene el nodo, y
        # subirlo entero es justo lo que no se quiere. Se lee primero en la Pi.
        if (Test-Path -LiteralPath $enNodo) {
            return (& $salida 'Pendiente' 0 'esta en el nodo y no tiene manifiesto: pulsa [T]')
        }
        Write-Information ('   {0}: nuevo, va entero ({1})...' -f $rel, (Format-BytesDelLaboratorio -Bytes $Archivo.Tamano)) -InformationAction Continue
        $actual = Get-HuellasPorBloque -Ruta $Archivo.Ruta -Bloque $Ficha.Bloque
        $indices = [int[]]@()
        if ($actual.Huellas.Count -gt 0) { $indices = [int[]](0..($actual.Huellas.Count - 1)) }
    }

    $bytes = Get-BytesDeIndices -Indices $indices -Bloque $Ficha.Bloque -Tamano $actual.Tamano
    if ($Simular) { return (& $salida 'Enviaria' $bytes '') }

    [void](Write-BloquesEnSuSitio -Origen $Archivo.Ruta -Destino $enNodo -Bloque $Ficha.Bloque -Indices $indices `
            -Tamano $actual.Tamano -FechaUtc $Archivo.FechaUtc -Confirm:$false)
    $leidas = Get-HuellasEnDestino -Ficha $Ficha -Relativa $rel -Indices $indices
    $cotejo = Compare-HuellasLeidas -Esperadas $actual.Huellas -Indices $indices -Leidas $leidas.Mapa `
        -Tamano $actual.Tamano -TamanoLeido $leidas.Tamano
    if (-not $cotejo.Correcto) {
        # EL MANIFIESTO NO SE TOCA: sigue diciendo lo que habia, y la siguiente
        # corrida no se puede fiar del nodo hasta una lectura completa.
        Write-RegistroRespaldo -Nivel 'ERROR' -Etapa 'laboratorio' -Mensaje ('{0}: {1} de {2} bloques no cuadran en el nodo (tamano {3} contra {4}).' -f
            $rel, $cotejo.Malos, @($indices).Count, $leidas.Tamano, $actual.Tamano)
        return (& $salida 'Falla' $bytes ('{0} bloques no cuadran en el nodo' -f $cotejo.Malos))
    }
    Write-ManifiestoDeBloques -Ruta $rutaManifiesto -Huellas $actual -FechaUtc $Archivo.FechaUtc -Confirm:$false
    Write-RegistroRespaldo -Etapa 'laboratorio' -Mensaje ('{0}: {1} bloques, {2}, confirmados en el nodo.' -f $rel, @($indices).Count, (Format-BytesDelLaboratorio -Bytes $bytes))
    return (& $salida 'Enviado' $bytes '')
}

function Invoke-GuardadoDelLaboratorio {
    <#
        .SYNOPSIS
            El guardado entero: guardas, archivos pequenos, discos grandes, estado.
        .PARAMETER Configuracion
            El objeto de configuracion completo.
        .PARAMETER Simular
            Mide lo que enviaria cada VM y no escribe nada.
    #>
    [CmdletBinding(SupportsShouldProcess, ConfirmImpact = 'High')]
    [OutputType([psobject])]
    param(
        [Parameter(Mandatory)][psobject] $Configuracion,
        [switch] $Simular
    )

    $inicio = Get-Date
    $res = [ordered]@{
        Inicio = $inicio; Simulada = [bool]$Simular; Abortada = $false; Motivo = $null
        PorVm = @(); Archivos = @(); Pequenos = $null; Enviado = [long]0; Estado = ''; Fin = $null
    }
    $abortar = {
        param($Motivo)
        $res.Abortada = $true
        $res.Motivo = $Motivo
        $res.Fin = Get-Date
        Write-RegistroRespaldo -Nivel 'ATENCION' -Etapa 'laboratorio' -Mensaje $Motivo
        [pscustomobject]$res
    }

    # --- Guardas -------------------------------------------------------------
    $ficha = Get-FichaDelLaboratorio -Configuracion $Configuracion
    if (-not $ficha) { return (& $abortar 'La configuracion no declara el bloque laboratorio.') }
    $carpeta = Get-CarpetaDeEstado -Configuracion $Configuracion
    if (-not (Test-Path -LiteralPath $ficha.Origen -PathType Container)) { return (& $abortar "No existe el laboratorio en $($ficha.Origen).") }
    if (Test-Path -LiteralPath (Join-Path $carpeta 'laboratorio\lectura.json')) {
        return (& $abortar 'Hay una lectura completa en la Pi sin recoger: pulsa [T] para recogerla antes de guardar.')
    }
    $marca = Get-MarcaDeCorrida -Carpeta $carpeta -Archivo 'LABORATORIO.lock'
    if ($marca.Existe -and -not $marca.Vieja) { return (& $abortar ('Ya hay un guardado del laboratorio en marcha (pid {0}).' -f $marca.Pid)) }
    $quieto = Test-LaboratorioQuieto -Ficha $ficha
    if (-not $quieto.Quieto) { return (& $abortar $quieto.Motivo) }
    $padre = Split-Path -Path $ficha.Destino -Parent
    if (-not (Test-Path -LiteralPath $padre)) { return (& $abortar "El nodo no responde en $padre.") }
    if ($ficha.Verificador -ne 'local') {
        try { [void](Invoke-EnLaPi -Ficha $Ficha -Orden 'true') }
        catch { return (& $abortar ('ssh {0} no responde: hace falta para que la Pi confirme lo escrito.' -f $ficha.Ssh)) }
    }

    $inventario = @(Get-InventarioDelLaboratorio -Ficha $ficha)
    # UN ESPEJO DE UN ORIGEN VACIO BORRA EL NODO. D: sin montar o recien
    # formateado no es "nada que guardar".
    if ($inventario.Count -eq 0) { return (& $abortar "El laboratorio en $($ficha.Origen) esta vacio: un espejo de un origen vacio borraria el nodo.") }
    $grandes = @($inventario | Where-Object { $_.Grande } | Sort-Object -Property Relativa)
    $pequenos = @($inventario | Where-Object { -not $_.Grande })

    # --- Espacio -------------------------------------------------------------
    $necesario = Get-SumaDe -Objetos $pequenos -Propiedad 'Tamano'
    foreach ($g in $grandes) {
        $m = Read-ManifiestoDeBloques -Ruta (Get-RutaDelManifiesto -Carpeta $carpeta -Relativa $g.Relativa)
        if ($m) { $necesario += [Math]::Max([long]0, $g.Tamano - $m.Tamano) }
        elseif (-not (Test-Path -LiteralPath (Join-Path $ficha.Destino $g.Relativa))) { $necesario += $g.Tamano }
    }
    $libre = Get-EspacioLibre -Ruta $padre
    if ($null -ne $libre -and $necesario -gt ($libre - $script:MargenEnNodo)) {
        return (& $abortar ('No cabe: hacen falta hasta {0} y el nodo tiene {1} libres (margen 20 GB).' -f
                (Format-BytesDelLaboratorio -Bytes $necesario), (Format-BytesDelLaboratorio -Bytes $libre)))
    }

    if (-not $Simular) {
        if (-not $PSCmdlet.ShouldProcess($ficha.Destino, 'Guardar el laboratorio')) { return (& $abortar 'Cancelado.') }
        Enter-MarcaDeCorrida -Carpeta $carpeta -Tipo 'laboratorio' -Archivo 'LABORATORIO.lock' -Confirm:$false | Out-Null
        Write-EstadoDelLaboratorio -Estado 'Copiando' -Detalle 'Guardando el laboratorio: solo lo que cambio' `
            -Momento $inicio.ToString('s') -Metodo 'delta' -Carpeta $carpeta -Confirm:$false
    }

    $archivos = New-Object System.Collections.Generic.List[psobject]
    try {
        if ($ficha.Verificador -ne 'local' -and -not $Simular) { [void](Publish-BlkhashEnLaPi -Ficha $ficha) }

        # --- 1. Lo pequeno, con robocopy, SIN los grandes --------------------
        # Si robocopy viera un disco grande lo truncaria y lo copiaria entero:
        # la trampa medida con el v1. Se excluye por NOMBRE, que vale en los dos
        # lados, y asi robocopy tampoco lo purga del nodo.
        $nombresGrandes = @($grandes | ForEach-Object { Split-Path -Path $_.Relativa -Leaf } | Sort-Object -Unique)
        Write-Information '   Archivos pequenos (configuracion de las VMs)...' -InformationAction Continue
        $res.Pequenos = Invoke-Robocopy -Origen $ficha.Origen -Destino $ficha.Destino -Clase 'B' -UnSoloHilo `
            -SoloListar:$Simular -ExcluirArchivos (@($ficha.ExcluirArchivos) + $nombresGrandes) -ExcluirCarpetas @($ficha.ExcluirCarpetas)

        # --- 2. Los grandes, por bloques -------------------------------------
        foreach ($g in $grandes) {
            try { $archivos.Add((Invoke-ArchivoGrande -Ficha $ficha -Archivo $g -Carpeta $carpeta -Simular:$Simular -Confirm:$false)) }
            catch {
                Write-RegistroRespaldo -Nivel 'ERROR' -Etapa 'laboratorio' -Mensaje ('{0}: {1}' -f $g.Relativa, $_.Exception.Message)
                $archivos.Add([pscustomobject]@{ Relativa = $g.Relativa; Vm = $g.Vm; Estado = 'Falla'; Enviado = [long]0; Motivo = $_.Exception.Message })
            }
        }

        # --- 3. Lo que ya no esta en D: --------------------------------------
        # Es un espejo, como el v1: un disco grande que se borro de D: se borra
        # del nodo. Solo si el nodo sigue siendo lo que dice su manifiesto.
        $raizManifiestos = Join-Path $carpeta 'laboratorio\bloques'
        if (Test-Path -LiteralPath $raizManifiestos) {
            $vigentes = @{}
            foreach ($a in $inventario) { $vigentes[$a.Relativa] = $a.Grande }
            foreach ($blk in @(Get-ChildItem -LiteralPath $raizManifiestos -Recurse -File -Filter '*.blk')) {
                $rel = $blk.FullName.Substring($raizManifiestos.Length + 1)
                $rel = $rel.Substring(0, $rel.Length - 4)
                if ($vigentes.ContainsKey($rel) -and $vigentes[$rel]) { continue }
                $vm = ''
                if ($rel.Contains('\')) { $vm = $rel.Split('\')[0] }
                if ($Simular) { continue }
                if ($vigentes.ContainsKey($rel)) {
                    # Encogio por debajo del umbral: ahora lo lleva robocopy.
                    Remove-Item -LiteralPath $blk.FullName -Force
                    continue
                }
                $m = Read-ManifiestoDeBloques -Ruta $blk.FullName
                $n = Get-Item -LiteralPath (Join-Path $ficha.Destino $rel) -Force -ErrorAction SilentlyContinue
                if ($n -and $n.Length -eq $m.Tamano -and [Math]::Abs($n.LastWriteTimeUtc.Ticks - $m.FechaUtcTicks) -le $script:Tolerancia) {
                    Remove-Item -LiteralPath $n.FullName -Force
                    Remove-Item -LiteralPath $blk.FullName -Force
                    Write-RegistroRespaldo -Etapa 'laboratorio' -Mensaje "$rel ya no esta en D: y se borro del nodo (espejo)."
                }
                elseif (-not $n) {
                    Remove-Item -LiteralPath $blk.FullName -Force
                }
                else {
                    $archivos.Add([pscustomobject]@{ Relativa = $rel; Vm = $vm; Estado = 'Pendiente'; Enviado = [long]0; Motivo = 'ya no esta en D: y el nodo no cuadra con su manifiesto: no se borra' })
                }
            }
        }
    }
    catch {
        # Que no quede en "Copiando": sin marca, eso el tablero lo pinta CORTADO,
        # que es verdad, pero sin decir por que.
        if (-not $Simular) {
            Write-EstadoDelLaboratorio -Estado 'Falla' -Detalle ('Se corto: {0}' -f $_.Exception.Message) `
                -Momento $inicio.ToString('s') -Metodo 'delta' -Carpeta $carpeta -Confirm:$false
        }
        throw
    }
    finally {
        if (-not $Simular) { Exit-MarcaDeCorrida -Carpeta $carpeta -Archivo 'LABORATORIO.lock' -Confirm:$false }
    }

    # --- Veredicto por VM ------------------------------------------------------
    $res.Archivos = $archivos.ToArray()
    $res.Enviado = Get-SumaDe -Objetos $archivos.ToArray() -Propiedad 'Enviado'
    $porVm = New-Object System.Collections.Generic.List[psobject]
    foreach ($v in @(Get-ResumenDeVmsDelLaboratorio -Inventario $inventario)) {
        $suyos = @($archivos | Where-Object { $_.Vm -eq $v.Vm })
        $peor = @($suyos | Where-Object { $_.Estado -eq 'Falla' }) + @($suyos | Where-Object { $_.Estado -eq 'Pendiente' })
        $estadoVm = 'AlDia'
        $detalle = ''
        if (@($suyos | Where-Object { $_.Estado -eq 'Falla' }).Count -gt 0) { $estadoVm = 'Falla' }
        elseif (@($suyos | Where-Object { $_.Estado -eq 'Pendiente' }).Count -gt 0) { $estadoVm = 'Pendiente' }
        if ($peor.Count -gt 0) { $detalle = '{0}: {1}' -f (Split-Path -Path $peor[0].Relativa -Leaf), $peor[0].Motivo }
        $porVm.Add([pscustomobject]@{
                Vm = $v.Vm; Bytes = $v.Bytes; Estado = $estadoVm; Detalle = $detalle
                Enviado = Get-SumaDe -Objetos $suyos -Propiedad 'Enviado'
            })
    }
    $res.PorVm = $porVm.ToArray()

    $fallan = @($porVm | Where-Object { $_.Estado -eq 'Falla' })
    $pendientes = @($porVm | Where-Object { $_.Estado -eq 'Pendiente' })
    $robocopyBien = $res.Pequenos.Correcto
    $partes = New-Object System.Collections.Generic.List[string]
    if (-not $robocopyBien) { $partes.Add('robocopy no dio por buenos los archivos pequenos') }
    if ($fallan.Count -gt 0) { $partes.Add(('{0} VMs fallaron ({1})' -f $fallan.Count, (($fallan | ForEach-Object { $_.Vm }) -join ', '))) }
    if ($pendientes.Count -gt 0) { $partes.Add(('{0} VMs pendientes de leer en la Pi con [T] ({1})' -f $pendientes.Count, (($pendientes | ForEach-Object { $_.Vm }) -join ', '))) }
    $res.Estado = 'Protegido'
    $frase = 'guardado: {0} enviados, el resto ya estaba en el nodo' -f (Format-BytesDelLaboratorio -Bytes $res.Enviado)
    if ($partes.Count -gt 0) { $res.Estado = 'Falla'; $frase = 'INCOMPLETO: ' + ($partes -join '; ') }

    if (-not $Simular) {
        $total = Get-SumaDe -Objetos $inventario -Propiedad 'Tamano'
        Write-EstadoDelLaboratorio -Estado $res.Estado -Detalle $frase -Momento $inicio.ToString('s') `
            -Tamano (Format-BytesDelLaboratorio -Bytes $total) -Metodo 'delta' -Carpeta $carpeta -Confirm:$false

        # LA FECHA DE GUARDADO SOLO SE MUEVE EN LAS VMS QUE QUEDARON AL DIA. Una
        # pendiente o fallida conserva la de su ultimo guardado bueno.
        $filas = foreach ($v in $porVm) {
            $guardado = $null
            if ($v.Estado -eq 'AlDia') { $guardado = $inicio.ToString('s') }
            [pscustomobject]@{ Vm = $v.Vm; Bytes = $v.Bytes; Guardado = $guardado; Enviado = $v.Enviado; Estado = $v.Estado; Detalle = $v.Detalle }
        }
        Write-EstadoDelLaboratorioPorVm -Filas @($filas | Where-Object { $_ }) -Vigentes @($porVm | ForEach-Object { $_.Vm }) -Carpeta $carpeta -Confirm:$false

        $resumen = @(
            'Ultimo guardado del laboratorio en el NAS'
            'Fecha:     {0:yyyy-MM-dd HH:mm}' -f $inicio
            'Metodo:    motor del cliente, por bloques (ADR-0104)'
            'Tamano:    {0} en {1} archivos' -f (Format-BytesDelLaboratorio -Bytes $total), $inventario.Count
            'Enviado:   {0}' -f (Format-BytesDelLaboratorio -Bytes $res.Enviado)
            'Duracion:  {0:N0} min' -f ((Get-Date) - $inicio).TotalMinutes
            "Resultado: $($res.Estado) - $frase"
        ) -join "`r`n"
        try { Set-ContenidoAtomico -Ruta (Join-Path $ficha.Destino 'ULTIMO_GUARDADO.txt') -Contenido $resumen -Confirm:$false }
        catch { Write-RegistroRespaldo -Nivel 'ATENCION' -Etapa 'laboratorio' -Mensaje "No se pudo escribir ULTIMO_GUARDADO.txt en el nodo: $($_.Exception.Message)" }
        $lab = Join-Path $ficha.Origen '_lab'
        if (Test-Path -LiteralPath $lab -PathType Container) {
            try { Set-ContenidoAtomico -Ruta (Join-Path $lab 'ULTIMO_GUARDADO.txt') -Contenido $resumen -Confirm:$false }
            catch { Write-Verbose "Sin ULTIMO_GUARDADO.txt en D:: $($_.Exception.Message)" }
        }
        $nivel = if ($res.Estado -eq 'Protegido') { 'OK' } else { 'ATENCION' }
        Write-RegistroRespaldo -Nivel $nivel -Etapa 'laboratorio' -Mensaje $frase
    }
    $res.Fin = Get-Date
    return [pscustomobject]$res
}

# ---------------------------------------------------------------------------
#  Leer el nodo entero en la Pi
# ---------------------------------------------------------------------------

function Complete-LecturaDeArchivo {
    <#
        .SYNOPSIS
            Lo que la Pi leyo de un archivo pasa a ser su manifiesto.
        .DESCRIPTION
            LA LECTURA MANDA SOBRE EL MANIFIESTO ANTERIOR. Lo leido es lo que el
            disco del nodo tiene de verdad; si no coincide con el manifiesto, el
            que se equivocaba era el manifiesto -o el disco se degrado-, y en los
            dos casos el siguiente guardado tiene que reenviar esos bloques.
        .PARAMETER Carpeta
            La carpeta de estado.
        .PARAMETER Relativa
            Ruta relativa a la raiz del laboratorio.
        .PARAMETER Huellas
            Lo leido: Tamano, Bloque y Huellas.
        .PARAMETER FechaUtc
            La fecha del archivo en el nodo cuando se leyo.
    #>
    [CmdletBinding(SupportsShouldProcess)]
    [OutputType([psobject])]
    param(
        [Parameter(Mandatory)][string] $Carpeta,
        [Parameter(Mandatory)][string] $Relativa,
        [Parameter(Mandatory)][psobject] $Huellas,
        [Parameter(Mandatory)][datetime] $FechaUtc
    )

    $ruta = Get-RutaDelManifiesto -Carpeta $Carpeta -Relativa $Relativa
    $previo = Read-ManifiestoDeBloques -Ruta $ruta
    $distintos = $null
    if ($previo) {
        # Sin @(): Get-IndicesCambiados devuelve el arreglo como UN objeto, y
        # @() lo envolveria en otro de un elemento -- "1 distinto" siempre.
        $cambiados = Get-IndicesCambiados -Actual $Huellas -Previo $previo
        $distintos = $cambiados.Count
        if ($previo.Tamano -ne $Huellas.Tamano) { $distintos++ }
        if ($distintos -gt 0) {
            Write-RegistroRespaldo -Nivel 'ATENCION' -Etapa 'laboratorio' -Mensaje ('{0}: el nodo no era su manifiesto en {1} bloques. Se corrige el manifiesto; el siguiente guardado los reenvia.' -f $Relativa, $distintos)
        }
    }
    Write-ManifiestoDeBloques -Ruta $ruta -Huellas $Huellas -FechaUtc $FechaUtc -Confirm:$false
    $vm = ''
    if ($Relativa.Contains('\')) { $vm = $Relativa.Split('\')[0] }
    return [pscustomobject]@{ Relativa = $Relativa; Vm = $vm; TeniaManifiesto = [bool]$previo; Distintos = $distintos; Correcto = $true; Motivo = '' }
}

function Invoke-LecturaDelLaboratorio {
    <#
        .SYNOPSIS
            La lectura completa del laboratorio en la Pi: la lanza, dice como va o la recoge.
        .DESCRIPTION
            SON HORAS Y EL EQUIPO NO TIENE QUE ESPERAR. La Pi lee a 23 MB/s: unas
            3.5 h para los 281 GB del laboratorio. Se lanza en la Pi con setsid y
            se desentiende; cada vez que se pulsa [T] despues, dice por donde va,
            y cuando termino, recoge y convierte lo leido en manifiestos.

            Se puede hacer con las VMs encendidas: lee el NODO, no D:. Lo unico
            que no se puede es guardar mientras tanto, y Invoke-Guardado... se
            niega mientras haya una lectura sin recoger.
        .PARAMETER Configuracion
            El objeto de configuracion completo.
    #>
    [CmdletBinding(SupportsShouldProcess)]
    [OutputType([psobject])]
    param([Parameter(Mandatory)][psobject] $Configuracion)

    $ficha = Get-FichaDelLaboratorio -Configuracion $Configuracion
    if (-not $ficha) { return [pscustomobject]@{ Estado = 'SinLaboratorio'; Detalle = 'La configuracion no declara el bloque laboratorio.'; Archivos = @() } }
    $carpeta = Get-CarpetaDeEstado -Configuracion $Configuracion
    $trabajo = Join-Path $carpeta 'laboratorio\lectura.json'
    $marcarVms = {
        param($Leidos, $Momento)
        $filas = foreach ($g in @($Leidos | Group-Object -Property Vm)) {
            if (@($g.Group | Where-Object { -not $_.Correcto }).Count -gt 0) { continue }
            $previa = (Read-EstadoDelLaboratorioPorVm -Carpeta $carpeta)[$g.Name]
            $estado = $null
            if ($previa -and $previa.Estado -eq 'Pendiente') { $estado = '' }
            [pscustomobject]@{ Vm = $g.Name; Lectura = $Momento; Estado = $estado }
        }
        Write-EstadoDelLaboratorioPorVm -Filas @($filas | Where-Object { $_ }) -Carpeta $carpeta -Confirm:$false
    }

    # --- Verificador local (la bateria): leer y escribir en el acto ----------
    if ($ficha.Verificador -eq 'local') {
        $momento = (Get-Date).ToString('s')
        $leidos = foreach ($a in @(Get-InventarioDelLaboratorio -Ficha $ficha -Raiz $ficha.Destino | Where-Object { $_.Grande })) {
            Complete-LecturaDeArchivo -Carpeta $carpeta -Relativa $a.Relativa -FechaUtc $a.FechaUtc `
                -Huellas (Get-HuellasPorBloque -Ruta $a.Ruta -Bloque $ficha.Bloque) -Confirm:$false
        }
        & $marcarVms @($leidos) $momento
        return [pscustomobject]@{ Estado = 'Recogida'; Detalle = ('{0} archivos leidos' -f @($leidos).Count); Archivos = @($leidos) }
    }

    $dir = $script:LecturaEnLaPi
    $usuario = $ficha.UsuarioEnLaPi

    # --- Ya hay una lanzada: como va, o recogerla ----------------------------
    if (Test-Path -LiteralPath $trabajo -PathType Leaf) {
        $t = Get-Content -LiteralPath $trabajo -Raw -Encoding UTF8 | ConvertFrom-Json
        $estado = @(Invoke-EnLaPi -Ficha $ficha -Orden ("sudo -n -u {0} sh -c 'cat {1}/salida/AVANCE 2>/dev/null; test -f {1}/salida/FIN && echo FIN; true'" -f $usuario, $dir))
        if ($estado -notcontains 'FIN') {
            $avance = ''
            if ($estado.Count -gt 0) { $avance = $estado[0] }
            return [pscustomobject]@{ Estado = 'EnCurso'; Detalle = ('la Pi va por el archivo {0} (lanzada {1})' -f $avance.Replace(' ', ' de '), $t.Inicio); Archivos = @() }
        }

        $leidos = New-Object System.Collections.Generic.List[psobject]
        foreach ($e in @($t.Archivos)) {
            $rel = '' + $e.Relativa
            $vm = ''
            if ($rel.Contains('\')) { $vm = $rel.Split('\')[0] }
            try {
                $lineas = @(Invoke-EnLaPi -Ficha $ficha -Orden ("sudo -n -u {0} sh -c 'cat {1}/salida/{2}.blk 2>/dev/null || cat {1}/salida/{2}.err'" -f $usuario, $dir, $e.I))
                if ($lineas.Count -eq 0 -or $lineas[0] -notmatch '^BS (\d+) SIZE (\d+) MTIMENS (\d+) ESTABLE (\d)$') {
                    throw ('la Pi no pudo leerlo: {0}' -f ($lineas -join ' '))
                }
                $bs = [int]$Matches[1]; $size = [long]$Matches[2]; $ns = [long]$Matches[3]; $estable = $Matches[4] -eq '1'
                if (-not $estable) { throw 'cambio mientras la Pi lo leia' }
                $fecha = [datetime]::new(621355968000000000 + [long]($ns / 100), [System.DateTimeKind]::Utc)
                $h = [pscustomobject]@{ Tamano = $size; Bloque = $bs; Huellas = @($lineas | Select-Object -Skip 1 | Where-Object { $_ }) }
                $leidos.Add((Complete-LecturaDeArchivo -Carpeta $carpeta -Relativa $rel -Huellas $h -FechaUtc $fecha -Confirm:$false))
            }
            catch {
                Write-RegistroRespaldo -Nivel 'ERROR' -Etapa 'laboratorio' -Mensaje ('Lectura en la Pi de {0}: {1}' -f $rel, $_.Exception.Message)
                $leidos.Add([pscustomobject]@{ Relativa = $rel; Vm = $vm; TeniaManifiesto = $false; Distintos = $null; Correcto = $false; Motivo = $_.Exception.Message })
            }
        }
        & $marcarVms $leidos.ToArray() ('' + $t.Inicio)
        [void](Invoke-EnLaPi -Ficha $ficha -Orden ("sudo -n -u {0} rm -rf {1}" -f $usuario, $dir))
        Remove-Item -LiteralPath $trabajo -Force
        $malos = @($leidos | Where-Object { -not $_.Correcto }).Count
        $distintos = Get-SumaDe -Objetos @($leidos | Where-Object { $_.Distintos }) -Propiedad 'Distintos'
        $frase = '{0} archivos leidos en la Pi, {1} con error, {2} bloques que no eran su manifiesto' -f $leidos.Count, $malos, $distintos
        Write-RegistroRespaldo -Nivel $(if ($malos -eq 0) { 'OK' } else { 'ATENCION' }) -Etapa 'laboratorio' -Mensaje $frase
        return [pscustomobject]@{ Estado = 'Recogida'; Detalle = $frase; Archivos = $leidos.ToArray() }
    }

    # --- Lanzarla ------------------------------------------------------------
    if (-not (Test-Path -LiteralPath $ficha.Destino -PathType Container)) {
        return [pscustomobject]@{ Estado = 'SinNodo'; Detalle = "El nodo no responde en $($ficha.Destino)."; Archivos = @() }
    }
    $grandes = @(Get-InventarioDelLaboratorio -Ficha $ficha -Raiz $ficha.Destino | Where-Object { $_.Grande } | Sort-Object -Property Relativa)
    if ($grandes.Count -eq 0) { return [pscustomobject]@{ Estado = 'Nada'; Detalle = 'En el nodo no hay discos grandes que leer.'; Archivos = @() } }
    if (-not $PSCmdlet.ShouldProcess($ficha.DestinoEnLaPi, 'Lanzar la lectura completa en la Pi')) { return $null }

    [void](Publish-BlkhashEnLaPi -Ficha $ficha)
    $lista = @($grandes | ForEach-Object { Get-RutaEnLaPi -Ficha $ficha -Relativa $_.Relativa })
    [void](Invoke-EnLaPi -Ficha $ficha -Entrada $lista -Orden ("sudo -n -u {0} sh -c 'rm -rf {1} && mkdir -p {1} && cat > {1}/lista.txt'" -f $usuario, $dir))
    [void](Invoke-EnLaPi -Ficha $ficha -Orden ("sudo -n -u {0} sh -c 'cd {1} && setsid nohup python3 {2} lote {3} lista.txt salida </dev/null >log.txt 2>&1 &'" -f
            $usuario, $dir, $script:BlkhashEnLaPi, $ficha.Bloque))

    $i = 0
    $registro = [pscustomobject]@{
        Inicio   = (Get-Date).ToString('s')
        Bloque   = $ficha.Bloque
        Archivos = @($grandes | ForEach-Object { [pscustomobject]@{ I = $i; Relativa = $_.Relativa }; $i++ })
    }
    $dirTrabajo = Split-Path -Path $trabajo -Parent
    if (-not (Test-Path -LiteralPath $dirTrabajo)) { New-Item -ItemType Directory -Path $dirTrabajo -Force | Out-Null }
    Set-ContenidoAtomico -Ruta $trabajo -Contenido ($registro | ConvertTo-Json -Depth 4) -Confirm:$false
    $bytes = Get-SumaDe -Objetos $grandes -Propiedad 'Tamano'
    # 23 MB/s: lo que lee la Pi de su disco por el USB 2.0 (seccion 5.2).
    $minutos = [Math]::Max(1, [int]($bytes / 23MB / 60))
    $cuanto = if ($minutos -lt 90) { '{0} min' -f $minutos } else { '{0:N1} h' -f ($minutos / 60) }
    $frase = 'Lectura completa lanzada en la Pi: {0} archivos, {1}, unos {2}. Vuelve a pulsar [T] para ver como va.' -f $grandes.Count, (Format-BytesDelLaboratorio -Bytes $bytes), $cuanto
    Write-RegistroRespaldo -Etapa 'laboratorio' -Mensaje $frase
    return [pscustomobject]@{ Estado = 'Lanzada'; Detalle = $frase; Archivos = @() }
}

# ---------------------------------------------------------------------------
#  Ver
# ---------------------------------------------------------------------------

function Get-VistaDelLaboratorio {
    <#
        .SYNOPSIS
            Lo que pinta [L]: una fila por VM. No escribe nada.
        .DESCRIPTION
            D: se lista en local. El nodo se lista por SMB -unos cien archivos-
            solo para la columna EN EL NODO; si no responde, la columna sale con
            '?' y lo demas igual.
        .PARAMETER Configuracion
            El objeto de configuracion completo.
    #>
    [CmdletBinding()]
    [OutputType([psobject])]
    param([Parameter(Mandatory)][psobject] $Configuracion)

    $ficha = Get-FichaDelLaboratorio -Configuracion $Configuracion
    if (-not $ficha) { return $null }
    $carpeta = Get-CarpetaDeEstado -Configuracion $Configuracion
    $estado = Read-EstadoRespaldo -Carpeta $carpeta

    $vms = @()
    if (Test-Path -LiteralPath $ficha.Origen -PathType Container) {
        $vms = @(Get-ResumenDeVmsDelLaboratorio -Inventario @(Get-InventarioDelLaboratorio -Ficha $ficha))
    }
    $enNodo = $null
    try {
        if (Test-Path -LiteralPath $ficha.Destino -PathType Container) {
            $enNodo = @{}
            foreach ($v in @(Get-ResumenDeVmsDelLaboratorio -Inventario @(Get-InventarioDelLaboratorio -Ficha $ficha -Raiz $ficha.Destino))) {
                $enNodo[$v.Vm] = $v.Bytes
            }
        }
    }
    catch { $enNodo = $null }

    $abiertas = @(Get-VmAbiertaDelLaboratorio -Ficha $ficha)
    $filas = @(Get-FilasDelLaboratorio -Vms $vms -PorVm (Read-EstadoDelLaboratorioPorVm -Carpeta $carpeta) `
            -EnNodo $enNodo -Base ('' + $estado['laboratorio_copia']) -Abiertas $abiertas)
    $lectura = ''
    $trabajo = Join-Path $carpeta 'laboratorio\lectura.json'
    if (Test-Path -LiteralPath $trabajo) {
        try { $lectura = '' + (Get-Content -LiteralPath $trabajo -Raw -Encoding UTF8 | ConvertFrom-Json).Inicio } catch { $lectura = '?' }
    }
    $manifiestos = 0
    $raizManifiestos = Join-Path $carpeta 'laboratorio\bloques'
    if (Test-Path -LiteralPath $raizManifiestos) {
        $manifiestos = @(Get-ChildItem -LiteralPath $raizManifiestos -Recurse -File -Filter '*.blk').Count
    }
    return [pscustomobject]@{
        Filas          = $filas
        Abiertas       = $abiertas
        NodoLeido      = ($null -ne $enNodo)
        LecturaEnCurso = $lectura
        Manifiestos    = $manifiestos
        Detalle        = '' + $estado['laboratorio_detalle']
    }
}

if ($MyInvocation.InvocationName -eq '.') {
    Write-Verbose 'laboratorio.ps1 cargado con punto: se exponen las funciones y no se toca nada.'
    return
}

$parametrosConfig = @{}
if ($PSBoundParameters.ContainsKey('RutaConfiguracion')) { $parametrosConfig['Ruta'] = $RutaConfiguracion }
$configuracion = Get-ConfiguracionRespaldo @parametrosConfig

switch ($Accion) {
    'Ver'     { Get-VistaDelLaboratorio -Configuracion $configuracion }
    'Previa'  { Invoke-GuardadoDelLaboratorio -Configuracion $configuracion -Simular }
    'Guardar' { Invoke-GuardadoDelLaboratorio -Configuracion $configuracion -Confirm:$false }
    'Leer'    { Invoke-LecturaDelLaboratorio -Configuracion $configuracion -Confirm:$false }
}
