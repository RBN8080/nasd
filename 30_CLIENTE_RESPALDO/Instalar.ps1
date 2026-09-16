<#
.SYNOPSIS
    Deja el cliente de respaldo listo en un PC limpio. ADR-0095.

.DESCRIPTION
    ESTE GUION NO IMPLEMENTA NADA DEL MOTOR. Escribe 3-Config\respaldo.jsonc a
    partir de la plantilla publicada, con los valores de ESTE equipo y de SU
    nodo, y registra las dos tareas del Programador llamando a
    1-Interfaz\Registrar-Tarea.ps1, que es quien sabe hacerlo.

    POR QUE BASTA CON ESO, y es merito del diseno anterior, no de este guion:
    el contrato (30_CLIENTE_RESPALDO.md, criterio 12 de la seccion 15) exige
    que clonar el cliente en otra maquina funcione cambiando SOLO 3-Config\.
    Se comprobo al escribir esto: no hay ni una direccion del nodo ni una ruta
    de un equipo concreto en ninguno de los .ps1 del motor. Asi que instalar es
    exactamente "rellenar un archivo y registrar dos tareas".

    NO PIDE ADMINISTRADOR, igual que Registrar-Tarea.ps1: las tareas se
    registran para el usuario actual con RunLevel Limited, y pedir elevacion
    para esto seria regalar privilegio a cambio de nada.

    LO QUE NO HACE, Y HAY QUE HACER A MANO. Se dice aqui y se repite al
    terminar, porque son las dos cosas que dejan el respaldo a medias sin que
    parezca que falta nada:

      1. LA CREDENCIAL DEL NODO. La conexion SMB no la resuelve ningun codigo
         de este cliente: la resuelve Windows con el token del usuario. Por eso
         no hay ningun nombre de credencial escrito en el producto.
      2. LOS CENTINELAS. Este guion los deja VACIOS a proposito. La capa
         antirransomware compara huellas SHA-256 de archivos que tienen que
         existir de antemano, y un centinela declarado que no existe ABORTA la
         corrida. Vacio es la unica configuracion honesta para un PC recien
         instalado; con las huellas puestas es lo que protege de verdad.

.PARAMETER Nodo
    La direccion o el nombre del NAS. Lo que iria detras de las dos barras:
    con -Nodo 192.168.1.38 el destino queda como \\192.168.1.38\datos.

.PARAMETER NombreNodo
    Como se llama el nodo dentro del disco frio, para no mezclar lo que viene
    del NAS con lo que viene de este equipo. Solo se usa si algun dia se hace
    la pasada al disco frio.

.PARAMETER Equipo
    El nombre de este equipo en el destino. Por omision, el del sistema.

.EXAMPLE
    .\Instalar.ps1 -Nodo 192.168.1.38

.EXAMPLE
    .\Instalar.ps1 -Nodo nas.local -Equipo PORTATIL-SALON
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [ValidateNotNullOrEmpty()]
    [string] $Nodo,

    [string] $NombreNodo = 'NODO-01',

    [string] $Equipo = $env:COMPUTERNAME
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$raiz      = $PSScriptRoot
$config    = Join-Path $raiz '3-Config\respaldo.jsonc'
$plantilla = Join-Path $raiz '3-Config\respaldo.ejemplo.jsonc'
$registrar = Join-Path $raiz '1-Interfaz\Registrar-Tarea.ps1'

function Paso { param([string] $Texto) Write-Host "`n===== $Texto =====" -ForegroundColor Cyan }
function Bien { param([string] $Texto) Write-Host "  OK    $Texto" -ForegroundColor Green }
function Ojo  { param([string] $Texto) Write-Host "  AVISO $Texto" -ForegroundColor Yellow }

function Alto {
    param([string] $Hecho, [string[]] $Detalle = @())
    Write-Host "ERROR: $Hecho" -ForegroundColor Red
    foreach ($linea in $Detalle) { Write-Host "       $linea" }
    exit 1
}

# ---------------------------------------------------------------------------
#  COMPROBACION PREVIA -- nada de esto escribe
# ---------------------------------------------------------------------------

Paso 'Comprobacion previa'

# NO SE SOBRESCRIBE UNA CONFIGURACION QUE YA EXISTE, y no es prudencia
# generica: respaldo.jsonc lleva las huellas de los centinelas y el bloque de
# deuda de la primera corrida, que son datos que costo reunir y que no se
# pueden regenerar desde ninguna parte.
if (Test-Path -LiteralPath $config) {
    Alto "ya existe $config." @(
        'Este equipo ya esta configurado. No se toca: ese archivo lleva las',
        'huellas de los centinelas y el bloque de la primera corrida, que no se',
        'pueden regenerar.',
        'Si de verdad quiere empezar de cero, muevalo usted a otro sitio primero.'
    )
}

if (-not (Test-Path -LiteralPath $plantilla)) {
    Alto "falta $plantilla." @(
        'La plantilla es lo que este guion rellena, y no esta.',
        'Si esta usted en el repositorio PRIVADO, es normal: alli la plantilla vive',
        'en 40_PUBLICACION\plantillas\ y solo se copia a 3-Config\ al publicar.',
        'Use el clon del repositorio publico, que si la trae.'
    )
}

if (-not (Test-Path -LiteralPath $registrar)) {
    Alto "falta $registrar." @(
        'Es quien registra las dos tareas del Programador; sin el, el respaldo no',
        'correria solo y esto seria una instalacion a medias.',
        'El clon esta incompleto: clone el repositorio entero.'
    )
}
Bien 'la plantilla y el registrador estan en su sitio'

# EL DESTINO SE MIRA AHORA Y NO AL FINAL. Que el nodo no responda no impide
# escribir la configuracion --- se puede instalar con el NAS apagado --- pero
# descubrirlo despues de registrar las tareas significa que la primera corrida
# programada aborta sola y en silencio, de madrugada.
$unc = "\\$Nodo\datos"
if (Test-Path -LiteralPath $unc) {
    Bien "$unc responde"
} else {
    Ojo "$unc no responde todavia."
    Write-Host "       Se continua igual: la configuracion se puede escribir con el NAS apagado."
    Write-Host "       Pero la primera corrida ABORTARA si no se arregla antes. Dos causas:"
    Write-Host "         - el nodo esta apagado o la direccion no es esa;"
    Write-Host "         - falta guardar la credencial en el Administrador de credenciales."
    Write-Host "       Para lo segundo, una sola vez y desde este mismo usuario:"
    Write-Host "         cmdkey /add:$Nodo /user:nas /pass"
    Write-Host "       (La pide Windows, no este cliente: por eso el nombre de la"
    Write-Host "        credencial no esta escrito en ninguna parte del producto.)"
}

# ---------------------------------------------------------------------------
#  LA CONFIGURACION
# ---------------------------------------------------------------------------

Paso 'Escribiendo 3-Config\respaldo.jsonc'

$texto = Get-Content -LiteralPath $plantilla -Raw -Encoding UTF8

# Se sustituye sobre el TEXTO y no sobre un objeto convertido, a proposito: la
# plantilla son 240 lineas en las que casi todo son comentarios que explican
# que significa cada clave. Pasarla por ConvertFrom-Json y volver a serializar
# los tiraria todos, y quien edite esto despues se quedaria sin la unica
# documentacion que tiene al lado.
#
# .Replace() y no -replace: el segundo interpreta expresiones regulares, y
# estas cadenas estan llenas de barras invertidas.
$perfil = $env:USERPROFILE.Replace('\', '\\')   # las rutas van escapadas en JSON

$texto = $texto.Replace('192.168.1.38', $Nodo)
$texto = $texto.Replace('C:\\Users\\usuario', $perfil)
$texto = $texto.Replace('"equipo": "EQUIPO-01"', ('"equipo": "{0}"' -f $Equipo))
$texto = $texto.Replace('"prefijoEquipo": "EQUIPO-01"', ('"prefijoEquipo": "{0}"' -f $Equipo))
$texto = $texto.Replace('"prefijoNodo": "NODO-01"', ('"prefijoNodo": "{0}"' -f $NombreNodo))

# LOS CENTINELAS SE VACIAN, Y ES LA UNICA OPCION QUE FUNCIONA. La plantilla
# trae dos de ejemplo con la huella a ceros; declarados y sin existir, la
# comprobacion los cuenta como FALTA y aborta TODAS las corridas. Un cliente
# que nunca copia nada es peor que uno sin capa antirransomware, sobre todo
# porque el motivo no se ve: el icono se pone rojo y ya.
$texto = [regex]::Replace($texto, '"centinelas"\s*:\s*\[.*?\]', '"centinelas": []', 'Singleline')

# UTF8 sin BOM, que es lo que el cargador lee (-Encoding UTF8 explicito en
# comun.ps1). Se escribe con .NET porque Set-Content de PowerShell 5.1 pone BOM.
[System.IO.File]::WriteAllText($config, $texto, (New-Object System.Text.UTF8Encoding($false)))
Bien "escrito $config"

# ---------------------------------------------------------------------------
#  QUE LO VALIDE EL PROPIO PRODUCTO
# ---------------------------------------------------------------------------
#
# No se reimplementa aqui la lista de claves obligatorias: se llama al cargador
# de verdad, que es quien las exige. Dos listas de lo mismo se separan, y la
# que se quedaria vieja seria esta.

Paso 'Comprobando la configuracion con el cargador del propio cliente'

try {
    . (Join-Path $raiz '2-Nucleo\comun.ps1')
    $cfg = Get-ConfiguracionRespaldo -Ruta $config
    Bien ("configuracion valida: equipo '{0}', destino '{1}'" -f $cfg.equipo, $cfg.destinos.nodo.unc)

    $cadencia = Get-CadenciaDeCorrida -Configuracion $cfg
    Bien ("cadencia: {0}" -f $cadencia.Resumen)
} catch {
    Alto "la configuracion recien escrita NO la acepta el cargador." @(
        ("Dijo: {0}" -f $_.Exception.Message),
        'Es un defecto de la plantilla o de este guion, no algo que usted haya hecho mal.',
        "Se deja $config en su sitio para poder mirarlo."
    )
}

# AL MENOS UNA RAIZ TIENE QUE EXISTIR DE VERDAD. Si ninguna resuelve, el motor
# aborta con 'La configuracion no resuelve ninguna raiz' --- correcto, pero se
# descubriria en la primera corrida programada y no ahora.
try {
    . (Join-Path $raiz '2-Nucleo\clasificar.ps1')
    $raices = @(Get-RaicesDeRespaldo -Configuracion $cfg)
    if ($raices.Count -eq 0) {
        Ojo 'ninguna de las carpetas declaradas existe en este equipo.'
        Write-Host "       La plantilla declara Documents, Pictures, Videos y C:\dev."
        Write-Host "       Edite 'contenedores' y 'raicesDeclaradas' en $config"
        Write-Host "       antes de la primera corrida, o abortara por no tener nada que copiar."
    } else {
        Bien ("{0} carpetas resueltas para copiar" -f $raices.Count)
    }
} catch {
    Ojo ("no se pudieron resolver las carpetas: {0}" -f $_.Exception.Message)
}

# ---------------------------------------------------------------------------
#  LAS DOS TAREAS
# ---------------------------------------------------------------------------
#
# SON DOS Y NO UNA: el motor (que copia, tres veces al dia) y el indicador (el
# icono de la barra, al iniciar sesion). Registrar solo la primera deja un
# respaldo que funciona pero que nadie mira; solo la segunda, un icono que no
# tiene nada que contar.

Paso 'Registrando las tareas del Programador'

foreach ($pieza in @('Motor', 'Indicador')) {
    try {
        & $registrar -Pieza $pieza -Accion Registrar -RutaConfiguracion $config -Confirm:$false
        Bien "tarea de $pieza registrada"
    } catch {
        Alto ("no se pudo registrar la tarea de {0}." -f $pieza) @(
            ("Dijo: {0}" -f $_.Exception.Message),
            "La configuracion SI quedo escrita en $config.",
            'Registre las tareas a mano cuando lo arregle:',
            ("  .\1-Interfaz\Registrar-Tarea.ps1 -Pieza {0} -Accion Registrar -Confirm:`$false" -f $pieza)
        )
    }
}

# ---------------------------------------------------------------------------
#  EL CIERRE
# ---------------------------------------------------------------------------

Write-Host "`n===== CLIENTE INSTALADO =====" -ForegroundColor Green
Write-Host ""
Write-Host "  Configuracion:  $config"
Write-Host "  Destino:        $unc"
Write-Host "  Este equipo:    $Equipo"
Write-Host ""
Write-Host "  LO QUE FALTA, Y NO LO PUEDE HACER ESTE GUION:" -ForegroundColor Yellow
Write-Host ""
Write-Host "  1. La credencial del nodo, si no la tenia ya:"
Write-Host "       cmdkey /add:$Nodo /user:nas /pass"
Write-Host ""
Write-Host "  2. Los centinelas. Estan VACIOS: la capa antirransomware NO esta"
Write-Host "     vigilando nada todavia. Para activarla, cree un archivo en cada"
Write-Host "     carpeta que quiera vigilar, saque su huella, y anadalos a"
Write-Host "     'centinelas' en la configuracion:"
Write-Host "       Get-FileHash -Algorithm SHA256 <ruta>"
Write-Host ""
Write-Host "  3. Revise que 'contenedores', 'raicesDeclaradas' y 'exclusiones'"
Write-Host "     describen lo que usted quiere copiar. La plantilla trae un"
Write-Host "     ejemplo, no su disco."
Write-Host ""
Write-Host "  COMPRUEBE QUE TODO ESTA BIEN:"
Write-Host "       .\4-Pruebas\Invoke-Pruebas.ps1"
Write-Host ""
