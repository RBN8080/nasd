package web

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

// TODO recurso que una plantilla pide tiene que existir de verdad.
//
// EL MODO DE FALLO QUE ESTO ATRAPA es silencioso, y por eso merece prueba: una
// hoja de estilo o un guion mal escritos en un «src» no rompen el render, no
// devuelven error y no aparecen en el diario. La página se sirve con un 200
// perfecto y simplemente le falta el aspecto o una función, y eso se descubre
// mirando la pantalla —o peor, no se descubre—.
//
// El 05/08/2026 se añadió menus.js, y con él la tercera ocasión de escribir mal
// una ruta. Se comprueba contra el sistema de archivos INCRUSTADO, que es el
// mismo del que sirve el binario en el nodo: si el archivo no se hubiera
// incluido en la directiva «go:embed», esto se pondría en rojo aquí y no en el
// teléfono del responsable.
func TestTodoRecursoQuePidenLasPlantillasExiste(t *testing.T) {
	plantillas, err := fs.Glob(recursos, "plantillas/*.html")
	if err != nil {
		t.Fatalf("listando plantillas: %v", err)
	}
	if len(plantillas) == 0 {
		t.Fatal("no se encontró ninguna plantilla incrustada")
	}

	// Coge tanto href= como src=, que son las dos formas de pedir un recurso.
	ref := regexp.MustCompile(`(?:src|href)="(/estatico/[^"]+)"`)

	comprobados := 0
	for _, p := range plantillas {
		datos, err := fs.ReadFile(recursos, p)
		if err != nil {
			t.Fatalf("leyendo %s: %v", p, err)
		}
		for _, m := range ref.FindAllStringSubmatch(string(datos), -1) {
			url := m[1]
			// La URL empieza por «/estatico/» y el archivo vive en «estatico/».
			nombre := strings.TrimPrefix(url, "/")
			if _, err := fs.Stat(recursos, nombre); err != nil {
				t.Errorf("%s pide %q y ese archivo NO está incrustado: %v", p, url, err)
				continue
			}
			comprobados++
		}
	}
	if comprobados == 0 {
		t.Fatal("ninguna plantilla pidió un recurso estático; ¿cambió la forma de referenciarlos?")
	}
}

// Y que además se sirvan. Que el archivo esté incrustado y que la ruta HTTP lo
// entregue son dos cosas distintas, y la segunda es la que ve el navegador.
func TestLosRecursosEstaticosSeSirven(t *testing.T) {
	s := servidorConAuth(t)
	h := s.Rutas()

	// Los estáticos van ANTES de la sesión a propósito: la página de acceso los
	// necesita para verse, y quien aún no ha entrado tiene que poder verla.
	for _, ruta := range []string{
		"/estatico/estilo.css",
		"/estatico/menus.js",
		"/estatico/estado.js",
		"/estatico/subida.js",
		"/estatico/visor.js",
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", ruta, nil))
		if w.Code != http.StatusOK {
			t.Errorf("GET %s -> %d; se esperaba 200", ruta, w.Code)
		}
		if w.Body.Len() == 0 {
			t.Errorf("GET %s devolvió un cuerpo vacío", ruta)
		}
	}
}
