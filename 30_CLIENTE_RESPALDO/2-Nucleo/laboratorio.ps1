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
        Ver              la tabla por VM, sin escribir nada.
        Previa           cuanto enviaria cada VM, sin escribir nada.
        Guardar          envia lo que cambio.
        PreviaFrio       cuanto copiaria al disco frio, sin escribir nada.
        GuardarFrio      copia el laboratorio al disco frio (aditivo, por bloques, sin red).
        Validar          esta cada VM en D:, el nodo y el disco frio? Solo lee, sin releer contenido.
        Leer             la lectura del laboratorio en la Pi: la lanza, dice como va o la recoge.
        PlanLectura      analiza que archivos grandes hay en el nodo y cuales estan pendientes de [T].
        EstadoLectura    consulta en vivo el estado de una lectura [T] en curso sin recogerla.
        CancelarLectura  detiene una lectura [T] en curso en la Pi y limpia lectura.json.

    .PARAMETER SoloPendientes
        Solo para -Accion Leer: lee en la Pi unicamente los archivos grandes cuyo
        manifiesto falta, no cuadra con el nodo o pertenece a una VM marcada Pendiente.

    .EXAMPLE
        .\laboratorio.ps1 -Accion Previa
        .\laboratorio.ps1 -Accion Guardar
#>
[CmdletBinding(SupportsShouldProcess, ConfirmImpact = 'High')]
[OutputType([psobject])]
param(
    [ValidateNotNullOrEmpty()]
    [string] $RutaConfiguracion,

    [ValidateSet('Ver', 'Previa', 'Guardar', 'PreviaFrio', 'GuardarFrio', 'Validar', 'Leer', 'PlanLectura', 'EstadoLectura', 'CancelarLectura')]
    [string] $Accion = 'Ver',

    [switch] $SoloPendientes,

    [switch] $Profundo,

    [switch] $PassThru
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
        .PARAMETER Juego
            Que destino describe: 'bloques' es el nodo; 'bloques-frio-<firma>'
            es un disco frio concreto. Cada destino tiene su propio estado, y el
            del disco lleva SU firma: otro disco no hereda lo que no tiene.
    #>
    [CmdletBinding()]
    [OutputType([string])]
    param(
        [Parameter(Mandatory)][ValidateNotNullOrEmpty()][string] $Carpeta,
        [Parameter(Mandatory)][ValidateNotNullOrEmpty()][string] $Relativa,
        [ValidateNotNullOrEmpty()][string] $Juego = 'bloques'
    )
    return (Join-Path (Join-Path $Carpeta ('laboratorio\' + $Juego)) ($Relativa + '.blk'))
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

function Group-IndicesContiguos {
    <#
        .SYNOPSIS
            Agrupa indices consecutivos en tramos contiguos acotados (run coalescing).
        .DESCRIPTION
            Escribir bloques de 1 MiB de uno en uno por SMB paga un ciclo
            sincrono por bloque. Agrupar bloques consecutivos hasta 4 MiB satura
            el pipelining multi-credito de SMB3 sin disparar la memoria.
        .PARAMETER Indices
            Indices de bloque a escribir.
        .PARAMETER Bloque
            Tamano de bloque en bytes.
        .PARAMETER MaxBytesTramo
            Tope por escritura contigua. Por omision 4 MiB.
    #>
    [CmdletBinding()]
    [OutputType([psobject[]])]
    param(
        [Parameter(Mandatory)][AllowEmptyCollection()][int[]] $Indices,
        [Parameter(Mandatory)][ValidateRange(4096, 67108864)][int] $Bloque,
        [ValidateRange(4096, 67108864)][int] $MaxBytesTramo = 4MB
    )

    $tramos = New-Object System.Collections.Generic.List[psobject]
    if (@($Indices).Count -eq 0) { return , $tramos.ToArray() }

    $maxBloques = [Math]::Max(1, [int][Math]::Floor($MaxBytesTramo / $Bloque))
    $ordenados = @($Indices | Sort-Object -Unique)

    $inicio = $ordenados[0]
    $cuenta = 1
    for ($k = 1; $k -lt $ordenados.Count; $k++) {
        $actual = $ordenados[$k]
        if ($actual -eq ($inicio + $cuenta) -and $cuenta -lt $maxBloques) {
            $cuenta++
        }
        else {
            $tramos.Add([pscustomobject]@{ Inicio = $inicio; Cuenta = $cuenta })
            $inicio = $actual
            $cuenta = 1
        }
    }
    $tramos.Add([pscustomobject]@{ Inicio = $inicio; Cuenta = $cuenta })
    return , $tramos.ToArray()
}

function Write-BloquesEnSuSitio {
    <#
        .SYNOPSIS
            Copia esos bloques del origen al destino, en su sitio. Ajusta tamano y fecha.
        .DESCRIPTION
            ABRE SIN TRUNCAR. Es lo que robocopy no sabe hacer: abrir el destino
            y escribir solo los bloques que cambiaron. Si el destino no existe se
            crea, y entonces los indices son todos.

            Agrupa bloques contiguos hasta 4 MiB y abre los FileStream con
            buffer explicito de 1 MiB (SequentialScan) para aprovechar el
            pipelining SMB3 en vez del buffer por omision de 4 KiB.
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
        .PARAMETER Vm
            Opcional: nombre de la VM para telemetria en LABORATORIO.progreso.
        .PARAMETER CarpetaEstado
            Opcional: carpeta de estado donde escribir LABORATORIO.progreso.
    #>
    [CmdletBinding(SupportsShouldProcess)]
    [OutputType([long])]
    param(
        [Parameter(Mandatory)][ValidateNotNullOrEmpty()][string] $Origen,
        [Parameter(Mandatory)][ValidateNotNullOrEmpty()][string] $Destino,
        [Parameter(Mandatory)][int] $Bloque,
        [Parameter(Mandatory)][AllowEmptyCollection()][int[]] $Indices,
        [Parameter(Mandatory)][long] $Tamano,
        [Parameter(Mandatory)][datetime] $FechaUtc,
        [AllowEmptyString()][string] $Vm = '',
        [AllowEmptyString()][string] $CarpetaEstado = ''
    )

    if (-not $PSCmdlet.ShouldProcess($Destino, "Escribir $(@($Indices).Count) bloques")) { return [long]0 }
    $dir = Split-Path -Path $Destino -Parent
    if (-not (Test-Path -LiteralPath $dir)) { New-Item -ItemType Directory -Path $dir -Force | Out-Null }

    $totalBytes = Get-BytesDeIndices -Indices $Indices -Bloque $Bloque -Tamano $Tamano
    $tramos = Group-IndicesContiguos -Indices $Indices -Bloque $Bloque -MaxBytesTramo 4MB
    $maxBuf = [Math]::Max($Bloque, 4MB)
    $bufferStream = [Math]::Min(1048576, [Math]::Max(65536, $Bloque))
    $hoja = Split-Path -Path $Destino -Leaf
    $crono = [System.Diagnostics.Stopwatch]::StartNew()
    $ultimoAviso = [long]0

    $escritos = [long]0
    $o = New-Object System.IO.FileStream($Origen, [System.IO.FileMode]::Open, [System.IO.FileAccess]::Read,
        [System.IO.FileShare]::ReadWrite, $bufferStream, [System.IO.FileOptions]::SequentialScan)
    try {
        $d = New-Object System.IO.FileStream($Destino, [System.IO.FileMode]::OpenOrCreate, [System.IO.FileAccess]::ReadWrite,
            [System.IO.FileShare]::None, $bufferStream, [System.IO.FileOptions]::SequentialScan)
        try {
            if ($d.Length -ne $Tamano) { $d.SetLength($Tamano) }
            $buf = New-Object byte[] $maxBuf
            foreach ($tramo in $tramos) {
                $off = [long]$tramo.Inicio * $Bloque
                $largo = [int][Math]::Min([long]$tramo.Cuenta * $Bloque, $Tamano - $off)
                if ($largo -le 0) { continue }
                if ($buf.Length -lt $largo) { $buf = New-Object byte[] $largo }

                if ($o.Position -ne $off) { $o.Position = $off }
                $leido = 0
                while ($leido -lt $largo) {
                    $r = $o.Read($buf, $leido, $largo - $leido)
                    if ($r -le 0) { throw "El origen se acabo antes de tiempo en el bloque $($tramo.Inicio) de $Origen" }
                    $leido += $r
                }

                if ($d.Position -ne $off) { $d.Position = $off }
                $d.Write($buf, 0, $largo)
                $escritos += $largo

                if (($escritos - $ultimoAviso) -ge 16MB -or $escritos -eq $totalBytes) {
                    $ultimoAviso = $escritos
                    $seg = [Math]::Max(0.1, $crono.Elapsed.TotalSeconds)
                    $mbs = ($escritos / 1MB) / $seg
                    $eta = if ($mbs -gt 0.05 -and $totalBytes -gt $escritos) { [int][Math]::Ceiling((($totalBytes - $escritos) / 1MB) / $mbs / 60.0) } else { 0 }
                    $pct = if ($totalBytes -gt 0) { [Math]::Min(100, [int](100.0 * $escritos / $totalBytes)) } else { 100 }
                    Write-Progress -Activity "Enviando $hoja" `
                        -Status ('{0:N0} de {1:N0} MiB ({2:N1} MB/s)' -f ($escritos / 1MB), ($totalBytes / 1MB), $mbs) `
                        -PercentComplete $pct
                    if ($CarpetaEstado -and $Vm) {
                        Write-ProgresoDelLaboratorio -Vm $Vm -Archivo $hoja -Fase 'escribiendo' `
                            -Hechos $escritos -Totales $totalBytes -MbPorSegundo $mbs -EtaMinutos $eta `
                            -Carpeta $CarpetaEstado -Confirm:$false
                    }
                }
            }
            # A DISCO, no al cache del sistema: la confirmacion que sigue no
            # vale nada si lo escrito todavia vive solo en memoria.
            $d.Flush($true)
        }
        finally { $d.Dispose() }
    }
    finally { $o.Dispose() }
    Write-Progress -Activity "Enviando $hoja" -Completed
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
    $esperada = Get-HuellaDeArchivo -Ruta $local -Algoritmo SHA256
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
#  Lecturas locales: la huella de D: se calcula UNA vez, el disco USB se lee sin cache
# ---------------------------------------------------------------------------

function Get-HuellasDeOrigen {
    <#
        .SYNOPSIS
            Las huellas por bloque de un archivo de D:, sin releerlo si no cambio.
        .DESCRIPTION
            Hashear 300 GB de D: cuesta ~8 min de NVMe y se pagaba otra vez por
            cada destino (nodo, disco frio). Se guarda la huella junto con el
            tamano y la fecha que D: tenia al calcularla; si siguen iguales, se
            reutiliza. Es la MISMA confianza que ya sostiene el "al dia" del
            nodo (tamano y fecha iguales = sin cambios), asi que no anade
            ningun supuesto nuevo. Es estado derivado, no copia: si se borra, se
            recalcula.
        .PARAMETER Ficha
            Lo que devuelve Get-FichaDelLaboratorio.
        .PARAMETER Archivo
            Una entrada de Get-InventarioDelLaboratorio.
        .PARAMETER Carpeta
            La carpeta de estado.
    #>
    [CmdletBinding()]
    [OutputType([psobject])]
    param(
        [Parameter(Mandatory)][psobject] $Ficha,
        [Parameter(Mandatory)][psobject] $Archivo,
        [Parameter(Mandatory)][string] $Carpeta
    )

    $rutaCache = Get-RutaDelManifiesto -Carpeta $Carpeta -Relativa $Archivo.Relativa -Juego 'origen'
    $m = $null
    try { $m = Read-ManifiestoDeBloques -Ruta $rutaCache } catch { $m = $null }
    if ($m -and $m.Bloque -eq $Ficha.Bloque -and $m.Tamano -eq $Archivo.Tamano -and
        $m.FechaUtcTicks -eq $Archivo.FechaUtc.Ticks -and
        @($m.Huellas).Count -eq [int][Math]::Ceiling($Archivo.Tamano / [double]$Ficha.Bloque)) {
        return [pscustomobject]@{ Tamano = [long]$m.Tamano; Bloque = [int]$m.Bloque; Huellas = @($m.Huellas) }
    }
    $actual = Get-HuellasPorBloque -Ruta $Archivo.Ruta -Bloque $Ficha.Bloque
    try { Write-ManifiestoDeBloques -Ruta $rutaCache -Huellas $actual -FechaUtc $Archivo.FechaUtc -Confirm:$false }
    catch { Write-Verbose "Sin cache de huellas para $($Archivo.Relativa): $($_.Exception.Message)" }
    return $actual
}

function Get-HuellasDeIndices {
    <#
        .SYNOPSIS
            SHA-256 de esos bloques de un archivo LOCAL, leidos del disco y no
            de la memoria.
        .DESCRIPTION
            Lo que la Pi hace con "la cache tirada", aqui con FILE_FLAG_NO_BUFFERING:
            el cotejo de lo que se acaba de escribir en el disco USB tiene que
            salir del disco. Si el sistema lo sirviera de su cache, el cotejo
            confirmaria lo que se escribio, no lo que quedo. Si el volumen no
            admite lectura directa, cae a lectura normal y lo dice en Verbose.
        .PARAMETER Ruta
            El archivo.
        .PARAMETER Bloque
            Tamano de bloque. Multiplo de 4096 para la lectura directa.
        .PARAMETER Indices
            Los bloques a leer. Vacio: todos.
    #>
    [CmdletBinding()]
    [OutputType([psobject])]
    param(
        [Parameter(Mandatory)][ValidateNotNullOrEmpty()][string] $Ruta,
        [Parameter(Mandatory)][ValidateRange(4096, 67108864)][int] $Bloque,
        [AllowEmptyCollection()][int[]] $Indices = @()
    )

    $abrir = {
        param($Directo)
        $opc = [System.IO.FileOptions]::SequentialScan
        $buf = 1048576
        if ($Directo) { $opc = [System.IO.FileOptions]0x20000000; $buf = 1 }
        New-Object System.IO.FileStream($Ruta, [System.IO.FileMode]::Open, [System.IO.FileAccess]::Read, [System.IO.FileShare]::Read, $buf, $opc)
    }
    $directo = ($Bloque % 4096 -eq 0)
    try { $fs = & $abrir $directo }
    catch { Write-Verbose "Sin lectura directa en ${Ruta}: $($_.Exception.Message)"; $directo = $false; $fs = & $abrir $false }

    $sha = New-Object System.Security.Cryptography.SHA256Cng
    $mapa = @{}
    try {
        $tamano = $fs.Length
        $total = [int][Math]::Ceiling($tamano / [double]$Bloque)
        $lista = @($Indices)
        if ($lista.Count -eq 0 -and $total -gt 0) { $lista = @(0..($total - 1)) }
        $buf = New-Object byte[] $Bloque
        foreach ($i in ($lista | Sort-Object -Unique)) {
            $off = [long]$i * $Bloque
            if ($off -ge $tamano) { $mapa[[int]$i] = '-'; continue }
            $largo = [int][Math]::Min([long]$Bloque, $tamano - $off)
            $fs.Position = $off
            $leido = 0
            # Sin buffer, la ultima lectura se pide completa y devuelve lo que hay.
            while ($leido -lt $largo) {
                $r = $fs.Read($buf, $leido, $Bloque - $leido)
                if ($r -le 0) { break }
                $leido += $r
            }
            if ($leido -lt $largo) { $mapa[[int]$i] = '-'; continue }
            $mapa[[int]$i] = ([System.BitConverter]::ToString($sha.ComputeHash($buf, 0, $largo))).Replace('-', '').ToLowerInvariant()
        }
    }
    finally { $fs.Dispose(); $sha.Dispose() }
    return [pscustomobject]@{ Tamano = [long]$tamano; Mapa = $mapa }
}

# ---------------------------------------------------------------------------
#  La copia fria del laboratorio: mismo metodo que el nodo, sin red
# ---------------------------------------------------------------------------

function Get-RaizFriaDelLaboratorio {
    <#
        .SYNOPSIS
            Donde caen las VMs en el disco frio y que juego de manifiestos las describe.
        .PARAMETER Ficha
            Lo que devuelve Get-FichaDelLaboratorio.
        .PARAMETER Disco
            Lo que devuelve Get-DiscoFrio.
    #>
    [CmdletBinding()]
    [OutputType([psobject])]
    param(
        [Parameter(Mandatory)][psobject] $Ficha,
        [Parameter(Mandatory)][psobject] $Disco
    )
    return [pscustomobject]@{
        Raiz  = (Join-Path $Disco.Raiz $Ficha.Frio.Relativa).TrimEnd('\')
        Juego = ('bloques-frio-' + $Disco.Firma)
    }
}

function Invoke-ArchivoGrandeFrio {
    <#
        .SYNOPSIS
            Un disco virtual grande al disco frio: solo sus bloques cambiados.
        .DESCRIPTION
            Como Invoke-ArchivoGrande, con tres diferencias que salen de que el
            destino es un disco LOCAL y no un recurso de red:

              1. NO HAY PENDIENTE. Si el disco tiene el archivo y no hay
                 manifiesto -o el archivo no es el que dice el manifiesto: copia
                 cortada, otro programa, disco cambiado de caja-, se lee ESE
                 archivo en local y se anota. Leer el USB cuesta minutos; releer
                 el nodo costaba horas. Asi una copia interrumpida se reanuda
                 sola, bloque a bloque, sin empezar de cero.
              2. EL COTEJO SALE DEL DISCO. Los bloques escritos se vuelven a leer
                 sin cache y se comparan con los de D:. Solo entonces cambia el
                 manifiesto.
              3. NUNCA BORRA. Un archivo que ya no esta en D: se queda en el
                 disco (aditivo, seccion 6).
        .PARAMETER Ficha
            Lo que devuelve Get-FichaDelLaboratorio.
        .PARAMETER Archivo
            Una entrada de Get-InventarioDelLaboratorio.
        .PARAMETER Carpeta
            La carpeta de estado.
        .PARAMETER Frio
            Lo que devuelve Get-RaizFriaDelLaboratorio.
        .PARAMETER Simular
            Mide y no escribe (salvo el cache de huellas de D:).
    #>
    [CmdletBinding(SupportsShouldProcess)]
    [OutputType([psobject])]
    param(
        [Parameter(Mandatory)][psobject] $Ficha,
        [Parameter(Mandatory)][psobject] $Archivo,
        [Parameter(Mandatory)][string] $Carpeta,
        [Parameter(Mandatory)][psobject] $Frio,
        [switch] $Simular
    )

    $rel = $Archivo.Relativa
    $enFrio = Join-Path $Frio.Raiz $rel
    $rutaManifiesto = Get-RutaDelManifiesto -Carpeta $Carpeta -Relativa $rel -Juego $Frio.Juego
    $manifiesto = Read-ManifiestoDeBloques -Ruta $rutaManifiesto
    $salida = {
        param($Estado, $Enviado, $Motivo)
        [pscustomobject]@{ Relativa = $rel; Vm = $Archivo.Vm; Estado = $Estado; Enviado = [long]$Enviado; Motivo = $Motivo }
    }
    $mismaFecha = { param($A, $B) [Math]::Abs($A - $B) -le $script:Tolerancia }

    $enDisco = Get-Item -LiteralPath $enFrio -Force -ErrorAction SilentlyContinue
    $adoptado = $false
    if ($manifiesto -and $enDisco -and
        ($enDisco.Length -ne $manifiesto.Tamano -or -not (& $mismaFecha $enDisco.LastWriteTimeUtc.Ticks $manifiesto.FechaUtcTicks))) {
        $manifiesto = $null          # el disco no es lo que el manifiesto dice
    }
    if ($manifiesto -and -not $enDisco) { $manifiesto = $null }   # lo borraron del disco

    if (-not $manifiesto -and $enDisco) {
        # Reanudar o readoptar: leer lo que el disco tiene de verdad.
        if ($Simular) {
            return (& $salida 'Leeria' 0 'esta en el disco sin manifiesto: lo leeria en local para saber que tiene')
        }
        if ($Archivo.Vm) {
            Write-ProgresoDelLaboratorio -Vm $Archivo.Vm -Archivo (Split-Path $rel -Leaf) -Fase 'hasheando' `
                -Hechos 0 -Totales $enDisco.Length -MbPorSegundo 0 -Carpeta $Carpeta -Confirm:$false
        }
        $leido = Get-HuellasDeIndices -Ruta $enFrio -Bloque $Ficha.Bloque
        $huellasDisco = New-Object System.Collections.Generic.List[string]
        $nBloques = [int][Math]::Ceiling($leido.Tamano / [double]$Ficha.Bloque)
        for ($i = 0; $i -lt $nBloques; $i++) { $huellasDisco.Add('' + $leido.Mapa[$i]) }
        $manifiesto = [pscustomobject]@{
            Bloque = $Ficha.Bloque; Tamano = $leido.Tamano
            FechaUtcTicks = $enDisco.LastWriteTimeUtc.Ticks; Huellas = $huellasDisco.ToArray()
        }
        $adoptado = $true
    }

    if ($manifiesto -and -not $adoptado -and $Archivo.Tamano -eq $manifiesto.Tamano -and (& $mismaFecha $Archivo.FechaUtc.Ticks $manifiesto.FechaUtcTicks)) {
        return (& $salida 'AlDia' 0 '')
    }

    # Archivo nuevo en el disco y solo se mide: va entero, no hace falta hashear.
    if (-not $manifiesto -and $Simular) { return (& $salida 'Enviaria' $Archivo.Tamano 'nuevo en el disco') }
    $actual = Get-HuellasDeOrigen -Ficha $Ficha -Archivo $Archivo -Carpeta $Carpeta
    if ($manifiesto) {
        $indices = Get-IndicesCambiados -Actual $actual -Previo $manifiesto
        # Un '-' o un hueco en las huellas leidas del disco es "no se pudo leer": se reenvia.
        if ($adoptado) {
            $extra = New-Object System.Collections.Generic.List[int]
            for ($i = 0; $i -lt @($manifiesto.Huellas).Count; $i++) { if (-not $manifiesto.Huellas[$i] -or $manifiesto.Huellas[$i] -eq '-') { $extra.Add($i) } }
            $indices = [int[]]@(@($indices) + @($extra) | Sort-Object -Unique)
        }
    }
    else {
        $indices = [int[]]@()
        if ($actual.Huellas.Count -gt 0) { $indices = [int[]](0..($actual.Huellas.Count - 1)) }
    }
    $bytes = Get-BytesDeIndices -Indices $indices -Bloque $Ficha.Bloque -Tamano $actual.Tamano
    if ($Simular) {
        $motivo = ''
        if (-not $enDisco) { $motivo = 'nuevo en el disco' }
        return (& $salida 'Enviaria' $bytes $motivo)
    }

    [void](Write-BloquesEnSuSitio -Origen $Archivo.Ruta -Destino $enFrio -Bloque $Ficha.Bloque -Indices $indices `
            -Tamano $actual.Tamano -FechaUtc $Archivo.FechaUtc -Vm $Archivo.Vm -CarpetaEstado $Carpeta -Confirm:$false)

    if ($Archivo.Vm) {
        Write-ProgresoDelLaboratorio -Vm $Archivo.Vm -Archivo (Split-Path $rel -Leaf) -Fase 'verificando' `
            -Hechos $bytes -Totales $bytes -MbPorSegundo 0 -Carpeta $Carpeta -Confirm:$false
    }
    $leidas = Get-HuellasDeIndices -Ruta $enFrio -Bloque $Ficha.Bloque -Indices $indices
    $cotejo = Compare-HuellasLeidas -Esperadas $actual.Huellas -Indices $indices -Leidas $leidas.Mapa `
        -Tamano $actual.Tamano -TamanoLeido $leidas.Tamano
    if (-not $cotejo.Correcto) {
        # Sin manifiesto: la proxima vez se relee el disco y se reanuda desde lo que haya.
        if (Test-Path -LiteralPath $rutaManifiesto) { Remove-Item -LiteralPath $rutaManifiesto -Force }
        Write-RegistroRespaldo -Nivel 'ERROR' -Etapa 'laboratorio-frio' -Mensaje ('{0}: {1} de {2} bloques no cuadran en el disco frio.' -f $rel, $cotejo.Malos, @($indices).Count)
        return (& $salida 'Falla' $bytes ('{0} bloques no cuadran en el disco frio' -f $cotejo.Malos))
    }
    Write-ManifiestoDeBloques -Ruta $rutaManifiesto -Huellas $actual -FechaUtc $Archivo.FechaUtc -Confirm:$false
    Write-RegistroRespaldo -Etapa 'laboratorio-frio' -Mensaje ('{0}: {1} bloques, {2}, confirmados en el disco frio.' -f $rel, @($indices).Count, (Format-BytesDelLaboratorio -Bytes $bytes))
    return (& $salida 'Enviado' $bytes '')
}

function Invoke-GuardadoFrioDelLaboratorio {
    <#
        .SYNOPSIS
            El laboratorio de D: al disco frio: guardas, pequenos, grandes por bloques.
        .DESCRIPTION
            Siempre aditivo. Lo llama disco.ps1 como tercera pasada (dentro de
            su marca y sus guardas del disco y los centinelas) o, a solo,
            laboratorio.ps1 -Accion GuardarFrio.
        .PARAMETER Configuracion
            El objeto de configuracion completo.
        .PARAMETER Disco
            Lo que devuelve Get-DiscoFrio. Si falta, se busca.
        .PARAMETER Simular
            Mide lo que enviaria cada VM y no escribe nada.
    #>
    [CmdletBinding(SupportsShouldProcess, ConfirmImpact = 'High')]
    [OutputType([psobject])]
    param(
        [Parameter(Mandatory)][psobject] $Configuracion,
        [AllowNull()][psobject] $Disco = $null,
        [switch] $Simular
    )

    $res = [ordered]@{
        Inicio = Get-Date; Simulada = [bool]$Simular; Abortada = $false; Omitida = $false; Motivo = $null
        PorVm = @(); Archivos = @(); Pequenos = $null; Enviado = [long]0; Estado = ''; Correcto = $false; Fin = $null
    }
    $abortar = {
        param($Motivo, [bool]$Omitir)
        $res.Abortada = (-not $Omitir)
        $res.Omitida = $Omitir
        $res.Motivo = $Motivo
        $res.Estado = if ($Omitir) { 'Atencion' } else { 'Falla' }
        $res.Fin = Get-Date
        Write-RegistroRespaldo -Nivel 'ATENCION' -Etapa 'laboratorio-frio' -Mensaje $Motivo
        [pscustomobject]$res
    }

    $ficha = Get-FichaDelLaboratorio -Configuracion $Configuracion
    if (-not $ficha -or -not $ficha.Frio.Habilitado) {
        $res.Omitida = $true; $res.Correcto = $true; $res.Estado = 'Protegido'; $res.Motivo = 'El laboratorio no esta declarado para el disco frio.'
        $res.Fin = Get-Date
        return [pscustomobject]$res
    }
    if (-not (Test-Path -LiteralPath $ficha.Origen -PathType Container)) { return (& $abortar "No existe el laboratorio en $($ficha.Origen)." $false) }
    if (-not $Disco) { $Disco = Get-DiscoFrio -Configuracion $Configuracion }
    if (-not $Disco) { return (& $abortar 'El disco frio no esta conectado o no paso firma y centinela: el laboratorio no se copio.' $false) }

    # LAS VMS ENCENDIDAS NO SON UN FALLO: el laboratorio se difiere y se dice.
    # Copiar un .vmdk que cambia bajo los pies daria una imagen inconsistente.
    $quieto = Test-LaboratorioQuieto -Ficha $ficha
    if (-not $quieto.Quieto) { return (& $abortar ('Laboratorio NO copiado al disco frio: ' + $quieto.Motivo) $true) }

    $carpeta = Get-CarpetaDeEstado -Configuracion $Configuracion
    $frio = Get-RaizFriaDelLaboratorio -Ficha $ficha -Disco $Disco
    $inventario = @(Get-InventarioDelLaboratorio -Ficha $ficha)
    if ($inventario.Count -eq 0) { return (& $abortar "El laboratorio en $($ficha.Origen) esta vacio: no hay nada que copiar." $false) }
    $grandes = @($inventario | Where-Object { $_.Grande } | Sort-Object -Property Relativa)
    $pequenos = @($inventario | Where-Object { -not $_.Grande })

    # --- Renombres: mover en el disco lo que ya esta, en vez de copiarlo otra vez ---
    $vigentes = @{}
    foreach ($a in $inventario) { $vigentes[$a.Relativa] = $a.Grande }
    $renombres = @{}
    foreach ($g in $grandes) {
        if (Test-Path -LiteralPath (Get-RutaDelManifiesto -Carpeta $carpeta -Relativa $g.Relativa -Juego $frio.Juego) -PathType Leaf) { continue }
        if (Test-Path -LiteralPath (Join-Path $frio.Raiz $g.Relativa)) { continue }
        $hallado = Find-ManifiestoRenombrado -Ficha $ficha -Archivo $g -Carpeta $carpeta -Vigentes $vigentes -Juego $frio.Juego -RaizDestino $frio.Raiz
        if ($hallado) { $renombres[$g.Relativa] = $hallado }
    }

    # --- Espacio -------------------------------------------------------------
    $necesario = Get-SumaDe -Objetos $pequenos -Propiedad 'Tamano'
    foreach ($g in $grandes) {
        if ($renombres.ContainsKey($g.Relativa)) { continue }
        $m = Read-ManifiestoDeBloques -Ruta (Get-RutaDelManifiesto -Carpeta $carpeta -Relativa $g.Relativa -Juego $frio.Juego)
        if ($m) { $necesario += [Math]::Max([long]0, $g.Tamano - $m.Tamano) }
        elseif (-not (Test-Path -LiteralPath (Join-Path $frio.Raiz $g.Relativa))) { $necesario += $g.Tamano }
    }
    $libre = Get-EspacioLibre -Ruta $Disco.Raiz
    if ($null -ne $libre -and $necesario -gt ($libre - $script:MargenEnNodo)) {
        return (& $abortar ('No cabe en el disco frio: hacen falta hasta {0} y hay {1} libres (margen 20 GB).' -f
                (Format-BytesDelLaboratorio -Bytes $necesario), (Format-BytesDelLaboratorio -Bytes $libre)) $false)
    }

    if (-not $Simular -and -not $PSCmdlet.ShouldProcess($frio.Raiz, 'Copiar el laboratorio al disco frio')) { return (& $abortar 'Cancelado.' $true) }

    $archivos = New-Object System.Collections.Generic.List[psobject]
    try {
        if (-not $Simular) {
            foreach ($relNueva in @($renombres.Keys)) {
                $ren = $renombres[$relNueva]
                $destNuevo = Join-Path $frio.Raiz $relNueva
                $rutaManifNuevo = Get-RutaDelManifiesto -Carpeta $carpeta -Relativa $relNueva -Juego $frio.Juego
                try {
                    $dirDest = Split-Path -Path $destNuevo -Parent
                    if ($dirDest -and -not (Test-Path -LiteralPath $dirDest)) { New-Item -ItemType Directory -Path $dirDest -Force | Out-Null }
                    Move-Item -LiteralPath $ren.DestinoAnterior -Destination $destNuevo -ErrorAction Stop
                    $dirManif = Split-Path -Path $rutaManifNuevo -Parent
                    if ($dirManif -and -not (Test-Path -LiteralPath $dirManif)) { New-Item -ItemType Directory -Path $dirManif -Force | Out-Null }
                    Move-Item -LiteralPath $ren.ManifiestoAnterior -Destination $rutaManifNuevo -Force -ErrorAction Stop
                    Write-RegistroRespaldo -Nivel 'ATENCION' -Etapa 'laboratorio-frio' `
                        -Mensaje ('RENOMBRE RECONOCIDO en el disco frio (sin recopiar): {0} -> {1}' -f $ren.RelativaAnterior, $relNueva)
                }
                catch {
                    Write-RegistroRespaldo -Nivel 'ATENCION' -Etapa 'laboratorio-frio' `
                        -Mensaje ('No se pudo migrar el renombre {0} -> {1}: {2}' -f $ren.RelativaAnterior, $relNueva, $_.Exception.Message)
                }
            }
        }

        # --- Lo pequeno, con robocopy aditivo y SIN los grandes ---------------
        $hojasPequenos = New-Object System.Collections.Generic.HashSet[string]([System.StringComparer]::OrdinalIgnoreCase)
        foreach ($pq in $pequenos) { [void]$hojasPequenos.Add((Split-Path -Path $pq.Relativa -Leaf)) }
        $exclusiones = New-Object System.Collections.Generic.List[string]
        foreach ($g in $grandes) {
            $hojaG = Split-Path -Path $g.Relativa -Leaf
            if ($hojasPequenos.Contains($hojaG)) { [void]$exclusiones.Add($g.Ruta); [void]$exclusiones.Add((Join-Path $frio.Raiz $g.Relativa)) }
            else { [void]$exclusiones.Add($hojaG) }
        }
        $res.Pequenos = Invoke-Robocopy -Origen $ficha.Origen -Destino $frio.Raiz -Clase 'A' -UnSoloHilo `
            -SoloListar:$Simular -ExcluirArchivos (@($ficha.ExcluirArchivos) + @($exclusiones | Sort-Object -Unique)) -ExcluirCarpetas @($ficha.ExcluirCarpetas)

        # --- Los grandes, por bloques ------------------------------------------
        foreach ($g in $grandes) {
            try { $hecho = Invoke-ArchivoGrandeFrio -Ficha $ficha -Archivo $g -Carpeta $carpeta -Frio $frio -Simular:$Simular -Confirm:$false }
            catch {
                Write-RegistroRespaldo -Nivel 'ERROR' -Etapa 'laboratorio-frio' -Mensaje ('{0}: {1}' -f $g.Relativa, $_.Exception.Message)
                $hecho = [pscustomobject]@{ Relativa = $g.Relativa; Vm = $g.Vm; Estado = 'Falla'; Enviado = [long]0; Motivo = $_.Exception.Message }
            }
            $archivos.Add($hecho)
        }
    }
    finally {
        if (-not $Simular) { Remove-ProgresoDelLaboratorio -Carpeta $carpeta -Confirm:$false }
    }

    $res.Archivos = $archivos.ToArray()
    $res.Enviado = Get-SumaDe -Objetos $res.Archivos -Propiedad 'Enviado'
    $porVm = New-Object System.Collections.Generic.List[psobject]
    foreach ($v in @(Get-ResumenDeVmsDelLaboratorio -Inventario $inventario)) {
        $suyos = @($archivos | Where-Object { $_.Vm -eq $v.Vm })
        $estadoVm = 'AlDia'
        if (@($suyos | Where-Object { $_.Estado -eq 'Falla' }).Count -gt 0) { $estadoVm = 'Falla' }
        $porVm.Add([pscustomobject]@{ Vm = $v.Vm; Bytes = $v.Bytes; Estado = $estadoVm; Enviado = (Get-SumaDe -Objetos $suyos -Propiedad 'Enviado') })
    }
    $res.PorVm = $porVm.ToArray()

    $fallan = @($porVm | Where-Object { $_.Estado -eq 'Falla' })
    $partes = New-Object System.Collections.Generic.List[string]
    if (-not $res.Pequenos.Correcto) { $partes.Add('robocopy no dio por buenos los archivos pequenos') }
    if ($fallan.Count -gt 0) { $partes.Add(('{0} VMs fallaron ({1})' -f $fallan.Count, (($fallan | ForEach-Object { $_.Vm }) -join ', '))) }
    if ($partes.Count -gt 0) { $res.Estado = 'Falla'; $res.Motivo = 'LABORATORIO CON FALLOS en el disco frio: ' + ($partes -join '; ') }
    else {
        $res.Estado = 'Protegido'; $res.Correcto = $true
        $res.Motivo = 'laboratorio en el disco frio: {0} enviados, el resto ya estaba' -f (Format-BytesDelLaboratorio -Bytes $res.Enviado)
    }
    if (-not $Simular) {
        $nivel = if ($res.Correcto) { 'OK' } else { 'ATENCION' }
        Write-RegistroRespaldo -Nivel $nivel -Etapa 'laboratorio-frio' -Mensaje $res.Motivo
    }
    $res.Fin = Get-Date
    return [pscustomobject]$res
}

# ---------------------------------------------------------------------------
#  Validar: esta cada VM en los tres sitios? Solo lee, sin releer nada pesado
# ---------------------------------------------------------------------------

function Get-ValidacionDelLaboratorio {
    <#
        .SYNOPSIS
            Compara D:, el nodo y el disco frio, archivo por archivo, sin leer contenido.
        .DESCRIPTION
            COSTE: tres listados (D: y el disco, locales; el nodo, UN listado por
            SMB: ~700 archivos, un segundo) y la lectura de los manifiestos, que
            son archivos locales pequenos. No lee ni un bloque de ningun
            .vmdk, no escribe nada y no usa la Pi.

            QUE AFIRMA CADA CELDA, y hasta donde llega:
              OK        mismo tamano y misma fecha que en D:. Para un disco
                        grande, ademas hay manifiesto cuyo tamano y fecha
                        coinciden: lo que se confirmo bloque a bloque en el
                        ultimo guardado sigue siendo lo que hay.
              OK-fecha  (solo discos grandes) igual en tamano y fecha pero SIN
                        manifiesto que lo respalde: se parece, no se probo.
              DISTINTO  esta, pero no es la version de D:.
              FALTA     no esta.
              ?         no se pudo mirar (nodo sin red, disco desconectado).
            Lo que NO prueba: que los bits no se hayan degradado en reposo. Eso
            es -Profundo (disco frio: relee y coteja contra el manifiesto) y [T]
            (nodo: lo relee la Pi). Ningun metodo barato lo prueba.
        .PARAMETER Configuracion
            El objeto de configuracion completo.
        .PARAMETER Profundo
            Relee del disco frio cada disco grande y lo coteja con su manifiesto.
            Lectura local de cientos de GB: decenas de minutos.
    #>
    [CmdletBinding()]
    [OutputType([psobject])]
    param(
        [Parameter(Mandatory)][psobject] $Configuracion,
        [switch] $Profundo
    )

    $ficha = Get-FichaDelLaboratorio -Configuracion $Configuracion
    if (-not $ficha) { return $null }
    $carpeta = Get-CarpetaDeEstado -Configuracion $Configuracion
    $inventario = @(Get-InventarioDelLaboratorio -Ficha $ficha)

    $listar = {
        param($Raiz)
        $mapa = @{}
        try {
            if (Test-Path -LiteralPath $Raiz -PathType Container) {
                foreach ($a in @(Get-InventarioDelLaboratorio -Ficha $ficha -Raiz $Raiz)) { $mapa[$a.Relativa] = $a }
                return $mapa
            }
        }
        catch { Write-Verbose "No se pudo listar ${Raiz}: $($_.Exception.Message)" }
        return $null
    }
    $enNodo = & $listar $ficha.Destino

    $disco = $null
    $enFrio = $null
    $frio = $null
    if ($ficha.Frio.Habilitado) {
        $disco = Get-DiscoFrio -Configuracion $Configuracion 2>$null
        if ($disco) {
            $frio = Get-RaizFriaDelLaboratorio -Ficha $ficha -Disco $disco
            $enFrio = & $listar $frio.Raiz
            if ($null -eq $enFrio) { $enFrio = @{} }   # el disco esta, el laboratorio todavia no
        }
    }

    $igual = {
        param($A, $B) ($A.Tamano -eq $B.Tamano -and [Math]::Abs($A.FechaUtc.Ticks - $B.FechaUtc.Ticks) -le $script:Tolerancia)
    }
    $celda = {
        param($Mapa, $Juego, $Archivo)
        if ($null -eq $Mapa) { return '?' }
        $otro = $Mapa[$Archivo.Relativa]
        if (-not $otro) { return 'FALTA' }
        if (-not (& $igual $Archivo $otro)) { return 'DISTINTO' }
        if (-not $Archivo.Grande) { return 'OK' }
        $m = Read-ManifiestoDeBloques -Ruta (Get-RutaDelManifiesto -Carpeta $carpeta -Relativa $Archivo.Relativa -Juego $Juego)
        if ($m -and $m.Tamano -eq $otro.Tamano -and [Math]::Abs($m.FechaUtcTicks - $otro.FechaUtc.Ticks) -le $script:Tolerancia) { return 'OK' }
        return 'OK-fecha'
    }

    $filas = New-Object System.Collections.Generic.List[psobject]
    foreach ($a in $inventario) {
        $cNodo = & $celda $enNodo 'bloques' $a
        $cFrio = 'n/a'
        if ($ficha.Frio.Habilitado) { $cFrio = if ($null -eq $enFrio) { '?' } else { & $celda $enFrio $frio.Juego $a } }
        if ($Profundo -and $a.Grande -and $cFrio -eq 'OK') {
            $m = Read-ManifiestoDeBloques -Ruta (Get-RutaDelManifiesto -Carpeta $carpeta -Relativa $a.Relativa -Juego $frio.Juego)
            $leido = Get-HuellasDeIndices -Ruta (Join-Path $frio.Raiz $a.Relativa) -Bloque $ficha.Bloque
            $malos = 0
            for ($i = 0; $i -lt @($m.Huellas).Count; $i++) { if (-not $leido.Mapa.ContainsKey($i) -or $leido.Mapa[$i] -ne $m.Huellas[$i]) { $malos++ } }
            if ($malos -gt 0 -or $leido.Tamano -ne $m.Tamano) { $cFrio = 'DEGRADADO' }
        }
        $filas.Add([pscustomobject]@{ Relativa = $a.Relativa; Vm = $a.Vm; Grande = $a.Grande; Bytes = $a.Tamano; Nodo = $cNodo; Frio = $cFrio })
    }

    $peor = {
        param($Grupo, $Prop)
        $v = @($Grupo | ForEach-Object { $_.$Prop })
        foreach ($c in @('DEGRADADO', 'DISTINTO', 'FALTA', '?', 'OK-fecha')) { if ($v -contains $c) { return $c } }
        if ($v -contains 'n/a') { return 'n/a' }
        return 'OK'
    }
    $porVm = New-Object System.Collections.Generic.List[psobject]
    foreach ($g in @($filas | Group-Object -Property Vm | Sort-Object -Property Name)) {
        $porVm.Add([pscustomobject]@{
                Vm = $(if ($g.Name) { $g.Name } else { '(raiz)' }); Archivos = $g.Count
                Bytes = (Get-SumaDe -Objetos $g.Group -Propiedad 'Bytes')
                Nodo = (& $peor $g.Group 'Nodo'); Frio = (& $peor $g.Group 'Frio')
            })
    }
    $enTres = @($porVm | Where-Object { $_.Nodo -eq 'OK' -and ($_.Frio -eq 'OK' -or $_.Frio -eq 'n/a') }).Count
    return [pscustomobject]@{
        PorVm = $porVm.ToArray(); Archivos = $filas.ToArray()
        NodoLeido = ($null -ne $enNodo); DiscoConectado = [bool]$disco; Profundo = [bool]$Profundo
        VmsEnTres = $enTres; VmsTotal = $porVm.Count
        BytesTotal = (Get-SumaDe -Objetos $filas.ToArray() -Propiedad 'Bytes')
    }
}

function Write-ValidacionDelLaboratorio {
    <#
        .SYNOPSIS
            Pinta lo que devuelve Get-ValidacionDelLaboratorio.
        .PARAMETER Validacion
            El resultado.
    #>
    [CmdletBinding()]
    [OutputType([void])]
    param([Parameter(Mandatory)][psobject] $Validacion)

    Write-Host ('{0,-24} {1,6} {2,10}  {3,-10} {4,-10}' -f 'VM', 'ARCH.', 'TAMANO', 'NODO', 'DISCO FRIO')
    foreach ($v in $Validacion.PorVm) {
        Write-Host ('{0,-24} {1,6} {2,10}  {3,-10} {4,-10}' -f $v.Vm, $v.Archivos, (Format-BytesDelLaboratorio -Bytes $v.Bytes), $v.Nodo, $v.Frio)
    }
    foreach ($f in @($Validacion.Archivos | Where-Object { $_.Nodo -notin @('OK', 'n/a') -or $_.Frio -notin @('OK', 'n/a') } | Select-Object -First 40)) {
        Write-Host ('   {0}: nodo {1}, disco frio {2}' -f $f.Relativa, $f.Nodo, $f.Frio)
    }
    if (-not $Validacion.NodoLeido) { Write-Warning 'El nodo no respondio: su columna sale con ?.' }
    if (-not $Validacion.DiscoConectado) { Write-Warning 'El disco frio no esta conectado (o no paso firma y centinela): su columna sale con ?.' }
    Write-Host ('{0} de {1} carpetas confirmadas en el nodo Y en el disco frio.' -f $Validacion.VmsEnTres, $Validacion.VmsTotal)
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

function Get-HuellaDeBloqueUnico {
    <#
        .SYNOPSIS
            SHA-256 de un solo bloque de un archivo (sin leer el archivo entero).
    #>
    [CmdletBinding()]
    [OutputType([string])]
    param(
        [Parameter(Mandatory)][ValidateNotNullOrEmpty()][string] $Ruta,
        [Parameter(Mandatory)][int] $Bloque,
        [Parameter(Mandatory)][int] $Indice
    )

    $sha = New-Object System.Security.Cryptography.SHA256Cng
    $fs = New-Object System.IO.FileStream($Ruta, [System.IO.FileMode]::Open, [System.IO.FileAccess]::Read, [System.IO.FileShare]::ReadWrite)
    try {
        $off = [long]$Indice * $Bloque
        if ($off -ge $fs.Length) { return '' }
        $largo = [int][Math]::Min([long]$Bloque, $fs.Length - $off)
        $buf = New-Object byte[] $largo
        $fs.Position = $off
        $leido = 0
        while ($leido -lt $largo) {
            $r = $fs.Read($buf, $leido, $largo - $leido)
            if ($r -le 0) { break }
            $leido += $r
        }
        return ([System.BitConverter]::ToString($sha.ComputeHash($buf, 0, $leido))).Replace('-', '').ToLowerInvariant()
    }
    finally {
        $fs.Dispose()
        $sha.Dispose()
    }
}

function Find-ManifiestoRenombrado {
    <#
        .SYNOPSIS
            Detecta si un disco grande sin manifiesto es una VM/.vmdk renombrada.
        .DESCRIPTION
            Compara contra manifiestos .blk huerfanos (cuya ruta vieja ya no
            existe en D:) exigiendo:
              1. Mismo tamano de bloque y mismo tamano de archivo.
              2. El archivo viejo en el nodo sigue coincidiendo con su manifiesto.
              3. Al menos 2 huellas distintas en el manifiesto y coincidencia
                 exacta de 3 bloques de muestra (primero, medio, ultimo distintos)
                 en <5 ms sobre NVMe.
              4. Coincidencia UNICA (si hay ambiguedad, no migra).
    #>
    [CmdletBinding()]
    [OutputType([psobject])]
    param(
        [Parameter(Mandatory)][psobject] $Ficha,
        [Parameter(Mandatory)][psobject] $Archivo,
        [Parameter(Mandatory)][string] $Carpeta,
        [Parameter(Mandatory)][hashtable] $Vigentes,
        [ValidateNotNullOrEmpty()][string] $Juego = 'bloques',
        [AllowEmptyString()][string] $RaizDestino = ''
    )

    if (-not $RaizDestino) { $RaizDestino = $Ficha.Destino }
    $raizManifiestos = Join-Path $Carpeta ('laboratorio\' + $Juego)
    if (-not (Test-Path -LiteralPath $raizManifiestos -PathType Container)) { return $null }

    $candidatos = New-Object System.Collections.Generic.List[psobject]
    foreach ($blk in @(Get-ChildItem -LiteralPath $raizManifiestos -Recurse -File -Filter '*.blk' -ErrorAction SilentlyContinue)) {
        $relVieja = $blk.FullName.Substring($raizManifiestos.Length + 1)
        $relVieja = $relVieja.Substring(0, $relVieja.Length - 4)
        if ($Vigentes.ContainsKey($relVieja)) { continue }

        $m = Read-ManifiestoDeBloques -Ruta $blk.FullName
        if (-not $m -or $m.Bloque -ne $Ficha.Bloque -or $m.Tamano -ne $Archivo.Tamano) { continue }
        $nh = @($m.Huellas).Count
        if ($nh -eq 0) { continue }

        # El destino viejo en el nodo debe existir y cuadrar con su manifiesto.
        $nodoViejo = Join-Path $RaizDestino $relVieja
        $n = Get-Item -LiteralPath $nodoViejo -Force -ErrorAction SilentlyContinue
        if (-not $n -or $n.Length -ne $m.Tamano -or
            [Math]::Abs($n.LastWriteTimeUtc.Ticks - $m.FechaUtcTicks) -gt $script:Tolerancia) { continue }

        # Elegir hasta 3 indices de muestra con huellas distintas (evita falsos
        # positivos entre discos recien creados llenos de ceros).
        $muestraIdx = New-Object System.Collections.Generic.List[int]
        $vistas = New-Object System.Collections.Generic.HashSet[string]
        $ordenPrueba = @(0, [int]($nh / 2), ($nh - 1)) | Select-Object -Unique
        foreach ($idx in $ordenPrueba) {
            if ($vistas.Add($m.Huellas[$idx])) { $muestraIdx.Add($idx) }
        }
        if ($muestraIdx.Count -lt 2 -and $nh -ge 2) {
            for ($idx = 0; $idx -lt $nh -and $muestraIdx.Count -lt 3; $idx++) {
                if ($vistas.Add($m.Huellas[$idx])) { $muestraIdx.Add($idx) }
            }
        }
        if ($nh -ge 2 -and $muestraIdx.Count -lt 2) { continue }

        $coincide = $true
        foreach ($idx in $muestraIdx) {
            $hLocal = Get-HuellaDeBloqueUnico -Ruta $Archivo.Ruta -Bloque $Ficha.Bloque -Indice $idx
            if ($hLocal -ne $m.Huellas[$idx]) { $coincide = $false; break }
        }
        if ($coincide) {
            $candidatos.Add([pscustomobject]@{
                RelativaAnterior   = $relVieja
                ManifiestoAnterior = $blk.FullName
                DestinoAnterior    = $nodoViejo
                Manifiesto         = $m
            })
        }
    }

    if ($candidatos.Count -ne 1) { return $null }
    return $candidatos[0]
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
        [switch] $Simular,
        [AllowNull()][psobject] $RenombreSimulado = $null
    )

    $rel = $Archivo.Relativa
    $enNodo = Join-Path $Ficha.Destino $rel
    $rutaManifiesto = Get-RutaDelManifiesto -Carpeta $Carpeta -Relativa $rel
    $manifiesto = Read-ManifiestoDeBloques -Ruta $rutaManifiesto
    $salida = {
        param($Estado, $Enviado, $Motivo)
        [pscustomobject]@{ Relativa = $rel; Vm = $Archivo.Vm; Estado = $Estado; Enviado = [long]$Enviado; Motivo = $Motivo }
    }

    if (-not $manifiesto -and $Simular -and $RenombreSimulado) {
        $manifiesto = $RenombreSimulado.Manifiesto
        if ($Archivo.Tamano -eq $manifiesto.Tamano -and
            [Math]::Abs($Archivo.FechaUtc.Ticks - $manifiesto.FechaUtcTicks) -le $script:Tolerancia) {
            return (& $salida 'AlDia' 0 ('migraria desde {0} sin re-subir' -f $RenombreSimulado.RelativaAnterior))
        }
        $actual = Get-HuellasDeOrigen -Ficha $Ficha -Archivo $Archivo -Carpeta $Carpeta
        $indices = Get-IndicesCambiados -Actual $actual -Previo $manifiesto
        $bytesSim = Get-BytesDeIndices -Indices $indices -Bloque $Ficha.Bloque -Tamano $actual.Tamano
        return (& $salida 'Enviaria' $bytesSim ('migraria desde {0}' -f $RenombreSimulado.RelativaAnterior))
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
        if (-not $Simular -and $Archivo.Vm) {
            Write-ProgresoDelLaboratorio -Vm $Archivo.Vm -Archivo (Split-Path $rel -Leaf) -Fase 'hasheando' `
                -Hechos 0 -Totales $Archivo.Tamano -MbPorSegundo 0 -Carpeta $Carpeta -Confirm:$false
        }
        $actual = Get-HuellasDeOrigen -Ficha $Ficha -Archivo $Archivo -Carpeta $Carpeta
        $indices = Get-IndicesCambiados -Actual $actual -Previo $manifiesto
    }
    else {
        # SIN MANIFIESTO Y YA EN EL NODO: no se sabe que tiene el nodo, y
        # subirlo entero es justo lo que no se quiere. Se lee primero en la Pi.
        if (Test-Path -LiteralPath $enNodo) {
            return (& $salida 'Pendiente' 0 'esta en el nodo y no tiene manifiesto: pulsa [T]')
        }
        Write-Information ('   {0}: nuevo, va entero ({1})...' -f $rel, (Format-BytesDelLaboratorio -Bytes $Archivo.Tamano)) -InformationAction Continue
        if (-not $Simular -and $Archivo.Vm) {
            Write-ProgresoDelLaboratorio -Vm $Archivo.Vm -Archivo (Split-Path $rel -Leaf) -Fase 'hasheando' `
                -Hechos 0 -Totales $Archivo.Tamano -MbPorSegundo 0 -Carpeta $Carpeta -Confirm:$false
        }
        $actual = Get-HuellasDeOrigen -Ficha $Ficha -Archivo $Archivo -Carpeta $Carpeta
        $indices = [int[]]@()
        if ($actual.Huellas.Count -gt 0) { $indices = [int[]](0..($actual.Huellas.Count - 1)) }
    }

    $bytes = Get-BytesDeIndices -Indices $indices -Bloque $Ficha.Bloque -Tamano $actual.Tamano
    if ($Simular) { return (& $salida 'Enviaria' $bytes '') }

    [void](Write-BloquesEnSuSitio -Origen $Archivo.Ruta -Destino $enNodo -Bloque $Ficha.Bloque -Indices $indices `
            -Tamano $actual.Tamano -FechaUtc $Archivo.FechaUtc `
            -Vm $Archivo.Vm -CarpetaEstado $Carpeta -Confirm:$false)
    if ($Archivo.Vm) {
        Write-ProgresoDelLaboratorio -Vm $Archivo.Vm -Archivo (Split-Path $rel -Leaf) -Fase 'verificando' `
            -Hechos $bytes -Totales $bytes -MbPorSegundo 0 -Carpeta $Carpeta -Confirm:$false
    }
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

    $vigentesMap = @{}
    foreach ($a in $inventario) { $vigentesMap[$a.Relativa] = $a.Grande }

    # --- 0. Deteccion de renombres de VMs / .vmdk (antes de espacio y robocopy) --
    $renombresLab = @{}
    foreach ($g in $grandes) {
        $rutaM = Get-RutaDelManifiesto -Carpeta $carpeta -Relativa $g.Relativa
        if (Test-Path -LiteralPath $rutaM -PathType Leaf) { continue }
        if (Test-Path -LiteralPath (Join-Path $ficha.Destino $g.Relativa)) { continue }
        $hallado = Find-ManifiestoRenombrado -Ficha $ficha -Archivo $g -Carpeta $carpeta -Vigentes $vigentesMap
        if ($hallado) { $renombresLab[$g.Relativa] = $hallado }
    }

    # --- Espacio -------------------------------------------------------------
    $necesario = Get-SumaDe -Objetos $pequenos -Propiedad 'Tamano'
    foreach ($g in $grandes) {
        if ($renombresLab.ContainsKey($g.Relativa)) { continue }
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

        # --- 0b. Migrar renombres detectados ANTES de robocopy /MIR ----------
        # Si robocopy corriera antes cuando cambio el nombre del .vmdk, purgaria
        # el disco viejo del nodo antes de poder renombrarlo en su sitio.
        if (-not $Simular) {
            foreach ($relNueva in @($renombresLab.Keys)) {
                $ren = $renombresLab[$relNueva]
                $destNuevo = Join-Path $ficha.Destino $relNueva
                $rutaManifNuevo = Get-RutaDelManifiesto -Carpeta $carpeta -Relativa $relNueva
                try {
                    $dirDest = Split-Path -Path $destNuevo -Parent
                    if ($dirDest -and -not (Test-Path -LiteralPath $dirDest)) { New-Item -ItemType Directory -Path $dirDest -Force | Out-Null }
                    Move-Item -LiteralPath $ren.DestinoAnterior -Destination $destNuevo -ErrorAction Stop

                    $dirManif = Split-Path -Path $rutaManifNuevo -Parent
                    if ($dirManif -and -not (Test-Path -LiteralPath $dirManif)) { New-Item -ItemType Directory -Path $dirManif -Force | Out-Null }
                    Move-Item -LiteralPath $ren.ManifiestoAnterior -Destination $rutaManifNuevo -Force -ErrorAction Stop
                    Write-RegistroRespaldo -Nivel 'ATENCION' -Etapa 'laboratorio' `
                        -Mensaje ("RENOMBRE RECONOCIDO (sin re-subir): {0} -> {1}" -f $ren.RelativaAnterior, $relNueva)
                }
                catch {
                    Write-RegistroRespaldo -Nivel 'ATENCION' -Etapa 'laboratorio' `
                        -Mensaje ("No se pudo migrar renombre {0} -> {1}: {2}" -f $ren.RelativaAnterior, $relNueva, $_.Exception.Message)
                }
            }
        }

        # --- 1. Lo pequeno, con robocopy, SIN los grandes --------------------
        # Si robocopy viera un disco grande lo truncaria y lo copiaria entero:
        # la trampa medida con el v1. Si ningun archivo pequeno comparte nombre
        # base con un disco grande se excluye por hoja (vale en origen y destino);
        # si hubiera colision de nombre base, se excluyen rutas completas.
        $hojasPequenos = New-Object System.Collections.Generic.HashSet[string]([System.StringComparer]::OrdinalIgnoreCase)
        foreach ($pq in $pequenos) { [void]$hojasPequenos.Add((Split-Path -Path $pq.Relativa -Leaf)) }
        $exclusionesGrandes = New-Object System.Collections.Generic.List[string]
        foreach ($g in $grandes) {
            $hojaG = Split-Path -Path $g.Relativa -Leaf
            if ($hojasPequenos.Contains($hojaG)) {
                [void]$exclusionesGrandes.Add($g.Ruta)
                [void]$exclusionesGrandes.Add((Join-Path $ficha.Destino $g.Relativa))
            }
            else {
                [void]$exclusionesGrandes.Add($hojaG)
            }
        }
        $nombresGrandes = @($exclusionesGrandes | Sort-Object -Unique)
        Write-Information '   Archivos pequenos (configuracion de las VMs)...' -InformationAction Continue
        $res.Pequenos = Invoke-Robocopy -Origen $ficha.Origen -Destino $ficha.Destino -Clase 'B' -UnSoloHilo `
            -SoloListar:$Simular -ExcluirArchivos (@($ficha.ExcluirArchivos) + $nombresGrandes) -ExcluirCarpetas @($ficha.ExcluirCarpetas)

        # --- 2. Los grandes, por bloques (con checkpoint por VM) -------------
        $vmsResumen = @{}
        foreach ($vr in @(Get-ResumenDeVmsDelLaboratorio -Inventario $inventario)) { $vmsResumen[$vr.Vm] = $vr }

        foreach ($grupoVm in @($grandes | Group-Object -Property Vm | Sort-Object -Property Name)) {
            $archivosVm = New-Object System.Collections.Generic.List[psobject]
            foreach ($g in @($grupoVm.Group | Sort-Object -Property Relativa)) {
                $renSim = if ($Simular -and $renombresLab.ContainsKey($g.Relativa)) { $renombresLab[$g.Relativa] } else { $null }
                $intento = 0
                $maxInt = if (-not $Simular -and $ficha.Verificador -ne 'local') { 2 } else { 1 }
                $hechoG = $null
                while ($intento -lt $maxInt) {
                    $intento++
                    try {
                        $hechoG = Invoke-ArchivoGrande -Ficha $ficha -Archivo $g -Carpeta $carpeta -Simular:$Simular -RenombreSimulado $renSim -Confirm:$false
                        break
                    }
                    catch {
                        $esRed = ($_.Exception -is [System.IO.IOException])
                        if ($esRed -and $intento -lt $maxInt) {
                            Write-RegistroRespaldo -Nivel 'ATENCION' -Etapa 'laboratorio' `
                                -Mensaje ('{0}: fallo transitorio de red ({1}), reintentando en 5 s...' -f $g.Relativa, $_.Exception.Message)
                            Start-Sleep -Seconds 5
                            continue
                        }
                        Write-RegistroRespaldo -Nivel 'ERROR' -Etapa 'laboratorio' -Mensaje ('{0}: {1}' -f $g.Relativa, $_.Exception.Message)
                        $hechoG = [pscustomobject]@{ Relativa = $g.Relativa; Vm = $g.Vm; Estado = 'Falla'; Enviado = [long]0; Motivo = $_.Exception.Message }
                        break
                    }
                }
                $archivos.Add($hechoG)
                $archivosVm.Add($hechoG)
            }

            # Checkpoint incremental en LABORATORIO.tsv al confirmar cada VM,
            # para no perder horas de avance si una VM posterior se corta.
            if (-not $Simular -and $grupoVm.Name -and $vmsResumen.ContainsKey($grupoVm.Name)) {
                $suyosVm = $archivosVm.ToArray()
                $hayFallaVm = @($suyosVm | Where-Object { $_.Estado -eq 'Falla' }).Count -gt 0
                $hayPendVm  = @($suyosVm | Where-Object { $_.Estado -eq 'Pendiente' }).Count -gt 0
                if (-not $hayFallaVm -and -not $hayPendVm) {
                    Write-EstadoDelLaboratorioPorVm -Filas @([pscustomobject]@{
                            Vm       = $grupoVm.Name
                            Bytes    = $vmsResumen[$grupoVm.Name].Bytes
                            Guardado = $inicio.ToString('s')
                            Enviado  = (Get-SumaDe -Objetos $suyosVm -Propiedad 'Enviado')
                            Estado   = 'AlDia'
                            Detalle  = ''
                        }) -Carpeta $carpeta -Confirm:$false
                }
            }
        }

        # --- 3. Lo que ya no esta en D: --------------------------------------
        # Es un espejo, como el v1: un disco grande que se borro de D: se borra
        # del nodo. Solo si el nodo sigue siendo lo que dice su manifiesto.
        $raizManifiestos = Join-Path $carpeta 'laboratorio\bloques'
        if (Test-Path -LiteralPath $raizManifiestos) {
            $vigentes = $vigentesMap
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
        if (-not $Simular) {
            Remove-ProgresoDelLaboratorio -Carpeta $carpeta -Confirm:$false
            Exit-MarcaDeCorrida -Carpeta $carpeta -Archivo 'LABORATORIO.lock' -Confirm:$false
        }
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

function ConvertFrom-AvanceDeLectura {
    <#
        .SYNOPSIS
            Interpreta la linea AVANCE de blkhash.py lote (formato viejo 2 campos o nuevo 5 campos).
        .DESCRIPTION
            Funcion pura para pruebas y para la vista [T].
            Formato viejo:  "8 18"
            Formato nuevo:  "8 18 1207959552 9876543210 23.4"
        .PARAMETER Linea
            Texto leido de salida/AVANCE.
        .PARAMETER Lectura
            Objeto leido de lectura.json (opcional) para resolver nombre de archivo y VM actual.
    #>
    [CmdletBinding()]
    [OutputType([psobject])]
    param(
        [AllowEmptyString()][AllowNull()][string] $Linea = '',
        [AllowNull()][psobject] $Lectura = $null
    )

    $partes = @()
    if (-not [string]::IsNullOrWhiteSpace($Linea)) {
        $partes = @($Linea.Trim() -split '\s+')
    }
    $indice = 0
    $total = 0
    $valido = $false
    if ($partes.Count -ge 2) {
        [int] $iTmp = 0; [int] $tTmp = 0
        if ([int]::TryParse($partes[0], [ref]$iTmp) -and [int]::TryParse($partes[1], [ref]$tTmp)) {
            $indice = $iTmp
            $total = $tTmp
            $valido = $true
        }
    }
    if (-not $valido -and $Lectura -and ($Lectura.PSObject.Properties.Name -contains 'Archivos')) {
        $total = @($Lectura.Archivos).Count
    }

    $bytesLeidos = [long]-1
    $tamanoArchivo = [long]-1
    $porcentajeArchivo = -1
    $mbPorSegundo = ''
    $tieneIntra = $false
    if ($valido -and $partes.Count -ge 5) {
        [long] $bTmp = 0; [long] $sTmp = 0
        if ([long]::TryParse($partes[2], [ref]$bTmp) -and [long]::TryParse($partes[3], [ref]$sTmp) -and $sTmp -gt 0) {
            $bytesLeidos = $bTmp
            $tamanoArchivo = $sTmp
            $porcentajeArchivo = [int][Math]::Min(100, [Math]::Floor(100.0 * $bTmp / $sTmp))
            $mbPorSegundo = $partes[4]
            $tieneIntra = $true
        }
    }

    $archivoActual = ''
    $vmActual = ''
    if ($Lectura -and ($Lectura.PSObject.Properties.Name -contains 'Archivos') -and $null -ne $Lectura.Archivos) {
        $listaArch = @($Lectura.Archivos)
        $encontrado = $null
        foreach ($item in $listaArch) {
            if ($item -and ($item.PSObject.Properties.Name -contains 'I') -and ([int]$item.I -eq $indice)) {
                $encontrado = $item
                break
            }
        }
        if (-not $encontrado -and $indice -ge 0 -and $indice -lt $listaArch.Count) {
            $encontrado = $listaArch[$indice]
        }
        if ($encontrado -and ($encontrado.PSObject.Properties.Name -contains 'Relativa')) {
            $archivoActual = '' + $encontrado.Relativa
            if ($archivoActual.Contains('\')) { $vmActual = $archivoActual.Split('\')[0] }
        }
    }

    $enProceso = if ($total -gt 0) { [Math]::Min($total, $indice + 1) } else { 0 }
    return [pscustomobject]@{
        Valido            = $valido
        Indice            = $indice
        Completados       = [Math]::Min($indice, $total)
        EnProceso         = $enProceso
        Total             = $total
        ArchivoActual     = $archivoActual
        VmActual          = $vmActual
        BytesLeidos       = $bytesLeidos
        TamanoArchivo     = $tamanoArchivo
        PorcentajeArchivo = $porcentajeArchivo
        MbPorSegundo      = $mbPorSegundo
        TieneIntraArchivo = $tieneIntra
    }
}

function Get-PlanDeLecturaDelLaboratorio {
    <#
        .SYNOPSIS
            Analiza que discos grandes hay en el nodo y cuales estan pendientes de [T].
        .DESCRIPTION
            Solo lectura: no lanza nada en la Pi ni escribe lectura.json. Permite
            que la vista [T] ensene el tamano y tiempo estimado de leer solo las
            VMs pendientes frente a releer el laboratorio entero.
        .PARAMETER Configuracion
            El objeto de configuracion completo.
    #>
    [CmdletBinding()]
    [OutputType([psobject])]
    param([Parameter(Mandatory)][psobject] $Configuracion)

    $ficha = Get-FichaDelLaboratorio -Configuracion $Configuracion
    if (-not $ficha) {
        return [pscustomobject]@{
            TieneLaboratorio   = $false
            NodoVivo           = $false
            EnCurso            = $false
            Lectura            = $null
            TotalArchivos      = 0
            TotalBytes         = [long]0
            TotalMinutos       = 0
            PendientesArchivos = 0
            PendientesBytes    = [long]0
            PendientesMinutos  = 0
            PendientesVms      = @()
            ArchivosTodos      = @()
            ArchivosPendientes = @()
        }
    }

    $carpeta = Get-CarpetaDeEstado -Configuracion $Configuracion
    $lectura = Read-LecturaDelLaboratorio -Carpeta $carpeta
    $nodoVivo = [bool](Test-Path -LiteralPath $ficha.Destino -PathType Container)
    if (-not $nodoVivo) {
        return [pscustomobject]@{
            TieneLaboratorio   = $true
            NodoVivo           = $false
            EnCurso            = ($null -ne $lectura)
            Lectura            = $lectura
            TotalArchivos      = 0
            TotalBytes         = [long]0
            TotalMinutos       = 0
            PendientesArchivos = 0
            PendientesBytes    = [long]0
            PendientesMinutos  = 0
            PendientesVms      = @()
            ArchivosTodos      = @()
            ArchivosPendientes = @()
        }
    }

    $grandes = @(Get-InventarioDelLaboratorio -Ficha $ficha -Raiz $ficha.Destino | Where-Object { $_.Grande } | Sort-Object -Property Relativa)
    $porVm = Read-EstadoDelLaboratorioPorVm -Carpeta $carpeta
    $pendientes = New-Object System.Collections.Generic.List[psobject]
    foreach ($g in $grandes) {
        $rutaM = Get-RutaDelManifiesto -Carpeta $carpeta -Relativa $g.Relativa
        $m = Read-ManifiestoDeBloques -Ruta $rutaM
        $estadoVm = if ($g.Vm -and $porVm.ContainsKey($g.Vm)) { '' + $porVm[$g.Vm].Estado } else { '' }
        $necesita = (-not $m) -or
            ($m.Tamano -ne $g.Tamano) -or
            ([Math]::Abs($g.FechaUtc.Ticks - $m.FechaUtcTicks) -gt $script:Tolerancia) -or
            ($estadoVm -eq 'Pendiente')
        if ($necesita) { [void]$pendientes.Add($g) }
    }
    $pendArr = $pendientes.ToArray()
    $bytesTodos = Get-SumaDe -Objetos $grandes -Propiedad 'Tamano'
    $bytesPend  = Get-SumaDe -Objetos $pendArr -Propiedad 'Tamano'
    $minTodos = if ($bytesTodos -gt 0) { [Math]::Max(1, [int]($bytesTodos / 23MB / 60)) } else { 0 }
    $minPend  = if ($bytesPend -gt 0) { [Math]::Max(1, [int]($bytesPend / 23MB / 60)) } else { 0 }
    $vmsPend = @($pendArr | ForEach-Object { $_.Vm } | Where-Object { $_ } | Sort-Object -Unique)

    return [pscustomobject]@{
        TieneLaboratorio   = $true
        NodoVivo           = $true
        EnCurso            = ($null -ne $lectura)
        Lectura            = $lectura
        TotalArchivos      = $grandes.Count
        TotalBytes         = $bytesTodos
        TotalMinutos       = $minTodos
        PendientesArchivos = $pendArr.Count
        PendientesBytes    = $bytesPend
        PendientesMinutos  = $minPend
        PendientesVms      = $vmsPend
        ArchivosTodos      = $grandes
        ArchivosPendientes = $pendArr
    }
}

function Get-EstadoDeLecturaDelLaboratorio {
    <#
        .SYNOPSIS
            Consulta en vivo el avance de una lectura [T] en la Pi sin recogerla ni lanzarla.
        .PARAMETER Configuracion
            El objeto de configuracion completo.
    #>
    [CmdletBinding()]
    [OutputType([psobject])]
    param([Parameter(Mandatory)][psobject] $Configuracion)

    $ficha = Get-FichaDelLaboratorio -Configuracion $Configuracion
    if (-not $ficha) {
        return [pscustomobject]@{
            Estado = 'SinLaboratorio'; EnCurso = $false; Terminada = $false; ProcesoVivo = $false
            SshOk = $false; Lectura = $null; Avance = $null
            Detalle = 'La configuracion no declara el bloque laboratorio.'
        }
    }
    $carpeta = Get-CarpetaDeEstado -Configuracion $Configuracion
    $lectura = Read-LecturaDelLaboratorio -Carpeta $carpeta
    if (-not $lectura) {
        return [pscustomobject]@{
            Estado = 'Ninguna'; EnCurso = $false; Terminada = $false; ProcesoVivo = $false
            SshOk = $true; Lectura = $null; Avance = $null
            Detalle = 'No hay ninguna lectura en curso.'
        }
    }
    if ($ficha.Verificador -eq 'local') {
        return [pscustomobject]@{
            Estado = 'EnCurso'; EnCurso = $true; Terminada = $true; ProcesoVivo = $false
            SshOk = $true; Lectura = $lectura; Avance = (ConvertFrom-AvanceDeLectura -Linea '' -Lectura $lectura)
            Detalle = 'Modo local listo para recoger.'
        }
    }

    $dir = $script:LecturaEnLaPi
    $usuario = $ficha.UsuarioEnLaPi
    try {
        # Sin comillas dobles (PowerShell 5.1 las recorta al invocar ssh.exe) y
        # con `set -f` para impedir globbing sobre `[n]as-blkhash.py`.
        $orden = "sudo -n -u {0} sh -c 'set -f; printf AVANCE:; cat {1}/salida/AVANCE 2>/dev/null || echo; test -f {1}/salida/FIN && echo FIN:1 || echo FIN:0; ps -eo args 2>/dev/null | grep -q [n]as-blkhash.py && echo VIVO:1 || echo VIVO:0'" -f $usuario, $dir
        $lineas = @(Invoke-EnLaPi -Ficha $ficha -Orden $orden)
    }
    catch {
        return [pscustomobject]@{
            Estado = 'SinConexion'; EnCurso = $true; Terminada = $false; ProcesoVivo = $false
            SshOk = $false; Lectura = $lectura; Avance = (ConvertFrom-AvanceDeLectura -Linea '' -Lectura $lectura)
            Detalle = ('No se pudo consultar la Pi por ssh: {0}' -f $_.Exception.Message)
        }
    }

    $lineaAvance = ''
    $fin = $false
    $vivo = $false
    foreach ($l in $lineas) {
        $s = ('' + $l).Trim()
        if ($s.StartsWith('AVANCE:')) { $lineaAvance = $s.Substring(7).Trim() }
        elseif ($s -eq 'FIN:1') { $fin = $true }
        elseif ($s -eq 'VIVO:1') { $vivo = $true }
    }
    $av = ConvertFrom-AvanceDeLectura -Linea $lineaAvance -Lectura $lectura
    $estadoTexto = if ($fin) { 'Terminada' } elseif ($vivo) { 'EnCurso' } else { 'Detenida' }
    $det = if ($fin) {
        'La Pi termino de leer los {0} archivos. Lista para recoger manifiestos.' -f $av.Total
    }
    elseif ($vivo) {
        $parteArchivo = if ($av.ArchivoActual) { ' ({0})' -f $av.ArchivoActual } else { '' }
        $parteIntra = if ($av.TieneIntraArchivo) { ' - {0}% a {1} MB/s' -f $av.PorcentajeArchivo, $av.MbPorSegundo } else { '' }
        'Archivo {0} de {1}{2}{3}' -f $av.EnProceso, $av.Total, $parteArchivo, $parteIntra
    }
    else {
        'No se ve blkhash.py corriendo en la Pi y no hay FIN (posible reinicio o corte).'
    }

    return [pscustomobject]@{
        Estado      = $estadoTexto
        EnCurso     = $true
        Terminada   = $fin
        ProcesoVivo = $vivo
        SshOk       = $true
        Lectura     = $lectura
        Avance      = $av
        Detalle     = $det
    }
}

function Stop-LecturaDelLaboratorio {
    <#
        .SYNOPSIS
            Cancela una lectura [T] en curso en la Pi y retira lectura.json.
        .DESCRIPTION
            No modifica ningun archivo del respaldo ni manifiestos ya guardados;
            solo detiene el proceso de lectura blkhash.py lote en la Pi y limpia
            el trabajo temporal /tmp/nas-lab-lectura y lectura.json.
        .PARAMETER Configuracion
            El objeto de configuracion completo.
    #>
    [CmdletBinding(SupportsShouldProcess)]
    [OutputType([psobject])]
    param([Parameter(Mandatory)][psobject] $Configuracion)

    $ficha = Get-FichaDelLaboratorio -Configuracion $Configuracion
    if (-not $ficha) {
        return [pscustomobject]@{ Cancelada = $false; Detalle = 'La configuracion no declara el bloque laboratorio.' }
    }
    $carpeta = Get-CarpetaDeEstado -Configuracion $Configuracion
    $trabajo = Join-Path $carpeta 'laboratorio\lectura.json'
    if (-not (Test-Path -LiteralPath $trabajo -PathType Leaf)) {
        return [pscustomobject]@{ Cancelada = $false; Detalle = 'No habia ninguna lectura en curso que cancelar.' }
    }
    if (-not $PSCmdlet.ShouldProcess($trabajo, 'Cancelar la lectura del laboratorio en la Pi')) {
        return [pscustomobject]@{ Cancelada = $false; Detalle = 'Cancelacion abortada.' }
    }

    if ($ficha.Verificador -ne 'local') {
        $dir = $script:LecturaEnLaPi
        $usuario = $ficha.UsuarioEnLaPi
        try {
            $orden = "sudo -n -u {0} sh -c 'set -f; pkill -f [n]as-blkhash.py 2>/dev/null || true; rm -rf {1}'" -f $usuario, $dir
            [void](Invoke-EnLaPi -Ficha $ficha -Orden $orden)
        }
        catch {
            return [pscustomobject]@{
                Cancelada = $false
                Detalle   = ('No se pudo contactar la Pi por ssh para detener la lectura ({0}). lectura.json se conserva.' -f $_.Exception.Message)
            }
        }
    }

    Remove-Item -LiteralPath $trabajo -Force -ErrorAction SilentlyContinue
    Write-RegistroRespaldo -Nivel 'ATENCION' -Etapa 'laboratorio' -Mensaje 'Lectura [T] cancelada por el usuario: proceso detenido en la Pi y lectura.json retirado.'
    return [pscustomobject]@{
        Cancelada = $true
        Detalle   = 'Lectura cancelada en la Pi y trabajo temporal limpiado. Los manifiestos previos siguen intactos.'
    }
}

function Invoke-LecturaDelLaboratorio {
    <#
        .SYNOPSIS
            La lectura del laboratorio en la Pi: la lanza, dice como va o la recoge.
        .DESCRIPTION
            SON HORAS Y EL EQUIPO NO TIENE QUE ESPERAR. La Pi lee a 23 MB/s: unas
            3.5 h para los 281 GB del laboratorio. Se lanza en la Pi con setsid y
            se desentiende; cada vez que se pulsa [T] despues, dice por donde va,
            y cuando termino, recoge y convierte lo leido en manifiestos.

            Con -SoloPendientes lee unicamente los discos grandes cuyo manifiesto
            falta, no cuadra con el nodo o pertenece a una VM marcada Pendiente.

            Se puede hacer con las VMs encendidas: lee el NODO, no D:. Lo unico
            que no se puede es guardar mientras tanto, y Invoke-Guardado... se
            niega mientras haya una lectura sin recoger.
        .PARAMETER Configuracion
            El objeto de configuracion completo.
        .PARAMETER SoloPendientes
            Si se indica al lanzar, lee solo las VMs/archivos pendientes en vez del
            laboratorio entero.
    #>
    [CmdletBinding(SupportsShouldProcess)]
    [OutputType([psobject])]
    param(
        [Parameter(Mandatory)][psobject] $Configuracion,
        [switch] $SoloPendientes
    )

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
        $planLocal = Get-PlanDeLecturaDelLaboratorio -Configuracion $Configuracion
        if (-not $planLocal.NodoVivo) {
            return [pscustomobject]@{ Estado = 'SinNodo'; Detalle = "El nodo no responde en $($ficha.Destino)."; Archivos = @() }
        }
        $objetivoLocal = @(if ($SoloPendientes) { $planLocal.ArchivosPendientes } else { $planLocal.ArchivosTodos }) | Where-Object { $null -ne $_ }
        $objetivoLocal = @($objetivoLocal)
        if ($objetivoLocal.Count -eq 0) {
            $msgNada = if ($SoloPendientes) { 'No hay archivos pendientes de leer: todos los manifiestos cuadran con el nodo.' } else { 'En el nodo no hay discos grandes que leer.' }
            return [pscustomobject]@{ Estado = 'Nada'; Detalle = $msgNada; Archivos = @() }
        }
        $momento = (Get-Date).ToString('s')
        $leidos = foreach ($a in $objetivoLocal) {
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
        $est = Get-EstadoDeLecturaDelLaboratorio -Configuracion $Configuracion
        if (-not $est.SshOk) {
            return [pscustomobject]@{ Estado = 'SinNodo'; Detalle = $est.Detalle; Archivos = @() }
        }
        if (-not $est.Terminada) {
            return [pscustomobject]@{
                Estado  = 'EnCurso'
                Detalle = ('{0} (lanzada {1})' -f $est.Detalle, $t.Inicio)
                Avance  = $est.Avance
                Vivo    = $est.ProcesoVivo
                Archivos = @()
            }
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
    $plan = Get-PlanDeLecturaDelLaboratorio -Configuracion $Configuracion
    if (-not $plan.NodoVivo) {
        return [pscustomobject]@{ Estado = 'SinNodo'; Detalle = "El nodo no responde en $($ficha.Destino)."; Archivos = @() }
    }
    $grandes = @(if ($SoloPendientes) { $plan.ArchivosPendientes } else { $plan.ArchivosTodos }) | Where-Object { $null -ne $_ }
    $grandes = @($grandes)
    if ($grandes.Count -eq 0) {
        $msgNada = if ($SoloPendientes) { 'No hay VMs pendientes de leer en la Pi: todos los manifiestos cuadran con el nodo.' } else { 'En el nodo no hay discos grandes que leer.' }
        return [pscustomobject]@{ Estado = 'Nada'; Detalle = $msgNada; Archivos = @() }
    }
    $alcanceTexto = if ($SoloPendientes) { 'pendientes' } else { 'completo' }
    if (-not $PSCmdlet.ShouldProcess($ficha.DestinoEnLaPi, ('Lanzar lectura ({0}) en la Pi' -f $alcanceTexto))) { return $null }

    [void](Publish-BlkhashEnLaPi -Ficha $ficha)
    $lista = @($grandes | ForEach-Object { Get-RutaEnLaPi -Ficha $ficha -Relativa $_.Relativa })
    [void](Invoke-EnLaPi -Ficha $ficha -Entrada $lista -Orden ("sudo -n -u {0} sh -c 'rm -rf {1} && mkdir -p {1} && cat > {1}/lista.txt'" -f $usuario, $dir))
    [void](Invoke-EnLaPi -Ficha $ficha -Orden ("sudo -n -u {0} sh -c 'cd {1} && setsid nohup python3 {2} lote {3} lista.txt salida </dev/null >log.txt 2>&1 &'" -f
            $usuario, $dir, $script:BlkhashEnLaPi, $ficha.Bloque))

    $i = 0
    $registro = [pscustomobject]@{
        Inicio   = (Get-Date).ToString('s')
        Alcance  = $alcanceTexto
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
    $etiq = if ($SoloPendientes) { 'Lectura de pendientes' } else { 'Lectura completa' }
    $frase = '{0} lanzada en la Pi: {1} archivos, {2}, unos {3}. Pulsa [E] para seguirla en vivo o vuelve al tablero.' -f $etiq, $grandes.Count, (Format-BytesDelLaboratorio -Bytes $bytes), $cuanto
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
    'Ver'             { Get-VistaDelLaboratorio -Configuracion $configuracion }
    'Previa'          { Invoke-GuardadoDelLaboratorio -Configuracion $configuracion -Simular }
    'Guardar'         { Invoke-GuardadoDelLaboratorio -Configuracion $configuracion -Confirm:$false }
    'PreviaFrio'      { Invoke-GuardadoFrioDelLaboratorio -Configuracion $configuracion -Simular }
    'GuardarFrio'     {
        $carpetaFrio = Get-CarpetaDeEstado -Configuracion $configuracion
        Enter-MarcaDeCorrida -Carpeta $carpetaFrio -Tipo 'laboratorio' -Archivo 'LABORATORIO.lock' -Confirm:$false | Out-Null
        try { Invoke-GuardadoFrioDelLaboratorio -Configuracion $configuracion -Confirm:$false }
        finally { Exit-MarcaDeCorrida -Carpeta $carpetaFrio -Archivo 'LABORATORIO.lock' -Confirm:$false }
    }
    'Validar'         {
        $validacion = Get-ValidacionDelLaboratorio -Configuracion $configuracion -Profundo:$Profundo
        if ($validacion) { Write-ValidacionDelLaboratorio -Validacion $validacion; if ($PassThru) { $validacion } }
    }
    'Leer'            { Invoke-LecturaDelLaboratorio -Configuracion $configuracion -SoloPendientes:$SoloPendientes -Confirm:$false }
    'PlanLectura'     { Get-PlanDeLecturaDelLaboratorio -Configuracion $configuracion }
    'EstadoLectura'   { Get-EstadoDeLecturaDelLaboratorio -Configuracion $configuracion }
    'CancelarLectura' { Stop-LecturaDelLaboratorio -Configuracion $configuracion -Confirm:$false }
}
