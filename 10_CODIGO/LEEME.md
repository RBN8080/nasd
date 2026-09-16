# 10_CODIGO — el servicio y su compilación

`nasd` es un servicio web escrito en Go, **sin una sola dependencia externa**.
`go.mod` no lista ninguna y no es casualidad: es una restricción del proyecto, y
añadir una exige justificarla por escrito.

```
cmd/nasd/            el programa y su raíz de composición
internal/            todo lo demás: almacén, seguridad, web, config, geoip…
despliegue/          la unidad systemd y el ejemplo de configuración
nas-miniatura/       auxiliar en C: extrae la miniatura EXIF de un JPEG
nas-sensor/          auxiliar en C: mira paquetes en bruto
pruebas/             arneses de humo y de compilación cruzada
```

---

## Compilación

Lo único que hace falta es **Go 1.25 o posterior**:

```sh
go build ./cmd/nasd
```

Para el nodo, que es ARM de 64 bits, desde cualquier máquina:

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o nasd ./cmd/nasd
```

`CGO_ENABLED=0` es lo que hace que el binario no dependa de nada instalado en
el nodo: se copia y corre.

**No hace falta compilar a mano.** `20_APROVISIONAMIENTO/instalar.sh` lo hace
por usted si el nodo tiene Go, y si no, se para y le escribe este mismo comando
con su arquitectura ya puesta.

### Los dos auxiliares en C

Son opcionales y se compilan aparte, con `zig cc` como compilador cruzado:

```sh
make compilar-miniatura
make compilar-sensor
```

Sin `nas-miniatura`, la ruta de miniaturas devuelve 404 y el NAS funciona
igual. Sin `nas-sensor`, no hay capa de paquetes: es un servicio de systemd
separado que instala `19_sensor.sh`.

---

## Verificación

```sh
make verificar
```

Esa es **la puerta**, y pasa por gofmt, `go vet`, una comprobación propia del
proyecto, la batería de pruebas entera y análisis estático compilando para
`linux/arm64` —no para la máquina en la que usted está—, porque hay avisos que
solo existen en la plataforma de destino.

```sh
make carreras
```

El detector de carreras. Va aparte porque tarda bastante más, pero no es
opcional al cerrar un bloque de trabajo: dos de los peores defectos que ha
tenido este programa fueron carreras.

**En Windows las dos cosas corren dentro de WSL**, y no por gusto: Smart App
Control rechaza los binarios sin firmar que Go enlaza para cada paquete de
pruebas. El Makefile lo resuelve solo.

---

## Configuración

`despliegue/nasd.toml.ejemplo` es el archivo de configuración comentado entero.
En el nodo vive en `/etc/nasd/nasd.toml` y lo escribe
`20_APROVISIONAMIENTO/05_instalar_servicio.sh` la primera vez.

Dos cosas que conviene saber antes de tocarlo:

- **Casi todo tiene un valor por omisión dentro del binario.** El instalador
  escribe el TOML solo la primera vez, así que en un nodo que ya existe manda
  lo que el binario trae.
- **`red.lan` delimita la red local.** De ese valor depende qué peticiones se
  consideran internas —y por tanto quién puede borrar, mover y dar de alta— y
  qué contabiliza el panel como acceso externo. Si no se declara, se toma el
  `/24` de la dirección de escucha. Si su red no es un `/24`, declárelo.

**Aquí no hay ningún secreto, y es una regla del proyecto.** La contraseña de
la web no está en el TOML: systemd se la entrega al proceso desde un archivo
aparte. El testigo del DDNS y el del canal de avisos, igual.

### La primera cuenta

No se crea desde la web ni desde el TOML. La fija
`20_APROVISIONAMIENTO/07_credencial_web.sh`, tecleándola en el nodo: no se pasa
como argumento —quedaría en el historial del shell y en la lista de procesos— y
solo se guardan sus derivaciones. Es **la misma contraseña** para la web y para
SMB.

---

## Despliegue sobre un nodo en producción

```sh
make desplegar
```

Respalda el binario anterior, copia el nuevo, compara huellas y reinicia. Exige
más que compilar: el árbol de git limpio (para que el binario lleve un sello de
versión que signifique algo), `zig` para los auxiliares, `staticcheck`, y un
alias SSH llamado `rpi` en su `~/.ssh/config`.

Para la **primera** instalación no use esto: use
`20_APROVISIONAMIENTO/instalar.sh`, que no da por hecho nada de lo anterior.

---

## Las citas de los comentarios

El código cita decisiones —`ADR-0044`, `RF-30`, `§7`— que apuntan a documentos
de diseño que no se publican. No va a poder abrirlas.

La mayoría son redundantes: la decisión suele estar narrada **en el propio
archivo donde se aplica**, justo al lado de la cita, y con la medición que la
motivó. Esa densidad de comentario es deliberada y es lo que hace que el código
se pueda leer sin el catálogo.
