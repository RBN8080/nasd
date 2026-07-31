package web

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"nasd/internal/autenticacion"
)

const claveDePrueba = "contraseña-de-prueba-larga"

func servidorConAuth(t *testing.T) *Servidor {
	t.Helper()
	// Iteraciones bajas: aquí se prueba el control de acceso, no el coste
	// del KDF, que está medido en el nodo.
	linea, err := autenticacion.Derivar(claveDePrueba, 1000)
	if err != nil {
		t.Fatalf("Derivar: %v", err)
	}
	s, err := Nuevo(Opciones{
		Almacen:          almacenVacio{},
		Registro:         slog.New(slog.NewJSONHandler(io.Discard, nil)),
		PlazoInactividad: time.Minute,
		Credencial:       linea,
		DuracionSesion:   time.Hour,
	})
	if err != nil {
		t.Fatalf("Nuevo: %v", err)
	}
	return s
}

// RF-15, criterio literal: «sin sesión válida, toda ruta distinta del
// formulario de acceso responde 401 o 403».
func TestSinSesionTodoResponde401(t *testing.T) {
	s := servidorConAuth(t)
	h := s.Rutas()

	rutas := []struct{ metodo, ruta string }{
		{"GET", "/"},
		{"GET", "/ver/fotos"},
		{"GET", "/descargar/x.bin"},
		{"POST", "/subir"},
		{"POST", "/directorio"},
		{"POST", "/subidas"},
		{"HEAD", "/subidas/0123456789abcdef0123456789abcdef"},
		{"PATCH", "/subidas/0123456789abcdef0123456789abcdef"},
	}
	for _, c := range rutas {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(c.metodo, c.ruta, nil))
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s %s -> %d; RF-15 exige 401 o 403", c.metodo, c.ruta, w.Code)
		}
	}
}

// El formulario de acceso y sus assets sí deben ser alcanzables: si no, no
// habría forma de autenticarse.
func TestElFormularioDeAccesoEsAlcanzable(t *testing.T) {
	s := servidorConAuth(t)
	h := s.Rutas()
	for _, ruta := range []string{"/acceso", "/estatico/estilo.css"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", ruta, nil))
		if w.Code != http.StatusOK {
			t.Errorf("GET %s -> %d; se esperaba 200", ruta, w.Code)
		}
	}
}

func TestAccesoCorrectoAbreSesion(t *testing.T) {
	s := servidorConAuth(t)
	h := s.Rutas()

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/acceso",
		strings.NewReader(url.Values{"clave": {claveDePrueba}}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.ServeHTTP(w, r)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("acceso correcto -> %d; se esperaba 303", w.Code)
	}
	var cookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == nombreCookie {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatal("no se entregó la cookie de sesión")
	}
	if !cookie.HttpOnly {
		t.Error("la cookie DEBE ser HttpOnly: si no, el JavaScript la alcanza")
	}
	if cookie.SameSite != http.SameSiteLaxMode {
		t.Error("la cookie debe ser SameSite=Lax para frenar el CSRF")
	}

	// Y con ella, lo protegido deja de dar 401.
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest("POST", "/subidas", nil)
	r2.AddCookie(cookie)
	h.ServeHTTP(w2, r2)
	if w2.Code == http.StatusUnauthorized {
		t.Error("con sesión válida no debería seguir dando 401")
	}
}

func TestAccesoIncorrectoNoAbreSesion(t *testing.T) {
	s := servidorConAuth(t)
	h := s.Rutas()

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/acceso",
		strings.NewReader(url.Values{"clave": {"equivocada-y-larga"}}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Accept", "text/html")
	h.ServeHTTP(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("contraseña incorrecta -> %d; se esperaba 401", w.Code)
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == nombreCookie && c.Value != "" {
			t.Fatal("se entregó una cookie de sesión con la contraseña incorrecta")
		}
	}
}

// El limitador debe cortar la repetición: sin él, con 3.2 s por verificación
// en el nodo, cualquiera en la LAN satura la CPU pidiendo /acceso en bucle.
func TestLimitadorCortaLosIntentosRepetidos(t *testing.T) {
	s := servidorConAuth(t)
	h := s.Rutas()

	intentar := func() int {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/acceso",
			strings.NewReader(url.Values{"clave": {"mal-pero-larga-aqui"}}.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.RemoteAddr = "192.168.1.99:5555"
		h.ServeHTTP(w, r)
		return w.Code
	}
	for i := range maxIntentosFallidos {
		if c := intentar(); c != http.StatusUnauthorized {
			t.Fatalf("intento %d -> %d", i+1, c)
		}
	}
	// El siguiente debe llevar Retry-After.
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/acceso",
		strings.NewReader(url.Values{"clave": {claveDePrueba}}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.RemoteAddr = "192.168.1.99:5555"
	h.ServeHTTP(w, r)
	if w.Header().Get("Retry-After") == "" {
		t.Error("tras superar el tope debía responder con Retry-After")
	}
	// Y la contraseña BUENA tampoco pasa mientras dure el bloqueo.
	if w.Code == http.StatusSeeOther {
		t.Error("el bloqueo no se aplicó: entró con la contraseña correcta")
	}
}

// P5: sin credencial no se arranca. No existe un modo sin autenticar.
func TestSinCredencialNoArranca(t *testing.T) {
	for _, mala := range []string{"", "basura", "md5$1$a$b"} {
		_, err := Nuevo(Opciones{
			Almacen:        almacenVacio{},
			Registro:       slog.New(slog.NewJSONHandler(io.Discard, nil)),
			Credencial:     mala,
			DuracionSesion: time.Hour,
		})
		if err == nil {
			t.Errorf("arrancó con una credencial inválida: %q", mala)
		}
	}
}

// Cerrar sesión debe invalidar el testigo de verdad, no solo borrar la
// cookie del navegador.
func TestSalirInvalidaElTestigoEnElServidor(t *testing.T) {
	s := servidorConAuth(t)
	h := s.Rutas()

	tok, _ := s.sesiones.Abrir()
	cookie := &http.Cookie{Name: nombreCookie, Value: tok}

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/salir", nil)
	r.AddCookie(cookie)
	h.ServeHTTP(w, r)

	if s.sesiones.Valida(tok) {
		t.Fatal("el testigo sigue siendo válido en el servidor tras salir")
	}
}
