package web

import (
	"context"
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
