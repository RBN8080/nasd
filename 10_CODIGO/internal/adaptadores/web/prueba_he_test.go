package web

import (
	"net/http"
	"testing"
)

// La ruta temporal de Hurricane Electric (prueba_he.go): se sirve sin sesión,
// con el texto exacto, y no abre nada más.
func TestPruebaHESeSirveSinSesion(t *testing.T) {
	s := servidorConAuth(t)

	w := pedir(t, s, "GET", rutaPruebaHE)
	if w.Code != http.StatusOK {
		t.Fatalf("GET %s sin sesión -> %d, se esperaba 200", rutaPruebaHE, w.Code)
	}
	if got := w.Body.String(); got != contenidoPruebaHE {
		t.Errorf("cuerpo %q, se esperaba %q", got, contenidoPruebaHE)
	}
	if ct := w.Header().Get("Content-Type"); ct != "text/plain; charset=utf-8" {
		t.Errorf("Content-Type %q", ct)
	}
	if w.Header().Get("Set-Cookie") != "" {
		t.Error("la ruta pública no debe abrir sesión")
	}
}

func TestPruebaHENoAbreNadaMas(t *testing.T) {
	s := servidorConAuth(t)
	opaco := observar(t, s, "GET", "/esto-no-existe-123")

	for _, c := range []struct{ metodo, ruta string }{
		{"POST", rutaPruebaHE},
		{"PUT", rutaPruebaHE},
		{"GET", rutaPruebaHE + "/x"},
		{"GET", "/xdrz46cxb"},
		{"GET", "/otro.txt"},
	} {
		if got := observar(t, s, c.metodo, c.ruta); got != opaco {
			t.Errorf("%s %s -> %d y no la respuesta opaca de siempre", c.metodo, c.ruta, got.estado)
		}
	}
}
