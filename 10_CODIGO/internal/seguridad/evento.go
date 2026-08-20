// Package seguridad registra los rechazos del servidor web con el detalle
// suficiente para distinguir la actividad normal de un intento externo.
//
// # QUÉ PROBLEMA RESUELVE, MEDIDO Y NO SUPUESTO
//
// Antes de esto, /estado publicaba una sola cifra —«N rechazadas»— que era
// literalmente «toda respuesta 4xx» (web/contadores.go, campo cliente). En
// ese único número caían, sin distinguirse:
//
//   - el 401 de la PRIMERA visita del día del propio responsable, que aún no
//     tiene cookie de sesión;
//   - el 403 de un usuario normal pulsando algo que es del superusuario;
//   - el 403 de la raíz reservada de una cuenta (ADR-0058);
//   - y cualquier sondeo de Internet.
//
// Peor: como la ruta comodín «/» va envuelta en exigirSesion, una petición a
// /wp-login.php SIN sesión nunca llega al mux interno y NO produce 404 sino
// el MISMO 401 que la visita legítima. «Ruta inexistente» y «sin sesión» eran
// el mismo suceso indistinguible, y son la mayoría del contador.
//
// Y el contador se pone a cero en cada reinicio del servicio, que con P-11
// vivo ocurre solo por las tardes: no cubría ni un día.
//
// # QUÉ SE GUARDA Y QUÉ NO
//
// Se guarda el HECHO —qué pasó, quién lo pidió, qué se respondió y qué regla
// lo decidió—, nunca una interpretación. «Fuerza bruta» o «escaneo» son
// INFERENCIAS sobre un conjunto de hechos, no propiedades de un suceso
// suelto, y por eso no existen como Motivo: se derivan al mirar y se
// presentan como lo que son. Fue una condición explícita del responsable —
// «no clasifiques automáticamente todo rechazo como ataque»— y el modelo la
// hace cumplible en lugar de confiarla a la disciplina de quien lea.
//
// 04_SEGURIDAD.md §6 gobierna lo que NUNCA entra aquí: contraseñas, cookies,
// cabeceras Authorization, cuerpos de petición. La regla de sesion.go sobre
// el nombre tecleado se hereda tal cual y por el mismo motivo: se anota SOLO
// si la cuenta existe, porque con dos casillas alguien acaba tecleando su
// contraseña en la del nombre y anotar lo que llegue metería ese secreto
// aquí.
package seguridad

import (
	"net/netip"
	"slices"
	"time"
)

// topeAgente acota el User-Agent que se guarda.
//
// NO es cosmético: es lo que hace REAL el tope de memoria del anillo. Sin
// límite, un cliente puede enviar una cabecera de 64 KB —MaxHeaderBytes de
// servidor.go la admite— y 2000 de esas serían 128 MB en un nodo de 592 MB
// (RES-01). Con 160 bytes el peor caso del anillo entero queda por debajo de
// 1 MB, que es un coste que sí se puede afirmar.
//
// 160 y no menos porque lo que identifica a un cliente automatizado suele ir
// al principio de la cadena («zgrab/0.x», «python-requests/2.x»), pero un
// navegador real necesita más para no quedar irreconocible.
const topeAgente = 160

// Red dice desde dónde entró la petición.
//
// Sustituye a la geolocalización en la pregunta que de verdad importa a
// diario —«¿esto viene de fuera?»— y se calcula con la biblioteca estándar,
// sin base de datos ni consultas a terceros. País y ASN son otra cosa y
// llegan aparte (etapa 4).
type Red uint8

const (
	RedDesconocida Red = iota
	// RedNodo es el propio nodo hablando consigo mismo (127.0.0.1, ::1).
	//
	// SE SEPARA DE RedLocal Y NO ES UN CAPRICHO: son los verificadores, las
	// comprobaciones de despliegue y cualquier curl ejecutado por SSH. Es
	// decir, casi siempre el responsable o un agente probando algo. Mezclarlo
	// con «Red local» hizo que en la primera captura del panel el nodo
	// apareciera junto al PC de casa como si fueran vecinos, y que ::1 y
	// 192.168.1.38 —la MISMA máquina— salieran como dos orígenes distintos
	// sin nada que lo explicara.
	RedNodo
	// RedLocal es la LAN doméstica: 192.168.1.0/24 (ADR-0018).
	RedLocal
	// RedTunel es WireGuard: 10.77.0.0/24 (06_ACCESO_REMOTO §3). Un rechazo
	// desde aquí es un dispositivo del responsable, no un extraño: el túnel
	// es mudo sin clave, así que llegar hasta aquí ya exigió la clave.
	RedTunel
	// RedInternet es todo lo demás. Es la única que puede ser un tercero, y
	// por eso es la que el panel destaca.
	RedInternet
)

func (r Red) String() string {
	switch r {
	case RedNodo:
		return "nodo"
	case RedLocal:
		return "lan"
	case RedTunel:
		return "tunel"
	case RedInternet:
		return "internet"
	}
	return "desconocida"
}

// Etiqueta es el texto que se enseña. Vive aquí y no en la plantilla por lo
// mismo que textoSesionActiva en web/panel.go: hay más de un sitio que lo
// pinta y no pueden divergir.
func (r Red) Etiqueta() string {
	switch r {
	case RedNodo:
		return "El propio nodo"
	case RedLocal:
		return "Red local"
	case RedTunel:
		return "Túnel WireGuard"
	case RedInternet:
		return "Internet"
	}
	return "Origen desconocido"
}

var (
	// Los dos prefijos que el nodo conoce. Literales y no configurables: son
	// los mismos que ya están fijados en ADR-0018 y en 12_wireguard.sh, y una
	// opción de configuración que nadie va a cambiar es exactamente el
	// «por si acaso» que el estilo del proyecto prohíbe.
	prefijoLAN   = netip.MustParsePrefix("192.168.1.0/24")
	prefijoTunel = netip.MustParsePrefix("10.77.0.0/24")

	// prefijosPropios son los /64 de las direcciones IPv6 globales del propio
	// nodo. Se APRENDEN al arrancar (AprenderRedesPropias) en vez de
	// escribirse aquí, y esa decisión tiene una historia de una hora:
	//
	// La primera versión de este paquete solo conocía las dos redes IPv4 de
	// arriba, y con eso bastaba en las pruebas. En cuanto se desplegó, el
	// historial real enseñó al iPhone del responsable —en su propia Wi-Fi,
	// hablando IPv6 nativo desde 3fff:2a0:101e:3d82:…— clasificado como
	// INTERNET. Es decir: la casa contada como si fuera un extraño, y en la
	// única cifra que el responsable declaró importante.
	//
	// NO se escribe el prefijo literal porque lo delega el proveedor y puede
	// rotar: quedaría una constante que un día deja de ser verdad EN SILENCIO
	// y vuelve a contar la casa como Internet, que es el modo de fallo que
	// este proyecto persigue. Preguntándoselo al sistema, el nodo se corrige
	// solo el día que el operador cambie la delegación.
	//
	// ESTADO COMPARTIDO (ADR-0013), el quinto del programa y el más simple:
	// se escribe UNA vez desde la raíz de composición antes de que exista
	// ningún servidor, y a partir de ahí es de solo lectura. No lleva candado
	// porque no hay un segundo escritor; si algún día lo hubiera, esto pasa a
	// necesitar uno.
	prefijosPropios []netip.Prefix
)

// AprenderRedesPropias registra como «de casa» los /64 de las direcciones
// IPv6 globales del nodo. La llama la raíz de composición al arrancar.
//
// Devuelve lo aprendido para que quien la llame pueda registrarlo: si un día
// esto queda vacío por un cambio de red, la clasificación se degrada sin
// fallar —todo lo IPv6 de casa volvería a contarse como Internet— y hay que
// poder verlo en el diario en lugar de descubrirlo en el panel.
func AprenderRedesPropias(direcciones []netip.Addr) []netip.Prefix {
	var out []netip.Prefix
	for _, ip := range direcciones {
		// Solo IPv6 global: las IPv4 ya las cubren los prefijos de arriba, y
		// las locales de enlace (fe80::/10) no las usa nadie para hablar con
		// el NAS.
		if !ip.IsValid() || !ip.Is6() || ip.Is4In6() || !ip.IsGlobalUnicast() {
			continue
		}
		// /64 es el tamaño de una red doméstica delegada por SLAAC (RFC 4291
		// §2.5.1): el nodo y los demás aparatos de la casa comparten esos 64
		// bits y difieren en los otros 64.
		p, err := ip.Prefix(64)
		if err != nil {
			continue
		}
		if !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	prefijosPropios = out
	return out
}

// ClasificarRed decide de qué red viene una dirección.
//
// El orden importa y no es alfabético: se comprueban los dos prefijos
// conocidos y TODO lo demás cae en Internet. Es fallo cerrado — una
// dirección que no reconocemos se trata como externa, nunca como de casa.
func ClasificarRed(ip netip.Addr) Red {
	if !ip.IsValid() {
		return RedDesconocida
	}
	// Una IPv4 que llega por un socket IPv6 viene como ::ffff:a.b.c.d y NO
	// casa con el prefijo IPv4 tal cual. nasd escucha en [::]:443 (tls.go),
	// así que este caso es el NORMAL para todo lo que entra por TLS, no una
	// rareza: sin desenvolverla, todo el tráfico de Internet por HTTPS se
	// clasificaría mal.
	ip = ip.Unmap()
	switch {
	case prefijoLAN.Contains(ip):
		return RedLocal
	case prefijoTunel.Contains(ip):
		return RedTunel
	case ip.IsLoopback():
		return RedNodo
	case ip.IsLinkLocalUnicast():
		// fe80::/10 es LOCAL DEL ENLACE por definición de RFC 4291 §2.5.6: un
		// router no la reenvía nunca, así que un paquete con ese origen no
		// puede venir de Internet. Es un HECHO del protocolo, no una
		// suposición sobre esta red.
		//
		// SE AÑADIÓ EL 2026-08-16 AL VERIFICAR ADR-0064 EN EL NODO, y no
		// estaba de más: netip.Prefix.Contains devuelve FALSO para cualquier
		// dirección con zona («fe80::…%2», que es como llegan las de enlace
		// desde el socket), así que ninguno de los casos de arriba la
		// reconocía y caía en Internet por el fallo cerrado del final.
		//
		// Antes de ADR-0064 apenas se notaba —solo clasificaba PETICIONES, y
		// nadie navega contra una fe80—, pero ahora se clasifica CADA conexión
		// TCP, y una sola de enlace habría contado como un extraño en la única
		// cifra que el responsable declaró importante. Es el mismo defecto que
		// ya tuvo el IPv6 de casa en la primera versión del panel, por otro
		// camino.
		return RedLocal
	}
	// Lo aprendido al arrancar: el IPv6 de casa. Va DESPUÉS de los literales
	// porque estos son ciertos siempre y aquello depende de lo que el
	// proveedor delegue hoy.
	for _, p := range prefijosPropios {
		if p.Contains(ip) {
			return RedLocal
		}
	}
	return RedInternet
}

// DeFuera responde de una vez la ÚNICA pregunta que el responsable declaró
// importante: «¿esto viene de Internet?».
//
// Existe como método y no como comparación suelta porque hay ya cuatro sitios
// que la hacen —el resumen, las señales, el filtro y la plantilla— y una
// comparación repetida cuatro veces es una que alguien acaba escribiendo al
// revés. Aquí solo puede estar bien o mal una vez.
func (r Red) DeFuera() bool { return r == RedInternet }

// Motivo es la regla que produjo el rechazo. Es un HECHO del servidor: cada
// valor corresponde a un punto concreto del código que decidió negar.
type Motivo uint8

const (
	MotivoDesconocido Motivo = iota
	// SinSesion — no había cookie de sesión y la ruta existe. Es el caso
	// NORMAL de la primera visita en un aparato, y por eso el panel lo
	// presenta como rutina y no como amenaza.
	SinSesion
	// SesionCaducada — SÍ había cookie, pero ya no vale. Se distingue de la
	// anterior porque significan cosas opuestas: esto es alguien que estuvo
	// dentro y se le acabó el plazo (ADR-0059), no un desconocido.
	SesionCaducada
	// RutaInexistente — se pidió algo que no está: ni como ruta de la tabla
	// del servidor ni como archivo del volumen.
	//
	// Es la clasificación que antes era IMPOSIBLE de separar de SinSesion: el
	// comodín «/» de servidor.go corta en exigirSesion antes de que el mux
	// interno pueda devolver un 404, así que un sondeo a /wp-login.php y la
	// primera visita del día salían exactamente iguales.
	//
	// Es UN solo motivo y no dos —«ruta del servidor» y «archivo»— a
	// propósito: para quien mira, los dos son «pidió algo que no hay». Lo que
	// separa un sondeo de un enlace viejo no es cuál de los dos fue, sino la
	// red de origen, si había sesión y cuántas rutas distintas probó el mismo
	// origen. Eso es agregación, no una etiqueta del suceso suelto.
	RutaInexistente
	// CredencialIncorrecta — contraseña mal en el formulario de acceso.
	CredencialIncorrecta
	// PermisoInsuficiente — sesión válida, pero la ruta es del superusuario.
	PermisoInsuficiente
	// LimiteDeIntentos — el limitador de sesion.go cortó por repetición.
	LimiteDeIntentos
	// TestigoCSRF — falta el testigo o no cuadra.
	TestigoCSRF
	// RecursoReservado — la raíz de una cuenta o el contenedor homeUsers,
	// que el servidor protege a propósito (ADR-0058).
	RecursoReservado
	// PeticionMalformada — el 400 de un cuerpo o unos parámetros ilegibles.
	PeticionMalformada
	// SoloDesdeDentro — la operación existe, la sesión es válida y aun así se
	// niega porque llega desde Internet: destruir o administrar cuentas exige
	// la LAN o el túnel cuando quien pide es el superusuario.
	//
	// SE AÑADE AL FINAL Y NO EN SU SITIO «LÓGICO», igual que los anteriores:
	// el valor numérico no se persiste —String() es lo que baja al archivo—
	// pero MotivoDesde recorre el rango, así que insertar en medio movería
	// el tope y es un descuido que no hace falta arriesgar.
	SoloDesdeDentro
)

// clave es el texto estable con el que un motivo se persiste y viaja por la
// URL de los filtros. Separado de Etiqueta a propósito: la etiqueta se puede
// reescribir por gusto, y si el archivo guardara la etiqueta, cambiar una
// palabra dejaría ilegible el historial ya escrito.
func (m Motivo) String() string {
	switch m {
	case SinSesion:
		return "sin_sesion"
	case SesionCaducada:
		return "sesion_caducada"
	case RutaInexistente:
		return "ruta_inexistente"
	case CredencialIncorrecta:
		return "credencial_incorrecta"
	case PermisoInsuficiente:
		return "permiso_insuficiente"
	case LimiteDeIntentos:
		return "limite_de_intentos"
	case TestigoCSRF:
		return "testigo_csrf"
	case RecursoReservado:
		return "recurso_reservado"
	case PeticionMalformada:
		return "peticion_malformada"
	case SoloDesdeDentro:
		return "solo_desde_dentro"
	}
	return "desconocido"
}

// MotivoDesde recupera un motivo de su forma estable. El booleano distingue
// «no reconocido» de MotivoDesconocido, que es un valor legítimo: sin él, un
// archivo de una versión futura con motivos nuevos se leería como si todos
// fueran desconocidos y nadie se enteraría.
func MotivoDesde(s string) (Motivo, bool) {
	for m := MotivoDesconocido; m <= SoloDesdeDentro; m++ {
		if m.String() == s {
			return m, true
		}
	}
	return MotivoDesconocido, false
}

// Etiqueta describe el motivo en la lengua del panel.
func (m Motivo) Etiqueta() string {
	switch m {
	case SinSesion:
		return "Sin sesión"
	case SesionCaducada:
		return "Sesión caducada"
	case RutaInexistente:
		return "Ruta inexistente"
	case CredencialIncorrecta:
		return "Credencial incorrecta"
	case PermisoInsuficiente:
		return "Permiso insuficiente"
	case LimiteDeIntentos:
		return "Límite de intentos"
	case TestigoCSRF:
		return "Testigo CSRF"
	case RecursoReservado:
		return "Recurso reservado"
	case PeticionMalformada:
		return "Petición malformada"
	case SoloDesdeDentro:
		return "Solo desde dentro"
	}
	return "Motivo desconocido"
}

// Gravedad ordena los motivos por lo que exigen de quien mira, NO por lo
// alarmante que suenen.
type Gravedad uint8

const (
	// Rutina — sucede todos los días y no significa nada por sí solo.
	Rutina Gravedad = iota
	// Aviso — merece una mirada si se repite.
	Aviso
	// Atencion — no debería pasar en un nodo sano.
	Atencion
)

func (g Gravedad) String() string {
	switch g {
	case Aviso:
		return "aviso"
	case Atencion:
		return "atencion"
	}
	return "rutina"
}

func (g Gravedad) Etiqueta() string {
	switch g {
	case Aviso:
		return "Aviso"
	case Atencion:
		return "Atención"
	}
	return "Rutina"
}

// Gravedad de cada motivo, con el porqué de los que no son evidentes.
func (m Motivo) Gravedad() Gravedad {
	switch m {
	case SinSesion, SesionCaducada:
		// Las dos son el día a día: abrir el NAS sin cookie, o volver a él
		// tras un rato. Marcarlas como sospechosas ahogaría el panel en su
		// propio ruido, que es el defecto que este trabajo viene a corregir.
		return Rutina
	case CredencialIncorrecta, RutaInexistente, PeticionMalformada:
		// Una contraseña mal la falla cualquiera; una ruta inexistente puede
		// ser un enlace viejo. Ninguna dice nada suelta — dicen mucho
		// repetidas, y de eso se encarga la agregación, no la etiqueta.
		return Aviso
	case SoloDesdeDentro:
		// AVISO, y no es una calibración perezosa entre Rutina y Atención.
		//
		// Rutina la escondería, y esto no sucede todos los días. Atención
		// gritaría cada vez que el responsable pulsa «borrar» desde el móvil
		// con datos, que es la forma NORMAL de encontrarse esta regla y no
		// tiene nada de sospechosa.
		//
		// Lo que la hace merecer una mirada es lo otro que puede significar:
		// llegar aquí exige una sesión de superusuario VÁLIDA desde Internet.
		// Si esa sesión no era suya, este rechazo es el más importante que el
		// nodo puede registrar. El panel no puede distinguir los dos casos, y
		// «merece una mirada si se repite» es exactamente lo que Aviso dice.
		return Aviso
	case PermisoInsuficiente, LimiteDeIntentos, TestigoCSRF, RecursoReservado:
		// Estas cuatro exigen una sesión ya iniciada o un patrón deliberado:
		// llegar hasta ellas por accidente navegando es difícil.
		return Atencion
	}
	return Aviso
}

// Evento es un rechazo, tal como ocurrió.
//
// Los campos son los que el servidor CONOCE con certeza en el momento de
// negar. No hay ninguno que haya que adivinar: lo que no se sabe se queda
// vacío y el panel lo dice, en vez de rellenarlo con un valor por omisión
// que se leería como un dato (mismo criterio que los punteros de web/estado.go
// y que Disponibilidad() devolviendo -1 sin muestras).
type Evento struct {
	Momento time.Time
	Origen  netip.Addr
	Red     Red
	Metodo  string
	Ruta    string
	Estado  int
	Motivo  Motivo
	// Agente es el User-Agent, truncado a topeAgente. Vacío si no vino: un
	// cliente automatizado a menudo no envía ninguno, y esa AUSENCIA es en sí
	// misma un dato que el panel debe poder enseñar.
	Agente string
	// Cuenta es el nombre intentado, y SOLO si esa cuenta existe — la misma
	// regla que web/sesion.go aplica al diario, por el mismo motivo: lo que se
	// teclea en la casilla del nombre puede ser una contraseña puesta donde
	// no iba, y este archivo se guarda en disco.
	Cuenta string
}

// TruncarAgente recorta el User-Agent al tope. Exportada porque quien la
// necesita es el adaptador web, que es quien lee la cabecera.
func TruncarAgente(s string) string {
	if len(s) <= topeAgente {
		return s
	}
	// Corte por bytes y no por runas: la cabecera es ASCII por especificación
	// (RFC 9110 §10.1.5) y un corte a media runa solo puede venir de una
	// cabecera que ya venía mal formada. Se retrocede hasta el último byte
	// inicial para no dejar una secuencia rota en un archivo que luego se
	// lee como JSON.
	corte := topeAgente
	for corte > 0 && s[corte]&0xC0 == 0x80 {
		corte--
	}
	return s[:corte]
}
