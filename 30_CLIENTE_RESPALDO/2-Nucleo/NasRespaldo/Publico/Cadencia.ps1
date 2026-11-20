#Requires -Version 5.1

# Modulo NasRespaldo - dominio: Cadencia.ps1

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

function Set-HorarioDeRespaldo {
    <#
        .SYNOPSIS
            Reescribe SOLO las ventanas de corrida y el umbral de aviso dentro de
            respaldo.jsonc. El resto del archivo queda igual, byte a byte.
        .DESCRIPTION
            SE EDITA EL TEXTO, NO EL OBJETO, Y ESA ES LA DECISION QUE MANDA AQUI.
            Volcar la configuracion con ConvertTo-Json produciria un archivo
            valido y DESTRUIRIA TODOS LOS COMENTARIOS -- que en este archivo no
            son adorno: son la mitad de la documentacion del sistema y explican
            por que cada numero vale lo que vale. Mover una ventana desde el menu
            no puede costar eso.

            Asi que se sustituyen exactamente dos trozos de texto -- el arreglo
            "ventanas" y el valor "horasParaAvisar" --, ambos claves unicas en el
            archivo. Y se sustituyen por posicion y longitud del hallazgo, no con
            Regex::Replace, porque en el texto de reemplazo el simbolo del dolar
            tiene significado y una llave con nombre raro podria colarse como
            referencia a un grupo.

            TRES GUARDAS, Y NINGUNA SOBRA:

              ANTES     la cadencia candidata pasa entera por
                        Get-CadenciaDeCorrida. Si dos ventanas se solapan, si
                        alguna cruza la medianoche, o si el umbral queda por
                        debajo del hueco recibol, NO SE ESCRIBE NADA. Las reglas
                        no se duplican aqui: se reutiliza el unico juez.
              DURANTE   Set-ContenidoAtomico, que escribe a un temporal y deja
                        copia del archivo anterior.
              DESPUES   se RELEE el archivo del disco y se comprueba que la
                        cadencia que sale de el es la que se pidio. Si no
                        coincide, se restaura el texto original y se lanza.

            LA TERCERA ES LA QUE IMPORTA de verdad. Una sustitucion por texto que
            saliera mal dejaria el archivo sin cadencia legible, y el motor SE
            NIEGA A CORRER sin ella: el respaldo se habria detenido por un ajuste
            de horario. Releer es la unica forma de enterarse en el momento y no
            tres corridas mas tarde.

            LO QUE ESTA FUNCION NO PUEDE TOCAR, y es deliberado (seccion 10.1):
            el umbral del freno, los centinelas, las clases y las exclusiones. Un
            horario es CUANDO se copia; esas cuatro son CUANTO se puede destruir
            antes de que el sistema se plante. Subir un umbral desde una pantalla
            desarma la defensa con dos pulsaciones y sin dejar rastro, y por eso
            siguen pidiendo abrir el archivo a mano.
        .PARAMETER Ventanas
            Las ventanas nuevas: objetos con `inicio` (HH:mm) y `duracionHoras`.
        .PARAMETER HorasParaAvisar
            El umbral nuevo. CERO significa "conserva el que ya hay".
        .PARAMETER Ruta
            El .jsonc. Por omision, el de 3-Config junto a este guion.
    #>
    [CmdletBinding(SupportsShouldProcess, ConfirmImpact = 'High')]
    [OutputType([psobject])]
    param(
        [Parameter(Mandatory)]
        [ValidateCount(1, 8)]
        [psobject[]] $Ventanas,

        [ValidateRange(0, 720)]
        [int] $HorasParaAvisar = 0,

        # CALCULA Y NO ESCRIBE, EN SILENCIO. Existe en vez de reusar -WhatIf
        # porque -WhatIf pinta su propia linea en la consola -- "Se esta
        # realizando la operacion..." -- y la vista previa del tablero es una
        # pantalla cuidada donde esa linea ensena las tripas y encima repite en
        # otro idioma lo que la pantalla ya dice mejor.
        #
        # Recorre exactamente el mismo camino: valida la cadencia entera y
        # comprueba que los dos trozos de texto existen en el archivo. Lo unico
        # que no hace es escribir.
        [switch] $SoloCalcular,

        [ValidateNotNullOrEmpty()]
        [string] $Ruta = (Join-Path $script:RaizCliente '3-Config\respaldo.jsonc')
    )

    $actual = Get-ConfiguracionRespaldo -Ruta $Ruta
    $avisar = if ($HorasParaAvisar -gt 0) { $HorasParaAvisar } else { [int]$actual.cadencia.horasParaAvisar }

    # LA GUARDA DE ANTES. Se arma una configuracion de mentira con solo lo que va
    # a cambiar y se le pide su veredicto al mismo juez que usa el motor.
    $candidato = [pscustomobject]@{
        cadencia = [pscustomobject]@{
            ventanas        = @($Ventanas | ForEach-Object {
                    [pscustomobject]@{ inicio = ('' + $_.inicio); duracionHoras = [double]$_.duracionHoras }
                })
            horasParaAvisar = $avisar
        }
    }
    $nueva = Get-CadenciaDeCorrida -Configuracion $candidato

    $texto = Get-Content -LiteralPath $Ruta -Raw -Encoding UTF8

    $patronVentanas = '"ventanas"\s*:\s*\[[^\]]*\]'
    $hallado = [regex]::Match($texto, $patronVentanas)
    if (-not $hallado.Success) {
        throw "No se encontro el arreglo `"ventanas`" en $Ruta. No se toca un archivo que no se reconoce."
    }

    # Se respeta la forma que ya tenia el archivo: una ventana por linea, con la
    # misma sangria. Un ajuste desde el menu no puede dejar el archivo con un
    # aspecto distinto al que tenia, o la proxima diferencia de git seria ilegible.
    # EL FINAL DE LINEA SE HEREDA DEL ARCHIVO, NO DEL SISTEMA OPERATIVO, y esto
    # costo un defecto real: con [Environment]::NewLine se metieron CRLF en un
    # archivo que el repositorio mantiene en LF por .gitattributes. El contenido
    # quedaba identico y git no ensenaba ninguna diferencia -- solo un aviso de
    # que iba a normalizarlo --, asi que el archivo se quedaba con finales de
    # linea MEZCLADOS sin que nada fallara. Se elige el que ya manda en el
    # archivo, contando, y asi vale igual si algun dia se decide lo contrario.
    $conRetorno = ([regex]::Matches($texto, "`r`n")).Count
    $sinRetorno = ([regex]::Matches($texto, "(?<!`r)`n")).Count
    $finDeLinea = if ($conRetorno -gt $sinRetorno) { "`r`n" } else { "`n" }

    $filas = @($nueva.Ventanas | ForEach-Object {
            '      {{ "inicio": "{0}", "duracionHoras": {1} }}' -f $_.Inicio, ([math]::Round($_.LargoMinutos / 60, 2))
        })
    $bloque = '"ventanas": [' + $finDeLinea + ($filas -join (',' + $finDeLinea)) + $finDeLinea + '    ]'

    $nuevoTexto = $texto.Remove($hallado.Index, $hallado.Length).Insert($hallado.Index, $bloque)

    $patronAvisar = '"horasParaAvisar"\s*:\s*\d+'
    $halladoAvisar = [regex]::Match($nuevoTexto, $patronAvisar)
    if (-not $halladoAvisar.Success) {
        throw "No se encontro `"horasParaAvisar`" en $Ruta."
    }
    $nuevoTexto = $nuevoTexto.Remove($halladoAvisar.Index, $halladoAvisar.Length).
    Insert($halladoAvisar.Index, ('"horasParaAvisar": {0}' -f $avisar))

    if ($SoloCalcular) {
        return [pscustomobject]@{ Escrito = $false; Cadencia = $nueva; Motivo = 'SoloCalcular' }
    }

    $queCambia = 'Horario: {0} corridas, {1}' -f $nueva.CorridasPorDia, $nueva.Resumen
    if (-not $PSCmdlet.ShouldProcess($Ruta, $queCambia)) {
        return [pscustomobject]@{ Escrito = $false; Cadencia = $nueva; Motivo = 'WhatIf' }
    }

    Set-ContenidoAtomico -Ruta $Ruta -Contenido $nuevoTexto -Confirm:$false

    # LA GUARDA DE DESPUES. Se relee del disco, no de la variable.
    try {
        $releida = Get-ConfiguracionRespaldo -Ruta $Ruta
        $comprobada = Get-CadenciaDeCorrida -Configuracion $releida
    }
    catch {
        Set-ContenidoAtomico -Ruta $Ruta -Contenido $texto -Confirm:$false
        throw "El archivo quedo ilegible tras el cambio y SE RESTAURO el anterior: $($_.Exception.Message)"
    }
    if ($comprobada.Resumen -ne $nueva.Resumen -or $comprobada.HorasParaAvisar -ne $nueva.HorasParaAvisar) {
        Set-ContenidoAtomico -Ruta $Ruta -Contenido $texto -Confirm:$false
        throw ("Lo escrito no es lo pedido y SE RESTAURO el anterior. Pedido: '{0}' / {1} h. Leido: '{2}' / {3} h." -f `
                $nueva.Resumen, $nueva.HorasParaAvisar, $comprobada.Resumen, $comprobada.HorasParaAvisar)
    }

    Write-RegistroRespaldo -Nivel 'ATENCION' -Etapa 'cadencia' -SoloArchivo `
        -Mensaje ('HORARIO CAMBIADO desde el tablero: {0} corridas al dia, {1}. Umbral de aviso {2} h, hueco maximo {3} h.' -f `
            $comprobada.CorridasPorDia, $comprobada.Resumen, $comprobada.HorasParaAvisar, $comprobada.HuecoNominalMaximoHoras)

    return [pscustomobject]@{ Escrito = $true; Cadencia = $comprobada; Motivo = $null }
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

        [datetime] $Momento = (Get-Date),

        # Cuando la corrida programada de la ventana en curso acaba de
        # terminar, la "proxima" ventana que debe anotar ESTADO.txt es la
        # siguiente, no la que acaba de completarse.
        [switch] $ExcluirActual
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

    # Dentro de una ventana: le toca AHORA, salvo que se pida la siguiente tras
    # cerrar la actual.
    $dentro = @($Cadencia.Ventanas | Where-Object { $min -ge $_.InicioMinutos -and $min -lt $_.FinMinutos })
    if (-not $ExcluirActual -and $dentro.Count -gt 0) {
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
