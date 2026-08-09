package web

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nasd/internal/almacen"
	"nasd/internal/autenticacion"
	"nasd/internal/metricas"
)

const claveDePrueba = "contraseña-de-prueba-larga"

// iteracionesDePrueba: bajas a propósito. Aquí se prueba el control de acceso,
// no el coste del KDF, que está medido en el nodo (3.6 s por verificación).
const iteracionesDePrueba = 1000

// registroDePrueba devuelve un registro vacío respaldado por un archivo
// temporal, que es lo que Nuevo exige desde ADR-0055.
func registroDePrueba(t *testing.T) *autenticacion.Registro {
	t.Helper()
	reg, err := autenticacion.CargarRegistro(
		filepath.Join(t.TempDir(), "usuarios"), iteracionesDePrueba)
	if err != nil {
		t.Fatalf("CargarRegistro: %v", err)
	}
	return reg
}

// metricasDePrueba devuelve un registro de métricas vacío respaldado por un
// archivo temporal, que es lo que Nuevo exige desde P-4 etapa 3.
func metricasDePrueba(t *testing.T) *metricas.Registro {
	t.Helper()
	reg, err := metricas.CargarRegistro(filepath.Join(t.TempDir(), "uso-disco"))
	if err != nil {
		t.Fatalf("metricas.CargarRegistro: %v", err)
	}
	return reg
}

// almacenPorUsuarioDePrueba entrega SIEMPRE el mismo almacén.
//
// Vale porque lo que se comprueba en este paquete es el reparto —quién recibe
// un almacén y quién no—, no el aislamiento en sí: ese vive en fsposix, se
// mide contra disco de verdad y tiene sus propias pruebas
// (fsposix/aislamiento_test.go).
func almacenPorUsuarioDePrueba(a almacen.Almacen) func(string) (almacen.Almacen, error) {
	return func(string) (almacen.Almacen, error) { return a, nil }
}

// promoverUsuarioDePrueba es un no-op: los paquetes que no prueban el panel
// de administración no necesitan que esto haga nada, solo que exista —Nuevo
// la exige desde la corrección del 06/08 (etapa 2, promoción al dar de baja).
func promoverUsuarioDePrueba(string) error { return nil }

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
		AlmacenDe:        almacenPorUsuarioDePrueba(almacenVacio{}),
		PromoverUsuario:  promoverUsuarioDePrueba,
		Registro:         slog.New(slog.NewJSONHandler(io.Discard, nil)),
		PlazoInactividad: time.Minute,
		Credencial:       linea,
		Usuarios:         registroDePrueba(t),
		DuracionSesion:   time.Hour,
		Metricas:         metricasDePrueba(t),
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
		// Fase 4: /estado también, y no es un extra. Publica capacidad del
		// disco, temperatura y ritmo de uso; sin TLS (ADR-0018) eso sería
		// reconocimiento gratis para cualquiera en la LAN (ADR-0034).
		{"GET", "/estado"},
		// Y el flujo en vivo de esa misma pantalla (ADR-0051), que publica
		// exactamente lo mismo y encima de forma continua. Dejarlo fuera
		// habría abierto por la puerta de al lado lo que la fila de arriba
		// cierra — que es la clase de asimetría que D-21 obliga a comprobar
		// vía por vía en lugar de darla por hecha.
		{"GET", "/estado/flujo"},
		// Y el del panel de administración, por el mismo motivo (P-7,
		// ADR-0056): publica quién tiene sesión abierta ahora mismo.
		{"GET", "/administracion/flujo"},
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
		strings.NewReader(url.Values{
			"usuario": {autenticacion.NombreSuperusuario},
			"clave":   {claveDePrueba},
		}.Encode()))
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
		strings.NewReader(url.Values{
			"usuario": {autenticacion.NombreSuperusuario},
			"clave":   {"equivocada-y-larga"},
		}.Encode()))
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
			strings.NewReader(url.Values{
				"usuario": {autenticacion.NombreSuperusuario},
				"clave":   {"mal-pero-larga-aqui"},
			}.Encode()))
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
		strings.NewReader(url.Values{
			"usuario": {autenticacion.NombreSuperusuario},
			"clave":   {claveDePrueba},
		}.Encode()))
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
			Almacen:         almacenVacio{},
			AlmacenDe:       almacenPorUsuarioDePrueba(almacenVacio{}),
			PromoverUsuario: promoverUsuarioDePrueba,
			Registro:        slog.New(slog.NewJSONHandler(io.Discard, nil)),
			Credencial:      mala,
			Usuarios:        registroDePrueba(t),
			DuracionSesion:  time.Hour,
			Metricas:        metricasDePrueba(t),
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

	tok, _ := s.sesiones.Abrir(autenticacion.NombreSuperusuario)
	cookie := &http.Cookie{Name: nombreCookie, Value: tok}

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/salir", nil)
	r.AddCookie(cookie)
	h.ServeHTTP(w, r)

	if s.sesiones.Valida(tok) {
		t.Fatal("el testigo sigue siendo válido en el servidor tras salir")
	}
}
