package canal

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"nasd/internal/aviso"
)

// Las pruebas del canal van contra un httptest.Server y NUNCA contra Internet:
// una prueba que necesita red no se ejecuta en la puerta de «make verificar», y
// una que no se ejecuta no prueba nada.
//
// El campo destino es privado y estas pruebas viven en el MISMO paquete, así
// que apuntan al servidor local sin que el código de producción necesite una
// variable global ni un constructor «para pruebas» — que sería código escrito
// para el andamiaje y no para el producto.

func canalDePrueba(url string) *Telegram {
	return &Telegram{
		cliente: &http.Client{Timeout: plazoPorIntento},
		destino: url,
		chat:    "-100123",
		// Un milisegundo: lo que se comprueba es CUÁNTAS veces se reintenta y
		// bajo qué condiciones, no que Go sepa dormir.
		espera: time.Millisecond,
	}
}

func avisoDePrueba() aviso.Aviso {
	return aviso.Aviso{Severidad: aviso.Amarillo, Titulo: "prueba", Texto: "cuerpo"}
}

func silencioso() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// registroEn captura lo que se escribe en el diario, que es donde acabarían las
// fugas de secretos si alguien envolviera mal un error.
func registroEn(w io.Writer) *slog.Logger {
	return slog.New(slog.NewTextHandler(w, nil))
}

func TestUnEnvioCorrectoMandaLoQueDebe(t *testing.T) {
	var visto struct {
		chat, texto, modo, silencio string
		metodo, tipo                string
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		visto.metodo = r.Method
		visto.tipo = r.Header.Get("Content-Type")
		visto.chat = r.FormValue("chat_id")
		visto.texto = r.FormValue("text")
		visto.modo = r.FormValue("parse_mode")
		visto.silencio = r.FormValue("disable_notification")
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer srv.Close()

	a := avisoDePrueba()
	a.Silencioso = true
	if err := canalDePrueba(srv.URL).Enviar(context.Background(), a); err != nil {
		t.Fatalf("Enviar: %v", err)
	}

	if visto.metodo != http.MethodPost {
		t.Errorf("método = %q; se esperaba POST", visto.metodo)
	}
	if !strings.HasPrefix(visto.tipo, "application/x-www-form-urlencoded") {
		t.Errorf("Content-Type = %q", visto.tipo)
	}
	if visto.chat != "-100123" || visto.texto != "cuerpo" {
		t.Errorf("chat=%q texto=%q", visto.chat, visto.texto)
	}
	if visto.modo != "HTML" {
		t.Errorf("parse_mode = %q; se esperaba HTML", visto.modo)
	}
	// EL SILENCIO DEL RESUMEN, que es la mitad del anti-fatiga que no depende
	// de nuestra política sino del proveedor.
	if visto.silencio != "true" {
		t.Errorf("disable_notification = %q; un aviso silencioso debe pedirlo", visto.silencio)
	}
}

func TestElRojoNoPideSilencio(t *testing.T) {
	var pidioSilencio string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		pidioSilencio = r.FormValue("disable_notification")
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer srv.Close()

	a := aviso.Aviso{Severidad: aviso.Rojo, Titulo: "x", Texto: "y"}
	if err := canalDePrueba(srv.URL).Enviar(context.Background(), a); err != nil {
		t.Fatalf("Enviar: %v", err)
	}
	if pidioSilencio != "" {
		t.Errorf("un aviso crítico pidió silencio (%q); debe sonar", pidioSilencio)
	}
}

func TestUn401NoSeReintenta(t *testing.T) {
	// Es el único fallo que NO se arregla esperando: el token no vale.
	// Reintentar solo gasta el nodo y la cuota contra una puerta que no se va a
	// abrir — la misma lección que umbralAccionFuerzaBruta aprendió mirando a
	// un atacante insistir contra una contraseña.
	var intentos atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		intentos.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"ok":false,"error_code":401,"description":"Unauthorized"}`)
	}))
	defer srv.Close()

	err := canalDePrueba(srv.URL).Enviar(context.Background(), avisoDePrueba())
	if err == nil {
		t.Fatal("un 401 se dio por entregado")
	}
	// El valor tiene que ser RECONOCIBLE, no un texto: es lo que permite a la
	// cola encender el indicador de /estado que dice «vaya a cambiar el
	// secreto» en vez de «algo falla».
	if !errors.Is(err, ErrSecretoRechazado) {
		t.Errorf("error = %v; se esperaba ErrSecretoRechazado", err)
	}
	if n := intentos.Load(); n != 1 {
		t.Errorf("se intentó %d veces; un 401 se intenta UNA", n)
	}
}

func TestUn429RespetaElRetryAfterDelProveedor(t *testing.T) {
	// Cuando el proveedor dice cuánto esperar, eso es información y no una
	// opinión: manda sobre nuestra fórmula de espera.
	var intentos atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if intentos.Add(1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"ok":false,"error_code":429,"parameters":{"retry_after":1}}`)
			return
		}
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer srv.Close()

	inicio := time.Now()
	if err := canalDePrueba(srv.URL).Enviar(context.Background(), avisoDePrueba()); err != nil {
		t.Fatalf("Enviar: %v", err)
	}
	if n := intentos.Load(); n != 2 {
		t.Errorf("intentos = %d; se esperaban 2", n)
	}
	// UN SEGUNDO ENTERO, cuando la espera propia de este canal está puesta en
	// un milisegundo: la única forma de tardar tanto es haber hecho caso al
	// retry_after del proveedor en vez de a la fórmula propia.
	if d := time.Since(inicio); d < time.Second {
		t.Errorf("esperó %v; se esperaba al menos el 1 s que pidió el proveedor", d)
	}
}

func TestUn5xxSeReintentaYAcabaFallando(t *testing.T) {
	var intentos atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		intentos.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	// Contexto con plazo para no pagar las esperas completas del backoff.
	ctx, cancelar := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelar()

	if err := canalDePrueba(srv.URL).Enviar(ctx, avisoDePrueba()); err == nil {
		t.Fatal("una racha de 502 se dio por entregada")
	}
	if n := intentos.Load(); n < 2 {
		t.Errorf("intentos = %d; un 5xx debe reintentarse", n)
	}
}

func TestUn400NoSeReintentaPorqueEsDefectoNuestro(t *testing.T) {
	// Marcado roto, texto demasiado largo o chat inexistente. Reintentarlo daría
	// exactamente el mismo resultado, y esconder un defecto propio tras tres
	// intentos idénticos es la forma de no arreglarlo nunca.
	var intentos atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		intentos.Add(1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"ok":false,"error_code":400,"description":"can't parse entities"}`)
	}))
	defer srv.Close()

	if err := canalDePrueba(srv.URL).Enviar(context.Background(), avisoDePrueba()); err == nil {
		t.Fatal("un 400 se dio por entregado")
	}
	if n := intentos.Load(); n != 1 {
		t.Errorf("se intentó %d veces; un 400 se intenta UNA", n)
	}
}

func TestElPlazoCortaUnProveedorQueNoResponde(t *testing.T) {
	// RNF-12 exige plazo declarado en toda E/S. Sin él —el valor por omisión de
	// http.Client NO TIENE NINGUNO— una goroutine podría no volver nunca.
	// EL MANEJADOR SE LIBERA A MANO, y no basta con esperar al contexto de la
	// petición: medido aquí mismo el 2026-08-25, cuando el CLIENTE abandona por
	// su plazo, el servidor de prueba NO cancela el contexto de una petición con
	// cuerpo hasta que intenta escribir. Con «<-r.Context().Done()» a secas, el
	// manejador no volvía nunca y srv.Close() —que espera a las peticiones en
	// curso— colgaba la prueba entera: 90 s hasta el timeout de «go test».
	//
	// El canal propio cierra ANTES que el servidor (los defer van al revés de
	// como se escriben), así que el manejador siempre tiene por dónde salir.
	liberar := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-liberar:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(liberar)

	c := canalDePrueba(srv.URL)
	c.cliente = &http.Client{Timeout: 100 * time.Millisecond}

	ctx, cancelar := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelar()

	hecho := make(chan error, 1)
	go func() { hecho <- c.Enviar(ctx, avisoDePrueba()) }()
	select {
	case err := <-hecho:
		if err == nil {
			t.Error("un proveedor mudo se dio por entregado")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Enviar no volvió: el plazo no está cortando")
	}
}

func TestCancelarElContextoAbortaLaEntrega(t *testing.T) {
	// Al apagar el servicio, un aviso a medio entregar no puede retrasar el
	// cierre ordenado que los historiales necesitan para volcarse.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway) // fuerza el reintento y su espera
	}))
	defer srv.Close()

	// La espera se sube a medio segundo SOLO en esta prueba: con el
	// milisegundo del resto, los tres intentos terminan antes de que dé tiempo
	// a cancelar y la prueba pasaría sin haber comprobado nada. Lo que se mide
	// es que la cancelación interrumpa el DESCANSO entre intentos.
	c := canalDePrueba(srv.URL)
	c.espera = 500 * time.Millisecond

	ctx, cancelar := context.WithCancel(context.Background())
	hecho := make(chan error, 1)
	go func() { hecho <- c.Enviar(ctx, avisoDePrueba()) }()

	time.Sleep(50 * time.Millisecond)
	cancelar()

	select {
	case err := <-hecho:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("error = %v; se esperaba context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelar el contexto no abortó la entrega")
	}
}

func TestElTokenNoApareceEnNingunErrorNiEnElDiario(t *testing.T) {
	// LA REGLA QUE GOBIERNA telegram.go: Telegram obliga a llevar el token en la
	// RUTA de la URL, y los errores de net/http incluyen la URL completa. Si
	// alguien envolviera esos errores con %w, el token acabaría en el diario del
	// nodo — y el diario vive en el disco de datos, que viaja.
	//
	// Se comprueba sobre el ERROR y sobre el DIARIO, que es donde acabaría.
	const token = "123456789:AAsecretoQUEnoDEBEsalirJAMAS"

	// (a) El proveedor devuelve un error de protocolo.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	c, err := NuevoTelegram(token, "-100123")
	if err != nil {
		t.Fatalf("NuevoTelegram: %v", err)
	}
	c.destino = srv.URL + "/bot" + token + "/sendMessage"
	srv.Close() // y ahora, además, el servidor no está: error de red

	c.espera = time.Millisecond
	err = c.Enviar(context.Background(), avisoDePrueba())
	if err == nil {
		t.Fatal("se dio por entregado contra un servidor cerrado")
	}
	if strings.Contains(err.Error(), token) {
		t.Errorf("EL TOKEN ESTÁ EN EL ERROR: %v", err)
	}

	// (b) Lo mismo, pasando por la cola, que es quien lo escribe en el diario.
	var diario strings.Builder
	reg := slog.New(slog.NewTextHandler(&diario, nil))
	cola := NuevaCola(c, reg)
	cola.entregar(context.Background(), avisoDePrueba())
	if strings.Contains(diario.String(), token) {
		t.Errorf("EL TOKEN ESTÁ EN EL DIARIO:\n%s", diario.String())
	}
	if !strings.Contains(diario.String(), "no se pudo entregar") {
		t.Errorf("el fallo no se registró en absoluto:\n%s", diario.String())
	}
}

func TestNuevoTelegramExigeLasDosMitades(t *testing.T) {
	// P5: falla al construir, no al primer aviso — que sería, por definición,
	// el peor momento posible para descubrirlo.
	if _, err := NuevoTelegram("", "-100"); err == nil {
		t.Error("aceptó un token vacío")
	}
	if _, err := NuevoTelegram("tok", ""); err == nil {
		t.Error("aceptó un chat vacío")
	}
}
