package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"nasd/internal/autenticacion"
)

// Pruebas del panel de administración — P-4, etapa 2 (ADR-0055).
//
// Igual que en acceso_test.go, lo que se mide aquí es el REPARTO y el
// CONTROL de acceso al panel, no el registro de cuentas en sí —Alta y Baja
// tienen sus propias pruebas contra disco real en autenticacion/usuarios_test.go—.

// La regla la aplica soloSuperusuario, la misma envoltura que ya protege
// /estado (TestSoloElSuperusuarioAlcanzaElEstado). Aquí se comprueba que las
// CUATRO rutas nuevas están detrás de ella, no solo la primera.
func TestSoloElSuperusuarioAlcanzaLaAdministracion(t *testing.T) {
	s, _ := servidorMultiusuario(t)
	h := s.Rutas()
	deJuan := cookieLlamada(entrar(t, h, "juan", claveDeJuan), nombreCookie)

	for _, c := range []struct{ metodo, ruta string }{
		{"GET", "/administracion"},
		{"GET", "/administracion/baja/juan"},
	} {
		r := httptest.NewRequest(c.metodo, c.ruta, nil)
		r.AddCookie(deJuan)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusForbidden {
			t.Errorf("%s %s como usuario normal -> %d; se esperaba 403", c.metodo, c.ruta, w.Code)
		}
	}
	// Las POST también, aunque el 403 de soloSuperusuario llegue antes de que
	// exigirCSRF mire el formulario: un usuario normal no debe poder ni
	// intentarlo.
	for _, ruta := range []string{"/administracion/alta", "/administracion/baja"} {
		if got := postCon(t, h, deJuan, ruta, url.Values{}); got != http.StatusForbidden {
			t.Errorf("POST %s como usuario normal -> %d; se esperaba 403", ruta, got)
		}
	}

	// Y al superusuario no se le cierra: la regla separa, no bloquea a todos.
	deAdmin := cookieLlamada(
		entrar(t, h, autenticacion.NombreSuperusuario, claveDePrueba), nombreCookie)
	r := httptest.NewRequest("GET", "/administracion", nil)
	r.AddCookie(deAdmin)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code == http.StatusForbidden {
		t.Error("GET /administracion como superusuario -> 403; le corresponde verlo")
	}
}

// Cortesía, no control —el control es la prueba de arriba—: la barra no
// ofrece un botón que va a responder 403.
func TestLaBarraSoloOfreceAdministracionAlSuperusuario(t *testing.T) {
	s, _ := servidorMultiusuario(t)
	h := s.Rutas()

	deJuan := listadoCon(t, h, cookieLlamada(entrar(t, h, "juan", claveDeJuan), nombreCookie))
	if strings.Contains(deJuan, `href="/administracion"`) {
		t.Error("la barra de un usuario normal ofrece «Administración»")
	}

	deAdmin := listadoCon(t, h, cookieLlamada(
		entrar(t, h, autenticacion.NombreSuperusuario, claveDePrueba), nombreCookie))
	if !strings.Contains(deAdmin, `href="/administracion"`) {
		t.Error("al superusuario le desapareció «Administración» de la barra")
	}
}

// El alta desde el panel es la misma operación que «nasd --crear-usuario»:
// la cuenta queda en el registro compartido, con la contraseña ya derivada.
func TestAltaDeUsuarioDesdeElPanel(t *testing.T) {
	s, _ := servidorMultiusuario(t)

	w := postConSesion(t, s, "/administracion/alta", url.Values{
		"nombre": {"nueva"},
		"clave":  {"contrasena-bastante-larga"},
	})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("alta -> %d; se esperaba 303", w.Code)
	}
	if _, existe := s.usuarios.Buscar("nueva"); !existe {
		t.Error("la cuenta no quedó en el registro tras el alta")
	}
}

// Registro.Alta ya exige 14 caracteres (LongitudMinimaUsuario); el panel no
// debe silenciar ese error ni crear la cuenta de todos modos.
func TestAltaConClaveCortaNoCreaLaCuenta(t *testing.T) {
	s, _ := servidorMultiusuario(t)

	w := postConSesion(t, s, "/administracion/alta", url.Values{
		"nombre": {"nueva"},
		"clave":  {"corta"},
	})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("alta con clave corta -> %d; se esperaba 303 (redirige con el error)", w.Code)
	}
	if loc := w.Header().Get("Location"); !strings.Contains(loc, "err=1") {
		t.Errorf("Location = %q; se esperaba err=1", loc)
	}
	if _, existe := s.usuarios.Buscar("nueva"); existe {
		t.Error("se creó una cuenta con una clave por debajo del mínimo")
	}
}

// El nombre reservado del superusuario tampoco se cuela por aquí —es la
// misma NombreValido que ya prueba autenticacion/usuarios_test.go, pero
// importa comprobar que el panel no la esquiva de camino.
func TestAltaNoAceptaElNombreDelSuperusuario(t *testing.T) {
	s, _ := servidorMultiusuario(t)

	postConSesion(t, s, "/administracion/alta", url.Values{
		"nombre": {autenticacion.NombreSuperusuario},
		"clave":  {"contrasena-bastante-larga"},
	})
	if _, existe := s.usuarios.Buscar(autenticacion.NombreSuperusuario); existe {
		t.Error("se creó una cuenta con el nombre reservado del superusuario")
	}
}

// LA PRUEBA CENTRAL DE LA BAJA: una contraseña de confirmación incorrecta no
// da de baja a nadie, aunque la sesión del superusuario sea válida.
func TestBajaExigeLaContrasenaDelSuperusuarioOtraVez(t *testing.T) {
	s, _ := servidorMultiusuario(t)

	w := postConSesion(t, s, "/administracion/baja", url.Values{
		"nombre": {"juan"},
		"clave":  {"esto-no-es-la-contrasena-correcta"},
	})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("baja con contraseña incorrecta -> %d; se esperaba 303 (vuelve a pedirla)", w.Code)
	}
	if loc := w.Header().Get("Location"); !strings.Contains(loc, "/administracion/baja/juan") {
		t.Errorf("Location = %q; se esperaba volver a la confirmación de juan", loc)
	}
	if _, existe := s.usuarios.Buscar("juan"); !existe {
		t.Error("juan quedó dado de baja con la contraseña de confirmación incorrecta")
	}
}

// Y con la contraseña correcta —la del SUPERUSUARIO, no la de juan— sí.
func TestBajaConLaContrasenaCorrectaQuitaLaCuenta(t *testing.T) {
	s, _ := servidorMultiusuario(t)

	w := postConSesion(t, s, "/administracion/baja", url.Values{
		"nombre": {"juan"},
		"clave":  {claveDePrueba},
	})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("baja -> %d; se esperaba 303", w.Code)
	}
	if _, existe := s.usuarios.Buscar("juan"); existe {
		t.Error("juan seguía en el registro tras la baja confirmada")
	}
}

// La contraseña de JUAN, aunque sea válida para su propia cuenta, no sirve
// para confirmar la baja: lo que se repite es la del superusuario.
func TestBajaConLaContrasenaDeLaCuentaQueSeVaNoSirve(t *testing.T) {
	s, _ := servidorMultiusuario(t)

	postConSesion(t, s, "/administracion/baja", url.Values{
		"nombre": {"juan"},
		"clave":  {claveDeJuan},
	})
	if _, existe := s.usuarios.Buscar("juan"); !existe {
		t.Error("la contraseña de la propia cuenta bastó para confirmar su baja")
	}
}

// El panel muestra, por cada cuenta, si tiene sesión abierta AHORA MISMO —el
// único dato en vivo de esta primera versión (etapa 2).
func TestElPanelMuestraQuienTieneSesionActiva(t *testing.T) {
	s, _ := servidorMultiusuario(t)
	if err := s.usuarios.Alta("ana", "otra-contrasena-larga"); err != nil {
		t.Fatalf("Alta(ana): %v", err)
	}
	// Solo ana tiene sesión; juan nunca entró en esta prueba.
	if _, err := s.sesiones.Abrir("ana"); err != nil {
		t.Fatalf("Abrir(ana): %v", err)
	}

	cuerpo := peticionConSesion(t, s, "/administracion").Body.String()

	if !strings.Contains(cuerpo, "juan") || !strings.Contains(cuerpo, "ana") {
		t.Fatalf("el panel no lista a las dos cuentas:\n%s", cuerpo)
	}
	if n := strings.Count(cuerpo, ">activa<"); n != 1 {
		t.Errorf(`"activa" aparece %d veces; se esperaba 1 (solo ana)`, n)
	}
}
