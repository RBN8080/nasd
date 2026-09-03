# 30_CLIENTE_RESPALDO — el esqueleto

| Campo | Valor |
|---|---|
| **Qué es** | El código del cliente de respaldo de Windows hacia el nodo |
| **Contrato** | `../30_CLIENTE_RESPALDO.md` v1.18.0. **Ahí está todo el diseño**; aquí solo vive la implementación |
| **Estado** | **Esqueleto de la Fase 0, creado el 2026-09-02.** Cero líneas de lógica. Ningún archivo de aquí hace nada todavía |
| **Lenguaje** | PowerShell (Windows) y bash (nodo) — §16.bis |

> **Este directorio no se diseña, se implementa.** El contrato se cerró tras
> cinco iteraciones con el responsable y cada decisión de §3 a §10 ya fue
> discutida y descartada su alternativa. Si algo de aquí parece que le falta una
> decisión, la decisión está en el contrato — búscala antes de inventarla.

---

## Qué hay, y qué gobierna a cada cosa

```
30_CLIENTE_RESPALDO/
│
├── 1-Interfaz/           dos capas de lectura, y NINGUNA es el único camino:
│   │                     el motor tiene que correr sin menú y sin icono desde
│   │                     la tarea programada, o nada se podría automatizar
│   ├── estado.cmd        §9    la ventana de estado
│   ├── tablero.ps1       §10.1 el menú de opciones
│   └── indicador.ps1     §10.2 el icono de la barra
│
├── 2-Nucleo/
│   ├── respaldo.ps1      §4 §7 §8   EL MOTOR, freno y centinelas
│   ├── clasificar.ps1    §3         qué se respalda y cómo
│   ├── verificar.ps1     §9         los tres niveles de certeza
│   ├── semilla.ps1       §5         la lista, no los programas
│   ├── notificar.ps1     §11        la costura con el nodo
│   ├── testigo.ps1       §13.3      mismo sistema que ADR-0074, otro reloj
│   └── comun.ps1         §16.bis    atómico, registro, configuración
│
└── 3-Config/
    └── respaldo.jsonc    §3.5 §6    LA TABLA DEL CONTRATO, legible por máquina
```

**§15 criterio 12: clonar esto en otra máquina debe funcionar cambiando SOLO
`3-Config/`.** Nada específico de EQUIPO-01 vive fuera de ahí.

**§15 criterio 13: ninguna credencial en este repositorio.** La conexión al nodo
va por el Administrador de credenciales de Windows; la URL del testigo también.

---

## Las tres cosas que hay que saber antes de escribir una línea

**1. La deuda de §8 se paga antes de la primera corrida, no después.** El árbol
del nodo lo pobló una persona el 2026-09-02, no el motor. `Documents` y `dev`
son **clase B, o sea espejo**: la primera corrida real compara lo que hay allí
contra `respaldo.jsonc` y **borra lo que no coincida**. El bloque
`deudaPrimeraCorrida` de la configuración lleva las siete raíces con sus cifras
exactas; el motor las comprueba y aborta si no dan.

**2. Se prueba con basura antes que en vivo.** Los criterios 2, 3, 5 y 6 de §15
—borrar en clase A, borrar en clase B, tocar un centinela, simular un cambio
masivo— **hay que romperlos a propósito**, y eso no se hace sobre archivos
vivos. Primero carpetas de prueba con archivos de mentira; después en vivo.

**3. Los `.ps1` van sin acentos, y no es descuido.** El repositorio va en LF y
sin BOM (`.gitattributes`), y **PowerShell 5.1 lee un `.ps1` sin BOM como ANSI**:
los acentos salen rotos. Es la misma razón por la que `.gitmessage` está escrito
sin acentos. Los acentos viven en los `.md`, y en los comentarios del `.jsonc`
que siempre se lee con `-Encoding UTF8` explícito.

Los `.cmd` son el caso contrario y llevan su excepción en `.gitattributes`:
`cmd.exe` con finales LF rompe en `goto` y en los bloques de varias líneas.

---

## Trampas ya pagadas — no volver a pisarlas

Salen de §12.septies y §12.quinquies, y cada una costó tiempo real.

| Trampa | La lección |
|---|---|
| Una ruta UNC perdió una barra y `\\192.168.1.38\…` quedó `\192.168.1.38\…`, que Windows resuelve **relativo a `C:`**. Los 27 508 archivos se copiaron a `C:\192.168.1.38\` y **al nodo no llegó ni uno** | **Abortar si el destino no es UNC y el nodo no responde.** Y una velocidad que no cuadra con lo medido es un fallo hasta que se demuestre lo contrario: 560 MB/s sobre un enlace de 13.4 fue la primera señal |
| Sin `/XJ`, robocopy sigue las junctions de `Documents` a `Music`, `Pictures` y `Videos`, y arrastra los 60 GB del drone por la puerta de atrás | `/XJ` **no es opcional.** Por poco no se pone |
| Robocopy devolvió **código 1** y una herramienta dio la copia por fallida. El 1 significa *«se copiaron archivos correctamente»* | Es un **mapa de bits**: **0–7 correcto, ≥ 8 error** |
| Un cotejo de huellas dio «27 508 diferencias» que eran cero: PowerShell escribe MD5 en mayúsculas, `md5sum` en minúsculas | **Normalizar los dos lados antes de comparar.** Un cotejo 100 % distinto casi nunca es un desastre: casi siempre es formato |
| Un proceso `md5sum` por archivo proyectaba **49 min** en el Pi; por lotes de 500 fueron minutos | Sobre un Pi **domina el coste de arrancar procesos** cuando los archivos son pequeños |
| Se leyó en línea recta contra un sector muerto: **26 minutos para avanzar 1 MB**, sin saber que detrás había 1.85 GiB legibles | Ante un archivo que no se copia, **sondear antes de insistir** |
