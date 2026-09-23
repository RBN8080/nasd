#Requires -Version 5.1
<#
    .SYNOPSIS
        Comprueba. Y no afirma lo que no comprobo.

    .DESCRIPTION
        Contrato: 30_CLIENTE_RESPALDO.md seccion 9.

        TRES NIVELES DE CERTEZA QUE NO DEBEN CONFUNDIRSE:

          "Corrio"                    el codigo de salida          costo nulo
          "Coincide"                  comparacion en seco con /L   costo segundos
          "El contenido es identico"  huella criptografica         costo alto
                                                                   -> por MUESTREO

        Este guion no lee un registro viejo: hace la comparacion REAL contra el
        destino en ese momento, y por eso puede afirmar lo que afirma.

        DOS TRAMPAS MEDIDAS EL 2026-09-02, y las dos costaron tiempo real:

          1. NORMALIZAR LOS DOS LADOS ANTES DE COMPARAR. PowerShell escribe la
             huella en MAYUSCULAS y md5sum en minusculas. Salieron "27 508
             diferencias" que eran cero. Paso DOS VECES: la segunda, un
             tr 'A-F' 'a-f' que solo bajaba los digitos hex y dejaba DJI como
             dJI. Un cotejo que sale 100 % distinto casi nunca es un desastre:
             casi siempre es formato. Get-HuellaDeArchivo devuelve minusculas
             para que desde este lado no pueda repetirse.

          2. EN EL NODO, POR LOTES. Un proceso md5sum por archivo proyectaba
             49 min para 27 508 archivos; por lotes de 500 fueron minutos. Sobre
             un Pi, el coste de ARRANCAR PROCESOS domina cuando los archivos son
             pequenos. Por eso el nivel 3 va POR MUESTREO desde aqui.

        Y una velocidad que no cuadra con lo medido es un fallo hasta que se
        demuestre lo contrario. Techo medido PC -> nodo:
          bloque grande      13.4 MB/s   - el techo es el Pi 3B+, no SMB
          archivos pequenos  60.2 arch/s - manda el conteo, no los bytes

    .PARAMETER RutaConfiguracion
        Configuracion a usar. Por omision la de 3-Config.

    .PARAMETER TamanoMuestra
        Cuantos archivos por raiz se comprueban por huella (nivel 3). Cero
        salta el nivel 3 y deja la verificacion en "coincide".

    .PARAMETER Recientes
        Rutas de origen que la corrida acaba de copiar. Si se pasan, el nivel 3
        lee de ahi y la raiz entera queda para el muestreo semanal (seccion 9).

    .EXAMPLE
        .\verificar.ps1
        .\verificar.ps1 -TamanoMuestra 25
#>
[CmdletBinding()]
[OutputType([psobject])]
param(
    [ValidateNotNullOrEmpty()]
    [string] $RutaConfiguracion,

    [ValidateRange(0, 10000)]
    [int] $TamanoMuestra = 10,

    # Cuesta un recorrido del disco entero, asi que no va por omision: se pide.
    [switch] $RevisarDisco,

    # EL LATIDO VERDE DEL TESTIGO (ADR-0079). NO va por omision, y no es un
    # descuido: quien mira una verificacion a mano ya esta delante de la
    # pantalla y no necesita un vigilante externo. Lo que el testigo vigila es
    # el AUTOMATISMO, asi que solo lo pide la corrida programada.
    [switch] $EmitirLatido,

    # Lo pasa respaldo.ps1 tras una corrida buena. Sin el, la muestra sale de la
    # raiz entera, que es lo que quiere quien coteja a mano desde la opcion [3].
    [string[]] $Recientes
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

. "$PSScriptRoot\comun.ps1"
. "$PSScriptRoot\clasificar.ps1"
. "$PSScriptRoot\testigo.ps1"

function Test-CoincidenciaDeRaiz {
    <#
        .SYNOPSIS
            Nivel 2, "coincide": comparacion en seco contra el destino.
        .DESCRIPTION
            Cuesta segundos y responde la pregunta que de verdad importa antes
            de una corrida: cuantos archivos faltan por copiar y cuantos SOBRAN
            en el destino. En una raiz clase B, los que sobran son exactamente
            los que el espejo borraria.
        .PARAMETER Raiz
            La raiz ya resuelta.
        .PARAMETER Destino
            Su ruta en el destino.
    #>
    [CmdletBinding()]
    [OutputType([psobject])]
    param(
        [Parameter(Mandatory)][psobject] $Raiz,
        [Parameter(Mandatory)][ValidateNotNullOrEmpty()][string] $Destino
    )

    $existe = Test-Path -LiteralPath $Destino -PathType Container
    $seco = Invoke-Robocopy -Origen $Raiz.Ruta -Destino $Destino -Clase $Raiz.Clase -SoloListar

    # UN SOBRANTE SOLO ES UNA DIFERENCIA SI ESTA CLASE LO FUERA A BORRAR.
    #
    # Es el mismo defecto de familia que el freno tenia y que se parcheo el
    # 2026-09-04: en clase A -/E /XO- un archivo que sobra en el destino es
    # EXACTAMENTE lo que esa clase existe para conservar. Contarlo como
    # diferencia era llamar fallo al acierto.
    #
    # DETECTADO EL 04/09 Y NO PARCHEADO ENTONCES a proposito, para no tocar dos
    # subsistemas la misma noche. Se cierra hoy.
    #
    # POR QUE AHORA Y NO DESPUES DE LA FASE 7: medido el 2026-09-08, este
    # defecto NO esta produciendo hoy ningun falso positivo -- las dos raices
    # que difieren, Pictures y C:\dev, tienen FALTANTES reales, asi que el
    # "2 de 8 raices con diferencias" del ESTADO.txt era verdadero --. Pero se
    # dispara EN CUANTO Pictures se ponga al dia: tendra Faltan=0 y conservara
    # sus 12 sobrantes legitimos de clase A. Seria el primer resultado que el
    # responsable veria al reencender el cliente, y seria falso.
    $sobranQueImportan = if ($Raiz.Clase -eq 'B') { $seco.NumABorrar } else { 0 }

    return [pscustomobject]@{
        Raiz           = $Raiz.Ruta
        Clase          = $Raiz.Clase
        Destino        = $Destino
        DestinoExiste  = $existe
        Faltan         = $seco.NumACopiar
        # Sobran se sigue publicando ENTERO: quien mire tiene que poder ver el
        # sobrante aunque no cuente como diferencia (seccion 10, no se quita
        # telemetria para que el sistema se vea limpio). Lo que cambia es que
        # deja de ENSUCIAR el veredicto en la clase que no borra.
        Sobran         = $seco.NumABorrar
        SobranQueImportan = $sobranQueImportan
        Coincide       = ($seco.NumACopiar -eq 0 -and $sobranQueImportan -eq 0)
        CodigoRobocopy = $seco.Codigo
    }
}

function Test-HuellaPorMuestreo {
    <#
        .SYNOPSIS
            Nivel 3, "el contenido es identico": huellas, y solo por muestreo.
        .DESCRIPTION
            El nivel 3 es CARO: leer entero lo que se respalda, en los dos lados,
            sobre un enlace de 13.4 MB/s. Aqui se toma una muestra al azar y se
            comparan sus huellas archivo por archivo.

            DE DONDE SALE LA MUESTRA (seccion 9). Sin -Candidatos, de la raiz
            entera: es el muestreo semanal y el de la opcion [3]. Con
            -Candidatos, solo de lo que la corrida acaba de copiar. Releer en
            cada corrida lo que ya estaba costaba 8 min de lectura por la red
            -medido el 2026-09-23: la copia de las ocho raices tardo 2 min y el
            cotejo de despues 8- para confirmar lo ya confirmado.

            SI LA RAIZ NO COPIO NADA, SE LEE UN ARCHIVO PEQUENO. El verde del
            testigo exige haber leido algo (Test-CotejoLimpio, ADR-0079): una
            corrida sin cambios no puede dejarlo en rojo, y un archivo de hasta
            -TopePequeno basta para probar que el destino responde y se lee.

            UN ARCHIVO QUE CAMBIO DESPUES DE COPIARSE NO SE COMPARA. Si el
            origen es mas nuevo que el destino, esa version no la llevo ninguna
            corrida y su huella no puede desmentirla. Lo recien copiado es justo
            lo que se esta editando: sin esta regla, el uso normal del equipo
            pondria el cotejo en FALLO. Se salta sin gastar puesto de la muestra
            y se cuenta en Posteriores. Lo contrario -destino mas nuevo, el
            archivo en vuelo de un apagon- SI se compara.

            UN ARCHIVO QUE FALTA EN EL DESTINO SOLO ES FALLO EN LO RECIEN
            COPIADO, porque ahi la corrida dice haberlo llevado. En la raiz
            entera es un pendiente, y eso ya lo cuenta el nivel 2.

            Que la muestra pase NO demuestra que el resto este bien, y esta
            funcion no lo afirma: devuelve cuantos comprobo, para que quien lea
            el estado sepa el alcance de lo que se le esta diciendo.
        .PARAMETER Raiz
            La raiz ya resuelta.
        .PARAMETER Destino
            Su ruta en el destino.
        .PARAMETER Cuantos
            Tamano de la muestra.
        .PARAMETER Candidatos
            Rutas de ORIGEN recien copiadas, de todas las raices: se usan las
            que cuelgan de esta.
        .PARAMETER TopePequeno
            Tamano maximo del archivo que se lee si la raiz no copio nada.
    #>
    [CmdletBinding()]
    [OutputType([psobject])]
    param(
        [Parameter(Mandatory)][psobject] $Raiz,
        [Parameter(Mandatory)][ValidateNotNullOrEmpty()][string] $Destino,
        [Parameter(Mandatory)][ValidateRange(0, 10000)][int] $Cuantos,
        [string[]] $Candidatos,
        [ValidateRange(1, 1073741824)][int64] $TopePequeno = 1MB
    )

    $diferencias = New-Object System.Collections.Generic.List[psobject]
    $comprobados = 0
    $ausentes    = 0
    $posteriores = 0
    $alcance     = 'completo'
    if ($PSBoundParameters.ContainsKey('Candidatos')) { $alcance = 'recientes' }
    $raizOrigen  = $Raiz.Ruta.TrimEnd('\')

    if ($Cuantos -gt 0 -and (Test-Path -LiteralPath $Destino -PathType Container)) {
        if ($alcance -eq 'completo') {
            $pool = @(Get-ChildItem -LiteralPath $Raiz.Ruta -File -Force -Recurse -ErrorAction SilentlyContinue |
                    ForEach-Object { $_.FullName })
        }
        else {
            $prefijo = $raizOrigen + '\'
            $pool = @($Candidatos | ForEach-Object { ('' + $_).Trim() } |
                    Where-Object { $_.StartsWith($prefijo, [System.StringComparison]::OrdinalIgnoreCase) })
        }

        # La segunda ronda solo existe para lo recien copiado, y solo si la
        # primera no leyo nada: es la del archivo pequeno.
        $cupo = $Cuantos
        for ($ronda = 1; $ronda -le 2; $ronda++) {
            if ($ronda -eq 2) {
                if ($alcance -ne 'recientes' -or $comprobados -gt 0) { break }
                $alcance = 'pequeno'
                $cupo = 1
                $pool = @(Get-ChildItem -LiteralPath $Raiz.Ruta -File -Force -Recurse -ErrorAction SilentlyContinue |
                        Where-Object { $_.Length -le $TopePequeno } | ForEach-Object { $_.FullName })
            }
            if ($pool.Count -eq 0) { continue }

            # SE BARAJA TODO Y SE RECORRE HASTA LLENAR EL CUPO: un archivo que se
            # salta no gasta un puesto de la muestra.
            $puestos = 0
            foreach ($ruta in @($pool | Get-Random -Count $pool.Count)) {
                if ($puestos -ge $cupo) { break }
                $o = Get-Item -LiteralPath $ruta -Force -ErrorAction SilentlyContinue
                # Borrado del origen despues de la copia: no hay con que comparar.
                if (-not $o -or $o.PSIsContainer) { continue }

                $relativa = $ruta.Substring($raizOrigen.Length).TrimStart('\')
                $gemelo = Join-Path $Destino $relativa
                $d = Get-Item -LiteralPath $gemelo -Force -ErrorAction SilentlyContinue
                if (-not $d) {
                    # LO RECIEN COPIADO TIENE QUE ESTAR: la corrida dice que lo
                    # llevo, y si falta, la desmiente. En los otros modos, que
                    # falte es un pendiente de nivel 2 ("Faltan"), no una huella
                    # distinta: contarlo aqui daba FALLO por el uso normal del
                    # equipo. Medido el 2026-09-23: las 3 "diferencias" de 80
                    # lecturas eran capturas hechas despues de la ultima copia.
                    $ausentes++
                    if ($alcance -eq 'recientes') {
                        $puestos++
                        $diferencias.Add([pscustomobject]@{ Ruta = $relativa; Motivo = 'NO ESTA EN EL DESTINO' })
                    }
                    continue
                }
                # El mismo margen de 2 s que /FFT.
                if (($o.LastWriteTimeUtc - $d.LastWriteTimeUtc).TotalSeconds -gt 2) {
                    $posteriores++
                    continue
                }
                $puestos++
                $comprobados++
                # SHA256: aqui no hay linea base MD5 que respetar, y el nivel 3
                # es la afirmacion mas fuerte que hace este sistema.
                $hOrigen  = Get-HuellaDeArchivo -Ruta $o.FullName -Algoritmo SHA256
                $hDestino = Get-HuellaDeArchivo -Ruta $gemelo     -Algoritmo SHA256
                if ($hOrigen -ne $hDestino) {
                    $diferencias.Add([pscustomobject]@{ Ruta = $relativa; Motivo = 'HUELLA DISTINTA' })
                }
            }
        }
    }

    return [pscustomobject]@{
        Raiz        = $Raiz.Ruta
        Alcance     = $alcance
        Comprobados = $comprobados
        Ausentes    = $ausentes
        Posteriores = $posteriores
        Diferencias = $diferencias.ToArray()
        Correcto    = ($diferencias.Count -eq 0)
    }
}

function Get-AlcanceDelMuestreo {
    <#
        .SYNOPSIS
            Decide si el cotejo lee de la raiz entera o de lo recien copiado.
        .DESCRIPTION
            Funcion pura, para poder probarla. Toca 'completo' cuando nadie pasa
            lo recien copiado -la opcion [3]-, cuando no consta muestreo previo,
            cuando han pasado -Dias o mas, y cuando la fecha anotada es del
            futuro: un reloj que se movio no puede aplazar sin limite el
            muestreo semanal.
        .PARAMETER UltimoMuestreo
            `muestreo_momento` de ESTADO.txt, tal cual. Vacio si no consta.
        .PARAMETER HayRecientes
            Quien llama paso la lista de lo recien copiado.
        .PARAMETER Ahora
            El momento de referencia. Existe para las pruebas.
        .PARAMETER Dias
            Cada cuanto toca el muestreo completo.
    #>
    [CmdletBinding()]
    [OutputType([string])]
    param(
        [string] $UltimoMuestreo = '',
        [switch] $HayRecientes,
        [datetime] $Ahora = (Get-Date),
        [ValidateRange(1, 365)][int] $Dias = $script:DiasEntreMuestreos
    )

    if (-not $HayRecientes) { return 'completo' }
    [datetime] $cuando = [datetime]::MinValue
    if (-not [datetime]::TryParse($UltimoMuestreo, [ref] $cuando)) { return 'completo' }
    if ($cuando -gt $Ahora) { return 'completo' }
    if (($Ahora - $cuando).TotalDays -ge $Dias) { return 'completo' }
    return 'recientes'
}

function Test-DiscoFrioEnVuelo {
    <#
        .SYNOPSIS
            Busca en el disco frio lo que una copia cortada dejo roto.

        .DESCRIPTION
            LA SECCION 9 DICE "CUBRE NODO Y DISCO EXTERNO EN LA MISMA VISTA", Y
            HASTA EL 2026-09-02 EL DISCO NO SE COMPROBABA. La verificacion solo
            miraba equipo -> nodo; del disco se decia si estaba conectado y nada
            mas. El hueco salio a la luz con un apagon a media copia: ocho
            archivos quedaron con el tamano correcto y el contenido distinto, y
            /XO los habria saltado en todas las corridas siguientes mientras el
            motor los daba por buenos.

            SE MIRAN LOS DOS ORIGENES DEL DISCO (seccion 6.2), porque el disco
            recibe de dos sitios y una copia cortada puede haber sido de
            cualquiera de los dos: las raices del equipo y las del nodo.

            NO REPARA. Detecta y devuelve. La reparacion pisa archivos y eso no
            se hace sin que una persona lo pida.
        .PARAMETER Configuracion
            El objeto de configuracion completo.
        .PARAMETER Disco
            Lo que devuelve Get-DiscoFrio.
        .PARAMETER Desde
            Solo se miran archivos del disco tocados a partir de ese momento.
            Por omision, nada se filtra.
    #>
    [CmdletBinding()]
    [OutputType([psobject])]
    param(
        [Parameter(Mandatory)][psobject] $Configuracion,
        [Parameter(Mandatory)][psobject] $Disco,
        [datetime] $Desde = [datetime]::MinValue
    )

    $enVuelo   = New-Object System.Collections.Generic.List[psobject]
    $revisadas = New-Object System.Collections.Generic.List[psobject]
    $saltadas  = New-Object System.Collections.Generic.List[psobject]

    $pares = New-Object System.Collections.Generic.List[psobject]

    # Origen 1: las raices del equipo.
    $raizEquipo = '{0}{1}' -f $Disco.Raiz, $Configuracion.destinos.discoFrio.prefijoEquipo
    foreach ($r in @(Get-RaicesDeRespaldo -Configuracion $Configuracion)) {
        $pares.Add([pscustomobject]@{
            Origen  = $r.Ruta
            Destino = (Get-RutaEnDestino -RutaOrigen $r.Ruta -RaizDestino $raizEquipo `
                        -Traducciones @($Configuracion.destinos.discoFrio.traduccionDeRutas))
            Lado    = 'equipo'
        })
    }

    # Origen 2: las raices del nodo. Si el nodo no responde NO se inventa nada:
    # se anota como no revisada, que es distinto de revisada y limpia.
    $raizNodo = '{0}{1}' -f $Disco.Raiz, $Configuracion.destinos.discoFrio.prefijoNodo
    if (Test-Path -LiteralPath $Configuracion.destinos.nodo.unc) {
        foreach ($r in @(Get-RaizDelNodoParaDisco -Configuracion $Configuracion)) {
            $pares.Add([pscustomobject]@{
                Origen  = $r.Ruta
                Destino = (Get-RutaEnDestino -RutaOrigen $r.Ruta -RaizDestino $raizNodo `
                            -Traducciones @($Configuracion.destinos.discoFrio.traduccionDelNodo))
                Lado    = 'nodo'
            })
        }
    }
    else {
        $saltadas.Add([pscustomobject]@{ Lado = 'nodo'; Motivo = 'El nodo no responde: sus raices no se pudieron revisar' })
    }

    foreach ($par in $pares) {
        try {
            $hallado = @(Get-ArchivosEnVuelo -Origen $par.Origen -Destino $par.Destino -Desde $Desde)
            foreach ($h in $hallado) { $enVuelo.Add($h) }
            $revisadas.Add([pscustomobject]@{ Lado = $par.Lado; Origen = $par.Origen; Hallazgos = $hallado.Count })
        }
        catch {
            # Un origen que no responde NO es cero hallazgos (comun.ps1).
            $saltadas.Add([pscustomobject]@{ Lado = $par.Lado; Motivo = $_.Exception.Message })
        }
    }

    return [pscustomobject]@{
        Revisadas = $revisadas.ToArray()
        Saltadas  = $saltadas.ToArray()
        EnVuelo   = $enVuelo.ToArray()
        # LIMPIO EXIGE LAS DOS COSAS: cero hallazgos Y cero saltadas. Declararlo
        # limpio sin haber podido mirar la mitad es el fallo que persigue todo
        # este archivo.
        Limpio    = ($enVuelo.Count -eq 0 -and $saltadas.Count -eq 0)
    }
}

function Get-EstadoDeRespaldo {
    <#
        .SYNOPSIS
            La vista que lee estado.cmd. Mide ahora; no lee un registro viejo.
        .PARAMETER Configuracion
            El objeto de configuracion completo.
        .PARAMETER Muestra
            Tamano de la muestra del nivel 3.
        .PARAMETER Recientes
            Lo recien copiado. Si se pasa, el nivel 3 lee de ahi y no de la raiz
            entera (Test-HuellaPorMuestreo).
    #>
    [CmdletBinding()]
    [OutputType([psobject])]
    param(
        [Parameter(Mandatory)][psobject] $Configuracion,
        [ValidateRange(0, 10000)][int] $Muestra = 10,
        [switch] $RevisarDisco,
        [string[]] $Recientes
    )

    $unc = $Configuracion.destinos.nodo.unc
    $nodoVivo = Test-DestinoNodo -Unc $unc
    $raices = Get-RaicesDeRespaldo -Configuracion $Configuracion
    $huerfanos = Get-HuerfanosDeRespaldo -Configuracion $Configuracion

    $bandeja = $null
    $rutaBandeja = @([string[]]$Configuracion.exclusiones | Where-Object { $_ -like '*Downloads*' }) | Select-Object -First 1
    if ($rutaBandeja) { $bandeja = Get-EstadoDeBandeja -Ruta $rutaBandeja }

    $coincidencias = @()
    $huellas = @()
    if ($nodoVivo) {
        $raizNodo = '{0}\{1}\{2}' -f $unc.TrimEnd('\'), $Configuracion.destinos.nodo.raiz, $Configuracion.destinos.nodo.prefijoEquipo
        $traducciones = @($Configuracion.destinos.nodo.traduccionDeRutas)
        # SE DICE POR DONDE VA, Y NO ES UN ADORNO. El nivel 3 lee archivos
        # enteros por SMB y la muestra puede caer sobre videos del drone de
        # cientos de MB: a 13.4 MB/s eso son minutos POR RAIZ. Sin una linea que
        # se mueva, quien mira la pantalla concluye que se colgo y lo mata.
        # Medido el 2026-09-02: se dio por colgado y se corto a mano.
        $n = 0
        $total = @($raices).Count
        foreach ($r in $raices) {
            $n++
            $destino = Get-RutaEnDestino -RutaOrigen $r.Ruta -RaizDestino $raizNodo -Traducciones $traducciones
            Write-Progress -Activity 'Verificando contra el nodo' `
                -Status ("{0} de {1}: {2}" -f $n, $total, $r.Ruta) `
                -PercentComplete ([int](100 * ($n - 1) / [Math]::Max(1, $total)))
            Write-Information ("   [{0}/{1}] {2}" -f $n, $total, $r.Ruta) -InformationAction Continue

            $coincidencias += Test-CoincidenciaDeRaiz -Raiz $r -Destino $destino
            $argsHuella = @{ Raiz = $r; Destino = $destino; Cuantos = $Muestra }
            $deDonde = 'al azar de toda la raiz'
            if ($PSBoundParameters.ContainsKey('Recientes')) {
                $argsHuella['Candidatos'] = $Recientes
                $deDonde = 'de lo recien copiado'
            }
            if ($Muestra -gt 0) {
                Write-Information ("        comprobando hasta {0} huellas {1} (lee los archivos enteros)..." -f $Muestra, $deDonde) -InformationAction Continue
            }
            $huellas       += Test-HuellaPorMuestreo @argsHuella
        }
        Write-Progress -Activity 'Verificando contra el nodo' -Completed
    }

    $disco = Get-DiscoFrio -Configuracion $Configuracion

    # $null, no un objeto vacio, y la distincion es la de siempre: "no se
    # reviso" tiene que poder distinguirse de "se reviso y esta limpio".
    $discoEnVuelo = $null
    if ($RevisarDisco -and $disco) {
        $discoEnVuelo = Test-DiscoFrioEnVuelo -Configuracion $Configuracion -Disco $disco
    }

    return [pscustomobject]@{
        Momento       = Get-Date
        NodoVivo      = $nodoVivo
        DiscoFrio     = $disco
        DiscoEnVuelo  = $discoEnVuelo
        Raices        = $raices
        Huerfanos     = $huerfanos
        Bandeja       = $bandeja
        Coincidencias = $coincidencias
        Huellas       = $huellas
        TodoCoincide  = ($coincidencias.Count -gt 0 -and @($coincidencias | Where-Object { -not $_.Coincide }).Count -eq 0)
    }
}

if ($MyInvocation.InvocationName -eq '.') {
    Write-Verbose 'verificar.ps1 cargado con punto: se exponen las funciones y no se verifica nada.'
    return
}

$parametrosConfig = @{}
if ($PSBoundParameters.ContainsKey('RutaConfiguracion')) { $parametrosConfig['Ruta'] = $RutaConfiguracion }
$configuracion = Get-ConfiguracionRespaldo @parametrosConfig

# EL ICONO TIENE QUE ENSENAR QUE ESTO TRABAJA, y no lo hacia. Este cotejo lee
# archivos ENTEROS por la red y tarda minutos, pero era el unico de los tres
# largos que no ponia la marca: respaldo.ps1 y disco.ps1 si. El icono se quedaba
# quieto, y un icono quieto durante una operacion larga se lee como "no esta
# pasando nada" -- que es exactamente lo que llevo a dar por colgada una corrida
# y matarla a mano el 2026-09-02. El pulso suave de la seccion 10.2 ya estaba
# escrito y probado; lo que faltaba era que alguien lo encendiera aqui.
#
# NO SE PISA UNA MARCA AJENA, y esta es la parte delicada. Cotejar mientras una
# corrida programada copia es legitimo. Si este guion pusiera su marca encima y
# la retirara al salir, dejaria el icono en verde fijo con la copia todavia
# corriendo -- una mentira tranquilizadora, que es la peor clase. Solo se retira
# la marca que se puso aqui.
$carpetaEstado = Get-CarpetaDeEstado -Configuracion $configuracion
$marcaPrevia = Get-MarcaDeCorrida -Carpeta $carpetaEstado
$marcaPuesta = $false
if (-not ($marcaPrevia.Existe -and -not $marcaPrevia.Vieja)) {
    [void](Enter-MarcaDeCorrida -Carpeta $carpetaEstado -Tipo 'verificacion' -Confirm:$false)
    $marcaPuesta = $true
}

try {
    # QUE SE LEE ENTERO (seccion 9): lo recien copiado, salvo que quien llama no
    # lo pase o que el muestreo de la raiz entera este vencido.
    $previo = Read-EstadoRespaldo -Carpeta $carpetaEstado
    $alcance = Get-AlcanceDelMuestreo -UltimoMuestreo ('' + $previo['muestreo_momento']) `
        -HayRecientes:($PSBoundParameters.ContainsKey('Recientes'))
    $argsEstado = @{ Configuracion = $configuracion; Muestra = $TamanoMuestra; RevisarDisco = $RevisarDisco }
    if ($alcance -eq 'recientes') { $argsEstado['Recientes'] = $Recientes }
    $informe = Get-EstadoDeRespaldo @argsEstado

    # LA COMPROBACION SE GUARDA, NO SOLO SE IMPRIME. Hasta ahora este resultado
    # salia por pantalla y moria al cerrar la ventana: el tablero no podia decir
    # cuando se comprobo por ultima vez, y "hace tres semanas que nadie comprueba
    # que lo copiado se lee" es una senal tan util como un fallo rojo.
    $detalleComprobacion = if ($informe.TodoCoincide) {
        'sin diferencias en {0} raices' -f @($informe.Coincidencias).Count
    }
    else {
        '{0} de {1} raices con diferencias' -f `
            @($informe.Coincidencias | Where-Object { -not $_.Coincide }).Count, @($informe.Coincidencias).Count
    }
    Write-EstadoDeComprobacion -Tipo 'huellas' -Correcto ([bool]$informe.TodoCoincide) `
        -Detalle $detalleComprobacion -Carpeta $carpetaEstado -Confirm:$false

    # La mitad "lo copiado se lee" la resuelve Test-CotejoLimpio, que vive en
    # testigo.ps1 para poder probarse: mira el CONTENIDO y no la estructura,
    # y exige haber leido algo. El porque, entero, esta en su cabecera.
    $cotejo = Test-CotejoLimpio -Informe $informe

    # EL MUESTREO SEMANAL SOLO SE DA POR HECHO SI LEYO ALGO de la raiz entera.
    # Con el nodo caido o la muestra a cero -opcion [9]- no se anota, y el
    # siguiente cotejo lo vuelve a intentar.
    if ($alcance -eq 'completo' -and $cotejo.Leidos -gt 0) {
        Write-EstadoDeComprobacion -Tipo 'muestreo' -Correcto ([bool]$cotejo.Limpio) `
            -Detalle ('{0} archivos leidos enteros, {1} distintos' -f $cotejo.Leidos, $cotejo.Distintos) `
            -Carpeta $carpetaEstado -Confirm:$false
    }

    # ---------------------------------------------------------------------
    #  EL LATIDO VERDE  -  ADR-0079, y hasta hoy no lo mandaba NADIE
    #
    #  respaldo.ps1 emitia /start al empezar y /fail al fallar, y el "estoy
    #  bien" se quedo sin emisor porque exige algo que la copia no puede
    #  afirmar: que lo copiado se LEE. Con el check ya creado, eso dejaba al
    #  testigo en rojo permanente incluso con el sistema sano -- una alarma
    #  que suena siempre es una alarma que se acaba desactivando.
    #
    #  LAS DOS CONDICIONES SE RESUELVEN AQUI Y NO SE PUEDEN SALTAR DESDE
    #  FUERA. No hay un parametro "manda verde igualmente": si falta
    #  cualquiera de las dos, Send-LatidoDelCliente lo degrada a /fail el
    #  solo. Esta funcion solo puede APORTAR hechos, no conclusiones.
    #
    #    corrida termino    lo dice ESTADO.txt, que escribio respaldo.ps1
    #    verificacion paso  lo dice esta corrida, que acaba de leer archivos
    #
    #  SE LEE ESTADO.txt EN VEZ DE FIARSE DE QUIEN LLAMA, y es deliberado: si
    #  respaldo.ps1 pasara "yo termine bien" como parametro, el verde se
    #  sostendria en la palabra del llamador. Asi se sostiene en lo que quedo
    #  escrito, que es lo mismo que mira el icono.
    if ($EmitirLatido) {
        $ultimo = Read-EstadoRespaldo -Carpeta $carpetaEstado
        $corridaTermino = (('' + $ultimo['estado']) -eq 'Protegido')

        $latido = Send-LatidoDelCliente -Senal 'Bien' `
            -CorridaTermino:$corridaTermino -VerificacionPaso:$cotejo.Limpio `
            -Detalle ('copia: {0}; cotejo {1}: {2} archivos leidos enteros, {3} distintos' -f `
                ('' + $ultimo['estado']), $alcance, $cotejo.Leidos, $cotejo.Distintos) `
            -Confirm:$false

        # SE ANOTA SIEMPRE, mande o no. "El testigo no esta configurado" tiene
        # que poder distinguirse de "el testigo dijo que todo va bien", y la
        # unica forma de verlo despues es que quede escrito.
        if ($latido.Enviado) {
            Write-RegistroRespaldo -Nivel 'OK' -Etapa 'testigo' -SoloArchivo `
                -Mensaje ('Latido {0} emitido al testigo externo.' -f $latido.Senal)
        }
        else {
            Write-RegistroRespaldo -Nivel 'ATENCION' -Etapa 'testigo' -SoloArchivo `
                -Mensaje ('Latido NO emitido: {0}' -f $latido.Motivo)
        }
    }

    $informe
}
finally {
    # VA EN finally Y NO AL FINAL DEL try: si el cotejo revienta a media red, la
    # marca tiene que irse igual. Una marca abandonada envejece y el icono pinta
    # FALLA -- cierto cuando el motor murio de verdad, y una mentira si solo es
    # que el nodo dejo de responder a mitad de una comprobacion.
    if ($marcaPuesta) { Exit-MarcaDeCorrida -Carpeta $carpetaEstado -Confirm:$false }
}
