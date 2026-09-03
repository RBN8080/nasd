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

    # `cadencia` es OBLIGATORIA desde el cierre del pendiente 26. No tiene valor
    # por omision a proposito: de ella cuelgan el umbral del indicador, el color
    # de la tabla del tablero y cuantos latidos espera el testigo. Un valor por
    # omision silencioso significaria que un dia esos tres numeros describen una
    # cadencia distinta de la que corre de verdad, y ahi es donde el sistema
    # empieza a mentir.
    $obligatorias = @(
        'version', 'equipo', 'destinos', 'contenedores', 'raicesDeclaradas',
        'raicesDelNodo', 'exclusiones', 'freno', 'centinelas', 'cadencia',
        'deudaPrimeraCorrida'
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
        [string] $CarpetaRegistro = (Join-Path $env:LOCALAPPDATA 'NasRespaldo\registro'),

        # SE ESCRIBE AL ARCHIVO Y NO SE ECOA A LA CONSOLA.
        #
        # Existe por un caso medido y no por gusto: las carpetas sin numerar son
        # CATORCE en este equipo, y cada corrida escupia catorce ADVERTENCIA
        # identicas en forma que tapaban el veredicto de la opcion que se acababa
        # de pulsar. El tablero YA pinta "14 carpetas sin clasificar" tres
        # renglones mas arriba, asi que el muro repetia en catorce lineas lo que
        # ya estaba en una.
        #
        # LO QUE NO SE PIERDE, y es la mitad importante: la linea sigue yendo al
        # archivo del dia con su nivel ATENCION intacto. La seccion 3.4 exige que
        # lo que no lleva numero SE REPORTE y no se ignore en silencio; eso se
        # cumple en el registro, que es donde se va a mirar. Lo que se retira es
        # el eco, que es fatiga de alarmas de ISA-18.2 -- la misma norma que este
        # proyecto ya cita para que solo el rojo parpadee.
        [switch] $SoloArchivo
    )

    if (-not (Test-Path -LiteralPath $CarpetaRegistro)) {
        New-Item -ItemType Directory -Path $CarpetaRegistro -Force | Out-Null
    }
    $archivo = Join-Path $CarpetaRegistro ('respaldo-{0:yyyy-MM-dd}.log' -f (Get-Date))
    $linea = '{0} {1,-8} {2,-12} {3}' -f (Get-Date -Format 's'), $Nivel, $Etapa, $Mensaje
    Add-Content -LiteralPath $archivo -Value $linea -Encoding UTF8

    if ($SoloArchivo) {
        Write-Verbose -Message $Mensaje
        return
    }

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
        .PARAMETER Intentos
            Cuantas veces se pregunta antes de darlo por caido.
        .PARAMETER SegundosEntreIntentos
            Cuanto se espera entre preguntas.
        .PARAMETER Unc
            Raiz UNC del recurso, por ejemplo \\192.168.1.38\datos
    #>
    [CmdletBinding()]
    [OutputType([bool])]
    param(
        [Parameter(Mandatory)]
        [AllowEmptyString()]
        [string] $Unc,

        [ValidateRange(1, 20)][int] $Intentos = 5,
        [ValidateRange(1, 300)][int] $SegundosEntreIntentos = 30
    )

    # QUE NO SEA UNC NO SE REINTENTA: es un error de configuracion, y esperar no
    # lo arregla. Ademas robocopy escribiria feliz en un disco local con un
    # nombre que parece una direccion IP, asi que esto aborta al instante.
    if (-not (Test-RutaUnc -Ruta $Unc)) {
        Write-RegistroRespaldo -Nivel 'ERROR' -Etapa 'guarda' -Mensaje "El destino NO es una ruta UNC: '$Unc'. Abortado antes de escribir nada."
        return $false
    }
    # QUE EL NODO NO CONTESTE SI SE REINTENTA, y esto no es terquedad. El nodo es
    # una Raspberry Pi de la casa: se reinicia tras un corte de luz y tarda un
    # par de minutos en levantar la red y montar los discos. Preguntar UNA vez y
    # abortar convierte un reinicio normal en un respaldo perdido y en un icono
    # rojo que nadie va a mirar hasta el dia siguiente. Medido el 2026-09-02: el
    # nodo se cayo por un apagon y la corrida aborto al primer intento.
    for ($i = 1; $i -le $Intentos; $i++) {
        if (Test-Path -LiteralPath $Unc) {
            if ($i -gt 1) {
                Write-RegistroRespaldo -Etapa 'guarda' -Mensaje "El nodo respondio en el intento $i de $Intentos."
            }
            return $true
        }
        if ($i -lt $Intentos) {
            Write-RegistroRespaldo -Nivel 'ATENCION' -Etapa 'guarda' `
                -Mensaje "El nodo no responde en '$Unc' (intento $i de $Intentos). Se reintenta en $SegundosEntreIntentos s."
            Start-Sleep -Seconds $SegundosEntreIntentos
        }
    }

    Write-RegistroRespaldo -Nivel 'ERROR' -Etapa 'guarda' `
        -Mensaje "El nodo no responde en '$Unc' tras $Intentos intentos. Abortado antes de escribir nada."
    return $false
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

        # ARTEFACTO DE /MT, NO UN FALLO. Con ocho hilos escribiendo a la vez, la
        # salida de robocopy se entrelaza y de vez en cuando sale una linea
        # partida con bytes de control dentro -incluido NUL, que NO cuenta como
        # espacio en blanco-. Esa linea no cuelga del origen ni del destino, asi
        # que caia en el cubo de "sin clasificar" y TUMBABA LA CORRIDA ENTERA.
        # Medido el 2026-09-02: 'C:\dev' reportado como fallido con codigo 3
        # -copia correcta con sobrantes- por una linea que se veia vacia.
        #
        # Se limpian los caracteres de control ANTES de decidir si la linea esta
        # vacia. Una linea de error de verdad -"Acceso denegado"- tiene texto y
        # sobrevive a esta limpieza intacta: la guarda no se debilita.
        if (Test-LineaDeRobocopyVacia -Linea $texto) { continue }
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
            # Los tramos vacios se descartan. La pasada nodo -> disco traduce a
            # CADENA VACIA a proposito -en el disco se quita el nivel
            # `01_BACKUP/`, seccion 6.2.bis-, y sin este filtro saldria una barra
            # doble en mitad de la ruta.
            $partes = @($RaizDestino.TrimEnd('\'), $t.a.Trim('\'), $resto) |
                Where-Object { -not [string]::IsNullOrWhiteSpace($_) }
            return ($partes -join '\')
        }
    }
    throw "No hay traduccion declarada para la ruta '$RutaOrigen'. Anadela a destinos.traduccionDeRutas antes de copiar nada."
}

function Test-MotorIntacto {
    <#
        .SYNOPSIS
            Comprueba que las piezas del motor siguen ahi y que la ultima
            corrida programada no fallo.

        .DESCRIPTION
            POR QUE EXISTE, y no es una comprobacion generica.

            Este equipo tiene el antivirus con su modulo de cortafuegos activo, y desde
            que se empezo a construir el motor ha estado emitiendo alertas. El
            responsable las acoto el 2026-09-02: son de CONEXION A SERVIDORES
            -puertos que ya no son los que el antivirus espera-, no de deteccion
            sobre el codigo. O sea que hoy no hay nada que temer del contenido.

            LO QUE SI PUEDE PASAR, Y ES EL MOTIVO DE ESTA FUNCION: un antivirus
            que decide actuar no avisa al programa que se lleva por delante. Si
            pusiera en cuarentena respaldo.ps1, la tarea programada seguiria
            existiendo y el tablero seguiria diciendo "motor 3 ventanas Ready" --
            todo verde, y ni una copia mas. El fallo se veria semanas despues,
            que es la forma mas cara de enterarse.

            SE COMPRUEBA PRESENCIA, NO HUELLA, y es deliberado. Los archivos del
            motor cambian legitimamente cada vez que se trabaja en el, asi que un
            manifiesto de huellas sonaria en cada sesion de desarrollo -- un
            aviso que suena siempre es un aviso que se desactiva (ISA-18.2, la
            misma norma que este proyecto cita para el semaforo). Lo que un
            antivirus produce es DESAPARICION o TRUNCADO, y eso si es inequivoco.

            LA SEGUNDA MITAD ES LastTaskResult. Una tarea puede existir, aparecer
            "Ready" y haber terminado su ultima corrida con un codigo de error.
            Sin mirarlo, "la tarea existe" se lee como "la tarea funciona", que
            son dos afirmaciones distintas.

        .PARAMETER RaizProyecto
            Carpeta del cliente. Por omision, la que contiene a 2-Nucleo.

        .PARAMETER Tareas
            Nombres de las tareas programadas a comprobar.
    #>
    [CmdletBinding()]
    [OutputType([psobject])]
    param(
        [ValidateNotNullOrEmpty()]
        [string] $RaizProyecto = (Split-Path $PSScriptRoot -Parent),

        [string[]] $Tareas = @('NasRespaldo-Diario', 'NasRespaldo-Indicador')
    )

    # LAS PIEZAS SIN LAS QUE NO HAY RESPALDO. Se nombran una a una en vez de
    # recorrer la carpeta: recorrer no puede detectar una AUSENCIA, que es
    # justamente lo que hay que detectar.
    $piezas = @(
        '2-Nucleo\respaldo.ps1', '2-Nucleo\comun.ps1', '2-Nucleo\clasificar.ps1',
        '2-Nucleo\verificar.ps1', '2-Nucleo\disco.ps1', '2-Nucleo\semilla.ps1',
        '2-Nucleo\notificar.ps1', '2-Nucleo\testigo.ps1',
        '1-Interfaz\tablero.ps1', '1-Interfaz\indicador.ps1',
        '1-Interfaz\Registrar-Tarea.ps1', '1-Interfaz\estilo.ps1',
        '3-Config\respaldo.jsonc'
    )

    $faltan = New-Object System.Collections.Generic.List[string]
    foreach ($p in $piezas) {
        $ruta = Join-Path $RaizProyecto $p
        if (-not (Test-Path -LiteralPath $ruta -PathType Leaf)) {
            $faltan.Add("$p -- NO ESTA")
            continue
        }
        # Un archivo de cero bytes existe igual de bien que uno bueno, y no
        # sirve para nada. Es la misma comprobacion que ya hace la prueba de
        # restauracion sobre RESTAURAR.ps1.
        if ((Get-Item -LiteralPath $ruta).Length -eq 0) {
            $faltan.Add("$p -- VACIO")
        }
    }

    $tareasMal = New-Object System.Collections.Generic.List[string]
    foreach ($nombre in $Tareas) {
        $t = Get-ScheduledTask -TaskName $nombre -ErrorAction SilentlyContinue
        if (-not $t) { $tareasMal.Add("$nombre -- NO EXISTE"); continue }
        if ($t.State -eq 'Disabled') { $tareasMal.Add("$nombre -- DESHABILITADA"); continue }

        $info = $t | Get-ScheduledTaskInfo -ErrorAction SilentlyContinue
        if (-not $info) { continue }
        # 0 es correcto y 267009 es "corriendo ahora mismo", que tambien lo es.
        # Cualquier otro codigo es una corrida que termino mal.
        if ($info.LastTaskResult -ne 0 -and $info.LastTaskResult -ne 267009) {
            $tareasMal.Add(('{0} -- su ultima corrida termino con codigo {1}' -f $nombre, $info.LastTaskResult))
        }
    }

    return [pscustomobject]@{
        Intacto      = ($faltan.Count -eq 0 -and $tareasMal.Count -eq 0)
        PiezasTotal  = $piezas.Count
        PiezasFallan = $faltan.ToArray()
        TareasTotal  = @($Tareas).Count
        TareasFallan = $tareasMal.ToArray()
    }
}

function Get-CarpetaDeEstado {
    <#
        .SYNOPSIS
            Donde viven ESTADO.txt y EN_CURSO.lock. LOCAL, y es una decision.
        .DESCRIPTION
            La seccion 8 dibuja ESTADO.txt dentro de `_SISTEMA/` en el nodo, y
            alli se PUBLICA una copia para que una restauracion de emergencia
            pueda leerlo. Pero el original vive en el equipo, por dos razones que
            no son de comodidad:

              1. El indicador (seccion 10.2) tiene que pintar algo cuando el nodo
                 NO responde. Si su unica fuente estuviera en el nodo, un nodo
                 caido dejaria el icono sin datos -y un icono ausente se parece
                 a "todo bien", que es el punto ciego que la propia seccion 10.2
                 declara-.

              2. EN_CURSO.lock existe para detectar un motor que ARRANCO Y NUNCA
                 TERMINO. Si el motor muere a mitad, lo que queda vivo es el
                 equipo, no el nodo: la marca tiene que estar donde el indicador
                 la pueda ver aunque la red se haya caido con el motor.
        .PARAMETER Configuracion
            Si se da y declara `carpetaEstado`, manda ese valor. Existe para que
            las pruebas no escriban su ESTADO.txt encima del de produccion -paso
            el 2026-09-02 y el indicador pinto el resultado de una caja de
            arena-. La resolucion vive AQUI y no repetida en cada guion: dos
            copias de esta regla es una copia que un dia se queda atras.
    #>
    [CmdletBinding()]
    [OutputType([string])]
    param(
        [psobject] $Configuracion
    )
    if ($Configuracion -and
        ($Configuracion.PSObject.Properties.Name -contains 'carpetaEstado') -and
        $Configuracion.carpetaEstado) {
        return $Configuracion.carpetaEstado
    }
    return (Join-Path $env:LOCALAPPDATA 'NasRespaldo\estado')
}

function Write-EstadoRespaldo {
    <#
        .SYNOPSIS
            Escribe ESTADO.txt de forma atomica. Es lo que lee el indicador.
        .DESCRIPTION
            Formato de clave=valor, una por linea: legible por una persona en una
            emergencia y trivial de leer por el indicador sin analizador.
            Se escribe con Set-ContenidoAtomico porque el indicador lo esta
            leyendo mientras el motor lo reescribe.
        .PARAMETER Estado
            Protegido, Copiando, Atencion, Falla o SinDatos. Son los cinco de la
            tabla de la seccion 10.2, ni uno mas.
        .PARAMETER Detalle
            Una frase para la persona.
        .PARAMETER Datos
            Pares adicionales que el tablero y el indicador saben leer.
        .PARAMETER Carpeta
            Donde escribirlo.
    #>
    [CmdletBinding(SupportsShouldProcess)]
    [OutputType([void])]
    param(
        [Parameter(Mandatory)]
        [ValidateSet('Protegido', 'Copiando', 'Atencion', 'Falla', 'SinDatos')]
        [string] $Estado,

        [ValidateNotNull()]
        [string] $Detalle = '',

        [hashtable] $Datos = @{},

        [ValidateNotNullOrEmpty()]
        [string] $Carpeta = (Get-CarpetaDeEstado)
    )

    if (-not $PSCmdlet.ShouldProcess((Join-Path $Carpeta 'ESTADO.txt'), 'Escribir estado')) { return }

    # LO DE LOS OTROS SE CONSERVA. Esta funcion reescribia el archivo entero
    # desde cero, asi que cada corrida al nodo BORRABA el veredicto de la copia
    # fria y las fechas de comprobacion: el tablero decia 'ultima copia al
    # disco: nunca' y 'huellas: nunca' a los pocos minutos de haberlas hecho.
    # Medido el 2026-09-02 pintando la ventana y leyendola.
    #
    # Es el mismo error que Write-EstadoDelDisco existe para no cometer, visto
    # desde el otro lado. Cada destino y cada comprobacion son duenos de sus
    # claves, y quien escribe solo pisa las suyas.
    $previo = Read-EstadoRespaldo -Carpeta $Carpeta
    $heredadas = @{}
    foreach ($clave in $previo.Keys) {
        if ($clave -like 'disco_*' -or $clave -like 'huellas_*' -or
            $clave -like 'semilla_*' -or $clave -like 'restauracion_*') {
            if (-not $Datos.ContainsKey($clave)) { $heredadas[$clave] = $previo[$clave] }
        }
    }

    $sb = New-Object System.Text.StringBuilder
    [void]$sb.AppendLine('# ESTADO.txt - lo que lee el indicador (seccion 10.2)')
    [void]$sb.AppendLine('# Escrito de forma atomica: nunca se lee a medias.')
    [void]$sb.AppendLine(('estado={0}' -f $Estado))
    [void]$sb.AppendLine(('momento={0}' -f (Get-Date -Format 's')))
    [void]$sb.AppendLine(('detalle={0}' -f ($Detalle -replace '[\r\n]+', ' ')))
    foreach ($clave in ($heredadas.Keys | Sort-Object)) {
        [void]$sb.AppendLine(('{0}={1}' -f $clave, (('' + $heredadas[$clave]) -replace '[\r\n]+', ' ')))
    }
    foreach ($clave in ($Datos.Keys | Sort-Object)) {
        [void]$sb.AppendLine(('{0}={1}' -f $clave, (('' + $Datos[$clave]) -replace '[\r\n]+', ' ')))
    }

    Set-ContenidoAtomico -Ruta (Join-Path $Carpeta 'ESTADO.txt') -Contenido $sb.ToString() -Confirm:$false
}

function Read-EstadoRespaldo {
    <#
        .SYNOPSIS
            Lee ESTADO.txt. Devuelve SinDatos si no hay o si no se puede leer.
        .DESCRIPTION
            No lanza nunca. El indicador lo llama en bucle y una excepcion ahi
            mataria el icono, que es justo el punto ciego declarado: un icono
            ausente se parece a "todo bien".
        .PARAMETER Carpeta
            Donde buscarlo.
    #>
    [CmdletBinding()]
    [OutputType([hashtable])]
    param(
        [ValidateNotNullOrEmpty()]
        [string] $Carpeta = (Get-CarpetaDeEstado)
    )

    $vacio = @{ estado = 'SinDatos'; momento = ''; detalle = 'No hay ESTADO.txt todavia' }
    $ruta = Join-Path $Carpeta 'ESTADO.txt'
    try {
        if (-not (Test-Path -LiteralPath $ruta -PathType Leaf)) { return $vacio }
        $mapa = @{}
        foreach ($linea in (Get-Content -LiteralPath $ruta -Encoding UTF8 -ErrorAction Stop)) {
            if ($linea -match '^\s*#' -or $linea -notmatch '=') { continue }
            $par = $linea -split '=', 2
            $mapa[$par[0].Trim()] = $par[1]
        }
        if ($mapa.Count -eq 0) { return $vacio }
        return $mapa
    }
    catch {
        return $vacio
    }
}

function Enter-MarcaDeCorrida {
    <#
        .SYNOPSIS
            Pone EN_CURSO.lock. Existe SOLO mientras el motor copia.
        .DESCRIPTION
            Es la mitad de la deteccion de motor caido de la seccion 10.2: una
            marca PRESENTE PERO VIEJA significa "arranco y nunca termino", que es
            FALLA y no "copiando". Guarda dentro el identificador del proceso y
            la hora, para que el indicador pueda distinguir las dos cosas sin
            adivinar.
        .PARAMETER Carpeta
            Donde ponerla.
        .PARAMETER Tipo
            'nodo' -la que corre sola- o 'disco' -la copia fria-. SE GUARDA
            PORQUE LAS DOS DURAN COSAS DISTINTAS: la del nodo son minutos y la
            del disco puede ser horas, asi que el plazo a partir del cual una
            marca deja de ser creible no puede ser el mismo. Sin esto, una copia
            al disco perfectamente sana pintaba el icono de ROJO al pasar de
            hora y media.
    #>
    [CmdletBinding(SupportsShouldProcess)]
    [OutputType([string])]
    param(
        [ValidateNotNullOrEmpty()]
        [string] $Carpeta = (Get-CarpetaDeEstado),

        [ValidateSet('nodo', 'disco')]
        [string] $Tipo = 'nodo'
    )
    $ruta = Join-Path $Carpeta 'EN_CURSO.lock'
    if (-not $PSCmdlet.ShouldProcess($ruta, 'Marcar corrida en curso')) { return $ruta }
    $texto = "pid={0}`ninicio={1}`nequipo={2}`ntipo={3}`n" -f $PID, (Get-Date -Format 's'), $env:COMPUTERNAME, $Tipo
    Set-ContenidoAtomico -Ruta $ruta -Contenido $texto -Confirm:$false
    return $ruta
}

function Exit-MarcaDeCorrida {
    <#
        .SYNOPSIS
            Retira EN_CURSO.lock. Va SIEMPRE en un finally.
        .DESCRIPTION
            Si no se retira, el indicador vera una marca vieja y pintara FALLA.
            Eso es lo correcto cuando el motor murio de verdad, y una mentira si
            solo es que alguien olvido el finally.
        .PARAMETER Carpeta
            Donde estaba.
    #>
    [CmdletBinding(SupportsShouldProcess)]
    [OutputType([void])]
    param(
        [ValidateNotNullOrEmpty()]
        [string] $Carpeta = (Get-CarpetaDeEstado)
    )
    $ruta = Join-Path $Carpeta 'EN_CURSO.lock'
    if (-not (Test-Path -LiteralPath $ruta -PathType Leaf)) { return }
    if (-not $PSCmdlet.ShouldProcess($ruta, 'Retirar marca de corrida')) { return }
    Remove-Item -LiteralPath $ruta -Force -ErrorAction SilentlyContinue
}

function Get-MarcaDeCorrida {
    <#
        .SYNOPSIS
            Lee EN_CURSO.lock y dice si esta VIVA o VIEJA.
        .DESCRIPTION
            La tabla de la seccion 10.2, entera:
              marca presente y reciente            -> Copiando
              MARCA PRESENTE PERO VIEJA            -> arranco y nunca termino: FALLA
              sin marca                            -> no hay corrida en curso
        .PARAMETER Carpeta
            Donde buscarla.
        .PARAMETER MinutosParaVieja
            A partir de cuantos minutos una marca deja de ser creible.
    #>
    [CmdletBinding()]
    [OutputType([psobject])]
    param(
        [ValidateNotNullOrEmpty()]
        [string] $Carpeta = (Get-CarpetaDeEstado),
        [ValidateRange(1, 1440)][int] $MinutosParaVieja = 90,

        # La copia fria mueve decenas de GB por un enlace que pasa por el equipo
        # (seccion 6.2): ocho horas es holgado a proposito. Quien delata una
        # corrida muerta de verdad es el PID, no el reloj.
        [ValidateRange(1, 1440)][int] $MinutosParaViejaDisco = 480
    )

    $ruta = Join-Path $Carpeta 'EN_CURSO.lock'
    if (-not (Test-Path -LiteralPath $ruta -PathType Leaf)) {
        return [pscustomobject]@{ Existe = $false; Vieja = $false; Pid = 0; Inicio = $null; Minutos = 0 }
    }
    $mapa = @{}
    foreach ($linea in (Get-Content -LiteralPath $ruta -Encoding UTF8 -ErrorAction SilentlyContinue)) {
        if ($linea -notmatch '=') { continue }
        $par = $linea -split '=', 2
        $mapa[$par[0].Trim()] = $par[1].Trim()
    }
    # TryParse exige que la variable YA tenga el tipo: con $null, PowerShell no
    # puede resolver la sobrecarga de [ref] y lanza "no se encuentra ninguna
    # sobrecarga". Se declaran tipadas antes de usarlas.
    [datetime] $inicio = [datetime]::MinValue
    $hayInicio = $false
    if ($mapa.ContainsKey('inicio')) { $hayInicio = [datetime]::TryParse($mapa['inicio'], [ref]$inicio) }
    $minutos = if ($hayInicio) { [int]((Get-Date) - $inicio).TotalMinutes } else { [int]::MaxValue }
    $tipo = if ($mapa.ContainsKey('tipo')) { $mapa['tipo'] } else { 'nodo' }
    $plazo = if ($tipo -eq 'disco') { $MinutosParaViejaDisco } else { $MinutosParaVieja }
    [int] $procesoId = 0
    if ($mapa.ContainsKey('pid')) { [void][int]::TryParse($mapa['pid'], [ref]$procesoId) }

    # Dos senales, y la del proceso manda: si el PID ya no existe, la corrida
    # murio aunque la marca sea de hace un minuto. Matar el motor a media corrida
    # es el criterio 9 de la seccion 15, y esta es la unica forma de verlo rapido.
    $procesoVivo = $false
    if ($procesoId -gt 0) {
        $procesoVivo = $null -ne (Get-Process -Id $procesoId -ErrorAction SilentlyContinue)
    }

    return [pscustomobject]@{
        Existe      = $true
        Tipo        = $tipo
        Vieja       = ((-not $procesoVivo) -or ($minutos -ge $plazo))
        ProcesoVivo = $procesoVivo
        Pid         = $procesoId
        Inicio      = $(if ($hayInicio) { $inicio } else { $null })
        Minutos     = $minutos
    }
}


# ---------------------------------------------------------------------------
#  La cadencia de las corridas  -  el pendiente 26, decidido CON DATOS
#
#  DE UNA CORRIDA DIARIA A LAS 22:30 -HORA QUE NADIE MIDIO- A TRES CORRIDAS EN
#  VENTANAS MEDIDAS. La decision salio del registro de eventos de Windows de los
#  30 dias del 2026-08-03 al 2026-09-02, no de una intuicion:
#
#    - La PC esta DESPIERTA el 94.1 % de las horas. Mediana de 23.99 h al dia y
#      CERO dias con 0 h. No es un equipo que se enciende y se apaga: son 22
#      sesiones en 30 dias -0.73 al dia- y la mas larga duro 211.7 h.
#    - NO SE SUSPENDE. Los 25 `Kernel-Power 42` tienen su `107` entre 3 y 8
#      segundos despues: intenta dormirse a diario y algo la despierta al
#      instante. Suspension real acumulada en 30 dias: unos 2 minutos.
#    - De los 22 apagados limpios, 17 son REINICIOS: mediana del hueco 0.3 min.
#    - Lo que si pasa son 15 APAGONES INESPERADOS, uno cada dos dias, mediana
#      19 min caida. Ese, y no el horario, es el enemigo -y es el modo de fallo
#      que persigue la seccion 12.duodecies-.
#
#  POR QUE TRES VENTANAS Y NO CUATRO, tambien medido: simulando 200 meses a
#  resolucion de minuto sobre esos mismos datos, el peor hueco entre corridas
#  buenas queda en 18.4 h con tres y SUBE a 23.4 h con cuatro. El peor caso no
#  lo pone el reparto de ventanas sino el unico apagon largo de 15.2 h del
#  29-30/08, y la cuarta ventana solo cabe en la banda 08:00-11:00, que es la
#  peor del dia. Mas corridas habrian empeorado el peor caso.
#
#  EL SORTEO LO HACE EL PROPIO PROGRAMADOR DE TAREAS, con `RandomDelay` igual al
#  largo de la ventana. LA PROMESA ES LA VENTANA, NO UN MINUTO, y eso costo una
#  correccion el mismo dia: se creyo que `NextRunTime` devolvia el minuto ya
#  sorteado -tres registros seguidos daban 04:30:00, 04:58:50 y 05:26:17- y era
#  falso. Leerlo diez veces seguidas SIN TOCAR NADA da diez horas distintas: el
#  Programador sortea en cada consulta y solo fija el minuto al disparar. Lo que
#  devuelve es una MUESTRA de la ventana. Ver Get-ProximaVentanaDeCorrida.
#
#  La condicion del responsable se cumple igual, y con la ventana basta:
#    antes del inicio      todavia no le tocaba
#    dentro de la ventana  le toca, puede caer en cualquier momento
#    pasado el fin         NO CORRIO
#  El minuto exacto lo aporta el registro DESPUES, con la hora real y su desfase.
#
#  Y por eso no hay semilla ni archivo de sal: fijar el minuto de antemano
#  obligaria a re-registrar los disparadores cada dia, y un disparador que se
#  re-registra a si mismo es un disparador que un dia no se re-registra.
# ---------------------------------------------------------------------------

function Get-CadenciaDeCorrida {
    <#
        .SYNOPSIS
            Lee y normaliza el bloque `cadencia` de la configuracion.
        .DESCRIPTION
            EL HUECO MAXIMO NO SE DECLARA: SE CALCULA. Es la distancia entre el
            principio de una ventana y el final de la siguiente, o sea lo peor
            que puede tardar el sistema en volver a copiar. Si estuviera escrito
            en el archivo, alguien moveria una ventana y el numero se quedaria
            atras -y de ese numero cuelgan el umbral del indicador y lo que
            espera el testigo-. Un valor que puede mentir sobre la propia
            proteccion no se guarda: se deriva.

            `horasParaAvisar` SI se declara, porque no se deriva de nada: es una
            politica. Su valor de hoy -22 h- sale de que el peor hueco medido
            entre corridas buenas fue de 18.4 h (p90 18.2, mediana 17.0) sobre
            200 meses simulados, y de que 22 h significa exactamente "se
            perdieron dos ventanas seguidas".
        .PARAMETER Configuracion
            El objeto de configuracion completo.
    #>
    [CmdletBinding()]
    [OutputType([psobject])]
    param(
        [Parameter(Mandatory)][psobject] $Configuracion
    )

    if ($Configuracion.PSObject.Properties.Name -notcontains 'cadencia') {
        throw 'La configuracion no declara "cadencia". Sin ella el sistema no sabe cuantas corridas esperar, y el testigo no puede distinguir "todavia no le tocaba" de "no corrio".'
    }
    $c = $Configuracion.cadencia
    $ventanasCrudas = @($c.ventanas)
    if ($ventanasCrudas.Count -lt 1) {
        throw 'La configuracion declara "cadencia" sin ninguna ventana.'
    }

    $lista = New-Object System.Collections.Generic.List[psobject]
    $indice = 0
    foreach ($v in $ventanasCrudas) {
        $indice++
        if (('' + $v.inicio) -notmatch '^([01][0-9]|2[0-3]):([0-5][0-9])$') {
            throw "La ventana $indice tiene un inicio invalido: '$($v.inicio)'. Se esperaba HH:mm."
        }
        $horas = [double]$v.duracionHoras
        if ($horas -le 0 -or $horas -gt 12) {
            throw "La ventana $indice dura $horas h. Fuera de rango: una ventana de 0 h no sortea nada, y una de mas de 12 h no es una ventana."
        }
        $partes    = ('' + $v.inicio) -split ':'
        $inicioMin = ([int]$partes[0]) * 60 + [int]$partes[1]
        $largoMin  = [int][math]::Round($horas * 60)
        $finMin    = $inicioMin + $largoMin
        $lista.Add([pscustomobject]@{
                Indice        = $indice
                Inicio        = '' + $v.inicio
                InicioMinutos = $inicioMin
                LargoMinutos  = $largoMin
                FinMinutos    = $finMin
                # Los parentesis NO sobran: dentro de un literal de hash, la coma
                # del operador -f se lee como separador de la tabla y PowerShell
                # deja de analizar la linea.
                Fin           = ('{0:00}:{1:00}' -f ([int]($finMin / 60) % 24), ($finMin % 60))
                RandomDelay   = ('PT{0}M' -f $largoMin)
            })
    }
    $ventanas = @($lista | Sort-Object InicioMinutos)
    # Se renumera DESPUES de ordenar: el indice que sale en el registro tiene que
    # ser el orden del dia, no el orden en que estan escritas en el archivo.
    for ($i = 0; $i -lt $ventanas.Count; $i++) { $ventanas[$i].Indice = $i + 1 }

    # DOS VENTANAS SOLAPADAS SE PIERDEN EN SILENCIO. Con MultipleInstances a
    # IgnoreNew, la segunda corrida no se encola ni avisa: el Programador la
    # descarta. Mejor no arrancar que copiar la mitad de las veces que se cree.
    for ($i = 1; $i -lt $ventanas.Count; $i++) {
        if ($ventanas[$i].InicioMinutos -lt $ventanas[$i - 1].FinMinutos) {
            throw ("Las ventanas {0} y {1} se solapan ({2}-{3} y {4}-{5}). Dos corridas solapadas se pierden en silencio por MultipleInstances=IgnoreNew." -f `
                    $ventanas[$i - 1].Indice, $ventanas[$i].Indice, `
                    $ventanas[$i - 1].Inicio, $ventanas[$i - 1].Fin, `
                    $ventanas[$i].Inicio, $ventanas[$i].Fin)
        }
    }
    if ($ventanas[$ventanas.Count - 1].FinMinutos -gt 1440) {
        throw 'La ultima ventana se pasa de la medianoche. Una ventana que cruza el dia no la puede expresar un disparador diario.'
    }

    # EL HUECO MAXIMO, CALCULADO. Del principio de una ventana al final de la
    # siguiente, dando la vuelta al dia en la ultima.
    $hueco = 0
    for ($i = 0; $i -lt $ventanas.Count; $i++) {
        $sig    = ($i + 1) % $ventanas.Count
        $finSig = $ventanas[$sig].FinMinutos
        if ($sig -le $i) { $finSig += 1440 }
        $g = $finSig - $ventanas[$i].InicioMinutos
        if ($g -gt $hueco) { $hueco = $g }
    }

    $avisar = 22
    if (($c.PSObject.Properties.Name -contains 'horasParaAvisar') -and $c.horasParaAvisar) {
        $avisar = [int]$c.horasParaAvisar
    }
    # UN UMBRAL POR DEBAJO DEL HUECO NOMINAL AVISA DE ALGO QUE NO HA PASADO, y es
    # la fatiga de alarmas de ISA-18.2 que este contrato ya cita para el semaforo
    # y para los avisos: una alarma que suena con el sistema sano deja de leerse.
    if (($avisar * 60) -le $hueco) {
        throw ("horasParaAvisar ({0} h) no puede ser menor o igual que el hueco recibol maximo ({1:N1} h): avisaria de corridas que todavia no tocaban." -f `
                $avisar, ($hueco / 60))
    }

    return [pscustomobject]@{
        Ventanas                = $ventanas
        CorridasPorDia          = $ventanas.Count
        HuecoNominalMaximoHoras = [math]::Round($hueco / 60, 2)
        HorasParaAvisar         = $avisar
        Resumen                 = (($ventanas | ForEach-Object { '{0}-{1}' -f $_.Inicio, $_.Fin }) -join ', ')
    }
}

function Get-VentanaDeCorrida {
    <#
        .SYNOPSIS
            A que ventana pertenece una corrida, y si llego a tiempo o tarde.
        .DESCRIPTION
            ESTO ES LO QUE HACE AUDITABLE LA ALEATORIEDAD. Con una hora fija
            bastaba un registro que dijera "corrio a las 22:31". Con un minuto
            sorteado dentro de una ventana, "corrio a las 05:26" no dice nada por
            si solo: hay que poder afirmar que 05:26 CAIA DENTRO de la ventana
            que le tocaba, o que no caia -y entonces es una corrida recuperada
            por StartWhenAvailable despues de un apagon-.

            Una corrida fuera de toda ventana NO es un error: es exactamente lo
            que se decidio que pasara cuando la PC estaba muerta a su hora. Lo
            que no puede pasar es que no se distinga de una a tiempo.
        .PARAMETER Momento
            Cuando arranco la corrida.
        .PARAMETER Cadencia
            La que devuelve Get-CadenciaDeCorrida.
    #>
    [CmdletBinding()]
    [OutputType([psobject])]
    param(
        [Parameter(Mandatory)][datetime] $Momento,
        [Parameter(Mandatory)][psobject] $Cadencia
    )

    $min    = $Momento.Hour * 60 + $Momento.Minute
    $dentro = @($Cadencia.Ventanas | Where-Object { $min -ge $_.InicioMinutos -and $min -lt $_.FinMinutos })

    if ($dentro.Count -gt 0) {
        $v = $dentro[0]
        return [pscustomobject]@{
            Indice      = $v.Indice
            Total       = $Cadencia.CorridasPorDia
            Ventana     = '{0}-{1}' -f $v.Inicio, $v.Fin
            ATiempo     = $true
            Desfase     = ($min - $v.InicioMinutos)
            Descripcion = ('ventana {0}/{1} [{2}-{3}] - corrio {4:HH:mm} - A TIEMPO, {5} min dentro de la ventana' -f `
                    $v.Indice, $Cadencia.CorridasPorDia, $v.Inicio, $v.Fin, $Momento, ($min - $v.InicioMinutos))
        }
    }

    # Fuera de toda ventana: se atribuye a la ULTIMA QUE YA HABIA EMPEZADO, que
    # es la que quedo sin correr. Si ninguna empezo todavia hoy -madrugada, antes
    # de la primera- la que quedo colgando es la ULTIMA DE AYER.
    $previas = @($Cadencia.Ventanas | Where-Object { $_.FinMinutos -le $min } | Sort-Object FinMinutos)
    if ($previas.Count -gt 0) {
        $v       = $previas[$previas.Count - 1]
        $retraso = $min - $v.FinMinutos
    }
    else {
        $v       = $Cadencia.Ventanas[$Cadencia.Ventanas.Count - 1]
        $retraso = ($min + 1440) - $v.FinMinutos
    }
    return [pscustomobject]@{
        Indice      = $v.Indice
        Total       = $Cadencia.CorridasPorDia
        Ventana     = '{0}-{1}' -f $v.Inicio, $v.Fin
        ATiempo     = $false
        Desfase     = $retraso
        Descripcion = ('ventana {0}/{1} [{2}-{3}] - corrio {4:HH:mm} - TARDE, {5} h {6} min despues de cerrarse la ventana (corrida recuperada)' -f `
                $v.Indice, $Cadencia.CorridasPorDia, $v.Inicio, $v.Fin, $Momento, [int]($retraso / 60), ($retraso % 60))
    }
}

function Get-ProximaVentanaDeCorrida {
    <#
        .SYNOPSIS
            Cual es la proxima ventana de corrida, y si la tarea sigue viva.
        .DESCRIPTION
            LA PROMESA ES LA VENTANA, NO UN MINUTO, Y ESO SE MIDIO.

            El 2026-09-02 se creyo lo contrario y quedo escrito en tres sitios:
            que `NextRunTime` devolvia el minuto YA SORTEADO y que por tanto la
            hora exacta se podia leer antes de que ocurriera. La evidencia
            parecia clara -tres registros seguidos del disparador de las 04:00
            con RandomDelay de 3 h dieron 04:30:00, 04:58:50 y 05:26:17-.

            ERA FALSO, Y LO DESTAPO LEERLO DIEZ VECES SEGUIDAS SIN TOCAR NADA:
            21:21:33, 20:00:53, 21:28:03, 20:10:17, 20:32:43, 19:25:52,
            20:50:13, 21:58:33, 20:24:36, 20:58:08. El Programador SORTEA EL
            RETRASO EN CADA CONSULTA y solo lo fija cuando el disparador dispara
            de verdad. Lo que devuelve `NextRunTime` es UNA MUESTRA de la
            ventana, no un compromiso.

            Publicar esa muestra como "proxima corrida: 20:27" habria sido
            exactamente la clase de mentira que este contrato existe para
            impedir: un dato que se lee como una promesa y que cambia solo.

            LO QUE SI ES CIERTO, Y BASTA. La ventana se sabe de antemano, y con
            ella se distinguen las tres cosas que hay que distinguir:
              antes del inicio      todavia no le tocaba
              dentro de la ventana  le toca, puede caer en cualquier momento
              pasado el fin         NO CORRIO
            Esa era la condicion del responsable, y una ventana la cumple entera.
            El minuto exacto lo aporta el registro DESPUES, con la hora real y su
            desfase (Get-VentanaDeCorrida).

            POR QUE NO SE VUELVE A UNA SEMILLA. Fijar el minuto de antemano
            exigiria calcularlo con una semilla y RE-REGISTRAR los disparadores
            cada dia. Un disparador que se re-registra a si mismo es un
            disparador que un dia no se re-registra, y entonces el respaldo deja
            de correr sin que nadie lo note. Se cambiaria una fragilidad real por
            una precision que el criterio no pide.

            NO LANZA NUNCA. El tablero y el indicador la llaman, y ninguno de los
            dos puede morirse porque el Programador no conteste -un icono ausente
            se parece a "todo bien", que es el punto ciego de la seccion 10.2-.
        .PARAMETER Cadencia
            La que devuelve Get-CadenciaDeCorrida.
        .PARAMETER NombreTarea
            La tarea del motor.
        .PARAMETER Momento
            Desde cuando se pregunta. Parametrizado para poder probarlo sin
            depender de la hora a la que corran las pruebas.
    #>
    [CmdletBinding()]
    [OutputType([psobject])]
    param(
        [Parameter(Mandatory)][psobject] $Cadencia,

        [ValidateNotNullOrEmpty()]
        [string] $NombreTarea = 'NasRespaldo-Diario',

        [datetime] $Momento = (Get-Date)
    )

    $existe = $false
    $habilitada = $false
    $disparadores = 0
    $motivo = $null
    try {
        $t = Get-ScheduledTask -TaskName $NombreTarea -ErrorAction SilentlyContinue
        if ($t) {
            $existe = $true
            $habilitada = ($t.State -ne 'Disabled')
            $disparadores = @($t.Triggers).Count
        }
        else { $motivo = 'la tarea no existe: el respaldo no corre solo' }
    }
    catch { $motivo = $_.Exception.Message }

    $min = $Momento.Hour * 60 + $Momento.Minute

    # Dentro de una ventana: le toca AHORA, y lo que importa es cuando se cierra.
    $dentro = @($Cadencia.Ventanas | Where-Object { $min -ge $_.InicioMinutos -and $min -lt $_.FinMinutos })
    if ($dentro.Count -gt 0) {
        $v = $dentro[0]
        return [pscustomobject]@{
            Existe       = $existe
            Habilitada   = $habilitada
            Disparadores = $disparadores
            Motivo       = $motivo
            EnVentana    = $true
            Ventana      = ('{0}-{1}' -f $v.Inicio, $v.Fin)
            Inicio       = $Momento.Date.AddMinutes($v.InicioMinutos)
            Fin          = $Momento.Date.AddMinutes($v.FinMinutos)
            Descripcion  = ('le toca AHORA, ventana {0}/{1} hasta las {2}' -f $v.Indice, $Cadencia.CorridasPorDia, $v.Fin)
        }
    }

    # Si no, la siguiente que empiece hoy; y si no queda ninguna, la primera de
    # manana. Sin este segundo caso, a las 23:00 el tablero se quedaria mudo.
    $siguientes = @($Cadencia.Ventanas | Where-Object { $_.InicioMinutos -gt $min } | Sort-Object InicioMinutos)
    if ($siguientes.Count -gt 0) {
        $v = $siguientes[0]
        $dia = $Momento.Date
        $cuando = 'hoy'
    }
    else {
        $v = $Cadencia.Ventanas[0]
        $dia = $Momento.Date.AddDays(1)
        $cuando = 'manana'
    }
    return [pscustomobject]@{
        Existe       = $existe
        Habilitada   = $habilitada
        Disparadores = $disparadores
        Motivo       = $motivo
        EnVentana    = $false
        Ventana      = ('{0}-{1}' -f $v.Inicio, $v.Fin)
        Inicio       = $dia.AddMinutes($v.InicioMinutos)
        Fin          = $dia.AddMinutes($v.FinMinutos)
        Descripcion  = ('{0} entre {1} y {2}, ventana {3}/{4}' -f $cuando, $v.Inicio, $v.Fin, $v.Indice, $Cadencia.CorridasPorDia)
    }
}

# ---------------------------------------------------------------------------
#  Capa 3 - los centinelas.
#
#  VIVEN AQUI Y NO EN respaldo.ps1 POR UNA RAZON MEDIDA EL 2026-09-02, y cara:
#  disco.ps1 los necesitaba y los alcanzaba cargando respaldo.ps1 con punto.
#  Cargar con punto un guion que tiene param() DECLARA SUS VARIABLES CON EL
#  VALOR POR OMISION en el ambito que lo carga: eso puso a $SoloSimular en
#  $false justo antes de usarlo, y una "simulacion" copio de verdad al disco
#  frio. Lo compartido va en comun.ps1; ningun guion carga a otro guion.
# ---------------------------------------------------------------------------

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
        .PARAMETER Centinelas
            Lista de la configuracion, con ruta y huella esperada.
    #>
    [CmdletBinding()]
    [OutputType([psobject])]
    param(
        [Parameter(Mandatory)][AllowEmptyCollection()][psobject[]] $Centinelas
    )

    $fallos = New-Object System.Collections.Generic.List[psobject]
    foreach ($c in $Centinelas) {
        if (-not (Test-Path -LiteralPath $c.ruta -PathType Leaf)) {
            $fallos.Add([pscustomobject]@{ Ruta = $c.ruta; Motivo = 'FALTA' })
            continue
        }
        $actual = Get-HuellaDeArchivo -Ruta $c.ruta
        if ($actual -ne $c.huella.ToLowerInvariant()) {
            $fallos.Add([pscustomobject]@{ Ruta = $c.ruta; Motivo = 'HUELLA DISTINTA' })
        }
    }
    return [pscustomobject]@{
        Revisados = @($Centinelas).Count
        Fallos    = $fallos.ToArray()
        Correcto  = ($fallos.Count -eq 0)
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
        return (Get-FileHash -LiteralPath $Ruta -Algorithm $Algoritmo).Hash.ToLowerInvariant()
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


function Hide-VentanaDeConsola {
    <#
        .SYNOPSIS
            Esconde la ventana negra de consola, PERO SOLO SI ES NUESTRA.

        .DESCRIPTION
            -WindowStyle Hidden NO BASTA, y se midio el 2026-09-02: la tarea se
            registro con esa bandera y la ventana aparecia igual. PowerShell CREA
            la consola y despues intenta ocultarla, asi que segun como arranque el
            proceso la ventana se queda en el escritorio.

            LA CONDICION ES LO IMPORTANTE. GetConsoleProcessList dice cuantos
            procesos comparten esta consola:

              1 proceso   la consola la creo este proceso al arrancar -tarea
                          programada, lanzador externo-. Es nuestra: se esconde.
              2 o mas     alguien la tenia abierta y lanzo esto desde ahi.
                          NO SE TOCA: esconderla le cerraria la terminal en la
                          cara a quien esta trabajando, y ademas se perderia la
                          salida que fue a ver.

            Sin esa distincion habria que elegir entre molestar a diario o no
            poder correr el motor a mano, y las dos son malas.
    #>
    [Diagnostics.CodeAnalysis.SuppressMessageAttribute(
        'PSUseShouldProcessForStateChangingFunctions', '',
        Justification = 'Oculta la ventana de SU PROPIO proceso y solo cuando nadie mas la comparte. No cambia nada persistente: ni disco, ni registro, ni configuracion. Pedir confirmacion para esconder una ventana al arrancar seria exactamente la ventana que se quiere evitar.')]
    [CmdletBinding()]
    [OutputType([bool])]
    param()

    try {
        if (-not ('NasRespaldo.VentanaPropia' -as [type])) {
            Add-Type -Namespace 'NasRespaldo' -Name 'VentanaPropia' -MemberDefinition @'
[System.Runtime.InteropServices.DllImport("kernel32.dll")]
public static extern System.IntPtr GetConsoleWindow();

[System.Runtime.InteropServices.DllImport("kernel32.dll")]
public static extern uint GetConsoleProcessList(uint[] lpdwProcessList, uint dwProcessCount);

[System.Runtime.InteropServices.DllImport("user32.dll")]
public static extern bool ShowWindow(System.IntPtr hWnd, int nCmdShow);
'@
        }

        $ventana = [NasRespaldo.VentanaPropia]::GetConsoleWindow()
        # Sin consola no hay nada que esconder.
        if ($ventana -eq [System.IntPtr]::Zero) { return $false }

        $lista = New-Object uint32[] 8
        $cuantos = [NasRespaldo.VentanaPropia]::GetConsoleProcessList($lista, 8)
        if ($cuantos -ne 1) { return $false }

        # 0 es SW_HIDE.
        return [NasRespaldo.VentanaPropia]::ShowWindow($ventana, 0)
    }
    catch {
        # Que no se pueda esconder NO es motivo para no respaldar ni para
        # quedarse sin icono. Se sigue, y se deja constancia en verboso.
        Write-Verbose "No se pudo esconder la consola: $($_.Exception.Message)"
        return $false
    }
}


function Write-EstadoDelDisco {
    <#
        .SYNOPSIS
            Anota como quedo la copia fria SIN tocar el veredicto del nodo.

        .DESCRIPTION
            DOS DESTINOS, DOS SALUDES, Y CONFUNDIRLAS BORRO UN FALLO REAL. Hasta
            el 2026-09-02 la copia al disco escribia `estado=Protegido` al
            terminar bien. Ese dia la corrida al nodo habia ABORTADO -el nodo no
            respondia- y el icono estaba en rojo, correctamente; al copiar al
            disco, el rojo se puso verde y el fallo del nodo desaparecio sin que
            nadie lo hubiera arreglado.

            Que la copia fria haya ido bien no dice NADA sobre si el respaldo
            diario al nodo funciona. Son dos destinos independientes (seccion
            6), asi que el disco escribe sus propias claves -`disco_*`- y el
            veredicto principal, el que pinta el icono, sigue siendo el del
            nodo.

            SE LEE Y SE FUNDE, no se sobrescribe: cualquier otra clave que
            hubiera en ESTADO.txt se conserva.
        .PARAMETER Estado
            Como quedo la copia fria.
        .PARAMETER Detalle
            Una frase para la persona.
        .PARAMETER Carpeta
            Donde vive ESTADO.txt.
    #>
    [CmdletBinding(SupportsShouldProcess)]
    [OutputType([void])]
    param(
        [Parameter(Mandatory)]
        [ValidateSet('Protegido', 'Copiando', 'Atencion', 'Falla', 'SinDatos')]
        [string] $Estado,

        [ValidateNotNull()]
        [string] $Detalle = '',

        [ValidateNotNullOrEmpty()]
        [string] $Carpeta = (Get-CarpetaDeEstado)
    )

    $previo = Read-EstadoRespaldo -Carpeta $Carpeta

    $datos = @{}
    foreach ($clave in $previo.Keys) {
        if ($clave -in @('estado', 'momento', 'detalle')) { continue }
        if ($clave -like 'disco_*') { continue }
        $datos[$clave] = $previo[$clave]
    }
    $datos['disco_estado']  = $Estado
    $datos['disco_detalle'] = $Detalle
    $datos['disco_momento'] = (Get-Date -Format 's')

    # El veredicto principal se conserva TAL CUAL. Si no habia ninguno todavia,
    # SinDatos: decir "protegido" porque el disco fue bien seria justo la
    # mentira que esta funcion existe para impedir.
    $estadoPrincipal = '' + $previo['estado']
    if ($estadoPrincipal -notin @('Protegido', 'Copiando', 'Atencion', 'Falla', 'SinDatos')) {
        $estadoPrincipal = 'SinDatos'
    }
    $detallePrincipal = '' + $previo['detalle']

    if (-not $PSCmdlet.ShouldProcess((Join-Path $Carpeta 'ESTADO.txt'), 'Anotar el resultado del disco frio')) { return }

    # Se reescribe entero -es un archivo de tres lineas y pico- pero conservando
    # momento, que es cuando corrio EL NODO y no cuando corrio el disco.
    $sb = New-Object System.Text.StringBuilder
    [void]$sb.AppendLine('# ESTADO.txt - lo que lee el indicador (seccion 10.2)')
    [void]$sb.AppendLine('# Escrito de forma atomica: nunca se lee a medias.')
    [void]$sb.AppendLine(('estado={0}' -f $estadoPrincipal))
    [void]$sb.AppendLine(('momento={0}' -f ('' + $previo['momento'])))
    [void]$sb.AppendLine(('detalle={0}' -f ($detallePrincipal -replace '[\r\n]+', ' ')))
    foreach ($clave in ($datos.Keys | Sort-Object)) {
        [void]$sb.AppendLine(('{0}={1}' -f $clave, (('' + $datos[$clave]) -replace '[\r\n]+', ' ')))
    }
    Set-ContenidoAtomico -Ruta (Join-Path $Carpeta 'ESTADO.txt') -Contenido $sb.ToString() -Confirm:$false
}


function Test-LineaDeRobocopyVacia {
    <#
        .SYNOPSIS
            Dice si una linea de robocopy no lleva informacion.

        .DESCRIPTION
            ES UNA FUNCION Y NO UNA LINEA SUELTA PARA QUE SE PUEDA PROBAR. El
            criterio decide si una linea rara se ignora o TUMBA LA CORRIDA
            ENTERA, asi que merece una prueba propia.

            Con /MT robocopy escribe desde ocho hilos y la salida se entrelaza:
            de vez en cuando sale una linea partida con bytes de control dentro,
            NUL incluido. NUL no cuenta como espacio en blanco, asi que
            IsNullOrWhiteSpace la daba por buena, no colgaba del origen ni del
            destino, y el motor declaraba fallida una copia correcta. Medido el
            2026-09-02 sobre 'C:\dev' con codigo 3.

            LO QUE NO SE PUEDE PERDER: una linea con texto de verdad -"Acceso
            denegado"- tiene que seguir contando. Por eso solo se quitan
            caracteres de control, nunca texto.
        .PARAMETER Linea
            La linea tal cual la escribio robocopy.
    #>
    [CmdletBinding()]
    [OutputType([bool])]
    param(
        [Parameter(Mandatory)][AllowEmptyString()][AllowNull()][string] $Linea
    )
    if ($null -eq $Linea) { return $true }
    return [string]::IsNullOrWhiteSpace(($Linea -replace '[\x00-\x08\x0B\x0C\x0E-\x1F]', ''))
}


function Resolve-EstadoVigente {
    <#
        .SYNOPSIS
            Ajusta un veredicto guardado a lo que sigue siendo cierto AHORA.

        .DESCRIPTION
            UN ROJO CUYA CAUSA YA NO EXISTE NO PUEDE SEGUIR SIENDO ROJO. El
            2026-09-02 el nodo se cayo por un apagon, la corrida aborto y el
            icono se puso rojo -correctamente-. Cuando el nodo volvio, el icono
            seguia parpadeando en rojo mientras el tablero, en la misma
            pantalla, decia "Nodo: responde". Dos cosas contradictorias a la vez
            no son un estado: son un semaforo roto.

            TRES COSAS DISTINTAS DONDE ANTES HABIA DOS:

              Rojo    esto esta roto AHORA
              Ambar   la ultima corrida fallo, la causa ya no existe, FALTA
                      una corrida
              Verde   hubo una corrida buena

            El ambar no miente en ninguna direccion: no dice que estes protegido
            -no lo estas hasta que corra- ni grita por algo que ya paso. El rojo
            se reserva para lo que sigue roto, que es lo unico que lo mantiene
            util (ISA-18.2, fatiga de alarmas).

            ES UNA FUNCION PURA A PROPOSITO: recibe la medicion, no la hace. Asi
            se puede probar sin nodo, sin disco y sin barra de tareas.

        .PARAMETER Estado
            El veredicto guardado.
        .PARAMETER Causa
            El token de por que aborto, si aborto.
        .PARAMETER DestinoResponde
            Si el destino responde AHORA MISMO.
    #>
    [CmdletBinding()]
    [OutputType([psobject])]
    param(
        [Parameter(Mandatory)][AllowEmptyString()][string] $Estado,
        [AllowEmptyString()][AllowNull()][string] $Causa = '',
        [bool] $DestinoResponde = $false
    )

    # La UNICA causa que se cura sola es que el destino no respondiera: es la
    # unica que se puede volver a medir y que puede haber cambiado sin que nadie
    # toque nada. Un centinela alterado NO se cura solo, y el freno espera una
    # decision de una persona: los dos siguen valiendo lo que valian.
    if ($Estado -eq 'Falla' -and $Causa -eq 'destinoInalcanzable' -and $DestinoResponde) {
        return [pscustomobject]@{
            Estado   = 'Atencion'
            Detalle  = 'La ultima corrida fallo porque el nodo no respondia. El nodo YA responde: falta una corrida'
            Ajustado = $true
        }
    }

    return [pscustomobject]@{ Estado = $Estado; Detalle = ''; Ajustado = $false }
}


function Publish-EstadoAlNodo {
    <#
        .SYNOPSIS
            Deja una copia de ESTADO.txt en el nodo, dentro de _SISTEMA.

        .DESCRIPTION
            LA SECCION 8 LA PIDE Y NO EXISTIA. El original vive en el equipo por
            dos razones que no son de comodidad -el indicador tiene que pintar
            algo con el nodo caido, y la marca de corrida tiene que estar donde
            el indicador la vea aunque la red se haya ido con el motor-, pero la
            COPIA en el nodo tiene otra razon distinta: en una restauracion
            desde cero, el equipo ya no existe. Lo unico que queda es el nodo, y
            ahi tiene que poder leerse cuando fue la ultima corrida buena.

            SIN ESTA COPIA, `_SISTEMA/` no llegaba a crearse nunca: el emisor de
            eventos lo crea al primer aviso, y el aviso verde no se emite (seccion
            11). Medido el 2026-09-02: la carpeta no existia en el nodo despues
            de varias fases.

            NO TUMBA LA CORRIDA. Publicar es un extra: si el nodo no responde o
            no deja escribir, se anota y se sigue. El respaldo ya esta hecho.
        .PARAMETER RutaSistema
            La carpeta _SISTEMA del equipo dentro del nodo.
        .PARAMETER Carpeta
            Donde vive el ESTADO.txt original.
    #>
    [CmdletBinding(SupportsShouldProcess)]
    [OutputType([bool])]
    param(
        [Parameter(Mandatory)][ValidateNotNullOrEmpty()][string] $RutaSistema,
        [ValidateNotNullOrEmpty()][string] $Carpeta = (Get-CarpetaDeEstado)
    )

    $origen = Join-Path $Carpeta 'ESTADO.txt'
    if (-not (Test-Path -LiteralPath $origen -PathType Leaf)) { return $false }
    if (-not $PSCmdlet.ShouldProcess($RutaSistema, 'Publicar ESTADO.txt en el nodo')) { return $false }

    try {
        if (-not (Test-Path -LiteralPath $RutaSistema -PathType Container)) {
            New-Item -ItemType Directory -Path $RutaSistema -Force -ErrorAction Stop | Out-Null
        }
        Copy-Item -LiteralPath $origen -Destination (Join-Path $RutaSistema 'ESTADO.txt') -Force -ErrorAction Stop
        return $true
    }
    catch {
        Write-RegistroRespaldo -Nivel 'ATENCION' -Etapa 'estado' `
            -Mensaje "No se pudo publicar ESTADO.txt en el nodo: $($_.Exception.Message)"
        return $false
    }
}

function Write-EstadoPorRaiz {
    <#
        .SYNOPSIS
            Anota como quedo CADA RAIZ en UN destino, sin tocar el otro destino.

        .DESCRIPTION
            EL TABLERO NO PUEDE ESCANEAR PARA PINTARSE. La tabla por raiz que
            pide el plan -que raiz, de que clase, cuanto le falta y como esta en
            cada destino- es la respuesta a "que exactamente"; pero medirla en
            vivo significa recorrer ocho raices por SMB, y eso son minutos. Un
            tablero que tarda minutos en aparecer no se abre nunca.

            Asi que LA MIDE QUIEN YA LA MIDIO: el motor acaba de copiar y sabe,
            raiz por raiz, cuantos archivos quedaron pendientes y si robocopy
            fue limpio. Ese dato se guarda aqui y el tablero solo lo lee.

            UN ARCHIVO, DOS DESTINOS, Y CADA UNO ESCRIBE SOLO SU MITAD: se
            reemplazan unicamente las lineas de $Destino y las del otro se
            conservan tal cual. Es la misma regla que Write-EstadoDelDisco:
            que la copia fria haya ido bien no dice nada del nodo.

            Formato TSV porque una ruta lleva espacios y comas pero nunca un
            tabulador, y porque en una emergencia se abre con cualquier cosa.
        .PARAMETER Destino
            'nodo' o 'disco'.
        .PARAMETER Copias
            Lo que devolvio Invoke-Robocopy por cada raiz.
        .PARAMETER Carpeta
            Donde vive el estado.
    #>
    [CmdletBinding(SupportsShouldProcess)]
    [OutputType([void])]
    param(
        [Parameter(Mandatory)]
        [ValidateSet('nodo', 'disco')]
        [string] $Destino,

        [Parameter(Mandatory)]
        [AllowEmptyCollection()]
        [psobject[]] $Copias,

        [ValidateNotNull()]
        [string] $QuitarPrefijo = '',

        [ValidateNotNullOrEmpty()]
        [string] $Carpeta = (Get-CarpetaDeEstado)
    )

    $ruta = Join-Path $Carpeta 'RAICES.tsv'
    $momento = Get-Date -Format 's'

    # Lo que ya habia del OTRO destino se conserva palabra por palabra.
    $otras = New-Object System.Collections.Generic.List[string]
    if (Test-Path -LiteralPath $ruta -PathType Leaf) {
        try {
            foreach ($linea in (Get-Content -LiteralPath $ruta -Encoding UTF8 -ErrorAction Stop)) {
                if ($linea -match '^\s*#' -or [string]::IsNullOrWhiteSpace($linea)) { continue }
                if (($linea -split "`t")[0] -ne $Destino) { $otras.Add($linea) }
            }
        }
        catch {
            # Un TSV ilegible no puede tumbar una corrida que ya copio bien. Se
            # pierde la tabla anterior y se reconstruye con esta.
            Write-RegistroRespaldo -Nivel 'ATENCION' -Etapa 'estado' `
                -Mensaje "RAICES.tsv no se pudo leer, se reconstruye: $($_.Exception.Message)"
        }
    }

    if (-not $PSCmdlet.ShouldProcess($ruta, "Anotar $(@($Copias).Count) raices del destino $Destino")) { return }

    $sb = New-Object System.Text.StringBuilder
    [void]$sb.AppendLine('# RAICES.tsv - una linea por raiz y destino. Lo pinta el tablero.')
    [void]$sb.AppendLine('# destino	raiz	clase	pendientes	estado	momento')
    foreach ($linea in $otras) { [void]$sb.AppendLine($linea) }
    foreach ($c in $Copias) {
        if ($null -eq $c) { continue }
        # Pendientes es lo que robocopy DIJO que faltaba por copiar en esta
        # pasada. Tras una copia buena es 0; si no lo es, algo se quedo.
        $pendientes = 0
        if ($null -ne $c.NumACopiar) { $pendientes = [int]$c.NumACopiar }
        $estadoRaiz = if ($c.Correcto) { 'AlDia' } else { 'Falla' }

        # LA ETIQUETA TIENE QUE CASAR ENTRE LOS DOS DESTINOS O LA TABLA MIENTE.
        # La copia fria corre dos pasadas: una desde el equipo, cuyas raices se
        # llaman C:\dev, y otra desde el nodo, cuyas raices son rutas UNC. Sin
        # recortar el prefijo, la misma raiz saldria dos veces con dos nombres y
        # ninguna de las dos filas tendria las dos columnas.
        $etiqueta = '' + $c.Origen
        if ($QuitarPrefijo -and $etiqueta.StartsWith($QuitarPrefijo, [System.StringComparison]::OrdinalIgnoreCase)) {
            $etiqueta = $etiqueta.Substring($QuitarPrefijo.Length).TrimStart('\')
        }
        [void]$sb.AppendLine(('{0}	{1}	{2}	{3}	{4}	{5}' -f `
                    $Destino, $etiqueta, $c.Clase, $pendientes, $estadoRaiz, $momento))
    }
    Set-ContenidoAtomico -Ruta $ruta -Contenido $sb.ToString() -Confirm:$false
}

function Read-EstadoPorRaiz {
    <#
        .SYNOPSIS
            Lee RAICES.tsv. Nunca lanza: el tablero se pinta igual sin el.
        .PARAMETER Carpeta
            Donde vive el estado.
    #>
    [CmdletBinding()]
    [OutputType([psobject[]])]
    param(
        [ValidateNotNullOrEmpty()]
        [string] $Carpeta = (Get-CarpetaDeEstado)
    )

    $ruta = Join-Path $Carpeta 'RAICES.tsv'
    $filas = New-Object System.Collections.Generic.List[psobject]
    try {
        if (-not (Test-Path -LiteralPath $ruta -PathType Leaf)) { return $filas.ToArray() }
        foreach ($linea in (Get-Content -LiteralPath $ruta -Encoding UTF8 -ErrorAction Stop)) {
            if ($linea -match '^\s*#' -or [string]::IsNullOrWhiteSpace($linea)) { continue }
            $campos = $linea -split "`t"
            if ($campos.Count -lt 6) { continue }
            $filas.Add([pscustomobject]@{
                    Destino    = $campos[0]
                    Raiz       = $campos[1]
                    Clase      = $campos[2]
                    Pendientes = [int]$campos[3]
                    Estado     = $campos[4]
                    Momento    = $campos[5]
                })
        }
    }
    catch {
        return @()
    }
    return $filas.ToArray()
}

function Get-EspacioLibre {
    <#
        .SYNOPSIS
            Bytes libres en una ruta, incluida una ruta UNC. $null si no se sabe.

        .DESCRIPTION
            SE PREGUNTA POR LA RUTA, NO POR LA UNIDAD. El destino principal es
            \192.168.1.38\datos y ahi no hay letra de unidad que consultar:
            DriveInfo y Win32_LogicalDisk no ven un recurso de red, asi que
            devolvian nada justo para el destino que importa.

            GetDiskFreeSpaceEx si acepta UNC, y ademas devuelve el espacio QUE
            LE TOCA A ESTE USUARIO -no el del volumen- que es el numero honesto
            cuando hay cuota.

            Nunca lanza: es un adorno del tablero. Si no se puede saber, se
            dice que no se sabe, que es distinto de decir cero.
        .PARAMETER Ruta
            Carpeta local o UNC.
    #>
    [CmdletBinding()]
    [OutputType([System.Nullable[long]])]
    param(
        [Parameter(Mandatory)]
        [ValidateNotNullOrEmpty()]
        [string] $Ruta
    )

    try {
        if (-not ('NasRespaldo.Espacio' -as [type])) {
            Add-Type -Namespace 'NasRespaldo' -Name 'Espacio' -MemberDefinition @'
[System.Runtime.InteropServices.DllImport("kernel32.dll", CharSet = System.Runtime.InteropServices.CharSet.Unicode, SetLastError = true)]
[return: System.Runtime.InteropServices.MarshalAs(System.Runtime.InteropServices.UnmanagedType.Bool)]
public static extern bool GetDiskFreeSpaceEx(string lpDirectoryName,
    out ulong lpFreeBytesAvailable, out ulong lpTotalNumberOfBytes, out ulong lpTotalNumberOfFreeBytes);
'@
        }
        [uint64] $libres = 0; [uint64] $total = 0; [uint64] $librestotal = 0
        # La barra final importa: sin ella la API trata el ultimo tramo como
        # nombre de archivo y falla en las raices de recurso compartido.
        $arg = $Ruta.TrimEnd('\') + '\'
        if ([NasRespaldo.Espacio]::GetDiskFreeSpaceEx($arg, [ref]$libres, [ref]$total, [ref]$librestotal)) {
            return [long]$libres
        }
        return $null
    }
    catch {
        return $null
    }
}

function Write-EstadoDeComprobacion {
    <#
        .SYNOPSIS
            Anota cuando se comprobo por ultima vez y como salio, sin tocar los
            veredictos de copia.

        .DESCRIPTION
            COPIAR NO ES COMPROBAR, Y EL TABLERO TENIA QUE PODER DECIRLO. Hasta
            ahora verificar.ps1 y la prueba de restauracion imprimian su
            resultado en pantalla y no lo guardaban en ninguna parte: al cerrar
            la ventana, la unica prueba de que el respaldo servia desaparecia.

            El plan pide dos fechas visibles -"huellas 24/08 sin diferencias" y
            "restauracion 18/08 OK"- y una fecha vieja ahi es una senal tan util
            como un fallo: dice que hace semanas que nadie comprueba que lo
            copiado se puede leer.

            Se funde con lo que hubiera: las claves de copia se conservan.
        .PARAMETER Tipo
            'huellas' -la verificacion- o 'restauracion' -la prueba de leer.
        .PARAMETER Correcto
            Si salio limpio.
        .PARAMETER Detalle
            Una frase corta para la persona.
        .PARAMETER Carpeta
            Donde vive ESTADO.txt.
    #>
    [CmdletBinding(SupportsShouldProcess)]
    [OutputType([void])]
    param(
        # 'semilla' NO ES 'restauracion'. Leer la semilla desde el nodo prueba
        # que el kit de arranque esta entero y se puede leer; el criterio 11
        # -restaurar de verdad en una maquina limpia- es otra cosa y sigue
        # abierto. Llamarlas igual convertiria el tablero en el semaforo que
        # miente: verde en "restauracion" sin haber restaurado nada.
        [Parameter(Mandatory)]
        [ValidateSet('huellas', 'semilla')]
        [string] $Tipo,

        [Parameter(Mandatory)]
        [bool] $Correcto,

        [ValidateNotNull()]
        [string] $Detalle = '',

        [ValidateNotNullOrEmpty()]
        [string] $Carpeta = (Get-CarpetaDeEstado)
    )

    $previo = Read-EstadoRespaldo -Carpeta $Carpeta

    $datos = @{}
    foreach ($clave in $previo.Keys) {
        if ($clave -in @('estado', 'momento', 'detalle')) { continue }
        if ($clave -like "$Tipo`_*") { continue }
        $datos[$clave] = $previo[$clave]
    }
    $datos["$Tipo`_momento"] = (Get-Date -Format 's')
    $datos["$Tipo`_estado"]  = if ($Correcto) { 'OK' } else { 'Falla' }
    $datos["$Tipo`_detalle"] = $Detalle

    $estadoPrincipal = '' + $previo['estado']
    if ($estadoPrincipal -notin @('Protegido', 'Copiando', 'Atencion', 'Falla', 'SinDatos')) {
        $estadoPrincipal = 'SinDatos'
    }

    if (-not $PSCmdlet.ShouldProcess((Join-Path $Carpeta 'ESTADO.txt'), "Anotar la comprobacion $Tipo")) { return }

    $sb = New-Object System.Text.StringBuilder
    [void]$sb.AppendLine('# ESTADO.txt - lo que lee el indicador (seccion 10.2)')
    [void]$sb.AppendLine('# Escrito de forma atomica: nunca se lee a medias.')
    [void]$sb.AppendLine(('estado={0}' -f $estadoPrincipal))
    [void]$sb.AppendLine(('momento={0}' -f ('' + $previo['momento'])))
    [void]$sb.AppendLine(('detalle={0}' -f (('' + $previo['detalle']) -replace '[\r\n]+', ' ')))
    foreach ($clave in ($datos.Keys | Sort-Object)) {
        [void]$sb.AppendLine(('{0}={1}' -f $clave, (('' + $datos[$clave]) -replace '[\r\n]+', ' ')))
    }
    Set-ContenidoAtomico -Ruta (Join-Path $Carpeta 'ESTADO.txt') -Contenido $sb.ToString() -Confirm:$false
}
