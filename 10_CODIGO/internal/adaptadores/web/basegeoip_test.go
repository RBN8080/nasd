package web

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nasd/internal/aviso"
	"nasd/internal/geoip"
)

// El indicador de la base de país y operador — 00_RECTOR.md §7.octies.
//
// LO QUE ESTAS PRUEBAS PROTEGEN es una sola propiedad, y es la razón de existir
// del indicador: que una base que dejó de refrescarse NO se pueda pintar de
// verde. El archivo no envejece solo —sigue resolviendo direcciones igual de
// bien—, así que sin mirar su EDAD un refresco que lleve meses sin ejecutarse se
// ve exactamente igual que uno que corrió anoche. Es el mismo punto ciego que
// evaluarRespaldo cubre para el cliente de Windows, y aquí tarda más en
// notarse: la cadencia es mensual, no de horas.

func conBase(preparada time.Time) Instantanea {
	return Instantanea{BaseGeoIP: preparada}
}

func unicoGeo(t *testing.T, i Instantanea, ahora time.Time) indicador {
	t.Helper()
	is := evaluarBaseGeoIP(i, ahora)
	if len(is) != 1 {
		t.Fatalf("se esperaba un indicador; salieron %d", len(is))
	}
	return is[0]
}

// SIN BASE NO SE PINTA. geoip es la única dependencia OPCIONAL del servidor
// (ADR-0062) y un semáforo permanentemente gris enseña a ignorar el panel
// entero, que es lo que ADR-0065 corrigió retirando 24 elementos.
func TestLaBaseGeoIPNoSePintaSiNoHayBase(t *testing.T) {
	if is := evaluarBaseGeoIP(Instantanea{}, time.Now()); len(is) != 0 {
		t.Errorf("sin base no debe haber indicador; salieron %d", len(is))
	}
}

func TestUnaBaseRecienPreparadaEsVerde(t *testing.T) {
	ahora := time.Date(2026, 9, 12, 6, 0, 0, 0, time.Local)
	ind := unicoGeo(t, conBase(ahora.Add(-30*time.Minute)), ahora)

	if ind.Veredicto != vOK {
		t.Errorf("veredicto = %q; se esperaba ok", ind.Veredicto)
	}
	// La FECHA va en el valor y no solo la edad: es la que el panel de
	// seguridad publica al lado de cada país, y las dos pantallas tienen que
	// poder cotejarse sin hacer cuentas.
	if !strings.Contains(ind.Valor, "2026-09-12") {
		t.Errorf("el valor no dice de cuándo es la base: %q", ind.Valor)
	}
	if ind.Accion != "" {
		t.Errorf("una base al día no pide hacer nada; acción = %q", ind.Accion)
	}
}

// EL PEOR CASO NORMAL NO PUEDE ALARMAR, y este es el número que lo fija.
//
// OnCalendar=monthly con RandomizedDelaySec=6h: el mes más largo son 31 días y
// el retardo aleatorio de la corrida siguiente añade hasta 6 horas. Si el umbral
// mordiera aquí, el indicador daría un amarillo al año por diseño y se
// aprendería a ignorarlo — que es justo el fallo que ADR-0065 vino a corregir.
func TestElPeorCasoNormalDelRefrescoMensualSigueSiendoVerde(t *testing.T) {
	ahora := time.Date(2026, 9, 12, 6, 0, 0, 0, time.Local)
	ind := unicoGeo(t, conBase(ahora.Add(-(31*24+6)*time.Hour)), ahora)

	if ind.Veredicto != vOK {
		t.Errorf("31 d 6 h es la cadencia NORMAL del temporizador; veredicto = %q", ind.Veredicto)
	}
}

// PASADO EL UMBRAL, LA VENTANA MENSUAL SE PERDIÓ. Es lo único que este indicador
// afirma, y por eso el texto tiene que llevar QUÉ HACER: sin la acción, el
// amarillo obliga a ir a buscar en la documentación qué guion refresca esto, que
// es la mitad de no avisar.
func TestUnaBaseQueRebasaElUmbralPideActuar(t *testing.T) {
	ahora := time.Date(2026, 9, 12, 6, 0, 0, 0, time.Local)
	ind := unicoGeo(t, conBase(ahora.Add(-46*24*time.Hour)), ahora)

	if ind.Veredicto != vAtencion {
		t.Errorf("veredicto = %q; se esperaba atencion", ind.Veredicto)
	}
	if !strings.Contains(ind.Accion, "18_geoip.sh") {
		t.Errorf("la acción no dice qué ejecutar: %q", ind.Accion)
	}
	if !strings.Contains(ind.Valor, "sin refrescarse") {
		t.Errorf("el valor no dice que lleva tiempo sin refrescarse: %q", ind.Valor)
	}
}

// UNA FECHA EN EL FUTURO NO ES UNA BASE FRESCA. La resta daría una edad
// negativa, que cae por debajo de cualquier umbral y se leería como recién
// preparada: la mentira tranquilizadora que evaluarRespaldo ya declara para el
// reloj del equipo. Aquí el reloj es el del propio nodo, así que además invalida
// las demás fechas de la página.
func TestUnaBaseConFechaEnElFuturoNoSePintaDeVerde(t *testing.T) {
	ahora := time.Date(2026, 9, 12, 6, 0, 0, 0, time.Local)
	ind := unicoGeo(t, conBase(ahora.Add(48*time.Hour)), ahora)

	if ind.Veredicto == vOK {
		t.Errorf("una base fechada en el futuro no puede salir en verde: %q / %q", ind.Valor, ind.Veredicto)
	}
	if !strings.Contains(ind.Accion, "timedatectl") {
		t.Errorf("la acción no apunta al reloj del nodo: %q", ind.Accion)
	}
}

// LA RAZÓN DE SER DEL CAMBIO, Y LA ÚNICA PRUEBA QUE LA RECORRE ENTERA.
//
// El refresco mensual ya funcionaba; lo que no había era forma de enterarse
// cuando dejara de funcionar sin abrir el panel. Esta prueba va del indicador al
// mensaje entregado, porque ese camino es lo que sustituye al OnFailure= de
// systemd que se descartó.
func TestUnaBaseVencidaSalePorElCanalDeAvisos(t *testing.T) {
	var entregados []aviso.Aviso
	s := servidorDePrueba()
	s.reg = slog.New(&registroCapturado{})
	s.emitir = func(a aviso.Aviso) { entregados = append(entregados, a) }

	ahora := time.Date(2026, 9, 12, 6, 0, 0, 0, time.Local)
	vencida := conBase(ahora.Add(-60 * 24 * time.Hour))
	s.anunciar(evaluarBaseGeoIP(vencida, ahora))

	if len(entregados) != 1 {
		t.Fatalf("se esperaba un aviso; salieron %d", len(entregados))
	}
	if entregados[0].Severidad != aviso.Amarillo {
		t.Errorf("severidad = %v; una base atrasada envejece etiquetas, no tumba el nodo",
			entregados[0].Severidad)
	}
	// Amarillo INTERRUMPE: solo el verde llega en silencio (severidad.go). Un
	// aviso que no suena no cierra el agujero que esto viene a cerrar.
	if entregados[0].Silencioso {
		t.Errorf("el aviso llegó en silencio; el responsable no se enteraría")
	}
	if !strings.Contains(entregados[0].Texto, "Base de país y operador") {
		t.Errorf("el aviso no dice de qué indicador habla: %q", entregados[0].Texto)
	}

	// Y NO SE REPITE. El refresco es mensual: repetir el mismo amarillo cada
	// cinco minutos son 288 mensajes diarios sobre una fila que no va a cambiar
	// hasta que alguien ejecute el guion.
	s.anunciar(evaluarBaseGeoIP(vencida, ahora))
	if len(entregados) != 1 {
		t.Errorf("el mismo veredicto no debe volver a avisar; van %d", len(entregados))
	}
}

// baseEnDisco prepara una base mínima de verdad y le pone la fecha pedida.
//
// Se construye con geoip.Preparar y no con un doble porque lo que se comprueba
// es la CADENA entera: que la mtime del archivo llegue a geoip.Fecha(), de ahí a
// la instantánea y de ahí a la pantalla. Un doble saltaría justo el eslabón que
// puede romperse.
func baseEnDisco(t *testing.T, preparada time.Time) *geoip.BaseDatos {
	t.Helper()
	ruta := filepath.Join(t.TempDir(), "geoip")
	v4 := "8.8.8.0\t8.8.8.255\t15169\tUS\tGOOGLE\n"
	if _, err := geoip.Preparar(strings.NewReader(v4), strings.NewReader(""), ruta); err != nil {
		t.Fatalf("Preparar: %v", err)
	}
	if err := os.Chtimes(ruta, preparada, preparada); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}
	b, err := geoip.Abrir(ruta)
	if err != nil {
		t.Fatalf("Abrir: %v", err)
	}
	t.Cleanup(func() { b.Cerrar() })
	return b
}

func TestLaFilaDeLaBaseGeoIPLlegaALaPantallaYAlJSON(t *testing.T) {
	s := servidorConAuth(t)
	// Se enchufa por donde lo enchufa la raíz de composición.
	s.geo = baseEnDisco(t, time.Now().Add(-3*24*time.Hour))
	h := s.Rutas()
	cookie := abrirSesionDePrueba(t, h)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/estado", nil)
	r.AddCookie(cookie)
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /estado -> %d", w.Code)
	}
	if cuerpo := w.Body.String(); !strings.Contains(cuerpo, "Base de país y operador") {
		t.Errorf("la fila de la base NO aparece en /estado")
	}

	// Y en el JSON, que es contrato aparte.
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest("GET", "/estado?formato=json", nil)
	r2.AddCookie(cookie)
	h.ServeHTTP(w2, r2)
	if !strings.Contains(w2.Body.String(), "base_geoip") {
		t.Errorf("la clave de la base NO aparece en /estado?formato=json")
	}
}

// SIN BASE, LA PANTALLA QUEDA COMO ESTABA. Es la garantía de que este cambio no
// toca nada en un nodo al que todavía no se le ha ejecutado 18_geoip.sh.
func TestSinBaseGeoIPLaPantallaNoCambia(t *testing.T) {
	s := servidorConAuth(t)
	h := s.Rutas()
	cookie := abrirSesionDePrueba(t, h)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/estado", nil)
	r.AddCookie(cookie)
	h.ServeHTTP(w, r)
	if strings.Contains(w.Body.String(), "Datos de referencia") {
		t.Errorf("sin base, el grupo no debe existir")
	}
}
