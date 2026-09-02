#Requires -Version 5.1
<#
    estilo.ps1  -  Como se ve el tablero. Sin logica de respaldo, sin decisiones.

    Contrato: 30_CLIENTE_RESPALDO.md secciones 10.1 y 10.2.

    POR QUE EXISTE UN ARCHIVO SOLO PARA ESTO. El tablero tenia el dibujo metido
    entre las llamadas al motor. Mezclar las dos cosas hace que cambiar como se
    ve obligue a tocar el archivo que lanza copias, y ese es el ultimo archivo
    del proyecto que conviene tocar por gusto.

    DOS REGLAS QUE VIENEN DE LA SECCION 10.2 Y NO SON DECORACION:

      1. EL COLOR NUNCA VA SOLO. Cada estado lleva simbolo Y color, porque el
         color solo no basta (WCAG 2.2 1.4.1) y este repositorio ya tiene esa
         preocupacion registrada para el semaforo de la web. En una terminal en
         blanco y negro, o para quien no distingue rojo de verde, el simbolo es
         lo unico que queda.

      2. SE DEGRADA, NO SE ROMPE. Si la consola no admite secuencias de color,
         no se escribe ni una: se veria basura en vez de un tablero. Si no
         admite UTF-8, los marcos y los simbolos caen a ASCII. Un tablero que
         solo funciona en la terminal buena es un tablero que un dia no se lee.

    LOS .ps1 DE ESTE PROYECTO SON ASCII PURO (ver comun.ps1). Los caracteres de
    marco y los simbolos NO se escriben literales: se piden por punto de codigo
    con [char], asi que este archivo sigue siendo ASCII y aun asi dibuja marcos.

    Uso: se carga con punto desde el tablero.
        . "$PSScriptRoot\estilo.ps1"
#>

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$script:AnchoTablero = 70

function Initialize-Consola {
    <#
        .SYNOPSIS
            Prepara la consola y dice QUE se puede usar en ella.
        .DESCRIPTION
            No supone nada: pregunta. Devuelve un objeto con dos permisos,
            Color y Unicode, y el resto del archivo se limita a obedecerlos.

            Con la salida redirigida -una tuberia, un archivo, una captura- no
            hay consola que configurar y las dos respuestas son NO. Eso es lo
            correcto: los codigos de color en un archivo de texto son basura.
    #>
    [CmdletBinding()]
    [OutputType([psobject])]
    param()

    $color   = $false
    $unicode = $false

    try {
        [Console]::OutputEncoding = [System.Text.Encoding]::UTF8
        $unicode = $true
    }
    catch {
        $unicode = $false
    }

    try {
        if (-not ('NasRespaldo.Consola' -as [type])) {
            Add-Type -Namespace 'NasRespaldo' -Name 'Consola' -MemberDefinition @'
[System.Runtime.InteropServices.DllImport("kernel32.dll", SetLastError = true)]
public static extern System.IntPtr GetStdHandle(int nStdHandle);

[System.Runtime.InteropServices.DllImport("kernel32.dll", SetLastError = true)]
public static extern bool GetConsoleMode(System.IntPtr hConsoleHandle, out uint lpMode);

[System.Runtime.InteropServices.DllImport("kernel32.dll", SetLastError = true)]
public static extern bool SetConsoleMode(System.IntPtr hConsoleHandle, uint dwMode);
'@
        }

        # -11 es la salida estandar. 0x0004 es ENABLE_VIRTUAL_TERMINAL_PROCESSING:
        # PowerShell 5.1 no lo enciende solo, al contrario que PowerShell 7.
        $asa = [NasRespaldo.Consola]::GetStdHandle(-11)
        [uint32] $modo = 0
        if ([NasRespaldo.Consola]::GetConsoleMode($asa, [ref] $modo)) {
            $color = [NasRespaldo.Consola]::SetConsoleMode($asa, ($modo -bor 0x0004))
        }
    }
    catch {
        $color = $false
    }

    return [pscustomobject]@{ Color = $color; Unicode = $unicode }
}

function Get-Paleta {
    <#
        .SYNOPSIS
            Los codigos de color, o cadenas vacias si la consola no puede.
        .DESCRIPTION
            Devolver cadenas vacias en vez de no llamar a la funcion mantiene
            un solo camino de codigo: el tablero escribe siempre igual y aqui
            se decide si eso pinta algo o no.
        .PARAMETER Capacidades
            Lo que devuelve Initialize-Consola.
    #>
    [CmdletBinding()]
    [OutputType([hashtable])]
    param(
        [Parameter(Mandatory)][psobject] $Capacidades
    )

    if (-not $Capacidades.Color) {
        return @{
            Fin = ''; Tenue = ''; Fuerte = ''; Etiqueta = ''; Valor = ''; Titulo = ''
            Verde = ''; Ambar = ''; Rojo = ''; Gris = ''; Marco = ''
        }
    }

    # LOS COLORES NO SE INVENTAN AQUI: SON LOS DEL PANEL DEL NODO.
    # 10_CODIGO/internal/adaptadores/web/estatico/estilo.css declara el sistema
    # de la casa, y el tablero es otra ventana del MISMO producto. Dos paletas
    # distintas para el mismo NAS obligan a aprender dos idiomas de colores, y
    # el dia que importa se lee el equivocado.
    #
    #   --ok    #3fb950   --av    #d29922   --fa   #f85149   --nada #6b7280
    #   --tx    #e9ecf1   --tx2   #868d99   --tx3  #5b616b
    #   --ac    #b9cdea   --n5    #30343c
    #
    # Se usa color de 24 bits -ESC[38;2;R;G;B- y no los 16 basicos: los 16 los
    # remapea el tema de cada terminal, asi que "verde" acabaria siendo el verde
    # de otro. Aqui el verde es EXACTAMENTE el del panel. Cualquier consola que
    # admita VT -que es lo que Initialize-Consola acaba de encender- admite 24
    # bits; y si no admite color no se escribe ni una secuencia.
    $e = [char] 27
    return @{
        Fin      = "$e[0m"
        Fuerte   = "$e[1m"

        Marco    = "$e[38;2;48;52;60m"      # --n5, la linea estructural
        Titulo   = "$e[38;2;185;205;234m"   # --ac, los rotulos de bloque
        Etiqueta = "$e[38;2;134;141;153m"   # --tx2, el nombre del campo
        Valor    = "$e[38;2;233;236;241m"   # --tx, el contenido
        Tenue    = "$e[38;2;91;97;107m"     # --tx3, lo secundario de verdad

        Verde    = "$e[38;2;63;185;80m"     # --ok
        Ambar    = "$e[38;2;210;153;34m"    # --av
        Rojo     = "$e[38;2;248;81;73m"     # --fa
        Gris     = "$e[38;2;107;114;128m"   # --nada
    }
}

function Get-ReglaDeAviso {
    <#
        .SYNOPSIS
            La barra vertical del componente `aviso` del panel.
        .DESCRIPTION
            PORTAR UNA MAQUETA ES PORTAR SU SISTEMA DE COMPONENTES, no copiarle
            los colores. En el panel del nodo un estado no se pinta cambiandole
            el color al texto: se pinta con una CAJA CON REGLA LATERAL en el
            color semantico -`.aviso{border-left:2px solid}`, y `.mal`, `.bien`
            y `.ojo` solo cambian ese borde-. Aqui esa regla es un bloque medio
            a la izquierda de la linea de estado, y hace el mismo trabajo:
            marcar el bloque entero sin tenir la letra.
        .PARAMETER Unicode
            Si no, se cae a una barra ASCII.
    #>
    [CmdletBinding()]
    [OutputType([string])]
    param(
        [bool] $Unicode = $true
    )
    if (-not $Unicode) { return '|' }
    return [string][char] 0x258C   # medio bloque izquierdo
}

function Get-Simbolo {
    <#
        .SYNOPSIS
            El simbolo de un estado. LA FORMA, no el color.
        .DESCRIPTION
            Las formas son las de la seccion 10.2, las mismas del icono de la
            barra, para que la barra y el tablero no cuenten historias
            distintas: circulo protegido, circulo a medias copiando, triangulo
            atencion, rombo falla, circulo hueco sin datos.
        .PARAMETER Estado
            Uno de los cinco.
        .PARAMETER Unicode
            Si no, se devuelve la version ASCII.
    #>
    [CmdletBinding()]
    [OutputType([string])]
    param(
        [Parameter(Mandatory)]
        [ValidateSet('Protegido', 'Copiando', 'Atencion', 'Falla', 'SinDatos')]
        [string] $Estado,
        [bool] $Unicode = $true
    )

    if (-not $Unicode) {
        switch ($Estado) {
            'Protegido' { return '(*)' }
            'Copiando'  { return '(>)' }
            'Atencion'  { return '/!\' }
            'Falla'     { return '<X>' }
            default     { return '( )' }
        }
    }

    switch ($Estado) {
        'Protegido' { return ' ' + [char] 0x25CF + ' ' }   # circulo lleno
        'Copiando'  { return ' ' + [char] 0x25D0 + ' ' }   # circulo a medias
        'Atencion'  { return ' ' + [char] 0x25B2 + ' ' }   # triangulo
        'Falla'     { return ' ' + [char] 0x25C6 + ' ' }   # rombo
        default     { return ' ' + [char] 0x25CB + ' ' }   # circulo hueco
    }
}

function Get-ColorDeEstado {
    <#
        .SYNOPSIS
            El color de un estado. Acompana al simbolo, nunca lo sustituye.
        .PARAMETER Estado
            Uno de los cinco.
        .PARAMETER Paleta
            Lo que devuelve Get-Paleta.
    #>
    [CmdletBinding()]
    [OutputType([string])]
    param(
        [Parameter(Mandatory)][string] $Estado,
        [Parameter(Mandatory)][hashtable] $Paleta
    )

    switch ($Estado) {
        'Protegido' { return $Paleta.Verde }
        'Copiando'  { return $Paleta.Verde }
        'Atencion'  { return $Paleta.Ambar }
        'Falla'     { return $Paleta.Rojo }
        default     { return $Paleta.Gris }
    }
}

function Get-TrazoDeMarco {
    <#
        .SYNOPSIS
            Los caracteres del marco, en Unicode o en ASCII.
        .PARAMETER Unicode
            Si no, se devuelven guiones y barras.
    #>
    [CmdletBinding()]
    [OutputType([hashtable])]
    param(
        [bool] $Unicode = $true
    )

    if (-not $Unicode) {
        return @{
            Horizontal = '-'; Vertical = '|'
            SupIzq = '+'; SupDer = '+'; InfIzq = '+'; InfDer = '+'
            UnionIzq = '+'; UnionDer = '+'
        }
    }

    return @{
        Horizontal = [string][char] 0x2500
        Vertical   = [string][char] 0x2502
        SupIzq     = [string][char] 0x250C
        SupDer     = [string][char] 0x2510
        InfIzq     = [string][char] 0x2514
        InfDer     = [string][char] 0x2518
        UnionIzq   = [string][char] 0x251C
        UnionDer   = [string][char] 0x2524
    }
}

function Format-LineaDeMarco {
    <#
        .SYNOPSIS
            Una linea horizontal del marco, con titulo opcional a la izquierda.
        .DESCRIPTION
            El titulo va DENTRO del trazo y no en una linea aparte: ahorra
            alto de pantalla, que en una terminal es el recurso escaso, y deja
            el bloque y su nombre pegados sin una linea en blanco entre medias.
        .PARAMETER Trazo
            Lo que devuelve Get-TrazoDeMarco.
        .PARAMETER Paleta
            Lo que devuelve Get-Paleta.
        .PARAMETER Titulo
            Texto a incrustar. Vacio para una linea limpia.
        .PARAMETER Posicion
            Superior, Union o Inferior.
        .PARAMETER Derecha
            Texto pegado al extremo derecho, por ejemplo la fecha.
    #>
    [CmdletBinding()]
    [OutputType([string])]
    param(
        [Parameter(Mandatory)][hashtable] $Trazo,
        [Parameter(Mandatory)][hashtable] $Paleta,
        [string] $Titulo = '',
        [ValidateSet('Superior', 'Union', 'Inferior')]
        [string] $Posicion = 'Union',
        [string] $Derecha = ''
    )

    $izq = switch ($Posicion) {
        'Superior' { $Trazo.SupIzq }
        'Inferior' { $Trazo.InfIzq }
        default    { $Trazo.UnionIzq }
    }
    $der = switch ($Posicion) {
        'Superior' { $Trazo.SupDer }
        'Inferior' { $Trazo.InfDer }
        default    { $Trazo.UnionDer }
    }

    # EL ROTULO NO ES LINEA, ES TEXTO: va en el acento (--ac) y el trazo se
    # queda en --n5. En el panel pasa igual, un titulo de bloque nunca se pinta
    # del color del borde.
    $cuerpo = $Trazo.Horizontal
    $anchoCuerpo = 1
    if ($Titulo) {
        $rotulo = ' ' + $Titulo.ToUpperInvariant() + ' '
        $cuerpo += $Paleta.Fin + $Paleta.Titulo + $rotulo + $Paleta.Fin + $Paleta.Marco
        $anchoCuerpo += $rotulo.Length
    }

    $cola = ''
    $anchoCola = 0
    if ($Derecha) {
        $cola = $Paleta.Fin + $Paleta.Tenue + (' ' + $Derecha + ' ') + $Paleta.Fin + $Paleta.Marco + $Trazo.Horizontal
        $anchoCola = $Derecha.Length + 3
    }

    # Lo visible se mide SIN los codigos de color: el relleno se calcula sobre
    # caracteres que ocupan sitio, no sobre bytes de escape.
    # Se mide lo VISIBLE, nunca $cuerpo.Length: ahora esas cadenas llevan
    # secuencias de escape dentro y contarlas torceria el marco.
    $relleno = $script:AnchoTablero - $anchoCuerpo - $anchoCola
    if ($relleno -lt 0) { $relleno = 0 }

    return '{0}{1}{2}{3}{4}{5}{6}' -f `
        $Paleta.Marco, $izq, $cuerpo, ($Trazo.Horizontal * $relleno), $cola, $der, $Paleta.Fin
}

function Format-LineaDeTablero {
    <#
        .SYNOPSIS
            Una linea de contenido, enmarcada y con el ancho cuadrado.
        .PARAMETER Trazo
            Lo que devuelve Get-TrazoDeMarco.
        .PARAMETER Paleta
            Lo que devuelve Get-Paleta.
        .PARAMETER Texto
            Lo que se ve. Puede llevar codigos de color dentro.
        .PARAMETER Visible
            Cuantos caracteres ocupa Texto de verdad. Se da cuando Texto lleva
            codigos de color, porque .Length los contaria y el marco saldria
            torcido. Cero significa "usa .Length".
    #>
    [CmdletBinding()]
    [OutputType([string])]
    param(
        [Parameter(Mandatory)][hashtable] $Trazo,
        [Parameter(Mandatory)][hashtable] $Paleta,
        [string] $Texto = '',
        [int] $Visible = 0
    )

    # -1 y no -2: el ancho total de una linea de contenido tiene que ser el
    # mismo que el de una linea de marco -esquina + Ancho + esquina-, y aqui ya
    # se gasta un espacio de sangria despues de la barra. Con -2 el borde
    # derecho quedaba un caracter adentro y el marco salia torcido.
    $ancho = if ($Visible -gt 0) { $Visible } else { $Texto.Length }
    $relleno = $script:AnchoTablero - $ancho - 1
    if ($relleno -lt 0) { $relleno = 0 }

    return '{0}{1}{2} {3}{4}{5}{1}{6}' -f `
        $Paleta.Marco, $Trazo.Vertical, $Paleta.Fin, $Texto, (' ' * $relleno), $Paleta.Marco, $Paleta.Fin
}

function Format-Campo {
    <#
        .SYNOPSIS
            Etiqueta a la izquierda, valor alineado. La rejilla del tablero.
        .DESCRIPTION
            Una sola funcion para todas las filas: si la sangria y el ancho de
            etiqueta viven en un sitio, las columnas no se desalinean cuando
            alguien anade una fila.
        .PARAMETER Etiqueta
            Nombre del campo.
        .PARAMETER Valor
            Contenido, ya con color si toca.
        .PARAMETER Paleta
            Lo que devuelve Get-Paleta.
        .PARAMETER VisibleValor
            Longitud real de Valor si lleva codigos de color. Cero usa .Length.
    #>
    [CmdletBinding()]
    [OutputType([psobject])]
    param(
        # Vacia se permite: una fila de continuacion no tiene nombre propio.
        [Parameter(Mandatory)][AllowEmptyString()][string] $Etiqueta,
        [Parameter(Mandatory)][AllowEmptyString()][string] $Valor,
        [Parameter(Mandatory)][hashtable] $Paleta,
        [int] $VisibleValor = 0
    )

    $anchoEtiqueta = 12
    $eti = $Etiqueta.PadRight($anchoEtiqueta)
    $visValor = if ($VisibleValor -gt 0) { $VisibleValor } else { $Valor.Length }

    return [pscustomobject]@{
        Texto   = '  {0}{1}{2}{3}' -f $Paleta.Etiqueta, $eti, $Paleta.Fin, $Valor
        Visible = 2 + $anchoEtiqueta + $visValor
    }
}

# EL RITMO DEL ICONO VIVE AQUI, con su forma y su color. Los tres son la
# misma decision -como se ve un estado- y estaban repartidos entre dos
# archivos. Ademas estilo.ps1 no tiene bloque param(), asi que las pruebas
# pueden cargarlo con punto y comprobar el pulso; dentro de indicador.ps1
# no habia forma de alcanzarlo sin abrir una barra de tareas.
function Get-OpacidadDelPulso {
    <#
        .SYNOPSIS
            Que tan encendido va el nucleo en este instante.
        .DESCRIPTION
            LA TABLA DE LA SECCION 10.2, Y AQUI SI ESTA IMPLEMENTADA:

              Protegido   fijo             opaco siempre
              Copiando    PULSO SUAVE      sube y baja entre 120 y 255
              Atencion    FIJO             opaco siempre
              Falla       PARPADEA         salta entre 255 y 50, ~1 s
              SinDatos    fijo             opaco siempre

            EL ROJO SALTA Y EL VERDE RESPIRA, y la diferencia es deliberada. Un
            salto entre dos extremos llama; un recorrido continuo acompana. Si el
            verde parpadeara igual que el rojo, en dos semanas se dejaria de
            registrar el parpadeo -y entonces tampoco se registraria el dia que
            fuera rojo-. El movimiento es un recurso que se gasta (ISA-18.2).
        .PARAMETER Estado
            Uno de los cinco.
        .PARAMETER Tick
            El contador del temporizador.
    #>
    [CmdletBinding()]
    [OutputType([int])]
    param(
        [Parameter(Mandatory)][string] $Estado,
        [Parameter(Mandatory)][int] $Tick
    )

    if ($Estado -eq 'Falla') {
        # Ciclo de 5 ticks = 1 s: encendido 0.6 s, apagado 0.4 s.
        if (($Tick % 5) -lt 3) { return 255 }
        return 50
    }

    if ($Estado -eq 'Copiando') {
        # Onda triangular de 10 ticks = 2 s. Sin saltos: todos los intermedios.
        $fase = $Tick % 10
        $subida = if ($fase -lt 5) { $fase } else { 10 - $fase }
        return [int](120 + ($subida * 27))
    }

    return 255
}


function Limit-Texto {
    <#
        .SYNOPSIS
            Recorta un texto para que quepa, con puntos suspensivos.
        .DESCRIPTION
            EL MARCO NO SE PUEDE ROMPER. Un valor mas largo que el ancho empuja
            el borde derecho fuera y la ventana deja de ser una ventana: pasa
            con un detalle largo, con una ruta profunda o con un mensaje de
            error. Medido el 2026-09-02 con el detalle de la copia fria.

            Se recorta el TEXTO PLANO, antes de colorear: contar una cadena que
            ya lleva secuencias de escape dentro daria un ancho falso.
        .PARAMETER Texto
            Texto plano, sin codigos de color.
        .PARAMETER Maximo
            Cuantos caracteres caben.
    #>
    [CmdletBinding()]
    [OutputType([string])]
    param(
        [Parameter(Mandatory)][AllowEmptyString()][string] $Texto,
        [Parameter(Mandatory)][ValidateRange(4, 500)][int] $Maximo
    )
    if ($Texto.Length -le $Maximo) { return $Texto }
    return $Texto.Substring(0, $Maximo - 3) + '...'
}

function Get-AnchoDeValor {
    <#
        .SYNOPSIS
            Cuanto sitio le queda a un valor dentro del marco.
        .DESCRIPTION
            El calculo vive aqui, con el ancho y la sangria, y no repartido por
            el tablero: si alguien cambia el ancho del marco, esto se entera.
    #>
    [CmdletBinding()]
    [OutputType([int])]
    param()
    # 2 de sangria + 12 de etiqueta + 1 de aire contra el borde derecho.
    return ($script:AnchoTablero - 15)
}
