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

// LA PÁGINA NO SE ANCLA (v1.137.0). Safari 27 activó el anclaje de
// desplazamiento por omisión y, con las cifras en vivo, movía la página sola en
// el iPhone. Quitar esta línea no rompe nada que se vea en el ordenador: por eso
// la vigila una prueba.
func TestLaPaginaNoSeAncla(t *testing.T) {
	hoja, ok := estaticos()["/estatico/estilo.css"]
	if !ok {
		t.Fatal("no está la hoja de estilos entre los recursos incrustados")
	}
	regla := regexp.MustCompile(`(?m)^html, body, \.cuerpo\{overflow-anchor:none;\}`)
	if !regla.Match(hoja.crudo) {
		t.Error("falta «html, body, .cuerpo{overflow-anchor:none;}»: sin ella, Safari 27 mueve la página " +
			"cada vez que se refrescan las cifras")
	}
}

// sinComentarios quita los comentarios de la hoja: explican lo que se retiró y
// nombran justo las propiedades que estas pruebas prohíben.
func sinComentarios(css []byte) string {
	return regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(string(css), "")
}

// NI DESENFOQUE NI MÁSCARA EN TODA LA HOJA (v1.138.0).
//
// La cinta de Seguridad corre siempre y va pegada arriba. Con «mask-image», el
// navegador vuelve a componer la capa en cada fotograma; con «backdrop-filter»,
// vuelve a desenfocar lo de debajo. En el ordenador no se nota y en el iPhone
// el desplazamiento va a tirones: es lo que costó tres despliegues en Estado.
// La prueba de arriba miraba cinco selectores; esta no deja ninguno.
func TestLoPegadoNoDesenfocaNiEnmascara(t *testing.T) {
	hoja, ok := estaticos()["/estatico/estilo.css"]
	if !ok {
		t.Fatal("no está la hoja de estilos entre los recursos incrustados")
	}
	css := sinComentarios(hoja.crudo)
	for _, prohibida := range []string{"backdrop-filter", "mask-image"} {
		if strings.Contains(css, prohibida) {
			t.Errorf("la hoja vuelve a usar «%s»; en el iPhone eso hace que el desplazamiento vaya a tirones", prohibida)
		}
	}
}

// TODO LO QUE SE DESPLAZA DESACTIVA EL ANCLAJE (v1.138.0).
//
// «overflow-anchor» no se hereda: la regla de la página no cubre los cuadros
// con barra propia, el rail, los menús ni el cajón, y Safari 27 ancla cada uno
// por su cuenta. Quien añada una barra nueva tiene que añadirla a la regla.
func TestTodoLoQueSeDesplazaNoSeAncla(t *testing.T) {
	hoja, ok := estaticos()["/estatico/estilo.css"]
	if !ok {
		t.Fatal("no está la hoja de estilos entre los recursos incrustados")
	}
	css := sinComentarios(hoja.crudo)
	regla := regexp.MustCompile(`([^{}]+)\{([^{}]*)\}`)
	ultimaClase := regexp.MustCompile(`[.#]?[\w-]+$`)

	sinAncla := map[string]bool{}
	for _, m := range regla.FindAllStringSubmatch(css, -1) {
		if !strings.Contains(m[2], "overflow-anchor:none") {
			continue
		}
		for _, sel := range strings.Split(m[1], ",") {
			sinAncla[strings.TrimSpace(sel)] = true
		}
	}
	vistas := 0
	for _, m := range regla.FindAllStringSubmatch(css, -1) {
		if !strings.Contains(m[2], "overflow-y:auto") && !strings.Contains(m[2], "overflow:auto") {
			continue
		}
		for _, sel := range strings.Split(m[1], ",") {
			vistas++
			quien := ultimaClase.FindString(strings.TrimSpace(sel))
			if !sinAncla[quien] {
				t.Errorf("«%s» se desplaza y «%s» no está en una regla con overflow-anchor:none", strings.TrimSpace(sel), quien)
			}
		}
	}
	if vistas < 5 {
		t.Errorf("solo se encontraron %d reglas con barra propia; ¿cambió la forma de escribirlas?", vistas)
	}
}

// LOS TRES FLUJOS EN VIVO ESPERAN A QUE LA PÁGINA PARE (v1.138.0).
//
// La pausa nació en estado.js y solo la tenía Estado: Seguridad escribía en la
// página cada uno o dos segundos sin mirar si se estaba moviendo. Vive en
// quieto.js, que carga el marco ANTES que el archivo de cada módulo.
func TestLosFlujosEnVivoEsperanAQueLaPaginaPare(t *testing.T) {
	for _, ruta := range []string{"/estatico/estado.js", "/estatico/seguridad.js", "/estatico/cuentas.js"} {
		js, ok := estaticos()[ruta]
		if !ok {
			t.Fatalf("no está %s entre los recursos incrustados", ruta)
		}
		if !strings.Contains(string(js.crudo), "nasQuieto") {
			t.Errorf("%s escribe en la página sin pasar por la pausa de quieto.js", ruta)
		}
	}
	cuerpo := panelSeguridad(t, servidorConAuth(t), "")
	comun := strings.Index(cuerpo, `src="/estatico/quieto.js"`)
	modulo := strings.Index(cuerpo, `src="/estatico/seguridad.js"`)
	if comun < 0 || modulo < 0 || comun > modulo {
		t.Error("quieto.js tiene que cargarse antes que el archivo del módulo: sin él, se escribe mientras la página se mueve")
	}
}
