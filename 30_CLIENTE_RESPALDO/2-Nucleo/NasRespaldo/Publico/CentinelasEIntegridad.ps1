#Requires -Version 5.1

# Modulo NasRespaldo - dominio: CentinelasEIntegridad.ps1

function Test-Centinela {
    <#
        .SYNOPSIS
            Capa 3. Si un centinela no cuadra, se aborta antes de escribir nada.
        .DESCRIPTION
            Un punado de archivos senuelo con huella conocida repartidos entre
            las raices. Nadie los usa, asi que si cambian es que algo los esta
            tocando. Se revisan AL ARRANCAR.

            Un centinela que FALTA cuenta como fallo igual que uno alterado:
            borrarlo es la forma mas barata de desarmar esta capa.

            TOLERANCIA A RENOMBRE LEGITIMO (NIST SP 1800-26 + ADR-0077):
            si se pasa -Contenedores y una ruta declarada dentro de un
            contenedor ya no existe, se busca en las hijas de primer nivel de
            ese MISMO contenedor un archivo con el mismo nombre cuya huella
            SHA-256 coincida EXACTAMENTE con la esperada:
              - Hallado en una sola carpeta NN_* -> Renombrado (no es ataque).
              - Hallado en una sola carpeta sin numero -> Desactivado por
                renombre (se avisa en vez de fingir ransomware).
              - Hallado en mas de una carpeta, ausente o con huella alterada ->
                FALLO duro (aborto antirransomware intacto).
        .PARAMETER Centinelas
            Lista de la configuracion, con ruta y huella esperada.
        .PARAMETER Contenedores
            Opcional: contenedores declarados para reconocer renombres legitimos
            de carpetas NN_* dentro del mismo contenedor.
    #>
    [CmdletBinding()]
    [OutputType([psobject])]
    param(
        [Parameter(Mandatory)][AllowEmptyCollection()][psobject[]] $Centinelas,
        [AllowEmptyCollection()][psobject[]] $Contenedores = @()
    )

    $rutasContenedores = New-Object System.Collections.Generic.HashSet[string]([System.StringComparer]::OrdinalIgnoreCase)
    foreach ($cont in @($Contenedores)) {
        if ($null -eq $cont) { continue }
        $rc = if ($cont -is [string]) { $cont } elseif ($cont.PSObject.Properties.Name -contains 'ruta') { '' + $cont.ruta } else { '' }
        $rc = $rc.Trim().TrimEnd('\')
        if ($rc) { [void]$rutasContenedores.Add($rc) }
    }

    # Rutas de centinelas que siguen presentes en su lugar original: nunca
    # pueden ser reclamadas como destino de un renombre de otra entrada.
    $ocupadas = New-Object System.Collections.Generic.HashSet[string]([System.StringComparer]::OrdinalIgnoreCase)
    foreach ($c in $Centinelas) {
        if ($c -and $c.ruta -and (Test-Path -LiteralPath $c.ruta -PathType Leaf)) {
            [void]$ocupadas.Add($c.ruta)
        }
    }

    $fallos       = New-Object System.Collections.Generic.List[psobject]
    $renombrados  = New-Object System.Collections.Generic.List[psobject]
    $desactivados = New-Object System.Collections.Generic.List[psobject]

    foreach ($c in $Centinelas) {
        $huellaEsperada = ('' + $c.huella).ToLowerInvariant()
        if (Test-Path -LiteralPath $c.ruta -PathType Leaf) {
            $actual = Get-HuellaDeArchivo -Ruta $c.ruta
            if ($actual -ne $huellaEsperada) {
                $fallos.Add([pscustomobject]@{ Ruta = $c.ruta; Motivo = 'HUELLA DISTINTA' })
            }
            continue
        }

        # Ruta ausente: solo se busca renombre dentro del mismo contenedor
        # declarado. Una raiz declarada (como C:\dev) o una llamada sin
        # -Contenedores falla en seco como siempre.
        $raizAnterior   = Split-Path -Path $c.ruta -Parent
        $padreAnterior  = if ($raizAnterior) { (Split-Path -Path $raizAnterior -Parent).TrimEnd('\') } else { '' }
        $nombreArchivo  = Split-Path -Path $c.ruta -Leaf

        $resuelto = $false
        if ($padreAnterior -and $rutasContenedores.Contains($padreAnterior) -and
            (Test-Path -LiteralPath $padreAnterior -PathType Container)) {

            $coincidencias = New-Object System.Collections.Generic.List[psobject]
            foreach ($dir in @(Get-ChildItem -LiteralPath $padreAnterior -Directory -Force -ErrorAction SilentlyContinue)) {
                if (($dir.Attributes -band [System.IO.FileAttributes]::ReparsePoint) -ne 0) { continue }
                $candidato = Join-Path $dir.FullName $nombreArchivo
                if ($ocupadas.Contains($candidato)) { continue }
                if (-not (Test-Path -LiteralPath $candidato -PathType Leaf)) { continue }
                try {
                    $hCand = Get-HuellaDeArchivo -Ruta $candidato
                }
                catch { continue }
                if ($hCand -eq $huellaEsperada) {
                    $coincidencias.Add([pscustomobject]@{
                        Dir       = $dir
                        Candidato = $candidato
                    })
                }
            }

            if ($coincidencias.Count -eq 1) {
                $match = $coincidencias[0]
                [void]$ocupadas.Add($match.Candidato)
                $ficha = [pscustomobject]@{
                    RutaAnterior = $c.ruta
                    RutaNueva    = $match.Candidato
                    RaizAnterior = $raizAnterior
                    RaizNueva    = $match.Dir.FullName
                    Huella       = $huellaEsperada
                    Contenedor   = $padreAnterior
                }
                if ($match.Dir.Name -match '^\d{2}_.') {
                    $renombrados.Add($ficha)
                }
                else {
                    $desactivados.Add($ficha)
                }
                $resuelto = $true
            }
            elseif ($coincidencias.Count -gt 1) {
                $fallos.Add([pscustomobject]@{ Ruta = $c.ruta; Motivo = 'CENTINELA DUPLICADO' })
                $resuelto = $true
            }
        }

        if (-not $resuelto) {
            $fallos.Add([pscustomobject]@{ Ruta = $c.ruta; Motivo = 'FALTA' })
        }
    }

    return [pscustomobject]@{
        Revisados    = @($Centinelas).Count
        Fallos       = $fallos.ToArray()
        Renombrados  = $renombrados.ToArray()
        Desactivados = $desactivados.ToArray()
        Correcto     = ($fallos.Count -eq 0)
    }
}

function Set-RutaDeCentinelaEnConfig {
    <#
        .SYNOPSIS
            Actualiza la ruta de UN centinela renombrado dentro de respaldo.jsonc
            conservando comentarios, sangria y finales de linea.
        .DESCRIPTION
            Sigue las mismas tres guardas que Set-HorarioDeRespaldo: edita solo
            el literal exacto asociado a la huella criptografica verificada,
            escribe de forma atomica y relee el archivo del disco con
            restauracion automatica si algo no cuadra.
        .PARAMETER RutaAnterior
            Ruta literal que estaba declarada antes del renombre.
        .PARAMETER RutaNueva
            Ruta nueva donde se confirmo la misma huella SHA-256.
        .PARAMETER Huella
            Huella SHA-256 esperada (candado para no tocar otra entrada).
        .PARAMETER Ruta
            Archivo .jsonc a actualizar.
    #>
    [CmdletBinding(SupportsShouldProcess, ConfirmImpact = 'High')]
    [OutputType([psobject])]
    param(
        [Parameter(Mandatory)][ValidateNotNullOrEmpty()][string] $RutaAnterior,
        [Parameter(Mandatory)][ValidateNotNullOrEmpty()][string] $RutaNueva,
        [Parameter(Mandatory)][ValidateNotNullOrEmpty()][string] $Huella,
        [ValidateNotNullOrEmpty()]
        [string] $Ruta = (Join-Path $script:RaizCliente '3-Config\respaldo.jsonc')
    )

    if (-not (Test-Path -LiteralPath $Ruta -PathType Leaf)) {
        throw "No existe la configuracion para actualizar centinela: $Ruta"
    }

    $texto = Get-Content -LiteralPath $Ruta -Raw -Encoding UTF8
    $jsonAntes = $RutaAnterior.Replace('\', '\\').Replace('"', '\"')
    $jsonNueva = $RutaNueva.Replace('\', '\\').Replace('"', '\"')
    $huellaLow = $Huella.ToLowerInvariant()

    # Solo coincide con la entrada de objeto que contiene EXACTAMENTE esta ruta
    # y esta huella SHA-256, evitando tocar comentarios u otras secciones.
    $patron = '(\{\s*"ruta"\s*:\s*")' + [regex]::Escape($jsonAntes) + '("\s*,\s*"huella"\s*:\s*"' + [regex]::Escape($huellaLow) + '"\s*\})'
    $m = [regex]::Match($texto, $patron, [System.Text.RegularExpressions.RegexOptions]::IgnoreCase)
    if (-not $m.Success) {
        throw "No se encontro el centinela '$RutaAnterior' con huella '$huellaLow' en $Ruta."
    }

    $reemplazo = $m.Groups[1].Value + $jsonNueva + $m.Groups[2].Value
    $nuevoTexto = $texto.Remove($m.Index, $m.Length).Insert($m.Index, $reemplazo)

    if (-not $PSCmdlet.ShouldProcess($Ruta, "Actualizar centinela renombrado: $RutaAnterior -> $RutaNueva")) {
        return [pscustomobject]@{ Escrito = $false; RutaAnterior = $RutaAnterior; RutaNueva = $RutaNueva }
    }

    Set-ContenidoAtomico -Ruta $Ruta -Contenido $nuevoTexto -Confirm:$false

    # Relectura obligatoria: si el archivo no analiza o no contiene el nuevo par
    # ruta/huella, se restaura byte a byte el original.
    try {
        $releida = Get-ConfiguracionRespaldo -Ruta $Ruta
        $confirmado = @($releida.centinelas | Where-Object {
                ('' + $_.ruta).Equals($RutaNueva, [System.StringComparison]::OrdinalIgnoreCase) -and
                ('' + $_.huella).ToLowerInvariant() -eq $huellaLow
            }).Count -eq 1
        if (-not $confirmado) {
            throw 'El centinela actualizado no aparecio al releer el archivo.'
        }
    }
    catch {
        Set-ContenidoAtomico -Ruta $Ruta -Contenido $texto -Confirm:$false
        throw "No se pudo verificar el centinela actualizado y se restauro el archivo original: $($_.Exception.Message)"
    }

    Write-RegistroRespaldo -Nivel 'ATENCION' -Etapa 'centinela' -SoloArchivo `
        -Mensaje ("CENTINELA ACTUALIZADO por renombre: {0} -> {1}" -f $RutaAnterior, $RutaNueva)
    return [pscustomobject]@{ Escrito = $true; RutaAnterior = $RutaAnterior; RutaNueva = $RutaNueva }
}

function Move-RaizRenombradaEnDestino {
    <#
        .SYNOPSIS
            Migra atomicamente en el destino una raiz NN_* renombrada en local.
        .DESCRIPTION
            Cuando Test-Centinela confirma que una raiz NN_* cambio de nombre
            dentro del mismo contenedor, renombrar la carpeta equivalente en el
            NAS (mismo volumen ext4 via SMB) evita re-subir decenas de GB y
            evita dejar la carpeta vieja huerfana en el destino.

            Solo actua si:
              - origen anterior y origen nuevo traducen al MISMO padre destino,
              - el destino anterior existe,
              - el destino nuevo NO existe todavia (nunca pisa ni fusiona).
        .PARAMETER RaizAnterior
            Ruta local anterior de la raiz.
        .PARAMETER RaizNueva
            Ruta local nueva de la raiz.
        .PARAMETER RaizDestino
            Raiz del equipo en el destino (p. ej. \\nas\datos\01_BACKUP\PC).
        .PARAMETER Traducciones
            Tabla destinos.nodo.traduccionDeRutas.
        .PARAMETER Simular
            No renombra; informa si migraria.
    #>
    [CmdletBinding(SupportsShouldProcess, ConfirmImpact = 'Medium')]
    [OutputType([psobject])]
    param(
        [Parameter(Mandatory)][ValidateNotNullOrEmpty()][string] $RaizAnterior,
        [Parameter(Mandatory)][ValidateNotNullOrEmpty()][string] $RaizNueva,
        [Parameter(Mandatory)][ValidateNotNullOrEmpty()][string] $RaizDestino,
        [Parameter(Mandatory)][psobject[]] $Traducciones,
        [switch] $Simular
    )

    $destAnterior = Get-RutaEnDestino -RutaOrigen $RaizAnterior -RaizDestino $RaizDestino -Traducciones $Traducciones
    $destNuevo    = Get-RutaEnDestino -RutaOrigen $RaizNueva    -RaizDestino $RaizDestino -Traducciones $Traducciones

    $padreAnt = (Split-Path -Path $destAnterior -Parent).TrimEnd('\')
    $padreNvo = (Split-Path -Path $destNuevo    -Parent).TrimEnd('\')
    if (-not $padreAnt.Equals($padreNvo, [System.StringComparison]::OrdinalIgnoreCase)) {
        return [pscustomobject]@{ Migrada = $false; DestinoAnterior = $destAnterior; DestinoNuevo = $destNuevo; Motivo = 'distinto contenedor destino' }
    }
    if (-not (Test-Path -LiteralPath $destAnterior -PathType Container)) {
        return [pscustomobject]@{ Migrada = $false; DestinoAnterior = $destAnterior; DestinoNuevo = $destNuevo; Motivo = 'destino anterior no existe' }
    }
    if (Test-Path -LiteralPath $destNuevo -PathType Container) {
        return [pscustomobject]@{ Migrada = $false; DestinoAnterior = $destAnterior; DestinoNuevo = $destNuevo; Motivo = 'destino nuevo ya existe' }
    }

    $nombreNuevo = Split-Path -Path $destNuevo -Leaf
    if ($Simular) {
        return [pscustomobject]@{ Migrada = $false; Simulada = $true; DestinoAnterior = $destAnterior; DestinoNuevo = $destNuevo; Motivo = 'simulacion: migraria atomicamente en destino' }
    }
    if (-not $PSCmdlet.ShouldProcess($destAnterior, "Renombrar destino a $nombreNuevo")) {
        return [pscustomobject]@{ Migrada = $false; DestinoAnterior = $destAnterior; DestinoNuevo = $destNuevo; Motivo = 'WhatIf' }
    }

    try {
        Rename-Item -LiteralPath $destAnterior -NewName $nombreNuevo -ErrorAction Stop
        return [pscustomobject]@{ Migrada = $true; DestinoAnterior = $destAnterior; DestinoNuevo = $destNuevo; Motivo = 'renombrada atomicamente en destino' }
    }
    catch {
        return [pscustomobject]@{ Migrada = $false; DestinoAnterior = $destAnterior; DestinoNuevo = $destNuevo; Motivo = "no se pudo renombrar: $($_.Exception.Message)" }
    }
}

function New-Centinela {
    <#
        .SYNOPSIS
            Crea un centinela y devuelve la entrada lista para la configuracion.
        .DESCRIPTION
            Lo crea EL MOTOR, no una persona. Nombre con guion bajo delante para
            que no se auto-inscriba, y contenido fechado para que se distinga de
            un archivo real.
        .PARAMETER Carpeta
            Donde ponerlo.
    #>
    [CmdletBinding(SupportsShouldProcess)]
    [OutputType([psobject])]
    param(
        [Parameter(Mandatory)][ValidateNotNullOrEmpty()][string] $Carpeta
    )

    $ruta = Join-Path $Carpeta '_centinela_respaldo.txt'
    if (-not $PSCmdlet.ShouldProcess($ruta, 'Crear centinela')) { return $null }

    # CADA CENTINELA LLEVA UN VALOR UNICO E IMPREDECIBLE, y no es adorno.
    # Con un contenido identico en todas las raices, los ocho comparten huella:
    # quien conozca uno los conoce todos y puede REPONER cualquiera despues de
    # tocarlo, que es exactamente lo que esta capa existe para impedir. El valor
    # sale del generador criptografico del sistema, no de Get-Random.
    $bytes = New-Object byte[] 32
    $generador = [System.Security.Cryptography.RandomNumberGenerator]::Create()
    try { $generador.GetBytes($bytes) } finally { $generador.Dispose() }
    $semilla = [System.BitConverter]::ToString($bytes).Replace('-', '').ToLowerInvariant()

    $texto = @"
Centinela del cliente de respaldo del NAS. NO lo edite ni lo borre.

Este archivo no lo usa nadie. Si su contenido cambia, algo lo esta tocando, y el
motor ABORTA antes de escribir en el respaldo (seccion 7, capa 3). Borrarlo
cuenta igual que alterarlo: es la forma mas barata de desarmar esta capa.

Creado: $(Get-Date -Format 's')
Equipo: $env:COMPUTERNAME
Valor : $semilla
"@
    Set-ContenidoAtomico -Ruta $ruta -Contenido $texto -Confirm:$false
    return [pscustomobject]@{ ruta = $ruta; huella = (Get-HuellaDeArchivo -Ruta $ruta) }
}

function Get-HuellaDeArchivo {
    <#
        .SYNOPSIS
            Huella de un archivo, en minusculas siempre.
        .DESCRIPTION
            NORMALIZAR LOS DOS LADOS ANTES DE COMPARAR. PowerShell escribe la
            huella en MAYUSCULAS y md5sum en minusculas; el 2026-09-02 eso
            produjo "27 508 diferencias" que eran cero, dos veces seguidas. Un
            cotejo que sale 100 % distinto casi nunca es un desastre: casi
            siempre es formato. Esta funcion devuelve minusculas para que el
            error no pueda repetirse desde este lado.

            DOS ALGORITMOS, Y LA DIFERENCIA IMPORTA:

              SHA256 (por omision) para los CENTINELAS. Esa capa es
                ADVERSARIAL: existe para detectar que algo esta tocando archivos
                que nadie usa. MD5 tiene colisiones baratas, asi que un atacante
                que quisiera podria alterar un centinela conservando su MD5 y la
                capa 3 no se enteraria. PSScriptAnalyzer marca MD5 como roto y
                tiene razon PARA ESTE USO.

              MD5 solo por INTEROPERABILIDAD con el nodo, y hay que pedirlo
                expresamente. La linea base del 2026-08-31 y las 27 508 huellas
                de la seccion 12.septies son MD5, calculadas con md5sum en el
                Pi. Ahi el adversario no existe: se compara contra corrupcion
                accidental, y rehacer esa linea base en SHA256 costaria otra
                lectura completa de 60 GB sobre un enlace de 13.4 MB/s.
        .PARAMETER Ruta
            Archivo a medir.
        .PARAMETER Algoritmo
            SHA256 por omision. MD5 solo para cotejar contra el nodo.
    #>
    [CmdletBinding()]
    [OutputType([string])]
    param(
        [Parameter(Mandatory, ValueFromPipeline)]
        [ValidateNotNullOrEmpty()]
        [string] $Ruta,

        [ValidateSet('SHA256', 'MD5')]
        [string] $Algoritmo = 'SHA256'
    )
    process {
        # .NET directo en vez de Get-FileHash: evita el fallo de cmdlet cuando
        # PowerShell 5.1 hereda el PSModulePath de PowerShell 7, es mas rapido
        # y lanza IOException exacta si otro proceso bloquea el archivo.
        $alg = [System.Security.Cryptography.HashAlgorithm]::Create($Algoritmo)
        if ($null -eq $alg) { throw "Algoritmo de huella no disponible: $Algoritmo" }
        try {
            $flujo = [System.IO.File]::OpenRead($Ruta)
            try {
                $hashBytes = $alg.ComputeHash($flujo)
                return ([System.BitConverter]::ToString($hashBytes).Replace('-', '').ToLowerInvariant())
            }
            finally { $flujo.Dispose() }
        }
        finally { $alg.Dispose() }
    }
}

function Get-ArchivosEnVuelo {
    <#
        .SYNOPSIS
            Los archivos que una copia interrumpida deja rotos Y ESCONDIDOS.

        .DESCRIPTION
            EL PUNTO CIEGO DE /XO, Y ES EL MOTIVO DE QUE ESTA FUNCION EXISTA.

            La clase A copia con /E /XO. /XO significa "excluir cuando el ORIGEN
            es mas viejo que el destino". Robocopy escribe primero los datos y
            SOLO AL TERMINAR le pone al destino la fecha del origen. Si la
            corrida se corta a medias -un apagon, un cable, un cierre de
            sesion-, el archivo que estaba en vuelo se queda con la fecha DE ESE
            MOMENTO, que es mas nueva que la del origen.

            A partir de ahi, /XO lo salta EN TODAS LAS CORRIDAS SIGUIENTES. El
            archivo queda incompleto para siempre, robocopy devuelve 0, y el
            motor lo declara sano. Es exactamente el modo de fallo que este
            contrato persigue: no el que falla a gritos, sino el que se declara
            correcto.

            EL TAMANO NO BASTA PARA DESCARTARLO. NTFS registra en su diario los
            metadatos -incluido el tamano- antes que los datos. Tras un apagon
            sucio un archivo puede medir lo que debe y contener ceros. Por eso
            esta funcion solo SELECCIONA candidatos por metadatos; quien afirma
            que el contenido es identico es la huella, y eso lo hace
            Test-HuellaEnVuelo.

            NO BORRA NADA. Detecta y reporta, el mismo trato que da la seccion
            3.3 punto 4 al huerfano y ADR-0080 al secreto. La reparacion es un
            gesto aparte y explicito.

        .PARAMETER Origen
            Carpeta de origen, la autoridad.
        .PARAMETER Destino
            Carpeta de destino a revisar.
        .PARAMETER ToleranciaSegundos
            Margen de fecha. Por omision 2, el mismo de /FFT: sin el, cada
            archivo de una copia Windows -> Linux saldria sospechoso.
        .PARAMETER Desde
            Si se da, solo se miran archivos del destino tocados a partir de ese
            momento. Es lo barato y lo correcto cuando se sabe cuando arranco la
            corrida que se corto: un archivo en vuelo se escribio DURANTE ella.
    #>
    [CmdletBinding()]
    [OutputType([psobject[]])]
    param(
        [Parameter(Mandatory)][ValidateNotNullOrEmpty()][string] $Origen,
        [Parameter(Mandatory)][ValidateNotNullOrEmpty()][string] $Destino,
        [ValidateRange(0, 3600)][int] $ToleranciaSegundos = 2,
        [datetime] $Desde = [datetime]::MinValue
    )

    # UN ORIGEN QUE NO RESPONDE NO ES "CERO HALLAZGOS", Y ESTA DISTINCION COSTO
    # UNA MEDICION FALSA EL 2026-09-02: con el nodo caido, la primera version de
    # esta funcion no encontraba con que comparar y devolvia una lista vacia,
    # que se lee igual que "todo esta bien". Sin origen NO se puede afirmar
    # nada, asi que se falla a gritos en vez de declarar salud.
    if (-not (Test-Path -LiteralPath $Origen -PathType Container)) {
        throw "El origen '$Origen' no responde. Sin origen no hay con que comparar, y una lista vacia se leeria como 'no hay nada roto'."
    }

    $hallazgos = New-Object System.Collections.Generic.List[psobject]
    if (-not (Test-Path -LiteralPath $Destino -PathType Container)) { return $hallazgos.ToArray() }

    $raizOrigen  = $Origen.TrimEnd('\')
    $raizDestino = $Destino.TrimEnd('\')

    foreach ($d in @(Get-ChildItem -LiteralPath $raizDestino -Recurse -File -Force -ErrorAction SilentlyContinue)) {
        if ($d.LastWriteTime -lt $Desde) { continue }

        $relativa = $d.FullName.Substring($raizDestino.Length).TrimStart('\')
        $gemelo   = Join-Path $raizOrigen $relativa
        $o = Get-Item -LiteralPath $gemelo -Force -ErrorAction SilentlyContinue

        # Sin gemelo en el origen NO es un hallazgo de esta funcion: en clase A
        # el destino conserva a proposito lo que el origen ya borro (seccion 4).
        if (-not $o) { continue }

        $motivo = $null
        if ($o.Length -ne $d.Length) {
            $motivo = 'TamanoDistinto'
        }
        elseif (($d.LastWriteTime - $o.LastWriteTime).TotalSeconds -gt $ToleranciaSegundos) {
            # El destino es MAS NUEVO que el origen: /XO no lo volvera a tocar.
            $motivo = 'DestinoMasNuevo'
        }
        if (-not $motivo) { continue }

        $hallazgos.Add([pscustomobject]@{
            Relativa      = $relativa
            RutaOrigen    = $o.FullName
            RutaDestino   = $d.FullName
            TamanoOrigen  = $o.Length
            TamanoDestino = $d.Length
            FechaOrigen   = $o.LastWriteTime
            FechaDestino  = $d.LastWriteTime
            Motivo        = $motivo
        })
    }

    return $hallazgos.ToArray()
}

function Test-HuellaEnVuelo {
    <#
        .SYNOPSIS
            Dice si un archivo en vuelo quedo intacto o roto. Por huella.
        .DESCRIPTION
            Es el unico nivel de certeza que sirve aqui (seccion 9): tras un
            apagon el tamano puede coincidir y el contenido no.
        .PARAMETER EnVuelo
            Lo que devuelve Get-ArchivosEnVuelo.
    #>
    [CmdletBinding()]
    [OutputType([psobject[]])]
    param(
        [Parameter(Mandatory)][AllowEmptyCollection()][psobject[]] $EnVuelo
    )

    $resultado = New-Object System.Collections.Generic.List[psobject]
    foreach ($a in $EnVuelo) {
        $identico = $false
        $detalle  = ''
        try {
            if ($a.TamanoOrigen -eq $a.TamanoDestino) {
                $ho = Get-HuellaDeArchivo -Ruta $a.RutaOrigen  -Algoritmo SHA256
                $hd = Get-HuellaDeArchivo -Ruta $a.RutaDestino -Algoritmo SHA256
                $identico = ($ho -eq $hd)
                $detalle  = if ($identico) { 'Contenido identico: solo le falto la fecha' } else { 'CONTENIDO DISTINTO' }
            }
            else {
                $detalle = 'Tamano distinto: no hace falta huella'
            }
        }
        catch {
            $detalle = "No se pudo comparar: $($_.Exception.Message)"
        }
        $resultado.Add([pscustomobject]@{
            Relativa    = $a.Relativa
            RutaOrigen  = $a.RutaOrigen
            RutaDestino = $a.RutaDestino
            Motivo      = $a.Motivo
            Identico    = $identico
            Detalle     = $detalle
        })
    }
    return $resultado.ToArray()
}

function Repair-ArchivosEnVuelo {
    <#
        .SYNOPSIS
            Rehace los archivos que una copia interrumpida dejo rotos.

        .DESCRIPTION
            SOBRESCRIBE, NUNCA BORRA. La pasada al disco es aditiva y esa
            propiedad ya salvo una vez este proyecto (seccion 12.undecies): no
            se rompe ni para reparar. El archivo malo se pisa con el bueno del
            origen, que es la autoridad; nada se retira del destino.

            POR QUE NO BASTA CON VOLVER A CORRER LA COPIA NORMAL: la clase A usa
            /XO, y /XO es justo lo que salta estos archivos -su fecha en el
            destino es MAS NUEVA que la del origen-. Repararlos exige quitar
            /XO y anadir /IS /IT, y eso NO se hace en la corrida normal: sin
            /XO, una corrida cualquiera empezaria a pisar en el destino cosas
            que el origen tiene mas viejas, que es media politica de clase A
            tirada a la basura. Por eso es una funcion aparte y explicita.

            SE TRABAJA POR CARPETA, no por arbol: robocopy recibe la carpeta y
            los NOMBRES concretos, sin /E, asi que no puede tocar un archivo que
            no este en la lista.

        .PARAMETER EnVuelo
            Lo que devuelve Get-ArchivosEnVuelo.
    #>
    [CmdletBinding(SupportsShouldProcess, ConfirmImpact = 'High')]
    [OutputType([psobject[]])]
    param(
        [Parameter(Mandatory)][AllowEmptyCollection()][psobject[]] $EnVuelo
    )

    $resultado = New-Object System.Collections.Generic.List[psobject]
    $porCarpeta = $EnVuelo | Group-Object { Split-Path $_.RutaOrigen -Parent }

    foreach ($grupo in $porCarpeta) {
        $dirOrigen  = $grupo.Name
        $dirDestino = Split-Path ($grupo.Group[0].RutaDestino) -Parent
        $nombres    = @($grupo.Group | ForEach-Object { Split-Path $_.RutaDestino -Leaf })

        if (-not $PSCmdlet.ShouldProcess($dirDestino, ("Rehacer {0} archivo(s) rotos" -f $nombres.Count))) { continue }

        # /IS incluye los que robocopy considera iguales, /IT los retocados.
        # Sin /E: solo esta carpeta. Sin /XO: es lo que hay que anular.
        $argumentos = @($dirOrigen, $dirDestino) + $nombres +
            @('/IS', '/IT', '/XJ', '/DCOPY:DAT', '/FFT', '/R:2', '/W:5', '/NP', '/FP', '/NJH', '/NJS', '/NDL', '/NS')

        Write-Verbose "robocopy $($argumentos -join ' ')"
        $salida = & robocopy.exe @argumentos 2>&1
        $codigo = $LASTEXITCODE

        $ok = ($codigo -le $script:RobocopyMaximoCorrecto)
        if (-not $ok) {
            Write-RegistroRespaldo -Nivel 'ERROR' -Etapa 'reparacion' `
                -Mensaje ("La reparacion de '{0}' devolvio {1}. Primera linea: {2}" -f $dirDestino, $codigo, ($salida | Select-Object -First 1))
        }

        foreach ($n in $nombres) {
            $resultado.Add([pscustomobject]@{
                Carpeta  = $dirDestino
                Archivo  = $n
                Codigo   = $codigo
                Correcto = $ok
            })
        }
    }

    return $resultado.ToArray()
}
