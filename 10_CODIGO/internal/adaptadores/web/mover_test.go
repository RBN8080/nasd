package web

import (
	"context"
	"io"
	"iter"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"nasd/internal/almacen"
	"nasd/internal/autenticacion"
)

// almacenDeMover es un árbol en memoria: lo que se prueba aquí es la capa web
// —qué destino se compone, qué se lista y adónde se vuelve—, no el rename(2),
// que ya tiene sus pruebas en fsposix/administracion_test.go.
type almacenDeMover struct {
	almacenVacio
	// hijos mapea cada directorio a sus entradas inmediatas.
	hijos map[string][]almacen.Entrada
	// movimientos guarda lo que se pidió mover, en orden. Es la única forma
	// de comprobar desde fuera QUÉ destino compuso el manejador.
	movimientos [][2]string
	// choque, si no está vacío, hace fallar el movimiento hacia esa ruta con
	// ErrYaExiste, que es lo que hace el almacén real por RF-23.
	choque string
}

func (a *almacenDeMover) Listar(_ context.Context, r almacen.RutaSegura) iter.Seq2[almacen.Entrada, error] {
	return func(yield func(almacen.Entrada, error) bool) {
		for _, e := range a.hijos[r.Rel()] {
			if !yield(e, nil) {
				return
			}
		}
	}
}

// CrearDirectorio acepta siempre: aquí se prueba ADÓNDE se vuelve tras
// crearla, no la creación en sí —eso es de fsposix—.
func (a *almacenDeMover) CrearDirectorio(context.Context, almacen.RutaSegura) error {
	return nil
}

// Resumen devuelve un conteo fijo: lo usa la pantalla de confirmar borrado.
func (a *almacenDeMover) Resumen(_ context.Context, r almacen.RutaSegura) (almacen.Conteo, error) {
	for _, e := range a.hijos[r.Padre().Rel()] {
		if e.Ruta.Rel() == r.Rel() {
			return almacen.Conteo{
				EsDirectorio: e.EsDirectori,
				Archivos:     1,
				Bytes:        206158430,
			}, nil
		}
	}
	return almacen.Conteo{}, almacen.ErrNoExiste
}

func (a *almacenDeMover) Renombrar(_ context.Context, origen, destino almacen.RutaSegura) error {
	a.movimientos = append(a.movimientos, [2]string{origen.Rel(), destino.Rel()})
	if a.choque != "" && destino.Rel() == a.choque {
		return almacen.ErrYaExiste
	}
	return nil
}

// agregar cuelga una entrada de su directorio padre.
func (a *almacenDeMover) agregar(t *testing.T, ruta string, esDir bool) almacen.RutaSegura {
	t.Helper()
	r, err := almacen.NuevaRuta(ruta)
	if err != nil {
		t.Fatalf("NuevaRuta(%q): %v", ruta, err)
	}
	if a.hijos == nil {
		a.hijos = map[string][]almacen.Entrada{}
	}
	padre := r.Padre().Rel()
	a.hijos[padre] = append(a.hijos[padre], almacen.Entrada{
		Ruta: r, Nombre: r.Nombre(), EsDirectori: esDir,
	})
	return r
}

func servidorDeMover(t *testing.T) (*Servidor, *almacenDeMover) {
	t.Helper()
	linea, err := autenticacion.Derivar(claveDePrueba, 1000)
	if err != nil {
		t.Fatalf("Derivar: %v", err)
	}
	a := &almacenDeMover{}
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

func postConSesion(t *testing.T, s *Servidor, ruta string, campos url.Values) *httptest.ResponseRecorder {
	t.Helper()
	cookie, csrf := sesionAbierta(t, s)
	campos.Set("csrf", csrf)
	r := httptest.NewRequest(http.MethodPost, ruta, strings.NewReader(campos.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	s.Rutas().ServeHTTP(w, r)
	return w
}

// EL DEFECTO DE ORIGEN, fijado para que no vuelva (ADR-0054).
//
// El listado ofrecía un campo de texto donde se escribía el destino, y el
// servidor decidía si era mover o renombrar según llevara una barra. El
// responsable escribió el nombre de la carpeta destino —lo natural— y obtuvo
// «ya existe un elemento con ese nombre». Mientras exista un campo de texto
// para el destino, el defecto puede volver: se prueba su AUSENCIA.
func TestElListadoNoPideLaRutaDeDestinoEscritaAMano(t *testing.T) {
	s, a := servidorDeMover(t)
	a.agregar(t, "IMG_7539.MOV", false)

	cuerpo := peticionConSesion(t, s, "/").Body.String()

	if strings.Contains(cuerpo, `placeholder="carpeta/destino/`) {
		t.Error("sigue habiendo un campo para teclear la ruta de destino: es " +
			"exactamente lo que ADR-0054 retira, porque pide algo que el " +
			"usuario no puede saber")
	}
	// Y va en un formulario, no en un enlace: en el panel conviven botones y
	// un vínculo suelto se lee como de otra familia.
	if !strings.Contains(cuerpo, `action="/mover/IMG_7539.MOV"`) {
		t.Fatalf("el menú de la fila no lleva a la vista de mover:\n%s", cuerpo)
	}
}

// El destino se compone en el SERVIDOR a partir del origen. Aunque el cliente
// mande un nombre, no se usa: cambiar el nombre es renombrar (RF-16) y tiene
// su propio gesto. Mezclarlos otra vez repondría el defecto.
func TestMoverConservaElNombreAunqueElClienteMandeOtro(t *testing.T) {
	s, a := servidorDeMover(t)
	a.agregar(t, "Videos", true)
	a.agregar(t, "IMG_7539.MOV", false)

	w := postConSesion(t, s, "/mover", url.Values{
		"origen":  {"IMG_7539.MOV"},
		"carpeta": {"Videos"},
		// Campos que el manejador debe ignorar por completo.
		"destino": {"otro-nombre.MOV"},
		"nombre":  {"otro-nombre.MOV"},
	})

	if w.Code != http.StatusSeeOther {
		t.Fatalf("POST /mover -> %d; se esperaba 303", w.Code)
	}
	if len(a.movimientos) != 1 {
		t.Fatalf("se esperaba un movimiento, hubo %d", len(a.movimientos))
	}
	if got := a.movimientos[0]; got[1] != "Videos/IMG_7539.MOV" {
		t.Errorf("destino compuesto %q; se esperaba «Videos/IMG_7539.MOV»", got[1])
	}
}

// La vista solo puede ofrecer carpetas: un archivo nunca es destino de un
// movimiento, así que enseñarlo sería enseñar algo que no se puede pulsar.
func TestLaVistaDeMoverSoloListaCarpetas(t *testing.T) {
	s, a := servidorDeMover(t)
	a.agregar(t, "Videos", true)
	a.agregar(t, "notas.txt", false)
	a.agregar(t, "IMG_7539.MOV", false)

	cuerpo := peticionConSesion(t, s, "/mover/IMG_7539.MOV").Body.String()

	if !strings.Contains(cuerpo, "Videos") {
		t.Error("no ofrece la carpeta a la que se podría mover")
	}
	if strings.Contains(cuerpo, "notas.txt") {
		t.Error("lista un archivo como posible destino")
	}
}

// Una carpeta no puede mudarse dentro de sí misma —el almacén lo rechaza con
// ErrDentroDeSiMismo—, así que ofrecerla sería un callejón sin salida. Al
// omitirla, todo su subárbol deja de ser alcanzable desde esta vista.
func TestLaVistaDeMoverNoSeOfreceLaCarpetaQueSeMueve(t *testing.T) {
	s, a := servidorDeMover(t)
	a.agregar(t, "Fotos", true)
	a.agregar(t, "Videos", true)

	cuerpo := peticionConSesion(t, s, "/mover/Fotos").Body.String()

	if strings.Contains(cuerpo, `href="/mover/Fotos?a=Fotos"`) {
		t.Error("ofrece mover «Fotos» dentro de «Fotos»: siempre falla")
	}
	if !strings.Contains(cuerpo, "Videos") {
		t.Error("dejó de ofrecer las demás carpetas")
	}
}

// Tras mover se vuelve a la MISMA vista, ya apuntando al sitio nuevo y con el
// aviso encendido: el responsable pidió ver la confirmación y salir él.
func TestTrasMoverSeVuelveALaVistaConElAvisoEncendido(t *testing.T) {
	s, a := servidorDeMover(t)
	a.agregar(t, "Videos", true)
	a.agregar(t, "IMG_7539.MOV", false)

	w := postConSesion(t, s, "/mover", url.Values{
		"origen": {"IMG_7539.MOV"}, "carpeta": {"Videos"},
	})

	destino := w.Header().Get("Location")
	if !strings.HasPrefix(destino, "/mover/Videos/IMG_7539.MOV") {
		t.Fatalf("volvió a %q; se esperaba la vista de mover del sitio nuevo", destino)
	}
	if !strings.Contains(destino, "ok=1") {
		t.Fatalf("volvió sin encender el aviso: %q", destino)
	}

	cuerpo := peticionConSesion(t, s, destino).Body.String()
	if !strings.Contains(cuerpo, "mensaje exito") {
		t.Error("la vista no dibuja el aviso verde de operación completada")
	}
	// Y «Cerrar» tiene que sacar a la carpeta donde acaba de aterrizar. Es un
	// <form method="get"> y no un enlace: en esta interfaz, lo que se pulsa
	// junto a botones ES un botón, aunque por dentro sea una navegación.
	if !strings.Contains(cuerpo, `action="/ver/Videos"`) {
		t.Errorf("«Cerrar» no lleva a la carpeta destino:\n%s", cuerpo)
	}
}

// EL RESPONSABLE TUVO QUE PEDIR ESTO DOS VECES, sobre dos capturas: primero
// «Mover…» en el menú del listado y luego «Cerrar» aquí. Los dos se habían
// dejado como enlaces y salían en color de vínculo entre botones.
//
// La regla que fija esta prueba: en la fila de acciones de la vista de mover
// NO hay enlaces. Lo que se pulsa ahí es un botón, aunque por dentro sea una
// navegación. No hay tercera vez.
func TestEnLaFilaDeAccionesDeMoverNoHayEnlaces(t *testing.T) {
	s, a := servidorDeMover(t)
	a.agregar(t, "Videos", true)
	a.agregar(t, "notas.txt", false)

	cuerpo := peticionConSesion(t, s, "/mover/notas.txt").Body.String()

	ini := strings.Index(cuerpo, `<div class="acciones-mover">`)
	if ini < 0 {
		t.Fatal("no se encuentra la fila de acciones")
	}
	fila := cuerpo[ini:]
	if fin := strings.Index(fila, "</main>"); fin >= 0 {
		fila = fila[:fin]
	}
	if strings.Contains(fila, "<a ") || strings.Contains(fila, "<a>") {
		t.Errorf("hay un enlace entre los botones de la fila de acciones:\n%s", fila)
	}
	for _, b := range []string{"Mover aquí", "Crear carpeta", "Cerrar"} {
		if !strings.Contains(fila, b) {
			t.Errorf("falta «%s» en la fila de acciones", b)
		}
	}
}

// Un choque de nombres NO expulsa de la vista: es información para seguir
// eligiendo carpeta, no un error terminal. RF-23 sigue mandando — no se
// sobrescribe nada.
func TestUnChoqueDeNombresSeCuentaDentroDeLaVista(t *testing.T) {
	s, a := servidorDeMover(t)
	a.agregar(t, "Videos", true)
	a.agregar(t, "IMG_7539.MOV", false)
	a.choque = "Videos/IMG_7539.MOV"

	w := postConSesion(t, s, "/mover", url.Values{
		"origen": {"IMG_7539.MOV"}, "carpeta": {"Videos"},
	})

	if w.Code != http.StatusSeeOther {
		t.Fatalf("un choque de nombres -> %d; se esperaba volver a la vista", w.Code)
	}
	destino := w.Header().Get("Location")
	if !strings.HasPrefix(destino, "/mover/IMG_7539.MOV") {
		t.Errorf("tras el choque se fue a %q en vez de volver al navegador", destino)
	}
	if !strings.Contains(destino, "err=") {
		t.Errorf("volvió sin explicar por qué no se movió: %q", destino)
	}
}

// Los nombres con «&» son legales y rompen una consulta mal escapada: con
// PathEscape —que es lo que usa escaparURL— el «&» sobrevive sin escapar y
// parte el parámetro, así que la vista abriría otra carpeta o ninguna.
func TestUnaCarpetaConAmpersandNoPierdeLaNavegacion(t *testing.T) {
	_, a := servidorDeMover(t)
	carpeta := a.agregar(t, "Fotos & vídeos", true)
	a.agregar(t, "IMG_7539.MOV", false)

	enlace := urlDeMover(almacen.Raiz(), carpeta)
	if strings.Contains(enlace, "&v") {
		t.Fatalf("el «&» del nombre quedó sin escapar y parte la consulta: %q", enlace)
	}

	// Y el recorrido completo tiene que devolver esa misma carpeta.
	u, err := url.Parse(urlDeMover(almacen.Raiz(), carpeta))
	if err != nil {
		t.Fatalf("la URL construida no es válida: %v", err)
	}
	if got := u.Query().Get("a"); got != "Fotos & vídeos" {
		t.Errorf("al releer la consulta salió %q; se esperaba «Fotos & vídeos»", got)
	}
}

// Crear una carpeta DESDE el navegador de mover devuelve al navegador, no al
// listado: quien está eligiendo destino acaba de fabricarlo.
func TestCrearCarpetaDesdeMoverVuelveAlNavegador(t *testing.T) {
	s, _ := servidorDeMover(t)

	w := postConSesion(t, s, "/directorio", url.Values{
		"destino": {"Videos"},
		"nombre":  {"2026"},
		"mover":   {"IMG_7539.MOV"},
	})

	if got := w.Header().Get("Location"); !strings.HasPrefix(got, "/mover/IMG_7539.MOV") {
		t.Errorf("volvió a %q; se esperaba seguir en el navegador de mover", got)
	}
}

// Sin ese campo, crear carpeta sigue comportándose como siempre.
func TestCrearCarpetaSinMoverSigueVolviendoAlListado(t *testing.T) {
	s, _ := servidorDeMover(t)

	w := postConSesion(t, s, "/directorio", url.Values{
		"destino": {"Videos"}, "nombre": {"2026"},
	})

	if got := w.Header().Get("Location"); !strings.HasPrefix(got, "/ver/Videos") {
		t.Errorf("volvió a %q; se esperaba el listado de la carpeta", got)
	}
}

// /renombrar ya NO mueve: una barra en el destino es un nombre inválido, no
// una ruta que reinterpretar. Es la mitad que cierra ADR-0054.
func TestRenombrarYaNoAceptaUnaRutaComoDestino(t *testing.T) {
	s, a := servidorDeMover(t)
	a.agregar(t, "IMG_7539.MOV", false)

	w := postConSesion(t, s, "/renombrar", url.Values{
		"origen": {"IMG_7539.MOV"}, "destino": {"Videos/IMG_7539.MOV"},
	})

	if w.Code == http.StatusSeeOther {
		t.Fatal("/renombrar sigue aceptando una ruta como destino: esa es la " +
			"ambigüedad que ADR-0054 retira")
	}
	if len(a.movimientos) != 0 {
		t.Errorf("llegó a tocar el almacén con %v", a.movimientos)
	}
}
