package web

import (
	"bytes"
	"context"
	"crypto/tls"
	"io"
	"iter"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nasd/internal/almacen"
	"nasd/internal/autenticacion"
)

// mensajeArchivoNoCompatible fija la redacción de RF-25 palabra por palabra.
//
// Vive en la prueba y no en el código porque el texto solo se escribe en un
// sitio —visor.html—, y una constante en producción que nadie usara sería
// código zombi. Aquí sí trabaja: si alguien reescribe el mensaje «para que
// suene mejor», estas pruebas se ponen en rojo.
const mensajeArchivoNoCompatible = "Este archivo no se puede abrir en el navegador"

// almacenDeApertura sirve contenido de memoria. Se necesita uno propio porque
// almacenVacio no devuelve bytes, y aquí lo que se prueba es justamente qué
// bytes salen y con qué cabeceras.
type almacenDeApertura struct {
	almacenVacio
	entradas  []almacen.Entrada
	contenido map[string][]byte
	// aperturas cuenta las llamadas a Abrir. Una lista positiva que rechaza
	// por extensión NO debe llegar a tocar el almacén, y contar es la única
	// forma de comprobarlo desde fuera.
	aperturas int
}

func (a *almacenDeApertura) Listar(context.Context, almacen.RutaSegura) iter.Seq2[almacen.Entrada, error] {
	return func(yield func(almacen.Entrada, error) bool) {
		for _, e := range a.entradas {
			if !yield(e, nil) {
				return
			}
		}
	}
}

func (a *almacenDeApertura) Abrir(_ context.Context, r almacen.RutaSegura) (io.ReadSeekCloser, almacen.Entrada, error) {
	a.aperturas++
	datos, ok := a.contenido[r.Rel()]
	if !ok {
		return nil, almacen.Entrada{}, almacen.ErrNoExiste
	}
	return lectorDeMemoria{bytes.NewReader(datos)}, almacen.Entrada{
		Ruta: r, Nombre: r.Nombre(), Tamano: int64(len(datos)),
	}, nil
}

type lectorDeMemoria struct{ *bytes.Reader }

func (lectorDeMemoria) Close() error { return nil }

func servidorDeApertura(t *testing.T) (*Servidor, *almacenDeApertura) {
	t.Helper()
	linea, err := autenticacion.Derivar(claveDePrueba, 1000)
	if err != nil {
		t.Fatalf("Derivar: %v", err)
	}
	a := &almacenDeApertura{contenido: map[string][]byte{}}
	s, err := Nuevo(Opciones{
		Almacen:          a,
		AlmacenDe:        almacenPorUsuarioDePrueba(a),
		Registro:         slog.New(slog.NewJSONHandler(io.Discard, nil)),
		PlazoInactividad: time.Minute,
		Credencial:       linea,
		Usuarios:         registroDePrueba(t),
		DuracionSesion:   time.Hour,
	})
	if err != nil {
		t.Fatalf("Nuevo: %v", err)
	}
	return s, a
}

// agregar deja un archivo listo para listarlo, abrirlo y servirlo.
func (a *almacenDeApertura) agregar(t *testing.T, nombre string, datos []byte) almacen.RutaSegura {
	t.Helper()
	ruta, err := almacen.NuevaRuta(nombre)
	if err != nil {
		t.Fatalf("NuevaRuta(%q): %v", nombre, err)
	}
	a.entradas = append(a.entradas, almacen.Entrada{
		Ruta: ruta, Nombre: ruta.Nombre(), Tamano: int64(len(datos)),
	})
	a.contenido[ruta.Rel()] = datos
	return ruta
}

func peticionConSesion(t *testing.T, s *Servidor, ruta string) *httptest.ResponseRecorder {
	t.Helper()
	cookie, _ := sesionAbierta(t, s)
	r := httptest.NewRequest(http.MethodGet, ruta, nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	s.Rutas().ServeHTTP(w, r)
	return w
}

// peticionConSesionHTTPS simula una petición que llegó por TLS, con el mismo
// campo que usa el servidor de verdad: r.TLS != nil. httptest.NewRequest lo
// deja en nil por omisión —simula HTTP—, así que las pruebas que necesitan el
// otro lado de esa rama lo fijan a mano, sin abrir un socket TLS real.
func peticionConSesionHTTPS(t *testing.T, s *Servidor, ruta string) *httptest.ResponseRecorder {
	t.Helper()
	cookie, _ := sesionAbierta(t, s)
	r := httptest.NewRequest(http.MethodGet, ruta, nil)
	r.AddCookie(cookie)
	r.TLS = &tls.ConnectionState{}
	w := httptest.NewRecorder()
	s.Rutas().ServeHTTP(w, r)
	return w
}

// EL DEFECTO QUE ORIGINÓ RF-25, convertido en prueba: el nombre del archivo ya
// no puede apuntar a /descargar. Mientras lo hiciera, todo navegador que
// respete «attachment» —Brave, entre otros— descargaba al hacer clic, que es
// exactamente lo que se reportó.
func TestElNombreAbreYSoloElMenuDescarga(t *testing.T) {
	s, a := servidorDeApertura(t)
	a.agregar(t, "foto.png", []byte("\x89PNG\r\n\x1a\n"))
	a.agregar(t, "paquete.zip", []byte("PK\x03\x04"))

	cuerpo := peticionConSesion(t, s, "/").Body.String()

	for _, nombre := range []string{"foto.png", "paquete.zip"} {
		if !strings.Contains(cuerpo, `href="/abrir/`+nombre+`"`) {
			t.Errorf("el nombre de %s no apunta a /abrir", nombre)
		}
		if strings.Contains(cuerpo, `href="/descargar/`+nombre+`"`) {
			t.Errorf("el nombre de %s sigue apuntando a /descargar", nombre)
		}
		if !strings.Contains(cuerpo, `action="/descargar/`+nombre+`"`) {
			t.Errorf("el menú de %s no ofrece Descargar", nombre)
		}
	}

	// «Descargar» va ARRIBA del panel, antes que renombrar, mover y borrar.
	if i, j := strings.Index(cuerpo, "Descargar"), strings.Index(cuerpo, "Renombrar"); i < 0 || i > j {
		t.Errorf("Descargar no es la primera opción del menú (Descargar=%d, Renombrar=%d)", i, j)
	}
}

// Sobre HTTPS, las imágenes —y solo ellas— se abren en una pestaña nueva.
func TestSoloLasImagenesAbrenEnPestanaNuevaSobreHTTPS(t *testing.T) {
	s, a := servidorDeApertura(t)
	a.agregar(t, "foto.png", []byte("\x89PNG\r\n\x1a\n"))
	a.agregar(t, "notas.txt", []byte("hola"))

	for linea := range strings.SplitSeq(peticionConSesionHTTPS(t, s, "/").Body.String(), "\n") {
		if !strings.Contains(linea, `href="/abrir/`) {
			continue
		}
		enPestana := strings.Contains(linea, `target="_blank"`)
		if strings.Contains(linea, "foto.png") && !enPestana {
			t.Error("la imagen no abre en una pestaña nueva por HTTPS")
		}
		if strings.Contains(linea, "notas.txt") && enPestana {
			t.Error("un archivo que no es imagen abre en una pestaña nueva")
		}
		if enPestana && !strings.Contains(linea, `rel="noopener"`) {
			t.Error("se abre una pestaña nueva sin rel=noopener")
		}
	}
}

// EL ARREGLO DEL 2026-08-06, convertido en prueba: sobre HTTP en la LAN
// ninguna imagen abre en pestaña nueva, ni siquiera ella. Es la mitad del
// defecto que encontró el diario del nodo —cero peticiones de imagen lo
// alcanzaban mientras target=_blank apuntaba a una URL http:// en Safari en
// iOS— y ADR-0053 lo documenta con la evidencia completa. Degradado a la
// misma pestaña, igual que el resto; no roto.
func TestNingunaImagenAbreEnPestanaNuevaSobreHTTP(t *testing.T) {
	s, a := servidorDeApertura(t)
	a.agregar(t, "foto.png", []byte("\x89PNG\r\n\x1a\n"))

	cuerpo := peticionConSesion(t, s, "/").Body.String()
	for linea := range strings.SplitSeq(cuerpo, "\n") {
		if !strings.Contains(linea, "foto.png") {
			continue
		}
		if strings.Contains(linea, `target="_blank"`) {
			t.Error("una imagen abre en pestaña nueva sobre HTTP: eso es justo lo que Safari en iOS bloquea (ADR-0053)")
		}
	}
	if !strings.Contains(cuerpo, `href="/abrir/foto.png">foto.png`) {
		t.Fatalf("el enlace de la imagen no navega en la misma pestaña sobre HTTP: %s", cuerpo)
	}
}

// RF-25: un tipo que el navegador no puede mostrar termina en el mensaje
// acordado y en NADA más. Ni descarga de respaldo, ni redirección.
func TestTipoNoAbribleTerminaEnElMensajeYNadaMas(t *testing.T) {
	s, a := servidorDeApertura(t)
	a.agregar(t, "paquete.zip", []byte("PK\x03\x04contenido"))

	for _, extremo := range []string{"/abrir/paquete.zip", "/contenido/paquete.zip"} {
		w := peticionConSesion(t, s, extremo)
		if w.Code != http.StatusUnsupportedMediaType {
			t.Errorf("GET %s -> %d; se esperaba 415", extremo, w.Code)
		}
		if !strings.Contains(w.Body.String(), mensajeArchivoNoCompatible) {
			t.Errorf("GET %s no muestra el mensaje de RF-25", extremo)
		}
		if w.Header().Get("Content-Disposition") != "" {
			t.Errorf("GET %s sugiere una descarga", extremo)
		}
		if strings.Contains(w.Body.String(), "/descargar/") {
			t.Errorf("GET %s ofrece la descarga como consuelo", extremo)
		}
		if strings.Contains(w.Body.String(), "PK\x03\x04contenido") {
			t.Errorf("GET %s filtró los bytes del archivo", extremo)
		}
	}

	// Y no llegó a abrir el almacén ninguna de las dos veces: la lista
	// positiva decide ANTES de tocar el disco.
	if a.aperturas != 0 {
		t.Errorf("una extensión fuera de la lista abrió el almacén %d vez/veces", a.aperturas)
	}
}

// El «Ok» del mensaje solo sale de él: lleva al listado de la carpeta y no
// dispara ninguna otra acción.
func TestElBotonOkSoloVuelveAlListado(t *testing.T) {
	s, a := servidorDeApertura(t)
	a.agregar(t, "carpeta/paquete.zip", nil)

	cuerpo := peticionConSesion(t, s, "/abrir/carpeta/paquete.zip").Body.String()
	if !strings.Contains(cuerpo, `href="/ver/carpeta"`) {
		t.Fatalf("el mensaje no ofrece la vuelta al listado de la carpeta: %s", cuerpo)
	}
	if strings.Count(cuerpo, "<a ") != 1 {
		t.Errorf("el mensaje ofrece más de una acción; RF-25 pide únicamente «Ok»")
	}
}

// Lo abrible se sirve EN LÍNEA, con el MIME de la lista y sin dejar que el
// navegador lo reinterprete.
func TestElContenidoAbribleSeSirveEnLinea(t *testing.T) {
	s, a := servidorDeApertura(t)
	datos := []byte("hola\n")
	a.agregar(t, "notas.txt", datos)

	w := peticionConSesion(t, s, "/contenido/notas.txt")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /contenido/notas.txt -> %d", w.Code)
	}
	if w.Body.String() != string(datos) {
		t.Errorf("cuerpo = %q; se esperaba %q", w.Body.String(), datos)
	}
	if ct := w.Header().Get("Content-Type"); ct != "text/plain; charset=utf-8" {
		t.Errorf("Content-Type = %q; sale de la lista, no del contenido", ct)
	}
	if cd := w.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, "inline;") {
		t.Errorf("Content-Disposition = %q; se esperaba inline", cd)
	}
	if w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("falta nosniff: sin él el navegador puede reinterpretar el tipo")
	}
	if w.Header().Get("Cross-Origin-Resource-Policy") != "same-origin" {
		t.Error("falta Cross-Origin-Resource-Policy")
	}
	if csp := w.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "sandbox") {
		t.Errorf("Content-Security-Policy = %q; se esperaba el aislamiento con sandbox", csp)
	}
}

// /descargar NO cambia. Sigue siendo el único que ordena «attachment», y ese
// es ahora todo su cometido.
func TestDescargarSigueOrdenandoAttachment(t *testing.T) {
	s, a := servidorDeApertura(t)
	a.agregar(t, "notas.txt", []byte("hola"))

	w := peticionConSesion(t, s, "/descargar/notas.txt")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /descargar/notas.txt -> %d", w.Code)
	}
	if cd := w.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment;") {
		t.Errorf("Content-Disposition = %q; se esperaba attachment", cd)
	}
}

// El contenido ACTIVO no se sirve en línea por mucho que se pida a mano: no
// está en la lista, y ahí acaba la conversación. Es el control de
// 04_SEGURIDAD.md §4 sobre el vector de XSS almacenado (CWE-79).
func TestElContenidoActivoNoSeSirveEnLinea(t *testing.T) {
	s, a := servidorDeApertura(t)
	for _, nombre := range []string{"pagina.html", "dibujo.svg", "guion.js", "hoja.css", "datos.xml"} {
		a.agregar(t, nombre, []byte("<script>alert('x')</script>"))

		w := peticionConSesion(t, s, "/contenido/"+nombre)
		if w.Code != http.StatusUnsupportedMediaType {
			t.Errorf("GET /contenido/%s -> %d; se esperaba 415", nombre, w.Code)
		}
		if strings.Contains(w.Body.String(), "alert('x')") {
			t.Errorf("GET /contenido/%s filtró contenido ejecutable", nombre)
		}
	}
}

// Un PDF que no lo es termina en el mensaje, no en un visor en blanco: el
// <object> nativo no avisaría a la página de su propio fallo.
func TestUnPDFSinFirmaTerminaEnElMensaje(t *testing.T) {
	s, a := servidorDeApertura(t)
	a.agregar(t, "falso.pdf", []byte("esto no es un documento PDF"))
	a.agregar(t, "bueno.pdf", []byte("%PDF-1.7\n1 0 obj\n<<>>\nendobj\n"))

	for _, extremo := range []string{"/abrir/falso.pdf", "/contenido/falso.pdf"} {
		w := peticionConSesion(t, s, extremo)
		if w.Code != http.StatusUnsupportedMediaType {
			t.Errorf("GET %s -> %d; se esperaba 415", extremo, w.Code)
		}
		if !strings.Contains(w.Body.String(), mensajeArchivoNoCompatible) {
			t.Errorf("GET %s no muestra el mensaje de RF-25", extremo)
		}
		if strings.Contains(w.Body.String(), "esto no es un documento PDF") {
			t.Errorf("GET %s filtró los bytes del archivo inválido", extremo)
		}
	}

	// Y el PDF legítimo sí pasa: la comprobación separa, no bloquea.
	if w := peticionConSesion(t, s, "/contenido/bueno.pdf"); w.Code != http.StatusOK {
		t.Fatalf("GET /contenido/bueno.pdf -> %d", w.Code)
	}
	// Sin sandbox, y a propósito: ver apertura.go.
	w := peticionConSesion(t, s, "/contenido/bueno.pdf")
	if strings.Contains(w.Header().Get("Content-Security-Policy"), "sandbox") {
		t.Error("el PDF lleva sandbox; eso deja el visor nativo en blanco")
	}
	if w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("el PDF se sirve sin nosniff")
	}
}

// Los rangos de RFC 7233 siguen vivos en /contenido, y no es un extra: sin
// ellos no se puede saltar dentro de un vídeo.
func TestElContenidoAdmiteRangos(t *testing.T) {
	s, a := servidorDeApertura(t)
	a.agregar(t, "clip.mp4", []byte("0123456789"))

	cookie, _ := sesionAbierta(t, s)
	r := httptest.NewRequest(http.MethodGet, "/contenido/clip.mp4", nil)
	r.AddCookie(cookie)
	r.Header.Set("Range", "bytes=2-5")
	w := httptest.NewRecorder()
	s.Rutas().ServeHTTP(w, r)

	if w.Code != http.StatusPartialContent {
		t.Fatalf("con Range -> %d; se esperaba 206", w.Code)
	}
	if w.Body.String() != "2345" {
		t.Errorf("cuerpo = %q; se esperaba %q", w.Body.String(), "2345")
	}
}

// Sin sesión, los dos extremos nuevos responden como el resto (RF-15). Se
// comprueba aquí y no solo en sesion_test.go porque una ruta nueva registrada
// fuera de «protegido» es un fallo que no se ve mirando el código.
func TestLosExtremosNuevosExigenSesion(t *testing.T) {
	s, a := servidorDeApertura(t)
	a.agregar(t, "notas.txt", []byte("hola"))

	for _, ruta := range []string{"/abrir/notas.txt", "/contenido/notas.txt"} {
		w := httptest.NewRecorder()
		s.Rutas().ServeHTTP(w, httptest.NewRequest(http.MethodGet, ruta, nil))
		if w.Code != http.StatusUnauthorized && w.Code != http.StatusForbidden {
			t.Errorf("GET %s sin sesión -> %d; se esperaba 401 o 403", ruta, w.Code)
		}
	}
}

// El visor NO puede ofrecer «/ver/<archivo>»: es la ruta del listado, y sobre
// algo que no es una carpeta responde 400. Salió al mirar la página real, no
// las aserciones: la ascendencia de una ruta se incluye a sí misma, así que
// unas migas construidas con ella terminaban en un enlace roto.
func TestElVisorNoEnlazaElListadoDeUnArchivo(t *testing.T) {
	s, a := servidorDeApertura(t)
	a.agregar(t, "carpeta/hoja.txt", []byte("dentro\n"))

	cuerpo := peticionConSesion(t, s, "/abrir/carpeta/hoja.txt").Body.String()
	if strings.Contains(cuerpo, `href="/ver/carpeta/hoja.txt"`) {
		t.Error("el visor enlaza el listado del propio archivo, que responde 400")
	}
	if !strings.Contains(cuerpo, `href="/ver/carpeta"`) {
		t.Errorf("el visor no ofrece la vuelta a la carpeta contenedora: %s", cuerpo)
	}
}

func TestEscaparRutaURLCodificaPorComponente(t *testing.T) {
	const ruta = "carpeta uno/área 100% #1?.txt"
	const esperada = "carpeta%20uno/%C3%A1rea%20100%25%20%231%3F.txt"
	if got := escaparRutaURL(ruta); got != esperada {
		t.Fatalf("escaparRutaURL(%q) = %q; se esperaba %q", ruta, got, esperada)
	}
}

// El defecto de escapado, extremo a extremo. html/template no escapa «#» ni
// «?» dentro de un href —son sintaxis legítima de URL—, así que sin
// escaparRutaURL el enlace de este archivo apuntaba a «nota » y el resto se
// convertía en fragmento y consulta.
func TestUnNombreConCaracteresReservadosSigueSiendoAlcanzable(t *testing.T) {
	s, a := servidorDeApertura(t)
	const nombre = "nota #1?.txt"
	datos := []byte("alcanzable\n")
	ruta := a.agregar(t, nombre, datos)
	codificada := escaparRutaURL(ruta.Rel())

	cuerpo := peticionConSesion(t, s, "/").Body.String()
	for _, fragmento := range []string{
		`href="/abrir/` + codificada + `"`,
		`action="/descargar/` + codificada + `"`,
		`href="/borrar/` + codificada + `"`,
	} {
		if !strings.Contains(cuerpo, fragmento) {
			t.Errorf("el listado no contiene %q", fragmento)
		}
	}

	w := peticionConSesion(t, s, "/abrir/"+codificada)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /abrir codificado -> %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), `src="/contenido/`+codificada+`"`) {
		t.Errorf("el visor no conservó la ruta codificada: %s", w.Body.String())
	}

	w = peticionConSesion(t, s, "/contenido/"+codificada)
	if w.Code != http.StatusOK || w.Body.String() != string(datos) {
		t.Fatalf("GET /contenido codificado -> %d, %q", w.Code, w.Body.String())
	}
}

// El visor de cada clase dibuja el elemento que le toca, y el mensaje queda
// oculto hasta que el navegador diga que no puede.
func TestCadaClaseDibujaSuElemento(t *testing.T) {
	s, a := servidorDeApertura(t)
	casos := []struct {
		nombre, elemento string
		datos            []byte
	}{
		{"foto.png", "<img ", []byte("\x89PNG\r\n\x1a\n")},
		{"clip.mp4", "<video ", []byte("x")},
		{"cancion.mp3", "<audio ", []byte("x")},
		{"manual.pdf", "<object ", []byte("%PDF-1.7\n")},
		{"notas.txt", "<iframe ", []byte("hola")},
	}
	for _, c := range casos {
		a.agregar(t, c.nombre, c.datos)

		w := peticionConSesion(t, s, "/abrir/"+c.nombre)
		if w.Code != http.StatusOK {
			t.Errorf("GET /abrir/%s -> %d", c.nombre, w.Code)
			continue
		}
		if !strings.Contains(w.Body.String(), c.elemento) {
			t.Errorf("/abrir/%s no dibuja %s", c.nombre, c.elemento)
		}
		if !strings.Contains(w.Body.String(), `id="no-compatible" hidden`) {
			t.Errorf("/abrir/%s deja el mensaje de RF-25 a la vista", c.nombre)
		}
	}
}
