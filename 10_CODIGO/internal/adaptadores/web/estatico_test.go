package web

import (
	"bytes"
	"compress/gzip"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path"
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

// LA RECARGA NO DEBE VOLVER A BAJAR EL ARCHIVO, y esa es toda la razon de ser
// del ETag. Antes del 2026-08-19 /estatico/ no mandaba ninguna cabecera de
// cache: medido contra el nodo, cada carga de /listado rebajaba 55 895 bytes
// enteros porque embed.FS reporta mtime CERO y ServeContent omitia
// Last-Modified, asi que ni siquiera If-Modified-Since servia.
func TestUnRecursoConocidoSeRevalidaCon304(t *testing.T) {
	s := servidorConAuth(t)
	h := s.Rutas()

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/estatico/estilo.css", nil))
	etag := w.Header().Get("ETag")
	if etag == "" {
		t.Fatal("sin ETag no hay revalidacion posible")
	}
	if cc := w.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("Cache-Control = %q; se esperaba no-cache", cc)
	}

	// Y ahora la segunda visita, que es la que importa.
	r := httptest.NewRequest("GET", "/estatico/estilo.css", nil)
	r.Header.Set("If-None-Match", etag)
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, r)

	if w2.Code != http.StatusNotModified {
		t.Errorf("con If-None-Match -> %d; se esperaba 304", w2.Code)
	}
	// UN 304 CON CUERPO NO AHORRA NADA, que es justo lo que se venia a
	// arreglar. Se comprueba el cuerpo y no solo el codigo.
	if w2.Body.Len() != 0 {
		t.Errorf("el 304 trae %d bytes de cuerpo; debe venir vacio", w2.Body.Len())
	}
}

// El ETag debe seguir al CONTENIDO: dos recursos distintos no pueden compartirlo,
// o el navegador serviria uno creyendo tener el otro.
func TestCadaRecursoTieneSuPropioEtag(t *testing.T) {
	s := servidorConAuth(t)
	h := s.Rutas()

	vistos := map[string]string{}
	for _, ruta := range []string{"/estatico/estilo.css", "/estatico/menus.js", "/estatico/visor.js"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", ruta, nil))
		etag := w.Header().Get("ETag")
		if otra, repetido := vistos[etag]; repetido {
			t.Errorf("%s y %s comparten ETag %s", ruta, otra, etag)
		}
		vistos[etag] = ruta
	}
}

// GZIP SOLO A QUIEN LO PIDE. Mandar bytes comprimidos a un cliente que no los
// admite le entrega basura binaria, y sin Vary una cache intermedia acabaria
// haciendolo por su cuenta.
func TestSoloSeComprimeParaQuienLoAdmite(t *testing.T) {
	s := servidorConAuth(t)
	h := s.Rutas()

	sinGzip := httptest.NewRecorder()
	h.ServeHTTP(sinGzip, httptest.NewRequest("GET", "/estatico/estilo.css", nil))
	if ce := sinGzip.Header().Get("Content-Encoding"); ce != "" {
		t.Errorf("Content-Encoding = %q sin haberlo pedido", ce)
	}
	if v := sinGzip.Header().Get("Vary"); v != "Accept-Encoding" {
		t.Errorf("Vary = %q; sin el, una cache intermedia serviria gzip a quien no lo admite", v)
	}

	r := httptest.NewRequest("GET", "/estatico/estilo.css", nil)
	r.Header.Set("Accept-Encoding", "gzip")
	conGzip := httptest.NewRecorder()
	h.ServeHTTP(conGzip, r)
	if ce := conGzip.Header().Get("Content-Encoding"); ce != "gzip" {
		t.Fatalf("Content-Encoding = %q; se esperaba gzip", ce)
	}
	if conGzip.Body.Len() >= sinGzip.Body.Len() {
		t.Errorf("comprimido %d bytes frente a %d sin comprimir: no comprime nada",
			conGzip.Body.Len(), sinGzip.Body.Len())
	}
	// Y lo comprimido tiene que descomprimirse a lo mismo, o estariamos
	// sirviendo dos recursos distintos por la misma URL.
	z, err := gzip.NewReader(bytes.NewReader(conGzip.Body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	llano, err := io.ReadAll(z)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(llano, sinGzip.Body.Bytes()) {
		t.Error("lo comprimido no coincide con lo servido en crudo")
	}
}

// «gzip;q=0» es como un cliente DICE QUE NO. Buscar la palabra suelta en la
// cabecera lo entendia justo al reves.
func TestGzipRechazadoConQCeroNoSeEnvia(t *testing.T) {
	s := servidorConAuth(t)
	r := httptest.NewRequest("GET", "/estatico/estilo.css", nil)
	r.Header.Set("Accept-Encoding", "gzip;q=0")
	w := httptest.NewRecorder()
	s.Rutas().ServeHTTP(w, r)

	if ce := w.Header().Get("Content-Encoding"); ce != "" {
		t.Errorf("Content-Encoding = %q pese a gzip;q=0", ce)
	}
}

// Un recurso que no existe sigue siendo un 404 y no un panico ni un 200 vacio.
func TestUnEstaticoInexistenteEs404(t *testing.T) {
	s := servidorConAuth(t)
	w := httptest.NewRecorder()
	s.Rutas().ServeHTTP(w, httptest.NewRequest("GET", "/estatico/no-existe.css", nil))
	if w.Code != http.StatusNotFound {
		t.Errorf("GET de un estatico inexistente -> %d; se esperaba 404", w.Code)
	}
}

// TODA PAGINA TIENE QUE INVOCAR LA CABECERA COMPARTIDA.
//
// Al sacar el <head> a un {{define}} unico se gano que un cambio se haga una
// vez, y se perdio la garantia que daba la repeticion: antes era imposible que
// una pagina se quedara sin hoja de estilos, porque cada una llevaba su <link>
// escrito. Ahora basta con olvidar una linea. Esto es esa garantia, devuelta.
func TestTodaPaginaInvocaLaCabeceraCompartida(t *testing.T) {
	paginas, err := fs.Glob(recursos, "plantillas/*.html")
	if err != nil {
		t.Fatal(err)
	}
	revisadas := 0
	for _, p := range paginas {
		// Los parciales empiezan por «_» y no son paginas: son lo invocado.
		if strings.HasPrefix(path.Base(p), "_") {
			continue
		}
		datos, err := fs.ReadFile(recursos, p)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(datos), `{{template "cabeza"`) {
			t.Errorf("%s no invoca la cabecera compartida: se quedaria sin hoja de estilos", p)
		}
		revisadas++
	}
	if revisadas < 9 {
		t.Errorf("solo se revisaron %d paginas; se esperaban al menos 9", revisadas)
	}
}
