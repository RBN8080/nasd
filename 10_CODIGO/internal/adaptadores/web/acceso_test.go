package web

import (
	"context"
	"io"
	"iter"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"nasd/internal/almacen"
	"nasd/internal/autenticacion"
)

// Pruebas del acceso con varios usuarios — ADR-0055, etapa 1b.
//
// LO QUE SE COMPRUEBA AQUÍ es el REPARTO: quién resulta ser quien pide, y qué
// almacén acaba recibiendo el manejador que le atiende. El aislamiento en sí
// —que la carpeta de uno no alcance la de otro— se mide contra disco de verdad
// en fsposix/aislamiento_test.go, y no se repite aquí con un simulacro: una
// prueba de contención contra un doble solo demuestra que el doble contiene.

// almacenMarcado sabe de quién es. Es lo que permite afirmar QUIÉN recibió el
// almacén, en lugar de suponerlo porque la petición respondió 200.
type almacenMarcado struct {
	almacenVacio
	dueno string
}

func (a *almacenMarcado) Listar(context.Context, almacen.RutaSegura) iter.Seq2[almacen.Entrada, error] {
	return func(yield func(almacen.Entrada, error) bool) {
		ruta, _ := almacen.NuevaRuta("de-" + a.dueno + ".txt")
		yield(almacen.Entrada{Ruta: ruta, Nombre: ruta.Nombre()}, nil)
	}
}

// reparto registra a quién se le pidió un almacén y con cuál se le respondió.
type reparto struct {
	mu      sync.Mutex
	pedidos []string
	raiz    *almacenMarcado
}

func (rp *reparto) para(usuario string) (almacen.Almacen, error) {
	rp.mu.Lock()
	defer rp.mu.Unlock()
	rp.pedidos = append(rp.pedidos, usuario)
	return &almacenMarcado{dueno: usuario}, nil
}

func (rp *reparto) pedidosHechos() []string {
	rp.mu.Lock()
	defer rp.mu.Unlock()
	return append([]string(nil), rp.pedidos...)
}

const claveDeJuan = "clave-de-juan-bastante-larga"

// servidorMultiusuario deja un nodo con el superusuario y una cuenta dada de
// alta de verdad, pasando por el registro y su derivación.
func servidorMultiusuario(t *testing.T) (*Servidor, *reparto) {
	t.Helper()
	linea, err := autenticacion.Derivar(claveDePrueba, iteracionesDePrueba)
	if err != nil {
		t.Fatalf("Derivar: %v", err)
	}
	usuarios := registroDePrueba(t)
	if err := usuarios.Alta("juan", claveDeJuan); err != nil {
		t.Fatalf("Alta(juan): %v", err)
	}
	rp := &reparto{raiz: &almacenMarcado{dueno: "raiz"}}
	s, err := Nuevo(Opciones{
		Almacen:          rp.raiz,
		AlmacenDe:        rp.para,
		Registro:         slog.New(slog.NewJSONHandler(io.Discard, nil)),
		PlazoInactividad: time.Minute,
		Credencial:       linea,
		Usuarios:         usuarios,
		DuracionSesion:   time.Hour,
	})
	if err != nil {
		t.Fatalf("Nuevo: %v", err)
	}
	return s, rp
}

// entrar hace un acceso real y devuelve las cookies que el servidor entregó.
func entrar(t *testing.T, h http.Handler, usuario, clave string) []*http.Cookie {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/acceso", strings.NewReader(url.Values{
		"usuario": {usuario},
		"clave":   {clave},
	}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.ServeHTTP(w, r)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("acceso de %q -> %d; se esperaba 303", usuario, w.Code)
	}
	return w.Result().Cookies()
}

func cookieLlamada(cookies []*http.Cookie, nombre string) *http.Cookie {
	for _, c := range cookies {
		if c.Name == nombre {
			return c
		}
	}
	return nil
}

// listadoCon pide el listado con esas cookies y devuelve el cuerpo.
func listadoCon(t *testing.T, h http.Handler, cookies ...*http.Cookie) string {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)
	for _, c := range cookies {
		if c != nil {
			r.AddCookie(c)
		}
	}
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("GET / -> %d; se esperaba 200", w.Code)
	}
	return w.Body.String()
}

// LA PRUEBA CENTRAL DE LA ETAPA: quien entra como juan recibe el almacén de
// juan, y no el de la casa.
func TestCadaUsuarioRecibeElAlmacenDeSuCarpeta(t *testing.T) {
	s, rp := servidorMultiusuario(t)
	h := s.Rutas()

	cuerpo := listadoCon(t, h, cookieLlamada(entrar(t, h, "juan", claveDeJuan), nombreCookie))

	if !strings.Contains(cuerpo, "de-juan.txt") {
		t.Error("el listado de juan no salió de su almacén")
	}
	if strings.Contains(cuerpo, "de-raiz.txt") {
		t.Fatal("A JUAN LE LLEGÓ EL ALMACÉN DE LA RAÍZ: está viendo el disco entero")
	}
	if p := rp.pedidosHechos(); len(p) == 0 || p[len(p)-1] != "juan" {
		t.Errorf("se pidió el almacén de %v; se esperaba el de juan", p)
	}
}

// El superusuario NO pasa por ParaUsuario: su almacén es la raíz, sin prefijo,
// y por eso su comportamiento no cambia respecto de antes de existir esto.
func TestElSuperusuarioSigueViendoLaRaiz(t *testing.T) {
	s, rp := servidorMultiusuario(t)
	h := s.Rutas()

	cuerpo := listadoCon(t, h,
		cookieLlamada(entrar(t, h, autenticacion.NombreSuperusuario, claveDePrueba), nombreCookie))

	if !strings.Contains(cuerpo, "de-raiz.txt") {
		t.Error("el superusuario no recibió el almacén de la raíz")
	}
	if p := rp.pedidosHechos(); len(p) != 0 {
		t.Errorf("se pidió una carpeta de usuario para el superusuario: %v", p)
	}
}

// EL INTENTO OBVIO DE ESCALADA: quien entra como juan cambia a mano la cookie
// que recuerda el nombre y pide el listado como si fuera el administrador.
//
// No funciona, y el motivo es de diseño: esa cookie solo evita teclear el
// nombre en el formulario. Quién eres lo dice la sesión, que vive en el
// servidor y se fija al verificar la contraseña.
func TestCambiarLaCookieDelNombreNoCambiaQuienEres(t *testing.T) {
	s, _ := servidorMultiusuario(t)
	h := s.Rutas()

	sesion := cookieLlamada(entrar(t, h, "juan", claveDeJuan), nombreCookie)
	suplantacion := &http.Cookie{
		Name:  cookieUsuario,
		Value: autenticacion.NombreSuperusuario,
	}

	cuerpo := listadoCon(t, h, sesion, suplantacion)
	if strings.Contains(cuerpo, "de-raiz.txt") {
		t.Fatal("SUPLANTACIÓN: cambiando una cookie del navegador se llegó a la raíz")
	}
	if !strings.Contains(cuerpo, "de-juan.txt") {
		t.Error("juan dejó de ver lo suyo")
	}
}

// La cookie de sesión no puede llevar el nombre dentro: si lo llevara, sería
// el cliente quien dice quién es.
func TestLaCookieDeSesionNoLlevaElNombreDentro(t *testing.T) {
	s, _ := servidorMultiusuario(t)
	h := s.Rutas()

	c := cookieLlamada(entrar(t, h, "juan", claveDeJuan), nombreCookie)
	if c == nil {
		t.Fatal("no se entregó cookie de sesión")
	}
	if strings.Contains(c.Value, "juan") {
		t.Errorf("el testigo de sesión contiene el nombre del usuario: %q", c.Value)
	}
}

// RN-06 llevado al caso nuevo: la contraseña sola no abre nada, porque el
// servidor no puede saber de quién es sin probarla contra todas — que es
// justo lo que se descartó por costar 3.6 s por cuenta.
func TestSinNombreNoSeEntra(t *testing.T) {
	s, _ := servidorMultiusuario(t)
	h := s.Rutas()

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/acceso",
		strings.NewReader(url.Values{"clave": {claveDeJuan}}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Accept", "text/html")
	h.ServeHTTP(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("contraseña sin nombre -> %d; se esperaba 401", w.Code)
	}
	if c := cookieLlamada(w.Result().Cookies(), nombreCookie); c != nil && c.Value != "" {
		t.Fatal("se abrió sesión sin saber de quién era")
	}
}

// CWE-208 y enumeración de cuentas: que la cuenta exista o no NO puede
// cambiar la respuesta. Si la cambiara, el formulario sería un listador de
// cuentas: se prueban nombres y se lee lo que contesta.
//
// EL EXPERIMENTO ESTÁ MONTADO PARA AISLAR ESA ÚNICA VARIABLE. Se teclea
// SIEMPRE el mismo nombre y la misma contraseña equivocada, y lo único que
// cambia entre las dos mitades es si esa cuenta está dada de alta. Comparar en
// cambio dos nombres distintos no probaría nada: los cuerpos diferirían porque
// el formulario devuelve escrito lo que el propio cliente tecleó, que es algo
// que el atacante ya sabe.
func TestQueLaCuentaExistaNoCambiaLaRespuesta(t *testing.T) {
	intentar := func(t *testing.T, conCuenta bool) (int, string) {
		t.Helper()
		linea, err := autenticacion.Derivar(claveDePrueba, iteracionesDePrueba)
		if err != nil {
			t.Fatalf("Derivar: %v", err)
		}
		usuarios := registroDePrueba(t)
		if conCuenta {
			if err := usuarios.Alta("juan", claveDeJuan); err != nil {
				t.Fatalf("Alta(juan): %v", err)
			}
		}
		s, err := Nuevo(Opciones{
			Almacen:        almacenVacio{},
			AlmacenDe:      almacenPorUsuarioDePrueba(almacenVacio{}),
			Registro:       slog.New(slog.NewJSONHandler(io.Discard, nil)),
			Credencial:     linea,
			Usuarios:       usuarios,
			DuracionSesion: time.Hour,
		})
		if err != nil {
			t.Fatalf("Nuevo: %v", err)
		}
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/acceso", strings.NewReader(url.Values{
			"usuario": {"juan"},
			"clave":   {"esta-no-es-la-suya"},
		}.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Accept", "text/html")
		s.Rutas().ServeHTTP(w, r)
		return w.Code, w.Body.String()
	}

	codigoConCuenta, cuerpoConCuenta := intentar(t, true)
	codigoSinCuenta, cuerpoSinCuenta := intentar(t, false)

	if codigoConCuenta != codigoSinCuenta {
		t.Errorf("códigos distintos: con cuenta %d, sin cuenta %d",
			codigoConCuenta, codigoSinCuenta)
	}
	if cuerpoConCuenta != cuerpoSinCuenta {
		t.Error("la respuesta delata si la cuenta existe: los cuerpos difieren")
	}
	if !strings.Contains(cuerpoConCuenta, avisoAccesoFallido) {
		t.Errorf("no se mostró el aviso único; cuerpo: %q", cuerpoConCuenta)
	}
}

// El aparato recuerda el nombre y después solo pide la contraseña, enseñando
// LA INICIAL Y NADA MÁS.
func TestTrasEntrarSoloSePideLaContrasenaYSeEnsenaLaInicial(t *testing.T) {
	s, _ := servidorMultiusuario(t)
	h := s.Rutas()

	recuerdo := cookieLlamada(entrar(t, h, "juan", claveDeJuan), cookieUsuario)
	if recuerdo == nil {
		t.Fatal("no se entregó la cookie que recuerda el nombre")
	}
	if !recuerdo.HttpOnly {
		t.Error("la cookie del nombre debe ser HttpOnly: no la necesita el JavaScript")
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/acceso", nil)
	r.AddCookie(recuerdo)
	h.ServeHTTP(w, r)
	cuerpo := w.Body.String()

	if strings.Contains(cuerpo, `name="usuario"`) {
		t.Error("se volvió a pedir el nombre en un aparato que ya lo sabía")
	}
	if !strings.Contains(cuerpo, "¿No eres J?, cambiar de usuario") {
		t.Errorf("falta el aviso con la inicial; cuerpo: %q", cuerpo)
	}
	// El nombre completo NO se enseña: la inicial basta para reconocerse y no
	// delata de quién es la cuenta a quien mire la pantalla.
	if strings.Contains(cuerpo, "juan") {
		t.Error("el formulario enseña el nombre completo, no solo la inicial")
	}
}

// «cambiar de usuario» tiene que olvidar el nombre de verdad, no solo dejar de
// enseñarlo.
func TestCambiarDeUsuarioOlvidaElNombre(t *testing.T) {
	s, _ := servidorMultiusuario(t)
	h := s.Rutas()

	recuerdo := cookieLlamada(entrar(t, h, "juan", claveDeJuan), cookieUsuario)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/acceso?cambiar=1", nil)
	r.AddCookie(recuerdo)
	h.ServeHTTP(w, r)

	if !strings.Contains(w.Body.String(), `name="usuario"`) {
		t.Error("tras cambiar de usuario debía volver a pedirse el nombre")
	}
	olvido := cookieLlamada(w.Result().Cookies(), cookieUsuario)
	if olvido == nil || olvido.MaxAge >= 0 {
		t.Error("la cookie del nombre no se retiró del navegador")
	}
}

// Salir cierra la sesión pero NO olvida el nombre: si lo olvidara, habría que
// teclearlo cada vez y la cookie no serviría más que el primer día.
func TestSalirNoOlvidaElNombre(t *testing.T) {
	s, _ := servidorMultiusuario(t)
	h := s.Rutas()

	cookies := entrar(t, h, "juan", claveDeJuan)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/salir", nil)
	r.AddCookie(cookieLlamada(cookies, nombreCookie))
	h.ServeHTTP(w, r)

	if c := cookieLlamada(w.Result().Cookies(), cookieUsuario); c != nil && c.MaxAge < 0 {
		t.Error("salir borró el nombre recordado; eso es «cambiar de usuario», no «salir»")
	}
}

// P5 llevado al cableado: si faltan las piezas del acceso por usuario, no se
// arranca. Sin AlmacenDe, un usuario válido entraría y se encontraría un error
// interno; sin Usuarios, ninguna cuenta existiría y la única pista sería
// «contraseña incorrecta» para una contraseña buena.
func TestSinLasPiezasDelAccesoPorUsuarioNoArranca(t *testing.T) {
	linea, err := autenticacion.Derivar(claveDePrueba, iteracionesDePrueba)
	if err != nil {
		t.Fatalf("Derivar: %v", err)
	}
	base := func() Opciones {
		return Opciones{
			Almacen:        almacenVacio{},
			AlmacenDe:      almacenPorUsuarioDePrueba(almacenVacio{}),
			Registro:       slog.New(slog.NewJSONHandler(io.Discard, nil)),
			Credencial:     linea,
			Usuarios:       registroDePrueba(t),
			DuracionSesion: time.Hour,
		}
	}
	sinAlmacenDe := base()
	sinAlmacenDe.AlmacenDe = nil
	if _, err := Nuevo(sinAlmacenDe); err == nil {
		t.Error("arrancó sin saber cómo acotar el volumen a un usuario")
	}
	sinUsuarios := base()
	sinUsuarios.Usuarios = nil
	if _, err := Nuevo(sinUsuarios); err == nil {
		t.Error("arrancó sin registro de usuarios")
	}
}

// El estado del nodo es solo del superusuario — decisión del responsable,
// 2026-08-06. Publica temperatura, capacidad y ritmo de uso de TODO el nodo:
// a un usuario normal no le informa de nada suyo y le entrega reconocimiento
// del sistema entero.
//
// SE COMPRUEBAN LAS DOS VÍAS POR SEPARADO, no una y por inspección la otra:
// /estado/flujo publica exactamente lo mismo y de forma continua, así que
// cerrar solo la página dejaría abierta la puerta de al lado. Es lo que D-21
// obliga a verificar vía por vía.
func TestSoloElSuperusuarioAlcanzaElEstado(t *testing.T) {
	s, _ := servidorMultiusuario(t)
	h := s.Rutas()

	// EL CONTEXTO CANCELADO NO ES UN TRUCO: /estado/flujo es un flujo de
	// eventos que por diseño no termina hasta que el cliente se va (ADR-0051),
	// así que pedirlo sin nada que lo corte cuelga la prueba — pasó al
	// escribirla. Cancelar el contexto es exactamente lo que hace un navegador
	// al cerrar la pestaña, y no altera lo que se está midiendo: el rechazo
	// ocurre ANTES, en la envoltura.
	pedir := func(ruta string, sesion *http.Cookie) int {
		ctx, cancelar := context.WithCancel(context.Background())
		cancelar()
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", ruta, nil).WithContext(ctx)
		r.AddCookie(sesion)
		h.ServeHTTP(w, r)
		return w.Code
	}

	deJuan := cookieLlamada(entrar(t, h, "juan", claveDeJuan), nombreCookie)
	for _, ruta := range []string{"/estado", "/estado/flujo"} {
		if c := pedir(ruta, deJuan); c != http.StatusForbidden {
			t.Errorf("GET %s como usuario normal -> %d; se esperaba 403", ruta, c)
		}
	}

	// Y al superusuario NO se le cierra: la regla separa, no bloquea a todos.
	deAdmin := cookieLlamada(
		entrar(t, h, autenticacion.NombreSuperusuario, claveDePrueba), nombreCookie)
	for _, ruta := range []string{"/estado", "/estado/flujo"} {
		if c := pedir(ruta, deAdmin); c == http.StatusForbidden {
			t.Errorf("GET %s como superusuario -> 403; le corresponde verlo", ruta)
		}
	}
}

// Y la barra no ofrece una puerta que va a responder 403. Es cortesía, no
// control —el control está en la prueba de arriba—, pero un botón que falla
// siempre es un defecto de interfaz.
func TestLaBarraSoloOfreceEstadoAlSuperusuario(t *testing.T) {
	s, _ := servidorMultiusuario(t)
	h := s.Rutas()

	deJuan := listadoCon(t, h, cookieLlamada(entrar(t, h, "juan", claveDeJuan), nombreCookie))
	if strings.Contains(deJuan, `href="/estado"`) {
		t.Error("la barra de un usuario normal ofrece «Estado»")
	}

	deAdmin := listadoCon(t, h, cookieLlamada(
		entrar(t, h, autenticacion.NombreSuperusuario, claveDePrueba), nombreCookie))
	if !strings.Contains(deAdmin, `href="/estado"`) {
		t.Error("al superusuario le desapareció «Estado» de la barra")
	}
}

// El atajo «Usuarios» a homeUsers/ es cortesía, igual que «Estado»: NO abre
// ninguna puerta nueva —esa carpeta ya era navegable desde la raíz— y por eso
// no necesita su propia envoltura en el servidor. Solo se ofrece a quien de
// verdad puede sacarle algo: para un usuario normal, /ver/homeUsers cae dentro
// de SU carpeta, no de la casa, y no encontraría nada al pulsarlo.
func TestLaBarraSoloOfreceUsuariosAlSuperusuario(t *testing.T) {
	s, _ := servidorMultiusuario(t)
	h := s.Rutas()

	deJuan := listadoCon(t, h, cookieLlamada(entrar(t, h, "juan", claveDeJuan), nombreCookie))
	if strings.Contains(deJuan, `href="/ver/homeUsers"`) {
		t.Error("la barra de un usuario normal ofrece «Usuarios»")
	}

	deAdmin := listadoCon(t, h, cookieLlamada(
		entrar(t, h, autenticacion.NombreSuperusuario, claveDePrueba), nombreCookie))
	if !strings.Contains(deAdmin, `href="/ver/homeUsers"`) {
		t.Error("al superusuario no se le ofrece el atajo «Usuarios»")
	}
}
