package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"nasd/internal/autenticacion"
)

// sesionAbierta devuelve la cookie y el testigo CSRF de una sesión real.
func sesionAbierta(t *testing.T, s *Servidor) (*http.Cookie, string) {
	t.Helper()
	tok, err := s.sesiones.Abrir(autenticacion.NombreSuperusuario)
	if err != nil {
		t.Fatalf("Abrir sesión: %v", err)
	}
	csrf, ok := s.sesiones.Csrf(tok)
	if !ok {
		t.Fatal("la sesión recién abierta no tiene testigo CSRF")
	}
	return &http.Cookie{Name: nombreCookie, Value: tok}, csrf
}

func postCon(t *testing.T, h http.Handler, cookie *http.Cookie, ruta string, campos url.Values) int {
	t.Helper()
	r := httptest.NewRequest("POST", ruta, strings.NewReader(campos.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w.Code
}

// Con sesión válida pero SIN testigo CSRF, toda operación que cambia algo
// debe rechazarse. Es la segunda capa sobre SameSite=Lax, y con borrados
// irreversibles y sin papelera (D-15) una sola capa no basta.
func TestSinTestigoCSRFSeRechazaTodoLoQueCambia(t *testing.T) {
	s := servidorConAuth(t)
	h := s.Rutas()
	cookie, _ := sesionAbierta(t, s)

	casos := []struct {
		ruta   string
		campos url.Values
	}{
		{"/borrar", url.Values{"ruta": {"x.txt"}, "confirmado": {"si"}}},
		{"/renombrar", url.Values{"origen": {"a.txt"}, "destino": {"b.txt"}}},
		{"/directorio", url.Values{"destino": {""}, "nombre": {"nueva"}}},
		// Mover dejó de ser parte de /renombrar el 2026-08-06 (ADR-0054), y un
		// extremo nuevo que cambia el disco entra en esta lista el mismo día
		// que nace: si no, la puerta se queda mirando solo lo viejo.
		{"/mover", url.Values{"origen": {"a.txt"}, "carpeta": {"destino"}}},
		// Panel de administración (P-4, etapa 2): no cambia el disco, cambia
		// quién puede entrar, y eso pesa igual o más.
		{"/administracion/alta", url.Values{"nombre": {"nueva"}, "clave": {"contrasena-bastante-larga"}}},
		{"/administracion/baja", url.Values{"nombre": {"juan"}, "clave": {claveDePrueba}}},
	}
	for _, c := range casos {
		if got := postCon(t, h, cookie, c.ruta, c.campos); got != http.StatusForbidden {
			t.Errorf("POST %s sin CSRF -> %d; se esperaba 403", c.ruta, got)
		}
	}
}

// Un testigo de OTRA sesión tampoco vale: si valiera, bastaría con abrir una
// sesión propia para atacar la ajena.
func TestElTestigoDeOtraSesionNoVale(t *testing.T) {
	s := servidorConAuth(t)
	h := s.Rutas()
	cookieA, _ := sesionAbierta(t, s)
	_, csrfB := sesionAbierta(t, s)

	got := postCon(t, h, cookieA, "/borrar",
		url.Values{"csrf": {csrfB}, "ruta": {"x.txt"}, "confirmado": {"si"}})
	if got != http.StatusForbidden {
		t.Errorf("con el CSRF de otra sesión -> %d; se esperaba 403", got)
	}
}

// RF-18: sin haber pasado por la confirmación, no se borra. El campo
// «confirmado» solo lo pone esa pantalla.
func TestSinConfirmacionNoSeBorra(t *testing.T) {
	s := servidorConAuth(t)
	h := s.Rutas()
	cookie, csrf := sesionAbierta(t, s)

	got := postCon(t, h, cookie, "/borrar", url.Values{"csrf": {csrf}, "ruta": {"x.txt"}})
	if got == http.StatusSeeOther {
		t.Fatal("borró sin confirmación explícita: RF-18 exige que ninguna " +
			"acción destructiva se ejecute con un solo clic accidental")
	}
}

// Y la raíz no se borra ni con sesión, ni con testigo, ni con confirmación.
func TestNoSePuedeBorrarLaRaiz(t *testing.T) {
	s := servidorConAuth(t)
	h := s.Rutas()
	cookie, csrf := sesionAbierta(t, s)

	for _, r := range []string{"", ".", "/"} {
		got := postCon(t, h, cookie, "/borrar",
			url.Values{"csrf": {csrf}, "ruta": {r}, "confirmado": {"si"}})
		if got == http.StatusSeeOther {
			t.Errorf("aceptó borrar la raíz con ruta %q", r)
		}
	}
}

// Ahora TODO lo que cambia algo exige testigo, incluidas las subidas.
// Antes solo lo hacían renombrar, borrar y crear carpeta.
func TestLasSubidasTambienExigenCSRF(t *testing.T) {
	s := servidorConAuth(t)
	h := s.Rutas()
	cookie, _ := sesionAbierta(t, s)

	for _, c := range []struct{ metodo, ruta string }{
		{"POST", "/subidas"},
		{"PATCH", "/subidas/0123456789abcdef0123456789abcdef"},
		{"DELETE", "/subidas/0123456789abcdef0123456789abcdef"},
	} {
		r := httptest.NewRequest(c.metodo, c.ruta, nil)
		r.AddCookie(cookie)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusForbidden {
			t.Errorf("%s %s sin CSRF -> %d; se esperaba 403", c.metodo, c.ruta, w.Code)
		}
	}
}

// El testigo se acepta por cabecera, que es como lo manda el cliente
// JavaScript, y ademas es una barrera en si misma: un formulario de otro
// sitio no puede fijar cabeceras propias.
func TestElTestigoValePorCabecera(t *testing.T) {
	s := servidorConAuth(t)
	h := s.Rutas()
	cookie, csrf := sesionAbierta(t, s)

	r := httptest.NewRequest("POST", "/subidas", nil)
	r.AddCookie(cookie)
	r.Header.Set("Nas-Csrf", csrf)
	r.Header.Set("Upload-Length", "10")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code == http.StatusForbidden {
		t.Fatal("rechazó un testigo válido enviado por cabecera")
	}
}

// Regresión de la trampa de ADR-0025: csrfRecibido NO debe disparar el
// análisis multipart, que vuelca a tmpfs, es decir a RAM.
func TestCsrfNoDisparaElAnalisisMultipart(t *testing.T) {
	cuerpo := "--x\r\nContent-Disposition: form-data; name=\"csrf\"\r\n\r\nvalor\r\n--x--\r\n"
	r := httptest.NewRequest("POST", "/borrar", strings.NewReader(cuerpo))
	r.Header.Set("Content-Type", "multipart/form-data; boundary=x")

	_ = csrfRecibido(r)

	if r.MultipartForm != nil {
		t.Fatal("csrfRecibido analizó el multipart: eso vuelca a os.TempDir(), " +
			"que con PrivateTmp=yes es RAM (ADR-0025)")
	}
}
