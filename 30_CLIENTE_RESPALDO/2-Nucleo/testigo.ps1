#Requires -Version 5.1
<#
    testigo.ps1  -  El latido del CLIENTE hacia el testigo externo.

    Contrato: 30_CLIENTE_RESPALDO.md seccion 13.3.  ADR-0079.
    Referencia: ADR-0074, que es el testigo del NODO.

    MISMO SISTEMA, OTRO RELOJ. Y ahi esta todo:

                        NODO                        EQUIPO
      normal            encendido siempre           APAGADO MEDIA JORNADA
      su silencio       significa que murio         no significa nada, es de noche
      medida            latido 5 min, Grace 90 min  "hubo una corrida buena en
                                                     los ultimos N dias"

    Ponerle al cliente el latido de ADR-0074 haria sonar la alarma CADA
    MADRUGADA, que es la regla de ruido de la seccion 11 rota a diario.

    UN SEGUNDO CHECK en la cuenta de Healthchecks.io que YA EXISTE, no un
    sistema paralelo: dos sistemas de vigilancia que no se conocen es peor que
    uno. Y NO se comparte el check del nodo, que era la trampa comoda: el latido
    del nodo taparia el silencio del cliente, y el check estaria verde con el
    cliente muerto desde hace un mes.

    EL "ESTOY BIEN" SOLO SE EMITE SI LA CORRIDA TERMINO **Y** LA VERIFICACION
    PASO. Si se emitiera al acabar de copiar, un cliente que copia basura o que
    no verifica SE VERIA SANO - que es exactamente el engano contra el que se
    construyo ADR-0074. El aviso no dice "corri": dice "corri y comprobe".
    Esa condicion la impone Send-LatidoDelCliente y no se puede saltar desde
    fuera: no hay un parametro para "manda verde igualmente".

    LO QUE ESTE LATIDO NO DEMUESTRA, escrito para que no se confunda, igual que
    el criterio (7) de RF-41: detecta que HUBO UNA CORRIDA BUENA. No demuestra
    que los datos del nodo sigan enteros, ni que el disco frio este al dia, ni
    que nadie haya alterado el respaldo. Un cliente comprometido con privilegios
    puede emitirlo igual. DETECTA AUSENCIA, NO COMPROMISO.

    LA URL DEL CHECK ES UN SECRETO, y ADR-0074 dice que es el que MAS DANO HACE
    -mas que el token-: quien la tiene puede fingir que todo va bien. No vive en
    este repositorio (seccion 15, criterio 13). Se resuelve por el Administrador
    de credenciales de Windows.

    HOY ESTE ARCHIVO ESTA INERTE A PROPOSITO. Crear el check en Healthchecks.io
    y guardar su URL es trabajo del RESPONSABLE, no del agente, por la misma
    razon que el del nodo: es un secreto y no debe pasar por aqui. Mientras no
    exista, cada funcion lo dice y no falla.
#>

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

# Nombre de la entrada en el Administrador de credenciales de Windows.
$script:NombreCredencialTestigo = 'NasRespaldo:TestigoCliente'

function Get-UrlDelTestigo {
    <#
        .SYNOPSIS
            Lee la URL del check desde el Administrador de credenciales.
        .DESCRIPTION
            Devuelve $null si no esta guardada, sin lanzar: que el testigo no
            este configurado no puede tumbar una corrida de respaldo. La corrida
            es lo importante; el aviso es lo secundario, y ese orden esta escrito
            en la regla que gobierna `internal/aviso`.

            Se usa `cmdkey`/`CredRead` a traves de la API de Windows para no
            depender de ningun modulo de galeria (ADR-0013).
        .PARAMETER Nombre
            Nombre de la credencial.
    #>
    [CmdletBinding()]
    [OutputType([string])]
    param(
        [ValidateNotNullOrEmpty()]
        [string] $Nombre = $script:NombreCredencialTestigo
    )

    try {
        $tipo = 'NasRespaldoCred' -as [type]
        if (-not $tipo) {
            $firma = @'
using System;
using System.Runtime.InteropServices;
public class NasRespaldoCred {
    [StructLayout(LayoutKind.Sequential, CharSet=CharSet.Unicode)]
    private struct CREDENTIAL {
        public uint Flags; public uint Type; public IntPtr TargetName;
        public IntPtr Comment; public System.Runtime.InteropServices.ComTypes.FILETIME LastWritten;
        public uint CredentialBlobSize; public IntPtr CredentialBlob; public uint Persist;
        public uint AttributeCount; public IntPtr Attributes; public IntPtr TargetAlias;
        public IntPtr UserName;
    }
    [DllImport("advapi32.dll", SetLastError=true, CharSet=CharSet.Unicode)]
    private static extern bool CredRead(string target, uint type, uint flags, out IntPtr credential);
    [DllImport("advapi32.dll")]
    private static extern void CredFree(IntPtr buffer);

    public static string Leer(string target) {
        IntPtr p;
        if (!CredRead(target, 1, 0, out p)) { return null; }
        try {
            CREDENTIAL c = (CREDENTIAL)Marshal.PtrToStructure(p, typeof(CREDENTIAL));
            if (c.CredentialBlobSize == 0) { return null; }
            return Marshal.PtrToStringUni(c.CredentialBlob, (int)c.CredentialBlobSize / 2);
        } finally { CredFree(p); }
    }
}
'@
            Add-Type -TypeDefinition $firma -Language CSharp -ErrorAction Stop
        }
        return [NasRespaldoCred]::Leer($Nombre)
    }
    catch {
        Write-Verbose "No se pudo leer la credencial '$Nombre': $($_.Exception.Message)"
        return $null
    }
}

function Send-LatidoDelCliente {
    <#
        .SYNOPSIS
            Emite el latido. Verde SOLO si copio Y verifico.
        .DESCRIPTION
            Tres senales, que son las que Healthchecks entiende:
              /start   la corrida arranco
              (vacio)  termino BIEN -- y solo si las DOS condiciones se cumplen
              /fail    termino mal, o el freno la detuvo

            La condicion del verde esta aqui dentro y no se puede saltar desde
            fuera: no existe un parametro "manda verde igualmente". Si se
            emitiera al acabar de copiar, un cliente que copia basura o que no
            verifica se veria sano, que es el engano contra el que se construyo
            ADR-0074.
        .PARAMETER Senal
            Inicio, Bien o Mal.
        .PARAMETER CorridaTermino
            La copia acabo sin abortar.
        .PARAMETER VerificacionPaso
            La verificacion contra el destino salio limpia.
        .PARAMETER Detalle
            Texto corto que Healthchecks guarda junto al ping.
        .PARAMETER Url
            La del check. Si se omite se lee del Administrador de credenciales.
    #>
    [CmdletBinding(SupportsShouldProcess)]
    [OutputType([psobject])]
    param(
        [Parameter(Mandatory)]
        [ValidateSet('Inicio', 'Bien', 'Mal')]
        [string] $Senal,

        [switch] $CorridaTermino,
        [switch] $VerificacionPaso,

        [ValidateNotNull()]
        [string] $Detalle = '',

        [string] $Url
    )

    if (-not $PSBoundParameters.ContainsKey('Url') -or [string]::IsNullOrWhiteSpace($Url)) {
        $Url = Get-UrlDelTestigo
    }
    if ([string]::IsNullOrWhiteSpace($Url)) {
        return [pscustomobject]@{
            Enviado = $false
            Senal   = $Senal
            Motivo  = "El testigo no esta configurado: falta la credencial '$script:NombreCredencialTestigo'. Es trabajo del responsable, no del agente."
        }
    }

    # LA CONDICION DEL VERDE. No se negocia.
    if ($Senal -eq 'Bien' -and -not ($CorridaTermino -and $VerificacionPaso)) {
        $Senal = 'Mal'
        $Detalle = 'Degradado a fallo: el "estoy bien" exige corrida terminada Y verificacion pasada. ' + $Detalle
    }

    $destino = switch ($Senal) {
        'Inicio' { $Url.TrimEnd('/') + '/start' }
        'Mal'    { $Url.TrimEnd('/') + '/fail' }
        default  { $Url.TrimEnd('/') }
    }

    if (-not $PSCmdlet.ShouldProcess('el testigo externo', "Latido '$Senal'")) {
        return [pscustomobject]@{ Enviado = $false; Senal = $Senal; Motivo = 'WhatIf' }
    }

    try {
        [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
        $parametros = @{
            Uri             = $destino
            Method          = 'Post'
            TimeoutSec      = 15
            UseBasicParsing = $true
            ErrorAction     = 'Stop'
        }
        if ($Detalle) { $parametros['Body'] = $Detalle }
        Invoke-WebRequest @parametros | Out-Null
        return [pscustomobject]@{ Enviado = $true; Senal = $Senal; Motivo = $null }
    }
    catch {
        # Que el testigo no responda NO tumba la corrida. El respaldo es lo
        # importante; el aviso es lo secundario, y ese orden esta escrito.
        Write-Warning "El testigo no respondio: $($_.Exception.Message)"
        return [pscustomobject]@{ Enviado = $false; Senal = $Senal; Motivo = $_.Exception.Message }
    }
}
