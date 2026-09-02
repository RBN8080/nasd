#Requires -Version 5.1
<#
    notificar.ps1  -  LA COSTURA con el nodo.

    La seccion 11 lo nombra por su nombre y dice que es el UNICO archivo que
    cambia si el transporte cambia. Esa frase es el contrato de este archivo.

    NO SE CONSTRUYE UN NOTIFICADOR NUEVO. `10_CODIGO/internal/aviso` ya existe
    con severidades, politica, agrupacion por situacion y pruebas, y ADR-0073 /
    ADR-0074 ya establecieron Telegram y el testigo externo.

    EL CLIENTE SOLO EMITE EL EVENTO; EL NODO DECIDE QUE HACER CON EL.

    LO QUE SE LEYO ANTES DE ESCRIBIR ESTO, porque la seccion 11 lo exigia, y lo
    que decidio la forma:

      - `internal/aviso` NO es un notificador generico: es EL EVALUADOR DE
        SITUACIONES DEL NODO. Su enum `Clase` esta atado a hechos de
        `internal/seguridad` -sondeo ajeno, anomalia de conducta- y su propia
        documentacion dice que no se inventa ninguna categoria sin un hecho
        medible detras. No hay una clase para "un respaldo no corrio", y no
        deberia inventarse una desde aqui.

      - Anadirsela seria MODIFICAR EL PRODUCTO, y la cabecera de este contrato
        dice literalmente: "Este documento describe un cliente; no modifica el
        producto". Asi que el cliente NO extiende el catalogo del nodo.

      - Por eso el cliente EMITE en el vocabulario del nodo y lo deja donde el
        nodo pueda recogerlo el dia que alguien decida absorberlo: `_SISTEMA/`,
        que la seccion 8 ya declara. Hoy nadie lo lee, y eso esta bien: el canal
        que de verdad avisa hoy es el testigo externo (testigo.ps1, ADR-0079),
        que no necesita ni una linea nueva en `nasd`.

    VOCABULARIO. El cliente piensa en OK / ATENCION / FRENO / ERROR (seccion 11)
    y el nodo en Verde / Amarillo / Naranja / Rojo (`internal/aviso/severidad.go`).
    La traduccion vive aqui y en ningun otro sitio.

      OK        -> verde      silencioso
      ATENCION  -> amarillo   entra al resumen agrupado
      FRENO     -> rojo       avisa siempre: el freno significa que algo se
                              detuvo por conducta anomala, y eso es intervenir
      ERROR     -> rojo       avisa siempre

    NARANJA NO SE EMITE DESDE AQUI, y es la misma razon por la que el nodo
    tampoco lo emite: el naranja es "dejo de reportar", y algo que dejo de
    reportar no puede reportarlo. Lo dice el testigo externo o no lo dice nadie.

    REGLA DE RUIDO: el exito diario NO notifica. Una alerta que llega todos los
    dias deja de leerse justo antes del dia que importaba.
#>

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

# La traduccion, en un solo sitio.
$script:SeveridadDelNodo = @{
    'OK'       = 'verde'
    'ATENCION' = 'amarillo'
    'FRENO'    = 'rojo'
    'ERROR'    = 'rojo'
}

function ConvertTo-SeveridadDelNodo {
    <#
        .SYNOPSIS
            Traduce la severidad del cliente a la del nodo.
        .PARAMETER Nivel
            OK, ATENCION, FRENO o ERROR.
    #>
    [CmdletBinding()]
    [OutputType([string])]
    param(
        [Parameter(Mandatory)]
        [ValidateSet('OK', 'ATENCION', 'FRENO', 'ERROR')]
        [string] $Nivel
    )
    return $script:SeveridadDelNodo[$Nivel]
}

function Send-EventoAlNodo {
    <#
        .SYNOPSIS
            Emite UN evento. No decide nada: eso es del nodo.
        .DESCRIPTION
            Escribe una linea JSON en `_SISTEMA/eventos-cliente.jsonl` del nodo.
            Una linea por suceso, anexada: el mismo registro estructurado que el
            resto del proyecto usa, en un formato que cualquier lector futuro
            puede consumir sin acordar nada mas.

            SI EL NODO NO RESPONDE, ESTO NO FALLA. Ninguna decision del cliente
            puede depender de que un aviso se haya entregado -es la regla que
            gobierna `internal/aviso`, heredada tal cual-. Si el destino no esta,
            el evento queda en el registro local y ya.
        .PARAMETER Nivel
            Severidad del cliente.
        .PARAMETER Situacion
            Clave corta y estable de QUE paso. Se agrupa por ella.
        .PARAMETER Mensaje
            Una frase para la persona.
        .PARAMETER Hechos
            Cifras que sostienen la frase. Sin ellas el aviso es una opinion.
        .PARAMETER RutaSistema
            Carpeta `_SISTEMA` del nodo.
    #>
    [CmdletBinding(SupportsShouldProcess)]
    [OutputType([psobject])]
    param(
        [Parameter(Mandatory)]
        [ValidateSet('OK', 'ATENCION', 'FRENO', 'ERROR')]
        [string] $Nivel,

        [Parameter(Mandatory)][ValidateNotNullOrEmpty()][string] $Situacion,
        [Parameter(Mandatory)][ValidateNotNullOrEmpty()][string] $Mensaje,

        [hashtable] $Hechos = @{},

        [ValidateNotNullOrEmpty()]
        [string] $RutaSistema
    )

    $evento = [ordered]@{
        momento   = (Get-Date).ToString('o')
        origen    = $env:COMPUTERNAME
        severidad = ConvertTo-SeveridadDelNodo -Nivel $Nivel
        nivel     = $Nivel
        situacion = $Situacion
        mensaje   = $Mensaje
        hechos    = $Hechos
    }

    # Silencioso por diseno: el exito diario no notifica (seccion 11).
    if ($Nivel -eq 'OK') {
        Write-Verbose "Evento verde, no se emite: $Situacion"
        return [pscustomobject]@{ Emitido = $false; Motivo = 'verde: el exito no notifica'; Evento = $evento }
    }

    if (-not $PSBoundParameters.ContainsKey('RutaSistema')) {
        return [pscustomobject]@{ Emitido = $false; Motivo = 'sin destino declarado'; Evento = $evento }
    }

    if (-not $PSCmdlet.ShouldProcess($RutaSistema, "Emitir evento '$Situacion'")) {
        return [pscustomobject]@{ Emitido = $false; Motivo = 'WhatIf'; Evento = $evento }
    }

    try {
        if (-not (Test-Path -LiteralPath $RutaSistema -PathType Container)) {
            New-Item -ItemType Directory -Path $RutaSistema -Force -ErrorAction Stop | Out-Null
        }
        $linea = ($evento | ConvertTo-Json -Compress -Depth 6)
        Add-Content -LiteralPath (Join-Path $RutaSistema 'eventos-cliente.jsonl') -Value $linea -Encoding UTF8 -ErrorAction Stop
        return [pscustomobject]@{ Emitido = $true; Motivo = $null; Evento = $evento }
    }
    catch {
        # Que el canal falle no puede tumbar la corrida. Se anota y se sigue.
        Write-Warning "No se pudo emitir el evento '$Situacion' al nodo: $($_.Exception.Message)"
        return [pscustomobject]@{ Emitido = $false; Motivo = $_.Exception.Message; Evento = $evento }
    }
}
