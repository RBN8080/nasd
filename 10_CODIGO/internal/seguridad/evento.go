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
)

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
	}
	return "desconocido"
}

// MotivoDesde recupera un motivo de su forma estable. El booleano distingue
// «no reconocido» de MotivoDesconocido, que es un valor legítimo: sin él, un
// archivo de una versión futura con motivos nuevos se leería como si todos
// fueran desconocidos y nadie se enteraría.
func MotivoDesde(s string) (Motivo, bool) {
	for m := MotivoDesconocido; m <= PeticionMalformada; m++ {
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
