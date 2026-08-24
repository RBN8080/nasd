package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// EL ESTADO QUE SE REGISTRA TIENE QUE SER EL QUE RECIBIÓ EL CLIENTE.
//
// # POR QUÉ ESTA BATERÍA CORRE CONTRA UN SERVIDOR DE VERDAD
//
// Lo que hay que demostrar no es que capturaDeEstado siga una regla que
// alguien escribió en un comentario: es que coincide con net/http. Una prueba
// con httptest.NewRecorder compararía el envoltorio contra otro simulacro y
// daría por buena cualquier regla que los dos compartieran.
//
// Aquí se levanta un servidor real, se pide de verdad, y se comparan DOS
// cosas: lo que el cliente ve en la línea de estado y lo que el envoltorio
// anotó. Si divergen, la prueba lo dice — que es exactamente el defecto que se
// encontró el 2026-08-24 con el doble WriteHeader.

// conCaptura levanta un servidor cuyo manejador va envuelto en capturaDeEstado,
// hace una petición y devuelve el estado que vio el CLIENTE y el que anotó el
// ENVOLTORIO.
func conCaptura(t *testing.T, manejador func(w http.ResponseWriter)) (delCliente, anotado int, bytes int64) {
	t.Helper()
	var cap *capturaDeEstado
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cap = &capturaDeEstado{ResponseWriter: w, estado: http.StatusOK}
		manejador(cap)
	}))
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/loquesea")
	if err != nil {
		t.Fatalf("pedir: %v", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if cap == nil {
		t.Fatal("el manejador no llegó a correr")
	}
	return resp.StatusCode, cap.estado, cap.bytes
}

// EL DEFECTO MEDIDO: un manejador que llama dos veces a WriteHeader.
//
// net/http descarta la segunda —deja «superfluous response.WriteHeader call»
// en el diario— porque la cabecera ya salió. El envoltorio se quedaba con la
// última y anotaba 500 donde el cliente recibió 404.
//
// No es teórico: habría metido un rechazo normal en el contador de 5xx, que es
// el numerador del SLI-1, y en el panel de seguridad como error del servidor.
func TestUnSegundoWriteHeaderNoCambiaLoQueSeRegistra(t *testing.T) {
	cliente, anotado, _ := conCaptura(t, func(w http.ResponseWriter) {
		w.WriteHeader(http.StatusNotFound)
		w.WriteHeader(http.StatusInternalServerError)
	})
	if cliente != http.StatusNotFound {
		t.Fatalf("el cliente recibió %d; net/http conserva el primero", cliente)
	}
	if anotado != cliente {
		t.Errorf("se registró %d y el cliente recibió %d", anotado, cliente)
	}
}

// Escribir sin WriteHeader es el 200 implícito de net/http, y a partir de ahí
// el estado ya no puede cambiar — ni para el cliente ni para el registro.
func TestEscribirSinWriteHeaderSeRegistraComo200(t *testing.T) {
	cliente, anotado, bytes := conCaptura(t, func(w http.ResponseWriter) {
		io.WriteString(w, "hola")
	})
	if cliente != http.StatusOK || anotado != http.StatusOK {
		t.Errorf("cliente=%d anotado=%d; se esperaba 200 en los dos", cliente, anotado)
	}
	if bytes != 4 {
		t.Errorf("bytes = %d; se escribieron 4", bytes)
	}
}

// Y un WriteHeader DESPUÉS de haber escrito tampoco mueve el registro: para
// entonces la cabecera ya salió.
func TestWriteHeaderTrasEscribirNoCambiaLoRegistrado(t *testing.T) {
	cliente, anotado, _ := conCaptura(t, func(w http.ResponseWriter) {
		io.WriteString(w, "hola")
		w.WriteHeader(http.StatusInternalServerError)
	})
	if cliente != http.StatusOK || anotado != cliente {
		t.Errorf("cliente=%d anotado=%d; se esperaba 200 en los dos", cliente, anotado)
	}
}

// LA OTRA MITAD, Y LA QUE «quedarse con el primero» habría roto: una respuesta
// informativa 1xx NO fija la cabecera. net/http la envía y sigue esperando el
// estado final, así que registrar el 103 sería la misma mentira por el otro
// lado.
func TestUnaRespuestaInformativaNoFijaElEstado(t *testing.T) {
	cliente, anotado, _ := conCaptura(t, func(w http.ResponseWriter) {
		w.WriteHeader(http.StatusEarlyHints) // 103
		w.WriteHeader(http.StatusNotFound)
	})
	if cliente != http.StatusNotFound {
		t.Fatalf("el cliente recibió %d; se esperaba el estado final 404", cliente)
	}
	if anotado != cliente {
		t.Errorf("se registró %d y el cliente recibió %d", anotado, cliente)
	}
}

// Un estado normal se registra tal cual, que es el caso de todos los días y el
// que no puede romperse al arreglar los raros.
func TestElEstadoNormalSeRegistraTalCual(t *testing.T) {
	for _, quiero := range []int{200, 204, 301, 400, 401, 403, 404, 500} {
		cliente, anotado, _ := conCaptura(t, func(w http.ResponseWriter) {
			w.WriteHeader(quiero)
		})
		if cliente != quiero || anotado != quiero {
			t.Errorf("estado %d: cliente=%d anotado=%d", quiero, cliente, anotado)
		}
	}
}

// Unwrap tiene que seguir llegando al ResponseWriter real, o los plazos por
// actividad de plazos.go dejan de funcionar EN SILENCIO — que es como se
// perdería una descarga de 4 GB (RF-09) sin que nadie supiera por qué.
func TestElControladorDeRespuestaAlcanzaElWriterReal(t *testing.T) {
	var fallo error
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cap := &capturaDeEstado{ResponseWriter: w, estado: http.StatusOK}
		rc := http.NewResponseController(cap)
		// Las dos que usa el proyecto: el plazo por actividad (plazos.go) y el
		// vaciado del flujo en vivo (vivo.go).
		if err := rc.SetWriteDeadline(time.Now().Add(time.Minute)); err != nil {
			fallo = err
		}
		io.WriteString(cap, "hola")
		if err := rc.Flush(); err != nil && fallo == nil {
			fallo = err
		}
	}))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("pedir: %v", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if fallo != nil {
		t.Errorf("el controlador no alcanzó el writer real: %v", fallo)
	}
}

// LA PROPIEDAD COMPLETA, POR LA VÍA DE VERDAD: lo que el panel de seguridad
// registra como estado es lo que el cliente recibió.
//
// Se comprueba a través de Rutas() y no llamando al envoltorio a mano: lo que
// importa es que la cadena entera —conRegistro incluido— conserve el estado.
func TestElHistorialRegistraElEstadoQueRecibioElCliente(t *testing.T) {
	s := servidorConAuth(t)
	r := httptest.NewRequest(http.MethodGet, "/inventada", nil)
	r.RemoteAddr = "203.0.113.7:44001"
	w := httptest.NewRecorder()
	s.Rutas().ServeHTTP(w, r)

	eventos := s.seguridad.Desde(time.Time{})
	if len(eventos) != 1 {
		t.Fatalf("eventos = %d; se esperaba 1", len(eventos))
	}
	if eventos[0].Estado != w.Code {
		t.Errorf("el historial dice %d y el cliente recibió %d", eventos[0].Estado, w.Code)
	}
	if !strings.HasPrefix(eventos[0].Ruta, "/inventada") {
		t.Errorf("la ruta no se conservó: %q", eventos[0].Ruta)
	}
}
