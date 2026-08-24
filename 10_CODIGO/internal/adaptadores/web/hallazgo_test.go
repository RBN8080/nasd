package web

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nasd/internal/seguridad"
)

// LA RESPUESTA INESPERADA, POR LA VÍA DE VERDAD.
//
// # POR QUÉ ESTAS PRUEBAS ENVUELVEN UN MANEJADOR PROPIO
//
// Este NAS no publica /.git/config, y esa es exactamente su virtud: no hay
// forma de provocar el caso pidiéndoselo a las rutas reales, porque siempre
// contestan 404 o 401. Lo que hay que demostrar aquí es que el MIDDLEWARE
// reconoce el suceso cuando ocurre — es decir, que el día que una ruta nueva se
// acote mal, el panel se entere. Así que se le pone delante un manejador que
// finge esa avería.
//
// La otra mitad —que las rutas reales NO lo producen— sí se prueba contra el
// servidor entero, más abajo.

// atendiendo hace una petición a través de conRegistro con un manejador que
// responde lo que se le diga, y devuelve la respuesta.
func atendiendo(t *testing.T, s *Servidor, metodo, ruta string, estado int, cabeceras map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	manejador := s.conRegistro(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(estado)
		io.WriteString(w, "contenido")
	}))
	r := httptest.NewRequest(metodo, ruta, nil)
	r.RemoteAddr = "203.0.113.7:44001"
	for k, v := range cabeceras {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	manejador.ServeHTTP(w, r)
	return w
}

// B1 — UNA RUTA AJENA ATENDIDA CON CONTENIDO PRODUCE EL HALLAZGO.
func TestUnaRutaAjenaAtendidaConContenidoSeRegistra(t *testing.T) {
	s := servidorConAuth(t)
	atendiendo(t, s, http.MethodGet, "/.git/config", http.StatusOK, nil)

	v := s.hallazgos.Todos()
	if len(v) != 1 {
		t.Fatalf("hallazgos = %d; se esperaba 1", len(v))
	}
	if v[0].Ruta != "/.git/config" || v[0].Estado != 200 || v[0].Metodo != http.MethodGet {
		t.Errorf("el hallazgo no conserva el hecho: %+v", v[0])
	}
	if v[0].Clase != seguridad.RutaAjenaAtendida {
		t.Errorf("clase = %v", v[0].Clase)
	}
	if v[0].UltimoOrigen.String() != "203.0.113.7" {
		t.Errorf("origen = %v", v[0].UltimoOrigen)
	}

	// Y NO SE MEZCLA CON LOS RECHAZOS: el anillo de Evento sigue significando
	// «un rechazo, tal como ocurrió». Meter aquí un 200 habría vuelto falsos su
	// nombre, sus filtros y el contador que /estado publica.
	if n := s.seguridad.Total(); n != 0 {
		t.Errorf("una respuesta con contenido entró en el anillo de rechazos: %d", n)
	}
}

// B3 — LA MISMA RUTA CON 404 SIGUE SIENDO SONDEO, y va donde siempre.
//
// Es la distinción entera de este trabajo en una prueba: la misma petición,
// dos estados, dos sitios distintos y dos significados opuestos.
func TestLaMismaRutaCon404EsSondeoYNoExposicion(t *testing.T) {
	s := servidorConAuth(t)
	atendiendo(t, s, http.MethodGet, "/.git/config", http.StatusNotFound, nil)

	if n := len(s.hallazgos.Todos()); n != 0 {
		t.Errorf("un 404 produjo %d hallazgos; no debía producir ninguno", n)
	}
	eventos := s.seguridad.Desde(time.Time{})
	if len(eventos) != 1 {
		t.Fatalf("rechazos = %d; se esperaba 1", len(eventos))
	}
	if eventos[0].Estado != http.StatusNotFound || eventos[0].Ruta != "/.git/config" {
		t.Errorf("el rechazo no conserva el hecho: %+v", eventos[0])
	}
	// Y SUSTENTA la señal, que es lo máximo que un sondeo puede sustentar.
	o := seguridad.PorOrigen(eventos)
	if len(o) != 1 || len(o[0].Senales) == 0 {
		t.Error("el sondeo no sustenta ninguna señal")
	}
}

// B2 — LO LEGÍTIMO NO LO PRODUCE, comprobado contra las RUTAS REALES.
//
// Esta es la mitad que impide que la regla degenere en «todo lo que no es un
// rechazo es exposición». Se recorre la web de verdad —con sesión, para que
// las páginas respondan 200— y el panel tiene que seguir sin hallazgos.
func TestLaWebNormalNoProduceNingunHallazgo(t *testing.T) {
	s := servidorConAuth(t)
	cookie, _ := sesionAbierta(t, s)

	for _, ruta := range []string{"/", "/estado", "/seguridad", "/acceso", "/estatico/estilo.css"} {
		r := desdeCasa(httptest.NewRequest(http.MethodGet, ruta, nil))
		r.AddCookie(cookie)
		w := httptest.NewRecorder()
		s.Rutas().ServeHTTP(w, r)
		if w.Code >= 500 {
			t.Fatalf("GET %s -> %d", ruta, w.Code)
		}
	}
	if n := len(s.hallazgos.Todos()); n != 0 {
		t.Errorf("la navegación normal produjo %d hallazgos: %+v", n, s.hallazgos.Todos())
	}
}

// EL ARCHIVO DEL USUARIO NO ES UNA EXPOSICIÓN DEL SERVIDOR.
//
// Nada impide que el responsable suba «respaldo.bak» o una carpeta «.git»
// suyos. Servírselos es el NAS funcionando como debe, y alarmar por ello
// gastaría la credibilidad de la única alarma seria de la página — el mismo
// razonamiento con el que ADR-0065 le quitó el énfasis a SenalSoftwareAjeno.
func TestUnArchivoDelUsuarioConNombreAjenoNoEsUnHallazgo(t *testing.T) {
	s := servidorConAuth(t)
	for _, ruta := range []string{
		"/contenido/respaldo.bak", "/descargar/notas.php",
		"/abrir/copia.sql", "/miniatura/foto.bak",
	} {
		atendiendo(t, s, http.MethodGet, ruta, http.StatusOK, nil)
	}
	if n := len(s.hallazgos.Todos()); n != 0 {
		t.Errorf("los archivos del usuario produjeron %d hallazgos: %+v", n, s.hallazgos.Todos())
	}
}

// F — PRIVACIDAD. La función nueva NO empieza a capturar nada de lo que
// 04_SEGURIDAD §6 mantiene fuera, ni siquiera «por si sirve después».
//
// Se comprueba en los DOS sitios por los que el dato podría escaparse: la
// estructura que se guarda y la página que se pinta.
func TestElHallazgoNoCapturaSecretosDeLaPeticion(t *testing.T) {
	s := servidorConAuth(t)
	atendiendo(t, s, http.MethodGet, "/.git/config", http.StatusOK, map[string]string{
		"Cookie":        "nas_sesion=SECRETO-DE-SESION",
		"Authorization": "Basic U0VDUkVUTy1CQVNJQ08=",
		"User-Agent":    "AGENTE-QUE-NO-DEBE-ENTRAR",
		"X-Inventada":   "CABECERA-ARBITRARIA",
	})

	v := s.hallazgos.Todos()
	if len(v) != 1 {
		t.Fatalf("hallazgos = %d", len(v))
	}
	enLaEstructura := fmt.Sprintf("%+v", v[0])
	cuerpo := panelSeguridad(t, s, "")
	for _, secreto := range []string{
		"SECRETO-DE-SESION", "U0VDUkVUTy1CQVNJQ08=",
		"AGENTE-QUE-NO-DEBE-ENTRAR", "CABECERA-ARBITRARIA",
	} {
		if strings.Contains(enLaEstructura, secreto) {
			t.Errorf("el hallazgo guardó %q: %s", secreto, enLaEstructura)
		}
		if strings.Contains(cuerpo, secreto) {
			t.Errorf("el panel pinta %q", secreto)
		}
	}
}

// LA CADENA DE CONSULTA NO SE GUARDA. Puede llevar un testigo, una clave o
// cualquier cosa que alguien pegue en la barra, y guardarla «por si sirve»
// sería justo la captura de más que este trabajo tenía prohibida.
func TestElHallazgoNoGuardaLaCadenaDeConsulta(t *testing.T) {
	s := servidorConAuth(t)
	atendiendo(t, s, http.MethodGet, "/.git/config?token=SECRETO-EN-LA-URL", http.StatusOK, nil)

	v := s.hallazgos.Todos()
	if len(v) != 1 {
		t.Fatalf("hallazgos = %d", len(v))
	}
	if strings.Contains(v[0].Ruta, "SECRETO-EN-LA-URL") || strings.Contains(v[0].Ruta, "?") {
		t.Errorf("se guardó la cadena de consulta: %q", v[0].Ruta)
	}
}

// EL PANEL LO ENSEÑA, Y NO AFIRMA LO QUE NO PUEDE SABER.
//
// # LO QUE CAMBIÓ EL 2026-08-24, Y LO QUE NO
//
// Esta prueba exigía además dos frases de la leyenda —«habla de este servidor,
// no de quien lo pidió» y «No demuestra…»—. Esa leyenda se redujo a una línea
// con las otras cuatro de la página, por decisión del responsable sobre
// capturas del panel en uso. Las definiciones viven en NAS_OPERACION.txt §15.
//
// LA SEGUNDA MITAD DE LA PRUEBA NO SE TOCA, y es la que de verdad protegía:
// prohíbe que la página use «exploit», «intrusión» o «comprometido». Negar el
// compromiso en prosa era prudencia; no afirmarlo es la regla. Un texto puede
// acortarse; una palabra que el servidor no puede sostener, no puede aparecer.
func TestElPanelEnsenaElHallazgoSinAfirmarCompromiso(t *testing.T) {
	s := servidorConAuth(t)
	atendiendo(t, s, http.MethodGet, "/.git/config", http.StatusOK, nil)

	cuerpo := panelSeguridad(t, s, "")
	for _, quiero := range []string{
		"Respuestas inesperadas",
		"/.git/config",
		"Requieren revisión",
	} {
		if !strings.Contains(cuerpo, quiero) {
			t.Errorf("el panel no dice %q", quiero)
		}
	}
	for _, prohibido := range []string{"Exploit", "exploit", "Intrusión", "Comprometido"} {
		if strings.Contains(cuerpo, prohibido) {
			t.Errorf("el panel afirma %q, que el servidor no puede saber", prohibido)
		}
	}
}

// UN HALLAZGO NO OBEDECE A LA VENTANA. Que el nodo respondiera donde no debía
// sigue siendo verdad hoy si nadie ha ido a mirar, y esconderlo tras «últimas
// 24 horas» sería apagar la alarma dejando el problema en pie.
func TestElHallazgoNoDesapareceAlAcotarLaVentana(t *testing.T) {
	s := servidorConAuth(t)
	atendiendo(t, s, http.MethodGet, "/.git/config", http.StatusOK, nil)

	for _, consulta := range []string{"", "?horas=1", "?red=lan", "?motivo=credencial_incorrecta"} {
		if !strings.Contains(panelSeguridad(t, s, consulta), "/.git/config") {
			t.Errorf("el hallazgo desapareció con el filtro %q", consulta)
		}
	}
}

// SE ANOTA VENGA DE DONDE VENGA, también desde casa — y ahí se aparta a
// propósito del criterio de las señales.
//
// Una señal INTERPRETA a quien pide, y desde la LAN esa interpretación no
// informa de nada. Esto no habla de quien pide: habla del servidor. Que el nodo
// publique /.git/config es igual de cierto si quien lo descubre es el propio
// responsable con curl, y ese es justo el caso en el que más falta hace verlo.
func TestElHallazgoSeAnotaTambienDesdeCasa(t *testing.T) {
	s := servidorConAuth(t)
	manejador := s.conRegistro(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "contenido")
	}))
	r := desdeCasa(httptest.NewRequest(http.MethodGet, "/.env", nil))
	manejador.ServeHTTP(httptest.NewRecorder(), r)

	v := s.hallazgos.Todos()
	if len(v) != 1 {
		t.Fatalf("hallazgos desde casa = %d; se esperaba 1", len(v))
	}
	// Y se dice DE DÓNDE vino, que es el contexto que evita leerlo como un
	// ataque cuando fue una comprobación del propio responsable.
	if v[0].UltimaRed.DeFuera() {
		t.Errorf("la red se clasificó como Internet: %v", v[0].UltimaRed)
	}
}
