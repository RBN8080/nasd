package web

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// respuestaConPlazoDePrueba implementa http.ResponseWriter y además
// SetWriteDeadline/SetReadDeadline directamente, que es lo que
// http.ResponseController exige para no rechazar el envoltorio. Con
// httptest.NewRecorder() ninguna de las dos existe, y escrituraConPlazo /
// lecturaConPlazo se quedarían sin envolver — exactamente la degradación
// silenciosa que su comentario declara aceptar cuando el ResponseWriter no
// admite plazos, y que aquí no es lo que se quiere probar.
type respuestaConPlazoDePrueba struct {
	cabeceras http.Header
}

func (r *respuestaConPlazoDePrueba) Header() http.Header {
	if r.cabeceras == nil {
		r.cabeceras = make(http.Header)
	}
	return r.cabeceras
}
func (r *respuestaConPlazoDePrueba) Write(p []byte) (int, error)      { return len(p), nil }
func (r *respuestaConPlazoDePrueba) WriteHeader(int)                  {}
func (r *respuestaConPlazoDePrueba) SetWriteDeadline(time.Time) error { return nil }
func (r *respuestaConPlazoDePrueba) SetReadDeadline(time.Time) error  { return nil }

// --- ADR-0059: el toque de actividad durante una transferencia larga -------
//
// Estas pruebas son UNITARIAS y no esperan los 10 s reales de
// intervaloDeToque: retrasan ultimoToque a mano, en el mismo paquete, para
// simular el paso del tiempo sin dormir el proceso. Una prueba de extremo a
// extremo con una descarga de verdad tendría que durar más de 10 s para
// observar un segundo toque, que es justo el coste que ADR-0026 declaró
// inaceptable para RF-09 en otro contexto.

func TestEscrituraConPlazoTocaEnElPrimerBloque(t *testing.T) {
	w := &respuestaConPlazoDePrueba{}
	toques := 0
	esc := escrituraConPlazo(w, time.Minute, func() { toques++ })
	if _, err := esc.Write([]byte("primer bloque")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if toques != 1 {
		t.Fatalf("toques = %d; el primer bloque de una transferencia debe avisar a la sesión", toques)
	}
}

// Dentro de intervaloDeToque, escribir mucho no debe llamar a tocar en cada
// bloque: Tocar es una escritura bajo mutex, y un flujo de bloques pequeños
// lo saturaría sin este suelo.
func TestEscrituraConPlazoNoTocaEnCadaBloque(t *testing.T) {
	w := &respuestaConPlazoDePrueba{}
	toques := 0
	esc := escrituraConPlazo(w, time.Minute, func() { toques++ })
	for range 20 {
		if _, err := esc.Write([]byte("x")); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	if toques != 1 {
		t.Fatalf("toques = %d; se esperaba 1: los bloques siguientes llegaron dentro de intervaloDeToque", toques)
	}
}

// Pasado intervaloDeToque, el siguiente bloque SÍ debe tocar otra vez. Es lo
// que hace que una descarga de varios minutos —una petición HTTP que nunca
// vuelve a pasar por exigirSesion mientras dura— mantenga su propia sesión
// viva y no la encuentre caducada al terminar.
func TestEscrituraConPlazoVuelveATocarPasadoElIntervalo(t *testing.T) {
	w := &respuestaConPlazoDePrueba{}
	toques := 0
	esc := escrituraConPlazo(w, time.Minute, func() { toques++ })
	e := esc.(*escritorConPlazo)

	if _, err := e.Write([]byte("bloque 1")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	// Simula que ha pasado más de intervaloDeToque desde el último toque,
	// sin dormir el proceso de verdad.
	e.ultimoToque = time.Now().Add(-intervaloDeToque - time.Millisecond)

	if _, err := e.Write([]byte("bloque 2, minutos después")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if toques != 2 {
		t.Fatalf("toques = %d; se esperaban 2 tras superar el intervalo", toques)
	}
}

// tocar puede ser nil —vivo.go lo pasa así a propósito— y Write no debe
// reventar por ello.
func TestEscrituraConPlazoSinTocarNoPanica(t *testing.T) {
	w := &respuestaConPlazoDePrueba{}
	esc := escrituraConPlazo(w, time.Minute, nil)
	if _, err := esc.Write([]byte("sin tocador")); err != nil {
		t.Fatalf("Write: %v", err)
	}
}

// El mismo contrato, del lado de la lectura: una subida larga —PATCH de tus,
// o el multipart de /subir— también debe avisar a la sesión mientras
// avanzan bytes, no solo al terminar.
func TestLecturaConPlazoTocaYSeThrottla(t *testing.T) {
	w := &respuestaConPlazoDePrueba{}
	toques := 0
	origen := strings.NewReader(strings.Repeat("a", 64))
	lec := lecturaConPlazo(w, origen, time.Minute, func() { toques++ })
	l := lec.(*lectorConPlazo)

	buf := make([]byte, 8)
	for range 4 {
		if _, err := l.Read(buf); err != nil {
			t.Fatalf("Read: %v", err)
		}
	}
	if toques != 1 {
		t.Fatalf("toques = %d; se esperaba 1: las lecturas seguidas caen dentro de intervaloDeToque", toques)
	}

	l.ultimoToque = time.Now().Add(-intervaloDeToque - time.Millisecond)
	if _, err := l.Read(buf); err != nil {
		t.Fatalf("Read: %v", err)
	}
	if toques != 2 {
		t.Fatalf("toques = %d; se esperaban 2 tras superar el intervalo", toques)
	}
}

func TestLecturaConPlazoSinTocarNoPanica(t *testing.T) {
	w := &respuestaConPlazoDePrueba{}
	origen := strings.NewReader("sin tocador")
	lec := lecturaConPlazo(w, origen, time.Minute, nil)
	buf := make([]byte, 4)
	if _, err := lec.Read(buf); err != nil {
		t.Fatalf("Read: %v", err)
	}
}
