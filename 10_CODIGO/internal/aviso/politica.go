package aviso

// Política de entrega — CUÁNDO llega, no CUÁNTO importa.
//
// # LA SEPARACIÓN QUE ESTE ARCHIVO EXISTE PARA MANTENER
//
// La severidad dice qué ocurrió en el nodo. El modo dice cómo quiere el
// responsable que se le moleste. Son dos ejes, y mezclarlos —hacer del silencio
// una quinta severidad, por ejemplo— habría producido lo de siempre: un ajuste
// de comodidad que acaba cambiando el juicio sobre un hecho.
//
// Aquí la severidad NUNCA se toca. Lo único que se decide es el camino.

// Modo es cómo quiere el responsable que se le entregue.
type Modo uint8

const (
	// ModoNormal — el día a día.
	ModoNormal Modo = iota
	// ModoSilencio — no interrumpir salvo por lo crítico.
	//
	// NO DEPENDE DE HORARIOS, y es deliberado: el responsable trabaja a horas
	// variables, así que una franja fija habría callado el nodo justo cuando
	// está despierto y lo habría despertado cuando no. Es un interruptor, no un
	// calendario.
	ModoSilencio
	// ModoObservacion — todo llega en el acto, incluida la actividad.
	//
	// Para desarrollo, pruebas o cuando se está investigando tráfico. Es el
	// modo más ruidoso a propósito: sirve para VER, no para vivir con él.
	ModoObservacion
)

const UltimoModo = ModoObservacion

func (m Modo) String() string {
	switch m {
	case ModoSilencio:
		return "silencio"
	case ModoObservacion:
		return "observacion"
	}
	return "normal"
}

// ModoDesde recupera un modo de su forma estable. El booleano distingue «no
// reconocido» de ModoNormal, que es un valor legítimo: sin él, un modo mal
// escrito en la configuración se leería como «normal» en silencio, y el
// responsable creería estar en silencio mientras el nodo le escribe.
func ModoDesde(s string) (Modo, bool) {
	for m := ModoNormal; m <= UltimoModo; m++ {
		if m.String() == s {
			return m, true
		}
	}
	return ModoNormal, false
}

func (m Modo) Etiqueta() string {
	switch m {
	case ModoSilencio:
		return "Silencio"
	case ModoObservacion:
		return "Observación temporal"
	}
	return "Normal"
}

// Entrega es el camino que toma un aviso.
type Entrega uint8

const (
	// EntregaInmediata — sale ya, por el canal, sonando.
	EntregaInmediata Entrega = iota
	// EntregaResumen — se cuenta en el próximo resumen y no interrumpe.
	EntregaResumen
	// EntregaRetenida — merecía salir ya, pero el modo la retiene hasta el
	// próximo resumen, donde aparece ENTERA y no como un número.
	//
	// # POR QUÉ NO ES LO MISMO QUE EntregaResumen
	//
	// Las dos acaban en el resumen, y aun así son distintas y el resumen las
	// pinta distinto: lo que nació verde es una CIFRA («127 conexiones»); lo
	// retenido es una SITUACIÓN que alguien decidió no ver en su momento y que
	// tiene que leerse entera, con su origen y su evidencia. Fundirlas
	// convertiría un amarillo silenciado en una línea de estadística.
	EntregaRetenida
)

func (e Entrega) String() string {
	switch e {
	case EntregaResumen:
		return "resumen"
	case EntregaRetenida:
		return "retenida"
	}
	return "inmediata"
}

// Decidir dice por dónde sale un aviso.
//
// # EL ROJO SE RESUELVE ANTES DE MIRAR EL MODO, Y ESA LÍNEA ES LA GARANTÍA
//
// No es una optimización ni un atajo de escritura: es la forma de que la
// precedencia no se pueda perder al añadir un modo nuevo. Con el rojo dentro
// del switch, un cuarto modo escrito de prisa podría olvidarse de su caso y
// silenciar lo crítico sin que nada fallara a gritos. Aquí, para silenciar un
// rojo hay que borrar esta línea a propósito.
//
// Lo cubre TestElRojoSeEntregaInmediatoEnTodosLosModos, que recorre el enum
// entero en vez de enumerar los modos a mano — así un modo futuro entra en la
// prueba sin que nadie se acuerde de añadirlo.
func Decidir(s Severidad, m Modo) Entrega {
	if s == Rojo {
		return EntregaInmediata
	}
	if m == ModoObservacion {
		return EntregaInmediata
	}

	switch s {
	case Verde:
		// Al resumen siempre. Es la actividad de fondo de una dirección
		// pública: 8 peticiones de Internet en 21 días medidos, y aun así no
		// hay ninguna razón para que cada una haga sonar un teléfono.
		return EntregaResumen
	case Amarillo:
		if m == ModoSilencio {
			return EntregaRetenida
		}
		return EntregaInmediata
	case Naranja:
		// El naranja que NACE AQUÍ es «el canal estuvo mudo», y llega cuando el
		// canal vuelve. No se retiene: retenerlo hasta el próximo resumen sería
		// esconder justo el hecho de que hubo un hueco, y ese hueco es lo único
		// que explica por qué faltan avisos.
		//
		// El naranja de verdad —«el nodo dejó de reportar»— no pasa por aquí:
		// lo emite el testigo externo, que no ejecuta este código (ADR-0074).
		return EntregaInmediata
	}
	return EntregaResumen
}
