package web

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"regexp"
	"strings"
	"testing"
)

// La huella del pie tiene que ser LA DEL BINARIO QUE ESTÁ CORRIENDO.
//
// Es el motivo entero de que exista: el responsable la compara con el
// «sha256sum» del artefacto para saber, sin entrar por SSH, que el nodo está
// ejecutando lo que él compiló. Una huella que fuera de otro archivo —o que se
// calculara sobre algo distinto cada vez— sería peor que no tener ninguna:
// diría que sí cuando no. Aquí se recalcula a mano y se exige que coincida.
func TestLaHuellaDelPieEsLaDelEjecutable(t *testing.T) {
	h := huellaDelBinario()
	if h == "" {
		t.Skip("os.Executable no dio ruta en este entorno; sin huella el pie sigue " +
			"siendo correcto, solo más corto")
	}

	ruta, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	datos, err := os.ReadFile(ruta)
	if err != nil {
		t.Fatalf("leyendo el ejecutable: %v", err)
	}
	suma := sha256.Sum256(datos)
	quiero := hex.EncodeToString(suma[:])[:12]

	if h != quiero {
		t.Fatalf("la huella del pie es %q y la del ejecutable %q", h, quiero)
	}
}

// Doce caracteres hexadecimales, ni más ni menos.
//
// Se comprueba la FORMA además del valor porque el pie es un renglón medido: la
// línea completa ya no cabe en el ancho de un teléfono, y alargar la huella
// —cambiando el corte por descuido— la partiría en dos renglones.
func TestLaHuellaTieneLaFormaQueEsperaElPie(t *testing.T) {
	h := huellaDelBinario()
	if h == "" {
		t.Skip("sin huella en este entorno")
	}
	if !regexp.MustCompile(`^[0-9a-f]{12}$`).MatchString(h) {
		t.Fatalf("huella %q: se esperaban 12 caracteres hexadecimales en minúscula", h)
	}
}

// Se calcula UNA sola vez. Son unos 11 MB leídos del medio de arranque, que en
// este nodo es un consumible (P9) y comparte el bus USB 2.0 con el disco de
// datos (RES-02). Hacerlo en cada render sería pagar esa lectura por cada visita
// a la página de estado.
func TestLaHuellaSeCalculaUnaSolaVez(t *testing.T) {
	uno, dos := huellaDelBinario(), huellaDelBinario()
	if uno != dos {
		t.Fatalf("dos llamadas dieron resultados distintos: %q y %q", uno, dos)
	}
}

// El pie sale por campos sueltos y NO como una cadena ya unida: es lo que
// permite a la plantilla impedir que un renglón se parta por dentro de la
// huella o de la fecha. Si alguien vuelve a unirlos aquí, el pie del teléfono
// puede quedar con media huella en cada línea.
func TestLaVersionSaleEnCamposSueltos(t *testing.T) {
	partes := versionDelBinario()
	if len(partes) == 0 {
		t.Fatal("el pie no puede quedarse sin ningún campo")
	}
	if partes[0] != "nasd" {
		t.Fatalf("el primer campo debe ser el nombre del binario, y es %q", partes[0])
	}
	for _, p := range partes {
		if strings.Contains(p, "·") {
			t.Fatalf("el campo %q ya trae el separador dentro: los campos van "+
				"sueltos y es la plantilla quien los une", p)
		}
	}
}
