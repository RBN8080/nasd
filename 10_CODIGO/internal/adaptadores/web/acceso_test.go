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

type reparto struct {
	mu            sync.Mutex
	pedidos       []string
	raiz          *almacenMarcado
	promovidos    []string
	errorPromover error
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

func (rp *reparto) promover(nombre string) error {
	rp.mu.Lock()
	defer rp.mu.Unlock()
	if rp.errorPromover != nil {
		return rp.errorPromover
	}
	rp.promovidos = append(rp.promovidos, nombre)
	return nil
}

func (rp *reparto) promovidosHechos() []string {
	rp.mu.Lock()
	defer rp.mu.Unlock()
	return append([]string(nil), rp.promovidos...)
}

const claveDeJuan = "clave-de-juan-bastante-larga"


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
		PromoverUsuario:  rp.promover,
		Registro:         slog.New(slog.NewJSONHandler(io.Discard, nil)),
		PlazoInactividad: time.Minute,
		Credencial:       linea,
		Usuarios:         usuarios,
		DuracionSesion:   time.Hour,
		Metricas:         metricasDePrueba(t),
		Seguridad:        seguridadDePrueba(t),
		Cuarentena:       cuarentenaDePrueba(t),
		Lista:            listaDePrueba(t),
		Novedades:        novedadesDePrueba(t),
		Hallazgos:        hallazgosDePrueba(t),
		Conexiones:       conexionesDePrueba(t),
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

func desdeCasa(r *http.Request) *http.Request {
	r.RemoteAddr = "192.168.1.18:5000"
	return r
}

func listadoCon(t *testing.T, h http.Handler, cookies ...*http.Cookie) string {
	t.Helper()
	w := httptest.NewRecorder()
	r := desdeCasa(httptest.NewRequest("GET", "/", nil))
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

// EL ATERRIZAJE DEPENDE DE LA CUENTA, y las dos mitades importan por igual:
// /resumen vive detrás de soloSuperusuario, así que mandar ahí a juan sería
// recibirlo con un 403 nada más teclear bien la contraseña.
func TestElAterrizajeDependeDeLaCuenta(t *testing.T) {
	s, _ := servidorMultiusuario(t)

	for _, c := range []struct{ usuario, clave, destino string }{
		{autenticacion.NombreSuperusuario, claveDePrueba, "/resumen"},
		{"juan", claveDeJuan, "/"},
	} {
		w := postAcceso(t, s, credenciales(c.usuario, c.clave), "", "")
		if w.Code != http.StatusSeeOther {
			t.Fatalf("acceso de %q -> %d; se esperaba 303", c.usuario, w.Code)
		}
		if destino := w.Header().Get("Location"); destino != c.destino {
			t.Errorf("%q aterriza en %q; se esperaba %q", c.usuario, destino, c.destino)
		}
	}
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
			Almacen:         almacenVacio{},
			AlmacenDe:       almacenPorUsuarioDePrueba(almacenVacio{}),
			PromoverUsuario: promoverUsuarioDePrueba,
			Registro:        slog.New(slog.NewJSONHandler(io.Discard, nil)),
			Credencial:      linea,
			Usuarios:        usuarios,
			DuracionSesion:  time.Hour,
			Metricas:        metricasDePrueba(t),
			Seguridad:       seguridadDePrueba(t),
			Cuarentena:      cuarentenaDePrueba(t),
			Lista:           listaDePrueba(t),
			Novedades:       novedadesDePrueba(t),
			Hallazgos:       hallazgosDePrueba(t),
			Conexiones:      conexionesDePrueba(t),
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
			Almacen:         almacenVacio{},
			AlmacenDe:       almacenPorUsuarioDePrueba(almacenVacio{}),
			PromoverUsuario: promoverUsuarioDePrueba,
			Registro:        slog.New(slog.NewJSONHandler(io.Discard, nil)),
			Credencial:      linea,
			Usuarios:        registroDePrueba(t),
			DuracionSesion:  time.Hour,
			Metricas:        metricasDePrueba(t),
			Seguridad:       seguridadDePrueba(t),
			Cuarentena:      cuarentenaDePrueba(t),
			Lista:           listaDePrueba(t),
			Novedades:       novedadesDePrueba(t),
			Hallazgos:       hallazgosDePrueba(t),
			Conexiones:      conexionesDePrueba(t),
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
	// P-4, etapa 2: sin esto, una baja quitaría el acceso y dejaría la
	// carpeta perdida dentro de homeUsers/ sin que nada lo avisara.
	sinPromoverUsuario := base()
	sinPromoverUsuario.PromoverUsuario = nil
	if _, err := Nuevo(sinPromoverUsuario); err == nil {
		t.Error("arrancó sin poder promover la carpeta de un usuario al darlo de baja")
	}
}


func TestSoloElSuperusuarioAlcanzaElEstado(t *testing.T) {
	s, _ := servidorMultiusuario(t)
	h := s.Rutas()


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

// almacenConEntradas lista exactamente los nombres dados. Sirve para probar
// qué se esconde del listado y qué no, algo que almacenMarcado —una sola
// entrada fija— no permite comprobar.
type almacenConEntradas struct {
	almacenVacio
	nombres []string
}

func (a almacenConEntradas) Listar(context.Context, almacen.RutaSegura) iter.Seq2[almacen.Entrada, error] {
	return func(yield func(almacen.Entrada, error) bool) {
		for _, n := range a.nombres {
			r, err := almacen.NuevaRuta(n)
			if err != nil {
				yield(almacen.Entrada{}, err)
				return
			}
			if !yield(almacen.Entrada{Ruta: r, Nombre: n, EsDirectori: true}, nil) {
				return
			}
		}
	}
}

func TestHomeUsersSeEscondeSoloParaElSuperusuarioYSoloEnLaRaiz(t *testing.T) {
	linea, err := autenticacion.Derivar(claveDePrueba, iteracionesDePrueba)
	if err != nil {
		t.Fatalf("Derivar: %v", err)
	}
	usuarios := registroDePrueba(t)
	if err := usuarios.Alta("juan", claveDeJuan); err != nil {
		t.Fatalf("Alta(juan): %v", err)
	}
	raiz := almacenConEntradas{nombres: []string{"homeUsers", "fotos"}}
	// La carpeta de juan tiene, dentro de SU espacio, una subcarpeta que por
	// coincidencia se llama igual que la especial — y por eso debe verse.
	deJuan := almacenConEntradas{nombres: []string{"homeUsers", "recibos"}}

	s, err := Nuevo(Opciones{
		Almacen:          raiz,
		AlmacenDe:        almacenPorUsuarioDePrueba(deJuan),
		PromoverUsuario:  promoverUsuarioDePrueba,
		Registro:         slog.New(slog.NewJSONHandler(io.Discard, nil)),
		PlazoInactividad: time.Minute,
		Credencial:       linea,
		Usuarios:         usuarios,
		DuracionSesion:   time.Hour,
		Metricas:         metricasDePrueba(t),
		Seguridad:        seguridadDePrueba(t),
		Cuarentena:       cuarentenaDePrueba(t),
		Lista:            listaDePrueba(t),
		Novedades:        novedadesDePrueba(t),
		Hallazgos:        hallazgosDePrueba(t),
		Conexiones:       conexionesDePrueba(t),
	})
	if err != nil {
		t.Fatalf("Nuevo: %v", err)
	}
	h := s.Rutas()

	entrada := func(n string) string { return ">" + n + "</span>" }
	cuerpoAdmin := listadoCon(t, h, cookieLlamada(
		entrar(t, h, autenticacion.NombreSuperusuario, claveDePrueba), nombreCookie))
	if strings.Contains(cuerpoAdmin, entrada("homeUsers")) {
		t.Errorf("la raíz del superusuario sigue listando homeUsers:\n%s", cuerpoAdmin)
	}
	if !strings.Contains(cuerpoAdmin, entrada("fotos")) {
		t.Errorf("se escondió también una carpeta que no era homeUsers:\n%s", cuerpoAdmin)
	}

	cuerpoJuan := listadoCon(t, h, cookieLlamada(entrar(t, h, "juan", claveDeJuan), nombreCookie))
	if !strings.Contains(cuerpoJuan, entrada("homeUsers")) {
		t.Errorf("a juan se le escondió SU PROPIA carpeta, que solo coincide en el nombre:\n%s", cuerpoJuan)
	}
	if !strings.Contains(cuerpoJuan, entrada("recibos")) {
		t.Errorf("no aparece la otra carpeta de juan:\n%s", cuerpoJuan)
	}
}
