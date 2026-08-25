package seguridad

import (
	"bufio"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"nasd/internal/atomico"
)

// Hallazgos — lo que el nodo descubre SOBRE SÍ MISMO.
//
// # EL HUECO QUE ESTO CIERRA, Y POR QUÉ NO CABÍA EN NINGUNA PIEZA ANTERIOR
//
// El anillo de rechazos se alimenta solo de respuestas >= 400 (web/servidor.go,
// conRegistro). Es lo correcto para lo que aquel anillo es —«un rechazo, tal
// como ocurrió»— y deja fuera exactamente el caso más importante:
//
//	GET /.git/config -> 404   entra. Es un sondeo, y no pasó nada.
//	GET /.git/config -> 200   NO entraba. Y es lo único de los dos que
//	                          significa algo grave.
//
// El segundo no se puede meter en Evento sin volver falso su propio contrato:
// «Rechazos en la ventana» contaría cosas que no son rechazos, Motivo tendría
// que inventarse un valor para «no se negó nada», y los filtros, los contadores
// y la cronología —que dicen «rechazo» en el texto— pasarían a mentir. Es el
// mismo argumento con el que ADR-0064 sacó la conexión entrante a un anillo
// propio en vez de forzarla dentro de Motivo.
//
// # POR QUÉ TAMPOCO ES UNA Senal
//
// Una Senal interpreta la CONDUCTA DE QUIEN PIDE: «este origen parece un
// escáner». Esto dice lo contrario, y de otro sujeto:
//
//	«ESTE SERVIDOR respondió con contenido en una ruta que su arquitectura
//	dice que no publica.»
//
// Sigue siendo verdad aunque quien la pidiera fuera el propio responsable con
// curl. Meterlo en el enum de Senal para reaprovechar la maquinaria habría
// dejado una «sospecha sobre el origen» que en realidad no habla del origen, y
// la tabla de orígenes la habría pintado como un cargo contra alguien.
//
// # LO QUE ESTO NO DICE, Y NO ES UN MATIZ
//
// NO dice explotación, ni intrusión, ni compromiso. El servidor no conoce
// ninguna de las tres: conoce que devolvió un 200 donde no debía. Puede ser un
// archivo que alguien dejó donde no iba, una ruta nueva mal acotada o un fallo
// de contención. Las tres exigen ir a mirar, y ninguna se puede afirmar desde
// aquí. Por eso la clase se llama por lo que se observa y no por lo que se
// teme.

// topeHallazgos acota cuántas rutas distintas se conservan.
//
// # POR QUÉ 64 ES HOLGADO Y NO ARRIESGADO
//
// A diferencia de la cuarentena —donde el tope existe porque un extraño puede
// empujar el tamaño— aquí la estructura SOLO crece cuando el nodo se está
// portando mal: cada entrada nueva exige que el servidor haya devuelto
// contenido en una ruta ajena distinta. En un nodo sano esto está VACÍO, y una
// sola entrada ya es motivo para ir a mirar.
//
// 64 rutas × (256 B de ruta acotada más el resto) queda por debajo de 32 KB, y
// llegar a llenarlo significaría que el nodo publica 64 rutas que no debería:
// mucho antes de eso el problema es otro. Lleno, deja de admitir claves nuevas
// y no crece — misma regla que topeCuarentena.
const topeHallazgos = 64

// ClaseHallazgo es QUÉ se observó. Es un enum de una sola clase a propósito:
// no se inventan categorías para las que todavía no hay un caso medido, que es
// la regla que este proyecto aplica a los umbrales y a las opciones de
// configuración.
type ClaseHallazgo uint8

const (
	HallazgoDesconocido ClaseHallazgo = iota
	// RutaAjenaAtendida — una ruta de la familia que este NAS no ejecuta
	// obtuvo una respuesta CON CONTENIDO.
	//
	// El nombre describe el comportamiento del servidor —«atendida»— y no una
	// intención del cliente. «Exploit exitoso» habría sido la etiqueta
	// alarmante y falsa: entregar bytes en una ruta ajena no demuestra que
	// nadie ejecutara nada, y este NAS no ejecuta nada por definición.
	RutaAjenaAtendida
)

func (c ClaseHallazgo) String() string {
	if c == RutaAjenaAtendida {
		return "ruta_ajena_atendida"
	}
	return "desconocido"
}

// ClaseHallazgoDesde recupera una clase de su forma estable. Mismo contrato y
// mismo booleano que MotivoDesde y SenalDesde, y por el mismo motivo: un
// archivo escrito por una versión futura con clases nuevas se leería como si
// todas fueran la primera del enum, en silencio.
func ClaseHallazgoDesde(s string) (ClaseHallazgo, bool) {
	for c := HallazgoDesconocido; c <= RutaAjenaAtendida; c++ {
		if c.String() == s {
			return c, true
		}
	}
	return HallazgoDesconocido, false
}

func (c ClaseHallazgo) Etiqueta() string {
	if c == RutaAjenaAtendida {
		return "Ruta ajena atendida"
	}
	return "Hallazgo desconocido"
}

// Explicacion dice qué significa y, sobre todo, qué NO significa.
//
// La segunda mitad pesa más que en Senal.Explicacion: allí acota un falso
// positivo, y aquí acota una CONCLUSIÓN que quien lea va a sacar solo si no se
// le dice que no puede.
func (c ClaseHallazgo) Explicacion() string {
	if c == RutaAjenaAtendida {
		return "Este servidor devolvió contenido en una ruta de un programa que no ejecuta. " +
			"Es un hecho sobre el NODO, no sobre quien la pidió: hay que ir a mirar qué hay ahí y por qué se sirve. " +
			"NO demuestra que nadie ejecutara nada, ni que se accediera a un repositorio, ni que el nodo esté comprometido; " +
			"demuestra que respondió donde su arquitectura dice que no publica nada."
	}
	return ""
}

// espaciosDeUsuario son los prefijos bajo los que CUALQUIER nombre es
// legítimo, porque lo eligió quien subió el archivo.
//
// # POR QUÉ EXISTE ESTA LISTA Y POR QUÉ NO CONTAMINA A LA SEÑAL
//
// El comentario de RutaDeSoftwareAjeno afirma que «este NAS nunca ha servido
// nada así». Contrastado con web.Rutas(), esa frase es cierta para la tabla de
// rutas del servidor y FALSA para el volumen: nada impide que el responsable
// suba «respaldo.bak» o una carpeta «.git», y entonces
// «/contenido/respaldo.bak → 200» es el NAS funcionando exactamente como debe.
//
// Sin esta lista, el primer archivo así encendería un hallazgo que dice «el
// servidor se expuso» sobre un archivo que su dueño puso a propósito. Una
// alarma que se dispara por lo normal deja de mirarse, y esta es la única del
// panel que no puede permitírselo.
//
// NO SE APLICA A SenalSoftwareAjeno, y es deliberado: aquella dice «merece una
// mirada» sobre un RECHAZO, y ahí ser permisiva sale barato —ADR-0065 ya le
// quitó el énfasis por eso—. Esto afirma que el nodo hizo algo que no debía, y
// una afirmación así se hace con la regla estricta. Cambiar además la
// calibración de una inferencia ya ajustada, sin tráfico medido que lo pida,
// es lo que aquel mismo ADR descartó.
//
// SI MAÑANA SE AÑADE UNA RUTA que sirva archivos del usuario y no se apunta
// aquí, el fallo es un hallazgo de más —ruidoso y visible— y nunca uno de
// menos. Es el lado por el que conviene equivocarse.
var espaciosDeUsuario = []string{
	"/contenido/", "/descargar/", "/abrir/", "/miniatura/", "/ver/",
}

// RespuestaInesperada dice si una respuesta con contenido cayó donde este NAS
// no publica nada.
//
// # LA REGLA, Y POR QUÉ NO ES «estado < 400»
//
// «Todo lo que no sea un rechazo es exposición» convertiría cada portada, cada
// listado y cada descarga en un hallazgo. Hacen falta LAS TRES condiciones:
//
//  1. EL ESTADO ENTREGA CONTENIDO: 200 o 206, y nada más. Una redirección
//     (3xx) NO entrega nada —dice dónde mirar— y este NAS redirige a /acceso
//     constantemente; contarla sería llamar exposición a la puerta cerrada.
//     Un 304 tampoco entrega bytes, y para producirlo haría falta que el
//     cliente ya tuviera el archivo, que es un caso que este nodo no puede
//     generar hoy. Un 204 diría «la ruta existe y no devuelve nada», pero
//     ningún GET de este NAS responde 204: incluirlo sería una regla sin caso,
//     y este proyecto no escribe código por si acaso. Los tres entran el día
//     que exista el caso, no antes.
//  2. LA RUTA NO ES DEL ESPACIO DEL USUARIO. Ver espaciosDeUsuario.
//  3. LA RUTA ES DE LA FAMILIA AJENA, con la MISMA función que decide la
//     señal y la evidencia (RutaDeSoftwareAjeno). Una sola verdad.
//
// # EL MÉTODO NO FILTRA, Y ES A PROPÓSITO
//
// Un «HEAD /.git/config → 200» dice lo mismo que el GET —la ruta existe y se
// sirve— y es además la forma MÁS barata de comprobarlo. Filtrar por método
// dejaría pasar justo la variante que usaría quien está mirando. Se guarda,
// para poder leerlo; no se usa para decidir.
//
// # QUÉ CUESTA EN EL CAMINO DE CADA PETICIÓN
//
// Las tres comprobaciones van de la más barata a la más cara, y a propósito:
// primero dos enteros, luego cinco prefijos sin reservar memoria, y solo al
// final la copia en minúsculas de la ruta. Todo lo que este NAS sirve de
// verdad —listados, descargas, miniaturas, estáticos— sale en la primera o en
// la segunda. Una ruta larga solo llega a la tercera si el servidor le devolvió
// un 200, y a una ruta larga y ajena no se lo devuelve.
func RespuestaInesperada(ruta string, estado int) bool {
	// 200 y 206 escritos como números y no como http.StatusOK: este paquete no
	// importa net/http a propósito —modela hechos, no protocolo— y traerlo
	// entero por dos constantes sería pagar una dependencia por cosmética.
	if estado != 200 && estado != 206 {
		return false
	}
	for _, p := range espaciosDeUsuario {
		if strings.HasPrefix(ruta, p) {
			return false
		}
	}
	return RutaDeSoftwareAjeno(ruta)
}

// Hallazgo es UNA ruta que se atendió y no debería, con lo que se sabe de ella.
//
// # POR QUÉ AGREGA EN VEZ DE GUARDAR CADA SUCESO
//
// Un anillo de sucesos sueltos —como el de rechazos— se llenaría con la misma
// ruta repetida mil veces y taparía la segunda ruta expuesta, que es la que
// aportaría información nueva. Lo que hay que responder aquí es «¿QUÉ está
// publicando el nodo que no debería?», y esa pregunta tiene tantas respuestas
// como rutas distintas, no como peticiones.
type Hallazgo struct {
	Clase   ClaseHallazgo `json:"clase"`
	Metodo  string        `json:"metodo"`
	Ruta    string        `json:"ruta"`
	Estado  int           `json:"estado"`
	Veces   int64         `json:"veces"`
	Primera time.Time     `json:"primera"`
	Ultima  time.Time     `json:"ultima"`
	// UltimoOrigen y UltimaRed son quién la obtuvo la última vez.
	//
	// SOLO EL ÚLTIMO, y no el conjunto: una ruta que se publica está publicada
	// para todo el mundo, así que quién la pidió es secundario. Guardar el
	// conjunto sería, además, una estructura que crece con lo que haga un
	// tercero — la clase de estado que topeCuarentena y topeAgente existen
	// para impedir. Quien quiera la lista completa la tiene en el diario
	// (ADR-0037), que sí registra cada petición con su origen.
	UltimoOrigen netip.Addr `json:"ultimo_origen,omitzero"`
	// UltimaRed no se persiste: se deriva de la dirección al releer, igual que
	// hace Anillo.Anotar. Guardarla congelaría la clasificación del día en que
	// se escribió, y los prefijos IPv6 propios se APRENDEN en cada arranque.
	UltimaRed Red `json:"-"`
}

// Hallazgos guarda lo encontrado, en memoria y respaldado por un archivo en
// /var/lib/nasd/ — misma escritura atómica que los anillos (ADR-0024).
//
// # POR QUÉ SE PERSISTE
//
// Porque es lo único de todo el panel que no se puede volver a observar. Un
// rechazo perdido lo repite el siguiente escáner; una conexión perdida, la
// siguiente visita. Un «el nodo devolvió 200 en /.git/config el martes» que se
// borra al reiniciar —15 arranques en 14 días medidos— desaparece justo la
// clase de hecho que nadie puede reconstruir después.
//
// # ESTADO COMPARTIDO (ADR-0013)
//
// Lleva mutex por lo mismo que el anillo de rechazos: se escribe desde
// conRegistro, es decir desde la goroutine de cada petición, mientras el panel
// lo recorre entero para pintarse.
type Hallazgos struct {
	mu     sync.Mutex
	ruta   string
	porVia map[claveSonda]*Hallazgo
	// total son los sucesos vistos desde siempre, no las rutas distintas. Se
	// publica por lo mismo que en los dos anillos: una estructura acotada no
	// debe leerse como «esto es todo lo que ha pasado».
	total int64
	sucio bool
}

// CargarHallazgos lee lo que hubiera. Devuelve una estructura USABLE aunque el
// archivo esté ilegible, igual que CargarAnillo y CargarCuarentena: quedarse
// sin esta pieza cuesta el historial de hallazgos, no el servicio.
func CargarHallazgos(ruta string) (*Hallazgos, error) {
	h := &Hallazgos{ruta: ruta, porVia: make(map[claveSonda]*Hallazgo)}
	if err := h.releer(); err != nil {
		return h, err
	}
	return h, nil
}

func (h *Hallazgos) releer() error {
	f, err := os.Open(h.ruta)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("abrir los hallazgos %q: %w", h.ruta, err)
	}
	defer f.Close()

	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 0, 8*1024), 8*1024)
	for s.Scan() {
		linea := strings.TrimSpace(s.Text())
		if linea == "" || strings.HasPrefix(linea, "#") {
			if n, ok := totalDeCabecera(linea); ok {
				h.total = n
			}
			continue
		}
		var x Hallazgo
		if err := json.Unmarshal([]byte(linea), &x); err != nil {
			// Una línea ilegible no invalida las demás, igual que en la
			// cuarentena: es un registro de observación, no un libro de
			// cuentas.
			continue
		}
		if x.Ruta == "" || len(h.porVia) >= topeHallazgos {
			continue
		}
		x.UltimaRed = ClasificarRed(x.UltimoOrigen)
		h.porVia[claveSonda{metodo: x.Metodo, ruta: x.Ruta, estado: x.Estado}] = &x
	}
	// El máximo de los dos, por lo mismo que en Anillo.releer: un archivo sin
	// la marca se cae a lo único que se puede afirmar, y un total menor que lo
	// guardado sería un archivo inconsistente.
	h.total = max(h.total, int64(len(h.porVia)))
	return s.Err()
}

// Anotar registra que una ruta ajena se atendió. Corre en el camino de la
// petición, así que no toca disco: escribe en un mapa acotado y sale.
func (h *Hallazgos) Anotar(x Hallazgo) {
	x.Ruta = truncar(x.Ruta, topeRuta)
	if x.Ruta == "" {
		return
	}
	if x.UltimaRed == RedDesconocida {
		x.UltimaRed = ClasificarRed(x.UltimoOrigen)
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	h.total++
	h.sucio = true
	k := claveSonda{metodo: x.Metodo, ruta: x.Ruta, estado: x.Estado}
	if viejo, hay := h.porVia[k]; hay {
		viejo.Veces++
		viejo.Ultima = x.Ultima
		viejo.UltimoOrigen, viejo.UltimaRed = x.UltimoOrigen, x.UltimaRed
		return
	}
	if len(h.porVia) >= topeHallazgos {
		// Lleno se dejan de admitir claves NUEVAS, pero el total ya ha subido:
		// la cifra dice que hubo más de lo que se enseña, en vez de callarlo.
		return
	}
	x.Veces = 1
	x.Primera = x.Ultima
	h.porVia[k] = &x
}

// Todos devuelve lo encontrado, de lo más reciente a lo más antiguo.
//
// NO ADMITE VENTANA, al revés que los anillos, y es deliberado: un hallazgo no
// caduca porque pase el tiempo. Que el nodo publicara /.git/config hace tres
// días sigue siendo verdad hoy si nadie ha ido a mirar, y esconderlo tras el
// filtro de «últimas 24 horas» sería apagar la única alarma del panel dejando
// el problema en pie. Por eso el panel lo pinta fuera de los filtros, junto a
// lo apartado y lo bloqueado.
func (h *Hallazgos) Todos() []Hallazgo {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]Hallazgo, 0, len(h.porVia))
	for _, x := range h.porVia {
		out = append(out, *x)
	}
	slices.SortFunc(out, func(x, y Hallazgo) int {
		if c := y.Ultima.Compare(x.Ultima); c != 0 {
			return c
		}
		// Desempate DETERMINISTA, por lo mismo que en PorOrigen: sin él, dos
		// hallazgos del mismo instante bailan entre recargas.
		if c := strings.Compare(x.Ruta, y.Ruta); c != 0 {
			return c
		}
		if c := strings.Compare(x.Metodo, y.Metodo); c != 0 {
			return c
		}
		return cmp.Compare(x.Estado, y.Estado)
	})
	return out
}

// Total son los sucesos vistos desde siempre, no las rutas distintas.
func (h *Hallazgos) Total() int64 {
	if h == nil {
		return 0
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.total
}

// Mantener vuelca cada minuto y una última vez al cerrarse el canal. Misma
// forma y mismo canal de parada que los anillos y la cuarentena (main.go).
func (h *Hallazgos) Mantener(hecho <-chan struct{}, alFallar func(error)) {
	t := time.NewTicker(intervaloVolcado)
	defer t.Stop()
	for {
		select {
		case <-hecho:
			if err := h.Volcar(); err != nil {
				alFallar(err)
			}
			return
		case <-t.C:
			if err := h.Volcar(); err != nil {
				alFallar(err)
			}
		}
	}
}

// Volcar escribe los hallazgos enteros, de forma atómica. El contenido se
// compone DENTRO del candado y se escribe FUERA, igual que en los anillos.
func (h *Hallazgos) Volcar() error {
	h.mu.Lock()
	if !h.sucio {
		h.mu.Unlock()
		return nil
	}
	orden := make([]Hallazgo, 0, len(h.porVia))
	for _, x := range h.porVia {
		orden = append(orden, *x)
	}
	total := h.total
	h.sucio = false
	h.mu.Unlock()

	slices.SortFunc(orden, func(x, y Hallazgo) int { return x.Primera.Compare(y.Primera) })

	err := atomico.Escribir(h.ruta, ".hallazgos-*", func(w io.Writer) error {
		fmt.Fprint(w, "# Respuestas inesperadas — una ruta por línea, en JSON.\n")
		fmt.Fprint(w, "# Esto habla del NODO, no de quien pidió: dice dónde respondió con contenido\n")
		fmt.Fprint(w, "# una ruta que su arquitectura no publica. No afirma explotación ni compromiso.\n")
		fmt.Fprintf(w, "%s%d\n", marcaTotal, total)
		enc := json.NewEncoder(w)
		for _, x := range orden {
			if err := enc.Encode(x); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		// Lo de memoria sigue siendo lo bueno y hay que reintentarlo en el
		// siguiente ciclo, igual que en la cuarentena y los anillos.
		h.mu.Lock()
		h.sucio = true
		h.mu.Unlock()
	}
	return err
}

// MarshalJSON y UnmarshalJSON de ClaseHallazgo, por el mismo motivo que en
// Senal y Alcance: el archivo guarda la clave estable y no el número del enum,
// para que insertar una clase nueva en medio no reinterprete lo ya escrito.
func (c ClaseHallazgo) MarshalJSON() ([]byte, error) { return json.Marshal(c.String()) }

func (c *ClaseHallazgo) UnmarshalJSON(b []byte) error {
	var clave string
	if err := json.Unmarshal(b, &clave); err != nil {
		return err
	}
	v, ok := ClaseHallazgoDesde(clave)
	if !ok {
		return fmt.Errorf("clase de hallazgo desconocida: %q", clave)
	}
	*c = v
	return nil
}
