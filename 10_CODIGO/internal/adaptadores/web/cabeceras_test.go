package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestCabecerasDeSeguridadEnPaginasPropias — R5 del pentest externo del
// 2026-08-13 (ADR-0060): hasta esta versión SOLO /contenido llevaba
// cabeceras de aislamiento (ADR-0052); las páginas propias del NAS —donde se
// da de alta y de baja cuentas— no llevaban ninguna. Se comprueba en dos
// rutas de forma distinta: /acceso sin sesión (la página pública) y / con
// sesión (una página protegida real, no el formulario de acceso).
func TestCabecerasDeSeguridadEnPaginasPropias(t *testing.T) {
	s, _ := servidorDeApertura(t)

	casos := []struct {
		nombre string
		pedir  func() *httptest.ResponseRecorder
	}{
		{"/acceso sin sesión", func() *httptest.ResponseRecorder {
			w := httptest.NewRecorder()
			s.Rutas().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/acceso", nil))
			return w
		}},
		{"/ con sesión", func() *httptest.ResponseRecorder {
			return peticionConSesion(t, s, "/")
		}},
	}

	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			w := c.pedir()

			esperadas := map[string]string{
				"X-Frame-Options":        "DENY",
				"X-Content-Type-Options": "nosniff",
				"Referrer-Policy":        "same-origin",
			}
			for cabecera, esperado := range esperadas {
				if got := w.Header().Get(cabecera); got != esperado {
					t.Errorf("%s = %q; se esperaba %q", cabecera, got, esperado)
				}
			}
			csp := w.Header().Get("Content-Security-Policy")
			for _, campo := range []string{"frame-ancestors 'none'", "object-src 'none'", "script-src 'self'"} {
				if !strings.Contains(csp, campo) {
					t.Errorf("Content-Security-Policy = %q; se esperaba que incluyera %q", csp, campo)
				}
			}
		})
	}
}

// TestVisorDePDFRelajaObjectSrc — la CSP global trae object-src 'none', y
// visor.html es la única página con un <object> (el PDF). Sin esta excepción
// el visor se queda en blanco sin ni siquiera pedir /contenido.
func TestVisorDePDFRelajaObjectSrc(t *testing.T) {
	s, a := servidorDeApertura(t)
	a.agregar(t, "doc.pdf", []byte("%PDF-1.7\n1 0 obj\n<<>>\nendobj\n"))

	w := peticionConSesion(t, s, "/abrir/doc.pdf")
	csp := w.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "object-src 'self'") {
		t.Errorf("Content-Security-Policy = %q; se esperaba object-src 'self' en el visor", csp)
	}
}

// TestContenidoRelajaElFramingASoloMismoOrigen — visor.html embebe
// /contenido en <object> (PDF) e <iframe> (texto) DEL PROPIO ORIGEN. El
// X-Frame-Options: DENY global rompería justo eso si /contenido no lo
// relajara a SAMEORIGIN — que sigue bloqueando a cualquier otro origen.
func TestContenidoRelajaElFramingASoloMismoOrigen(t *testing.T) {
	s, a := servidorDeApertura(t)
	a.agregar(t, "nota.txt", []byte("hola"))

	w := peticionConSesion(t, s, "/contenido/nota.txt")
	if got := w.Header().Get("X-Frame-Options"); got != "SAMEORIGIN" {
		t.Errorf("X-Frame-Options = %q; se esperaba SAMEORIGIN", got)
	}
	if csp := w.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "frame-ancestors 'self'") {
		t.Errorf("Content-Security-Policy = %q; se esperaba frame-ancestors 'self'", csp)
	}
}
