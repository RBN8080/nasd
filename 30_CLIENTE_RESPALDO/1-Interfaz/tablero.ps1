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

    .PARAMETER Opcion
        Ejecuta esa opcion del menu y sale, sin pintar la ventana. Lo usa el
        tablero de PowerShell 7 para correr cada opcion en 5.1 (ADR-0097).

    .PARAMETER Interactivo
        Solo junto con -Opcion: activa cabecera, confirmaciones de seguridad,
        reintentos y pausa de vuelta al tablero como en una sesion interactiva.

    .EXAMPLE
        .\tablero.ps1
#>
[CmdletBinding()]
[OutputType([void])]
param(
    [ValidateNotNullOrEmpty()]
    [string] $RutaConfiguracion,

    [ValidatePattern('^[0-9A-Za-z]+$')]
    [string] $Opcion,

    [switch] $Interactivo
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$nucleo = Join-Path (Split-Path $PSScriptRoot -Parent) '2-Nucleo'
. "$nucleo\comun.ps1"
. "$nucleo\clasificar.ps1"
. "$nucleo\testigo.ps1"
. "$PSScriptRoot\estilo.ps1"

function New-ContextoDeTablero {
    <#
        .SYNOPSIS
            Encapsula el estado de sesion e interfaz del tablero en un objeto unico.
        .DESCRIPTION
            Sustituye multiples variables $script: dispersas por un contexto
            explicito inicializado en un solo punto.
    #>
    [Diagnostics.CodeAnalysis.SuppressMessageAttribute(
        'PSUseShouldProcessForStateChangingFunctions', '',
        Justification = 'Construye un objeto de estado en memoria; no modifica disco ni sistema.')]
    [CmdletBinding()]
    [OutputType([psobject])]
    param(
        [psobject] $Capacidades = (Initialize-Consola),
        [bool] $PanelRico = $false
    )
    return [pscustomobject]@{
        NombreTarea              = 'NasRespaldo-Diario'
        NombreIndicador          = 'NasRespaldo-Indicador'
        Capacidades              = $Capacidades
        Paleta                   = (Get-Paleta -Capacidades $Capacidades)
        Regla                    = (Get-ReglaDeAviso -Unicode $Capacidades.Unicode)
        PanelRico                = $PanelRico
        CanceladaEnVista         = $false
        PermitirReintentoEnVista = $false
        EtiquetaReintentoEnVista = 'actualizar'
        AvisoMenu                = ''
    }
}

function Reset-EstadoDeVista {
    <#
        .SYNOPSIS
            Restablece las banderas transitorias al abrir una subvista.
    #>
    [CmdletBinding()]
    [OutputType([void])]
    param(
        [Parameter(Mandatory)][psobject] $Contexto
    )
    $Contexto.CanceladaEnVista         = $false
    $Contexto.PermitirReintentoEnVista = $false
    $Contexto.EtiquetaReintentoEnVista = 'actualizar'
}

# EL PANEL CON SPECTRE ES OPCIONAL (ADR-0097): PowerShell 7.4 y el modulo
# PwshSpectreConsole. Sin ellos se pinta la ventana de siempre.
. "$PSScriptRoot\panel.ps1"
$script:Ctx         = New-ContextoDeTablero -Capacidades (Initialize-Consola) -PanelRico (Initialize-PanelRico)
$script:Capacidades = $script:Ctx.Capacidades
$script:Paleta      = $script:Ctx.Paleta
$script:Regla       = $script:Ctx.Regla
$script:PanelRico   = $script:Ctx.PanelRico

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
        [switch] $Avisar,

        [ValidateRange(0, 500)][int] $Ancho = 0,
        [ValidateRange(8, 30)][int] $AnchoEtiqueta = 14
    )
    $utiles = @($Partes | Where-Object { -not [string]::IsNullOrWhiteSpace($_) })
    if ($utiles.Count -eq 0) { return }
    if ([string]::IsNullOrEmpty($Color)) { $Color = $Paleta.Valor }
    if ($Ancho -le 0) { $Ancho = $script:AnchoTablero }
    $Marca = ''
    $anchoMarca = 0
    if ($Avisar) {
        $Marca = '{0}{1}{2}' -f $Color, $script:Regla, $Paleta.Fin
        $anchoMarca = $script:Regla.Length
    }

    # SE PARTE EN VARIAS LINEAS, NO SE RECORTA. Incluso una sola parte larga se
    # envuelve por palabras para no perder avisos criticos en ventanas estrechas.
    $sitio = [Math]::Max(20, $Ancho - $AnchoEtiqueta)
    $separador = '  |  '
    $lineas = New-Object System.Collections.Generic.List[string]
    $actual = ''
    foreach ($parte in $utiles) {
        if ($parte.Length -gt $sitio) {
            if ($actual) { $lineas.Add($actual); $actual = '' }
            foreach ($trozo in @(Split-TextoEnLineas -Texto $parte -Ancho $sitio)) {
                $lineas.Add($trozo)
            }
            continue
        }
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
            (Format-Celda -Texto $rotulo -Ancho ($AnchoEtiqueta - $anchoMarca) -Paleta $Paleta -Color $Paleta.Etiqueta), `
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
            Las filas de la tabla por raiz, pintadas con la paleta de siempre.
        .DESCRIPTION
            Que dice cada celda y de que tono lo decide Get-FilasDeTabla; aqui
            solo se traduce el tono a color.
        .PARAMETER Filas
            Lo que devolvio Read-EstadoPorRaiz.
        .PARAMETER Paleta
            Los colores.
        .PARAMETER HorasParaAmbar
            Ver Get-FilasDeTabla.
    #>
    [CmdletBinding()]
    [OutputType([void])]
    param(
        [Parameter(Mandatory)][AllowEmptyCollection()][psobject[]] $Filas,
        [Parameter(Mandatory)][hashtable] $Paleta,
        [ValidateRange(1, 720)][double] $HorasParaAmbar = 12,
        [AllowEmptyCollection()][psobject[]] $RaicesActuales = @(),
        [AllowEmptyCollection()][psobject[]] $Centinelas = @(),
        [AllowNull()][psobject] $Geometria = $null
    )

    $geo = if ($Geometria) { $Geometria } else { Get-GeometriaDeConsola }
    $modelo = @(Get-FilasDeTabla -Filas $Filas -HorasParaAmbar $HorasParaAmbar `
            -RaicesActuales $RaicesActuales -Centinelas $Centinelas)
    if ($modelo.Count -eq 0) {
        Write-Linea ('  {0}Sin registro de volumenes todavia. Ejecute [1] Respaldo al nodo SMB.{1}' -f `
                $Paleta.Tenue, $Paleta.Fin)
        return
    }

    foreach ($m in $modelo) {
        Write-Linea ('  {0}{1}{2}{3}{4}' -f `
            (Format-Celda -Texto $m.Etiqueta -Ancho $geo.ColRaiz -Paleta $Paleta -Color $Paleta.Valor), `
            (Format-Celda -Texto $m.Clase -Ancho $geo.ColPol -Paleta $Paleta -Color $Paleta.Tenue), `
            (Format-Celda -Texto ('{0} arch. ' -f $m.Movidos) -Ancho $geo.ColDelta -Paleta $Paleta -Derecha `
                    -Color $Paleta.Tenue), `
            (Format-Celda -Texto $m.Nodo.Texto -Ancho $geo.ColNodo -Paleta $Paleta -Color $Paleta[$m.Nodo.Tono]), `
            (Format-Celda -Texto $m.Disco.Texto -Ancho $geo.ColDisco -Paleta $Paleta -Color $Paleta[$m.Disco.Tono]))
    }
}

function Get-FilasDeTabla {
    <#
        .SYNOPSIS
            La tabla por raiz YA DECIDIDA: el texto y el tono de cada celda.
        .DESCRIPTION
            LAS DOS VENTANAS BEBEN DE AQUI (ADR-0097), la de siempre y la de
            Spectre, asi que cada regla de color vive en un solo sitio. El tono
            es un nombre -Verde, Ambar, Rojo, Gris- que cada ventana traduce a
            su paleta.

            CRUCE EN VIVO CON EL DISCO: si se pasa -RaicesActuales (lo que
            Get-RaicesDeRespaldo ve AHORA en los contenedores locales), la tabla
            deja de ser solo una foto historica de RAICES.tsv:
              - Una carpeta NN_* nueva sale de inmediato como NUEVA (ambar).
              - Una raiz que estaba en RAICES.tsv pero desaparecio del disco
                (renombrada o borrada) sale como AUSENTE (rojo) en vez de seguir
                pintando AL DIA en verde.
        .PARAMETER Filas
            Lo que devolvio Read-EstadoPorRaiz.
        .PARAMETER HorasParaAmbar
            A partir de cuantas horas una copia buena deja de pintarse verde.
        .PARAMETER RaicesActuales
            Opcional: raices actuales resueltas del disco en vivo.
        .PARAMETER Centinelas
            Opcional: centinelas declarados, para detectar raices sin cobertura.
    #>
    [CmdletBinding()]
    [OutputType([psobject[]])]
    param(
        [Parameter(Mandatory)][AllowEmptyCollection()][psobject[]] $Filas,
        [ValidateRange(1, 720)][double] $HorasParaAmbar = 12,
        [AllowEmptyCollection()][psobject[]] $RaicesActuales = @(),
        [AllowEmptyCollection()][psobject[]] $Centinelas = @()
    )

    $resultado = New-Object System.Collections.Generic.List[psobject]

    $porDestino = @{}
    $nombrePorClave = @{}
    foreach ($f in $Filas) {
        if ($null -eq $f) { continue }
        if (-not $porDestino.ContainsKey($f.Destino)) { $porDestino[$f.Destino] = @{} }
        $clave = ('' + $f.Raiz).TrimEnd('\').ToLowerInvariant()
        $porDestino[$f.Destino][$clave] = $f
        if (-not $nombrePorClave.ContainsKey($clave)) { $nombrePorClave[$clave] = ('' + $f.Raiz).TrimEnd('\') }
    }

    $actualesPorClave = @{}
    foreach ($ra in @($RaicesActuales)) {
        if ($null -eq $ra -or -not ($ra.PSObject.Properties.Name -contains 'Ruta') -or -not $ra.Ruta) { continue }
        $clave = ('' + $ra.Ruta).TrimEnd('\').ToLowerInvariant()
        $actualesPorClave[$clave] = $ra
        $nombrePorClave[$clave] = ('' + $ra.Ruta).TrimEnd('\')
    }

    $hayVivo = ($actualesPorClave.Count -gt 0)
    if ($porDestino.Count -eq 0 -and -not $hayVivo) { return $resultado.ToArray() }

    $centPorRaiz = New-Object System.Collections.Generic.HashSet[string]([System.StringComparer]::OrdinalIgnoreCase)
    foreach ($ce in @($Centinelas)) {
        if ($null -eq $ce -or -not ($ce.PSObject.Properties.Name -contains 'ruta') -or -not $ce.ruta) { continue }
        $padre = Split-Path -Path ('' + $ce.ruta) -Parent
        if ($padre) { [void]$centPorRaiz.Add($padre.TrimEnd('\')) }
    }

    # El orden lo mandan las raices activas y del nodo; las exclusivas del disco
    # (como _HISTORICO y homeUsers) se anaden detras sin perderse.
    $orden = New-Object System.Collections.Generic.List[string]
    $primerBloque = New-Object System.Collections.Generic.HashSet[string]([System.StringComparer]::OrdinalIgnoreCase)
    foreach ($k in $actualesPorClave.Keys) { [void]$primerBloque.Add($k) }
    if ($porDestino.ContainsKey('nodo')) {
        foreach ($k in $porDestino['nodo'].Keys) { [void]$primerBloque.Add($k) }
    }
    foreach ($k in ($primerBloque | Sort-Object { $nombrePorClave[$_] })) {
        [void]$orden.Add($k)
    }
    if ($porDestino.ContainsKey('disco')) {
        foreach ($k in ($porDestino['disco'].Keys | Sort-Object { $nombrePorClave[$_] })) {
            if (-not $orden.Contains($k)) { [void]$orden.Add($k) }
        }
    }

    foreach ($clave in $orden) {
        $raiz    = $nombrePorClave[$clave]
        $enNodo  = $null; $enDisco = $null; $actual = $null
        if ($porDestino.ContainsKey('nodo')  -and $porDestino['nodo'].ContainsKey($clave))  { $enNodo  = $porDestino['nodo'][$clave] }
        if ($porDestino.ContainsKey('disco') -and $porDestino['disco'].ContainsKey($clave)) { $enDisco = $porDestino['disco'][$clave] }
        if ($actualesPorClave.ContainsKey($clave)) { $actual = $actualesPorClave[$clave] }

        $esLocal = ($raiz -match '^[A-Za-z]:')
        $estadoVivo = 'Activa'
        if ($hayVivo -and $null -ne $actual -and $null -eq $enNodo) { $estadoVivo = 'Nueva' }
        elseif ($hayVivo -and $esLocal -and $null -ne $enNodo -and $null -eq $actual) { $estadoVivo = 'Ausente' }
        elseif ($hayVivo -and $esLocal -and $null -eq $actual -and $null -eq $enNodo -and $null -ne $enDisco) { $estadoVivo = 'SoloDisco' }
        elseif (-not $esLocal) { $estadoVivo = 'SoloNodo' }

        # LA COLUMNA CL refleja la clase vigente del nodo (o del disco real si
        # es nueva), y muestra "B/A" cuando nodo y disco discrepan.
        $claseNodo = $null
        if ($actual) { $claseNodo = '' + $actual.Clase }
        elseif ($enNodo) { $claseNodo = '' + $enNodo.Clase }

        $claseCelda = '?'
        if ($claseNodo) {
            $claseCelda = $claseNodo
            if ($enDisco -and ('' + $enDisco.Clase) -ne $claseNodo) {
                $claseCelda = '{0}/{1}' -f $claseNodo, $enDisco.Clase
            }
        }
        elseif ($enDisco) { $claseCelda = '-' }

        # COPIADO pertenece al ultimo pase del NODO (o al disco solo en raices
        # nacidas en el nodo). Ya no se mezcla max(nodo, disco) entre fechas
        # separadas por semanas.
        $movidos = 0
        if ($enNodo) { $movidos = [int]$enNodo.Pendientes }
        elseif ($enDisco -and -not $actual) { $movidos = [int]$enDisco.Pendientes }

        $celdas = @()
        $columna = 0
        foreach ($lado in @($enNodo, $enDisco)) {
            $columna++
            if ($columna -eq 1 -and $estadoVivo -eq 'Nueva') {
                $celdas += [pscustomobject]@{ Texto = 'NUEVA'; Tono = 'Ambar' }
                continue
            }
            if ($columna -eq 1 -and $estadoVivo -eq 'Ausente') {
                $celdas += [pscustomobject]@{ Texto = 'AUSENTE'; Tono = 'Rojo' }
                continue
            }
            if ($columna -eq 1 -and $estadoVivo -eq 'SoloDisco') {
                $celdas += [pscustomobject]@{ Texto = 'solo en disco'; Tono = 'Gris' }
                continue
            }
            if ($null -eq $lado) {
                $texto = '--'
                if ($columna -eq 1 -and -not $esLocal) { $texto = 'es el origen' }
                $celdas += [pscustomobject]@{ Texto = $texto; Tono = 'Gris' }
                continue
            }
            if ($lado.Estado -ne 'AlDia') {
                $celdas += [pscustomobject]@{ Texto = 'FALLO'; Tono = 'Rojo' }
                continue
            }
            $edad = Format-Antiguedad -Momento $lado.Momento
            [datetime] $cuando = [datetime]::MinValue
            $reciente = $false
            if ([datetime]::TryParse(('' + $lado.Momento), [ref] $cuando)) {
                $reciente = (((Get-Date) - $cuando).TotalHours -le $HorasParaAmbar)
            }
            if ($reciente) {
                $celdas += [pscustomobject]@{ Texto = 'AL DIA'; Tono = 'Verde' }
            }
            else {
                $celdas += [pscustomobject]@{ Texto = $edad; Tono = 'Ambar' }
            }
        }

        $sinCentinela = ($hayVivo -and $null -ne $actual -and $centPorRaiz.Count -gt 0 -and -not $centPorRaiz.Contains($raiz))
        $resultado.Add([pscustomobject]@{
            Etiqueta     = (Format-EtiquetaDeRaiz -Raiz $raiz)
            Ruta         = $raiz
            Clase        = $claseCelda
            Movidos      = $movidos
            Nodo         = $celdas[0]
            Disco        = $celdas[1]
            EstadoVivo   = $estadoVivo
            SinCentinela = $sinCentinela
        })
    }
    return $resultado.ToArray()
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
            @{ Eti = 'motor'; Tarea = $script:Ctx.NombreTarea },
            @{ Eti = 'icono'; Tarea = $script:Ctx.NombreIndicador })) {
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
        .PARAMETER Laboratorio
            Lo que devolvio Get-ResumenDelLaboratorio, o $null.
    #>
    [CmdletBinding()]
    [OutputType([string[]])]
    param(
        [Parameter(Mandatory)][hashtable] $Estado,
        [AllowNull()][psobject] $Disco,
        [Parameter(Mandatory)][AllowEmptyCollection()][psobject[]] $Filas,
        [AllowNull()][psobject] $Laboratorio,
        [AllowEmptyCollection()][psobject[]] $HuerfanosActuales = @(),
        [AllowEmptyCollection()][psobject[]] $ModeloTabla = @(),
        [AllowNull()][psobject] $CentinelasVivo = $null,
        [AllowNull()][psobject] $LecturaEnCurso = $null
    )

    $partes = New-Object System.Collections.Generic.List[string]

    if ($CentinelasVivo) {
        if (@($CentinelasVivo.Fallos).Count -gt 0) {
            $partes.Add(('{0} centinela(s) con fallo: no se copiara hasta revisar' -f @($CentinelasVivo.Fallos).Count))
        }
        if (@($CentinelasVivo.Renombrados).Count -gt 0) {
            $partes.Add(('{0} raiz renombrada: corre [1] para migrarla en el nodo' -f @($CentinelasVivo.Renombrados).Count))
        }
        if (@($CentinelasVivo.Desactivados).Count -gt 0) {
            $partes.Add(('{0} raiz sin prefijo NN_ (desactivada por renombre)' -f @($CentinelasVivo.Desactivados).Count))
        }
    }

    $nuevas = @($ModeloTabla | Where-Object { $_.EstadoVivo -eq 'Nueva' }).Count
    if ($nuevas -gt 0) {
        $partes.Add(('{0} raiz(es) nueva(s) sin copiar todavia ([1])' -f $nuevas))
    }
    $ausentes = @($ModeloTabla | Where-Object { $_.EstadoVivo -eq 'Ausente' }).Count
    if ($ausentes -gt 0) {
        $partes.Add(('{0} raiz(es) ausente(s) en disco (renombrada o borrada)' -f $ausentes))
    }
    $sinCent = @($ModeloTabla | Where-Object { $_.SinCentinela }).Count
    if ($sinCent -gt 0) {
        $partes.Add(('{0} raiz(es) activa(s) sin centinela' -f $sinCent))
    }

    $huerfanos = 0
    if ($PSBoundParameters.ContainsKey('HuerfanosActuales')) {
        $huerfanos = @($HuerfanosActuales | Where-Object { -not (($_.PSObject.Properties.Name -contains 'Conocido') -and $_.Conocido) }).Count
    }
    elseif ($Estado.ContainsKey('huerfanos')) {
        [void][int]::TryParse(('' + $Estado['huerfanos']), [ref] $huerfanos)
    }
    if ($huerfanos -gt 0) {
        $partes.Add(('{0} carpetas sin clasificar ([6])' -f $huerfanos))
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
    if ($LecturaEnCurso) {
        $partes.Add('lectura del laboratorio en la Pi en curso ([3] -> [7])')
    }
    elseif ($Laboratorio -and $Laboratorio.NoAlDia -gt 0) {
        $partes.Add(('laboratorio: {0} de {1} carpetas sin guardar en el nodo ([3])' -f $Laboratorio.NoAlDia, $Laboratorio.Vms))
    }

    if (-not (Get-UrlDelTestigo)) { $partes.Add('el testigo externo no esta configurado') }

    return $partes.ToArray()
}

function Resolve-EstadoDeProteccion {
    <#
        .SYNOPSIS
            Evalua el bloque PROTECCION con semantica estricta (nunca miente).
        .DESCRIPTION
            Verde SOLO si la comprobacion en vivo de centinelas corrio, 100%
            estan integros, 0 renombres pendientes, 0 desactivados y 0 volumenes
            activos sin cobertura. Ambar si falta cobertura o dato; Rojo si falla
            algun centinela. Funcion pura.
    #>
    [CmdletBinding()]
    [OutputType([psobject])]
    param(
        [AllowEmptyCollection()][psobject[]] $CentinelasConfig = @(),
        [AllowNull()][psobject] $CentinelasVivo = $null,
        [AllowNull()][hashtable] $Estado = @{},
        [AllowEmptyCollection()][psobject[]] $ModeloTabla = @()
    )

    $partes = New-Object System.Collections.Generic.List[string]
    $cambio = if ($Estado -and $Estado.ContainsKey('cambio') -and -not [string]::IsNullOrWhiteSpace('' + $Estado['cambio'])) {
        '' + $Estado['cambio']
    }
    else { '' }

    if (@($CentinelasConfig).Count -eq 0) {
        [void]$partes.Add('centinelas no configurados (0)')
        if ($cambio) { [void]$partes.Add(('delta ultima ejecucion: {0} %' -f $cambio)) }
        return [pscustomobject]@{ Tono = 'Ambar'; Regla = $true; Partes = $partes.ToArray(); Motivo = 'sin centinelas antirransomware configurados' }
    }

    $sinCent = @($ModeloTabla | Where-Object { ($_.PSObject.Properties.Name -contains 'SinCentinela') -and $_.SinCentinela }).Count

    if (-not $CentinelasVivo) {
        [void]$partes.Add(('centinelas {0} declarados (sin verificar en vivo)' -f @($CentinelasConfig).Count))
        if ($cambio) { [void]$partes.Add(('delta ultima ejecucion: {0} %' -f $cambio)) }
        return [pscustomobject]@{ Tono = 'Ambar'; Regla = $false; Partes = $partes.ToArray(); Motivo = 'centinelas sin verificar en vivo' }
    }

    $fallos = @($CentinelasVivo.Fallos).Count
    $ren = @($CentinelasVivo.Renombrados).Count
    $des = @($CentinelasVivo.Desactivados).Count
    $rev = [int]$CentinelasVivo.Revisados
    $ok = $rev - $fallos

    if ($fallos -gt 0) {
        [void]$partes.Add(('centinelas {0}/{1} ({2} con fallo)' -f $ok, $rev, $fallos))
        if ($cambio) { [void]$partes.Add(('delta ultima ejecucion: {0} %' -f $cambio)) }
        return [pscustomobject]@{ Tono = 'Rojo'; Regla = $true; Partes = $partes.ToArray(); Motivo = ('{0} centinela(s) alterado(s) o ausente(s)' -f $fallos) }
    }

    if ($ren -gt 0 -or $des -gt 0 -or $sinCent -gt 0) {
        [void]$partes.Add(('centinelas {0}/{1}' -f $ok, $rev))
        if ($ren -gt 0) { [void]$partes.Add(('{0} renombre(s) por migrar' -f $ren)) }
        if ($des -gt 0) { [void]$partes.Add(('{0} volumen(es) desactivado(s)' -f $des)) }
        if ($sinCent -gt 0) { [void]$partes.Add(('{0} volumen(es) sin centinela' -f $sinCent)) }
        if ($cambio) { [void]$partes.Add(('delta: {0} %' -f $cambio)) }
        return [pscustomobject]@{ Tono = 'Ambar'; Regla = $false; Partes = $partes.ToArray(); Motivo = 'cobertura de centinelas incompleta o renombre pendiente' }
    }

    [void]$partes.Add(('centinelas {0}/{1} integros' -f $ok, $rev))
    if ($cambio) { [void]$partes.Add(('delta ultima ejecucion: {0} %' -f $cambio)) }
    return [pscustomobject]@{ Tono = 'Verde'; Regla = $false; Partes = $partes.ToArray(); Motivo = '' }
}

function Resolve-EstadoDeIntegridad {
    <#
        .SYNOPSIS
            Evalua el bloque INTEGRIDAD (SHA-256, auditoria, semilla y ensayo DR).
        .DESCRIPTION
            Basado en NIST SP 800-34 Rev. 1 y Krogh (The DAM Book):
              Rojo   si falla SHA-256, muestreo completo o semilla DR.
              Ambar  si cualquier control falta, esta vencido (> DiasMuestreoMax)
                     o nunca se registro un ensayo real de recuperacion (DR).
              Verde  SOLO cuando los cuatro controles estan OK y vigentes.
            Funcion pura.
    #>
    [CmdletBinding()]
    [OutputType([psobject])]
    param(
        [Parameter(Mandatory)][AllowEmptyCollection()][hashtable] $Estado,
        [datetime] $Ahora = (Get-Date),
        [ValidateRange(1, 365)][int] $DiasMuestreoMax = 14
    )

    $huellasEst = '' + $Estado['huellas_estado']
    $huellasMom = '' + $Estado['huellas_momento']
    $huellasDet = '' + $Estado['huellas_detalle']
    $muestreoEst = '' + $Estado['muestreo_estado']
    $muestreoMom = '' + $Estado['muestreo_momento']
    $estructuraEst = '' + $Estado['estructura_estado']
    $estructuraDet = '' + $Estado['estructura_detalle']
    $semillaEst = '' + $Estado['semilla_estado']
    $semillaMom = '' + $Estado['semilla_momento']
    $restMom = '' + $Estado['restauracion_probada']

    $partes = New-Object System.Collections.Generic.List[string]
    $hTxt = 'SHA-256: ' + (Format-Antiguedad -Momento $huellasMom)
    if ($huellasDet) { $hTxt += ' (' + $huellasDet + ')' }
    [void]$partes.Add($hTxt)

    if ($estructuraEst -eq 'Falla' -and $estructuraDet) {
        [void]$partes.Add('estructura: ' + $estructuraDet)
    }

    [datetime] $dtMuestreo = [datetime]::MinValue
    $muestreoValido = [datetime]::TryParse($muestreoMom, [ref]$dtMuestreo)
    $muestreoVencido = (-not $muestreoValido) -or (($Ahora - $dtMuestreo).TotalDays -gt $DiasMuestreoMax)
    $mTxt = 'auditoria completa: ' + (Format-Antiguedad -Momento $muestreoMom)
    if ($muestreoEst -eq 'Falla') { $mTxt += ' (CON DIFERENCIAS)' }
    elseif ($muestreoValido -and $muestreoVencido) { $mTxt += ' (vencida)' }
    [void]$partes.Add($mTxt)

    [void]$partes.Add('semilla DR: ' + (Format-Antiguedad -Momento $semillaMom))
    $restValido = (-not [string]::IsNullOrWhiteSpace($restMom)) -and ($restMom -ne 'nunca')
    $rTxt = if ($restValido) { 'ensayo DR: ' + (Format-Antiguedad -Momento $restMom) } else { 'ensayo DR: pendiente' }
    [void]$partes.Add($rTxt)

    if ($huellasEst -eq 'Falla' -or $muestreoEst -eq 'Falla' -or $semillaEst -eq 'Falla') {
        $motF = if ($huellasEst -eq 'Falla' -or $muestreoEst -eq 'Falla') { 'discrepancia de integridad SHA-256 detectada' } else { 'semilla de recuperacion incompleta o ausente' }
        return [pscustomobject]@{ Tono = 'Rojo'; Regla = $true; Partes = $partes.ToArray(); Motivo = $motF }
    }

    $pendientes = New-Object System.Collections.Generic.List[string]
    if ($huellasEst -ne 'OK') { [void]$pendientes.Add('SHA-256 sin confirmar') }
    if ($estructuraEst -eq 'Falla') { [void]$pendientes.Add('diferencias de estructura') }
    if ($muestreoEst -ne 'OK' -or $muestreoVencido) { [void]$pendientes.Add('auditoria completa vencida o pendiente') }
    if ($semillaEst -ne 'OK') { [void]$pendientes.Add('semilla sin verificar') }
    if (-not $restValido) { [void]$pendientes.Add('ensayo DR pendiente') }

    if ($pendientes.Count -gt 0) {
        return [pscustomobject]@{ Tono = 'Ambar'; Regla = $false; Partes = $partes.ToArray(); Motivo = ($pendientes -join '; ') }
    }

    return [pscustomobject]@{ Tono = 'Verde'; Regla = $false; Partes = $partes.ToArray(); Motivo = '' }
}

function Resolve-EstadoDeServicio {
    <#
        .SYNOPSIS
            Evalua el bloque SERVICIO (tareas programadas e integridad del motor).
        .DESCRIPTION
            Verde si el programador y el indicador estan activos con las
            ventanas esperadas y el motor esta integro; Ambar ante desajuste no
            critico; Rojo si falta la tarea del motor o alguna pieza del motor.
            Acepta datos precalculados para pruebas unitarias deterministas.
    #>
    [CmdletBinding()]
    [OutputType([psobject])]
    param(
        [int] $VentanasEsperadas = 0,
        [AllowNull()][string[]] $TareasTexto = $null,
        [AllowNull()][psobject] $MotorIntacto = $null
    )

    $tareas = if ($null -ne $TareasTexto) { @($TareasTexto) } else { @(Get-EstadoDeTarea) }
    $partes = New-Object System.Collections.Generic.List[string]
    foreach ($t in $tareas) {
        if (-not [string]::IsNullOrWhiteSpace($t)) { [void]$partes.Add($t) }
    }

    $motorMal = ($tareas -join ' ') -match 'motor (NO EXISTE|DESHABILITADO)'
    $iconoMal = ($tareas -join ' ') -match 'icono (NO EXISTE|DESHABILITADO)'
    $piezasRotas = $false
    if ($MotorIntacto) {
        if (@($MotorIntacto.PiezasFallan).Count -gt 0) {
            $piezasRotas = $true
            [void]$partes.Add(('motor: {0} pieza(s) faltante(s)' -f @($MotorIntacto.PiezasFallan).Count))
        }
        else {
            [void]$partes.Add(('motor integro ({0} piezas)' -f $MotorIntacto.PiezasTotal))
        }
    }

    $desajusteVentanas = $false
    if ($VentanasEsperadas -gt 0) {
        $lineaMotor = @($tareas | Where-Object { $_ -like 'motor*' } | Select-Object -First 1)
        if ($lineaMotor.Count -gt 0 -and $lineaMotor[0] -match '(\d+)\s+ventanas') {
            if ([int]$Matches[1] -ne $VentanasEsperadas) {
                $desajusteVentanas = $true
                [void]$partes.Add(('esperadas {0} ventanas' -f $VentanasEsperadas))
            }
        }
    }

    if ($motorMal -or $piezasRotas) {
        return [pscustomobject]@{ Tono = 'Rojo'; Regla = $true; Partes = $partes.ToArray(); Motivo = 'servicio automatico o integridad del motor comprometida' }
    }
    if ($iconoMal -or $desajusteVentanas -or $tareas.Count -eq 0) {
        return [pscustomobject]@{ Tono = 'Ambar'; Regla = $false; Partes = $partes.ToArray(); Motivo = 'servicio programado degradado o indicador inactivo' }
    }
    return [pscustomobject]@{ Tono = 'Verde'; Regla = $false; Partes = $partes.ToArray(); Motivo = '' }
}

function Get-AlertasPriorizadas {
    <#
        .SYNOPSIS
            Racionaliza las incidencias activas por severidad (ANSI/ISA-18.2).
        .DESCRIPTION
            Separa fallos criticos de advertencias operativas, ordena de mayor
            a menor severidad y evita mezclar ruido rutinario con fallos reales.
            Devuelve objetos { Nivel, Mensaje, Opcion }.
    #>
    [CmdletBinding()]
    [OutputType([psobject[]])]
    param(
        [Parameter(Mandatory)][hashtable] $Estado,
        [AllowNull()][psobject] $Disco,
        [Parameter(Mandatory)][AllowEmptyCollection()][psobject[]] $Filas,
        [AllowNull()][psobject] $Laboratorio,
        [AllowEmptyCollection()][psobject[]] $HuerfanosActuales = @(),
        [AllowEmptyCollection()][psobject[]] $ModeloTabla = @(),
        [AllowNull()][psobject] $CentinelasVivo = $null,
        [AllowNull()][psobject] $LecturaEnCurso = $null,
        [AllowNull()][psobject] $Servicio = $null,
        [AllowNull()][psobject] $Integridad = $null
    )

    $criticas = New-Object System.Collections.Generic.List[psobject]
    $advertencias = New-Object System.Collections.Generic.List[psobject]

    if ($CentinelasVivo -and @($CentinelasVivo.Fallos).Count -gt 0) {
        [void]$criticas.Add([pscustomobject]@{
            Nivel = 'Critico'; Opcion = '6'
            Mensaje = ('{0} centinela(s) con fallo: respaldo detenido' -f @($CentinelasVivo.Fallos).Count)
        })
    }
    if ($Integridad -and $Integridad.Tono -eq 'Rojo') {
        [void]$criticas.Add([pscustomobject]@{
            Nivel = 'Critico'; Opcion = '1'
            Mensaje = $Integridad.Motivo
        })
    }
    if ($Servicio -and $Servicio.Tono -eq 'Rojo') {
        [void]$criticas.Add([pscustomobject]@{
            Nivel = 'Critico'; Opcion = '5'
            Mensaje = $Servicio.Motivo
        })
    }
    $ausentes = @($ModeloTabla | Where-Object { $_.EstadoVivo -eq 'Ausente' }).Count
    if ($ausentes -gt 0) {
        [void]$criticas.Add([pscustomobject]@{
            Nivel = 'Critico'; Opcion = '6'
            Mensaje = ('{0} volumen(es) ausente(s) en disco local' -f $ausentes)
        })
    }
    $conFalloNodo = @($Filas | Where-Object { $_.Estado -ne 'AlDia' -and $_.Destino -ne 'disco' }).Count
    if ($conFalloNodo -gt 0) {
        [void]$criticas.Add([pscustomobject]@{
            Nivel = 'Critico'; Opcion = '1'
            Mensaje = ('{0} volumen(es) con fallo en nodo SMB' -f $conFalloNodo)
        })
    }
    $conFalloDisco = @($Filas | Where-Object { $_.Estado -ne 'AlDia' -and $_.Destino -eq 'disco' }).Count
    if ($conFalloDisco -gt 0) {
        [void]$criticas.Add([pscustomobject]@{
            Nivel = 'Critico'; Opcion = '2'
            Mensaje = ('{0} volumen(es) con fallo en disco frio' -f $conFalloDisco)
        })
    }

    if ($CentinelasVivo -and @($CentinelasVivo.Renombrados).Count -gt 0) {
        [void]$advertencias.Add([pscustomobject]@{
            Nivel = 'Advertencia'; Opcion = '1'
            Mensaje = ('{0} volumen(es) renombrado(s): ejecutar respaldo para migrar' -f @($CentinelasVivo.Renombrados).Count)
        })
    }
    $nuevas = @($ModeloTabla | Where-Object { $_.EstadoVivo -eq 'Nueva' }).Count
    if ($nuevas -gt 0) {
        [void]$advertencias.Add([pscustomobject]@{
            Nivel = 'Advertencia'; Opcion = '1'
            Mensaje = ('{0} volumen(es) nuevo(s) pendiente(s) de primera copia' -f $nuevas)
        })
    }
    $sinCent = @($ModeloTabla | Where-Object { $_.SinCentinela }).Count
    if ($sinCent -gt 0) {
        [void]$advertencias.Add([pscustomobject]@{
            Nivel = 'Advertencia'; Opcion = '6'
            Mensaje = ('{0} volumen(es) activo(s) sin centinela' -f $sinCent)
        })
    }
    if ($LecturaEnCurso) {
        [void]$advertencias.Add([pscustomobject]@{
            Nivel = 'Advertencia'; Opcion = '3'
            Mensaje = 'Lectura de manifiestos en la Pi en curso'
        })
    }
    elseif ($Laboratorio -and $Laboratorio.NoAlDia -gt 0) {
        [void]$advertencias.Add([pscustomobject]@{
            Nivel = 'Advertencia'; Opcion = '3'
            Mensaje = ('Laboratorio VM: {0}/{1} carpetas desactualizadas en nodo' -f $Laboratorio.NoAlDia, $Laboratorio.Vms)
        })
    }

    $huerfanos = 0
    if ($PSBoundParameters.ContainsKey('HuerfanosActuales')) {
        $huerfanos = @($HuerfanosActuales | Where-Object { -not (($_.PSObject.Properties.Name -contains 'Conocido') -and $_.Conocido) }).Count
    }
    elseif ($Estado.ContainsKey('huerfanos')) {
        [void][int]::TryParse(('' + $Estado['huerfanos']), [ref]$huerfanos)
    }
    if ($huerfanos -gt 0) {
        [void]$advertencias.Add([pscustomobject]@{
            Nivel = 'Advertencia'; Opcion = '6'
            Mensaje = ('{0} carpeta(s) sin clasificar en contenedores' -f $huerfanos)
        })
    }

    # Disco frio fuera de linea solo es advertencia si nunca se copio o excede 14 dias.
    if (-not $Disco) {
        [datetime] $dtDisco = [datetime]::MinValue
        $discoReciente = [datetime]::TryParse(('' + $Estado['disco_momento']), [ref]$dtDisco) -and (((Get-Date) - $dtDisco).TotalDays -le 14)
        if (-not $discoReciente) {
            [void]$advertencias.Add([pscustomobject]@{
                Nivel = 'Advertencia'; Opcion = '2'
                Mensaje = 'Copia fria fuera de linea vencida o sin ejecutar (conectar NAS-COLD-01)'
            })
        }
    }

    if (-not (Get-UrlDelTestigo)) {
        [void]$advertencias.Add([pscustomobject]@{
            Nivel = 'Advertencia'; Opcion = '5'
            Mensaje = 'Testigo externo (Healthchecks) sin configurar'
        })
    }

    return (@($criticas.ToArray()) + @($advertencias.ToArray()))
}

function Resolve-VeredictoDeConsola {
    <#
        .SYNOPSIS
            Concilia el estado del indicador con las comprobaciones en vivo.
        .DESCRIPTION
            Impide que el encabezado muestre NOMINAL (verde) cuando un control
            en vivo (centinelas, integridad SHA-256 o servicio) reporta fallo
            o degradacion real. Funcion pura.
    #>
    [CmdletBinding()]
    [OutputType([psobject])]
    param(
        [Parameter(Mandatory)][string] $EstadoIndicador,
        [AllowEmptyString()][string] $DetalleIndicador = '',
        [AllowNull()][psobject] $Proteccion = $null,
        [AllowNull()][psobject] $Integridad = $null,
        [AllowNull()][psobject] $Servicio = $null
    )

    if ($EstadoIndicador -eq 'Copiando') {
        return [pscustomobject]@{ Estado = 'Copiando'; Detalle = $DetalleIndicador }
    }
    if ($Proteccion -and $Proteccion.Tono -eq 'Rojo') {
        return [pscustomobject]@{ Estado = 'Falla'; Detalle = ('Proteccion comprometida: ' + $Proteccion.Motivo) }
    }
    if ($Integridad -and $Integridad.Tono -eq 'Rojo') {
        return [pscustomobject]@{ Estado = 'Falla'; Detalle = ('Integridad comprometida: ' + $Integridad.Motivo) }
    }
    if ($Servicio -and $Servicio.Tono -eq 'Rojo') {
        return [pscustomobject]@{ Estado = 'Falla'; Detalle = ('Servicio comprometido: ' + $Servicio.Motivo) }
    }
    if ($EstadoIndicador -eq 'Falla') {
        return [pscustomobject]@{ Estado = 'Falla'; Detalle = $DetalleIndicador }
    }
    if ($EstadoIndicador -eq 'Protegido') {
        if ($Proteccion -and $Proteccion.Tono -eq 'Ambar') {
            return [pscustomobject]@{ Estado = 'Atencion'; Detalle = ('Proteccion: ' + $Proteccion.Motivo) }
        }
        if ($Servicio -and $Servicio.Tono -eq 'Ambar') {
            return [pscustomobject]@{ Estado = 'Atencion'; Detalle = ('Servicio: ' + $Servicio.Motivo) }
        }
    }
    return [pscustomobject]@{ Estado = $EstadoIndicador; Detalle = $DetalleIndicador }
}

function Show-Ventana {
    <#
        .SYNOPSIS
            Pinta la ventana de estado y el menu adaptados al ancho real de la terminal.
        .DESCRIPTION
            Calcula geometria responsiva (Compacto / Estandar / Amplio) en cada
            redibujo, muestra telemetria viva de PROGRESO.json y usa semantica
            estricta de colores en PROTECCION, INTEGRIDAD, SERVICIO y ALERTAS.
        .PARAMETER Configuracion
            El objeto de configuracion completo.
        .PARAMETER Datos
            Opcional: resultado ya resuelto de Get-DatosDeVentana.
    #>
    [CmdletBinding()]
    [OutputType([void])]
    param(
        [Parameter(Mandatory)][psobject] $Configuracion,
        [AllowNull()][psobject] $Datos = $null
    )

    $d       = if ($Datos) { $Datos } else { Get-DatosDeVentana -Configuracion $Configuracion }
    $geo     = Get-GeometriaDeConsola
    $script:AnchoTablero = $geo.Ancho
    $p       = $script:Paleta
    $u       = $script:Capacidades.Unicode
    $colorEs = Get-ColorDeEstado -Estado $d.Estado -Paleta $p
    $simbolo = Get-Simbolo -Estado $d.Estado -Unicode $u
    $etEstado = Get-EtiquetaDeEstado -Estado $d.Estado
    $ancho   = $geo.Ancho

    # --- Cabecera: identificacion, reloj vivo y estado real a la derecha -----
    $horaAct = if ($d.PSObject.Properties.Name -contains 'Actualizado' -and $d.Actualizado) { $d.Actualizado } else { (Get-Date).ToString('HH:mm:ss') }
    $izq = 'CONSOLA DE RESPALDO | {0}  ({1})' -f $d.Equipo, $horaAct
    $sepSimbolo = if ($u) { '' } else { ' ' }
    $der = '{0}{1}{2}' -f $simbolo, $sepSimbolo, $etEstado
    $hueco = $ancho - $izq.Length - $der.Length
    if ($hueco -lt 1) {
        $izq = 'RESPALDO | {0}' -f $d.Equipo
        $hueco = [Math]::Max(1, $ancho - $izq.Length - $der.Length)
    }
    Write-Linea ('  {0}{1}{2}{3}{4}{5}{6}' -f `
            ($p.Fuerte + $p.Titulo), $izq, $p.Fin, (' ' * $hueco), $colorEs, $der, $p.Fin)
    Write-Linea (Get-Regla -Paleta $p -Unicode $u -Ancho $ancho)
    foreach ($trozo in @(Split-TextoEnLineas -Texto $d.Detalle -Ancho $ancho)) {
        Write-Linea ('  {0}{1}{2}' -f $p.Tenue, $trozo, $p.Fin)
    }
    Write-Linea ' '

    # --- Destinos: nodo SMB, disco frio y laboratorio VM ---------------------
    $destinos = @($d.Nodo, $d.Disco)
    if ($d.PSObject.Properties.Name -contains 'Laboratorio' -and $d.Laboratorio) { $destinos += $d.Laboratorio }
    foreach ($destino in $destinos) {
        Write-Linea ('  {0} {1} {2} {3}' -f `
            (Format-Celda -Texto $destino.Nombre -Ancho ($geo.DestNombre - 1) -Paleta $p -Color $p.Etiqueta), `
            (Format-Celda -Texto $destino.Situacion -Ancho ($geo.DestSituacion - 1) -Paleta $p -Color $p[$destino.TonoSituacion]), `
            (Format-Celda -Texto $destino.Libres -Ancho ($geo.DestCapacidad - 1) -Paleta $p -Color $p.Tenue), `
            (Format-Celda -Texto ('ult. sync: {0}' -f $destino.UltimaCopia) `
                    -Ancho $geo.DestCopia -Paleta $p -Color $p[$destino.TonoCopia]))
    }

    # --- Barra viva si existe operacion activa en PROGRESO.json --------------
    if ($d.PSObject.Properties.Name -contains 'ProgresoActivo' -and $d.ProgresoActivo -and $d.ProgresoActivo.Activo) {
        $prog = $d.ProgresoActivo
        $barra = Format-BarraDeProgreso -Porcentaje $prog.Porcentaje -Ancho 16 -Unicode $u
        $resProg = Format-ProgresoDeOperacion -Progreso $prog
        $txtVivo = Limit-Texto -Texto $resProg -Maximo ([Math]::Max(20, $ancho - 30))
        Write-Linea ('  {0}EN CURSO   {1}{2}{3}  {4}{5}{3}' -f ($p.Fuerte + $p.Verde), $p.Verde, $barra, $p.Fin, $p.Valor, $txtVivo)
    }
    Write-Linea ' '

    # --- Tabla de volumenes / raices -----------------------------------------
    Write-Linea ('  {0}{1}{2}{3}{4}' -f `
        (Format-Celda -Texto 'VOLUMEN' -Ancho $geo.ColRaiz -Paleta $p -Color $p.Etiqueta), `
        (Format-Celda -Texto 'POL' -Ancho $geo.ColPol -Paleta $p -Color $p.Etiqueta), `
        (Format-Celda -Texto 'DELTA ' -Ancho $geo.ColDelta -Paleta $p -Color $p.Etiqueta -Derecha), `
        (Format-Celda -Texto 'NODO SMB' -Ancho $geo.ColNodo -Paleta $p -Color $p.Etiqueta), `
        (Format-Celda -Texto 'DISCO FRIO' -Ancho $geo.ColDisco -Paleta $p -Color $p.Etiqueta))
    Write-Linea (Get-Regla -Paleta $p -Unicode $u -Ancho $ancho)
    Write-CuerpoDeTabla -Filas $d.Filas -Paleta $p -HorasParaAmbar $d.HorasVerde `
        -RaicesActuales $d.RaicesActuales -Centinelas $d.Centinelas -Geometria $geo
    Write-Linea ' '

    # --- Panel de control con color semantico estricto -----------------------
    Write-LineaDeSumario -Etiqueta 'PROTECCION' -Paleta $p -Partes $d.Proteccion.Partes `
        -Color $p[$d.Proteccion.Tono] -Avisar:([bool]$d.Proteccion.Regla) -Ancho $ancho -AnchoEtiqueta $geo.ColEtiquetaSumario
    Write-LineaDeSumario -Etiqueta 'INTEGRIDAD' -Paleta $p -Partes $d.Integridad.Partes `
        -Color $p[$d.Integridad.Tono] -Avisar:([bool]$d.Integridad.Regla) -Ancho $ancho -AnchoEtiqueta $geo.ColEtiquetaSumario
    Write-LineaDeSumario -Etiqueta 'SERVICIO' -Paleta $p -Partes $d.Servicio.Partes `
        -Color $p[$d.Servicio.Tono] -Avisar:([bool]$d.Servicio.Regla) -Ancho $ancho -AnchoEtiqueta $geo.ColEtiquetaSumario

    if (@($d.Alertas).Count -gt 0) {
        $vis = @($d.Alertas | Select-Object -First 3)
        $hayCrit = @($vis | Where-Object { $_.Nivel -eq 'Critico' }).Count -gt 0
        $colorAl = if ($hayCrit) { $p.Rojo } else { $p.Ambar }
        $txtAl = New-Object System.Collections.Generic.List[string]
        foreach ($al in $vis) {
            $pref = if ($al.Opcion) { '[{0}] ' -f $al.Opcion } else { '' }
            [void]$txtAl.Add($pref + $al.Mensaje)
        }
        $extra = @($d.Alertas).Count - $vis.Count
        if ($extra -gt 0) { [void]$txtAl.Add(('+{0} mas en [6] Diagnostico' -f $extra)) }
        Write-LineaDeSumario -Etiqueta 'ALERTAS' -Paleta $p -Partes $txtAl.ToArray() `
            -Color $colorAl -Avisar -Ancho $ancho -AnchoEtiqueta $geo.ColEtiquetaSumario
    }

    # --- Menu consolidado [1]-[6] --------------------------------------------
    $menu = Get-MenuDelTablero
    Write-Linea ' '
    Write-Linea (Get-Regla -Paleta $p -Unicode $u -Ancho $ancho)
    if ($geo.ColumnasMenu -eq 1) {
        Write-Linea ('  {0}OPERACIONES Y SISTEMA{1}' -f ($p.Fuerte + $p.Titulo), $p.Fin)
        foreach ($item in $menu.Items) {
            Write-Linea ('  {0}' -f (Format-Celda -Texto ('[{0}] {1}' -f $item.Tecla, $item.Etiqueta) -Ancho $ancho -Paleta $p -Color $p.Valor))
        }
    }
    else {
        $c1 = $geo.AnchoColMenu
        $c2 = [Math]::Max(24, $ancho - $c1)
        Write-Linea ('  {0}{1}{2}{3}' -f ($p.Fuerte + $p.Titulo), `
                $menu.Columnas[0].PadRight($c1), $menu.Columnas[1], $p.Fin)
        foreach ($fila in $menu.Filas) {
            Write-Linea ('  {0}{1}' -f `
                (Format-Celda -Texto $fila[0] -Ancho $c1 -Paleta $p -Color $p.Valor), `
                (Format-Celda -Texto $fila[1] -Ancho $c2 -Paleta $p -Color $p.Valor))
        }
    }
    foreach ($lNota in @(Split-TextoEnLineas -Texto $menu.Nota -Ancho $ancho)) {
        Write-Linea ('  {0}{1}{2}' -f $p.Tenue, $lNota, $p.Fin)
    }
    Write-Linea ' '
}

function Get-DatosDeVentana {
    <#
        .SYNOPSIS
            Todo lo que ensena la ventana de estado, ya decidido. No pinta nada.
        .DESCRIPTION
            LAS DOS VENTANAS BEBEN DE AQUI (ADR-0097): la de siempre y la de
            Spectre (panel.ps1). Cada regla -de que color va la ultima copia,
            que cuenta como pendiente- vive en un solo sitio, y las dos formas
            de pintar solo traducen TONOS a su paleta.

            NO ESCANEA NADA. La tabla sale de RAICES.tsv, que escribio el motor
            cuando de verdad midio. Un tablero que tarda minutos en abrirse no
            se abre nunca.
        .PARAMETER Configuracion
            El objeto de configuracion completo.
    #>
    [CmdletBinding()]
    [OutputType([psobject])]
    param(
        [Parameter(Mandatory)][psobject] $Configuracion
    )

    $carpetaEstado = Get-CarpetaDeEstado -Configuracion $Configuracion
    $v      = Get-EstadoDelIndicador
    $estado = Read-EstadoRespaldo -Carpeta $carpetaEstado
    $filas  = @(Read-EstadoPorRaiz -Carpeta $carpetaEstado)

    # CRUCE EN VIVO CON EL DISCO LOCAL (<15 ms: 1 nivel en 5 contenedores).
    # Nunca toca el nodo; asi una raiz NN_* nueva o renombrada se ve al instante.
    $raicesActuales = @()
    $huerfanosActuales = @()
    $scanOk = $false
    try {
        $raicesActuales = @(Get-RaicesDeRespaldo -Configuracion $Configuracion)
        $huerfanosActuales = @(Get-HuerfanosDeRespaldo -Configuracion $Configuracion)
        $scanOk = $true
    }
    catch {
        Write-Verbose "No se pudo consultar el disco local para el tablero: $($_.Exception.Message)"
    }

    $centinelasConfig = @($Configuracion.centinelas)
    $centVivo = $null
    try {
        $centVivo = Test-Centinela -Centinelas $centinelasConfig -Contenedores @($Configuracion.contenedores)
    }
    catch {
        Write-Verbose "No se pudieron comprobar centinelas en vivo: $($_.Exception.Message)"
    }

    $unc = '' + $Configuracion.destinos.nodo.unc
    # Test-Path directo y no Test-DestinoNodo: pintar la ventana no debe escribir
    # lineas de ERROR en el registro ni esperar reintentos de medio minuto.
    $nodoVivo = [bool](Test-Path -LiteralPath $unc -ErrorAction SilentlyContinue)
    $libresNodo = $null
    if ($nodoVivo) { $libresNodo = Get-EspacioLibre -Ruta $unc }

    # "ULT. COPIA" SALE DE `copia_momento`, NO DE `momento`. Son cosas distintas
    # y confundirlas produjo la peor mentira del tablero: el 04/09 esta linea
    # decia "ult. copia hoy 20:47" de una corrida que aborto en el freno sin
    # copiar un solo archivo. `momento` es cuando corrio; `copia_momento` es
    # cuando copio bien, y el motor solo lo escribe si la corrida termino en
    # Protegido.
    #
    # SIN LA CLAVE, "nunca" -- y en ambar. Un ESTADO.txt anterior al 08/09 no la
    # tiene, y eso NO se rellena con `momento` para que la linea no vuelva a
    # afirmar una copia que nadie puede sostener. Que diga "nunca" y se corrija
    # sola en la primera corrida buena es preferible a que mienta hoy.
    $ultimaCopia = 'nunca'
    $tonoCopia   = 'Ambar'
    if ($estado.ContainsKey('copia_momento')) {
        $ultimaCopia = Format-Antiguedad -Momento ('' + $estado['copia_momento'])
        if ($ultimaCopia -ne 'nunca') { $tonoCopia = 'Valor' }
    }

    # El disco se conecta A PETICION (seccion 6.3): no hay calendario y es
    # deliberado. Que no este no es una falla, asi que no se pinta en rojo.
    $disco = Get-DiscoFrio -Configuracion $Configuracion 2>$null
    $libresDisco = $null
    if ($disco) { $libresDisco = Get-EspacioLibre -Ruta $disco.Raiz }
    $tonoDisco = 'Valor'
    if (('' + $estado['disco_estado']) -eq 'Atencion') { $tonoDisco = 'Ambar' }
    elseif (('' + $estado['disco_estado']) -eq 'Falla') { $tonoDisco = 'Rojo' }

    $horasVerde = 12
    $ventanasEsp = 0
    try {
        $cadInfo = Get-CadenciaDeCorrida -Configuracion $Configuracion
        $horasVerde = $cadInfo.HuecoNominalMaximoHoras
        $ventanasEsp = [int]$cadInfo.CorridasPorDia
    }
    catch { Write-Verbose "Cadencia ilegible; el corte del verde se queda en $horasVerde h: $($_.Exception.Message)" }

    $lecturaLab = Read-LecturaDelLaboratorio -Carpeta $carpetaEstado
    $progresoActivo = Read-ProgresoDeOperacion -Carpeta $carpetaEstado
    $laboratorio = Get-DestinoDelLaboratorio -Estado $estado `
        -Marca (Get-MarcaDeCorrida -Carpeta $carpetaEstado -Archivo 'LABORATORIO.lock') `
        -Progreso (Read-ProgresoDelLaboratorio -Carpeta $carpetaEstado) `
        -LecturaEnCurso $lecturaLab

    $modeloTabla = @(Get-FilasDeTabla -Filas $filas -HorasParaAmbar $horasVerde `
            -RaicesActuales $raicesActuales -Centinelas $centinelasConfig)

    $proteccion = Resolve-EstadoDeProteccion -CentinelasConfig $centinelasConfig -CentinelasVivo $centVivo `
        -Estado $estado -ModeloTabla $modeloTabla
    $integridad = Resolve-EstadoDeIntegridad -Estado $estado
    $motorSalud = $null
    try { $motorSalud = Test-MotorIntacto -Tareas @() } catch { $motorSalud = $null }
    $servicio = Resolve-EstadoDeServicio -VentanasEsperadas $ventanasEsp -MotorIntacto $motorSalud

    $resumenLab = Get-ResumenDelLaboratorio -Configuracion $Configuracion -Estado $estado
    $argsPendiente = @{
        Estado         = $estado
        Disco          = $disco
        Filas          = $filas
        Laboratorio    = $resumenLab
        ModeloTabla    = $modeloTabla
        CentinelasVivo = $centVivo
        LecturaEnCurso = $lecturaLab
    }
    if ($scanOk) { $argsPendiente['HuerfanosActuales'] = $huerfanosActuales }
    $pendienteLegado = @(Get-ResumenPendiente @argsPendiente)
    $alertas = @(Get-AlertasPriorizadas @argsPendiente -Servicio $servicio -Integridad $integridad)

    $veredicto = Resolve-VeredictoDeConsola -EstadoIndicador ('' + $v.Estado) -DetalleIndicador ('' + $v.Detalle) `
        -Proteccion $proteccion -Integridad $integridad -Servicio $servicio

    return [pscustomobject]@{
        Equipo         = '' + $Configuracion.destinos.nodo.prefijoEquipo
        Estado         = '' + $veredicto.Estado
        Detalle        = '' + $veredicto.Detalle
        Actualizado    = (Get-Date).ToString('HH:mm:ss')
        ProgresoActivo = $progresoActivo
        LecturaActiva  = ($null -ne $lecturaLab)
        Nodo           = [pscustomobject]@{
            Nombre        = 'NODO SMB'
            Situacion     = $(if ($nodoVivo) { 'En linea' } else { 'FUERA DE LINEA' })
            TonoSituacion = $(if ($nodoVivo) { 'Verde' } else { 'Rojo' })
            Libres        = (Format-Espacio -Bytes $libresNodo)
            UltimaCopia   = $ultimaCopia
            TonoCopia     = $tonoCopia
        }
        Disco          = [pscustomobject]@{
            Nombre        = 'DISCO FRIO'
            Situacion     = $(if ($disco) { 'Montado' } else { 'Desconectado' })
            TonoSituacion = $(if ($disco) { 'Verde' } else { 'Gris' })
            Libres        = (Format-Espacio -Bytes $libresDisco)
            UltimaCopia   = (Format-Antiguedad -Momento ('' + $estado['disco_momento']))
            TonoCopia     = $tonoDisco
        }
        Laboratorio    = $laboratorio
        Filas          = $filas
        RaicesActuales = $raicesActuales
        Centinelas     = $centinelasConfig
        HorasVerde     = $horasVerde
        Proteccion     = $proteccion
        Integridad     = $integridad
        Servicio       = $servicio
        Alertas        = $alertas
        # Claves heredadas conservadas para compatibilidad de pruebas/consumidores.
        Seguridad      = $proteccion.Partes
        Comprobado     = $integridad.Partes
        TonoComprobado = $integridad.Tono
        Automatismo    = $servicio.Partes
        Pendiente      = $pendienteLegado
    }
}

function Get-DestinoDelLaboratorio {
    <#
        .SYNOPSIS
            La fila LAB VMWARE de la ventana, ya decidida. Funcion pura.
        .DESCRIPTION
            Seccion 5.2: el laboratorio se guarda en el nodo a peticion y fuera
            del motor. Hasta el 2026-10-09 la ventana no lo nombraba en ningun
            sitio -solo raices de C:- aunque es el mayor volumen que viaja.

            QUE SE ENSENA, de peor a mejor:
              marca viva                         guardando    verde
              marca muerta, o Copiando sin marca CORTADO      rojo
              el ultimo guardado fallo           FALLO        rojo
              copiado, falta el cotejo en la Pi  sin cotejar  ambar
              copiado y cotejado                 cotejado     verde
              nunca se anoto                     a peticion   gris

            CORTADO ES ROJO porque el laboratorio es un espejo: un guardado
            interrumpido deja a medias la copia del nodo de la VM que iba
            subiendo.

            "ULT. COPIA" SALE DE laboratorio_copia Y NO DE laboratorio_momento:
            la segunda se escribe al empezar, y pintarla seria anunciar una
            copia que todavia no termino -el error del 04/09-.
        .PARAMETER Estado
            Lo que devuelve Read-EstadoRespaldo.
        .PARAMETER Marca
            Lo que devuelve Get-MarcaDeCorrida -Archivo LABORATORIO.lock.
    #>
    [CmdletBinding()]
    [OutputType([psobject])]
    param(
        [Parameter(Mandatory)][AllowEmptyCollection()][hashtable] $Estado,
        [Parameter(Mandatory)][psobject] $Marca,
        [AllowNull()][psobject] $Progreso = $null,
        [AllowNull()][psobject] $LecturaEnCurso = $null
    )

    $ultimo = '' + $Estado['laboratorio_estado']
    $metodo = '' + $Estado['laboratorio_metodo']
    $situacion = 'a peticion'
    $tono = 'Gris'
    $libres = '' + $Estado['laboratorio_tamano']
    if ($Marca.Existe -and -not $Marca.Vieja) {
        $situacion = 'guardando'; $tono = 'Verde'
        if ($Progreso -and $Progreso.Reciente) {
            $libres = '{0}% {1}MB/s {2}' -f $Progreso.Porcentaje, $Progreso.MbPorSegundo, $Progreso.Vm
        }
        elseif ($metodo -eq 'foto-vss') { $libres = 'foto, {0} min' -f $Marca.Minutos }
        else { $libres = 'lleva {0} min' -f $Marca.Minutos }
    }
    elseif ($LecturaEnCurso) {
        $situacion = 'leyendo Pi'; $tono = 'Ambar'
        $libres = '{0}m ({1} arch.)' -f $LecturaEnCurso.Minutos, $LecturaEnCurso.TotalArchivos
    }
    elseif ($Marca.Existe -or $ultimo -eq 'Copiando') { $situacion = 'CORTADO'; $tono = 'Rojo' }
    elseif ($ultimo -eq 'Falla') { $situacion = 'FALLO'; $tono = 'Rojo' }
    elseif ($ultimo -eq 'Atencion') { $situacion = 'sin cotejar'; $tono = 'Ambar' }
    elseif ($ultimo -eq 'Protegido') { $situacion = 'cotejado'; $tono = 'Verde' }

    $ultimaCopia = Format-Antiguedad -Momento ('' + $Estado['laboratorio_copia'])
    return [pscustomobject]@{
        Nombre        = 'LAB VMWARE'
        Situacion     = $situacion
        TonoSituacion = $tono
        Libres        = $libres
        UltimaCopia   = $ultimaCopia
        TonoCopia     = $(if ($ultimaCopia -eq 'nunca') { 'Ambar' } else { 'Valor' })
    }
}

function Get-MenuDelTablero {
    <#
        .SYNOPSIS
            Menu consolidado en 6 subvistas operativas numeradas ([1]-[6]), en dos
            columnas equilibradas y lista secuencial para modo compacto.
        .DESCRIPTION
            Agrupa simulaciones, ejecuciones y verificaciones dentro de su
            propio dominio operativo (estilo consola de appliance), con opciones
            estrictamente numericas en orden y sin sangrias manuales ni huecos.
            Los parametros de proteccion siguen declarados en respaldo.jsonc.
    #>
    [CmdletBinding()]
    [OutputType([psobject])]
    param()

    $items = @(
        [pscustomobject]@{ Tecla = '1'; Etiqueta = 'Respaldo al nodo SMB';         Grupo = 'OPERACIONES' }
        [pscustomobject]@{ Tecla = '2'; Etiqueta = 'Copia fria (NAS-COLD-01)';     Grupo = 'OPERACIONES' }
        [pscustomobject]@{ Tecla = '3'; Etiqueta = 'Laboratorio VM (VMware_LAB)';  Grupo = 'OPERACIONES' }
        [pscustomobject]@{ Tecla = '4'; Etiqueta = 'Recuperacion (_SEMILLA)';      Grupo = 'SISTEMA' }
        [pscustomobject]@{ Tecla = '5'; Etiqueta = 'Programacion y servicio';      Grupo = 'SISTEMA' }
        [pscustomobject]@{ Tecla = '6'; Etiqueta = 'Diagnostico e inventario';     Grupo = 'SISTEMA' }
    )
    return [pscustomobject]@{
        Columnas = @('OPERACIONES', 'MANTENIMIENTO Y SISTEMA')
        Filas    = @(
            , @('[1] Respaldo al nodo SMB',        '[4] Recuperacion (_SEMILLA)')
            , @('[2] Copia fria (NAS-COLD-01)',    '[5] Programacion y servicio')
            , @('[3] Laboratorio VM (VMware_LAB)', '[6] Diagnostico e inventario')
        )
        Items    = $items
        Nota     = '[Enter] Refrescar  [0] Salir  |  Config: 3-Config/respaldo.jsonc'
    }
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
            -Accion 'Ejecuta [1] -> [2] (Respaldo al nodo SMB) y vuelve a cotejar. Si repite, el problema no es la copia sino el origen o el disco del nodo.'
        return
    }

    if ($faltan -gt 0 -or $sobran -gt 0) {
        Write-Veredicto -Nivel 'ATENCION' -Frase (
            'El contenido coincide en los {0} archivos leidos, pero la estructura no: faltan {1} en el nodo y sobran {2}.' -f $leidos, $faltan, $sobran) `
            -Accion 'Es lo esperable si hubo cambios desde la ultima corrida. Ejecuta [1] -> [2] para ponerlos al dia.'
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
            @{ Eti = 'motor'; Tarea = $script:Ctx.NombreTarea },
            @{ Eti = 'icono'; Tarea = $script:Ctx.NombreIndicador })) {
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
        -Accion 'Si falta un archivo, revisa la cuarentena del antivirus. Si falta una tarea, re-registrala desde [5] -> [1] -> [6].'
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

    if (-not $Revision) {
        Write-Veredicto -Nivel 'SIN DATOS' -Frase 'El disco frio no esta conectado: no se reviso nada.' `
            -Accion 'Conecta el disco frio USB y pulsa [1] para reintentar.'
        return
    }

    $revisadas = @($Revision.Revisadas).Count
    $saltadas = @($Revision.Saltadas)
    $rotos = @($Revision.EnVuelo)

    Write-Linea ('   Raices revisadas: {0}' -f $revisadas)
    foreach ($s in $saltadas) {
        Write-Warning ('NO REVISADO ({0}): {1}' -f $s.Lado, $s.Motivo)
    }

    if ($rotos.Count -eq 0 -and $saltadas.Count -eq 0) {
        Write-Veredicto -Nivel 'OK' -Frase ('{0} raices revisadas en el disco frio: 0 archivos a medias. El disco esta cuadrado.' -f $revisadas)
        return
    }
    if ($rotos.Count -eq 0) {
        Write-Veredicto -Nivel 'ATENCION' -Frase ('{0} raices revisadas sin archivos a medias, pero {1} no se pudieron mirar.' -f $revisadas, $saltadas.Count) `
            -Accion 'No equivale a "limpio": revisa los avisos de arriba (por ejemplo si el nodo no respondio) y repite [2] -> [3].'
        return
    }

    Write-Linea ''
    Show-Texto -Objeto ($rotos | Select-Object Motivo, TamanoOrigen, TamanoDestino, Relativa)
    Write-Linea ''
    Write-Veredicto -Nivel 'FALLO' -Frase ('{0} archivo(s) a medias de una copia cortada en el disco frio ({1} raices revisadas).' -f $rotos.Count, $revisadas) `
        -Accion 'Reparalos con Repair-ArchivosEnVuelo (2-Nucleo/comun.ps1): sobrescribe solo esos archivos y nunca borra.'
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

    $unc = $Configuracion.destinos.nodo.unc.TrimEnd('\')
    $raizSemilla = '{0}\{1}\{2}\_SEMILLA' -f $unc, $Configuracion.destinos.nodo.raiz, $Configuracion.destinos.nodo.prefijoEquipo

    Write-Linea ('   Semilla en: {0}' -f $raizSemilla)
    if (-not (Test-Path -LiteralPath $raizSemilla -PathType Container)) {
        Write-EstadoDeComprobacion -Tipo 'semilla' -Correcto $false `
            -Detalle 'la semilla no esta en el nodo' -Confirm:$false
        Write-Veredicto -Nivel 'FALLO' -Frase 'La semilla no esta en el nodo (_SEMILLA no encontrada).' `
            -Accion 'Regenerala ahora con [4] -> [2] del tablero.'
        return
    }

    $semillaOk = $false
    $lineasGuion = 0
    $archivos = @(Get-ChildItem -LiteralPath $raizSemilla -File -Force -ErrorAction SilentlyContinue)
    Write-Linea ''
    Show-Texto -Objeto ($archivos | Select-Object Name, Length, LastWriteTime) -Vacio 'La carpeta de la semilla esta VACIA.'
    Write-Linea ''

    $guion = Join-Path $raizSemilla 'RESTAURAR.ps1'
    if (Test-Path -LiteralPath $guion -PathType Leaf) {
        # SE LEE DE VERDAD, no se comprueba solo que exista: un archivo de cero
        # bytes existe igual de bien que uno bueno.
        $texto = Get-Content -LiteralPath $guion -Raw -Encoding UTF8 -ErrorAction SilentlyContinue
        if (-not [string]::IsNullOrWhiteSpace($texto)) {
            $lineasGuion = @($texto -split "`n").Count
            $semillaOk = $true
        }
    }

    # SE GUARDA, no solo se imprime. Sin esto la comprobacion moria al cerrar la
    # ventana y el tablero no podia decir cuando fue la ultima vez.
    Write-EstadoDeComprobacion -Tipo 'semilla' -Correcto $semillaOk `
        -Detalle $(if ($semillaOk) { '{0} archivos, RESTAURAR.ps1 se lee' -f $archivos.Count }
            else { 'la semilla no esta completa' }) -Confirm:$false

    if (-not $semillaOk) {
        Write-Veredicto -Nivel 'FALLO' -Frase ('La semilla en el nodo esta incompleta ({0} archivo(s), falta o esta vacio RESTAURAR.ps1).' -f $archivos.Count) `
            -Accion 'Regenerala con [4] -> [2] del tablero.'
        return
    }

    $estado = Read-EstadoRespaldo
    if ($estado.ContainsKey('restauracion_probada') -and $estado['restauracion_probada']) {
        Write-Veredicto -Nivel 'OK' -Frase ('Semilla legible en el nodo: {0} archivos, RESTAURAR.ps1 con {1} lineas. Ultima restauracion real probada: {2}.' -f
            $archivos.Count, $lineasGuion, $estado['restauracion_probada'])
    }
    else {
        Write-Veredicto -Nivel 'ATENCION' -Frase ('Semilla legible en el nodo ({0} archivos, RESTAURAR.ps1 con {1} lineas), pero NUNCA se ha probado una restauracion real en maquina limpia.' -f
            $archivos.Count, $lineasGuion) `
            -Accion 'Criterio 11 abierto: prueba RESTAURAR.ps1 en una VM limpia y anota la fecha (aprovisionar maquinas queda fuera de alcance, seccion 16).'
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
    $tarea = Get-ScheduledTask -TaskName $script:Ctx.NombreTarea -ErrorAction SilentlyContinue
    $disparadores = 0
    if ($tarea) { $disparadores = @($tarea.Triggers).Count }

    $c = $hecho.Cadencia
    if ($disparadores -ne $c.CorridasPorDia) {
        Write-Veredicto -Nivel 'ATENCION' -Frase (
            'El horario se guardo ({0}), pero la tarea quedo con {1} disparadores y deberian ser {2}.' -f `
                $c.Resumen, $disparadores, $c.CorridasPorDia) `
            -Accion 'Vuelve a entrar a ajustes y pulsa [6] para re-registrar la tarea.'
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
        Clear-PantallaInteractiva
        Write-CabeceraDeVista -Titulo '[5] > [1] AJUSTES DEL HORARIO'
        $cadencia = $null
        try { $cadencia = Get-CadenciaDeCorrida -Configuracion $vigente }
        catch {
            Write-Veredicto -Nivel 'FALLO' -Frase ('No se puede leer el horario: {0}' -f $_.Exception.Message) `
                -Accion 'Hay que arreglar 3-Config/respaldo.jsonc a mano antes de poder ajustarlo desde aqui.'
            [void](Wait-CierreDeVista)
            return $vigente
        }

        Write-Anuncio -Que 'Ajustes del horario' `
            -Detalle 'Cambia CUANDO corre el respaldo. Escribe en 3-Config/respaldo.jsonc y vuelve a registrar la tarea.'
        Show-HorarioActual -Cadencia $cadencia

        $siguiente = Get-ProximaVentanaDeCorrida -Cadencia $cadencia -NombreTarea $script:Ctx.NombreTarea
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
        Write-Linea ('   {0}[1]{1} Mover una ventana          {0}[4]{1} Anadir una corrida' -f ($p.Fuerte + $p.Valor), $p.Fin)
        Write-Linea ('   {0}[2]{1} Cambiar su duracion        {0}[5]{1} Quitar una corrida' -f ($p.Fuerte + $p.Valor), $p.Fin)
        Write-Linea ('   {0}[3]{1} Cambiar umbral de aviso    {0}[6]{1} Re-registrar la tarea' -f ($p.Fuerte + $p.Valor), $p.Fin)
        Write-Linea ('   {0}[0]{1} Volver a la vista anterior' -f ($p.Fuerte + $p.Valor), $p.Fin)
        Write-Linea ''

        $eleccion = ('' + (Read-Host '   ajuste')).Trim().ToUpperInvariant()
        $resultado = $null

        switch ($eleccion) {
            '1' {
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
            '2' {
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
            '3' {
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
            '4' {
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
            '5' {
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
            '6' {
                Write-Anuncio -Que 'Re-registrar la tarea programada' `
                    -Detalle 'Vuelve a sortear las horas del dia dentro de las ventanas de arriba.'
                $reg = @{ Pieza = 'Motor'; Accion = 'Registrar' }
                if ($RutaConfiguracion) { $reg['RutaConfiguracion'] = $RutaConfiguracion }
                & "$PSScriptRoot\Registrar-Tarea.ps1" @reg -Confirm:$false 6> $null | Out-Null
                $t = Get-ScheduledTask -TaskName $script:Ctx.NombreTarea -ErrorAction SilentlyContinue
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
            '0' { $seguirAqui = $false }
            ''  { $seguirAqui = $false }
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

        if ($seguirAqui) {
            [void](Wait-CierreDeVista -EtiquetaVolver 'continuar en ajustes')
        }
    }

    return $vigente
}

function Show-Laboratorio {
    <#
        .SYNOPSIS
            [L]: el laboratorio de D: VM por VM, y que hacer con lo que no esta al dia.
        .DESCRIPTION
            Las filas ya vienen decididas (Get-FilasDelLaboratorio, comun.ps1):
            aqui solo se pinta. Seccion 5.2, ADR-0104.
        .PARAMETER Vista
            Lo que devuelve laboratorio.ps1 -Accion Ver.
    #>
    [CmdletBinding()]
    [OutputType([void])]
    param(
        [Parameter(Mandatory)][AllowNull()][psobject] $Vista
    )

    $p = $script:Paleta
    if (-not $Vista) {
        Write-Veredicto -Nivel 'SIN DATOS' -Frase 'La configuracion no declara el laboratorio.' `
            -Accion 'Anade el bloque laboratorio a 3-Config/respaldo.jsonc.'
        return
    }
    $filas = @($Vista.Filas)
    if ($filas.Count -eq 0) {
        Write-Veredicto -Nivel 'SIN DATOS' -Frase 'No hay nada en el laboratorio de D:.' -Accion 'Comprueba que D: esta montado.'
        return
    }

    Write-Linea ('   {0}{1} {2} {3} {4} {5} {6} {7}{8}' -f $p.Tenue, 'VM'.PadRight(22), 'EN D:'.PadLeft(9), 'EN EL NODO'.PadLeft(11),
        'GUARDADA'.PadRight(12), 'DESDE ENTONCES'.PadRight(20), 'ULT. ENVIO'.PadLeft(10), 'LECTURA PI', $p.Fin)
    foreach ($f in $filas) {
        $color = switch ('' + $f.Tono) { 'Verde' { $p.Verde } 'Rojo' { $p.Rojo } default { $p.Ambar } }
        Write-Linea ('   {0} {1} {2} {3} {4} {5} {6}' -f
            (Format-Celda -Texto $f.Vm -Ancho 22 -Paleta $p -Color $p.Valor),
            (Format-Celda -Texto $f.EnD -Ancho 9 -Paleta $p -Derecha),
            (Format-Celda -Texto $f.EnNodo -Ancho 11 -Paleta $p -Derecha),
            (Format-Celda -Texto $f.Guardada -Ancho 12 -Paleta $p),
            (Format-Celda -Texto $f.Desde -Ancho 20 -Paleta $p -Color $color),
            (Format-Celda -Texto $f.Enviado -Ancho 10 -Paleta $p -Derecha),
            (Format-Celda -Texto $f.Lectura -Ancho 12 -Paleta $p))
    }
    foreach ($f in @($filas | Where-Object { $_.Detalle })) {
        Write-Linea ('   {0}{1}: {2}{3}' -f $p.Tenue, $f.Vm, $f.Detalle, $p.Fin)
    }
    if (-not $Vista.NodoLeido) { Write-Warning 'El nodo no respondio: la columna EN EL NODO sale con ?.' }
    if ($Vista.LecturaEnCurso) {
        Write-Linea ('   Hay una lectura completa en la Pi lanzada {0}: pulsa [7] para ver como va.' -f $Vista.LecturaEnCurso)
    }

    $noAlDia = @($filas | Where-Object { $_.Desde -notlike 'al dia*' })
    $abiertas = @($Vista.Abiertas)
    if ($noAlDia.Count -eq 0) {
        Write-Veredicto -Nivel 'OK' -Frase ('Las {0} carpetas del laboratorio estan al dia en el nodo.' -f $filas.Count)
        return
    }
    $accion = 'Apaga las VMs y pulsa [5] (Sincronizar al nodo): solo viaja lo que cambio. [3] dice cuanto antes de escribir nada.'
    if ($Vista.Manifiestos -eq 0) {
        $accion = 'Antes del primer guardado pulsa [7] (Lectura en Pi): la Pi lee el nodo y anota que tiene (horas; puedes usar las VMs).'
    }
    elseif (@($noAlDia | Where-Object { $_.Desde -like 'pendiente*' }).Count -gt 0) {
        $accion = 'Las pendientes necesitan [7] (Lectura en Pi) antes de [5]: el nodo ya no es lo que anotamos.'
    }
    if ($abiertas.Count -gt 0) { $accion += (' Ahora mismo hay abiertas: {0}.' -f ($abiertas -join ', ')) }
    Write-Veredicto -Nivel 'ATENCION' -Frase ('{0} de {1} carpetas del laboratorio no estan al dia en el nodo.' -f $noAlDia.Count, $filas.Count) -Accion $accion
}

function Show-GuardadoDelLaboratorio {
    <#
        .SYNOPSIS
            Lo que hizo el guardado del laboratorio, o su vista previa: cuanto viajo por VM y el veredicto.
        .PARAMETER Resultado
            Lo que devuelve laboratorio.ps1 -Accion Guardar o -Accion Previa.
    #>
    [CmdletBinding()]
    [OutputType([void])]
    param(
        [Parameter(Mandatory)][AllowNull()][psobject] $Resultado
    )

    $p = $script:Paleta
    if (-not $Resultado) { Write-Veredicto -Nivel 'SIN DATOS' -Frase 'El guardado no devolvio nada.'; return }
    if ($Resultado.Abortada) {
        Write-Veredicto -Nivel 'ATENCION' -Frase ('No se escribio nada. {0}' -f $Resultado.Motivo)
        return
    }

    $verbo = if ($Resultado.Simulada) { 'enviaria' } else { 'enviado' }
    foreach ($v in @($Resultado.PorVm | Where-Object { $_.Enviado -gt 0 -or $_.Estado -ne 'AlDia' })) {
        $color = switch ('' + $v.Estado) { 'AlDia' { $p.Verde } 'Falla' { $p.Rojo } default { $p.Ambar } }
        $texto = if ($v.Estado -eq 'AlDia') { '{0} {1}' -f $verbo, (Format-BytesDelLaboratorio -Bytes $v.Enviado) } else { $v.Detalle }
        Write-Linea ('   {0} {1}' -f (Format-Celda -Texto $v.Vm -Ancho 22 -Paleta $p -Color $p.Valor), (Format-Celda -Texto $texto -Ancho 70 -Paleta $p -Color $color))
    }
    $total = Format-BytesDelLaboratorio -Bytes $Resultado.Enviado
    if ($Resultado.Simulada) {
        $nivel = if ($Resultado.Estado -eq 'Protegido') { 'OK' } else { 'ATENCION' }
        Write-Veredicto -Nivel $nivel -Frase ('Vista previa: se enviarian {0} en total. No se escribio nada.' -f $total) `
            -Accion $(if ($nivel -eq 'OK') { '' } else { 'Lo que sale en ambar o rojo no se podria guardar asi: mira su linea.' })
        return
    }
    if ($Resultado.Estado -eq 'Protegido') {
        Write-Veredicto -Nivel 'OK' -Frase ('Laboratorio guardado: {0} enviados y confirmados; lo demas ya estaba.' -f $total)
    }
    else {
        Write-Veredicto -Nivel 'FALLO' -Frase ('Guardado incompleto ({0} enviados). Lo que esta en rojo o ambar no quedo en destino.' -f $total) `
            -Accion 'Si hay pendientes de lectura, ejecuta [7] (Lectura en Pi) y vuelve a [5]. El detalle, en el registro de hoy.'
    }
}

function Show-LecturaDelLaboratorio {
    <#
        .SYNOPSIS
            Lo que hizo la lectura en la Pi: la lanzo, sigue, o la recogio.
        .PARAMETER Resultado
            Lo que devuelve laboratorio.ps1 -Accion Leer.
    #>
    [CmdletBinding()]
    [OutputType([void])]
    param(
        [Parameter(Mandatory)][AllowNull()][psobject] $Resultado
    )

    if (-not $Resultado) { Write-Veredicto -Nivel 'SIN DATOS' -Frase 'La lectura no devolvio nada.'; return }
    switch ('' + $Resultado.Estado) {
        'Lanzada'  { Write-Veredicto -Nivel 'OK' -Frase $Resultado.Detalle }
        'EnCurso'  { Write-Veredicto -Nivel 'ATENCION' -Frase ('Todavia en curso: {0}.' -f $Resultado.Detalle) -Accion 'Vuelve a entrar en [3] -> [7] mas tarde para recogerla. Mientras, el guardado al nodo espera.' }
        'Recogida' {
            $malos = @($Resultado.Archivos | Where-Object { -not $_.Correcto }).Count
            if ($malos -eq 0) { Write-Veredicto -Nivel 'OK' -Frase ('Lectura recogida: {0}. El guardado diferencial ya sabe que tiene el nodo.' -f $Resultado.Detalle) }
            else { Write-Veredicto -Nivel 'ATENCION' -Frase ('Lectura recogida con errores: {0}.' -f $Resultado.Detalle) -Accion 'El detalle esta en el registro de hoy. Esas VMs siguen pendientes: vuelve a lanzar la lectura en [3] -> [7].' }
        }
        default    { Write-Veredicto -Nivel 'SIN DATOS' -Frase ('' + $Resultado.Detalle) }
    }
}

function Test-HayCorridaEnCurso {
    <#
        .SYNOPSIS
            Dice si ya hay alguien escribiendo, para no lanzar un segundo motor.

        .DESCRIPTION
            LA PUERTA QUE FALTABA EN EL UNICO SITIO QUE PODIA ABRIRLA.

            El Programador de tareas impide dos corridas automaticas a la vez
            -MultipleInstances IgnoreNew-, asi que se daba por hecho que dos
            motores no podian coincidir. Falso: NADA impedia pulsar [1] mientras
            una corrida programada estaba copiando. Las ventanas son tres al dia
            a minuto sorteado y una corrida dura ~107 s, de modo que el solape no
            hay que buscarlo: llega solo.

            LO QUE COSTABA, MEDIDO. Dos procesos anexando al mismo registro del
            dia perdian entre el 62 % y el 100 % de sus lineas por violacion de
            uso compartido -- y como Write-RegistroRespaldo corre con
            ErrorActionPreference 'Stop', esa violacion LANZABA y mataba la
            corrida a mitad. Ademas el segundo motor pisaba EN_CURSO.lock del
            primero, el que acabara antes borraba la marca del otro, y los dos
            robocopy trabajaban sobre el mismo par origen -> destino.

            El registro ya no se rompe -- Write-LineaDeRegistro comparte el
            archivo y se rinde en silencio -- pero eso arregla el sintoma. Dos
            motores copiando a la vez al mismo sitio sigue sin tener ningun
            sentido, y esta es la unica puerta por la que entra el segundo.

            ES LA MISMA COMPROBACION QUE YA HACIA verificar.ps1, movida a donde
            hacia mas falta. Alli se usa para NO PISAR una marca ajena; aqui para
            no arrancar. Cotejar mientras se copia es legitimo -- solo lee --, y
            por eso la opcion [3] no pasa por esta puerta.

            UNA MARCA VIEJA NO CIERRA NADA. Si el proceso que la puso ya no
            existe, lo que hay no es una corrida en curso sino un cadaver, y
            negarse a copiar por un cadaver dejaria el respaldo bloqueado hasta
            que alguien borrase un archivo a mano.
        .PARAMETER Configuracion
            El objeto de configuracion, para dar con la carpeta de estado.
    #>
    [CmdletBinding()]
    [OutputType([psobject])]
    param(
        [Parameter(Mandatory)][psobject] $Configuracion
    )

    $marca = Get-MarcaDeCorrida -Carpeta (Get-CarpetaDeEstado -Configuracion $Configuracion)
    if (-not ($marca.Existe -and -not $marca.Vieja)) {
        return [pscustomobject]@{ EnCurso = $false; Detalle = '' }
    }
    $que = switch ('' + $marca.Tipo) {
        'disco'        { 'una copia al disco frio' }
        'verificacion' { 'un cotejo contra el nodo' }
        default        { 'una corrida al nodo' }
    }
    return [pscustomobject]@{
        EnCurso = $true
        Detalle = ('Ya hay {0} en marcha (pid {1}, desde hace {2} min). No se lanza una segunda: espera a que termine.' -f
            $que, $marca.Pid, $marca.Minutos)
    }
}

function Show-Huerfanos {
    <#
        .SYNOPSIS
            [H]: lista las carpetas sin numerar dentro de los contenedores.
        .DESCRIPTION
            Permite inspeccionar en vivo que carpetas quedan fuera del respaldo
            sin abrir los registros en LOCALAPPDATA. Separa carpetas nuevas de
            las ya listadas en huerfanosConocidos.
        .PARAMETER Configuracion
            El objeto de configuracion completo.
    #>
    [CmdletBinding()]
    [OutputType([void])]
    param(
        [Parameter(Mandatory)][psobject] $Configuracion
    )

    $todos = @(Get-HuerfanosDeRespaldo -Configuracion $Configuracion)
    $nuevos = @($todos | Where-Object { -not (($_.PSObject.Properties.Name -contains 'Conocido') -and $_.Conocido) })
    $conocidos = @($todos | Where-Object { ($_.PSObject.Properties.Name -contains 'Conocido') -and $_.Conocido })

    if ($todos.Count -eq 0) {
        Write-Veredicto -Nivel 'OK' -Frase '0 carpetas sin clasificar dentro de los contenedores declarados.'
        return
    }

    $tabla = @(foreach ($h in $todos) {
        [pscustomobject]@{
            Carpeta = (Format-EtiquetaDeRaiz -Raiz $h.Ruta)
            Estado  = $(if (($h.PSObject.Properties.Name -contains 'Conocido') -and $h.Conocido) { 'conocida' } else { 'sin clasificar' })
        }
    })
    Write-Linea ''
    Show-Texto -Objeto $tabla
    Write-Linea ''

    if ($nuevos.Count -eq 0) {
        Write-Veredicto -Nivel 'OK' -Frase ('0 carpetas nuevas sin clasificar ({0} conocidas de programas en respaldo.jsonc).' -f $conocidos.Count)
    }
    else {
        Write-Veredicto -Nivel 'ATENCION' -Frase ('{0} carpeta(s) sin numerar no se copian ({1} conocida(s)).' -f $nuevos.Count, $conocidos.Count) `
            -Accion 'Para respaldar una carpeta, renombrala con prefijo NN_ (por ejemplo 04_PROYECTO). Si es de un programa y no quieres verla en PENDIENTE, anade su nombre a huerfanosConocidos en 3-Config/respaldo.jsonc.'
    }
}

# ---------------------------------------------------------------------------
#  NAVEGACION DE PANTALLAS DISCRETAS (TUI LIMPIA)
# ---------------------------------------------------------------------------
# Estado de navegacion centralizado en $script:Ctx (New-ContextoDeTablero).

function Test-ConsolaInteractiva {
    <#
        .SYNOPSIS
            True solo cuando hay consola interactiva real (sin redireccion).
        .DESCRIPTION
            Impide llamadas a Clear-Host o pausas bloqueantes cuando el guion
            corre dentro de pruebas automatizadas o con salida redirigida.
    #>
    [CmdletBinding()]
    [OutputType([bool])]
    param()

    try {
        return (-not [Console]::IsOutputRedirected -and -not [Console]::IsInputRedirected)
    }
    catch {
        return $false
    }
}

function Clear-PantallaInteractiva {
    <#
        .SYNOPSIS
            Limpia la pantalla solo en sesion interactiva real.
    #>
    [CmdletBinding()]
    [OutputType([void])]
    param()

    if (-not (Test-ConsolaInteractiva)) { return }
    try { Clear-Host }
    catch { Write-Verbose "Clear-Host omitido: $($_.Exception.Message)" }
}

function Get-MetaDeOpcion {
    <#
        .SYNOPSIS
            Metadatos de navegacion de cada opcion del menu (titulo, refresco, vista propia).
        .DESCRIPTION
            Incluye las 6 subvistas principales numeradas [1]-[6] del tablero
            consolidado, las operaciones atomicas invocadas por las subvistas y
            alias CLI heredados fuera del menu interactivo.
        .PARAMETER Opcion
            Digito [1]-[6] o identificador de operacion en mayusculas. Devuelve $null si no existe.
    #>
    [CmdletBinding()]
    [OutputType([psobject])]
    param(
        [Parameter(Mandatory)][AllowEmptyString()][string] $Opcion
    )

    $tabla = @{
        # --- Menu principal consolidado [1]-[6] ------------------------------
        '1' = @{ Titulo = '[1] RESPALDO AL NODO SMB';              Refrescable = $false; PropiaVista = $true  }
        '2' = @{ Titulo = '[2] COPIA FRIA FUERA DE LINEA';         Refrescable = $false; PropiaVista = $true  }
        '3' = @{ Titulo = '[3] LABORATORIO DE MAQUINAS VIRTUALES'; Refrescable = $false; PropiaVista = $true  }
        '4' = @{ Titulo = '[4] RECUPERACION ANTE DESASTRES';       Refrescable = $false; PropiaVista = $true  }
        '5' = @{ Titulo = '[5] PROGRAMACION Y SERVICIO';           Refrescable = $false; PropiaVista = $true  }
        '6' = @{ Titulo = '[6] DIAGNOSTICO E INVENTARIO';          Refrescable = $false; PropiaVista = $true  }
        # --- Operaciones atomicas invocadas por subvistas y CLI --------------
        'NODOCOPIAR'       = @{ Titulo = 'COPIA AL NODO SMB';                 Refrescable = $false; PropiaVista = $false }
        'NODOSIMULAR'      = @{ Titulo = 'VISTA PREVIA AL NODO SMB';          Refrescable = $true;  PropiaVista = $false }
        'NODOVERIFICAR'    = @{ Titulo = 'VERIFICACION DEL NODO SMB';         Refrescable = $true;  PropiaVista = $false }
        'ESTADOCOMPLETO'   = @{ Titulo = 'ESTADO COMPLETO';                   Refrescable = $true;  PropiaVista = $false }
        'SEMILLAREGENERAR' = @{ Titulo = 'SEMILLA DE ARRANQUE';               Refrescable = $false; PropiaVista = $false }
        'SERVICIOVER'      = @{ Titulo = 'ESTADO DEL SERVICIO';               Refrescable = $true;  PropiaVista = $false }
        'DISCOCOPIAR'      = @{ Titulo = 'COPIA AL DISCO FRIO';               Refrescable = $false; PropiaVista = $false }
        'DISCOSIMULAR'     = @{ Titulo = 'VISTA PREVIA AL DISCO FRIO';        Refrescable = $true;  PropiaVista = $false }
        'DISCOAUDITAR'     = @{ Titulo = 'AUDITORIA DEL DISCO FRIO';          Refrescable = $true;  PropiaVista = $false }
        'SEMILLAPROBAR'    = @{ Titulo = 'ENSAYO DE RESTAURACION';            Refrescable = $true;  PropiaVista = $false }
        'HUERFANOSVER'     = @{ Titulo = 'CARPETAS SIN CLASIFICAR';           Refrescable = $true;  PropiaVista = $false }
        'LABVER'           = @{ Titulo = 'VER EL LABORATORIO';                Refrescable = $true;  PropiaVista = $false }
        'LABSIMULAR'       = @{ Titulo = 'VISTA PREVIA DEL LABORATORIO';      Refrescable = $true;  PropiaVista = $false }
        'LABGUARDAR'       = @{ Titulo = 'GUARDAR LABORATORIO AL NODO';       Refrescable = $false; PropiaVista = $false }
        'LABLEERPI'        = @{ Titulo = 'LECTURA DEL LABORATORIO EN LA PI';  Refrescable = $false; PropiaVista = $true  }
        # --- Alias CLI heredados (solo uso directo via -Opcion) --------------
        'H' = @{ Titulo = 'CARPETAS SIN CLASIFICAR';           Refrescable = $true;  PropiaVista = $false }
        'L' = @{ Titulo = 'VER EL LABORATORIO';                Refrescable = $true;  PropiaVista = $false }
        'V' = @{ Titulo = 'VISTA PREVIA DEL LABORATORIO';      Refrescable = $true;  PropiaVista = $false }
        'G' = @{ Titulo = 'GUARDAR LABORATORIO AL NODO';       Refrescable = $false; PropiaVista = $false }
        'T' = @{ Titulo = 'LECTURA DEL LABORATORIO EN LA PI';  Refrescable = $false; PropiaVista = $true  }
    }
    $clave = ('' + $Opcion).Trim().ToUpperInvariant()
    if (-not $tabla.ContainsKey($clave)) { return $null }
    $m = $tabla[$clave]
    return [pscustomobject]@{
        Opcion      = $clave
        Titulo      = $m.Titulo
        Refrescable = [bool]$m.Refrescable
        PropiaVista = [bool]$m.PropiaVista
    }
}

function Write-CabeceraDeVista {
    <#
        .SYNOPSIS
            Pinta la cabecera con ruta de navegacion (breadcrumb) adaptada al ancho.
        .PARAMETER Titulo
            Titulo de la subvista activa.
    #>
    [CmdletBinding()]
    [OutputType([void])]
    param(
        [Parameter(Mandatory)][ValidateNotNullOrEmpty()][string] $Titulo
    )

    $geo = Get-GeometriaDeConsola
    $script:AnchoTablero = $geo.Ancho
    $p = $script:Paleta
    $u = $script:Capacidades.Unicode
    Write-Linea ' '
    Write-Linea ('  {0}CONSOLA{1}  {2}>{1}  {3}{4}{1}' -f $p.Tenue, $p.Fin, $p.Marco, ($p.Fuerte + $p.Titulo), $Titulo)
    Write-Linea (Get-Regla -Paleta $p -Unicode $u -Ancho $geo.Ancho)
    Write-Linea ' '
}

function Test-ConfirmacionPositiva {
    <#
        .SYNOPSIS
            True solo si el usuario confirmo explicitamente con S o SI. Funcion pura.
        .PARAMETER Respuesta
            Texto introducido en el aviso de confirmacion.
    #>
    [CmdletBinding()]
    [OutputType([bool])]
    param(
        [AllowEmptyString()][AllowNull()][string] $Respuesta = ''
    )

    $limpia = ('' + $Respuesta).Trim().ToUpperInvariant()
    return ($limpia -eq 'S' -or $limpia -eq 'SI')
}

function Confirm-AccionDeTablero {
    <#
        .SYNOPSIS
            Confirmacion de 1 paso con retorno seguro por omision (Enter = volver).
        .PARAMETER Resumen
            Que va a ocurrir si se confirma.
        .PARAMETER EtiquetaSi
            Pregunta corta delante de [S] si / [Enter] volver.
    #>
    [CmdletBinding()]
    [OutputType([bool])]
    param(
        [Parameter(Mandatory)][ValidateNotNullOrEmpty()][string] $Resumen,
        [ValidateNotNullOrEmpty()][string] $EtiquetaSi = 'Ejecutar ahora'
    )

    if (-not (Test-ConsolaInteractiva)) { return $true }
    $p = $script:Paleta
    Write-Linea ' '
    foreach ($l in @(Split-TextoEnLineas -Texto $Resumen -Ancho ($script:AnchoTablero - 2))) {
        Write-Linea ('   {0}{1}{2}' -f $p.Valor, $l, $p.Fin)
    }
    Write-Linea ' '
    Write-Linea ('   {0}{1}?{2}   {3}[S]{2} si    {4}/{2}    {3}[Enter]{2} volver sin tocar nada' -f
        $p.Ambar, $EtiquetaSi, $p.Fin, ($p.Fuerte + $p.Valor), $p.Tenue)
    Write-Linea ' '
    $resp = Read-Host '   confirmar'
    if (Test-ConfirmacionPositiva -Respuesta ('' + $resp)) {
        Write-Linea ' '
        return $true
    }
    $script:Ctx.CanceladaEnVista = $true
    return $false
}

function Wait-CierreDeVista {
    <#
        .SYNOPSIS
            Pausa obligatoria al cierre de una vista con opcion uniforme de volver o refrescar.
        .PARAMETER Refrescable
            Ofrece [1] actualizar junto a [0 / Enter] volver.
        .PARAMETER EtiquetaRefrescar
            Texto junto a [1].
        .PARAMETER EtiquetaVolver
            Texto junto a [0 / Enter].
    #>
    [CmdletBinding()]
    [OutputType([string])]
    param(
        [switch] $Refrescable,
        [ValidateNotNullOrEmpty()][string] $EtiquetaRefrescar = 'actualizar',
        [ValidateNotNullOrEmpty()][string] $EtiquetaVolver = 'volver al tablero'
    )

    if (-not (Test-ConsolaInteractiva)) { return 'Volver' }
    $p = $script:Paleta
    $u = $script:Capacidades.Unicode
    Write-Linea ' '
    Write-Linea (Get-Regla -Paleta $p -Unicode $u)
    if ($Refrescable) {
        Write-Linea ('   {0}[1]{1} {2}      {0}[0 / Enter]{1} {3}' -f ($p.Fuerte + $p.Valor), $p.Fin, $EtiquetaRefrescar, $EtiquetaVolver)
    }
    else {
        Write-Linea ('   {0}[0 / Enter]{1} {2}' -f ($p.Fuerte + $p.Valor), $p.Fin, $EtiquetaVolver)
    }
    Write-Linea ' '
    $r = ('' + (Read-Host '   accion')).Trim().ToUpperInvariant()
    if ($Refrescable -and ($r -eq '1' -or $r -eq 'R')) { return 'Refrescar' }
    return 'Volver'
}

function Show-DiscoFrioNoConectado {
    <#
        .SYNOPSIS
            Explica limpiamente cuando el disco frio no esta conectado y habilita [1] reintentar.
        .PARAMETER Configuracion
            El objeto de configuracion completo.
    #>
    [CmdletBinding()]
    [OutputType([void])]
    param(
        [Parameter(Mandatory)][psobject] $Configuracion
    )

    $ficha = $Configuracion.destinos.discoFrio
    $etiquetaDisco = 'disco frio'
    if ($ficha.PSObject.Properties.Name -contains 'etiqueta' -and -not [string]::IsNullOrWhiteSpace('' + $ficha.etiqueta)) {
        $etiquetaDisco = '' + $ficha.etiqueta
    }
    $script:Ctx.PermitirReintentoEnVista = $true
    $script:Ctx.EtiquetaReintentoEnVista = 'reintentar tras conectar'
    Write-Linea ' '
    Write-Veredicto -Nivel 'ATENCION' -Frase (
        'El disco frio ({0}, firma {1}, centinela {2}) no esta conectado o no paso la verificacion. No se toco nada.' -f
        $etiquetaDisco, $ficha.firma, $ficha.centinela) `
        -Accion ('Conecta la unidad USB {0} y pulsa [1] para reintentar aqui mismo, o [0 / Enter] para volver al tablero.' -f $etiquetaDisco)
}

function Format-DuracionMinutos {
    <#
        .SYNOPSIS
            Formatea una estimacion de minutos como "45 min" o "2.4 h".
    #>
    [CmdletBinding()]
    [OutputType([string])]
    param([int] $Minutos)

    if ($Minutos -le 0) { return '<1 min' }
    if ($Minutos -lt 90) { return '{0} min' -f $Minutos }
    return '{0:N1} h' -f ($Minutos / 60.0)
}

function Write-TarjetaDeLecturaPi {
    <#
        .SYNOPSIS
            Pinta el bloque de estado en vivo de una lectura [T] en la Pi.
        .PARAMETER EstadoLectura
            Lo que devuelve laboratorio.ps1 -Accion EstadoLectura.
    #>
    [CmdletBinding()]
    [OutputType([void])]
    param(
        [Parameter(Mandatory)][psobject] $EstadoLectura
    )

    $p = $script:Paleta
    $lec = $EstadoLectura.Lectura
    $av = $EstadoLectura.Avance
    $inicioTxt = if ($lec -and $lec.Inicio) { $lec.Inicio.Replace('T', ' ') } else { '?' }
    $minTxt = if ($lec) { Format-DuracionMinutos -Minutos $lec.Minutos } else { '?' }
    $alcanceTxt = if ($lec -and $lec.Alcance) { $lec.Alcance } else { 'completo' }

    Write-Linea ('   {0}Lanzada    {1}  {2}  (hace {3}, alcance: {4})' -f $p.Etiqueta, $p.Fin, $inicioTxt, $minTxt, $alcanceTxt)

    if (-not $EstadoLectura.SshOk) {
        Write-Linea ('   {0}Estado Pi  {1}  {2}sin conexion ssh{1}' -f $p.Etiqueta, $p.Fin, $p.Ambar)
        Write-Linea ' '
        Write-Veredicto -Nivel 'ATENCION' -Frase $EstadoLectura.Detalle `
            -Accion 'Comprueba que la Pi responde por ssh y pulsa [2] para refrescar.'
        return
    }

    $colorPi = if ($EstadoLectura.ProcesoVivo) { $p.Verde } else { $p.Rojo }
    $textoPi = if ($EstadoLectura.ProcesoVivo) { 'corriendo en la Pi (puedes usar tus VMs)' } else { 'NO SE VE CORRIENDO EN LA PI (sin FIN)' }
    Write-Linea ('   {0}Estado Pi  {1}  {2}{3}{1}' -f $p.Etiqueta, $p.Fin, $colorPi, $textoPi)

    if ($av -and $av.Total -gt 0) {
        Write-Linea ('   {0}Progreso   {1}  archivo {2} de {3}  ({4} terminados)' -f
            $p.Etiqueta, $p.Fin, $av.EnProceso, $av.Total, $av.Completados)
    }
    if ($av -and $av.ArchivoActual) {
        Write-Linea ('   {0}Leyendo    {1}  {2}' -f $p.Etiqueta, $p.Fin, $av.ArchivoActual)
        if ($av.TieneIntraArchivo) {
            Write-Linea ('   {0}Avance     {1}  {2}{3}%  ({4} / {5}, {6} MB/s){1}' -f
                $p.Etiqueta, $p.Fin, $p.Verde, $av.PorcentajeArchivo,
                (Format-BytesDelLaboratorio -Bytes $av.BytesLeidos),
                (Format-BytesDelLaboratorio -Bytes $av.TamanoArchivo),
                $av.MbPorSegundo)
        }
        else {
            Write-Linea ('   {0}Avance     {1}  {2}leyendo disco en la Pi (detalle % intra-archivo disponible en proximas lecturas){1}' -f
                $p.Etiqueta, $p.Fin, $p.Tenue)
        }
    }

    if (-not $EstadoLectura.ProcesoVivo) {
        Write-Linea ' '
        Write-Veredicto -Nivel 'FALLO' -Frase $EstadoLectura.Detalle `
            -Accion 'Si la Pi se reinicio o se corto, pulsa [3] para cancelar y limpiar esta lectura y vuelve a lanzarla.'
    }
}

function Invoke-VistaLecturaPi {
    <#
        .SYNOPSIS
            Subvista interactiva dedicada para [3] > [7]: plan, pendientes vs completo,
            seguimiento en vivo con recogida automatica y cancelacion controlada.
        .PARAMETER Configuracion
            El objeto de configuracion vigente.
        .PARAMETER RutaConfiguracion
            La ruta, si se paso una; vacia para la de 3-Config.
    #>
    [CmdletBinding()]
    [OutputType([void])]
    param(
        [Parameter(Mandatory)][psobject] $Configuracion,
        [AllowEmptyString()][string] $RutaConfiguracion = ''
    )

    $comunes = @{}
    if ($RutaConfiguracion) { $comunes['RutaConfiguracion'] = $RutaConfiguracion }
    $p = $script:Paleta
    $u = $script:Capacidades.Unicode
    $carpetaEstado = Get-CarpetaDeEstado -Configuracion $Configuracion
    $seguirT = $true

    while ($seguirT) {
        Clear-PantallaInteractiva
        Write-CabeceraDeVista -Titulo '[3] > [7] LECTURA DEL LABORATORIO EN LA PI'

        $lecturaActiva = Read-LecturaDelLaboratorio -Carpeta $carpetaEstado

        if (-not $lecturaActiva) {
            Write-Anuncio -Que 'Leer discos virtuales del laboratorio en la Pi' `
                -Detalle 'La Pi relee el disco del nodo para crear o cotejar manifiestos. Puedes seguir usando tus VMs.'
            Write-Linea ' '
            Write-Linea ('   {0}Analizando que discos del nodo necesitan lectura...{1}' -f $p.Tenue, $p.Fin)

            $plan = & "$nucleo\laboratorio.ps1" @comunes -Accion PlanLectura
            Clear-PantallaInteractiva
            Write-CabeceraDeVista -Titulo '[3] > [7] LECTURA DEL LABORATORIO EN LA PI'
            Write-Anuncio -Que 'Leer discos virtuales del laboratorio en la Pi' `
                -Detalle 'La Pi relee el disco del nodo para crear o cotejar manifiestos. Puedes seguir usando tus VMs.'
            Write-Linea ' '

            if (-not $plan -or -not $plan.TieneLaboratorio) {
                Write-Veredicto -Nivel 'SIN DATOS' -Frase 'La configuracion no declara el bloque laboratorio.'
                if ((Wait-CierreDeVista -Refrescable) -ne 'Refrescar') { $seguirT = $false }
                continue
            }
            if (-not $plan.NodoVivo) {
                Write-Veredicto -Nivel 'SIN DATOS' -Frase 'El nodo no responde por red: no se puede consultar el laboratorio.' `
                    -Accion 'Comprueba que el NAS esta encendido y pulsa [1] para reintentar.'
                if ((Wait-CierreDeVista -Refrescable -EtiquetaRefrescar 'reintentar') -ne 'Refrescar') { $seguirT = $false }
                continue
            }
            if ($plan.TotalArchivos -eq 0) {
                Write-Veredicto -Nivel 'SIN DATOS' -Frase 'En el nodo no hay discos virtuales grandes que leer.'
                if ((Wait-CierreDeVista -Refrescable) -ne 'Refrescar') { $seguirT = $false }
                continue
            }

            $bytesTotalTxt = Format-BytesDelLaboratorio -Bytes $plan.TotalBytes
            $durTotalTxt = Format-DuracionMinutos -Minutos $plan.TotalMinutos
            Write-Linea ('   {0}Laboratorio completo {1}  {2} archivo(s), {3}  (~{4} en la Pi)' -f
                $p.Etiqueta, $p.Fin, $plan.TotalArchivos, $bytesTotalTxt, $durTotalTxt)

            if ($plan.PendientesArchivos -gt 0) {
                $bytesPendTxt = Format-BytesDelLaboratorio -Bytes $plan.PendientesBytes
                $durPendTxt = Format-DuracionMinutos -Minutos $plan.PendientesMinutos
                Write-Linea ('   {0}Pendientes de leer   {1}  {2}{3} archivo(s), {4}  (~{5} en la Pi){1}' -f
                    $p.Etiqueta, $p.Fin, $p.Ambar, $plan.PendientesArchivos, $bytesPendTxt, $durPendTxt)
                if (@($plan.PendientesVms).Count -gt 0) {
                    Write-Linea ('   {0}VMs pendientes       {1}  {2}' -f $p.Etiqueta, $p.Fin, ($plan.PendientesVms -join ', '))
                }
            }
            else {
                Write-Linea ('   {0}Pendientes de leer   {1}  {2}0 pendientes: todos los manifiestos cuadran con el nodo{1}' -f
                    $p.Etiqueta, $p.Fin, $p.Verde)
            }

            Write-Linea ' '
            Write-Linea (Get-Regla -Paleta $p -Unicode $u)
            $hayPendientes = ($plan.PendientesArchivos -gt 0)
            if ($hayPendientes) {
                Write-Linea ('   {0}[1]{1} Leer solo las pendientes (~{2})' -f ($p.Fuerte + $p.Valor), $p.Fin, (Format-DuracionMinutos -Minutos $plan.PendientesMinutos))
                Write-Linea ('   {0}[2]{1} Leer laboratorio completo (~{2})' -f ($p.Fuerte + $p.Valor), $p.Fin, $durTotalTxt)
                Write-Linea ('   {0}[3]{1} Actualizar analisis' -f ($p.Fuerte + $p.Valor), $p.Fin)
                Write-Linea ('   {0}[0]{1} Volver a la vista anterior' -f ($p.Fuerte + $p.Valor), $p.Fin)
            }
            else {
                Write-Linea ('   {0}[1]{1} Leer laboratorio completo (~{2}, cotejo total del disco del nodo)' -f ($p.Fuerte + $p.Valor), $p.Fin, $durTotalTxt)
                Write-Linea ('   {0}[2]{1} Actualizar analisis' -f ($p.Fuerte + $p.Valor), $p.Fin)
                Write-Linea ('   {0}[0]{1} Volver a la vista anterior' -f ($p.Fuerte + $p.Valor), $p.Fin)
            }
            Write-Linea ' '

            $el = ('' + (Read-Host '   accion')).Trim().ToUpperInvariant()
            if ($el -eq '' -or $el -eq '0') { $seguirT = $false; continue }
            if ($hayPendientes) {
                switch ($el) {
                    '1' {
                        $resP = 'Se lanzara en la Pi la lectura de {0} archivo(s) pendientes ({1}, ~{2}). Mientras corre, el guardado al nodo esperara.' -f
                            $plan.PendientesArchivos, (Format-BytesDelLaboratorio -Bytes $plan.PendientesBytes), (Format-DuracionMinutos -Minutos $plan.PendientesMinutos)
                        if (Confirm-AccionDeTablero -Resumen $resP -EtiquetaSi 'Lanzar lectura de pendientes') {
                            $rLanz = & "$nucleo\laboratorio.ps1" @comunes -Accion Leer -SoloPendientes -Confirm:$false
                            Show-LecturaDelLaboratorio -Resultado $rLanz
                            if (-not $rLanz -or $rLanz.Estado -ne 'Lanzada') {
                                [void](Wait-CierreDeVista)
                                $seguirT = $false
                            }
                            else { Start-Sleep -Milliseconds 900 }
                        }
                    }
                    '2' {
                        $resC = 'Se lanzara en la Pi la lectura COMPLETA de {0} archivo(s) ({1}, ~{2}). Mientras corre, el guardado al nodo esperara.' -f
                            $plan.TotalArchivos, $bytesTotalTxt, $durTotalTxt
                        if (Confirm-AccionDeTablero -Resumen $resC -EtiquetaSi 'Lanzar lectura completa') {
                            $rLanz = & "$nucleo\laboratorio.ps1" @comunes -Accion Leer -Confirm:$false
                            Show-LecturaDelLaboratorio -Resultado $rLanz
                            if (-not $rLanz -or $rLanz.Estado -ne 'Lanzada') {
                                [void](Wait-CierreDeVista)
                                $seguirT = $false
                            }
                            else { Start-Sleep -Milliseconds 900 }
                        }
                    }
                    '3' { }
                    default {
                        Write-Veredicto -Nivel 'SIN DATOS' -Frase ('"{0}" no es una opcion valida aqui.' -f $el)
                        [void](Wait-CierreDeVista -EtiquetaVolver 'continuar')
                    }
                }
            }
            else {
                switch ($el) {
                    '1' {
                        $resC = 'Se lanzara en la Pi la lectura COMPLETA de {0} archivo(s) ({1}, ~{2}). Mientras corre, el guardado al nodo esperara.' -f
                            $plan.TotalArchivos, $bytesTotalTxt, $durTotalTxt
                        if (Confirm-AccionDeTablero -Resumen $resC -EtiquetaSi 'Lanzar lectura completa') {
                            $rLanz = & "$nucleo\laboratorio.ps1" @comunes -Accion Leer -Confirm:$false
                            Show-LecturaDelLaboratorio -Resultado $rLanz
                            if (-not $rLanz -or $rLanz.Estado -ne 'Lanzada') {
                                [void](Wait-CierreDeVista)
                                $seguirT = $false
                            }
                            else { Start-Sleep -Milliseconds 900 }
                        }
                    }
                    '2' { }
                    default {
                        Write-Veredicto -Nivel 'SIN DATOS' -Frase ('"{0}" no es una opcion valida aqui.' -f $el)
                        [void](Wait-CierreDeVista -EtiquetaVolver 'continuar')
                    }
                }
            }
            continue
        }

        # --- Hay lectura activa en la Pi -------------------------------------
        Write-Linea ('   {0}Consultando avance en la Pi...{1}' -f $p.Tenue, $p.Fin)
        $estT = & "$nucleo\laboratorio.ps1" @comunes -Accion EstadoLectura

        Clear-PantallaInteractiva
        Write-CabeceraDeVista -Titulo '[3] > [7] LECTURA DEL LABORATORIO EN LA PI'

        if ($estT -and $estT.Terminada) {
            Write-Anuncio -Que 'Lectura terminada en la Pi: recogiendo manifiestos' `
                -Detalle 'Descargando huellas por bloque al equipo y actualizando LABORATORIO.tsv.'
            Write-Linea ' '
            $rec = & "$nucleo\laboratorio.ps1" @comunes -Accion Leer -Confirm:$false
            Show-LecturaDelLaboratorio -Resultado $rec
            [void](Wait-CierreDeVista)
            $seguirT = $false
            continue
        }

        Write-TarjetaDeLecturaPi -EstadoLectura $estT
        Write-Linea ' '
        Write-Linea (Get-Regla -Paleta $p -Unicode $u)
        Write-Linea ('   {0}[1]{1} Seguir en vivo (cada 10 s; recoge sola al terminar)' -f ($p.Fuerte + $p.Valor), $p.Fin)
        Write-Linea ('   {0}[2]{1} Refrescar avance ahora' -f ($p.Fuerte + $p.Valor), $p.Fin)
        Write-Linea ('   {0}[3]{1} Cancelar lectura en la Pi' -f ($p.Fuerte + $p.Valor), $p.Fin)
        Write-Linea ('   {0}[0]{1} Volver a la vista anterior (la Pi sigue trabajando)' -f ($p.Fuerte + $p.Valor), $p.Fin)
        Write-Linea ' '

        $el2 = ('' + (Read-Host '   accion')).Trim().ToUpperInvariant()
        switch ($el2) {
            ''  { $seguirT = $false }
            '0' { $seguirT = $false }
            '2' { }
            '3' {
                $resX = 'Se detendra blkhash.py en la Pi y se limpiara lectura.json. El disco del nodo y los manifiestos previos no se tocan.'
                if (Confirm-AccionDeTablero -Resumen $resX -EtiquetaSi 'Cancelar lectura en la Pi') {
                    $cRes = & "$nucleo\laboratorio.ps1" @comunes -Accion CancelarLectura -Confirm:$false
                    if ($cRes -and $cRes.Cancelada) {
                        Write-Veredicto -Nivel 'OK' -Frase $cRes.Detalle
                        [void](Wait-CierreDeVista)
                        $seguirT = $false
                    }
                    else {
                        Write-Veredicto -Nivel 'FALLO' -Frase $(if ($cRes) { $cRes.Detalle } else { 'No se pudo cancelar la lectura.' })
                        [void](Wait-CierreDeVista -EtiquetaVolver 'continuar')
                    }
                }
            }
            '1' {
                $enVivo = $true
                while ($enVivo) {
                    $estVivo = & "$nucleo\laboratorio.ps1" @comunes -Accion EstadoLectura
                    Clear-PantallaInteractiva
                    Write-CabeceraDeVista -Titulo '[3] > [7] LECTURA EN LA PI (EN VIVO)'

                    if (-not $estVivo -or -not $estVivo.EnCurso) {
                        Write-Veredicto -Nivel 'SIN DATOS' -Frase 'Ya no hay ninguna lectura en curso.'
                        [void](Wait-CierreDeVista)
                        $enVivo = $false
                        $seguirT = $false
                        break
                    }
                    if ($estVivo.Terminada) {
                        Write-Anuncio -Que 'Lectura terminada: recogiendo manifiestos automaticamente' `
                            -Detalle 'Descargando huellas por bloque al equipo...'
                        Write-Linea ' '
                        $recVivo = & "$nucleo\laboratorio.ps1" @comunes -Accion Leer -Confirm:$false
                        Show-LecturaDelLaboratorio -Resultado $recVivo
                        [void](Wait-CierreDeVista)
                        $enVivo = $false
                        $seguirT = $false
                        break
                    }

                    Write-TarjetaDeLecturaPi -EstadoLectura $estVivo
                    Write-Linea ' '
                    Write-Linea (Get-Regla -Paleta $p -Unicode $u)
                    Write-Linea ('   {0}Ultima consulta {1:HH:mm:ss}   -   refresco automatico cada 10 s{2}' -f $p.Tenue, (Get-Date), $p.Fin)
                    Write-Linea ('   {0}[1]{1} Refrescar ya     {0}[0 / Esc]{1} Volver (la Pi sigue trabajando)' -f
                        ($p.Fuerte + $p.Valor), $p.Fin)

                    # Espera 10 s en tramos de 200 ms para responder al instante a 0 / Esc / 1.
                    for ($paso = 0; $paso -lt 50; $paso++) {
                        try {
                            if ([Console]::KeyAvailable) {
                                $tecla = [Console]::ReadKey($true)
                                if ($tecla.Key -eq [System.ConsoleKey]::Escape -or
                                    $tecla.KeyChar -eq '0') {
                                    $enVivo = $false
                                    $seguirT = $false
                                    break
                                }
                                if ($tecla.KeyChar -eq '1' -or $tecla.Key -eq [System.ConsoleKey]::Enter) {
                                    break
                                }
                            }
                        }
                        catch {
                            $enVivo = $false
                            break
                        }
                        Start-Sleep -Milliseconds 200
                    }
                }
            }
            default {
                Write-Veredicto -Nivel 'SIN DATOS' -Frase ('"{0}" no es una opcion valida aqui.' -f $el2)
                [void](Wait-CierreDeVista -EtiquetaVolver 'continuar')
            }
        }
    }
}

function Read-AccionEnVivo {
    <#
        .SYNOPSIS
            Espera pulsacion directa de una tecla o refresco automatico / cambio de ancho.
        .DESCRIPTION
            Mantiene la consola viva sin bloquear el redibujo: refresca cada
            IntervaloSegundos y detecta al instante redimensionamientos de la
            ventana de terminal. Si la entrada esta redirigida cae a Read-Host.
    #>
    [CmdletBinding()]
    [OutputType([psobject])]
    param(
        [ValidateRange(1, 300)][int] $IntervaloSegundos = 15,
        [int] $AnchoInicial = 0,
        [string] $Etiqueta = 'Seleccione opcion [1-6], [Enter] refrescar, [0] salir'
    )

    if (-not (Test-ConsolaInteractiva)) {
        $txt = ('' + (Read-Host '  opcion')).Trim().ToUpperInvariant()
        if ($txt -eq '0') { return [pscustomobject]@{ Tipo = 'Salir'; Tecla = '0' } }
        if ($txt -eq '')  { return [pscustomobject]@{ Tipo = 'Refrescar'; Tecla = '' } }
        return [pscustomobject]@{ Tipo = 'Opcion'; Tecla = $txt }
    }

    $p = $script:Paleta
    Write-Host ('  {0}{1}{2} ' -f $p.Tenue, $Etiqueta, $p.Fin) -NoNewline

    $anchoBase = if ($AnchoInicial -gt 0) { $AnchoInicial } else { Get-AnchoDeConsola }
    $pasos = [Math]::Max(1, [int]($IntervaloSegundos * 1000 / 150))
    for ($i = 0; $i -lt $pasos; $i++) {
        try {
            $wAct = Get-AnchoDeConsola
            if ($wAct -ne $anchoBase) {
                return [pscustomobject]@{ Tipo = 'Redimension'; Tecla = '' }
            }
            if ([Console]::KeyAvailable) {
                $k = [Console]::ReadKey($true)
                if ($k.Key -eq [System.ConsoleKey]::Escape) {
                    return [pscustomobject]@{ Tipo = 'Salir'; Tecla = '0' }
                }
                if ($k.Key -eq [System.ConsoleKey]::Enter) {
                    return [pscustomobject]@{ Tipo = 'Refrescar'; Tecla = '' }
                }
                $ch = ('' + $k.KeyChar).Trim().ToUpperInvariant()
                if (-not $ch) { continue }
                if ($ch -eq '0') {
                    return [pscustomobject]@{ Tipo = 'Salir'; Tecla = '0' }
                }
                return [pscustomobject]@{ Tipo = 'Opcion'; Tecla = $ch }
            }
        }
        catch {
            break
        }
        Start-Sleep -Milliseconds 150
    }
    return [pscustomobject]@{ Tipo = 'Refrescar'; Tecla = '' }
}

function Invoke-VistaNodo {
    <#
        .SYNOPSIS
            Subvista [1]: operaciones y verificacion contra el nodo SMB.
    #>
    [CmdletBinding()]
    [OutputType([void])]
    param(
        [Parameter(Mandatory)][psobject] $Configuracion,
        [AllowEmptyString()][string] $RutaConfiguracion = ''
    )

    $p = $script:Paleta; $u = $script:Capacidades.Unicode
    $carpeta = Get-CarpetaDeEstado -Configuracion $Configuracion
    $seguirV = $true
    while ($seguirV) {
        Clear-PantallaInteractiva
        Write-CabeceraDeVista -Titulo '[1] RESPALDO AL NODO SMB'
        $unc = '' + $Configuracion.destinos.nodo.unc
        $vivo = [bool](Test-Path -LiteralPath $unc -ErrorAction SilentlyContinue)
        $lib = if ($vivo) { Format-Espacio -Bytes (Get-EspacioLibre -Ruta $unc) } else { 'no disponible' }
        $est = Read-EstadoRespaldo -Carpeta $carpeta
        $ult = Format-Antiguedad -Momento ('' + $est['copia_momento'])
        $colorN = if ($vivo) { $p.Verde } else { $p.Rojo }
        $sitN = if ($vivo) { 'En linea' } else { 'FUERA DE LINEA' }

        Write-Linea ('   {0}Destino SMB   {1}  {2}' -f $p.Etiqueta, $p.Fin, $unc)
        Write-Linea ('   {0}Estado        {1}  {2}{3}{1}   ({4})' -f $p.Etiqueta, $p.Fin, $colorN, $sitN, $lib)
        Write-Linea ('   {0}Ultima copia  {1}  {2}' -f $p.Etiqueta, $p.Fin, $ult)
        Write-Linea ' '
        Write-Linea (Get-Regla -Paleta $p -Unicode $u -Ancho $script:AnchoTablero)
        Write-Linea ('   {0}[1]{1} Simulacion previa (sin escritura)' -f ($p.Fuerte + $p.Valor), $p.Fin)
        Write-Linea ('   {0}[2]{1} Ejecutar respaldo al nodo SMB' -f ($p.Fuerte + $p.Valor), $p.Fin)
        Write-Linea ('   {0}[3]{1} Verificar integridad (estructura y SHA-256)' -f ($p.Fuerte + $p.Valor), $p.Fin)
        Write-Linea ('   {0}[4]{1} Actualizar estado' -f ($p.Fuerte + $p.Valor), $p.Fin)
        Write-Linea ('   {0}[0]{1} Volver a la consola' -f ($p.Fuerte + $p.Valor), $p.Fin)
        Write-Linea ' '

        $el = ('' + (Read-Host '   accion')).Trim().ToUpperInvariant()
        switch ($el) {
            ''  { $seguirV = $false }
            '0' { $seguirV = $false }
            '4' { }
            '1' {
                Clear-PantallaInteractiva; Write-CabeceraDeVista -Titulo '[1] > [1] SIMULACION AL NODO SMB'
                Invoke-OpcionDelTablero -Opcion 'NodoSimular' -Configuracion $Configuracion -RutaConfiguracion $RutaConfiguracion -Interactivo
                [void](Wait-CierreDeVista -EtiquetaVolver 'continuar en Nodo SMB')
            }
            '2' {
                Clear-PantallaInteractiva; Write-CabeceraDeVista -Titulo '[1] > [2] EJECUTAR RESPALDO AL NODO SMB'
                Invoke-OpcionDelTablero -Opcion 'NodoCopiar' -Configuracion $Configuracion -RutaConfiguracion $RutaConfiguracion -Interactivo
                if (-not $script:Ctx.CanceladaEnVista) { [void](Wait-CierreDeVista -EtiquetaVolver 'continuar en Nodo SMB') }
                $script:Ctx.CanceladaEnVista = $false
            }
            '3' {
                Clear-PantallaInteractiva; Write-CabeceraDeVista -Titulo '[1] > [3] VERIFICACION DE INTEGRIDAD (SHA-256)'
                Invoke-OpcionDelTablero -Opcion 'NodoVerificar' -Configuracion $Configuracion -RutaConfiguracion $RutaConfiguracion -Interactivo
                [void](Wait-CierreDeVista -EtiquetaVolver 'continuar en Nodo SMB')
            }
            default {
                Write-Veredicto -Nivel 'SIN DATOS' -Frase ('Opcion "{0}" no valida en esta vista.' -f $el)
                [void](Wait-CierreDeVista -EtiquetaVolver 'continuar en Nodo SMB')
            }
        }
    }
}

function Invoke-VistaDiscoFrio {
    <#
        .SYNOPSIS
            Subvista [2]: operaciones y auditoria del disco frio fuera de linea.
    #>
    [CmdletBinding()]
    [OutputType([void])]
    param(
        [Parameter(Mandatory)][psobject] $Configuracion,
        [AllowEmptyString()][string] $RutaConfiguracion = ''
    )

    $p = $script:Paleta; $u = $script:Capacidades.Unicode
    $carpeta = Get-CarpetaDeEstado -Configuracion $Configuracion
    $ficha = $Configuracion.destinos.discoFrio
    $seguirV = $true
    while ($seguirV) {
        Clear-PantallaInteractiva
        Write-CabeceraDeVista -Titulo '[2] COPIA FRIA FUERA DE LINEA'
        $disco = Get-DiscoFrio -Configuracion $Configuracion 2>$null
        $est = Read-EstadoRespaldo -Carpeta $carpeta
        $ult = Format-Antiguedad -Momento ('' + $est['disco_momento'])

        if ($disco) {
            $lib = Format-Espacio -Bytes (Get-EspacioLibre -Ruta $disco.Raiz)
            Write-Linea ('   {0}Unidad        {1}  {2}Montado en {3}{1}  (firma {4}, {5})' -f $p.Etiqueta, $p.Fin, $p.Verde, $disco.Raiz, $disco.Firma, $lib)
        }
        else {
            Write-Linea ('   {0}Unidad        {1}  {2}Desconectado{1}  (esperada firma {3}, centinela {4})' -f $p.Etiqueta, $p.Fin, $p.Ambar, $ficha.firma, $ficha.centinela)
        }
        Write-Linea ('   {0}Politica      {1}  Aditiva estricta (equipo + nodo + laboratorio VM; nunca borra)' -f $p.Etiqueta, $p.Fin)
        Write-Linea ('   {0}Ultima copia  {1}  {2}' -f $p.Etiqueta, $p.Fin, $ult)
        Write-Linea ' '
        Write-Linea (Get-Regla -Paleta $p -Unicode $u -Ancho $script:AnchoTablero)
        Write-Linea ('   {0}[1]{1} Simulacion previa (3 pasadas, sin escritura)' -f ($p.Fuerte + $p.Valor), $p.Fin)
        Write-Linea ('   {0}[2]{1} Ejecutar copia fria aditiva completa' -f ($p.Fuerte + $p.Valor), $p.Fin)
        Write-Linea ('   {0}[3]{1} Auditar archivos incompletos en disco frio' -f ($p.Fuerte + $p.Valor), $p.Fin)
        Write-Linea ('   {0}[4]{1} Re-detectar unidad USB' -f ($p.Fuerte + $p.Valor), $p.Fin)
        Write-Linea ('   {0}[0]{1} Volver a la consola' -f ($p.Fuerte + $p.Valor), $p.Fin)
        Write-Linea ' '

        $el = ('' + (Read-Host '   accion')).Trim().ToUpperInvariant()
        switch ($el) {
            ''  { $seguirV = $false }
            '0' { $seguirV = $false }
            '4' { }
            '1' {
                Clear-PantallaInteractiva; Write-CabeceraDeVista -Titulo '[2] > [1] SIMULACION DE COPIA FRIA'
                Invoke-OpcionDelTablero -Opcion 'DiscoSimular' -Configuracion $Configuracion -RutaConfiguracion $RutaConfiguracion -Interactivo
                [void](Wait-CierreDeVista -EtiquetaVolver 'continuar en Disco frio')
            }
            '2' {
                Clear-PantallaInteractiva; Write-CabeceraDeVista -Titulo '[2] > [2] EJECUTAR COPIA FRIA ADITIVA'
                Invoke-OpcionDelTablero -Opcion 'DiscoCopiar' -Configuracion $Configuracion -RutaConfiguracion $RutaConfiguracion -Interactivo
                if (-not $script:Ctx.CanceladaEnVista) { [void](Wait-CierreDeVista -EtiquetaVolver 'continuar en Disco frio') }
                $script:Ctx.CanceladaEnVista = $false
            }
            '3' {
                Clear-PantallaInteractiva; Write-CabeceraDeVista -Titulo '[2] > [3] AUDITORIA DE ARCHIVOS INCOMPLETOS'
                Invoke-OpcionDelTablero -Opcion 'DiscoAuditar' -Configuracion $Configuracion -RutaConfiguracion $RutaConfiguracion -Interactivo
                [void](Wait-CierreDeVista -EtiquetaVolver 'continuar en Disco frio')
            }
            default {
                Write-Veredicto -Nivel 'SIN DATOS' -Frase ('Opcion "{0}" no valida en esta vista.' -f $el)
                [void](Wait-CierreDeVista -EtiquetaVolver 'continuar en Disco frio')
            }
        }
    }
}

function Invoke-VistaLaboratorio {
    <#
        .SYNOPSIS
            Subvista [3]: gestion integral del laboratorio VM (nodo, disco frio,
            validacion en 3 sitios y lectura de manifiestos en la Pi).
    #>
    [CmdletBinding()]
    [OutputType([void])]
    param(
        [Parameter(Mandatory)][psobject] $Configuracion,
        [AllowEmptyString()][string] $RutaConfiguracion = ''
    )

    $comunes = @{}
    if ($RutaConfiguracion) { $comunes['RutaConfiguracion'] = $RutaConfiguracion }
    $p = $script:Paleta; $u = $script:Capacidades.Unicode
    $seguirV = $true
    while ($seguirV) {
        Clear-PantallaInteractiva
        Write-CabeceraDeVista -Titulo '[3] LABORATORIO DE MAQUINAS VIRTUALES'
        $vista = & "$nucleo\laboratorio.ps1" @comunes -Accion Ver
        Show-Laboratorio -Vista $vista
        Write-Linea ' '
        Write-Linea (Get-Regla -Paleta $p -Unicode $u -Ancho $script:AnchoTablero)
        Write-Linea ('   {0}[1]{1} Validacion 3 sitios (D: / Nodo / Frio)   {0}[5]{1} Sincronizar lab al nodo SMB' -f ($p.Fuerte + $p.Valor), $p.Fin)
        Write-Linea ('   {0}[2]{1} Validacion profunda del disco frio       {0}[6]{1} Copia fria de VMs al USB' -f ($p.Fuerte + $p.Valor), $p.Fin)
        Write-Linea ('   {0}[3]{1} Simulacion diferencial al nodo SMB       {0}[7]{1} Lectura de manifiestos en Pi' -f ($p.Fuerte + $p.Valor), $p.Fin)
        Write-Linea ('   {0}[4]{1} Simulacion local al disco frio           {0}[8]{1} Actualizar vista' -f ($p.Fuerte + $p.Valor), $p.Fin)
        Write-Linea ('   {0}[0]{1} Volver a la consola' -f ($p.Fuerte + $p.Valor), $p.Fin)
        Write-Linea ' '

        $el = ('' + (Read-Host '   accion')).Trim().ToUpperInvariant()
        switch ($el) {
            ''  { $seguirV = $false }
            '0' { $seguirV = $false }
            '8' { }
            '1' {
                Clear-PantallaInteractiva; Write-CabeceraDeVista -Titulo '[3] > [1] VALIDACION 3 SITIOS'
                Write-Anuncio -Que 'Validacion del laboratorio en D:, nodo SMB y disco frio' `
                    -Detalle 'Solo lectura (~3 s). Verifica presencia, tamano, fecha y vigencia de manifiestos.'
                Write-Linea ' '
                & "$nucleo\laboratorio.ps1" @comunes -Accion Validar
                [void](Wait-CierreDeVista -EtiquetaVolver 'continuar en Laboratorio VM')
            }
            '2' {
                Clear-PantallaInteractiva; Write-CabeceraDeVista -Titulo '[3] > [2] VALIDACION PROFUNDA DEL DISCO FRIO'
                Write-Anuncio -Que 'Validacion profunda contra manifiestos del disco frio' `
                    -Detalle 'Solo lectura. Relee fisicamente los bloques del USB (decenas de minutos).'
                if (Confirm-AccionDeTablero -Resumen 'Se releeran los discos virtuales en el disco frio para detectar degradacion de bits contra sus manifiestos.' -EtiquetaSi 'Iniciar lectura profunda') {
                    & "$nucleo\laboratorio.ps1" @comunes -Accion Validar -Profundo
                    [void](Wait-CierreDeVista -EtiquetaVolver 'continuar en Laboratorio VM')
                }
                $script:Ctx.CanceladaEnVista = $false
            }
            '3' {
                Clear-PantallaInteractiva; Write-CabeceraDeVista -Titulo '[3] > [3] SIMULACION AL NODO SMB'
                Invoke-OpcionDelTablero -Opcion 'LabSimular' -Configuracion $Configuracion -RutaConfiguracion $RutaConfiguracion -Interactivo
                [void](Wait-CierreDeVista -EtiquetaVolver 'continuar en Laboratorio VM')
            }
            '4' {
                Clear-PantallaInteractiva; Write-CabeceraDeVista -Titulo '[3] > [4] SIMULACION AL DISCO FRIO'
                Write-Anuncio -Que 'Vista previa de copia fria del laboratorio VM' `
                    -Detalle 'NO escribe nada en el disco USB. Mide bloques a copiar y espacio requerido.'
                $pf = & "$nucleo\laboratorio.ps1" @comunes -Accion PreviaFrio
                Show-GuardadoDelLaboratorio -Resultado $pf
                [void](Wait-CierreDeVista -EtiquetaVolver 'continuar en Laboratorio VM')
            }
            '5' {
                Clear-PantallaInteractiva; Write-CabeceraDeVista -Titulo '[3] > [5] SINCRONIZAR LABORATORIO AL NODO'
                Invoke-OpcionDelTablero -Opcion 'LabGuardar' -Configuracion $Configuracion -RutaConfiguracion $RutaConfiguracion -Interactivo
                if (-not $script:Ctx.CanceladaEnVista) { [void](Wait-CierreDeVista -EtiquetaVolver 'continuar en Laboratorio VM') }
                $script:Ctx.CanceladaEnVista = $false
            }
            '6' {
                Clear-PantallaInteractiva; Write-CabeceraDeVista -Titulo '[3] > [6] COPIA FRIA DE VMS AL USB'
                Write-Anuncio -Que 'Copia fria diferencial de VMs al disco USB' `
                    -Detalle 'Escribe bloques en el disco frio (aditivo, reanudable, sin red). Requiere VMs apagadas.'
                $discoLab = Get-DiscoFrio -Configuracion $Configuracion 2>$null
                if (-not $discoLab) {
                    Show-DiscoFrioNoConectado -Configuracion $Configuracion
                    [void](Wait-CierreDeVista -EtiquetaVolver 'continuar en Laboratorio VM')
                }
                elseif (Confirm-AccionDeTablero -Resumen ('Se copiara el laboratorio VM por bloques al disco frio en {0} (firma {1}). Confirme que todas las VMs estan apagadas.' -f $discoLab.Raiz, $discoLab.Firma)) {
                    $gf = & "$nucleo\laboratorio.ps1" @comunes -Accion GuardarFrio -Confirm:$false
                    Show-GuardadoDelLaboratorio -Resultado $gf
                    [void](Wait-CierreDeVista -EtiquetaVolver 'continuar en Laboratorio VM')
                }
                $script:Ctx.CanceladaEnVista = $false
            }
            '7' {
                Invoke-VistaLecturaPi -Configuracion $Configuracion -RutaConfiguracion $RutaConfiguracion
            }
            default {
                Write-Veredicto -Nivel 'SIN DATOS' -Frase ('Opcion "{0}" no valida en esta vista.' -f $el)
                [void](Wait-CierreDeVista -EtiquetaVolver 'continuar en Laboratorio VM')
            }
        }
    }
}

function Invoke-VistaRecuperacion {
    <#
        .SYNOPSIS
            Subvista [4]: recuperacion ante desastres (_SEMILLA y estado DR).
    #>
    [CmdletBinding()]
    [OutputType([void])]
    param(
        [Parameter(Mandatory)][psobject] $Configuracion,
        [AllowEmptyString()][string] $RutaConfiguracion = ''
    )

    $p = $script:Paleta; $u = $script:Capacidades.Unicode
    $carpeta = Get-CarpetaDeEstado -Configuracion $Configuracion
    $seguirV = $true
    while ($seguirV) {
        Clear-PantallaInteractiva
        Write-CabeceraDeVista -Titulo '[4] RECUPERACION ANTE DESASTRES (_SEMILLA)'
        $est = Read-EstadoRespaldo -Carpeta $carpeta
        $sem = Format-Antiguedad -Momento ('' + $est['semilla_momento'])
        $dr = if ($est.ContainsKey('restauracion_probada') -and $est['restauracion_probada']) { '' + $est['restauracion_probada'] } else { 'pendiente (nunca registrada)' }
        Write-Linea ('   {0}Ultima verificacion  {1}  {2}' -f $p.Etiqueta, $p.Fin, $sem)
        Write-Linea ('   {0}Ensayo DR en limpio  {1}  {2}' -f $p.Etiqueta, $p.Fin, $dr)
        Write-Linea ' '
        Write-Linea (Get-Regla -Paleta $p -Unicode $u -Ancho $script:AnchoTablero)
        Write-Linea ('   {0}[1]{1} Verificar integridad de _SEMILLA y RESTAURAR.ps1 en el nodo' -f ($p.Fuerte + $p.Valor), $p.Fin)
        Write-Linea ('   {0}[2]{1} Regenerar _SEMILLA en el nodo (sin llaves privadas)' -f ($p.Fuerte + $p.Valor), $p.Fin)
        Write-Linea ('   {0}[3]{1} Actualizar estado' -f ($p.Fuerte + $p.Valor), $p.Fin)
        Write-Linea ('   {0}[0]{1} Volver a la consola' -f ($p.Fuerte + $p.Valor), $p.Fin)
        Write-Linea ' '

        $el = ('' + (Read-Host '   accion')).Trim().ToUpperInvariant()
        switch ($el) {
            ''  { $seguirV = $false }
            '0' { $seguirV = $false }
            '3' { }
            '1' {
                Clear-PantallaInteractiva; Write-CabeceraDeVista -Titulo '[4] > [1] VERIFICACION DE _SEMILLA'
                Invoke-OpcionDelTablero -Opcion 'SemillaProbar' -Configuracion $Configuracion -RutaConfiguracion $RutaConfiguracion -Interactivo
                [void](Wait-CierreDeVista -EtiquetaVolver 'continuar en Recuperacion')
            }
            '2' {
                Clear-PantallaInteractiva; Write-CabeceraDeVista -Titulo '[4] > [2] REGENERAR _SEMILLA'
                Invoke-OpcionDelTablero -Opcion 'SemillaRegenerar' -Configuracion $Configuracion -RutaConfiguracion $RutaConfiguracion -Interactivo
                if (-not $script:Ctx.CanceladaEnVista) { [void](Wait-CierreDeVista -EtiquetaVolver 'continuar en Recuperacion') }
                $script:Ctx.CanceladaEnVista = $false
            }
            default {
                Write-Veredicto -Nivel 'SIN DATOS' -Frase ('Opcion "{0}" no valida en esta vista.' -f $el)
                [void](Wait-CierreDeVista -EtiquetaVolver 'continuar en Recuperacion')
            }
        }
    }
}

function Invoke-VistaProgramacion {
    <#
        .SYNOPSIS
            Subvista [5]: unifica estado de tareas/integridad del motor y
            gestion de ventanas de ejecucion.
    #>
    [CmdletBinding()]
    [OutputType([void])]
    param(
        [Parameter(Mandatory)][psobject] $Configuracion,
        [AllowEmptyString()][string] $RutaConfiguracion = ''
    )

    $p = $script:Paleta; $u = $script:Capacidades.Unicode
    $seguirV = $true
    while ($seguirV) {
        Clear-PantallaInteractiva
        Write-CabeceraDeVista -Titulo '[5] PROGRAMACION Y SERVICIO'
        Show-VeredictoDelAutomatismo
        Write-Linea ' '
        Write-Linea (Get-Regla -Paleta $p -Unicode $u -Ancho $script:AnchoTablero)
        Write-Linea ('   {0}[1]{1} Administrar ventanas de ejecucion y umbral de alerta' -f ($p.Fuerte + $p.Valor), $p.Fin)
        Write-Linea ('   {0}[2]{1} Re-evaluar servicio' -f ($p.Fuerte + $p.Valor), $p.Fin)
        Write-Linea ('   {0}[0]{1} Volver a la consola' -f ($p.Fuerte + $p.Valor), $p.Fin)
        Write-Linea ' '

        $el = ('' + (Read-Host '   accion')).Trim().ToUpperInvariant()
        switch ($el) {
            ''  { $seguirV = $false }
            '0' { $seguirV = $false }
            '2' { }
            '1' {
                $null = Show-Reparto -Configuracion $Configuracion -RutaConfiguracion $RutaConfiguracion
            }
            default {
                Write-Veredicto -Nivel 'SIN DATOS' -Frase ('Opcion "{0}" no valida en esta vista.' -f $el)
                [void](Wait-CierreDeVista -EtiquetaVolver 'continuar en Programacion')
            }
        }
    }
}

function Invoke-VistaDiagnostico {
    <#
        .SYNOPSIS
            Subvista [6]: cola completa de alertas priorizadas, carpetas sin
            clasificar, estado completo y eventos recientes del registro diario.
    #>
    [CmdletBinding()]
    [OutputType([void])]
    param(
        [Parameter(Mandatory)][psobject] $Configuracion,
        [AllowEmptyString()][string] $RutaConfiguracion = ''
    )

    $p = $script:Paleta; $u = $script:Capacidades.Unicode
    $seguirV = $true
    while ($seguirV) {
        Clear-PantallaInteractiva
        Write-CabeceraDeVista -Titulo '[6] DIAGNOSTICO E INVENTARIO'
        $dDiag = Get-DatosDeVentana -Configuracion $Configuracion
        Write-Linea ('   {0}ALERTAS PRIORIZADAS ({1}){2}' -f ($p.Fuerte + $p.Titulo), @($dDiag.Alertas).Count, $p.Fin)
        if (@($dDiag.Alertas).Count -eq 0) {
            Write-Linea ('     {0}0 incidencias activas.{1}' -f $p.Verde, $p.Fin)
        }
        else {
            foreach ($al in @($dDiag.Alertas)) {
                $col = if ($al.Nivel -eq 'Critico') { $p.Rojo } else { $p.Ambar }
                $etiq = if ($al.Nivel -eq 'Critico') { 'CRITICO    ' } else { 'ADVERTENCIA' }
                Write-Linea ('     {0}{1}{2}  [{3}] {4}' -f $col, $etiq, $p.Fin, $al.Opcion, $al.Mensaje)
            }
        }
        Write-Linea ' '
        Write-Linea (Get-Regla -Paleta $p -Unicode $u -Ancho $script:AnchoTablero)
        Write-Linea ('   {0}[1]{1} Carpetas sin clasificar en contenedores' -f ($p.Fuerte + $p.Valor), $p.Fin)
        Write-Linea ('   {0}[2]{1} Registro de estado completo (ESTADO.txt)' -f ($p.Fuerte + $p.Valor), $p.Fin)
        Write-Linea ('   {0}[3]{1} Ultimos 20 eventos del registro de hoy' -f ($p.Fuerte + $p.Valor), $p.Fin)
        Write-Linea ('   {0}[4]{1} Actualizar diagnostico' -f ($p.Fuerte + $p.Valor), $p.Fin)
        Write-Linea ('   {0}[0]{1} Volver a la consola' -f ($p.Fuerte + $p.Valor), $p.Fin)
        Write-Linea ' '

        $el = ('' + (Read-Host '   accion')).Trim().ToUpperInvariant()
        switch ($el) {
            ''  { $seguirV = $false }
            '0' { $seguirV = $false }
            '4' { }
            '1' {
                Clear-PantallaInteractiva; Write-CabeceraDeVista -Titulo '[6] > [1] CARPETAS SIN CLASIFICAR'
                Invoke-OpcionDelTablero -Opcion 'HuerfanosVer' -Configuracion $Configuracion -RutaConfiguracion $RutaConfiguracion -Interactivo
                [void](Wait-CierreDeVista -EtiquetaVolver 'continuar en Diagnostico')
            }
            '2' {
                Clear-PantallaInteractiva; Write-CabeceraDeVista -Titulo '[6] > [2] ESTADO COMPLETO'
                Invoke-OpcionDelTablero -Opcion 'EstadoCompleto' -Configuracion $Configuracion -RutaConfiguracion $RutaConfiguracion -Interactivo
                [void](Wait-CierreDeVista -EtiquetaVolver 'continuar en Diagnostico')
            }
            '3' {
                Clear-PantallaInteractiva; Write-CabeceraDeVista -Titulo '[6] > [3] ULTIMOS EVENTOS DE HOY'
                $carpLog = Get-CarpetaDeRegistro -Configuracion $Configuracion
                $archLog = Join-Path $carpLog ('respaldo-{0:yyyy-MM-dd}.log' -f (Get-Date))
                if (Test-Path -LiteralPath $archLog -PathType Leaf) {
                    $ultimas = @(Get-Content -LiteralPath $archLog -Encoding UTF8 -Tail 20 -ErrorAction SilentlyContinue)
                    foreach ($l in $ultimas) { Write-Linea ('   ' + $l) }
                    Write-Veredicto -Nivel 'OK' -Frase ('Mostradas {0} linea(s) recientes de {1}.' -f $ultimas.Count, (Split-Path $archLog -Leaf))
                }
                else {
                    Write-Veredicto -Nivel 'SIN DATOS' -Frase ('No existe registro para hoy en {0}.' -f $carpLog)
                }
                [void](Wait-CierreDeVista -EtiquetaVolver 'continuar en Diagnostico')
            }
            default {
                Write-Veredicto -Nivel 'SIN DATOS' -Frase ('Opcion "{0}" no valida en esta vista.' -f $el)
                [void](Wait-CierreDeVista -EtiquetaVolver 'continuar en Diagnostico')
            }
        }
    }
}

function Invoke-OpcionDelTablero {
    <#
        .SYNOPSIS
            Ejecuta una opcion del menu.
        .DESCRIPTION
            ES UNA FUNCION, Y NO EL CUERPO DEL BUCLE, POR POWERSHELL 7 (ADR-0097).
            El tablero de 7 solo pinta: cada opcion la corre un PowerShell 5.1
            aparte con -Opcion, que llega aqui. El motor esta escrito y probado
            contra 5.1.

            UN FALLO DENTRO DE UNA OPCION NO PUEDE CERRAR LA VENTANA. Se cuenta y
            se vuelve al menu: el dia que algo va mal es justo el dia que hace
            falta el tablero.
        .PARAMETER Opcion
            Lo que se tecleo, ya en mayusculas.
        .PARAMETER Configuracion
            El objeto de configuracion vigente.
        .PARAMETER RutaConfiguracion
            La ruta, si se paso una; vacia para la de 3-Config.
        .PARAMETER Interactivo
            Activa confirmaciones de 1 paso en acciones de escritura y reintento
            guiado cuando el disco frio no esta conectado.
    #>
    [CmdletBinding()]
    param(
        [Parameter(Mandatory)][AllowEmptyString()][string] $Opcion,
        [Parameter(Mandatory)][psobject] $Configuracion,
        [AllowEmptyString()][string] $RutaConfiguracion = '',
        [switch] $Interactivo
    )

    $comunes = @{}
    if ($RutaConfiguracion) { $comunes['RutaConfiguracion'] = $RutaConfiguracion }

    try {
        switch ($Opcion.ToUpperInvariant()) {
            '1' {
                Invoke-VistaNodo -Configuracion $Configuracion -RutaConfiguracion $RutaConfiguracion
            }
            '2' {
                Invoke-VistaDiscoFrio -Configuracion $Configuracion -RutaConfiguracion $RutaConfiguracion
            }
            '3' {
                Invoke-VistaLaboratorio -Configuracion $Configuracion -RutaConfiguracion $RutaConfiguracion
            }
            '4' {
                Invoke-VistaRecuperacion -Configuracion $Configuracion -RutaConfiguracion $RutaConfiguracion
            }
            '5' {
                Invoke-VistaProgramacion -Configuracion $Configuracion -RutaConfiguracion $RutaConfiguracion
            }
            '6' {
                Invoke-VistaDiagnostico -Configuracion $Configuracion -RutaConfiguracion $RutaConfiguracion
            }
            'NODOCOPIAR' {
                # NO HAY NADA QUE AUTORIZAR DESDE AQUI (ADR-0085). Cuando
                # existia el freno, autorizarlo estaba deliberadamente fuera
                # del menu para que no fuera un clic. Retirada la capa 2, los
                # abortos que quedan -centinela, origen inutilizable, deuda- no
                # se autorizan de ninguna manera, ni aqui ni en la consola.
                $puerta = Test-HayCorridaEnCurso -Configuracion $Configuracion
                if ($puerta.EnCurso) {
                    Write-Veredicto -Nivel 'ATENCION' -Frase $puerta.Detalle
                }
                else {
                    Write-Anuncio -Que 'Copia al NODO' `
                        -Detalle 'Escribe en el nodo. Migra renombres NN_ reconocidos por centinela y omite raices sin cambios.'
                    if ($Interactivo -and -not (Confirm-AccionDeTablero -Resumen 'Se copiaran al nodo las raices con cambios y se verificaran las huellas de lo copiado.')) {
                        return
                    }
                    $r1 = & "$nucleo\respaldo.ps1" @comunes -Confirm:$false
                    if ($null -eq $r1) {
                        Write-Veredicto -Nivel 'SIN DATOS' -Frase 'La corrida no devolvio resultado.'
                    }
                    elseif ($r1.Abortada) {
                        Write-Veredicto -Nivel 'FALLO' -Frase ('' + $r1.Motivo) `
                            -Accion 'No se escribio nada. Revisa el registro de hoy en LOCALAPPDATA\NasRespaldo\registro.'
                    }
                    else {
                        $fallos1 = @($r1.Copias | Where-Object { -not $_.Correcto }).Count
                        $copiados1 = 0
                        foreach ($c1 in @($r1.Copias)) { $copiados1 += [int]$c1.NumACopiar }
                        $ren1 = if ($r1.PSObject.Properties.Name -contains 'Renombres') { @($r1.Renombres).Count } else { 0 }
                        $notaRen = if ($ren1 -gt 0) { ', {0} renombre(s) migrado(s)' -f $ren1 } else { '' }
                        if ($fallos1 -gt 0) {
                            Write-Veredicto -Nivel 'FALLO' -Frase ('{0} de {1} raices fallaron al copiar ({2} archivos copiados{3}).' -f $fallos1, @($r1.Copias).Count, $copiados1, $notaRen) `
                                -Accion 'Revisa el registro de hoy para ver que raiz devolvio error de robocopy.'
                        }
                        else {
                            Write-Veredicto -Nivel 'OK' -Frase ('{0} raices al dia en el nodo, {1} archivos copiados{2}.' -f @($r1.Copias).Count, $copiados1, $notaRen)
                        }
                    }
                }
            }
            'NODOSIMULAR' {
                Write-Anuncio -Que 'Simulacion de la copia al NODO' `
                    -Detalle 'NO escribe nada. Mide que cambiaria en las raices actuales. ~1 min.'
                $r2 = & "$nucleo\respaldo.ps1" @comunes -SoloSimular -Confirm:$false
                Show-VeredictoDeSimulacion -Resultado $r2 -Configuracion $Configuracion
            }
            'NODOVERIFICAR' {
                Write-Anuncio -Que 'Cotejar lo guardado en el NODO contra el original de este equipo' `
                    -Detalle 'NO escribe nada. Lee archivos ENTEROS por la red: tarda MINUTOS y no esta colgado.'
                Show-VeredictoDelCotejo -Resultado (& "$nucleo\verificar.ps1" @comunes)
            }
            'ESTADOCOMPLETO' {
                Write-Anuncio -Que 'Estado completo del cliente' `
                    -Detalle 'Solo lee ESTADO.txt y las raices locales vigentes.'
                $est4 = Read-EstadoRespaldo -Carpeta (Get-CarpetaDeEstado -Configuracion $Configuracion)
                Show-Texto -Objeto $est4 -Vacio 'No hay estado escrito todavia.'
                $raices4 = @(Get-RaicesDeRespaldo -Configuracion $Configuracion)
                $nivel4 = switch ('' + $est4['estado']) { 'Protegido' { 'OK' } 'Falla' { 'FALLO' } 'SinDatos' { 'SIN DATOS' } default { 'ATENCION' } }
                Write-Veredicto -Nivel $nivel4 -Frase ('Estado {0}: {1} ({2} raices activas en disco).' -f ('' + $est4['estado']), ('' + $est4['detalle']), $raices4.Count)
            }
            'SEMILLAREGENERAR' {
                $destino = '{0}\{1}\{2}\_SEMILLA' -f `
                    $Configuracion.destinos.nodo.unc.TrimEnd('\'),
                    $Configuracion.destinos.nodo.raiz,
                    $Configuracion.destinos.nodo.prefijoEquipo
                Write-Anuncio -Que 'Regenerar la semilla de arranque en el NODO' `
                    -Detalle ('Escribe listas de programas/entorno y RESTAURAR.ps1 en {0}. Menos de 1 MB.' -f $destino)
                if ($Interactivo -and -not (Confirm-AccionDeTablero -Resumen ('Se regenerara _SEMILLA en {0} (sin llaves SSH).' -f $destino))) {
                    return
                }
                $r5 = & "$nucleo\semilla.ps1" -Destino $destino -Confirm:$false
                if ($null -eq $r5) {
                    Write-Veredicto -Nivel 'SIN DATOS' -Frase 'La semilla no devolvio resultado.'
                }
                else {
                    Write-Linea ' '
                    Show-Texto -Objeto @($r5.Escritas) -Vacio 'No se escribio ninguna pieza.'
                    Write-Linea ' '
                    $nEsc5 = @($r5.Escritas).Count
                    $nFal5 = @($r5.Fallidas).Count
                    if ($nFal5 -gt 0) {
                        Write-Veredicto -Nivel 'ATENCION' -Frase ('Semilla regenerada con {0} piezas ({1:N0} B) y {2} pieza(s) fallida(s).' -f $nEsc5, $r5.Bytes, $nFal5) `
                            -Accion 'Revisa MANIFIESTO.txt en _SEMILLA o el registro de hoy para ver que pieza falto.'
                    }
                    else {
                        Write-Veredicto -Nivel 'OK' -Frase ('Semilla regenerada en el nodo: {0} piezas, {1:N0} bytes, 0 fallos (RESTAURAR.ps1 listo).' -f $nEsc5, $r5.Bytes)
                    }
                }
            }
            'SERVICIOVER' {
                Write-Anuncio -Que 'Estado del automatismo' `
                    -Detalle 'Solo lee. Comprueba las dos tareas y que las piezas del motor sigan ahi.'
                Show-VeredictoDelAutomatismo
            }
            'DISCOCOPIAR' {
                # Las pasadas completas (equipo, nodo y laboratorio). Si el nodo
                # no responde o hay VMs encendidas, queda declarada INCOMPLETA.
                $puerta = Test-HayCorridaEnCurso -Configuracion $Configuracion
                if ($puerta.EnCurso) {
                    Write-Veredicto -Nivel 'ATENCION' -Frase $puerta.Detalle
                }
                else {
                    Write-Anuncio -Que 'Copia al DISCO FRIO (tres pasadas, siempre aditiva)' `
                        -Detalle 'Escribe en el disco USB (equipo, nodo y laboratorio de D:). Nunca borra.'
                    if ($Interactivo) {
                        $disco7 = Get-DiscoFrio -Configuracion $Configuracion 2>$null
                        if (-not $disco7) {
                            Show-DiscoFrioNoConectado -Configuracion $Configuracion
                            return
                        }
                        $libres7 = Format-Espacio -Bytes (Get-EspacioLibre -Ruta $disco7.Raiz)
                        $resumen7 = 'Disco frio listo en {0} (firma {1}{2}). Se ejecutaran las pasadas aditivas (equipo -> disco, nodo -> disco y el laboratorio de D: por bloques; con VMs encendidas el laboratorio se difiere).' -f
                            $disco7.Raiz, $disco7.Firma, $(if ($libres7) { ', ' + $libres7 } else { '' })
                        if (-not (Confirm-AccionDeTablero -Resumen $resumen7)) {
                            return
                        }
                    }
                    $rd = & "$nucleo\disco.ps1" @comunes -Confirm:$false
                    if ($rd.Abortada) {
                        Write-Veredicto -Nivel 'FALLO' -Frase ('' + $rd.Motivo)
                    }
                    elseif (@($rd.Fallidas).Count -gt 0) {
                        # ANTES QUE "COMPLETA". El 2026-10-18 salia "COMPLETA"
                        # justo debajo de "FALLO C:\dev".
                        Write-Veredicto -Nivel 'FALLO' -Frase ('COPIA FRIA CON FALLOS: {0} raices sin dar por buenas ({1}).' -f @($rd.Fallidas).Count, (@($rd.Fallidas) -join ', ')) `
                            -Accion 'Lo demas se copio; revisa el registro de hoy.'
                    }
                    elseif (-not $rd.Completa) {
                        Write-Veredicto -Nivel 'ATENCION' -Frase ('COPIA FRIA INCOMPLETA: ' + $rd.MotivoIncompleta) `
                            -Accion 'Resuelve eso (nodo respondiendo, VMs apagadas) y vuelve a ejecutar [2] -> [2]; lo ya copiado no se repite.'
                    }
                    elseif ($rd.ArchivosCopiados -eq 0) {
                        Write-Veredicto -Nivel 'OK' -Frase 'Copia fria AL DIA: todas las pasadas corrieron y no habia nada nuevo que copiar.'
                    }
                    else {
                        Write-Veredicto -Nivel 'OK' -Frase ('Copia fria COMPLETA: {0} archivos nuevos copiados.' -f $rd.ArchivosCopiados)
                    }
                }
            }
            'DISCOSIMULAR' {
                Write-Anuncio -Que 'Vista previa de la copia al DISCO FRIO' `
                    -Detalle 'NO escribe nada. Mide que copiaria desde el equipo, el nodo y el laboratorio.'
                if ($Interactivo) {
                    $disco8 = Get-DiscoFrio -Configuracion $Configuracion 2>$null
                    if (-not $disco8) {
                        Show-DiscoFrioNoConectado -Configuracion $Configuracion
                        return
                    }
                }
                $r8 = & "$nucleo\disco.ps1" @comunes -SoloSimular -Confirm:$false
                if ($null -eq $r8) {
                    Write-Veredicto -Nivel 'SIN DATOS' -Frase 'La simulacion no devolvio nada.'
                }
                elseif ($r8.Abortada) {
                    Write-Veredicto -Nivel 'ATENCION' -Frase ('' + $r8.Motivo)
                }
                else {
                    $todas8 = @(@($r8.PasadaEquipo) + @($r8.PasadaNodo) | Where-Object { $null -ne $_ })
                    Show-Texto -Objeto ($todas8 | Select-Object Origen, NumACopiar, Codigo, Correcto)
                    $n8 = 0; foreach ($x8 in $todas8) { $n8 += [int]$x8.NumACopiar }
                    $nivel8 = if ($r8.Completa) { 'OK' } else { 'ATENCION' }
                    Write-Veredicto -Nivel $nivel8 -Frase ('Vista previa: copiaria {0} archivos en {1} raices (completa: {2}).' -f $n8, $todas8.Count, $r8.Completa)
                }
            }
            { $_ -in @('HUERFANOSVER', 'H') } {
                Write-Anuncio -Que 'Carpetas sin clasificar en contenedores' `
                    -Detalle 'Solo lee en local (milisegundos). Excluye junctions de Windows.'
                Show-Huerfanos -Configuracion $Configuracion
            }
            'DISCOAUDITAR' {
                # Busca lo que una copia cortada dejo a medias.
                Write-Anuncio -Que 'Cotejar el DISCO FRIO contra el equipo y el nodo' `
                    -Detalle 'NO escribe nada. Busca archivos cortados o a medias en las pasadas (puede tardar).'
                if ($Interactivo) {
                    $disco9 = Get-DiscoFrio -Configuracion $Configuracion 2>$null
                    if (-not $disco9) {
                        Show-DiscoFrioNoConectado -Configuracion $Configuracion
                        return
                    }
                }
                $r9 = & "$nucleo\verificar.ps1" @comunes -RevisarDisco -TamanoMuestra 0
                Show-RevisionDelDisco -Revision $r9.DiscoEnVuelo
            }
            'SEMILLAPROBAR' {
                Write-Anuncio -Que 'Ensayo de restauracion (semilla en el NODO)' `
                    -Detalle 'NO escribe nada. Comprueba que _SEMILLA y RESTAURAR.ps1 existen y se leen desde el nodo.'
                Show-PruebaDeRestauracion -Configuracion $Configuracion
            }
            # EL LABORATORIO DE D: (seccion 5.2, ADR-0104). Va en LABORATORIO.lock
            # y no pasa por Test-HayCorridaEnCurso: no comparte destino con el
            # motor, y laboratorio.ps1 ya se niega si hay otro guardado suyo.
            { $_ -in @('LABVER', 'L') } {
                Write-Anuncio -Que 'El laboratorio de D:, VM por VM' `
                    -Detalle 'Solo lee: D: en local y el nodo por la red. Segundos.'
                Show-Laboratorio -Vista (& "$nucleo\laboratorio.ps1" @comunes -Accion Ver)
            }
            { $_ -in @('LABSIMULAR', 'V') } {
                Write-Anuncio -Que 'Vista previa del laboratorio al nodo' `
                    -Detalle 'NO escribe nada. Lee en D: los discos que cambiaron para medir cuanto viajaria. Con las VMs apagadas.'
                Show-GuardadoDelLaboratorio -Resultado (& "$nucleo\laboratorio.ps1" @comunes -Accion Previa)
            }
            { $_ -in @('LABGUARDAR', 'G') } {
                Write-Anuncio -Que 'Laboratorio al nodo: solo lo que cambio' `
                    -Detalle 'Escribe en el nodo. Las VMs APAGADAS todo el rato; si cambio poco, son minutos.'
                if ($Interactivo) {
                    $carpetaG = Get-CarpetaDeEstado -Configuracion $Configuracion
                    $lectG = Read-LecturaDelLaboratorio -Carpeta $carpetaG
                    if ($lectG) {
                        Write-Veredicto -Nivel 'ATENCION' -Frase 'Hay una lectura en la Pi sin recoger: el guardado no puede escribir hasta que termine o se cancele.' `
                            -Accion 'Entra en [3] -> [7] para ver su avance en vivo, recogerla o cancelarla.'
                        return
                    }
                    $marcaG = Get-MarcaDeCorrida -Carpeta $carpetaG -Archivo 'LABORATORIO.lock'
                    if ($marcaG.Existe -and -not $marcaG.Vieja) {
                        Write-Veredicto -Nivel 'ATENCION' -Frase ('Ya hay un guardado del laboratorio en marcha (pid {0}, hace {1} min).' -f $marcaG.Pid, $marcaG.Minutos)
                        return
                    }
                    if (-not (Confirm-AccionDeTablero -Resumen 'Se enviaran al nodo solo los bloques cambiados de D:\VMware_LAB. Asegurate de tener las VMs apagadas.')) {
                        return
                    }
                }
                Show-GuardadoDelLaboratorio -Resultado (& "$nucleo\laboratorio.ps1" @comunes -Accion Guardar -Confirm:$false)
            }
            { $_ -in @('LABLEERPI', 'T') } {
                if ($Interactivo) {
                    Invoke-VistaLecturaPi -Configuracion $Configuracion -RutaConfiguracion $RutaConfiguracion
                }
                else {
                    Write-Anuncio -Que 'Leer el laboratorio entero en la Pi' `
                        -Detalle 'La Pi relee del disco del nodo cada disco virtual: horas. La primera vez la lanza; despues dice como va o la recoge. Puedes usar las VMs.'
                    Show-LecturaDelLaboratorio -Resultado (& "$nucleo\laboratorio.ps1" @comunes -Accion Leer -Confirm:$false)
                }
            }
            default { Write-Warning 'Opcion no reconocida.' }
        }
    }
    catch {
        Write-Veredicto -Nivel 'FALLO' -Frase ('La opcion fallo: {0}' -f $_.Exception.Message) `
            -Accion 'Revisa el registro de hoy en LOCALAPPDATA\NasRespaldo\registro.'
        Write-Verbose ('' + $_.ScriptStackTrace)
    }
}

function Invoke-VistaDeOpcion {
    <#
        .SYNOPSIS
            Ciclo de vida completo de una subvista interactiva: pantalla limpia,
            cabecera con ruta, ejecucion, pausa de cierre y refresco opcional.
        .PARAMETER Opcion
            Opcion elegida del menu.
        .PARAMETER Configuracion
            Configuracion vigente.
        .PARAMETER RutaConfiguracion
            Ruta de configuracion, si no es la de por omision.
    #>
    [CmdletBinding()]
    [OutputType([void])]
    param(
        [Parameter(Mandatory)][AllowEmptyString()][string] $Opcion,
        [Parameter(Mandatory)][psobject] $Configuracion,
        [AllowEmptyString()][string] $RutaConfiguracion = ''
    )

    $meta = Get-MetaDeOpcion -Opcion $Opcion
    if (-not $meta) {
        Write-Warning 'Opcion no reconocida.'
        return
    }

    # [A] y [T] gobiernan su propia pantalla, cabecera y bucle de vuelta.
    if ($meta.PropiaVista) {
        Invoke-OpcionDelTablero -Opcion $meta.Opcion -Configuracion $Configuracion -RutaConfiguracion $RutaConfiguracion -Interactivo
        return
    }

    $seguirVista = $true
    while ($seguirVista) {
        Reset-EstadoDeVista -Contexto $script:Ctx

        Clear-PantallaInteractiva
        Write-CabeceraDeVista -Titulo $meta.Titulo
        Invoke-OpcionDelTablero -Opcion $meta.Opcion -Configuracion $Configuracion -RutaConfiguracion $RutaConfiguracion -Interactivo

        if ($script:Ctx.CanceladaEnVista) {
            # Confirmacion cancelada con Enter/V: volver al tablero de inmediato.
            return
        }

        $puedeRefrescar = $meta.Refrescable -or $script:Ctx.PermitirReintentoEnVista
        $etiqR = if ($script:Ctx.PermitirReintentoEnVista) { $script:Ctx.EtiquetaReintentoEnVista } else { 'actualizar' }
        $paso = Wait-CierreDeVista -Refrescable:$puedeRefrescar -EtiquetaRefrescar $etiqR
        if ($paso -ne 'Refrescar') { $seguirVista = $false }
    }
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

# UNA SOLA OPCION, SIN MENU. Es como el tablero de PowerShell 7 pide cada una a
# un PowerShell 5.1 (ver Invoke-OpcionDelTablero). Con -Interactivo usa el
# ciclo completo de subvista (cabecera, confirmacion, refresco y pausa).
if ($PSBoundParameters.ContainsKey('Opcion')) {
    if ($Interactivo) {
        Invoke-VistaDeOpcion -Opcion $Opcion.ToUpperInvariant() -Configuracion $configuracion -RutaConfiguracion ('' + $rutaConfig)
    }
    else {
        Invoke-OpcionDelTablero -Opcion $Opcion.ToUpperInvariant() -Configuracion $configuracion -RutaConfiguracion ('' + $rutaConfig)
    }
    return
}

# EN POWERSHELL 7 ESTA VENTANA SOLO PINTA (ADR-0097). Las opciones corren en un
# PowerShell 5.1 que hereda esta misma consola: se ve y se teclea igual.
$enCore = ($PSVersionTable.PSVersion.Major -ge 6)
$powershell51 = Join-Path $env:SystemRoot 'System32\WindowsPowerShell\v1.0\powershell.exe'

$seguir = $true
while ($seguir) {
    Clear-PantallaInteractiva
    $anchoDibujo = Get-AnchoDeConsola
    $datosPantalla = Get-DatosDeVentana -Configuracion $configuracion

    # SI EL PANEL RICO FALLA, SE VUELVE A LA VENTANA DE SIEMPRE Y SE QUEDA ASI
    # (ADR-0097): una version nueva del modulo no puede dejar al tablero sin
    # ventana el dia que hace falta.
    $pintada = $false
    if ($script:PanelRico) {
        try {
            Show-VentanaRica -Datos $datosPantalla
            $pintada = $true
        }
        catch {
            Write-Warning ('El panel con Spectre fallo y se vuelve a la ventana de siempre: {0}' -f $_.Exception.Message)
            $script:PanelRico = $false
        }
    }
    if (-not $pintada) { Show-Ventana -Configuracion $configuracion -Datos $datosPantalla }

    if ($script:Ctx.AvisoMenu) {
        $pAviso = $script:Paleta
        Write-Linea ('  {0}! {1}{2}' -f $pAviso.Ambar, $script:Ctx.AvisoMenu, $pAviso.Fin)
        Write-Linea ' '
        $script:Ctx.AvisoMenu = ''
    }

    $operacionActiva = ($datosPantalla.ProgresoActivo -and $datosPantalla.ProgresoActivo.Activo) -or
        $datosPantalla.LecturaActiva -or ($datosPantalla.Estado -eq 'Copiando')
    $intervaloVivo = if ($operacionActiva) { 2 } else { 15 }
    $accion = Read-AccionEnVivo -IntervaloSegundos $intervaloVivo -AnchoInicial $anchoDibujo `
        -Etiqueta ('[1-6] abrir  [Enter] refrescar ({0}s)  [0] salir >' -f $intervaloVivo)

    if ($accion.Tipo -eq 'Salir') { $seguir = $false; continue }
    if ($accion.Tipo -eq 'Refrescar' -or $accion.Tipo -eq 'Redimension') {
        try { $configuracion = Get-ConfiguracionRespaldo @parametros }
        catch { $script:Ctx.AvisoMenu = 'No se pudo releer la configuracion: {0}' -f $_.Exception.Message }
        continue
    }

    $elegida = '' + $accion.Tecla
    $metaElegida = if ($elegida -match '^[1-6]$') { Get-MetaDeOpcion -Opcion $elegida } else { $null }
    if (-not $metaElegida) {
        $script:Ctx.AvisoMenu = 'Opcion "{0}" no reconocida. Use [1]-[6], [Enter] o [0].' -f $elegida
        continue
    }

    if ($enCore) {
        # Respuesta visual inmediata (<1 ms) mientras arranca el proceso hijo 5.1.
        Clear-PantallaInteractiva
        Write-CabeceraDeVista -Titulo $metaElegida.Titulo
        Write-Linea ('   {0}Abriendo vista...{1}' -f $script:Paleta.Tenue, $script:Paleta.Fin)

        $argumentos = @('-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', $PSCommandPath, '-Opcion', $elegida, '-Interactivo')
        if ($rutaConfig) { $argumentos += @('-RutaConfiguracion', $rutaConfig) }
        $modPrevio = $env:PSModulePath
        try {
            $env:PSModulePath = '{0}\WindowsPowerShell\Modules;{1}\System32\WindowsPowerShell\v1.0\Modules' -f $env:ProgramFiles, $env:SystemRoot
            & $powershell51 @argumentos
        }
        finally { $env:PSModulePath = $modPrevio }
    }
    else {
        Invoke-VistaDeOpcion -Opcion $elegida -Configuracion $configuracion -RutaConfiguracion ('' + $rutaConfig)
    }

    try { $configuracion = Get-ConfiguracionRespaldo @parametros }
    catch { $script:Ctx.AvisoMenu = 'No se pudo releer la configuracion: {0}' -f $_.Exception.Message }
}
