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
    [switch] $RevisarDisco
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

. "$PSScriptRoot\comun.ps1"
. "$PSScriptRoot\clasificar.ps1"

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

    return [pscustomobject]@{
        Raiz           = $Raiz.Ruta
        Clase          = $Raiz.Clase
        Destino        = $Destino
        DestinoExiste  = $existe
        Faltan         = $seco.NumACopiar
        Sobran         = $seco.NumABorrar
        Coincide       = ($seco.NumACopiar -eq 0 -and $seco.NumABorrar -eq 0)
        CodigoRobocopy = $seco.Codigo
    }
}

function Test-HuellaPorMuestreo {
    <#
        .SYNOPSIS
            Nivel 3, "el contenido es identico": huellas, y solo por muestreo.
        .DESCRIPTION
            El nivel 3 es CARO: leer entero lo que se respalda, en los dos lados,
            sobre un enlace de 13.4 MB/s. Por eso el contrato lo declara "por
            muestreo semanal" y no en cada corrida. Aqui se toma una muestra al
            azar y se comparan sus huellas archivo por archivo.

            Que la muestra pase NO demuestra que el resto este bien, y esta
            funcion no lo afirma: devuelve cuantos comprobo, para que quien lea
            el estado sepa el alcance de lo que se le esta diciendo.
        .PARAMETER Raiz
            La raiz ya resuelta.
        .PARAMETER Destino
            Su ruta en el destino.
        .PARAMETER Cuantos
            Tamano de la muestra.
    #>
    [CmdletBinding()]
    [OutputType([psobject])]
    param(
        [Parameter(Mandatory)][psobject] $Raiz,
        [Parameter(Mandatory)][ValidateNotNullOrEmpty()][string] $Destino,
        [Parameter(Mandatory)][ValidateRange(0, 10000)][int] $Cuantos
    )

    $diferencias = New-Object System.Collections.Generic.List[psobject]
    $comprobados = 0
    $ausentes    = 0

    if ($Cuantos -gt 0 -and (Test-Path -LiteralPath $Destino -PathType Container)) {
        $todos = @(Get-ChildItem -LiteralPath $Raiz.Ruta -File -Force -Recurse -ErrorAction SilentlyContinue)
        if ($todos.Count -gt 0) {
            $muestra = @($todos | Get-Random -Count ([math]::Min($Cuantos, $todos.Count)))
            foreach ($a in $muestra) {
                $relativa = $a.FullName.Substring($Raiz.Ruta.TrimEnd('\').Length).TrimStart('\')
                $gemelo = Join-Path $Destino $relativa
                if (-not (Test-Path -LiteralPath $gemelo -PathType Leaf)) {
                    $ausentes++
                    $diferencias.Add([pscustomobject]@{ Ruta = $relativa; Motivo = 'NO ESTA EN EL DESTINO' })
                    continue
                }
                $comprobados++
                # SHA256: aqui no hay linea base MD5 que respetar, y el nivel 3
                # es la afirmacion mas fuerte que hace este sistema.
                $hOrigen  = Get-HuellaDeArchivo -Ruta $a.FullName -Algoritmo SHA256
                $hDestino = Get-HuellaDeArchivo -Ruta $gemelo     -Algoritmo SHA256
                if ($hOrigen -ne $hDestino) {
                    $diferencias.Add([pscustomobject]@{ Ruta = $relativa; Motivo = 'HUELLA DISTINTA' })
                }
            }
        }
    }

    return [pscustomobject]@{
        Raiz        = $Raiz.Ruta
        Comprobados = $comprobados
        Ausentes    = $ausentes
        Diferencias = $diferencias.ToArray()
        Correcto    = ($diferencias.Count -eq 0)
    }
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
    #>
    [CmdletBinding()]
    [OutputType([psobject])]
    param(
        [Parameter(Mandatory)][psobject] $Configuracion,
        [ValidateRange(0, 10000)][int] $Muestra = 10,
        [switch] $RevisarDisco
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
            if ($Muestra -gt 0) {
                Write-Information ("        comprobando {0} huellas (lee los archivos enteros)..." -f $Muestra) -InformationAction Continue
            }
            $huellas       += Test-HuellaPorMuestreo -Raiz $r -Destino $destino -Cuantos $Muestra
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

Get-EstadoDeRespaldo -Configuracion $configuracion -Muestra $TamanoMuestra -RevisarDisco:$RevisarDisco
