package web

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"time"

	"nasd/internal/seguridad"
)

// Clasificación de los rechazos — etapa 1 del panel de seguridad.
//
// # POR QUÉ EL MOTIVO VIAJA POR EL CONTEXTO Y NO SE ANOTA EN CADA SITIO
//
// Quien SABE por qué se rechaza es el sitio que rechaza —exigirSesion,
// soloSuperusuario, el limitador, el verificador de CSRF—, pero quien conoce
// el resultado FINAL de la petición es conRegistro, que es también el único
// punto por el que pasan todas sin excepción.
//
// Si cada sitio anotara por su cuenta habría dos formas de equivocarse, y las
// dos son silenciosas: contar dos veces la misma petición cuando un manejador
// pasa por dos guardas, y dejar fuera cualquier rechazo futuro cuyo autor no
// se acuerde de anotar. Es exactamente el argumento que servidor.go ya tiene
// escrito para poner ahí los contadores y no en cada manejador — se sigue el
// mismo, no se estrena otro.
//
// Así que el sitio que rechaza solo deja dicho POR QUÉ, y conRegistro decide
// SI se anota (mirando el código de estado real) y lo hace una sola vez.

// marcadorDeRechazo es el hueco que conRegistro deja en el contexto para que
// la guarda que rechace escriba su motivo.
//
// Es un puntero a propósito: el contexto es inmutable y los manejadores
// reciben la petición por valor, así que un valor plano no podría escribirse
// desde dentro de la cadena.
//
// NO lleva mutex: una petición HTTP la atiende una sola goroutine, y lo que
// la cruza en paralelo —las transferencias largas de plazos.go— no toca esto.
type marcadorDeRechazo struct {
	motivo seguridad.Motivo
	// cuenta es el nombre intentado, y SOLO lo escribe quien ya ha
	// comprobado que existe (procesarAcceso). La regla es la misma que
	// 04_SEGURIDAD §6 impone al diario y por el mismo motivo: lo tecleado en
	// la casilla del nombre puede ser una contraseña puesta donde no iba.
	cuenta string
}

type claveDeRechazo struct{}

var claveRechazo claveDeRechazo

// conMarcador prepara el hueco. Lo llama conRegistro, antes de que nadie
// pueda necesitarlo.
func conMarcador(r *http.Request) (*http.Request, *marcadorDeRechazo) {
	m := &marcadorDeRechazo{}
	return r.WithContext(context.WithValue(r.Context(), claveRechazo, m)), m
}

// marcarRechazo deja dicho por qué se está negando esta petición.
//
// Si nadie lo llama, el rechazo se anota igualmente con motivo desconocido:
// eso es deliberado y no un descuido. Un 4xx sin clasificar es un punto ciego
// REAL del servidor, y enseñarlo como «Motivo desconocido» en el panel es lo
// que hace que se note y se le ponga nombre. Callarlo lo escondería.
func marcarRechazo(r *http.Request, motivo seguridad.Motivo) {
	if m, ok := r.Context().Value(claveRechazo).(*marcadorDeRechazo); ok {
		m.motivo = motivo
	}
}

// marcarCuentaIntentada acompaña a marcarRechazo cuando la cuenta EXISTE.
func marcarCuentaIntentada(r *http.Request, cuenta string) {
	if m, ok := r.Context().Value(claveRechazo).(*marcadorDeRechazo); ok {
		m.cuenta = cuenta
	}
}

// ipDe devuelve el origen como dirección, no como texto.
//
// # POR QUÉ NO SE MIRA X-Forwarded-For NI CF-Connecting-IP
//
// Porque NO HAY PROXY. nasd escucha directo en 192.168.1.38:80 (ADR-0018) y
// en [::]:443 (ADR-0046); lo que llega viene de la LAN, del túnel WireGuard o
// de Internet a pelo. No existe ningún intermediario legítimo cuya cabecera
// pudiera creerse.
//
// Implementarlo con una lista de proxies autorizados VACÍA sería código «por
// si acaso» —prohibido por el estilo declarado del proyecto— y además una
// superficie de suplantación que hoy no existe: cualquiera podría enviar
// «X-Forwarded-For: 192.168.1.18» y aparecer en el panel como si fuera de
// casa, envenenando el propio registro que sirve para detectarlo.
//
// El día que se ponga un proxy delante, esto se reabre CON su ADR y con la
// lista de orígenes de confianza escrita. Mientras tanto, la pregunta que ese
// mecanismo respondería —«¿esto viene de fuera?»— la responde
// seguridad.ClasificarRed, que no se puede falsificar porque sale del socket.
func ipDe(r *http.Request) netip.Addr {
	ip, err := netip.ParseAddr(origenDe(r))
	if err != nil {
		return netip.Addr{}
	}
	return ip
}

// anotarConexion registra toda conexión entrante de Internet, hable HTTP o no.
// Es el http.Server.ConnState de los dos servidores (servidor.go).
//
// # POR QUÉ AQUÍ Y NO EN UN net.Listener ENVUELTO
//
// Se consideró envolver el listener de web.EscucharTLS, que es el otro sitio
// por el que pasa toda conexión. Se descartó: solo cubriría el 443, y habría
// que envolver también el del 80 por separado o dejar un hueco asimétrico —
// exactamente el modo de fallo de D-21, donde una vía se verificó y la otra
// no. ConnState lo pone HTTPServer, que construye los dos servidores, así que
// ningún puerto futuro puede quedarse fuera por olvido. Es el mismo argumento
// que ya justifica poner el registro y los contadores en conRegistro.
//
// # POR QUÉ EN StateNew Y NO AL CERRAR
//
// StateNew es el HECHO —«se aceptó una conexión desde X»— y se sabe entero en
// ese instante. Esperar al cierre permitiría además decir si llegó a pedir
// algo, pero eso exige recordar cada conexión viva en un mapa hasta que
// muera: un sexto punto de estado compartido, con su candado, para responder
// una pregunta que la tabla de rechazos ya contesta enseñando a esa misma
// dirección. No se paga.
//
// NO FILTRA POR RED: eso lo hace Conexiones.Anotar, que descarta todo lo que
// no venga de Internet. El filtro vive en un solo sitio a propósito.
func (s *Servidor) anotarConexion(c net.Conn, estado http.ConnState) {
	if estado != http.StateNew {
		return
	}
	// RemoteAddr viene del socket y no de una cabecera, así que no se puede
	// falsificar — la misma propiedad que hace fiable a ipDe, y por eso aquí
	// tampoco se mira X-Forwarded-For.
	dir, err := netip.ParseAddrPort(c.RemoteAddr().String())
	if err != nil {
		// Sin dirección legible no hay nada que anotar. No se registra en el
		// diario: esto corre en el bucle de aceptación, y un origen capaz de
		// provocar el fallo podría convertir el propio registro en su
		// amplificador.
		return
	}
	ahora := time.Now()
	s.conexiones.Anotar(dir.Addr(), ahora)

	// LA PUERTA, y la propiedad que la justifica escrita donde se cumple:
	//
	//   AL APARTADO SE LE ANOTA IGUAL. Colgar no puede llevarse por delante el
	//   registro, así que aquí NO puede haber un «return» antes de Anotar.
	//
	// Bloquear en el cortafuegos habría hecho INVISIBLE al bloqueado —los dos
	// anillos viven dentro de este proceso y un paquete descartado por el
	// núcleo no llega—, y con dos únicos orígenes externos en 19 días eso
	// significaba quedarse ciego justo sobre lo único que este nodo ha visto.
	// Anotando igual se conserva la conexión en el panel, con su cuenta de
	// frenados, y aun así el apartado deja de hablar. Lo comprueba
	// TestAlApartadoSeLeAnotaLaConexionYDespuesSeLeCuelga mirando el anillo de
	// conexiones, no el HTML: en el HTML esta dirección saldría igual por estar
	// en la tabla de apartados, y la prueba pasaría sin demostrar nada.
	//
	// Se comprueba SOLO lo de Internet: la casa y el túnel no se cierran nunca,
	// pase lo que pase. Es la misma barandilla que Evaluar aplica al apartar,
	// repetida aquí a propósito — que la casa no se quede fuera no puede
	// depender de que otra función haya hecho bien su parte.
	if !seguridad.ClasificarRed(dir.Addr()).DeFuera() {
		return
	}

	// DECIDIR, EJECUTAR, CONTAR — en ese orden y en tres líneas distintas.
	//
	// Estaba en una sola: «if cuarentena.Frena(ip) || lista.Bloquea(ip) {
	// c.Close() }». Sumaba el frenado DENTRO de la condición, es decir antes
	// del cierre y sin mirar si el cierre salía bien, así que «Frenados»
	// contaba coincidencias con la política y el panel lo presentaba como
	// conexiones cerradas. El porqué entero está en seguridad/puerta.go; aquí
	// queda la consecuencia, que es lo que se cumple en este archivo:
	//
	//	A la cifra solo se llega POR DEBAJO de un Close() que devolvió nil.
	cierre := seguridad.Decidir(s.cuarentena, s.lista, dir.Addr(), ahora)
	if !cierre.Cierra() {
		return
	}
	if err := c.Close(); err != nil {
		s.anotarCierreFallido(err)
		return
	}
	cierre.Ejecutado(ahora)
}

// anotarCierreFallido registra que a un origen bloqueado NO se le pudo colgar.
//
// # POR QUÉ NO SE ESCRIBE UNA LÍNEA POR INTENTO
//
// Esto corre en el bucle de aceptación y lo dispara alguien de fuera: una
// línea de diario por conexión convertiría el registro en su amplificador. Es
// el mismo argumento que ya impedía anotar cada cierre correcto.
//
// # PERO TAMPOCO SE CALLA
//
// Un Close() que falla es la única forma de que el panel pueda estar diciendo
// menos de lo que pasa: hubo una conexión que debía cerrarse y siguió viva, y
// esa conexión SÍ va a llegar al manejador. Ignorarlo en silencio sería
// exactamente lo que este trabajo vino a quitar.
//
// El reparto: el CONTADOR lleva el volumen —lo publica el panel cuando no es
// cero— y el diario recibe UNA línea por arranque, la del primer fallo. Uno
// basta: si esto ocurre, ocurre por algo sistémico, y lo que hace falta es
// enterarse, no tener mil copias del mismo aviso.
func (s *Servidor) anotarCierreFallido(err error) {
	if s.cierresFallidos.Add(1) == 1 {
		s.reg.Warn("no se pudo cerrar la conexión de un origen bloqueado; no cuenta como frenada",
			"error", err)
	}
}

// anotarHallazgo registra que una ruta que este NAS no publica se atendió con
// contenido. Solo lo llama conRegistro, y solo cuando seguridad.RespuestaInesperada
// dice que sí.
//
// # NO SE CAPTURA NADA NUEVO
//
// Los cinco campos —instante, origen, red, método y ruta— son los mismos que
// el anillo de rechazos ya guardaba, y ni uno más. No entra el User-Agent (ya
// está en la cronología de quien hiciera además algún rechazo), ni la cadena
// de consulta, ni una sola cabecera. 04_SEGURIDAD §6 gobierna esto igual que
// gobierna Evento, y una función nueva no es motivo para relajarlo.
//
// SE ANOTA VENGA DE DONDE VENGA, también desde la LAN o desde el propio nodo,
// y ahí se aparta del criterio de las señales. No es una incoherencia: una
// señal INTERPRETA a quien pide, y desde casa esa interpretación no informa de
// nada. Esto no habla de quien pide — habla del servidor. Que el nodo publique
// /.git/config es igual de cierto y de grave si quien lo descubre es el propio
// responsable con curl, y de hecho ese es el caso en el que más falta hace
// verlo.
func (s *Servidor) anotarHallazgo(r *http.Request, estado int, momento time.Time) {
	ip := ipDe(r)
	s.hallazgos.Anotar(seguridad.Hallazgo{
		Clase:        seguridad.RutaAjenaAtendida,
		Metodo:       r.Method,
		Ruta:         r.URL.Path,
		Estado:       estado,
		Ultima:       momento,
		UltimoOrigen: ip,
		UltimaRed:    seguridad.ClasificarRed(ip),
	})
}

// anotarRechazo registra la petición negada. Solo lo llama conRegistro.
func (s *Servidor) anotarRechazo(r *http.Request, m *marcadorDeRechazo, estado int, momento time.Time) {
	s.seguridad.Anotar(seguridad.Evento{
		Momento: momento,
		Origen:  ipDe(r),
		Metodo:  r.Method,
		Ruta:    r.URL.Path,
		Estado:  estado,
		Motivo:  m.motivo,
		Cuenta:  m.cuenta,
		// La cabecera se lee AQUÍ y no se guarda la petición entera: lo único
		// que sale de ella es el User-Agent, ya acotado. Ninguna otra cabecera
		// se toca, y Authorization y Cookie quedan fuera por 04_SEGURIDAD §6.
		Agente: seguridad.TruncarAgente(r.UserAgent()),
	})
}
