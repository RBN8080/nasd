#Requires -Version 5.1
<#
    .SYNOPSIS
        Las pruebas del motor, sobre archivos de mentira. Nunca sobre los vivos.

    .DESCRIPTION
        Contrato: 30_CLIENTE_RESPALDO.md seccion 15.

        POR QUE ESTE ARCHIVO EXISTE Y POR QUE CORRE PRIMERO. Los criterios 2, 3,
        5 y 6 solo se pueden comprobar ROMPIENDOLOS A PROPOSITO: hay que borrar
        archivos, alterar un centinela y simular un cifrado masivo. Eso no se
        hace sobre los archivos del responsable. Se hace aqui, en un arenero de
        basura que se crea y se destruye en cada corrida.

        EL DESTINO DEL ARENERO ES UNA RUTA UNC DE VERDAD (\\localhost\C$\...),
        no una carpeta local. Si fuera local, la guarda que nacio del tropiezo
        del 2026-09-02 -la barra que faltaba- quedaria sin probar justo en las
        pruebas que existen para probarla.

        Sin dependencias: no usa Pester ni ningun modulo de galeria, igual que
        el resto del cliente (ADR-0013, ADR-0017).

    .PARAMETER CarpetaCaja
        Donde montar el arenero. Por omision C:\temp\_pruebas_respaldo.
        NO se pone bajo AppData a proposito: AppData esta en las exclusiones
        reales, y el arenero heredaria esa exclusion y fingiria pasar.

    .PARAMETER Conservar
        No destruye el arenero al terminar, para poder mirarlo.

    .EXAMPLE
        .\Invoke-Pruebas.ps1
#>
[CmdletBinding()]
[OutputType([psobject])]
param(
    [ValidateNotNullOrEmpty()]
    [string] $CarpetaCaja = 'C:\temp\_pruebas_respaldo',

    [switch] $Conservar
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$nucleo = Join-Path (Split-Path $PSScriptRoot -Parent) '2-Nucleo'
. "$nucleo\comun.ps1"
. "$nucleo\clasificar.ps1"
# respaldo.ps1 cargado con punto expone sus funciones y NO corre nada.
. "$nucleo\respaldo.ps1"

# ---------------------------------------------------------------------------
#  Arnes minimo. Treinta lineas en vez de una dependencia.
# ---------------------------------------------------------------------------

$script:Resultados = New-Object System.Collections.Generic.List[psobject]

function Test-Afirmacion {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory)][string] $Nombre,
        [Parameter(Mandatory)][AllowNull()] $Esperado,
        [Parameter(Mandatory)][AllowNull()] $Obtenido,
        [string] $Criterio = ''
    )
    # Sin operador ternario: PowerShell 5.1 no lo tiene (ADR-0078).
    if ($Esperado -is [array] -and $Obtenido -is [array]) {
        $ok = (($Esperado -join '|') -eq ($Obtenido -join '|'))
    }
    else {
        $ok = ($Esperado -eq $Obtenido)
    }
    $script:Resultados.Add([pscustomobject]@{
        Criterio = $Criterio
        Nombre   = $Nombre
        Esperado = $Esperado
        Obtenido = $Obtenido
        Paso     = $ok
    })
}

function Write-Titulo {
    param([Parameter(Mandatory)][string] $Texto)
    Write-Information "`n=== $Texto" -InformationAction Continue
}

# ---------------------------------------------------------------------------
#  El arenero
# ---------------------------------------------------------------------------

function New-CajaDePrueba {
    <#
        .SYNOPSIS
            Monta un equipo de mentira: contenedores, raices y basura dentro.
    #>
    [CmdletBinding(SupportsShouldProcess)]
    [OutputType([psobject])]
    param(
        [Parameter(Mandatory)][string] $Carpeta
    )

    if (-not $PSCmdlet.ShouldProcess($Carpeta, 'Crear arenero de pruebas')) { return $null }

    if (Test-Path -LiteralPath $Carpeta) {
        Remove-Item -LiteralPath $Carpeta -Recurse -Force
    }

    $origen  = Join-Path $Carpeta 'origen'
    $destino = Join-Path $Carpeta 'destino'

    $carpetas = @(
        "$origen\Documents\01_DOCS",
        "$origen\Documents\SinNumero",
        "$origen\Pictures\01_FOTOS",
        "$origen\proyecto\src",
        $destino
    )
    foreach ($c in $carpetas) { New-Item -ItemType Directory -Path $c -Force | Out-Null }

    # Archivos de mentira. Contenido distinto para que las huellas difieran.
    1..6 | ForEach-Object { "documento de mentira numero $_" | Set-Content -LiteralPath "$origen\Documents\01_DOCS\doc$_.txt" -Encoding UTF8 }
    1..4 | ForEach-Object { "foto de mentira numero $_"      | Set-Content -LiteralPath "$origen\Pictures\01_FOTOS\foto$_.txt" -Encoding UTF8 }
    1..5 | ForEach-Object { "codigo de mentira numero $_"    | Set-Content -LiteralPath "$origen\proyecto\src\mod$_.txt" -Encoding UTF8 }
    'esto no lleva numero, no se copia' | Set-Content -LiteralPath "$origen\Documents\SinNumero\suelto.txt" -Encoding UTF8

    # El destino se alcanza por UNC de verdad, no por ruta local.
    $destinoUnc = '\\localhost\C$' + $destino.Substring(2)

    return [pscustomobject]@{
        Raiz        = $Carpeta
        Origen      = $origen
        Destino     = $destino
        DestinoUnc  = $destinoUnc
        Config      = Join-Path $Carpeta 'respaldo.jsonc'
    }
}

function Write-ConfiguracionDeCaja {
    <#
        .SYNOPSIS
            Escribe el .jsonc del arenero, con la deuda calculada de lo que hay.
    #>
    [CmdletBinding(SupportsShouldProcess)]
    [OutputType([void])]
    param(
        [Parameter(Mandatory)][psobject] $Caja,
        [AllowEmptyCollection()][psobject[]] $Centinelas = @(),
        [double] $Umbral = 5,
        [switch] $DeudaRota,
        [switch] $Saldada
    )

    if (-not $PSCmdlet.ShouldProcess($Caja.Config, 'Escribir configuracion del arenero')) { return }

    $raicesDeuda = @(
        "$($Caja.Origen)\Documents\01_DOCS",
        "$($Caja.Origen)\Pictures\01_FOTOS",
        "$($Caja.Origen)\proyecto"
    )
    if ($DeudaRota) { $raicesDeuda += "$($Caja.Origen)\Documents\99_QUE_NO_EXISTE" }

    $deuda = @()
    foreach ($r in $raicesDeuda) {
        $arch = @(Get-ChildItem -LiteralPath $r -File -Force -Recurse -ErrorAction SilentlyContinue)
        [int64]$suma = 0
        foreach ($a in $arch) { $suma += $a.Length }
        $deuda += @{ ruta = $r; archivos = $arch.Count; bytes = $suma }
    }

    $cfg = [ordered]@{
        version  = '0.1.0-pruebas'
        equipo   = 'CAJA'
        destinos = [ordered]@{
            nodo = [ordered]@{
                unc                     = '\\localhost\C$'
                raiz                    = $Caja.Destino.Substring(3)
                prefijoEquipo           = 'CAJA'
                credencial              = 'ninguna'
                cadencia                = 'pruebas'
                abortarSiDestinoNoEsUNC = $true
                traduccionDeRutas       = @(
                    @{ de = $Caja.Origen; a = 'origen' }
                )
            }
            discoFrio = [ordered]@{
                serie = 'NO-EXISTE'; etiqueta = 'NINGUNA'; sistemaDeArchivos = 'NTFS'
                centinela = '_no_existe.txt'; modo = 'aditivo'; cadencia = 'pruebas'
                prefijoEquipo = 'CAJA'; prefijoNodo = 'NODO'
            }
        }
        contenedores = @(
            @{ ruta = "$($Caja.Origen)\Documents"; clase = 'B' }
            @{ ruta = "$($Caja.Origen)\Pictures";  clase = 'A' }
        )
        raicesDeclaradas = @(
            @{ ruta = "$($Caja.Origen)\proyecto"; clase = 'B'; nota = 'raiz declarada entera' }
        )
        carpetaEstado = (Join-Path $Caja.Raiz 'estado-motor')
        raicesDelNodo = @()
        exclusiones   = @('AppData', '.cache', 'Juegos', '*.vmdk')
        secretos      = [ordered]@{
            politica = 'detectar-y-reportar'
            cifrarRaizDeclarada = '_CONFIGS'
            patronesDeDeteccion = @('^\.env($|\.)', '\.pem$', '\.key$', 'contrase', 'token')
        }
        centinelas  = @($Centinelas)
        # LAS MISMAS TRES VENTANAS QUE PRODUCCION, y a proposito: el arenero
        # existe para que el motor corra de verdad contra archivos de mentira,
        # no para que corra con una cadencia de mentira. Si aqui hubiera una
        # cadencia distinta, las pruebas no tocarian los umbrales que de verdad
        # gobiernan el icono y el tablero.
        cadencia    = [ordered]@{
            ventanas = @(
                @{ inicio = '04:00'; duracionHoras = 3 }
                @{ inicio = '12:00'; duracionHoras = 3 }
                @{ inicio = '19:00'; duracionHoras = 3 }
            )
            horasParaAvisar         = 22
            recuperarCorridaPerdida = $true
        }
        testigo     = @{ proveedor = 'ninguno'; medida = 'pruebas'; latidosPorDia = 3; periodHoras = 12; graceHoras = 10 }
        deudaPrimeraCorrida = [ordered]@{
            fechaDeLaSubidaManual = '2026-09-02'
            totalArchivos = 0
            totalBytes    = 0
            raices        = $deuda
            saldada       = [bool]$Saldada
        }
    }

    $json = $cfg | ConvertTo-Json -Depth 12
    Set-ContenidoAtomico -Ruta $Caja.Config -Contenido "// arenero de pruebas, generado`n$json" -Confirm:$false
}

function Invoke-Motor {
    <#
        .SYNOPSIS
            Llama al motor con la configuracion del arenero.
    #>
    [CmdletBinding()]
    [OutputType([psobject])]
    param(
        [Parameter(Mandatory)][psobject] $Caja,
        [switch] $Simular,
        [switch] $OmitirDeuda
    )
    $motor = Join-Path (Split-Path $PSScriptRoot -Parent) '2-Nucleo\respaldo.ps1'
    return & $motor -RutaConfiguracion $Caja.Config `
        -SoloSimular:$Simular -OmitirDeuda:$OmitirDeuda -Confirm:$false
}

# ===========================================================================
#  PRUEBAS DE UNIDAD - las piezas que ya mordieron una vez
# ===========================================================================

Write-Titulo 'Unidad: las trampas ya pagadas'

# La barra que faltaba el 2026-09-02. Es LA prueba de esta funcion.
Test-Afirmacion -Nombre 'UNC correcta se acepta' `
    -Esperado $true  -Obtenido (Test-RutaUnc -Ruta '\\192.168.1.38\datos')
Test-Afirmacion -Nombre 'UNC con UNA barra se RECHAZA (el fallo del 02/09)' `
    -Esperado $false -Obtenido (Test-RutaUnc -Ruta '\192.168.1.38\datos')
Test-Afirmacion -Nombre 'Ruta local se rechaza' `
    -Esperado $false -Obtenido (Test-RutaUnc -Ruta 'C:\192.168.1.38\datos')
Test-Afirmacion -Nombre 'UNC sin recurso se rechaza' `
    -Esperado $false -Obtenido (Test-RutaUnc -Ruta '\\192.168.1.38')

# JSONC: una barra doble dentro de una cadena NO es un comentario.
$jsoncPrueba = @'
{
  // esto si es comentario
  "url": "https://ejemplo.com/x", /* y esto tambien */
  "ruta": "C:\\dev"
}
'@
$parseado = ConvertFrom-Jsonc -Texto $jsoncPrueba
Test-Afirmacion -Nombre 'JSONC no destroza una URL con //' `
    -Esperado 'https://ejemplo.com/x' -Obtenido $parseado.url
Test-Afirmacion -Nombre 'JSONC conserva rutas escapadas' `
    -Esperado 'C:\dev' -Obtenido $parseado.ruta

# Huella en minusculas: el cotejo que dio "27 508 diferencias" que eran cero.
$tmpHuella = Join-Path $env:TEMP ('h_{0}.txt' -f ([guid]::NewGuid().ToString('N')))
'contenido' | Set-Content -LiteralPath $tmpHuella -Encoding UTF8
$huella = Get-HuellaDeArchivo -Ruta $tmpHuella
Test-Afirmacion -Nombre 'La huella sale en minusculas siempre' `
    -Esperado $huella -Obtenido $huella.ToLowerInvariant()
Remove-Item -LiteralPath $tmpHuella -Force

# Exclusiones: un nombre suelto casa SEGMENTO, no trozo de palabra.
Test-Afirmacion -Nombre 'Exclusion por segmento: Juegos excluye ...\Juegos\...' `
    -Esperado $true  -Obtenido (Test-RutaExcluida -Ruta 'C:\x\Juegos\y.txt' -Exclusiones @('Juegos'))
Test-Afirmacion -Nombre 'Exclusion por segmento NO excluye JuegosPunk_Fotos' `
    -Esperado $false -Obtenido (Test-RutaExcluida -Ruta 'C:\x\JuegosPunk_Fotos\y.txt' -Exclusiones @('Juegos'))
Test-Afirmacion -Nombre 'Exclusion por comodin *.vmdk' `
    -Esperado $true  -Obtenido (Test-RutaExcluida -Ruta 'D:\vm\disco.vmdk' -Exclusiones @('*.vmdk'))

# Traduccion de rutas: la mas larga gana, o el motor duplica el arbol del nodo.
$trad = @(
    [pscustomobject]@{ de = 'C:\Users\usuario'; a = 'C\Users_usuario' },
    [pscustomobject]@{ de = 'C:';             a = 'C' }
)
Test-Afirmacion -Nombre 'Traduccion: C:\dev -> C\dev' `
    -Esperado '\\n\d\EQUIPO-01\C\dev' `
    -Obtenido (Get-RutaEnDestino -RutaOrigen 'C:\dev' -RaizDestino '\\n\d\EQUIPO-01' -Traducciones $trad)
Test-Afirmacion -Nombre 'Traduccion: el perfil gana sobre C: (Users_usuario en un segmento)' `
    -Esperado '\\n\d\EQUIPO-01\C\Users_usuario\Documents\01_ACADEMICO' `
    -Obtenido (Get-RutaEnDestino -RutaOrigen 'C:\Users\usuario\Documents\01_ACADEMICO' -RaizDestino '\\n\d\EQUIPO-01' -Traducciones $trad)

# El numero es el interruptor.
Test-Afirmacion -Nombre 'NN_ enciende el respaldo'      -Esperado $true  -Obtenido (Test-CarpetaNumerada -Nombre '09_ARCHIVO')
Test-Afirmacion -Nombre 'Sin numero no se copia'        -Esperado $false -Obtenido (Test-CarpetaNumerada -Nombre 'Audacity')
Test-Afirmacion -Nombre 'Guion bajo solo no es numero'  -Esperado $false -Obtenido (Test-CarpetaNumerada -Nombre '_CUARENTENA')

# ===========================================================================
#  LOS CRITERIOS DE LA SECCION 15
# ===========================================================================

Write-Titulo 'Criterios de aceptacion, sobre el arenero'

$caja = New-CajaDePrueba -Carpeta $CarpetaCaja -Confirm:$false
Write-ConfiguracionDeCaja -Caja $caja -Confirm:$false
$cfg = Get-ConfiguracionRespaldo -Ruta $caja.Config

# --- Criterio 4: lo que no lleva numero sale como huerfano -----------------
$huerfanos = Get-HuerfanosDeRespaldo -Configuracion $cfg
Test-Afirmacion -Criterio '4' -Nombre 'La carpeta sin numero aparece como huerfana' `
    -Esperado 1 -Obtenido @($huerfanos | Where-Object { $_.Ruta -like '*SinNumero' }).Count

# --- Estado inicial: tres raices resueltas ---------------------------------
$raices0 = Get-RaicesDeRespaldo -Configuracion $cfg
Test-Afirmacion -Nombre 'Se resuelven las tres raices del arenero' `
    -Esperado 3 -Obtenido $raices0.Count

# --- La deuda de la seccion 8: ABORTA si sobra o falta una raiz ------------
Write-Titulo 'La deuda de la seccion 8 (el aviso que no es cosmetico)'
Write-ConfiguracionDeCaja -Caja $caja -DeudaRota -Confirm:$false
$deudaR = Invoke-Motor -Caja $caja -Simular 2>$null
Test-Afirmacion -Criterio '8' -Nombre 'Con una raiz declarada de mas, la corrida ABORTA' `
    -Esperado $true -Obtenido $deudaR.Abortada
Test-Afirmacion -Criterio '8' -Nombre 'Y lo dice por la deuda, no por otra cosa' `
    -Esperado $true -Obtenido ($deudaR.Motivo -like 'DEUDA*')
Write-ConfiguracionDeCaja -Caja $caja -Confirm:$false

# --- Criterio 6: la medida informa, y ya no detiene (ADR-0085) -------------
# ESTA PRUEBA DECIA LO CONTRARIO HASTA EL 08/09/2026. Afirmaba que un destino
# vacio hacia saltar el freno y abortaba la corrida. Retirada la capa 2, la
# primera siembra COPIA -- que es justo lo que una primera siembra debe hacer --
# y la medida sigue estando ahi para que la opcion [2] la ensene.
Write-Titulo 'Criterio 6: la medida del cambio informa, no detiene'
$r6 = Invoke-Motor -Caja $caja -Simular
Test-Afirmacion -Criterio '6' -Nombre 'Destino vacio: la corrida NO se aborta' `
    -Esperado $false -Obtenido $r6.Abortada
Test-Afirmacion -Criterio '6' -Nombre 'Y la medida se tomo igual, raiz por raiz' `
    -Esperado 3 -Obtenido @($r6.Cambios).Count
Test-Afirmacion -Criterio '6' -Nombre 'Reconoce que es primera siembra, no un cifrado' `
    -Esperado $true -Obtenido (@($r6.Cambios | Where-Object { $_.PrimeraSiembra }).Count -eq 3)

# --- Primera copia real -----------------------------------------------------
Write-Titulo 'Primera copia'
$r7 = Invoke-Motor -Caja $caja
Test-Afirmacion -Nombre 'La corrida no aborta' -Esperado $false -Obtenido $r7.Abortada
Test-Afirmacion -Nombre 'Las tres raices se copiaron' -Esperado 3 -Obtenido @($r7.Copias | Where-Object { $_.Correcto }).Count

$dDocs  = "$($caja.Destino)\CAJA\origen\Documents\01_DOCS"
$dFotos = "$($caja.Destino)\CAJA\origen\Pictures\01_FOTOS"
Test-Afirmacion -Nombre 'Los 6 documentos llegaron' -Esperado 6 -Obtenido @(Get-ChildItem -LiteralPath $dDocs -File -ErrorAction SilentlyContinue).Count
Test-Afirmacion -Nombre 'Las 4 fotos llegaron'      -Esperado 4 -Obtenido @(Get-ChildItem -LiteralPath $dFotos -File -ErrorAction SilentlyContinue).Count
Test-Afirmacion -Criterio '4' -Nombre 'La carpeta sin numero NO se copio' `
    -Esperado $false -Obtenido (Test-Path -LiteralPath "$($caja.Destino)\CAJA\origen\Documents\SinNumero")

# --- Criterio 2: clase A, un borrado local NO viaja ------------------------
Write-Titulo 'Criterio 2: clase A preserva'
Remove-Item -LiteralPath "$($caja.Origen)\Pictures\01_FOTOS\foto1.txt" -Force
Invoke-Motor -Caja $caja | Out-Null
Test-Afirmacion -Criterio '2' -Nombre 'Clase A: el archivo borrado en local SIGUE en el destino' `
    -Esperado $true -Obtenido (Test-Path -LiteralPath "$dFotos\foto1.txt")
Test-Afirmacion -Criterio '2' -Nombre 'Clase A: el destino conserva los 4' `
    -Esperado 4 -Obtenido @(Get-ChildItem -LiteralPath $dFotos -File).Count

# --- Criterio 3: clase B, el espejo si borra -------------------------------
Write-Titulo 'Criterio 3: clase B espeja'
Remove-Item -LiteralPath "$($caja.Origen)\Documents\01_DOCS\doc1.txt" -Force
Invoke-Motor -Caja $caja | Out-Null
Test-Afirmacion -Criterio '3' -Nombre 'Clase B: el archivo borrado en local DESAPARECE del destino' `
    -Esperado $false -Obtenido (Test-Path -LiteralPath "$dDocs\doc1.txt")
Test-Afirmacion -Criterio '3' -Nombre 'Clase B: el destino queda en 5' `
    -Esperado 5 -Obtenido @(Get-ChildItem -LiteralPath $dDocs -File).Count

# --- El freno y la clase A: el ambar del 2026-09-04 ------------------------
#
# LO QUE PASO. `Pictures\01_Capturas`, clase A, 22 archivos: 4 capturas nuevas
# que copiar y 6 sobrantes del 02/09 que el destino guarda PORQUE CLASE A
# GUARDA. El freno sumaba las dos cosas -10 de 22, 45.45 %- y se plantaba: 45
# > 5 y 10 >= 10. La corrida no copio nada y el icono paso la noche en ambar
# por los seis archivos que el criterio 2 existe para conservar.
#
# Estas pruebas fijan la regla: un sobrante del destino solo cuenta para el
# freno si la clase de esa raiz lo fuera a borrar de verdad.
Write-Titulo 'El freno no cuenta como borrado lo que la clase A no borra (04/09/2026)'

# `foto1.txt` se borro en local dos bloques mas arriba y el criterio 2 acaba de
# comprobar que SIGUE en el destino. Ese es el sobrante, y es un acierto.
$raizA  = [pscustomobject]@{ Ruta = "$($caja.Origen)\Pictures\01_FOTOS"; Clase = 'A' }
$frenoA = Measure-CambioDeRaiz -Raiz $raizA -Destino $dFotos

Test-Afirmacion -Nombre 'Clase A: el sobrante del destino se SIGUE viendo' `
    -Esperado 1 -Obtenido $frenoA.ABorrar
Test-Afirmacion -Nombre 'Clase A: pero no borraria ninguno, porque /E /XO no borra' `
    -Esperado 0 -Obtenido $frenoA.Borraria
Test-Afirmacion -Nombre 'Clase A: el sobrante NO cuenta como afectado' `
    -Esperado 0 -Obtenido $frenoA.Afectados
Test-Afirmacion -Nombre 'Clase A: y por tanto el porcentaje sale a 0, no a 45.45' `
    -Esperado 0 -Obtenido $frenoA.Porcentaje

# La otra mitad de la regla: con clase B el sobrante SI se va a borrar, asi que
# tiene que seguir contando. Si esto se rompiera, el parche habria desarmado el
# freno en la unica clase donde un borrado masivo es posible.
'sobrante que el espejo se llevaria' | Set-Content -LiteralPath "$dDocs\_sobrante.txt" -Encoding UTF8
$raizB  = [pscustomobject]@{ Ruta = "$($caja.Origen)\Documents\01_DOCS"; Clase = 'B' }
$frenoB = Measure-CambioDeRaiz -Raiz $raizB -Destino $dDocs

Test-Afirmacion -Criterio '3' -Nombre 'Clase B: el sobrante SI se borraria' `
    -Esperado 1 -Obtenido $frenoB.Borraria
Test-Afirmacion -Criterio '3' -Nombre 'Clase B: y por tanto SI cuenta como afectado' `
    -Esperado $true -Obtenido ($frenoB.Afectados -ge 1)
Remove-Item -LiteralPath "$dDocs\_sobrante.txt" -Force

# LA DEFENSA NO SE DEBILITA, y esta es la prueba que lo dice. Un cifrado masivo
# reescribe los archivos DEL ORIGEN, y eso sale del lado del origen: cuenta
# entero en ACopiar aunque la clase sea A.
Get-ChildItem -LiteralPath "$($caja.Origen)\Pictures\01_FOTOS" -File | ForEach-Object {
    'CIFRADO POR ALGUIEN QUE NO ERA EL DUENO' | Set-Content -LiteralPath $_.FullName -Encoding UTF8
}
$frenoCifrado = Measure-CambioDeRaiz -Raiz $raizA -Destino $dFotos
Test-Afirmacion -Criterio '6' -Nombre 'Clase A: un cifrado masivo del origen SIGUE contandose entero' `
    -Esperado $true -Obtenido ($frenoCifrado.Porcentaje -ge 50)
Test-Afirmacion -Criterio '6' -Nombre 'Y frena por los archivos reescritos, no por los sobrantes' `
    -Esperado 3 -Obtenido $frenoCifrado.Afectados

# ---------------------------------------------------------------------------
#  LA PASADA EN SECO TIENE QUE DAR SIEMPRE EL MISMO NUMERO (2026-09-08)
#
#  Estas pruebas no existian, y son las que habrian cazado el defecto. Medido
#  contra el nodo el 2026-09-08, seis pasadas sobre 'C:\dev' con el disco en el
#  MISMO estado: con /MT:8 el conteo de sobrantes salio 411, 438, 488 y 550;
#  sin /MT salio 551 las tres veces, y cero lineas sin clasificar.
#
#  El freno de la capa 2 decide con ese numero. Un umbral alimentado por el
#  planificador de hilos falla en la direccion peligrosa: si se pierden
#  bastantes lineas de sobrante, el porcentaje baja del umbral y EL FRENO NO
#  SALTA. Por eso esto es una prueba de seguridad y no de rendimiento.
# ---------------------------------------------------------------------------
Write-Titulo 'La medida del freno es repetible: la pasada en seco no lleva /MT (08/09/2026)'

# LA PRUEBA QUE FIJA EL CONTRATO. El arenero es pequeno y ocho hilos casi nunca
# llegan a pisarse ahi, asi que contar tres veces no bastaria para cazar una
# regresion: lo que se afirma es la BANDERA, que si es determinista.
$vSeco = & { Invoke-Robocopy -Origen $raizB.Ruta -Destino $dDocs -Clase 'B' -SoloListar -Verbose } 4>&1 |
    Where-Object { $_ -is [System.Management.Automation.VerboseRecord] } |
    ForEach-Object { '' + $_.Message }
Test-Afirmacion -Nombre 'La pasada en seco NO lleva /MT: la medida del freno no se sortea' `
    -Esperado $false -Obtenido ([bool](@($vSeco) -match '/MT'))
Test-Afirmacion -Nombre 'Y sigue llevando /L: en seco no se escribe nada' `
    -Esperado $true -Obtenido ([bool](@($vSeco) -match '/L\b'))

# LA OTRA MITAD: la copia real conserva los ocho hilos. Ahi el entrelazado
# desordena el informe, no el trabajo, y ningun umbral se decide con esa salida.
# Sin esta prueba, "quitar /MT" se podria haber aplicado a las dos y el respaldo
# entero iria mas lento sin ganar nada.
$vReal = & { Invoke-Robocopy -Origen $raizB.Ruta -Destino $dDocs -Clase 'B' -Verbose } 4>&1 |
    Where-Object { $_ -is [System.Management.Automation.VerboseRecord] } |
    ForEach-Object { '' + $_.Message }
Test-Afirmacion -Nombre 'La copia real SI conserva /MT:8: alli el entrelazado no decide nada' `
    -Esperado $true -Obtenido ([bool](@($vReal) -match '/MT:8'))

# Y el conteo, tres veces seguidas sobre el mismo disco, tiene que ser el mismo.
# En el arenero es una comprobacion barata; contra el nodo es la que fallaba.
$repeticiones = @(1..3 | ForEach-Object {
    $r = Invoke-Robocopy -Origen $raizB.Ruta -Destino $dDocs -Clase 'B' -SoloListar
    '{0}/{1}/{2}' -f $r.NumACopiar, $r.NumABorrar, $r.SinClasificar.Count
})
Test-Afirmacion -Nombre 'Tres pasadas en seco seguidas dan el mismo conteo, al archivo' `
    -Esperado 1 -Obtenido (@($repeticiones | Sort-Object -Unique).Count)
Test-Afirmacion -Nombre 'Y ninguna fabrica lineas sin clasificar de la nada' `
    -Esperado $true -Obtenido ([bool](@($repeticiones) -match '/0$'))

# --- Criterio 1: una carpeta nueva entra sola ------------------------------
Write-Titulo 'Criterio 1: el numero es el interruptor'
New-Item -ItemType Directory -Path "$($caja.Origen)\Documents\09_Loquesea" -Force | Out-Null
'algo nuevo' | Set-Content -LiteralPath "$($caja.Origen)\Documents\09_Loquesea\nuevo.txt" -Encoding UTF8
# La deuda ya esta saldada -es una puerta de una sola vez-, asi que una carpeta
# numerada nueva es operacion normal. La configuracion NO se toca: sigue
# declarando los mismos contenedores y las mismas raices que antes.
Write-ConfiguracionDeCaja -Caja $caja -Saldada -Confirm:$false
$r10 = Invoke-Motor -Caja $caja
Test-Afirmacion -Criterio '1' -Nombre 'La carpeta nueva se respalda SIN editar la tabla de raices' `
    -Esperado $true -Obtenido (Test-Path -LiteralPath "$($caja.Destino)\CAJA\origen\Documents\09_Loquesea\nuevo.txt")
Test-Afirmacion -Criterio '1' -Nombre 'Y la deuda saldada ya no aborta las corridas normales' `
    -Esperado $false -Obtenido $r10.Abortada

# --- Criterio 5: centinela alterado -> aborta sin escribir nada ------------
Write-Titulo 'Criterio 5: los centinelas'
$cent = New-Centinela -Carpeta "$($caja.Origen)\Documents\01_DOCS" -Confirm:$false
Write-ConfiguracionDeCaja -Caja $caja -Centinelas @($cent) -Saldada -Confirm:$false
$r11 = Invoke-Motor -Caja $caja
Test-Afirmacion -Criterio '5' -Nombre 'Con el centinela intacto, la corrida procede' `
    -Esperado $false -Obtenido $r11.Abortada

$antesDeTocar = @(Get-ChildItem -LiteralPath $dDocs -File).Count
'ALGO LO TOCO' | Add-Content -LiteralPath $cent.ruta -Encoding UTF8
Remove-Item -LiteralPath "$($caja.Origen)\Documents\01_DOCS\doc2.txt" -Force  # un borrado que el espejo propagaria
$r12 = Invoke-Motor -Caja $caja
Test-Afirmacion -Criterio '5' -Nombre 'Centinela alterado: la corrida ABORTA' `
    -Esperado $true -Obtenido $r12.Abortada
Test-Afirmacion -Criterio '5' -Nombre 'Y aborta POR el centinela' `
    -Esperado $true -Obtenido ($r12.Motivo -like 'CENTINELA*')
Test-Afirmacion -Criterio '5' -Nombre 'Y NO ESCRIBIO NADA: el destino no cambio' `
    -Esperado $antesDeTocar -Obtenido @(Get-ChildItem -LiteralPath $dDocs -File).Count

# Un centinela BORRADO cuenta igual que uno alterado.
Remove-Item -LiteralPath $cent.ruta -Force
$r13 = Invoke-Motor -Caja $caja
Test-Afirmacion -Criterio '5' -Nombre 'Centinela BORRADO tambien aborta' `
    -Esperado $true -Obtenido ($r13.Abortada -and $r13.Motivo -like 'CENTINELA*')

# --- Guarda de origen: un origen vacio no vacia el destino -----------------
Write-Titulo 'Guarda de origen (el desastre que el freno no ve)'
Write-ConfiguracionDeCaja -Caja $caja -Saldada -Confirm:$false
Get-ChildItem -LiteralPath "$($caja.Origen)\proyecto" -Recurse -File | Remove-Item -Force
$r14 = Invoke-Motor -Caja $caja -OmitirDeuda
Test-Afirmacion -Nombre 'Origen vacio con clase B: ABORTA en vez de vaciar el destino' `
    -Esperado $true -Obtenido $r14.Abortada
Test-Afirmacion -Nombre 'Y el destino conserva sus archivos' `
    -Esperado 5 -Obtenido @(Get-ChildItem -LiteralPath "$($caja.Destino)\CAJA\origen\proyecto" -File -Recurse).Count

# --- ADR-0080: los secretos se detectan y se ensenan -----------------------
Write-Titulo 'ADR-0080: deteccion de secretos'
'CLAVE=xxx' | Set-Content -LiteralPath "$($caja.Origen)\Documents\01_DOCS\.env" -Encoding UTF8
'llave'     | Set-Content -LiteralPath "$($caja.Origen)\Documents\01_DOCS\privada.key" -Encoding UTF8
$cfgS = Get-ConfiguracionRespaldo -Ruta $caja.Config
$raicesS = Get-RaicesDeRespaldo -Configuracion $cfgS
$secretos = Find-SecretoEnRaiz -Raices $raicesS -Configuracion $cfgS
Test-Afirmacion -Nombre 'Detecta el .env y la .key' -Esperado 2 -Obtenido $secretos.Count
Test-Afirmacion -Nombre 'Y NO los excluye: siguen en la raiz que se copia' `
    -Esperado $true -Obtenido (Test-Path -LiteralPath "$($caja.Origen)\Documents\01_DOCS\.env")

# ===========================================================================
#  FASE 3 - estado, avisos, testigo e indicador
# ===========================================================================

Write-Titulo 'Fase 3: la capa de estado'

$cajaEstado = Join-Path $CarpetaCaja 'estado'
New-Item -ItemType Directory -Path $cajaEstado -Force | Out-Null

Write-EstadoRespaldo -Estado 'Protegido' -Detalle 'prueba' -Datos @{ raices = 3 } -Carpeta $cajaEstado -Confirm:$false
$e = Read-EstadoRespaldo -Carpeta $cajaEstado
Test-Afirmacion -Nombre 'ESTADO.txt se escribe y se relee' -Esperado 'Protegido' -Obtenido ('' + $e['estado'])
Test-Afirmacion -Nombre 'ESTADO.txt conserva los datos extra' -Esperado '3' -Obtenido ('' + $e['raices']).Trim()

# Un ESTADO.txt corrupto NO puede tumbar al indicador: un icono ausente se
# parece a "todo bien", que es el punto ciego declarado de la seccion 10.2.
'basura sin formato' | Set-Content -LiteralPath (Join-Path $cajaEstado 'ESTADO.txt') -Encoding UTF8
Test-Afirmacion -Nombre 'Un ESTADO.txt corrupto devuelve SinDatos, no una excepcion' `
    -Esperado 'SinDatos' -Obtenido ('' + (Read-EstadoRespaldo -Carpeta $cajaEstado)['estado'])

Remove-Item -LiteralPath (Join-Path $cajaEstado 'ESTADO.txt') -Force
Test-Afirmacion -Nombre 'Sin ESTADO.txt tambien devuelve SinDatos' `
    -Esperado 'SinDatos' -Obtenido ('' + (Read-EstadoRespaldo -Carpeta $cajaEstado)['estado'])

Write-Titulo 'Criterio 9: matar el motor a media corrida'

$sinMarca = Get-MarcaDeCorrida -Carpeta $cajaEstado
Test-Afirmacion -Nombre 'Sin marca, no hay corrida en curso' -Esperado $false -Obtenido $sinMarca.Existe

Enter-MarcaDeCorrida -Carpeta $cajaEstado -Confirm:$false | Out-Null
$viva = Get-MarcaDeCorrida -Carpeta $cajaEstado
Test-Afirmacion -Nombre 'Marca recien puesta: viva, no vieja' -Esperado $true -Obtenido ($viva.Existe -and -not $viva.Vieja)

# El motor muere: la marca queda y su PID ya no existe. Es FALLA, no "copiando".
$pidMuerto = (Start-Process -FilePath 'cmd.exe' -ArgumentList '/c','exit' -PassThru -WindowStyle Hidden).Id
Start-Sleep -Milliseconds 400
"pid=$pidMuerto`ninicio=$((Get-Date).ToString('s'))`nequipo=CAJA`n" |
    Set-Content -LiteralPath (Join-Path $cajaEstado 'EN_CURSO.lock') -Encoding UTF8
$muerta = Get-MarcaDeCorrida -Carpeta $cajaEstado
Test-Afirmacion -Criterio '9' -Nombre 'Marca reciente con el proceso MUERTO cuenta como vieja (arranco y no termino)' `
    -Esperado $true -Obtenido ($muerta.Existe -and $muerta.Vieja -and -not $muerta.ProcesoVivo)

Exit-MarcaDeCorrida -Carpeta $cajaEstado -Confirm:$false
Test-Afirmacion -Criterio '9' -Nombre 'La marca se retira al terminar' `
    -Esperado $false -Obtenido (Get-MarcaDeCorrida -Carpeta $cajaEstado).Existe

Write-Titulo 'La costura de avisos (seccion 11)'

Test-Afirmacion -Nombre 'OK traduce a verde'          -Esperado 'verde'    -Obtenido (ConvertTo-SeveridadDelNodo -Nivel 'OK')
Test-Afirmacion -Nombre 'ATENCION traduce a amarillo' -Esperado 'amarillo' -Obtenido (ConvertTo-SeveridadDelNodo -Nivel 'ATENCION')
Test-Afirmacion -Nombre 'FRENO traduce a rojo'        -Esperado 'rojo'     -Obtenido (ConvertTo-SeveridadDelNodo -Nivel 'FRENO')
Test-Afirmacion -Nombre 'ERROR traduce a rojo'        -Esperado 'rojo'     -Obtenido (ConvertTo-SeveridadDelNodo -Nivel 'ERROR')

$cajaSistema = Join-Path $CarpetaCaja '_SISTEMA'
$verde = Send-EventoAlNodo -Nivel 'OK' -Situacion 'p.ok' -Mensaje 'todo bien' -RutaSistema $cajaSistema -Confirm:$false
Test-Afirmacion -Nombre 'El exito diario NO notifica (regla de ruido)' -Esperado $false -Obtenido $verde.Emitido

$rojo = Send-EventoAlNodo -Nivel 'ERROR' -Situacion 'p.error' -Mensaje 'algo fallo' -Hechos @{ n = 1 } -RutaSistema $cajaSistema -Confirm:$false
Test-Afirmacion -Nombre 'Un ERROR si se emite' -Esperado $true -Obtenido $rojo.Emitido
$jsonl = Join-Path $cajaSistema 'eventos-cliente.jsonl'
Test-Afirmacion -Nombre 'El evento aterriza como una linea JSON' -Esperado 1 -Obtenido @(Get-Content -LiteralPath $jsonl).Count
$leido = Get-Content -LiteralPath $jsonl -Raw | ConvertFrom-Json
Test-Afirmacion -Nombre 'Y lleva la severidad del NODO, no la del cliente' -Esperado 'rojo' -Obtenido $leido.severidad

# Que el canal falle NO puede tumbar la corrida.
$sinDestino = Send-EventoAlNodo -Nivel 'ERROR' -Situacion 'p.x' -Mensaje 'y' -RutaSistema 'Z:\no\existe\nunca' -Confirm:$false
Test-Afirmacion -Nombre 'Si el nodo no responde, el aviso no lanza: se anota y se sigue' `
    -Esperado $false -Obtenido $sinDestino.Emitido

Write-Titulo 'El testigo (ADR-0079): el verde no se puede fingir'

$falso = Send-LatidoDelCliente -Senal 'Bien' -Url 'https://ejemplo.invalido/x' -Confirm:$false -WhatIf
Test-Afirmacion -Nombre 'Pedir "Bien" SIN corrida ni verificacion se degrada a Mal' `
    -Esperado 'Mal' -Obtenido $falso.Senal
$soloCopia = Send-LatidoDelCliente -Senal 'Bien' -CorridaTermino -Url 'https://ejemplo.invalido/x' -Confirm:$false -WhatIf
Test-Afirmacion -Nombre 'Copiar sin verificar TAMPOCO da verde' `
    -Esperado 'Mal' -Obtenido $soloCopia.Senal
$ambas = Send-LatidoDelCliente -Senal 'Bien' -CorridaTermino -VerificacionPaso -Url 'https://ejemplo.invalido/x' -Confirm:$false -WhatIf
Test-Afirmacion -Nombre 'Solo con las DOS condiciones el latido sale verde' `
    -Esperado 'Bien' -Obtenido $ambas.Senal
# CUIDADO CON ESTA PRUEBA: `-Url ''` NO significa "sin URL". Significa "cae al
# Administrador de credenciales". Mientras la credencial no existia daba igual, y
# el 2026-09-03, en cuanto se guardo la de verdad, ESTA LINEA MANDO UN /fail AL
# CHECK REAL y lo puso en rojo. La condicion honesta es nombrar una entrada que
# no existe, no pasar una cadena vacia y confiar en que nadie configure nada.
$sinUrl = Send-LatidoDelCliente -Senal 'Mal' -NombreEnElAlmacen 'NasRespaldo:EntradaQueNoExiste' -Confirm:$false
Test-Afirmacion -Nombre 'Sin credencial configurada no falla: lo dice y sigue' `
    -Esperado $false -Obtenido $sinUrl.Enviado
Test-Afirmacion -Nombre 'Y NO se envio nada: el camino sin credencial vuelve antes de la red' `
    -Esperado $true -Obtenido ($sinUrl.Motivo -like '*no esta configurado*')

# GUARDA: NINGUNA PRUEBA PUEDE ALCANZAR EL TESTIGO REAL. Se comprueba sobre el
# texto de este mismo archivo, porque el dano no lo hace una funcion mal escrita
# sino una llamada distraida -- que es exactamente como ocurrio.
#
# EL NOMBRE SE ARMA EN DOS TROZOS A PROPOSITO: escrito entero, esta linea se
# encontraria a si misma y la guarda fallaria siempre. Se vio a la primera
# corrida, que es justo lo que se le pide a una guarda.
$queBuscar = 'Send-Latido' + 'DelCliente'
$llamadasLatido = @(Get-Content -LiteralPath $PSCommandPath -Encoding UTF8 |
        Where-Object { $_ -match $queBuscar -and $_ -notmatch '^\s*#' })
$sinAtar = @($llamadasLatido | Where-Object { $_ -notmatch '-Url ' -and $_ -notmatch '-NombreEnElAlmacen ' })
Test-Afirmacion -Nombre 'Ninguna llamada al latido queda suelta: todas fijan Url o entrada del almacen' `
    -Esperado 0 -Obtenido $sinAtar.Count

# LA CONDICION DEL VERDE, QUE HASTA HOY NO EMITIA NADIE. respaldo.ps1 mandaba
# /start y /fail, y el "estoy bien" se habia quedado sin emisor porque exige algo
# que la copia no puede afirmar: que lo copiado SE LEE.
#
# ESTRUCTURA Y CONTENIDO NO SON LO MISMO, y confundirlos rompe el testigo en una
# direccion o en la otra.
$cotejoConPendientes = [pscustomobject]@{
    TodoCoincide = $false
    Coincidencias = @([pscustomobject]@{ Raiz = 'C:\dev'; Faltan = 10; Sobran = 0; Coincide = $false })
    Huellas = @([pscustomobject]@{ Raiz = 'C:\dev'; Comprobados = 10; Diferencias = @(); Correcto = $true })
}
$limpio = Test-CotejoLimpio -Informe $cotejoConPendientes
Test-Afirmacion -Nombre 'Archivos PENDIENTES de copiar NO tumban el verde: no son un fallo de la copia' `
    -Esperado $true -Obtenido $limpio.Limpio
Test-Afirmacion -Nombre 'Y se sabe con cuantos archivos leidos se afirma' `
    -Esperado 10 -Obtenido $limpio.Leidos

$cotejoPodridoT = [pscustomobject]@{
    TodoCoincide = $true
    Coincidencias = @([pscustomobject]@{ Raiz = 'C:\dev'; Faltan = 0; Sobran = 0; Coincide = $true })
    Huellas = @([pscustomobject]@{ Raiz = 'C:\dev'; Comprobados = 10
            Diferencias = @([pscustomobject]@{ Ruta = 'a.txt'; Motivo = 'HUELLA DISTINTA' })
            Correcto = $false
        })
}
Test-Afirmacion -Nombre 'Una HUELLA distinta si lo tumba: lo guardado no es lo que se creia tener' `
    -Esperado $false -Obtenido (Test-CotejoLimpio -Informe $cotejoPodridoT).Limpio

# "NO PUDE MIRAR" NO ES "MIRE Y ESTA BIEN". Con el nodo caido no se lee ni un
# archivo, asi que hay cero diferencias POR DEFINICION. Sin la condicion de haber
# leido algo, un nodo muerto daria VERDE -- la mentira mas cara del testigo.
$cotejoCiegoT = [pscustomobject]@{ TodoCoincide = $false; Coincidencias = @(); Huellas = @() }
Test-Afirmacion -Nombre 'Sin haber leido NI UN archivo no hay verde: cero diferencias no es cero riesgo' `
    -Esperado $false -Obtenido (Test-CotejoLimpio -Informe $cotejoCiegoT).Limpio
Test-Afirmacion -Nombre 'Un informe nulo tampoco da verde, y no revienta' `
    -Esperado $false -Obtenido (Test-CotejoLimpio -Informe $null).Limpio

# EL TESTIGO VIGILA EL AUTOMATISMO, NO A LA PERSONA. El /start salia en TODA
# corrida, tambien en la lanzada a mano desde el menu; y como el verde exige una
# verificacion que la corrida a mano no hace, el check quedaba ABIERTO y sin
# cerrar -- pasado el margen, Telegram avisaria de un sistema sano.
$fuenteMotor = Get-Content -LiteralPath (Join-Path (Split-Path $PSScriptRoot -Parent) '2-Nucleo\respaldo.ps1') -Raw -Encoding UTF8
Test-Afirmacion -Nombre 'El /start del testigo solo sale en la corrida PROGRAMADA' `
    -Esperado $true -Obtenido ($fuenteMotor -match "if \(\`$Programada\) \{[^}]*Senal 'Inicio'")
Test-Afirmacion -Nombre 'Y verificar.ps1 se invoca con & y NUNCA con punto (tiene param)' `
    -Esperado $true -Obtenido ($fuenteMotor -match '& "\$PSScriptRoot\\verificar\.ps1" @argsCotejo')

Write-Titulo 'Criterios 8 y 10: la tarea programada'

$indicador = Join-Path (Split-Path $PSScriptRoot -Parent) '1-Interfaz\indicador.ps1'
# Se le pasa un nombre de tarea que NO existe a proposito: la prueba mide que
# el indicador detecta su AUSENCIA, no si la tarea real esta registrada hoy.
$vista = & $indicador -UnaSolaLectura -NombreTarea 'NasRespaldo-QueNoExiste' -CarpetaEstado $cajaEstado
Test-Afirmacion -Criterio '10' -Nombre 'Sin tarea programada, el indicador lo detecta y pinta FALLA' `
    -Esperado 'Falla' -Obtenido $vista.Estado
Test-Afirmacion -Criterio '10' -Nombre 'Y dice que la causa es la tarea, no otra cosa' `
    -Esperado 'tarea' -Obtenido $vista.Fuente

$registrar = Join-Path (Split-Path $PSScriptRoot -Parent) '1-Interfaz\Registrar-Tarea.ps1'
(& $registrar -Accion Registrar -RutaConfiguracion $caja.Config -NombreTarea 'NasRespaldo-Pruebas' -Confirm:$false -WhatIf) 2>$null
Test-Afirmacion -Criterio '8' -Nombre 'Registrar-Tarea con -WhatIf no crea nada' `
    -Esperado $false -Obtenido ($null -ne (Get-ScheduledTask -TaskName 'NasRespaldo-Pruebas' -ErrorAction SilentlyContinue))

# El motor tiene que poder correr SIN menu y SIN indicador: se comprueba
# invocandolo como lo hara la tarea, en un proceso aparte y no interactivo.
# Se repuebla `proyecto` antes: la prueba de la guarda de origen lo dejo vacio a
# proposito, y aqui lo que se mide es OTRA cosa.
'codigo repuesto' | Set-Content -LiteralPath "$($caja.Origen)\proyecto\src\mod1.txt" -Encoding UTF8
Write-ConfiguracionDeCaja -Caja $caja -Saldada -Confirm:$false

$motorRuta = Join-Path (Split-Path $PSScriptRoot -Parent) '2-Nucleo\respaldo.ps1'
$codigoTarea = & {
    $ErrorActionPreference = 'Continue'
    & powershell.exe -NoProfile -NonInteractive -ExecutionPolicy Bypass `
        -Command "& '$motorRuta' -RutaConfiguracion '$($caja.Config)' -SoloSimular -Confirm:`$false | Out-Null; exit 0" 2>&1 | Out-Null
    $LASTEXITCODE
}
Test-Afirmacion -Criterio '8' -Nombre 'El motor corre en un proceso NO interactivo, sin menu ni indicador' `
    -Esperado 0 -Obtenido $codigoTarea

# ===========================================================================
#  FASE 4 - el disco frio y sus dos pasadas
# ===========================================================================

Write-Titulo 'Fase 4: la traduccion de rutas del disco (seccion 6.2.bis)'

# La regla del disco traduce a CADENA VACIA -se quita el nivel 01_BACKUP-, que
# es el caso que producia una barra doble antes de arreglarlo.
$tradNodo = @(
    [pscustomobject]@{ de = '\\192.168.1.38\datos\01_BACKUP'; a = '' },
    [pscustomobject]@{ de = '\\192.168.1.38\datos';           a = '' }
)
Test-Afirmacion -Nombre 'Disco: se quita el nivel 01_BACKUP y no queda barra doble' `
    -Esperado 'F:\NODO-01\_HISTORICO' `
    -Obtenido (Get-RutaEnDestino -RutaOrigen '\\192.168.1.38\datos\01_BACKUP\_HISTORICO' -RaizDestino 'F:\NODO-01' -Traducciones $tradNodo)
Test-Afirmacion -Nombre 'Disco: homeUsers cuelga del mismo nombre de maquina' `
    -Esperado 'F:\NODO-01\homeUsers\ana\01_Fotos' `
    -Obtenido (Get-RutaEnDestino -RutaOrigen '\\192.168.1.38\datos\homeUsers\ana\01_Fotos' -RaizDestino 'F:\NODO-01' -Traducciones $tradNodo)

# LA PROPIEDAD DE SEGURIDAD DE LA SECCION 6.2.bis: las dos pasadas tienen una
# raiz cada una y NO PUEDEN PISARSE. Se comprueba, no se enuncia.
$destEquipo = Get-RutaEnDestino -RutaOrigen 'C:\dev' -RaizDestino 'F:\EQUIPO-01' -Traducciones $trad
$destNodo   = Get-RutaEnDestino -RutaOrigen '\\192.168.1.38\datos\01_BACKUP\_HISTORICO' -RaizDestino 'F:\NODO-01' -Traducciones $tradNodo
Test-Afirmacion -Nombre 'Las dos pasadas del disco no pueden pisarse: raices distintas' `
    -Esperado $false `
    -Obtenido ($destEquipo.StartsWith('F:\NODO-01') -or $destNodo.StartsWith('F:\EQUIPO-01'))

Write-Titulo 'Criterios 7 y 7b: el disco se reconoce por SERIE y CENTINELA'

. "$nucleo\disco.ps1"

# 7b - "conectar otro disco USB cualquiera": sin serie coincidente, nada.
$cfgSerieMala = [pscustomobject]@{
    destinos = [pscustomobject]@{ discoFrio = [pscustomobject]@{
        serie = 'ESTA-SERIE-NO-EXISTE'; centinela = '_COPIA_FRIA_ZZ000000000A1.txt' } }
}
Test-Afirmacion -Criterio '7b' -Nombre 'Sin serie coincidente NO se reconoce ningun disco' `
    -Esperado $true -Obtenido ($null -eq (Get-DiscoFrio -Configuracion $cfgSerieMala))

# 7b - y con la serie correcta pero SIN el centinela, tampoco se escribe.
$cfgSinCentinela = [pscustomobject]@{
    destinos = [pscustomobject]@{ discoFrio = [pscustomobject]@{
        serie = 'ZZ000000000A1'; centinela = '_centinela_que_no_existe.txt' } }
}
Test-Afirmacion -Criterio '7b' -Nombre 'Serie correcta pero SIN centinela: tampoco se escribe' `
    -Esperado $true -Obtenido ($null -eq (Get-DiscoFrio -Configuracion $cfgSinCentinela 2>$null))

# 7 - "conectar el disco con otra letra": la letra se DERIVA, no se declara.
$cfgReal = Get-ConfiguracionRespaldo
$discoReal = Get-DiscoFrio -Configuracion $cfgReal
if ($discoReal) {
    Test-Afirmacion -Criterio '7' -Nombre 'El disco real se reconoce por serie, y la letra se DERIVA' `
        -Esperado $true -Obtenido ($discoReal.Letra -and $discoReal.Raiz -like "$($discoReal.Letra):*")
    Test-Afirmacion -Criterio '7' -Nombre 'La letra NO aparece en la configuracion del disco' `
        -Esperado $false `
        -Obtenido (($cfgReal.destinos.discoFrio.PSObject.Properties.Name) -contains 'letra')
}
else {
    Test-Afirmacion -Criterio '7' -Nombre 'El disco frio NO esta conectado: los criterios 7 y 7c no se pueden comprobar hoy' `
        -Esperado 'sin disco' -Obtenido 'sin disco'
}

Write-Titulo 'SIMULAR NO ESCRIBE -- la prueba que faltaba el 2026-09-02'

# ESTA PRUEBA EXISTE POR UN FALLO REAL Y CARO. `disco.ps1` cargaba `respaldo.ps1`
# con punto para alcanzar Test-Centinela, y cargar con punto un guion que tiene
# param() DECLARA SUS VARIABLES CON EL VALOR POR OMISION en el ambito que lo
# carga: eso puso $SoloSimular en $false justo antes de usarlo, y una
# "simulacion" copio de verdad al disco frio.
#
# Nada en la suite lo habria detectado, porque todas las pruebas comprobaban lo
# que el motor HACE y ninguna comprobaba lo que NO debe hacer. Se mide contando
# archivos en el destino ANTES y DESPUES, que es la unica forma que no depende
# de creerse lo que el propio guion informa.
$cajaSim = Join-Path $CarpetaCaja 'simulacion'
New-Item -ItemType Directory -Path "$cajaSim\origen\01_COSAS","$cajaSim\destino" -Force | Out-Null
1..5 | ForEach-Object { "archivo $_" | Set-Content -LiteralPath "$cajaSim\origen\01_COSAS\a$_.txt" -Encoding UTF8 }

$antesSim = @(Get-ChildItem -LiteralPath "$cajaSim\destino" -Recurse -File -Force -ErrorAction SilentlyContinue).Count
$sec = Invoke-Robocopy -Origen "$cajaSim\origen\01_COSAS" -Destino "$cajaSim\destino\01_COSAS" -Clase 'A' -SoloListar
$despuesSim = @(Get-ChildItem -LiteralPath "$cajaSim\destino" -Recurse -File -Force -ErrorAction SilentlyContinue).Count

Test-Afirmacion -Nombre 'Simular dice que copiaria los 5' -Esperado 5 -Obtenido $sec.NumACopiar
Test-Afirmacion -Nombre 'Y NO escribe ni un archivo: 0 antes, 0 despues' `
    -Esperado $antesSim -Obtenido $despuesSim
Test-Afirmacion -Nombre 'El destino sigue sin existir siquiera' `
    -Esperado $false -Obtenido (Test-Path -LiteralPath "$cajaSim\destino\01_COSAS")

# Y la causa raiz, comprobada directamente: ningun guion del nucleo carga a otro
# guion que tenga param(). Si alguien lo reintroduce, esta prueba lo dice.
$conParam = @('respaldo.ps1', 'disco.ps1', 'semilla.ps1', 'verificar.ps1')
$cargasProhibidas = @()
foreach ($archivo in (Get-ChildItem -LiteralPath $nucleo -Filter *.ps1)) {
    $texto = Get-Content -LiteralPath $archivo.FullName -Raw
    foreach ($otro in $conParam) {
        if ($archivo.Name -eq $otro) { continue }
        if ($texto -match [regex]::Escape(". `"`$PSScriptRoot\$otro`"")) {
            $cargasProhibidas += "$($archivo.Name) carga $otro"
        }
    }
}
Test-Afirmacion -Nombre 'Ningun guion carga con punto a otro que tenga param()' `
    -Esperado 0 -Obtenido $cargasProhibidas.Count

Write-Titulo 'Criterio 7d: un archivo que no se puede copiar se REPORTA'

# El techo de 4 GB de FAT32 desaparecio al pasar a NTFS, asi que este criterio
# ya no se puede provocar con el tamano. Lo que sigue vivo de el, y es lo que
# importa, es la regla: un archivo que no se copia NUNCA se omite en silencio.
# Se provoca con un archivo BLOQUEADO en exclusiva, que robocopy no puede leer.
$cajaBloqueo = Join-Path $CarpetaCaja 'bloqueo'
New-Item -ItemType Directory -Path "$cajaBloqueo\origen","$cajaBloqueo\destino" -Force | Out-Null
'contenido normal' | Set-Content -LiteralPath "$cajaBloqueo\origen\normal.txt" -Encoding UTF8
'contenido bloqueado' | Set-Content -LiteralPath "$cajaBloqueo\origen\bloqueado.txt" -Encoding UTF8
$flujo = [System.IO.File]::Open("$cajaBloqueo\origen\bloqueado.txt", 'Open', 'Read', 'None')
try {
    $resBloqueo = Invoke-Robocopy -Origen "$cajaBloqueo\origen" -Destino "$cajaBloqueo\destino" -Clase 'A'
    Test-Afirmacion -Criterio '7d' -Nombre 'Un archivo ilegible NO se da por copiado en silencio' `
        -Esperado $false -Obtenido $resBloqueo.Correcto
    Test-Afirmacion -Criterio '7d' -Nombre 'Y queda constancia de por que: lineas sin clasificar' `
        -Esperado $true -Obtenido ($resBloqueo.SinClasificar.Count -gt 0 -or $resBloqueo.Codigo -ge 8)
    Test-Afirmacion -Criterio '7d' -Nombre 'El archivo que SI se podia copiar llego igualmente' `
        -Esperado $true -Obtenido (Test-Path -LiteralPath "$cajaBloqueo\destino\normal.txt")
}
finally { $flujo.Close(); $flujo.Dispose() }

# ===========================================================================
#  Resumen
# ===========================================================================
# EL PUNTO CIEGO DE /XO - archivos que una copia cortada deja rotos
# ===========================================================================
# Estas pruebas existen por un apagon del 2026-09-02 a media copia al disco
# frio. Ocho archivos quedaron con EL TAMANO CORRECTO Y EL CONTENIDO DISTINTO,
# con la fecha del momento del corte -mas nueva que la del origen-, y /XO los
# habria saltado en todas las corridas siguientes mientras robocopy devolvia 0
# y el motor los daba por buenos.
Write-Titulo 'Archivos a medias de una copia cortada'

$cajaVuelo = Join-Path $CarpetaCaja 'envuelo'
$origenV   = Join-Path $cajaVuelo 'origen'
$destinoV  = Join-Path $cajaVuelo 'destino'
New-Item -ItemType Directory -Path $origenV, $destinoV -Force | Out-Null

# 1. Sano: mismo contenido, misma fecha. No es hallazgo.
Set-Content -LiteralPath "$origenV\sano.txt"  -Value 'contenido bueno' -Encoding UTF8
Copy-Item   -LiteralPath "$origenV\sano.txt"  -Destination "$destinoV\sano.txt"
(Get-Item "$destinoV\sano.txt").LastWriteTime = (Get-Item "$origenV\sano.txt").LastWriteTime

# 2. El caso real: MISMO TAMANO, contenido distinto, destino mas nuevo.
Set-Content -LiteralPath "$origenV\roto.txt"   -Value 'AAAAAAAAAAAAAAA' -Encoding UTF8
Set-Content -LiteralPath "$destinoV\roto.txt"  -Value 'BBBBBBBBBBBBBBB' -Encoding UTF8
(Get-Item "$origenV\roto.txt").LastWriteTime  = (Get-Date).AddDays(-10)
(Get-Item "$destinoV\roto.txt").LastWriteTime = (Get-Date)

# 3. Truncado: tamano distinto.
Set-Content -LiteralPath "$origenV\corto.txt"  -Value 'esto es largo de verdad' -Encoding UTF8
Set-Content -LiteralPath "$destinoV\corto.txt" -Value 'esto' -Encoding UTF8

# 4. Solo en el destino: en clase A eso es LEGITIMO y NO es hallazgo.
Set-Content -LiteralPath "$destinoV\viejo.txt" -Value 'el origen ya lo borro' -Encoding UTF8

$vuelo = @(Get-ArchivosEnVuelo -Origen $origenV -Destino $destinoV)
$nombresVuelo = @($vuelo | ForEach-Object { $_.Relativa } | Sort-Object)

Test-Afirmacion -Nombre 'Encuentra los dos rotos y solo esos' `
    -Esperado @('corto.txt', 'roto.txt') -Obtenido $nombresVuelo
Test-Afirmacion -Nombre 'El sano NO se reporta' `
    -Esperado $false -Obtenido ($nombresVuelo -contains 'sano.txt')
Test-Afirmacion -Nombre 'Lo que solo esta en el destino NO es hallazgo (clase A no borra)' `
    -Esperado $false -Obtenido ($nombresVuelo -contains 'viejo.txt')

$motivoRoto = @($vuelo | Where-Object { $_.Relativa -eq 'roto.txt' }).Motivo
Test-Afirmacion -Nombre 'El de mismo tamano se marca por FECHA, que es lo que /XO esconde' `
    -Esperado 'DestinoMasNuevo' -Obtenido $motivoRoto

# LA HUELLA ES QUIEN DECIDE. El de mismo tamano solo se destapa aqui.
$huellasVuelo = @(Test-HuellaEnVuelo -EnVuelo $vuelo)
$rotoHuella = @($huellasVuelo | Where-Object { $_.Relativa -eq 'roto.txt' }).Identico
Test-Afirmacion -Nombre 'Mismo tamano y contenido distinto: la huella lo declara ROTO' `
    -Esperado $false -Obtenido $rotoHuella

# EL FALLO QUE COSTO UNA MEDICION FALSA EL 2026-09-02: con el origen caido, la
# primera version devolvia cero hallazgos, que se lee igual que "todo bien".
$origenCaido = Join-Path $cajaVuelo 'no-existe-este-origen'
$lanzo = $false
try { Get-ArchivosEnVuelo -Origen $origenCaido -Destino $destinoV | Out-Null }
catch { $lanzo = $true }
Test-Afirmacion -Nombre 'Un origen que NO responde falla a gritos, no devuelve cero hallazgos' `
    -Esperado $true -Obtenido $lanzo

# Reparar SOBRESCRIBE y NUNCA BORRA: el archivo que solo vive en el destino
# tiene que seguir ahi despues de reparar.
$antesReparar = @(Get-ChildItem -LiteralPath $destinoV -File).Count
Repair-ArchivosEnVuelo -EnVuelo $vuelo -Confirm:$false | Out-Null
$despuesReparar = @(Get-ChildItem -LiteralPath $destinoV -File).Count

Test-Afirmacion -Nombre 'Reparar no borra nada: el mismo numero de archivos antes y despues' `
    -Esperado $antesReparar -Obtenido $despuesReparar
Test-Afirmacion -Nombre 'Y lo que solo vivia en el destino sigue vivo' `
    -Esperado $true -Obtenido (Test-Path -LiteralPath "$destinoV\viejo.txt")
Test-Afirmacion -Nombre 'Despues de reparar no queda ni uno a medias' `
    -Esperado 0 -Obtenido @(Get-ArchivosEnVuelo -Origen $origenV -Destino $destinoV).Count

$huellaOrigen  = Get-HuellaDeArchivo -Ruta "$origenV\roto.txt"
$huellaDestino = Get-HuellaDeArchivo -Ruta "$destinoV\roto.txt"
Test-Afirmacion -Nombre 'Y el contenido reparado es identico por huella, no solo del mismo tamano' `
    -Esperado $huellaOrigen -Obtenido $huellaDestino

# ===========================================================================
# EL RITMO DEL ICONO - seccion 10.2
# ===========================================================================
# Hasta el 2026-09-02 el temporizador latia una vez cada DIEZ SEGUNDOS y el
# "pulso suave" de copiando estaba escrito en un comentario y no en el codigo.
# El icono no se movia, y ninguna prueba lo decia porque ninguna miraba el
# ritmo. Estas si.
Write-Titulo 'El ritmo del icono'

. (Join-Path (Split-Path $PSScriptRoot -Parent) '1-Interfaz\estilo.ps1')

# Cinco ticks son un segundo (200 ms cada uno).
$pulsoFalla = @(0..9 | ForEach-Object { Get-OpacidadDelPulso -Estado 'Falla' -Tick $_ })
$pulsoCopia = @(0..9 | ForEach-Object { Get-OpacidadDelPulso -Estado 'Copiando' -Tick $_ })
$pulsoQuieto = @(0..9 | ForEach-Object { Get-OpacidadDelPulso -Estado 'Protegido' -Tick $_ })
$pulsoAviso = @(0..9 | ForEach-Object { Get-OpacidadDelPulso -Estado 'Atencion' -Tick $_ })

Test-Afirmacion -Nombre 'El rojo PARPADEA: cambia dentro del primer segundo' `
    -Esperado $true -Obtenido (@($pulsoFalla[0..4] | Sort-Object -Unique).Count -gt 1) -Criterio '9'
Test-Afirmacion -Nombre 'Y el rojo SALTA entre dos extremos, no tres' `
    -Esperado 2 -Obtenido @($pulsoFalla | Sort-Object -Unique).Count -Criterio '9'

# La diferencia entre parpadear y respirar, comprobada y no supuesta.
Test-Afirmacion -Nombre 'El verde de copiando RESPIRA: pasa por valores intermedios' `
    -Esperado $true -Obtenido (@($pulsoCopia | Sort-Object -Unique).Count -gt 2)
Test-Afirmacion -Nombre 'Y nunca se apaga del todo: el minimo del pulso sigue siendo visible' `
    -Esperado $true -Obtenido ((@($pulsoCopia | Measure-Object -Minimum).Minimum) -ge 100)

# SOLO EL ROJO Y EL VERDE SE MUEVEN. Si el ambar parpadeara, el movimiento
# dejaria de significar "esto esta roto".
Test-Afirmacion -Nombre 'Protegido es FIJO: no se mueve ni un tick' `
    -Esperado 1 -Obtenido @($pulsoQuieto | Sort-Object -Unique).Count
Test-Afirmacion -Nombre 'Atencion es FIJO: el ambar no parpadea (seccion 10.2)' `
    -Esperado 1 -Obtenido @($pulsoAviso | Sort-Object -Unique).Count

# ===========================================================================
# LA MARCA DE UNA COPIA FRIA DURA HORAS, NO MINUTOS
# ===========================================================================
# El plazo del nodo son 90 min. Aplicado a la copia fria, una copia sana de dos
# horas pintaba el icono de ROJO -"arranco y nunca termino"- siendo mentira.
Write-Titulo 'La marca distingue nodo de disco'

$cajaMarca = Join-Path $CarpetaCaja 'marca'
New-Item -ItemType Directory -Path $cajaMarca -Force | Out-Null
$hace2h = (Get-Date).AddHours(-2).ToString('s')

# Con el PID de este proceso, que esta vivo: asi lo unico que se prueba es el
# plazo, no la deteccion de proceso muerto.
Set-Content -LiteralPath "$cajaMarca\EN_CURSO.lock" -Encoding UTF8 `
    -Value "pid=$PID`ninicio=$hace2h`nequipo=$env:COMPUTERNAME`ntipo=disco`n"
$marcaDisco = Get-MarcaDeCorrida -Carpeta $cajaMarca

Set-Content -LiteralPath "$cajaMarca\EN_CURSO.lock" -Encoding UTF8 `
    -Value "pid=$PID`ninicio=$hace2h`nequipo=$env:COMPUTERNAME`ntipo=nodo`n"
$marcaNodo = Get-MarcaDeCorrida -Carpeta $cajaMarca

Test-Afirmacion -Nombre 'Una copia al DISCO de 2 h sigue siendo creible' `
    -Esperado $false -Obtenido $marcaDisco.Vieja
Test-Afirmacion -Nombre 'Y se sabe que es del disco, no del nodo' `
    -Esperado 'disco' -Obtenido $marcaDisco.Tipo
Test-Afirmacion -Nombre 'Una corrida al NODO de 2 h NO es creible: arranco y no termino' `
    -Esperado $true -Obtenido $marcaNodo.Vieja
Test-Afirmacion -Nombre 'Una marca sin tipo se trata como del nodo, que es el plazo corto' `
    -Esperado 'nodo' -Obtenido $marcaNodo.Tipo

# ===========================================================================
# LA VENTANA NEGRA NO VUELVE
# ===========================================================================
# -WindowStyle Hidden NO BASTA en Windows 11: la consola esta delegada en
# Windows Terminal, que abre SU PROPIA ventana. La tarea tiene que pedir
# conhost --headless. Se registra una tarea de usar y tirar, se mira como
# quedo, y se retira.
Write-Titulo 'Las tareas arrancan sin ventana'

$tareaPrueba = 'NasRespaldo-PruebaDeVentana'
$registrador = Join-Path (Split-Path $PSScriptRoot -Parent) '1-Interfaz\Registrar-Tarea.ps1'
try {
    & $registrador -Pieza Indicador -Accion Registrar -NombreTarea $tareaPrueba -Confirm:$false | Out-Null
    $tp = Get-ScheduledTask -TaskName $tareaPrueba -ErrorAction SilentlyContinue
    $accion = if ($tp) { $tp.Actions | Select-Object -First 1 } else { $null }

    Test-Afirmacion -Nombre 'La tarea del icono NO arranca powershell.exe directo' `
        -Esperado $false -Obtenido ($null -ne $accion -and $accion.Execute -like '*powershell.exe')
    Test-Afirmacion -Nombre 'Arranca por conhost, que es el host sin ventana' `
        -Esperado $true -Obtenido ($null -ne $accion -and $accion.Execute -like '*conhost.exe')
    Test-Afirmacion -Nombre 'Y en modo --headless, o conhost abriria ventana igual' `
        -Esperado $true -Obtenido ($null -ne $accion -and $accion.Arguments -like '--headless *')
    Test-Afirmacion -Nombre 'El disparador del icono es AL ENTRAR A LA SESION' `
        -Esperado 'MSFT_TaskLogonTrigger' `
        -Obtenido $(if ($tp) { @($tp.Triggers)[0].CimClass.CimClassName } else { 'sin tarea' })
    Test-Afirmacion -Nombre 'Y sin limite de tiempo: el icono no termina nunca' `
        -Esperado 'PT0S' -Obtenido $(if ($tp) { $tp.Settings.ExecutionTimeLimit } else { 'sin tarea' })
}
finally {
    Unregister-ScheduledTask -TaskName $tareaPrueba -Confirm:$false -ErrorAction SilentlyContinue
}
Test-Afirmacion -Nombre 'La tarea de prueba se retiro: no queda basura en el Programador' `
    -Esperado $null -Obtenido (Get-ScheduledTask -TaskName $tareaPrueba -ErrorAction SilentlyContinue)

# Y LA PRUEBA NO PUEDE MATAR EL ICONO DE VERDAD. Registrar una tarea retira el
# indicador anterior -si no, al actualizar el codigo queda un huerfano vivo con
# el cerrojo tomado y el nuevo se retira solo-, pero eso vale unicamente cuando
# se REEMPLAZA una tarea existente. La primera version de esa guarda mataba el
# icono del escritorio cada vez que alguien corria la suite.
$iconoSigueVivo = @(Get-ScheduledTask -TaskName 'NasRespaldo-Indicador' -ErrorAction SilentlyContinue).Count
Test-Afirmacion -Nombre 'Registrar una tarea NUEVA no toca el indicador ya instalado' `
    -Esperado $iconoSigueVivo `
    -Obtenido @(Get-ScheduledTask -TaskName 'NasRespaldo-Indicador' -ErrorAction SilentlyContinue).Count

# ===========================================================================
# DOS DESTINOS, DOS SALUDES
# ===========================================================================
# El 2026-09-02 la copia al disco escribio estado=Protegido al terminar bien, y
# con eso BORRO un fallo real del nodo que nadie habia arreglado: el icono paso
# de rojo a verde sin que el respaldo diario funcionara.
Write-Titulo 'El disco no pisa el veredicto del nodo'

$cajaEstado = Join-Path $CarpetaCaja 'estado-doble'
New-Item -ItemType Directory -Path $cajaEstado -Force | Out-Null

Write-EstadoRespaldo -Estado 'Falla' -Detalle 'El nodo no respondia' `
    -Datos @{ causa = 'destinoInalcanzable' } -Carpeta $cajaEstado -Confirm:$false
$antesDisco = Read-EstadoRespaldo -Carpeta $cajaEstado

Write-EstadoDelDisco -Estado 'Protegido' -Detalle 'Copia fria al dia' `
    -Carpeta $cajaEstado -Confirm:$false
$trasDisco = Read-EstadoRespaldo -Carpeta $cajaEstado

Test-Afirmacion -Nombre 'El veredicto del NODO sobrevive a una copia fria buena' `
    -Esperado 'Falla' -Obtenido ('' + $trasDisco['estado'])
Test-Afirmacion -Nombre 'Y su detalle no se reescribe' `
    -Esperado ('' + $antesDisco['detalle']) -Obtenido ('' + $trasDisco['detalle'])
Test-Afirmacion -Nombre 'Y el momento sigue siendo el de la corrida al nodo' `
    -Esperado ('' + $antesDisco['momento']) -Obtenido ('' + $trasDisco['momento'])
Test-Afirmacion -Nombre 'La causa del fallo se conserva para que el icono la lea' `
    -Esperado 'destinoInalcanzable' -Obtenido ('' + $trasDisco['causa'])
Test-Afirmacion -Nombre 'El disco anota SU estado en su propia clave' `
    -Esperado 'Protegido' -Obtenido ('' + $trasDisco['disco_estado'])
Test-Afirmacion -Nombre 'Y su propio momento, aparte del momento del nodo' `
    -Esperado $true -Obtenido $trasDisco.ContainsKey('disco_momento')

# Sin veredicto previo NO se hereda el del disco: decir "protegido" porque la
# copia fria fue bien seria la misma mentira al reves.
$cajaVirgen = Join-Path $CarpetaCaja 'estado-virgen'
New-Item -ItemType Directory -Path $cajaVirgen -Force | Out-Null
Write-EstadoDelDisco -Estado 'Protegido' -Detalle 'Copia fria al dia' `
    -Carpeta $cajaVirgen -Confirm:$false
Test-Afirmacion -Nombre 'Sin corrida al nodo previa, el veredicto es SinDatos, no Protegido' `
    -Esperado 'SinDatos' -Obtenido ('' + (Read-EstadoRespaldo -Carpeta $cajaVirgen)['estado'])

# ===========================================================================
# LA LINEA RARA DE /MT NO TUMBA UNA COPIA BUENA
# ===========================================================================
Write-Titulo 'Lineas de robocopy con basura de control'

$nulo = [string][char]0
Test-Afirmacion -Nombre 'Una linea de solo NUL se ignora' `
    -Esperado $true -Obtenido (Test-LineaDeRobocopyVacia -Linea $nulo)
Test-Afirmacion -Nombre 'Espacios con NUL entre medias tambien' `
    -Esperado $true -Obtenido (Test-LineaDeRobocopyVacia -Linea ('   ' + $nulo + '  '))
Test-Afirmacion -Nombre 'Una linea vacia de verdad, igual' `
    -Esperado $true -Obtenido (Test-LineaDeRobocopyVacia -Linea '')

# LO QUE NO SE PUEDE PERDER, y es el motivo de que la limpieza sea tan estrecha.
Test-Afirmacion -Nombre 'Pero un Acceso denegado SIGUE contando aunque lleve basura' `
    -Esperado $false -Obtenido (Test-LineaDeRobocopyVacia -Linea ($nulo + 'ERROR 5 Acceso denegado'))
Test-Afirmacion -Nombre 'Y cualquier linea con texto real cuenta' `
    -Esperado $false -Obtenido (Test-LineaDeRobocopyVacia -Linea '   x   ')

# ===========================================================================
# EL ROJO SE CURA SOLO CUANDO SU CAUSA SE CURO, Y SOLO ENTONCES
# ===========================================================================
# El icono se quedaba en rojo parpadeante despues de que el nodo volviera,
# mientras el tablero decia "Nodo: responde" en la misma pantalla.
Write-Titulo 'Un fallo cuya causa ya no existe'

Test-Afirmacion -Nombre 'Nodo caido: el rojo SIGUE siendo rojo' `
    -Esperado 'Falla' `
    -Obtenido (Resolve-EstadoVigente -Estado 'Falla' -Causa 'destinoInalcanzable' -DestinoResponde $false).Estado
Test-Afirmacion -Nombre 'Nodo de vuelta: el rojo baja a AMBAR, no a verde' `
    -Esperado 'Atencion' `
    -Obtenido (Resolve-EstadoVigente -Estado 'Falla' -Causa 'destinoInalcanzable' -DestinoResponde $true).Estado
Test-Afirmacion -Nombre 'Y NUNCA sube a Protegido solo: eso exige una corrida' `
    -Esperado $false `
    -Obtenido ((Resolve-EstadoVigente -Estado 'Falla' -Causa 'destinoInalcanzable' -DestinoResponde $true).Estado -eq 'Protegido')

# LO QUE NO SE PUEDE CURAR SOLO, y es la mitad importante de esta regla.
Test-Afirmacion -Nombre 'Un centinela alterado NO se cura porque el nodo responda' `
    -Esperado 'Falla' `
    -Obtenido (Resolve-EstadoVigente -Estado 'Falla' -Causa 'centinelaAlterado' -DestinoResponde $true).Estado
# LA CAUSA 'freno' YA NO LA ESCRIBE NADIE (ADR-0085), pero los ESTADO.txt de
# septiembre de 2026 la llevan dentro y hay que seguir leyendolos sin tropezar.
Test-Afirmacion -Nombre 'Una causa historica como freno se devuelve tal cual, sin tropezar' `
    -Esperado 'Atencion' `
    -Obtenido (Resolve-EstadoVigente -Estado 'Atencion' -Causa 'freno' -DestinoResponde $true).Estado
Test-Afirmacion -Nombre 'Un fallo sin causa anotada se respeta tal cual' `
    -Esperado 'Falla' `
    -Obtenido (Resolve-EstadoVigente -Estado 'Falla' -Causa '' -DestinoResponde $true).Estado
Test-Afirmacion -Nombre 'Y un verde no se toca' `
    -Esperado 'Protegido' `
    -Obtenido (Resolve-EstadoVigente -Estado 'Protegido' -Causa '' -DestinoResponde $true).Estado

# ===========================================================================
# LA PALETA DEL CLIENTE ES LA DEL PANEL DEL NODO
# ===========================================================================
# El tablero, el icono de la barra y el panel web son el MISMO producto. Si su
# verde y este verde no son el mismo verde, quien mira aprende dos idiomas de
# colores y el dia que importa lee el equivocado. Esta prueba lee estilo.css y
# comprueba que nadie los ha separado.
Write-Titulo 'La paleta no se separa del panel'

$css = Join-Path (Split-Path (Split-Path $PSScriptRoot -Parent) -Parent) `
    '10_CODIGO\internal\adaptadores\web\estatico\estilo.css'

if (-not (Test-Path -LiteralPath $css -PathType Leaf)) {
    Test-Afirmacion -Nombre 'Se encuentra estilo.css del panel, que es la fuente de la paleta' `
        -Esperado $true -Obtenido $false
}
else {
    $textoCss = Get-Content -LiteralPath $css -Raw -Encoding UTF8
    $estiloPs = Get-Content -LiteralPath (Join-Path (Split-Path $PSScriptRoot -Parent) '1-Interfaz\estilo.ps1') -Raw -Encoding UTF8
    $iconoPs  = Get-Content -LiteralPath (Join-Path (Split-Path $PSScriptRoot -Parent) '1-Interfaz\indicador.ps1') -Raw -Encoding UTF8

    foreach ($token in @('ok', 'av', 'fa', 'nada')) {
        $m = [regex]::Match($textoCss, ('--{0}:\s*#([0-9a-fA-F]{{6}})' -f $token))
        Test-Afirmacion -Nombre ("estilo.css declara --{0}" -f $token) -Esperado $true -Obtenido $m.Success
        if (-not $m.Success) { continue }

        $hex = $m.Groups[1].Value.ToLowerInvariant()
        $r = [Convert]::ToInt32($hex.Substring(0, 2), 16)
        $g = [Convert]::ToInt32($hex.Substring(2, 2), 16)
        $b = [Convert]::ToInt32($hex.Substring(4, 2), 16)

        # El tablero escribe color de 24 bits en decimal.
        Test-Afirmacion -Nombre ("El tablero usa el --{0} del panel ({1})" -f $token, $hex) `
            -Esperado $true -Obtenido ($estiloPs -match ('38;2;{0};{1};{2}' -f $r, $g, $b))

        # El icono lo dibuja en hexadecimal.
        $patronIcono = '0x{0:x2}, 0x{1:x2}, 0x{2:x2}' -f $r, $g, $b
        Test-Afirmacion -Nombre ("El icono usa el --{0} del panel ({1})" -f $token, $hex) `
            -Esperado $true -Obtenido ($iconoPs -match [regex]::Escape($patronIcono))
    }
}

# ===========================================================================
# LA COPIA DEL ESTADO EN EL NODO - seccion 8
# ===========================================================================
# La seccion 8 pide una copia de ESTADO.txt dentro de _SISTEMA en el nodo: en
# una restauracion desde cero el equipo ya no existe y esa copia es lo unico
# que dice cuando fue la ultima corrida buena. No se publicaba, y por eso
# _SISTEMA no llegaba a crearse: quien lo creaba era el emisor de eventos, y
# el evento verde no se emite (seccion 11).
Write-Titulo 'El estado se publica en el nodo'

$cajaPub = Join-Path $CarpetaCaja 'publicar'
$origenPub = Join-Path $cajaPub 'local'
New-Item -ItemType Directory -Path $origenPub -Force | Out-Null
Write-EstadoRespaldo -Estado 'Protegido' -Detalle 'prueba' -Carpeta $origenPub -Confirm:$false

$destinoPub = Join-Path $cajaPub 'nodo\_SISTEMA'
$publicado = Publish-EstadoAlNodo -RutaSistema $destinoPub -Carpeta $origenPub -Confirm:$false

Test-Afirmacion -Nombre 'Publica y lo dice' -Esperado $true -Obtenido $publicado
Test-Afirmacion -Nombre 'Crea _SISTEMA si no existe: por eso la carpeta llega a existir' `
    -Esperado $true -Obtenido (Test-Path -LiteralPath $destinoPub -PathType Container)
Test-Afirmacion -Nombre 'Y el ESTADO.txt del nodo dice lo mismo que el del equipo' `
    -Esperado (Get-Content -LiteralPath (Join-Path $origenPub 'ESTADO.txt') -Raw -Encoding UTF8) `
    -Obtenido (Get-Content -LiteralPath (Join-Path $destinoPub 'ESTADO.txt') -Raw -Encoding UTF8)

# PUBLICAR ES UN EXTRA: si falla, la corrida sigue siendo valida. El respaldo ya
# esta hecho cuando esto ocurre.
$sinEstado = Join-Path $cajaPub 'vacio'
New-Item -ItemType Directory -Path $sinEstado -Force | Out-Null
Test-Afirmacion -Nombre 'Sin ESTADO.txt que publicar devuelve falso y no lanza' `
    -Esperado $false -Obtenido (Publish-EstadoAlNodo -RutaSistema $destinoPub -Carpeta $sinEstado -Confirm:$false)

# ===========================================================================
Write-Titulo 'La tabla por raiz: dos destinos que no se pisan'
# ===========================================================================
# LA TABLA ES LA PIEZA CENTRAL DE LA MAQUETA APROBADA y la unica parte del
# tablero que responde "que exactamente". Si los dos destinos se pisan las
# lineas, la pantalla ensena media verdad con cara de verdad entera.

$cajaTabla = Join-Path $CarpetaCaja 'estado-tabla'
New-Item -ItemType Directory -Path $cajaTabla -Force | Out-Null

$copiasNodo = @(
    [pscustomobject]@{ Origen = 'C:\dev'; Clase = 'B'; NumACopiar = 3; Correcto = $true },
    [pscustomobject]@{ Origen = 'C:\Users\x\Documents'; Clase = 'B'; NumACopiar = 0; Correcto = $true })
Write-EstadoPorRaiz -Destino 'nodo' -Copias $copiasNodo -Carpeta $cajaTabla -Confirm:$false

$copiasDisco = @(
    [pscustomobject]@{ Origen = 'C:\dev'; Clase = 'A'; NumACopiar = 9; Correcto = $true },
    [pscustomobject]@{ Origen = '\\nodo\datos\01_BACKUP\_HISTORICO'; Clase = 'A'; NumACopiar = 0; Correcto = $true })
Write-EstadoPorRaiz -Destino 'disco' -Copias $copiasDisco -Carpeta $cajaTabla `
    -QuitarPrefijo '\\nodo\datos' -Confirm:$false

$leidas = @(Read-EstadoPorRaiz -Carpeta $cajaTabla)

Test-Afirmacion -Nombre 'Escribir el disco NO borra las lineas del nodo' `
    -Esperado 2 -Obtenido @($leidas | Where-Object { $_.Destino -eq 'nodo' }).Count

Test-Afirmacion -Nombre 'Y las del disco quedan escritas' `
    -Esperado 2 -Obtenido @($leidas | Where-Object { $_.Destino -eq 'disco' }).Count

# EL RECORTE DEL PREFIJO ES LO QUE HACE QUE LA TABLA CASE. La copia fria corre
# una pasada desde el nodo, cuyas raices son rutas UNC; sin recortar, la misma
# raiz saldria dos veces con dos nombres y ninguna fila tendria las dos
# columnas.
Test-Afirmacion -Nombre 'La raiz del nodo pierde el prefijo UNC y queda legible' `
    -Esperado '01_BACKUP\_HISTORICO' `
    -Obtenido (@($leidas | Where-Object { $_.Destino -eq 'disco' -and $_.Raiz -like '*_HISTORICO' })[0].Raiz)

Test-Afirmacion -Nombre 'C:\dev sale con el MISMO nombre en los dos destinos: por eso casan' `
    -Esperado 2 -Obtenido @($leidas | Where-Object { $_.Raiz -eq 'C:\dev' }).Count

# Volver a escribir el nodo tiene que dejar el disco intacto: es el caso que
# de verdad ocurre cada noche.
Write-EstadoPorRaiz -Destino 'nodo' -Copias @($copiasNodo[0]) -Carpeta $cajaTabla -Confirm:$false
$otraVez = @(Read-EstadoPorRaiz -Carpeta $cajaTabla)
Test-Afirmacion -Nombre 'Una segunda corrida al nodo no toca las lineas del disco' `
    -Esperado 2 -Obtenido @($otraVez | Where-Object { $_.Destino -eq 'disco' }).Count
Test-Afirmacion -Nombre 'Y reemplaza las suyas en vez de acumularlas' `
    -Esperado 1 -Obtenido @($otraVez | Where-Object { $_.Destino -eq 'nodo' }).Count

$sinNada = Join-Path $CarpetaCaja 'estado-vacio'
New-Item -ItemType Directory -Path $sinNada -Force | Out-Null
Test-Afirmacion -Nombre 'Sin RAICES.tsv devuelve vacio y no lanza: el tablero se pinta igual' `
    -Esperado 0 -Obtenido @(Read-EstadoPorRaiz -Carpeta $sinNada).Count

# ===========================================================================
Write-Titulo 'ESTADO.txt: cada quien pisa solo sus claves'
# ===========================================================================
# HASTA EL 2026-09-02 LA CORRIDA AL NODO BORRABA EL VEREDICTO DEL DISCO. Se vio
# pintando el tablero: decia "ultima copia al disco: nunca" pocos minutos
# despues de una copia fria completa. Write-EstadoDelDisco ya cuidaba el
# camino contrario; faltaba este.

$cajaEstado = Join-Path $CarpetaCaja 'estado-claves'
New-Item -ItemType Directory -Path $cajaEstado -Force | Out-Null

Write-EstadoRespaldo -Estado 'Protegido' -Detalle 'primera corrida' -Carpeta $cajaEstado -Confirm:$false
Write-EstadoDelDisco -Estado 'Protegido' -Detalle 'copia fria completa' -Carpeta $cajaEstado -Confirm:$false
Write-EstadoDeComprobacion -Tipo 'huellas' -Correcto $true -Detalle 'sin diferencias' `
    -Carpeta $cajaEstado -Confirm:$false

# La corrida de la noche siguiente.
Write-EstadoRespaldo -Estado 'Protegido' -Detalle 'segunda corrida' -Carpeta $cajaEstado -Confirm:$false
$tras = Read-EstadoRespaldo -Carpeta $cajaEstado

Test-Afirmacion -Nombre 'Una corrida al nodo CONSERVA el veredicto de la copia fria' `
    -Esperado 'Protegido' -Obtenido ('' + $tras['disco_estado'])
Test-Afirmacion -Nombre 'Y conserva la fecha de la ultima comprobacion de huellas' `
    -Esperado $true -Obtenido ($tras.ContainsKey('huellas_momento') -and $tras['huellas_momento'])
Test-Afirmacion -Nombre 'Pero SI actualiza lo suyo' `
    -Esperado 'segunda corrida' -Obtenido ('' + $tras['detalle'])

# Y al reves, que era lo que ya estaba bien: el disco no toca el veredicto del
# nodo. Se comprueba aqui tambien porque las dos mitades tienen que sostenerse
# a la vez o el archivo acaba contando dos historias.
Write-EstadoRespaldo -Estado 'Falla' -Detalle 'el nodo no respondia' -Carpeta $cajaEstado -Confirm:$false
Write-EstadoDelDisco -Estado 'Protegido' -Detalle 'pero el disco si' -Carpeta $cajaEstado -Confirm:$false
$cruzado = Read-EstadoRespaldo -Carpeta $cajaEstado
Test-Afirmacion -Nombre 'Una copia fria buena NO pone en verde un nodo que fallo' `
    -Esperado 'Falla' -Obtenido ('' + $cruzado['estado'])

# 'semilla' y 'restauracion' NO son lo mismo, y el tipo lo impide por
# construccion: leer la semilla no es haber restaurado nada (criterio 11).
$rechazado = $false
try { Write-EstadoDeComprobacion -Tipo 'restauracion' -Correcto $true -Carpeta $cajaEstado -Confirm:$false }
catch { $rechazado = $true }
Test-Afirmacion -Nombre 'El tipo "restauracion" se rechaza: comprobar la semilla no es haber restaurado' `
    -Esperado $true -Obtenido $rechazado

# EL MUESTREO SEMANAL TIENE FECHA PROPIA (seccion 9). Si el cotejo de cada
# corrida la pisara, el muestreo de la raiz entera no volveria a tocar nunca.
$huellasAntes = '' + (Read-EstadoRespaldo -Carpeta $cajaEstado)['huellas_momento']
Write-EstadoDeComprobacion -Tipo 'muestreo' -Correcto $true -Detalle '80 leidos' -Carpeta $cajaEstado -Confirm:$false
$trasMuestreo = Read-EstadoRespaldo -Carpeta $cajaEstado
Test-Afirmacion -Nombre 'El muestreo semanal anota su fecha sin pisar la de las huellas' `
    -Esperado $true -Obtenido ([bool]$trasMuestreo['muestreo_momento'] -and ('' + $trasMuestreo['huellas_momento']) -eq $huellasAntes)
Write-EstadoDeComprobacion -Tipo 'huellas' -Correcto $true -Detalle 'cotejo de lo recien copiado' -Carpeta $cajaEstado -Confirm:$false
Test-Afirmacion -Nombre 'Y el cotejo de cada corrida no borra la fecha del muestreo' `
    -Esperado ('' + $trasMuestreo['muestreo_momento']) -Obtenido ('' + (Read-EstadoRespaldo -Carpeta $cajaEstado)['muestreo_momento'])

# ===========================================================================
Write-Titulo 'La presentacion: que la ventana no mienta ni se rompa'
# ===========================================================================

# Se carga el tablero entero, no solo estilo.ps1: Format-Espacio y
# Format-EtiquetaDeRaiz viven ahi. Cargado con punto no abre ningun menu.
. "$PSScriptRoot\..\1-Interfaz\tablero.ps1"
$paletaSinColor = Get-Paleta -Capacidades ([pscustomobject]@{ Color = $false; Unicode = $false })

# --- ADR-0097: las dos ventanas beben de la misma tabla ---------------------
# Cada regla de color se decide una vez, en Get-FilasDeTabla; la ventana de
# siempre y la de Spectre solo traducen el tono.
$filasModelo = @(
    [pscustomobject]@{ Destino = 'nodo'; Raiz = 'C:\dev'; Clase = 'B'; Pendientes = 3
        Estado = 'AlDia'; Momento = (Get-Date).AddHours(-1).ToString('s') },
    [pscustomobject]@{ Destino = 'disco'; Raiz = 'C:\dev'; Clase = 'A'; Pendientes = 0
        Estado = 'AlDia'; Momento = (Get-Date).AddDays(-20).ToString('s') },
    [pscustomobject]@{ Destino = 'nodo'; Raiz = 'D:\roto'; Clase = 'A'; Pendientes = 0
        Estado = 'Fallo'; Momento = (Get-Date).ToString('s') },
    [pscustomobject]@{ Destino = 'disco'; Raiz = 'homeUsers\ma'; Clase = 'A'; Pendientes = 0
        Estado = 'AlDia'; Momento = (Get-Date).ToString('s') })
$modelo = @(Get-FilasDeTabla -Filas $filasModelo -HorasParaAmbar 12)
Test-Afirmacion -Nombre 'La tabla decidida da los tonos de siempre: al dia verde, viejo ambar, FALLO rojo, lo que no consta gris' `
    -Esperado 'Verde,Ambar|Rojo,Gris|Gris,Verde' `
    -Obtenido (($modelo | ForEach-Object { '{0},{1}' -f $_.Nodo.Tono, $_.Disco.Tono }) -join '|')
Test-Afirmacion -Nombre 'Y el texto de cada celda: la clase de los dos destinos, y "es el origen" para lo que vive en el nodo' `
    -Esperado 'B/A|es el origen' -Obtenido ('{0}|{1}' -f $modelo[0].Clase, $modelo[2].Nodo.Texto)
Test-Afirmacion -Nombre 'Los colores del panel con Spectre salen de Get-Paleta: el verde es el del nodo' `
    -Esperado '#3fb950' -Obtenido (Get-ColoresDelPanel)['Verde']
if ($PSVersionTable.PSVersion -lt [version]'7.4') {
    Test-Afirmacion -Nombre 'En PowerShell 5.1 no hay panel con Spectre: se pinta la ventana de siempre' `
        -Esperado $false -Obtenido (Initialize-PanelRico)
}

# EL PANEL CON SPECTRE SOLO SE PUEDE EJERCITAR EN UN pwsh CON EL MODULO. Se pinta
# en un proceso aparte con datos de mentira; si no hay con que, se dice en vez
# de contarlo como una prueba pasada.
$pwshExe = Get-Command pwsh.exe -ErrorAction SilentlyContinue
if (-not $pwshExe) {
    Write-Information '   (no hay pwsh en este equipo: el panel con Spectre no se ejercita)' -InformationAction Continue
}
else {
    $datosPanel = [pscustomobject]@{
        Equipo = 'PC-PRUEBA'; Estado = 'Atencion'; Detalle = 'un detalle con [corchetes] que no son marcado'
        Nodo   = [pscustomobject]@{ Nombre = 'NODO'; Situacion = 'responde'; TonoSituacion = 'Verde'
            Libres = '10 GB libres'; UltimaCopia = 'hoy 06:36'; TonoCopia = 'Valor' }
        Disco  = [pscustomobject]@{ Nombre = 'DISCO FRIO'; Situacion = 'no conectado'; TonoSituacion = 'Gris'
            Libres = ''; UltimaCopia = 'nunca'; TonoCopia = 'Valor' }
        Filas = $filasModelo; HorasVerde = 12
        Seguridad = @('centinelas 8/8'); Comprobado = @('huellas hoy 06:44')
        Automatismo = @('motor 3 ventanas Ready'); Pendiente = @('conecta el disco frio')
    }
    $xmlPanel = Join-Path $CarpetaCaja 'datos-panel.xml'
    $datosPanel | Export-Clixml -LiteralPath $xmlPanel -Depth 5
    $tableroRuta = Join-Path (Split-Path $PSScriptRoot -Parent) '1-Interfaz\tablero.ps1'
    $salidaPanel = @(& $pwshExe.Source -NoProfile -NonInteractive -Command (
            ". '{0}'; if (-not `$script:PanelRico) {{ 'SIN-PANEL-RICO'; exit 0 }}; Show-VentanaRica -Datos (Import-Clixml -LiteralPath '{1}')" -f
            $tableroRuta, $xmlPanel))
    $codigoPanel = $LASTEXITCODE
    $textoPanel = $salidaPanel -join "`n"
    if ($textoPanel -match 'SIN-PANEL-RICO') {
        Write-Information '   (pwsh sin PwshSpectreConsole: el panel con Spectre no se ejercita)' -InformationAction Continue
    }
    else {
        Test-Afirmacion -Nombre 'El panel con Spectre se pinta en pwsh sin excepcion (ADR-0097)' `
            -Esperado 0 -Obtenido $codigoPanel
        Test-Afirmacion -Nombre 'Y ensena la tabla entera: cada raiz y el FALLO en su celda' `
            -Esperado $true -Obtenido ($textoPanel -match 'C:\\dev' -and $textoPanel -match 'homeUsers' -and $textoPanel -match 'FALLO')
        Test-Afirmacion -Nombre 'Los corchetes salen tal cual: el texto se escapa antes del marcado' `
            -Esperado $true -Obtenido ($textoPanel -match '\[corchetes\]' -and $textoPanel -match '\[1\] al nodo')
    }
}

Test-Afirmacion -Nombre 'Una celda mas larga que su columna no empuja a la de al lado' `
    -Esperado 13 -Obtenido (Format-Celda -Texto ('x' * 40) -Ancho 13 -Paleta $paletaSinColor).Length
Test-Afirmacion -Nombre 'Y una mas corta se rellena hasta el ancho: las columnas cuadran' `
    -Esperado 13 -Obtenido (Format-Celda -Texto 'ok' -Ancho 13 -Paleta $paletaSinColor).Length

# EL COLOR NO PUEDE CONTAR PARA EL ANCHO. Colorear antes de rellenar mete las
# secuencias de escape en la cuenta y las columnas se descuadran sin que se vea
# por que, porque los codigos son invisibles.
$conColor = Get-Paleta -Capacidades ([pscustomobject]@{ Color = $true; Unicode = $true })
$pintada = Format-Celda -Texto 'ok' -Ancho 13 -Paleta $conColor -Color $conColor.Verde
$visible = $pintada -replace "$([char]27)\[[0-9;]*m", ''
Test-Afirmacion -Nombre 'Con color, lo VISIBLE sigue midiendo lo que la columna' `
    -Esperado 13 -Obtenido $visible.Length

# NO SABER NO ES CERO. Un destino que no responde no tiene "0 GB libres".
Test-Afirmacion -Nombre 'Sin dato de espacio no se inventa un cero' `
    -Esperado '' -Obtenido (Format-Espacio -Bytes $null)

# "hace 296 h" es exacto y no significa nada.
Test-Afirmacion -Nombre 'Una fecha ilegible dice "nunca", no una fecha vieja' `
    -Esperado 'nunca' -Obtenido (Format-Antiguedad -Momento '')
Test-Afirmacion -Nombre 'Lo de hoy se dice "hoy" con su hora' `
    -Esperado $true -Obtenido ((Format-Antiguedad -Momento (Get-Date -Format 's')) -like 'hoy *')
Test-Afirmacion -Nombre 'Lo de hace doce dias se dice en dias, no en horas' `
    -Esperado 'hace 12 dias' -Obtenido (Format-Antiguedad -Momento ((Get-Date).AddDays(-12).ToString('s')))

# Seis raices empiezan igual: recortadas por la derecha eran la misma fila.
$larga = Join-Path ([Environment]::GetFolderPath('UserProfile')) 'Documents\03_ADMINISTRATIVO'
Test-Afirmacion -Nombre 'La raiz del perfil se acorta con ~ y sigue distinguiendose' `
    -Esperado '~\Documents\03_ADMINISTRATIVO' -Obtenido (Format-EtiquetaDeRaiz -Raiz $larga)
Test-Afirmacion -Nombre 'Una raiz fuera del perfil se deja tal cual' `
    -Esperado 'C:\dev' -Obtenido (Format-EtiquetaDeRaiz -Raiz 'C:\dev')

# LA VENTANA SE TUMBO AL ABRIRLA Y NINGUNA PRUEBA LO VIO. El ancho de la
# sangria se calculaba restando la longitud de una cadena QUE YA LLEVABA COLOR
# DENTRO: con color media 23 en vez de 1, la resta daba -11 y Format-Celda
# rechazaba el ancho. Sin color media 1 y todo iba bien.
#
# Por eso no se vio: se comprobo con la salida redirigida, donde no hay color,
# que es exactamente el unico sitio donde el fallo NO puede ocurrir. Estas dos
# pruebas pintan con la paleta ENCENDIDA a proposito.
$script:Regla = Get-ReglaDeAviso -Unicode $true
$conColor = Get-Paleta -Capacidades ([pscustomobject]@{ Color = $true; Unicode = $true })

$tumbo = $null
try {
    $conAviso = @(Write-LineaDeSumario -Etiqueta 'PENDIENTE' -Paleta $conColor `
            -Partes @('algo que avisar') -Color $conColor.Ambar -Avisar 6>&1)
}
catch { $tumbo = $_.Exception.Message }

Test-Afirmacion -Nombre 'Con COLOR encendido, la linea de aviso no tumba la ventana' `
    -Esperado $null -Obtenido $tumbo

# Y la invariante que importa: la regla lateral NO desplaza el contenido. Con
# marca y sin marca, el texto tiene que empezar en la misma columna, o las dos
# lineas de un mismo bloque salen escalonadas.
$sinAviso = @(Write-LineaDeSumario -Etiqueta 'PENDIENTE' -Paleta $conColor `
        -Partes @('algo que avisar') -Color $conColor.Ambar 6>&1)

$verConMarca = ('' + $conAviso[0]) -replace "$([char]27)\[[0-9;]*m", ''
$verSinMarca = ('' + $sinAviso[0]) -replace "$([char]27)\[[0-9;]*m", ''

Test-Afirmacion -Nombre 'La regla lateral no desplaza el contenido ni un caracter' `
    -Esperado $verSinMarca.IndexOf('algo que avisar') `
    -Obtenido $verConMarca.IndexOf('algo que avisar')

# ===========================================================================
# ---------------------------------------------------------------------------
#  H9 - EL COTEJO MIRA LA CLASE, COMO YA HACIA EL FRENO
#
#  Mismo defecto de familia que se parcheo en el freno el 04/09: un sobrante
#  del destino solo es una diferencia si la clase de esa raiz lo fuera a
#  borrar. En clase A conservarlo es el acierto, no el fallo.
# ---------------------------------------------------------------------------
Write-Titulo 'H9: un sobrante de clase A no es una diferencia'

# VERIFICAR.PS1 CORRE AL CARGARSE -- tiene param() y cuerpo de nivel superior --
# asi que un dot-source aqui lanzaria una verificacion real contra el nodo. Se
# extrae del AST SOLO la funcion que se quiere probar. Es la unica forma de
# probar una funcion que vive en un guion ejecutable sin ejecutarlo, y deja la
# prueba atada al archivo de verdad en vez de a una copia que se desincroniza.
$astVerificar = [System.Management.Automation.Language.Parser]::ParseFile(
    (Join-Path $nucleo 'verificar.ps1'), [ref]$null, [ref]$null)
$fnCoincidencia = $astVerificar.FindAll({
    $args[0] -is [System.Management.Automation.Language.FunctionDefinitionAst] -and
    $args[0].Name -eq 'Test-CoincidenciaDeRaiz' }, $true)
Test-Afirmacion -Nombre 'Test-CoincidenciaDeRaiz sigue existiendo en verificar.ps1' `
    -Esperado 1 -Obtenido @($fnCoincidencia).Count
. ([scriptblock]::Create(@($fnCoincidencia)[0].Extent.Text))

$cajaH9 = Join-Path $CarpetaCaja 'cotejo-h9'
$origenH9  = Join-Path $cajaH9 'origen'
$destinoH9 = Join-Path $cajaH9 'destino'
New-Item -ItemType Directory -Path $origenH9, $destinoH9 -Force | Out-Null
'uno'  | Set-Content -LiteralPath (Join-Path $origenH9  'a.txt') -Encoding UTF8
'uno'  | Set-Content -LiteralPath (Join-Path $destinoH9 'a.txt') -Encoding UTF8
# El sobrante: esta en el destino y ya no en el origen.
'viejo' | Set-Content -LiteralPath (Join-Path $destinoH9 'borrado_en_local.txt') -Encoding UTF8

$h9a = Test-CoincidenciaDeRaiz -Raiz ([pscustomobject]@{ Ruta = $origenH9; Clase = 'A' }) -Destino $destinoH9
Test-Afirmacion -Nombre 'Clase A: el sobrante se SIGUE viendo en la telemetria' `
    -Esperado 1 -Obtenido $h9a.Sobran
Test-Afirmacion -Nombre 'Clase A: pero no cuenta como diferencia, porque /E /XO no borra' `
    -Esperado 0 -Obtenido $h9a.SobranQueImportan
Test-Afirmacion -Nombre 'Clase A: y por tanto COINCIDE -- conservarlo es el acierto' `
    -Esperado $true -Obtenido $h9a.Coincide

# LA OTRA MITAD, y sin ella el parche desarmaria el cotejo justo donde importa:
# en clase B ese mismo sobrante SI se va a borrar, asi que sigue siendo una
# diferencia que hay que ver antes de que el espejo se la lleve.
$h9b = Test-CoincidenciaDeRaiz -Raiz ([pscustomobject]@{ Ruta = $origenH9; Clase = 'B' }) -Destino $destinoH9
Test-Afirmacion -Nombre 'Clase B: el mismo sobrante SI cuenta como diferencia' `
    -Esperado 1 -Obtenido $h9b.SobranQueImportan
Test-Afirmacion -Nombre 'Clase B: y por tanto NO coincide' `
    -Esperado $false -Obtenido $h9b.Coincide

# UN FALTANTE ES UNA DIFERENCIA EN LAS DOS CLASES. Es lo que impide que este
# parche se lea como "clase A siempre coincide": si al destino le FALTA algo,
# no coincide, y da igual la clase.
'nuevo' | Set-Content -LiteralPath (Join-Path $origenH9 'b.txt') -Encoding UTF8
$h9c = Test-CoincidenciaDeRaiz -Raiz ([pscustomobject]@{ Ruta = $origenH9; Clase = 'A' }) -Destino $destinoH9
Test-Afirmacion -Nombre 'Clase A: un FALTANTE si es diferencia -- el parche no ciega el cotejo' `
    -Esperado $false -Obtenido $h9c.Coincide
Test-Afirmacion -Nombre 'Y se dice cuantos faltan, que es lo accionable' `
    -Esperado 1 -Obtenido $h9c.Faltan

# ---------------------------------------------------------------------------
#  H10: EL COTEJO DE CADA CORRIDA LEE LO RECIEN COPIADO (seccion 9)
#
#  Releer diez archivos al azar de cada raiz en cada corrida costaba 8 min de
#  lectura por la red para confirmar lo ya confirmado (medido el 2026-09-23).
#  La raiz entera queda para el muestreo semanal.
# ---------------------------------------------------------------------------
Write-Titulo 'H10: el cotejo de cada corrida lee lo recien copiado'

foreach ($nombreFn in @('Test-HuellaPorMuestreo', 'Get-AlcanceDelMuestreo')) {
    $fnH10 = $astVerificar.FindAll({
        $args[0] -is [System.Management.Automation.Language.FunctionDefinitionAst] -and
        $args[0].Name -eq $nombreFn }, $true)
    Test-Afirmacion -Nombre "$nombreFn existe en verificar.ps1" -Esperado 1 -Obtenido @($fnH10).Count
    . ([scriptblock]::Create(@($fnH10)[0].Extent.Text))
}

$cajaH10    = Join-Path $CarpetaCaja 'cotejo-h10'
$origenH10  = Join-Path $cajaH10 'origen'
$destinoH10 = Join-Path $cajaH10 'destino'
$vecinaH10  = Join-Path $cajaH10 'origen2'
New-Item -ItemType Directory -Path $origenH10, $destinoH10, $vecinaH10 -Force | Out-Null
1..15 | ForEach-Object { "archivo $_" | Set-Content -LiteralPath (Join-Path $origenH10 "a$_.txt") -Encoding UTF8 }
# Copy-Item conserva la fecha de escritura, igual que robocopy.
Get-ChildItem -LiteralPath $origenH10 -File | Copy-Item -Destination $destinoH10
# Una raiz VECINA cuyo nombre empieza igual. Si el filtro no exigiera la barra,
# su archivo se colaria como de esta raiz y saldria "NO ESTA EN EL DESTINO".
'de otra raiz' | Set-Content -LiteralPath (Join-Path $vecinaH10 'a1.txt') -Encoding UTF8
$raizH10 = [pscustomobject]@{ Ruta = $origenH10; Clase = 'A' }

$h10a = Test-HuellaPorMuestreo -Raiz $raizH10 -Destino $destinoH10 -Cuantos 10 `
    -Candidatos @((Join-Path $origenH10 'a1.txt'), (Join-Path $origenH10 'a2.txt'))
Test-Afirmacion -Nombre 'Con lo recien copiado lee SOLO eso: 2 de 15, no 10 al azar' `
    -Esperado 2 -Obtenido $h10a.Comprobados
Test-Afirmacion -Nombre 'Y dice de donde salio la muestra' -Esperado 'recientes' -Obtenido $h10a.Alcance

$h10b = Test-HuellaPorMuestreo -Raiz $raizH10 -Destino $destinoH10 -Cuantos 10 `
    -Candidatos @((Join-Path $vecinaH10 'a1.txt'), ((Join-Path $origenH10 'a3.txt') + '   '))
Test-Afirmacion -Nombre 'Lo de una raiz vecina no se cuela, y los espacios de robocopy no estorban' `
    -Esperado '1|0' -Obtenido ('{0}|{1}' -f $h10b.Comprobados, $h10b.Ausentes)

$h10c = Test-HuellaPorMuestreo -Raiz $raizH10 -Destino $destinoH10 -Cuantos 10 -Candidatos @()
Test-Afirmacion -Nombre 'Si la raiz no copio nada, lee UN archivo pequeno: el testigo exige haber leido algo' `
    -Esperado '1|pequeno' -Obtenido ('{0}|{1}' -f $h10c.Comprobados, $h10c.Alcance)

$grandeH10 = Join-Path $cajaH10 'solo-grande'
New-Item -ItemType Directory -Path (Join-Path $grandeH10 'o'), (Join-Path $grandeH10 'd') -Force | Out-Null
('x' * 2048) | Set-Content -LiteralPath (Join-Path $grandeH10 'o\g.bin') -Encoding ASCII
Copy-Item -LiteralPath (Join-Path $grandeH10 'o\g.bin') -Destination (Join-Path $grandeH10 'd')
$h10g = Test-HuellaPorMuestreo -Raiz ([pscustomobject]@{ Ruta = (Join-Path $grandeH10 'o'); Clase = 'A' }) `
    -Destino (Join-Path $grandeH10 'd') -Cuantos 10 -Candidatos @() -TopePequeno 1024
Test-Afirmacion -Nombre 'Y ese archivo es pequeno de verdad: por encima del tope no se lee' `
    -Esperado 0 -Obtenido $h10g.Comprobados

# EDITADO DESPUES DE COPIARSE: esa version no la llevo ninguna corrida.
'editado despues de copiar' | Set-Content -LiteralPath (Join-Path $origenH10 'a1.txt') -Encoding UTF8
(Get-Item -LiteralPath (Join-Path $origenH10 'a1.txt')).LastWriteTime = (Get-Date).AddMinutes(5)
$h10d = Test-HuellaPorMuestreo -Raiz $raizH10 -Destino $destinoH10 -Cuantos 10 `
    -Candidatos @((Join-Path $origenH10 'a1.txt'), (Join-Path $origenH10 'a2.txt'))
Test-Afirmacion -Nombre 'Un archivo editado DESPUES de copiarse no se compara, y no tumba el cotejo' `
    -Esperado '1|1|True' -Obtenido ('{0}|{1}|{2}' -f $h10d.Posteriores, $h10d.Comprobados, $h10d.Correcto)

# LO CONTRARIO SI SE VE: misma fecha y contenido podrido en el destino.
$fechaA2 = (Get-Item -LiteralPath (Join-Path $origenH10 'a2.txt')).LastWriteTime
'contenido podrido' | Set-Content -LiteralPath (Join-Path $destinoH10 'a2.txt') -Encoding UTF8
(Get-Item -LiteralPath (Join-Path $destinoH10 'a2.txt')).LastWriteTime = $fechaA2
$h10e = Test-HuellaPorMuestreo -Raiz $raizH10 -Destino $destinoH10 -Cuantos 10 `
    -Candidatos @((Join-Path $origenH10 'a2.txt'))
Test-Afirmacion -Nombre 'Un contenido podrido en lo recien copiado SI sale: saltar lo posterior no ciega el nivel 3' `
    -Esperado 1 -Obtenido @($h10e.Diferencias | Where-Object { $_.Motivo -eq 'HUELLA DISTINTA' }).Count

'recien creado' | Set-Content -LiteralPath (Join-Path $origenH10 'nuevo.txt') -Encoding UTF8
$h10f = Test-HuellaPorMuestreo -Raiz $raizH10 -Destino $destinoH10 -Cuantos 10 `
    -Candidatos @((Join-Path $origenH10 'nuevo.txt'))
Test-Afirmacion -Nombre 'Lo que la corrida dijo copiar y NO esta en el destino SI es un fallo' `
    -Esperado '1|False' -Obtenido ('{0}|{1}' -f `
        @($h10f.Diferencias | Where-Object { $_.Motivo -eq 'NO ESTA EN EL DESTINO' }).Count, $h10f.Correcto)

$h10h = Test-HuellaPorMuestreo -Raiz $raizH10 -Destino $destinoH10 -Cuantos 3
Test-Afirmacion -Nombre 'Sin lista, la muestra sale de la raiz entera, como en la opcion [3]' `
    -Esperado 'completo|3' -Obtenido ('{0}|{1}' -f $h10h.Alcance, $h10h.Comprobados)

# EN LA RAIZ ENTERA, LO QUE AUN NO LLEGO ES UN PENDIENTE, NO UNA HUELLA DISTINTA.
# Medido el 2026-09-23: las 3 "diferencias" de un muestreo eran capturas hechas
# despues de la ultima copia. Lo podrido de verdad -a2- sigue saliendo.
$h10i = Test-HuellaPorMuestreo -Raiz $raizH10 -Destino $destinoH10 -Cuantos 50
Test-Afirmacion -Nombre 'En el muestreo, lo que aun no llego se anota pero no es una diferencia; lo podrido si' `
    -Esperado '1|0|1' -Obtenido ('{0}|{1}|{2}' -f $h10i.Ausentes,
        @($h10i.Diferencias | Where-Object { $_.Motivo -eq 'NO ESTA EN EL DESTINO' }).Count,
        @($h10i.Diferencias | Where-Object { $_.Motivo -eq 'HUELLA DISTINTA' }).Count)

$informeRecientes = [pscustomobject]@{ TodoCoincide = $false; Coincidencias = @(); Huellas = @($h10a, $h10c) }
Test-Afirmacion -Nombre 'El cotejo de lo recien copiado basta para el verde del testigo (ADR-0079)' `
    -Esperado $true -Obtenido (Test-CotejoLimpio -Informe $informeRecientes).Limpio

$ahoraH10 = [datetime]'2026-09-23T12:00:00'
Test-Afirmacion -Nombre 'Sin muestreo previo, toca el de la raiz entera' -Esperado 'completo' `
    -Obtenido (Get-AlcanceDelMuestreo -UltimoMuestreo '' -HayRecientes -Ahora $ahoraH10)
Test-Afirmacion -Nombre 'Con el ultimo hace 2 dias, basta lo recien copiado' -Esperado 'recientes' `
    -Obtenido (Get-AlcanceDelMuestreo -UltimoMuestreo '2026-09-21T12:00:00' -HayRecientes -Ahora $ahoraH10)
Test-Afirmacion -Nombre 'Con el ultimo hace 8 dias, toca el semanal' -Esperado 'completo' `
    -Obtenido (Get-AlcanceDelMuestreo -UltimoMuestreo '2026-09-15T12:00:00' -HayRecientes -Ahora $ahoraH10)
Test-Afirmacion -Nombre 'Sin lista de lo recien copiado -la opcion [3]- siempre la raiz entera' -Esperado 'completo' `
    -Obtenido (Get-AlcanceDelMuestreo -UltimoMuestreo '2026-09-22T12:00:00' -Ahora $ahoraH10)
Test-Afirmacion -Nombre 'Una fecha del futuro no aplaza el semanal' -Esperado 'completo' `
    -Obtenido (Get-AlcanceDelMuestreo -UltimoMuestreo '2026-10-30T12:00:00' -HayRecientes -Ahora $ahoraH10)

Test-Afirmacion -Nombre 'Y el motor le pasa al cotejo lo que acaba de copiar' `
    -Esperado $true -Obtenido ($fuenteMotor -match "\`$argsCotejo\['Recientes'\] = ")

# ---------------------------------------------------------------------------
#  EL VEREDICTO SE PARTE, NO SE CORTA A MEDIA FRASE (04/09/2026)
# ---------------------------------------------------------------------------
$fraseLarga = 'FRENO: 1 de 8 raices rebasan el umbral del 5 %. No se copia NADA hasta que se autorice con -AutorizarFreno.'
$partida = @(Split-TextoEnLineas -Texto $fraseLarga -Ancho 66)
Test-Afirmacion -Nombre 'Una frase larga se parte en varias lineas, no se recorta' `
    -Esperado $true -Obtenido ($partida.Count -gt 1)
Test-Afirmacion -Nombre 'Y NINGUNA linea desborda el ancho: el marco aguanta' `
    -Esperado $true -Obtenido (@($partida | Where-Object { $_.Length -gt 66 }).Count -eq 0)
# LA PRUEBA QUE DEFINE LA DIFERENCIA CON Limit-Texto: no se pierde ni una palabra.
Test-Afirmacion -Nombre 'No se pierde texto por el camino: se lee la frase entera' `
    -Esperado ($fraseLarga -replace '\s+', ' ') -Obtenido (($partida -join ' ').Trim())
Test-Afirmacion -Nombre 'Una frase que ya cabe se queda en una sola linea' `
    -Esperado 1 -Obtenido @(Split-TextoEnLineas -Texto 'Corrida terminada.' -Ancho 66).Count
# Una ruta sin espacios mas larga que el ancho: se rompe la ruta, no la ventana.
$rutaLarga = @(Split-TextoEnLineas -Texto ('C:\' + ('x' * 90)) -Ancho 20)
Test-Afirmacion -Nombre 'Una palabra mas larga que el ancho se trocea en vez de desbordar' `
    -Esperado $true -Obtenido (@($rutaLarga | Where-Object { $_.Length -gt 20 }).Count -eq 0)

# ---------------------------------------------------------------------------
#  H6 - EL TABLERO NO PUEDE AFIRMAR UNA COPIA QUE EL REGISTRO NO RESPALDA
#
#  LA PRUEBA QUE NO EXISTIA Y QUE DEJO PASAR EL DEFECTO. El 04/09 el tablero
#  anuncio "ult. copia hoy 20:47" de una corrida que aborto con copias=0.
#  Nada lo vigilaba, y por eso llego a produccion.
# ---------------------------------------------------------------------------
Write-Titulo 'H6: una copia que no ocurrio no se anuncia'

$cajaH6 = Join-Path $CarpetaCaja 'estado-h6'
New-Item -ItemType Directory -Path $cajaH6 -Force | Out-Null

# 1) Una corrida que ABORTA: escribe momento, pero NO copia_momento.
Write-EstadoRespaldo -Estado 'Falla' -Detalle 'Centinela alterado' `
    -Datos @{ copias = 0; causa = 'centinelaAlterado' } -Carpeta $cajaH6 -Confirm:$false
$h6a = Read-EstadoRespaldo -Carpeta $cajaH6
Test-Afirmacion -Nombre 'Un aborto deja `momento`, que es cuando CORRIO' `
    -Esperado $true -Obtenido ($h6a.ContainsKey('momento'))
Test-Afirmacion -Nombre 'Pero NO deja `copia_momento`: no hubo copia que anunciar' `
    -Esperado $false -Obtenido ($h6a.ContainsKey('copia_momento'))

# 2) Una corrida BUENA: ahora si.
Write-EstadoRespaldo -Estado 'Protegido' -Detalle '3 raices al dia' `
    -Datos @{ copias = 3; copia_momento = (Get-Date -Format 's'); copia_archivos = 7 } `
    -Carpeta $cajaH6 -Confirm:$false
$h6b = Read-EstadoRespaldo -Carpeta $cajaH6
Test-Afirmacion -Nombre 'Una corrida buena SI anota copia_momento' `
    -Esperado $true -Obtenido ($h6b.ContainsKey('copia_momento'))
$copiaBuena = '' + $h6b['copia_momento']

# 3) Y UN ABORTO POSTERIOR NO LA PISA NI LA ADELANTA. Es la mitad importante:
#    la edad de la ultima copia buena tiene que crecer sola mientras no haya
#    otra, en vez de rejuvenecer con cada intento fallido.
Start-Sleep -Milliseconds 1100
Write-EstadoRespaldo -Estado 'Falla' -Detalle 'El nodo no responde' `
    -Datos @{ copias = 0; causa = 'destinoInalcanzable' } -Carpeta $cajaH6 -Confirm:$false
$h6c = Read-EstadoRespaldo -Carpeta $cajaH6
Test-Afirmacion -Nombre 'Un aborto posterior NO adelanta la fecha de la ultima copia buena' `
    -Esperado $copiaBuena -Obtenido ('' + $h6c['copia_momento'])
Test-Afirmacion -Nombre 'Y `momento` SI avanza: el intento consta aunque no copiara' `
    -Esperado $true -Obtenido (('' + $h6c['momento']) -gt $copiaBuena)

Write-Titulo 'La cadencia: tres ventanas, un sorteo y un registro auditable'
# ===========================================================================

# Las mismas tres ventanas que produccion. Se arma a mano y no leyendo el
# .jsonc: lo que se prueba es la REGLA, no si el archivo esta bien escrito.
function Get-CadenciaCruda {
    [CmdletBinding()]
    [OutputType([psobject])]
    param(
        [Parameter(Mandatory)][psobject[]] $Ventanas,
        [int] $Avisar = 22
    )
    return [pscustomobject]@{
        cadencia = [pscustomobject]@{ ventanas = $Ventanas; horasParaAvisar = $Avisar }
    }
}
$v04 = [pscustomobject]@{ inicio = '04:00'; duracionHoras = 3 }
$v12 = [pscustomobject]@{ inicio = '12:00'; duracionHoras = 3 }
$v19 = [pscustomobject]@{ inicio = '19:00'; duracionHoras = 3 }

$cad = Get-CadenciaDeCorrida -Configuracion (Get-CadenciaCruda -Ventanas @($v04, $v12, $v19))
Test-Afirmacion -Nombre 'Tres ventanas dan tres corridas al dia' `
    -Esperado 3 -Obtenido $cad.CorridasPorDia

# EL NUMERO DEL QUE CUELGA TODO LO DEMAS. Del principio de la ventana de las
# 19:00 al final de la de las 04:00 del dia siguiente van 12 h, y ese es el
# hueco que el reparto promete: el ambar de la tabla y lo que espera el testigo
# salen de aqui. Si esta prueba cambia de valor, esos dos tienen que cambiar.
Test-Afirmacion -Nombre 'El hueco recibol maximo se CALCULA, y son 12 h' `
    -Esperado 12 -Obtenido $cad.HuecoNominalMaximoHoras
Test-Afirmacion -Nombre 'El umbral de aviso sale de la configuracion, no de un valor fijo' `
    -Esperado 22 -Obtenido $cad.HorasParaAvisar

# El orden del archivo no manda: el indice que sale en el registro es el orden
# DEL DIA. Escritas al reves tienen que numerarse igual.
$alReves = Get-CadenciaDeCorrida -Configuracion (Get-CadenciaCruda -Ventanas @($v19, $v04, $v12))
Test-Afirmacion -Nombre 'Las ventanas se numeran por hora del dia, no por orden en el archivo' `
    -Esperado '04:00|12:00|19:00' -Obtenido (($alReves.Ventanas | Sort-Object Indice | ForEach-Object { $_.Inicio }) -join '|')

# DOS VENTANAS SOLAPADAS SE PIERDEN EN SILENCIO por MultipleInstances=IgnoreNew,
# asi que la configuracion no puede llegar viva hasta el Programador.
$lanzoSolape = $false
try {
    Get-CadenciaDeCorrida -Configuracion (Get-CadenciaCruda -Ventanas @(
            [pscustomobject]@{ inicio = '04:00'; duracionHoras = 5 },
            [pscustomobject]@{ inicio = '08:00'; duracionHoras = 3 })) | Out-Null
}
catch { $lanzoSolape = $true }
Test-Afirmacion -Nombre 'Ventanas solapadas se rechazan: la segunda corrida se perderia sin avisar' `
    -Esperado $true -Obtenido $lanzoSolape

# UN UMBRAL POR DEBAJO DEL HUECO AVISA DE ALGO QUE NO HA PASADO, que es la
# fatiga de alarmas que este contrato cita para el semaforo y para los avisos.
$lanzoUmbral = $false
try {
    Get-CadenciaDeCorrida -Configuracion (Get-CadenciaCruda -Ventanas @($v04, $v12, $v19) -Avisar 8) | Out-Null
}
catch { $lanzoUmbral = $true }
Test-Afirmacion -Nombre 'Un umbral de aviso menor que el hueco se rechaza: avisaria del sistema sano' `
    -Esperado $true -Obtenido $lanzoUmbral

# Un disparador diario no puede expresar una ventana que cruza la medianoche.
$lanzoMedianoche = $false
try {
    Get-CadenciaDeCorrida -Configuracion (Get-CadenciaCruda -Ventanas @(
            [pscustomobject]@{ inicio = '23:00'; duracionHoras = 3 })) | Out-Null
}
catch { $lanzoMedianoche = $true }
Test-Afirmacion -Nombre 'Una ventana que cruza la medianoche se rechaza' `
    -Esperado $true -Obtenido $lanzoMedianoche

# Sin bloque `cadencia` no se arranca, y es deliberado: un valor por omision
# silencioso dejaria al icono, al tablero y al testigo describiendo una cadencia
# distinta de la que corre.
$lanzoSinBloque = $false
try { Get-CadenciaDeCorrida -Configuracion ([pscustomobject]@{ equipo = 'X' }) | Out-Null }
catch { $lanzoSinBloque = $true }
Test-Afirmacion -Nombre 'Sin bloque cadencia, el motor se niega a adivinar' `
    -Esperado $true -Obtenido $lanzoSinBloque

# --- Que el registro distinga "a tiempo" de "recuperada" -------------------
# ES LA CONDICION QUE PUSO EL RESPONSABLE: con horas sorteadas, el registro
# tiene que poder decir a que hora le TOCABA y a que hora corrio de verdad.
$dentro = Get-VentanaDeCorrida -Momento ([datetime]'2026-09-03 05:26') -Cadencia $cad
Test-Afirmacion -Nombre 'Una corrida dentro de su ventana se marca A TIEMPO' `
    -Esperado $true -Obtenido $dentro.ATiempo
Test-Afirmacion -Nombre 'Y se le atribuye la ventana que le tocaba' `
    -Esperado '04:00-07:00' -Obtenido $dentro.Ventana
Test-Afirmacion -Nombre 'El desfase dentro de la ventana se mide en minutos desde su inicio' `
    -Esperado 86 -Obtenido $dentro.Desfase

$justo = Get-VentanaDeCorrida -Momento ([datetime]'2026-09-03 12:00') -Cadencia $cad
Test-Afirmacion -Nombre 'El primer minuto de una ventana ya cuenta como dentro' `
    -Esperado $true -Obtenido $justo.ATiempo
Test-Afirmacion -Nombre 'Y es la segunda ventana del dia' `
    -Esperado 2 -Obtenido $justo.Indice

# UNA CORRIDA RECUPERADA POR StartWhenAvailable NO ES UN ERROR, pero tampoco es
# una corrida a tiempo. Si las dos se vieran igual, el registro no serviria para
# lo unico que se le pide.
$tarde = Get-VentanaDeCorrida -Momento ([datetime]'2026-09-03 10:14') -Cadencia $cad
Test-Afirmacion -Nombre 'Una corrida fuera de ventana se marca TARDE, no a tiempo' `
    -Esperado $false -Obtenido $tarde.ATiempo
Test-Afirmacion -Nombre 'Y se le atribuye la ventana que quedo sin correr' `
    -Esperado '04:00-07:00' -Obtenido $tarde.Ventana
Test-Afirmacion -Nombre 'El retraso se cuenta desde que la ventana se cerro' `
    -Esperado 194 -Obtenido $tarde.Desfase

# LA MADRUGADA ES EL CASO QUE SE ROMPE SOLO. A las 02:00 no ha empezado ninguna
# ventana de hoy: la que quedo colgando es la ULTIMA DE AYER, y atribuirla a la
# primera de hoy diria que llego con siete horas de adelanto.
$madrugada = Get-VentanaDeCorrida -Momento ([datetime]'2026-09-03 02:00') -Cadencia $cad
Test-Afirmacion -Nombre 'Antes de la primera ventana, la que quedo colgando es la ultima de AYER' `
    -Esperado '19:00-22:00' -Obtenido $madrugada.Ventana
Test-Afirmacion -Nombre 'Y el retraso cruza la medianoche sin dar negativo' `
    -Esperado 240 -Obtenido $madrugada.Desfase

# --- El defecto de la tabla: verde por HORAS, no por dia de calendario -----
# HASTA EL 2026-09-02 LA CELDA SE PINTABA VERDE SOLO SI LA COPIA ERA DE HOY, asi
# que con la corrida unica de las 22:30 el tablero habria dicho "ayer 22:30" en
# ambar desde medianoche hasta la noche siguiente: 22 h y media de ambar al dia
# con el sistema sano. Nadie lo vio porque la tarea no llego a correr ni una vez.
#
# Se prueba con un umbral de 48 h y una copia de hace 30: la etiqueta la llama
# "ayer" o "hace 1 dias" -o sea, OTRO DIA DE CALENDARIO- y aun asi tiene que
# salir verde. Con la regla vieja esta prueba falla siempre; con la nueva pasa
# siempre, corra a la hora que corra.
$hace30h = [pscustomobject]@{
    Destino = 'nodo'; Raiz = 'C:\dev'; Clase = 'B'; Pendientes = 0
    Estado = 'AlDia'; Momento = (Get-Date).AddHours(-30).ToString('s')
}
$pintado = @(Write-CuerpoDeTabla -Filas @($hace30h) -Paleta $paletaSinColor -HorasParaAmbar 48 6>&1)
Test-Afirmacion -Nombre 'Una copia de OTRO DIA pero dentro del hueco sale AL DIA, no ambar' `
    -Esperado $true -Obtenido (('' + $pintado) -match 'AL DIA')

$pintadoViejo = @(Write-CuerpoDeTabla -Filas @($hace30h) -Paleta $paletaSinColor -HorasParaAmbar 12 6>&1)
Test-Afirmacion -Nombre 'Y pasado el hueco deja de ser AL DIA y ensena su edad' `
    -Esperado $false -Obtenido (('' + $pintadoViejo) -match 'AL DIA')

# --- La tarea: tantos disparadores como ventanas, y todos con sorteo -------
# UN DISPARADOR QUE DESAPARECE ES UN TERCIO DEL RESPALDO QUE DEJA DE CORRER, y
# ninguna otra pantalla lo diria. Se registra de verdad y se retira.
$nombreEnsayo = 'NasRespaldo-PruebaDeCadencia'
try {
    & $registrar -Pieza Motor -Accion Registrar -RutaConfiguracion $caja.Config `
        -NombreTarea $nombreEnsayo -Confirm:$false -InformationAction SilentlyContinue | Out-Null
    $tEnsayo = Get-ScheduledTask -TaskName $nombreEnsayo -ErrorAction SilentlyContinue
    $disparadores = @($tEnsayo.Triggers)
    Test-Afirmacion -Nombre 'La tarea queda con un disparador por ventana' `
        -Esperado 3 -Obtenido $disparadores.Count
    # SE COMPARA LA DURACION, NO LA CADENA. El Programador NORMALIZA lo que se
    # le da: se registra 'PT180M' y devuelve 'PT3H'. Una prueba que compare el
    # texto pasa hoy y falla el dia que una ventana dure 2.5 h, sin que nada se
    # haya roto. Medido el 2026-09-02, cuando esta misma prueba fallo asi.
    Test-Afirmacion -Nombre 'Y cada uno lleva su sorteo: RandomDelay del largo de la ventana' `
        -Esperado '180|180|180' `
        -Obtenido (($disparadores | ForEach-Object { [int][System.Xml.XmlConvert]::ToTimeSpan($_.RandomDelay).TotalMinutes }) -join '|')
    Test-Afirmacion -Nombre 'Las bases son las tres horas de la configuracion' `
        -Esperado '04:00|12:00|19:00' `
        -Obtenido ((($disparadores | ForEach-Object { ([datetime]$_.StartBoundary).ToString('HH:mm') }) | Sort-Object) -join '|')

    # LA MEDICION QUE CORRIGIO EL DISENO, CLAVADA EN UNA PRUEBA.
    # El 2026-09-02 se dio por hecho que `NextRunTime` devolvia el minuto YA
    # SORTEADO y se llego a escribir en tres sitios. Es falso: el Programador
    # sortea el retraso EN CADA CONSULTA y solo lo fija al disparar. Diez
    # lecturas seguidas dieron diez horas distintas.
    #
    # Esta prueba existe para que nadie vuelva a deducir lo contrario y monte
    # encima un "proxima corrida: 20:27" que se lee como promesa y cambia solo.
    # Si algun dia Windows dejara de re-sortear, ESTA PRUEBA FALLA y obliga a
    # mirar de nuevo -que es justo lo que se quiere-.
    $lecturas = @(1..8 | ForEach-Object {
            (Get-ScheduledTask -TaskName $nombreEnsayo | Get-ScheduledTaskInfo).NextRunTime
        })
    $distintas = @($lecturas | Select-Object -Unique).Count
    Test-Afirmacion -Nombre 'NextRunTime RE-SORTEA en cada lectura: no es un minuto comprometido' `
        -Esperado $true -Obtenido ($distintas -gt 1)

    # Lo que si es cierto y es de lo que cuelga el diseno: por muy re-sorteadas
    # que esten, TODAS caen dentro de las ventanas declaradas.
    $fuera = @($lecturas | Where-Object {
            $h = ([datetime]$_).Hour
            -not (($h -ge 4 -and $h -lt 7) -or ($h -ge 12 -and $h -lt 15) -or ($h -ge 19 -and $h -lt 22))
        })
    Test-Afirmacion -Nombre 'Pero todas las muestras caen dentro de alguna ventana declarada' `
        -Esperado 0 -Obtenido $fuera.Count
}
finally {
    Unregister-ScheduledTask -TaskName $nombreEnsayo -Confirm:$false -ErrorAction SilentlyContinue
}
Test-Afirmacion -Nombre 'El ensayo de cadencia no deja tarea detras' `
    -Esperado $false -Obtenido ($null -ne (Get-ScheduledTask -TaskName $nombreEnsayo -ErrorAction SilentlyContinue))

# --- Una corrida A MANO no pertenece a ninguna ventana ----------------------
# LO ENCONTRO EL RESPONSABLE MIRANDO LA PANTALLA, no una prueba. Pulso [2]
# simular a las 17:30 y el registro la acuso de "TARDE, 2 h 30 min despues de
# cerrarse la ventana": el sistema afirmando algo falso sobre si mismo, en un
# proyecto cuyo unico trabajo es no mentir. Una corrida que lanza una persona no
# iba a la cita de las 12:00-15:00, asi que no puede llegar tarde a ella.
#
# La tarea pasa -DesdeTarea; el tablero y la consola no. Se comprueba en los dos
# sitios: que el ARGUMENTO viaje en la tarea, y que sin el no se acuse a nadie.
$argsTarea = ''
try {
    & $registrar -Pieza Motor -Accion Registrar -RutaConfiguracion $caja.Config `
        -NombreTarea $nombreEnsayo -Confirm:$false -InformationAction SilentlyContinue | Out-Null
    $argsTarea = '' + (@((Get-ScheduledTask -TaskName $nombreEnsayo).Actions)[0].Arguments)
}
finally {
    Unregister-ScheduledTask -TaskName $nombreEnsayo -Confirm:$false -ErrorAction SilentlyContinue
}
Test-Afirmacion -Nombre 'La tarea programada pasa -DesdeTarea: sus corridas SI cuentan contra la ventana' `
    -Esperado $true -Obtenido ($argsTarea -like '*-DesdeTarea*')

# ===========================================================================
# LA LINEA DE MANDO REGISTRADA TIENE QUE ARRANCAR EL MOTOR DE VERDAD
# ===========================================================================
# ESTA ES LA PRUEBA QUE FALTABA, Y COSTO UN DIA ENTERO SIN RESPALDO.
#
# El 2026-09-02 se anadio `-Confirm:$false` al final de la linea de mando de la
# tarea. Con `-File`, PowerShell 5.1 pasa lo que va detras COMO TEXTO LITERAL:
# llega la cadena "$false", enlazarla al [switch] -Confirm falla, y el fallo
# ocurre ANTES de la primera linea del guion. El 2026-09-03 las tres ventanas
# -06:26, 13:33 y 19:22- dispararon, arrancaron el motor y lo mataron en menos
# de un segundo: cero copias en todo el dia.
#
# NINGUNA PRUEBA LO VIO PORQUE NINGUNA EJECUTABA ESA LINEA. Se comprobaba su
# FORMA -que llevara --headless, que llevara -DesdeTarea- y el motor se invocaba
# siempre en proceso con el operador `&`, que es justo el camino donde
# `-Confirm:$false` SI funciona. Comprobar la forma de una linea de mando no
# dice nada sobre si arranca.
#
# ASI QUE AQUI SE EJECUTA LA LINEA REGISTRADA, TAL CUAL, y se exige rastro. Con
# tres cuidados que no son opcionales:
#
#   1. -SoloSimular. Sin el, esta prueba emitiria /start y /fail AL CHECK REAL
#      de Healthchecks: `Send-LatidoDelCliente` lee la URL del Administrador de
#      credenciales, y el `testigo` de la configuracion del arenero no lo evita.
#      Ya paso una vez -esta anotado en el propio testigo.ps1- y no se repite.
#      En simulacion el latido no se emite: los dos envios cuelgan de -not
#      $Simular y del retorno temprano.
#
#   2. LOCALAPPDATA redirigido al arenero. El registro del motor cuelga de esa
#      variable, y el proceso hijo la hereda. Sin esto, la prueba escribiria en
#      el registro de PRODUCCION una linea "ventana 3/3 ... A TIEMPO"
#      indistinguible de una corrida programada de verdad.
#
#   3. Se ejecuta por conhost, como la tarea. Su codigo de salida NO se mira -lo
#      devuelve conhost y siempre es 0-; se mira lo que el motor dejo escrito.
Write-Titulo 'La linea de mando de la tarea arranca el motor'

Test-Afirmacion -Nombre 'La linea registrada NO lleva -Confirm: con -File no se puede enlazar' `
    -Esperado $false -Obtenido ($argsTarea -like '*-Confirm*')
Test-Afirmacion -Nombre 'Y lleva la configuracion, para no correr contra una distinta de la que le dio el horario' `
    -Esperado $true -Obtenido ($argsTarea -like '*-RutaConfiguracion*')

$appDataDeLaCaja = Join-Path $caja.Raiz 'appdata-tarea'
if (Test-Path -LiteralPath $appDataDeLaCaja) { Remove-Item -LiteralPath $appDataDeLaCaja -Recurse -Force }
New-Item -ItemType Directory -Path $appDataDeLaCaja -Force | Out-Null

$appDataDeVerdad = $env:LOCALAPPDATA
$registroDeLaCaja = ''
try {
    $env:LOCALAPPDATA = $appDataDeLaCaja
    Start-Process -FilePath (Join-Path $env:SystemRoot 'System32\conhost.exe') `
        -ArgumentList ($argsTarea + ' -SoloSimular') -Wait | Out-Null
}
finally {
    $env:LOCALAPPDATA = $appDataDeVerdad
}
$logsDeLaCaja = @(Get-ChildItem -LiteralPath (Join-Path $appDataDeLaCaja 'NasRespaldo\registro') `
        -Filter '*.log' -ErrorAction SilentlyContinue)
if ($logsDeLaCaja.Count -gt 0) {
    $registroDeLaCaja = Get-Content -LiteralPath $logsDeLaCaja[0].FullName -Raw -Encoding UTF8
}

Test-Afirmacion -Nombre 'La linea de mando de la tarea ARRANCA el motor: deja registro' `
    -Esperado $true -Obtenido ($registroDeLaCaja.Length -gt 0)
Test-Afirmacion -Nombre 'Y el motor llega a clasificar, o sea que paso el prologo entero' `
    -Esperado $true -Obtenido ($registroDeLaCaja -match 'clasificar')
# Que la linea de la ventana aparezca demuestra dos cosas de golpe: que el
# guion corrio Y que -DesdeTarea se enlazo. Un argumento que no enlaza no
# produce una corrida a medias: no produce ninguna.
Test-Afirmacion -Nombre 'Y -DesdeTarea llega ENLAZADO: la corrida se atribuye a su ventana' `
    -Esperado $true -Obtenido ($registroDeLaCaja -match 'ventana \d+/\d+')
Test-Afirmacion -Nombre 'La prueba no escribio en el registro de produccion' `
    -Esperado $true -Obtenido ($env:LOCALAPPDATA -eq $appDataDeVerdad)

# Y la contraria, que es la que fallaba: una corrida a mano deja "a mano" en
# ESTADO.txt y NINGUNA acusacion de retraso en el registro.
$carpetaCaja = Join-Path $caja.Raiz 'estado-motor'
Write-ConfiguracionDeCaja -Caja $caja -Saldada -Confirm:$false
'algo' | Set-Content -LiteralPath "$($caja.Origen)\proyecto\src\mod1.txt" -Encoding UTF8
Invoke-Motor -Caja $caja | Out-Null
$estadoManual = Read-EstadoRespaldo -Carpeta $carpetaCaja
Test-Afirmacion -Nombre 'Una corrida a mano se anota como "a mano", no como una ventana' `
    -Esperado 'a mano' -Obtenido ('' + $estadoManual['ventana'])
Test-Afirmacion -Nombre 'Y no se le cuelga un veredicto de puntualidad que no le corresponde' `
    -Esperado $false -Obtenido $estadoManual.ContainsKey('ventana_atiempo')

# --- La proxima ventana: lo que SI se puede prometer ------------------------
# Se le pasa el momento a proposito, para que la prueba no dependa de la hora a
# la que corran las pruebas -que fue exactamente el defecto del criterio AL DIA-.
$antes = Get-ProximaVentanaDeCorrida -Cadencia $cad -Momento ([datetime]'2026-09-03 09:30')
Test-Afirmacion -Nombre 'Entre dos ventanas, la proxima es la siguiente que empieza' `
    -Esperado '12:00-15:00' -Obtenido $antes.Ventana
Test-Afirmacion -Nombre 'Y se dice que NO le toca todavia' `
    -Esperado $false -Obtenido $antes.EnVentana

$durante = Get-ProximaVentanaDeCorrida -Cadencia $cad -Momento ([datetime]'2026-09-03 13:10')
Test-Afirmacion -Nombre 'Dentro de una ventana se dice que le toca AHORA' `
    -Esperado $true -Obtenido $durante.EnVentana

# A LAS 23:00 NO QUEDA NINGUNA VENTANA HOY, y sin este caso el tablero se
# quedaria mudo justo en la franja en la que mas se mira.
$noche = Get-ProximaVentanaDeCorrida -Cadencia $cad -Momento ([datetime]'2026-09-03 23:00')
Test-Afirmacion -Nombre 'Pasada la ultima ventana, la proxima es la primera de MANANA' `
    -Esperado '04:00-07:00' -Obtenido $noche.Ventana
Test-Afirmacion -Nombre 'Y su fecha es la del dia siguiente, no la de hoy' `
    -Esperado '2026-09-04' -Obtenido $noche.Inicio.ToString('yyyy-MM-dd')

# ---------------------------------------------------------------------------
#  EL UMBRAL VIAJA EN ESTADO.txt  -  la costura de avisos del nodo
#
#  ESTADO.txt se publica tambien en el nodo, y desde el 2026-09-02 el nodo lo
#  lee para decir si el respaldo de este equipo esta al dia (ADR-0081). El
#  umbral que separa "al dia" de "viejo" vive en respaldo.jsonc, que es un
#  archivo del EQUIPO: el nodo no lo puede leer.
#
#  Si no viajara, el nodo tendria que escribirse su propio 22 y habria DOS
#  criterios para la misma pregunta. Estas pruebas son las que impiden que
#  alguien lo quite sin darse cuenta de lo que se lleva por delante.
# ---------------------------------------------------------------------------
$carpetaCaja = Join-Path $caja.Raiz 'estado-motor'
Write-ConfiguracionDeCaja -Caja $caja -Saldada -Confirm:$false
'algo mas' | Set-Content -LiteralPath "$($caja.Origen)\proyecto\src\mod1.txt" -Encoding UTF8
Invoke-Motor -Caja $caja | Out-Null
$estadoUmbral = Read-EstadoRespaldo -Carpeta $carpetaCaja

Test-Afirmacion -Nombre 'ESTADO.txt publica el umbral de aviso, para que el nodo no se invente el suyo' `
    -Esperado '22' -Obtenido ('' + $estadoUmbral['horas_para_avisar'])
Test-Afirmacion -Nombre 'Y el hueco que lo justifica, porque un umbral suelto no dice nada' `
    -Esperado '12' -Obtenido ('' + $estadoUmbral['hueco_maximo_horas'])
Test-Afirmacion -Nombre 'Y de que equipo habla: el arbol del nodo espeja una ruta por maquina' `
    -Esperado $true -Obtenido (-not [string]::IsNullOrWhiteSpace('' + $estadoUmbral['equipo']))

# EL UMBRAL PUBLICADO ES EL DE LA CONFIGURACION, no una constante escrita al
# lado. Sin esto, cambiar `horasParaAvisar` en el archivo dejaria al nodo
# juzgando con el numero viejo -y las dos pantallas se contradirian sin que
# nada fallara-.
$cadCaja = Get-CadenciaDeCorrida -Configuracion (Get-ConfiguracionRespaldo -Ruta $caja.Config)
Test-Afirmacion -Nombre 'El umbral publicado sale de la cadencia, no de un valor fijo' `
    -Esperado ('' + $cadCaja.HorasParaAvisar) -Obtenido ('' + $estadoUmbral['horas_para_avisar'])

# ---------------------------------------------------------------------------
#  PENDIENTE 27  -  la forma comun, y el antivirus
# ---------------------------------------------------------------------------
Write-Titulo 'El pendiente 27: la forma comun y el motor intacto'

# LA PROPIEDAD SE LLAMA `Frenos`, EN PLURAL, y esta prueba existe porque pedir
# `Freno` fue el defecto que abrio el pendiente 27: Select-Object devolvia una
# columna vacia que se leia como "no hubo freno" -- una afirmacion
# tranquilizadora que nadie habia comprobado.
Write-ConfiguracionDeCaja -Caja $caja -Saldada -Confirm:$false
$sim27 = Invoke-Motor -Caja $caja -Simular
$nombres27 = @($sim27.PSObject.Properties.Name)
Test-Afirmacion -Nombre 'El resultado del motor trae `Cambios`, en plural' `
    -Esperado $true -Obtenido ($nombres27 -contains 'Cambios')
Test-Afirmacion -Nombre 'Y NO trae `Cambio` en singular: pedirla daba una columna vacia que mentia' `
    -Esperado $false -Obtenido ($nombres27 -contains 'Cambio')
Test-Afirmacion -Nombre 'Hay una medida por raiz, que es lo que la opcion [2] tiene que ensenar' `
    -Esperado $true -Obtenido (@($sim27.Cambios).Count -gt 0)

# EL MOTOR INTACTO. Lo pidio el responsable el 2026-09-02 por las alertas de
# el antivirus: un antivirus que pone un guion en cuarentena no avisa a nadie, y la
# tarea seguiria diciendo "Ready" con el respaldo muerto.
$intacto = Test-MotorIntacto -RaizProyecto (Split-Path $PSScriptRoot -Parent) -Tareas @()
Test-Afirmacion -Nombre 'Con el proyecto entero, el motor se declara INTACTO' `
    -Esperado $true -Obtenido $intacto.Intacto
Test-Afirmacion -Nombre 'Y son 14 piezas, no una cuenta al vuelo de la carpeta' `
    -Esperado 14 -Obtenido $intacto.PiezasTotal

# UNA PIEZA QUE FALTA SE TIENE QUE VER. Se comprueba contra una carpeta vacia,
# que es lo que deja un antivirus que se lleva los archivos.
$cajaVacia = Join-Path $caja.Raiz 'motor-sin-piezas'
New-Item -ItemType Directory -Path $cajaVacia -Force | Out-Null
$roto = Test-MotorIntacto -RaizProyecto $cajaVacia -Tareas @()
Test-Afirmacion -Nombre 'Si faltan las piezas, NO se declara intacto' `
    -Esperado $false -Obtenido $roto.Intacto
Test-Afirmacion -Nombre 'Y se nombran las 14 que faltan, no un "algo falla"' `
    -Esperado 14 -Obtenido @($roto.PiezasFallan).Count

# UN ARCHIVO DE CERO BYTES EXISTE IGUAL DE BIEN QUE UNO BUENO Y NO SIRVE PARA
# NADA. Es la misma comprobacion que ya hace la prueba de restauracion.
$cajaVacia2 = Join-Path $caja.Raiz 'motor-truncado'
foreach ($sub in @('1-Interfaz', '2-Nucleo', '3-Config')) {
    New-Item -ItemType Directory -Path (Join-Path $cajaVacia2 $sub) -Force | Out-Null
}
foreach ($p in @('2-Nucleo\respaldo.ps1', '2-Nucleo\comun.ps1', '2-Nucleo\clasificar.ps1',
        '2-Nucleo\verificar.ps1', '2-Nucleo\disco.ps1', '2-Nucleo\semilla.ps1',
        '2-Nucleo\notificar.ps1', '2-Nucleo\testigo.ps1', '1-Interfaz\tablero.ps1',
        '1-Interfaz\indicador.ps1', '1-Interfaz\Registrar-Tarea.ps1', '1-Interfaz\estilo.ps1',
        '1-Interfaz\panel.ps1', '3-Config\respaldo.jsonc')) {
    'contenido' | Set-Content -LiteralPath (Join-Path $cajaVacia2 $p) -Encoding ASCII
}
Set-Content -LiteralPath (Join-Path $cajaVacia2 '2-Nucleo\respaldo.ps1') -Value '' -NoNewline -Encoding ASCII
$truncado = Test-MotorIntacto -RaizProyecto $cajaVacia2 -Tareas @()
Test-Afirmacion -Nombre 'Un guion del motor VACIO cuenta como pieza rota, no como presente' `
    -Esperado 1 -Obtenido @($truncado.PiezasFallan).Count

# ---------------------------------------------------------------------------
#  UNA CORRIDA PROGRAMADA QUE NO DEJA RASTRO
# ---------------------------------------------------------------------------
# EL 2026-09-03 ESTA COMPROBACION NO EXISTIA Y COSTO UN DIA SIN RESPALDO. La
# tarea disparo sus tres ventanas, el motor murio en el enlace de parametros las
# tres veces, y el Programador siguio diciendo LastTaskResult 0 -- porque quien
# devuelve ese codigo es `conhost --headless`, no PowerShell. `Test-MotorIntacto`
# devolvia Intacto: True con el respaldo parado.
#
# Lo que no se puede falsear es el rastro: cualquier corrida que pase del
# prologo escribe ESTADO.txt en su primer segundo.
$cajaRastro = Join-Path $caja.Raiz 'rastro'
New-Item -ItemType Directory -Path $cajaRastro -Force | Out-Null

# El caso REAL del 2026-09-03: la tarea dice que arranco a las 19:22 y lo ultimo
# escrito es de la noche anterior.
$infoMuda = [pscustomobject]@{ LastRunTime = (Get-Date).AddHours(-2); LastTaskResult = 0 }
Write-EstadoRespaldo -Estado 'Protegido' -Detalle 'la corrida de ayer' -Carpeta $cajaRastro -Confirm:$false
# Write-EstadoRespaldo sella el momento AHORA, asi que para reproducir "lo ultimo
# escrito es viejo" hay que envejecerlo a mano.
$rutaRastro = Join-Path $cajaRastro 'ESTADO.txt'
(Get-Content -LiteralPath $rutaRastro -Raw -Encoding UTF8) `
    -replace 'momento=.*', ('momento={0:s}' -f (Get-Date).AddHours(-26)) |
    Set-Content -LiteralPath $rutaRastro -Encoding UTF8 -NoNewline
$sinRastro = Test-RastroDeCorridaProgramada -Info $infoMuda -CarpetaEstado $cajaRastro
Test-Afirmacion -Nombre 'La tarea arranco y ESTADO.txt es anterior: NO dejo rastro' `
    -Esperado $false -Obtenido $sinRastro.Dejo
Test-Afirmacion -Nombre 'Y lo dice con las dos horas, no con un "algo falla"' `
    -Esperado $true -Obtenido ($sinRastro.Detalle -match 'NO DEJO RASTRO')

# La contraria: una corrida que si escribio despues de arrancar.
Write-EstadoRespaldo -Estado 'Protegido' -Detalle 'la corrida de hace un rato' -Carpeta $cajaRastro -Confirm:$false
$conRastro = Test-RastroDeCorridaProgramada -Info $infoMuda -CarpetaEstado $cajaRastro
Test-Afirmacion -Nombre 'Si ESTADO.txt es POSTERIOR al arranque, si dejo rastro' `
    -Esperado $true -Obtenido $conRastro.Dejo

# UNA TAREA QUE NUNCA CORRIO NO ES UNA TAREA ROTA. Windows devuelve 30/11/1999,
# y acusarla seria un ambar el dia que se registra la tarea por primera vez.
$infoNueva = [pscustomobject]@{ LastRunTime = [datetime]'1999-11-30'; LastTaskResult = 267011 }
Test-Afirmacion -Nombre 'Una tarea que nunca ha corrido no se acusa de nada' `
    -Esperado $true -Obtenido (Test-RastroDeCorridaProgramada -Info $infoNueva -CarpetaEstado $cajaRastro).Dejo

# LA GRACIA. Sin ella esta comprobacion acusaria al motor que esta arrancando
# BIEN en ese mismo instante, que es el peor falso positivo posible aqui.
$infoRecien = [pscustomobject]@{ LastRunTime = (Get-Date).AddMinutes(-1); LastTaskResult = 267009 }
$vacioRastro = Join-Path $caja.Raiz 'rastro-vacio'
New-Item -ItemType Directory -Path $vacioRastro -Force | Out-Null
Test-Afirmacion -Nombre 'Una corrida de hace un minuto todavia no se juzga' `
    -Esperado $true -Obtenido (Test-RastroDeCorridaProgramada -Info $infoRecien -CarpetaEstado $vacioRastro).Dejo
Test-Afirmacion -Nombre 'Pero pasada la gracia, sin ESTADO.txt ninguno, es que no corrio' `
    -Esperado $false `
    -Obtenido (Test-RastroDeCorridaProgramada -Info $infoMuda -CarpetaEstado $vacioRastro).Dejo

# EL MURO DE ADVERTENCIAS: al archivo entero, a la consola una linea.
$cajaReg = Join-Path $caja.Raiz 'registro-eco'
Write-RegistroRespaldo -Nivel 'ATENCION' -Etapa 'huerfano' -SoloArchivo `
    -Mensaje 'una carpeta cualquiera' -CarpetaRegistro $cajaReg -WarningVariable ecoSilencioso -WarningAction SilentlyContinue
Test-Afirmacion -Nombre 'Con -SoloArchivo la linea NO se ecoa a la consola' `
    -Esperado 0 -Obtenido @($ecoSilencioso).Count
$archivoReg = Join-Path $cajaReg ('respaldo-{0:yyyy-MM-dd}.log' -f (Get-Date))
Test-Afirmacion -Nombre 'Pero SI queda escrita en el registro del dia, con su nivel' `
    -Esperado $true -Obtenido ((Get-Content -LiteralPath $archivoReg -Raw) -match 'ATENCION\s+huerfano\s+una carpeta cualquiera')

# EL VEREDICTO NO SE SALE DEL ANCHO. Lo destapo mirar el buffer de una consola
# real: la frase de la opcion [2] se cortaba a mitad de una cifra --
# "...Copiaria" / "67 y borraria 3."-- y una cifra partida deja de ser una cifra.
. "$PSScriptRoot\..\1-Interfaz\tablero.ps1"
$largo = 'Nada frenaria. 8 raices miradas; la mas movida es dev con 0.26 % (umbral 5 %, minimo 10 archivos). Copiaria 67 y borraria 3.'
$partido = Format-TextoAjustado -Texto $largo -Ancho 60
Test-Afirmacion -Nombre 'Un veredicto largo se parte en varias lineas' `
    -Esperado $true -Obtenido (@($partido).Count -gt 1)
Test-Afirmacion -Nombre 'Y ninguna linea rebasa el ancho pedido' `
    -Esperado $true -Obtenido (@($partido | Where-Object { $_.Length -gt 60 }).Count -eq 0)
Test-Afirmacion -Nombre 'No se pierde ni se duplica una palabra al partir' `
    -Esperado ($largo -split '\s+').Count -Obtenido (($partido -join ' ') -split '\s+').Count
Test-Afirmacion -Nombre 'Y NUNCA se parte una cifra por la mitad' `
    -Esperado $true -Obtenido (@($partido | Where-Object { $_ -match '0\.26 %' }).Count -eq 1)
Test-Afirmacion -Nombre 'Un texto vacio devuelve una linea vacia, no revienta' `
    -Esperado 1 -Obtenido @(Format-TextoAjustado -Texto '' -Ancho 60).Count

# UNA FRASE QUE CABE EN UNA LINEA TIENE QUE SEGUIR SIENDO UN ARREGLO, y este es
# el hueco por el que se colo un defecto que afectaba a las ONCE opciones.
# PowerShell desenvuelve un arreglo de un solo elemento al salir de la funcion,
# asi que el llamador -- que pide $lineas[0] esperando la primera LINEA -- se
# quedaba con la primera LETRA. En pantalla se leia "ATENCION  E".
#
# LAS CUATRO PRUEBAS DE ARRIBA NO LO CAZARON, Y POR UNA RAZON QUE VALE LA PENA
# DEJAR ESCRITA: todas usan frases largas que se parten en dos o mas lineas, asi
# que el arreglo sobrevivia por accidente. Hizo falta PULSAR la opcion en una
# consola real para verlo.
$corta = Format-TextoAjustado -Texto 'Cabe entera en una linea.' -Ancho 60
Test-Afirmacion -Nombre 'Una frase de una sola linea vuelve como ARREGLO, no como cadena suelta' `
    -Esperado $true -Obtenido ($corta -is [array])
Test-Afirmacion -Nombre 'Y su primer elemento es la LINEA entera, no la primera letra' `
    -Esperado 'Cabe entera en una linea.' -Obtenido $corta[0]
$pantallaCorta = (Write-Veredicto -Nivel 'OK' -Frase 'Todo cuadra: 8 de 8 raices.' 6>&1 | Out-String)
Test-Afirmacion -Nombre 'Y en pantalla un veredicto corto sale ENTERO, no su inicial' `
    -Esperado $true -Obtenido ($pantallaCorta -match 'Todo cuadra: 8 de 8 raices\.')

# ---------------------------------------------------------------------------
#  EL HORARIO SE MUEVE DESDE EL TABLERO  -  decision del responsable, 03/09
#
#  "Me gustaria que las corridas pudieran ser movibles en el tablero y no en el
#  archivo, con opciones sencillas". Eso resuelve al reves la enmienda de 10.1
#  que llevaba desde el 02/09 esperando su visto bueno.
#
#  LO PELIGROSO NO ES CAMBIAR EL HORARIO: es reescribir un archivo cuyos
#  comentarios son la mitad de la documentacion del sistema. Volcarlo con
#  ConvertTo-Json daria un archivo valido y los borraria todos.
# ---------------------------------------------------------------------------

Write-Titulo 'El horario se ajusta desde el tablero, sin destrozar el archivo'

$cfgReal = Join-Path (Split-Path $PSScriptRoot -Parent) '3-Config\respaldo.jsonc'
$cfgCaja = Join-Path $caja.Raiz 'respaldo-copia.jsonc'
Copy-Item -LiteralPath $cfgReal -Destination $cfgCaja -Force

# LA CADENCIA REAL, ANOTADA ANTES DE QUE ESTE BLOQUE ESCRIBA NADA.
#
# Se guarda en vez de escribirse como literal mas abajo, y el motivo es que la
# afirmacion del final no habla del horario: habla de que ESTAS PRUEBAS NO
# TOCARON EL ARCHIVO REAL. Comparar contra un horario escrito a mano probaba
# dos cosas a la vez y solo una era la que interesaba, asi que la prueba
# fallaba en cualquier equipo cuyo horario fuera otro -- es decir, en todos
# menos en el que se escribio. Comparando el archivo consigo mismo, la
# afirmacion dice exactamente lo que dice su nombre y vale en cualquier PC.
$cadenciaRealAntes = (Get-CadenciaDeCorrida -Configuracion (Get-ConfiguracionRespaldo -Ruta $cfgReal)).Resumen

$textoAntes = Get-Content -LiteralPath $cfgCaja -Raw -Encoding UTF8
$comentariosAntes = @($textoAntes -split "`n" | Where-Object { $_ -match '^\s*//' }).Count
$centinelasAntes = @((Get-ConfiguracionRespaldo -Ruta $cfgCaja).centinelas).Count

$horarioNuevo = @(
    [pscustomobject]@{ inicio = '05:00'; duracionHoras = 2 },
    [pscustomobject]@{ inicio = '13:00'; duracionHoras = 2 },
    [pscustomobject]@{ inicio = '20:00'; duracionHoras = 2 }
)
$cambio = Set-HorarioDeRespaldo -Ventanas $horarioNuevo -Ruta $cfgCaja -Confirm:$false
Test-Afirmacion -Nombre 'El horario nuevo se escribe y se vuelve a leer del disco' `
    -Esperado '05:00-07:00, 13:00-15:00, 20:00-22:00' -Obtenido $cambio.Cadencia.Resumen
Test-Afirmacion -Nombre 'Y el hueco maximo se RECALCULA solo: nadie lo teclea' `
    -Esperado 11 -Obtenido $cambio.Cadencia.HuecoNominalMaximoHoras

# LA PRUEBA QUE JUSTIFICA TODA LA MANIOBRA. Si esta falla, el ajuste desde el
# menu se lleva por delante la documentacion del proyecto.
$textoDespues = Get-Content -LiteralPath $cfgCaja -Raw -Encoding UTF8
Test-Afirmacion -Nombre 'LOS COMENTARIOS DEL ARCHIVO SOBREVIVEN al cambio de horario' `
    -Esperado $comentariosAntes -Obtenido @($textoDespues -split "`n" | Where-Object { $_ -match '^\s*//' }).Count
Test-Afirmacion -Nombre 'Y las protecciones de la seccion 10.1 no se tocan' `
    -Esperado $centinelasAntes -Obtenido @((Get-ConfiguracionRespaldo -Ruta $cfgCaja).centinelas).Count

# LOS FINALES DE LINEA SE HEREDAN DEL ARCHIVO. Con [Environment]::NewLine se
# metieron CRLF en un archivo que el repositorio mantiene en LF, y NADA FALLO:
# el contenido quedaba identico, git no ensenaba diferencias -- solo un aviso de
# que iba a normalizarlo -- y el archivo se quedaba con finales MEZCLADOS. Un
# defecto que no rompe nada hoy y ensucia cada diferencia futura.
Test-Afirmacion -Nombre 'Un archivo en LF sigue en LF: no se le cuelan retornos de carro' `
    -Esperado 0 -Obtenido ([regex]::Matches($textoDespues, "`r")).Count

# Y al reves: si el archivo viniera en CRLF, se respeta.
$cfgCrLf = Join-Path $caja.Raiz 'respaldo-crlf.jsonc'
# Los dos -replace van en su propia variable: encadenados dentro de la llamada,
# PowerShell los lee como argumentos sueltos y WriteAllText recibe cuatro.
$soloLf = $textoAntes -replace "`r`n", "`n"
$enCrLf = $soloLf -replace "`n", "`r`n"
[System.IO.File]::WriteAllText($cfgCrLf, $enCrLf, (New-Object System.Text.UTF8Encoding($false)))
Set-HorarioDeRespaldo -Ventanas $horarioNuevo -Ruta $cfgCrLf -Confirm:$false | Out-Null
$textoCrLf = Get-Content -LiteralPath $cfgCrLf -Raw -Encoding UTF8
Test-Afirmacion -Nombre 'Y un archivo en CRLF no se queda a medias: sigue entero en CRLF' `
    -Esperado ([regex]::Matches($textoCrLf, "`n")).Count -Obtenido ([regex]::Matches($textoCrLf, "`r`n")).Count

# LAS TRES GUARDAS. Cada una tiene que parar ANTES de escribir, no despues.
$antesDelIntento = Get-Content -LiteralPath $cfgCaja -Raw -Encoding UTF8

$solapadas = @(
    [pscustomobject]@{ inicio = '04:00'; duracionHoras = 5 },
    [pscustomobject]@{ inicio = '08:00'; duracionHoras = 3 }
)
$lanzoSolape = $false
try { Set-HorarioDeRespaldo -Ventanas $solapadas -Ruta $cfgCaja -Confirm:$false | Out-Null } catch { $lanzoSolape = $true }
Test-Afirmacion -Nombre 'Dos ventanas solapadas se rechazan: la segunda corrida se perderia en silencio' `
    -Esperado $true -Obtenido $lanzoSolape
Test-Afirmacion -Nombre 'Y el archivo NO se toco' `
    -Esperado $true -Obtenido ((Get-Content -LiteralPath $cfgCaja -Raw -Encoding UTF8) -eq $antesDelIntento)

$cruzaMedianoche = @([pscustomobject]@{ inicio = '22:00'; duracionHoras = 4 })
$lanzoMedianoche = $false
try { Set-HorarioDeRespaldo -Ventanas $cruzaMedianoche -Ruta $cfgCaja -Confirm:$false | Out-Null } catch { $lanzoMedianoche = $true }
Test-Afirmacion -Nombre 'Una ventana que cruza la medianoche se rechaza: un disparador diario no la expresa' `
    -Esperado $true -Obtenido $lanzoMedianoche

$lanzoUmbral = $false
try { Set-HorarioDeRespaldo -Ventanas $horarioNuevo -HorasParaAvisar 5 -Ruta $cfgCaja -Confirm:$false | Out-Null } catch { $lanzoUmbral = $true }
Test-Afirmacion -Nombre 'Un umbral por debajo del hueco se rechaza: avisaria del sistema sano' `
    -Esperado $true -Obtenido $lanzoUmbral
Test-Afirmacion -Nombre 'Y tras los tres rechazos el archivo sigue intacto, byte a byte' `
    -Esperado $true -Obtenido ((Get-Content -LiteralPath $cfgCaja -Raw -Encoding UTF8) -eq $antesDelIntento)

# LA VISTA PREVIA TIENE QUE CALCULAR Y NO ESCRIBIR. De ahi sale la pantalla del
# tablero, asi que si escribiera, la vista previa SERIA el cambio.
$vistaPrevia = Set-HorarioDeRespaldo -Ventanas @([pscustomobject]@{ inicio = '06:00'; duracionHoras = 1 }) `
    -HorasParaAvisar 30 -Ruta $cfgCaja -SoloCalcular -Confirm:$false
Test-Afirmacion -Nombre 'La vista previa CALCULA el horario que quedaria' `
    -Esperado '06:00-07:00' -Obtenido $vistaPrevia.Cadencia.Resumen
Test-Afirmacion -Nombre 'Pero NO escribe: es el mismo codigo con la escritura apagada' `
    -Esperado $false -Obtenido $vistaPrevia.Escrito
Test-Afirmacion -Nombre 'Y el archivo lo confirma' `
    -Esperado $true -Obtenido ((Get-Content -LiteralPath $cfgCaja -Raw -Encoding UTF8) -eq $antesDelIntento)

# LA CONFIGURACION DE VERDAD NO SE TOCO EN NINGUN MOMENTO. Estas pruebas
# escriben horarios; hacerlo sobre el archivo real cambiaria cuando corre el
# respaldo del responsable como efecto secundario de correr las pruebas.
Test-Afirmacion -Nombre 'El respaldo.jsonc REAL no lo tocaron las pruebas' `
    -Esperado $cadenciaRealAntes `
    -Obtenido (Get-CadenciaDeCorrida -Configuracion (Get-ConfiguracionRespaldo -Ruta $cfgReal)).Resumen

# ---------------------------------------------------------------------------
#  EL COTEJO -- la opcion [3], y el icono que se quedaba quieto
#
#  Probando el menu a mano el 2026-09-02 salieron tres cosas de la MISMA opcion:
#  no decia contra QUE cotejaba, volcaba una lista cruda sin veredicto, y el
#  icono no se movia durante varios minutos de trabajo real.
#
#  Lo tercero era un defecto de verdad y no una impresion: verificar.ps1 era el
#  UNICO de los tres guiones largos que no ponia la marca de corrida. El pulso
#  suave de la seccion 10.2 llevaba meses escrito y probado, y no habia nadie
#  que lo encendiera.
# ---------------------------------------------------------------------------

$cajaCotejo = Join-Path $caja.Raiz 'cotejo'
New-Item -ItemType Directory -Path $cajaCotejo -Force | Out-Null

[void](Enter-MarcaDeCorrida -Carpeta $cajaCotejo -Tipo 'verificacion' -Confirm:$false)
$marcaVerif = Get-MarcaDeCorrida -Carpeta $cajaCotejo
Test-Afirmacion -Nombre 'La marca del cotejo se distingue de la del nodo y de la del disco' `
    -Esperado 'verificacion' -Obtenido $marcaVerif.Tipo
Test-Afirmacion -Nombre 'Y viva NO cuenta como vieja: el icono la pinta como trabajo en curso' `
    -Esperado $false -Obtenido $marcaVerif.Vieja
Exit-MarcaDeCorrida -Carpeta $cajaCotejo -Confirm:$false
Test-Afirmacion -Nombre 'Al terminar el cotejo, la marca se retira' `
    -Esperado $false -Obtenido (Test-Path -LiteralPath (Join-Path $cajaCotejo 'EN_CURSO.lock'))

# UN COTEJO DE DOS HORAS NO ES CREIBLE. Comparte el plazo CORTO del nodo y no
# las ocho horas holgadas de la copia fria: un cotejo que lleva dos horas es un
# proceso colgado, no un disco lento moviendo decenas de GB.
$hace2hCotejo = (Get-Date).AddHours(-2).ToString('s')
Set-Content -LiteralPath (Join-Path $cajaCotejo 'EN_CURSO.lock') -Encoding UTF8 `
    -Value "pid=$PID`ninicio=$hace2hCotejo`nequipo=$env:COMPUTERNAME`ntipo=verificacion`n"
Test-Afirmacion -Nombre 'Un cotejo de 2 h NO es creible: usa el plazo corto, no el del disco' `
    -Esperado $true -Obtenido (Get-MarcaDeCorrida -Carpeta $cajaCotejo).Vieja
Remove-Item -LiteralPath (Join-Path $cajaCotejo 'EN_CURSO.lock') -Force

# EL ICONO DICE QUE COTEJA, NO QUE COPIA. El estado sigue siendo uno de los
# cinco -el nodo lee esa palabra del ESTADO.txt, y renombrarla obligaria a
# redesplegar el nodo por un matiz-, y lo que cambia es el DETALLE, que es lo
# unico que una persona llega a leer.
[void](Enter-MarcaDeCorrida -Carpeta $cajaCotejo -Tipo 'verificacion' -Confirm:$false)
$vistaCotejo = & $indicador -UnaSolaLectura -CarpetaEstado $cajaCotejo
Test-Afirmacion -Nombre 'Cotejando, el icono reusa el estado que ya existia (el del pulso suave)' `
    -Esperado 'Copiando' -Obtenido $vistaCotejo.Estado
Test-Afirmacion -Nombre 'Pero el detalle dice COTEJANDO y no "copiando": el icono no miente' `
    -Esperado $true -Obtenido ($vistaCotejo.Detalle -like 'Cotejando*')
Exit-MarcaDeCorrida -Carpeta $cajaCotejo -Confirm:$false

# SIN MARCA NO PUEDE REVENTAR, y estuvo a punto de hacerlo. Get-MarcaDeCorrida
# NO devuelve la propiedad Tipo cuando no hay marca, asi que leerla fuera del
# if habria tumbado el icono bajo StrictMode SIEMPRE QUE TODO IBA BIEN -- el
# peor momento imaginable para quedarse sin icono, porque el silencio se
# confunde con normalidad.
$vistaSinMarca = & $indicador -UnaSolaLectura -CarpetaEstado $cajaCotejo
Test-Afirmacion -Nombre 'Sin marca el icono resuelve igual: un Tipo que no existe no lo tumba' `
    -Esperado $true `
    -Obtenido ($null -ne $vistaSinMarca -and -not [string]::IsNullOrWhiteSpace('' + $vistaSinMarca.Estado))

# EL VEREDICTO DEL COTEJO. Write-Linea sale por Write-Information, asi que la
# pantalla se captura con 6>&1: se comprueba lo que SE LEE, no lo que se calcula.
$cotejoLimpio = [pscustomobject]@{
    NodoVivo      = $true
    Coincidencias = @([pscustomobject]@{ Raiz = 'C:\dev'; Clase = 'B'; Faltan = 0; Sobran = 0; Coincide = $true })
    Huellas       = @([pscustomobject]@{ Raiz = 'C:\dev'; Comprobados = 7; Ausentes = 0; Diferencias = @(); Correcto = $true })
}
$pantallaLimpia = (Show-VeredictoDelCotejo -Resultado $cotejoLimpio 6>&1 | Out-String)
Test-Afirmacion -Nombre 'Un cotejo sin diferencias da OK' `
    -Esperado $true -Obtenido ($pantallaLimpia -match 'OK\s')
Test-Afirmacion -Nombre 'Y el verde lo sostiene la cifra de archivos LEIDOS ENTEROS, no de raices' `
    -Esperado $true -Obtenido ($pantallaLimpia -match '7 archivos leidos enteros')

# ESTRUCTURA DISTINTA ES ATENCION: lo que falta se copia solo en la siguiente
# corrida, asi que no es un fallo.
$cotejoDesfasado = [pscustomobject]@{
    NodoVivo      = $true
    Coincidencias = @([pscustomobject]@{ Raiz = 'C:\dev'; Clase = 'B'; Faltan = 12; Sobran = 0; Coincide = $false })
    Huellas       = @([pscustomobject]@{ Raiz = 'C:\dev'; Comprobados = 7; Ausentes = 0; Diferencias = @(); Correcto = $true })
}
$pantallaDesfasada = (Show-VeredictoDelCotejo -Resultado $cotejoDesfasado 6>&1 | Out-String)
Test-Afirmacion -Nombre 'Faltar archivos en el nodo es ATENCION, no FALLO: la proxima corrida los pone' `
    -Esperado $true -Obtenido ($pantallaDesfasada -match 'ATENCION')

# CONTENIDO DISTINTO ES FALLO, Y ES LA DISTINCION QUE JUSTIFICA LA OPCION. Una
# huella que no casa significa que lo guardado NO ES lo que se creia tener, y
# ninguna corrida futura va a notarlo: robocopy compara fecha y tamano.
$cotejoPodrido = [pscustomobject]@{
    NodoVivo      = $true
    Coincidencias = @([pscustomobject]@{ Raiz = 'C:\dev'; Clase = 'B'; Faltan = 0; Sobran = 0; Coincide = $true })
    Huellas       = @([pscustomobject]@{ Raiz = 'C:\dev'; Comprobados = 7
            Ausentes = 0
            Diferencias = @([pscustomobject]@{ Ruta = 'a.txt'; Motivo = 'HUELLA DISTINTA' })
            Correcto = $false
        })
}
$pantallaPodrida = (Show-VeredictoDelCotejo -Resultado $cotejoPodrido 6>&1 | Out-String)
Test-Afirmacion -Nombre 'Una huella distinta es FALLO, y gana sobre una estructura que si cuadra' `
    -Esperado $true -Obtenido ($pantallaPodrida -match 'FALLO')

# "NO PUDE MIRAR" NO ES "MIRE Y ESTA BIEN". Con el nodo apagado, un OK seria la
# mentira mas cara del tablero.
$cotejoCiego = [pscustomobject]@{ NodoVivo = $false; Coincidencias = @(); Huellas = @() }
$pantallaCiega = (Show-VeredictoDelCotejo -Resultado $cotejoCiego 6>&1 | Out-String)
Test-Afirmacion -Nombre 'Con el nodo caido el cotejo dice SIN DATOS, nunca OK' `
    -Esperado $true -Obtenido ($pantallaCiega -match 'SIN DATOS')

# EL VOCABULARIO DEL MENU QUEDA FIJADO. Se pidio mirando la pantalla, y sin una
# prueba vuelve solo en el proximo cambio.
$fuenteMenu = Get-Content -LiteralPath (Join-Path (Split-Path $PSScriptRoot -Parent) '1-Interfaz\tablero.ps1') -Raw -Encoding UTF8
Test-Afirmacion -Nombre 'El menu pide una "opcion", no una "tecla"' `
    -Esperado $true -Obtenido ($fuenteMenu -match "Read-Host\s+'\s*opcion'")
Test-Afirmacion -Nombre 'Ninguna opcion se sigue llamando "simular": no decia simular QUE' `
    -Esperado $true -Obtenido ($fuenteMenu -notmatch "\[\d\]\s+simular")
Test-Afirmacion -Nombre 'La vista previa va SANGRADA bajo la copia que previsualiza' `
    -Esperado $true -Obtenido ($fuenteMenu -match "\[2\]\s\s+vista previa" -and $fuenteMenu -match "\[8\]\s\s+vista previa")

# ---------------------------------------------------------------------------
#  LA AUDITORIA DEL 2026-09-14 -- siete defectos, y seis eran el mismo camino
#
#  Todos comparten una forma: el motor se cae por una via que nadie previo, y
#  NADIE LO DICE. La prueba del criterio 9 ya cubria "matan el motor" -la marca
#  se queda, su PID muere, el icono lo pinta rojo-, pero no cubria "el motor se
#  muere solo", que es la via por la que se llega sin que nadie apriete nada.
# ---------------------------------------------------------------------------

Write-Titulo 'Auditoria 2026-09-14: el motor que moria en silencio'

# --- 1. 'Copiando' SIN MARCA ES FALLA, Y ANTES ERA VERDE --------------------
#
# El defecto mas caro de los siete. Una excepcion dentro de la corrida dejaba
# ESTADO.txt en 'Copiando' y el finally retiraba la marca, asi que el indicador
# no veia ni marca vieja ni un estado malo y caia en su ultimo return, que dice
# Protegido. VERDE con el motor muerto, hasta 22 h.
$cajaVerde = Join-Path $caja.Raiz 'estado-copiando-colgado'
New-Item -ItemType Directory -Path $cajaVerde -Force | Out-Null
Write-EstadoRespaldo -Estado 'Copiando' -Detalle 'Corrida en curso' -Carpeta $cajaVerde -Confirm:$false

# Una tarea que EXISTA y este habilitada, o el indicador sale por el paso 1 y la
# prueba mediria otra cosa. Se busca una cualquiera del sistema en vez de fiarse
# de que la tarea real este registrada hoy: eso es lo que ya rompio una prueba
# el 2026-09-02.
$tareaCualquiera = @(Get-ScheduledTask -ErrorAction SilentlyContinue |
        Where-Object { $_.State -ne 'Disabled' } | Select-Object -First 1)
if ($tareaCualquiera.Count -eq 1) {
    $vistaColgada = & $indicador -UnaSolaLectura -NombreTarea $tareaCualquiera[0].TaskName -CarpetaEstado $cajaVerde
    Test-Afirmacion -Criterio '9' -Nombre 'Un "Copiando" SIN marca es FALLA, no Protegido (el verde que mentia)' `
        -Esperado 'Falla' -Obtenido $vistaColgada.Estado
    Test-Afirmacion -Criterio '9' -Nombre 'Y lo dice sin rodeos: empezo y nunca dijo como acabo' `
        -Esperado $true -Obtenido ($vistaColgada.Detalle -like '*nunca dijo como acabo*')
}

# --- 2. EL CATCH QUE FALTABA, PROBADO ROMPIENDO EL MOTOR DE VERDAD ----------
#
# No se simula la excepcion: se provoca. Una raiz declarada FUERA de toda
# traduccion hace que Get-RutaEnDestino lance -- lanza a proposito, para no
# inventarse un arbol paralelo en el nodo -- y esa excepcion sale justo por
# donde salian todas: dentro de la corrida y despues de escribir 'Copiando'.
$fuera = Join-Path $caja.Raiz 'fuera-de-traduccion'
New-Item -ItemType Directory -Path (Join-Path $fuera 'algo') -Force | Out-Null
'contenido' | Set-Content -LiteralPath (Join-Path $fuera 'algo\archivo.txt') -Encoding UTF8

# SE PARTE DE LA CONFIGURACION DEL ARENERO PERO SE DEJA UNA SOLA RAIZ, Y ES A
# PROPOSITO: las pruebas anteriores rompen centinelas y vacian carpetas a
# proposito, asi que heredar su estado haria que esta corrida abortara
# LIMPIAMENTE en una guarda anterior -- y entonces no probaria nada. Lo que se
# quiere medir es que la excepcion, cuando llega, deja rastro.
$cfgRota = Join-Path $caja.Raiz 'respaldo-sin-traduccion.jsonc'
$objRoto = Get-Content -LiteralPath $caja.Config -Raw -Encoding UTF8 | ConvertFrom-Jsonc
$objRoto.contenedores     = @()
$objRoto.centinelas       = @()
$objRoto.raicesDeclaradas = @([pscustomobject]@{
        ruta = $fuera; clase = 'A'; nota = 'sin traduccion declarada: el motor tiene que lanzar'
    })
$objRoto.carpetaEstado = Join-Path $caja.Raiz 'estado-excepcion'
$objRoto | ConvertTo-Json -Depth 12 | Set-Content -LiteralPath $cfgRota -Encoding UTF8
New-Item -ItemType Directory -Path $objRoto.carpetaEstado -Force | Out-Null

$motorAuditoria = Join-Path (Split-Path $PSScriptRoot -Parent) '2-Nucleo\respaldo.ps1'
$lanzo = $false
try { & $motorAuditoria -RutaConfiguracion $cfgRota -OmitirDeuda -Confirm:$false | Out-Null }
catch { $lanzo = $true }

Test-Afirmacion -Nombre 'Una raiz sin traduccion sigue lanzando: la excepcion NO se traga' `
    -Esperado $true -Obtenido $lanzo
$estadoTrasMorir = Read-EstadoRespaldo -Carpeta $objRoto.carpetaEstado
Test-Afirmacion -Criterio '9' -Nombre 'Pero ahora deja escrito FALLA antes de que la excepcion suba' `
    -Esperado 'Falla' -Obtenido ('' + $estadoTrasMorir['estado'])
Test-Afirmacion -Criterio '9' -Nombre 'Con causa "excepcion", que Resolve-EstadoVigente NO rebaja a ambar' `
    -Esperado 'excepcion' -Obtenido ('' + $estadoTrasMorir['causa']).Trim()
Test-Afirmacion -Criterio '9' -Nombre 'Y la marca no se queda colgada: el finally sigue haciendo su parte' `
    -Esperado $false -Obtenido (Test-Path -LiteralPath (Join-Path $objRoto.carpetaEstado 'EN_CURSO.lock'))

# Un estado 'Falla' con causa 'excepcion' NO es de los que se curan solos, ni
# aunque el nodo responda: nadie ha arreglado lo que rompio la corrida.
$sigueRojo = Resolve-EstadoVigente -Estado 'Falla' -Causa 'excepcion' -DestinoResponde $true
Test-Afirmacion -Nombre 'Una corrida muerta sigue ROJA aunque el nodo responda' `
    -Esperado 'Falla' -Obtenido $sigueRojo.Estado

# --- 3. EL REGISTRO NO PUEDE TUMBAR UNA CORRIDA -----------------------------
#
# Add-Content abre sin compartir la escritura: dos corridas a la vez perdian
# entre el 62 % y el 100 % de sus lineas, y como esto corre con 'Stop', la
# violacion LANZABA y mataba el motor.
$cajaLog = Join-Path $caja.Raiz 'registro-disputado'
New-Item -ItemType Directory -Path $cajaLog -Force | Out-Null
$logDelDia = Join-Path $cajaLog ('respaldo-{0:yyyy-MM-dd}.log' -f (Get-Date))

# Otro escritor lo tiene abierto en modo compartido, que es como lo deja ahora
# el propio motor. Antes esto bastaba para que el segundo muriera.
Write-LineaDeRegistro -Archivo $logDelDia -Linea 'primera linea' | Out-Null
$otro = [System.IO.File]::Open($logDelDia, [System.IO.FileMode]::Append,
    [System.IO.FileAccess]::Write, [System.IO.FileShare]::ReadWrite)
try {
    $escribio = Write-LineaDeRegistro -Archivo $logDelDia -Linea 'segunda linea con el archivo ya abierto'
}
finally { $otro.Dispose() }
Test-Afirmacion -Nombre 'Con el registro del dia ya abierto por otro, la linea SE ESCRIBE igual' `
    -Esperado $true -Obtenido $escribio
Test-Afirmacion -Nombre 'Y no se pierde la que ya estaba: las dos conviven' `
    -Esperado $true `
    -Obtenido ((Get-Content -LiteralPath $logDelDia -Raw -Encoding UTF8) -match 'primera linea' -and
               (Get-Content -LiteralPath $logDelDia -Raw -Encoding UTF8) -match 'segunda linea')

# Y CUANDO DE VERDAD NO SE PUEDE, SE RINDE EN SILENCIO. Bloqueo exclusivo: nadie
# va a poder escribir. Lo que NO puede pasar es que eso lance y se lleve por
# delante una copia que iba bien.
$cerrado = [System.IO.File]::Open($logDelDia, [System.IO.FileMode]::Append,
    [System.IO.FileAccess]::Write, [System.IO.FileShare]::None)
$tumbo = $false
try { Write-RegistroRespaldo -Nivel 'OK' -Etapa 'prueba' -Mensaje 'con el registro bloqueado' -CarpetaRegistro $cajaLog }
catch { $tumbo = $true }
finally { $cerrado.Dispose() }
Test-Afirmacion -Criterio '9' -Nombre 'Un registro que no se puede escribir NO tumba la corrida' `
    -Esperado $false -Obtenido $tumbo

# LAS 800 LINEAS DE DOS PROCESOS SALEN 800. Compartir el archivo no bastaba:
# FileMode::Append pone el puntero al final AL ABRIR, asi que dos procesos que
# abren a la vez escriben en el mismo sitio y uno pisa al otro -- 799 de 800, y
# los dos creyendo haber escrito. Con el permiso AppendData, Windows coloca cada
# escritura al final en el momento de escribirla.
$logDuelo = Join-Path $cajaLog 'duelo.log'
$guionDuelo = Join-Path $cajaLog 'escribe.ps1'
@"
param([string]`$archivo)
`$ErrorActionPreference = 'Stop'
. '$(Join-Path (Split-Path $PSScriptRoot -Parent) '2-Nucleo\comun.ps1')'
for (`$i = 0; `$i -lt 400; `$i++) {
    [void](Write-LineaDeRegistro -Archivo `$archivo -Linea ("linea {0} de {1}" -f `$i, `$PID))
}
"@ | Set-Content -LiteralPath $guionDuelo -Encoding UTF8
# Con $using: y no con -ArgumentList: es lo que el analizador pide y ademas deja
# leer de un vistazo que los dos trabajos apuntan al MISMO archivo, que es lo
# unico que hace valida esta prueba.
$duelo = 1..2 | ForEach-Object {
    Start-Job -ScriptBlock {
        & powershell.exe -NoProfile -ExecutionPolicy Bypass -File $using:guionDuelo -archivo $using:logDuelo
    }
}
$duelo | Wait-Job | Out-Null
$duelo | Remove-Job
Test-Afirmacion -Nombre 'Dos procesos anexando a la vez no se pisan ni una linea' `
    -Esperado 800 -Obtenido @(Get-Content -LiteralPath $logDuelo -Encoding UTF8).Count

# Y NO DEVUELVE NADA A LA TUBERIA. Write-LineaDeRegistro si devuelve si pudo
# escribir, y dejar ese booleano suelto haria que toda guarda que anota justo
# antes de su return -Test-OrigenUtilizable, Test-EspacioEnDestino,
# Get-DiscoFrio- devolviera DOS cosas en vez de su veredicto. Una guarda que
# dice "no, y ademas si" no es una guarda.
$loQueDevuelve = @(Write-RegistroRespaldo -Nivel 'OK' -Etapa 'prueba' `
        -Mensaje 'esto no puede salir por la tuberia' -CarpetaRegistro $cajaLog)
Test-Afirmacion -Nombre 'Write-RegistroRespaldo no emite NADA a la tuberia' `
    -Esperado 0 -Obtenido $loQueDevuelve.Count

# --- 4. LA PUERTA DE LAS DOS CORRIDAS ---------------------------------------
#
# El Programador impide dos corridas automaticas, pero nada impedia pulsar [1]
# mientras una estaba copiando.
$cajaPuerta = Join-Path $caja.Raiz 'estado-puerta'
New-Item -ItemType Directory -Path $cajaPuerta -Force | Out-Null
$cfgPuerta = [pscustomobject]@{ carpetaEstado = $cajaPuerta }

Test-Afirmacion -Nombre 'Sin marca, el tablero deja arrancar una corrida' `
    -Esperado $false -Obtenido (Test-HayCorridaEnCurso -Configuracion $cfgPuerta).EnCurso

[void](Enter-MarcaDeCorrida -Carpeta $cajaPuerta -Confirm:$false)
$puertaCerrada = Test-HayCorridaEnCurso -Configuracion $cfgPuerta
Test-Afirmacion -Nombre 'Con una corrida VIVA en marcha, el tablero se niega a lanzar otra' `
    -Esperado $true -Obtenido $puertaCerrada.EnCurso
Test-Afirmacion -Nombre 'Y dice quien la tiene tomada, no solo que no' `
    -Esperado $true -Obtenido ($puertaCerrada.Detalle -match ('pid {0}' -f $PID))

# UNA MARCA VIEJA NO BLOQUEA. Negarse a copiar por un cadaver dejaria el
# respaldo parado hasta que alguien borrase un archivo a mano.
Set-Content -LiteralPath (Join-Path $cajaPuerta 'EN_CURSO.lock') -Encoding UTF8 `
    -Value "pid=999999`ninicio=$((Get-Date).AddHours(-5).ToString('s'))`nequipo=CAJA`ntipo=nodo`n"
Test-Afirmacion -Nombre 'Una marca VIEJA no bloquea: es un cadaver, no una corrida' `
    -Esperado $false -Obtenido (Test-HayCorridaEnCurso -Configuracion $cfgPuerta).EnCurso
Exit-MarcaDeCorrida -Carpeta $cajaPuerta -Confirm:$false

# Y la puerta esta puesta donde se escribe, no donde solo se lee: cotejar
# mientras se copia es legitimo.
$fuenteTablero = Get-Content -LiteralPath (Join-Path (Split-Path $PSScriptRoot -Parent) '1-Interfaz\tablero.ps1') -Raw -Encoding UTF8
Test-Afirmacion -Nombre 'Las dos opciones que ESCRIBEN pasan por la puerta' `
    -Esperado 2 -Obtenido ([regex]::Matches($fuenteTablero, 'Test-HayCorridaEnCurso -Configuracion \$configuracion', 'IgnoreCase').Count)

# --- 5. LA GUARDA DE ESPACIO ------------------------------------------------
Test-Afirmacion -Nombre 'Sin nada que escribir no se pregunta por el espacio' `
    -Esperado $true -Obtenido (Test-EspacioEnDestino -Unc $caja.Raiz -BytesNecesarios 0).Cabe
Test-Afirmacion -Nombre 'Lo que cabe de sobra, cabe' `
    -Esperado $true -Obtenido (Test-EspacioEnDestino -Unc $caja.Raiz -BytesNecesarios 1024).Cabe
Test-Afirmacion -Nombre 'Un petabyte no cabe, y se dice ANTES de escribir un byte' `
    -Esperado $false -Obtenido (Test-EspacioEnDestino -Unc $caja.Raiz -BytesNecesarios 1PB).Cabe
# NO SABER NO ES MOTIVO PARA NO RESPALDAR. Un respaldo que se niega a correr
# porque no pudo medir un disco es un respaldo que no corre.
$noSeSabe = Test-EspacioEnDestino -Unc '\\no-existe-este-equipo-zz\recurso' -BytesNecesarios 1024
Test-Afirmacion -Nombre 'Si no se puede medir el espacio se copia igual, y se anota' `
    -Esperado $true -Obtenido ($noSeSabe.Cabe -and $noSeSabe.Motivo -eq 'no se pudo medir')

# --- 6. LAS RUTAS DE DISPOSITIVO NO SON EL NODO -----------------------------
Test-Afirmacion -Criterio '1' -Nombre 'Una UNC de verdad sigue valiendo' `
    -Esperado $true -Obtenido (Test-RutaUnc -Ruta '\\192.168.1.38\datos')
Test-Afirmacion -Criterio '1' -Nombre 'La barra que faltaba el 02/09 se sigue cazando' `
    -Esperado $false -Obtenido (Test-RutaUnc -Ruta '\192.168.1.38\datos')
Test-Afirmacion -Criterio '1' -Nombre '\\?\C:\ es el disco LOCAL disfrazado de UNC: se rechaza' `
    -Esperado $false -Obtenido (Test-RutaUnc -Ruta '\\?\C:\datos')
Test-Afirmacion -Criterio '1' -Nombre 'Y \\.\C:\ tambien' `
    -Esperado $false -Obtenido (Test-RutaUnc -Ruta '\\.\C:\datos')

# --- 7. EL RESULTADO ABORTADO ESTA COMPLETO ---------------------------------
# Bajo StrictMode, leer una propiedad que no existe LANZA. Las salidas por
# aborto no llevaban Fin, asi que el primero que cronometrara una corrida
# abortada se llevaba la excepcion dentro de la corrida.
$abortada = Invoke-CorridaDeRespaldo -Configuracion ([pscustomobject]@{
        deudaPrimeraCorrida = [pscustomobject]@{ raices = @(); saldada = $true }
        contenedores        = @()
        raicesDeclaradas    = @()
        exclusiones         = @()
    }) -Confirm:$false
Test-Afirmacion -Nombre 'Una corrida sin raices aborta, no revienta' `
    -Esperado $true -Obtenido $abortada.Abortada
# LA GUARDA DE "NINGUNA RAIZ" NO PODIA CUBRIR SU PROPIO CASO. PowerShell
# desenvuelve lo que devuelve una funcion, asi que una lista vacia llegaba como
# $null y `$null.Count` LANZA bajo StrictMode: en vez del aborto limpio salia una
# excepcion. Se destapo al probar el catch nuevo.
Test-Afirmacion -Nombre 'Y aborta LIMPIAMENTE, con su causa, en vez de lanzar' `
    -Esperado 'sinRaices' -Obtenido ('' + $abortada.Causa)
Test-Afirmacion -Nombre 'Y su resultado trae Fin declarado: StrictMode no lo puede tumbar' `
    -Esperado $true -Obtenido ($abortada.PSObject.Properties.Name -contains 'Fin')

# UNA GUARDA DEVUELVE UN VEREDICTO, NO UNA LISTA. Es la otra cara de que
# Write-RegistroRespaldo no emita: estas funciones anotan y despues devuelven.
$origenAusente = Test-OrigenUtilizable -Raiz ([pscustomobject]@{
        Ruta = Join-Path $caja.Raiz 'no-existe-jamas'; Clase = 'B' })
Test-Afirmacion -Nombre 'Una guarda que dice que no, devuelve UN solo no' `
    -Esperado $true -Obtenido ($origenAusente -is [bool] -and -not $origenAusente)

# --- 8. ADR-0078: LOS .ps1 VAN SIN ACENTOS, Y AHORA HAY QUIEN LO VIGILE -----
#
# El repositorio va en LF y sin BOM, y PowerShell 5.1 lee un .ps1 sin BOM como
# ANSI: un acento sale roto. La regla estaba escrita en tres sitios y NADIE la
# comprobaba; la auditoria encontro un "paso" con tilde viviendo en comun.ps1.
$conAcentos = New-Object System.Collections.Generic.List[string]
foreach ($archivoPs1 in @(Get-ChildItem -LiteralPath (Split-Path $PSScriptRoot -Parent) -Filter '*.ps1' -Recurse)) {
    $bytes = [System.IO.File]::ReadAllBytes($archivoPs1.FullName)
    if (@($bytes | Where-Object { $_ -gt 127 }).Count -gt 0) { $conAcentos.Add($archivoPs1.Name) }
}
Test-Afirmacion -Nombre 'Ningun .ps1 lleva un solo byte fuera de ASCII (ADR-0078)' `
    -Esperado '' -Obtenido ($conAcentos -join ', ')

# ===========================================================================

if (-not $Conservar) {
    Remove-Item -LiteralPath $CarpetaCaja -Recurse -Force -ErrorAction SilentlyContinue
}

$total = $script:Resultados.Count
$pasan = @($script:Resultados | Where-Object { $_.Paso }).Count
$fallan = $total - $pasan

$script:Resultados | ForEach-Object {
    $marca = if ($_.Paso) { 'OK  ' } else { 'FALLA' }
    $crit  = if ($_.Criterio) { "[c$($_.Criterio)] " } else { '' }
    $linea = '{0} {1}{2}' -f $marca, $crit, $_.Nombre
    if (-not $_.Paso) { $linea += "  (esperado '$($_.Esperado)', obtenido '$($_.Obtenido)')" }
    Write-Information $linea -InformationAction Continue
}
Write-Information "`n$pasan de $total pasan. $fallan fallan." -InformationAction Continue

return [pscustomobject]@{
    Total      = $total
    Pasan      = $pasan
    Fallan     = $fallan
    Resultados = $script:Resultados.ToArray()
}
