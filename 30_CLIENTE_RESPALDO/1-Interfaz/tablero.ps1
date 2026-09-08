#Requires -Version 5.1
<#
    .SYNOPSIS
        El menu de opciones sobre la ventana de estado.

    .DESCRIPTION
        Contrato: 30_CLIENTE_RESPALDO.md seccion 10.1.

        NINGUNA INTERFAZ ES EL UNICO CAMINO: el motor tiene que correr sin menu
        y sin icono desde la tarea programada, o nada se podria automatizar
        (seccion 10). Este archivo NO contiene logica de respaldo: llama al
        motor, igual que lo llamaria la tarea.

        SUPERFICIE DE ATAQUE: ninguna nueva. Proceso local, sesion del usuario,
        sin puerto ni servicio. Lo que pudiera ejecutar el menu ya podia
        ejecutar el respaldo.

        EL RIESGO REAL ES OTRO: que el menu permita debilitar la proteccion sin
        que se note. Por eso el reparto NO es negociable, y este archivo lo
        respeta por omision:

          DESDE EL MENU              SOLO EDITANDO respaldo.jsonc
          pausar raices              UMBRAL DEL FRENO
          verbosidad                 CENTINELAS
          lanzar acciones            POLITICA DE BORRADO
          re-registrar la tarea      CLASES
                                     VENTANAS DE CORRIDA

        La ventana los ENSENA todos para que se puedan mirar, y dice donde se
        cambian. Ver no es poder cambiar.

        "HORA DE CORRIDA" CAMBIO DE COLUMNA AL CERRAR EL PENDIENTE 26, y no es
        un detalle. Cuando habia UNA hora, cambiarla era elegir un momento del
        dia. Ahora son TRES VENTANAS SACADAS DE UNA MEDICION de 30 dias, y de
        ellas cuelgan el umbral del icono, el ambar de la tabla y lo que espera
        el testigo: juntarlas o moverlas a ojo degrada la proteccion sin que se
        note, que es justo el riesgo que la seccion 10.1 nombra al partir el
        reparto en dos. Lo que si queda en el menu es RE-REGISTRAR LA TAREA, que
        es lo que hay que hacer despues de editar el archivo. Queda anotado en el
        contrato como enmienda pendiente del visto bueno del responsable.

        LAS CINCO ACCIONES QUE PIDE LA SECCION 10.1 ESTAN TODAS: copiar al nodo,
        copiar al disco, verificar huellas, probar restauracion y ajustes. Las
        dos ultimas faltaban hasta el 2026-09-02, y su ausencia no se habia
        notado porque nada las comprobaba.

        NUNCA SE CANALIZA HACIA Write-Information, Y ESTO COSTO UN MENU ROTO.
        `Write-Information` tiene `MessageData` OBLIGATORIO y por canalizacion:
        si lo que llega es una cadena vacia -y `Out-String` devuelve exactamente
        eso cuando no hay nada que formatear-, el enlace del parametro falla y
        PowerShell SE PARA A PEDIRLO por consola. Desde fuera se ve como "elegi
        una opcion, salio un mensaje raro y no hizo nada". Se resuelve
        formateando a variable y escribiendo solo si hay texto: Show-Texto.

        Y CADA OPCION VA EN try/catch: un fallo dentro de una accion tiene que
        contarse y devolver el menu, no tumbar la ventana. Un tablero que se
        cierra ante el primer error es un tablero que no se usa el dia malo.

        EL ESTADO LO RESUELVE EL INDICADOR, NO ESTE ARCHIVO, y es a proposito:
        si el tablero calculara el suyo, la barra y la ventana podrian contar
        historias distintas del mismo momento y no habria forma de saber cual
        creer. Se invoca con -UnaSolaLectura, SIN CARGARLO CON PUNTO: indicador
        .ps1 tiene bloque param(), y cargar con punto un guion con param()
        reinicia esas variables en quien lo carga. Asi se convirtio una
        simulacion en una copia real el 2026-09-02 (seccion 12.undecies).

        EL DIBUJO VIVE EN estilo.ps1. Aqui solo se decide QUE se ensena.

    .PARAMETER RutaConfiguracion
        Configuracion a usar. Por omision la de 3-Config.

    .EXAMPLE
        .\tablero.ps1
#>
[CmdletBinding()]
[OutputType([void])]
param(
    [ValidateNotNullOrEmpty()]
    [string] $RutaConfiguracion
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$nucleo = Join-Path (Split-Path $PSScriptRoot -Parent) '2-Nucleo'
. "$nucleo\comun.ps1"
. "$nucleo\testigo.ps1"
. "$PSScriptRoot\estilo.ps1"

$script:NombreTarea     = 'NasRespaldo-Diario'
$script:NombreIndicador = 'NasRespaldo-Indicador'

$script:Capacidades = Initialize-Consola
$script:Paleta      = Get-Paleta -Capacidades $script:Capacidades
$script:Regla       = Get-ReglaDeAviso -Unicode $script:Capacidades.Unicode

function Show-Texto {
    <#
        .SYNOPSIS
            Escribe un objeto ya formateado, y NADA si no hay nada que escribir.
        .DESCRIPTION
            El sustituto de canalizar hacia Write-Information. Ver la cabecera:
            MessageData es obligatorio y una cadena vacia rompe el enlace del
            parametro, dejando el menu pidiendo un valor por consola.
        .PARAMETER Objeto
            Lo que se quiere ensenar. Puede ser $null.
        .PARAMETER Vacio
            Que decir cuando no hay nada. Vacio para no decir nada.
        .PARAMETER Lista
            Formatear como lista en vez de como tabla.
    #>
    [CmdletBinding()]
    [OutputType([void])]
    param(
        [Parameter(Mandatory)][AllowNull()] $Objeto,
        [string] $Vacio = '',
        [switch] $Lista
    )

    $texto = ''
    if ($null -ne $Objeto) {
        $texto = if ($Lista) {
            $Objeto | Format-List | Out-String -Width 200
        }
        else {
            $Objeto | Format-Table -AutoSize | Out-String -Width 200
        }
    }
    if ([string]::IsNullOrWhiteSpace($texto)) {
        if ($Vacio) { Write-Information ('   ' + $Vacio) -InformationAction Continue }
        return
    }
    Write-Information $texto.TrimEnd() -InformationAction Continue
}

function Write-Linea {
    <#
        .SYNOPSIS
            Una linea suelta fuera del marco. Nunca vacia.
        .PARAMETER Texto
            Lo que se escribe.
    #>
    [CmdletBinding()]
    [OutputType([void])]
    param([Parameter(Mandatory)][AllowEmptyString()][string] $Texto)
    # UN ESPACIO, NO UNA CADENA VACIA. La maqueta separa bloques con lineas en
    # blanco, pero MessageData de Write-Information es obligatorio y ademas
    # acepta pipeline: pasarle '' falla el enlace y PowerShell se para a pedirlo
    # por consola, que en una tarea programada es un cuelgue silencioso.
    if ([string]::IsNullOrEmpty($Texto)) { $Texto = ' ' }
    Write-Information $Texto -InformationAction Continue
}

function Get-EstadoDelIndicador {
    <#
        .SYNOPSIS
            Pregunta al indicador en que estado esta. Nunca lanza.
        .DESCRIPTION
            Se llama al guion, no se carga con punto. Si algo falla se devuelve
            SinDatos: una ventana que no abre porque el estado no se pudo leer
            es peor que una ventana que dice que no lo sabe.
    #>
    [CmdletBinding()]
    [OutputType([psobject])]
    param()

    try {
        $v = & "$PSScriptRoot\indicador.ps1" -UnaSolaLectura
        if ($v) { return $v }
    }
    catch {
        Write-Verbose "El indicador no pudo resolver el estado: $($_.Exception.Message)"
    }
    return [pscustomobject]@{ Estado = 'SinDatos'; Detalle = 'No se pudo leer el estado'; Fuente = 'error' }
}

function Format-Espacio {
    <#
        .SYNOPSIS
            Bytes libres en la frase corta del tablero. Vacio si no se sabe.
        .DESCRIPTION
            NO SABER NO ES CERO. Un destino que no responde no tiene "0 GB
            libres": no tiene dato, y pintar un cero ahi es una alarma inventada
            en una pantalla cuyo unico trabajo es no mentir.
        .PARAMETER Bytes
            Lo que devolvio Get-EspacioLibre, que puede ser $null.
    #>
    [CmdletBinding()]
    [OutputType([string])]
    param(
        [AllowNull()][System.Nullable[long]] $Bytes
    )
    if ($null -eq $Bytes) { return '' }
    if ($Bytes -ge 1TB) { return '{0:N1} TB libres' -f ($Bytes / 1TB) }
    return '{0:N0} GB libres' -f ($Bytes / 1GB)
}

function Write-LineaDeSumario {
    <#
        .SYNOPSIS
            Una linea de resumen: rotulo a la izquierda y partes separadas.
        .PARAMETER Etiqueta
            El rotulo del bloque.
        .PARAMETER Partes
            Los trozos, que se unen con un separador.
        .PARAMETER Paleta
            Los colores.
        .PARAMETER Color
            Color del contenido. Por defecto, el del valor.
    #>
    [CmdletBinding()]
    [OutputType([void])]
    param(
        [Parameter(Mandatory)][string] $Etiqueta,
        [Parameter(Mandatory)][AllowEmptyCollection()][string[]] $Partes,
        [Parameter(Mandatory)][hashtable] $Paleta,
        [AllowEmptyString()][string] $Color = '',

        # LA REGLA LATERAL DEL COMPONENTE `aviso` DEL PANEL. Alli un aviso no se
        # marca tinendole la letra: se marca con una caja de borde izquierdo
        # -.aviso{border-left:2px solid}- y .mal, .bien y .ojo solo cambian ese
        # borde. Aqui es un bloque medio, y la lleva la unica linea que es un
        # aviso de verdad, no todas: una marca que esta siempre no marca nada.
        [switch] $Avisar
    )
    $utiles = @($Partes | Where-Object { -not [string]::IsNullOrWhiteSpace($_) })
    if ($utiles.Count -eq 0) { return }
    if ([string]::IsNullOrEmpty($Color)) { $Color = $Paleta.Valor }
    # EL ANCHO VISIBLE SE LLEVA APARTE, Y ESTA LINEA EXPLICA POR QUE. Aqui se
    # calculaba la sangria como 12 - $Marca.Length, y $Marca lleva los codigos
    # de color DENTRO: con color, .Length vale 23 en vez de 1, la resta daba
    # -11 y la ventana se tumbaba entera al abrirla. Con la salida redirigida
    # no hay color, $Marca mide 1, y el fallo no aparece -- que es justo por lo
    # que se me paso: lo comprobe donde no podia ocurrir.
    #
    # Es el mismo error contra el que avisa Format-Celda dos funciones mas
    # abajo. Contar una cadena ya pintada da un ancho falso, siempre.
    $Marca = ''
    $anchoMarca = 0
    if ($Avisar) {
        $Marca = '{0}{1}{2}' -f $Color, $script:Regla, $Paleta.Fin
        $anchoMarca = $script:Regla.Length
    }

    # SE PARTE EN VARIAS LINEAS, NO SE RECORTA. Recortar con puntos suspensivos
    # esconde justo el ultimo aviso de la lista, y en la linea PENDIENTE el
    # ultimo era "el testigo externo no esta configurado": el aviso mas
    # importante de la pantalla desaparecia por caber mal.
    $sitio = $script:AnchoTablero - 12
    $separador = '   -   '
    $lineas = New-Object System.Collections.Generic.List[string]
    $actual = ''
    foreach ($parte in $utiles) {
        $candidata = if ($actual) { $actual + $separador + $parte } else { $parte }
        if ($candidata.Length -le $sitio -or -not $actual) { $actual = $candidata; continue }
        $lineas.Add($actual)
        $actual = $parte
    }
    if ($actual) { $lineas.Add($actual) }

    $primera = $true
    foreach ($linea in $lineas) {
        $rotulo = if ($primera) { $Etiqueta } else { '' }
        Write-Linea ('  {0}{1}{2}{3}{4}' -f `
                $Marca, `
            (Format-Celda -Texto $rotulo -Ancho (12 - $anchoMarca) -Paleta $Paleta -Color $Paleta.Etiqueta), `
                $Color, (Limit-Texto -Texto $linea -Maximo $sitio), $Paleta.Fin)
        $primera = $false
    }
}

function Format-EtiquetaDeRaiz {
    <#
        .SYNOPSIS
            El nombre corto de una raiz para la columna de la tabla.
        .DESCRIPTION
            SEIS RAICES EMPIEZAN IGUAL, Y RECORTADAS POR LA DERECHA SE VOLVIAN
            LA MISMA FILA. Con la columna a 28 caracteres, cinco raices salian
            como "C:\Users\usuario\Documents\..." y la tabla dejaba de distinguir
            lo unico que tenia que distinguir. Medido pintandola el 2026-09-02.

            El prefijo del perfil se sustituye por ~, que es como se escribe una
            ruta de casa en cualquier sitio, y lo que sobrevive es el tramo que
            de verdad diferencia una raiz de otra.
        .PARAMETER Raiz
            La ruta tal cual la guardo el motor.
    #>
    [CmdletBinding()]
    [OutputType([string])]
    param(
        [Parameter(Mandatory)][AllowEmptyString()][string] $Raiz
    )

    $perfil = [Environment]::GetFolderPath('UserProfile')
    if ($perfil -and $Raiz.StartsWith($perfil, [System.StringComparison]::OrdinalIgnoreCase)) {
        return '~' + $Raiz.Substring($perfil.Length)
    }
    return $Raiz
}

function Write-CuerpoDeTabla {
    <#
        .SYNOPSIS
            Las filas de la tabla por raiz, una por raiz y con las dos columnas.
        .DESCRIPTION
            SE UNEN LOS DOS DESTINOS POR LA ETIQUETA DE LA RAIZ. Una raiz puede
            estar en el nodo y no en el disco -es lo normal, el disco se conecta
            a peticion- y otra puede estar solo en el disco, porque la copia
            fria arrastra ademas lo que solo vive en el nodo. Las dos aparecen,
            y la columna que no tiene dato dice que no lo tiene.

            UNA COLUMNA VACIA NO SE PINTA DE VERDE. Es la misma regla de
            Write-EstadoDelDisco un piso mas abajo: "no me consta" y "esta bien"
            no se dibujan igual.
        .PARAMETER Filas
            Lo que devolvio Read-EstadoPorRaiz.
        .PARAMETER Paleta
            Los colores.
        .PARAMETER HorasParaAmbar
            A partir de cuantas horas una copia buena deja de pintarse verde.

            NO ES UN DIA DE CALENDARIO, Y ESE ERA UN DEFECTO REAL. Hasta el
            2026-09-02 la celda se pintaba verde solo si la ultima copia habia
            sido HOY, asi que con la corrida unica de las 22:30 el tablero
            habria pintado las ocho raices en AMBAR diciendo "ayer 22:30" desde
            medianoche hasta las 22:30 del dia siguiente: 22 horas y media de
            ambar al dia con el sistema perfectamente sano. Nadie lo habia visto
            porque la tarea no habia llegado a correr ni una vez -su LastRunTime
            era 11/30/1999- y las corridas de ese dia fueron a mano.

            Ahora el color sale del HUECO NOMINAL de la cadencia: mientras la
            copia sea mas reciente que lo que el reparto de ventanas promete, es
            verde. La etiqueta sigue diciendo "hoy 05:26" o "ayer 21:14", que es
            como lo lee una persona; lo que cambia es quien decide el color.
    #>
    [CmdletBinding()]
    [OutputType([void])]
    param(
        [Parameter(Mandatory)][AllowEmptyCollection()][psobject[]] $Filas,
        [Parameter(Mandatory)][hashtable] $Paleta,
        [ValidateRange(1, 720)][double] $HorasParaAmbar = 12
    )

    if (@($Filas).Count -eq 0) {
        Write-Linea ('  {0}Todavia no hay tabla: la escribe el motor al copiar. Corre [1].{2}{1}' -f `
                $Paleta.Tenue, $Paleta.Fin, '')
        return
    }

    $porDestino = @{}
    foreach ($f in $Filas) {
        if (-not $porDestino.ContainsKey($f.Destino)) { $porDestino[$f.Destino] = @{} }
        $porDestino[$f.Destino][$f.Raiz] = $f
    }

    # El orden lo manda el nodo -es la que corre sola- y lo que solo tenga el
    # disco se anade detras, sin perderse.
    $orden = New-Object System.Collections.Generic.List[string]
    foreach ($clave in @('nodo', 'disco')) {
        if (-not $porDestino.ContainsKey($clave)) { continue }
        foreach ($r in ($porDestino[$clave].Keys | Sort-Object)) {
            if (-not $orden.Contains($r)) { $orden.Add($r) }
        }
    }

    foreach ($raiz in $orden) {
        $enNodo = $null; $enDisco = $null
        if ($porDestino.ContainsKey('nodo'))  { $enNodo  = $porDestino['nodo'][$raiz] }
        if ($porDestino.ContainsKey('disco')) { $enDisco = $porDestino['disco'][$raiz] }
        $ref = if ($enNodo) { $enNodo } else { $enDisco }

        # LA COLUMNA DICE LO QUE EL NUMERO ES, NO LO QUE GUSTARIA QUE FUERA.
        # La maqueta la titulaba PENDIENTE, pero el dato que el motor puede dar
        # sin volver a escanear es cuantos archivos MOVIO el ultimo pase. Saber
        # lo que falta de verdad exige un robocopy en seco por raiz, que son los
        # minutos que este tablero no puede gastar. Llamarla "pendiente"
        # ensenaria 67 junto a AL DIA y se leeria como 67 archivos en riesgo.
        $movidos = [int]$ref.Pendientes
        if ($enDisco -and [int]$enDisco.Pendientes -gt $movidos) { $movidos = [int]$enDisco.Pendientes }

        $celdas = @()
        $columna = 0
        foreach ($lado in @($enNodo, $enDisco)) {
            $columna++
            if ($null -eq $lado) {
                # UNA RAIZ QUE VIVE EN EL NODO NO LE "FALTA" AL NODO. _HISTORICO
                # y homeUsers son del nodo: la copia fria los arrastra, pero
                # nadie los copia AL nodo porque ya estan ahi. Un guion en esa
                # celda se lee como un hueco, y un hueco en la columna del nodo
                # se lee como un fallo.
                #
                # Para lo demas, ni verde ni rojo: no consta, que es distinto
                # de estar bien.
                # Solo en la columna del NODO: en la del disco, que le falte a
                # una raiz del nodo si es un hueco de verdad.
                $texto = '--'
                if ($columna -eq 1 -and $raiz -notmatch '^[A-Za-z]:') { $texto = 'es el origen' }
                $celdas += (Format-Celda -Texto $texto -Ancho 13 -Paleta $Paleta -Color $Paleta.Gris)
                continue
            }
            if ($lado.Estado -ne 'AlDia') {
                $celdas += (Format-Celda -Texto 'FALLO' -Ancho 13 -Paleta $Paleta -Color $Paleta.Rojo)
                continue
            }
            # AL DIA A SECAS CUANDO ESTA DENTRO DEL HUECO QUE PROMETE LA
            # CADENCIA, Y LA EDAD CUANDO NO. Poner la fecha siempre no cabia en
            # la columna y se recortaba a "AL DIA hoy...", que es ruido; y una
            # copia de hace doce dias que dijera solo "AL DIA" seria peor: al
            # dia de cuando.
            #
            # EL CORTE ES POR HORAS Y NO POR DIA DE CALENDARIO. Con el corte por
            # dia, una corrida de las 21:14 se ponia ambar a medianoche -tres
            # horas despues- aunque la siguiente ventana no abriera hasta las
            # 04:00. Eso es avisar de algo que no ha pasado.
            $edad = Format-Antiguedad -Momento $lado.Momento
            [datetime] $cuando = [datetime]::MinValue
            $reciente = $false
            if ([datetime]::TryParse(('' + $lado.Momento), [ref] $cuando)) {
                $reciente = (((Get-Date) - $cuando).TotalHours -le $HorasParaAmbar)
            }
            if ($reciente) {
                $celdas += (Format-Celda -Texto 'AL DIA' -Ancho 13 -Paleta $Paleta -Color $Paleta.Verde)
            }
            else {
                $celdas += (Format-Celda -Texto $edad -Ancho 13 -Paleta $Paleta -Color $Paleta.Ambar)
            }
        }

        Write-Linea ('  {0}{1}{2}{3}{4}' -f `
            (Format-Celda -Texto (Format-EtiquetaDeRaiz -Raiz $raiz) -Ancho 30 -Paleta $Paleta -Color $Paleta.Valor), `
            (Format-Celda -Texto ('' + $ref.Clase) -Ancho 3 -Paleta $Paleta -Color $Paleta.Tenue), `
            (Format-Celda -Texto ('{0} arch. ' -f $movidos) -Ancho 11 -Paleta $Paleta -Derecha `
                    -Color $Paleta.Tenue), `
                $celdas[0], $celdas[1])
    }
}

function Get-EstadoDeTarea {
    <#
        .SYNOPSIS
            Las dos tareas programadas, en trozos para una linea de resumen.
        .DESCRIPTION
            Que las tareas existan es la diferencia entre un respaldo y un guion
            que alguien tiene que acordarse de ejecutar, asi que el tablero lo
            dice en la cara aunque la maqueta no lo dibujara: la maqueta es
            anterior a que las tareas existieran.
    #>
    [CmdletBinding()]
    [OutputType([string[]])]
    param()

    $partes = New-Object System.Collections.Generic.List[string]
    foreach ($par in @(
            @{ Eti = 'motor'; Tarea = $script:NombreTarea },
            @{ Eti = 'icono'; Tarea = $script:NombreIndicador })) {
        $t = Get-ScheduledTask -TaskName $par.Tarea -ErrorAction SilentlyContinue
        if (-not $t) { $partes.Add(('{0} NO EXISTE' -f $par.Eti)); continue }
        if ($t.State -eq 'Disabled') { $partes.Add(('{0} DESHABILITADO' -f $par.Eti)); continue }

        $hora = ''
        if ($par.Eti -eq 'motor') {
            # SE CUENTAN LAS VENTANAS, NO SE ENSENA LA PRIMERA HORA. Esta linea
            # decia "motor 04:00" tomando el primer disparador, y con tres
            # ventanas eso se lee como "corre una vez al dia a las 04:00", que es
            # falso en la unica pantalla que no puede mentir. Ademas el numero de
            # disparadores es justo lo que hay que vigilar: uno que desaparece es
            # un tercio del respaldo que deja de correr.
            #
            # Indexado y no `Select-Object -First 1`: -First corta la tuberia
            # lanzando StopUpstreamCommandsException, y esa excepcion de control
            # queda suelta en el flujo de errores.
            $cuantos = @($t.Triggers).Count
            $hora = ' {0} ventanas' -f $cuantos
            if ($cuantos -eq 1) {
                $d = @($t.Triggers)[0]
                [datetime] $h = [datetime]::MinValue
                if ($d -and $d.StartBoundary -and [datetime]::TryParse($d.StartBoundary, [ref] $h)) {
                    $hora = ' {0:HH:mm}' -f $h
                }
            }
        }
        $partes.Add(('{0}{1} {2}' -f $par.Eti, $hora, $t.State))
    }
    return $partes.ToArray()
}

function Get-ResumenPendiente {
    <#
        .SYNOPSIS
            Lo que le falta al sistema, en frases accionables. Vacio si no falta.
        .DESCRIPTION
            LA LINEA SOLO APARECE SI HAY ALGO. Una linea "PENDIENTE: nada" que
            esta siempre puesta se vuelve invisible en una semana, y entonces
            tampoco se ve el dia que dice algo.
        .PARAMETER Estado
            Lo que devolvio Read-EstadoRespaldo.
        .PARAMETER Disco
            La ficha del disco frio, o $null si no esta conectado.
        .PARAMETER Filas
            Lo que devolvio Read-EstadoPorRaiz.
    #>
    [CmdletBinding()]
    [OutputType([string[]])]
    param(
        [Parameter(Mandatory)][hashtable] $Estado,
        [AllowNull()][psobject] $Disco,
        [Parameter(Mandatory)][AllowEmptyCollection()][psobject[]] $Filas
    )

    $partes = New-Object System.Collections.Generic.List[string]

    $huerfanos = 0
    if ($Estado.ContainsKey('huerfanos')) { [void][int]::TryParse(('' + $Estado['huerfanos']), [ref] $huerfanos) }
    if ($huerfanos -gt 0) {
        $partes.Add(('{0} carpetas sin clasificar' -f $huerfanos))
    }

    if (-not $Disco) { $partes.Add('conecta el disco frio') }

    $conFallo = @($Filas | Where-Object { $_.Estado -ne 'AlDia' })
    if ($conFallo.Count -gt 0) {
        $partes.Add(('{0} raices con fallo' -f $conFallo.Count))
    }

    # EL TESTIGO ES LA UNICA PIEZA QUE AVISA CUANDO NADIE MIRA LA PANTALLA.
    # Sin el, todo este tablero solo sirve si alguien se acuerda de abrirlo, y
    # un respaldo que lleva tres semanas parado se ve igual que uno sano hasta
    # que alguien lo abre. Por eso sale como pendiente y no como nota al pie.
    if (-not (Get-UrlDelTestigo)) { $partes.Add('el testigo externo no esta configurado') }

    return $partes.ToArray()
}

function Show-Ventana {
    <#
        .SYNOPSIS
            Pinta la ventana de estado y el menu.
        .DESCRIPTION
            ES LA MAQUETA APROBADA DEL PLAN, no una lista de campos. El plan
            -00_PLAN_RESPALDO_EQUIPO-01_v5_FINAL_FINAL.html, seccion "Tablero
            interactivo"- dibuja una pantalla concreta, y su pieza central es
            UNA TABLA POR RAIZ con una columna por destino. Sin esa tabla el
            tablero contesta "estas protegido" pero no contesta "que
            exactamente", que es la pregunta que se hace de verdad delante de
            un respaldo.

            Se separa con REGLAS a todo lo ancho y no con un marco cerrado: es
            lo que dibuja la maqueta y lo que hace el panel del nodo.

            NO ESCANEA NADA PARA PINTARSE. La tabla sale de RAICES.tsv, que
            escribio el motor cuando de verdad midio. Un tablero que tarda
            minutos en abrirse no se abre nunca.
        .PARAMETER Configuracion
            El objeto de configuracion completo.
    #>
    [CmdletBinding()]
    [OutputType([void])]
    param(
        [Parameter(Mandatory)][psobject] $Configuracion
    )

    $v       = Get-EstadoDelIndicador
    $estado  = Read-EstadoRespaldo
    $filas   = @(Read-EstadoPorRaiz)
    $p       = $script:Paleta
    $u       = $script:Capacidades.Unicode
    $colorEs = Get-ColorDeEstado -Estado $v.Estado -Paleta $p
    $simbolo = Get-Simbolo -Estado $v.Estado -Unicode $u
    $ancho   = $script:AnchoTablero

    # --- Cabecera: quien soy, y el veredicto pegado a la derecha ------------
    $izq = 'RESPALDO {0}' -f $Configuracion.destinos.nodo.prefijoEquipo
    $der = 'VEREDICTO:{0}{1}' -f $simbolo, $v.Estado.ToUpperInvariant()
    $hueco = $ancho - $izq.Length - $der.Length
    if ($hueco -lt 1) { $hueco = 1 }
    Write-Linea ('  {0}{1}{2}{3}{4}{5}{6}' -f `
            ($p.Fuerte + $p.Titulo), $izq, $p.Fin, (' ' * $hueco), $colorEs, $der, $p.Fin)
    Write-Linea (Get-Regla -Paleta $p -Unicode $u)
    Write-Linea ('  {0}{1}{2}' -f $p.Tenue, (Limit-Texto -Texto $v.Detalle -Maximo $ancho), $p.Fin)
    Write-Linea ' '

    # --- Los dos destinos ---------------------------------------------------
    $unc = '' + $Configuracion.destinos.nodo.unc
    # Test-Path directo y no Test-DestinoNodo: pintar la ventana no debe escribir
    # lineas de ERROR en el registro ni esperar reintentos de medio minuto.
    $nodoVivo = Test-Path -LiteralPath $unc -ErrorAction SilentlyContinue
    $libresNodo = $null
    if ($nodoVivo) { $libresNodo = Get-EspacioLibre -Ruta $unc }
    Write-Linea ('  {0}{1}{2}{3}' -f `
        (Format-Celda -Texto 'NODO' -Ancho 11 -Paleta $p -Color $p.Etiqueta), `
        (Format-Celda -Texto $(if ($nodoVivo) { 'responde' } else { 'NO RESPONDE' }) -Ancho 14 -Paleta $p `
                -Color $(if ($nodoVivo) { $p.Verde } else { $p.Rojo })), `
        (Format-Celda -Texto (Format-Espacio -Bytes $libresNodo) -Ancho 16 -Paleta $p -Color $p.Tenue), `
        (Format-Celda -Texto ('ult. copia {0}' -f (Format-Antiguedad -Momento ('' + $estado['momento']))) `
                -Ancho 26 -Paleta $p -Color $p.Valor))

    # El disco se conecta A PETICION (seccion 6.3): no hay calendario y es
    # deliberado. Que no este no es una falla, asi que no se pinta en rojo.
    $disco = Get-DiscoFrio -Configuracion $Configuracion 2>$null
    $libresDisco = $null
    if ($disco) { $libresDisco = Get-EspacioLibre -Ruta $disco.Raiz }
    $colorDisco = $p.Valor
    if (('' + $estado['disco_estado']) -eq 'Atencion') { $colorDisco = $p.Ambar }
    Write-Linea ('  {0}{1}{2}{3}' -f `
        (Format-Celda -Texto 'DISCO FRIO' -Ancho 11 -Paleta $p -Color $p.Etiqueta), `
        (Format-Celda -Texto $(if ($disco) { 'conectado' } else { 'no conectado' }) -Ancho 14 -Paleta $p `
                -Color $(if ($disco) { $p.Verde } else { $p.Gris })), `
        (Format-Celda -Texto (Format-Espacio -Bytes $libresDisco) -Ancho 16 -Paleta $p -Color $p.Tenue), `
        (Format-Celda -Texto ('ult. copia {0}' -f (Format-Antiguedad -Momento ('' + $estado['disco_momento']))) `
                -Ancho 26 -Paleta $p -Color $colorDisco))
    Write-Linea ' '

    # --- La tabla por raiz: la pieza central de la maqueta -------------------
    Write-Linea ('  {0}{1}{2}{3}{4}' -f `
        (Format-Celda -Texto 'RAIZ' -Ancho 30 -Paleta $p -Color $p.Etiqueta), `
        (Format-Celda -Texto 'CL' -Ancho 3 -Paleta $p -Color $p.Etiqueta), `
        (Format-Celda -Texto 'COPIADO ' -Ancho 11 -Paleta $p -Color $p.Etiqueta -Derecha), `
        (Format-Celda -Texto 'NODO' -Ancho 13 -Paleta $p -Color $p.Etiqueta), `
        (Format-Celda -Texto 'DISCO FRIO' -Ancho 13 -Paleta $p -Color $p.Etiqueta))
    Write-Linea (Get-Regla -Paleta $p -Unicode $u)
    # EL CORTE DEL VERDE SALE DE LA CADENCIA, no de un numero escrito aqui: es el
    # hueco que el propio reparto de ventanas promete. Si no se puede leer, 12 h,
    # que es el hueco de la cadencia de hoy -pintar de mas no es la falla grave;
    # pintar de verde algo viejo si-.
    $horasVerde = 12
    try { $horasVerde = (Get-CadenciaDeCorrida -Configuracion $Configuracion).HuecoNominalMaximoHoras }
    catch { Write-Verbose "Cadencia ilegible; el corte del verde se queda en $horasVerde h: $($_.Exception.Message)" }
    Write-CuerpoDeTabla -Filas $filas -Paleta $p -HorasParaAmbar $horasVerde
    Write-Linea ' '

    # --- Las lineas de resumen que pide la maqueta ---------------------------
    $centinelas = '{0} declarados' -f @($Configuracion.centinelas).Count
    if ($estado.ContainsKey('centinelas')) { $centinelas = '' + $estado['centinelas'] }
    $cambio = '?'
    if ($estado.ContainsKey('cambio')) { $cambio = '' + $estado['cambio'] }
    Write-LineaDeSumario -Etiqueta 'SEGURIDAD' -Paleta $p -Partes @(
        ('centinelas {0}' -f $centinelas),
        ('cambio {0} % en la ultima corrida' -f $cambio))

    $huellas = 'huellas {0}' -f (Format-Antiguedad -Momento ('' + $estado['huellas_momento']))
    if ($estado.ContainsKey('huellas_detalle')) { $huellas += ' ' + $estado['huellas_detalle'] }
    # TRES COMPROBACIONES DISTINTAS Y NINGUNA VALE POR OTRA: las huellas dicen
    # que lo copiado coincide, la semilla dice que el kit de arranque se lee, y
    # la restauracion real -criterio 11- dice que de verdad se puede volver.
    Write-LineaDeSumario -Etiqueta 'COMPROBADO' -Paleta $p -Partes @(
        $huellas,
        ('semilla {0}' -f (Format-Antiguedad -Momento ('' + $estado['semilla_momento']))),
        ('restauracion real {0}' -f (Format-Antiguedad -Momento ('' + $estado['restauracion_probada']))))

    Write-LineaDeSumario -Etiqueta 'AUTOMATISMO' -Paleta $p -Partes (Get-EstadoDeTarea)

    $pendiente = @(Get-ResumenPendiente -Estado $estado -Disco $disco -Filas $filas)
    if ($pendiente.Count -gt 0) {
        Write-LineaDeSumario -Etiqueta 'PENDIENTE' -Paleta $p -Partes $pendiente -Color $p.Ambar -Avisar
    }

    # --- Menu ----------------------------------------------------------------
    # Los parametros de proteccion NO estan aqui, y no es un olvido (seccion
    # 10.1): los centinelas y las clases se cambian
    # editando el archivo, porque subir un umbral desde una pantalla bonita
    # desarma la defensa con dos pulsaciones y sin dejar rastro.
    #
    # EL NOMBRE DE CADA OPCION ES UNA PALABRA QUE SE ENTIENDE SOLA, y el "contra
    # que" lo dice el ANUNCIO al abrirla, no el menu. Se pidio asi mirando la
    # pantalla el 2026-09-03: "no quiero ahi poner todo, sino una palabra que
    # pueda ser entendible a la primera".
    #
    # "vista previa" VA SANGRADA bajo la copia que previsualiza, y esa sangria
    # responde "simular QUE" sin gastar una palabra: [2] cuelga de [1] y [8] de
    # [7]. Antes las dos decian "simular" a secas, cada una en una fila suelta,
    # y no se sabia de que copia hablaban.
    #
    # "cotejar" y no "verificar": en Mexico una copia cotejada es la que se
    # comparo contra su original, asi que la palabra ya lleva dentro el "contra
    # que" que faltaba. "Verificar el nodo" no decia contra que se verificaba.
    Write-Linea ' '
    Write-Linea (Get-Regla -Paleta $p -Unicode $u)
    Write-Linea ('  {0}{1}{2}{3}{4}' -f ($p.Fuerte + $p.Titulo), `
            'COPIAR                    ', 'COMPROBAR                   ', 'SISTEMA', $p.Fin)
    foreach ($fila in @(
            , @('[1] al nodo', '[3] cotejar el nodo', '[5] semilla de arranque')
            , @('[2]    vista previa', '[4] estado completo', '[6] automatismo')
            , @('[7] al disco frio', '[9] cotejar el disco frio', '[A] ajustes')
            , @('[8]    vista previa', '[R] ensayo de restauracion', '[S] salir'))) {
        Write-Linea ('  {0}{1}{2}' -f `
            (Format-Celda -Texto $fila[0] -Ancho 26 -Paleta $p -Color $p.Valor), `
            (Format-Celda -Texto $fila[1] -Ancho 28 -Paleta $p -Color $p.Valor), `
            (Format-Celda -Texto $fila[2] -Ancho 24 -Paleta $p -Color $p.Valor))
    }
    Write-Linea ('  {0}centinelas y clases: se cambian en 3-Config/respaldo.jsonc{1}' -f $p.Tenue, $p.Fin)
    Write-Linea ' '
}

# ---------------------------------------------------------------------------
#  LA FORMA COMUN DE LAS OPCIONES  -  pendiente 27
#
#  El responsable lo abrio mirando la pantalla el 2026-09-02: "las opciones
#  deben de hacer algo concreto con inicio y fin y no existir de adorno, eso se
#  ve vibecodeado". Tenia razon y habia evidencia: la opcion [2] hacia
#  Select-Object sobre una propiedad llamada Freno cuando se llama Frenos, asi
#  que imprimia "Freno :" con nada detras. Eso NO sale vacio: SALE PEOR -- se
#  lee como "no hubo freno", una afirmacion tranquilizadora que nadie habia
#  comprobado, en la unica pantalla que no puede mentir.
#
#  LA FORMA, y no se estrena vocabulario. Cada opcion se comporta como un
#  mini-aviso del nodo, con las tres piezas que internal/aviso ya declara
#  obligatorias -- titulo, hechos, recomendacion:
#
#    ANUNCIO    una linea al empezar. Dice SI ESCRIBE O NO, DONDE y cuanto tarda.
#    HECHOS     lo del medio. Nunca un objeto crudo; como mucho una tabla.
#    VEREDICTO  una linea al terminar, que empieza por una de cuatro palabras
#               que YA existen en este proyecto -- OK, ATENCION, FALLO,
#               SIN DATOS -- y LLEVA SIEMPRE LA CIFRA QUE LA SOSTIENE.
#    ACCION     solo si no es OK. Que hacer ahora.
#
#  SIN CIFRA NO HAY VEREDICTO. Y SIN DATOS es la pieza que faltaba: "no pude
#  mirar" tiene que ser distinguible de "mire y esta bien", o una comprobacion
#  que no pudo hacerse acaba pareciendose a un verde.
# ---------------------------------------------------------------------------

function Write-Anuncio {
    <#
        .SYNOPSIS
            La linea de apertura de una opcion: que va a hacer y con que riesgo.
        .PARAMETER Que
            La accion, en tres o cuatro palabras.
        .PARAMETER Detalle
            Si escribe o no, donde, y cuanto puede tardar.
    #>
    [CmdletBinding()]
    [OutputType([void])]
    param(
        [Parameter(Mandatory)][ValidateNotNullOrEmpty()][string] $Que,
        [ValidateNotNull()][string] $Detalle = ''
    )
    $p = $script:Paleta
    Write-Linea ('   {0}> {1}{2}' -f ($p.Fuerte + $p.Titulo), $Que, $p.Fin)
    if ($Detalle) { Write-Linea ('     {0}{1}{2}' -f $p.Tenue, $Detalle, $p.Fin) }
}

function Write-Veredicto {
    <#
        .SYNOPSIS
            La linea de cierre de una opcion. Afirma algo, con su cifra.
        .DESCRIPTION
            CUATRO PALABRAS Y NO MAS, y son las que este proyecto ya usa: OK,
            ATENCION y FALLO son el vocabulario de severidad de la seccion 11, y
            SIN DATOS es el quinto estado del indicador de la seccion 10.2.
            Estrenar una quinta seria tener dos escalas para lo mismo.
        .PARAMETER Nivel
            OK, ATENCION, FALLO o SIN DATOS.
        .PARAMETER Frase
            Que se afirma. Con su numero.
        .PARAMETER Accion
            Que hacer ahora. De hecho obligatorio en todo lo que no sea OK: el
            charter dice que toda alerta tiene que ser accionable.
    #>
    [CmdletBinding()]
    [OutputType([void])]
    param(
        [Parameter(Mandatory)]
        [ValidateSet('OK', 'ATENCION', 'FALLO', 'SIN DATOS')]
        [string] $Nivel,

        [Parameter(Mandatory)][ValidateNotNullOrEmpty()][string] $Frase,
        [ValidateNotNull()][string] $Accion = ''
    )
    $p = $script:Paleta
    $color = switch ($Nivel) {
        'OK'       { $p.Verde }
        'ATENCION' { $p.Ambar }
        'FALLO'    { $p.Rojo }
        default    { $p.Gris }
    }

    # SE PARTE POR PALABRAS, CON SANGRIA COLGANTE, Y ESTO NO ES ESTETICA.
    #
    # La primera version se salia del ancho de la consola y la terminal la
    # cortaba a mitad de una cifra: "...Copiaria" / " 67 y borraria 3.". Un
    # veredicto partido en el peor sitio se lee mal justo el dia malo, y ademas
    # el corte lo hace la ventana, asi que cambia de tamano con ella.
    #
    # Se descubrio MIRANDO EL BUFFER de una consola real -- no leyendo el
    # codigo, donde la frase parece caber-. Es el mismo metodo con el que la
    # ventana destapo dos defectos el 2026-09-02.
    $ancho = 96
    try {
        $w = $Host.UI.RawUI.BufferSize.Width
        # Menos el margen y la columna del nivel; y con suelo, porque una
        # consola estrecha no puede dejar el texto en dos caracteres.
        if ($w -gt 40) { $ancho = [Math]::Max(40, $w - 16) }
    }
    catch { Write-Verbose 'Sin consola medible: se usa el ancho por omision.' }

    $lineas = Format-TextoAjustado -Texto $Frase -Ancho $ancho
    Write-Linea ('   {0}{1,-9}{2} {3}' -f $color, $Nivel, $p.Fin, $lineas[0])
    foreach ($resto in $lineas | Select-Object -Skip 1) {
        Write-Linea ('                {0}' -f $resto)
    }

    if ($Accion) {
        foreach ($l in (Format-TextoAjustado -Texto $Accion -Ancho $ancho)) {
            Write-Linea ('             {0}{1}{2}' -f $p.Tenue, $l, $p.Fin)
        }
    }
}

function Format-TextoAjustado {
    <#
        .SYNOPSIS
            Parte un texto por palabras a un ancho dado. Nunca corta una palabra.
        .DESCRIPTION
            Existe porque las cifras son lo que sostiene un veredicto, y una
            cifra partida por la mitad deja de ser una cifra. Cortar por palabras
            garantiza que "0.26 %" o "26927" lleguen enteros a la misma linea.
        .PARAMETER Texto
            Lo que hay que partir.
        .PARAMETER Ancho
            Caracteres por linea.
    #>
    [CmdletBinding()]
    [OutputType([string[]])]
    param(
        [Parameter(Mandatory)][AllowEmptyString()][string] $Texto,
        [ValidateRange(20, 500)][int] $Ancho = 96
    )

    # LA COMA NO ES UN ADORNO: SIN ELLA ESTA FUNCION MIENTE SOBRE SU TIPO.
    # PowerShell DESENVUELVE un arreglo de un solo elemento al salir de una
    # funcion, asi que un texto que cabia en una linea volvia como cadena suelta
    # -- y el llamador, que hace $lineas[0] esperando la primera LINEA, se
    # quedaba con la primera LETRA. El veredicto salia literalmente "ATENCION E".
    #
    # Se vio pulsando la opcion [3] en una consola de verdad el 2026-09-03. Las
    # pruebas no lo cazaban porque todas sus frases eran largas y se partian en
    # dos o mas lineas: el arreglo sobrevivia por accidente. Afectaba a las once
    # opciones, no solo a esa.
    if ([string]::IsNullOrWhiteSpace($Texto)) { return , @('') }

    $salida = New-Object System.Collections.Generic.List[string]
    $actual = ''
    foreach ($palabra in ($Texto -split '\s+')) {
        if ($actual -eq '') { $actual = $palabra; continue }
        if (($actual.Length + 1 + $palabra.Length) -le $Ancho) {
            $actual = '{0} {1}' -f $actual, $palabra
        }
        else {
            $salida.Add($actual)
            $actual = $palabra
        }
    }
    if ($actual -ne '') { $salida.Add($actual) }
    return , $salida.ToArray()
}

function Show-VeredictoDeSimulacion {
    <#
        .SYNOPSIS
            Lo que la opcion [2] tiene que contestar: si algo frenaria, y con que
            numero.
        .DESCRIPTION
            ESTA ES LA OPCION MAS IMPORTANTE DEL MENU -- mirar antes de saltar --
            y era la unica que no reportaba ningun veredicto.

            LO QUE SE ENSENA ES LO QUE EL FRENO MIDIO, no lo que dice la
            configuracion. Un "umbral 5 %" solo repite lo que ya esta escrito en
            el archivo; lo que dice algo es el porcentaje que DE VERDAD
            cambiaria: un 0.3 % frente a un umbral del 5 % es tranquilidad
            medida, y un 4.8 % es un aviso que ningun umbral da por si solo.
        .PARAMETER Resultado
            Lo que devuelve respaldo.ps1 en simulacion.
        .PARAMETER Configuracion
            Para poder decir contra que umbral se comparo.
    #>
    [CmdletBinding()]
    [OutputType([void])]
    param(
        [Parameter(Mandatory)][AllowNull()] $Resultado,
        [Parameter(Mandatory)][psobject] $Configuracion
    )

    if ($null -eq $Resultado) {
        Write-Veredicto -Nivel 'SIN DATOS' -Frase 'La simulacion no devolvio nada.' `
            -Accion 'Revisa el registro del dia en LOCALAPPDATA\NasRespaldo\registro.'
        return
    }

    if ($Resultado.Abortada) {
        # RETIRADA LA CAPA 2 (ADR-0085), TODO ABORTO ES UN FALLO. Los que
        # quedan -centinela, origen inutilizable, deuda- no admiten
        # autorizacion: no hay nada que ofrecer al que mira salvo el registro.
        Write-Veredicto -Nivel 'FALLO' -Frase ('' + $Resultado.Motivo) `
            -Accion 'No se copiaria nada. Mira el registro del dia para saber que capa lo paro.'
        return
    }

    # LA PROPIEDAD SE LLAMA Cambios, EN PLURAL: es una por raiz. Se llamaba
    # Frenos hasta ADR-0085; el defecto que abrio el pendiente 27 fue pedirla
    # en singular, que no existe, y la prueba que lo vigila sigue en pie.
    $cambios = @($Resultado.Cambios | Where-Object { $null -ne $_ })

    if ($cambios.Count -eq 0) {
        Write-Veredicto -Nivel 'SIN DATOS' -Frase 'La simulacion no llego a medir ninguna raiz.' `
            -Accion 'Comprueba que el nodo responde y vuelve a intentarlo.'
        return
    }

    $ordenados = $cambios | Sort-Object { [double]$_.Porcentaje } -Descending
    $peor      = @($ordenados)[0]
    $aCopiar   = ($cambios | Measure-Object -Property ACopiar -Sum).Sum
    # SE SUMA Borraria, NO ABorrar. ABorrar es lo que sobra en el destino;
    # Borraria es lo que esta clase haria con ello, y con clase A es 0 siempre.
    # Decir "borraria 6" en un cliente cuyas ocho raices son clase A -que por
    # definicion no borra- era una alarma inventada en la unica pantalla que
    # existe para mirar antes de saltar.
    $aBorrar   = ($cambios | Measure-Object -Property Borraria -Sum).Sum
    $sobran    = ($cambios | Measure-Object -Property ABorrar  -Sum).Sum

    Write-Linea ''
    Show-Texto -Objeto ($ordenados | Select-Object Raiz, Clase, Porcentaje, ACopiar, ABorrar, Borraria, TotalOrigen)
    Write-Linea ''
    Write-Veredicto -Nivel 'OK' -Frase (
        '{0} raices miradas. La mas movida es {1} con {2} % de su contenido. Copiaria {3} archivos y borraria {4}.' -f
            $cambios.Count, (Split-Path $peor.Raiz -Leaf), $peor.Porcentaje, $aCopiar, $aBorrar)
    if ($sobran -gt $aBorrar) {
        $p = $script:Paleta
        Write-Linea ('  {0}{1} sobran en el destino y ahi se quedan: son copias que el origen ya no tiene y que la clase A conserva a proposito. No son una perdida.{2}' -f
            $p.Tenue, $sobran, $p.Fin)
    }
}

function Show-VeredictoDelCotejo {
    <#
        .SYNOPSIS
            Lo que la opcion [3] tiene que contestar: si lo guardado en el nodo
            dice lo mismo que el original, y con cuantos archivos leidos.
        .DESCRIPTION
            SE COTEJA CONTRA EL ORIGINAL DE ESTE EQUIPO. Eso era exactamente lo
            que la pantalla no decia: la opcion se llamaba "verificar el nodo",
            volcaba una lista cruda, y quien la miraba no sabia contra que se
            verificaba ni si el resultado era bueno.

            DOS PREGUNTAS DISTINTAS, Y NO SE MEZCLAN:

              coincidencia   la ESTRUCTURA -- que archivos faltan alli o sobran
              huella         el CONTENIDO  -- unos cuantos se leen enteros y se
                             comparan byte a byte

            UNA HUELLA DISTINTA ES MAS GRAVE QUE UN ARCHIVO QUE FALTA, y por eso
            una manda FALLO y la otra ATENCION. Un archivo que falta se copia en
            la siguiente corrida y el sistema se cura solo. Un contenido que no
            coincide significa que lo guardado NO ES lo que se creia tener, y
            ninguna corrida futura lo va a notar: robocopy compara fecha y
            tamano, no contenido. Es el mismo agujero que abrio la opcion [9]
            tras el apagon del 2026-09-02.
        .PARAMETER Resultado
            Lo que devuelve verificar.ps1.
    #>
    [CmdletBinding()]
    [OutputType([void])]
    param(
        [Parameter(Mandatory)][AllowNull()] $Resultado
    )

    if ($null -eq $Resultado) {
        Write-Veredicto -Nivel 'SIN DATOS' -Frase 'El cotejo no devolvio nada.' `
            -Accion 'Revisa el registro del dia en LOCALAPPDATA\NasRespaldo\registro.'
        return
    }

    # "No pude mirar" NO es "mire y esta bien". Es la distincion que sostiene
    # todo el vocabulario de la seccion 11, y con el nodo apagado es la unica
    # respuesta honesta.
    if (-not $Resultado.NodoVivo) {
        Write-Veredicto -Nivel 'SIN DATOS' -Frase 'El nodo no respondio: no se cotejo nada.' `
            -Accion 'Comprueba que el NAS esta encendido y en la red, y vuelve a intentarlo.'
        return
    }

    $coinc = @($Resultado.Coincidencias | Where-Object { $null -ne $_ })
    $huel = @($Resultado.Huellas | Where-Object { $null -ne $_ })

    if ($coinc.Count -eq 0) {
        Write-Veredicto -Nivel 'SIN DATOS' -Frase 'No se llego a cotejar ninguna raiz.' `
            -Accion 'Comprueba que las raices declaradas existen y vuelve a intentarlo.'
        return
    }

    # UNA FILA POR RAIZ con las dos preguntas al lado. Select-Object -First 1 y
    # no [0]: bajo StrictMode indexar un arreglo vacio lanza, y una raiz sin
    # huellas es un caso normal cuando la muestra es cero.
    $filas = @(foreach ($c in $coinc) {
            $h = $huel | Where-Object { $_.Raiz -eq $c.Raiz } | Select-Object -First 1
            [pscustomobject]@{
                Raiz      = (Split-Path $c.Raiz -Leaf)
                Clase     = $c.Clase
                Faltan    = $c.Faltan
                Sobran    = $c.Sobran
                Leidos    = $(if ($h) { $h.Comprobados } else { 0 })
                Distintos = $(if ($h) { @($h.Diferencias).Count } else { 0 })
            }
        })

    Write-Linea ''
    Show-Texto -Objeto $filas
    Write-Linea ''

    $leidos = ($filas | Measure-Object -Property Leidos -Sum).Sum
    $distintos = ($filas | Measure-Object -Property Distintos -Sum).Sum
    $faltan = ($filas | Measure-Object -Property Faltan -Sum).Sum
    $sobran = ($filas | Measure-Object -Property Sobran -Sum).Sum

    if ($distintos -gt 0) {
        Write-Veredicto -Nivel 'FALLO' -Frase (
            '{0} de los {1} archivos leidos NO coinciden con su original. Lo guardado en el nodo no es lo que se creia tener.' -f $distintos, $leidos) `
            -Accion 'Corre la opcion [1] y vuelve a cotejar. Si repite, el problema no es la copia sino el origen o el disco del nodo.'
        return
    }

    if ($faltan -gt 0 -or $sobran -gt 0) {
        Write-Veredicto -Nivel 'ATENCION' -Frase (
            'El contenido coincide en los {0} archivos leidos, pero la estructura no: faltan {1} en el nodo y sobran {2}.' -f $leidos, $faltan, $sobran) `
            -Accion 'Es lo esperable si hubo cambios desde la ultima corrida. La opcion [1] los pone al dia.'
        return
    }

    # LA CIFRA QUE SOSTIENE EL VERDE ES "leidos enteros", no "raices miradas":
    # comparar fechas no demuestra nada sobre el contenido, y es justo lo que
    # esta opcion existe para comprobar.
    Write-Veredicto -Nivel 'OK' -Frase (
        'Lo guardado en el nodo coincide con el original. {0} raices cotejadas y {1} archivos leidos enteros y comparados, 0 diferencias.' -f
        $coinc.Count, $leidos)
}

function Show-VeredictoDelAutomatismo {
    <#
        .SYNOPSIS
            Lo que la opcion [6] tiene que contestar: si el automatismo esta vivo
            y si algo le ha metido mano al motor.
        .DESCRIPTION
            DOS DEFECTOS QUE ESTA FUNCION CORRIGE, y los dos se vieron pulsando
            la opcion:

            (1) Se volcaba NextRunTime en crudo. ESE NUMERO NO ES UNA PROMESA:
                el Programador re-sortea el retraso en cada consulta, asi que
                leerlo diez veces da diez horas distintas. Ensenarlo es publicar
                como compromiso un numero que cambia solo -- el mismo error que
                ya se corrigio en la linea de resumen del tablero.

            (2) Se volcaba LastTaskResult sin traducir. "267009" no le dice nada
                a nadie; significa "esta corriendo ahora".

            Y ANADE LA COMPROBACION QUE PIDIO EL RESPONSABLE el 2026-09-02, por
            el antivirus: que las piezas del motor sigan estando. Ver
            Test-MotorIntacto.
    #>
    [CmdletBinding()]
    [OutputType([void])]
    param()

    $filas = New-Object System.Collections.Generic.List[psobject]
    foreach ($par in @(
            @{ Eti = 'motor'; Tarea = $script:NombreTarea },
            @{ Eti = 'icono'; Tarea = $script:NombreIndicador })) {
        $t = Get-ScheduledTask -TaskName $par.Tarea -ErrorAction SilentlyContinue
        if (-not $t) {
            $filas.Add([pscustomobject]@{ Pieza = $par.Eti; Estado = 'NO EXISTE'; Ventanas = ''; Ultima = ''; Resultado = '' })
            continue
        }
        $info = $t | Get-ScheduledTaskInfo -ErrorAction SilentlyContinue
        $ultima = ''
        $codigo = ''
        if ($info) {
            # 11/30/1999 es el "nunca" del Programador de tareas.
            if ($info.LastRunTime -and $info.LastRunTime.Year -gt 2000) {
                $ultima = '{0:dd/MM HH:mm}' -f $info.LastRunTime
            }
            else { $ultima = 'nunca' }
            $codigo = switch ($info.LastTaskResult) {
                0       { 'correcta' }
                267009  { 'corriendo ahora' }
                267011  { 'nunca ha corrido' }
                default { ('codigo {0}' -f $info.LastTaskResult) }
            }
        }
        $ventanas = if ($par.Eti -eq 'motor') { '' + @($t.Triggers).Count } else { '-' }
        $filas.Add([pscustomobject]@{
                Pieza     = $par.Eti
                Estado    = '' + $t.State
                Ventanas  = $ventanas
                Ultima    = $ultima
                Resultado = $codigo
            })
    }

    $p = $script:Paleta
    Write-Linea ''
    Show-Texto -Objeto $filas.ToArray()
    # NO SE ENSENA LA PROXIMA HORA, Y SE DICE POR QUE. Ver la cabecera.
    Write-Linea ('   {0}La hora exacta de la proxima corrida no se puede saber: Windows la sortea{1}' -f $p.Tenue, $p.Fin)
    Write-Linea ('   {0}dentro de la ventana y solo la fija al disparar.{1}' -f $p.Tenue, $p.Fin)
    Write-Linea ''

    $motor = Test-MotorIntacto
    foreach ($f in @($motor.PiezasFallan)) { Write-Warning ('PIEZA DEL MOTOR: {0}' -f $f) }
    foreach ($f in @($motor.TareasFallan)) { Write-Warning ('TAREA: {0}' -f $f) }

    if ($motor.Intacto) {
        Write-Veredicto -Nivel 'OK' -Frase (
            'El automatismo esta vivo: {0} tareas en pie y las {1} piezas del motor intactas.' -f
                $motor.TareasTotal, $motor.PiezasTotal)
        return
    }

    $rotas = @($motor.PiezasFallan).Count + @($motor.TareasFallan).Count
    Write-Veredicto -Nivel 'FALLO' -Frase (
        '{0} de {1} comprobaciones fallan: el respaldo automatico NO esta garantizado.' -f
            $rotas, ($motor.PiezasTotal + $motor.TareasTotal)) `
        -Accion 'Si falta un archivo, mira la cuarentena de el antivirus: un antivirus que actua no avisa al programa que se lleva. Si falta una tarea, re-registrala con [A].'
}

function Show-RevisionDelDisco {
    <#
        .SYNOPSIS
            Ensena lo que la verificacion encontro en el disco frio.
        .DESCRIPTION
            NO REPARA. Ensena y dice como se repara, porque reparar pisa
            archivos y ese gesto es de una persona (seccion 10.1: el menu lanza
            acciones, no relaja protecciones).
        .PARAMETER Revision
            Lo que devuelve Test-DiscoFrioEnVuelo.
    #>
    [CmdletBinding()]
    [OutputType([void])]
    param(
        [Parameter(Mandatory)][AllowNull()][psobject] $Revision
    )

    $p = $script:Paleta
    if (-not $Revision) {
        Write-Warning 'El disco frio no esta conectado: no se reviso nada.'
        return
    }

    Write-Linea ('   Raices revisadas: {0}' -f @($Revision.Revisadas).Count)
    foreach ($s in @($Revision.Saltadas)) {
        Write-Warning ('NO REVISADO ({0}): {1}' -f $s.Lado, $s.Motivo)
    }

    $rotos = @($Revision.EnVuelo)
    if ($rotos.Count -eq 0 -and @($Revision.Saltadas).Count -eq 0) {
        Write-Linea ('   {0}Sin archivos a medias. El disco esta cuadrado.{1}' -f $p.Verde, $p.Fin)
        return
    }
    if ($rotos.Count -eq 0) {
        Write-Warning 'Sin hallazgos EN LO QUE SE PUDO MIRAR. No es lo mismo que "limpio".'
        return
    }

    Write-Linea ('   {0}{1} archivo(s) a medias de una copia cortada.{2}' -f $p.Rojo, $rotos.Count, $p.Fin)
    Show-Texto -Objeto ($rotos | Select-Object Motivo, TamanoOrigen, TamanoDestino, Relativa)
    Write-Linea '   Se reparan con Repair-ArchivosEnVuelo (2-Nucleo/comun.ps1): sobrescribe, nunca borra.'
}

function Show-PruebaDeRestauracion {
    <#
        .SYNOPSIS
            Comprueba que la semilla existe, se lee y sabe cuando se probo.

        .DESCRIPTION
            LO QUE ESTO ES Y LO QUE NO ES, dicho aqui para que nadie confunda
            una cosa con la otra. Esto comprueba que la semilla ESTA y SE LEE.
            El criterio 11 de la seccion 15 pide otra cosa -restaurar en una
            maquina virtual limpia y CRONOMETRARLO- y aprovisionar maquinas esta
            fuera de alcance (seccion 16), asi que esa prueba la lanza una
            persona y esta pantalla solo dice cuanto hace que no se hace.

            La linea de "ultima prueba real" esta para incomodar cuando envejezca
            (seccion 9). Si no hay ninguna, lo dice a gritos: una semilla que
            nadie ha restaurado nunca es una promesa, no un respaldo.
        .PARAMETER Configuracion
            El objeto de configuracion completo.
    #>
    [CmdletBinding()]
    [OutputType([void])]
    param(
        [Parameter(Mandatory)][psobject] $Configuracion
    )

    $p = $script:Paleta
    $unc = $Configuracion.destinos.nodo.unc.TrimEnd('\')
    $raizSemilla = '{0}\{1}\{2}\_SEMILLA' -f $unc, $Configuracion.destinos.nodo.raiz, $Configuracion.destinos.nodo.prefijoEquipo

    Write-Linea ('   Semilla en: {0}' -f $raizSemilla)
    if (-not (Test-Path -LiteralPath $raizSemilla -PathType Container)) {
        Write-Warning 'LA SEMILLA NO ESTA EN EL NODO. Regenerala con la opcion [5].'
        Write-EstadoDeComprobacion -Tipo 'semilla' -Correcto $false `
            -Detalle 'la semilla no esta en el nodo' -Confirm:$false
        return
    }

    $semillaOk = $false
    $archivos = @(Get-ChildItem -LiteralPath $raizSemilla -File -Force -ErrorAction SilentlyContinue)
    Show-Texto -Objeto ($archivos | Select-Object Name, Length, LastWriteTime) -Vacio 'La carpeta de la semilla esta VACIA.'

    $guion = Join-Path $raizSemilla 'RESTAURAR.ps1'
    if (Test-Path -LiteralPath $guion -PathType Leaf) {
        # SE LEE DE VERDAD, no se comprueba solo que exista: un archivo de cero
        # bytes existe igual de bien que uno bueno.
        $texto = Get-Content -LiteralPath $guion -Raw -Encoding UTF8 -ErrorAction SilentlyContinue
        if ([string]::IsNullOrWhiteSpace($texto)) {
            Write-Warning 'RESTAURAR.ps1 existe pero esta VACIO o no se puede leer.'
        }
        else {
            Write-Linea ('   {0}RESTAURAR.ps1 se lee desde el nodo: {1} lineas.{2}' -f $p.Verde, @($texto -split "`n").Count, $p.Fin)
            $semillaOk = $true
        }
    }
    else {
        Write-Warning 'Falta RESTAURAR.ps1 dentro de la semilla.'
    }

    # SE GUARDA, no solo se imprime. Sin esto la comprobacion moria al cerrar la
    # ventana y el tablero no podia decir cuando fue la ultima vez.
    Write-EstadoDeComprobacion -Tipo 'semilla' -Correcto $semillaOk `
        -Detalle $(if ($semillaOk) { '{0} archivos, RESTAURAR.ps1 se lee' -f $archivos.Count }
            else { 'la semilla no esta completa' }) -Confirm:$false

    $estado = Read-EstadoRespaldo
    if ($estado.ContainsKey('restauracion_probada') -and $estado['restauracion_probada']) {
        Write-Linea ('   Ultima restauracion REAL probada: {0}' -f $estado['restauracion_probada'])
    }
    else {
        Write-Linea ('   {0}NUNCA se ha restaurado en una maquina limpia (criterio 11 abierto).{1}' -f $p.Ambar, $p.Fin)
        Write-Linea '   Eso lo hace una persona: aprovisionar maquinas esta fuera de alcance (seccion 16).'
    }
}

function Show-HorarioActual {
    <#
        .SYNOPSIS
            Pinta el horario vigente y las tres cifras que cuelgan de el.
        .DESCRIPTION
            LAS TRES CIFRAS DE ABAJO NO SON ADORNO, Y VAN JUNTAS A PROPOSITO: son
            exactamente lo que se mueve solo cuando se mueve una ventana.

              hueco maximo   se CALCULA de las ventanas; nadie lo elige
              umbral         cuando el icono pasa a ambar; tiene que ser MAYOR
                             que el hueco o avisaria de algo que no ha pasado
              Healthchecks   period y grace del testigo externo

            La tercera es la unica que este programa NO puede cambiar: vive en la
            web de Healthchecks. Por eso se ensena siempre, con los numeros ya
            calculados: mover una ventana y no tocarla alli deja al vigilante
            externo juzgando con un horario que ya no existe, y eso no falla --
            se queda callado, o grita sin motivo.
        .PARAMETER Cadencia
            Lo que devuelve Get-CadenciaDeCorrida.
    #>
    [CmdletBinding()]
    [OutputType([void])]
    param(
        [Parameter(Mandatory)][psobject] $Cadencia
    )

    $p = $script:Paleta
    Write-Linea ''
    Write-Linea ('   {0}HORARIO ACTUAL{1}        {2} corridas al dia, a minuto sorteado' -f `
        ($p.Fuerte + $p.Titulo), $p.Fin, $Cadencia.CorridasPorDia)
    foreach ($v in $Cadencia.Ventanas) {
        Write-Linea ('     {0}   {1} - {2}    {3} h' -f `
                $v.Indice, $v.Inicio, $v.Fin, ([math]::Round($v.LargoMinutos / 60, 2)))
    }
    Write-Linea ''
    Write-Linea ('     Peor hueco entre corridas   {0,5} h   (se calcula, no se elige)' -f $Cadencia.HuecoNominalMaximoHoras)
    Write-Linea ('     Avisa sin corrida buena     {0,5} h   (cuando el icono pasa a ambar)' -f $Cadencia.HorasParaAvisar)
    Write-Linea ('     {0}En Healthchecks: period {1} h, grace {2} h{3}' -f `
            $p.Tenue, $Cadencia.HuecoNominalMaximoHoras, `
        ([math]::Round($Cadencia.HorasParaAvisar - $Cadencia.HuecoNominalMaximoHoras, 2)), $p.Fin)
}

function Set-HorarioConVistaPrevia {
    <#
        .SYNOPSIS
            Ensena que pasaria con el horario nuevo, lo confirma, lo escribe y
            vuelve a registrar la tarea.
        .DESCRIPTION
            SE ENSENA ANTES DE ESCRIBIR, Y LA VISTA PREVIA ES REAL: se obtiene
            llamando a Set-HorarioDeRespaldo con -WhatIf, que valida entero -- si
            las ventanas se solapan o el umbral queda corto, lanza ahi mismo -- y
            no escribe ni un byte. No es una simulacion aparte que pudiera
            separarse del original: es el mismo codigo con la escritura apagada.

            Y DESPUES DE ESCRIBIR SE VUELVE A REGISTRAR LA TAREA, siempre. Sin
            eso, el archivo diria una cosa y el Programador de Windows seguiria
            disparando a las horas viejas: el tablero ensenaria un horario que no
            es el que corre. Es el peor resultado posible de esta pantalla, asi
            que no se ofrece como paso aparte ni se pregunta.
        .PARAMETER Ventanas
            Las ventanas nuevas.
        .PARAMETER HorasParaAvisar
            Umbral nuevo. CERO conserva el actual.
        .PARAMETER RutaConfiguracion
            El .jsonc, si no es el de por omision.
    #>
    # ConfirmImpact Low Y NO High, y la razon no es cosmetica: la confirmacion
    # que ve una persona ya la hace la vista previa de mas abajo, con el horario
    # nuevo pintado delante. Un segundo aviso nativo encima seria ruido, y el
    # ruido es lo que ensena a decir "si" sin leer. Lo que ShouldProcess aporta
    # aqui es que -WhatIf recorra el flujo entero sin escribir.
    [CmdletBinding(SupportsShouldProcess, ConfirmImpact = 'Low')]
    [OutputType([psobject])]
    param(
        [Parameter(Mandatory)][psobject[]] $Ventanas,
        [ValidateRange(0, 720)][int] $HorasParaAvisar = 0,
        [AllowEmptyString()][string] $RutaConfiguracion = ''
    )

    $p = $script:Paleta
    $parametros = @{ Ventanas = $Ventanas }
    if ($HorasParaAvisar -gt 0) { $parametros['HorasParaAvisar'] = $HorasParaAvisar }
    if ($RutaConfiguracion) { $parametros['Ruta'] = $RutaConfiguracion }

    $ensayo = $null
    try {
        $ensayo = Set-HorarioDeRespaldo @parametros -SoloCalcular -Confirm:$false
    }
    catch {
        Write-Veredicto -Nivel 'FALLO' -Frase ('Ese horario no se puede aplicar. {0}' -f $_.Exception.Message) `
            -Accion 'No se escribio nada y el horario sigue como estaba.'
        return $null
    }

    Write-Linea ''
    Write-Linea ('   {0}QUEDARIA ASI{1}' -f ($p.Fuerte + $p.Titulo), $p.Fin)
    Show-HorarioActual -Cadencia $ensayo.Cadencia
    Write-Linea ''

    $respuesta = Read-Host '   Aplicar este horario? (s/N)'
    if (('' + $respuesta) -notmatch '^[sS]') {
        Write-Veredicto -Nivel 'SIN DATOS' -Frase 'Cancelado. El horario sigue exactamente como estaba.'
        return $null
    }

    if (-not $PSCmdlet.ShouldProcess('el horario de respaldo', ('Aplicar: {0}' -f $ensayo.Cadencia.Resumen))) {
        return $null
    }

    $hecho = $null
    try {
        $hecho = Set-HorarioDeRespaldo @parametros -Confirm:$false
    }
    catch {
        Write-Veredicto -Nivel 'FALLO' -Frase ('No se pudo guardar el horario. {0}' -f $_.Exception.Message) `
            -Accion 'El archivo anterior se restauro solo. Comprueba 3-Config/respaldo.jsonc.'
        return $null
    }

    # LA RUTA VIAJA, y no viajaba. Registrar-Tarea lee la cadencia POR SU CUENTA
    # para sacar un disparador por ventana: sin pasarle la misma ruta, leeria la
    # configuracion por omision y registraria las ventanas de OTRO archivo --
    # justo el que se acaba de no cambiar. El tablero diria un horario y el
    # Programador dispararia otro.
    # `6>$null` Y NO -InformationAction: Registrar-Tarea fija Continue en su
    # propia llamada, asi que la preferencia del llamador no la puede callar. Su
    # linea de confirmacion es correcta cuando se ejecuta suelto y aqui sobra --
    # rompe la forma de la pantalla y repite lo que el veredicto de abajo ya dice
    # con su cifra. Solo se silencia ESTA llamada; el resto del tablero escribe
    # por el mismo flujo y no se toca.
    $registro = @{ Pieza = 'Motor'; Accion = 'Registrar' }
    if ($RutaConfiguracion) { $registro['RutaConfiguracion'] = $RutaConfiguracion }
    & "$PSScriptRoot\Registrar-Tarea.ps1" @registro -Confirm:$false 6> $null | Out-Null
    $tarea = Get-ScheduledTask -TaskName $script:NombreTarea -ErrorAction SilentlyContinue
    $disparadores = 0
    if ($tarea) { $disparadores = @($tarea.Triggers).Count }

    $c = $hecho.Cadencia
    if ($disparadores -ne $c.CorridasPorDia) {
        Write-Veredicto -Nivel 'ATENCION' -Frase (
            'El horario se guardo ({0}), pero la tarea quedo con {1} disparadores y deberian ser {2}.' -f `
                $c.Resumen, $disparadores, $c.CorridasPorDia) `
            -Accion 'Vuelve a entrar a ajustes y pulsa [T] para re-registrar la tarea.'
        return $hecho
    }

    Write-Veredicto -Nivel 'OK' -Frase (
        'Horario aplicado: {0} corridas al dia, {1}. Peor hueco {2} h y aviso a las {3} h. La tarea quedo con {4} disparadores, uno por ventana.' -f `
            $c.CorridasPorDia, $c.Resumen, $c.HuecoNominalMaximoHoras, $c.HorasParaAvisar, $disparadores) `
        -Accion ('En Healthchecks pon period {0} h y grace {1} h, o el testigo externo seguira juzgando con el horario viejo.' -f `
            $c.HuecoNominalMaximoHoras, [math]::Round($c.HorasParaAvisar - $c.HuecoNominalMaximoHoras, 2))
    return $hecho
}

function Read-VentanaElegida {
    <#
        .SYNOPSIS
            Pregunta cual de las ventanas, y devuelve su indice base cero o -1.
        .PARAMETER Cadencia
            La cadencia vigente.
        .PARAMETER Para
            Que se va a hacer con ella, para poder decirlo en la pregunta.
    #>
    [CmdletBinding()]
    [OutputType([int])]
    param(
        [Parameter(Mandatory)][psobject] $Cadencia,
        [Parameter(Mandatory)][string] $Para
    )

    $total = @($Cadencia.Ventanas).Count
    $cual = Read-Host ('   Cual ventana quieres {0}? (1-{1}, o Enter para dejarlo)' -f $Para, $total)
    if ([string]::IsNullOrWhiteSpace($cual)) { return -1 }
    [int] $n = 0
    if (-not [int]::TryParse(('' + $cual).Trim(), [ref]$n) -or $n -lt 1 -or $n -gt $total) {
        Write-Veredicto -Nivel 'SIN DATOS' -Frase ('"{0}" no es una de las {1} ventanas. No se cambia nada.' -f $cual, $total)
        return -1
    }
    return ($n - 1)
}

function ConvertTo-ListaDeVentana {
    <#
        .SYNOPSIS
            Pasa la cadencia vigente a la forma sencilla que espera
            Set-HorarioDeRespaldo, para poder modificarla y devolverla.
        .PARAMETER Cadencia
            La cadencia vigente.
    #>
    [CmdletBinding()]
    [OutputType([psobject[]])]
    param(
        [Parameter(Mandatory)][psobject] $Cadencia
    )
    return , @($Cadencia.Ventanas | ForEach-Object {
            [pscustomobject]@{ inicio = $_.Inicio; duracionHoras = [math]::Round($_.LargoMinutos / 60, 2) }
        })
}

function Show-Reparto {
    <#
        .SYNOPSIS
            La pantalla de ajustes: el horario se cambia AQUI, y las cuatro
            protecciones siguen pidiendo abrir el archivo.
        .DESCRIPTION
            Se llama Reparto y no Ajustes porque el analizador rechaza sustantivos
            en plural (charter 6.1: advertencia = error), y porque "reparto" es el
            nombre que usa el contrato para el corte de la seccion 10.1.

            EL HORARIO VUELVE AL MENU, POR DECISION DEL RESPONSABLE (2026-09-03):
            "me gustaria que las corridas pudieran ser movibles en el tablero y no
            en el archivo, con opciones sencillas". Eso resuelve la enmienda que
            estaba pendiente de su visto bueno desde el 02/09, y la resuelve al
            reves de como la habia dejado el agente.

            Y EL CORTE DE 10.1 NO SE ROMPE, PORQUE EL CORTE NUNCA FUE "TODO O
            NADA". Lo que 10.1 protege es lo que decide CUANTO se puede destruir
            antes de que el sistema se plante -- centinelas,
            clases y exclusiones --, y eso sigue exigiendo abrir el archivo a
            mano. Un horario decide CUANDO se copia: moverlo mal hace que se copie
            con menos frecuencia, y de eso avisan el icono y el testigo. Aflojar
            una clase o un centinela, en cambio, no lo nota nadie.

            LO QUE SI HACIA FALTA ERA QUE NO SE PUDIERA MOVER A OJO, y de eso se
            encargan las tres guardas de Set-HorarioDeRespaldo mas la vista previa
            de esta pantalla. Ninguna cifra derivada se teclea: el hueco se
            calcula y el umbral se compara contra el.
        .PARAMETER Configuracion
            El objeto de configuracion completo.
        .PARAMETER RutaConfiguracion
            El .jsonc, si no es el de por omision.
    #>
    [CmdletBinding()]
    [OutputType([psobject])]
    param(
        [Parameter(Mandatory)][psobject] $Configuracion,
        [AllowEmptyString()][string] $RutaConfiguracion = ''
    )

    $p = $script:Paleta
    $vigente = $Configuracion
    $seguirAqui = $true

    while ($seguirAqui) {
        $cadencia = $null
        try { $cadencia = Get-CadenciaDeCorrida -Configuracion $vigente }
        catch {
            Write-Veredicto -Nivel 'FALLO' -Frase ('No se puede leer el horario: {0}' -f $_.Exception.Message) `
                -Accion 'Hay que arreglar 3-Config/respaldo.jsonc a mano antes de poder ajustarlo desde aqui.'
            return $vigente
        }

        Write-Anuncio -Que 'Ajustes del horario' `
            -Detalle 'Cambia CUANDO corre el respaldo. Escribe en 3-Config/respaldo.jsonc y vuelve a registrar la tarea.'
        Show-HorarioActual -Cadencia $cadencia

        $siguiente = Get-ProximaVentanaDeCorrida -Cadencia $cadencia -NombreTarea $script:NombreTarea
        $proxima = $siguiente.Descripcion
        if (-not $siguiente.Existe) { $proxima = 'LA TAREA NO EXISTE: no corre solo' }
        elseif (-not $siguiente.Habilitada) { $proxima = 'TAREA DESHABILITADA' }
        Write-Linea ''
        Write-Linea ('     {0}Proxima corrida: {1}{2}' -f $p.Valor, $proxima, $p.Fin)
        Write-Linea '     El minuto lo sortea Windows dentro de la ventana; no se sabe hasta que dispara.'

        # LAS PROTECCIONES QUE QUEDAN SE ENSENAN Y NO SE TOCAN. Ver la cabecera.
        Write-Linea ''
        Write-Linea ('   {0}SOLO EDITANDO EL ARCHIVO{1}   (seccion 10.1, y sigue asi a proposito)' -f $p.Fuerte, $p.Fin)
        Write-Linea ('     Centinelas            {0,5} declarados' -f @($Configuracion.centinelas).Count)
        Write-Linea ('     Clases                {0,5} contenedores, {1} raices' -f `
            @($Configuracion.contenedores).Count, @($Configuracion.raicesDeclaradas).Count)
        Write-Linea '     Politica de borrado         clase A nunca borra, clase B espeja'
        Write-Linea '     Cada uno lleva encima, en el archivo, un comentario que dice que pasa si se cambia.'

        Write-Linea ''
        Write-Linea ('   {0}[M]{1} mover una ventana      {0}[D]{1} cambiar su duracion    {0}[U]{1} umbral de aviso' -f $p.Valor, $p.Fin)
        Write-Linea ('   {0}[+]{1} anadir una corrida     {0}[-]{1} quitar una corrida     {0}[T]{1} re-registrar la tarea' -f $p.Valor, $p.Fin)
        Write-Linea ('   {0}[V]{1} volver al menu' -f $p.Valor, $p.Fin)
        Write-Linea ''

        $eleccion = ('' + (Read-Host '   ajuste')).Trim().ToUpperInvariant()
        $resultado = $null

        switch ($eleccion) {
            'M' {
                $i = Read-VentanaElegida -Cadencia $cadencia -Para 'mover'
                if ($i -lt 0) { break }
                $hora = Read-Host '   Nueva hora de inicio, en formato HH:mm (por ejemplo 05:30)'
                if (('' + $hora).Trim() -notmatch '^([01][0-9]|2[0-3]):([0-5][0-9])$') {
                    Write-Veredicto -Nivel 'SIN DATOS' -Frase ('"{0}" no es una hora valida. Se espera HH:mm, de 00:00 a 23:59.' -f $hora)
                    break
                }
                $lista = ConvertTo-ListaDeVentana -Cadencia $cadencia
                $lista[$i].inicio = ('' + $hora).Trim()
                $resultado = Set-HorarioConVistaPrevia -Ventanas $lista -RutaConfiguracion $RutaConfiguracion
            }
            'D' {
                $i = Read-VentanaElegida -Cadencia $cadencia -Para 'alargar o acortar'
                if ($i -lt 0) { break }
                $horas = Read-Host '   Cuantas horas debe durar? (de 0.5 a 12)'
                [double] $h = 0
                if (-not [double]::TryParse(('' + $horas).Trim(), [ref]$h) -or $h -le 0 -or $h -gt 12) {
                    Write-Veredicto -Nivel 'SIN DATOS' -Frase ('"{0}" no sirve como duracion. Tiene que estar entre 0.5 y 12 horas.' -f $horas)
                    break
                }
                $lista = ConvertTo-ListaDeVentana -Cadencia $cadencia
                $lista[$i].duracionHoras = $h
                $resultado = Set-HorarioConVistaPrevia -Ventanas $lista -RutaConfiguracion $RutaConfiguracion
            }
            '+' {
                $hora = Read-Host '   A que hora empieza la corrida nueva? HH:mm'
                if (('' + $hora).Trim() -notmatch '^([01][0-9]|2[0-3]):([0-5][0-9])$') {
                    Write-Veredicto -Nivel 'SIN DATOS' -Frase ('"{0}" no es una hora valida. Se espera HH:mm.' -f $hora)
                    break
                }
                $horas = Read-Host '   Cuantas horas dura? (Enter para 3)'
                [double] $h = 3
                if (-not [string]::IsNullOrWhiteSpace($horas)) {
                    if (-not [double]::TryParse(('' + $horas).Trim(), [ref]$h) -or $h -le 0 -or $h -gt 12) {
                        Write-Veredicto -Nivel 'SIN DATOS' -Frase ('"{0}" no sirve como duracion.' -f $horas)
                        break
                    }
                }
                $lista = @(ConvertTo-ListaDeVentana -Cadencia $cadencia)
                $lista += [pscustomobject]@{ inicio = ('' + $hora).Trim(); duracionHoras = $h }
                $resultado = Set-HorarioConVistaPrevia -Ventanas $lista -RutaConfiguracion $RutaConfiguracion
            }
            '-' {
                if (@($cadencia.Ventanas).Count -le 1) {
                    Write-Veredicto -Nivel 'ATENCION' -Frase 'Queda una sola corrida al dia: quitarla dejaria el respaldo sin horario.' `
                        -Accion 'Si de verdad quieres que no corra solo, deshabilita la tarea en vez de vaciar el horario.'
                    break
                }
                $i = Read-VentanaElegida -Cadencia $cadencia -Para 'quitar'
                if ($i -lt 0) { break }
                # Se quita POR POSICION y no por comparacion de objetos: dos
                # ventanas pueden tener la misma hora escrita de dos formas, y
                # -ne sobre psobject compara referencias.
                $todas = @(ConvertTo-ListaDeVentana -Cadencia $cadencia)
                $lista = @($todas | Select-Object -Index (0..($todas.Count - 1) | Where-Object { $_ -ne $i }))
                $resultado = Set-HorarioConVistaPrevia -Ventanas $lista -RutaConfiguracion $RutaConfiguracion
            }
            'U' {
                Write-Linea ('     Tiene que ser MAYOR que el hueco maximo, que hoy son {0} h.' -f $cadencia.HuecoNominalMaximoHoras)
                $u = Read-Host '   A cuantas horas sin corrida buena debe avisar?'
                [int] $n = 0
                if (-not [int]::TryParse(('' + $u).Trim(), [ref]$n) -or $n -lt 1 -or $n -gt 720) {
                    Write-Veredicto -Nivel 'SIN DATOS' -Frase ('"{0}" no sirve como umbral.' -f $u)
                    break
                }
                $lista = ConvertTo-ListaDeVentana -Cadencia $cadencia
                $resultado = Set-HorarioConVistaPrevia -Ventanas $lista -HorasParaAvisar $n -RutaConfiguracion $RutaConfiguracion
            }
            'T' {
                Write-Anuncio -Que 'Re-registrar la tarea programada' `
                    -Detalle 'Vuelve a sortear las horas del dia dentro de las ventanas de arriba.'
                $reg = @{ Pieza = 'Motor'; Accion = 'Registrar' }
                if ($RutaConfiguracion) { $reg['RutaConfiguracion'] = $RutaConfiguracion }
                & "$PSScriptRoot\Registrar-Tarea.ps1" @reg -Confirm:$false 6> $null | Out-Null
                $t = Get-ScheduledTask -TaskName $script:NombreTarea -ErrorAction SilentlyContinue
                $cuantos = 0
                if ($t) { $cuantos = @($t.Triggers).Count }
                if ($cuantos -eq $cadencia.CorridasPorDia) {
                    Write-Veredicto -Nivel 'OK' -Frase ('Tarea re-registrada con {0} disparadores, uno por ventana.' -f $cuantos)
                }
                else {
                    Write-Veredicto -Nivel 'FALLO' -Frase ('La tarea quedo con {0} disparadores y deberian ser {1}.' -f $cuantos, $cadencia.CorridasPorDia) `
                        -Accion 'Mira el Programador de tareas de Windows: algo impidio registrarla entera.'
                }
            }
            # SALEN TRES COSAS, Y NO SOBRA NINGUNA. 'V' es la que anuncia la
            # pantalla; 'S' porque es lo que se teclea por costumbre -- el menu
            # de arriba sale con S y la mano va sola--; y Enter porque una
            # pantalla de la que no se sabe salir se abandona cerrando la
            # ventana, y cerrar la ventana a media edicion es como se pierden
            # los cambios.
            'V' { $seguirAqui = $false }
            'S' { $seguirAqui = $false }
            '' { $seguirAqui = $false }
            default { Write-Veredicto -Nivel 'SIN DATOS' -Frase ('"{0}" no es una de las opciones de esta pantalla.' -f $eleccion) }
        }

        # SI SE ESCRIBIO, SE RELEE. El objeto que trajo el tablero se quedo con el
        # horario viejo, y seguir pintandolo diria una cosa mientras el archivo
        # dice otra -- que es justo lo que esta pantalla existe para evitar.
        if ($resultado -and $resultado.Escrito) {
            $recarga = @{}
            if ($RutaConfiguracion) { $recarga['Ruta'] = $RutaConfiguracion }
            try { $vigente = Get-ConfiguracionRespaldo @recarga }
            catch { Write-Verbose "No se pudo releer la configuracion: $($_.Exception.Message)" }
        }
    }

    return $vigente
}

# CARGADO CON PUNTO SE EXPONEN LAS FUNCIONES Y NO SE ABRE NADA. Es la misma
# puerta que ya tienen verificar.ps1 y disco.ps1, y aqui hacia falta por una
# razon concreta: sin ella, la unica forma de comprobar como QUEDA PINTADA la
# ventana era abrirla a mano y mirarla, que es exactamente el "declarar hecho
# sin verlo" que este proyecto tiene prohibido.
if ($MyInvocation.InvocationName -eq '.') {
    Write-Verbose 'tablero.ps1 cargado con punto: se exponen las funciones y no se abre el menu.'
    return
}

$parametros = @{}
if ($PSBoundParameters.ContainsKey('RutaConfiguracion')) { $parametros['Ruta'] = $RutaConfiguracion }
$configuracion = Get-ConfiguracionRespaldo @parametros
$rutaConfig = if ($PSBoundParameters.ContainsKey('RutaConfiguracion')) { $RutaConfiguracion } else { $null }

$seguir = $true
while ($seguir) {
    Show-Ventana -Configuracion $configuracion
    # SE PIDE UNA "opcion", NO UNA "tecla". Lo pidio el responsable el
    # 2026-09-03: "no quiero ver tecla, se ve vibecodeado con esos terminos".
    # Y ademas es lo cierto -- lo que se elige es una operacion, y el teclado es
    # solo por donde entra.
    $opcion = Read-Host '  opcion'
    $comunes = @{}
    if ($rutaConfig) { $comunes['RutaConfiguracion'] = $rutaConfig }

    # UN FALLO DENTRO DE UNA OPCION NO PUEDE CERRAR LA VENTANA. Se cuenta y se
    # vuelve al menu: el dia que algo va mal es justo el dia que hace falta el
    # tablero.
    try {
        switch ($opcion.Trim().ToUpperInvariant()) {
            '1' {
                # NO HAY NADA QUE AUTORIZAR DESDE AQUI (ADR-0085). Cuando
                # existia el freno, autorizarlo estaba deliberadamente fuera
                # del menu para que no fuera un clic. Retirada la capa 2, los
                # abortos que quedan -centinela, origen inutilizable, deuda- no
                # se autorizan de ninguna manera, ni aqui ni en la consola.
                Write-Linea '   Copiando al nodo...'
                $r1 = & "$nucleo\respaldo.ps1" @comunes -Confirm:$false
                if ($r1 -and $r1.Abortada) { Write-Warning $r1.Motivo }
                else { Write-Linea '   Corrida terminada.' }
            }
            '2' {
                Write-Anuncio -Que 'Simulacion de la copia al NODO' `
                    -Detalle 'NO escribe nada. Mide que cambiaria en las 8 raices. ~1 min.'
                $r2 = & "$nucleo\respaldo.ps1" @comunes -SoloSimular -Confirm:$false
                Show-VeredictoDeSimulacion -Resultado $r2 -Configuracion $configuracion
            }
            '3' {
                Write-Anuncio -Que 'Cotejar lo guardado en el NODO contra el original de este equipo' `
                    -Detalle 'NO escribe nada. Lee archivos ENTEROS por la red: tarda MINUTOS y no esta colgado.'
                Show-VeredictoDelCotejo -Resultado (& "$nucleo\verificar.ps1" @comunes)
            }
            '4' { Show-Texto -Objeto (Read-EstadoRespaldo) -Vacio 'No hay estado escrito todavia.' }
            '5' {
                $destino = '{0}\{1}\{2}\_SEMILLA' -f `
                    $configuracion.destinos.nodo.unc.TrimEnd('\'),
                    $configuracion.destinos.nodo.raiz,
                    $configuracion.destinos.nodo.prefijoEquipo
                Show-Texto -Objeto (& "$nucleo\semilla.ps1" -Destino $destino -Confirm:$false) -Lista -Vacio 'La semilla no devolvio nada.'
            }
            '6' {
                Write-Anuncio -Que 'Estado del automatismo' `
                    -Detalle 'Solo lee. Comprueba las dos tareas y que las piezas del motor sigan ahi.'
                Show-VeredictoDelAutomatismo
            }
            '7' {
                # Las DOS pasadas. Si el nodo no responde corre solo la del
                # equipo y la copia queda declarada INCOMPLETA: nunca se dice
                # "al dia" a una copia fria a la que le falto la mitad (6.2).
                Write-Linea '   Copiando al disco frio (dos pasadas)...'
                $rd = & "$nucleo\disco.ps1" @comunes -Confirm:$false
                if ($rd.Abortada) { Write-Warning $rd.Motivo }
                elseif (-not $rd.Completa) {
                    Write-Warning 'COPIA FRIA INCOMPLETA: corrio solo la pasada equipo -> disco. Lo que solo vive en el nodo NO llego.'
                }
                elseif ($rd.ArchivosCopiados -eq 0) {
                    Write-Linea '   Copia fria AL DIA: las dos pasadas corrieron y no habia nada nuevo que copiar.'
                }
                else {
                    Write-Linea ('   Copia fria COMPLETA: {0} archivos nuevos.' -f $rd.ArchivosCopiados)
                }
            }
            '8' { Show-Texto -Objeto (& "$nucleo\disco.ps1" @comunes -SoloSimular -Confirm:$false) -Lista -Vacio 'La simulacion no devolvio nada.' }
            '9' {
                # Busca lo que una copia cortada dejo a medias. Existe porque un
                # apagon el 2026-09-02 dejo ocho archivos con el tamano correcto
                # y el contenido distinto, y /XO los habria saltado para siempre.
                Write-Linea '   Revisando el disco (recorre las dos rutas: puede tardar)...'
                $r9 = & "$nucleo\verificar.ps1" @comunes -RevisarDisco -TamanoMuestra 0
                Show-RevisionDelDisco -Revision $r9.DiscoEnVuelo
            }
            'R' { Show-PruebaDeRestauracion -Configuracion $configuracion }
            'A' {
                # SE RECOGE LO QUE DEVUELVE, y no es un detalle: si ahi dentro se
                # cambio el horario, el objeto que traiamos se quedo con el viejo
                # y la ventana seguiria pintando un horario que ya no corre.
                $rutaParaAjustes = if ($rutaConfig) { $rutaConfig } else { '' }
                $configuracion = Show-Reparto -Configuracion $configuracion -RutaConfiguracion $rutaParaAjustes
            }
            'S' { $seguir = $false }
            default { Write-Warning 'Opcion no reconocida.' }
        }
    }
    catch {
        Write-Warning ('La opcion fallo: {0}' -f $_.Exception.Message)
        Write-Verbose ('' + $_.ScriptStackTrace)
    }
}
