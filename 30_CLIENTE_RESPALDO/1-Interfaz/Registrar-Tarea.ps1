#Requires -Version 5.1
<#
    .SYNOPSIS
        Registra, consulta o retira la tarea programada que hace que corra solo.

    .DESCRIPTION
        Contrato: 30_CLIENTE_RESPALDO.md secciones 10 y 12 (Fase 3), y el
        criterio 8 de la seccion 15: "correr desde la tarea programada funciona
        SIN MENU Y SIN INDICADOR".

        EL OBJETIVO DEL PROYECTO ENTERO ES ESTE ARCHIVO. La seccion 1 lo dice:
        "el objetivo no es tener copias, es que las copias ocurran SIN DEPENDER
        DE SU DISCIPLINA". Todo lo demas existe para que esta tarea pueda correr
        sin que nadie mire.

        TRES DECISIONES QUE VALE LA PENA JUSTIFICAR:

        1. SIN PRIVILEGIOS DE ADMINISTRADOR. La tarea corre como el usuario, en
           su sesion, con sus permisos. El motor solo necesita leer sus carpetas
           y escribir en un recurso SMB: pedir administrador para eso seria
           regalar privilegio a cambio de nada, y el respaldo automatico YA es un
           privilegio (seccion 7, "el problema de fondo").

        2. `-WindowStyle Hidden` y `-NonInteractive`. Si la tarea abriera una
           ventana cada dia, en dos semanas estaria deshabilitada. Y
           `-NonInteractive` garantiza que nada del motor pueda quedarse
           esperando una respuesta que nadie va a dar de madrugada.

        3. NO SE PASA `-AutorizarFreno`, Y ES EL PUNTO MAS IMPORTANTE DE ESTE
           ARCHIVO. Si el freno salta en una corrida automatica, la corrida se
           detiene y espera a una persona. Una tarea que se autorizara a si misma
           convertiria la capa 2 en un adorno: seria exactamente el cifrado
           masivo replicandose sin que nadie lo pare.

    .PARAMETER Accion
        Registrar, Consultar, Deshabilitar, Habilitar o Retirar.

    .PARAMETER Hora
        Hora diaria de la corrida, formato HH:mm.

    .PARAMETER NombreTarea
        Como se llama en el Programador de tareas.

    .EXAMPLE
        .\Registrar-Tarea.ps1 -Accion Consultar
        .\Registrar-Tarea.ps1 -Accion Registrar -Hora 22:30
#>
[CmdletBinding(SupportsShouldProcess, ConfirmImpact = 'Medium')]
[OutputType([psobject])]
param(
    [ValidateSet('Registrar', 'Consultar', 'Deshabilitar', 'Habilitar', 'Retirar')]
    [string] $Accion = 'Consultar',

    [ValidatePattern('^([01]\d|2[0-3]):[0-5]\d$')]
    [string] $Hora = '22:30',

    [ValidateNotNullOrEmpty()]
    [string] $NombreTarea = 'NasRespaldo-Diario'
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$motor = Join-Path (Split-Path $PSScriptRoot -Parent) '2-Nucleo\respaldo.ps1'
if (-not (Test-Path -LiteralPath $motor -PathType Leaf)) {
    throw "No se encuentra el motor en $motor"
}

function Get-TareaDeRespaldo {
    [CmdletBinding()]
    [OutputType([psobject])]
    param([Parameter(Mandatory)][string] $Nombre)
    return (Get-ScheduledTask -TaskName $Nombre -ErrorAction SilentlyContinue)
}

switch ($Accion) {

    'Registrar' {
        $argumentos = @(
            '-NoProfile'
            '-NonInteractive'
            '-ExecutionPolicy', 'Bypass'
            '-WindowStyle', 'Hidden'
            '-File', ('"{0}"' -f $motor)
            '-Confirm:$false'
        ) -join ' '

        $accionTarea = New-ScheduledTaskAction -Execute 'powershell.exe' -Argument $argumentos
        $disparador  = New-ScheduledTaskTrigger -Daily -At $Hora
        $principal   = New-ScheduledTaskPrincipal -UserId ('{0}\{1}' -f $env:USERDOMAIN, $env:USERNAME) -LogonType Interactive -RunLevel Limited
        $ajustes     = New-ScheduledTaskSettingsSet `
            -AllowStartIfOnBatteries `
            -DontStopIfGoingOnBatteries `
            -StartWhenAvailable `
            -MultipleInstances IgnoreNew `
            -ExecutionTimeLimit (New-TimeSpan -Hours 4)

        # -StartWhenAvailable importa: el equipo esta APAGADO MEDIA JORNADA
        # (ADR-0079). Sin eso, una corrida que caiga con el equipo apagado
        # simplemente no ocurre y nadie se entera hasta que el testigo lo diga.
        # -MultipleInstances IgnoreNew evita dos motores a la vez sobre el mismo
        # destino, que con clase B seria una carrera con el espejo.

        if ($PSCmdlet.ShouldProcess($NombreTarea, "Registrar tarea diaria a las $Hora")) {
            Register-ScheduledTask -TaskName $NombreTarea `
                -Action $accionTarea -Trigger $disparador -Principal $principal -Settings $ajustes `
                -Description 'Cliente de respaldo del NAS. Corre sin menu y sin indicador (30_CLIENTE_RESPALDO seccion 15, criterio 8). NO se autoriza el freno: si salta, espera a una persona.' `
                -Force | Out-Null
            Write-Information "Tarea '$NombreTarea' registrada, diaria a las $Hora." -InformationAction Continue
        }
        return (Get-TareaDeRespaldo -Nombre $NombreTarea)
    }

    'Deshabilitar' {
        if ($PSCmdlet.ShouldProcess($NombreTarea, 'Deshabilitar')) {
            Disable-ScheduledTask -TaskName $NombreTarea -ErrorAction Stop | Out-Null
        }
        return (Get-TareaDeRespaldo -Nombre $NombreTarea)
    }

    'Habilitar' {
        if ($PSCmdlet.ShouldProcess($NombreTarea, 'Habilitar')) {
            Enable-ScheduledTask -TaskName $NombreTarea -ErrorAction Stop | Out-Null
        }
        return (Get-TareaDeRespaldo -Nombre $NombreTarea)
    }

    'Retirar' {
        if ($PSCmdlet.ShouldProcess($NombreTarea, 'Retirar la tarea programada')) {
            Unregister-ScheduledTask -TaskName $NombreTarea -Confirm:$false -ErrorAction Stop
            Write-Warning "Tarea '$NombreTarea' retirada. El respaldo YA NO CORRE SOLO."
        }
        return $null
    }

    default {
        $t = Get-TareaDeRespaldo -Nombre $NombreTarea
        if (-not $t) {
            Write-Warning "La tarea '$NombreTarea' NO existe. El respaldo no corre solo."
            return $null
        }
        return [pscustomobject]@{
            Nombre    = $t.TaskName
            Estado    = $t.State
            Info      = ($t | Get-ScheduledTaskInfo)
            Accion    = ($t.Actions | Select-Object -First 1).Execute
            Argumento = ($t.Actions | Select-Object -First 1).Arguments
        }
    }
}
