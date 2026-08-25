package canal

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"nasd/internal/aviso"
)

// La cola — lo que impide que un proveedor lento se convierta en un problema
// del NAS.
//
// # QUIÉN ESPERA A QUIÉN
//
// Quien produce un aviso —el ciclo de vigilancia, que corre cada minuto y
// además evalúa la cuarentena— NO puede quedarse esperando a un POST. Si lo
// hiciera, un proveedor que tarda diez segundos retrasaría diez segundos la
// evaluación de conducta del nodo, y el enforcement pasaría a depender del
// tiempo de respuesta de un tercero. Eso es exactamente lo que la arquitectura
// aprobada prohíbe: NOTIFICACIÓN va después de ENFORCEMENT, nunca delante.
//
// Encolar no bloquea NUNCA. Entregar lo hace una goroutine propia que puede
// tardar lo que quiera sin que nadie se entere.

const (
	// topeCola es cuántos avisos caben esperando.
	//
	// SESENTA Y CUATRO, que es enorme para lo que este nodo produce —entre uno
	// y tres avisos al día, con 8 peticiones de Internet en 21 días medidos—.
	// Está dimensionado así a propósito: con este margen, que la cola se llene
	// no es «hay mucho tráfico», es «el canal lleva horas roto», y entonces lo
	// que hay que mirar es el indicador de /estado, no la cola.
	topeCola = 64

	// umbralCanalCaido es cuánto tiene que durar una racha de fallos para que,
	// al recuperarse, se avise de que hubo un hueco.
	//
	// QUINCE MINUTOS, y sale de la aritmética del reintento, no del gusto: con
	// tres intentos y la espera acotada en 300 s, un aviso tarda como mucho
	// unos diez minutos en darse por perdido. Que la racha pase de quince
	// significa que al menos un aviso entero se agotó y el siguiente ciclo
	// también falló — o sea, una caída de verdad y no un 429 suelto.
	//
	// Por debajo de eso no se dice nada: un aviso de «el canal parpadeó» sería
	// justo el ruido que toda esta capa existe para quitar.
	umbralCanalCaido = 15 * time.Minute
)

// Cola entrega avisos en serie, sin hacer esperar a quien los produce.
//
// # ESTADO COMPARTIDO (ADR-0013)
//
// Lleva mutex para sus cifras: las escribe la goroutine emisora y las lee el
// manejador de /estado, que es la goroutine de una petición cualquiera.
type Cola struct {
	canal    Canal
	reg      *slog.Logger
	entradas chan aviso.Aviso

	mu               sync.Mutex
	entregados       int64
	fallidos         int64
	descartados      int64
	ultimoExito      time.Time
	ultimoIntento    time.Time
	ultimoError      string
	secretoRechazado bool
	// caidoDesde es cuándo empezó la racha de fallos en curso; cero si el canal
	// está sano. Es lo que permite decir, al volver, cuánto duró el hueco.
	caidoDesde    time.Time
	fallosEnRacha int
}

func NuevaCola(c Canal, reg *slog.Logger) *Cola {
	return &Cola{
		canal:    c,
		reg:      reg,
		entradas: make(chan aviso.Aviso, topeCola),
	}
}

// Encolar deja un aviso para entregar. NO BLOQUEA NUNCA, y esa es toda su
// especificación.
//
// # QUÉ PASA SI ESTÁ LLENA, Y POR QUÉ SE TIRA LO MÁS VIEJO
//
// Se descarta el aviso MÁS ANTIGUO para hacer sitio al nuevo. En un canal de
// avisos, lo reciente vale más que lo viejo: si el canal lleva horas caído, lo
// último que pasó describe mejor la situación actual que lo primero. Es la
// decisión contraria a la de un registro —donde se conserva el principio— y es
// deliberada, porque esto no es el registro: el registro son los cuatro
// historiales de internal/seguridad, que no pierden nada.
//
// Los tres «select» son no bloqueantes, así que en el peor caso —una carrera
// con la goroutine emisora— se pierde el nuevo en vez del viejo y se cuenta
// igual. Se prefiere esa imprecisión a un candado que pudiera hacer esperar a
// quien produce, que es justo lo que esta función existe para impedir.
func (c *Cola) Encolar(a aviso.Aviso) {
	select {
	case c.entradas <- a:
		return
	default:
	}

	select {
	case <-c.entradas:
	default:
	}
	select {
	case c.entradas <- a:
	default:
		c.contarDescarte()
		return
	}
	c.contarDescarte()
}

func (c *Cola) contarDescarte() {
	c.mu.Lock()
	c.descartados++
	primero := c.descartados == 1
	c.mu.Unlock()
	// UNA línea por arranque, no una por descarte: si esto ocurre, ocurre por
	// algo sistémico y lo que hace falta es enterarse, no tener mil copias del
	// mismo aviso. Es el mismo reparto que anotarCierreFallido ya usa en
	// web/seguridad.go — el contador lleva el volumen y el diario, la noticia.
	if primero {
		c.reg.Warn("la cola de avisos está llena; se descartan los más antiguos",
			"canal", c.canal.Nombre(), "tope", topeCola)
	}
}

// Atender entrega los avisos hasta que se cancele el contexto.
//
// Una sola goroutine, así que los envíos van EN SERIE. No es una limitación que
// haya que quitar algún día: Telegram acota a un mensaje por segundo y por
// chat, de modo que enviar en paralelo solo conseguiría 429. En serie, con la
// espera del reintento, se respeta el ritmo del proveedor sin escribir un
// limitador propio.
func (c *Cola) Atender(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case a := <-c.entradas:
			c.entregar(ctx, a)
		}
	}
}

func (c *Cola) entregar(ctx context.Context, a aviso.Aviso) {
	ahora := time.Now()
	err := c.canal.Enviar(ctx, a)

	c.mu.Lock()
	c.ultimoIntento = ahora
	if err == nil {
		c.entregados++
		c.ultimoExito = ahora
		c.ultimoError = ""
		c.secretoRechazado = false
		caido, fallos := c.caidoDesde, c.fallosEnRacha
		c.caidoDesde, c.fallosEnRacha = time.Time{}, 0
		c.mu.Unlock()

		// EL ÚNICO NARANJA QUE NACE EN EL NODO. Se emite AL VOLVER porque es el
		// único instante en el que se puede entregar: mientras el canal estaba
		// caído no había por dónde decirlo.
		if !caido.IsZero() && ahora.Sub(caido) >= umbralCanalCaido {
			c.reg.Warn("el canal de avisos vuelve tras una caída",
				"canal", c.canal.Nombre(),
				"duracion_min", int(ahora.Sub(caido).Minutes()), "fallos", fallos)
			c.Encolar(aviso.MensajeCanalRecuperado(caido, fallos, ahora))
		}
		return
	}

	// Fallo. Si el contexto se canceló es un apagado ordenado, no una avería:
	// no se cuenta contra el canal ni enciende ninguna racha.
	if ctx.Err() != nil {
		c.mu.Unlock()
		return
	}
	c.fallidos++
	c.ultimoError = err.Error()
	c.fallosEnRacha++
	if c.caidoDesde.IsZero() {
		c.caidoDesde = ahora
	}
	primeroDeLaRacha := c.fallosEnRacha == 1
	if errors.Is(err, ErrSecretoRechazado) {
		c.secretoRechazado = true
	}
	c.mu.Unlock()

	// UNA línea por RACHA y no por intento, por lo mismo que en los descartes:
	// un canal caído tres horas no debe escribir 180 líneas idénticas en un
	// medio de arranque que P9 declara consumible.
	if primeroDeLaRacha {
		c.reg.Warn("no se pudo entregar un aviso",
			"canal", c.canal.Nombre(), "severidad", a.Severidad.String(), "error", err)
	}
}

// Salud publica lo que el canal puede afirmar de sí mismo.
//
// El tipo vive en internal/aviso y no aquí: lo rellena este adaptador y lo lee
// el adaptador web, así que ponerlo en cualquiera de los dos habría acoplado un
// adaptador al otro. Ver aviso/salud.go.
func (c *Cola) Salud() aviso.SaludCanal {
	if c == nil {
		return aviso.SaludCanal{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return aviso.SaludCanal{
		Configurado:      true,
		Nombre:           c.canal.Nombre(),
		Entregados:       c.entregados,
		Fallidos:         c.fallidos,
		Descartados:      c.descartados,
		UltimoExito:      c.ultimoExito,
		UltimoIntento:    c.ultimoIntento,
		UltimoError:      c.ultimoError,
		SecretoRechazado: c.secretoRechazado,
		EnCola:           len(c.entradas),
	}
}
