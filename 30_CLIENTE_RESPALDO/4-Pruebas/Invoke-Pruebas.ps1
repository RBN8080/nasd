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
        freno       = @{ umbralPorcentajeDeArchivosQueCambian = $Umbral }
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
        [switch] $Autorizar,
        [switch] $OmitirDeuda
    )
    $motor = Join-Path (Split-Path $PSScriptRoot -Parent) '2-Nucleo\respaldo.ps1'
    return & $motor -RutaConfiguracion $Caja.Config `
        -SoloSimular:$Simular -AutorizarFreno:$Autorizar -OmitirDeuda:$OmitirDeuda -Confirm:$false
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

# --- Criterio 6: cambio masivo -> se detiene y pide autorizacion -----------
Write-Titulo 'Criterio 6: el freno'
$r6 = Invoke-Motor -Caja $caja -Simular
Test-Afirmacion -Criterio '6' -Nombre 'Destino vacio: el freno salta y NO copia' `
    -Esperado $true -Obtenido $r6.Abortada
Test-Afirmacion -Criterio '6' -Nombre 'Y el motivo es el freno' `
    -Esperado $true -Obtenido ($r6.Motivo -like 'FRENO*')
Test-Afirmacion -Criterio '6' -Nombre 'Reconoce que es primera siembra, no un cifrado' `
    -Esperado $true -Obtenido (@($r6.Frenos | Where-Object { $_.PrimeraSiembra }).Count -eq 3)

# --- Primera copia real, autorizando el freno ------------------------------
Write-Titulo 'Primera copia (freno autorizado a proposito)'
$r7 = Invoke-Motor -Caja $caja -Autorizar
Test-Afirmacion -Nombre 'La corrida autorizada no aborta' -Esperado $false -Obtenido $r7.Abortada
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
Invoke-Motor -Caja $caja -Autorizar | Out-Null
Test-Afirmacion -Criterio '2' -Nombre 'Clase A: el archivo borrado en local SIGUE en el destino' `
    -Esperado $true -Obtenido (Test-Path -LiteralPath "$dFotos\foto1.txt")
Test-Afirmacion -Criterio '2' -Nombre 'Clase A: el destino conserva los 4' `
    -Esperado 4 -Obtenido @(Get-ChildItem -LiteralPath $dFotos -File).Count

# --- Criterio 3: clase B, el espejo si borra -------------------------------
Write-Titulo 'Criterio 3: clase B espeja'
Remove-Item -LiteralPath "$($caja.Origen)\Documents\01_DOCS\doc1.txt" -Force
Invoke-Motor -Caja $caja -Autorizar | Out-Null
Test-Afirmacion -Criterio '3' -Nombre 'Clase B: el archivo borrado en local DESAPARECE del destino' `
    -Esperado $false -Obtenido (Test-Path -LiteralPath "$dDocs\doc1.txt")
Test-Afirmacion -Criterio '3' -Nombre 'Clase B: el destino queda en 5' `
    -Esperado 5 -Obtenido @(Get-ChildItem -LiteralPath $dDocs -File).Count

# --- Criterio 1: una carpeta nueva entra sola ------------------------------
Write-Titulo 'Criterio 1: el numero es el interruptor'
New-Item -ItemType Directory -Path "$($caja.Origen)\Documents\09_Loquesea" -Force | Out-Null
'algo nuevo' | Set-Content -LiteralPath "$($caja.Origen)\Documents\09_Loquesea\nuevo.txt" -Encoding UTF8
# La deuda ya esta saldada -es una puerta de una sola vez-, asi que una carpeta
# numerada nueva es operacion normal. La configuracion NO se toca: sigue
# declarando los mismos contenedores y las mismas raices que antes.
Write-ConfiguracionDeCaja -Caja $caja -Saldada -Confirm:$false
$r10 = Invoke-Motor -Caja $caja -Autorizar
Test-Afirmacion -Criterio '1' -Nombre 'La carpeta nueva se respalda SIN editar la tabla de raices' `
    -Esperado $true -Obtenido (Test-Path -LiteralPath "$($caja.Destino)\CAJA\origen\Documents\09_Loquesea\nuevo.txt")
Test-Afirmacion -Criterio '1' -Nombre 'Y la deuda saldada ya no aborta las corridas normales' `
    -Esperado $false -Obtenido $r10.Abortada

# --- Criterio 5: centinela alterado -> aborta sin escribir nada ------------
Write-Titulo 'Criterio 5: los centinelas'
$cent = New-Centinela -Carpeta "$($caja.Origen)\Documents\01_DOCS" -Confirm:$false
Write-ConfiguracionDeCaja -Caja $caja -Centinelas @($cent) -Saldada -Confirm:$false
$r11 = Invoke-Motor -Caja $caja -Autorizar
Test-Afirmacion -Criterio '5' -Nombre 'Con el centinela intacto, la corrida procede' `
    -Esperado $false -Obtenido $r11.Abortada

$antesDeTocar = @(Get-ChildItem -LiteralPath $dDocs -File).Count
'ALGO LO TOCO' | Add-Content -LiteralPath $cent.ruta -Encoding UTF8
Remove-Item -LiteralPath "$($caja.Origen)\Documents\01_DOCS\doc2.txt" -Force  # un borrado que el espejo propagaria
$r12 = Invoke-Motor -Caja $caja -Autorizar
Test-Afirmacion -Criterio '5' -Nombre 'Centinela alterado: la corrida ABORTA' `
    -Esperado $true -Obtenido $r12.Abortada
Test-Afirmacion -Criterio '5' -Nombre 'Y aborta POR el centinela' `
    -Esperado $true -Obtenido ($r12.Motivo -like 'CENTINELA*')
Test-Afirmacion -Criterio '5' -Nombre 'Y NO ESCRIBIO NADA: el destino no cambio' `
    -Esperado $antesDeTocar -Obtenido @(Get-ChildItem -LiteralPath $dDocs -File).Count

# Un centinela BORRADO cuenta igual que uno alterado.
Remove-Item -LiteralPath $cent.ruta -Force
$r13 = Invoke-Motor -Caja $caja -Autorizar
Test-Afirmacion -Criterio '5' -Nombre 'Centinela BORRADO tambien aborta' `
    -Esperado $true -Obtenido ($r13.Abortada -and $r13.Motivo -like 'CENTINELA*')

# --- Guarda de origen: un origen vacio no vacia el destino -----------------
Write-Titulo 'Guarda de origen (el desastre que el freno no ve)'
Write-ConfiguracionDeCaja -Caja $caja -Saldada -Confirm:$false
Get-ChildItem -LiteralPath "$($caja.Origen)\proyecto" -Recurse -File | Remove-Item -Force
$r14 = Invoke-Motor -Caja $caja -Autorizar -OmitirDeuda
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
$sinUrl = Send-LatidoDelCliente -Senal 'Mal' -Url '' -Confirm:$false
Test-Afirmacion -Nombre 'Sin credencial configurada no falla: lo dice y sigue' `
    -Esperado $false -Obtenido $sinUrl.Enviado

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
Test-Afirmacion -Nombre 'El freno tampoco: espera una decision de una persona' `
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

# ===========================================================================
Write-Titulo 'La presentacion: que la ventana no mienta ni se rompa'
# ===========================================================================

# Se carga el tablero entero, no solo estilo.ps1: Format-Espacio y
# Format-EtiquetaDeRaiz viven ahi. Cargado con punto no abre ningun menu.
. "$PSScriptRoot\..\1-Interfaz\tablero.ps1"
$paletaSinColor = Get-Paleta -Capacidades ([pscustomobject]@{ Color = $false; Unicode = $false })

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

# Y la contraria, que es la que fallaba: una corrida a mano deja "a mano" en
# ESTADO.txt y NINGUNA acusacion de retraso en el registro.
$carpetaCaja = Join-Path $caja.Raiz 'estado-motor'
Write-ConfiguracionDeCaja -Caja $caja -Saldada -Confirm:$false
'algo' | Set-Content -LiteralPath "$($caja.Origen)\proyecto\src\mod1.txt" -Encoding UTF8
Invoke-Motor -Caja $caja -Autorizar | Out-Null
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
