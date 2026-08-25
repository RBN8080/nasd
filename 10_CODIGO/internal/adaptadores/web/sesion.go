package web

import (
	"context"
	"errors"
	"mime"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"nasd/internal/almacen"
	"nasd/internal/autenticacion"
	"nasd/internal/seguridad"
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

	// UN SOLO MENSAJE PARA TODOS LOS FALLOS DE ACCESO, y desde ADR-0072
	// «todos» significa todos, no solo los dos de la credencial.
	//
	// Distinguir «ese usuario no existe» de «esa contraseña no es» convertiría
	// el formulario en un listador de cuentas. El texto anterior —«Usuario o
	// contraseña incorrectos»— ya cubría ese par, pero se quedaba corto: al
	// endurecer el extremo aparecieron seis fallos más (límite por origen,
	// cuerpo excesivo, formato, formulario ilegible, sin capacidad y fallo al
	// abrir la sesión) y decir «contraseña incorrecta» ante ellos sería MENTIR
	// hacia dentro además de hacia fuera. Este texto es verdadero en los ocho
	// casos y sigue diciendo qué hacer.
	//
	// «Compruebe la contraseña» es una SUGERENCIA, no una afirmación: no dice
	// que estuviera mal, dice por dónde empezar. En el caso frecuente —una
	// letra de más— es exactamente la ayuda que hace falta.
	avisoAccesoFallido = "No se pudo iniciar sesión. Compruebe la contraseña y vuelva a intentarlo."

	// topeCuerpoAcceso acota el cuerpo del POST de acceso — ADR-0072 §10.
	//
	// # DE DÓNDE SALE EL NÚMERO
	//
	// El formulario manda dos campos y nada más:
	//
	//	usuario=<hasta 32 bytes>&clave=<contraseña>
	//
	// 32 es el máximo del nombre (autenticacion.NombreValido). Con el peor
	// caso de codificación de formulario —cada byte escapado como %XX, tres
	// caracteres por byte— eso son 96, más «usuario=» y «&clave=» son 111. Con
	// 1 KiB quedan ~913 para la contraseña, es decir 304 bytes aunque llegara
	// entera escapada, y unos 900 caracteres si es ASCII normal, que es lo que
	// pasa siempre.
	//
	// # POR QUÉ NO 10 MB «POR SI ACASO»
	//
	// Porque el cuerpo se lee ANTES de saber si la petición vale, y todo lo
	// que se acepte leer es memoria y tiempo que un desconocido puede gastar
	// del nodo sin coste suyo. Y porque no hace falta: la contraseña más larga
	// que este NAS puede tener en uso cabe cuarenta veces.
	//
	// # POR QUÉ NO MÁS PEQUEÑO
	//
	// Una contraseña legítima que no quepa se convierte en «no se pudo iniciar
	// sesión» sin explicación posible, que es el peor fallo que este extremo
	// puede tener. 1 KiB no puede alcanzar a ninguna.
	topeCuerpoAcceso = 1 << 10

	// tipoFormularioAcceso es el ÚNICO formato que el formulario usa. Se
	// comprueba contra la plantilla, no contra la costumbre: acceso.html es un
	// <form method="post"> sin enctype, y el de HTML por omisión es este.
	//
	// No se aceptan multipart, JSON ni nada más. Este extremo no los necesita,
	// y multipart en particular es la vía por la que un cuerpo pequeño obliga a
	// trabajo grande — el mismo motivo por el que ADR-0025 lo prohíbe en las
	// subidas.
	tipoFormularioAcceso = "application/x-www-form-urlencoded"

	// plazoCuerpoAcceso corta al cliente que abre el POST y manda el cuerpo a
	// cuentagotas — ADR-0072 §10.
	//
	// # POR QUÉ NO SE TOCA ReadTimeout DEL SERVIDOR
	//
	// Porque es ABSOLUTO y global, y está sin fijar a propósito: RF-09 exige
	// 4 GB íntegros y cualquier valor compatible con una subida de dos minutos
	// no protegería de nada aquí. Ver el comentario de HTTPServer, que lleva
	// escrito por qué eso no se «arregla».
	//
	// # POR QUÉ AQUÍ SÍ VALE UN PLAZO ABSOLUTO
	//
	// Porque este cuerpo mide como mucho 1 KiB. No hay ninguna transferencia
	// legítima larga que proteger: son dos campos que caben en un segmento
	// TCP. El plazo por actividad de plazos.go existe para lo contrario —lo que
	// avanza despacio pero avanza— y aplicarlo aquí sería usar el mecanismo
	// caro para el caso que no lo necesita.
	//
	// 10 s es el mismo orden que ReadHeaderTimeout, que gobierna la fase justo
	// anterior de la misma petición.
	plazoCuerpoAcceso = 10 * time.Second

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

	// topeEnEsperaKDF es cuántas peticiones pueden estar ESPERANDO turno para
	// derivar, además de la que está derivando — ADR-0072 §11.
	//
	// # POR QUÉ NO CERO
	//
	// Cero cola es lo más simple y es defendible en abstracto, pero rompe un
	// caso real de esta casa: tras reiniciar el servicio, las sesiones —que
	// viven en memoria a propósito— desaparecen y varias personas vuelven a
	// entrar a la vez. Con cero cola, quien pulse segundo ve «no se pudo
	// iniciar sesión» y concluye que su contraseña está mal. Un rechazo que se
	// lee como una avería es peor que la espera que evita.
	//
	// # POR QUÉ DOS Y NO MÁS
	//
	// Uno derivando y dos esperando son TRES accesos simultáneos atendidos sin
	// un fallo falso, y el registro admite como mucho 32 cuentas pero la casa
	// tiene cinco. El cuarto simultáneo se rechaza barato, que es lo que se
	// quería: sin cola ilimitada, ninguna goroutine esperando indefinidamente.
	topeEnEsperaKDF = 2

	// esperaMaximaKDF acota lo que una petición admitida a la sala puede
	// esperar. Sale de la medición, no del gusto:
	//
	//	derivación en el nodo (2026-07-31)      3.232 s
	//	peor espera del primero de la sala      3.232 s   (lo que queda del activo)
	//	peor espera del segundo de la sala      6.464 s   (el activo + el primero)
	//
	// 8 s cubre ese peor caso legítimo con margen para el límite térmico blando
	// (RNF-11), que puede hacer una derivación más lenta que la medida. Pasado
	// el plazo se rechaza y se dice por qué: SinCapacidadCripto.
	esperaMaximaKDF = 8 * time.Second
)

// limitadorAcceso frena los intentos de inicio de sesión.
//
// Dos controles, y hacen cosas distintas:
//
//  1. La ADMISIÓN acota el gasto criptográfico. Una sola derivación a la vez,
//     así que por muchos clientes que empujen, el gasto queda acotado a un
//     núcleo. Importa porque el nodo ya opera con el límite térmico blando
//     activo (RNF-11) y las cuatro CPU al 100 % lo empeorarían.
//
//  2. El contador por origen corta la repetición. Sin él, acotar el gasto
//     solo convierte la saturación en una cola infinita.
//
// # POR QUÉ LA ADMISIÓN YA NO ES UN MUTEX — ADR-0072 §11
//
// Era `verificar sync.Mutex`, y cumplía la mitad: una derivación a la vez, sí.
// Pero Lock() ESPERA PARA SIEMPRE, así que N peticiones simultáneas se
// convertían en N goroutines vivas, cada una con su conexión abierta y su
// petición en memoria, apiladas detrás de un trabajo de 3.2 s. El límite por
// origen no lo evita: son orígenes distintos, y cada uno tiene sus cinco
// intentos antes de que el contador lo pare.
//
// La propiedad que hacía falta no es «una a la vez», son dos:
//
//	derivaciones activas a la vez   <= 1
//	peticiones esperando turno      <= topeEnEsperaKDF, y cada una con plazo
//
// Un mutex da la primera y no puede dar la segunda. Dos canales dan las dos.
type limitadorAcceso struct {
	// activo es el presupuesto criptográfico: CAPACIDAD 1, y esa capacidad ES
	// la regla «como máximo una derivación simultánea». No es un número
	// ajustable — subirlo pondría dos A53 al 100 % en un nodo que ya se limita
	// solo por temperatura.
	activo chan struct{}
	// sala es la sala de espera: quien no entra a la primera puede aguardar
	// aquí, y solo caben topeEnEsperaKDF. El que llega y la encuentra llena se
	// va con un rechazo barato en vez de quedarse.
	sala chan struct{}
	// espera es el plazo máximo dentro de la sala. Es un CAMPO y no la
	// constante directamente para que las pruebas puedan comprobar el
	// vencimiento sin tardar ocho segundos de verdad; producción lo recibe de
	// nuevoLimitador y nadie más lo toca.
	espera time.Duration

	mu       sync.Mutex
	fallidos map[string]*intentos
}

// admitir pide permiso para derivar. Devuelve la función que devuelve el turno
// y si se consiguió; con false NO SE HA VERIFICADO NADA y no hay nada que
// soltar.
//
// # LOS TRES DESENLACES, Y NINGUNO ES «ESPERAR INDEFINIDAMENTE»
//
//	presupuesto libre        -> entra al momento
//	ocupado y sala con sitio -> espera como mucho l.espera
//	ocupado y sala llena     -> se va, barato, sin tocar la CPU
//
// La sala se libera al SALIR de aquí, con o sin turno: cuenta a quien está
// esperando, no a quien está derivando. Sin eso, el hueco de la sala quedaría
// retenido durante toda la derivación y la sala valdría la mitad.
func (l *limitadorAcceso) admitir(ctx context.Context) (func(), bool) {
	select {
	case l.activo <- struct{}{}:
		return func() { <-l.activo }, true
	default:
	}

	select {
	case l.sala <- struct{}{}:
		defer func() { <-l.sala }()
	default:
		return nil, false
	}

	t := time.NewTimer(l.espera)
	defer t.Stop()
	select {
	case l.activo <- struct{}{}:
		return func() { <-l.activo }, true
	case <-t.C:
		return nil, false
	case <-ctx.Done():
		// El cliente se fue. No hay a quién responder y desde luego no hay que
		// gastarle 3.2 s de CPU al nodo por él.
		return nil, false
	}
}

type intentos struct {
	n     int
	desde time.Time
}

func nuevoLimitador() *limitadorAcceso {
	return &limitadorAcceso{
		activo:   make(chan struct{}, 1),
		sala:     make(chan struct{}, topeEnEsperaKDF),
		espera:   esperaMaximaKDF,
		fallidos: make(map[string]*intentos),
	}
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

// claveUsuario nombra al usuario dentro del contexto de la petición. Es de un
// tipo propio y no una cadena: así ningún otro paquete puede escribir en esa
// misma clave, ni por accidente ni a propósito.
type claveDeContexto struct{}

var claveUsuario claveDeContexto

// usuarioDe devuelve de quién es la petición. Solo tiene valor detrás de
// la puerta opaca (opaca.go), que es la única que lo pone.
func usuarioDe(r *http.Request) string {
	u, _ := r.Context().Value(claveUsuario).(string)
	return u
}

// tocadorDe entrega el cierre que cuenta actividad en ESTA sesión —
// ADR-0059. Lee la cookie una vez y lo devuelve envuelto; sin cookie, un
// cierre que no hace nada, para que llamarlo a ciegas en una transferencia
// nunca sea un error.
//
// Es lo que consumen las descargas y subidas largas (plazos.go): una sola
// petición HTTP puede durar minutos moviendo bytes sin que la puerta opaca
// vuelva a pasar por en medio, así que sin esto una transferencia de 4 GB
// caducaría su propia sesión a mitad de camino.
func (s *Servidor) tocadorDe(r *http.Request) func() {
	c, err := r.Cookie(nombreCookie)
	if err != nil {
		return func() {}
	}
	return func() { s.sesiones.Tocar(c.Value) }
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

// procesarAcceso atiende el POST del formulario — RF-15, ADR-0072 §10.
//
// # EL ORDEN ES LA DEFENSA, Y ESTE ES EL ORDEN
//
//	origen
//	  -> ¿ya limitado por origen?      (un mapa en memoria)
//	  -> Content-Type                  (una cabecera)
//	  -> cuerpo acotado + plazo        (1 KiB, 10 s)
//	  -> parseo                        (dos campos)
//	  -> nombre sintácticamente válido (reglas públicas)
//	  -> ADMISIÓN al presupuesto       (dos canales)
//	  -> PBKDF2                        (3.2 s en el nodo)
//	  -> sesión
//
// Cada escalón es órdenes de magnitud más barato que el siguiente, y ninguno
// de los siete primeros toca la CPU de forma apreciable. La regla que los
// ordena es una sola: NO SE PAGA EL KDF PARA DESCUBRIR QUE LA PETICIÓN NO
// MERECÍA LLEGAR A ÉL.
//
// # HACIA FUERA, UN SOLO DESENLACE
//
// Los ocho fallos posibles salen por accesoFallido, con el mismo estado, el
// mismo cuerpo y el mismo texto. Ninguno lleva Retry-After ni una plantilla
// distinta: eso los volvería a separar y devolvería el oráculo por la puerta
// de atrás.
//
// # HACIA DENTRO, OCHO HECHOS DISTINTOS
//
// Y cada uno con su Motivo. El panel tiene que poder decir si el nodo está
// saturado, si alguien está probando contraseñas o si alguien está mandando
// megabytes al formulario — tres problemas con tres respuestas distintas que
// una sola etiqueta haría indistinguibles.
func (s *Servidor) procesarAcceso(w http.ResponseWriter, r *http.Request) {
	origen := origenDe(r)

	// 1 — ¿ESTE ORIGEN YA ESTÁ LIMITADO? Lo primero, porque es lo más barato y
	// porque es lo único que puede decidirse sin leer un solo byte del cuerpo.
	if ok, espera := s.limitador.permitido(origen); !ok {
		s.anotarAccesoLimitado(origen, espera)
		s.accesoFallido(w, r, seguridad.LimiteDeIntentos)
		return
	}

	// 2 — FORMATO. Una cabecera, sin tocar el cuerpo. ParseMediaType para
	// aceptar los parámetros legítimos —«; charset=UTF-8» lo manda algún
	// cliente— sin aceptar por eso otro tipo distinto.
	tipo, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || tipo != tipoFormularioAcceso {
		s.accesoFallido(w, r, seguridad.FormatoNoAdmitido)
		return
	}

	// 3 — TAMAÑO Y TIEMPO DEL CUERPO, antes de leerlo.
	//
	// MaxBytesReader corta la LECTURA, no comprueba Content-Length: un cuerpo
	// que miente sobre su tamaño, o que no lo declara, se corta igual en el
	// byte 1025. Y el plazo corta al que manda esos 1024 bytes de uno en uno.
	//
	// El plazo se pide con NewResponseController y el error se ignora a
	// propósito, igual que en plazos.go: si el ResponseWriter no admite plazos
	// —un httptest.ResponseRecorder, por ejemplo— se sigue sin él en vez de
	// convertir una limitación del envoltorio en un fallo de acceso.
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(plazoCuerpoAcceso))
	r.Body = http.MaxBytesReader(w, r.Body, topeCuerpoAcceso)

	// 4 — PARSEO. Ya acotado en bytes y en segundos.
	if err := r.ParseForm(); err != nil {
		var grande *http.MaxBytesError
		if errors.As(err, &grande) {
			s.accesoFallido(w, r, seguridad.CuerpoExcesivo)
			return
		}
		s.accesoFallido(w, r, seguridad.PeticionMalformada)
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

	// 5 — CAMPOS MÍNIMAMENTE VÁLIDOS, y aquí SÍ se rechaza barato.
	//
	// ESTO NO FILTRA NADA, y es la distinción que sostiene toda la defensa de
	// enumeración: las reglas del nombre son PÚBLICAS —de 2 a 32, minúsculas,
	// empieza por letra— y cualquiera puede comprobarlas sin preguntarle al
	// servidor. Saber que «A/B» no es un nombre válido no dice si «ana» existe.
	//
	// Lo que sí filtraría —si un nombre VÁLIDO corresponde a alguien— sigue
	// costando exactamente lo mismo exista o no: de eso se encarga
	// verificarAcceso con la credencial de relleno del registro. Esa propiedad
	// no se toca (CWE-208).
	//
	// La contraseña vacía se rechaza por lo mismo: quien manda el formulario
	// sabe si rellenó la casilla, así que no hay nada que ocultarle.
	if nombreAceptable(usuario) == "" || clave == "" {
		s.accesoFallido(w, r, seguridad.PeticionMalformada)
		return
	}

	// 6 — ADMISIÓN. El último control barato, y el que decide si esta petición
	// llega siquiera a la parte cara.
	soltar, admitida := s.limitador.admitir(r.Context())
	if !admitida {
		s.anotarSinCapacidad(origen)
		// NO SE CUENTA COMO CREDENCIAL INCORRECTA, y no es un matiz — ADR-0071.
		// Aquí no se ha verificado NINGUNA contraseña: no se ha derivado nada.
		// Contarlo como fallo de credencial alimentaría SenalFuerzaBruta, y con
		// ella el apartado automático, con un hecho que no ocurrió: el nodo
		// acabaría apartando a gente por estar él ocupado. Por el mismo motivo
		// no se toca accesosFallidos ni limitador.fallo.
		s.accesoFallido(w, r, seguridad.SinCapacidadCripto)
		return
	}

	// 7 — PBKDF2. Todo lo anterior existe para que aquí solo llegue lo que ya
	// pasó seis filtros.
	valida, conocido := s.verificarAcceso(usuario, clave)
	soltar()

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
		// El historial hereda la MISMA regla, y con más motivo: esto se
		// escribe en un archivo de /var/lib que se lee entero al arrancar.
		// Solo se apunta el nombre si la cuenta EXISTE; si no, no se apunta
		// nada —ni siquiera «desconocido»—, porque el campo vacío ya lo dice
		// y así no hay ninguna vía por la que lo tecleado llegue al disco.
		if conocido {
			marcarCuentaIntentada(r, usuario)
		}
		s.accesoFallido(w, r, seguridad.CredencialIncorrecta)
		return
	}

	// 8 — SESIÓN.
	testigo, err := s.sesiones.Abrir(usuario)
	if err != nil {
		// LA CONTRASEÑA ERA CORRECTA Y AUN ASÍ SE SALE POR EL MISMO SITIO.
		//
		// Antes esto respondía 500. Un 500 aquí es un oráculo perfecto: dice
		// «esa contraseña es buena, vuelve luego», que es justo lo que ningún
		// fallo de este extremo puede decir. Hacia fuera va el mismo aviso que
		// todo lo demás; hacia dentro va su motivo propio y una línea de Error
		// en el diario, porque esto es una AVERÍA del nodo y quien lo cuida
		// tiene que enterarse.
		s.reg.Error("no se pudo abrir la sesión", "error", err)
		s.accesoFallido(w, r, seguridad.FalloAlAbrirSesion)
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
		// MaxAge lleva el tope ABSOLUTO (s.duracionSesion), NO el de
		// inactividad de ADR-0059, y no es un descuido: el navegador no sabe
		// nada de actividad, solo de tiempo transcurrido desde que se puso
		// la cookie. Ponerle aquí el plazo corto la borraría a los 5 minutos
		// DEL ACCESO, no de la última actividad, y echaría fuera a quien
		// sigue usando la web. El servidor es el único que sabe cuándo hubo
		// actividad, y por eso el plazo corto vive solo en Sesiones —el
		// cliente conserva la cookie hasta el tope absoluto; qué hace el
		// servidor con un testigo inactivo es cosa de la puerta.
		MaxAge: int(s.duracionSesion.Seconds()),
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

// accesoFallido es la ÚNICA salida de un intento de acceso que no abrió sesión.
//
// # POR QUÉ EL FORMULARIO SE REDIBUJA CON EL NOMBRE DE LA COOKIE Y NO CON EL QUE SE TECLEÓ
//
// Porque el que se tecleó solo se conoce si el formulario llegó a parsearse, y
// eso separa los fallos en dos grupos observables: los que devuelven el nombre
// —el cuerpo se leyó y se entendió— y los que no —cuerpo excesivo, formato
// inválido, límite por origen—. Es un oráculo pequeño y es exactamente la
// clase de diferencia que ADR-0072 §10 viene a quitar.
//
// COSTE DECLARADO: en un aparato nuevo, fallar la contraseña obliga a volver a
// teclear el nombre. En el aparato de uso diario no se nota, porque la cookie
// nas_usuario lo recuerda y es de ella de donde sale.
//
// NI Retry-After NI NINGUNA OTRA PISTA. El límite por origen ya no se anuncia:
// decir «vuelve en 300 s» identifica ese fallo entre los ocho y, de paso, le
// dice a quien prueba contraseñas cuándo merece la pena volver.
func (s *Servidor) accesoFallido(w http.ResponseWriter, r *http.Request, motivo seguridad.Motivo) {
	marcarRechazo(r, motivo)
	s.pedirAccesoComo(w, r, usuarioRecordado(r), avisoAccesoFallido)
}

// anotarAccesoLimitado y anotarSinCapacidad escriben UNA línea por arranque.
//
// # POR QUÉ NO UNA POR PETICIÓN
//
// Las dos las dispara alguien de fuera y ninguna está acotada por el propio
// limitador —a la primera se llega justo cuando el limitador ya dijo que no—,
// así que una línea por intento convertiría el diario en el amplificador de
// quien insiste: petición barata, línea cara y persistente. Es el mismo
// argumento, y el mismo patrón, que anotarCierreFallido (seguridad.go).
//
// # Y POR QUÉ NO SE CALLAN
//
// Porque el HECHO no se pierde: cada rechazo entra igual en el anillo con su
// motivo, su origen y su instante, y el panel los cuenta y los agrupa. Lo que
// se acota es la REPETICIÓN en el diario, no el registro. La línea única
// existe para que quien lea el diario se entere de que esto está pasando.
func (s *Servidor) anotarAccesoLimitado(origen string, espera time.Duration) {
	if s.accesosLimitados.Add(1) == 1 {
		s.reg.Warn("acceso bloqueado por intentos repetidos; no se repetirá esta línea",
			"origen", origen, "espera_s", int(espera.Seconds()))
	}
}

func (s *Servidor) anotarSinCapacidad(origen string) {
	if s.accesosSinCapacidad.Add(1) == 1 {
		s.reg.Warn("acceso rechazado sin verificar: el presupuesto criptográfico estaba ocupado; no se repetirá esta línea",
			"origen", origen)
	}
}

// verificarAcceso comprueba la contraseña de quien dice ser «usuario», y de
// paso informa de si ese nombre corresponde a alguien — lo segundo SOLO para
// el registro, nunca para la respuesta.
//
// Las dos ramas cuestan lo mismo: una derivación de PBKDF2 con las mismas
// iteraciones. La del superusuario contra su credencial; la de un usuario
// contra la suya o, si no existe, contra la de relleno del registro. Sin esa
// simetría, un cronómetro distinguiría los nombres reales de los inventados
// (CWE-208). ESO NO SE TOCA: es la defensa de enumeración entera.
//
// # DOS COSAS SE MUDARON FUERA DE AQUÍ — ADR-0072 §10
//
//  1. EL CERROJO. Era `s.limitador.verificar.Lock()`, y garantizaba una
//     derivación a la vez esperando para siempre. Ahora quien llama tiene que
//     traer ya el turno concedido por limitadorAcceso.admitir, que garantiza
//     lo mismo Y ADEMÁS que la espera esté acotada.
//
//  2. EL FILTRO DEL NOMBRE. Rechazar un nombre sintácticamente imposible es un
//     control BARATO, y estaba detrás del cerrojo, es decir detrás de la cola
//     de lo caro. Ahora vive en procesarAcceso, en su escalón.
//
// PRECONDICIÓN, y por eso está escrita: solo se llama con turno concedido y
// con un nombre que ya pasó nombreAceptable.
func (s *Servidor) verificarAcceso(usuario, clave string) (valida, conocido bool) {
	if usuario == autenticacion.NombreSuperusuario {
		return autenticacion.Verificar(s.credencial, clave), true
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
		// No puede ocurrir detrás de la puerta opaca, y precisamente por eso se
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
			// La cuenta SÍ se apunta aquí, al contrario que en el formulario
			// de acceso: esta sesión ya está autenticada, así que el nombre es
			// uno real del registro y no algo tecleado que pudiera ser una
			// contraseña mal puesta.
			marcarCuentaIntentada(r, usuarioDe(r))
			marcarRechazo(r, seguridad.PermisoInsuficiente)
			http.Error(w, "no autorizado", http.StatusForbidden)
			return
		}
		siguiente(w, r)
	}
}

// avisoSoloDesdeDentro es lo que lee quien se topa con la regla. Dice DÓNDE
// se puede hacer, no solo que aquí no: un 403 a secas ante un botón que la
// propia página acaba de ofrecer se lee como una avería.
const avisoSoloDesdeDentro = "esta operación solo se hace desde la red de casa o por el túnel"

// acotadoPorRed responde, para UNA petición, si se le niegan las operaciones
// que no se deshacen.
//
// Vive aparte de la envoltura porque tiene DOS clientes: soloDesdeDentro, que
// niega en el servidor, y verListado, que decide qué ofrece el menú. Si la
// regla estuviera escrita dos veces, un día la página ofrecería un botón que
// el servidor rechaza — que es justo el defecto que ADR-0054 tardó en
// descubrirse, una interfaz enseñando algo distinto de lo que el servidor
// hacía.
func acotadoPorRed(r *http.Request) bool {
	if usuarioDe(r) != autenticacion.NombreSuperusuario {
		return false
	}
	switch seguridad.ClasificarRed(ipDe(r)) {
	case seguridad.RedLocal, seguridad.RedTunel, seguridad.RedNodo:
		return false
	}
	return true
}

// soloDesdeDentro niega al SUPERUSUARIO, y solo a él, lo que no se deshace
// cuando la petición llega de Internet.
//
// # POR QUÉ EXISTE
//
// RF-18 dice con todas las letras que la confirmación de borrado es «la única
// barrera del sistema», porque no hay papelera (D-15) y el borrado es
// definitivo también por SMB. Desde Internet esa barrera está a UNA
// CONTRASEÑA de distancia, y desde ADR-0047 esa misma contraseña abre también
// SMB. El propósito declarado del 443 (ADR-0042) es entrar desde un equipo
// AJENO, así que la credencial se teclea, por diseño, en máquinas que el
// responsable no controla.
//
// Esto no impide el compromiso: lo acota. Con la regla puesta, una sesión de
// superusuario robada desde fuera lee y sube, pero no destruye ni da de alta
// cuentas. Eso convierte un daño irreversible en uno reversible.
//
// # POR QUÉ SOLO AL SUPERUSUARIO
//
// La barrera escala con la autoridad, no con la persona. La sesión del
// superusuario alcanza el volumen ENTERO y el registro de cuentas; la de un
// usuario normal está enraizada en su propia carpeta (ADR-0055), así que el
// radio de daño de su sesión ya está acotado por construcción. Cobrarle a él
// el mismo peaje le quitaría el uso diario desde fuera sin cerrar nada que no
// estuviera cerrado. Por eso un usuario normal pasa de largo por aquí.
//
// # LISTA POSITIVA, NO «SI VIENE DE INTERNET»
//
// Se enumera lo que PASA —casa, túnel y el propio nodo— en vez de lo que se
// niega. Con la forma negativa, RedDesconocida (una dirección que ni siquiera
// se pudo leer) se colaría, porque DeFuera() solo es cierto para RedInternet;
// y un valor de Red que se añada mañana entraría también, en silencio. Así
// solo puede pasar lo que alguien escribió a mano que puede pasar.
func (s *Servidor) soloDesdeDentro(siguiente http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !acotadoPorRed(r) {
			siguiente(w, r)
			return
		}
		s.reg.Warn("operación acotada a la red de casa, pedida desde fuera",
			"usuario", usuarioDe(r), "ruta", r.URL.Path,
			"metodo", r.Method, "origen", origenDe(r))
		// La cuenta se apunta por el mismo motivo que en soloSuperusuario: la
		// sesión ya está autenticada, así que el nombre es uno real del
		// registro y no algo tecleado que pudiera ser una contraseña.
		marcarCuentaIntentada(r, usuarioDe(r))
		marcarRechazo(r, seguridad.SoloDesdeDentro)
		http.Error(w, avisoSoloDesdeDentro, http.StatusForbidden)
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
