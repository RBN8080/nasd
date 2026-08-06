package web

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"nasd/internal/almacen"
	"nasd/internal/autenticacion"
)

// Autenticación de la web — RF-15, D-14 / ADR-0021.
//
// RN-06 es vinculante: la autenticación se entrega ANTES que cualquier
// operación destructiva. Este archivo es ese «antes».

const (
	nombreCookie = "nas_sesion"

	// cookieUsuario recuerda QUIÉN usa este aparato, para no volver a pedir el
	// nombre. No es una credencial y no da acceso a nada: solo evita teclearlo.
	//
	// EXISTE POR UNA MEDICIÓN. Se propuso deducir el usuario a partir de la
	// contraseña sola; se cronometró una verificación en el nodo y son 3.6 s
	// (600 000 iteraciones de PBKDF2 sobre un A53 sin aceleración). Deducirlo
	// obliga a probar contra cada cuenta —cinco usuarios, ~18 s por intento, y
	// un intento FALLIDO cuesta igual—, con lo que el formulario de acceso se
	// convierte en un amplificador que clava el único núcleo desde Internet.
	// Recordar el nombre cuesta una cookie y deja una sola derivación por
	// intento. ADR-0055.
	cookieUsuario = "nas_usuario"

	// El nombre se recuerda mucho más que la sesión: la sesión caduca porque
	// da acceso, el nombre no da acceso a nada.
	duracionRecuerdo = 365 * 24 * time.Hour

	// Un solo mensaje para todos los fallos de acceso. Distinguir «ese usuario
	// no existe» de «esa contraseña no es» convertiría el formulario en un
	// listador de cuentas: bastaría probar nombres y leer la respuesta.
	avisoAccesoFallido = "Usuario o contraseña incorrectos."

	// Iteraciones de PBKDF2. Recomendación OWASP vigente para HMAC-SHA256.
	//
	// MEDIDO EN EL NODO (2026-07-31): 3 232 ms por verificación en el A53 del
	// 3B+, que no tiene aceleración criptográfica. Es lento a propósito —de
	// eso trata un KDF— y solo se paga al iniciar sesión.
	//
	// No se baja el coste para «ir más rápido»: el flanco que abre no es el
	// hash sino la saturación de CPU, y eso se ataca limitando la tasa
	// (limitadorAcceso), no debilitando la derivación.
	IteracionesPBKDF2 = 600000

	// Ventana y tope de intentos fallidos por origen antes de rechazar.
	// Con 3.2 s por verificación, 5 intentos son ~16 s de CPU: suficiente
	// para un despiste humano, insuficiente para un ataque. [R]
	maxIntentosFallidos = 5
	ventanaIntentos     = 5 * time.Minute
)

// limitadorAcceso frena los intentos de inicio de sesión.
//
// Dos controles, y hacen cosas distintas:
//
//  1. El mutex SERIALIZA las verificaciones. Una sola derivación a la vez,
//     así que por muchos clientes que empujen, el gasto queda acotado a un
//     núcleo. Importa porque el nodo ya opera con el límite térmico blando
//     activo (RNF-11) y las cuatro CPU al 100 % lo empeorarían.
//
//  2. El contador por origen corta la repetición. Sin él, serializar solo
//     convierte la saturación en una cola infinita.
type limitadorAcceso struct {
	verificar sync.Mutex

	mu       sync.Mutex
	fallidos map[string]*intentos
}

type intentos struct {
	n     int
	desde time.Time
}

func nuevoLimitador() *limitadorAcceso {
	return &limitadorAcceso{fallidos: make(map[string]*intentos)}
}

// permitido indica si ese origen puede intentarlo, y cuánto esperar si no.
func (l *limitadorAcceso) permitido(origen string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	it, ok := l.fallidos[origen]
	if !ok {
		return true, 0
	}
	if time.Since(it.desde) > ventanaIntentos {
		delete(l.fallidos, origen)
		return true, 0
	}
	if it.n >= maxIntentosFallidos {
		return false, ventanaIntentos - time.Since(it.desde)
	}
	return true, 0
}

func (l *limitadorAcceso) fallo(origen string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	it, ok := l.fallidos[origen]
	if !ok || time.Since(it.desde) > ventanaIntentos {
		l.fallidos[origen] = &intentos{n: 1, desde: time.Now()}
		return
	}
	it.n++
}

func (l *limitadorAcceso) acierto(origen string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.fallidos, origen)
}

// purgar limpia los contadores vencidos. Sin esto el mapa crecería con cada
// origen que falle alguna vez y jamás menguaría.
func (l *limitadorAcceso) purgar() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for k, it := range l.fallidos {
		if time.Since(it.desde) > ventanaIntentos {
			delete(l.fallidos, k)
			n++
		}
	}
	return n
}

func origenDe(r *http.Request) string {
	if h, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return h
	}
	return r.RemoteAddr
}

// exigirSesion protege todo lo que no sea el propio formulario de acceso.
//
// RF-15 pide literalmente que «sin sesión válida, toda ruta distinta del
// formulario de acceso responde 401 o 403». Se cumple al pie de la letra:
// se responde 401 — no una redirección 303, que sería más cómoda pero
// incumpliría el criterio— y el CUERPO del 401 lleva el formulario, así que
// el navegador muestra algo usable sin falsear el código de estado.
func (s *Servidor) exigirSesion(siguiente http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(nombreCookie)
		if err != nil {
			s.pedirAcceso(w, r, "")
			return
		}
		usuario, vigente := s.sesiones.Usuario(c.Value)
		// FALLO CERRADO: una sesión vigente pero sin dueño no pasa. No debería
		// existir —Abrir rechaza el nombre vacío—, y justo por eso, si alguna
		// vez aparece una, lo que NO puede hacer es acabar mirando la carpeta
		// del superusuario por descarte (ADR-0055).
		if !vigente || usuario == "" {
			s.pedirAcceso(w, r, "")
			return
		}
		siguiente.ServeHTTP(w, r.WithContext(
			context.WithValue(r.Context(), claveUsuario, usuario)))
	})
}

// claveUsuario nombra al usuario dentro del contexto de la petición. Es de un
// tipo propio y no una cadena: así ningún otro paquete puede escribir en esa
// misma clave, ni por accidente ni a propósito.
type claveDeContexto struct{}

var claveUsuario claveDeContexto

// usuarioDe devuelve de quién es la petición. Solo tiene valor detrás de
// exigirSesion, que es el único que lo pone.
func usuarioDe(r *http.Request) string {
	u, _ := r.Context().Value(claveUsuario).(string)
	return u
}

func (s *Servidor) pedirAcceso(w http.ResponseWriter, r *http.Request, aviso string) {
	s.pedirAccesoComo(w, r, usuarioRecordado(r), aviso)
}

// vistaAcceso es lo que ve el formulario.
//
// Usuario vacío significa «este aparato no sabe quién eres»: se pide el
// nombre. Con nombre, solo se pide la contraseña.
type vistaAcceso struct {
	Aviso   string
	Usuario string
	// Inicial es lo ÚNICO que se enseña de quien usa este aparato. Basta para
	// que uno se reconozca y no delata el nombre completo a quien lo coja
	// prestado o mire por encima del hombro.
	Inicial string
}

func (s *Servidor) pedirAccesoComo(w http.ResponseWriter, r *http.Request, usuario, aviso string) {
	w.Header().Set("Cache-Control", "no-store")
	if !aceptaHTML(r) {
		http.Error(w, "no autenticado", http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusUnauthorized)
	s.renderAcceso(w, usuario, aviso)
}

func (s *Servidor) renderAcceso(w http.ResponseWriter, usuario, aviso string) {
	v := vistaAcceso{Aviso: aviso, Usuario: usuario, Inicial: inicialDe(usuario)}
	if err := s.plantillas.ExecuteTemplate(w, "acceso.html", v); err != nil {
		s.reg.Error("render del formulario de acceso", "error", err)
	}
}

func inicialDe(usuario string) string {
	if usuario == "" {
		return ""
	}
	return strings.ToUpper(string([]rune(usuario)[0]))
}

// usuarioRecordado lee el nombre que este aparato tiene guardado.
//
// Se VALIDA aunque venga de nuestra propia cookie, porque una cookie la
// escribe el cliente y puede llegar con cualquier cosa dentro. Lo que no pase
// el filtro se trata como si no hubiera nombre: se vuelve a pedir.
func usuarioRecordado(r *http.Request) string {
	c, err := r.Cookie(cookieUsuario)
	if err != nil {
		return ""
	}
	return nombreAceptable(c.Value)
}

// nombreAceptable devuelve el nombre si puede ser de alguien, o "" si no.
// No dice si ese alguien existe: eso solo lo sabe quien verifica la
// contraseña, y responde siempre lo mismo (avisoAccesoFallido).
func nombreAceptable(v string) string {
	if v == autenticacion.NombreSuperusuario {
		return v
	}
	if autenticacion.NombreValido(v) != nil {
		return ""
	}
	return v
}

// aceptaHTML distingue a un navegador de un cliente de la API.
//
// No cambia el código de estado —siempre es 401— solo si el cuerpo lleva el
// formulario o un texto plano. Un navegador anuncia text/html; el fetch del
// cliente tus y curl mandan "*/*".
func aceptaHTML(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "text/html")
}

func (s *Servidor) mostrarAcceso(w http.ResponseWriter, r *http.Request) {
	// Si ya hay sesión, no tiene sentido pedirla otra vez.
	if c, err := r.Cookie(nombreCookie); err == nil && s.sesiones.Valida(c.Value) {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	usuario := usuarioRecordado(r)

	// «¿No eres J?, cambiar de usuario»: el aparato deja de dar por sabido
	// quién lo usa y vuelve a preguntar el nombre.
	if r.URL.Query().Has("cambiar") {
		s.olvidarUsuario(w)
		usuario = ""
	}

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	s.renderAcceso(w, usuario, "")
}

func (s *Servidor) procesarAcceso(w http.ResponseWriter, r *http.Request) {
	origen := origenDe(r)

	if ok, espera := s.limitador.permitido(origen); !ok {
		s.reg.Warn("acceso bloqueado por intentos repetidos",
			"origen", origen, "espera_s", int(espera.Seconds()))
		w.Header().Set("Retry-After", "300")
		s.pedirAcceso(w, r, "Demasiados intentos fallidos. Espere unos minutos.")
		return
	}
	if err := r.ParseForm(); err != nil {
		s.pedirAcceso(w, r, "Petición inválida.")
		return
	}
	// El nombre lo teclea quien entra la primera vez en este aparato; después
	// lo pone la cookie que lo recuerda. Lo tecleado manda, para que «cambiar
	// de usuario» funcione aunque la cookie siga ahí.
	usuario := strings.TrimSpace(r.PostFormValue("usuario"))
	if usuario == "" {
		usuario = usuarioRecordado(r)
	}
	clave := r.PostFormValue("clave")

	valida, conocido := s.verificarAcceso(usuario, clave)
	if !valida {
		s.limitador.fallo(origen)
		s.contadores.accesosFallidos.Add(1)
		// NUNCA se registra la contraseña ni parte de ella (04_SEGURIDAD §6).
		//
		// Y EL NOMBRE SOLO SI EXISTE. Con dos campos, tarde o temprano alguien
		// teclea su contraseña en la casilla del nombre; anotar lo que llegue
		// metería esa contraseña en el diario, que es exactamente lo que la
		// regla de arriba prohíbe. Un nombre que no existe no aporta nada al
		// registro y sí puede ser un secreto mal puesto.
		anotado := usuario
		if !conocido {
			anotado = "desconocido"
		}
		s.reg.Warn("intento de acceso fallido", "origen", origen, "usuario", anotado)
		s.pedirAccesoComo(w, r, nombreAceptable(usuario), avisoAccesoFallido)
		return
	}

	testigo, err := s.sesiones.Abrir(usuario)
	if err != nil {
		s.reg.Error("no se pudo abrir la sesión", "error", err)
		http.Error(w, "error interno", http.StatusInternalServerError)
		return
	}
	s.limitador.acierto(origen)
	s.reg.Info("sesión iniciada", "origen", origen, "usuario", usuario,
		"sesiones_abiertas", s.sesiones.Abiertas())

	// El aparato recuerda el nombre para no volver a pedirlo. Se renueva en
	// cada entrada: así un aparato en uso no vuelve a preguntarlo nunca.
	http.SetCookie(w, &http.Cookie{
		Name:     cookieUsuario,
		Value:    usuario,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(duracionRecuerdo.Seconds()),
		Secure:   r.TLS != nil,
	})

	http.SetCookie(w, &http.Cookie{
		Name:     nombreCookie,
		Value:    testigo,
		Path:     "/",
		HttpOnly: true,                 // inalcanzable desde JavaScript
		SameSite: http.SameSiteLaxMode, // frena el CSRF entre sitios
		MaxAge:   int(s.duracionSesion.Seconds()),
		// Secure SOLO si la petición llegó por TLS — ADR-0046, que supersede
		// a ADR-0018.
		//
		// Por qué condicional y no siempre: marcarla siempre rompería el
		// acceso por HTTP desde la LAN, porque el navegador no envía cookies
		// Secure sobre HTTP. Y no vale «usar siempre el nombre», porque desde
		// dentro de casa el nombre NO funciona: resuelve a la IP pública y el
		// router no hace NAT loopback, medido el 2026-08-01.
		//
		// Coste declarado: una sesión iniciada por HTTP en la LAN lleva
		// cookie sin Secure. Vive solo dentro de casa, donde D-18 ya aceptó
		// que el contenido viaja en claro. Desde Internet, siempre hay TLS y
		// siempre lleva Secure.
		Secure: r.TLS != nil,
	})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// verificarAcceso comprueba la contraseña de quien dice ser «usuario», y de
// paso informa de si ese nombre corresponde a alguien — lo segundo SOLO para
// el registro, nunca para la respuesta.
//
// Las dos ramas cuestan lo mismo: una derivación de PBKDF2 con las mismas
// iteraciones. La del superusuario contra su credencial; la de un usuario
// contra la suya o, si no existe, contra la de relleno del registro. Sin esa
// simetría, un cronómetro distinguiría los nombres reales de los inventados
// (CWE-208).
func (s *Servidor) verificarAcceso(usuario, clave string) (valida, conocido bool) {
	// Serializado: una derivación a la vez, pase lo que pase. Con 3.6 s cada
	// una en el nodo, cuatro en paralelo clavarían las cuatro CPU del 3B+, que
	// ya opera con el límite térmico blando activo (RNF-11).
	s.limitador.verificar.Lock()
	defer s.limitador.verificar.Unlock()

	if usuario == autenticacion.NombreSuperusuario {
		return autenticacion.Verificar(s.credencial, clave), true
	}
	// Un nombre que ni siquiera puede ser de nadie se rechaza sin gastar la
	// derivación, y eso NO filtra nada: las reglas del nombre son públicas y
	// cualquiera puede comprobarlas sin preguntarle al servidor. Lo que sí
	// filtraría —si un nombre VÁLIDO existe o no— cuesta siempre lo mismo,
	// porque de eso se encarga Verifica con su credencial de relleno.
	if nombreAceptable(usuario) == "" {
		return false, false
	}
	// SE RELEE EL REGISTRO ANTES DE MIRARLO. Las altas las hace otro proceso
	// —«nasd --crear-usuario»—, así que una copia cargada al arrancar se queda
	// vieja en cuanto se da de alta a alguien: pasó en el nodo el 2026-08-06,
	// la orden dijo «cuenta creada» y el servicio respondió que no existía.
	//
	// Si el archivo estuviera roto se sigue con lo que hay en memoria: quien
	// ya estaba dado de alta no se queda fuera por eso. Pero NO en silencio.
	if err := s.usuarios.Refrescar(); err != nil {
		s.reg.Error("no se pudo releer el registro de usuarios", "error", err)
	}
	_, conocido = s.usuarios.Buscar(usuario)
	return s.usuarios.Verifica(usuario, clave), conocido
}

// olvidarUsuario borra el nombre que este aparato recordaba.
func (s *Servidor) olvidarUsuario(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: cookieUsuario, Value: "", Path: "/", HttpOnly: true, MaxAge: -1,
	})
}

// almacenDeLaSesion devuelve el almacén ACOTADO a quien hace la petición.
//
// Es la única puerta por la que un manejador consigue con qué trabajar, y por
// eso está aquí y no repartida: quien atiende una petición no elige almacén,
// lo recibe ya acotado (ADR-0055).
func (s *Servidor) almacenDeLaSesion(r *http.Request) (almacen.Almacen, error) {
	usuario := usuarioDe(r)
	switch {
	case usuario == "":
		// No puede ocurrir detrás de exigirSesion, y precisamente por eso se
		// trata como error y no como «pues el del superusuario»: si algún día
		// ocurre, el fallo debe ser ruidoso y no una escalada silenciosa.
		return nil, errors.New("petición sin usuario en la sesión")
	case usuario == autenticacion.NombreSuperusuario:
		return s.almacenRaiz, nil
	}
	// Sin caché a propósito: ParaUsuario solo asegura que la carpeta existe, y
	// guardar el resultado añadiría estado compartido —con su cerrojo y sus
	// entradas rancias tras dar de baja a alguien— a cambio de ahorrar tres
	// llamadas al sistema por petición. No compensa.
	return s.abrirAlmacen(usuario)
}

// manejadorDeUsuario atiende una petición con el almacén de quien la hace.
//
// EL PARÁMETRO ES LA GARANTÍA, y por eso no es un campo del servidor: un
// manejador no puede olvidarse de acotar lo que ve, porque no tiene manera de
// conseguir el almacén sin recibirlo ya acotado. La versión con campo
// compartido compila igual de bien y se equivoca en silencio.
type manejadorDeUsuario func(http.ResponseWriter, *http.Request, almacen.Almacen)

// soloSuperusuario cierra una ruta a todo el que no sea el responsable.
//
// LA REGLA VIVE AQUÍ Y NO EN LA PLANTILLA. Esconder el botón «Estado» de la
// barra no impide teclear /estado, y esa confusión —creer que una interfaz que
// no ofrece algo lo impide— es justo la asimetría que D-21 obliga a comprobar
// vía por vía. La plantilla también lo esconde, pero como cortesía: para no
// ofrecer una puerta que va a responder 403.
//
// 403 y no 404: quien pide ya está autenticado y la ruta existe. Fingir que no
// existe no oculta nada —está en la barra del superusuario y en el manual— y
// convertiría un «no te toca» en un «esto está roto».
func (s *Servidor) soloSuperusuario(siguiente http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if usuarioDe(r) != autenticacion.NombreSuperusuario {
			s.reg.Warn("acceso a una ruta de administración sin serlo",
				"usuario", usuarioDe(r), "ruta", r.URL.Path, "origen", origenDe(r))
			http.Error(w, "no autorizado", http.StatusForbidden)
			return
		}
		siguiente(w, r)
	}
}

func (s *Servidor) conAlmacen(f manejadorDeUsuario) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		alm, err := s.almacenDeLaSesion(r)
		if err != nil {
			s.reg.Error("no se pudo abrir la carpeta del usuario",
				"usuario", usuarioDe(r), "error", err)
			http.Error(w, "error interno", http.StatusInternalServerError)
			return
		}
		f(w, r, alm)
	}
}

func (s *Servidor) salir(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(nombreCookie); err == nil {
		s.sesiones.Cerrar(c.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name: nombreCookie, Value: "", Path: "/", HttpOnly: true, MaxAge: -1,
	})
	// El nombre NO se olvida al salir: salir es «he terminado», no «este ya no
	// es mi aparato». Olvidarlo obligaría a teclearlo cada vez y dejaría la
	// cookie sin más uso que el primer día. Para olvidarlo está «cambiar de
	// usuario», que es donde alguien lo pide a propósito.
	s.reg.Info("sesión cerrada", "origen", origenDe(r))
	http.Redirect(w, r, "/acceso", http.StatusSeeOther)
}
