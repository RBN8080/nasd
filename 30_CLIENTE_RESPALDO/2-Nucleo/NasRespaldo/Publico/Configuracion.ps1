#Requires -Version 5.1

# Modulo NasRespaldo - dominio: Configuracion.ps1

function ConvertFrom-Jsonc {
    <#
        .SYNOPSIS
            Convierte texto JSONC (JSON con comentarios) en objetos.
        .DESCRIPTION
            Retira comentarios de linea (//) y de bloque, respetando las cadenas
            entre comillas: una barra doble DENTRO de una cadena no es un
            comentario. Un recortador ingenuo destroza cualquier URL.
        .PARAMETER Texto
            El contenido completo del archivo .jsonc.
        .EXAMPLE
            Get-Content c.jsonc -Raw -Encoding UTF8 | ConvertFrom-Jsonc
    #>
    [CmdletBinding()]
    [OutputType([psobject])]
    param(
        [Parameter(Mandatory, ValueFromPipeline)]
        [AllowEmptyString()]
        [string] $Texto
    )
    process {
        $sb = New-Object System.Text.StringBuilder
        $enCadena = $false
        $escapado = $false
        $i = 0
        while ($i -lt $Texto.Length) {
            $c = $Texto[$i]
            if ($enCadena) {
                [void]$sb.Append($c)
                if ($escapado)      { $escapado = $false }
                elseif ($c -eq '\') { $escapado = $true }
                elseif ($c -eq '"') { $enCadena = $false }
                $i++
                continue
            }
            if ($c -eq '"') { $enCadena = $true; [void]$sb.Append($c); $i++; continue }
            if ($c -eq '/' -and ($i + 1) -lt $Texto.Length) {
                $siguiente = $Texto[$i + 1]
                if ($siguiente -eq '/') {
                    while ($i -lt $Texto.Length -and $Texto[$i] -ne "`n") { $i++ }
                    continue
                }
                if ($siguiente -eq '*') {
                    $i += 2
                    while (($i + 1) -lt $Texto.Length -and -not ($Texto[$i] -eq '*' -and $Texto[$i + 1] -eq '/')) { $i++ }
                    $i += 2
                    continue
                }
            }
            [void]$sb.Append($c)
            $i++
        }
        return ($sb.ToString() | ConvertFrom-Json)
    }
}

function Get-ConfiguracionRespaldo {
    <#
        .SYNOPSIS
            Lee y valida 3-Config/respaldo.jsonc.
        .DESCRIPTION
            Lee SIEMPRE con -Encoding UTF8 explicito: PowerShell 5.1 sin ese
            parametro interpreta el archivo como ANSI y destroza los acentos de
            los comentarios. Valida que existan las claves de las que depende el
            motor, para fallar aqui y no a mitad de una copia.
        .PARAMETER Ruta
            Ruta al .jsonc. Por omision, el de 3-Config junto a este guion.
    #>
    [CmdletBinding()]
    [OutputType([psobject])]
    param(
        [ValidateNotNullOrEmpty()]
        [string] $Ruta = (Join-Path $script:RaizCliente '3-Config\respaldo.jsonc')
    )

    if (-not (Test-Path -LiteralPath $Ruta -PathType Leaf)) {
        throw "No existe la configuracion: $Ruta"
    }

    $cfg = Get-Content -LiteralPath $Ruta -Raw -Encoding UTF8 | ConvertFrom-Jsonc

    # `cadencia` es OBLIGATORIA desde el cierre del pendiente 26. No tiene valor
    # por omision a proposito: de ella cuelgan el umbral del indicador, el color
    # de la tabla del tablero y cuantos latidos espera el testigo. Un valor por
    # omision silencioso significaria que un dia esos tres numeros describen una
    # cadencia distinta de la que corre de verdad, y ahi es donde el sistema
    # empieza a mentir.
    $obligatorias = @(
        'version', 'equipo', 'destinos', 'contenedores', 'raicesDeclaradas',
        'raicesDelNodo', 'exclusiones', 'centinelas', 'cadencia',
        'deudaPrimeraCorrida'
    )
    $faltan = @($obligatorias | Where-Object { $cfg.PSObject.Properties.Name -notcontains $_ })
    if ($faltan.Count -gt 0) {
        throw "La configuracion no declara: $($faltan -join ', ')"
    }
    if ($cfg.destinos.PSObject.Properties.Name -notcontains 'nodo') {
        throw 'La configuracion no declara destinos.nodo'
    }

    Write-Verbose "Configuracion v$($cfg.version) leida de $Ruta"
    return $cfg
}

function Set-ContenidoAtomico {
    <#
        .SYNOPSIS
            Escribe un archivo entero o no lo escribe: nunca a medias.
        .DESCRIPTION
            Escribe a un temporal en la MISMA carpeta -para que el reemplazo no
            cruce volumenes- y despues sustituye. Usa File::Replace cuando el
            destino ya existe, que es la operacion atomica de NTFS; si no existe,
            un Move basta porque no hay nada que un lector pueda ver a medias.
            El indicador de la seccion 10.2 lee ESTADO.txt mientras el motor lo
            reescribe: sin esto, puede leer medio archivo.
        .PARAMETER Ruta
            Archivo destino.
        .PARAMETER Contenido
            Texto completo a escribir.
    #>
    [CmdletBinding(SupportsShouldProcess)]
    [OutputType([void])]
    param(
        [Parameter(Mandatory)]
        [ValidateNotNullOrEmpty()]
        [string] $Ruta,

        [Parameter(Mandatory)]
        [AllowEmptyString()]
        [string] $Contenido
    )

    if (-not $PSCmdlet.ShouldProcess($Ruta, 'Escribir de forma atomica')) { return }

    $carpeta = Split-Path -Path $Ruta -Parent
    if ($carpeta -and -not (Test-Path -LiteralPath $carpeta)) {
        New-Item -ItemType Directory -Path $carpeta -Force | Out-Null
    }
    $marca    = [guid]::NewGuid().ToString('N').Substring(0, 8)
    $temporal = '{0}.{1}.tmp' -f $Ruta, $marca
    $respaldo = '{0}.{1}.bak' -f $Ruta, $marca
    try {
        # UTF8Encoding($false) = sin BOM, para no ensuciar lo que otros lean.
        [System.IO.File]::WriteAllText($temporal, $Contenido, (New-Object System.Text.UTF8Encoding($false)))
        if (Test-Path -LiteralPath $Ruta -PathType Leaf) {
            # File::Replace EXIGE una ruta de respaldo real. Pasarle $null falla
            # en PowerShell 5.1 -medido- porque PowerShell lo convierte en cadena
            # vacia y .NET la rechaza: "La ruta de acceso no tiene un formato
            # valido". El respaldo se crea y se retira aqui mismo; lo que importa
            # es que la SUSTITUCION del destino sigue siendo atomica, que es lo
            # que protege al lector del indicador.
            [System.IO.File]::Replace($temporal, $Ruta, $respaldo)
        }
        else {
            [System.IO.File]::Move($temporal, $Ruta)
        }
    }
    finally {
        foreach ($sobra in @($temporal, $respaldo)) {
            if (Test-Path -LiteralPath $sobra -PathType Leaf) {
                Remove-Item -LiteralPath $sobra -Force -ErrorAction SilentlyContinue
            }
        }
    }
}

function Hide-VentanaDeConsola {
    <#
        .SYNOPSIS
            Esconde la ventana negra de consola, PERO SOLO SI ES NUESTRA.

        .DESCRIPTION
            -WindowStyle Hidden NO BASTA, y se midio el 2026-09-02: la tarea se
            registro con esa bandera y la ventana aparecia igual. PowerShell CREA
            la consola y despues intenta ocultarla, asi que segun como arranque el
            proceso la ventana se queda en el escritorio.

            LA CONDICION ES LO IMPORTANTE. GetConsoleProcessList dice cuantos
            procesos comparten esta consola:

              1 proceso   la consola la creo este proceso al arrancar -tarea
                          programada, lanzador externo-. Es nuestra: se esconde.
              2 o mas     alguien la tenia abierta y lanzo esto desde ahi.
                          NO SE TOCA: esconderla le cerraria la terminal en la
                          cara a quien esta trabajando, y ademas se perderia la
                          salida que fue a ver.

            Sin esa distincion habria que elegir entre molestar a diario o no
            poder correr el motor a mano, y las dos son malas.
    #>
    [Diagnostics.CodeAnalysis.SuppressMessageAttribute(
        'PSUseShouldProcessForStateChangingFunctions', '',
        Justification = 'Oculta la ventana de SU PROPIO proceso y solo cuando nadie mas la comparte. No cambia nada persistente: ni disco, ni registro, ni configuracion. Pedir confirmacion para esconder una ventana al arrancar seria exactamente la ventana que se quiere evitar.')]
    [CmdletBinding()]
    [OutputType([bool])]
    param()

    try {
        if (-not ('NasRespaldo.VentanaPropia' -as [type])) {
            Add-Type -Namespace 'NasRespaldo' -Name 'VentanaPropia' -MemberDefinition @'
[System.Runtime.InteropServices.DllImport("kernel32.dll")]
public static extern System.IntPtr GetConsoleWindow();

[System.Runtime.InteropServices.DllImport("kernel32.dll")]
public static extern uint GetConsoleProcessList(uint[] lpdwProcessList, uint dwProcessCount);

[System.Runtime.InteropServices.DllImport("user32.dll")]
public static extern bool ShowWindow(System.IntPtr hWnd, int nCmdShow);
'@
        }

        $ventana = [NasRespaldo.VentanaPropia]::GetConsoleWindow()
        # Sin consola no hay nada que esconder.
        if ($ventana -eq [System.IntPtr]::Zero) { return $false }

        $lista = New-Object uint32[] 8
        $cuantos = [NasRespaldo.VentanaPropia]::GetConsoleProcessList($lista, 8)
        if ($cuantos -ne 1) { return $false }

        # 0 es SW_HIDE.
        return [NasRespaldo.VentanaPropia]::ShowWindow($ventana, 0)
    }
    catch {
        # Que no se pueda esconder NO es motivo para no respaldar ni para
        # quedarse sin icono. Se sigue, y se deja constancia en verboso.
        Write-Verbose "No se pudo esconder la consola: $($_.Exception.Message)"
        return $false
    }
}
