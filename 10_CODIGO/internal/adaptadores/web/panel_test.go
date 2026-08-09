package web

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"nasd/internal/almacen"
	"nasd/internal/autenticacion"
)

// Pruebas del panel de administración — P-4, etapa 2 (ADR-0055).
//
// Igual que en acceso_test.go, lo que se mide aquí es el REPARTO y el
// CONTROL de acceso al panel, no el registro de cuentas en sí —Alta y Baja
// tienen sus propias pruebas contra disco real en autenticacion/usuarios_test.go—.

// La regla la aplica soloSuperusuario, la misma envoltura que ya protege
// /estado (TestSoloElSuperusuarioAlcanzaElEstado). Aquí se comprueba que las
// CINCO rutas nuevas están detrás de ella, no solo la primera.
func TestSoloElSuperusuarioAlcanzaLaAdministracion(t *testing.T) {
	s, _ := servidorMultiusuario(t)
	h := s.Rutas()
	deJuan := cookieLlamada(entrar(t, h, "juan", claveDeJuan), nombreCookie)

	for _, c := range []struct{ metodo, ruta string }{
		{"GET", "/administracion"},
		{"GET", "/administracion/baja/juan"},
		// El flujo en vivo publica lo mismo que la página y encima de forma
		// continua (P-7, ADR-0056): si se quedara fuera de esta tabla, abriría
		// por la puerta de al lado lo que la primera línea cierra.
		{"GET", "/administracion/flujo"},
	} {
		r := httptest.NewRequest(c.metodo, c.ruta, nil)
		r.AddCookie(deJuan)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusForbidden {
			t.Errorf("%s %s como usuario normal -> %d; se esperaba 403", c.metodo, c.ruta, w.Code)
		}
	}
	// Las POST también, aunque el 403 de soloSuperusuario llegue antes de que
	// exigirCSRF mire el formulario: un usuario normal no debe poder ni
	// intentarlo.
	for _, ruta := range []string{"/administracion/alta", "/administracion/baja", "/administracion/refrescar"} {
		if got := postCon(t, h, deJuan, ruta, url.Values{}); got != http.StatusForbidden {
			t.Errorf("POST %s como usuario normal -> %d; se esperaba 403", ruta, got)
		}
	}

	// Y al superusuario no se le cierra: la regla separa, no bloquea a todos.
	deAdmin := cookieLlamada(
		entrar(t, h, autenticacion.NombreSuperusuario, claveDePrueba), nombreCookie)
	r := httptest.NewRequest("GET", "/administracion", nil)
	r.AddCookie(deAdmin)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code == http.StatusForbidden {
		t.Error("GET /administracion como superusuario -> 403; le corresponde verlo")
	}
}

// Cortesía, no control —el control es la prueba de arriba—: la barra no
// ofrece un botón que va a responder 403.
func TestLaBarraSoloOfreceAdministracionAlSuperusuario(t *testing.T) {
	s, _ := servidorMultiusuario(t)
	h := s.Rutas()

	deJuan := listadoCon(t, h, cookieLlamada(entrar(t, h, "juan", claveDeJuan), nombreCookie))
	if strings.Contains(deJuan, `href="/administracion"`) {
		t.Error("la barra de un usuario normal ofrece «Administración»")
	}

	deAdmin := listadoCon(t, h, cookieLlamada(
		entrar(t, h, autenticacion.NombreSuperusuario, claveDePrueba), nombreCookie))
	if !strings.Contains(deAdmin, `href="/administracion"`) {
		t.Error("al superusuario le desapareció «Administración» de la barra")
	}
}

// El alta desde el panel es la misma operación que «nasd --crear-usuario»:
// la cuenta queda en el registro compartido, con la contraseña ya derivada.
func TestAltaDeUsuarioDesdeElPanel(t *testing.T) {
	s, _ := servidorMultiusuario(t)

	w := postConSesion(t, s, "/administracion/alta", url.Values{
		"nombre": {"nueva"},
		"clave":  {"contrasena-bastante-larga"},
	})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("alta -> %d; se esperaba 303", w.Code)
	}
	if _, existe := s.usuarios.Buscar("nueva"); !existe {
		t.Error("la cuenta no quedó en el registro tras el alta")
	}
}

// Registro.Alta ya exige 14 caracteres (LongitudMinimaUsuario); el panel no
// debe silenciar ese error ni crear la cuenta de todos modos.
func TestAltaConClaveCortaNoCreaLaCuenta(t *testing.T) {
	s, _ := servidorMultiusuario(t)

	w := postConSesion(t, s, "/administracion/alta", url.Values{
		"nombre": {"nueva"},
		"clave":  {"corta"},
	})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("alta con clave corta -> %d; se esperaba 303 (redirige con el error)", w.Code)
	}
	if loc := w.Header().Get("Location"); !strings.Contains(loc, "err=1") {
		t.Errorf("Location = %q; se esperaba err=1", loc)
	}
	if _, existe := s.usuarios.Buscar("nueva"); existe {
		t.Error("se creó una cuenta con una clave por debajo del mínimo")
	}
}

// El nombre reservado del superusuario tampoco se cuela por aquí —es la
// misma NombreValido que ya prueba autenticacion/usuarios_test.go, pero
// importa comprobar que el panel no la esquiva de camino.
func TestAltaNoAceptaElNombreDelSuperusuario(t *testing.T) {
	s, _ := servidorMultiusuario(t)

	postConSesion(t, s, "/administracion/alta", url.Values{
		"nombre": {autenticacion.NombreSuperusuario},
		"clave":  {"contrasena-bastante-larga"},
	})
	if _, existe := s.usuarios.Buscar(autenticacion.NombreSuperusuario); existe {
		t.Error("se creó una cuenta con el nombre reservado del superusuario")
	}
}

// LA PRUEBA CENTRAL DE LA BAJA: una contraseña de confirmación incorrecta no
// da de baja a nadie, aunque la sesión del superusuario sea válida.
func TestBajaExigeLaContrasenaDelSuperusuarioOtraVez(t *testing.T) {
	s, _ := servidorMultiusuario(t)

	w := postConSesion(t, s, "/administracion/baja", url.Values{
		"nombre": {"juan"},
		"clave":  {"esto-no-es-la-contrasena-correcta"},
	})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("baja con contraseña incorrecta -> %d; se esperaba 303 (vuelve a pedirla)", w.Code)
	}
	if loc := w.Header().Get("Location"); !strings.Contains(loc, "/administracion/baja/juan") {
		t.Errorf("Location = %q; se esperaba volver a la confirmación de juan", loc)
	}
	if _, existe := s.usuarios.Buscar("juan"); !existe {
		t.Error("juan quedó dado de baja con la contraseña de confirmación incorrecta")
	}
}

// Y con la contraseña correcta —la del SUPERUSUARIO, no la de juan— sí.
//
// Y ADEMÁS —corrección del 06/08— la carpeta se manda a promover ANTES de
// tocar el registro: es lo que corrige el defecto reportado en uso real,
// donde la cuenta desaparecía y su carpeta se quedaba huérfana dentro de
// homeUsers/.
func TestBajaConLaContrasenaCorrectaQuitaLaCuentaYPromueveLaCarpeta(t *testing.T) {
	s, rp := servidorMultiusuario(t)

	w := postConSesion(t, s, "/administracion/baja", url.Values{
		"nombre": {"juan"},
		"clave":  {claveDePrueba},
	})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("baja -> %d; se esperaba 303", w.Code)
	}
	if _, existe := s.usuarios.Buscar("juan"); existe {
		t.Error("juan seguía en el registro tras la baja confirmada")
	}
	promovidos := rp.promovidosHechos()
	if len(promovidos) != 1 || promovidos[0] != "juan" {
		t.Errorf("promovidosHechos() = %v; se esperaba solo [juan]", promovidos)
	}
}

// SI NO SE PUEDE PROMOVER LA CARPETA, LA BAJA SE ABORTA ENTERA. Lo contrario
// —quitar la cuenta igual— es exactamente el defecto que se corrige: una
// carpeta que se queda sin dueño visible en ningún sitio.
func TestBajaSeAbortaSiNoSePuedePromoverLaCarpeta(t *testing.T) {
	s, rp := servidorMultiusuario(t)
	rp.errorPromover = errors.New("ya existe algo con ese nombre en la raíz")

	w := postConSesion(t, s, "/administracion/baja", url.Values{
		"nombre": {"juan"},
		"clave":  {claveDePrueba},
	})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("baja -> %d; se esperaba 303 (vuelve al panel con el error)", w.Code)
	}
	if _, existe := s.usuarios.Buscar("juan"); !existe {
		t.Error("la cuenta se dio de baja aunque su carpeta no se pudo promover")
	}
}

// La contraseña de JUAN, aunque sea válida para su propia cuenta, no sirve
// para confirmar la baja: lo que se repite es la del superusuario.
func TestBajaConLaContrasenaDeLaCuentaQueSeVaNoSirve(t *testing.T) {
	s, _ := servidorMultiusuario(t)

	postConSesion(t, s, "/administracion/baja", url.Values{
		"nombre": {"juan"},
		"clave":  {claveDeJuan},
	})
	if _, existe := s.usuarios.Buscar("juan"); !existe {
		t.Error("la contraseña de la propia cuenta bastó para confirmar su baja")
	}
}

// El panel muestra, por cada cuenta, si tiene sesión abierta AHORA MISMO —el
// único dato en vivo de esta primera versión (etapa 2).
func TestElPanelMuestraQuienTieneSesionActiva(t *testing.T) {
	s, _ := servidorMultiusuario(t)
	if err := s.usuarios.Alta("ana", "otra-contrasena-larga"); err != nil {
		t.Fatalf("Alta(ana): %v", err)
	}
	// Solo ana tiene sesión; juan nunca entró en esta prueba.
	if _, err := s.sesiones.Abrir("ana"); err != nil {
		t.Fatalf("Abrir(ana): %v", err)
	}

	cuerpo := peticionConSesion(t, s, "/administracion").Body.String()

	if !strings.Contains(cuerpo, "juan") || !strings.Contains(cuerpo, "ana") {
		t.Fatalf("el panel no lista a las dos cuentas:\n%s", cuerpo)
	}
	if n := strings.Count(cuerpo, ">activa<"); n != 1 {
		t.Errorf(`"activa" aparece %d veces; se esperaba 1 (solo ana)`, n)
	}
}

// El flujo en vivo del panel — P-7, ADR-0056.
//
// LO QUE ESTA PRUEBA DEFIENDE no es que el flujo emita, sino que emita lo MISMO
// que pinta la página: la prueba de arriba busca ">activa<" en el HTML y esta
// busca ese mismo texto en el marco. Si alguien cambia el rótulo en un solo
// sitio, una de las dos se pone en rojo.
func TestElFlujoDeCuentasSigueLaSesionEnVivo(t *testing.T) {
	s, _ := servidorMultiusuario(t)
	if err := s.usuarios.Alta("ana", "otra-contrasena-larga"); err != nil {
		t.Fatalf("Alta(ana): %v", err)
	}
	testigo, err := s.sesiones.Abrir("ana")
	if err != nil {
		t.Fatalf("Abrir(ana): %v", err)
	}

	marcos, cancelar := s.cuentas.suscribir()
	defer cancelar()

	// Con la sesión de ana abierta: ella activa, juan no.
	m := esperarMarco(t, marcos)
	buscar := func(m marcoCuentas, nombre string) (filaCuenta, bool) {
		for _, f := range m.Cuentas {
			if f.Nombre == nombre {
				return f, true
			}
		}
		return filaCuenta{}, false
	}

	ana, ok := buscar(m, "ana")
	if !ok {
		t.Fatalf("el marco no trae a ana: %+v", m.Cuentas)
	}
	if ana.Texto != textoSesionActiva || ana.Veredicto != vOK {
		t.Errorf("ana con sesión abierta -> texto %q veredicto %q; se esperaba %q/%q",
			ana.Texto, ana.Veredicto, textoSesionActiva, vOK)
	}
	juan, ok := buscar(m, "juan")
	if !ok {
		t.Fatalf("el marco no trae a juan: %+v", m.Cuentas)
	}
	if juan.Texto != "" || juan.Veredicto != "" {
		t.Errorf("juan sin sesión -> texto %q veredicto %q; se esperaban vacíos", juan.Texto, juan.Veredicto)
	}

	// Y AL CERRAR SESIÓN LA PASTILLA SE APAGA SOLA, que es literalmente el
	// defecto que P-7 arregla: antes seguía diciendo «activa» hasta recargar.
	s.sesiones.Cerrar(testigo)

	plazo := time.Now().Add(2 * time.Second)
	for time.Now().Before(plazo) {
		m := esperarMarco(t, marcos)
		if f, ok := buscar(m, "ana"); ok && f.Texto == "" && f.Veredicto == "" {
			return
		}
	}
	t.Fatal("tras cerrar la sesión de ana su pastilla siguió encendida en el flujo")
}

func esperarMarco(t *testing.T, marcos <-chan marcoCuentas) marcoCuentas {
	t.Helper()
	select {
	case m := <-marcos:
		return m
	case <-time.After(2 * time.Second):
		t.Fatal("no llegó ningún marco del flujo de cuentas")
		return marcoCuentas{}
	}
}

// Pruebas de las métricas de uso de disco — P-4, etapa 3.
//
// LO QUE SE COMPRUEBA AQUÍ es el manejador —qué hace refrescarMetricas con lo
// que Resumen le devuelve—, no Resumen en sí: eso ya tiene sus propias
// pruebas contra disco real en fsposix/administracion_test.go.

// almacenConTamano solo añade un Resumen con un valor fijo a almacenVacio:
// es lo único que refrescarMetricas necesita de un almacén.
type almacenConTamano struct {
	almacenVacio
	bytes int64
}

func (a almacenConTamano) Resumen(context.Context, almacen.RutaSegura) (almacen.Conteo, error) {
	return almacen.Conteo{EsDirectorio: true, Bytes: a.bytes}, nil
}

// servidorParaMetricas da de alta una cuenta por cada entrada del mapa, con
// el almacén devolviendo el tamaño indicado. El mapa se lee EN CADA
// LLAMADA a AlmacenDe —no se copia al construir—, así que una prueba puede
// cambiar un valor entre dos refrescos para simular que una carpeta creció.
func servidorParaMetricas(t *testing.T, bytesPorUsuario map[string]int64) *Servidor {
	t.Helper()
	linea, err := autenticacion.Derivar(claveDePrueba, iteracionesDePrueba)
	if err != nil {
		t.Fatalf("Derivar: %v", err)
	}
	usuarios := registroDePrueba(t)
	for nombre := range bytesPorUsuario {
		if err := usuarios.Alta(nombre, claveDeJuan); err != nil {
			t.Fatalf("Alta(%s): %v", nombre, err)
		}
	}
	s, err := Nuevo(Opciones{
		Almacen: almacenVacio{},
		AlmacenDe: func(nombre string) (almacen.Almacen, error) {
			return almacenConTamano{bytes: bytesPorUsuario[nombre]}, nil
		},
		PromoverUsuario:  promoverUsuarioDePrueba,
		Registro:         slog.New(slog.NewJSONHandler(io.Discard, nil)),
		PlazoInactividad: time.Minute,
		Credencial:       linea,
		Usuarios:         usuarios,
		DuracionSesion:   time.Hour,
		Metricas:         metricasDePrueba(t),
	})
	if err != nil {
		t.Fatalf("Nuevo: %v", err)
	}
	return s
}

// Antes del primer «Refrescar métricas», el panel no inventa un tamaño: lo
// dice explícitamente, en vez de sugerir 0 B como si ya se hubiera medido.
func TestPanelMuestraSinMedirTodaviaAntesDeRefrescar(t *testing.T) {
	s := servidorParaMetricas(t, map[string]int64{"juan": 999})
	cuerpo := peticionConSesion(t, s, "/administracion").Body.String()
	if !strings.Contains(cuerpo, "sin medir todavía") {
		t.Errorf("el panel no dice «sin medir todavía» antes del primer refresco:\n%s", cuerpo)
	}
}

// EL RECORRIDO CENTRAL DE LA ETAPA: la primera medida no tiene variación que
// mostrar —no hay con qué compararla—, y la segunda sí, con signo.
func TestRefrescarMetricasMideYMuestraLaVariacionEnLaSegundaVez(t *testing.T) {
	bytes := map[string]int64{"juan": 2 * 1024 * 1024}
	s := servidorParaMetricas(t, bytes)

	if w := postConSesion(t, s, "/administracion/refrescar", url.Values{}); w.Code != http.StatusSeeOther {
		t.Fatalf("refrescar -> %d; se esperaba 303", w.Code)
	}
	cuerpo := peticionConSesion(t, s, "/administracion").Body.String()
	if !strings.Contains(cuerpo, "2.0 MB (primera medición)") {
		t.Fatalf("tras el primer refresco falta el tamaño o «primera medición»:\n%s", cuerpo)
	}

	// La carpeta de juan «crece» a 3 MB entre un refresco y el siguiente.
	bytes["juan"] = 3 * 1024 * 1024
	if w := postConSesion(t, s, "/administracion/refrescar", url.Values{}); w.Code != http.StatusSeeOther {
		t.Fatalf("segundo refrescar -> %d; se esperaba 303", w.Code)
	}
	cuerpo = peticionConSesion(t, s, "/administracion").Body.String()
	// html/template escapa el «+» como entidad HTML (&#43;) — es el mismo
	// carácter una vez que el navegador lo interpreta, ADR-0017 no lo cambia.
	if !strings.Contains(cuerpo, "3.0 MB (&#43;1.0 MB)") {
		t.Errorf("la segunda medida no muestra la variación con signo esperada:\n%s", cuerpo)
	}
}

// Una cuenta que nunca entró no tiene carpeta —ParaUsuario nunca la crea,
// ver fsposix/almacen.go— y eso NO es un fallo del refresco: cuenta como
// 0 bytes, porque Resumen devuelve almacen.ErrNoExiste sobre una raíz que
// no existe todavía.
func TestRefrescarMetricasCuentaComoCeroLaCuentaSinCarpeta(t *testing.T) {
	linea, err := autenticacion.Derivar(claveDePrueba, iteracionesDePrueba)
	if err != nil {
		t.Fatalf("Derivar: %v", err)
	}
	usuarios := registroDePrueba(t)
	if err := usuarios.Alta("nunca-entro", claveDeJuan); err != nil {
		t.Fatalf("Alta: %v", err)
	}
	s, err := Nuevo(Opciones{
		Almacen:          almacenVacio{},
		AlmacenDe:        almacenPorUsuarioDePrueba(almacenVacio{}), // Resumen -> ErrNoExiste
		PromoverUsuario:  promoverUsuarioDePrueba,
		Registro:         slog.New(slog.NewJSONHandler(io.Discard, nil)),
		PlazoInactividad: time.Minute,
		Credencial:       linea,
		Usuarios:         usuarios,
		DuracionSesion:   time.Hour,
		Metricas:         metricasDePrueba(t),
	})
	if err != nil {
		t.Fatalf("Nuevo: %v", err)
	}

	postConSesion(t, s, "/administracion/refrescar", url.Values{})

	cuerpo := peticionConSesion(t, s, "/administracion").Body.String()
	if !strings.Contains(cuerpo, "0 B (primera medición)") {
		t.Errorf("una cuenta sin carpeta debería medir 0 B, no fallar ni seguir «sin medir»:\n%s", cuerpo)
	}
}

// SI UNA CUENTA FALLA AL MEDIRSE, LAS DEMÁS NO SE QUEDAN SIN REFRESCAR: un
// error aislado se registra y se salta, no aborta el lote entero —al
// contrario que la baja (promoverUsuario), que si aborta entera a propósito.
func TestRefrescarMetricasContinuaSiUnaCuentaFalla(t *testing.T) {
	linea, err := autenticacion.Derivar(claveDePrueba, iteracionesDePrueba)
	if err != nil {
		t.Fatalf("Derivar: %v", err)
	}
	usuarios := registroDePrueba(t)
	for _, nombre := range []string{"rota", "sana"} {
		if err := usuarios.Alta(nombre, claveDeJuan); err != nil {
			t.Fatalf("Alta(%s): %v", nombre, err)
		}
	}
	s, err := Nuevo(Opciones{
		Almacen: almacenVacio{},
		AlmacenDe: func(nombre string) (almacen.Almacen, error) {
			if nombre == "rota" {
				return nil, errors.New("simulado: no se pudo abrir el almacén de esta cuenta")
			}
			return almacenConTamano{bytes: 4096}, nil
		},
		PromoverUsuario:  promoverUsuarioDePrueba,
		Registro:         slog.New(slog.NewJSONHandler(io.Discard, nil)),
		PlazoInactividad: time.Minute,
		Credencial:       linea,
		Usuarios:         usuarios,
		DuracionSesion:   time.Hour,
		Metricas:         metricasDePrueba(t),
	})
	if err != nil {
		t.Fatalf("Nuevo: %v", err)
	}

	if w := postConSesion(t, s, "/administracion/refrescar", url.Values{}); w.Code != http.StatusSeeOther {
		t.Fatalf("refrescar -> %d; se esperaba 303 aunque una cuenta fallara", w.Code)
	}

	if _, medida := s.metricas.Todas()["sana"]; !medida {
		t.Error("«sana» no se midió porque «rota» falló: el lote se abortó entero")
	}
	if _, medida := s.metricas.Todas()["rota"]; medida {
		t.Error("«rota» aparece medida a pesar de que su almacén devolvía error")
	}
}
