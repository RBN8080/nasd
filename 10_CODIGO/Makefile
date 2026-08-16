# Compilación cruzada y despliegue — D-06 / ADR-0006.
#
# Se escribe y se compila en el host; al nodo llega SOLO el binario.
# Nunca se compila en la Pi: 592 MB y cuatro A53 compitiendo con el propio
# servicio que se está probando.

# Alias de ~/.ssh/config, NO "usuario@ip".
#
# VERIFICADO 2026-07-30: «ssh usuario@192.168.1.38» FALLA con
# «Permission denied (publickey)», porque la clave del nodo es
# id_ed25519_rpi y solo se selecciona a traves del alias, que ademas
# lleva IdentitiesOnly=yes. Usar la IP directa rompe el despliegue.
NODO    ?= rpi
BINARIO := nasd

# nas-miniatura (10_CODIGO/nas-miniatura/miniatura.c) — programa auxiliar en
# C, sin dependencias, que extrae la miniatura EXIF incrustada de un JPEG sin
# decodificar la imagen. Rector §7.nonies.bis / 00_RECTOR.md 2026-08-15.
#
# El binario se construye DENTRO de su propio directorio fuente
# (nas-miniatura/nas-miniatura), no en la raíz de 10_CODIGO junto a $(BINARIO):
# «nas-miniatura» a secas en la raíz colisionaría con el directorio del mismo
# nombre que contiene miniatura.c.
BIN_MINIATURA  := nas-miniatura/nas-miniatura
SRC_MINIATURA  := nas-miniatura/miniatura.c

# Compilador cruzado a aarch64-linux-musl. Elegido por el responsable sobre
# la cadena oficial de ARM: una sola descarga, sin instalador, produce un
# binario ESTATICO que no depende de nada del sistema del nodo, y no exige
# sysroot porque miniatura.c no enlaza ninguna libreria.
#
# Igual que GCCBIN mas abajo: winget instala zig y modifica el PATH
# persistente, pero esta sesion de shell arrancó ANTES de esa modificación y
# no la ve hasta reiniciar la terminal (comprobado el 2026-08-15). Se apunta
# a la ruta completa por la misma razón, no por descuido.
#
# Si esta ruta no existiera (paquete desinstalado o movido):
#     winget install zig.zig
ifeq ($(OS),Windows_NT)
ZIG := /c/Users/usuario/AppData/Local/Microsoft/WinGet/Packages/zig.zig_Microsoft.Winget.Source_8wekyb3d8bbwe/zig-x86_64-windows-0.16.0/zig.exe
else
ZIG := zig
endif

# En Windows, make elige cmd.exe si no encuentra un sh, y ENTONCES TODAS las
# reglas fallan con «no se reconoce como un comando interno o externo» — un
# mensaje que senala a tee o a CGO_ENABLED y nunca a la causa real, que es el
# interprete. Ocurrio el 2026-08-04 al estrenar «make» desde PowerShell.
#
# No basta con apuntar a sh.exe: invocado asi hereda el PATH de Windows, donde
# tee, grep, sha256sum y cut NO estan (comprobado el mismo dia). Hace falta un
# shell de LOGIN, que es el que monta /usr/bin. Se verifico ademas que -lc
# conserva el directorio de trabajo, sin lo cual las reglas no encontrarian
# go.mod.
#
# Ruta 8.3 (PROGRA~1) porque make no entiende el espacio de «Program Files».
ifeq ($(OS),Windows_NT)
SHELL       := C:/PROGRA~1/Git/bin/bash.exe
.SHELLFLAGS := -lc
endif

.PHONY: todo verificar verificar-c verificar-limpio carreras prueba compilar compilar-miniatura desplegar volver registro limpiar

todo: verificar compilar

# La equivalencia de utillaje que ADR-0013 dejó VINCULANTE al aceptar Go
# como excepción al charter §6.1: advertencia = error.
verificar: verificar-c
	gofmt -l . | tee /dev/stderr | (! read)
	go vet ./...
	@echo ">> comprobando ADR-0025: ParseMultipartForm / FormFile prohibidos"
	@# Se descartan los COMENTARIOS de línea completa antes de buscar.
	@#
	@# Sin esto la puerta daba un falso positivo y el proyecto se quedó con
	@# «make verificar» roto: el arreglo de CSRF del 2026-07-31 documentó en
	@# administracion.go POR QUÉ no se usa PostFormValue —que llama por dentro
	@# a ParseMultipartForm— y la propia explicación disparaba la alarma.
	@#
	@# Un verificador que grita sin motivo se acaba ignorando o borrando, que
	@# es la otra cara del defecto que 00_RECTOR.md §12.5 lleva persiguiendo.
	@#
	@# LÍMITE CONOCIDO: solo se filtran los comentarios de línea completa, así
	@# que una llamada real con comentario al final SÍ se detecta. Es el lado
	@# por el que conviene equivocarse.
	@! grep -rn "ParseMultipartForm\|\.FormFile(" --include="*.go" . \
	  | grep -v "_test.go" | grep -v "PROHIBIDO" \
	  | grep -vE '^[^:]+:[0-9]+:[[:space:]]*//' \
	  || (echo "ERROR: uso prohibido — vuelca a tmpfs, es decir a RAM (ADR-0025)"; exit 1)
	go test ./...
	@# Se analiza para la plataforma REAL, no para la del host: hay avisos que
	@# solo existen en linux/arm64. Charter §6.1 deja el análisis estático como
	@# «advertencia = error», y sin esta línea la puerta no lo cumplía.
	GOOS=linux GOARCH=arm64 staticcheck ./...

# Correccion de nas-miniatura (miniatura.c), sin CGO de por medio: es un
# programa aparte, no un paquete Go, así que ni «go vet» ni «go test» lo
# tocan. Se SALTA limpiamente si zig no está instalado, para no romper esta
# puerta en una máquina donde solo se está tocando código Go — pero si zig
# SÍ está, un fallo aquí detiene «verificar» igual que cualquier otro.
#
# Compila NATIVO (sin --target) y no cruzado a la Pi: el objetivo de esta
# compilación es correr los desinfectantes de dirección y de comportamiento
# indefinido contra el corpus de pruebas.sh, y para eso hace falta un binario
# que se ejecute EN ESTA máquina. La compilación cruzada de producción va en
# «compilar-miniatura», sin desinfectantes —no hay runtime de ASan para
# aarch64-linux-musl estático— y es la que de verdad viaja al nodo.
verificar-c:
	@if [ ! -x "$(ZIG)" ] && ! command -v "$(ZIG)" >/dev/null 2>&1; then \
	  echo ">> zig no está instalado: se omite verificar-c (make compilar SÍ lo exige)"; \
	  exit 0; \
	fi
	@echo ">> nas-miniatura: compilando con desinfectantes y ejecutando el corpus"
	"$(ZIG)" cc -std=c11 -Wall -Wextra -Werror -O0 -g \
	  -fsanitize=address,undefined \
	  -o nas-miniatura/miniatura_verificar $(SRC_MINIATURA)
	bash nas-miniatura/pruebas/corpus.sh nas-miniatura/miniatura_verificar
	@rm -f nas-miniatura/miniatura_verificar nas-miniatura/miniatura_verificar.exe \
	  nas-miniatura/miniatura_verificar.pdb

# El detector de carreras. Se separa de «verificar» porque tarda bastante más,
# pero NO es opcional al cerrar un bloque: el programa tiene tres puntos de
# estado compartido y dos de los peores defectos de este proyecto fueron
# carreras (rector v1.11.0).
#
# -race exige CGO_ENABLED=1, y por tanto un compilador de C. Windows no trae
# uno de fábrica: hasta el 2026-08-04 esto era un hueco conocido en este PC
# (NAS_OPERACION.txt §8.C), rodeable solo copiando el código a WSL. Ese día se
# encontró que ya había un gcc funcional instalado por winget —comprobado
# compilando y ejecutando un binario real, no solo con --version— y no está
# en el PATH del sistema. Se añade aquí, SOLO para esta regla.
#
# Si esta ruta no existiera (paquete desinstalado o movido):
#     winget install BrechtSanders.WinLibs.POSIX.UCRT
ifeq ($(OS),Windows_NT)
GCCBIN := /c/Users/usuario/AppData/Local/Microsoft/WinGet/Packages/BrechtSanders.WinLibs.POSIX.UCRT_Microsoft.Winget.Source_8wekyb3d8bbwe/mingw64/bin
carreras:
	PATH="$(GCCBIN):$$PATH" CGO_ENABLED=1 go test -race -count=1 ./...
else
carreras:
	CGO_ENABLED=1 go test -race -count=1 ./...
endif

prueba:
	go test -v ./...

# Binario estático ARM64. Sin CGO: se copia y corre, sin dependencias.
compilar: compilar-miniatura
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build \
	  -trimpath -ldflags="-s -w" -o $(BINARIO) ./cmd/nasd
	@ls -lh $(BINARIO)

# nas-miniatura, cruzado a aarch64-linux-musl. «-static» además de musl
# porque musl por sí solo no evita un enlazado dinámico contra su propia
# libc; el binario que llega al nodo no debe depender de NADA que ya esté
# instalado ahí — mismo principio que el binario de nasd, «se copia y
# corre». Sin desinfectantes: no existe runtime de ASan/UBSan para este
# objetivo estático; esa cobertura la da «verificar-c» sobre un build nativo.
compilar-miniatura:
	"$(ZIG)" cc -target aarch64-linux-musl -std=c11 -Wall -Wextra -Werror \
	  -O2 -static -s -o $(BIN_MINIATURA) $(SRC_MINIATURA)
	@ls -lh $(BIN_MINIATURA)

# No se exige commitear cada retoque — el responsable lo pidio explicitamente
# el 2026-08-04 para no llenar el historial de cambios triviales. Pero
# vcs.revision (apartado E de NAS_OPERACION.txt) solo sirve si el commit que
# senala es real, y eso es innegociable justo al desplegar: es el instante en
# que el binario sale a produccion. Por eso el arbol se exige limpio AQUI, no
# al editar.
#
# LA PUERTA MIRA EL REPOSITORIO ENTERO, Y HASTA EL 2026-08-06 NO LO HACIA.
#
# Miraba solo 10_CODIGO, con el argumento de que un .md pendiente en otra
# carpeta no deberia bloquear un despliegue de codigo. El argumento es
# razonable y aun asi la puerta estaba rota, porque NO ES ESTA REGLA QUIEN
# DECIDE lo que se estampa en el binario: Go calcula vcs.modified sobre
# «git status --porcelain» del repositorio COMPLETO, archivos sin seguimiento
# incluidos. Acotar la puerta a 10_CODIGO no acotaba la marca; solo dejaba de
# vigilarla.
#
# Ocurrio de verdad: dos .patch que una sesion anterior dejo en la raiz —ni
# siquiera codigo— bastaron para que un binario compilado desde un arbol
# «limpio» segun esta regla saliera estampado como ARBOL MODIFICADO, pasara la
# puerta y llegara a produccion. Comprobado apartando los dos archivos y
# recompilando: vcs.modified paso de true a false sin tocar una linea.
#
# La regla y la marca tienen que medir LO MISMO, o la garantia de
# «vcs.modified=false» que exige 00_RECTOR.md no es comprobable por nadie.
# Se usa «git status --porcelain» a proposito: es exactamente lo que mira Go.
verificar-limpio:
	@test -z "$$(git status --porcelain)" || ( \
	  echo "ERROR: el repositorio tiene cambios sin commitear o archivos sin seguimiento."; \
	  echo "       Go estamparia vcs.modified=true y el binario saldria como ARBOL MODIFICADO."; \
	  echo "       Commitee, descarte o ignore lo siguiente antes de desplegar:"; \
	  git status --porcelain; \
	  exit 1)

# El respaldo va PRIMERO y por eso este objetivo no es solo «copiar y reiniciar»:
# sin él, un binario defectuoso deja el servicio caído sin nada a lo que volver.
# El procedimiento manual ya lo exigía (NAS_OPERACION.txt §8.F); automatizarlo
# a medias habría convertido «make desplegar» en la vía rápida a perderlo.
#
# Si /usr/local/bin/nasd no existiera, esta primera orden falla y el despliegue
# se detiene. Es deliberado: en un nodo EN PRODUCCION esa ausencia es la señal
# de que algo va mal, no un caso que haya que sortear.
# «verificar» va PRIMERO y no es negociable desde el 2026-08-04. Hasta ese dia
# «desplegar» solo dependia de verificar-limpio y compilar, es decir, comprobaba
# el ORIGEN del binario pero no su CORRECCION. Ese mismo dia un renombrado
# estetico —«El servicio» a «Servicio»— dejo en rojo
# TestEstadoConSesionRespondeHTMLYJSON, que usa ese texto para probar que la
# plantilla se renderizo ENTERA, y el binario se instalo en produccion igual.
# Nadie se entero hasta que la puerta se ejecuto a mano, horas despues.
#
# Una puerta por la que el camino diario no pasa no es una puerta. Cuesta unos
# segundos por despliegue; el defecto que evita costo un binario en produccion.
desplegar: verificar verificar-limpio compilar
	ssh $(NODO) 'sudo install -m 0755 /usr/local/bin/$(BINARIO) /usr/local/bin/$(BINARIO).previous'
	scp $(BINARIO) $(NODO):/tmp/$(BINARIO)
	ssh $(NODO) 'sudo install -m 0755 /tmp/$(BINARIO) /usr/local/bin/$(BINARIO) && sudo systemctl restart nasd && sleep 1 && systemctl is-active nasd'
	@# «active» dice que ARRANCÓ; la huella dice que arrancó ESTE archivo. Son
	@# dos afirmaciones distintas y el despliegue necesita las dos.
	@huella_local=$$(sha256sum $(BINARIO) | cut -d' ' -f1); \
	 huella_nodo=$$(ssh $(NODO) 'sha256sum /usr/local/bin/$(BINARIO)' | cut -d' ' -f1); \
	 test "$$huella_local" = "$$huella_nodo" \
	   || (echo "ERROR: la huella instalada NO coincide con la compilada"; exit 1); \
	 echo "huella verificada  $$huella_local"

# La vuelta atrás se automatiza por la misma razón que el respaldo: el
# 2026-08-03, tecleada a mano, la ruta se escribió «/usr/local/nasd.previous»
# —sin bin/— y la copia quedó donde el procedimiento no la busca. Un objetivo
# no se equivoca de ruta bajo presión; una persona con el servicio caído, sí.
volver:
	ssh $(NODO) 'sudo install -m 0755 /usr/local/bin/$(BINARIO).previous /usr/local/bin/$(BINARIO) && sudo systemctl restart nasd && systemctl is-active nasd'

registro:
	ssh $(NODO) 'journalctl -u nasd -n 50 --no-pager -o cat'

limpiar:
	rm -f $(BINARIO) $(BIN_MINIATURA)
