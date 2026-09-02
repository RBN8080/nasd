# ---------------------------------------------------------------------------
#  comun.ps1  -  Lo que usan todos los demas. Sin logica de respaldo.
#
#  Contrato: 30_CLIENTE_RESPALDO.md seccion 16.bis "Que se reutiliza - paradigmas".
#
#  Paradigmas que hereda del nodo, no lineas de codigo:
#    - Cero dependencias externas (ADR-0013, ADR-0017). Solo PowerShell y el
#      .NET que Windows ya trae. NADA de modulos de galeria.
#    - Escritura atomica (internal/atomico): a temporal y renombrar. Nunca a
#      medias, porque el indicador esta leyendo al mismo tiempo.
#    - Registro estructurado: una linea por suceso.
#    - "Verificado, no supuesto": las cifras se miden y se fechan.
#
#  CONVENCION DE ESTE DIRECTORIO, y no es cosmetica:
#  los .ps1 se escriben SIN ACENTOS. El repositorio va en LF y sin BOM
#  (.gitattributes: * text=auto eol=lf), y PowerShell 5.1 lee un .ps1 sin BOM
#  como ANSI: los acentos salen rotos. Es la misma razon por la que .gitmessage
#  esta escrito sin acentos. Los acentos viven en los .md y en los comentarios
#  del .jsonc, que se leen con -Encoding UTF8 explicito.
#
#  Fase 0: esqueleto. Aqui no hay implementacion todavia.
# ---------------------------------------------------------------------------

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

# Pendiente de Fase 2:
#   Leer-Config          lee 3-Config/respaldo.jsonc con -Encoding UTF8
#   Escribir-Atomico     escribe a .tmp y renombra
#   Registrar            una linea por suceso, con marca de tiempo
#   Resolver-Destino     y ABORTA si el destino del nodo no empieza por \\
