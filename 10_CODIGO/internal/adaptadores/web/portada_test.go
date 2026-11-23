package web

import (
	"net/http"
	"testing"
	"time"
)

// Sin sesión, la portada pedida desde casa o por el túnel lleva al formulario
// de acceso (2026-11-23). Desde Internet, y en cualquier otra ruta, sigue el
// 404 opaco de ADR-0072 y ADR-0088.

func TestLaPortadaDesdeCasaLlevaAlAcceso(t *testing.T) {
	s := servidorConAuth(t)
	h := s.Rutas()
	for _, c := range []struct{ nombre, origen string }{
		{"la LAN", "192.168.1.18:5000"},
		{"el túnel", "10.77.0.3:51820"},
		{"el propio nodo", "127.0.0.1:40000"},
	} {
		for _, metodo := range []string{http.MethodGet, http.MethodHead} {
			t.Run(c.nombre+" "+metodo, func(t *testing.T) {
				w := pedirDesde(t, h, metodo, "/", c.origen, nil)
				if w.Code != http.StatusSeeOther {
					t.Fatalf("%s / desde %s -> %d; se esperaba 303", metodo, c.nombre, w.Code)
				}
				if got := w.Header().Get("Location"); got != "/acceso" {
					t.Errorf("Location = %q; se esperaba /acceso", got)
				}
				if got := w.Header().Get("Cache-Control"); got != "no-store" {
					t.Errorf("Cache-Control = %q; una redirección cacheada seguiría tras entrar", got)
				}
			})
		}
	}
}

// Una sesión caducada o inventada es el caso real del iPhone: también lleva
// al formulario. Y no cuenta como rechazo en el panel: nadie fue rechazado.
func TestLaPortadaDesdeCasaConSesionInvalidaLlevaAlAcceso(t *testing.T) {
	s := servidorConAuth(t)
	w := pedirDesde(t, s.Rutas(), http.MethodGet, "/", "10.77.0.3:51820",
		&http.Cookie{Name: nombreCookie, Value: "inventada"})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("GET / con cookie inválida desde el túnel -> %d; se esperaba 303", w.Code)
	}
	if ev := s.seguridad.Desde(time.Time{}); len(ev) != 0 {
		t.Errorf("se anotaron %d rechazos; llevar al acceso no es rechazar", len(ev))
	}
}

func TestLaExcepcionSoloEsLaPortadaYSoloDesdeCasa(t *testing.T) {
	s := servidorConAuth(t)
	h := s.Rutas()
	for _, c := range []struct{ nombre, metodo, ruta, origen string }{
		{"portada desde Internet", http.MethodGet, "/", "203.0.113.7:5000"},
		{"portada desde IPv6 ajena", http.MethodGet, "/", "[2001:db8::7]:5000"},
		{"origen ilegible", http.MethodGet, "/", "sin-puerto"},
		{"ruta protegida desde casa", http.MethodGet, "/estado", "192.168.1.18:5000"},
		{"ruta inexistente desde casa", http.MethodGet, "/.env", "192.168.1.18:5000"},
		{"otro método sobre la portada", http.MethodPost, "/", "192.168.1.18:5000"},
	} {
		t.Run(c.nombre, func(t *testing.T) {
			w := pedirDesde(t, h, c.metodo, c.ruta, c.origen, nil)
			if w.Code != http.StatusNotFound || w.Header().Get("Location") != "" {
				t.Errorf("%s %s desde %s -> %d (Location %q); se esperaba el 404 opaco",
					c.metodo, c.ruta, c.origen, w.Code, w.Header().Get("Location"))
			}
		})
	}
}
