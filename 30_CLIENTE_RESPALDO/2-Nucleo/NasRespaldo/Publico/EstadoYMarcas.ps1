#Requires -Version 5.1

# Modulo NasRespaldo - dominio: EstadoYMarcas.ps1

function Test-RastroDeCorridaProgramada {
    <#
        .SYNOPSIS
            Dice si la ultima corrida que disparo el Programador dejo rastro.

        .DESCRIPTION
            POR QUE EXISTE. El 2026-09-03 la tarea disparo sus TRES ventanas
            -06:26, 13:33 y 19:22-, el motor arranco las tres veces y murio en
            menos de un segundo cada vez: un `-Confirm:$false` detras de `-File`
            fallaba al enlazar y mataba el guion ANTES de su primera linea. Cero
            copias en todo el dia.

            Y NADA EN EL SISTEMA PODIA VERLO. El Programador decia LastRunTime
            19:22, LastTaskResult 0, State Ready y NumberOfMissedRuns 0, porque
            `conhost --headless` se traga el codigo de salida del hijo.
            `Test-MotorIntacto` -escrita justamente para cazar un motor que no
            corre- devolvia Intacto: True con tres corridas caidas.

            LO QUE NO SE PUEDE FALSEAR ES EL RASTRO. Cualquier corrida que pase
            del prologo escribe ESTADO.txt: `Enter-MarcaDeCorrida` y un
            `Copiando` salen en el primer segundo, y el `finally` garantiza que
            el estado final se escriba pase lo que pase dentro. Asi que si el
            Programador afirma haber arrancado el motor a una hora y lo ultimo
            escrito en ESTADO.txt es ANTERIOR a esa hora, el motor no llego a
            ejecutarse. Eso es exacto y no depende de ningun codigo de salida.

            LA GRACIA NO ES ADORNO: una corrida recien disparada todavia no ha
            escrito nada, y sin ella esta funcion acusaria a un motor que esta
            arrancando bien en ese mismo instante.

        .PARAMETER Info
            Lo que devuelve Get-ScheduledTaskInfo para la tarea del motor.

        .PARAMETER CarpetaEstado
            Donde vive ESTADO.txt.

        .PARAMETER GraciaMinutos
            Cuanto se le concede a una corrida para dejar su primera huella.
    #>
    [CmdletBinding()]
    [OutputType([psobject])]
    param(
        [Parameter(Mandatory)][psobject] $Info,

        [ValidateNotNullOrEmpty()]
        [string] $CarpetaEstado = (Get-CarpetaDeEstado),

        [ValidateRange(1, 1440)][int] $GraciaMinutos = 5
    )

    # Sin una hora de arranque creible no hay nada que juzgar. Una tarea que
    # nunca corrio trae 30/11/1999, que es "nunca" y no un fallo.
    if ($Info.PSObject.Properties.Name -notcontains 'LastRunTime') {
        return [pscustomobject]@{ Dejo = $true; Detalle = 'el Programador no informa de la ultima corrida' }
    }
    $ultima = $Info.LastRunTime -as [datetime]
    if ($null -eq $ultima -or $ultima.Year -lt 2000) {
        return [pscustomobject]@{ Dejo = $true; Detalle = 'la tarea todavia no ha corrido ninguna vez' }
    }
    if (((Get-Date) - $ultima).TotalMinutes -lt $GraciaMinutos) {
        return [pscustomobject]@{
            Dejo    = $true
            Detalle = ('la corrida de las {0:HH:mm} es demasiado reciente para juzgarla' -f $ultima)
        }
    }

    $estado = Read-EstadoRespaldo -Carpeta $CarpetaEstado
    [datetime] $momento = [datetime]::MinValue
    $hayMomento = $false
    if ($estado.ContainsKey('momento')) {
        $hayMomento = [datetime]::TryParse(('' + $estado['momento']), [ref]$momento)
    }
    if (-not $hayMomento) {
        return [pscustomobject]@{
            Dejo    = $false
            Detalle = ('arranco el {0:yyyy-MM-dd HH:mm} y NO DEJO RASTRO: no hay ESTADO.txt legible' -f $ultima)
        }
    }
    if ($momento -lt $ultima) {
        return [pscustomobject]@{
            Dejo    = $false
            Detalle = ('arranco el {0:yyyy-MM-dd HH:mm} y NO DEJO RASTRO: lo ultimo escrito es de {1:yyyy-MM-dd HH:mm}' -f $ultima, $momento)
        }
    }
    return [pscustomobject]@{
        Dejo    = $true
        Detalle = ('la corrida de las {0:HH:mm} dejo rastro' -f $ultima)
    }
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

            LA SEGUNDA MITAD ERA LastTaskResult, Y ERA UNA RED CON UN AGUJERO.
            La idea seguia siendo la correcta -una tarea puede existir, aparecer
            "Ready" y haber terminado su ultima corrida mal, y sin mirarlo "la
            tarea existe" se lee como "la tarea funciona"-. Lo que fallaba es la
            fuente: las dos tareas arrancan por `conhost --headless`, que
            DEVUELVE 0 pase lo que pase con el hijo. El 2026-09-03 esta funcion
            devolvio Intacto: True con las tres corridas del dia caidas.

            LA SEGUNDA MITAD ES AHORA EL RASTRO, que es lo unico que no se puede
            falsear: si el Programador afirma haber arrancado el motor a una hora
            y ESTADO.txt no tiene nada de esa hora en adelante, el motor no llego
            a ejecutarse. Ver Test-RastroDeCorridaProgramada. El codigo de salida
            se sigue mirando -un valor distinto de 0 seguiria significando algo-
            pero ya no es lo que sostiene la comprobacion.

        .PARAMETER RaizProyecto
            Carpeta del cliente. Por omision, la que contiene a 2-Nucleo.

        .PARAMETER Tareas
            Nombres de las tareas programadas a comprobar.

        .PARAMETER TareaMotor
            Cual de las de $Tareas es el motor. Solo a esa se le pide rastro:
            el indicador no escribe ESTADO.txt y no termina nunca.

        .PARAMETER CarpetaEstado
            Donde vive el ESTADO.txt contra el que se busca ese rastro.
    #>
    [CmdletBinding()]
    [OutputType([psobject])]
    param(
        [ValidateNotNullOrEmpty()]
        [string] $RaizProyecto = $script:RaizCliente,

        [string[]] $Tareas = @('NasRespaldo-Diario', 'NasRespaldo-Indicador'),

        [ValidateNotNullOrEmpty()]
        [string] $TareaMotor = 'NasRespaldo-Diario',

        [ValidateNotNullOrEmpty()]
        [string] $CarpetaEstado = (Get-CarpetaDeEstado)
    )

    # LAS PIEZAS SIN LAS QUE NO HAY RESPALDO. Se nombran una a una en vez de
    # recorrer la carpeta: recorrer no puede detectar una AUSENCIA, que es
    # justamente lo que hay que detectar.
    $piezas = @(
        '2-Nucleo\respaldo.ps1', '2-Nucleo\comun.ps1', '2-Nucleo\clasificar.ps1',
        '2-Nucleo\NasRespaldo\NasRespaldo.psd1', '2-Nucleo\NasRespaldo\NasRespaldo.psm1',
        '2-Nucleo\verificar.ps1', '2-Nucleo\disco.ps1', '2-Nucleo\semilla.ps1',
        '2-Nucleo\notificar.ps1', '2-Nucleo\testigo.ps1',
        '1-Interfaz\tablero.ps1', '1-Interfaz\indicador.ps1',
        '1-Interfaz\Registrar-Tarea.ps1', '1-Interfaz\estilo.ps1',
        '1-Interfaz\panel.ps1',
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
        #
        # PERO ESTE CODIGO CASI NUNCA PUEDE ACUSAR, Y HAY QUE DECIRLO AQUI. Las
        # dos tareas arrancan por `conhost.exe --headless`, y conhost DEVUELVE 0
        # pase lo que pase con el hijo -medido el 2026-09-03 con un guion que
        # deja una marca y sale con 3: la marca aparece y conhost devuelve 0-.
        # O sea que el Programador anota 0 aunque PowerShell haya muerto antes de
        # ejecutar una sola linea. Se conserva porque un codigo distinto de 0 SI
        # significaria algo (fallo del propio conhost), pero NO es la red que
        # sostiene esto: la red es Test-RastroDeCorridaProgramada, mas abajo.
        if ($info.LastTaskResult -ne 0 -and $info.LastTaskResult -ne 267009) {
            $tareasMal.Add(('{0} -- su ultima corrida termino con codigo {1}' -f $nombre, $info.LastTaskResult))
        }

        # LA COMPROBACION QUE DE VERDAD MIRA SI EL MOTOR CORRIO.
        # Solo para el motor: el indicador no escribe ESTADO.txt -solo lo lee- y
        # no termina nunca, asi que no tiene rastro que buscarle.
        if ($nombre -eq $TareaMotor) {
            $rastro = Test-RastroDeCorridaProgramada -Info $info -CarpetaEstado $CarpetaEstado
            if (-not $rastro.Dejo) { $tareasMal.Add(('{0} -- {1}' -f $nombre, $rastro.Detalle)) }
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
        # `copia_*` SE HEREDA, Y ES LO QUE IMPIDE QUE EL TABLERO MIENTA.
        # Las otras cuatro familias son de OTROS duenos -- la copia fria, el
        # cotejo, la semilla, la restauracion -- y se heredan para que una
        # corrida al nodo no borre lo que ellos escribieron.
        #
        # `copia_momento` es distinto: es del motor, pero describe un HECHO
        # PASADO -- cuando fue la ultima corrida que copio bien -- y ese hecho
        # sobrevive a la corrida de hoy. Si no se heredara, un aborto lo
        # borraria y el tablero volveria a quedarse sin nada que ensenar en la
        # linea "ult. copia", que es como se llego a pintar ahi `momento` y a
        # anunciar una copia que no ocurrio (04/09/2026).
        #
        # Su dueno no es "esta corrida": es "la ultima corrida buena". Solo la
        # pisa otra corrida buena, que es justo lo que se quiere.
        #
        # `laboratorio_*` es del guardado del laboratorio (seccion 5.2), que va
        # fuera del motor y dura horas. Sin heredarla, la primera corrida
        # programada que cayera durante un guardado borraba lo que el tablero
        # ensena de el.
        #
        # `muestreo_*` ES DEL COTEJO Y FALTABA AQUI DESDE QUE NACIO (51161fd,
        # 23/09). De `muestreo_momento` depende cuando toca releer la raiz
        # entera -una vez por semana, seccion 9-. Cada corrida lo borraba, el
        # cotejo de despues lo encontraba vacio y releia la raiz entera OTRA
        # VEZ: el muestreo semanal se hacia en todas las corridas, que es justo
        # el coste que 51161fd habia quitado. Se vio el 2026-10-09 porque
        # muestreo_momento y huellas_momento eran el mismo segundo.
        if ($clave -like 'disco_*' -or $clave -like 'huellas_*' -or
            $clave -like 'estructura_*' -or
            $clave -like 'semilla_*' -or $clave -like 'restauracion_*' -or
            $clave -like 'copia_*' -or $clave -like 'laboratorio_*' -or
            $clave -like 'muestreo_*') {
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
            'nodo' -la que corre sola-, 'disco' -la copia fria- o 'verificacion'
            -el cotejo de la opcion [3]-. SE GUARDA PORQUE DURAN COSAS DISTINTAS:
            la del nodo son minutos y la del disco puede ser horas, asi que el
            plazo a partir del cual una marca deja de ser creible no puede ser el
            mismo. Sin esto, una copia al disco perfectamente sana pintaba el
            icono de ROJO al pasar de hora y media.

            'verificacion' comparte el plazo del nodo -son minutos- y NO escribe
            estado: solo enciende el pulso del icono mientras lee. Igual que la
            copia fria, que tampoco pisa el veredicto del nodo.

            'laboratorio' es el guardado de D:\VMware_LAB (seccion 5.2), que va
            fuera del motor y dura horas. Va SIEMPRE en LABORATORIO.lock, nunca
            en EN_CURSO.lock: ver -Archivo.
        .PARAMETER Archivo
            EN_CURSO.lock para el motor, LABORATORIO.lock para el laboratorio.
            DOS ARCHIVOS PORQUE EL MOTOR NO PREGUNTA: respaldo.ps1 pone y retira
            EN_CURSO.lock en cada corrida sin mirar de quien era. Con una sola
            marca, la corrida programada de las 12:00 apagaba el aviso de un
            guardado del laboratorio que seguia copiando, y al terminar este
            borraba la marca de una corrida viva. Se exige aqui y no se confia
            al llamador: la pareja tipo-archivo cruzada lanza.
    #>
    [CmdletBinding(SupportsShouldProcess)]
    [OutputType([string])]
    param(
        [ValidateNotNullOrEmpty()]
        [string] $Carpeta = (Get-CarpetaDeEstado),

        [ValidateSet('nodo', 'disco', 'verificacion', 'laboratorio')]
        [string] $Tipo = 'nodo',

        [ValidateSet('EN_CURSO.lock', 'LABORATORIO.lock')]
        [string] $Archivo = 'EN_CURSO.lock'
    )
    if (($Tipo -eq 'laboratorio') -ne ($Archivo -eq 'LABORATORIO.lock')) {
        throw ("Una marca de tipo '{0}' no va en {1}: el laboratorio usa LABORATORIO.lock y el motor EN_CURSO.lock." -f $Tipo, $Archivo)
    }
    $ruta = Join-Path $Carpeta $Archivo
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
        .PARAMETER Archivo
            Cual de las dos marcas (ver Enter-MarcaDeCorrida).
    #>
    [CmdletBinding(SupportsShouldProcess)]
    [OutputType([void])]
    param(
        [ValidateNotNullOrEmpty()]
        [string] $Carpeta = (Get-CarpetaDeEstado),

        [ValidateSet('EN_CURSO.lock', 'LABORATORIO.lock')]
        [string] $Archivo = 'EN_CURSO.lock'
    )
    $ruta = Join-Path $Carpeta $Archivo
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
        .PARAMETER Archivo
            Cual de las dos marcas se lee (ver Enter-MarcaDeCorrida).
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
        [ValidateRange(1, 1440)][int] $MinutosParaViejaDisco = 480,

        # El laboratorio son ~277 GB por un nodo que escribe a 13 MB/s: la copia
        # entera ronda las 6 h, y ocho horas ya no serian holgadas. Un dia
        # entero, y otra vez quien manda es el PID.
        [ValidateRange(1, 1440)][int] $MinutosParaViejaLaboratorio = 1440,

        [ValidateSet('EN_CURSO.lock', 'LABORATORIO.lock')]
        [string] $Archivo = 'EN_CURSO.lock'
    )

    $ruta = Join-Path $Carpeta $Archivo
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
    $plazo = switch ($tipo) {
        'disco'       { $MinutosParaViejaDisco }
        'laboratorio' { $MinutosParaViejaLaboratorio }
        default       { $MinutosParaVieja }
    }
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

        [ValidateSet('', 'manual', 'automatico')]
        [string] $Origen = '',

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
    if ($Origen) { $datos['disco_origen'] = $Origen }

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

function Write-EstadoDelLaboratorio {
    <#
        .SYNOPSIS
            Anota como va el guardado del laboratorio SIN tocar el veredicto del
            nodo.

        .DESCRIPTION
            Seccion 5.2: D:\VMware_LAB se guarda en el nodo, a peticion y FUERA
            DE LA CORRIDA DIARIA: lo guarda laboratorio.ps1 desde el tablero
            (ADR-0104), o el guion v1 de D: en una emergencia. Fuera de la
            corrida no quiere decir invisible, y lo era: el 2026-10-09 se
            estaban subiendo 168 GB y el
            tablero decia "Protegido" con el icono quieto, porque nada de ese
            guardado pasaba por ESTADO.txt.

            ES EL TRATO DE LA COPIA FRIA (Write-EstadoDelDisco). Un destino
            aparte escribe SUS claves -laboratorio_*- y el veredicto principal,
            el que pinta el icono, sigue siendo el de las corridas diarias. Que
            el laboratorio vaya bien no dice nada del respaldo diario, ni al
            reves.

            "ULT. COPIA" NO SE MUEVE MIENTRAS SE COPIA. laboratorio_copia solo
            se escribe cuando la copia termino (Atencion o Protegido); empezar
            o fallar la conservan. Es la leccion del 04/09: esa linea llego a
            anunciar una copia que no habia ocurrido.
        .PARAMETER Estado
            Copiando mientras guarda; Atencion si ya se copio y falta el cotejo
            en la Pi; Protegido si se copio y se coteja; Falla si algo fallo.
        .PARAMETER Detalle
            Una frase para la persona.
        .PARAMETER Momento
            DE CUANDO ES LO GUARDADO, no cuando se anota: el instante en que se
            congelo el laboratorio con las VMs apagadas. El cotejo puede acabar
            horas despues, y lo que el nodo guarda es el laboratorio de aquel
            momento.
        .PARAMETER Tamano
            Cuanto ocupa lo guardado, ya escrito para la persona. Si no se da,
            se conserva el que hubiera.
        .PARAMETER Metodo
            Como viaja la copia: 'directo' (v1, con las VMs apagadas toda la
            copia) o 'foto-vss' (v2, desde una foto; las VMs pueden seguir
            encendidas). Sirve para que el tablero no pinte "guardando" como
            downtime cuando no lo es. Al empezar una copia sin decirlo se asume
            'directo'; los demas estados conservan el anterior. 'delta' es el
            del motor (laboratorio.ps1, ADR-0104): VMs apagadas y solo los
            bloques que cambiaron.
        .PARAMETER Carpeta
            Donde vive ESTADO.txt.
    #>
    [CmdletBinding(SupportsShouldProcess)]
    [OutputType([void])]
    param(
        [Parameter(Mandatory)]
        [ValidateSet('Protegido', 'Copiando', 'Atencion', 'Falla')]
        [string] $Estado,

        [ValidateNotNull()]
        [string] $Detalle = '',

        [ValidateNotNullOrEmpty()]
        [string] $Momento = (Get-Date -Format 's'),

        [ValidateNotNull()]
        [string] $Tamano = '',

        [ValidateSet('', 'directo', 'foto-vss', 'delta')]
        [string] $Metodo = '',

        [ValidateNotNullOrEmpty()]
        [string] $Carpeta = (Get-CarpetaDeEstado)
    )

    $previo = Read-EstadoRespaldo -Carpeta $Carpeta

    $datos = @{}
    foreach ($clave in $previo.Keys) {
        if ($clave -in @('estado', 'momento', 'detalle')) { continue }
        if ($clave -like 'laboratorio_*') { continue }
        $datos[$clave] = $previo[$clave]
    }
    $datos['laboratorio_estado']  = $Estado
    $datos['laboratorio_detalle'] = $Detalle
    $datos['laboratorio_momento'] = $Momento
    $datos['laboratorio_anotado'] = (Get-Date -Format 's')
    if ($Estado -in @('Atencion', 'Protegido')) {
        $datos['laboratorio_copia'] = $Momento
    }
    elseif ($previo.ContainsKey('laboratorio_copia')) {
        $datos['laboratorio_copia'] = $previo['laboratorio_copia']
    }
    if ($Tamano) { $datos['laboratorio_tamano'] = $Tamano }
    elseif ($previo.ContainsKey('laboratorio_tamano')) { $datos['laboratorio_tamano'] = $previo['laboratorio_tamano'] }

    # El metodo de la copia: 'directo' (v1, VMs apagadas toda la copia) o 'foto-vss'
    # (v2, copia desde una foto; las VMs pueden seguir encendidas). Lo usa el tablero
    # para que "guardando" no se lea como downtime cuando no lo es. Empezar una copia
    # sin decir metodo se asume 'directo'; los demas estados conservan el anterior.
    if ($Metodo) { $datos['laboratorio_metodo'] = $Metodo }
    elseif ($Estado -eq 'Copiando') { $datos['laboratorio_metodo'] = 'directo' }
    elseif ($previo.ContainsKey('laboratorio_metodo')) { $datos['laboratorio_metodo'] = $previo['laboratorio_metodo'] }

    # El veredicto principal, tal cual; sin uno, SinDatos y no un verde
    # prestado por el laboratorio.
    $estadoPrincipal = '' + $previo['estado']
    if ($estadoPrincipal -notin @('Protegido', 'Copiando', 'Atencion', 'Falla', 'SinDatos')) {
        $estadoPrincipal = 'SinDatos'
    }

    if (-not $PSCmdlet.ShouldProcess((Join-Path $Carpeta 'ESTADO.txt'), 'Anotar el guardado del laboratorio')) { return }

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
    # toque nada. Un centinela alterado NO se cura solo, y sigue valiendo lo que
    # valia. La causa 'freno' ya no la escribe nadie desde ADR-0085, pero los
    # ESTADO.txt de septiembre de 2026 la llevan dentro y esta funcion tiene que
    # seguir devolviendolos tal cual en vez de tropezar con ellos.
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
            'huellas' -la verificacion-, 'muestreo' -el pase semanal sobre la
            raiz entera- o 'semilla' -la prueba de leer el kit de arranque-.
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
        # 'muestreo' NO ES 'huellas'. Las huellas se anotan tras cualquier
        # cotejo; el muestreo, solo tras el que leyo de la raiz ENTERA. De su
        # fecha depende cuando toca el siguiente (seccion 9), y si el cotejo de
        # lo recien copiado la pisara, el pase semanal no llegaria nunca.
        [Parameter(Mandatory)]
        [ValidateSet('huellas', 'estructura', 'muestreo', 'semilla')]
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
