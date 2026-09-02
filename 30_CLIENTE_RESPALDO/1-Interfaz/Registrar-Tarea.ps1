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

        2. SE ARRANCA POR conhost --headless, NO POR powershell.exe A SECAS, y
           esto es lo que de verdad quita la ventana negra. Si la tarea abriera
           una ventana cada dia, en dos semanas estaria deshabilitada.

           `-WindowStyle Hidden` NO BASTA EN WINDOWS 11, y se midio el
           2026-09-02 con la tarea ya registrada: la ventana aparecia igual.
           El motivo es que Windows 11 delega la consola en WINDOWS TERMINAL,
           y Windows Terminal abre SU PROPIA ventana; `-WindowStyle` solo habla
           con la consola clasica, que ahi ni existe. Esconderla despues desde
           el propio proceso tampoco sirve: GetConsoleWindow devuelve la
           pseudo-consola, no la ventana que se ve.

           `conhost.exe --headless` pide el host clasico de forma explicita y
           SIN VENTANA, saltandose la delegacion. Es parte de Windows, asi que
           no anade dependencia ninguna (ADR-0013).

           `-NonInteractive` se mantiene en el motor: garantiza que nada pueda
           quedarse esperando una respuesta que nadie va a dar de madrugada.

        3. NO SE PASA `-AutorizarFreno`, Y ES EL PUNTO MAS IMPORTANTE DE ESTE
           ARCHIVO. Si el freno salta en una corrida automatica, la corrida se
           detiene y espera a una persona. Una tarea que se autorizara a si misma
           convertiria la capa 2 en un adorno: seria exactamente el cifrado
           masivo replicandose sin que nadie lo pare.

        4. EL INDICADOR TAMBIEN ES UNA TAREA, Y HASTA HOY NO LO ERA. La seccion
           10.2 declara el punto ciego -"si el indicador muere no hay icono, y
           un icono ausente se parece a todo bien"- y dice como se mitiga:
           RELANZANDOLO DESDE LA TAREA. Esa tarea NO EXISTIA. El icono se
           construyo en la Fase 3 y nunca arrancaba solo, asi que el punto ciego
           estaba abierto de par en par: bastaba reiniciar el equipo para
           quedarse sin icono. Medido el 2026-09-02, cuando un apagon dejo el
           equipo sin indicador y nadie se habria enterado.

           Por eso son DOS tareas y no una: el motor corre A UNA HORA y termina;
           el indicador arranca AL ENTRAR A LA SESION y no termina nunca. Un
           disparador diario para el icono no tendria sentido, y un limite de
           tiempo de ejecucion lo mataria a media jornada.

    .PARAMETER Pieza
        Motor (la corrida diaria) o Indicador (el icono de la barra).

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
    [ValidateSet('Motor', 'Indicador')]
    [string] $Pieza = 'Motor',

    [ValidateSet('Registrar', 'Consultar', 'Deshabilitar', 'Habilitar', 'Retirar')]
    [string] $Accion = 'Consultar',

    [ValidatePattern('^([01]\d|2[0-3]):[0-5]\d$')]
    [string] $Hora = '22:30',

    [ValidateNotNullOrEmpty()]
    [string] $NombreTarea
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

# EL NOMBRE NO SE PONE POR OMISION EN EL BLOQUE param: un valor por defecto no
# puede depender de otro parametro, y un nombre equivocado aqui registraria el
# icono ENCIMA del motor -Register-ScheduledTask va con -Force- y dejaria al
# equipo sin respaldo automatico sin decir nada.
if (-not $PSBoundParameters.ContainsKey('NombreTarea')) {
    $NombreTarea = if ($Pieza -eq 'Indicador') { 'NasRespaldo-Indicador' } else { 'NasRespaldo-Diario' }
}

$guion = if ($Pieza -eq 'Indicador') {
    Join-Path $PSScriptRoot 'indicador.ps1'
}
else {
    Join-Path (Split-Path $PSScriptRoot -Parent) '2-Nucleo\respaldo.ps1'
}
if (-not (Test-Path -LiteralPath $guion -PathType Leaf)) {
    throw "No se encuentra la pieza '$Pieza' en $guion"
}

function Get-TareaDeRespaldo {
    [CmdletBinding()]
    [OutputType([psobject])]
    param([Parameter(Mandatory)][string] $Nombre)
    return (Get-ScheduledTask -TaskName $Nombre -ErrorAction SilentlyContinue)
}

switch ($Accion) {

    'Registrar' {
        $usuario   = '{0}\{1}' -f $env:USERDOMAIN, $env:USERNAME
        $principal = New-ScheduledTaskPrincipal -UserId $usuario -LogonType Interactive -RunLevel Limited

        if ($Pieza -eq 'Indicador') {
            # NI -NonInteractive NI -Confirm: el indicador es una ventana de
            # mensajes de WinForms, no un proceso por lotes, y no tiene
            # ShouldProcess que confirmar.
            $argumentos = @(
                '--headless'
                'powershell.exe'
                '-NoProfile'
                '-ExecutionPolicy', 'Bypass'
                '-WindowStyle', 'Hidden'
                '-File', ('"{0}"' -f $guion)
            ) -join ' '

            $disparador = New-ScheduledTaskTrigger -AtLogOn -User $usuario
            # Medio minuto de margen: arrancar peleandose con el explorador por
            # la barra de tareas es la forma barata de quedarse sin icono.
            $disparador.Delay = 'PT30S'

            # SIN LIMITE DE TIEMPO, y es lo contrario que el motor. El motor
            # arranca, trabaja y termina, asi que un techo de 4 h lo protege de
            # colgarse. El indicador NO TERMINA NUNCA: cualquier limite lo
            # mataria a media jornada y dejaria al equipo sin icono, que es
            # justo el punto ciego declarado en la seccion 10.2.
            $ajustes = New-ScheduledTaskSettingsSet `
                -AllowStartIfOnBatteries `
                -DontStopIfGoingOnBatteries `
                -MultipleInstances IgnoreNew `
                -RestartCount 3 `
                -RestartInterval (New-TimeSpan -Minutes 1) `
                -ExecutionTimeLimit ([TimeSpan]::Zero)

            $descripcion = 'Indicador del respaldo del NAS. SOLO LEE ESTADO.txt Y PINTA: no ejecuta el respaldo, y cerrarlo no detiene nada (30_CLIENTE_RESPALDO seccion 10.2). Se relanza al entrar a la sesion porque un icono ausente se parece a "todo bien".'
            $queHace     = 'Registrar el indicador al iniciar sesion'
            $confirmado  = "Tarea '$NombreTarea' registrada: el icono arranca al entrar a la sesion."
        }
        else {
            $argumentos = @(
                '--headless'
                'powershell.exe'
                '-NoProfile'
                '-NonInteractive'
                '-ExecutionPolicy', 'Bypass'
                '-WindowStyle', 'Hidden'
                '-File', ('"{0}"' -f $guion)
                '-Confirm:$false'
            ) -join ' '

            $disparador = New-ScheduledTaskTrigger -Daily -At $Hora

            # -StartWhenAvailable importa: el equipo esta APAGADO MEDIA JORNADA
            # (ADR-0079). Sin eso, una corrida que caiga con el equipo apagado
            # simplemente no ocurre y nadie se entera hasta que el testigo lo
            # diga. -MultipleInstances IgnoreNew evita dos motores a la vez
            # sobre el mismo destino, que con clase B seria una carrera con el
            # espejo.
            $ajustes = New-ScheduledTaskSettingsSet `
                -AllowStartIfOnBatteries `
                -DontStopIfGoingOnBatteries `
                -StartWhenAvailable `
                -MultipleInstances IgnoreNew `
                -ExecutionTimeLimit (New-TimeSpan -Hours 4)

            $descripcion = 'Cliente de respaldo del NAS. Corre sin menu y sin indicador (30_CLIENTE_RESPALDO seccion 15, criterio 8). NO se autoriza el freno: si salta, espera a una persona.'
            $queHace     = "Registrar tarea diaria a las $Hora"
            $confirmado  = "Tarea '$NombreTarea' registrada, diaria a las $Hora."
        }

        # Ruta absoluta y no 'conhost.exe' a secas: el Programador de tareas no
        # resuelve el PATH del usuario de la misma forma que una sesion.
        $anfitrion = Join-Path $env:SystemRoot 'System32\conhost.exe'
        if (-not (Test-Path -LiteralPath $anfitrion -PathType Leaf)) {
            throw "No se encuentra $anfitrion. Sin el, la tarea abriria una ventana en cada corrida."
        }
        $accionTarea = New-ScheduledTaskAction -Execute $anfitrion -Argument $argumentos

        # SE MATA EL ICONO VIEJO ANTES DE REGISTRAR, Y HAY QUE HACERLO AQUI.
        # Register-ScheduledTask -Force reemplaza la tarea pero NO se lleva por
        # delante la instancia que ya estaba corriendo: queda huerfana. Y como
        # el indicador tiene cerrojo de instancia unica, la nueva arranca, ve el
        # cerrojo tomado por el huerfano y se retira educadamente. Resultado: la
        # tarea dice "Ready", todo parece bien, y en la barra sigue el icono con
        # el codigo VIEJO. Medido el 2026-09-02 tras actualizar el indicador.
        # SOLO SI YA HABIA UNA TAREA CON ESE NOMBRE, o sea cuando de verdad se
        # esta REEMPLAZANDO una instalacion viva. Registrar una tarea nueva no
        # tiene ningun indicador anterior que retirar, y matar procesos "por si
        # acaso" tuvo consecuencias: la suite registra una tarea de usar y tirar
        # para comprobar el arranque sin ventana, y con la version anterior de
        # esta guarda CORRER LAS PRUEBAS MATABA EL ICONO DEL ESCRITORIO.
        $yaExistia = $null -ne (Get-TareaDeRespaldo -Nombre $NombreTarea)
        if ($Pieza -eq 'Indicador' -and $yaExistia) {
            Stop-ScheduledTask -TaskName $NombreTarea -ErrorAction SilentlyContinue
            # El patron se arma por trozos a proposito: si el literal apareciera
            # entero, esta misma consulta se encontraria a si misma en su linea
            # de comando y contaria un proceso de mas.
            $patron = '*indi' + 'cador.ps1*'
            foreach ($viejo in @(Get-CimInstance Win32_Process -Filter "Name='powershell.exe'" -ErrorAction SilentlyContinue |
                                 Where-Object { $_.CommandLine -like $patron -and $_.ProcessId -ne $PID })) {
                Write-Information ("Se retira un indicador anterior (pid {0})." -f $viejo.ProcessId) -InformationAction Continue
                Stop-Process -Id $viejo.ProcessId -Force -ErrorAction SilentlyContinue
            }
        }

        if ($PSCmdlet.ShouldProcess($NombreTarea, $queHace)) {
            Register-ScheduledTask -TaskName $NombreTarea `
                -Action $accionTarea -Trigger $disparador -Principal $principal -Settings $ajustes `
                -Description $descripcion `
                -Force | Out-Null
            Write-Information $confirmado -InformationAction Continue
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
            $aviso = if ($Pieza -eq 'Indicador') {
                "Tarea '$NombreTarea' retirada. El icono ya NO arranca solo, y un icono ausente se parece a todo bien."
            }
            else {
                "Tarea '$NombreTarea' retirada. El respaldo YA NO CORRE SOLO."
            }
            Write-Warning $aviso
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
