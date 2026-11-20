#Requires -Version 5.1

# Modulo NasRespaldo - dominio: DestinosYCopia.ps1

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

            DOS BARRAS DELANTE NO BASTAN PARA QUE SEA RED, y esto es el mismo
            desastre con otra ortografia. El espacio de nombres de dispositivo de
            Windows -\\?\ y \\.\- empieza igual que una UNC y pasaba la guarda:

                \\?\C:\datos   ->  era C:\datos
                \\.\C:\datos   ->  era C:\datos

            Las dos son el disco LOCAL escritas de forma que parecen un servidor
            remoto, o sea exactamente el estropicio del 02/09 con un prefijo
            distinto. Y no hay motivo legitimo para declararlas: el destino del
            nodo es \\<equipo>\<recurso>, donde el primer tramo es un nombre de
            maquina. '?' y '.' no lo son.
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
    if ($Ruta -match '^\\\\[?.]\\') { return $false }
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

function Get-FirmaDeDisco {
    <#
        .SYNOPSIS
            La identidad que vive EN el disco, no en la caja USB que lo lleva.
        .DESCRIPTION
            MBR: la firma de 32 bits, en 8 cifras hexadecimales como la ensena
            `diskpart` (uniqueid disk). GPT: el GUID del disco, sin llaves. Las
            dos se escriben al particionar y no cambian al formatear, ni con la
            letra, ni con la caja (ADR-0102).
            Devuelve $null si el disco no tiene ninguna (sin inicializar).
        .PARAMETER Disco
            Un objeto de Get-Disk.
    #>
    [CmdletBinding()]
    [OutputType([string])]
    param(
        [Parameter(Mandatory)][psobject] $Disco
    )

    $estilo = '' + $Disco.PartitionStyle
    if ($estilo -eq 'GPT' -and $Disco.Guid) { return ('' + $Disco.Guid).Trim('{}').ToUpperInvariant() }
    if ($estilo -eq 'MBR' -and $Disco.Signature) { return '{0:X8}' -f [uint32]$Disco.Signature }
    return $null
}

function Get-DiscoFrio {
    <#
        .SYNOPSIS
            Localiza el disco frio por su FIRMA, nunca por letra ni por caja.
        .DESCRIPTION
            La letra cambia sola, y la serie que lee Windows es la de la caja
            USB, no la del disco: al cambiar de caja el 2026-10-18 el motor lo
            dio por "no conectado" con el disco puesto (ADR-0102). La firma
            esta grabada en el propio disco. La confirmacion es el archivo
            centinela en la raiz, que evita escribir en un disco parecido por
            accidente (seccion 15, criterios 7 y 7b).
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
    $disco = @(Get-Disk | Where-Object { (Get-FirmaDeDisco -Disco $_) -eq $ficha.firma })
    if ($disco.Count -eq 0) {
        # EL CENTINELA PUESTO Y LA FIRMA DISTINTA NO ES "NO CONECTADO". Asi lo
        # decia el 2026-10-18, cuatro veces, con el disco en F:. Se sigue sin
        # escribir, pero se dice que firma hay para que se pueda decidir.
        foreach ($v in @(Get-Volume | Where-Object { $_.DriveLetter })) {
            if (-not (Test-Path -LiteralPath (Join-Path ('{0}:\' -f $v.DriveLetter) $ficha.centinela) -PathType Leaf)) { continue }
            $vista = 'ilegible'
            try { $vista = Get-FirmaDeDisco -Disco (Get-Partition -DriveLetter $v.DriveLetter -ErrorAction Stop | Get-Disk -ErrorAction Stop) }
            catch { Write-Verbose "Firma de $($v.DriveLetter): ilegible: $($_.Exception.Message)" }
            Write-RegistroRespaldo -Nivel 'ERROR' -Etapa 'disco' -Mensaje ("El centinela del disco frio esta en {0}:\ pero ese disco tiene la firma {1} y la configuracion espera {2}. No se escribe nada." -f $v.DriveLetter, $vista, $ficha.firma)
            return $null
        }
        Write-Verbose "Disco frio con firma $($ficha.firma) no conectado."
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
        Write-RegistroRespaldo -Nivel 'ERROR' -Etapa 'disco' -Mensaje "Firma correcta en $raiz pero FALTA el centinela '$($ficha.centinela)'. No se escribe nada."
        return $null
    }

    return [pscustomobject]@{
        Firma     = $ficha.firma
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
        .PARAMETER UnSoloHilo
            La copia real sin /MT. La usa el disco frio: ver mas abajo.
        .PARAMETER ExcluirArchivos
            Nombres o patrones para /XF. Lo usa el laboratorio para dejar fuera
            los discos virtuales grandes, que viajan por bloques (ADR-0104).
            Robocopy tampoco los purga del destino.
        .PARAMETER ExcluirCarpetas
            Nombres o patrones para /XD.
    #>
    [CmdletBinding()]
    [OutputType([psobject])]
    param(
        [Parameter(Mandatory)][ValidateNotNullOrEmpty()][string] $Origen,
        [Parameter(Mandatory)][ValidateNotNullOrEmpty()][string] $Destino,
        [Parameter(Mandatory)][ValidateSet('A', 'B')][string] $Clase,
        [switch] $SoloListar,
        [switch] $UnSoloHilo,
        [string[]] $ExcluirArchivos = @(),
        [string[]] $ExcluirCarpetas = @()
    )

    # Banderas que NO son opcionales (seccion 4 del contrato):
    #   /XJ         Documents contiene junctions a Music, Pictures y Videos. Sin
    #               esto robocopy las sigue y arrastra los 60 GB del drone por la
    #               puerta de atras. Por poco no se pone el 2026-09-02.
    #   /FFT        Tolerancia de 2 s entre la marca de Windows y la de Linux.
    #               Sin esto el motor cree que todo cambio en cada corrida.
    #   /DCOPY:DAT  Conserva las fechas de las carpetas, no solo de los archivos.
    $comunes  = @('/XJ', '/DCOPY:DAT', '/FFT', '/R:2', '/W:5', '/NP')
    $politica = if ($Clase -eq 'A') { @('/E', '/XO') } else { @('/MIR') }

    # /MT SOLO EN LA COPIA REAL, NUNCA EN LA PASADA EN SECO.
    #
    # MEDIDO EL 2026-09-08 contra el nodo: seis pasadas sobre 'C:\dev' con el
    # disco en el mismo estado, contando las lineas de la salida.
    #
    #     modo        seg   ACopiar   ABorrar   sin clasificar
    #     /MT:8      11.9      1154       411        8
    #     /MT:8      11.9      1154       488        4
    #     /MT:8      12.3      1154       438        8
    #     sin /MT    18.9      1154       551        0
    #     sin /MT    18.9      1154       551        0
    #     sin /MT    19.2      1154       551        0
    #
    # SIN /MT LA MEDIDA ES EXACTA Y REPETIBLE. Con /MT:8 el conteo de sobrantes
    # varia entre 411 y 550 sobre el MISMO disco: se pierden entre el 11 % y el
    # 25 % de las lineas del destino, en silencio, porque ocho hilos escriben a
    # la vez en la misma salida y se pisan.
    #
    # POR QUE ES UN FALLO DE SEGURIDAD Y NO UNA MOLESTIA. De esta medida cuelga
    # el freno de la capa 2 (seccion 7). Un umbral alimentado por un numero que
    # depende del planificador de hilos no es un umbral: el mismo disco da
    # 6.16 % o 5.65 % segun como caiga. Y falla EN LA DIRECCION PELIGROSA: si
    # /MT se come bastantes lineas de sobrante, el porcentaje baja del umbral y
    # EL FRENO NO SALTA. Un borrado masivo real puede quedar subcontado hasta el
    # silencio, que es justo lo que la capa 2 existe para impedir.
    # Test-Coincidencia (verificar.ps1) bebe de la misma pasada.
    #
    # QUE CUESTA: siete segundos por corrida en la mayor de las ocho raices.
    # /MT:8 no compraba velocidad -12 s contra 19 s- a cambio de volver
    # aleatoria la unica defensa que no depende de reconocer al atacante.
    #
    # LA COPIA REAL LO CONSERVA. Ahi el entrelazado desordena el informe, no el
    # trabajo: robocopy copia bien con ocho hilos y ningun umbral se decide con
    # esa salida. El codigo de salida y el cubo de sin clasificar se siguen
    # mirando igual.
    #
    # SALVO EN EL DISCO FRIO (-UnSoloHilo), y ahi el entrelazado SI decidia. El
    # disco es aditivo: guarda lo que el equipo ya borro, y robocopy lo lista
    # como "*Archivo EXTRA" en cada pasada -567 lineas en 'C:\dev'-. Con ocho
    # hilos esas lineas salen partidas y sin ruta, caen en el cubo de sin
    # clasificar y la raiz se daba por FALLO con la copia hecha. Medido el
    # 2026-10-18 sobre 'C:\dev': 8 lineas partidas en la copia real, 19 a 22 en
    # tres pasadas en seco con /MT:8, y cero sin el.
    if (-not $SoloListar -and -not $UnSoloHilo) { $comunes += '/MT:8' }

    # /FP ruta completa, para poder clasificar por prefijo.
    # /NJH /NJS sin cabecera ni resumen: el resumen esta localizado y no se usa.
    # /NDL sin lineas de directorio. /NS sin tamanos.
    $formato = @('/FP', '/NJH', '/NJS', '/NDL', '/NS')

    $argumentos = @($Origen, $Destino) + $politica + $comunes + $formato
    if (@($ExcluirArchivos).Count -gt 0) { $argumentos += @('/XF') + @($ExcluirArchivos) }
    if (@($ExcluirCarpetas).Count -gt 0) { $argumentos += @('/XD') + @($ExcluirCarpetas) }
    if ($SoloListar) { $argumentos += '/L' }

    # SIN `2>&1`, Y QUITARLO ES UNA GUARDA. Ese redirector no traia nada y podia
    # tumbar la corrida entera:
    #
    #   LO QUE NO TRAIA. robocopy NO ESCRIBE POR EL CANAL DE ERROR. Medido con
    #   tres formas de hacerlo fallar -origen inexistente, bandera invalida y sin
    #   argumentos-: codigo 16 las tres veces y CERO lineas por ese canal. Sus
    #   "ERROR 2 (0x00000002)" salen por la salida normal, que es justo de donde
    #   los recoge el cubo de sin clasificar de mas abajo.
    #
    #   LO QUE SI PODIA COSTAR. Con $ErrorActionPreference = 'Stop' -que es como
    #   corre todo este cliente-, PowerShell 5.1 convierte la PRIMERA linea que un
    #   programa externo escriba por el canal de error, si va redirigida con 2>&1,
    #   en un error TERMINANTE (NativeCommandError). Verificado en este equipo.
    #   O sea que el dia que una version de robocopy, un antivirus interpuesto o
    #   una capa de red escribieran una sola linea por ahi, esta funcion dejaria
    #   de devolver su resultado tipado y LANZARIA -- y la corrida moriria a mitad
    #   dejando ESTADO.txt en 'Copiando'.
    #
    # Una trampa armada a cambio de nada. Se retira.
    Write-Verbose "robocopy $($argumentos -join ' ')"
    $salida = & robocopy.exe @argumentos
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
        # DESDE EL 2026-09-08 LA PASADA EN SECO YA NO LLEVA /MT, asi que estas
        # lineas dejan de fabricarse justo donde se decidia el freno. La
        # limpieza SE QUEDA, y no por inercia: la copia real sigue con ocho
        # hilos y sigue entrelazando. Lo que cambia es que ahora una linea sin
        # clasificar en una pasada en seco es una senal, no ruido esperado.
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
