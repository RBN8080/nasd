#Requires -Version 5.1
<#
    .SYNOPSIS
        El menu de teclas sobre la ventana de estado.

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
        ('cambio {0} % (freno: {1} %)' -f $cambio, $Configuracion.freno.umbralPorcentajeDeArchivosQueCambian))

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
    # 10.1): el umbral del freno, los centinelas y las clases se cambian
    # editando el archivo, porque subir un umbral desde una pantalla bonita
    # desarma la defensa con dos teclas y sin dejar rastro.
    Write-Linea ' '
    Write-Linea (Get-Regla -Paleta $p -Unicode $u)
    Write-Linea ('  {0}{1}{2}{3}{4}' -f ($p.Fuerte + $p.Titulo), `
            'COPIAR                 ', 'COMPROBAR                ', 'SISTEMA', $p.Fin)
    foreach ($fila in @(
            , @('[1] al nodo', '[3] verificar el nodo', '[5] semilla')
            , @('[2] simular', '[4] estado completo', '[6] tareas')
            , @('[7] al disco frio', '[9] revisar el disco', '[A] ajustes')
            , @('[8] simular el disco', '[R] probar restauracion', '[S] salir'))) {
        Write-Linea ('  {0}{1}{2}' -f `
            (Format-Celda -Texto $fila[0] -Ancho 23 -Paleta $p -Color $p.Valor), `
            (Format-Celda -Texto $fila[1] -Ancho 25 -Paleta $p -Color $p.Valor), `
            (Format-Celda -Texto $fila[2] -Ancho 20 -Paleta $p -Color $p.Valor))
    }
    Write-Linea ('  {0}freno, centinelas y clases: se cambian en 3-Config/respaldo.jsonc{1}' -f $p.Tenue, $p.Fin)
    Write-Linea ' '
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

function Show-Reparto {
    <#
        .SYNOPSIS
            El reparto de la seccion 10.1: lo que el menu SI puede cambiar
            y lo que no. Se llama Reparto y no Ajustes porque el analizador
            rechaza sustantivos en plural (charter 6.1: advertencia = error), y
            porque "reparto" es el nombre que usa el contrato.
        .DESCRIPTION
            El reparto de la seccion 10.1, hecho pantalla. Aqui solo aparece lo
            que el menu tiene permitido tocar; las cuatro protecciones se
            ENSENAN en la ventana y se cambian editando el archivo, que es
            justamente lo que impide relajarlas con un clic.
        .PARAMETER Configuracion
            El objeto de configuracion completo.
    #>
    [CmdletBinding()]
    [OutputType([void])]
    param(
        [Parameter(Mandatory)][psobject] $Configuracion
    )

    $p = $script:Paleta

    # LA HORA UNICA YA NO EXISTE, Y ESO MUEVE UNA LINEA DE LA SECCION 10.1.
    # El reparto de 10.1 ponia "hora de corrida" en la columna del menu, cuando
    # habia UNA hora y cambiarla era elegir un momento del dia. Con el pendiente
    # 26 lo que hay son TRES VENTANAS SACADAS DE UNA MEDICION de 30 dias, y de
    # ellas cuelgan el umbral del indicador, el ambar de la tabla y lo que espera
    # el testigo. Juntarlas o moverlas a ojo degrada la proteccion sin que se
    # note, que es exactamente el riesgo que 10.1 nombra al partir el reparto en
    # dos. Asi que las ventanas pasan a la columna del ARCHIVO, junto al umbral
    # del freno, y aqui se ENSENAN.
    #
    # Queda anotado como lo que es: una enmienda a 10.1 pendiente del visto bueno
    # del responsable, no una decision del agente. Lo que si sigue en el menu es
    # re-registrar la tarea, que es lo que hay que hacer despues de tocar el
    # archivo -y sortear de nuevo las horas del dia-.
    $cadencia = $null
    try { $cadencia = Get-CadenciaDeCorrida -Configuracion $Configuracion }
    catch { Write-Verbose "No se pudo leer la cadencia para el reparto: $($_.Exception.Message)" }
    $proxima = 'sin cadencia legible'
    if ($cadencia) {
        $siguiente = Get-ProximaVentanaDeCorrida -Cadencia $cadencia -NombreTarea $script:NombreTarea
        $proxima = $siguiente.Descripcion
        if (-not $siguiente.Existe) { $proxima = 'LA TAREA NO EXISTE: no corre solo' }
        elseif (-not $siguiente.Habilitada) { $proxima = 'TAREA DESHABILITADA' }
    }

    Write-Linea ''
    Write-Linea ('   {0}SE CAMBIAN DESDE AQUI{1}' -f $p.Fuerte, $p.Fin)
    Write-Linea ('   Re-registrar la tarea     : vuelve a sortear las horas del dia'    )
    Write-Linea ''
    Write-Linea ('   {0}SOLO EDITANDO 3-Config/respaldo.jsonc{1}   (seccion 10.1)' -f $p.Fuerte, $p.Fin)
    if ($cadencia) {
        # PARTIDA EN DOS LINEAS a proposito: las tres ventanas juntas se pasan
        # del ancho del tablero, y una linea que se sale rompe la unica pantalla
        # que tiene que poder leerse de un vistazo.
        Write-Linea ('   Corridas al dia           : {0}, a minuto sorteado' -f $cadencia.CorridasPorDia)
        Write-Linea ('   Ventanas                  : {0}' -f $cadencia.Resumen)
        Write-Linea ('   Hueco recibol maximo      : {0} h  -> ambar de la tabla' -f $cadencia.HuecoNominalMaximoHoras)
        Write-Linea ('   Avisa sin corrida buena   : {0} h  -> ambar del icono' -f $cadencia.HorasParaAvisar)
    }
    else {
        Write-Linea ('   Ventanas de corrida       : {0}NO SE PUDO LEER LA CADENCIA{1}' -f $p.Rojo, $p.Fin)
    }
    Write-Linea ('   Umbral del freno          : {0} %' -f $Configuracion.freno.umbralPorcentajeDeArchivosQueCambian)
    Write-Linea ('   Centinelas                : {0} declarados' -f @($Configuracion.centinelas).Count)
    Write-Linea  '   Politica de borrado       : clase A nunca borra, clase B espeja'
    Write-Linea ('   Clases                    : {0} contenedores, {1} raices' -f @($Configuracion.contenedores).Count, @($Configuracion.raicesDeclaradas).Count)
    Write-Linea ''
    Write-Linea ('   {0}Proxima corrida: {1}{2}' -f $p.Valor, $proxima, $p.Fin)
    # SE ENSENA LA VENTANA Y NO UN MINUTO, Y LA LINEA LO DICE. El Programador
    # sortea el retraso en CADA consulta: pedirle la hora diez veces da diez
    # respuestas distintas. Poner una de ellas aqui seria ensenar como promesa un
    # numero que cambia solo, en la unica pantalla que no puede mentir.
    Write-Linea '   El minuto lo sortea Windows dentro de la ventana; no se sabe hasta que dispara.'
    Write-Linea ''
    Write-Linea '   Cada uno lleva encima, en el archivo, un comentario que dice que pasa si se cambia.'
    Write-Linea ''

    $respuesta = Read-Host '   Re-registrar la tarea y volver a sortear? (s/N)'
    if ('' + $respuesta -notmatch '^[sS]') {
        Write-Linea '   Sin cambios.'
        return
    }
    & "$PSScriptRoot\Registrar-Tarea.ps1" -Pieza Motor -Accion Registrar -Confirm:$false -InformationAction Continue | Out-Null
    $t = Get-ScheduledTask -TaskName $script:NombreTarea -ErrorAction SilentlyContinue
    $cuantos = 0
    if ($t) { $cuantos = @($t.Triggers).Count }
    Write-Linea ('   {0}Tarea re-registrada con {1} disparadores, uno por ventana.{2}' -f $p.Verde, $cuantos, $p.Fin)
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
    $tecla = Read-Host '  tecla'
    $comunes = @{}
    if ($rutaConfig) { $comunes['RutaConfiguracion'] = $rutaConfig }

    # UN FALLO DENTRO DE UNA OPCION NO PUEDE CERRAR LA VENTANA. Se cuenta y se
    # vuelve al menu: el dia que algo va mal es justo el dia que hace falta el
    # tablero.
    try {
        switch ($tecla.Trim().ToUpperInvariant()) {
            '1' {
                # AUTORIZAR EL FRENO NO ES UNA OPCION DEL MENU y es deliberado:
                # si el freno salta, quien lo autoriza tiene que verlo primero y
                # volver a lanzar con -AutorizarFreno desde la consola. Un menu
                # que ofreciera "copiar de todos modos" convertiria la defensa
                # en un clic.
                Write-Linea '   Copiando al nodo...'
                $r1 = & "$nucleo\respaldo.ps1" @comunes -Confirm:$false
                if ($r1 -and $r1.Abortada) { Write-Warning $r1.Motivo }
                else { Write-Linea '   Corrida terminada.' }
            }
            '2' {
                Write-Linea '   Simulando (no se escribe nada)...'
                $r2 = & "$nucleo\respaldo.ps1" @comunes -SoloSimular -Confirm:$false
                Show-Texto -Objeto ($r2 | Select-Object Abortada, Motivo, Freno) -Lista -Vacio 'La simulacion no devolvio nada.'
            }
            '3' {
                Write-Linea '   Verificando contra el nodo. El nivel 3 LEE LOS ARCHIVOS ENTEROS:'
                Write-Linea '   con videos del drone en la muestra tarda MINUTOS. No esta colgado.'
                Show-Texto -Objeto (& "$nucleo\verificar.ps1" @comunes) -Lista -Vacio 'La verificacion no devolvio nada.'
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
                foreach ($n in @($script:NombreTarea, $script:NombreIndicador)) {
                    $t = Get-ScheduledTask -TaskName $n -ErrorAction SilentlyContinue
                    if ($t) { Show-Texto -Objeto ($t | Get-ScheduledTaskInfo) -Lista }
                    else { Write-Warning "La tarea '$n' NO existe." }
                }
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
            'A' { Show-Reparto -Configuracion $configuracion }
            'S' { $seguir = $false }
            default { Write-Warning 'Opcion no reconocida.' }
        }
    }
    catch {
        Write-Warning ('La opcion fallo: {0}' -f $_.Exception.Message)
        Write-Verbose ('' + $_.ScriptStackTrace)
    }
}
