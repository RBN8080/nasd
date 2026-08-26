package web

import (
	"net/http"
	"regexp"
	"strings"
	"testing"
)

// El rail (ADR-0075) no vuelve a derivar autoridad: lee usuarioDe y
// acotadoPorRed, las MISMAS funciones que ya deciden quién entra por la
// puerta real (sesion.go). Estas pruebas comprueban que lo que el rail
// ENSEÑA coincide con lo que el servidor de verdad PERMITE — la asimetría
// que D-21 obliga a comprobar vía por vía y no por muestreo.
func TestElRailSoloPintaLosModulosQueLaSesionPuedeAbrir(t *testing.T) {
	s, _ := servidorMultiusuario(t)

	casos := []struct {
		nombre   string
		cookie   *http.Cookie
		origen   string
		quiere   []string
		noQuiere []string
	}{
		{
			nombre: "superusuario desde casa ve los cinco",
			cookie: superusuarioEn(t, s),
			origen: "192.168.1.18:5000",
			quiere: []string{"/resumen", "/", "/estado", "/seguridad", "/administracion"},
		},
		{
			nombre:   "superusuario desde Internet NO ve Cuentas",
			cookie:   superusuarioEn(t, s),
			origen:   "203.0.113.7:44001",
			quiere:   []string{"/resumen", "/", "/estado", "/seguridad"},
			noQuiere: []string{"/administracion"},
		},
		{
			nombre:   "cuenta normal solo ve Archivos",
			cookie:   sesionAbiertaPara(t, s, "juan"),
			origen:   "192.168.1.18:5000",
			quiere:   []string{"/"},
			noQuiere: []string{"/resumen", "/estado", "/seguridad", "/administracion"},
		},
	}

	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			w := pedirDesde(t, s.Rutas(), http.MethodGet, "/", c.origen, c.cookie)
			cuerpo := w.Body.String()
			rail := extraerRail(t, cuerpo)
			for _, ruta := range c.quiere {
				if !strings.Contains(rail, `href="`+ruta+`"`) {
					t.Errorf("%s: el rail no ofrece %q\nrail: %s", c.nombre, ruta, rail)
				}
			}
			for _, ruta := range c.noQuiere {
				if strings.Contains(rail, `href="`+ruta+`"`) {
					t.Errorf("%s: el rail ofrece %q, que esta sesión no puede abrir\nrail: %s", c.nombre, ruta, rail)
				}
			}
		})
	}
}

// extraerRail acota la comprobación al <nav class="rail">, y no a la página
// entera: "/" aparece también como acción de «Cancelar» o como href de
// «Archivos» en las pestañas móviles, y buscar en todo el cuerpo daría falsos
// positivos que esconderían justo el defecto que esta prueba busca.
func extraerRail(t *testing.T, cuerpo string) string {
	t.Helper()
	re := regexp.MustCompile(`(?s)<nav class="rail">.*?</nav>`)
	m := re.FindString(cuerpo)
	if m == "" {
		t.Fatal("no se encontró <nav class=\"rail\"> en la página")
	}
	return m
}

// LA PROMESA DE ADR-0075: añadir un módulo no obliga a tocar el cromo. Se
// comprueba de la única forma que no miente — añadiendo uno de verdad a la
// tabla que marco.go ya usa, sin escribir una sola línea nueva en
// _marco.html ni en el CSS del rail — y viendo que aparece solo.
func TestAnadirUnModuloNoObligaATocarElCromo(t *testing.T) {
	original := modulos
	t.Cleanup(func() { modulos = original })
	modulos = append(append([]modulo(nil), original...), modulo{
		clave: "prueba", rotulo: "Módulo De Prueba", ruta: "/prueba-de-extensibilidad",
	})

	s := servidorConAuth(t)
	w := peticionConSesion(t, s, "/")
	rail := extraerRail(t, w.Body.String())
	if !strings.Contains(rail, `href="/prueba-de-extensibilidad"`) ||
		!strings.Contains(rail, "Módulo De Prueba") {
		t.Errorf("el módulo añadido a la tabla no apareció en el rail sin tocar ninguna plantilla:\n%s", rail)
	}
}

// Resumen (ADR-0075, RF-42) es tan de superusuario como /estado, y por el
// mismo motivo: reúne cifras de TODO el nodo.
func TestResumenNoSeSirveAUnaCuentaNormal(t *testing.T) {
	s, _ := servidorMultiusuario(t)
	cookie := sesionAbiertaPara(t, s, "juan")
	w := pedirDesde(t, s.Rutas(), http.MethodGet, "/resumen", "192.168.1.18:5000", cookie)
	if w.Code != http.StatusForbidden {
		t.Fatalf("/resumen para una cuenta normal -> %d; se esperaba %d", w.Code, http.StatusForbidden)
	}
}

func TestResumenSiSeSirveAlSuperusuario(t *testing.T) {
	s := servidorConAuth(t)
	w := peticionConSesion(t, s, "/resumen")
	if w.Code != http.StatusOK {
		t.Fatalf("/resumen para el superusuario -> %d; se esperaba 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Resumen") {
		t.Error("la página de /resumen no contiene su propio título")
	}
}

// LA CSP DE ESTA WEB ES «style-src 'self'» SIN 'unsafe-inline' (servidor.go,
// cspBase). Un solo «style="..."» colado en una plantilla no rompe nada en
// las pruebas de cabeceras —esas comprueban el HEADER, no el cuerpo— y
// tampoco se ve al ojo en una revisión rápida de una plantilla larga. Barre
// TODAS las páginas que llevan el cromo nuevo, que es donde se cometió este
// error dos veces durante la propia escritura de esta capa antes de
// detectarlo a mano.
func TestNingunaPaginaDelCromoLlevaEstiloEnLinea(t *testing.T) {
	s, _ := servidorMultiusuario(t)
	cookie := superusuarioEn(t, s)

	paginas := map[string]string{
		"resumen":        "/resumen",
		"archivos":       "/",
		"estado":         "/estado",
		"seguridad":      "/seguridad",
		"administracion": "/administracion",
	}
	for nombre, ruta := range paginas {
		w := pedirDesde(t, s.Rutas(), http.MethodGet, ruta, "192.168.1.18:5000", cookie)
		if strings.Contains(w.Body.String(), `style="`) {
			t.Errorf("%s (%s) lleva un estilo en línea, y la CSP no admite 'unsafe-inline'", nombre, ruta)
		}
	}
}

// El detalle se abre y se cierra por la URL, sin una sola línea de
// JavaScript — el mismo patrón en dos pasos que ya usan /mover/{ruta} y
// /borrar/{ruta}. Una clave o dirección que no existe deja el panel
// cerrado en vez de responder con un error: no es un 404, es «no hay nada
// que seleccionar».
func TestElDetalleSeAbreYSeCierraPorLaURLSinJavaScript(t *testing.T) {
	s := servidorConAuth(t)

	t.Run("estado, clave inexistente: sin panel y sin error", func(t *testing.T) {
		w := peticionConSesion(t, s, "/estado?ind=no-existe")
		if w.Code != http.StatusOK {
			t.Fatalf("código = %d, se esperaba 200", w.Code)
		}
		if strings.Contains(w.Body.String(), `class="detalle"`) {
			t.Error("se abrió un panel de detalle para una clave que no existe")
		}
	})

	t.Run("estado, clave real: el panel se abre", func(t *testing.T) {
		cuerpo := peticionConSesion(t, s, "/estado?ind=temperatura").Body.String()
		if !strings.Contains(cuerpo, `class="detalle"`) {
			t.Error("no se abrió el panel de detalle con una clave real")
		}
		if !strings.Contains(cuerpo, `href="/estado" aria-label="Cerrar"`) {
			t.Error("el panel de detalle no ofrece cómo cerrarse")
		}
	})

	t.Run("seguridad, origen inexistente: sin panel", func(t *testing.T) {
		w := peticionConSesion(t, s, "/seguridad?origen=203.0.113.99")
		if w.Code != http.StatusOK {
			t.Fatalf("código = %d, se esperaba 200", w.Code)
		}
		if strings.Contains(w.Body.String(), `class="detalle"`) {
			t.Error("se abrió un panel de detalle para un origen que no está en la tabla")
		}
	})

	t.Run("administracion, cuenta inexistente: sin panel", func(t *testing.T) {
		w := peticionConSesion(t, s, "/administracion?cuenta=no-existe")
		if w.Code != http.StatusOK {
			t.Fatalf("código = %d, se esperaba 200", w.Code)
		}
		if strings.Contains(w.Body.String(), `class="detalle"`) {
			t.Error("se abrió un panel de detalle para una cuenta que no existe")
		}
	})
}

// La gráfica de actividad declara su alcance en la propia página — no solo
// en la función pura de internal/seguridad (ver pordia_test.go) — porque es
// ahí donde alguien podría llegar a confiar en una promesa que no se hizo.
func TestLaPaginaDeSeguridadDeclaraElAlcanceDeLaGrafica(t *testing.T) {
	s := servidorConAuth(t)
	cuerpo := peticionConSesion(t, s, "/seguridad").Body.String()
	if !strings.Contains(cuerpo, "Actividad por día") {
		t.Fatal("falta la sección de actividad")
	}
	if !strings.Contains(cuerpo, "Sin rechazos en esta ventana.") {
		t.Errorf("sin eventos, la página no declaró que no hay datos:\n%s", cuerpo)
	}
}
