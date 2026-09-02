# ---------------------------------------------------------------------------
#  respaldo.ps1  -  EL MOTOR. Es el archivo que seccion 7 nombra por su nombre.
#
#  Contrato: 30_CLIENTE_RESPALDO.md seccion 4 (las tres clases), seccion 7 (capas 2 y 3),
#            seccion 6.2 (las dos pasadas al disco frio), seccion 8 (la deuda).
#
#  ORDEN OBLIGATORIO DE UNA CORRIDA. Las tres primeras pueden abortar:
#
#    0. SALDAR LA DEUDA DE seccion 8, y solo la primera vez.
#       El arbol del nodo lo poblo una persona el 2026-09-02, no el motor.
#       Documents y dev son CLASE B: la primera corrida BORRA lo que no
#       coincida con la configuracion. Resolver contenedores + raicesDeclaradas
#       y comprobar que dan las SIETE raices de deudaPrimeraCorrida, ni una mas
#       ni una menos. Si no cuadra: abortar sin escribir un byte.
#
#    1. CENTINELAS (capa 3). Revisar al arrancar. Si uno no cuadra, abortar
#       antes de escribir nada.
#
#    2. FRENO (capa 2). Corrida en seco con /L y contar cuantos archivos
#       cambiarian o se borrarian. Si rebasa el umbral: NO COPIA NADA, avisa y
#       espera autorizacion explicita.
#
#    3. Copiar, invocando robocopy. NUNCA reimplementar la copia (seccion 16.bis).
#
#  CODIGOS DE SALIDA DE ROBOCOPY - la trampa que ya mordio dos veces:
#  devuelve un MAPA DE BITS, no exito/fracaso. 0-7 es CORRECTO, >= 8 es error.
#  El 2026-09-01 una herramienta dio por fallida una copia buena porque
#  robocopy devolvio 1, que significa "se copiaron archivos correctamente"
#  (pendiente 22). Tratar "distinto de cero" como fallo produce una alerta
#  falsa diaria hasta que se dejan de leer.
#
#  BANDERAS QUE NO SON OPCIONALES (seccion 4):
#    Clase A - preservar:   /E /XO /XJ /DCOPY:DAT /FFT /MT:8 /R:2 /W:5 /NP
#    Clase B - espejo:     /MIR /XJ /DCOPY:DAT /FFT /MT:8 /R:2 /W:5 /NP
#    /XJ  Documents contiene junctions a Music, Pictures y Videos. SIN /XJ,
#         robocopy las sigue y arrastra los 60 GB del drone por la puerta de
#         atras, dentro de Documents. Por poco no se pone el 2026-09-02.
#    /FFT Tolerancia de 2 s entre la marca de Windows y la de Linux. Sin esto
#         el motor cree que todo cambio en cada corrida.
#
#  ANTE UN ARCHIVO QUE NO SE COPIA: SONDEAR ANTES DE INSISTIR (seccion 6.1).
#  Leer 4 KB cada 32 MB costo 2 minutos y localizo una zona muerta; leer en
#  linea recta gasto 26 minutos para avanzar 1 MB contra un muro, sin saber
#  que detras habia 1.85 GiB legibles.
#
#  Fase 0: esqueleto. Aqui no hay implementacion todavia.
# ---------------------------------------------------------------------------

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

# Pendiente de Fase 2.
