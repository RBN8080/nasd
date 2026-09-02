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

function Format-Antiguedad {
    <#
        .SYNOPSIS
            Convierte un momento en la frase que usa una persona.

        .DESCRIPTION
            "hace 296 h" es exacto y no significa nada. La maqueta del plan pide
            "hoy 02:04" y "hace 12 dias" porque asi es como se decide si hay que
            preocuparse: el numero de horas obliga a dividir mentalmente entre 24
            justo cuando se esta mirando el tablero con prisa.

            Una fecha que no se puede leer devuelve 'nunca', que es distinto de
            devolver una fecha vieja: no saber cuando fue la ultima copia no es
            lo mismo que saber que fue hace mucho.
        .PARAMETER Momento
            La marca de tiempo, como texto. Vacia o ilegible da 'nunca'.
    #>
    [CmdletBinding()]
    [OutputType([string])]
    param(
        [AllowEmptyString()][AllowNull()][string] $Momento
    )

    [datetime] $t = [datetime]::MinValue
    if ([string]::IsNullOrWhiteSpace($Momento) -or -not [datetime]::TryParse($Momento, [ref] $t)) {
        return 'nunca'
    }

    $ahora = Get-Date
    $dias = [int][math]::Floor(($ahora.Date - $t.Date).TotalDays)
    if ($dias -le 0) { return 'hoy {0:HH:mm}' -f $t }
    if ($dias -eq 1) { return 'ayer {0:HH:mm}' -f $t }
    if ($dias -lt 30) { return 'hace {0} dias' -f $dias }
    return '{0:dd/MM/yyyy}' -f $t
}

function Get-Regla {
    <#
        .SYNOPSIS
            La linea horizontal que separa bloques, ya coloreada.
        .DESCRIPTION
            EL TABLERO NO LLEVA CAJA. La maqueta aprobada separa con REGLAS
            HORIZONTALES a todo lo ancho, no con un marco cerrado; es la misma
            decision que el panel del nodo, donde las secciones se separan con
            un borde y nunca se encierran. Un marco ademas obliga a recortar
            cada valor contra el borde derecho, y ese recorte fue justo lo que
            rompio la ventana con una ruta larga.
        .PARAMETER Paleta
            Los colores.
        .PARAMETER Unicode
            Si no, guiones.
        .PARAMETER Ancho
            Cuanto mide. Por defecto, el ancho del tablero.
    #>
    [CmdletBinding()]
    [OutputType([string])]
    param(
        [Parameter(Mandatory)][hashtable] $Paleta,
        [bool] $Unicode = $true,
        [int] $Ancho = 0
    )
    if ($Ancho -le 0) { $Ancho = $script:AnchoTablero }
    $trazo = if ($Unicode) { [string][char] 0x2500 } else { '-' }
    return '  {0}{1}{2}' -f $Paleta.Marco, ($trazo * $Ancho), $Paleta.Fin
}

function Format-Celda {
    <#
        .SYNOPSIS
            Una celda de la tabla: recortada, alineada y coloreada, en ese orden.
        .DESCRIPTION
            EL ORDEN IMPORTA. Colorear antes de rellenar mete las secuencias de
            escape dentro de la cuenta de caracteres y las columnas dejan de
            estar alineadas -sin que se vea por que, porque los codigos son
            invisibles-. Aqui se recorta el texto plano, se rellena el texto
            plano, y solo al final se envuelve en color.
        .PARAMETER Texto
            Texto plano.
        .PARAMETER Ancho
            Ancho de la columna.
        .PARAMETER Color
            Codigo de color, o vacio.
        .PARAMETER Paleta
            Para saber como cerrar el color.
        .PARAMETER Derecha
            Alinea a la derecha. Para numeros.
    #>
    [CmdletBinding()]
    [OutputType([string])]
    param(
        [Parameter(Mandatory)][AllowEmptyString()][string] $Texto,
        [Parameter(Mandatory)][ValidateRange(1, 200)][int] $Ancho,
        [Parameter(Mandatory)][hashtable] $Paleta,
        [AllowEmptyString()][string] $Color = '',
        [switch] $Derecha
    )

    $t = $Texto
    if ($t.Length -gt $Ancho) {
        # Limit-Texto exige 4 de margen para los puntos suspensivos; por debajo
        # de eso se corta en seco, que en una columna de 2 es lo correcto.
        $t = if ($Ancho -ge 4) { Limit-Texto -Texto $t -Maximo $Ancho } else { $t.Substring(0, $Ancho) }
    }
    $relleno = if ($Derecha) { $t.PadLeft($Ancho) } else { $t.PadRight($Ancho) }
    if ([string]::IsNullOrEmpty($Color)) { return $relleno }
    return '{0}{1}{2}' -f $Color, $relleno, $Paleta.Fin
}
