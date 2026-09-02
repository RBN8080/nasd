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
        raicesDelNodo = @()
        exclusiones   = @('AppData', '.cache', 'Juegos', '*.vmdk')
        secretos      = [ordered]@{
            politica = 'detectar-y-reportar'
            cifrarRaizDeclarada = '_CONFIGS'
            patronesDeDeteccion = @('^\.env($|\.)', '\.pem$', '\.key$', 'contrase', 'token')
        }
        freno       = @{ umbralPorcentajeDeArchivosQueCambian = $Umbral }
        centinelas  = @($Centinelas)
        testigo     = @{ proveedor = 'ninguno'; medida = 'pruebas'; periodDias = $null; graceDias = $null }
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
#  Resumen
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
