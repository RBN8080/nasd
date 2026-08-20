package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// LA REGLA QUE ACOTA AL SUPERUSUARIO POR RED — soloDesdeDentro (sesion.go).
//
// Se comprueba VÍA POR VÍA y no por muestreo, que es lo que D-21 obliga desde
// que una asimetría entre dos caminos del mismo control pasó desapercibida:
// dejar una sola ruta fuera de la envoltura abriría por la puerta de al lado
// justo lo que las demás cierran.
//
// # POR QUÉ SE MIRA EL CUERPO Y NO SOLO EL 403
//
// Todas estas rutas ya tenían motivos propios para responder 403 —CSRF
// ausente, permiso insuficiente—, así que un 403 a secas NO demuestra que
// haya actuado ESTA regla. Se comprueba el aviso concreto, que solo escribe
// soloDesdeDentro. Es la misma trampa que estas pruebas destaparon en la
// batería de CSRF al añadirse (ver desdeCasa, acceso_test.go).

// vias son las once que la regla cubre, cada una con su método.
//
// Están TODAS: las cinco que destruyen contenido y las seis del panel de
// cuentas, incluidas la lectura y el flujo en vivo. El flujo entra porque
// publica lo mismo que la página y encima de forma continua — el argumento
// que ADR-0056 ya dejó escrito para /estado/flujo.
var vias = []struct{ metodo, ruta string }{
	{http.MethodGet, "/mover/foto.jpg"},
	{http.MethodPost, "/mover"},
	{http.MethodPost, "/renombrar"},
	{http.MethodGet, "/borrar/foto.jpg"},
	{http.MethodPost, "/borrar"},
	{http.MethodGet, "/administracion"},
	{http.MethodPost, "/administracion/alta"},
	{http.MethodGet, "/administracion/baja/juan"},
	{http.MethodPost, "/administracion/baja"},
	{http.MethodPost, "/administracion/refrescar"},
	{http.MethodGet, "/administracion/flujo"},
}

// pedirDesde lanza una petición fijando el origen, que es la única variable
// de estas pruebas.
//
// El contexto va CANCELADO a propósito: /administracion/flujo es SSE y, en el
// sentido «no se niega», entraría en su bucle de marcos y no volvería nunca.
// Con el contexto cancelado el bucle sale por su primer case (vivo.go), que
// es exactamente el camino que sigue una pestaña cerrada. Para el resto de
// vías no cambia nada: ninguna consulta el contexto antes de responder.
func pedirDesde(t *testing.T, h http.Handler, metodo, ruta, origen string, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	ctx, cancelar := context.WithCancel(context.Background())
	cancelar()
	r := httptest.NewRequest(metodo, ruta, nil).WithContext(ctx)
	r.RemoteAddr = origen
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// superusuarioEn abre sesión de superusuario y devuelve su cookie.
func superusuarioEn(t *testing.T, s *Servidor) *http.Cookie {
	t.Helper()
	cookie, _ := sesionAbierta(t, s)
	return cookie
}

// EL LADO QUE PROTEGE: desde Internet, al superusuario se le niegan las once.
func TestDesdeInternetElSuperusuarioNoDestruyeNiAdministra(t *testing.T) {
	s, _ := servidorMultiusuario(t)
	h := s.Rutas()
	cookie := superusuarioEn(t, s)

	for _, v := range vias {
		w := pedirDesde(t, h, v.metodo, v.ruta, "203.0.113.7:44001", cookie)
		if w.Code != http.StatusForbidden {
			t.Errorf("%s %s desde Internet -> %d; se esperaba 403", v.metodo, v.ruta, w.Code)
			continue
		}
		if !strings.Contains(w.Body.String(), avisoSoloDesdeDentro) {
			t.Errorf("%s %s desde Internet: 403 por otro motivo, no por la red:\n%s",
				v.metodo, v.ruta, w.Body.String())
		}
	}
}

// EL LADO QUE NO ESTORBA: desde casa y por el túnel, la regla no aparece.
//
// No se comprueba que respondan 200 —varias exigen CSRF o un cuerpo que aquí
// no se manda—, sino que el motivo del rechazo, si lo hay, NO es este. Eso
// aísla exactamente la regla bajo prueba de todo lo demás que estas vías
// hacen.
func TestDesdeCasaYPorElTunelElSuperusuarioConservaTodo(t *testing.T) {
	for _, origen := range []struct{ nombre, addr string }{
		{"la LAN", "192.168.1.18:5000"},
		{"el túnel", "10.77.0.3:51820"},
	} {
		t.Run(origen.nombre, func(t *testing.T) {
			s, _ := servidorMultiusuario(t)
			h := s.Rutas()
			cookie := superusuarioEn(t, s)

			for _, v := range vias {
				w := pedirDesde(t, h, v.metodo, v.ruta, origen.addr, cookie)
				if strings.Contains(w.Body.String(), avisoSoloDesdeDentro) {
					t.Errorf("%s %s desde %s: la regla lo negó, y desde ahí no debe",
						v.metodo, v.ruta, origen.nombre)
				}
			}
		})
	}
}

// LA MITAD QUE HACE LA DECISIÓN SOSTENIBLE: la regla NO alcanza a un usuario
// normal.
//
// Es lo que permitió aceptarla sin romper el uso diario de la segunda persona
// que usa este NAS. Su sesión está enraizada en su propia carpeta (ADR-0055),
// así que el radio de daño ya está acotado por construcción y cobrarle el
// mismo peaje no cerraría nada que no estuviera cerrado.
func TestLaReglaNoAlcanzaAUnUsuarioNormal(t *testing.T) {
	s, _ := servidorMultiusuario(t)
	h := s.Rutas()
	deJuan := cookieLlamada(entrar(t, h, "juan", claveDeJuan), nombreCookie)

	for _, v := range vias {
		// El panel de cuentas no es suyo por otra razón —soloSuperusuario— y
		// esa negativa es correcta; lo que se comprueba aquí es que no se le
		// niega por VENIR DE FUERA.
		w := pedirDesde(t, h, v.metodo, v.ruta, "203.0.113.7:44001", deJuan)
		if strings.Contains(w.Body.String(), avisoSoloDesdeDentro) {
			t.Errorf("%s %s: a un usuario normal se le aplicó una regla que no es suya",
				v.metodo, v.ruta)
		}
	}
}

// CORTESÍA, NO CONTROL: el menú no ofrece un botón que el servidor va a negar.
//
// El control es la primera prueba de este archivo. Esta comprueba lo otro que
// ADR-0054 dejó como lección: una interfaz que enseña algo distinto de lo que
// el servidor hace cuesta más de descubrir que el propio fallo.
func TestElListadoNoOfreceLoQueElServidorVaANegar(t *testing.T) {
	s, _ := servidorMultiusuario(t)
	h := s.Rutas()
	cookie := superusuarioEn(t, s)

	deFuera := pedirDesde(t, h, http.MethodGet, "/", "203.0.113.7:44001", cookie).Body.String()
	for _, sobra := range []string{
		`action="/renombrar"`,
		`action="/mover/`,
		`href="/borrar/`,
		`href="/administracion"`,
	} {
		if strings.Contains(deFuera, sobra) {
			t.Errorf("desde Internet el listado sigue ofreciendo %s", sobra)
		}
	}
	// Y dice DÓNDE se puede, que es lo que convierte una ausencia en una regla.
	if !strings.Contains(deFuera, "solo desde casa o por el túnel") {
		t.Error("el menú se queda mudo: no explica por qué faltan las acciones")
	}
	// «Seguridad» NO cuelga de esta regla: mirar quién toca el nodo es justo
	// lo que uno quiere poder hacer desde fuera.
	if !strings.Contains(deFuera, `href="/seguridad"`) {
		t.Error("desde Internet desapareció «Seguridad», y esa no está acotada")
	}

	deCasa := pedirDesde(t, h, http.MethodGet, "/", "192.168.1.18:5000", cookie).Body.String()
	for _, falta := range []string{
		`action="/renombrar"`,
		`href="/administracion"`,
	} {
		if !strings.Contains(deCasa, falta) {
			t.Errorf("desde casa el listado dejó de ofrecer %s", falta)
		}
	}
}

// El rechazo es un HECHO del servidor y termina donde terminan los demás: en
// el panel, con su motivo y no como un 403 anónimo.
func TestElRechazoPorRedQuedaEnElPanelConSuMotivo(t *testing.T) {
	s, _ := servidorMultiusuario(t)
	cookie := superusuarioEn(t, s)

	pedirDesde(t, s.Rutas(), http.MethodPost, "/borrar", "203.0.113.7:44001", cookie)

	cuerpo := panelSeguridad(t, s, "")
	if !strings.Contains(cuerpo, "Solo desde dentro") {
		t.Errorf("el panel no enseña el motivo del rechazo por red:\n%s", cuerpo)
	}
}

// EL BOTÓN MUERTO NO ESTABA SOLO EN EL LISTADO. /estado y /seguridad SÍ se ven
// desde Internet —no cuelgan de esta regla, a propósito— y sus barras llevaban
// a Administración igual que la del listado. Las tres se comprueban juntas
// porque el defecto sería el mismo y aparecería por separado.
func TestNingunaBarraOfreceAdministracionDesdeInternet(t *testing.T) {
	s, _ := servidorMultiusuario(t)
	h := s.Rutas()
	cookie := superusuarioEn(t, s)

	for _, pagina := range []string{"/", "/estado", "/seguridad"} {
		deFuera := pedirDesde(t, h, http.MethodGet, pagina, "203.0.113.7:44001", cookie)
		if deFuera.Code != http.StatusOK {
			t.Fatalf("GET %s desde Internet -> %d; esta página no está acotada por red",
				pagina, deFuera.Code)
		}
		if strings.Contains(deFuera.Body.String(), `href="/administracion"`) {
			t.Errorf("la barra de %s ofrece «Administración» desde Internet, y responde 403", pagina)
		}

		deCasa := pedirDesde(t, h, http.MethodGet, pagina, "192.168.1.18:5000", cookie)
		if !strings.Contains(deCasa.Body.String(), `href="/administracion"`) {
			t.Errorf("la barra de %s perdió «Administración» desde casa", pagina)
		}
	}
}
