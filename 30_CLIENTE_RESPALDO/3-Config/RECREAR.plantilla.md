# RECREAR — lo que no se copia, y cómo se rehace

<!-- Plantilla. `semilla.ps1` la lee con -Encoding UTF8, sustituye los marcadores
     {{FECHA}} y {{EQUIPO}}, y escribe el resultado dentro de la semilla.

     VIVE AQUÍ Y NO DENTRO DEL .ps1 A PROPÓSITO: los guiones de este cliente se
     escriben sin acentos, porque PowerShell 5.1 lee un .ps1 sin BOM como ANSI
     y los rompe (ADR-0078). Un documento que se lee el peor día del año no
     puede estar escrito sin acentos por una limitación del intérprete. -->

Generado por el cliente de respaldo el **{{FECHA}}** en **{{EQUIPO}}**.
Contrato: `30_CLIENTE_RESPALDO.md` §3.2, §5 y §8.

## El criterio, en una frase

> **¿La fuente puede reconstruir esto sin mí?**
> **Sí** → no se respalda nada. **No** → va en la semilla.

Lo que sigue es la lista de lo que la fuente **sí** puede reponer, con la fuente
al lado. No se copia a propósito: copiarlo serían cientos de GB para ahorrar
minutos.

## Orden de una restauración

1. **Windows y los controladores propios del equipo.**
2. **`RESTAURAR.ps1`**, que está en esta misma carpeta. Deja programas, editor e
   identidad de git. Se cronometra solo.
3. **Los datos.** No los trae ningún guion: se copian.
   - Del nodo: `\\192.168.1.38\datos\01_BACKUP\EQUIPO-01\`
   - Del disco frío: la unidad con etiqueta `COPIA-FRIA`, bajo `EQUIPO-01\`
   - Las rutas debajo del nombre de máquina son **idénticas** a las del equipo,
     así que se sabe de dónde vino cada archivo sin leer documentación.
4. **Las llaves SSH**, que viajan aparte y cifradas (`ADR-0080`).
5. **Las tareas programadas**, a mano, desde `tareas-programadas.csv`.

## Clase C — lo que no se respalda, y qué lo repone

| Qué | La fuente que lo repone |
|---|---|
| `AppData` | Cada programa lo regenera al arrancar |
| `.cache` | Es caché: se rehace sola |
| `Downloads` | Zona de paso. Lo que importaba ya se movió a su carpeta numerada |
| `Juegos` | La biblioteca se descarga de nuevo |
| `*.vmdk` | Los discos virtuales. La semilla guarda las **rutas de los `.vmx`**, que es la configuración; los discos se rehacen desde la plantilla |
| `D:\LAB\tools` | Herramientas descargables del laboratorio |

## Máquinas virtuales

El aprovisionamiento automático está **fuera de alcance** por decisión escrita
(§16): es construir máquinas desde plantilla, no respaldar archivos. El atajo
que da la mayor parte del beneficio sin escribir nada: una instantánea de la
máquina recién configurada, más una plantilla lista en `D:`. La semilla guarda
dónde estaba cada `.vmx`.

## Lo que este archivo no puede prometer

Que la lista esté completa. Se genera de la configuración, así que describe lo
que el motor **sabe** que excluye. Si algo importante vive fuera de toda raíz
declarada, aparece como **huérfano** en el inventario — y esa es exactamente la
razón por la que los huérfanos se reportan en vez de ignorarse en silencio.
