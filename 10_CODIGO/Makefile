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

.PHONY: todo verificar carreras prueba compilar desplegar volver registro limpiar

todo: verificar compilar

# La equivalencia de utillaje que ADR-0013 dejó VINCULANTE al aceptar Go
# como excepción al charter §6.1: advertencia = error.
verificar:
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

# El detector de carreras. Se separa de «verificar» porque tarda bastante más,
# pero NO es opcional al cerrar un bloque: el programa tiene tres puntos de
# estado compartido y dos de los peores defectos de este proyecto fueron
# carreras (rector v1.11.0).
carreras:
	go test -race ./...

prueba:
	go test -v ./...

# Binario estático ARM64. Sin CGO: se copia y corre, sin dependencias.
compilar:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build \
	  -trimpath -ldflags="-s -w" -o $(BINARIO) ./cmd/nasd
	@ls -lh $(BINARIO)

# El respaldo va PRIMERO y por eso este objetivo no es solo «copiar y reiniciar»:
# sin él, un binario defectuoso deja el servicio caído sin nada a lo que volver.
# El procedimiento manual ya lo exigía (NAS_OPERACION.txt §8.F); automatizarlo
# a medias habría convertido «make desplegar» en la vía rápida a perderlo.
#
# Si /usr/local/bin/nasd no existiera, esta primera orden falla y el despliegue
# se detiene. Es deliberado: en un nodo EN PRODUCCION esa ausencia es la señal
# de que algo va mal, no un caso que haya que sortear.
desplegar: compilar
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
	rm -f $(BINARIO)
