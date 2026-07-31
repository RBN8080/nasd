# Compilación cruzada y despliegue — D-06 / ADR-0006.
#
# Se escribe y se compila en el host; al nodo llega SOLO el binario.
# Nunca se compila en la Pi: 592 MB y cuatro A53 compitiendo con el propio
# servicio que se está probando.

NODO    ?= 192.168.1.38
USUARIO ?= usuario
BINARIO := nasd

.PHONY: todo verificar prueba compilar desplegar registro limpiar

todo: verificar compilar

# La equivalencia de utillaje que ADR-0013 dejó VINCULANTE al aceptar Go
# como excepción al charter §6.1: advertencia = error.
verificar:
	gofmt -l . | tee /dev/stderr | (! read)
	go vet ./...
	@echo ">> comprobando ADR-0025: ParseMultipartForm / FormFile prohibidos"
	@! grep -rn "ParseMultipartForm\|\.FormFile(" --include="*.go" . \
	  | grep -v "_test.go" | grep -v "PROHIBIDO" \
	  || (echo "ERROR: uso prohibido — vuelca a tmpfs, es decir a RAM (ADR-0025)"; exit 1)
	go test ./...

prueba:
	go test -v ./...

# Binario estático ARM64. Sin CGO: se copia y corre, sin dependencias.
compilar:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build \
	  -trimpath -ldflags="-s -w" -o $(BINARIO) ./cmd/nasd
	@ls -lh $(BINARIO)

desplegar: compilar
	scp $(BINARIO) $(USUARIO)@$(NODO):/tmp/$(BINARIO)
	ssh $(USUARIO)@$(NODO) 'sudo install -m 0755 /tmp/$(BINARIO) /usr/local/bin/$(BINARIO) && sudo systemctl restart nasd && sleep 1 && systemctl is-active nasd'

registro:
	ssh $(USUARIO)@$(NODO) 'journalctl -u nasd -n 50 --no-pager -o cat'

limpiar:
	rm -f $(BINARIO)
