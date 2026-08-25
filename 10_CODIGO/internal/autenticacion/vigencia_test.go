package autenticacion

import (
	"testing"
	"time"
)

// Consultar existe para no inventarle un pasado a una cadena que escribe el
// cliente — ADR-0072 §12.
//
// El defecto que cierra: el adaptador web apuntaba «sesión caducada» en cuanto
// la petición traía cookie, con el argumento de que «traía cookie, luego
// estuvo dentro». Cualquiera puede mandar «nas_sesion=loquesea». El panel
// afirmaba una historia que nunca ocurrió.
func TestConsultarSoloAfirmaLoQueElGestorPuedeDemostrar(t *testing.T) {
	t.Run("un testigo que nunca existió es desconocido, no caducado", func(t *testing.T) {
		s := NuevasSesiones(time.Hour, 0)
		if u, v := s.Consultar("me-lo-acabo-de-inventar"); v != Desconocida || u != "" {
			t.Errorf("Consultar de un testigo inventado -> (%q, %v); se esperaba Desconocida", u, v)
		}
	})

	t.Run("el testigo vacío es desconocido", func(t *testing.T) {
		s := NuevasSesiones(time.Hour, 0)
		if _, v := s.Consultar(""); v != Desconocida {
			t.Errorf("Consultar(\"\") -> %v; se esperaba Desconocida", v)
		}
	})

	t.Run("una sesión viva es vigente y dice de quién es", func(t *testing.T) {
		s := NuevasSesiones(time.Hour, 0)
		tok, err := s.Abrir("ana")
		if err != nil {
			t.Fatal(err)
		}
		if u, v := s.Consultar(tok); v != Vigente || u != "ana" {
			t.Errorf("Consultar de una sesión viva -> (%q, %v); se esperaba (\"ana\", Vigente)", u, v)
		}
	})

	t.Run("una sesión que este proceso abrió y venció SÍ es caducada", func(t *testing.T) {
		// Tope absoluto negativo: nace vencida. Es el único caso en el que
		// «caducada» se puede demostrar, porque la entrada está en el mapa.
		s := NuevasSesiones(-time.Second, 0)
		tok, err := s.Abrir("ana")
		if err != nil {
			t.Fatal(err)
		}
		if _, v := s.Consultar(tok); v != Caducada {
			t.Errorf("Consultar de una sesión vencida -> %v; se esperaba Caducada", v)
		}
	})

	t.Run("por inactividad también, que es el otro reloj de ADR-0059", func(t *testing.T) {
		s := NuevasSesiones(time.Hour, time.Millisecond)
		tok, err := s.Abrir("ana")
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
		if _, v := s.Consultar(tok); v != Caducada {
			t.Errorf("Consultar tras la inactividad -> %v; se esperaba Caducada", v)
		}
	})

	// EL LÍMITE, DECLARADO Y COMPROBADO. Consultar borra la entrada vencida al
	// verla, así que la SEGUNDA consulta ya no puede demostrar nada y dice
	// Desconocida. Es correcto —el gestor deja de saberlo de verdad— y está
	// escrito junto al tipo. Se prueba para que nadie lo descubra por sorpresa
	// mirando el panel.
	t.Run("la segunda consulta de una vencida ya no puede demostrar nada", func(t *testing.T) {
		s := NuevasSesiones(-time.Second, 0)
		tok, err := s.Abrir("ana")
		if err != nil {
			t.Fatal(err)
		}
		if _, v := s.Consultar(tok); v != Caducada {
			t.Fatalf("la primera consulta -> %v; se esperaba Caducada", v)
		}
		if _, v := s.Consultar(tok); v != Desconocida {
			t.Errorf("la segunda consulta -> %v; ya no consta, así que Desconocida", v)
		}
	})
}

// Usuario() es ahora la vista corta de Consultar. Se comprueba que las dos
// cuentan lo mismo: si divergieran, habría dos formas de decidir si una sesión
// vale y una de ellas se quedaría vieja.
func TestUsuarioYConsultarNoPuedenDiscrepar(t *testing.T) {
	s := NuevasSesiones(time.Hour, 0)
	viva, err := s.Abrir("ana")
	if err != nil {
		t.Fatal(err)
	}
	for _, tok := range []string{viva, "inventado", ""} {
		u1, ok := s.Usuario(tok)
		u2, v := s.Consultar(tok)
		if ok != (v == Vigente) || u1 != u2 {
			t.Errorf("testigo %q: Usuario dice (%q,%v) y Consultar dice (%q,%v)", tok, u1, ok, u2, v)
		}
	}
}
