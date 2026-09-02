#Requires -Version 5.1
<#
    .SYNOPSIS
        Las DOS pasadas al disco frio. Siempre aditivas, sin excepcion.

    .DESCRIPTION
        Contrato: 30_CLIENTE_RESPALDO.md secciones 6, 6.1, 6.2 y 6.2.bis.
        Criterios de aceptacion 7, 7b, 7c y 7d.

        EL DISCO TIENE DOS ORIGENES, NO UNO, y esa correccion salio de un hecho
        que aporto el responsable: las fotos del telefono se suben DIRECTAMENTE
        al nodo y nunca pasan por el equipo. Si el disco se sincronizara solo
        desde el equipo, esas fotos tendrian dos copias -telefono y nodo- ambas
        en la misma casa.

          equipo -> disco    lo que nace en el equipo, clases A y B
          nodo   -> disco    los arboles que SOLO existen en el nodo

        SIEMPRE ADITIVO, TODA CLASE, SIN EXCEPCION. Es la ultima red: un borrado
        que llegue hasta aqui ya no tiene de donde volver. Aqui NO se espeja
        aunque la raiz sea clase B en el nodo; eso es deliberado y es la
        diferencia entre respaldo y sincronizacion.

        LA INDEPENDENCIA ENTRE COPIAS SE SOSTIENE POR EL MODO, NO POR EL ORIGEN.
        Una corrupcion del nodo podria anadir basura al disco, pero no puede
        destruir lo que ya esta bien. Esa es la razon por la que traer datos del
        nodo no rompe la regla 3-2-1.

        DOS CONDICIONES QUE EL IMPLEMENTADOR NO PUEDE PASAR POR ALTO (seccion 6.2):

          1. La pasada nodo -> disco necesita EL NODO ALCANZABLE **Y** EL DISCO
             CONECTADO A LA VEZ. El equipo es quien orquesta: lee del recurso
             compartido y escribe al disco.

          2. Si el disco esta pero el nodo no responde, corre SOLO la pasada
             equipo -> disco Y LO DICE. NUNCA declarar "al dia" una copia fria a
             la que le falto la mitad. Esta funcion devuelve `Completa = $false`
             en ese caso, y quien lo ignore esta mintiendo a proposito.

        EL PRIMER NIVEL DEL DISCO ES EL NOMBRE DE LA MAQUINA (seccion 6.2.bis).
        Con eso las dos pasadas tienen una raiz cada una y NO PUEDEN PISARSE
        JAMAS: equipo bajo EQUIPO-01\, nodo bajo NODO-01\. Es una propiedad de
        seguridad, no una preferencia estetica.

        EL DISCO SE RECONOCE POR NUMERO DE SERIE, NUNCA POR LETRA (seccion 6.1),
        y la confirmacion es el archivo centinela en su raiz. Los dos criterios,
        y los dos tienen que pasar: es como el criterio 7b -"conectar otro disco
        USB cualquiera"- se resuelve sin depender de que nadie renombre nada.

    .PARAMETER RutaConfiguracion
        Configuracion a usar. Por omision la de 3-Config.

    .PARAMETER SoloSimular
        Hace las guardas y la comparacion en seco, y no copia.

    .PARAMETER OmitirNodo
        Corre solo la pasada equipo -> disco. La copia queda declarada INCOMPLETA.

    .EXAMPLE
        .\disco.ps1 -SoloSimular
        .\disco.ps1
#>
[CmdletBinding(SupportsShouldProcess, ConfirmImpact = 'High')]
[OutputType([psobject])]
param(
    [ValidateNotNullOrEmpty()]
    [string] $RutaConfiguracion,

    [switch] $SoloSimular,

    [switch] $OmitirNodo
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

. "$PSScriptRoot\comun.ps1"
. "$PSScriptRoot\clasificar.ps1"

function Invoke-PasadaAlDisco {
    <#
        .SYNOPSIS
            Copia un conjunto de raices al disco. SIEMPRE en modo aditivo.
        .DESCRIPTION
            La clase de la raiz NO decide el modo aqui: al disco todo va
            aditivo, "sin excepcion", porque es la ultima red (seccion 6). Lo
            que la clase decide es el modo hacia el NODO, no hacia el disco.
        .PARAMETER Raices
            Las raices ya resueltas.
        .PARAMETER RaizDestino
            F:\EQUIPO-01 o F:\NODO-01, ya con el nombre de maquina.
        .PARAMETER Traducciones
            La tabla de/a que corresponda a esta pasada.
        .PARAMETER Simular
            No copia: solo mide.
    #>
    [CmdletBinding(SupportsShouldProcess, ConfirmImpact = 'High')]
    [OutputType([psobject[]])]
    param(
        [Parameter(Mandatory)][AllowEmptyCollection()][psobject[]] $Raices,
        [Parameter(Mandatory)][ValidateNotNullOrEmpty()][string] $RaizDestino,
        [Parameter(Mandatory)][psobject[]] $Traducciones,
        [switch] $Simular
    )

    $resultados = New-Object System.Collections.Generic.List[psobject]
    foreach ($r in $Raices) {
        $destino = Get-RutaEnDestino -RutaOrigen $r.Ruta -RaizDestino $RaizDestino -Traducciones $Traducciones

        if ($Simular) {
            $seco = Invoke-Robocopy -Origen $r.Ruta -Destino $destino -Clase 'A' -SoloListar
            $resultados.Add([pscustomobject]@{
                Origen = $r.Ruta; Destino = $destino; Simulado = $true
                NumACopiar = $seco.NumACopiar; NumABorrar = $seco.NumABorrar
                Codigo = $seco.Codigo; Correcto = $seco.Correcto
                SinClasificar = $seco.SinClasificar
            })
            continue
        }

        if (-not $PSCmdlet.ShouldProcess($r.Ruta, "Copiar al disco frio en $destino")) { continue }
        $res = Invoke-Robocopy -Origen $r.Ruta -Destino $destino -Clase 'A'
        $resultados.Add($res)

        if ($res.Correcto) {
            Write-RegistroRespaldo -Etapa 'disco' -Mensaje "OK $($r.Ruta) -> $destino (robocopy $($res.Codigo), $($res.NumACopiar) archivos)"
        }
        else {
            Write-RegistroRespaldo -Nivel 'ERROR' -Etapa 'disco' -Mensaje "FALLO $($r.Ruta) -> $destino (robocopy $($res.Codigo))"
        }
    }
    return $resultados.ToArray()
}

function Invoke-CorridaAlDiscoFrio {
    <#
        .SYNOPSIS
            Las dos pasadas, con sus guardas y su veredicto honesto.
        .PARAMETER Configuracion
            El objeto de configuracion completo.
        .PARAMETER Simular
            No copia.
        .PARAMETER SinNodo
            Fuerza a saltarse la pasada del nodo.
    #>
    [CmdletBinding(SupportsShouldProcess, ConfirmImpact = 'High')]
    [OutputType([psobject])]
    param(
        [Parameter(Mandatory)][psobject] $Configuracion,
        [switch] $Simular,
        [switch] $SinNodo
    )

    $resultado = [ordered]@{
        Inicio        = Get-Date
        Simulada      = [bool]$Simular
        Abortada      = $false
        Motivo        = $null
        Disco         = $null
        PasadaEquipo  = @()
        PasadaNodo    = @()
        NodoAlcanzable= $false
        Completa      = $false
    }

    # --- Guarda 1: el disco, por SERIE y por CENTINELA -----------------------
    # Los dos criterios. Asi es como el criterio 7b -"conectar otro disco USB
    # cualquiera"- se resuelve: sin serie coincidente NI centinela, no se
    # escribe nada. Y el criterio 7 -"otra letra"- sale gratis, porque la letra
    # no se usa para identificar.
    $disco = Get-DiscoFrio -Configuracion $Configuracion
    if (-not $disco) {
        $resultado.Abortada = $true
        $resultado.Motivo = 'El disco frio no esta conectado, o no supera la prueba de serie y centinela. No se escribe nada.'
        Write-RegistroRespaldo -Nivel 'ATENCION' -Etapa 'disco' -Mensaje $resultado.Motivo
        return [pscustomobject]$resultado
    }
    $resultado.Disco = $disco
    Write-RegistroRespaldo -Etapa 'disco' -Mensaje "Disco frio reconocido en $($disco.Raiz) por serie $($disco.Serie) y centinela."

    # --- Guarda 2: los centinelas del equipo --------------------------------
    # Si algo esta tocando archivos que nadie usa, tampoco se escribe al disco.
    $cent = Test-Centinela -Centinelas @($Configuracion.centinelas)
    if (-not $cent.Correcto) {
        $resultado.Abortada = $true
        $resultado.Motivo = "CENTINELA ALTERADO: $($cent.Fallos.Count) de $($cent.Revisados). No se escribe en el disco frio."
        Write-RegistroRespaldo -Nivel 'ERROR' -Etapa 'disco' -Mensaje $resultado.Motivo
        return [pscustomobject]$resultado
    }

    $ficha = $Configuracion.destinos.discoFrio

    # LA COPIA FRIA TAMBIEN SE MARCA, Y HASTA HOY NO LO HACIA. El icono resuelve
    # "copiando" leyendo EN_CURSO.lock, que solo ponia la corrida al nodo: una
    # copia fria de hora y media dejaba el icono en verde fijo, diciendo
    # "protegido, nada en curso", mientras el disco escribia. Lo noto el
    # responsable mirando la barra el 2026-09-02. Un indicador que no se entera
    # de la mitad del trabajo no es un indicador.
    $carpetaEstado = Get-CarpetaDeEstado -Configuracion $Configuracion
    if (-not $Simular) {
        Enter-MarcaDeCorrida -Carpeta $carpetaEstado -Tipo 'disco' -Confirm:$false | Out-Null
        Write-EstadoRespaldo -Estado 'Copiando' -Detalle 'Copia al disco frio en curso' `
            -Carpeta $carpetaEstado -Confirm:$false
    }

    try {
        # --- Pasada A: equipo -> disco --------------------------------------
        $raicesEquipo = Get-RaicesDeRespaldo -Configuracion $Configuracion
        $raizEquipo = Join-Path $disco.Raiz $ficha.prefijoEquipo
        $resultado.PasadaEquipo = Invoke-PasadaAlDisco -Raices $raicesEquipo `
            -RaizDestino $raizEquipo -Traducciones @($ficha.traduccionDeRutas) `
            -Simular:$Simular -Confirm:$false

        # --- Pasada B: nodo -> disco ----------------------------------------
        # EXIGE LAS DOS COSAS A LA VEZ. Si el nodo no responde, esta pasada no
        # corre y la copia queda declarada INCOMPLETA.
        $nodoVivo = $false
        if (-not $SinNodo) {
            $nodoVivo = Test-DestinoNodo -Unc $Configuracion.destinos.nodo.unc
        }
        $resultado.NodoAlcanzable = $nodoVivo

        if ($nodoVivo) {
            $raicesNodo = Get-RaizDelNodoParaDisco -Configuracion $Configuracion
            $raizNodo = Join-Path $disco.Raiz $ficha.prefijoNodo
            $resultado.PasadaNodo = Invoke-PasadaAlDisco -Raices $raicesNodo `
                -RaizDestino $raizNodo -Traducciones @($ficha.traduccionDelNodo) `
                -Simular:$Simular -Confirm:$false
            $resultado.Completa = $true
        }
        else {
            $resultado.Completa = $false
            $motivo = if ($SinNodo) { 'se pidio omitir el nodo' } else { 'el nodo no responde' }
            # ESTA LINEA ES EL PUNTO DE LA SECCION 6.2. Nunca declarar "al dia"
            # una copia fria a la que le falto la mitad.
            Write-RegistroRespaldo -Nivel 'ATENCION' -Etapa 'disco' `
                -Mensaje "COPIA FRIA INCOMPLETA: corrio solo la pasada equipo -> disco porque $motivo. Lo que solo vive en el nodo NO llego."
        }
    }
    finally {
        # SIEMPRE en finally: una marca que se queda puesta hace que el icono
        # pinte FALLA, y eso es verdad cuando el motor murio y mentira cuando
        # solo faltaba retirarla.
        if (-not $Simular) { Exit-MarcaDeCorrida -Carpeta $carpetaEstado -Confirm:$false }
    }

    if (-not $Simular) {
        # UNA COPIA FRIA A LA QUE LE FALTO UNA PASADA NO SE PINTA DE VERDE.
        if ($resultado.Completa) {
            Write-EstadoRespaldo -Estado 'Protegido' `
                -Detalle 'Copia fria COMPLETA: las dos pasadas corrieron' `
                -Carpeta $carpetaEstado -Confirm:$false
        }
        else {
            Write-EstadoRespaldo -Estado 'Atencion' `
                -Detalle 'Copia fria INCOMPLETA: lo que solo vive en el nodo NO llego al disco' `
                -Carpeta $carpetaEstado -Confirm:$false
        }
    }

    $resultado.Fin = Get-Date
    return [pscustomobject]$resultado
}

if ($MyInvocation.InvocationName -eq '.') {
    Write-Verbose 'disco.ps1 cargado con punto: se exponen las funciones y no se copia nada.'
    return
}

# LA VENTANA NEGRA, ESCONDIDA SI ES NUESTRA. Lanzado por la tarea o por un
# lanzador externo, este proceso creo su propia consola y aparecia una
# ventana en el escritorio en cada corrida. Lanzado desde una terminal
# abierta no se toca nada: ahi la ventana es de quien la abrio.
Hide-VentanaDeConsola | Out-Null

$parametrosConfig = @{}
if ($PSBoundParameters.ContainsKey('RutaConfiguracion')) { $parametrosConfig['Ruta'] = $RutaConfiguracion }
$configuracion = Get-ConfiguracionRespaldo @parametrosConfig

Invoke-CorridaAlDiscoFrio -Configuracion $configuracion `
    -Simular:$SoloSimular -SinNodo:$OmitirNodo -WhatIf:$WhatIfPreference -Confirm:$false
