#Requires -Version 5.1
<#
    .SYNOPSIS
        El icono de la barra de tareas. Solo lee y pinta.

    .DESCRIPTION
        Contrato: 30_CLIENTE_RESPALDO.md seccion 10.2.

        SOLO LEE ESTADO.txt Y PINTA. NO EJECUTA EL RESPALDO. Cerrarlo no detiene
        nada. Y por eso ESTADO.txt se escribe de forma atomica: este proceso lo
        esta leyendo al mismo tiempo que el motor lo reescribe.

        CUADRO OSCURO CON NUCLEO QUE CAMBIA COLOR **Y** FORMA. La forma es
        deliberada: el color solo no basta (WCAG 2.2 1.4.1), y este repositorio
        ya tiene registrada esa misma preocupacion para el semaforo de la web.

          Protegido            circulo verde      fijo
          Copiando             circulo verde      pulso suave
          Requiere atencion    triangulo ambar    FIJO
          Falla                rombo rojo         PARPADEA
          Sin datos            circulo gris       fijo

        SOLO EL ROJO PARPADEA. Si el verde parpadeara cada vez que trabaja -y va
        a trabajar a diario- en dos semanas se deja de registrar el parpadeo, y
        entonces tampoco se registra el dia que sea rojo. El movimiento es un
        recurso que se gasta (ISA-18.2, fatiga de alarmas).

        DETECCION DE MOTOR CAIDO - la tabla de la seccion 10.2, entera:
          marca presente y reciente            copiando ahora          Copiando
          MARCA PRESENTE PERO VIEJA            arranco y nunca termino FALLA
          sin marca, dentro de lo esperado     termino bien            Protegido
          sin marca, ya paso la hora           no corrio               Atencion
          la tarea programada no existe o esta deshabilitada           FALLA

        PUNTO CIEGO DECLARADO: si el indicador muere no hay icono, y un icono
        ausente se parece a "todo bien". Se mitiga relanzandolo desde la tarea,
        pero EL SILENCIO DEL ICONO NUNCA ES PRUEBA DE NADA. La prueba esta en el
        tablero.

        EL ICONO ES NATIVO en el .NET que Windows ya trae: sin dependencias, sin
        tocar ADR-0013. Ese era el unico punto que podia presionar una decision
        ya tomada del repositorio, y en PowerShell desaparece (seccion 16.bis).

    .PARAMETER SegundosEntreLecturas
        Cada cuanto se relee ESTADO.txt.

    .PARAMETER HorasSinCorrerParaAvisar
        A partir de cuantas horas sin una corrida buena el icono pasa a Atencion.

    .PARAMETER UnaSolaLectura
        No abre icono: lee el estado, lo devuelve y sale. Es como lo prueban las
        pruebas, que no pueden abrir una barra de tareas.

    .EXAMPLE
        .\indicador.ps1
        .\indicador.ps1 -UnaSolaLectura
#>
[CmdletBinding()]
[OutputType([psobject])]
param(
    [ValidateRange(1, 3600)][int] $SegundosEntreLecturas = 10,
    [ValidateRange(1, 720)][int]  $HorasSinCorrerParaAvisar = 30,

    # Parametrizado y no fijo: una prueba que dependa de si la tarea REAL existe
    # en esta maquina es una prueba que pasa o falla segun el dia. Medido el
    # 2026-09-02, cuando registrar la tarea de verdad rompio su propia prueba.
    [ValidateNotNullOrEmpty()]
    [string] $NombreTarea = 'NasRespaldo-Diario',

    [ValidateNotNullOrEmpty()]
    [string] $CarpetaEstado = (Join-Path $env:LOCALAPPDATA 'NasRespaldo\estado'),

    [switch] $UnaSolaLectura
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

. "$PSScriptRoot\..\2-Nucleo\comun.ps1"
. "$PSScriptRoot\estilo.ps1"   # Get-OpacidadDelPulso: el ritmo vive con la forma y el color

$script:NombreTarea = $NombreTarea
$script:CarpetaEstado = $CarpetaEstado

function Get-EstadoParaElIcono {
    <#
        .SYNOPSIS
            Resuelve los cinco estados de la seccion 10.2. No lanza nunca.
        .DESCRIPTION
            El orden de las preguntas ES la tabla del contrato, y esta ordenado
            de peor a mejor a proposito: lo que descubre una FALLA gana sobre lo
            que descubre normalidad. Un motor caido con la tarea deshabilitada
            tiene que salir rojo, no ambar.
        .PARAMETER HorasParaAvisar
            Cuantas horas sin corrida buena antes de pasar a Atencion.
    #>
    [CmdletBinding()]
    [OutputType([psobject])]
    param(
        [ValidateRange(1, 720)][int] $HorasParaAvisar = 30
    )

    try {
        $marca  = Get-MarcaDeCorrida -Carpeta $script:CarpetaEstado
        $estado = Read-EstadoRespaldo -Carpeta $script:CarpetaEstado

        # 1. La tarea programada no existe o esta deshabilitada -> alguien la quito.
        $tarea = Get-ScheduledTask -TaskName $script:NombreTarea -ErrorAction SilentlyContinue
        if (-not $tarea) {
            return [pscustomobject]@{ Estado = 'Falla'; Detalle = 'La tarea programada no existe: el respaldo no corre solo'; Fuente = 'tarea' }
        }
        if ($tarea.State -eq 'Disabled') {
            return [pscustomobject]@{ Estado = 'Falla'; Detalle = 'La tarea programada esta DESHABILITADA'; Fuente = 'tarea' }
        }

        # 2. Marca presente pero vieja, o con su proceso muerto -> arranco y
        #    nunca termino. Es FALLA, no "copiando".
        if ($marca.Existe -and $marca.Vieja) {
            $porque = if (-not $marca.ProcesoVivo) { 'su proceso ya no existe' } else { "lleva $($marca.Minutos) min" }
            return [pscustomobject]@{ Estado = 'Falla'; Detalle = "Una corrida arranco y nunca termino ($porque)"; Fuente = 'marca' }
        }

        # 3. Marca presente y viva -> copiando ahora.
        if ($marca.Existe) {
            return [pscustomobject]@{ Estado = 'Copiando'; Detalle = "Copiando desde hace $($marca.Minutos) min"; Fuente = 'marca' }
        }

        # 4. Sin marca: manda lo que dejo escrito la ultima corrida.
        $ultimo = '' + $estado['estado']
        if ($ultimo -eq 'SinDatos') {
            return [pscustomobject]@{ Estado = 'SinDatos'; Detalle = '' + $estado['detalle']; Fuente = 'estado' }
        }
        # UN ROJO CUYA CAUSA YA NO EXISTE NO PUEDE SEGUIR SIENDO ROJO. El
        # 2026-09-02 el nodo se cayo por un apagon, la corrida aborto y el icono
        # se puso rojo -correctamente-. Cuando el nodo volvio, el icono seguia
        # parpadeando en rojo mientras el tablero, en la misma pantalla, decia
        # "Nodo: responde". Dos cosas contradictorias a la vez no es un estado,
        # es un semaforo roto.
        #
        # ROJO significa "esto esta roto AHORA". Si lo que lo rompio ya se
        # arreglo solo, baja a AMBAR: "la ultima corrida fallo y todavia no ha
        # habido otra buena". Eso es exacto y no miente en ninguna direccion: no
        # dice que estes protegido -no lo estas hasta que corra- ni grita por
        # algo que ya paso. El rojo se reserva para lo que sigue roto, que es lo
        # unico que lo mantiene util (ISA-18.2).
        # AQUI SOLO SE MIDE; QUIEN DECIDE ES Resolve-EstadoVigente (comun.ps1),
        # que es una funcion pura y por eso se puede probar sin nodo.
        $unc = ''
        try { $unc = (Get-ConfiguracionRespaldo).destinos.nodo.unc } catch { $unc = '' }
        $responde = [bool]($unc -and (Test-Path -LiteralPath $unc -ErrorAction SilentlyContinue))
        $vigente = Resolve-EstadoVigente -Estado $ultimo -Causa ('' + $estado['causa']) -DestinoResponde $responde
        if ($vigente.Ajustado) {
            return [pscustomobject]@{ Estado = $vigente.Estado; Detalle = $vigente.Detalle; Fuente = 'estado' }
        }

        if ($ultimo -in @('Falla', 'Atencion')) {
            return [pscustomobject]@{ Estado = $ultimo; Detalle = '' + $estado['detalle']; Fuente = 'estado' }
        }

        # 5. La ultima fue buena: solo queda preguntar si fue hace demasiado.
        # Tipada antes de TryParse: con $null, PowerShell no resuelve el [ref].
        [datetime] $momento = [datetime]::MinValue
        $hayMomento = $false
        if ($estado.ContainsKey('momento')) {
            $hayMomento = [datetime]::TryParse(('' + $estado['momento']), [ref]$momento)
        }
        if ($hayMomento) {
            $horas = ((Get-Date) - $momento).TotalHours
            if ($horas -gt $HorasParaAvisar) {
                return [pscustomobject]@{
                    Estado  = 'Atencion'
                    Detalle = ('La ultima corrida buena fue hace {0:N0} h' -f $horas)
                    Fuente  = 'antiguedad'
                }
            }
        }
        return [pscustomobject]@{ Estado = 'Protegido'; Detalle = '' + $estado['detalle']; Fuente = 'estado' }
    }
    catch {
        # Nunca lanzar: una excepcion aqui mata el icono, y un icono ausente se
        # parece a "todo bien". Prefiero un icono gris que ninguno.
        return [pscustomobject]@{ Estado = 'SinDatos'; Detalle = "No se pudo leer el estado: $($_.Exception.Message)"; Fuente = 'error' }
    }
}

function New-IconoDeEstado {
    <#
        .SYNOPSIS
            Dibuja el icono: cuadro oscuro y nucleo con COLOR Y FORMA.
        .PARAMETER Estado
            Uno de los cinco.
        .PARAMETER Opacidad
            0 a 255. NO es un interruptor sino un valor continuo, y esa es la
            diferencia entre un parpadeo y un pulso: el rojo salta entre dos
            extremos, el verde de "copiando" recorre los intermedios.
        .OUTPUTS
            Un objeto con el icono Y SU ASA. El asa hace falta: Icon::FromHandle
            NO se queda con la propiedad del HICON que devuelve GetHicon, asi que
            Dispose() del icono no la libera. Con un temporizador de 200 ms eso
            son cinco asas de GDI perdidas por segundo -mas de 400 000 al dia- y
            el escritorio acaba sin recursos. Quien la libera es DestroyIcon.
    #>
    [Diagnostics.CodeAnalysis.SuppressMessageAttribute(
        'PSUseShouldProcessForStateChangingFunctions', '',
        Justification = 'Construye un objeto Icon en memoria y lo devuelve. No toca disco, registro ni ningun estado del sistema: el verbo New- se usa aqui como en New-Object o New-TimeSpan. Anadir ShouldProcess a algo que se llama en cada tick del temporizador solo anadiria ruido.')]
    [CmdletBinding()]
    [OutputType([psobject])]
    param(
        [Parameter(Mandatory)]
        [ValidateSet('Protegido', 'Copiando', 'Atencion', 'Falla', 'SinDatos')]
        [string] $Estado,
        [ValidateRange(0, 255)][int] $Opacidad = 255
    )

    $lado = 16
    $mapa = New-Object System.Drawing.Bitmap $lado, $lado
    $g = [System.Drawing.Graphics]::FromImage($mapa)
    try {
        $g.SmoothingMode = [System.Drawing.Drawing2D.SmoothingMode]::AntiAlias
        # EL FONDO Y LOS CUATRO COLORES SON LOS DEL PANEL DEL NODO, no unos
        # parecidos. estilo.css declara --n3 #16181b para las superficies y
        # --ok/--av/--fa/--nada para los estados. El icono de la barra y el
        # panel web son el MISMO producto: si su verde y este verde no son el
        # mismo verde, quien mira aprende dos idiomas de colores y el dia que
        # importa lee el equivocado.
        $fondo = New-Object System.Drawing.SolidBrush ([System.Drawing.Color]::FromArgb(255, 0x16, 0x18, 0x1b))
        $g.FillRectangle($fondo, 0, 0, $lado, $lado)
        $fondo.Dispose()

        $color = switch ($Estado) {
            'Protegido' { [System.Drawing.Color]::FromArgb(255, 0x3f, 0xb9, 0x50) }   # --ok
            'Copiando'  { [System.Drawing.Color]::FromArgb(255, 0x3f, 0xb9, 0x50) }   # --ok
            'Atencion'  { [System.Drawing.Color]::FromArgb(255, 0xd2, 0x99, 0x22) }   # --av
            'Falla'     { [System.Drawing.Color]::FromArgb(255, 0xf8, 0x51, 0x49) }   # --fa
            default     { [System.Drawing.Color]::FromArgb(255, 0x6b, 0x72, 0x80) }   # --nada
        }
        if ($Opacidad -lt 255) { $color = [System.Drawing.Color]::FromArgb($Opacidad, $color.R, $color.G, $color.B) }
        $pincel = New-Object System.Drawing.SolidBrush $color
        try {
            switch ($Estado) {
                # Triangulo: la forma de "mira esto", distinta del circulo aunque
                # se vea en blanco y negro (WCAG 2.2 1.4.1).
                'Atencion' {
                    $puntos = @(
                        (New-Object System.Drawing.Point 8, 3),
                        (New-Object System.Drawing.Point 14, 13),
                        (New-Object System.Drawing.Point 2, 13)
                    )
                    $g.FillPolygon($pincel, $puntos)
                }
                # Rombo: la forma de "esto esta roto".
                'Falla' {
                    $puntos = @(
                        (New-Object System.Drawing.Point 8, 2),
                        (New-Object System.Drawing.Point 14, 8),
                        (New-Object System.Drawing.Point 8, 14),
                        (New-Object System.Drawing.Point 2, 8)
                    )
                    $g.FillPolygon($pincel, $puntos)
                }
                default { $g.FillEllipse($pincel, 3, 3, 10, 10) }
            }
        }
        finally { $pincel.Dispose() }

        $asa = $mapa.GetHicon()
        return [pscustomobject]@{
            Icono = [System.Drawing.Icon]::FromHandle($asa)
            Asa   = $asa
        }
    }
    finally {
        $g.Dispose()
        $mapa.Dispose()
    }
}

if ($UnaSolaLectura) {
    return (Get-EstadoParaElIcono -HorasParaAvisar $HorasSinCorrerParaAvisar)
}

# --- El icono de verdad -----------------------------------------------------
# UNO Y NADA MAS QUE UNO. Dos indicadores ponen DOS iconos en la barra, y esa
# es una forma barata de perder la confianza en el que importa: si hay dos y
# dicen cosas distintas -porque uno se quedo con un estado viejo-, ninguno
# sirve.
#
# ES PRECAUCION, NO LA CURA DE UN FALLO OBSERVADO, y conviene que quede escrito:
# el 2026-09-02 se creyo ver dos y era un error de medicion -la consulta que los
# contaba llevaba "indicador.ps1" en su propia linea de comando y se contaba a
# si misma-. El cerrojo se queda porque relanzar la tarea a mano es facil y el
# coste de esta linea es cero, pero NO se apunta un fallo que no existio.
#
# El cerrojo es un mutex con nombre, que el sistema suelta solo cuando el
# proceso muere: si el indicador se cae, el siguiente arranca sin tener que
# limpiar nada. Un archivo de bloqueo no daria esa garantia.
$script:cerrojo = New-Object System.Threading.Mutex($false, 'Local\NasRespaldo-Indicador')
if (-not $script:cerrojo.WaitOne(0)) {
    Write-Verbose 'Ya hay un indicador corriendo en esta sesion. Este se retira.'
    return
}

# La ventana negra, escondida si es nuestra: un icono de bandeja que arrastra
# una consola abierta todo el dia acaba cerrado por quien lo ve, y cerrarlo
# deja el escritorio sin indicador. La molestia no es estetica: se come la
# seccion 10.2.
Hide-VentanaDeConsola | Out-Null

# DestroyIcon: la unica forma de devolver el asa que entrega Bitmap.GetHicon.
if (-not ('NasRespaldo.Iconos' -as [type])) {
    Add-Type -Namespace 'NasRespaldo' -Name 'Iconos' -MemberDefinition @'
[System.Runtime.InteropServices.DllImport("user32.dll", SetLastError = true)]
public static extern bool DestroyIcon(System.IntPtr hIcon);
'@
}

Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName System.Drawing

$icono = New-Object System.Windows.Forms.NotifyIcon
$icono.Visible = $true
$menu = New-Object System.Windows.Forms.ContextMenuStrip
$icono.ContextMenuStrip = $menu

$abrirTablero = $menu.Items.Add('Abrir el tablero')
$abrirTablero.Add_Click({
        Start-Process powershell.exe -ArgumentList @(
            '-NoExit', '-ExecutionPolicy', 'Bypass', '-File', "$PSScriptRoot\tablero.ps1")
    })
[void]$menu.Items.Add('-')
$salir = $menu.Items.Add('Cerrar el indicador (no detiene el respaldo)')

# DOS RELOJES, Y CONFUNDIRLOS ERA EL DEFECTO. Hasta el 2026-09-02 el
# temporizador latia UNA VEZ CADA DIEZ SEGUNDOS, porque el mismo intervalo servia
# para releer el estado y para animar. Un cambio cada diez segundos no es un
# parpadeo: es un icono distinto de vez en cuando, y nadie lo lee como movimiento.
# Ademas el "pulso suave" de "copiando" que promete la seccion 10.2 estaba
# ESCRITO EN EL COMENTARIO Y NO EN EL CODIGO -solo el rojo alternaba-. Lo noto el
# responsable mirando la barra durante una copia.
#
# Ahora el temporizador late rapido y es el CONTADOR quien decide cuando toca
# releer el estado. Leer sigue costando lo mismo; animar es gratis.
$script:MsAnimacion = 200
$temporizador = New-Object System.Windows.Forms.Timer
$temporizador.Interval = $script:MsAnimacion

$script:tick            = 0
$script:ticksPorLectura = [Math]::Max(1, [int](($SegundosEntreLecturas * 1000) / $script:MsAnimacion))
$script:v               = $null
$script:ultimoDibujo    = ''
$script:asaActual       = [System.IntPtr]::Zero

$refrescar = {
    # 1. El estado se relee a su ritmo, no al de la animacion.
    if ($null -eq $script:v -or ($script:tick % $script:ticksPorLectura) -eq 0) {
        $script:v = Get-EstadoParaElIcono -HorasParaAvisar $HorasSinCorrerParaAvisar
        $texto = '{0} - {1}' -f $script:v.Estado, $script:v.Detalle
        if ($texto.Length -gt 63) { $texto = $texto.Substring(0, 60) + '...' }
        $icono.Text = $texto
    }

    # 2. La animacion va en cada tick, pero SOLO SE REDIBUJA SI CAMBIA ALGO: los
    #    tres estados fijos no gastan ni un dibujo por mucho que lata el reloj.
    $opacidad = Get-OpacidadDelPulso -Estado $script:v.Estado -Tick $script:tick
    $firma = '{0}|{1}' -f $script:v.Estado, $opacidad
    if ($firma -ne $script:ultimoDibujo) {
        $nuevo = New-IconoDeEstado -Estado $script:v.Estado -Opacidad $opacidad
        $iconoAnterior = $icono.Icon
        $asaAnterior   = $script:asaActual

        $icono.Icon       = $nuevo.Icono
        $script:asaActual = $nuevo.Asa

        # El orden importa: primero se pone el nuevo, despues se suelta el
        # viejo. Al reves, la bandeja se quedaria un instante sin icono.
        if ($iconoAnterior) { $iconoAnterior.Dispose() }
        if ($asaAnterior -ne [System.IntPtr]::Zero) {
            [void][NasRespaldo.Iconos]::DestroyIcon($asaAnterior)
        }
        $script:ultimoDibujo = $firma
    }
    $script:tick++
}

$temporizador.Add_Tick($refrescar)
$salir.Add_Click({
        $temporizador.Stop()
        $icono.Visible = $false
        $icono.Dispose()
        if ($script:asaActual -ne [System.IntPtr]::Zero) {
            [void][NasRespaldo.Iconos]::DestroyIcon($script:asaActual)
        }
        if ($script:cerrojo) { $script:cerrojo.ReleaseMutex(); $script:cerrojo.Dispose() }
        [System.Windows.Forms.Application]::Exit()
    })

& $refrescar
$temporizador.Start()
[System.Windows.Forms.Application]::Run()
