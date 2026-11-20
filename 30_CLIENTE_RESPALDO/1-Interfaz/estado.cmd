@echo off
rem ---------------------------------------------------------------------------
rem  estado.cmd  -  Lanzador del tablero de estado (30_CLIENTE_RESPALDO.md seccion 9).
rem
rem  Abre tablero.ps1 en PowerShell 7 si esta instalado (panel Spectre de
rem  ADR-0097) y cae limpiamente a Windows PowerShell 5.1 si no lo esta.
rem ---------------------------------------------------------------------------
setlocal
set "TABLERO=%~dp0tablero.ps1"

where pwsh.exe >nul 2>nul
if %errorlevel%==0 (
    pwsh.exe -NoLogo -ExecutionPolicy Bypass -File "%TABLERO%" %*
    exit /b %errorlevel%
)

powershell.exe -NoLogo -ExecutionPolicy Bypass -File "%TABLERO%" %*
exit /b %errorlevel%
