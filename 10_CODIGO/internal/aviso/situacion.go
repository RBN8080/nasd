package aviso

import (
	"strings"
	"time"
)

// Situación — la unidad que se notifica, y NO es el evento.
//
// # POR QUÉ NO SE NOTIFICA UN EVENTO
//
// El 2026-08-16, veinte direcciones de DRIFTNET recorrieron el 443 en cinco
// minutos. Con un mensaje por hecho, eso habrían sido veinte avisos de la misma
// historia, y el vigésimo primero —el que sí importara— habría llegado al mismo
// sitio: a un teléfono que su dueño ya dejó de mirar.
//
// Una Situación agrupa TODO lo que un mismo sujeto está haciendo dentro de una
// ventana: los hechos siguen enteros en el panel, y aquí se cuenta una sola
// historia con su evidencia resumida. Es la diferencia entre 500 hechos
// guardados y 1 aviso, que es exactamente lo que se buscaba.
//
// # UN SUJETO, UNA SITUACIÓN
//
// Un origen que explora Y al que además se acaba de apartar NO son dos avisos:
// son uno. La cuarentena es un HECHO de la situación de exploración —«respuesta:
// cuarentena aplicada»—, no una situación aparte. Verlo al revés fue el primer
// diseño y producía justo el ruido que esta pieza viene a evitar.

// Clase es QUÉ está pasando. Cada valor apunta a hechos que YA existen en
// internal/seguridad: no se inventa ninguna categoría para la que no haya un
// hecho medible detrás, que es la misma regla con la que ClaseHallazgo se
// quedó en un enum de un solo valor.
type Clase uint8

const (
	ClaseDesconocida Clase = iota
	// NO EXISTE UNA «ClaseActividad» PARA EL 401 CORRIENTE, y su ausencia es
	// una decisión:
	//
	// Un origen de Internet que pide algo y se lleva un rechazo limpio no tiene
	// una HISTORIA que contar, tiene VOLUMEN. El volumen se cuenta en el
	// resumen (Resumir, digest.go) a partir de las cifras, no se narra por
	// dirección. Darle una situación propia habría metido en el Registro una
	// clave por cada IP que toca la puerta, y además la deduplicación —que es
	// justo lo que debe pasar con una situación— habría hecho que la segunda
	// visita NO se contara en el resumen, que es justo lo que NO debe pasar con
	// un contador.
	//
	// Las dos cosas se separan aquí: SITUACIÓN es algo que se cuenta una vez;
	// VOLUMEN es algo que se suma siempre.

	// ClaseSondeoAjeno — se pidieron rutas de programas que este NAS no
	// ejecuta. Su propia explicación en seguridad dice que es «el rastreo
	// indiscriminado que recibe cualquier dirección pública», y ADR-0065 ya le
	// quitó el énfasis en el panel: aquí, por coherencia, va al resumen.
	ClaseSondeoAjeno
	// ClaseAnomalia — CONDUCTA SIN DETECTOR.
	//
	// # ES LA PIEZA QUE EL ENCARGO PEDÍA Y LA MÁS FÁCIL DE HACER MAL
	//
	// Un origen de Internet cuyos rechazos NO casan con ninguna señal conocida.
	// Puede ser algo nuevo o puede no ser nada, y el sistema no lo sabe. Lo que
	// hace es conservar el HECHO y decir exactamente eso.
	//
	// NO SE INVENTA UNA CATEGORÍA DE ATAQUE y no se atribuye intención: el
	// mensaje dice qué se observó, no qué se teme. Es la misma disciplina con
	// la que existe seguridad.MotivoDesconocido —«un 4xx sin clasificar es un
	// punto ciego REAL, y enseñarlo es lo que hace que se le ponga nombre»—.
	//
	// Su severidad NO es fija: nace en verde y sube a amarillo si la evidencia
	// lo justifica (ver evaluar.go). Es la costura por la que un detector
	// futuro se promueve el día que los datos lo pidan, y hasta entonces no se
	// finge saber más de lo que se sabe.
	ClaseAnomalia
	// ClaseExploracion — seguridad.SenalExploracion: ocho rutas distintas que
	// no existen desde el mismo origen.
	ClaseExploracion
	// ClaseFuerzaBruta — seguridad.SenalFuerzaBruta: contraseñas falladas.
	ClaseFuerzaBruta
	// ClaseRutaExpuesta — un seguridad.Hallazgo: el nodo respondió CON
	// CONTENIDO en una ruta que su arquitectura dice que no publica.
	//
	// Es el rojo por antonomasia y el único que habla del NODO en vez de quien
	// pide. En 21 días medidos hubo CERO.
	ClaseRutaExpuesta
	// ClaseEnforcementFallido — se decidió cerrar una conexión y el Close()
	// devolvió error, así que la conexión siguió viva.
	//
	// Rojo porque es la única forma de que el panel esté diciendo MENOS de lo
	// que pasa: hay un control que se creía ejecutado y no lo estaba.
	ClaseEnforcementFallido
	// ClaseAveriaDelNodo — un indicador de /estado pasó a fallo o a atención.
	// La severidad la decide el veredicto, no esta clase.
	ClaseAveriaDelNodo
	// ClaseCanalRecuperado — el canal de avisos estuvo caído y ha vuelto.
	//
	// # EL ÚNICO NARANJA QUE NACE EN EL NODO
	//
	// Existe porque un canal que estuvo mudo deja un agujero que nadie más
	// puede contar: durante ese rato pudo perderse un aviso. Se emite AL
	// VOLVER, que es el único instante en el que se puede entregar.
	ClaseCanalRecuperado
)

// UltimaClase es el mayor valor del enum. Mismo motivo que UltimaSeveridad.
const UltimaClase = ClaseCanalRecuperado

// String es la clave estable con la que una clase se persiste y forma parte de
// la identidad de una situación.
func (c Clase) String() string {
	switch c {
	case ClaseSondeoAjeno:
		return "sondeo_ajeno"
	case ClaseAnomalia:
		return "anomalia"
	case ClaseExploracion:
		return "exploracion"
	case ClaseFuerzaBruta:
		return "fuerza_bruta"
	case ClaseRutaExpuesta:
		return "ruta_expuesta"
	case ClaseEnforcementFallido:
		return "enforcement_fallido"
	case ClaseAveriaDelNodo:
		return "averia_del_nodo"
	case ClaseCanalRecuperado:
		return "canal_recuperado"
	}
	return "desconocida"
}

// ClaseDesde recupera una clase de su forma estable. Mismo contrato y mismo
// booleano que MotivoDesde, SenalDesde y ClaseHallazgoDesde.
func ClaseDesde(s string) (Clase, bool) {
	for c := ClaseDesconocida; c <= UltimaClase; c++ {
		if c.String() == s {
			return c, true
		}
	}
	return ClaseDesconocida, false
}

// Titulo es la primera línea del aviso: qué pasó, en una frase, sin alarmismo
// y sin afirmar más de lo observado.
//
// # DOS DE ESTOS TÍTULOS SON COPIA DE seguridad.Senal.Etiqueta, Y HAY QUE
// # MOVERLOS A LA VEZ
//
// ClaseSondeoAjeno y ClaseFuerzaBruta nombran la MISMA conducta que las
// señales del panel —evaluar.go las mapea una a una—, y sus textos se
// escribieron aquí a mano en vez de llamar al método. Son cadenas
// independientes y NINGUNA PRUEBA LAS COMPARA, así que al acortar las
// etiquetas del panel el 2026-09-10 estas se habrían quedado atrás en
// silencio: el nodo diría «Sondeo de software ajeno» en la pantalla y
// «Sondeo de software que aquí no existe» por Telegram, para el mismo hecho.
// Se mueven las dos con él.
//
// NO SE SUSTITUYEN POR UNA LLAMADA a Senal.Etiqueta(), y es deliberado:
// ClaseExploracion ya dice «Exploración sostenida» donde la señal dice
// «Exploración de rutas» —el aviso habla de lo que aguantó en el tiempo, la
// señal de lo que se vio en una petición—, y ese matiz se perdería atando los
// dos vocabularios. Lo que se comparte es el hecho, no la redacción.
func (c Clase) Titulo() string {
	switch c {
	case ClaseSondeoAjeno:
		return "Sondeo de software ajeno"
	case ClaseAnomalia:
		return "Conducta sin clasificar"
	case ClaseExploracion:
		return "Exploración sostenida"
	case ClaseFuerzaBruta:
		return "Contraseñas repetidas"
	case ClaseRutaExpuesta:
		return "Una ruta no publicada respondió con contenido"
	case ClaseEnforcementFallido:
		return "Un cierre de conexión no pudo ejecutarse"
	case ClaseAveriaDelNodo:
		return "Un indicador del nodo cambió de estado"
	case ClaseCanalRecuperado:
		return "El canal de avisos estuvo sin servicio"
	}
	return "Suceso sin clasificar"
}

// SeveridadBase es la severidad que corresponde a la clase por sí sola.
//
// Es una BASE y no la última palabra: evaluar.go puede subirla cuando la
// evidencia lo justifique (ClaseAnomalia) o cuando el veredicto lo diga
// (ClaseAveriaDelNodo). Nunca la baja: una situación no degrada.
func (c Clase) SeveridadBase() Severidad {
	switch c {
	case ClaseSondeoAjeno, ClaseAnomalia:
		return Verde
	case ClaseExploracion, ClaseFuerzaBruta, ClaseAveriaDelNodo:
		return Amarillo
	case ClaseCanalRecuperado:
		return Naranja
	case ClaseRutaExpuesta, ClaseEnforcementFallido:
		return Rojo
	}
	return Verde
}

// Recomendacion es QUÉ HACER, y es obligatoria en todo lo que interrumpe.
//
// El charter §8 exige que toda alerta sea accionable, y web/estado.go lleva
// desde la Fase 4 cumpliéndolo con el campo Accion de cada indicador. Aquí se
// sigue el mismo criterio y no se estrena otro.
//
// NINGUNA afirma más de lo que el nodo sabe: se pide mirar, nunca se declara
// una intrusión.
func (c Clase) Recomendacion() string {
	switch c {
	case ClaseSondeoAjeno:
		return "No se requiere intervención. Aquí no se ejecuta nada de eso."
	case ClaseAnomalia:
		return "No se requiere intervención inmediata. Revise /seguridad cuando le venga bien: " +
			"esta conducta todavía no tiene un detector propio y los hechos quedan guardados."
	case ClaseExploracion:
		return "No se requiere intervención inmediata. Revise /seguridad cuando le venga bien."
	case ClaseFuerzaBruta:
		return "Compruebe en /seguridad si el origen puede ser alguien de casa antes de bloquearlo."
	case ClaseRutaExpuesta:
		return "Revise /seguridad ahora. Hay que ir a mirar QUÉ hay en esa ruta y por qué se sirve. " +
			"Esto NO demuestra intrusión: demuestra que el nodo respondió donde no publica nada."
	case ClaseEnforcementFallido:
		return "Revise el diario del nodo: hubo una conexión que debía cerrarse y siguió viva, " +
			"así que el panel puede estar diciendo menos de lo que pasa."
	case ClaseAveriaDelNodo:
		return "Revise /estado. La acción concreta va en el propio indicador."
	case ClaseCanalRecuperado:
		return "No se requiere intervención. Revise /seguridad por si algo ocurrió mientras el canal estaba mudo."
	}
	return "Revise /seguridad."
}

// Hecho es un par «rótulo: valor» de la evidencia que acompaña a la situación.
//
// Es texto YA MINIMIZADO: quien construye la situación decide qué se puede
// enseñar fuera (ver evaluar.go y 04_SEGURIDAD §6.septies). Este tipo no filtra
// nada; solo transporta lo que se decidió enseñar.
type Hecho struct {
	Rotulo string `json:"rotulo"`
	Valor  string `json:"valor"`
}

// Situacion es lo que se notifica: un sujeto, lo que está haciendo, desde
// cuándo, cuántas veces y con qué evidencia.
type Situacion struct {
	Clase     Clase
	Severidad Severidad
	// Sujeto identifica de QUIÉN o de QUÉ habla. Normalmente la dirección de
	// origen; para lo que habla del nodo —hallazgos, averías, el canal— es la
	// ruta o el indicador, y vacío cuando habla del nodo entero.
	Sujeto  string
	Primera time.Time
	Ultima  time.Time
	// Veces son las observaciones ACUMULADAS de esta situación, no los eventos
	// del anillo. Lo rellena el Registro al absorber, no quien evalúa.
	Veces  int
	Hechos []Hecho
	// Accion sustituye a Clase.Recomendacion cuando quien construye la
	// situación conoce una acción MÁS CONCRETA que la genérica de su clase.
	//
	// Existe por un solo caso, y no por simetría: las averías del nodo. Cada
	// indicador de /estado ya lleva escrito su propio «qué hacer» desde la Fase
	// 4 —«Comprobar a mano por SSH: vcgencmd get_throttled…»— y reescribirlo
	// aquí habría creado un segundo texto para el mismo indicador, que es como
	// se acaba con dos consejos que se contradicen.
	//
	// Vacío es lo normal: manda la recomendación de la clase.
	Accion string
}

// Clave es la identidad de una situación a lo largo del tiempo, y es lo que
// permite que la segunda observación se absorba en vez de notificarse.
//
// Clase Y sujeto, las dos: la misma dirección explorando y esa misma dirección
// fallando contraseñas son dos historias distintas y merecen contarse por
// separado. Con solo el sujeto, la segunda taparía a la primera.
func (s Situacion) Clave() string { return s.Clase.String() + "|" + s.Sujeto }

// conHecho añade evidencia, saltándose los valores vacíos.
//
// Que se salten es deliberado: un rótulo con el valor en blanco se lee como un
// dato que se perdió, y este proyecto prefiere no decir nada antes que decir
// algo que hay que interpretar (mismo criterio que los punteros de
// web/estado.go y que Disponibilidad() devolviendo -1 sin muestras).
func (s *Situacion) conHecho(rotulo, valor string) {
	if strings.TrimSpace(valor) == "" {
		return
	}
	s.Hechos = append(s.Hechos, Hecho{Rotulo: rotulo, Valor: valor})
}
