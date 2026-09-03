#Requires -Version 5.1
<#
    .SYNOPSIS
        El motor de respaldo. Es el archivo que la seccion 7 nombra por su nombre.

    .DESCRIPTION
        Contrato: 30_CLIENTE_RESPALDO.md secciones 4, 6.2, 7 y 8.

        ORDEN OBLIGATORIO DE UNA CORRIDA. Las cuatro primeras etapas pueden
        abortar, y ninguna de ellas escribe un byte en el destino:

          0. LA DEUDA DE LA SECCION 8, y solo la primera vez.
             El arbol del nodo lo poblo una persona el 2026-09-02, no el motor.
             Documents y dev son CLASE B, o sea espejo: la primera corrida real
             compara lo que hay alli contra esta configuracion y BORRA lo que no
             coincida. Se comprueba que las raices resueltas son EXACTAMENTE las
             siete que se subieron, ni una mas ni una menos.

          1. GUARDAS DEL DESTINO. Que sea UNC de verdad y que el nodo responda.
             Una barra de menos convirtio \\192.168.1.38\datos en una carpeta
             local el 2026-09-02, y al nodo no llego ni un archivo.

          2. CENTINELAS (capa 3). Si uno no cuadra se ABORTA. No hay
             autorizacion posible: un centinela alterado significa que algo esta
             tocando archivos que nadie usa.

          3. FRENO (capa 2). Corrida en seco y conteo de cuantos archivos
             cambiarian o se borrarian. Si rebasa el umbral NO COPIA NADA: se
             detiene, avisa y ESPERA AUTORIZACION EXPLICITA (-AutorizarFreno).

          4. Copiar, invocando robocopy. NUNCA reimplementar la copia.

    .PARAMETER RutaConfiguracion
        Configuracion a usar. Por omision la de 3-Config. Las pruebas apuntan a
        un arenero con archivos de mentira.

    .PARAMETER SoloSimular
        Hace TODO menos copiar: guardas, centinelas y freno reales, y despues
        informa de lo que habria hecho. Es como se mira una corrida antes de
        dejarla correr de verdad.

    .PARAMETER AutorizarFreno
        Autorizacion explicita para continuar cuando el freno ha saltado. No
        tiene valor por omision a proposito: si el freno salta y nadie autoriza,
        no se copia.

    .PARAMETER OmitirDeuda
        Salta la comprobacion de la etapa 0. Existe para las pruebas y para
        cuando la deuda ya se declaro saldada. En una corrida real sobre el nodo
        es exactamente lo que no hay que hacer.

    .PARAMETER DesdeTarea
        La lanzo el Programador de tareas, no una persona. LO PASA SOLO LA TAREA
        (ver Registrar-Tarea.ps1) y cambia UNA cosa: si esta corrida pertenece a
        una ventana de la cadencia.

        SIN ESTO LA CADENCIA MIENTE. Una corrida que alguien lanza a mano a las
        17:30 no "llego tarde" a la ventana de las 12:00-15:00: no iba a esa
        cita. El 2026-09-02 el responsable simulo desde el tablero y el registro
        la acuso de TARDE, que es el sistema afirmando algo falso sobre si mismo.

    .EXAMPLE
        .\respaldo.ps1 -SoloSimular -Verbose
        Ensena que haria, sin tocar el destino.

    .EXAMPLE
        .\respaldo.ps1 -WhatIf
        Lo mismo que -SoloSimular pero por el mecanismo estandar de PowerShell.

    .NOTES
        Los .ps1 de este directorio se escriben SIN ACENTOS (ADR-0078).
#>
[CmdletBinding(SupportsShouldProcess, ConfirmImpact = 'High')]
[OutputType([psobject])]
param(
    [ValidateNotNullOrEmpty()]
    [string] $RutaConfiguracion,

    [switch] $SoloSimular,

    [switch] $AutorizarFreno,

    [switch] $OmitirDeuda,

    [switch] $DesdeTarea
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

. "$PSScriptRoot\comun.ps1"   # Test-Centinela y New-Centinela viven ahi
. "$PSScriptRoot\clasificar.ps1"
. "$PSScriptRoot\notificar.ps1"
. "$PSScriptRoot\testigo.ps1"

# ---------------------------------------------------------------------------
#  Etapa 0 - la deuda de la seccion 8
# ---------------------------------------------------------------------------

function Test-DeudaPrimeraCorrida {
    <#
        .SYNOPSIS
            Comprueba que las raices resueltas son EXACTAMENTE las declaradas.
        .DESCRIPTION
            La deuda que dejo adelantarse al motor. Se salda comparando el
            CONJUNTO de raices, que es lo que la seccion 8 pide: "ni una mas, ni
            una menos". Una raiz de mas significa copiar algo que nadie subio;
            una de menos significa que el espejo BORRARIA del nodo un arbol
            entero que si se subio.

            Los conteos y bytes del 2026-09-02 se comparan tambien, pero NO
            abortan: son procedencia, no invariante. Si el responsable edito un
            documento desde entonces, la cifra cambia y eso es normal. Abortar
            por ello seria un freno que suena todos los dias.
        .PARAMETER Configuracion
            El objeto de configuracion completo.
        .PARAMETER Raices
            Las raices ya resueltas por Get-RaicesDeRespaldo.
    #>
    [CmdletBinding()]
    [OutputType([psobject])]
    param(
        [Parameter(Mandatory)][psobject] $Configuracion,
        [Parameter(Mandatory)][AllowEmptyCollection()][psobject[]] $Raices
    )

    $deuda = $Configuracion.deudaPrimeraCorrida
    $declaradas = @($deuda.raices | ForEach-Object { $_.ruta.TrimEnd('\').ToLowerInvariant() })
    $resueltas  = @($Raices | ForEach-Object { $_.Ruta.TrimEnd('\').ToLowerInvariant() })

    $deMas   = @($resueltas | Where-Object { $declaradas -notcontains $_ })
    $deMenos = @($declaradas | Where-Object { $resueltas  -notcontains $_ })

    $deriva = New-Object System.Collections.Generic.List[psobject]
    foreach ($d in $deuda.raices) {
        $ruta = $d.ruta
        if (-not (Test-Path -LiteralPath $ruta -PathType Container)) { continue }
        $arch = @(Get-ChildItem -LiteralPath $ruta -File -Force -Recurse -ErrorAction SilentlyContinue)
        # Sumar a mano: con Set-StrictMode, Measure-Object sobre un conjunto
        # vacio no expone .Sum y el acceso a la propiedad revienta.
        [int64]$suma = 0
        foreach ($a in $arch) { $suma += $a.Length }
        if ($arch.Count -ne $d.archivos -or $suma -ne $d.bytes) {
            $deriva.Add([pscustomobject]@{
                Ruta            = $ruta
                ArchivosAhora   = $arch.Count
                ArchivosEn0902  = $d.archivos
                BytesAhora      = [int64]$suma
                BytesEn0902     = [int64]$d.bytes
            })
        }
    }

    $saldada = ($deMas.Count -eq 0 -and $deMenos.Count -eq 0)
    return [pscustomobject]@{
        Saldada          = $saldada
        RaicesDeMas      = $deMas
        RaicesDeMenos    = $deMenos
        RaicesDeclaradas = $declaradas.Count
        RaicesResueltas  = $resueltas.Count
        Deriva           = $deriva.ToArray()
    }
}

# ---------------------------------------------------------------------------
#  Etapa 3 - capa 2, el freno por tasa de cambio
# ---------------------------------------------------------------------------

function Measure-Freno {
    <#
        .SYNOPSIS
            Capa 2. Corrida en seco y conteo de lo que cambiaria o se borraria.
        .DESCRIPTION
            Un dia normal son decenas; un cifrado masivo son miles de golpe. La
            comparacion en seco ya se hace de todos modos, asi que esta capa
            cuesta lo que ya se estaba pagando.

            El porcentaje es SOBRE EL TOTAL DE ARCHIVOS DEL ORIGEN. Se informan
            por separado los que se copiarian y los que se BORRARIAN, porque no
            dan el mismo susto aunque el umbral sea uno solo: mil archivos que
            cambian es un dia de trabajo, mil que desaparecen es un desastre.
        .PARAMETER Raiz
            La raiz a medir, ya resuelta.
        .PARAMETER Destino
            Su ruta en el destino.
        .PARAMETER UmbralPorcentaje
            El de la configuracion.
    #>
    [CmdletBinding()]
    [OutputType([psobject])]
    param(
        [Parameter(Mandatory)][psobject] $Raiz,
        [Parameter(Mandatory)][ValidateNotNullOrEmpty()][string] $Destino,
        [Parameter(Mandatory)][ValidateRange(0, 100)][double] $UmbralPorcentaje,
        [ValidateRange(0, 100000)][int] $MinimoArchivos = 0
    )

    $totalOrigen = @(Get-ChildItem -LiteralPath $Raiz.Ruta -File -Force -Recurse -ErrorAction SilentlyContinue).Count
    $seco = Invoke-Robocopy -Origen $Raiz.Ruta -Destino $Destino -Clase $Raiz.Clase -SoloListar

    $afectados = $seco.NumACopiar + $seco.NumABorrar
    $porcentaje = if ($totalOrigen -gt 0) { [math]::Round(100.0 * $afectados / $totalOrigen, 2) } else { 0 }
    # Un destino vacio contra un origen con archivos no es un cifrado: es la
    # primera siembra. Se marca aparte para que el mensaje no mienta.
    $primeraSiembra = (-not (Test-Path -LiteralPath $Destino)) -or
                      (@(Get-ChildItem -LiteralPath $Destino -File -Force -Recurse -ErrorAction SilentlyContinue).Count -eq 0)

    # FRENA SOLO SI SE REBASAN LAS DOS COSAS. Calibrado el 2026-09-02: sin el
    # minimo absoluto, `Pictures\01_Capturas` -11 archivos, 5 % = 0.55- frena con
    # UNA captura nueva, y un freno que salta en el uso normal es un freno que se
    # acaba desactivando (ISA-18.2, la misma fatiga de alarmas que el contrato
    # cita para el semaforo). Ver respaldo.jsonc para los numeros medidos.
    $rebasado = ($porcentaje -gt $UmbralPorcentaje) -and ($afectados -ge $MinimoArchivos)

    return [pscustomobject]@{
        Raiz           = $Raiz.Ruta
        Clase          = $Raiz.Clase
        Destino        = $Destino
        TotalOrigen    = $totalOrigen
        ACopiar        = $seco.NumACopiar
        ABorrar        = $seco.NumABorrar
        Afectados      = $afectados
        Porcentaje     = $porcentaje
        Umbral         = $UmbralPorcentaje
        MinimoAbsoluto = $MinimoArchivos
        PrimeraSiembra = $primeraSiembra
        Rebasado       = $rebasado
        SinClasificar  = $seco.SinClasificar
        CodigoRobocopy = $seco.Codigo
    }
}

# ---------------------------------------------------------------------------
#  Guarda de origen - no esta en el contrato como capa, pero evita el
#  desastre que ninguna otra capa ve.
# ---------------------------------------------------------------------------

function Test-OrigenUtilizable {
    <#
        .SYNOPSIS
            Se niega a espejar desde un origen que no existe o esta vacio.
        .DESCRIPTION
            El freno mira la TASA de cambio; esta guarda mira el caso en que el
            origen desaparecio -letra de unidad distinta, carpeta renombrada,
            disco desconectado-. Con clase B eso es una orden de borrar el
            destino entero, y el freno la aprobaria si el umbral estuviera alto.
            Con clase A no borra, pero tampoco tiene sentido seguir.
        .PARAMETER Raiz
            La raiz a comprobar.
    #>
    [CmdletBinding()]
    [OutputType([bool])]
    param(
        [Parameter(Mandatory)][psobject] $Raiz
    )
    if (-not (Test-Path -LiteralPath $Raiz.Ruta -PathType Container)) {
        Write-RegistroRespaldo -Nivel 'ERROR' -Etapa 'guarda' -Mensaje "El origen no existe: $($Raiz.Ruta). No se copia ni se borra nada."
        return $false
    }
    $n = @(Get-ChildItem -LiteralPath $Raiz.Ruta -File -Force -Recurse -ErrorAction SilentlyContinue).Count
    if ($n -eq 0) {
        Write-RegistroRespaldo -Nivel 'ERROR' -Etapa 'guarda' -Mensaje "El origen esta VACIO: $($Raiz.Ruta). Con clase $($Raiz.Clase) eso seria una orden de vaciar el destino. Abortado."
        return $false
    }
    return $true
}

# ---------------------------------------------------------------------------
#  La corrida
# ---------------------------------------------------------------------------

function Invoke-CorridaDeRespaldo {
    <#
        .SYNOPSIS
            Ejecuta el orden completo de una corrida y devuelve su resultado.
        .PARAMETER Configuracion
            El objeto de configuracion completo.
        .PARAMETER Simular
            No copia: informa.
        .PARAMETER Autorizado
            El operador autoriza continuar aunque el freno haya saltado.
        .PARAMETER SaltarDeuda
            Omite la etapa 0.
    #>
    [CmdletBinding(SupportsShouldProcess, ConfirmImpact = 'High')]
    [OutputType([psobject])]
    param(
        [Parameter(Mandatory)][psobject] $Configuracion,
        [switch] $Simular,
        [switch] $Autorizado,
        [switch] $SaltarDeuda
    )

    $inicio = Get-Date
    $resultado = [ordered]@{
        Inicio       = $inicio
        Simulada     = [bool]$Simular
        Abortada     = $false
        Motivo       = $null
        # Token estable y legible por maquina de POR QUE se aborto. El Motivo es
        # una frase para la persona y puede reescribirse; la Causa la lee el
        # indicador para decidir si un fallo sigue vigente o ya se resolvio solo.
        Causa        = $null
        Raices       = @()
        Huerfanos    = @()
        Secretos     = @()
        Frenos       = @()
        Copias       = @()
        Deuda        = $null
        Centinelas   = $null
    }

    # --- Etapa 1a: resolver que se copia -----------------------------------
    $raices = Get-RaicesDeRespaldo -Configuracion $Configuracion
    $resultado.Raices = $raices
    if ($raices.Count -eq 0) {
        $resultado.Abortada = $true
        $resultado.Motivo = 'La configuracion no resuelve ninguna raiz. No hay nada que copiar.'
$resultado.Causa = 'sinRaices'
        Write-RegistroRespaldo -Nivel 'ERROR' -Etapa 'clasificar' -Mensaje $resultado.Motivo
        return [pscustomobject]$resultado
    }
    Write-RegistroRespaldo -Etapa 'clasificar' -Mensaje "Resueltas $($raices.Count) raices."

    $resultado.Huerfanos = Get-HuerfanosDeRespaldo -Configuracion $Configuracion
    foreach ($h in $resultado.Huerfanos) {
        Write-RegistroRespaldo -Nivel 'ATENCION' -Etapa 'huerfano' -Mensaje "$($h.Ruta) -- $($h.Motivo)"
    }

    # --- Etapa 0: la deuda de la seccion 8 ---------------------------------
    # Es una puerta DE UNA SOLA VEZ, atada a la subida manual del 2026-09-02.
    # Una vez saldada, anadir una carpeta numerada es operacion normal y NO
    # puede abortar: eso es justo el criterio 1 de la seccion 15. Se cierra
    # poniendo "saldada": true en la configuracion, que es un commit legible.
    $deudaYaSaldada = $false
    if ($Configuracion.deudaPrimeraCorrida.PSObject.Properties.Name -contains 'saldada') {
        $deudaYaSaldada = [bool]$Configuracion.deudaPrimeraCorrida.saldada
    }
    if (-not $SaltarDeuda -and -not $deudaYaSaldada) {
        $deuda = Test-DeudaPrimeraCorrida -Configuracion $Configuracion -Raices $raices
        $resultado.Deuda = $deuda
        if (-not $deuda.Saldada) {
            $resultado.Abortada = $true
            $resultado.Motivo = ('DEUDA DE LA SECCION 8 SIN SALDAR. Declaradas {0} raices, resueltas {1}. De mas: {2}. De menos: {3}.' -f
                $deuda.RaicesDeclaradas, $deuda.RaicesResueltas,
                (($deuda.RaicesDeMas -join '; ')   -replace '^$', 'ninguna'),
                (($deuda.RaicesDeMenos -join '; ') -replace '^$', 'ninguna'))
            Write-RegistroRespaldo -Nivel 'ERROR' -Etapa 'deuda' -Mensaje $resultado.Motivo
        $resultado.Causa = 'deudaSinSaldar'
            return [pscustomobject]$resultado
        }
        Write-RegistroRespaldo -Etapa 'deuda' -Mensaje "Deuda saldada: las $($deuda.RaicesResueltas) raices resueltas son las declaradas."
    }

    # --- Etapa 1b: guardas del destino -------------------------------------
    $unc = $Configuracion.destinos.nodo.unc
    if (-not (Test-DestinoNodo -Unc $unc)) {
        $resultado.Abortada = $true
        $resultado.Motivo = "Guarda de destino: '$unc' no es UNC alcanzable."
$resultado.Causa = 'destinoInalcanzable'
        return [pscustomobject]$resultado
    }
    $raizNodo = '{0}\{1}\{2}' -f $unc.TrimEnd('\'), $Configuracion.destinos.nodo.raiz, $Configuracion.destinos.nodo.prefijoEquipo

    # --- Etapa 2: centinelas -----------------------------------------------
    $cent = Test-Centinela -Centinelas @($Configuracion.centinelas)
    $resultado.Centinelas = $cent
    if (-not $cent.Correcto) {
        $resultado.Abortada = $true
        $resultado.Motivo = "CENTINELA ALTERADO. $($cent.Fallos.Count) de $($cent.Revisados) no cuadran. No se escribe nada, y esto no se puede autorizar."
$resultado.Causa = 'centinelaAlterado'
        Write-RegistroRespaldo -Nivel 'ERROR' -Etapa 'centinela' -Mensaje $resultado.Motivo
        foreach ($f in $cent.Fallos) {
            Write-RegistroRespaldo -Nivel 'ERROR' -Etapa 'centinela' -Mensaje "$($f.Ruta) -- $($f.Motivo)"
        }
        return [pscustomobject]$resultado
    }
    Write-RegistroRespaldo -Etapa 'centinela' -Mensaje "Centinelas correctos: $($cent.Revisados)."

    # --- Etapa 3: el freno, sobre TODAS las raices antes de copiar ninguna --
    $traducciones = @($Configuracion.destinos.nodo.traduccionDeRutas)
    $umbral = [double]$Configuracion.freno.umbralPorcentajeDeArchivosQueCambian
    $minimo = 0
    if ($Configuracion.freno.PSObject.Properties.Name -contains 'minimoArchivosParaFrenar') {
        $minimo = [int]$Configuracion.freno.minimoArchivosParaFrenar
    }
    $frenos = New-Object System.Collections.Generic.List[psobject]

    foreach ($r in $raices) {
        if (-not (Test-OrigenUtilizable -Raiz $r)) {
            $resultado.Abortada = $true
            $resultado.Motivo = "Guarda de origen: $($r.Ruta) no es utilizable."
$resultado.Causa = 'origenInutilizable'
            $resultado.Frenos = $frenos.ToArray()
            return [pscustomobject]$resultado
        }
        $destino = Get-RutaEnDestino -RutaOrigen $r.Ruta -RaizDestino $raizNodo -Traducciones $traducciones
        $frenos.Add((Measure-Freno -Raiz $r -Destino $destino -UmbralPorcentaje $umbral -MinimoArchivos $minimo))
    }
    $resultado.Frenos = $frenos.ToArray()

    $rebasadas = @($frenos | Where-Object { $_.Rebasado })
    if ($rebasadas.Count -gt 0 -and -not $Autorizado) {
        $resultado.Abortada = $true
        $resultado.Motivo = ('FRENO: {0} de {1} raices rebasan el umbral del {2} %. No se copia NADA hasta que se autorice con -AutorizarFreno.' -f
            $rebasadas.Count, $frenos.Count, $umbral)
        $resultado.Causa = 'freno'
        Write-RegistroRespaldo -Nivel 'FRENO' -Etapa 'freno' -Mensaje $resultado.Motivo
        foreach ($f in $rebasadas) {
            Write-RegistroRespaldo -Nivel 'FRENO' -Etapa 'freno' `
                -Mensaje ('{0} clase {1}: {2} % ({3} a copiar, {4} A BORRAR de {5}){6}' -f
                    $f.Raiz, $f.Clase, $f.Porcentaje, $f.ACopiar, $f.ABorrar, $f.TotalOrigen,
                    $(if ($f.PrimeraSiembra) { ' -- destino vacio: es la primera siembra, no un cifrado' } else { '' }))
        }
        return [pscustomobject]$resultado
    }
    if ($rebasadas.Count -gt 0) {
        Write-RegistroRespaldo -Nivel 'ATENCION' -Etapa 'freno' -Mensaje "Freno rebasado en $($rebasadas.Count) raices y AUTORIZADO explicitamente por el operador."
    }

    # --- Etapa 4: copiar ----------------------------------------------------
    if ($Simular) {
        Write-RegistroRespaldo -Etapa 'copia' -Mensaje 'Simulacion: hasta aqui llega. No se copia nada.'
        $resultado.Fin = Get-Date
        return [pscustomobject]$resultado
    }

    $copias = New-Object System.Collections.Generic.List[psobject]
    foreach ($r in $raices) {
        $destino = Get-RutaEnDestino -RutaOrigen $r.Ruta -RaizDestino $raizNodo -Traducciones $traducciones
        $accion = 'Copiar clase {0} a {1}' -f $r.Clase, $destino
        if (-not $PSCmdlet.ShouldProcess($r.Ruta, $accion)) { continue }

        $res = Invoke-Robocopy -Origen $r.Ruta -Destino $destino -Clase $r.Clase
        $copias.Add($res)
        if ($res.Correcto) {
            Write-RegistroRespaldo -Etapa 'copia' -Mensaje "OK $($r.Ruta) -> $destino (robocopy $($res.Codigo))"
        }
        else {
            Write-RegistroRespaldo -Nivel 'ERROR' -Etapa 'copia' -Mensaje "FALLO $($r.Ruta) -> $destino (robocopy $($res.Codigo), >= 8 es error)"
        }
    }
    $resultado.Copias = $copias.ToArray()
    $resultado.Fin = Get-Date
    return [pscustomobject]$resultado
}

# ---------------------------------------------------------------------------
#  La envoltura de estado, avisos y testigo - Fase 3
# ---------------------------------------------------------------------------

function Invoke-CorridaConEstado {
    <#
        .SYNOPSIS
            Corre el respaldo dejando rastro: ESTADO.txt, marca, evento y latido.
        .DESCRIPTION
            ENVOLTURA Y NO HILO SUELTO, a proposito. `Invoke-CorridaDeRespaldo`
            tiene siete puntos de salida -cada guarda que aborta es uno-, y
            escribir el estado en cada uno significa que el dia que se anada la
            octava guarda alguien se olvidara. Aqui el `finally` no se puede
            olvidar: pase lo que pase dentro, la marca se retira y el estado
            queda escrito.

            EL ORDEN IMPORTA. La marca se pone ANTES de empezar y se retira en el
            `finally`; si el motor muere a mitad, la marca se queda y el indicador
            la ve VIEJA -o con su PID muerto- y pinta FALLA. Es el criterio 9 de
            la seccion 15: matar el motor a media corrida pone el icono ROJO, no
            verde.
        .PARAMETER Configuracion
            El objeto de configuracion completo.
        .PARAMETER Simular
            No copia: informa.
        .PARAMETER Autorizado
            El operador autoriza continuar aunque el freno haya saltado.
        .PARAMETER SaltarDeuda
            Omite la etapa 0.
        .PARAMETER Programada
            La disparo el Programador de tareas, no una persona. Decide si esta
            corrida cuenta contra una ventana de la cadencia o no pertenece a
            ninguna -y por tanto si el registro puede llamarla TARDE-.
    #>
    [CmdletBinding(SupportsShouldProcess, ConfirmImpact = 'High')]
    [OutputType([psobject])]
    param(
        [Parameter(Mandatory)][psobject] $Configuracion,
        [switch] $Simular,
        [switch] $Autorizado,
        [switch] $SaltarDeuda,
        [switch] $Programada
    )

    if (-not $PSCmdlet.ShouldProcess('la corrida completa', 'Ejecutar respaldo')) { $Simular = $true }

    # La carpeta de estado sale de la CONFIGURACION, no de una ruta fija. Sin
    # esto, el arenero de pruebas escribia su ESTADO.txt encima del de
    # produccion y el indicador acababa pintando el resultado de una prueba
    # -medido el 2026-09-02-. Ademas es lo que el criterio 12 pide: clonar el
    # proyecto en otra maquina debe funcionar cambiando SOLO 3-Config.
    $carpetaEstado = Get-CarpetaDeEstado -Configuracion $Configuracion

    $rutaSistema = $null
    try {
        $rutaSistema = '{0}\{1}\{2}\_SISTEMA' -f `
            $Configuracion.destinos.nodo.unc.TrimEnd('\'),
            $Configuracion.destinos.nodo.raiz,
            $Configuracion.destinos.nodo.prefijoEquipo
    }
    catch { $rutaSistema = $null }

    # A QUE VENTANA PERTENECE ESTA CORRIDA. Es lo que hace auditable el sorteo:
    # con una hora fija bastaba anotar la hora, pero con un minuto sorteado
    # dentro de una ventana el registro tiene que poder afirmar que esa hora
    # CAIA DENTRO de la que le tocaba -o que no caia, y entonces es una corrida
    # recuperada por StartWhenAvailable despues de un apagon-. Sin esa linea, el
    # registro no distingue "todavia no le tocaba" de "no corrio".
    #
    # PERO SOLO SI LA DISPARO EL PROGRAMADOR. Una corrida a mano no pertenece a
    # ninguna ventana, y decir que "llego tarde" a una cita que no tenia es una
    # afirmacion falsa del sistema sobre si mismo. Se vio el 2026-09-02 en la
    # pantalla del responsable: pulso [2] simular a las 17:30 y el registro la
    # acuso de TARDE contra la ventana de las 12:00-15:00.
    #
    # UN PROBLEMA DE CADENCIA NO TUMBA LA CORRIDA. Copiar es lo importante y
    # anotar es lo secundario, que es el mismo orden que ya sigue el testigo.
    # Las dos declaradas ANTES del try: con StrictMode, leer una variable que el
    # try no llego a asignar es un error, y aqui se leen mas abajo.
    $ventana  = $null
    $cadencia = $null
    try {
        $cadencia = Get-CadenciaDeCorrida -Configuracion $Configuracion
        if ($Programada) {
            $ventana = Get-VentanaDeCorrida -Momento (Get-Date) -Cadencia $cadencia
            $nivelVentana = if ($ventana.ATiempo) { 'OK' } else { 'ATENCION' }
            Write-RegistroRespaldo -Nivel $nivelVentana -Etapa 'cadencia' -Mensaje $ventana.Descripcion
        }
        else {
            Write-RegistroRespaldo -Nivel 'OK' -Etapa 'cadencia' `
                -Mensaje ('corrida A MANO a las {0:HH:mm} - no pertenece a ninguna ventana y no cuenta contra la cadencia' -f (Get-Date))
        }
    }
    catch {
        Write-RegistroRespaldo -Nivel 'ATENCION' -Etapa 'cadencia' `
            -Mensaje "No se pudo situar la corrida en su ventana: $($_.Exception.Message)"
    }

    $resultado = $null
    try {
        if (-not $Simular) {
            Enter-MarcaDeCorrida -Carpeta $carpetaEstado -Confirm:$false | Out-Null
            Write-EstadoRespaldo -Estado 'Copiando' -Detalle 'Corrida en curso' -Carpeta $carpetaEstado -Confirm:$false
            Send-LatidoDelCliente -Senal 'Inicio' -Detalle 'corrida iniciada' -Confirm:$false | Out-Null
        }

        $resultado = Invoke-CorridaDeRespaldo -Configuracion $Configuracion `
            -Simular:$Simular -Autorizado:$Autorizado -SaltarDeuda:$SaltarDeuda -Confirm:$false
    }
    finally {
        if (-not $Simular) { Exit-MarcaDeCorrida -Carpeta $carpetaEstado -Confirm:$false }
    }

    if ($Simular -or $null -eq $resultado) { return $resultado }

    # --- Que estado deja esta corrida --------------------------------------
    $fallos = @($resultado.Copias | Where-Object { -not $_.Correcto })
    if ($resultado.Abortada) {
        # Un centinela alterado y un freno disparado NO son lo mismo, y el
        # indicador no debe pintarlos igual: el freno es una PARADA PRUDENTE que
        # espera una decision; el centinela es que algo esta tocando archivos que
        # nadie usa.
        if ($resultado.Motivo -like 'FRENO*') {
            $estado = 'Atencion'; $nivel = 'FRENO'; $situacion = 'respaldo.freno'
        }
        else {
            $estado = 'Falla';    $nivel = 'ERROR'; $situacion = 'respaldo.abortado'
        }
        $detalle = $resultado.Motivo
    }
    elseif ($fallos.Count -gt 0) {
        $estado = 'Falla'; $nivel = 'ERROR'; $situacion = 'respaldo.copia_fallida'
        $detalle = "{0} de {1} raices no se copiaron bien" -f $fallos.Count, $resultado.Copias.Count
    }
    else {
        $estado = 'Protegido'; $nivel = 'OK'; $situacion = 'respaldo.correcto'
        $copiados = 0
        foreach ($c in $resultado.Copias) { $copiados += $c.NumACopiar }
        $detalle = "{0} raices al dia, {1} archivos copiados" -f $resultado.Copias.Count, $copiados
    }

    $datos = @{
        raices    = @($resultado.Raices).Count
        huerfanos = @($resultado.Huerfanos).Count
        copias    = @($resultado.Copias).Count
        fallos    = $fallos.Count
    }

    # LA VENTANA VIAJA A ESTADO.txt, y hace falta para que el tablero pueda decir
    # "todavia no le tocaba" en vez de callarse: sin ella, quien abra la ventana
    # a las 09:00 no sabe si el sistema esta esperando o parado.
    #
    # SE ANOTA LA VENTANA Y NO UNA HORA, y esto se escribio mal primero. Se puso
    # `proxima` sacada de NextRunTime creyendo que traia el sorteo ya hecho.
    # NO LO TRAE: diez lecturas seguidas dan diez horas distintas -el Programador
    # sortea en cada consulta y solo fija el minuto al disparar-. Guardar esa
    # muestra habria puesto en ESTADO.txt un numero que se lee como promesa y
    # cambia solo, que es la clase de mentira que este archivo existe para no
    # cometer.
    if ($ventana) {
        $datos['ventana']         = $ventana.Ventana
        $datos['ventana_indice']  = '{0}/{1}' -f $ventana.Indice, $ventana.Total
        $datos['ventana_atiempo'] = $ventana.ATiempo
    }
    else {
        # "a mano" y no una ventana inventada. Que la ultima corrida la lanzara
        # una persona es un dato distinto de que llegara tarde a una cita, y el
        # tablero tiene que poder decirlo sin adornar.
        $datos['ventana'] = 'a mano'
    }
    if ($cadencia) {
        $siguiente = Get-ProximaVentanaDeCorrida -Cadencia $cadencia
        $datos['proxima_ventana'] = $siguiente.Ventana
        $datos['proxima_inicio']  = $siguiente.Inicio.ToString('s')

        # EL UMBRAL VIAJA CON EL ESTADO, Y NO ES UN DATO DE ADORNO.
        #
        # ESTADO.txt se publica tambien en el nodo (Publish-EstadoAlNodo), y
        # desde el 2026-09-02 el nodo lo lee para decir si el respaldo de este
        # equipo esta al dia. El umbral que separa "al dia" de "viejo" vive en
        # `cadencia.horasParaAvisar` de respaldo.jsonc, que es un archivo del
        # EQUIPO: el nodo no lo puede leer.
        #
        # Si el nodo se escribiera su propio 22, habria DOS criterios para la
        # misma pregunta y el dia que alguien separase las ventanas aqui, el
        # nodo seguiria juzgando con el numero viejo -y las dos pantallas se
        # contradirian sin que nada fallara-. Es exactamente lo que
        # 05_OPERACION seccion 4.1 prohibe y lo que la 13.3 exigio del testigo.
        #
        # Asi que lo publica quien lo posee. El nodo lo usa si esta; si falta,
        # se queda con su valor por omision y LO DICE.
        $datos['horas_para_avisar'] = $cadencia.HorasParaAvisar
        $datos['hueco_maximo_horas'] = $cadencia.HuecoNominalMaximoHoras
        $datos['equipo'] = $Configuracion.equipo
    }

    # LO QUE MIDIO EL FRENO, NO LO QUE DICE LA CONFIGURACION. El tablero
    # ensenaba "freno: 5 %" -el umbral- y eso solo repite lo que ya esta escrito
    # en el archivo. Lo que dice algo es el porcentaje que DE VERDAD cambio en
    # la ultima corrida: un 0.4 % frente a un umbral del 5 % es tranquilidad
    # medida, y un 4.8 % es un aviso que ningun umbral da por si solo.
    $medido = @($resultado.Frenos | Where-Object { $null -ne $_ } | ForEach-Object { [double]$_.Porcentaje })
    if ($medido.Count -gt 0) {
        $datos['cambio'] = '{0:N1}' -f (($medido | Measure-Object -Maximum).Maximum)
    }
    if ($resultado.Centinelas) {
        $revisados = [int]$resultado.Centinelas.Revisados
        $malos = @($resultado.Centinelas.Fallos).Count
        $datos['centinelas'] = '{0}/{1}' -f ($revisados - $malos), $revisados
    }
    # La causa viaja a ESTADO.txt para que el indicador pueda preguntarse si el
    # motivo del fallo SIGUE VIGENTE. Sin ella solo queda una frase en prosa, y
    # adivinar el estado del sistema leyendo prosa es como se construyen los
    # semaforos que mienten.
    if ($resultado.Causa) { $datos['causa'] = $resultado.Causa }
    Write-EstadoRespaldo -Estado $estado -Detalle $detalle -Datos $datos -Carpeta $carpetaEstado -Confirm:$false

    # LA TABLA POR RAIZ, QUE ES LA RESPUESTA A "QUE EXACTAMENTE". El tablero no
    # puede recorrer ocho raices por SMB cada vez que se abre, asi que la anota
    # quien acaba de medirla.
    Write-EstadoPorRaiz -Destino 'nodo' -Copias @($resultado.Copias) -Carpeta $carpetaEstado -Confirm:$false

    # La copia en el nodo, que pide la seccion 8. Va DESPUES de escribir el
    # original y antes de emitir: si el nodo esta, queda publicado; si no, se
    # anota y la corrida sigue siendo valida.
    if ($rutaSistema) { Publish-EstadoAlNodo -RutaSistema $rutaSistema -Carpeta $carpetaEstado -Confirm:$false | Out-Null }

    $emision = @{ Nivel = $nivel; Situacion = $situacion; Mensaje = $detalle; Hechos = $datos }
    if ($rutaSistema) { $emision['RutaSistema'] = $rutaSistema }
    Send-EventoAlNodo @emision -Confirm:$false | Out-Null

    # EL LATIDO VERDE NO SE MANDA AQUI. La corrida termino, pero "termino" no es
    # "esta bien": el verde exige ademas que la verificacion haya pasado
    # (ADR-0079). Quien la corre es verificar.ps1, y es quien puede afirmarlo.
    if ($estado -ne 'Protegido') {
        Send-LatidoDelCliente -Senal 'Mal' -Detalle $detalle -Confirm:$false | Out-Null
    }

    return $resultado
}

# ---------------------------------------------------------------------------
#  Punto de entrada
#
#  Si el archivo se carga CON PUNTO -como hacen las pruebas para alcanzar
#  Test-Centinela y New-Centinela- no debe correr nada. Solo se ejecuta cuando
#  se le invoca como guion.
# ---------------------------------------------------------------------------

if ($MyInvocation.InvocationName -eq '.') {
    Write-Verbose 'respaldo.ps1 cargado con punto: se exponen las funciones y no se ejecuta ninguna corrida.'
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

# -WhatIf es la forma estandar de pedir "ensename que harias"; la envoltura lo
# traduce a simulacion, asi que no hay dos caminos que mantener sincronizados.
Invoke-CorridaConEstado -Configuracion $configuracion `
    -Simular:$SoloSimular -Autorizado:$AutorizarFreno -SaltarDeuda:$OmitirDeuda `
    -Programada:$DesdeTarea `
    -WhatIf:$WhatIfPreference -Confirm:$false
