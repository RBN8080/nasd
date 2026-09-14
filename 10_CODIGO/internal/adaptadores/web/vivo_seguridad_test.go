package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"

	"nasd/internal/seguridad"
	"strings"
	"testing"
	"time"
)

func filtroDeConsulta(t *testing.T, consulta string) (f seguridad.Filtro) {
	t.Helper()
	q, err := url.ParseQuery(strings.TrimPrefix(consulta, "?"))
	if err != nil {
		t.Fatalf("consulta ilegible %q: %v", consulta, err)
	}
	filtro, _, _ := filtroDeSeguridad(q)
	return filtro
}

// EL FLUJO HABLA DE LO QUE SE ESTÁ MIRANDO, Y ESTA ES LA PRUEBA QUE LO SUJETA.
//
// Es el motivo entero por el que este flujo no usa el muestreador compartido:
// aquel reparte UN marco a todos los oyentes, y las cifras de este panel
// dependen del filtro de la URL. Sin esta prueba, alguien podría «simplificar»
// el flujo hacia el muestreador y el defecto volvería en silencio — dos
// pestañas con filtros distintos viendo las cifras de la otra.
func TestElFlujoDeSeguridadRespetaElFiltroDeQuienMira(t *testing.T) {
	s := servidorConAuth(t)
	h := s.Rutas()

	// Un rechazo desde Internet y dos desde la casa.
	deFuera := httptest.NewRequest("GET", "/wp-login.php", nil)
	deFuera.RemoteAddr = "8.8.8.8:44001"
	h.ServeHTTP(httptest.NewRecorder(), deFuera)
	for i := 0; i < 2; i++ {
		deCasa := httptest.NewRequest("GET", "/no-existe", nil)
		deCasa.RemoteAddr = "192.168.1.55:44002"
		h.ServeHTTP(httptest.NewRecorder(), deCasa)
	}

	internet := valorDelMarco(t, s.marcoDeSeguridad(filtroDeConsulta(t, "?red=internet")), "rechazos")
	casa := valorDelMarco(t, s.marcoDeSeguridad(filtroDeConsulta(t, "?red=lan")), "rechazos")

	if internet == casa {
		t.Fatalf("el flujo da la misma cifra para Internet y para la casa (%q): no está mirando el filtro", internet)
	}
	if internet != "1" {
		t.Errorf("rechazos de Internet: %q, se esperaba 1", internet)
	}
	if casa != "2" {
		t.Errorf("rechazos de la casa: %q, se esperaba 2", casa)
	}
}

func valorDelMarco(t *testing.T, m marcoSeguridad, clave string) string {
	t.Helper()
	for _, c := range m.Cifras {
		if c.Clave == clave {
			return c.Valor
		}
	}
	t.Fatalf("el marco no trae la cifra %q", clave)
	return ""
}

// CADA CUADRO DE LA PÁGINA TIENE ANCLA EN EL FLUJO, y al revés. Espejo de
// TestCadaFilaVivaDeLaPaginaTieneAnclaEnElFlujo, y por el mismo motivo: si una
// clave deja de coincidir, ese cuadro se queda congelado y no falla nada a
// gritos — se ve al mirar la pantalla un rato largo, o no se ve.
func TestCadaCifraDeLaPaginaTieneAnclaEnElFlujo(t *testing.T) {
	s := servidorConAuth(t)
	cuerpo := panelSeguridad(t, s, "")

	enLaPagina := map[string]bool{}
	for resto := cuerpo; ; {
		i := strings.Index(resto, `data-cifra="`)
		if i < 0 {
			break
		}
		resto = resto[i+len(`data-cifra="`):]
		j := strings.Index(resto, `"`)
		if j < 0 {
			t.Fatal("un data-cifra sin cerrar")
		}
		enLaPagina[resto[:j]] = true
	}
	if len(enLaPagina) == 0 {
		t.Fatal("la banda de cifras perdió sus anclas: el flujo no tendría dónde escribir")
	}

	marco := s.marcoDeSeguridad(filtroDeConsulta(t, ""))
	enElFlujo := map[string]bool{}
	for _, c := range marco.Cifras {
		enElFlujo[c.Clave] = true
		if !enLaPagina[c.Clave] {
			t.Errorf("el flujo manda la cifra %q y la página no tiene dónde ponerla", c.Clave)
		}
	}
	for clave := range enLaPagina {
		if !enElFlujo[clave] {
			t.Errorf("la página tiene el cuadro %q y el flujo nunca lo refresca", clave)
		}
	}
}

// LA VENTANA SE RECALCULA EN CADA MARCO. Una pestaña abierta toda la tarde con
// «últimas 24 horas» tiene que seguir contando las últimas 24 horas, no las 24
// que iban desde que se abrió.
func TestLaVentanaDelFlujoNoSeCongelaAlAbrirlo(t *testing.T) {
	a := filtroDeConsulta(t, "?horas=24")
	time.Sleep(2 * time.Millisecond)
	b := filtroDeConsulta(t, "?horas=24")
	if !b.Desde.After(a.Desde) {
		t.Error("dos lecturas del mismo filtro dan la misma ventana: está congelada")
	}
}

// Y EL EXTREMO ENTERO, de punta a punta: cabeceras de SSE y al menos un marco
// con las cinco cifras dentro.
func TestElFlujoDeSeguridadEntregaMarcos(t *testing.T) {
	s := servidorConAuth(t)
	h := s.Rutas()
	cookie, _ := sesionAbierta(t, s)

	// El contexto corta la conexión como haría el navegador al cerrar la
	// pestaña: sin él, un flujo SSE no termina nunca y la prueba se cuelga. Un
	// intervalo y medio basta para que llegue el primer marco sin que la prueba
	// se pase medio minuto esperando el segundo.
	ctx, cancelar := context.WithTimeout(context.Background(), intervaloSeguridadVivo+time.Second)
	defer cancelar()

	r := httptest.NewRequest("GET", "/seguridad/flujo", nil).WithContext(ctx)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /seguridad/flujo -> %d", w.Code)
	}
	if tipo := w.Header().Get("Content-Type"); !strings.HasPrefix(tipo, "text/event-stream") {
		t.Errorf("Content-Type %q; se esperaba text/event-stream", tipo)
	}
	if cache := w.Header().Get("Cache-Control"); !strings.Contains(cache, "no-store") {
		t.Errorf("Cache-Control %q; un flujo de telemetría no se almacena", cache)
	}
	cuerpo := w.Body.String()
	if !strings.Contains(cuerpo, "data: ") {
		t.Fatalf("no llegó ningún marco:\n%s", cuerpo)
	}
	// «paises» ocupa el sitio de «contrasenas» desde el 2026-09-13, por
	// decisión del responsable al aprobar el mapa de «Procedencia». Las cinco
	// anclas del marco tienen que seguir siendo exactamente las cinco que la
	// página pinta: si una deja de coincidir, el cuadro deja de refrescarse y
	// no falla nada a gritos — que es el contrato que este archivo vigila.
	for _, clave := range []string{"paquetes", "conexiones", "rechazos", "direcciones", "paises"} {
		if !strings.Contains(cuerpo, `"clave":"`+clave+`"`) {
			t.Errorf("el marco no trae la cifra %q", clave)
		}
	}
}
