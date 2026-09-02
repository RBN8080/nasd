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
          hora de corrida            UMBRAL DEL FRENO
          pausar raices              CENTINELAS
          verbosidad                 POLITICA DE BORRADO
          lanzar acciones            CLASES

        La ventana ENSENA esos cuatro valores para que se puedan mirar, y dice
        donde se cambian. Ver no es poder cambiar.

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
. "$PSScriptRoot\estilo.ps1"

$script:NombreTarea     = 'NasRespaldo-Diario'
$script:NombreIndicador = 'NasRespaldo-Indicador'

$script:Capacidades = Initialize-Consola
$script:Paleta      = Get-Paleta -Capacidades $script:Capacidades
$script:Trazo       = Get-TrazoDeMarco -Unicode $script:Capacidades.Unicode
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
    if ([string]::IsNullOrEmpty($Texto)) { return }
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

function Write-Campo {
    <#
        .SYNOPSIS
            Escribe una fila etiqueta/valor dentro del marco.
        .PARAMETER Etiqueta
            Nombre del campo.
        .PARAMETER Valor
            Contenido.
        .PARAMETER Color
            Codigo de color para el valor. Vacio lo deja sin colorear.
    #>
    [CmdletBinding()]
    [OutputType([void])]
    param(
        # AllowEmptyString tambien en la etiqueta: una fila de continuacion -el
        # detalle del disco debajo de su fila- no tiene nombre propio, y sin
        # esto el enlace del parametro tumbaba la ventana entera al pintar.
        [Parameter(Mandatory)][AllowEmptyString()][string] $Etiqueta,
        [Parameter(Mandatory)][AllowEmptyString()][string] $Valor,
        [string] $Color = ''
    )

    # Se recorta ANTES de colorear: con los codigos dentro, .Length miente.
    $corto = Limit-Texto -Texto $Valor -Maximo (Get-AnchoDeValor)
    $pintado = if ($Color) { '{0}{1}{2}' -f $Color, $corto, $script:Paleta.Fin } else { $corto }
    $campo = Format-Campo -Etiqueta $Etiqueta -Valor $pintado -Paleta $script:Paleta -VisibleValor $corto.Length
    Write-Information (Format-LineaDeTablero -Trazo $script:Trazo -Paleta $script:Paleta `
            -Texto $campo.Texto -Visible $campo.Visible) -InformationAction Continue
}

function Write-Separador {
    <#
        .SYNOPSIS
            Una linea del marco con su titulo incrustado.
        .PARAMETER Titulo
            Nombre del bloque.
        .PARAMETER Posicion
            Superior, Union o Inferior.
        .PARAMETER Derecha
            Texto pegado al extremo derecho.
    #>
    [CmdletBinding()]
    [OutputType([void])]
    param(
        [string] $Titulo = '',
        [ValidateSet('Superior', 'Union', 'Inferior')][string] $Posicion = 'Union',
        [string] $Derecha = ''
    )
    Write-Information (Format-LineaDeMarco -Trazo $script:Trazo -Paleta $script:Paleta `
            -Titulo $Titulo -Posicion $Posicion -Derecha $Derecha) -InformationAction Continue
}

function Show-Ventana {
    <#
        .SYNOPSIS
            Pinta la ventana de estado y el menu.
        .PARAMETER Configuracion
            El objeto de configuracion completo.
    #>
    [CmdletBinding()]
    [OutputType([void])]
    param(
        [Parameter(Mandatory)][psobject] $Configuracion
    )

    $v           = Get-EstadoDelIndicador
    $estado      = Read-EstadoRespaldo
    $simbolo     = Get-Simbolo -Estado $v.Estado -Unicode $script:Capacidades.Unicode
    $colorEstado = Get-ColorDeEstado -Estado $v.Estado -Paleta $script:Paleta
    $p           = $script:Paleta

    Write-Linea ''
    Write-Separador -Titulo 'Respaldo del NAS' -Posicion 'Superior' -Derecha (Get-Date -Format 'dd/MM HH:mm')
    Write-Information (Format-LineaDeTablero -Trazo $script:Trazo -Paleta $p) -InformationAction Continue

    # EL COMPONENTE `aviso` DEL PANEL, PORTADO. Alli un estado se marca con una
    # caja de REGLA LATERAL en el color semantico y el texto del mismo color;
    # aqui la regla es un bloque medio a la izquierda. Asi el bloque de estado
    # se lee como un bloque y no como una linea mas de la lista.
    #
    # EL SIMBOLO SIGUE MANDANDO SOBRE EL COLOR: en una terminal sin color la
    # linea tiene que decir lo mismo (seccion 10.2). Get-Simbolo ya trae su
    # propio espacio a cada lado y su ancho cambia entre Unicode y ASCII, asi
    # que no se le suman espacios aqui.
    $titular = '  {0}{1} {2}{3}{4}{5}' -f `
        $colorEstado, $script:Regla, $p.Fuerte, $simbolo, $v.Estado.ToUpperInvariant(), $p.Fin
    Write-Information (Format-LineaDeTablero -Trazo $script:Trazo -Paleta $p `
            -Texto $titular -Visible (2 + $script:Regla.Length + 1 + $simbolo.Length + $v.Estado.Length)) -InformationAction Continue

    # La regla continua por el detalle: es el mismo aviso, no dos cosas.
    $detalle = '  {0}{1}{2}   {3}{4}{5}' -f `
        $colorEstado, $script:Regla, $p.Fin, $p.Valor, $v.Detalle, $p.Fin
    Write-Information (Format-LineaDeTablero -Trazo $script:Trazo -Paleta $p `
            -Texto $detalle -Visible (2 + $script:Regla.Length + 3 + $v.Detalle.Length)) -InformationAction Continue

    [datetime] $cuando = [datetime]::MinValue
    if ([datetime]::TryParse(('' + $estado['momento']), [ref] $cuando)) {
        $horas = ((Get-Date) - $cuando).TotalHours
        $cola = 'ultima corrida al nodo  {0:dd/MM HH:mm}  (hace {1:N0} h)' -f $cuando, $horas
        $linea = '  {0}{1}{2}   {3}{4}{5}' -f $colorEstado, $script:Regla, $p.Fin, $p.Tenue, $cola, $p.Fin
        Write-Information (Format-LineaDeTablero -Trazo $script:Trazo -Paleta $p `
                -Texto $linea -Visible (2 + $script:Regla.Length + 3 + $cola.Length)) -InformationAction Continue
    }
    Write-Information (Format-LineaDeTablero -Trazo $script:Trazo -Paleta $p) -InformationAction Continue

    # --- Destinos ---------------------------------------------------------
    Write-Separador -Titulo 'Destinos'
    $unc = $Configuracion.destinos.nodo.unc
    # Test-Path directo y no Test-DestinoNodo: pintar la ventana no debe escribir
    # lineas de ERROR en el registro, ni esperar reintentos de medio minuto.
    if (Test-Path -LiteralPath $unc -ErrorAction SilentlyContinue) {
        Write-Campo -Etiqueta 'Nodo' -Valor ('{0}   responde' -f $unc) -Color $p.Verde
    }
    else {
        Write-Campo -Etiqueta 'Nodo' -Valor ('{0}   NO RESPONDE' -f $unc) -Color $p.Rojo
    }

    $disco = Get-DiscoFrio -Configuracion $Configuracion 2>$null
    if ($disco) {
        # LA FECHA SALE DE LA ULTIMA CORRIDA, NO DEL CENTINELA. El centinela es
        # el carnet de identidad del disco y la copia nunca lo toca, asi que
        # medir su fecha decia "escrito hace 1 dia" justo despues de escribir.
        $texto = '{0}   serie {1}' -f $disco.Raiz, $disco.Serie
        $color = $p.Verde
        [datetime] $cd = [datetime]::MinValue
        if ($estado.ContainsKey('disco_momento') -and
            [datetime]::TryParse(('' + $estado['disco_momento']), [ref] $cd)) {
            $texto += '   copiado {0:dd/MM HH:mm}' -f $cd
        }
        else {
            $texto += '   sin copia registrada'
            $color = $p.Gris
        }
        if (('' + $estado['disco_estado']) -eq 'Atencion') { $color = $p.Ambar }
        Write-Campo -Etiqueta 'Disco frio' -Valor $texto -Color $color
        if ($estado.ContainsKey('disco_detalle') -and $estado['disco_detalle']) {
            Write-Campo -Etiqueta '' -Valor ('' + $estado['disco_detalle']) -Color $p.Tenue
        }
    }
    else {
        # El disco se conecta A PETICION (seccion 6.3): no hay calendario y es
        # deliberado. Que no este no es una falla, asi que no se pinta en rojo.
        Write-Campo -Etiqueta 'Disco frio' -Valor 'no conectado' -Color $p.Gris
    }

    # --- Automatismo ------------------------------------------------------
    Write-Separador -Titulo 'Automatismo'
    foreach ($par in @(
            @{ Eti = 'Motor'; Tarea = $script:NombreTarea;     Falta = 'NO EXISTE - el respaldo no corre solo' },
            @{ Eti = 'Icono'; Tarea = $script:NombreIndicador; Falta = 'NO EXISTE - el icono no arranca solo' })) {
        $t = Get-ScheduledTask -TaskName $par.Tarea -ErrorAction SilentlyContinue
        if (-not $t) {
            Write-Campo -Etiqueta $par.Eti -Valor $par.Falta -Color $p.Rojo
        }
        elseif ($t.State -eq 'Disabled') {
            Write-Campo -Etiqueta $par.Eti -Valor ('{0}   DESHABILITADA' -f $par.Tarea) -Color $p.Ambar
        }
        else {
            $extra = ''
            if ($par.Eti -eq 'Motor') {
                $d = @($t.Triggers) | Select-Object -First 1
                if ($d -and $d.StartBoundary) {
                    [datetime] $h = [datetime]::MinValue
                    if ([datetime]::TryParse($d.StartBoundary, [ref] $h)) { $extra = '   {0:HH:mm}' -f $h }
                }
            }
            Write-Campo -Etiqueta $par.Eti -Valor ('{0}{1}   {2}' -f $par.Tarea, $extra, $t.State) -Color $p.Verde
        }
    }

    # --- Protecciones -----------------------------------------------------
    Write-Separador -Titulo 'Protecciones' -Derecha 'se cambian en 3-Config/respaldo.jsonc'
    $minimo = if ($Configuracion.freno.PSObject.Properties.Name -contains 'minimoArchivosParaFrenar') {
        $Configuracion.freno.minimoArchivosParaFrenar
    } else { 0 }
    Write-Campo -Etiqueta 'Freno' -Valor ('{0} % y minimo de {1} archivos' -f $Configuracion.freno.umbralPorcentajeDeArchivosQueCambian, $minimo)
    Write-Campo -Etiqueta 'Centinelas' -Valor ('{0} declarados' -f @($Configuracion.centinelas).Count)
    Write-Campo -Etiqueta 'Clases' -Valor ('{0} contenedores, {1} raices declaradas' -f @($Configuracion.contenedores).Count, @($Configuracion.raicesDeclaradas).Count)
    Write-Campo -Etiqueta 'Borrado' -Valor 'clase A nunca borra   -   clase B espeja'
    Write-Separador -Posicion 'Inferior'

    # --- Menu -------------------------------------------------------------
    Write-Linea ''
    Write-Linea ('   {0}COPIAR{1}                  {0}COMPROBAR{1}                 {0}SISTEMA{1}' -f $p.Fuerte, $p.Fin)
    Write-Linea '   [1] al nodo             [3] verificar el nodo     [5] semilla'
    Write-Linea '   [2] simular             [4] estado completo       [6] tareas'
    Write-Linea '   [7] al disco frio       [9] revisar el disco      [A] ajustes'
    Write-Linea '   [8] simular el disco    [R] probar restauracion   [S] salir'
    Write-Linea ''
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
        return
    }

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
        }
    }
    else {
        Write-Warning 'Falta RESTAURAR.ps1 dentro de la semilla.'
    }

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
    $t = Get-ScheduledTask -TaskName $script:NombreTarea -ErrorAction SilentlyContinue
    $hora = 'sin tarea'
    if ($t) {
        $d = @($t.Triggers) | Select-Object -First 1
        [datetime] $h = [datetime]::MinValue
        if ($d -and $d.StartBoundary -and [datetime]::TryParse($d.StartBoundary, [ref] $h)) { $hora = '{0:HH:mm}' -f $h }
    }

    Write-Linea ''
    Write-Linea ('   {0}SE CAMBIAN DESDE AQUI{1}' -f $p.Fuerte, $p.Fin)
    Write-Linea ('   Hora de la corrida diaria : {0}' -f $hora)
    Write-Linea ''
    Write-Linea ('   {0}SOLO EDITANDO 3-Config/respaldo.jsonc{1}   (seccion 10.1)' -f $p.Fuerte, $p.Fin)
    Write-Linea ('   Umbral del freno          : {0} %' -f $Configuracion.freno.umbralPorcentajeDeArchivosQueCambian)
    Write-Linea ('   Centinelas                : {0} declarados' -f @($Configuracion.centinelas).Count)
    Write-Linea  '   Politica de borrado       : clase A nunca borra, clase B espeja'
    Write-Linea ('   Clases                    : {0} contenedores, {1} raices' -f @($Configuracion.contenedores).Count, @($Configuracion.raicesDeclaradas).Count)
    Write-Linea ''
    Write-Linea '   Cada uno lleva encima, en el archivo, un comentario que dice que pasa si se cambia.'
    Write-Linea ''

    $nueva = Read-Host '   Nueva hora HH:mm (Enter para dejarla igual)'
    if ([string]::IsNullOrWhiteSpace($nueva)) {
        Write-Linea '   Sin cambios.'
        return
    }
    if ($nueva -notmatch '^([01]\d|2[0-3]):[0-5]\d$') {
        Write-Warning 'Formato no valido. Se esperaba HH:mm, por ejemplo 22:30. Sin cambios.'
        return
    }
    & "$PSScriptRoot\Registrar-Tarea.ps1" -Pieza Motor -Accion Registrar -Hora $nueva -Confirm:$false -InformationAction Continue | Out-Null
    Write-Linea ('   {0}Hora de la corrida cambiada a {1}.{2}' -f $p.Verde, $nueva, $p.Fin)
}

$parametros = @{}
if ($PSBoundParameters.ContainsKey('RutaConfiguracion')) { $parametros['Ruta'] = $RutaConfiguracion }
$configuracion = Get-ConfiguracionRespaldo @parametros
$rutaConfig = if ($PSBoundParameters.ContainsKey('RutaConfiguracion')) { $RutaConfiguracion } else { $null }

$seguir = $true
while ($seguir) {
    Show-Ventana -Configuracion $configuracion
    $tecla = Read-Host '   Elija'
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
