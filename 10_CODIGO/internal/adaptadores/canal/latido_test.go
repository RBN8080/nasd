package canal

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// El latido es la mitad del trabajo que NO puede vivir dentro del nodo, y estas
// pruebas cubren solo la mitad que sí: que la señal SALE. Que alguien la esté
// recibiendo —y sepa concluir que dejó de llegar— es del testigo externo, y por
// definición no se puede probar desde aquí.

func latidoDePrueba(url string, cuerpo func() string) *Latido {
	return &Latido{
		cliente:   &http.Client{Timeout: plazoLatido},
		destino:   url,
		intervalo: time.Hour,
		cuerpo:    cuerpo,
		reg:       silencioso(),
	}
}

func TestElLatidoMandaElDiagnosticoYCuenta(t *testing.T) {
	var visto struct {
		metodo, cuerpo string
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		visto.metodo, visto.cuerpo = r.Method, string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	l := latidoDePrueba(srv.URL, func() string { return "uptime: 3h\nhallazgos: 0\n" })
	l.unLatido(context.Background())

	if visto.metodo != http.MethodPost {
		t.Errorf("método = %q; se esperaba POST", visto.metodo)
	}
	if !strings.Contains(visto.cuerpo, "uptime") {
		t.Errorf("el diagnóstico no llegó: %q", visto.cuerpo)
	}
	s := l.Salud()
	if s.Latidos != 1 || s.Fallidos != 0 || s.UltimoExito.IsZero() {
		t.Errorf("salud = %+v; se esperaba un latido correcto", s)
	}
}

func TestElCuerpoDelLatidoNoLlevaDireccionesNiRutas(t *testing.T) {
	// El latido dice «estoy», no «esto ha pasado». Se manda 288 veces al día,
	// así que todo lo que entre se le entrega al proveedor 288 veces diarias
	// para siempre. El tope existe porque el cuerpo lo compone otro paquete a
	// través de una función, y lo que cruza esa frontera se acota igual que se
	// acota todo lo que entra de fuera.
	var recibido string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		recibido = string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	enorme := strings.Repeat("x", topeCuerpoLatido*3)
	latidoDePrueba(srv.URL, func() string { return enorme }).unLatido(context.Background())

	if len(recibido) > topeCuerpoLatido {
		t.Errorf("el cuerpo del latido mide %d bytes; el tope es %d", len(recibido), topeCuerpoLatido)
	}
}

func TestElLatidoNoReintenta(t *testing.T) {
	// El siguiente sale solo dentro de un intervalo y el testigo tiene margen
	// de sobra para varios perdidos (Period 5 min contra Grace 90 min,
	// ADR-0074). Reintentar aquí solo serviría para amontonar peticiones justo
	// cuando la red no está.
	var intentos atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		intentos.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	l := latidoDePrueba(srv.URL, nil)
	l.unLatido(context.Background())

	if n := intentos.Load(); n != 1 {
		t.Errorf("intentos = %d; el latido no reintenta", n)
	}
	if s := l.Salud(); s.Fallidos != 1 || s.Latidos != 0 {
		t.Errorf("salud = %+v; se esperaba un fallo y ningún latido", s)
	}
}

func TestElSecretoDelLatidoNoApareceEnElDiario(t *testing.T) {
	// La URL del check ES el secreto: quien la tenga puede falsificar latidos y
	// mantener el testigo en verde con el nodo muerto. Es el secreto cuyo robo
	// más daño hace de los tres, así que no puede acabar en un diario que vive
	// en un disco pensado para viajar.
	const uuid = "a1b2c3d4-SECRETO-DEL-CHECK"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	var diario strings.Builder
	l := latidoDePrueba(srv.URL+"/"+uuid, nil)
	l.reg = registroEn(&diario)
	l.unLatido(context.Background())

	if strings.Contains(diario.String(), uuid) {
		t.Errorf("EL SECRETO DEL TESTIGO ESTÁ EN EL DIARIO:\n%s", diario.String())
	}
	if !strings.Contains(diario.String(), "no se pudo emitir el latido") {
		t.Errorf("el fallo no se registró:\n%s", diario.String())
	}
}

func TestUnaRachaDeFallosEscribeUnaSolaLinea(t *testing.T) {
	// Una noche sin Internet serían 96 latidos fallidos. Una línea por cada uno
	// son 96 escrituras idénticas en un medio que P9 declara consumible, y el
	// aviso deja de leerse justo cuando aparece uno nuevo.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	var diario strings.Builder
	l := latidoDePrueba(srv.URL, nil)
	l.reg = registroEn(&diario)
	for range 20 {
		l.unLatido(context.Background())
	}

	if n := strings.Count(diario.String(), "no se pudo emitir el latido"); n != 1 {
		t.Errorf("20 fallos escribieron %d líneas; se esperaba 1", n)
	}
	// Pero el CONTADOR sí lleva el volumen: el diario recibe la noticia y el
	// indicador, la cifra. Es el mismo reparto que anotarCierreFallido.
	if s := l.Salud(); s.Fallidos != 20 {
		t.Errorf("fallidos = %d; se esperaban 20", s.Fallidos)
	}
}

func TestElLatidoAvisaCuandoVuelve(t *testing.T) {
	var fallar atomic.Bool
	fallar.Store(true)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if fallar.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	var diario strings.Builder
	l := latidoDePrueba(srv.URL, nil)
	l.reg = registroEn(&diario)
	for range 3 {
		l.unLatido(context.Background())
	}
	fallar.Store(false)
	l.unLatido(context.Background())

	if !strings.Contains(diario.String(), "vuelve a llegar") {
		t.Errorf("la recuperación no se registró:\n%s", diario.String())
	}
	if s := l.Salud(); s.UltimoError != "" {
		t.Errorf("el último error no se limpió al recuperarse: %q", s.UltimoError)
	}
}

func TestElLatidoSigueSiendoIndependienteDelCanalDeAvisos(t *testing.T) {
	// LAS DOS RUTAS SON DISTINTAS Y NO SE FUNDEN. Es la propiedad que sostiene
	// el diseño: si el canal de avisos cae, el latido tiene que seguir saliendo,
	// porque es lo único que impide que una caída del nodo pase inadvertida.
	//
	// Y al revés: un latido roto no puede impedir que salga un aviso crítico.
	srvLatido := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srvLatido.Close()

	// El canal de avisos, completamente roto.
	cola := NuevaCola(&canalFalso{err: context.DeadlineExceeded}, silencioso())
	for range 5 {
		cola.entregar(context.Background(), avisoDePrueba())
	}

	// El latido, ajeno a todo eso.
	l := latidoDePrueba(srvLatido.URL, nil)
	l.unLatido(context.Background())

	if s := l.Salud(); s.Latidos != 1 || s.Fallidos != 0 {
		t.Errorf("el latido se vio afectado por la caída del canal: %+v", s)
	}
	if s := cola.Salud(); s.Fallidos != 5 {
		t.Errorf("el canal no registró sus fallos: %+v", s)
	}
}

func TestNuevoLatidoRechazaUnaURLQueNoSirve(t *testing.T) {
	// P5: falla al construir, no al primer tic. Un latido mal configurado que
	// arranca «bien» deja al nodo sin vigilancia externa sin decirlo.
	// «http://» entra en la lista a propósito: esta URL ES el secreto —quien la
	// tenga puede falsificar latidos— y mandarla en claro la regalaría a
	// cualquiera en el camino. El código y el mensaje de error se contradecían
	// hasta el 2026-08-25, y se resolvió por el lado estricto.
	for _, malo := range []string{"", "   ", "hc-ping.com/uuid", "ftp://x", "http://hc-ping.com/uuid"} {
		if _, err := NuevoLatido(malo, time.Minute, nil, silencioso()); err == nil {
			t.Errorf("NuevoLatido(%q) lo dio por bueno", malo)
		}
	}
	if _, err := NuevoLatido("https://hc-ping.com/uuid", 0, nil, silencioso()); err == nil {
		t.Error("aceptó un intervalo de cero")
	}
	if _, err := NuevoLatido("https://hc-ping.com/uuid", time.Minute, nil, silencioso()); err != nil {
		t.Errorf("rechazó una configuración válida: %v", err)
	}
}

func TestUnLatidoNuloNoRompeNada(t *testing.T) {
	// El nodo puede correr SIN testigo configurado, y /estado pregunta la salud
	// igual. Salud() admite receptor nulo a propósito para que la raíz de
	// composición pueda pasar la closure antes de tener el latido.
	var l *Latido
	if s := l.Salud(); s.Configurado {
		t.Error("un latido nulo se declara configurado")
	}
}
