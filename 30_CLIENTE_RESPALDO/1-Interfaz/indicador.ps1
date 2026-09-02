# ---------------------------------------------------------------------------
#  indicador.ps1  -  El icono de la barra de tareas.
#
#  Contrato: 30_CLIENTE_RESPALDO.md seccion 10.2.
#
#  SOLO LEE ESTADO.txt Y PINTA. NO EJECUTA EL RESPALDO. Cerrarlo no detiene
#  nada. Y por eso ESTADO.txt se escribe de forma atomica (comun.ps1): este
#  proceso lo esta leyendo al mismo tiempo.
#
#  Cuadro oscuro con nucleo que cambia COLOR **Y** FORMA. La forma es
#  deliberada: el color solo no basta (WCAG 2.2 1.4.1), y este repositorio ya
#  tiene registrada esa misma preocupacion para el semaforo de la web.
#
#    Protegido            circulo verde      fijo
#    Copiando             circulo verde      pulso suave
#    Requiere atencion    triangulo ambar    FIJO
#    Falla                rombo rojo         PARPADEA
#    Sin datos            circulo gris       fijo
#
#  SOLO EL ROJO PARPADEA. Si el verde parpadeara cada vez que trabaja -y va a
#  trabajar a diario- en dos semanas se deja de registrar el parpadeo, y
#  entonces tampoco se registra el dia que sea rojo. El movimiento es un
#  recurso que se gasta (ISA-18.2, fatiga de alarmas).
#
#  DETECCION DE MOTOR CAIDO - unas lineas, y es la unica adicion al motor:
#    marca presente y reciente      copiando ahora            Copiando
#    MARCA PRESENTE PERO VIEJA      arranco y nunca termino   FALLA
#    sin marca, dentro de lo esperado  termino bien           Protegido
#    sin marca, ya paso la hora     no corrio                 Atencion
#    la tarea programada no existe o esta deshabilitada       FALLA
#
#  PUNTO CIEGO DECLARADO: si el indicador muere no hay icono, y un icono
#  ausente se parece a "todo bien". Se mitiga relanzandolo desde la tarea, pero
#  EL SILENCIO DEL ICONO NUNCA ES PRUEBA DE NADA. La prueba esta en el tablero.
#
#  El icono es NATIVO en el .NET que Windows ya trae: sin dependencias, sin
#  tocar ADR-0013. Ese era el unico punto que podia presionar una decision ya
#  tomada del repositorio, y en PowerShell desaparece (seccion 16.bis).
#
#  Fase 0: esqueleto. Aqui no hay implementacion todavia.
# ---------------------------------------------------------------------------

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

# Pendiente de Fase 3.
