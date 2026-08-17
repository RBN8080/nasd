package web

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"nasd/internal/almacen"
	"nasd/internal/autenticacion"
)

// jpegFalso son los bytes mínimos con forma de JPEG (SOI+EOI) que los
// dobles de esta prueba «escriben» como si fueran la miniatura real. nunca
// se decodifican en Go —eso es exactamente lo que este diseño evita—, así
// que no hace falta que sean una imagen de verdad.
var jpegFalso = []byte{0xFF, 0xD8, 0xFF, 0xD9}

// almacenDeMiniatura es un almacén de memoria propio, distinto del de
// apertura_test.go, porque aquí hace falta que DOS usuarios puedan tener
// contenido DISTINTO bajo el mismo nombre de archivo — es justo lo que
// prueba TestMiniaturaAislaPorUsuario, y almacenPorUsuarioDePrueba
// (sesion_test.go) siempre devuelve el mismo almacén para cualquier
// usuario, a propósito, porque el aislamiento real se prueba contra disco
// en fsposix. Aquí se prueba otra cosa: que la CLAVE DE CACHÉ no mezcle a
// dos usuarios, y para eso hacen falta almacenes que puedan divergir.
type almacenDeMiniatura struct {
	almacenVacio
	contenido map[string][]byte
	entradas  map[string]almacen.Entrada
}

func nuevoAlmacenDeMiniatura() *almacenDeMiniatura {
	return &almacenDeMiniatura{contenido: map[string][]byte{}, entradas: map[string]almacen.Entrada{}}
}

func (a *almacenDeMiniatura) agregar(t *testing.T, nombre string, datos []byte, modificado time.Time) almacen.RutaSegura {
	t.Helper()
	ruta, err := almacen.NuevaRuta(nombre)
	if err != nil {
		t.Fatalf("NuevaRuta(%q): %v", nombre, err)
	}
	a.contenido[ruta.Rel()] = datos
	a.entradas[ruta.Rel()] = almacen.Entrada{
		Ruta: ruta, Nombre: ruta.Nombre(), Tamano: int64(len(datos)), Modificado: modificado,
	}
	return ruta
}

func (a *almacenDeMiniatura) Abrir(_ context.Context, r almacen.RutaSegura) (io.ReadSeekCloser, almacen.Entrada, error) {
	datos, ok := a.contenido[r.Rel()]
	if !ok {
		return nil, almacen.Entrada{}, almacen.ErrNoExiste
	}
	return lectorDeMemoria{bytes.NewReader(datos)}, a.entradas[r.Rel()], nil
}

// servidorDeMiniatura arma un servidor con una carpeta de caché de
// miniaturas temporal y UN ALMACÉN POR USUARIO — a diferencia de
// servidorDeApertura, que reutiliza el mismo para todos porque a esa
// batería de pruebas no le importa la diferencia.
//
// «doble» sustituye ejecutarNasMiniatura mientras dure la prueba
// (t.Cleanup lo restaura): es la costura documentada en miniatura.go, y
// existe precisamente para no depender de un binario ARM64 en este host.
// Con doble==nil se conserva el ejecutor real, que en un host de pruebas
// simplemente no encontrará /usr/local/bin/nas-miniatura y fallará —vale
// para probar ESE camino de error sin necesidad de un doble.
func servidorDeMiniatura(t *testing.T, doble func(ctx context.Context, entrada, salida string) error) (*Servidor, map[string]*almacenDeMiniatura) {
	t.Helper()
	linea, err := autenticacion.Derivar(claveDePrueba, 1000)
	if err != nil {
		t.Fatalf("Derivar: %v", err)
	}

	almacenes := map[string]*almacenDeMiniatura{
		autenticacion.NombreSuperusuario: nuevoAlmacenDeMiniatura(),
		"juan":                           nuevoAlmacenDeMiniatura(),
		"maria":                          nuevoAlmacenDeMiniatura(),
	}

	if doble != nil {
		original := ejecutarNasMiniatura
		ejecutarNasMiniatura = doble
		t.Cleanup(func() { ejecutarNasMiniatura = original })
	}

	s, err := Nuevo(Opciones{
		Almacen: almacenes[autenticacion.NombreSuperusuario],
		AlmacenDe: func(usuario string) (almacen.Almacen, error) {
			return almacenes[usuario], nil
		},
		PromoverUsuario:  promoverUsuarioDePrueba,
		Registro:         slog.New(slog.NewJSONHandler(io.Discard, nil)),
		PlazoInactividad: time.Minute,
		Credencial:       linea,
		Usuarios:         registroDePrueba(t),
		DuracionSesion:   time.Hour,
		Metricas:         metricasDePrueba(t),
		Seguridad:        seguridadDePrueba(t),
		Conexiones:       conexionesDePrueba(t),
		DirMiniaturas:    filepath.Join(t.TempDir(), "miniaturas"),
	})
	if err != nil {
		t.Fatalf("Nuevo: %v", err)
	}
	return s, almacenes
}

func sesionAbiertaPara(t *testing.T, s *Servidor, usuario string) *http.Cookie {
	t.Helper()
	tok, err := s.sesiones.Abrir(usuario)
	if err != nil {
		t.Fatalf("Abrir sesión para %q: %v", usuario, err)
	}
	return &http.Cookie{Name: nombreCookie, Value: tok}
}

func peticionMiniatura(s *Servidor, cookie *http.Cookie, ruta string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, ruta, nil)
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	s.Rutas().ServeHTTP(w, r)
	return w
}

// Sin sesión, /miniatura responde como el resto de rutas protegidas — la
// misma comprobación que TestLosExtremosNuevosExigenSesion hace para
// /abrir y /contenido, y por el mismo motivo: una ruta nueva registrada
// fuera de «protegido» es un fallo que no se ve mirando el código.
func TestMiniaturaExigeSesion(t *testing.T) {
	s, almacenes := servidorDeMiniatura(t, nil)
	almacenes[autenticacion.NombreSuperusuario].agregar(t, "foto.jpg", []byte("x"), time.Now())

	w := peticionMiniatura(s, nil, "/miniatura/foto.jpg")
	if w.Code != http.StatusUnauthorized && w.Code != http.StatusForbidden {
		t.Errorf("GET /miniatura sin sesión -> %d; se esperaba 401 o 403", w.Code)
	}
}

// Un archivo que no es imagen —o que ni siquiera está en la lista positiva
// de apertura.go— nunca llega a invocar el subproceso. Mismo criterio que
// TestTipoNoAbribleTerminaEnElMensajeYNadaMas: la lista decide ANTES de
// tocar nada.
func TestMiniaturaSoloParaImagenes(t *testing.T) {
	llamadas := 0
	doble := func(context.Context, string, string) error {
		llamadas++
		return os.WriteFile("no-deberia-escribirse", jpegFalso, 0o600)
	}
	s, almacenes := servidorDeMiniatura(t, doble)
	almacenes[autenticacion.NombreSuperusuario].agregar(t, "paquete.zip", []byte("PK"), time.Now())
	almacenes[autenticacion.NombreSuperusuario].agregar(t, "notas.txt", []byte("hola"), time.Now())
	cookie := sesionAbiertaPara(t, s, autenticacion.NombreSuperusuario)

	for _, nombre := range []string{"paquete.zip", "notas.txt", "no-existe.jpg"} {
		w := peticionMiniatura(s, cookie, "/miniatura/"+nombre)
		if w.Code != http.StatusNotFound {
			t.Errorf("GET /miniatura/%s -> %d; se esperaba 404", nombre, w.Code)
		}
	}
	if llamadas != 0 {
		t.Errorf("nas-miniatura se invocó %d vez/veces para archivos que no debían tocarlo", llamadas)
	}
}

// La primera petición genera y cachea; la segunda sirve de la caché SIN
// volver a invocar el subproceso. Es la propiedad que hace viable abrir una
// carpeta con cientos de fotos.
func TestMiniaturaSirveDesdeCacheTrasGenerar(t *testing.T) {
	var llamadas atomic.Int32
	doble := func(_ context.Context, _, rutaSalida string) error {
		llamadas.Add(1)
		return os.WriteFile(rutaSalida, jpegFalso, 0o600)
	}
	s, almacenes := servidorDeMiniatura(t, doble)
	almacenes[autenticacion.NombreSuperusuario].agregar(t, "foto.jpg", []byte("contenido de la foto"), time.Now())
	cookie := sesionAbiertaPara(t, s, autenticacion.NombreSuperusuario)

	w1 := peticionMiniatura(s, cookie, "/miniatura/foto.jpg")
	if w1.Code != http.StatusOK {
		t.Fatalf("primera petición -> %d; se esperaba 200", w1.Code)
	}
	if !bytes.Equal(w1.Body.Bytes(), jpegFalso) {
		t.Errorf("cuerpo = %v; se esperaba %v", w1.Body.Bytes(), jpegFalso)
	}
	if ct := w1.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Errorf("Content-Type = %q; se esperaba image/jpeg", ct)
	}

	w2 := peticionMiniatura(s, cookie, "/miniatura/foto.jpg")
	if w2.Code != http.StatusOK || !bytes.Equal(w2.Body.Bytes(), jpegFalso) {
		t.Fatalf("segunda petición -> %d, %v", w2.Code, w2.Body.Bytes())
	}

	if got := llamadas.Load(); got != 1 {
		t.Errorf("nas-miniatura se invocó %d veces; se esperaba exactamente 1 (la segunda debía servir de caché)", got)
	}
}

// Cuando la imagen no trae miniatura aprovechable (código 1 de
// nas-miniatura, ver errSinMiniatura), la ruta responde 404 —no es un
// error, es un JPEG válido sin miniatura— y una segunda petición
// INMEDIATA no vuelve a lanzar el subproceso: es fallosMiniatura, el
// caché negativo, evitando que un archivo sin miniatura dispare un
// proceso en cada recarga de su carpeta.
func TestMiniaturaSinMiniaturaAprovechableNoReintentaEnseguida(t *testing.T) {
	var llamadas atomic.Int32
	doble := func(context.Context, string, string) error {
		llamadas.Add(1)
		return errSinMiniatura
	}
	s, almacenes := servidorDeMiniatura(t, doble)
	almacenes[autenticacion.NombreSuperusuario].agregar(t, "sin-exif.jpg", []byte("x"), time.Now())
	cookie := sesionAbiertaPara(t, s, autenticacion.NombreSuperusuario)

	for i := range 3 {
		w := peticionMiniatura(s, cookie, "/miniatura/sin-exif.jpg")
		if w.Code != http.StatusNotFound {
			t.Errorf("petición %d -> %d; se esperaba 404", i, w.Code)
		}
	}
	if got := llamadas.Load(); got != 1 {
		t.Errorf("nas-miniatura se invocó %d veces en 3 peticiones seguidas; se esperaba 1 (caché negativo)", got)
	}
}

// EL CASO CRÍTICO DE SEGURIDAD: dos usuarios con un archivo del MISMO
// nombre, MISMO tamaño y MISMA fecha de modificación —fabricado a
// propósito para que coincida todo salvo el usuario— no comparten hueco de
// caché. Sin el usuario en claveMiniatura, «maria» vería la miniatura de
// «juan» servida desde caché sin que nas-miniatura se ejecutara ni una vez
// para ella.
func TestMiniaturaAislaPorUsuario(t *testing.T) {
	var llamadas atomic.Int32
	doble := func(_ context.Context, _, rutaSalida string) error {
		llamadas.Add(1)
		return os.WriteFile(rutaSalida, jpegFalso, 0o600)
	}
	s, almacenes := servidorDeMiniatura(t, doble)

	// Misma fecha exacta a propósito: es el caso que de verdad probaría si
	// la clave dependiera solo de ruta+mtime+tamaño.
	momento := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	almacenes["juan"].agregar(t, "foto.jpg", []byte("la foto de juan, 11 bytes"), momento)
	almacenes["maria"].agregar(t, "foto.jpg", []byte("la foto de maria, 12 byte"), momento)
	if len(almacenes["juan"].contenido["foto.jpg"]) != len(almacenes["maria"].contenido["foto.jpg"]) {
		t.Fatalf("el experimento exige el MISMO tamaño para juan y maria; ajustar los literales")
	}

	cJuan := sesionAbiertaPara(t, s, "juan")
	cMaria := sesionAbiertaPara(t, s, "maria")

	wJuan := peticionMiniatura(s, cJuan, "/miniatura/foto.jpg")
	wMaria := peticionMiniatura(s, cMaria, "/miniatura/foto.jpg")

	if wJuan.Code != http.StatusOK || wMaria.Code != http.StatusOK {
		t.Fatalf("juan -> %d, maria -> %d; se esperaba 200 en los dos", wJuan.Code, wMaria.Code)
	}
	if got := llamadas.Load(); got != 2 {
		t.Errorf("nas-miniatura se invocó %d veces para dos usuarios distintos; se esperaban 2 —"+
			"si sale 1, la clave de caché está mezclando usuarios", got)
	}
}

// El semáforo de CAPACIDAD 1 (servidor.go) no es un límite arbitrario: con
// MemoryMax=192M para nasd y sus hijos juntos, dos generaciones a la vez
// arriesgan el techo del cgroup. Esta prueba lo comprueba de forma
// determinista, no por temporización: el doble se queda bloqueado hasta
// que el test lo suelta, y mide cuántas invocaciones estuvieron DENTRO del
// semáforo al mismo tiempo.
func TestElSemaforoLimitaLaGeneracionAUnaALaVez(t *testing.T) {
	var enCurso, maximo atomic.Int32
	entrar := make(chan struct{}, 2)
	seguir := make(chan struct{})
	doble := func(_ context.Context, _, rutaSalida string) error {
		n := enCurso.Add(1)
		for {
			m := maximo.Load()
			if n <= m || maximo.CompareAndSwap(m, n) {
				break
			}
		}
		entrar <- struct{}{}
		<-seguir
		enCurso.Add(-1)
		return os.WriteFile(rutaSalida, jpegFalso, 0o600)
	}
	s, almacenes := servidorDeMiniatura(t, doble)
	momento := time.Now()
	// Dos fotos DISTINTAS: si compartieran clave, la segunda serviría de
	// caché sin pasar por el doble, y la prueba no comprobaría nada.
	almacenes[autenticacion.NombreSuperusuario].agregar(t, "una.jpg", []byte("una"), momento)
	almacenes[autenticacion.NombreSuperusuario].agregar(t, "otra.jpg", []byte("otra, mas larga"), momento)
	cookie := sesionAbiertaPara(t, s, autenticacion.NombreSuperusuario)

	var wg sync.WaitGroup
	wg.Add(2)
	for _, nombre := range []string{"una.jpg", "otra.jpg"} {
		go func() {
			defer wg.Done()
			peticionMiniatura(s, cookie, "/miniatura/"+nombre)
		}()
	}

	// Deja que la PRIMERA goroutine entre y se bloquee dentro del
	// semáforo; si el semáforo no limitara nada, la segunda entraría
	// también casi de inmediato.
	<-entrar
	select {
	case <-entrar:
		t.Fatal("una segunda generación entró mientras la primera seguía dentro del semáforo")
	case <-time.After(150 * time.Millisecond):
		// Correcto: la segunda se quedó esperando su turno.
	}
	close(seguir)
	wg.Wait()

	if got := maximo.Load(); got > 1 {
		t.Errorf("concurrencia máxima observada dentro del semáforo = %d; se esperaba 1", got)
	}
}

// Una ruta con «..» la rechaza almacen.NuevaRuta, la MISMA puerta que usan
// /abrir y /contenido — no una comprobación nueva escrita para esta ruta.
func TestMiniaturaRutaConSaltoSeRechaza(t *testing.T) {
	llamadas := 0
	doble := func(context.Context, string, string) error {
		llamadas++
		return errSinMiniatura
	}
	s, _ := servidorDeMiniatura(t, doble)
	cookie := sesionAbiertaPara(t, s, autenticacion.NombreSuperusuario)

	w := peticionMiniatura(s, cookie, "/miniatura/..%2f..%2fetc%2fpasswd")
	if w.Code == http.StatusOK {
		t.Errorf("GET con salto de ruta -> 200; nunca debería servir nada")
	}
	if llamadas != 0 {
		t.Errorf("nas-miniatura se invocó %d vez/veces con una ruta que debía rechazarse antes", llamadas)
	}
}
