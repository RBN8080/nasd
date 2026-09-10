package web

// El flujo en vivo del panel de seguridad — /seguridad/flujo.
//
// # POR QUÉ ESTE FLUJO NO USA EL MUESTREADOR COMPARTIDO
//
// /estado y /administracion publican lo mismo para todo el que mire, así que un
// muestreador que reparte UN marco a todos los oyentes les sirve y además evita
// que el nodo se muestree dos veces (ADR-0056).
//
// El marco de seguridad es función del FILTRO: origen, ventana y motivo viajan
// en la URL, y dos pestañas pueden estar mirando cosas distintas. Un marco
// compartido le enseñaría a una las cifras de la otra —y sin fallar a gritos,
// que es la peor forma—. Por eso aquí se muestrea por conexión.
//
// ESO NO REINTRODUCE EL PELIGRO QUE EVITÓ ADR-0056, y conviene decir por qué:
// aquel era el doble muestreo de un lector CON ESTADO —el de /estado resta la
// muestra de CPU anterior, así que dos bucles se pisarían—. Este lector no
// guarda nada entre marcos: es función pura de (filtro, estado actual del
// nodo). No hay estado compartido que pueda competir, y lo delicado —el plazo
// de escritura, la revalidación de la sesión, el formato del evento— sigue
// estando escrito UNA vez, en emitirFlujo.

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"nasd/internal/seguridad"
)

// intervaloSeguridadVivo es cada cuánto se rehace el marco.
//
// NO SON LOS 250 ms DE /estado, y no es por pereza. Aquel intervalo está atado
// a la resolución de los jiffies de /proc/stat: por debajo, el porcentaje de
// CPU sale de dividir por casi cero. Aquí no se mide nada de eso; se releen el
// anillo de rechazos, el de conexiones y EL ARCHIVO DEL SENSOR, que son 100 KB
// de disco. Hacerlo cuatro veces por segundo en una Pi 3B es gasto sin
// contrapartida: lo que este panel cuenta —una petición rechazada, una
// dirección nueva— no llega a ese ritmo ni de lejos.
//
// Dos segundos es lo bastante rápido para que un barrido se vea llegar
// mientras se mira la pantalla, que es lo que el responsable pidió.
const intervaloSeguridadVivo = 2 * time.Second

// cifraViva es UN cuadro de «Volumen observado» con su ancla y su valor ya
// formateado.
//
// EL VALOR VIAJA COMO TEXTO, no como número, por ADR-0017: quien decide que
// «sin sensor» se escribe «—» es el servidor, igual que en la página. Si
// viajara el número, el JavaScript tendría que saber esa regla y habría dos
// sitios que decidir lo mismo.
type cifraViva struct {
	// Clave es el ancla del DOM —data-cifra—, no un rótulo: el nombre visible
	// del cuadro no cambia nunca y por eso no hace falta mandarlo.
	Clave string `json:"clave"`
	Valor string `json:"valor"`
}

type marcoSeguridad struct {
	Cifras []cifraViva `json:"cifras"`
	// Contencion es cuántas filas hay AHORA en «Contención activa» —apartados,
	// bloqueos y respuestas inesperadas—.
	//
	// NO SE MANDAN LAS FILAS, solo cuántas hay, y esa es una decisión tomada
	// antes en /administracion: en vivo va la cifra, no la tabla. Refrescar
	// filas bajo el cursor mientras alguien va a pulsar «Soltar» es peor que no
	// refrescarlas. Lo que el número permite es AVISAR de que hay algo nuevo y
	// dejar que sea la persona quien recargue.
	Contencion int `json:"contencion"`
}

// marcoDeSeguridad arma el marco para un filtro concreto.
//
// Lee lo mismo que la página y por el mismo camino. Lo único que no hace es
// componer las tablas: aquí no viajan.
func (s *Servidor) marcoDeSeguridad(f seguridad.Filtro) marcoSeguridad {
	ahora := time.Now()

	eventos := s.seguridad.Filtrados(f)
	origenes := seguridad.PorOrigen(eventos)
	resumen := seguridad.Resumir(eventos, origenes, s.seguridad.Total())

	conexiones := s.conexiones.Desde(f.Desde)

	// Los toques se leen del disco, como en la página, y un fallo aquí no tumba
	// el flujo: se deja el cuadro en «—», que es lo que la página enseña cuando
	// no hay sensor. Un error de lectura y un sensor ausente se parecen desde
	// aquí, y ninguno de los dos justifica cortar el resto de las cifras.
	hist, err := seguridad.LeerToques(s.rutaToques, f.Desde)
	if err != nil {
		s.reg.Warn("flujo de seguridad: no se pudo leer el historial de toques",
			"ruta", s.rutaToques, "error", err)
	}
	paquetes := "—"
	if hist.Hay {
		paquetes = strconv.Itoa(len(hist.EnVentana(f.Desde)))
	}

	return marcoSeguridad{
		Cifras: []cifraViva{
			{Clave: "paquetes", Valor: paquetes},
			{Clave: "conexiones", Valor: strconv.Itoa(len(conexiones))},
			{Clave: "rechazos", Valor: strconv.Itoa(resumen.Eventos)},
			{Clave: "direcciones", Valor: strconv.Itoa(resumen.IPsUnicas)},
			{Clave: "contrasenas", Valor: strconv.Itoa(resumen.AutenticacionFallida)},
		},
		Contencion: len(s.cuarentena.Vigentes(ahora)) +
			len(s.lista.Vigentes(ahora)) +
			len(s.hallazgos.Todos()),
	}
}

// flujoDeSeguridad sirve el flujo SSE de /seguridad, con el MISMO filtro que la
// página que lo abrió: seguridad.js le pasa la consulta de la URL tal cual.
//
// Si no la pasara, o si llegara con basura, filtroDeSeguridad cae en la ventana
// por omisión igual que la página — de modo que el peor caso es que el flujo
// hable de «Internet · 24 horas», que es con lo que el panel abre.
func (s *Servidor) flujoDeSeguridad(w http.ResponseWriter, r *http.Request) {
	emitirFlujo(s, w, r, func() (func(context.Context) (marcoSeguridad, bool), func()) {
		t := time.NewTicker(intervaloSeguridadVivo)
		siguiente := func(ctx context.Context) (marcoSeguridad, bool) {
			select {
			case <-ctx.Done():
				return marcoSeguridad{}, false
			case <-t.C:
				// LA VENTANA SE RECALCULA EN CADA MARCO, y por eso el filtro se
				// vuelve a interpretar aquí en vez de reutilizar el f de arriba
				// cuando lleva un «Desde» relativo: «últimas 24 horas» a las
				// 10:00 no es la misma ventana que a las 14:00, y una pestaña
				// abierta toda la tarde se habría quedado contando desde el
				// instante en que se abrió.
				vivo, _, _ := filtroDeSeguridad(r.URL.Query())
				return s.marcoDeSeguridad(vivo), true
			}
		}
		return siguiente, t.Stop
	})
}
