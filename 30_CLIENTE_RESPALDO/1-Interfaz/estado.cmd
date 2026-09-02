@echo off
rem ---------------------------------------------------------------------------
rem  estado.cmd  -  La ventana de estado. §9 lo nombra por su nombre.
rem
rem  Contrato: 30_CLIENTE_RESPALDO.md §9.
rem
rem  NO LEE UN REGISTRO VIEJO: hace la comparacion real contra los dos destinos
rem  EN ESE MOMENTO, y por eso puede afirmar lo que afirma.
rem
rem  Cubre en la misma vista:
rem    - nodo y disco externo
rem    - huerfanos (§3.3 punto 4)
rem    - antiguedad de la bandeja de transito (Downloads, §3.6)
rem    - estado de centinelas
rem    - tasa de cambio de la ultima corrida
rem    - dias desde la ultima conexion del disco frio (§6)
rem    - FECHA DE LA ULTIMA PRUEBA DE RESTAURACION
rem
rem  Esa ultima linea esta ahi PARA INCOMODAR CUANDO ENVEJEZCA. Los respaldos
rem  no valen; las restauraciones si (Google SRE Book, cap. 26).
rem
rem  Este .cmd es solo el lanzador sin ventana de PowerShell. La logica vive en
rem  2-Nucleo/verificar.ps1.
rem
rem  Fase 0: esqueleto. Aqui no hay implementacion todavia.
rem ---------------------------------------------------------------------------

echo Fase 0: el motor no existe todavia. Ver 30_CLIENTE_RESPALDO.md seccion 12.
exit /b 0
