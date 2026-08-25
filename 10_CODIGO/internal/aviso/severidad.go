// Package aviso decide QUÉ merece salir del nodo, con qué urgencia y con qué
// palabras. Es la capa que va DETRÁS del enforcement, nunca delante.
//
// # LA REGLA QUE GOBIERNA ESTE PAQUETE
//
// El proyecto entero se ordena así (ADR-0071):
//
//	HECHO -> INFERENCIA -> DECISIÓN -> ENFORCEMENT
//
// Esto se añade al final de esa cadena y no la toca:
//
//	... -> ENFORCEMENT -> NOTIFICACIÓN
//
// Nunca al revés. Ninguna decisión de seguridad puede depender de que un aviso
// se haya entregado, y ninguna función de este paquete se llama desde el
// camino de una petición. Si el proveedor externo está caído, el nodo detecta,
// aparta, bloquea y registra exactamente igual: lo único que se pierde es que
// alguien se entere hoy en vez de mañana.
//
// # TELEMETRÍA COMPLETA NO ES NOTIFICACIÓN COMPLETA
//
// El panel conserva TODO —los cuatro historiales de internal/seguridad siguen
// siendo la fuente de la verdad— y este paquete es deliberadamente selectivo.
// Puede haber 500 hechos guardados y UN aviso agrupado. Confundir las dos
// cosas es lo que produce un canal que nadie lee, y entonces el aviso que sí
// importaba llega al mismo sitio que los otros 499.
//
// # ESTE PAQUETE NO SABE DE RED NI DE DISCO (salvo su propia marca)
//
// No importa net/http ni internal/geoip, por el mismo motivo por el que
// internal/seguridad tampoco lo hace: modela decisiones sobre hechos, no
// protocolo. Quien habla con un proveedor es internal/adaptadores/aviso; quien
// resuelve el país es el adaptador web. Aquí solo entran valores ya resueltos.
package aviso

import (
	"encoding/json"
	"fmt"
)

// Severidad dice CUÁNTO pide de quien mira, y describe lo que ocurrió en el
// NODO — no lo intimidante que parezca quien lo provocó.
//
// # POR QUÉ ES UNA TERCERA ESCALA Y NO PODÍA REUTILIZAR NINGUNA DE LAS DOS
//
// El programa ya tenía dos, y miden cosas distintas:
//
//	seguridad.Gravedad  {Rutina, Aviso, Atencion}   -> el motivo de UN rechazo
//	web.veredicto       {ok, atencion, fallo, ...}  -> la salud del NODO
//
// Ninguna sirve entera: la primera no conoce la disponibilidad ni los
// hallazgos; la segunda no conoce a quien toca la puerta. Esta es la escala de
// la ENTREGA, y se DERIVA de las otras dos (ver Clase.Severidad y evaluar.go)
// en vez de calcularse aparte.
//
// Que sea derivada y no paralela es la propiedad que importa: si el panel
// dijera «Atención» y el aviso dijera Verde, uno de los dos estaría mintiendo,
// y 05_OPERACION §4.1 lleva desde la Fase 4 exigiendo que no haya dos
// criterios que puedan desincronizarse.
type Severidad uint8

const (
	// Verde — actividad que el nodo manejó correctamente. Se conserva y se
	// resume; no interrumpe a nadie.
	Verde Severidad = iota
	// Amarillo — conducta bastante persistente, hostil o significativa como
	// para merecer una mirada humana, AUNQUE los controles sigan funcionando.
	Amarillo
	// Naranja — problema de DISPONIBILIDAD o de vigilancia. No es intrusión, y
	// el vocabulario lo separa a propósito.
	//
	// # CASI SIEMPRE LO EMITE ALGUIEN QUE NO ES ESTE PROGRAMA
	//
	// El naranja de verdad —«el nodo dejó de reportar»— NO puede nacer aquí:
	// un nodo apagado no manda nada. Lo emite el testigo externo (ADR-0074).
	// Este valor existe porque el vocabulario tiene que ser el mismo a los dos
	// lados, y porque hay UN caso que sí nace en el nodo: que el propio canal
	// de avisos haya estado caído (ClaseCanalRecuperado).
	Naranja
	// Rojo — hay que intervenir. Debe ser RARO.
	//
	// Un solo suceso puede ser rojo si rompe una invariante; cientos de
	// peticiones siguen siendo verdes si el nodo las rechazó bien. En 21 días
	// medidos, este nodo produjo CERO hechos de esta clase.
	Rojo
)

// UltimaSeveridad es el mayor valor del enum, y existe para que ese tope viva
// en UN solo sitio — la misma lección que seguridad.UltimoMotivo aprendió de
// un defecto real, donde un recorrido se actualizó y el otro no.
const UltimaSeveridad = Rojo

// String es la clave estable con la que una severidad se persiste. Separada de
// Etiqueta por el mismo motivo que en Motivo y Senal: la etiqueta se reescribe
// por gusto, y si el archivo guardara la etiqueta, cambiar una palabra dejaría
// ilegible lo ya escrito.
func (s Severidad) String() string {
	switch s {
	case Verde:
		return "verde"
	case Amarillo:
		return "amarillo"
	case Naranja:
		return "naranja"
	case Rojo:
		return "rojo"
	}
	return "desconocida"
}

// SeveridadDesde recupera una severidad de su forma estable. El booleano
// distingue «no reconocida» de Verde, que es un valor legítimo: sin él, un
// archivo escrito por una versión futura se leería como si todo fuera verde,
// en silencio — que es el peor modo de fallo posible para esta pieza.
func SeveridadDesde(s string) (Severidad, bool) {
	for v := Verde; v <= UltimaSeveridad; v++ {
		if v.String() == s {
			return v, true
		}
	}
	return Verde, false
}

// Etiqueta es la palabra que se enseña.
func (s Severidad) Etiqueta() string {
	switch s {
	case Verde:
		return "ACTIVIDAD"
	case Amarillo:
		return "ATENCIÓN"
	case Naranja:
		return "SIN SEÑAL"
	case Rojo:
		return "CRÍTICO"
	}
	return "DESCONOCIDA"
}

// Marca es el distintivo visual del mensaje.
//
// Vive aquí y no en el formateador por lo mismo que Red.Etiqueta vive en
// seguridad y no en la plantilla: hay más de un sitio que lo pinta —el mensaje
// inmediato y el digest— y no pueden divergir.
func (s Severidad) Marca() string {
	switch s {
	case Verde:
		return "🟢"
	case Amarillo:
		return "🟡"
	case Naranja:
		return "🟠"
	case Rojo:
		return "🔴"
	}
	return "⚪"
}

// Interrumpe dice si esta severidad debe hacer sonar el teléfono.
//
// Solo el verde llega en silencio. Es la mitad del anti-fatiga que NO depende
// de nuestra política sino del proveedor: el resumen diario aterriza sin
// vibrar y lo crítico no (ADR-0074).
func (s Severidad) Interrumpe() bool { return s != Verde }

// MarshalJSON y UnmarshalJSON guardan la CLAVE ESTABLE y no el número del enum.
//
// Mismo motivo que en seguridad.Senal y seguridad.ClaseHallazgo: con el número,
// insertar una severidad nueva en medio del bloque const reinterpretaría en
// silencio todo lo ya escrito, y aquí lo ya escrito es la marca de qué se ha
// avisado — reinterpretarla mal significa callar un rojo o repetir un verde.
func (s Severidad) MarshalJSON() ([]byte, error) { return json.Marshal(s.String()) }

func (s *Severidad) UnmarshalJSON(b []byte) error {
	var clave string
	if err := json.Unmarshal(b, &clave); err != nil {
		return err
	}
	v, ok := SeveridadDesde(clave)
	if !ok {
		return fmt.Errorf("severidad desconocida: %q", clave)
	}
	*s = v
	return nil
}
