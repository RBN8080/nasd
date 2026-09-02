#Requires -Version 5.1
<#
    comun.ps1  -  Lo que usan todos los demas. Sin logica de respaldo.

    Contrato: 30_CLIENTE_RESPALDO.md seccion 16.bis "Que se reutiliza - paradigmas".

    Paradigmas que hereda del nodo, no lineas de codigo:
      - Cero dependencias externas (ADR-0013, ADR-0017). Solo PowerShell y el
        .NET que Windows ya trae. NADA de modulos de galeria en tiempo de
        ejecucion. PSScriptAnalyzer es utillaje de desarrollo (ADR-0078), no
        dependencia de este codigo.
      - Escritura atomica (internal/atomico): a temporal y renombrar. Nunca a
        medias, porque el indicador esta leyendo al mismo tiempo.
      - Registro estructurado: una linea por suceso.
      - "Verificado, no supuesto": las cifras se miden y se fechan.

    CONVENCION DE ESTE DIRECTORIO, y no es cosmetica:
    los .ps1 se escriben SIN ACENTOS. El repositorio va en LF y sin BOM
    (.gitattributes: * text=auto eol=lf), y PowerShell 5.1 lee un .ps1 sin BOM
    como ANSI: los acentos salen rotos. Es la misma razon por la que .gitmessage
    esta escrito sin acentos. Los acentos viven en los .md y en los comentarios
    del .jsonc, que se leen con -Encoding UTF8 explicito.

    Uso: se carga con punto desde los demas guiones.
        . "$PSScriptRoot\comun.ps1"
#>

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

# Codigos de salida de robocopy. NO es exito/fracaso: es un MAPA DE BITS.
# 0-7 correcto, >= 8 error. El 2026-09-01 una herramienta dio por fallida una
# copia buena porque robocopy devolvio 1, que significa "se copiaron archivos
# correctamente" (pendiente 22 del contrato).
$script:RobocopyMaximoCorrecto = 7

function ConvertFrom-Jsonc {
    <#
        .SYNOPSIS
            Convierte texto JSONC (JSON con comentarios) en objetos.
        .DESCRIPTION
            Retira comentarios de linea (//) y de bloque, respetando las cadenas
            entre comillas: una barra doble DENTRO de una cadena no es un
            comentario. Un recortador ingenuo destroza cualquier URL.
        .PARAMETER Texto
            El contenido completo del archivo .jsonc.
        .EXAMPLE
            Get-Content c.jsonc -Raw -Encoding UTF8 | ConvertFrom-Jsonc
    #>
    [CmdletBinding()]
    [OutputType([psobject])]
    param(
        [Parameter(Mandatory, ValueFromPipeline)]
        [AllowEmptyString()]
        [string] $Texto
    )
    process {
        $sb = New-Object System.Text.StringBuilder
        $enCadena = $false
        $escapado = $false
        $i = 0
        while ($i -lt $Texto.Length) {
            $c = $Texto[$i]
            if ($enCadena) {
                [void]$sb.Append($c)
                if ($escapado)      { $escapado = $false }
                elseif ($c -eq '\') { $escapado = $true }
                elseif ($c -eq '"') { $enCadena = $false }
                $i++
                continue
            }
            if ($c -eq '"') { $enCadena = $true; [void]$sb.Append($c); $i++; continue }
            if ($c -eq '/' -and ($i + 1) -lt $Texto.Length) {
                $siguiente = $Texto[$i + 1]
                if ($siguiente -eq '/') {
                    while ($i -lt $Texto.Length -and $Texto[$i] -ne "`n") { $i++ }
                    continue
                }
                if ($siguiente -eq '*') {
                    $i += 2
                    while (($i + 1) -lt $Texto.Length -and -not ($Texto[$i] -eq '*' -and $Texto[$i + 1] -eq '/')) { $i++ }
                    $i += 2
                    continue
                }
            }
            [void]$sb.Append($c)
            $i++
        }
        return ($sb.ToString() | ConvertFrom-Json)
    }
}

function Get-ConfiguracionRespaldo {
    <#
        .SYNOPSIS
            Lee y valida 3-Config/respaldo.jsonc.
        .DESCRIPTION
            Lee SIEMPRE con -Encoding UTF8 explicito: PowerShell 5.1 sin ese
            parametro interpreta el archivo como ANSI y destroza los acentos de
            los comentarios. Valida que existan las claves de las que depende el
            motor, para fallar aqui y no a mitad de una copia.
        .PARAMETER Ruta
            Ruta al .jsonc. Por omision, el de 3-Config junto a este guion.
    #>
    [CmdletBinding()]
    [OutputType([psobject])]
    param(
        [ValidateNotNullOrEmpty()]
        [string] $Ruta = (Join-Path (Split-Path $PSScriptRoot -Parent) '3-Config\respaldo.jsonc')
    )

    if (-not (Test-Path -LiteralPath $Ruta -PathType Leaf)) {
        throw "No existe la configuracion: $Ruta"
    }

    $cfg = Get-Content -LiteralPath $Ruta -Raw -Encoding UTF8 | ConvertFrom-Jsonc

    $obligatorias = @(
        'version', 'equipo', 'destinos', 'contenedores', 'raicesDeclaradas',
        'raicesDelNodo', 'exclusiones', 'freno', 'centinelas', 'deudaPrimeraCorrida'
    )
    $faltan = @($obligatorias | Where-Object { $cfg.PSObject.Properties.Name -notcontains $_ })
    if ($faltan.Count -gt 0) {
        throw "La configuracion no declara: $($faltan -join ', ')"
    }
    if ($cfg.destinos.PSObject.Properties.Name -notcontains 'nodo') {
        throw 'La configuracion no declara destinos.nodo'
    }

    Write-Verbose "Configuracion v$($cfg.version) leida de $Ruta"
    return $cfg
}

function Write-RegistroRespaldo {
    <#
        .SYNOPSIS
            Escribe una linea de registro estructurada, y solo una por suceso.
        .DESCRIPTION
            Formato: marca de tiempo, nivel, etapa, mensaje. Se anexa a un
            archivo por dia. Paradigma heredado del nodo: registro estructurado,
            una linea por suceso.
        .PARAMETER Mensaje
            Que paso. Una frase.
        .PARAMETER Nivel
            OK, ATENCION, FRENO o ERROR. Mismo vocabulario de severidad que el
            nodo, para que los dos lados hablen igual (seccion 11).
        .PARAMETER Etapa
            Que parte del motor lo emite.
        .PARAMETER CarpetaRegistro
            Donde se anexa. Por omision, bajo LOCALAPPDATA.
    #>
    [CmdletBinding()]
    [OutputType([void])]
    param(
        [Parameter(Mandatory)]
        [ValidateNotNullOrEmpty()]
        [string] $Mensaje,

        [ValidateSet('OK', 'ATENCION', 'FRENO', 'ERROR')]
        [string] $Nivel = 'OK',

        [ValidateNotNullOrEmpty()]
        [string] $Etapa = 'general',

        [ValidateNotNullOrEmpty()]
        [string] $CarpetaRegistro = (Join-Path $env:LOCALAPPDATA 'NasRespaldo\registro')
    )

    if (-not (Test-Path -LiteralPath $CarpetaRegistro)) {
        New-Item -ItemType Directory -Path $CarpetaRegistro -Force | Out-Null
    }
    $archivo = Join-Path $CarpetaRegistro ('respaldo-{0:yyyy-MM-dd}.log' -f (Get-Date))
    $linea = '{0} {1,-8} {2,-12} {3}' -f (Get-Date -Format 's'), $Nivel, $Etapa, $Mensaje
    Add-Content -LiteralPath $archivo -Value $linea -Encoding UTF8

    switch ($Nivel) {
        'ERROR'    { Write-Error   -Message $Mensaje -ErrorAction Continue }
        'FRENO'    { Write-Warning -Message $Mensaje }
        'ATENCION' { Write-Warning -Message $Mensaje }
        default    { Write-Verbose -Message $Mensaje }
    }
}

function Set-ContenidoAtomico {
    <#
        .SYNOPSIS
            Escribe un archivo entero o no lo escribe: nunca a medias.
        .DESCRIPTION
            Escribe a un temporal en la MISMA carpeta -para que el reemplazo no
            cruce volumenes- y despues sustituye. Usa File::Replace cuando el
            destino ya existe, que es la operacion atomica de NTFS; si no existe,
            un Move basta porque no hay nada que un lector pueda ver a medias.
            El indicador de la seccion 10.2 lee ESTADO.txt mientras el motor lo
            reescribe: sin esto, puede leer medio archivo.
        .PARAMETER Ruta
            Archivo destino.
        .PARAMETER Contenido
            Texto completo a escribir.
    #>
    [CmdletBinding(SupportsShouldProcess)]
    [OutputType([void])]
    param(
        [Parameter(Mandatory)]
        [ValidateNotNullOrEmpty()]
        [string] $Ruta,

        [Parameter(Mandatory)]
        [AllowEmptyString()]
        [string] $Contenido
    )

    if (-not $PSCmdlet.ShouldProcess($Ruta, 'Escribir de forma atomica')) { return }

    $carpeta = Split-Path -Path $Ruta -Parent
    if ($carpeta -and -not (Test-Path -LiteralPath $carpeta)) {
        New-Item -ItemType Directory -Path $carpeta -Force | Out-Null
    }
    $marca    = [guid]::NewGuid().ToString('N').Substring(0, 8)
    $temporal = '{0}.{1}.tmp' -f $Ruta, $marca
    $respaldo = '{0}.{1}.bak' -f $Ruta, $marca
    try {
        # UTF8Encoding($false) = sin BOM, para no ensuciar lo que otros lean.
        [System.IO.File]::WriteAllText($temporal, $Contenido, (New-Object System.Text.UTF8Encoding($false)))
        if (Test-Path -LiteralPath $Ruta -PathType Leaf) {
            # File::Replace EXIGE una ruta de respaldo real. Pasarle $null falla
            # en PowerShell 5.1 -medido- porque PowerShell lo convierte en cadena
            # vacia y .NET la rechaza: "La ruta de acceso no tiene un formato
            # valido". El respaldo se crea y se retira aqui mismo; lo que importa
            # es que la SUSTITUCION del destino sigue siendo atomica, que es lo
            # que protege al lector del indicador.
            [System.IO.File]::Replace($temporal, $Ruta, $respaldo)
        }
        else {
            [System.IO.File]::Move($temporal, $Ruta)
        }
    }
    finally {
        foreach ($sobra in @($temporal, $respaldo)) {
            if (Test-Path -LiteralPath $sobra -PathType Leaf) {
                Remove-Item -LiteralPath $sobra -Force -ErrorAction SilentlyContinue
            }
        }
    }
}

function Test-RutaUnc {
    <#
        .SYNOPSIS
            Dice si una ruta es UNC de verdad.
        .DESCRIPTION
            LA GUARDA QUE FALTABA EL 2026-09-02 (seccion 12.septies, tropiezo 1).
            Una ruta perdio una barra y "\\192.168.1.38\datos" quedo
            "\192.168.1.38\datos", que Windows resuelve RELATIVO A C:. Se
            copiaron 27 508 archivos a C:\192.168.1.38\ y al nodo no llego ni
            uno. Una sola barra de diferencia entre respaldar y no respaldar.
        .PARAMETER Ruta
            La ruta a comprobar.
    #>
    [CmdletBinding()]
    [OutputType([bool])]
    param(
        [Parameter(Mandatory)]
        [AllowEmptyString()]
        [string] $Ruta
    )
    return $Ruta -match '^\\\\[^\\]+\\[^\\]+'
}

function Test-DestinoNodo {
    <#
        .SYNOPSIS
            Comprueba que el destino es UNC Y que el nodo responde.
        .DESCRIPTION
            Las dos condiciones, y las dos abortan. No basta con que el nodo
            responda: si la ruta no es UNC, robocopy escribira felizmente en un
            disco local con un nombre que parece una direccion IP.
        .PARAMETER Unc
            Raiz UNC del recurso, por ejemplo \\192.168.1.38\datos
    #>
    [CmdletBinding()]
    [OutputType([bool])]
    param(
        [Parameter(Mandatory)]
        [AllowEmptyString()]
        [string] $Unc
    )

    if (-not (Test-RutaUnc -Ruta $Unc)) {
        Write-RegistroRespaldo -Nivel 'ERROR' -Etapa 'guarda' -Mensaje "El destino NO es una ruta UNC: '$Unc'. Abortado antes de escribir nada."
        return $false
    }
    if (-not (Test-Path -LiteralPath $Unc)) {
        Write-RegistroRespaldo -Nivel 'ERROR' -Etapa 'guarda' -Mensaje "El nodo no responde en '$Unc'. Abortado antes de escribir nada."
        return $false
    }
    return $true
}

function Get-DiscoFrio {
    <#
        .SYNOPSIS
            Localiza el disco frio por NUMERO DE SERIE, nunca por letra.
        .DESCRIPTION
            La letra cambia sola; el numero de serie sobrevivio a un formato
            completo, verificado el 2026-09-01 (seccion 6.1). La confirmacion es
            el archivo centinela en la raiz, que evita escribir en un disco
            parecido por accidente (seccion 15, criterios 7 y 7b).
            Devuelve $null si no esta conectado o si no supera las dos pruebas.
        .PARAMETER Configuracion
            El objeto de configuracion completo.
    #>
    [CmdletBinding()]
    [OutputType([psobject])]
    param(
        [Parameter(Mandatory)]
        [psobject] $Configuracion
    )

    $ficha = $Configuracion.destinos.discoFrio
    $disco = @(Get-Disk | Where-Object { $_.SerialNumber -eq $ficha.serie })
    if ($disco.Count -eq 0) {
        Write-Verbose "Disco frio con serie $($ficha.serie) no conectado."
        return $null
    }

    $volumen = @($disco | Get-Partition | Get-Volume | Where-Object { $_.DriveLetter })
    if ($volumen.Count -eq 0) {
        Write-RegistroRespaldo -Nivel 'ATENCION' -Etapa 'disco' -Mensaje 'El disco frio esta conectado pero sin letra de unidad.'
        return $null
    }
    $letra = $volumen[0].DriveLetter

    $raiz = '{0}:\' -f $letra
    $centinela = Join-Path $raiz $ficha.centinela
    if (-not (Test-Path -LiteralPath $centinela -PathType Leaf)) {
        Write-RegistroRespaldo -Nivel 'ERROR' -Etapa 'disco' -Mensaje "Serie correcta en $raiz pero FALTA el centinela '$($ficha.centinela)'. No se escribe nada."
        return $null
    }

    return [pscustomobject]@{
        Serie     = $ficha.serie
        Letra     = $letra
        Raiz      = $raiz
        Centinela = $centinela
    }
}

function Invoke-Robocopy {
    <#
        .SYNOPSIS
            Invoca robocopy y devuelve un resultado tipado. Nunca lo reimplementa.
        .DESCRIPTION
            La logica de espejo, saltar enlaces, reintentos y tolerancia de
            marcas de tiempo ya existe y esta probada (seccion 16.bis). Aqui solo
            se invoca y se interpreta.

            DOS COSAS QUE ESTA FUNCION EXISTE PARA HACER BIEN:

            1. EL CODIGO DE SALIDA ES UN MAPA DE BITS. 0-7 correcto, >= 8 error.
               Tratar "distinto de cero" como fallo produce una alerta falsa
               diaria hasta que se dejan de leer.

            2. LA SALIDA DE ROBOCOPY ESTA LOCALIZADA. Medido en este equipo, con
               cultura es-MX, robocopy escribe "*Archivo EXTRA", "Nuevo arch" y
               "Mas antiguo". Parsear esas etiquetas ata el motor al idioma del
               sistema. Aqui se clasifica POR PREFIJO DE RUTA, que no cambia con
               el idioma: lo que cuelga del origen se copiaria, lo que cuelga del
               destino sobra y el espejo lo BORRARIA.
        .PARAMETER Origen
            Carpeta de origen.
        .PARAMETER Destino
            Carpeta de destino.
        .PARAMETER Clase
            A (aditivo, el destino nunca borra) o B (espejo exacto).
        .PARAMETER SoloListar
            Anade /L: no copia, lista lo que copiaria. Es la base del freno y de
            la verificacion (secciones 7 y 9).
    #>
    [CmdletBinding()]
    [OutputType([psobject])]
    param(
        [Parameter(Mandatory)][ValidateNotNullOrEmpty()][string] $Origen,
        [Parameter(Mandatory)][ValidateNotNullOrEmpty()][string] $Destino,
        [Parameter(Mandatory)][ValidateSet('A', 'B')][string] $Clase,
        [switch] $SoloListar
    )

    # Banderas que NO son opcionales (seccion 4 del contrato):
    #   /XJ         Documents contiene junctions a Music, Pictures y Videos. Sin
    #               esto robocopy las sigue y arrastra los 60 GB del drone por la
    #               puerta de atras. Por poco no se pone el 2026-09-02.
    #   /FFT        Tolerancia de 2 s entre la marca de Windows y la de Linux.
    #               Sin esto el motor cree que todo cambio en cada corrida.
    #   /DCOPY:DAT  Conserva las fechas de las carpetas, no solo de los archivos.
    $comunes  = @('/XJ', '/DCOPY:DAT', '/FFT', '/MT:8', '/R:2', '/W:5', '/NP')
    $politica = if ($Clase -eq 'A') { @('/E', '/XO') } else { @('/MIR') }

    # /FP ruta completa, para poder clasificar por prefijo.
    # /NJH /NJS sin cabecera ni resumen: el resumen esta localizado y no se usa.
    # /NDL sin lineas de directorio. /NS sin tamanos.
    $formato = @('/FP', '/NJH', '/NJS', '/NDL', '/NS')

    $argumentos = @($Origen, $Destino) + $politica + $comunes + $formato
    if ($SoloListar) { $argumentos += '/L' }

    Write-Verbose "robocopy $($argumentos -join ' ')"
    $salida = & robocopy.exe @argumentos 2>&1
    $codigo = $LASTEXITCODE

    $origenNorm  = $Origen.TrimEnd('\')  + '\'
    $destinoNorm = $Destino.TrimEnd('\') + '\'

    $aCopiar    = New-Object System.Collections.Generic.List[string]
    $aBorrar    = New-Object System.Collections.Generic.List[string]
    $sinUbicar  = New-Object System.Collections.Generic.List[string]

    foreach ($linea in $salida) {
        $texto = [string]$linea
        if ([string]::IsNullOrWhiteSpace($texto)) { continue }
        # Las lineas de directorio terminan en barra; solo interesan archivos.
        if ($texto.TrimEnd().EndsWith('\')) { continue }

        $iOrigen  = $texto.IndexOf($origenNorm,  [System.StringComparison]::OrdinalIgnoreCase)
        $iDestino = $texto.IndexOf($destinoNorm, [System.StringComparison]::OrdinalIgnoreCase)

        if ($iOrigen -ge 0 -and ($iDestino -lt 0 -or $iOrigen -lt $iDestino)) {
            $aCopiar.Add($texto.Substring($iOrigen))
        }
        elseif ($iDestino -ge 0) {
            $aBorrar.Add($texto.Substring($iDestino))
        }
        else {
            # Ni origen ni destino: NO se adivina. Se guarda para que se vea.
            $sinUbicar.Add($texto.Trim())
        }
    }

    # EL CODIGO DE SALIDA NO BASTA, Y ESTO NO ES TEORICO.
    # Medido el 2026-09-02 en la primera corrida real: el nodo devolvia "Acceso
    # denegado" en cada intento sobre Videos/01_DRONE -la carpeta habia quedado
    # root:root en el `mv` manual del 02/09- y robocopy, con /MT, TERMINO
    # DEVOLVIENDO 0. Cero significa "nada que copiar, sin fallos". La copia no
    # ocurrio y el motor la dio por buena.
    #
    # Lo unico que delataba el fallo eran las lineas que no colgaban ni del
    # origen ni del destino. Por eso existe este bucket, y por eso una linea sin
    # clasificar ENSUCIA el resultado en vez de quedarse como curiosidad: un
    # respaldo que se declara sano cuando no copio nada es peor que uno que
    # falla a gritos.
    $limpio = ($codigo -le $script:RobocopyMaximoCorrecto) -and ($sinUbicar.Count -eq 0)
    if ($sinUbicar.Count -gt 0) {
        Write-RegistroRespaldo -Nivel 'ERROR' -Etapa 'copia' `
            -Mensaje ("robocopy devolvio {0} lineas que no cuelgan ni del origen ni del destino en '{1}'. Codigo {2}. La copia NO se da por buena. Primera: {3}" -f
                $sinUbicar.Count, $Origen, $codigo, $sinUbicar[0])
    }

    return [pscustomobject]@{
        Codigo        = $codigo
        Correcto      = $limpio
        CodigoCorrecto= ($codigo -le $script:RobocopyMaximoCorrecto)
        Origen        = $Origen
        Destino       = $Destino
        Clase         = $Clase
        Simulado      = [bool]$SoloListar
        ACopiar       = $aCopiar.ToArray()
        ABorrar       = $aBorrar.ToArray()
        SinClasificar = $sinUbicar.ToArray()
        NumACopiar    = $aCopiar.Count
        NumABorrar    = $aBorrar.Count
    }
}

function Get-RutaEnDestino {
    <#
        .SYNOPSIS
            Traduce una ruta del equipo a su ruta en el nodo o en el disco.
        .DESCRIPTION
            REGLA DE NOMBRES (seccion 8): la ruta en el destino ESPEJA la ruta en
            el equipo. Sin abreviaturas ingeniosas: en una restauracion de
            emergencia se sabe de donde vino cada archivo sin leer documentacion.

            La traduccion NO se adivina: sale de la tabla declarada en la
            configuracion, porque el arbol del nodo ya existe -lo poblo una
            persona el 2026-09-02- y una traduccion distinta crearia un arbol
            paralelo y duplicaria los datos en vez de actualizarlos.

            Se evalua de la traduccion mas larga a la mas corta, para que
            "C:\Users\usuario" gane sobre "C:".
        .PARAMETER RutaOrigen
            Ruta local, por ejemplo C:\Users\usuario\Documents\01_ACADEMICO
        .PARAMETER RaizDestino
            Raiz a la que colgar, ya con el prefijo de maquina.
        .PARAMETER Traducciones
            Lista de pares de/a, de la configuracion.
    #>
    [CmdletBinding()]
    [OutputType([string])]
    param(
        [Parameter(Mandatory)][ValidateNotNullOrEmpty()][string] $RutaOrigen,
        [Parameter(Mandatory)][ValidateNotNullOrEmpty()][string] $RaizDestino,
        [Parameter(Mandatory)][psobject[]] $Traducciones
    )

    $ordenadas = @($Traducciones | Sort-Object -Property @{ Expression = { $_.de.Length } } -Descending)
    foreach ($t in $ordenadas) {
        if ($RutaOrigen.StartsWith($t.de, [System.StringComparison]::OrdinalIgnoreCase)) {
            $resto = $RutaOrigen.Substring($t.de.Length).TrimStart('\')
            $partes = @($RaizDestino.TrimEnd('\'), $t.a.Trim('\'))
            if ($resto) { $partes += $resto }
            return ($partes -join '\')
        }
    }
    throw "No hay traduccion declarada para la ruta '$RutaOrigen'. Anadela a destinos.traduccionDeRutas antes de copiar nada."
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
        return (Get-FileHash -LiteralPath $Ruta -Algorithm $Algoritmo).Hash.ToLowerInvariant()
    }
}
