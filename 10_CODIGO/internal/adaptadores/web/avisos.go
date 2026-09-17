package web

import (
	"fmt"
	"net/netip"
	"strings"
	"time"

	"nasd/internal/aviso"
	"nasd/internal/seguridad"
)

// ADR-0073.
//
// # POR QUÉ ESTE ARCHIVO ESTÁ EN EL ADAPTADOR WEB Y NO EN internal/aviso
//
// Porque los hechos que hacen falta para decidir están repartidos en seis
// sitios, y este paquete es el ÚNICO que los tiene todos delante:
//
//	los orígenes agregados ......... los calcula seguridad.Vigilar y llegan aquí
//	el país y el operador .......... s.geo, que internal/seguridad no conoce
//	los hallazgos .................. s.hallazgos
//	los cierres que fallaron ....... s.cierresFallidos, un contador privado
//	los veredictos del nodo ........ evaluar(), que vive en estado.go
//	las conexiones de Internet ..... s.conexiones
//
// Reunirlos en internal/aviso habría obligado a aquel paquete a conocer geoip y
// los contadores del servidor, es decir a dejar de ser dominio. Aquí se reúnen
// y allí se decide: es el mismo reparto con el que la raíz de composición une
// fsposix y web sin que ninguno conozca al otro (ADR-0014).
//
// # LO QUE ESTE ARCHIVO NO HACE
//
// No se llama desde ningún manejador, ni desde conRegistro, ni desde el
// ConnState. Lo llama la goroutine de seguridad.Vigilar, una vez por minuto.
// Ninguna función de aquí puede acabar en el camino de una petición.

// EvaluarAvisos decide qué merece salir del nodo y lo entrega, sin bloquear.
//
// La llama la raíz de composición desde el observador de seguridad.Vigilar, que
// le pasa los orígenes YA agregados. No los vuelve a calcular: PorOrigen recorre
// la ventana entera y hacerlo dos veces por minuto en un A53 sería pagar dos
// veces el mismo trabajo (ver el comentario de Vigilar).
func (s *Servidor) EvaluarAvisos(origenes []seguridad.Origen, nuevos []seguridad.Apartado, ahora time.Time) {
	if s.avisos == nil || s.emitir == nil {
		return
	}

	entrada := s.reunir(origenes, nuevos, seguridad.VentanaCuarentena, ahora)

	// EL ORDEN DE LAS TRES LLAMADAS ES LA ARQUITECTURA APROBADA, escrita en
	// tres líneas: los hechos se convierten en situaciones, las situaciones se
	// correlacionan contra lo ya avisado, y solo lo que sobrevive a eso se
	// entrega según la política. Ningún paso puede saltarse el anterior.
	for _, sit := range s.avisos.Absorber(aviso.Evaluar(entrada, ahora), ahora) {
		s.entregar(sit)
	}

	s.quizaResumen(ahora)
}

// entregar aplica la política y manda por donde toque.
func (s *Servidor) entregar(sit aviso.Situacion) {
	switch aviso.Decidir(sit.Severidad, s.modoAvisos) {
	case aviso.EntregaInmediata:
		s.emitir(aviso.Mensaje(sit))
	case aviso.EntregaRetenida:
		s.retenidas.Guardar(sit)
	case aviso.EntregaResumen:
		// No se hace nada: el resumen NO se compone de situaciones absorbidas
		// una a una, sino de una lectura nueva de la ventana entera. Ver
		// quizaResumen y el comentario de cabecera de aviso/digest.go — sumar
		// aquí multiplicaría por sesenta la actividad real, porque cada ciclo
		// mira una ventana de una hora cada minuto.
	}
}

// quizaResumen emite el resumen si toca.
//
// # SE EMITE AUNQUE NO HAYA PASADO NADA, Y ESA ES SU SEGUNDA FUNCIÓN
//
// Con 8 peticiones de Internet en 21 días medidos, este canal puede pasar
// semanas mudo. Un canal mudo es indistinguible de uno roto, así que el resumen
// es al canal de avisos lo que el latido es al nodo: la prueba periódica de que
// la vía sigue abierta. Llega en silencio (Aviso.Silencioso), así que no cuesta
// una interrupción.
func (s *Servidor) quizaResumen(ahora time.Time) {
	if s.periodoResumen <= 0 {
		return
	}
	if s.ultimoResumen.IsZero() {
		// Al arrancar NO se manda uno de golpe: se cuenta el periodo desde
		// ahora. Con quince reinicios en catorce días medidos, lo contrario
		// habría convertido el resumen diario en un resumen por reinicio.
		s.ultimoResumen = ahora
		return
	}
	if ahora.Sub(s.ultimoResumen) < s.periodoResumen {
		return
	}

	ventana := ahora.Sub(s.ultimoResumen)
	s.ultimoResumen = ahora

	// SE RELEE LA VENTANA ENTERA, no se reutiliza la del ciclo. La del ciclo es
	// de una hora —la de la cuarentena— y el resumen habla de veinticuatro.
	amplia := s.reunir(seguridad.PorOrigen(s.seguridad.Desde(ahora.Add(-ventana))), nil, ventana, ahora)
	retenidas, omitidas := s.retenidas.Vaciar()
	resumen := aviso.Resumir(amplia, retenidas, ahora)
	resumen.RetenidasOmitidas += omitidas
	resumen.Avisadas, resumen.Absorbidas = s.avisos.Cuentas()

	s.emitir(aviso.MensajeResumen(resumen))
}

// reunir compone la Entrada del dominio a partir de lo que este adaptador tiene.
//
// Es la función que hace de traductora entre «lo que el servidor sabe» y «lo
// que hace falta para decidir», y por eso es donde se resuelven las dos cosas
// que internal/seguridad no puede saber: de dónde es cada dirección y si se le
// acaba de cerrar la puerta.
func (s *Servidor) reunir(origenes []seguridad.Origen, nuevos []seguridad.Apartado, ventana time.Duration, ahora time.Time) aviso.Entrada {
	// Índice de lo que se acaba de apartar, para poder colgárselo a su origen.
	// Un apartado NO es una situación aparte: es la RESPUESTA del nodo a la
	// conducta que ya tiene una, y separarlos produciría dos avisos de la misma
	// historia (ver aviso/situacion.go).
	apartados := make(map[netip.Addr]*seguridad.Apartado, len(nuevos))
	for i := range nuevos {
		apartados[nuevos[i].IP] = &nuevos[i]
	}

	vistos := make([]aviso.OrigenVisto, 0, len(origenes))
	for _, o := range origenes {
		v := aviso.OrigenVisto{Origen: o, Apartado: apartados[o.IP]}
		// El país y el operador solo tienen sentido para Internet: una IP
		// privada no tiene operador, y la base puede no estar instalada. Las
		// tres ausencias acaban igual —cadena vacía— y el mensaje sencillamente
		// no lleva esa línea, en vez de llevar un «desconocido» que se leería
		// como un dato.
		if o.Red.DeFuera() {
			if info, ok := s.geo.Buscar(o.IP); ok {
				v.Geo = strings.TrimSpace(fmt.Sprintf("%s · AS%d %s", info.Pais, info.ASN, info.Nombre))
			}
		}
		vistos = append(vistos, v)
	}

	return aviso.Entrada{
		Origenes:           vistos,
		Hallazgos:          s.hallazgos.Todos(),
		CierresFallidos:    s.cierresFallidos.Load(),
		ConexionesInternet: len(s.conexiones.Desde(ahora.Add(-ventana))),
		Ventana:            ventana,
	}
}

// AvisarAveria entrega el aviso de un indicador que ACABA DE CAMBIAR de estado.
//
// La llama anunciar() (mantenimiento.go), que es quien lleva desde la Fase 4 la
// memoria de veredictos previos y por tanto el único que sabe distinguir «sigue
// mal» de «acaba de ponerse mal».
//
// NO PASA POR EL REGISTRO, y el motivo está escrito en aviso.Entrada: aquella
// deduplicación es por aparición y con seis horas de vida, así que un indicador
// que falle, se recupere y vuelva a fallar la misma tarde no avisaría la segunda
// vez. Aquí el filtro es el cambio de veredicto, que para un nivel es el
// correcto.
func (s *Servidor) AvisarAveria(a aviso.Averia, ahora time.Time) {
	if s.emitir == nil {
		return
	}
	s.entregar(aviso.DeAveria(a, ahora))
}

// CuerpoDelLatido compone el diagnóstico que viaja con cada latido.
//
// # QUÉ SALE Y QUÉ NO, Y POR QUÉ ES TAN POCO
//
// El latido dice «estoy», no «esto ha pasado». Se manda cada cinco minutos —288
// veces al día—, así que todo lo que entre aquí se le entrega al proveedor 288
// veces diarias para siempre. Por eso NO lleva ni una dirección, ni una ruta, ni
// un nombre de cuenta: lo que hace falta para saber que el nodo vive es que el
// nodo escriba, y lo demás es telemetría regalada.
//
// Las cinco líneas que sí van tienen una función concreta cuando el responsable
// abre el testigo tras una ausencia: le dicen si el nodo volvió entero, cuánto
// lleva en pie y si hay algo esperándole en el panel.
func (s *Servidor) CuerpoDelLatido() string {
	var b strings.Builder
	fmt.Fprintf(&b, "nasd en pie desde %s\n", s.arranque.Format(time.RFC3339))
	fmt.Fprintf(&b, "uptime: %s\n", time.Since(s.arranque).Round(time.Minute))
	fmt.Fprintf(&b, "hallazgos: %d\n", len(s.hallazgos.Todos()))
	if s.avisos != nil {
		avisadas, absorbidas := s.avisos.Cuentas()
		fmt.Fprintf(&b, "avisos: %d emitidos, %d absorbidos\n", avisadas, absorbidas)
	}
	return b.String()
}
