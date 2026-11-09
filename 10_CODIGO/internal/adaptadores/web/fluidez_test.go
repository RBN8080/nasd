package web

import (
	"regexp"
	"strings"
	"testing"
)

// LAS BARRAS PEGADAS DEL TELÉFONO NO DESENFOCAN LO DE DEBAJO (v1.136.0).
//
// Con backdrop-filter, Safari en el iPhone vuelve a desenfocar el contenido en
// cada fotograma del desplazamiento, y /estado seguía bajando a tirones tras
// ADR-0107, que dejó escrito este paso como el siguiente. Ni el ojo ni las
// demás pruebas lo ven: en el ordenador estas barras ya no desenfocaban, y en
// Edge el desplazamiento va fino igual.
func TestLasBarrasPegadasNoDesenfocan(t *testing.T) {
	hoja, ok := estaticos()["/estatico/estilo.css"]
	if !ok {
		t.Fatal("no está la hoja de estilos entre los recursos incrustados")
	}
	vistas := 0
	for _, sel := range []string{".f1", ".tabs", ".ordenes", ".od", ".sgg"} {
		regla := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(sel) + `\{([^}]*)\}`)
		for _, m := range regla.FindAllStringSubmatch(string(hoja.crudo), -1) {
			vistas++
			if strings.Contains(m[1], "backdrop-filter") {
				t.Errorf("«%s» vuelve a llevar backdrop-filter; en el iPhone eso hace que el desplazamiento vaya a tirones", sel)
			}
		}
	}
	if vistas < 5 {
		t.Errorf("solo se encontraron %d reglas de barras; ¿cambiaron los selectores?", vistas)
	}
}
