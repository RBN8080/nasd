# Mantenimiento ligero

Referencia revisada: consola v1.134.0, 2026-11-07. Esta página contiene el
flujo vigente y las propuestas; los commits conservan la historia.

## Un cambio, una comprobación, una constancia

1. Describir el problema observable y cómo se reconocerá la solución.
2. Cambiar una cosa revisable. Corregir primero un fallo reproducido; refactorizar
   cuando reduzca un coste concreto, con la conducta cubierta por pruebas.
3. Ejecutar las comprobaciones que correspondan al riesgo. Para código del
   servicio: `cd 10_CODIGO && make verificar`; para concurrencia, además `-race`
   en Linux/WSL. Para interfaz: escritorio, teléfono, teclado, estado vacío,
   error y desconexión; comprobar permisos en el servidor.
4. Actualizar únicamente la explicación afectada y las imágenes si cambia la
   interfaz. Dejar en el commit/PR: problema, cambio, comprobación y límite.
5. Publicar la copia filtrada y verificar el SHA remoto. Un commit local, una
   captura bonita o un ensayo en seco no significan publicación ni despliegue.

Quien cambia una función mantiene su documentación en el mismo cambio.
No se exige un documento adicional por tarea ni repetir el relato en varios
archivos. No se incrementa la versión del servicio por mantenimiento documental.

## Cuándo sí usar ADR

Solo ante un hito que cambie arquitectura, persistencia/durabilidad, límites de
confianza, contratos públicos o una dependencia difícil de retirar. Basta una
página: contexto, decisión, alternativa importante y consecuencias.
Una corrección, un ajuste visual o una limpieza se explican en el commit/PR.
Si contradice un ADR anterior, identificarlo y señalar qué queda sustituido;
el historial se conserva, pero no obliga a crear otro ADR para cada ajuste.

## Prioridades propuestas

| Orden | Riesgo o coste | Próximo resultado comprobable |
|---|---|---|
| 1 | Documentación visual separada del producto | Capturas de las plantillas reales con datos ficticios, escritorio y móvil; regeneradas en este mantenimiento |
| 2 | Publicación incompleta o sin revisar documentos | Filtro también sobre documentos finales; reintento de push aunque no haya commits nuevos; cambios incluidos en este mantenimiento |
| 3 | Regresión del flujo vivo tras cambios de UX | Automatizar un escenario corto: desconexión/reconexión SSE, desplazamiento móvil y foco de teclado. Cerrar cuando no muestre conexión falsa ni pierda foco o posición |
| 4 | Respaldo aparentemente correcto pero irrecuperable | Restaurar una muestra en un destino aislado y comparar contenido; registrar fecha y resultado, sin tocar originales |
| 5 | Verificación solo local | Proponer CI sobre la copia pública para Go/Linux y corpus C; activarlo cuando el flujo filtrado pase allí. Nunca incluir secretos del nodo |

Los puntos 3–5 son propuestas pendientes, no defectos demostrados ni funciones
ya construidas. Priorizar pérdida de datos y control de acceso sobre cosmética.
El pendiente visual de la nota de Seguridad sigue separado de este mantenimiento.

## Qué acreditan las comprobaciones

Las capturas de [la interfaz](INTERFAZ.md) verifican presentación estática con
datos ficticios. No prueban Safari real, SSE, restauración, latencia del nodo ni
la instalación desde cero. `make verificar` cubre código y análisis local;
no acredita por sí solo el estado de producción. Las mediciones operativas
deben llevar fecha y no copiarse como un estado permanentemente vigente.

## Fundamento

Estas reglas son una adaptación al tamaño del proyecto, no una certificación.

- [DORA: calidad documental](https://dora.dev/capabilities/documentation-quality/):
  priorizar claridad, localización y fiabilidad; la cantidad de páginas no es
  el objetivo.
- [DORA: lotes pequeños](https://dora.dev/capabilities/working-in-small-batches/):
  cambios acotados para obtener feedback pronto y reducir trabajo acumulado.
- [Software Engineering at Google, capítulo 10](https://abseil.io/resources/swe-book/html/ch10.html):
  documentación junto al código, responsables claros y una fuente principal
  para cada explicación.
- [Bacchelli y Bird, ICSE 2013, Microsoft Research](https://www.microsoft.com/en-us/research/publication/expectations-outcomes-and-challenges-of-modern-code-review/):
  estudio empírico de revisión moderna; además de detectar fallos, la revisión
  ayuda a comprender y compartir conocimiento sobre el cambio.

Consultadas el 2026-11-07. Las recomendaciones no garantizan ausencia de fallos;
dan un criterio para decidir qué verificar y qué documentación mantener.
