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
	"slices"
	"strconv"
	"time"

	"nasd/internal/geoip"
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
	// Cinta son los segmentos de la banda de la fila de órdenes, con su texto
	// YA ESCRITO por Go (ADR-0017). Ver cinta_seguridad.go.
	Cinta []segmentoCinta `json:"cinta"`
	// Velocidad es la clase «v1…v6» de la animación. Viaja porque el texto
	// cambia de largo: una puerta cerrada nueva alarga la pista, y con la
	// duración quieta la cinta se aceleraría sola.
	Velocidad string `json:"velocidad"`
	// Hora es el reloj del nodo en ESTE marco, ya formateado.
	//
	// ES LA PRUEBA DE VIDA DEL PANEL, y por eso viaja en cada marco aunque
	// nada más haya cambiado: el piloto lo enseña y lo hace avanzar de segundo
	// en segundo mientras siga confirmándose. Si los marcos dejan de llegar,
	// el reloj se para delante de quien mira — que es lo que distingue un
	// panel tranquilo de un panel muerto (ADR-0083).
	Hora string `json:"hora"`
	// Llegadas son los rechazos de Internet registrados desde el marco
	// anterior, para que el mapa los encienda en el acto. Ver llegadasDesde.
	Llegadas []llegada `json:"llegadas,omitempty"`
}

// llegada es UN rechazo de Internet recién registrado, con lo justo para
// encenderlo en el mapa: el país y el punto del dibujo (viewBox 1000×501).
// No viaja la dirección: el mapa no la enseña, y lo que no se pinta no sale.
type llegada struct {
	ISO    string  `json:"iso"`
	Nombre string  `json:"nombre"`
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
}

// topeLlegadas acota un marco: más de ocho anillos a la vez ya no se leen, y
// una ráfaga no debe inflar el flujo.
const topeLlegadas = 8

// llegadasDesde devuelve los rechazos de Internet posteriores a «desde» que
// caen dentro del filtro de la página, y el momento del más reciente visto —
// sea de Internet o no—, que es el «desde» del marco siguiente.
//
// LEE DEL MISMO ANILLO Y CON EL MISMO FILTRO QUE EL RESTO DEL MARCO: un país
// no se enciende por un rechazo que la tabla de debajo no vaya a contar. Sin
// base de operadores no hay país que encender y no viaja nada.
func (s *Servidor) llegadasDesde(f seguridad.Filtro, desde time.Time) ([]llegada, time.Time) {
	ultimo := desde
	if s.geo == nil {
		return nil, ultimo
	}
	nuevos := slices.DeleteFunc(s.seguridad.Filtrados(f), func(e seguridad.Evento) bool {
		return !e.Momento.After(desde)
	})
	slices.SortFunc(nuevos, func(a, b seguridad.Evento) int { return a.Momento.Compare(b.Momento) })
	var out []llegada
	for _, e := range nuevos {
		ultimo = e.Momento
		if !e.Red.DeFuera() || !e.Origen.IsValid() {
			continue
		}
		info, ok := s.geo.Buscar(e.Origen)
		if !ok || info.Pais == "" {
			continue
		}
		c, ok := centrosDePais()[info.Pais]
		if !ok {
			continue
		}
		out = append(out, llegada{
			ISO: info.Pais, Nombre: geoip.NombreDePais(info.Pais),
			X: redondear(c[0]), Y: redondear(c[1]),
		})
	}
	if len(out) > topeLlegadas {
		out = out[len(out)-topeLlegadas:]
	}
	return out, ultimo
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

	paises := "—"
	if s.geo != nil {
		paises = strconv.Itoa(s.paisesDistintos(origenes))
	}

	// LAS TRES LISTAS SE LEEN UNA VEZ Y SE REPARTEN entre el recuento de
	// contención y la cinta. Leerlas dos veces daría dos fotos de instantes
	// distintos dentro del MISMO marco, y el aviso de «ha cambiado» podría
	// dispararse contra una cuenta que la cinta no está enseñando.
	apartados := s.cuarentena.Vigentes(ahora)
	bloqueos := s.lista.Vigentes(ahora)
	hallazgos := s.hallazgos.Todos()

	// LA CINTA LA COMPONE LA MISMA FUNCIÓN QUE LA PÁGINA. No hay aquí ni un
	// texto escrito a mano: si lo hubiera, la banda cambiaría de palabra sola
	// al primer tic. Ver cinta_seguridad.go.
	cinta := componerCinta(hechosDeCinta{
		Apartados: len(apartados),
		Bloqueos:  len(bloqueos),
		Hallazgos: len(hallazgos),
		Rechazo:   ultimoRechazoDe(s.seguridad, f.Red),
		Frenado:   ultimoFrenadoDe(bloqueos, apartados),
		Red:       etiquetaDeRed(f.Red),
		Ahora:     ahora,
	})

	return marcoSeguridad{
		Cifras: []cifraViva{
			{Clave: "paquetes", Valor: paquetes},
			{Clave: "conexiones", Valor: strconv.Itoa(len(conexiones))},
			{Clave: "rechazos", Valor: strconv.Itoa(resumen.Eventos)},
			{Clave: "direcciones", Valor: strconv.Itoa(resumen.IPsUnicas)},
			// «Países» sustituye a «Contraseñas» en la banda por decisión del
			// responsable (2026-09-13). La cifra de contraseñas no se pierde:
			// resumen.AutenticacionFallida sigue existiendo, y la señal
			// «Contraseñas repetidas» sigue saliendo en la fila del origen y
			// en «Contención activa», que es donde pide una decisión.
			//
			// Sin base de operadores va «—» y no un cero, por lo mismo que
			// «Paquetes» sin sensor: un cero se lee como «no ha venido nadie
			// de fuera» cuando significa «no se está mirando».
			{Clave: "paises", Valor: paises},
		},
		Contencion: len(apartados) + len(bloqueos) + len(hallazgos),
		Cinta:      cinta.Segmentos,
		Velocidad:  cinta.Velocidad,
		Hora:       cinta.Hora,
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
		// Las llegadas cuentan desde que se abrió el flujo: lo anterior ya está
		// pintado en el mapa que trajo la página.
		desde := time.Now()
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
				m := s.marcoDeSeguridad(vivo)
				m.Llegadas, desde = s.llegadasDesde(vivo, desde)
				return m, true
			}
		}
		return siguiente, t.Stop
	})
}
