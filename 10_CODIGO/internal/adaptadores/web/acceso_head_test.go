package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// EL HEAD DE /acceso REFLEJA AL GET Y NO SE CUELGA — veredicto de R13.
//
// # QUÉ DENUNCIÓ EL PENTEST, Y QUÉ ERA DE VERDAD
//
// El pentest del 2026-09-23 (07_AUDITORIAS.md, Auditoría 9) marcó como hallazgo
// de PRIORIDAD ALTA que «HEAD /acceso se cuelga indefinidamente»: probado con
// `curl -X HEAD`, la orden cortaba siempre en el --max-time sin cerrar el
// cuerpo. La hipótesis era un handler que no distingue el método y se queda
// bloqueado escribiendo un cuerpo que nadie lee, explotable sin credenciales.
//
// Al medirlo contra un servidor de VERDAD (no un ResponseRecorder, que no tiene
// socket donde colgarse) el hallazgo se cae: el servidor CONTESTA HEAD en ~1 ms.
//
//	GET  /acceso  ->  200, cuerpo = el formulario, Content-Length: N
//	HEAD /acceso  ->  200, SIN cuerpo,             Content-Length: N (el mismo)
//
// Es exactamente lo que manda HTTP (RFC 9110 §9.3.2): HEAD devuelve las MISMAS
// cabeceras que GET —Content-Length incluido— y ningún cuerpo. El mux de Go
// mapea el patrón «GET /acceso» también a HEAD (por eso opaca.go documenta el
// «Allow: GET, HEAD»), mostrarAcceso pinta la plantilla y net/http se traga la
// escritura del cuerpo en HEAD. Ni una goroutine ni un descriptor quedan
// retenidos: la conexión queda ociosa y reutilizable, acotada por IdleTimeout.
//
// El «cuelgue» es del CLIENTE: `curl -X HEAD` (a diferencia de `curl -I`) manda
// HEAD pero se pone a esperar los N bytes de cuerpo que el Content-Length
// anuncia y que HTTP prohíbe en HEAD. Espera a la Pi, no la Pi a nadie.
//
// # POR QUÉ ESTA PRUEBA, Y POR QUÉ NO UN 405
//
// La «solución» que sugería R13 —devolver 405 a HEAD— sería un RETROCESO: HEAD
// debe reflejar a GET, y monitores, proxies y cachés lo usan legítimamente. Un
// HEAD que da 405 mientras el GET da 200 es justo la incoherencia que HTTP
// prohíbe. Esta prueba fija el comportamiento correcto para que ese «arreglo»
// no pueda colarse sin romperla: si alguien registra un HEAD que no sea el
// reflejo del GET, aquí salta.
func TestElHeadDeAccesoReflejaAlGetYNoSeCuelga(t *testing.T) {
	s := servidorConPlazo(t, time.Minute)
	srv := httptest.NewServer(s.Rutas())
	defer srv.Close()

	// GET primero: es la referencia. El cuerpo es el formulario y su longitud
	// es la que el HEAD tiene que anunciar sin mandar.
	respGet, err := http.Get(srv.URL + "/acceso")
	if err != nil {
		t.Fatalf("GET /acceso: %v", err)
	}
	cuerpoGet, _ := io.ReadAll(respGet.Body)
	respGet.Body.Close()
	if respGet.StatusCode != http.StatusOK {
		t.Fatalf("GET /acceso -> %d; se esperaba 200 (es la única ruta pública)", respGet.StatusCode)
	}
	if len(cuerpoGet) == 0 {
		t.Fatal("GET /acceso devolvió cuerpo vacío; el formulario tiene que estar")
	}

	// HEAD con un cliente que TIENE plazo: si el servidor se colgara de verdad,
	// esto no daría 200 tarde, daría un error de timeout. Que devuelva es ya la
	// mitad del veredicto.
	req, _ := http.NewRequest(http.MethodHead, srv.URL+"/acceso", nil)
	cliente := &http.Client{Timeout: 5 * time.Second}

	inicio := time.Now()
	respHead, err := cliente.Do(req)
	transcurrido := time.Since(inicio)
	if err != nil {
		t.Fatalf("HEAD /acceso no volvió (se colgó): %v — tras %v", err, transcurrido)
	}
	defer respHead.Body.Close()

	// 1) Mismo estado que GET. Un 405 aquí sería el «arreglo» equivocado de R13.
	if respHead.StatusCode != http.StatusOK {
		t.Fatalf("HEAD /acceso -> %d; debe reflejar al GET (200), no dar 405 ni 404", respHead.StatusCode)
	}

	// 2) Mismo Content-Length que GET: HEAD anuncia lo que GET mandaría.
	if respHead.ContentLength != int64(len(cuerpoGet)) {
		t.Fatalf("HEAD anuncia Content-Length=%d y el cuerpo de GET mide %d; HEAD debe reflejar a GET",
			respHead.ContentLength, len(cuerpoGet))
	}

	// 3) Y SIN cuerpo: es lo que distingue HEAD de GET, y lo que net/http
	// garantiza al tragarse la escritura. Un cuerpo aquí sería el defecto de
	// verdad (y lo que un cliente ingenuo se queda esperando).
	cuerpoHead, _ := io.ReadAll(respHead.Body)
	if len(cuerpoHead) != 0 {
		t.Fatalf("HEAD /acceso trajo %d bytes de cuerpo; HEAD no lleva cuerpo", len(cuerpoHead))
	}
}
