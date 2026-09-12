package web

import (
	"context"
	"net/http"

	"nasd/internal/autenticacion"
	"nasd/internal/seguridad"
)

// Indistinguibilidad preautenticación — ADR-0072, RF-38.
//
// # LA PROPIEDAD, ESCRITA DE FORMA COMPROBABLE
//
//	Un cliente sin sesión no puede usar las diferencias de encaminamiento HTTP
//	de este NAS para distinguir una ruta protegida de una que no existe, salvo
//	la superficie pública explícitamente necesaria para autenticarse.
//
// No dice «invisible», ni «indetectable», ni «impenetrable». El host contesta,
// el 443 está abierto, el certificado nombra el dominio y /acceso se encuentra
// a la primera. Lo que deja de regalarse es el MAPA INTERIOR.
//
// # LA REGLA QUE GOBIERNA TODO ESTE ARCHIVO
//
//	La respuesta exterior puede mentir deliberadamente; el registro interior no.
//
// De ahí que Estado y Motivo dejen de contestar la misma pregunta:
//
//	Estado  ->  ¿qué vio el cliente?      (404, siempre, para lo no público)
//	Motivo  ->  ¿qué pasó de verdad?      (SinSesion, RutaInexistente,
//	                                       MetodoNoPermitido, SesionInvalida…)
//
// Un 404 opaco sobre /estado NO afirma que /estado no exista. Afirma que a
// quien preguntó no se le dice. El panel sigue viendo la verdad entera.
//
// # POR QUÉ 404 Y NO 401, QUE ES LO QUE HABÍA
//
// Hasta ADR-0072, RF-15 se cumplía al pie de la letra: 401 con el formulario
// dentro. Eso ocultaba el CONTENIDO pero publicaba la TOPOLOGÍA, porque el 401
// solo lo daban las rutas que pasaban por la guarda, y bastaba comparar con lo
// que hacía el mux por su cuenta para ir separando lo que existe de lo que no.
//
// # POR QUÉ 404 Y NO 403
//
// Un 403 dice «existe y no te toca», que es exactamente el dato que se quiere
// negar. Es la respuesta correcta DESPUÉS de autenticarse (soloSuperusuario la
// sigue dando) y la peor posible antes.
//
// # POR QUÉ NO UN 5xx
//
// Un 5xx es una afirmación sobre el NODO, no sobre la petición: contamina el
// SLI-1, invita a reintentar y dice «aquí hay algo vivo fallando». Los 5xx de
// este servidor siguen significando averías de verdad.
//
// # POR QUÉ NO SE DUERME LA RESPUESTA
//
// Igualar tiempos con un sleep convertiría cada sondeo barato en una goroutine
// y una conexión retenidas durante segundos: la opacidad se pagaría con
// disponibilidad, en un nodo de cuatro núcleos. Se acepta que un observador
// cuidadoso pueda notar que un rechazo fue barato. Eso no le dice qué ruta
// existe, ni qué usuario, ni qué contraseña.

// puertaOpaca es la puerta, y es el manejador MÁS EXTERNO del árbol HTTP.
//
// # POR QUÉ VA ANTES DE TODO MUX Y NO DENTRO DE UNO
//
// Porque http.ServeMux contesta cosas ÉL SOLO, antes de llamar a nadie, y
// algunas de esas cosas delatan. Medido contra un net/http real el 2026-08-24
// (Go 1.26) sobre la forma que tenía este servidor:
//
//	GET  //estado          ->  307 Location: /estado    (limpieza de ruta)
//	GET  /foo/../estado    ->  307 Location: /estado    (limpieza de ruta)
//	GET  /estatico         ->  307 Location: /estatico/ (subárbol)
//	POST /estado           ->  405 Allow: GET, HEAD     (método)
//
// Con la guarda colgando de «/» dentro del mux, esas cuatro salían del mux y
// la guarda no llegaba a opinar. La única forma de que la política se cumpla
// siempre es decidir ANTES de que exista encaminamiento real.
//
// # LO QUE SÍ SIGUE HACIENDO EL MUX
//
// Todo, en cuanto hay sesión. La opacidad es exclusivamente preautenticación:
// ver el reparto en Rutas() y §8 de ADR-0072.
func (s *Servidor) puertaOpaca(publico, protegido *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 1 — SUPERFICIE PÚBLICA. Se pregunta al mux público, que es la única
		// definición de qué es público: no hay una segunda lista aquí que
		// pudiera divergir de él (ADR-0072 §4).
		//
		// Un patrón vacío incluye el método equivocado: «PUT /acceso» no es
		// público y cae al 404 opaco como todo lo demás.
		if _, patron := publico.Handler(r); patron != "" {
			publico.ServeHTTP(w, r)
			return
		}

		// 2 — SESIÓN.
		c, err := r.Cookie(nombreCookie)
		if err != nil {
			s.negarOpaco(w, r, protegido, seguridad.SinSesion)
			return
		}
		usuario, vigencia := s.sesiones.Consultar(c.Value)
		// FALLO CERRADO: una sesión vigente pero sin dueño no pasa. No debería
		// existir —Abrir rechaza el nombre vacío— y justo por eso, si alguna
		// vez aparece una, lo que NO puede hacer es acabar mirando la carpeta
		// del superusuario por descarte (ADR-0055).
		if vigencia != autenticacion.Vigente || usuario == "" {
			s.negarOpaco(w, r, protegido, motivoDeVigencia(vigencia))
			return
		}

		// 3 — ROUTING NORMAL. Cuenta como actividad AL TERMINAR la petición,
		// no al empezar (ADR-0059): una petición larga no se da por acabada
		// antes de tiempo.
		defer s.sesiones.Tocar(c.Value)
		// LA MISMA PREGUNTA, AL OTRO LADO DE LA AUTENTICACIÓN. Hace falta por
		// un defecto visto en la primera captura del panel en producción:
		// /favicon.ico —que TODO navegador pide— salía con dos motivos
		// distintos según si había sesión. La misma petición no puede
		// clasificarse distinto según si has entrado. Esto no cambia ni una
		// respuesta: solo lo que se apunta.
		if motivo, existe := resolverRuta(protegido, r); !existe {
			marcarRechazo(r, motivo)
		}
		protegido.ServeHTTP(w, r.WithContext(
			context.WithValue(r.Context(), claveUsuario, usuario)))
	})
}

// motivoDeVigencia traduce lo que el gestor de sesiones SABE, y nada más.
//
// # EL DEFECTO QUE ESTO CIERRA
//
// Antes se apuntaba SesionCaducada en cuanto la petición traía cookie, con el
// argumento de que «traía cookie, luego estuvo dentro». Es una inferencia
// sobre el pasado a partir de una cadena que escribe el CLIENTE: cualquiera
// puede mandar «nas_sesion=loquesea» y aparecer en el panel como una sesión
// que expiró. El panel afirmaba una historia que nunca ocurrió.
//
// Ahora SesionCaducada se reserva para cuando autenticacion.Sesiones tiene la
// entrada y la ve vencida —eso sí lo emitió este proceso—, y una cadena que no
// consta es SesionInvalida, que es todo lo que se puede demostrar.
// La sesión VIGENTE PERO SIN DUEÑO del fallo cerrado también acaba aquí, y
// también como SesionInvalida: no es cosa de quien pide, es una sesión rota,
// y «inválida» es exactamente lo que es.
func motivoDeVigencia(v autenticacion.Vigencia) seguridad.Motivo {
	if v == autenticacion.Caducada {
		return seguridad.SesionCaducada
	}
	return seguridad.SesionInvalida
}

// negarOpaco es el ÚNICO camino por el que sale una negativa preautenticación.
//
// Clasifica primero —barato, sin ejecutar nada— y responde después. El orden
// importa: la clasificación es lo que conserva la verdad interior, y hacerla
// después de responder la dejaría a merced de un «return» olvidado.
func (s *Servidor) negarOpaco(w http.ResponseWriter, r *http.Request, protegido *http.ServeMux, siExistiera seguridad.Motivo) {
	motivo, existe := resolverRuta(protegido, r)
	if existe {
		motivo = siExistiera
	}
	marcarRechazo(r, motivo)
	s.responderOpaco(w)
}

// resolverRuta pregunta al ROUTER qué es esta petición, sin ejecutar nada suyo.
//
// Devuelve el motivo que corresponde cuando la ruta NO se puede atender, y un
// booleano que dice si sí se podría.
//
// # UNA SOLA FUENTE DE VERDAD
//
// No hay aquí ninguna tabla de rutas ni de métodos. Todo sale del propio
// *http.ServeMux que atiende de verdad, así que no existe un segundo catálogo
// que pueda divergir del primero al añadir una ruta mañana (ADR-0072 §4).
//
// # POR QUÉ HACEN FALTA DOS PREGUNTAS Y NO UNA
//
// Handler() distingue «hay patrón» de «no hay patrón», y eso no basta. Medido
// el 2026-08-24 contra un net/http real (Go 1.26):
//
//	GET  /estado        ->  patrón "GET /estado"   (existe)
//	POST /estado        ->  patrón ""              (¡existe, método distinto!)
//	GET  /.git/config   ->  patrón ""              (no existe)
//
// Las dos últimas son indistinguibles por el patrón, y son hechos distintos:
// una es un método equivocado sobre una ruta real y la otra es un sondeo. Sin
// separarlas, todos los métodos raros sobre rutas reales engordaban el
// recuento de «rutas inexistentes distintas» que alimenta SenalExploracion.
//
// La segunda pregunta se la hace al mux ejecutándolo contra un sumidero y
// mirando qué estado habría dado ÉL.
//
// # POR QUÉ ESTO NO EJECUTA NINGÚN MANEJADOR
//
// Porque solo se llega ahí cuando Handler() devolvió patrón VACÍO, y en
// net/http un patrón vacío significa que no encajó ninguno de los registrados:
// el manejador devuelto es entonces uno del propio mux —el de 404 o el de
// 405—, nunca uno de esta aplicación. La propiedad no se supone, se comprueba:
// TestElSondeoDelRouterNoEjecutaNingunManejador registra manejadores que
// entran en pánico y recorre todos los casos.
//
// # LO QUE CUESTA
//
// Una búsqueda en el árbol del mux y un mapa de cabeceras pequeño, y solo en
// el camino en el que ya se va a construir un Evento. Lo que este NAS sirve de
// verdad sale por la primera pregunta sin pagar la segunda.
func resolverRuta(mux *http.ServeMux, r *http.Request) (seguridad.Motivo, bool) {
	if _, patron := mux.Handler(r); patron != "" {
		return seguridad.MotivoDesconocido, true
	}
	if estadoQueDaria(mux, r) == http.StatusMethodNotAllowed {
		return seguridad.MetodoNoPermitido, false
	}
	// RutaInexistente cubre aquí lo que el mux contestaría con 404 y también
	// sus redirecciones de limpieza sobre rutas que no llevan a nada
	// («//no-existe»). No se le da motivo propio a la redirección: para quien
	// mira son la misma cosa —«pidió algo que no hay»— y un motivo más solo
	// añadiría una fila al desplegable del panel sin cambiar ninguna decisión.
	return seguridad.RutaInexistente, false
}

// estadoQueDaria ejecuta el mux contra un sumidero para leer el estado que él
// mismo habría producido. Solo se llama cuando Handler() dio patrón vacío: ver
// el porqué entero en resolverRuta.
//
// La petición se copia en superficie antes de pasarla: ServeMux.ServeHTTP
// escribe en r.Pattern y en los comodines de la petición que recibe, y este
// sondeo no debe dejar rastro en la que se va a atender de verdad.
func estadoQueDaria(mux *http.ServeMux, r *http.Request) int {
	sondeo := *r
	var sum sumidero
	mux.ServeHTTP(&sum, &sondeo)
	return sum.estado
}

// sumidero es un http.ResponseWriter que no escribe en ningún sitio y solo se
// queda con el estado. Nada de lo que reciba llega al cliente.
type sumidero struct {
	cabeceras http.Header
	estado    int
}

func (s *sumidero) Header() http.Header {
	if s.cabeceras == nil {
		// Dos entradas: el mux escribe Allow y Content-Type en su 405, que es
		// el máximo que este sumidero puede llegar a recibir.
		s.cabeceras = make(http.Header, 2)
	}
	return s.cabeceras
}

func (s *sumidero) WriteHeader(e int) {
	if s.estado == 0 {
		s.estado = e
	}
}

func (s *sumidero) Write(p []byte) (int, error) {
	if s.estado == 0 {
		s.estado = http.StatusOK
	}
	return len(p), nil
}

// responderOpaco es LA respuesta. Una sola, para toda petición no pública sin
// sesión, sea cual sea la ruta y sea cual sea el motivo interior.
//
// # NO RECIBE LA PETICIÓN, Y ESO ES LA PROPIEDAD
//
// La firma dejó de tomar *http.Request en ADR-0088. Lo tomaba para leer la
// cookie del nombre recordado, y mientras esa puerta siguiera abierta «el
// cuerpo no depende de la petición» era una promesa que había que volver a
// revisar cada vez que alguien tocara esta función. Ahora no hay nada que
// revisar: lo que no se recibe no se puede filtrar.
//
// # QUÉ NO PUEDE DEPENDER DE LA RUTA
//
// Ni el estado, ni el cuerpo, ni su longitud, ni el tipo, ni una redirección,
// ni Location, ni Allow, ni WWW-Authenticate, ni Set-Cookie, ni un mensaje de
// error, ni el nombre del patrón. Nada de eso se escribe aquí, y por eso no
// puede filtrarse.
//
// Las cabeceras globales de seguridad SÍ salen —conCabecerasSeguridad envuelve
// a esta puerta— y eso no rompe nada: son idénticas para todas las respuestas,
// justamente por ser globales. Que salgan es un requisito, no una concesión:
// una vía de error que se escapara sin CSP sería un agujero abierto por la
// puerta de al lado (D-21).
//
// # POR QUÉ EL CUERPO VA VACÍO — ADR-0088, QUE SUPERSEDE A ADR-0072 §2
//
// Hasta ADR-0088 el cuerpo era el formulario de acceso, y el argumento era
// bueno: tiene que ser el MISMO para todas las rutas, y de todos los cuerpos
// posibles ese era el único que además servía para algo —quien abría la
// dirección del NAS en el teléfono se encontraba la casilla de la contraseña
// en vez de un 404 seco—. El coste estaba declarado y se aceptó con los ojos
// abiertos.
//
// Lo que lo retira no es una opinión nueva, es una MEDICIÓN: en el panel de
// seguridad los sondeos automáticos piden «/» y no piden «/acceso». Contra ese
// tráfico —que es casi todo el que llega— el formulario dentro del 404 estaba
// sirviendo la interfaz a quien nunca habría sabido pedirla.
//
// Lo que se gana es poco, y es real:
//
//	el bot que solo pide «/» deja de recibir la interfaz
//	se corta el rastro a /estatico/* desde toda URL anónima
//	desaparece un eje de variación: el cuerpo ya no se renderiza con la
//	cookie nas_usuario, que la escribe el cliente
//
// LO QUE ESTO NO ES. No es esconder el NAS. /acceso sigue siendo pública a
// propósito (ADR-0072 §9) y entrega el formulario con un 200 a quien lo pida:
// quien busque este nodo A PROPÓSITO lo encuentra igual de rápido. Lo único
// que cambia es que deja de servírsele a quien no lo buscaba.
//
// # SIN NEGOCIACIÓN POR Accept
//
// A propósito: aquí no se mira Accept. Una sola representación, para nadie hay
// dos. Con el cuerpo vacío la cuestión casi se disuelve —no hay nada que
// representar— pero el tipo declarado sigue siendo uno y fijo.
func (s *Servidor) responderOpaco(w http.ResponseWriter) {
	h := w.Header()
	// text/plain y no text/html: ya no hay HTML que anunciar. El tipo es una
	// constante más de esta respuesta, no la consecuencia de lo que se escriba.
	h.Set("Content-Type", "text/plain; charset=utf-8")
	// Declarado, y no deducido del troceado, por el mismo motivo que cuando el
	// cuerpo medía 1050 bytes: la longitud es una de las cosas que no pueden
	// variar con la ruta, así que se dice en vez de dejar que salga sola.
	h.Set("Content-Length", "0")
	// no-store se queda aunque el formulario ya no viaje aquí: esta sigue
	// siendo la respuesta que recibe un equipo prestado (ADR-0042), y una
	// negativa cacheada contestaría por el servidor a una sesión posterior.
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNotFound)
	// Y NO SE LLAMA A Write. Con Content-Length: 0 declarado la respuesta está
	// completa aquí: no hay cuerpo, ni troceado, ni un error de escritura que
	// pudiera llegar a distinguir una ruta de otra.
}
