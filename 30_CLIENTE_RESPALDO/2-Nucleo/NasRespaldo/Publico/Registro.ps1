#Requires -Version 5.1

# Modulo NasRespaldo - dominio: Registro.ps1

function Write-LineaDeRegistro {
    <#
        .SYNOPSIS
            Anexa una linea al registro del dia SIN PODER TUMBAR AL LLAMADOR.

        .DESCRIPTION
            ES UNA FUNCION APARTE PORQUE ES UNA GUARDA, NO UN DETALLE. Aqui vivia
            un `Add-Content` a pelo, y `Add-Content` abre el archivo SIN COMPARTIR
            LA ESCRITURA: mientras lo tiene abierto, otro proceso que intente
            anexar recibe una violacion de uso compartido.

            MEDIDO, y no es un caso de laboratorio: dos procesos anexando 400
            lineas cada uno al mismo archivo del dia perdieron 247 y 399. O sea
            entre el 62 % y el 100 % de los apuntes.

            QUE DOS PROCESOS ESCRIBAN A LA VEZ ES NORMAL AQUI. El Programador
            impide dos corridas automaticas simultaneas (MultipleInstances
            IgnoreNew), pero NO impide que alguien pulse [1] en el tablero
            mientras una corrida programada esta copiando. Los dos escriben en
            `respaldo-AAAA-MM-DD.log`.

            Y EL FALLO NO SE QUEDABA EN UNA LINEA PERDIDA. `Write-RegistroRespaldo`
            corre con $ErrorActionPreference = 'Stop' y se llama desde el camino
            critico del motor: la violacion LANZABA, y como la corrida no tiene
            catch, el motor moria a mitad dejando ESTADO.txt en 'Copiando' -- que
            el indicador pintaba VERDE. Un apunte de registro que no se puede
            escribir no puede costar un respaldo.

            DOS ARREGLOS, Y HACEN FALTA LOS DOS:

              1. Se abre con FileShare::ReadWrite para que los dos escritores
                 quepan a la vez, Y CON EL PERMISO AppendData, que es la mitad
                 que de verdad importa.

                 COMPARTIR EL ARCHIVO NO BASTA, Y SE MIDIO. Con el permiso
                 normal de escritura, `FileMode::Append` coloca el puntero al
                 final UNA VEZ, AL ABRIR: dos procesos que abren a la vez se
                 quedan con el mismo desplazamiento y el segundo escribe encima
                 del primero. Dos procesos de 400 lineas dieron 799 de 800, y
                 los DOS creyeron haberlas escrito todas -- o sea una perdida
                 silenciosa, que es la peor clase.

                 Con AppendData, Windows situa cada escritura al final del
                 archivo EN EL MOMENTO DE ESCRIBIR, no al abrir. Cada linea va
                 en una sola llamada, asi que ni se pisan ni se parten. Con ese
                 permiso las 800 salen 800.

              2. Si aun asi no se puede -disco lleno, permisos, antivirus-, se
                 reintenta un punado de veces y despues SE RINDE EN SILENCIO.
                 Es la misma regla que ya gobierna a notificar.ps1 y testigo.ps1:
                 el respaldo es lo importante y anotar es lo secundario.

            EL BOM SE CONSERVA. `Add-Content -Encoding UTF8` en PowerShell 5.1
            escribe marca de orden de bytes al crear el archivo, y quien lo lee
            con `Get-Content` a secas depende de ella para no destrozar los
            acentos de una ruta. Se escribe a mano solo cuando el archivo nace.
        .PARAMETER Archivo
            El registro del dia.
        .PARAMETER Linea
            La linea ya formada, sin salto final.
    #>
    [CmdletBinding()]
    [OutputType([bool])]
    param(
        [Parameter(Mandatory)][ValidateNotNullOrEmpty()][string] $Archivo,
        [Parameter(Mandatory)][AllowEmptyString()][string] $Linea
    )

    $carpeta = Split-Path -Path $Archivo -Parent
    for ($intento = 1; $intento -le 5; $intento++) {
        try {
            if ($carpeta -and -not (Test-Path -LiteralPath $carpeta)) {
                New-Item -ItemType Directory -Path $carpeta -Force -ErrorAction Stop | Out-Null
            }
            $nace = -not (Test-Path -LiteralPath $Archivo -PathType Leaf)
            $texto = $Linea + "`r`n"
            $bytes = [System.Text.Encoding]::UTF8.GetBytes($texto)
            if ($nace) { $bytes = ([byte[]]@(0xEF, 0xBB, 0xBF)) + $bytes }

            if ($PSVersionTable.PSVersion.Major -ge 6) {
                # En .NET 6+ (PowerShell 7) no existe la sobrecarga con
                # FileSystemRights; FileMode::Append con FileAccess::Write usa
                # FILE_APPEND_DATA en Windows de forma nativa.
                $flujo = New-Object System.IO.FileStream(
                    $Archivo,
                    [System.IO.FileMode]::Append,
                    [System.IO.FileAccess]::Write,
                    [System.IO.FileShare]::ReadWrite,
                    4096,
                    [System.IO.FileOptions]::None)
            }
            else {
                $flujo = New-Object System.IO.FileStream(
                    $Archivo,
                    [System.IO.FileMode]::Append,
                    [System.Security.AccessControl.FileSystemRights]::AppendData,
                    [System.IO.FileShare]::ReadWrite,
                    4096,
                    [System.IO.FileOptions]::None)
            }
            try { $flujo.Write($bytes, 0, $bytes.Length) }
            finally { $flujo.Dispose() }
            return $true
        }
        catch {
            # Espera creciente y corta: lo que se disputa es un archivo de texto,
            # no un recurso caro. Cinco intentos cubren de sobra el solape de dos
            # corridas; lo que no arregle eso no lo arregla esperar mas.
            if ($intento -lt 5) { Start-Sleep -Milliseconds (20 * $intento) }
        }
    }
    return $false
}

function Get-CarpetaDeRegistro {
    <#
        .SYNOPSIS
            Donde se escriben los registros diarios respaldo-AAAA-MM-DD.log.
        .DESCRIPTION
            Permite aislar las pruebas del registro de produccion mediante la
            variable de entorno NASRESPALDO_REGISTRO o la configuracion, sin
            cambiar la ruta por omision bajo LOCALAPPDATA.
        .PARAMETER Configuracion
            Opcional: objeto de configuracion del cliente.
    #>
    [CmdletBinding()]
    [OutputType([string])]
    param(
        [psobject] $Configuracion
    )

    if ($Configuracion -and
        ($Configuracion.PSObject.Properties.Name -contains 'carpetaRegistro') -and
        $Configuracion.carpetaRegistro) {
        return $Configuracion.carpetaRegistro
    }
    if (-not [string]::IsNullOrWhiteSpace($env:NASRESPALDO_REGISTRO)) {
        return $env:NASRESPALDO_REGISTRO
    }
    if ($Configuracion -and
        ($Configuracion.PSObject.Properties.Name -contains 'carpetaEstado') -and
        $Configuracion.carpetaEstado) {
        return (Join-Path (Split-Path $Configuracion.carpetaEstado -Parent) 'registro')
    }
    return (Join-Path $env:LOCALAPPDATA 'NasRespaldo\registro')
}

function Remove-RegistrosAntiguos {
    <#
        .SYNOPSIS
            Retira registros diarios mas viejos que el plazo de retencion.
        .DESCRIPTION
            Mantiene acotado el crecimiento de LOCALAPPDATA\NasRespaldo\registro
            sin tocar nunca el registro de hoy ni tumbar una corrida si un
            archivo esta abierto por otra herramienta.
        .PARAMETER CarpetaRegistro
            Carpeta donde viven respaldo-AAAA-MM-DD.log.
        .PARAMETER DiasRetencion
            Cuantos dias conservar. Por omision 90.
    #>
    [CmdletBinding(SupportsShouldProcess)]
    [OutputType([int])]
    param(
        [ValidateNotNullOrEmpty()]
        [string] $CarpetaRegistro = (Get-CarpetaDeRegistro),

        [ValidateRange(1, 3650)]
        [int] $DiasRetencion = 90
    )

    $borrados = 0
    if (-not (Test-Path -LiteralPath $CarpetaRegistro -PathType Container)) { return 0 }
    $corte = (Get-Date).Date.AddDays(-$DiasRetencion)
    foreach ($f in @(Get-ChildItem -LiteralPath $CarpetaRegistro -Filter 'respaldo-*.log' -File -ErrorAction SilentlyContinue)) {
        if ($f.Name -notmatch '^respaldo-(\d{4}-\d{2}-\d{2})\.log$') { continue }
        [datetime] $fecha = [datetime]::MinValue
        if (-not [datetime]::TryParseExact($Matches[1], 'yyyy-MM-dd', [System.Globalization.CultureInfo]::InvariantCulture, [System.Globalization.DateTimeStyles]::None, [ref]$fecha)) {
            continue
        }
        if ($fecha -ge $corte) { continue }
        if (-not $PSCmdlet.ShouldProcess($f.FullName, 'Retirar registro antiguo')) { continue }
        try {
            Remove-Item -LiteralPath $f.FullName -Force -ErrorAction Stop
            $borrados++
        }
        catch {
            Write-Verbose "No se pudo retirar registro antiguo '$($f.Name)': $($_.Exception.Message)"
        }
    }
    return $borrados
}

function Write-RegistroRespaldo {
    <#
        .SYNOPSIS
            Escribe una linea de registro estructurada, y solo una por suceso.
        .DESCRIPTION
            Formato: marca de tiempo, nivel, etapa, mensaje. Se anexa a un
            archivo por dia. Paradigma heredado del nodo: registro estructurado,
            una linea por suceso.
        .PARAMETER Mensaje
            Que paso. Una frase.
        .PARAMETER Nivel
            OK, ATENCION, FRENO o ERROR. Mismo vocabulario de severidad que el
            nodo, para que los dos lados hablen igual (seccion 11).
        .PARAMETER Etapa
            Que parte del motor lo emite.
        .PARAMETER CarpetaRegistro
            Donde se anexa. Por omision, lo que resuelve Get-CarpetaDeRegistro.
    #>
    [CmdletBinding()]
    [OutputType([void])]
    param(
        [Parameter(Mandatory)]
        [ValidateNotNullOrEmpty()]
        [string] $Mensaje,

        [ValidateSet('OK', 'ATENCION', 'FRENO', 'ERROR')]
        [string] $Nivel = 'OK',

        [ValidateNotNullOrEmpty()]
        [string] $Etapa = 'general',

        [ValidateNotNullOrEmpty()]
        [string] $CarpetaRegistro = (Get-CarpetaDeRegistro),

        # SE ESCRIBE AL ARCHIVO Y NO SE ECOA A LA CONSOLA.
        #
        # Existe por un caso medido y no por gusto: las carpetas sin numerar son
        # CATORCE en este equipo, y cada corrida escupia catorce ADVERTENCIA
        # identicas en forma que tapaban el veredicto de la opcion que se acababa
        # de pulsar. El tablero YA pinta "14 carpetas sin clasificar" tres
        # renglones mas arriba, asi que el muro repetia en catorce lineas lo que
        # ya estaba en una.
        #
        # LO QUE NO SE PIERDE, y es la mitad importante: la linea sigue yendo al
        # archivo del dia con su nivel ATENCION intacto. La seccion 3.4 exige que
        # lo que no lleva numero SE REPORTE y no se ignore en silencio; eso se
        # cumple en el registro, que es donde se va a mirar. Lo que se retira es
        # el eco, que es fatiga de alarmas de ISA-18.2 -- la misma norma que este
        # proyecto ya cita para que solo el rojo parpadee.
        [switch] $SoloArchivo
    )

    $archivo = Join-Path $CarpetaRegistro ('respaldo-{0:yyyy-MM-dd}.log' -f (Get-Date))
    $linea = '{0} {1,-8} {2,-12} {3}' -f (Get-Date -Format 's'), $Nivel, $Etapa, $Mensaje
    # A Out-Null Y NO A PELO. Write-LineaDeRegistro devuelve si pudo escribir, y
    # esta funcion esta declarada [OutputType([void])] por un motivo: la llaman
    # Test-OrigenUtilizable, Test-EspacioEnDestino, Get-DiscoFrio y media docena
    # mas JUSTO ANTES de su propio return. Un booleano suelto se colaria en la
    # tuberia y esas funciones devolverian un ARRAY de dos cosas en vez de su
    # veredicto -- o sea que una guarda que dice "no" pasaria a decir "no, y
    # ademas si". Cazado al probar el arreglo, antes de que llegara a ninguna
    # corrida.
    Write-LineaDeRegistro -Archivo $archivo -Linea $linea | Out-Null

    if ($SoloArchivo) {
        Write-Verbose -Message $Mensaje
        return
    }

    switch ($Nivel) {
        # UN ErrorRecord CRUDO NO SALE A LA PANTALLA, NUNCA (04/09/2026).
        #
        # Write-Error pinta el mensaje CON marco: la linea de codigo que lo
        # emitio, los caretes debajo, CategoryInfo y FullyQualifiedErrorId. En
        # un proceso con menu eso cae en mitad del tablero y lo rompe -- paso el
        # 04/09 en la opcion [2], con seis lineas de volcado entre la tabla de
        # raices y el veredicto.
        #
        # WriteErrorLine escribe la MISMA linea, en rojo y por el MISMO flujo de
        # error, sin el marco. No se pierde telemetria (seccion 10): el archivo
        # del dia ya se escribio arriba con su nivel intacto, y quien capture la
        # salida sigue viendo el texto por stderr.
        #
        # El respaldo por Write-Error se queda para los hosts sin UI -- ISE
        # antiguo, algun runspace embebido -- donde $Host.UI puede no estar.
        'ERROR'    {
            if ($null -ne $Host -and $null -ne $Host.UI) { $Host.UI.WriteErrorLine("ERROR  $Mensaje") }
            else { Write-Error -Message $Mensaje -ErrorAction Continue }
        }
        'FRENO'    { Write-Warning -Message $Mensaje }
        'ATENCION' { Write-Warning -Message $Mensaje }
        default    { Write-Verbose -Message $Mensaje }
    }
}
