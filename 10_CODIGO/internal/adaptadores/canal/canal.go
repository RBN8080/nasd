// Package canal entrega avisos fuera del nodo — la PRIMERA salida de red que
// ha tenido nunca este programa.
//
// # LA PROPIEDAD QUE ESTE PAQUETE NO PUEDE ROMPER
//
// Nada de aquí se llama desde el camino de una petición. Ni desde conRegistro,
// ni desde el ConnState, ni desde un manejador. Se llama desde el ciclo de
// mantenimiento, que es una goroutine propia con su temporizador.
//
// El motivo está medido: el ConnState corre en el BUCLE DE ACEPTACIÓN, antes de
// que la conexión tenga hilo propio, y quien lo dispara es alguien de fuera. Un
// POST a un tercero ahí dentro convertiría un barrido de veinte conexiones en
// veinte llamadas externas encoladas delante de todo el que estuviera entrando
// — es decir, le regalaría al escáner una forma de dejar el NAS sin puerta. Es
// el mismo argumento que ya impide que el anillo de rechazos haga fsync por
// evento, aplicado a algo mucho más caro que un fsync.
//
// # SI ESTO FALLA, NO PASA NADA IMPORTANTE
//
// El nodo detecta, infiere, decide, aparta, bloquea y registra exactamente
// igual con el proveedor caído. Lo único que se pierde es que alguien se entere
// hoy en vez de mañana, y el panel conserva todo. Ninguna función de este
// paquete devuelve un error que alguien de arriba deba tratar como fatal.
package canal

import (
	"context"
	"log/slog"

	"nasd/internal/aviso"
)

// Canal es a dónde sale un aviso.
//
// # POR QUÉ ES UNA INTERFAZ CON DOS IMPLEMENTACIONES DESDE EL PRIMER DÍA
//
// No es por si algún día hay otro proveedor —eso sería código «por si acaso», y
// este proyecto no lo escribe—. Es porque hay DOS de verdad y hacen falta las
// dos:
//
//   - El diario existe siempre y no depende de nadie. Es lo que garantiza que
//     un nodo sin credencial de Telegram siga dejando constancia de lo que
//     habría avisado.
//   - Telegram es opcional y puede fallar.
//
// Con una sola implementación, la de red sería un camino privilegiado y
// quitarla dejaría al sistema mudo. Con dos, el proveedor es sustituible por
// construcción — que es además la única mitigación real del único proveedor de
// este diseño que no se puede autoalojar (ADR-0074).
type Canal interface {
	// Enviar entrega el aviso o devuelve por qué no pudo.
	//
	// DEBE respetar la cancelación del contexto: al apagar el servicio, un
	// envío colgado no puede retrasar el apagado ordenado.
	Enviar(ctx context.Context, a aviso.Aviso) error
	// Nombre identifica el canal en el diario. No lleva secretos.
	Nombre() string
}

// Diario escribe el aviso en el registro estructurado del servicio.
//
// # NO ES UN SUSTITUTO POBRE: ES LA RED DE SEGURIDAD
//
// Todo lo que se manda fuera pasa TAMBIÉN por aquí, y a propósito. Si mañana el
// responsable quiere saber qué se le avisó el martes y el proveedor ya lo
// borró, el diario del nodo lo tiene —persistente en el disco de datos por
// ADR-0037, con ~20 días reales de profundidad medidos—.
//
// Y es lo que hace que un nodo sin credencial configurada no sea un nodo mudo:
// sigue habiendo constancia local de cada situación que habría salido.
type Diario struct{ reg *slog.Logger }

func NuevoDiario(reg *slog.Logger) Diario { return Diario{reg: reg} }

func (Diario) Nombre() string { return "diario" }

// Enviar anota el aviso con el nivel que corresponde a su severidad.
//
// Los niveles son los MISMOS que ya usa el ciclo de mantenimiento para los
// veredictos de /estado (mantenimiento.go, anunciar): ERROR para lo que hay que
// atender, WARN para lo que merece una mirada, INFO para lo demás. No se
// estrena un vocabulario nuevo en el diario por tener una capa nueva.
//
// NO SE VUELCA EL TEXTO DEL MENSAJE, solo sus campos. El texto lleva marcado
// HTML y saltos de línea, y una línea de journald con eso dentro es ilegible
// justo el día que hay que leerla.
func (d Diario) Enviar(_ context.Context, a aviso.Aviso) error {
	args := []any{"severidad", a.Severidad.String(), "titulo", a.Titulo}
	switch a.Severidad {
	case aviso.Rojo:
		d.reg.Error("AVISO", args...)
	case aviso.Amarillo, aviso.Naranja:
		d.reg.Warn("aviso", args...)
	default:
		d.reg.Info("aviso", args...)
	}
	return nil
}
