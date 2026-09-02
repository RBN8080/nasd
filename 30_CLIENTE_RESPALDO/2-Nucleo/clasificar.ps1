#Requires -Version 5.1
<#
    clasificar.ps1  -  Decide QUE se respalda y COMO. Nada mas.

    Contrato: 30_CLIENTE_RESPALDO.md seccion 3.  ADR-0077.

    EL PRINCIPIO (seccion 3.1): una carpeta no tiene clase. La clase es propiedad
    del LUGAR donde la carpeta vive. Clasificar por nombre produce una lista que
    se pudre el dia que se deja de actualizar, y ese dia siempre llega.

    DOS PREGUNTAS DISTINTAS, CADA UNA CON SU PROPIA RESPUESTA (seccion 3.5):
      Se respalda?   -> EL NUMERO.      NN_ si; sin numero, no.
      Como?          -> EL CONTENEDOR.  Documentos/Escritorio espejo (B);
                                        Imagenes/Videos/Musica aditivo (A).
    Son independientes. El motor no infiere ni adivina: mira DONDE VIVE la
    carpeta y con eso ya sabe las dos cosas.

    ORDEN DE RESOLUCION (seccion 3.3) - cuatro preguntas, se queda con la
    primera que responda:
      1. Esta bajo una raiz declarada?      Hereda su clase.
      2. Tiene un marcador .CLASE-X dentro? Gana sobre la herencia.
      3. Coincide con un patron de exclusion? Se descarta este donde este.
      4. Nada respondio -> ES HUERFANO: SE REPORTA, NO SE IGNORA.

    EL PUNTO 4 NO ES NEGOCIABLE. Un dato sin clasificar tiene que ser visible.

    Uso: se carga con punto, y exige comun.ps1 cargado antes.
#>

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

# NN_Nombre: dos digitos, guion bajo, y algo detras. Sale de los nombres reales
# del equipo y del nodo: 01_ACADEMICO, 09_ARCHIVO, 01_Capturas, 01_ESCRITORIO.
$script:PatronNumerada = '^\d{2}_.'

function Test-CarpetaNumerada {
    <#
        .SYNOPSIS
            Dice si un nombre de carpeta lleva el numero que enciende el respaldo.
        .DESCRIPTION
            Ponerle numero a una carpeta ES decir "respaldame". Quitarselo es
            decir "ya no". Un rename, reversible, sin tocar configuracion
            (seccion 3.4).
        .PARAMETER Nombre
            Nombre de la carpeta, sin ruta.
    #>
    [CmdletBinding()]
    [OutputType([bool])]
    param(
        [Parameter(Mandatory)][AllowEmptyString()][string] $Nombre
    )
    return $Nombre -match $script:PatronNumerada
}

function Test-RutaExcluida {
    <#
        .SYNOPSIS
            Punto 3 del orden de resolucion: los patrones globales de exclusion.
        .DESCRIPTION
            Clase C: ni se copia ni se reporta. La lista mezcla tres formas y las
            tres se soportan a proposito, porque asi esta escrita en el contrato:
              - rutas completas   C:\Users\usuario\Downloads
              - nombres sueltos   AppData, .cache, Juegos
              - comodines         *.vmdk
        .PARAMETER Ruta
            Ruta completa a evaluar.
        .PARAMETER Exclusiones
            La lista de la configuracion.
    #>
    [CmdletBinding()]
    [OutputType([bool])]
    param(
        [Parameter(Mandatory)][ValidateNotNullOrEmpty()][string] $Ruta,
        [Parameter(Mandatory)][AllowEmptyCollection()][string[]] $Exclusiones
    )

    foreach ($patron in $Exclusiones) {
        if ([string]::IsNullOrWhiteSpace($patron)) { continue }

        # Ruta enraizada: basta con que sea prefijo.
        if ($patron -match '^[A-Za-z]:\\' -or $patron.StartsWith('\\')) {
            if ($Ruta.StartsWith($patron.TrimEnd('\'), [System.StringComparison]::OrdinalIgnoreCase)) {
                return $true
            }
            continue
        }
        # Comodin: se compara contra la ruta entera y contra el nombre final.
        if ($patron.Contains('*') -or $patron.Contains('?')) {
            if ($Ruta -like $patron -or (Split-Path $Ruta -Leaf) -like $patron) { return $true }
            continue
        }
        # Nombre suelto: casa si es UN SEGMENTO de la ruta, no un trozo de
        # palabra. Sin esto, "Juegos" excluiria "JuegosPunk_Fotos".
        $segmentos = $Ruta.Split([char]'\', [System.StringSplitOptions]::RemoveEmptyEntries)
        if ($segmentos -contains $patron) { return $true }
    }
    return $false
}

function Get-MarcadorDeClase {
    <#
        .SYNOPSIS
            Punto 2 del orden de resolucion: el marcador .CLASE-X dentro de la carpeta.
        .DESCRIPTION
            Gana sobre la herencia, y viaja con la carpeta si se mueve. Tiene
            precedente: CACHEDIR.TAG es una convencion que ya respetan tar, borg,
            restic y rsnapshot para que una carpeta declare como debe tratarse.
            Hoy ninguna carpeta lo usa; el motor lo mira igual porque el orden de
            resolucion lo incluye.
        .PARAMETER Carpeta
            Carpeta a inspeccionar.
    #>
    [CmdletBinding()]
    [OutputType([string])]
    param(
        [Parameter(Mandatory)][ValidateNotNullOrEmpty()][string] $Carpeta
    )
    foreach ($clase in 'A', 'B', 'C') {
        if (Test-Path -LiteralPath (Join-Path $Carpeta ".CLASE-$clase") -PathType Leaf) {
            return $clase
        }
    }
    return $null
}

function Get-RaicesDeRespaldo {
    <#
        .SYNOPSIS
            Resuelve la configuracion a la lista concreta de carpetas que se copian.
        .DESCRIPTION
            Aplica las dos clases de fila de la seccion 3.5:
              CONTENEDOR      -> entran solo sus hijas NN_*, con la clase del
                                 contenedor.
              RAIZ DECLARADA  -> entra entera, con la clase que dice la tabla.

            Es la funcion que salda la deuda de la seccion 8: lo que devuelva
            tiene que ser exactamente lo que se subio a mano el 2026-09-02, ni
            una raiz mas ni una menos, o la primera corrida borra lo que no
            coincida.
        .PARAMETER Configuracion
            El objeto de configuracion completo.
    #>
    [CmdletBinding()]
    [OutputType([psobject[]])]
    param(
        [Parameter(Mandatory)][psobject] $Configuracion
    )

    $exclusiones = [string[]]$Configuracion.exclusiones
    $resultado = New-Object System.Collections.Generic.List[psobject]

    foreach ($contenedor in $Configuracion.contenedores) {
        if (-not (Test-Path -LiteralPath $contenedor.ruta -PathType Container)) {
            Write-RegistroRespaldo -Nivel 'ATENCION' -Etapa 'clasificar' `
                -Mensaje "Contenedor declarado que no existe en el disco: $($contenedor.ruta)"
            continue
        }
        $hijas = @(Get-ChildItem -LiteralPath $contenedor.ruta -Directory -Force -ErrorAction SilentlyContinue)
        foreach ($h in $hijas) {
            if (-not (Test-CarpetaNumerada -Nombre $h.Name)) { continue }
            if (Test-RutaExcluida -Ruta $h.FullName -Exclusiones $exclusiones) { continue }

            $clase = Get-MarcadorDeClase -Carpeta $h.FullName
            $porMarcador = $null -ne $clase
            if (-not $porMarcador) { $clase = $contenedor.clase }
            if ($clase -eq 'C') { continue }

            $resultado.Add([pscustomobject]@{
                Ruta        = $h.FullName
                Clase       = $clase
                Tipo        = 'contenedor'
                Contenedor  = $contenedor.ruta
                PorMarcador = $porMarcador
            })
        }
    }

    foreach ($raiz in $Configuracion.raicesDeclaradas) {
        $habilitada = $true
        if ($raiz.PSObject.Properties.Name -contains 'habilitada') { $habilitada = [bool]$raiz.habilitada }
        if (-not $habilitada) {
            Write-Verbose "Raiz declarada deshabilitada, se omite: $($raiz.ruta)"
            continue
        }
        $esVirtual = ($raiz.PSObject.Properties.Name -contains 'virtual') -and [bool]$raiz.virtual
        if ($esVirtual) {
            Write-Verbose "Raiz virtual, la arma otra etapa: $($raiz.ruta)"
            continue
        }
        if (-not (Test-Path -LiteralPath $raiz.ruta -PathType Container)) {
            Write-RegistroRespaldo -Nivel 'ATENCION' -Etapa 'clasificar' `
                -Mensaje "Raiz declarada que no existe en el disco: $($raiz.ruta)"
            continue
        }
        $resultado.Add([pscustomobject]@{
            Ruta        = (Get-Item -LiteralPath $raiz.ruta).FullName.TrimEnd('\')
            Clase       = $raiz.clase
            Tipo        = 'raizDeclarada'
            Contenedor  = $null
            PorMarcador = $false
        })
    }

    return $resultado.ToArray()
}

function Get-HuerfanosDeRespaldo {
    <#
        .SYNOPSIS
            Punto 4 del orden de resolucion, y NO es negociable.
        .DESCRIPTION
            Devuelve lo que se ve pero no se copia, para que se vea. Dos clases:

              sinNumerar  carpeta dentro de un contenedor declarado que no lleva
                          numero. Se reporta como "sin numerar - no se copia".
                          Se ve, no se ignora; lo que no se hace es copiarla.

            Si el motor "se salta lo que no reconoce" en silencio, se crea una
            carpeta importante fuera de toda raiz y se descubre seis meses
            despues que nunca se respaldo.
        .PARAMETER Configuracion
            El objeto de configuracion completo.
    #>
    [CmdletBinding()]
    [OutputType([psobject[]])]
    param(
        [Parameter(Mandatory)][psobject] $Configuracion
    )

    $exclusiones = [string[]]$Configuracion.exclusiones
    $huerfanos = New-Object System.Collections.Generic.List[psobject]

    foreach ($contenedor in $Configuracion.contenedores) {
        if (-not (Test-Path -LiteralPath $contenedor.ruta -PathType Container)) { continue }
        $hijas = @(Get-ChildItem -LiteralPath $contenedor.ruta -Directory -Force -ErrorAction SilentlyContinue)
        foreach ($h in $hijas) {
            if (Test-CarpetaNumerada -Nombre $h.Name) { continue }
            if (Test-RutaExcluida -Ruta $h.FullName -Exclusiones $exclusiones) { continue }
            $huerfanos.Add([pscustomobject]@{
                Ruta   = $h.FullName
                Motivo = 'sin numerar - no se copia'
                Tipo   = 'sinNumerar'
            })
        }
    }

    return $huerfanos.ToArray()
}

function Get-EstadoDeBandeja {
    <#
        .SYNOPSIS
            El conteo y la antiguedad de la bandeja de transito. No copia nada.
        .DESCRIPTION
            Downloads es zona de paso, no acervo, y queda en clase C por decision
            del responsable (seccion 3.6). Lo que el motor SI hace es reportar el
            conteo y la antiguedad del archivo mas viejo, para que se note cuando
            la zona de paso lleve meses sin vaciarse. La incomodidad era la parte
            util de la idea original; la copia no.
        .PARAMETER Ruta
            Carpeta de la bandeja.
    #>
    [CmdletBinding()]
    [OutputType([psobject])]
    param(
        [Parameter(Mandatory)][ValidateNotNullOrEmpty()][string] $Ruta
    )

    if (-not (Test-Path -LiteralPath $Ruta -PathType Container)) {
        return [pscustomobject]@{ Ruta = $Ruta; Existe = $false; Archivos = 0; DiasDelMasViejo = $null }
    }
    $archivos = @(Get-ChildItem -LiteralPath $Ruta -File -Force -Recurse -ErrorAction SilentlyContinue)
    $dias = $null
    if ($archivos.Count -gt 0) {
        $masViejo = ($archivos | Sort-Object LastWriteTime | Select-Object -First 1).LastWriteTime
        $dias = [int]((Get-Date) - $masViejo).TotalDays
    }
    return [pscustomobject]@{
        Ruta            = $Ruta
        Existe          = $true
        Archivos        = $archivos.Count
        DiasDelMasViejo = $dias
    }
}

function Find-SecretoEnRaiz {
    <#
        .SYNOPSIS
            ADR-0080: detecta secretos y los ENSENA. Nunca los excluye en silencio.
        .DESCRIPTION
            Los patrones son una RED DE DETECCION, NO UNA GARANTIA, y tienen
            fugas medidas: el barrido del 2026-09-02 incluia "contrase" y aun asi
            no encontro "contrasena.txt", porque la enye no casaba. Aparecio al
            listar la carpeta a mano.

            Por eso esta funcion no decide: lo que casa SE COPIA IGUAL y aparece
            como hallazgo, con su ruta. Un motor que mueve archivos personales
            por su cuenta basandose en un nombre es peor problema que el que
            resuelve.
        .PARAMETER Raices
            Las carpetas ya resueltas por Get-RaicesDeRespaldo.
        .PARAMETER Configuracion
            El objeto de configuracion completo.
    #>
    [CmdletBinding()]
    [OutputType([psobject[]])]
    param(
        [Parameter(Mandatory)][AllowEmptyCollection()][psobject[]] $Raices,
        [Parameter(Mandatory)][psobject] $Configuracion
    )

    if ($Configuracion.PSObject.Properties.Name -notcontains 'secretos') { return @() }
    $patrones = [string[]]$Configuracion.secretos.patronesDeDeteccion
    if (-not $patrones -or $patrones.Count -eq 0) { return @() }

    $regex = [regex]::new(($patrones -join '|'), 'IgnoreCase')
    $hallazgos = New-Object System.Collections.Generic.List[psobject]

    foreach ($raiz in $Raices) {
        $archivos = @(Get-ChildItem -LiteralPath $raiz.Ruta -File -Force -Recurse -ErrorAction SilentlyContinue)
        foreach ($a in $archivos) {
            if ($regex.IsMatch($a.Name)) {
                $hallazgos.Add([pscustomobject]@{
                    Ruta   = $a.FullName
                    Bytes  = $a.Length
                    Raiz   = $raiz.Ruta
                    Clase  = $raiz.Clase
                })
            }
        }
    }
    return $hallazgos.ToArray()
}
