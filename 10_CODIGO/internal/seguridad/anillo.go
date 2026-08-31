package seguridad

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"nasd/internal/atomico"
)

// Capacidad del anillo.
//
// LA ROTACIÓN NO ES UNA TAREA, ES LA ESTRUCTURA. Un anillo de tamaño fijo no
// puede crecer: el evento 2001 pisa al 1, sin temporizador que purgue, sin
// una condición de carrera entre escribir y limpiar, y sin un modo de fallo
// donde la purga se cae y el archivo crece sin tope. Es la respuesta directa
// a «rotación y retención limitada para evitar crecimiento indefinido», y
// resuelve las dos con la misma decisión.
//
// 2000 sale del peor caso de memoria, no de una preferencia: con el
// User-Agent acotado a topeAgente y la ruta a topeRuta, un evento no pasa de
// ~400 B, así que el anillo lleno queda por debajo de 1 MB sobre un proceso
// que hoy ocupa ~8 MB de RSS. Es la cifra más alta que se puede afirmar sin
// que el panel deje de ser barato en un A53.
const Capacidad = 2000

// topeRuta acota la ruta guardada, por el mismo motivo que topeAgente: una
// petición puede pedir una URL larguísima y es precisamente un sondeo quien
// lo hace. 256 cubre cualquier ruta legítima de este NAS —los nombres de
// archivo reales rara vez pasan de 100— y trunca justo lo que no lo es.
const topeRuta = 256

// intervaloVolcado — cada cuánto el anillo baja a disco.
//
// NO se escribe en cada evento, a propósito. Un sondeo de Internet puede
// producir decenas de rechazos por segundo, y una escritura con fsync por
// cada uno convertiría el propio registro de seguridad en el amplificador
// que un atacante usaría para castigar el disco (P9: el medio de arranque es
// consumible, y aunque esto vive en /var/lib, la lógica de no regalar
// escrituras es la misma).
//
// El coste de la elección está acotado y escrito: en un corte de corriente se
// pierden los eventos desde el último volcado. NO se pierden de verdad —el
// diario de journald los tiene igual, persistente en el disco de datos por
// ADR-0037— porque esto es la vista rápida del panel, no la fuente de la
// verdad. Esa es exactamente la razón por la que se puede permitir volcar
// cada minuto en vez de cada evento.
const intervaloVolcado = time.Minute

// Anillo guarda los últimos Capacidad eventos, en memoria y respaldados por
// un archivo en /var/lib/nasd/ — el mismo StateDirectory y el mismo patrón de
// escritura atómica que internal/metricas (ADR-0024).
//
// # ESTADO COMPARTIDO (ADR-0013)
//
// Es el CUARTO del programa, tras el registro de subidas, el de sesiones y
// los contadores. A diferencia de los contadores —atómicos y sin coherencia
// entre campos— este SÍ lleva un mutex, y hace falta: un evento son varios
// campos que deben entrar juntos, y la lectura del panel recorre el buffer
// entero mientras las peticiones siguen anotando.
type Anillo struct {
	mu sync.Mutex
	// buf es un slice de tamaño FIJO, reservado entero al construir. Nunca
	// se hace append: escribir es asignar en una posición ya existente, así
	// que anotar un evento no reserva memoria ni puede provocar una pausa de
	// recolección en mitad de una petición.
	buf []Evento
	// siguiente es dónde va el próximo. Módulo Capacidad.
	siguiente int
	// total son los eventos anotados DESDE SIEMPRE, no los que caben. Se
	// guarda porque el panel necesita poder decir «se muestran 2000 de
	// 47 130»: sin esa cifra, un anillo lleno se lee como si eso fuera todo
	// lo que ha pasado, que es una mentira por omisión.
	total int64
	// sucio dice si hay algo sin volcar. Evita reescribir el archivo cada
	// minuto en un nodo tranquilo, que es el caso normal de este NAS.
	sucio bool
	// dias es el conteo por dia y por red que sostiene «Actividad por dia»
	// (pordia.go). Vive AQUI y no en una estructura aparte porque cuenta
	// exactamente los mismos rechazos que este anillo anota: con dos
	// escritores separados, uno podria perder un evento que el otro registro y
	// la grafica contradiria a la tabla sin que nada fallara a gritos.
	//
	// No crece con el trafico —un dia son cinco enteros— asi que conserva
	// DiasGrafica dias completos donde el buffer solo conserva Capacidad
	// eventos.
	dias porDia

	ruta string
}

// CargarAnillo lee el archivo si existe y deja el anillo listo.
//
// Que NO exista no es un error, igual que en metricas.CargarRegistro: un nodo
// recién actualizado no tiene historial todavía. Un archivo ILEGIBLE tampoco
// impide arrancar, y aquí la decisión es distinta de la de autenticacion: un
// registro de usuarios roto deja a gente sin poder entrar y por eso allí se
// falla a gritos, pero un historial de seguridad roto solo cuesta el
// historial. Negarse a arrancar el NAS entero por eso sería cambiar un
// servicio sano por un archivo de observación.
func CargarAnillo(ruta string) (*Anillo, error) {
	a := &Anillo{
		buf:  make([]Evento, Capacidad),
		ruta: ruta,
	}
	if err := a.releer(); err != nil {
		// Se devuelve el anillo VACÍO Y USABLE junto al error, para que la
		// raíz de composición pueda registrar el problema y seguir. Devolver
		// nil obligaría a quien llama a decidir entre no arrancar o duplicar
		// aquí la construcción del anillo vacío.
		return a, err
	}
	return a, nil
}

// marcaTotal es la cabecera que lleva el total historico en los TRES
// historiales de este paquete.
//
// EXISTE PORQUE EL TOTAL SE PERDIA EN CADA ARRANQUE. Hasta el 2026-08-18,
// releer() hacia «total = len(leidos)» y el archivo no guardaba la cifra: tras
// un reinicio, el panel decia «de N vistas desde que existe este registro» con
// N acotado a Capacidad. Con 15 arranques en 14 dias medidos en este nodo, esa
// frase era falsa casi siempre — decia «desde el ultimo corte de luz» creyendo
// decir «desde siempre», que es justo el modo de fallo que este proyecto
// persigue: una afirmacion que deja de ser verdad sin avisar.
//
// La estreno nas-sensor (ADR-0066) y se trae aqui para que los tres historiales
// digan la verdad de la misma forma.
const marcaTotal = "# total-visto: "

// totalDeCabecera lee la marca, si la linea la trae.
func totalDeCabecera(linea string) (int64, bool) {
	v, ok := strings.CutPrefix(linea, marcaTotal)
	if !ok {
		return 0, false
	}
	n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

func (a *Anillo) releer() error {
	f, err := os.Open(a.ruta)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("abrir el historial de seguridad %q: %w", a.ruta, err)
	}
	defer f.Close()

	// JSON Lines y no el formato con «:» de metricas: aquí hay campos de
	// texto libre —User-Agent y ruta— que pueden contener cualquier byte que
	// el cliente quiera, dos puntos y saltos de línea incluidos. Un separador
	// artesanal sería una inyección esperando a ocurrir; el escapado de JSON
	// ya está resuelto y probado en la biblioteca estándar.
	var (
		leidos []Evento
		total  int64
		dias   porDia
	)
	s := bufio.NewScanner(f)
	// Una línea no puede pasar del tamaño de un evento con sus topes; se deja
	// holgura para el escapado de JSON, que en el peor caso multiplica por 6
	// (\u00XX por byte).
	s.Buffer(make([]byte, 0, 8*1024), 8*1024)
	for n := 1; s.Scan(); n++ {
		linea := strings.TrimSpace(s.Text())
		if linea == "" || strings.HasPrefix(linea, "#") {
			if n, ok := totalDeCabecera(linea); ok {
				total = n
			}
			if clave, redes, ok := diaDeCabecera(linea); ok {
				dias.absorber(porDia{dias: map[string]map[Red]int{clave: redes}})
			}
			continue
		}
		var e eventoEnDisco
		if err := json.Unmarshal([]byte(linea), &e); err != nil {
			return fmt.Errorf("historial de seguridad, línea %d: %w", n, err)
		}
		ev, err := e.aEvento()
		if err != nil {
			return fmt.Errorf("historial de seguridad, línea %d: %w", n, err)
		}
		leidos = append(leidos, ev)
	}
	if err := s.Err(); err != nil {
		return fmt.Errorf("leer el historial de seguridad: %w", err)
	}

	// Si el archivo trae más de los que caben —porque una versión anterior
	// tenía el anillo más grande— se conservan los ÚLTIMOS, que son los que
	// importan. Descartar los primeros sería lo contrario de lo que quiere
	// quien mira un panel de seguridad.
	if len(leidos) > Capacidad {
		leidos = leidos[len(leidos)-Capacidad:]
	}
	copy(a.buf, leidos)
	a.siguiente = len(leidos) % Capacidad
	// EL MAXIMO DE LOS DOS, y las dos mitades tienen su motivo. Un archivo SIN
	// la marca es uno escrito antes del 2026-08-18: se cae a lo unico que se
	// puede afirmar, que es lo guardado, y desde el primer volcado la cifra ya
	// es la real. Y un total menor que lo guardado seria un archivo
	// inconsistente: nunca puede haber mas eventos conservados que vistos.
	a.total = max(total, int64(len(leidos)))

	// EL CONTEO POR DÍA SE RECONSTRUYE CON LO QUE HAYA EN EL BUFFER, y por eso
	// la gráfica no nace vacía el día que se estrena: los eventos que el anillo
	// ya guardaba llevan su fecha, así que los días a los que alcanzan se
	// pueden contar otra vez. absorber toma el MÁXIMO de las dos fuentes —ver
	// pordia.go—, de modo que un archivo sin líneas «# dia:» se cae a lo que el
	// buffer sostiene, y uno que ya las trae conserva además los días que el
	// buffer olvidó por rotación.
	a.dias = dias
	a.dias.absorber(deEventos(leidos))
	return nil
}

// Anotar registra un rechazo. Es lo que llama el adaptador web en caliente,
// así que no toca disco ni reserva memoria: escribe en una posición ya
// existente y sale.
func (a *Anillo) Anotar(e Evento) {
	e.Ruta = truncar(e.Ruta, topeRuta)
	e.Agente = TruncarAgente(e.Agente)
	if e.Red == RedDesconocida {
		e.Red = ClasificarRed(e.Origen)
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	a.buf[a.siguiente] = e
	a.siguiente = (a.siguiente + 1) % Capacidad
	a.total++
	a.dias.anotar(e.Momento, e.Red)
	a.sucio = true
}

// Total son los eventos vistos desde siempre, no los que caben.
func (a *Anillo) Total() int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.total
}

// Desde devuelve los eventos posteriores a un instante, del MÁS RECIENTE al
// más antiguo — que es el orden en el que se lee una vista cronológica de
// seguridad, y evita que quien la consume tenga que invertirla.
//
// Devuelve copias: quien mire no puede alterar el historial, y el mutex se
// suelta antes de que nadie lo recorra.
func (a *Anillo) Desde(t time.Time) []Evento {
	a.mu.Lock()
	defer a.mu.Unlock()

	cuantos := int(min(a.total, int64(Capacidad)))
	out := make([]Evento, 0, cuantos)
	for i := range cuantos {
		// Se retrocede desde la última escritura. +Capacidad antes del módulo
		// porque en Go el módulo de un negativo es negativo.
		pos := (a.siguiente - 1 - i + Capacidad) % Capacidad
		e := a.buf[pos]
		// El anillo está ordenado por naturaleza, así que el primero que
		// queda fuera de la ventana garantiza que los siguientes también.
		if e.Momento.Before(t) {
			break
		}
		out = append(out, e)
	}
	return out
}

// Mantener vuelca a disco cada intervaloVolcado hasta que se cancele, y hace
// un último volcado al salir.
//
// Ese volcado final es la razón de que el intervalo pueda ser de un minuto:
// un apagado ORDENADO —systemctl stop, un reinicio del servicio, el reinicio
// diario de P-11— no pierde nada. Solo un corte de corriente llega a perder
// eventos, y esos siguen en el diario (ADR-0037).
func (a *Anillo) Mantener(hecho <-chan struct{}, alFallar func(error)) {
	t := time.NewTicker(intervaloVolcado)
	defer t.Stop()
	for {
		select {
		case <-hecho:
			if err := a.Volcar(); err != nil {
				alFallar(err)
			}
			return
		case <-t.C:
			if err := a.Volcar(); err != nil {
				alFallar(err)
			}
		}
	}
}

// Volcar escribe el anillo entero al archivo, de forma atómica.
//
// El anillo ENTERO y no lo nuevo desde la última vez: un archivo que solo
// crece por añadidura tendría que podarse aparte, y esa poda es justamente el
// crecimiento indefinido que la estructura de anillo ya elimina. Reescribir
// 2000 líneas cada minuto en un nodo con actividad es ~500 KB; en un nodo
// tranquilo no se reescribe nada, porque «sucio» lo evita.
func (a *Anillo) Volcar() error {
	a.mu.Lock()
	if !a.sucio {
		a.mu.Unlock()
		return nil
	}
	// Se compone el contenido DENTRO del candado y se escribe FUERA: el
	// fsync de un archivo en una tarjeta puede tardar, y sostener el mutex
	// mientras tanto bloquearía cada petición que quisiera anotar un rechazo.
	// Es el mismo criterio que autenticacion aplica con la derivación de
	// 3.6 s (00_RECTOR.md v1.55.0).
	cuantos := int(min(a.total, int64(Capacidad)))
	orden := make([]Evento, 0, cuantos)
	for i := cuantos - 1; i >= 0; i-- {
		orden = append(orden, a.buf[(a.siguiente-1-i+Capacidad)%Capacidad])
	}
	a.dias.podar(time.Now())
	lineasDias := a.dias.lineas()
	// El total se copia DENTRO del candado, con el resto de la foto. Antes se
	// leía fuera con a.Total(), que es seguro pero toma una segunda foto: entre
	// las dos podía entrar un rechazo, y el archivo salía diciendo un total que
	// no correspondía a los eventos que llevaba debajo.
	total := a.total
	a.sucio = false
	a.mu.Unlock()

	if err := a.guardar(orden, total, lineasDias); err != nil {
		// Volver a marcarlo sucio para que el siguiente intento lo reintente.
		// Sin esto, un fallo transitorio de disco perdería en silencio todo lo
		// anotado hasta entonces.
		a.mu.Lock()
		a.sucio = true
		a.mu.Unlock()
		return err
	}
	return nil
}

// guardar escribe el historial de rechazos.
func (a *Anillo) guardar(eventos []Evento, total int64, dias []string) error {
	return atomico.Escribir(a.ruta, ".seguridad-*", func(w io.Writer) error {
		fmt.Fprintf(w, "# Historial de rechazos — anillo de %d eventos, del más antiguo al más reciente.\n", Capacidad)
		fmt.Fprint(w, "# Un evento por línea, en JSON. NUNCA contiene contraseñas, cookies ni cuerpos (04_SEGURIDAD §6).\n")
		fmt.Fprintf(w, "%s%d\n", marcaTotal, total)
		// El conteo por día va en la CABECERA y no al final: si un corte deja
		// el archivo a medias, lo que sobrevive es lo que menos se puede
		// reconstruir —los días a los que el buffer ya no alcanza—, y los
		// eventos perdidos siguen en el diario (ADR-0037).
		for _, linea := range dias {
			fmt.Fprintln(w, linea)
		}
		enc := json.NewEncoder(w)
		for _, e := range eventos {
			if err := enc.Encode(deEvento(e)); err != nil {
				return err
			}
		}
		return nil
	})
}

// eventoEnDisco es la forma serializada, separada del tipo de dominio a
// propósito: así renombrar un campo de Evento no rompe en silencio los
// archivos ya escritos, y el motivo viaja por su clave estable —no por su
// número, que cambiaría al insertar un valor nuevo en medio del bloque const.
type eventoEnDisco struct {
	Momento string `json:"t"`
	Origen  string `json:"ip"`
	Metodo  string `json:"m"`
	Ruta    string `json:"r"`
	Estado  int    `json:"c"`
	Motivo  string `json:"motivo"`
	Agente  string `json:"ua,omitempty"`
	Cuenta  string `json:"cuenta,omitempty"`
}

func deEvento(e Evento) eventoEnDisco {
	d := eventoEnDisco{
		Momento: e.Momento.Format(time.RFC3339),
		Metodo:  e.Metodo,
		Ruta:    e.Ruta,
		Estado:  e.Estado,
		Motivo:  e.Motivo.String(),
		Agente:  e.Agente,
		Cuenta:  e.Cuenta,
	}
	if e.Origen.IsValid() {
		d.Origen = e.Origen.String()
	}
	// La red NO se guarda: se recalcula al leer. Es derivable de la IP con
	// ClasificarRed, y guardarla permitiría que el archivo y el código
	// discreparan el día que cambie un prefijo.
	return d
}

func (d eventoEnDisco) aEvento() (Evento, error) {
	momento, err := time.Parse(time.RFC3339, d.Momento)
	if err != nil {
		return Evento{}, fmt.Errorf("fecha inválida: %w", err)
	}
	motivo, ok := MotivoDesde(d.Motivo)
	if !ok {
		return Evento{}, fmt.Errorf("motivo %q no reconocido", d.Motivo)
	}
	e := Evento{
		Momento: momento,
		Metodo:  d.Metodo,
		Ruta:    d.Ruta,
		Estado:  d.Estado,
		Motivo:  motivo,
		Agente:  d.Agente,
		Cuenta:  d.Cuenta,
	}
	if d.Origen != "" {
		ip, err := netip.ParseAddr(d.Origen)
		if err != nil {
			return Evento{}, fmt.Errorf("dirección %q inválida: %w", d.Origen, err)
		}
		e.Origen = ip
	}
	e.Red = ClasificarRed(e.Origen)
	return e, nil
}

func truncar(s string, tope int) string {
	if len(s) <= tope {
		return s
	}
	return s[:tope]
}
