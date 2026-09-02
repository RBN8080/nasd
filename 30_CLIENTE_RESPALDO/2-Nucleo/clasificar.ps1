# ---------------------------------------------------------------------------
#  clasificar.ps1  -  Decide QUE se respalda y COMO. Nada mas.
#
#  Contrato: 30_CLIENTE_RESPALDO.md seccion 3.
#
#  EL PRINCIPIO (seccion 3.1): una carpeta no tiene clase. La clase es propiedad del
#  LUGAR donde la carpeta vive. Clasificar por nombre produce una lista que se
#  pudre el dia que se deja de actualizar, y ese dia siempre llega.
#
#  DOS PREGUNTAS DISTINTAS, CADA UNA CON SU PROPIA RESPUESTA (seccion 3.5):
#    Se respalda?   -> EL NUMERO.      NN_ si; sin numero, no.
#    Como?          -> EL CONTENEDOR.  Documentos/Escritorio espejo (B);
#                                      Imagenes/Videos/Musica aditivo (A).
#  Son independientes. El motor no infiere ni adivina: mira DONDE VIVE la
#  carpeta y con eso ya sabe las dos cosas.
#
#  ORDEN DE RESOLUCION (seccion 3.3) - cuatro preguntas, se queda con la primera que
#  responda:
#    1. Esta bajo una raiz declarada?      Hereda su clase.
#    2. Tiene un marcador .CLASE-X dentro? Gana sobre la herencia. Viaja con la
#                                          carpeta si se mueve. Precedente:
#                                          CACHEDIR.TAG.
#    3. Coincide con un patron global de exclusion? Se descarta este donde este.
#    4. Nada respondio -> ES HUERFANO: SE REPORTA, NO SE IGNORA.
#
#  EL PUNTO 4 NO ES NEGOCIABLE. Un dato sin clasificar tiene que ser visible.
#  Si el motor "se salta lo que no reconoce" en silencio, se crea una carpeta
#  importante fuera de toda raiz y se descubre seis meses despues que nunca se
#  respaldo.
#
#  DOS TIPOS DE FILA EN LA TABLA, y confundirlos dejaba 141 GB fuera (seccion 3.5):
#    CONTENEDOR        -> quien entra lo decide EL NUMERO
#    RAIZ DECLARADA    -> quien entra lo decide LA TABLA
#  El guion bajo NUNCA quiso decir "no se respalda": _CONFIGS lleva guion bajo,
#  no lleva numero, y SI se respalda.
#
#  Y una carpeta sin numero dentro de un contenedor SE REPORTA como
#  "sin numerar - no se copia". Se ve, no se ignora. Lo que no se hace es
#  copiarla (seccion 3.4).
#
#  Fase 0: esqueleto. Aqui no hay implementacion todavia.
# ---------------------------------------------------------------------------

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

# Pendiente de Fase 2.
