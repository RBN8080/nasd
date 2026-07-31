package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// sesionAbierta devuelve la cookie y el testigo CSRF de una sesión real.
func sesionAbierta(t *testing.T, s *Servidor) (*http.Cookie, string) {
	t.Helper()
	tok, err := s.sesiones.Abrir()
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
