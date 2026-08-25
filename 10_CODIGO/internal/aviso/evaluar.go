package aviso

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"nasd/internal/seguridad"
)

// Evaluación — de hechos a situaciones, sin tocar nada.
//
// # ES UNA FUNCIÓN PURA, Y ESO NO ES ESTILO
//
// Evaluar no lee disco, no abre sockets, no mira el reloj por su cuenta y no
// muta nada de lo que recibe. Toma hechos y devuelve situaciones. Las tres
// consecuencias son las que se buscaban:
//
//  1. Se prueba entera sin red, sin disco y sin servidor, igual que
//     seguridad.PorOrigen y seguridad.Resumir.
//  2. No puede, ni por descuido, meter una llamada externa en el camino de una
//     petición: no tiene con qué.
//  3. Afinar un umbral no reescribe historia, porque aquí no se guarda nada.
//
// # DÓNDE VIVE LA MINIMIZACIÓN DE DATOS
//
// AQUÍ, y en ningún otro sitio. Los Hechos que salgan de esta función son
// exactamente lo que puede viajar a un tercero (04_SEGURIDAD §6.septies). El
// formateador de mensaje.go no filtra: pinta lo que reciba. Un solo sitio puede
// equivocarse, en vez de uno por cada canal que se enchufe — el mismo argumento
// con el que seguridad.Conexiones.Anotar se queda el filtro «solo Internet».

// topeSondasEnAviso es cuántas rutas se enseñan como evidencia.
//
// TRES, y no las diez de seguridad.TopeSondas: aquello se lee en una pantalla
// con la tabla entera al lado, y esto entra en una notificación de móvil que
// compite con el resto de la vida de quien la recibe. Tres bastan para
// reconocer un diccionario de escáner; a partir de ahí es más de lo mismo, y el
// panel lo tiene entero.
const topeSondasEnAviso = 3

// Averia es un indicador de /estado que cambió de veredicto, traducido a lo que
// esta capa necesita.
//
// # POR QUÉ NO SE IMPORTA EL TIPO DEL ADAPTADOR WEB
//
// Porque este paquete no puede depender de aquel: web importa seguridad y
// acabaría importando esto, y el ciclo no compilaría. Pero además no debe —
// modelar decisiones sobre hechos no exige saber cómo se pinta una fila de
// /estado. Es el mismo corte por el que internal/seguridad no conoce
// internal/geoip y quien los une es el adaptador (ADR-0014).
type Averia struct {
	// Clave es el identificador estable del indicador («disco», «limitacion»),
	// no su rótulo. Va al Sujeto de la situación, así que cambiarlo rompería la
	// continuidad de una avería en curso — igual que la Clave de web.indicador.
	Clave  string
	Nombre string
	Valor  string
	// Accion es el «qué hacer» que ya escribe web/estado.go. Se transporta tal
	// cual en vez de reescribirlo aquí: dos textos para el mismo indicador
	// acabarían diciendo cosas distintas.
	Accion string
	// Grave distingue el veredicto «fallo» del veredicto «atención», que es lo
	// que decide entre rojo y amarillo.
	Grave bool
}

// OrigenVisto es un seguridad.Origen con lo poco que ese paquete no puede
// saber: de dónde es la dirección y si se le acaba de cerrar la puerta.
//
// Lo compone el adaptador web, que es quien tiene la base de país/operador y la
// cuarentena delante. Aquí llega ya resuelto para que la evaluación siga siendo
// una función pura sobre valores.
type OrigenVisto struct {
	seguridad.Origen
	// Geo es «GB · AS211298 DRIFTNET», o vacío si no hay base instalada.
	//
	// VACÍO ES UN ESTADO LEGÍTIMO Y FRECUENTE: la base de geoip es la única
	// dependencia opcional del servidor, así que un nodo sin 18_geoip.sh
	// ejecutado funciona igual y el aviso sencillamente no lleva esa línea.
	Geo string
	// Apartado no es nil si la cuarentena acaba de apartar esta dirección.
	//
	// Va DENTRO del origen y no como una lista aparte a propósito: un apartado
	// no es una situación propia, es la RESPUESTA del nodo a la conducta que ya
	// tiene situación. Separarlos produciría dos avisos de la misma historia.
	Apartado *seguridad.Apartado
}

// Entrada son los hechos ya reunidos sobre los que se decide.
//
// Los reúne quien los posee —el ciclo de mantenimiento del adaptador web— y no
// esta función, porque están repartidos entre cuatro estructuras de
// internal/seguridad, los contadores del servidor y los veredictos de /estado.
type Entrada struct {
	// Origenes son los de la ventana, ya agregados por seguridad.PorOrigen.
	Origenes []OrigenVisto
	// Hallazgos son TODOS los que el nodo conserva, no solo los nuevos: quién
	// decide qué es nuevo es el Registro, no esta función. Un hallazgo no
	// caduca por que pase el tiempo.
	Hallazgos []seguridad.Hallazgo
	// AQUÍ NO HAY AVERÍAS DEL NODO, y su ausencia es una decisión.
	//
	// Los veredictos de /estado son NIVELES, no sucesos: un disco lleno sigue
	// lleno el minuto siguiente. Lo que hay que comunicar es el CAMBIO, y de
	// eso ya se encarga anunciar() en el ciclo de mantenimiento desde la Fase
	// 4, con su propia memoria de veredictos previos.
	//
	// Pasarlas por aquí habría tenido dos costes reales: obligaría a leer el
	// sistema —statfs y un subproceso vcgencmd— cada minuto en vez de cada
	// cinco, y las metería en el Registro, cuya deduplicación es por APARICIÓN.
	// Con eso, un indicador que fallara, se recuperara y volviera a fallar
	// dentro de la misma ventana de seis horas NO avisaría la segunda vez, que
	// es justo cuando más falta hace.
	//
	// Se construyen con DeAveria y se entregan directamente. Ver
	// web/avisos.go.

	// CierresFallidos es el contador acumulado de conexiones que debían
	// cerrarse y no se pudieron cerrar.
	CierresFallidos int64
	// ConexionesInternet son las conexiones TCP aceptadas desde Internet en la
	// ventana. Es VOLUMEN para el resumen, no una situación.
	ConexionesInternet int
	// Ventana es cuánto historial se miró, para poder decirlo en vez de dejar
	// que quien lea suponga.
	Ventana time.Duration
}

// Evaluar convierte hechos en situaciones, de la más grave a la menos.
//
// NO decide si se notifican —eso es del Registro y de la política—, ni las
// deduplica, ni las persiste. Solo dice qué está pasando ahora mismo.
func Evaluar(e Entrada, ahora time.Time) []Situacion {
	var out []Situacion

	for _, o := range e.Origenes {
		if s, hay := situacionDeOrigen(o); hay {
			out = append(out, s)
		}
	}

	// Los hallazgos, uno por RUTA. Es la única clase cuyo sujeto no es una
	// dirección, y con motivo: una ruta que se publica está publicada para todo
	// el mundo, así que quién la pidió es secundario (seguridad/hallazgos.go).
	for _, h := range e.Hallazgos {
		out = append(out, situacionDeHallazgo(h))
	}

	// El enforcement que no se pudo ejecutar. Sujeto vacío: habla del nodo.
	if e.CierresFallidos > 0 {
		s := Situacion{
			Clase:     ClaseEnforcementFallido,
			Severidad: ClaseEnforcementFallido.SeveridadBase(),
			Primera:   ahora,
			Ultima:    ahora,
		}
		s.conHecho("Conexiones no cerradas", fmt.Sprintf("%d desde el último arranque", e.CierresFallidos))
		s.conHecho("Qué significa", "se decidió cerrar la conexión y el Close() devolvió error, así que siguió viva")
		out = append(out, s)
	}

	ordenar(out)
	return out
}

// DeAveria compone la situación de un indicador del nodo que cambió de estado.
//
// Está separada de Evaluar a propósito: la llama el ciclo de mantenimiento
// cuando un veredicto CAMBIA, no cada vez que se mira. Ver el comentario de
// Entrada sobre por qué las averías no pasan por el Registro.
func DeAveria(a Averia, ahora time.Time) Situacion {
	s := Situacion{
		Clase:   ClaseAveriaDelNodo,
		Sujeto:  a.Clave,
		Primera: ahora,
		Ultima:  ahora,
		Veces:   1,
		Accion:  a.Accion,
	}
	// El veredicto manda sobre la base de la clase: «fallo» es rojo y
	// «atención» es amarillo. No se inventa un umbral propio — web/estado.go ya
	// decidió eso, y tener dos criterios es lo que 05_OPERACION §4.1 prohíbe
	// expresamente.
	if a.Grave {
		s.Severidad = Rojo
	} else {
		s.Severidad = Amarillo
	}
	s.conHecho("Indicador", a.Nombre)
	s.conHecho("Valor", a.Valor)
	return s
}

// situacionDeOrigen decide qué historia cuenta una dirección, si es que cuenta
// alguna.
//
// Devuelve falso para el caso NORMAL —alguien de fuera pidió algo y se llevó un
// rechazo limpio—, que es volumen y no historia. Ver el comentario de
// ClaseDesconocida en situacion.go.
func situacionDeOrigen(o OrigenVisto) (Situacion, bool) {
	// SOLO DESDE INTERNET, y se comprueba aquí aunque seguridad.PorOrigen ya
	// solo emita señales para lo de fuera. Es la misma barandilla repetida que
	// Cuarentena.Evaluar aplica antes de apartar, y por el mismo motivo: de
	// esta lista sale algo que llega al teléfono del responsable, y que la casa
	// quede fuera no puede depender de que otra función haya hecho bien su
	// parte.
	if !o.Red.DeFuera() {
		return Situacion{}, false
	}

	clase, hay := claseDeOrigen(o)
	if !hay {
		return Situacion{}, false
	}

	s := Situacion{
		Clase:     clase,
		Severidad: clase.SeveridadBase(),
		Sujeto:    o.IP.String(),
		Primera:   o.Primera,
		Ultima:    o.Ultima,
	}

	// LA ANOMALÍA SUBE A AMARILLO CON LA EVIDENCIA, y este es el único sitio
	// donde una severidad se calcula en vez de leerse de la clase.
	//
	// Una conducta sin detector es verde mientras solo sea rara. Pasa a
	// amarillo cuando alguno de sus rechazos es de los que NO se alcanzan
	// navegando —CSRF, permiso insuficiente, recurso reservado, límite de
	// intentos, cuerpo excesivo—, es decir cuando seguridad.Gravedad ya dijo
	// «Atencion» sobre el hecho suelto. No se estrena un umbral: se reutiliza
	// el juicio que el paquete de hechos ya emitió.
	if clase == ClaseAnomalia && o.Gravedad >= seguridad.Atencion {
		s.Severidad = Amarillo
	}

	s.conHecho("Origen", o.IP.String())
	s.conHecho("País / ASN", o.Geo)
	s.conHecho("Actividad", fmt.Sprintf("%d rechazos en %s", o.Eventos, duracionCorta(o.Ultima.Sub(o.Primera))))

	if o.RutasInexistentes > 0 {
		s.conHecho("Rutas inexistentes", fmt.Sprintf("%d distintas", o.RutasInexistentes))
	}
	if n := o.PorMotivo[seguridad.CredencialIncorrecta]; n > 0 {
		s.conHecho("Contraseñas falladas", fmt.Sprintf("%d", n))
	}
	if rutas := rutasPublicables(o.Evidencia, topeSondasEnAviso); len(rutas) > 0 {
		s.conHecho("Sondas", strings.Join(rutas, ", "))
	}
	// Las señales van juntas y en su forma estable: si un origen dispara dos, el
	// título cuenta la dominante y esta línea dice que hubo más de una, en vez
	// de emitir un segundo aviso de la misma historia.
	if len(o.Senales) > 1 {
		s.conHecho("Señales", strings.Join(clavesDeSenales(o.Senales), ", "))
	}

	// LA RESPUESTA DEL NODO, que es la mitad que convierte una alarma en un
	// informe: sin ella, quien lea no sabe si tiene que hacer algo YA o si el
	// nodo ya se ocupó.
	if o.Apartado != nil {
		s.conHecho("Respuesta", fmt.Sprintf("cuarentena automática aplicada hasta %s",
			o.Apartado.Hasta.Format("15:04 del 02/01")))
		if o.Apartado.Frenados > 0 {
			s.conHecho("Conexiones frenadas", fmt.Sprintf("%d", o.Apartado.Frenados))
		}
	}

	return s, true
}

// claseDeOrigen elige QUÉ historia cuenta una dirección cuando podría contar
// varias.
//
// # LA PRECEDENCIA ESTÁ ESCRITA, NO ES UN ACCIDENTE DEL ORDEN DEL SLICE
//
// Es la lección de seguridad/puerta.go, donde la atribución de un cierre
// resultó ser una consecuencia no escrita del cortocircuito de un «||» y habría
// cambiado al reordenar los términos. Aquí el orden es una decisión:
//
//  1. FUERZA BRUTA gana a todo lo demás. Es la única que habla de la puerta de
//     entrada: quien insiste con contraseñas busca ENTRAR, no mapear.
//  2. EXPLORACIÓN después. Es conducta dirigida y sostenida.
//  3. ANOMALÍA antes que el sondeo ajeno, y no al revés: «no sé qué es esto»
//     merece más atención que «esto es el ruido de fondo de Internet».
//  4. SONDEO AJENO al final, porque su propia explicación dice que no es nada.
func claseDeOrigen(o OrigenVisto) (Clase, bool) {
	tiene := func(x seguridad.Senal) bool { return slices.Contains(o.Senales, x) }

	switch {
	case tiene(seguridad.SenalFuerzaBruta):
		return ClaseFuerzaBruta, true
	case tiene(seguridad.SenalExploracion):
		return ClaseExploracion, true
	}

	// Sin señal conocida. Lo que decide entre «anomalía» y «nada» es si hubo
	// algún hecho que NO se explique como tráfico corriente: un motivo que el
	// servidor no supo clasificar, o uno al que no se llega navegando.
	if o.PorMotivo[seguridad.MotivoDesconocido] > 0 || o.Gravedad >= seguridad.Atencion {
		return ClaseAnomalia, true
	}
	if tiene(seguridad.SenalSoftwareAjeno) {
		return ClaseSondeoAjeno, true
	}
	return ClaseDesconocida, false
}

// situacionDeHallazgo describe una ruta que el nodo atendió y no debía.
//
// LA RUTA SÍ SALE, al contrario que en cualquier otro sitio de este paquete, y
// es seguro por construcción: seguridad.RespuestaInesperada solo produce
// hallazgos para rutas de la familia ajena y FUERA del espacio de archivos del
// usuario. Una ruta ahí es «/.git/config», nunca «/abrir/Fotos/...».
func situacionDeHallazgo(h seguridad.Hallazgo) Situacion {
	s := Situacion{
		Clase:     ClaseRutaExpuesta,
		Severidad: ClaseRutaExpuesta.SeveridadBase(),
		Sujeto:    h.Ruta,
		Primera:   h.Primera,
		Ultima:    h.Ultima,
	}
	s.conHecho("Ruta", h.Ruta)
	s.conHecho("Método", h.Metodo)
	s.conHecho("Estado HTTP", fmt.Sprintf("%d", h.Estado))
	s.conHecho("Veces", fmt.Sprintf("%d", h.Veces))
	if h.UltimoOrigen.IsValid() {
		s.conHecho("Último origen", h.UltimoOrigen.String())
	}
	// Lo que NO demuestra, dicho en el propio aviso y no solo en el panel: es
	// la frase que impide que quien lo reciba de madrugada concluya lo que el
	// nodo no ha afirmado.
	s.conHecho("Qué NO demuestra", "no prueba intrusión ni que nadie ejecutara nada")
	return s
}

// rutasPublicables filtra la evidencia y deja SOLO lo que puede salir del nodo.
//
// # ESTA FUNCIÓN ES LA FRONTERA DE PRIVACIDAD, Y ES DE LISTA POSITIVA
//
// Se apoya en seguridad.RutaDeSoftwareAjeno, la MISMA que decide la señal, la
// evidencia del panel y los hallazgos. Que sea la misma no es reutilización por
// ahorro: si esto usara una copia, un día alguien añadiría «.phtml» a una sola
// de ellas y las dos verdades divergirían.
//
// Lo que se deja fuera importa más que lo que entra. El 2026-08-24 este nodo
// registró una petición externa a
// «/abrir/Telefono B2 2017/Mensajeria Video/VID-20000101-0001.mp4» — una ruta
// REAL de la carpeta de una persona de la casa. Mandar eso a un tercero por
// comodidad de diagnóstico sería exactamente lo que 04_SEGURIDAD §6 lleva desde
// el principio impidiendo en el diario, deshecho por la puerta de al lado.
//
// Fallo cerrado: lo que no se reconoce como ruta ajena NO sale.
func rutasPublicables(evidencia []seguridad.Sonda, tope int) []string {
	out := make([]string, 0, tope)
	for _, sd := range evidencia {
		if len(out) >= tope {
			break
		}
		if !seguridad.RutaDeSoftwareAjeno(sd.Ruta) {
			continue
		}
		if !slices.Contains(out, sd.Ruta) {
			out = append(out, sd.Ruta)
		}
	}
	return out
}

func clavesDeSenales(ss []seguridad.Senal) []string {
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		out = append(out, s.Etiqueta())
	}
	return out
}

// ordenar deja lo más grave primero, y con desempate COMPLETO.
//
// El determinismo no es cosmético aquí: el Registro recorre esta lista para
// decidir qué notifica, y un orden que baila entre ciclos haría que dos
// ejecuciones con los mismos hechos produjeran avisos en distinto orden. Es el
// mismo criterio que seguridad.PorOrigen y sondasOrdenadas ya aplican.
func ordenar(ss []Situacion) {
	slices.SortFunc(ss, func(x, y Situacion) int {
		if c := cmp.Compare(y.Severidad, x.Severidad); c != 0 {
			return c
		}
		if c := y.Ultima.Compare(x.Ultima); c != 0 {
			return c
		}
		return strings.Compare(x.Clave(), y.Clave())
	})
}

// duracionCorta escribe un intervalo como lo diría una persona.
//
// Sin dependencias y sin decimales: «2 min» se lee de un vistazo en una
// notificación y «2m3.418s» —que es lo que da time.Duration.String()— no.
func duracionCorta(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%d s", max(int(d.Seconds()), 1))
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d h", int(d.Hours()))
	}
	return fmt.Sprintf("%d días", int(d.Hours()/24))
}
