package seguridad

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"nasd/internal/atomico"
)

// Lista — el bloqueo que decide una PERSONA, con su motivo escrito.
//
// # EN QUÉ SE DIFERENCIA DE LA CUARENTENA
//
// La cuarentena actúa sola, por conducta observada, sobre UNA dirección y
// caduca en 24 h. Esto es lo contrario en las cuatro cosas: lo pone alguien,
// puede ser por reputación, elige el alcance y dura lo que se decida. Son dos
// piezas y no una porque justificarlas es distinto — y porque una decisión
// tomada por una persona necesita algo que un automatismo no puede dar: el
// porqué, escrito.
//
// # EL MOTIVO ES OBLIGATORIO, Y NO ES BUROCRACIA
//
// El caso que originó esto lo dejó escrito el propio responsable al preparar
// el bloqueo de AS44382: «si se bloquea AS44382 y no a DRIFTNET, el motivo
// tiene que quedar escrito o dentro de dos sesiones nadie sabrá por qué».
// DRIFTNET es quien SÍ barrió el nodo —dos veces, con veinte direcciones— y
// sigue sin bloquearse a propósito; AS44382 hizo un paquete, una conexión y un
// 401. Una lista donde esas dos decisiones conviven sin explicación es una
// lista que nadie se atreve a tocar dentro de seis meses.
//
// # POR QUÉ TRAMOS Y NO PREFIJOS
//
// Porque la unidad de bloqueo que sirve contra estos actores no es la
// dirección —está medido: el /48 de AS44382 cubría 1 de sus 11 rangos— y
// porque la base de operadores publica TRAMOS. Un prefijo escrito a mano sigue
// valiendo: se convierte a su tramo al entrar, sin perder nada.

// Errores de las barandillas. Son valores y no textos para que la capa web
// pueda decir qué pasó sin comparar cadenas.
var (
	ErrSinMotivo   = errors.New("un bloqueo sin motivo escrito no se acepta")
	ErrSinAlcance  = errors.New("no hay nada que bloquear")
	ErrTocaLaCasa  = errors.New("ese alcance incluye la red de casa, el túnel o el propio nodo")
	ErrTeDejaFuera = errors.New("ese alcance incluye la dirección desde la que está mirando")
	ErrDemasiadas  = errors.New("la lista está llena")
	// ErrDemasiadosTramos protege la propiedad de FOTO del alcance «operador»:
	// antes que guardar una entrada recortada —que bloquearía menos de lo que
	// se firmó— o una que no se pueda releer, se niega y se dice por qué.
	ErrDemasiadosTramos = errors.New("ese alcance tiene demasiados tramos para guardarlo entero")
	ErrNoSeEncontro     = errors.New("esa entrada ya no está en la lista")
)

// topeLista acota cuántas entradas caben. A diferencia del de la cuarentena,
// este no lo empuja un extraño sino el responsable, así que es holgado: existe
// para que un archivo corrupto no se convierta en memoria sin fin.
const topeLista = 512

// topeLineaLista es lo más larga que puede ser la línea de UNA entrada.
//
// # EL DEFECTO MEDIDO QUE ESTA CONSTANTE CIERRA
//
// El 2026-08-24, arrancando el nodo, el diario decía:
//
//	"la lista de bloqueos no se pudo leer; se empieza vacía"
//	error: "bufio.Scanner: token too long"
//
// La causa, medida sobre el archivo real: la entrada de «AS13335 ·
// CLOUDFLARENET · US» son **1517 tramos** en una sola línea de **102 572
// bytes**, y bufio.Scanner sin configurar admite como mucho 64 KB. El
// resultado NO era un error visible en el panel: era un bloqueo que la persona
// había creado, que el archivo conservaba entero, y que **no bloqueaba nada**
// desde el primer reinicio. Silencioso, que es el modo de fallo que este
// proyecto persigue.
//
// Peor: el Scanner se DETIENE en la línea larga, así que las entradas que
// vinieran detrás se perderían también. Aquella era la última por casualidad.
//
// # POR QUÉ 1 MiB Y POR QUÉ NO SE TRUNCA LA ENTRADA
//
// Truncar los tramos era la otra salida, y rompe la propiedad que ADR-0070
// declaró para el alcance «operador»: la entrada es una FOTO de lo que la
// persona decidió. Una foto recortada en silencio bloquea MENOS de lo que se
// firmó, que es peor que no bloquear.
//
// Así que se mide al revés: el lector tiene que poder leer lo que el escritor
// escribió. 1 MiB cubre unos 11 000 tramos —siete veces la mayor entrada vista
// en este nodo— y se paga UNA vez al arrancar, nunca al servir. El buffer
// arranca en 64 KB y solo crece si hace falta, así que el caso normal no
// reserva más de lo que ya reservaba.
//
// Y para que el par no pueda volver a desajustarse, Anadir RECHAZA una entrada
// que no cupiera aquí: un bloqueo o entra y funciona, o se niega diciendo por
// qué. Lo que no puede volver a pasar es que se acepte y desaparezca.
const topeLineaLista = 1 << 20

// Alcance es qué se eligió bloquear. Se guarda además de los tramos porque es
// la INTENCIÓN, y los tramos son su consecuencia: al releer la lista dentro de
// un año, «el operador entero» explica once tramos que si no parecerían
// arbitrarios.
type Alcance uint8

const (
	AlcanceDireccion Alcance = iota
	AlcanceRango
	AlcanceOperador
)

func (a Alcance) String() string {
	switch a {
	case AlcanceRango:
		return "rango"
	case AlcanceOperador:
		return "operador"
	}
	return "direccion"
}

func (a Alcance) Etiqueta() string {
	switch a {
	case AlcanceRango:
		return "Rango"
	case AlcanceOperador:
		return "Operador"
	}
	return "Dirección"
}

func AlcanceDesde(s string) (Alcance, bool) {
	for x := AlcanceDireccion; x <= AlcanceOperador; x++ {
		if x.String() == s {
			return x, true
		}
	}
	return AlcanceDireccion, false
}

// Tramo es un intervalo cerrado de direcciones.
type Tramo struct {
	Desde netip.Addr `json:"desde"`
	Hasta netip.Addr `json:"hasta"`
}

// TramoDe convierte un prefijo en su tramo.
//
// Trabaja SIEMPRE en 16 bytes, también para IPv4: es la misma normalización
// que usa la base de operadores, y sin ella un /24 de IPv4 y un /120 de IPv6
// —que son lo mismo— no se compararían igual.
func TramoDe(p netip.Prefix) Tramo {
	p = p.Masked()
	primera := p.Addr().As16()
	bits := p.Bits()
	if p.Addr().Is4() {
		bits += 96
	}
	ultima := primera
	for i := bits; i < 128; i++ {
		ultima[i/8] |= 1 << (7 - i%8)
	}
	return Tramo{
		Desde: netip.AddrFrom16(primera).Unmap(),
		Hasta: netip.AddrFrom16(ultima).Unmap(),
	}
}

// Contiene dice si la dirección cae dentro del tramo.
func (t Tramo) Contiene(ip netip.Addr) bool {
	if !t.Desde.IsValid() || !t.Hasta.IsValid() || !ip.IsValid() {
		return false
	}
	v := netip.AddrFrom16(ip.As16())
	return v.Compare(netip.AddrFrom16(t.Desde.As16())) >= 0 &&
		v.Compare(netip.AddrFrom16(t.Hasta.As16())) <= 0
}

// Solapa dice si dos tramos comparten al menos una dirección.
func (t Tramo) Solapa(o Tramo) bool {
	if !t.Desde.IsValid() || !o.Desde.IsValid() {
		return false
	}
	a1, a2 := netip.AddrFrom16(t.Desde.As16()), netip.AddrFrom16(t.Hasta.As16())
	b1, b2 := netip.AddrFrom16(o.Desde.As16()), netip.AddrFrom16(o.Hasta.As16())
	return a1.Compare(b2) <= 0 && b1.Compare(a2) <= 0
}

func (t Tramo) String() string {
	if t.Desde == t.Hasta {
		return t.Desde.String()
	}
	return t.Desde.String() + " – " + t.Hasta.String()
}

// Entrada es UNA decisión de bloqueo. Puede cubrir varios tramos —el alcance
// «operador» son los once de AS44382— y sigue siendo una sola decisión, con un
// solo motivo y una sola cuenta de aciertos.
type Entrada struct {
	// ID identifica la entrada para poder retirarla. Sale del instante del
	// alta en base 36: es estable, corto y no hace falta un generador de
	// aleatorios para algo que solo tiene que distinguir entre entradas que
	// una persona crea a mano.
	ID       string    `json:"id"`
	Alcance  Alcance   `json:"alcance"`
	Etiqueta string    `json:"etiqueta"`
	Tramos   []Tramo   `json:"tramos"`
	Motivo   string    `json:"motivo"`
	Autor    string    `json:"autor"`
	Alta     time.Time `json:"alta"`
	// Caduca en cero significa PERMANENTE, y es la única forma de que lo sea:
	// quien la crea tiene que elegirlo. La reputación de un rango envejece, y
	// una lista que no caduca envejece con ella en silencio.
	Caduca time.Time `json:"caduca,omitzero"`
	// BaseGeo es la fecha de la base con la que se compuso el alcance.
	//
	// SE GUARDA PORQUE EL ALCANCE ES UNA FOTO, NO UNA REGLA VIVA: si mañana el
	// operador anuncia un tramo nuevo, esta entrada NO lo cubre. Una regla viva
	// seguiría al operador, pero cambiaría lo bloqueado sin que nadie lo
	// decidiera. Con la fecha delante, el panel puede decir que la base es más
	// nueva que la entrada y ofrecer revisarla.
	BaseGeo time.Time `json:"base_geo,omitzero"`
	// UltimoFrenado es cuándo sirvió por última vez.
	//
	// Existe para la marca de novedades: sin él, «un bloqueo suyo frenó algo»
	// no se puede distinguir de «este bloqueo frenó algo alguna vez», y la
	// marca se quedaría encendida para siempre desde el primer acierto.
	UltimoFrenado time.Time `json:"ultimo_frenado,omitzero"`
	// Frenados son las conexiones que esta entrada ha cerrado DE VERDAD, no las
	// veces que una dirección coincidió con sus tramos. Ver Cubre y puerta.go:
	// solo sube tras un net.Conn.Close() que devolvió nil.
	//
	// Es la cifra que permite RETIRAR lo que no sirve. Sin ella, una lista solo
	// crece: nadie quita una regla si no puede saber si alguna vez hizo algo.
	//
	// NO SON PETICIONES: después del cierre no hay petición que contar.
	Frenados int64 `json:"frenados"`
}

// Vigente dice si la entrada sigue en pie. Sin caducidad, siempre.
func (e Entrada) Vigente(ahora time.Time) bool {
	return e.Caduca.IsZero() || ahora.Before(e.Caduca)
}

// Contiene dice si alguno de sus tramos cubre la dirección.
func (e Entrada) Contiene(ip netip.Addr) bool {
	for _, t := range e.Tramos {
		if t.Contiene(ip) {
			return true
		}
	}
	return false
}

// Cubre describe en palabras lo que bloquea, para la pantalla de confirmación.
func (e Entrada) Cubre() string {
	partes := make([]string, 0, len(e.Tramos))
	for _, t := range e.Tramos {
		partes = append(partes, t.String())
	}
	return strings.Join(partes, ", ")
}

// Lista guarda los bloqueos manuales, en memoria y respaldados por un archivo
// en /var/lib/nasd/ — misma escritura atómica que los anillos (ADR-0024).
type Lista struct {
	mu       sync.Mutex
	ruta     string
	entradas []Entrada
	sucio    bool
}

func CargarLista(ruta string) (*Lista, error) {
	l := &Lista{ruta: ruta}
	if err := l.releer(); err != nil {
		return l, err
	}
	return l, nil
}

func (l *Lista) releer() error {
	f, err := os.Open(l.ruta)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("abrir la lista de bloqueos %q: %w", l.ruta, err)
	}
	defer f.Close()

	s := bufio.NewScanner(f)
	// EL BUFFER SE CONFIGURA, y no es una precaución teórica: sin esta línea,
	// la entrada de 1517 tramos de este nodo hacía fallar la lectura entera.
	// Ver topeLineaLista.
	s.Buffer(make([]byte, 0, 64*1024), topeLineaLista)
	for s.Scan() {
		linea := s.Bytes()
		if len(linea) == 0 || linea[0] == '#' {
			continue
		}
		var e Entrada
		if err := json.Unmarshal(linea, &e); err != nil {
			continue
		}
		if len(e.Tramos) == 0 || e.ID == "" {
			continue
		}
		l.entradas = append(l.entradas, e)
		if len(l.entradas) >= topeLista {
			break
		}
	}
	if err := s.Err(); err != nil {
		// SE DICE CUÁNTAS SOBREVIVIERON, y no solo que hubo un error.
		//
		// El Scanner se detiene en la línea que no puede leer, así que lo
		// cargado hasta ahí SIGUE VIGENTE y lo de detrás se ha perdido. Quien
		// lea el diario tiene que poder distinguir «no hay ningún bloqueo
		// puesto» de «hay ocho puestos y falta al menos uno», que son dos
		// situaciones opuestas. El mensaje anterior —«se empieza vacía»— era
		// literalmente falso en el segundo caso.
		return fmt.Errorf("lista de bloqueos %q: %w (quedan vigentes las %d entradas leídas antes del fallo)",
			l.ruta, err, len(l.entradas))
	}
	return nil
}

// Anadir mete una entrada tras pasar las barandillas.
//
// # LAS BARANDILLAS SON EL CONTROL, NO UN ADORNO
//
// Un bloqueo mal puesto no da un error: deja fuera a alguien, y quizá a quien
// lo puso. Por eso se comprueban AQUÍ —donde entra todo lo que llega a la
// lista— y no en la capa web, que es una de las formas de llegar y podría no
// ser la única mañana.
//
// quienPide es la dirección desde la que se está pidiendo el bloqueo. Puede
// ser inválida —no siempre se conoce— y entonces esa barandilla no aplica: la
// alternativa sería negar el bloqueo por no saber desde dónde se pide, que es
// peor.
func (l *Lista) Anadir(e Entrada, quienPide netip.Addr, ahora time.Time) (Entrada, error) {
	if strings.TrimSpace(e.Motivo) == "" {
		return Entrada{}, ErrSinMotivo
	}
	e.Tramos = slices.DeleteFunc(e.Tramos, func(t Tramo) bool {
		return !t.Desde.IsValid() || !t.Hasta.IsValid()
	})
	if len(e.Tramos) == 0 {
		return Entrada{}, ErrSinAlcance
	}
	for _, t := range e.Tramos {
		if tocaLaCasa(t) {
			return Entrada{}, ErrTocaLaCasa
		}
		if quienPide.IsValid() && t.Contiene(quienPide) {
			return Entrada{}, ErrTeDejaFuera
		}
	}

	// QUE QUEPA AL RELEERSE, comprobado sobre la serialización REAL y no sobre
	// una estimación de cuánto ocupa un tramo: si el formato cambia, esta
	// comprobación cambia con él sola. Se paga un json.Marshal por bloqueo
	// creado a mano, que es una acción de una persona, no del camino de una
	// petición.
	if linea, err := json.Marshal(e); err == nil && len(linea) > topeLineaLista {
		return Entrada{}, ErrDemasiadosTramos
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.entradas) >= topeLista {
		return Entrada{}, ErrDemasiadas
	}
	e.Alta = ahora
	e.ID = strconv.FormatInt(ahora.UnixNano(), 36)
	e.Motivo = strings.TrimSpace(e.Motivo)
	e.Frenados = 0
	l.entradas = append(l.entradas, e)
	l.sucio = true
	return e, nil
}

// tocaLaCasa dice si un tramo alcanza alguna red que NUNCA debe bloquearse.
//
// Se comprueba por SOLAPE con las redes de dentro, no preguntando por una
// dirección suelta: un tramo enorme que contenga 192.168.1.0/24 no tiene por
// qué contener la dirección concreta del PC de casa, y aun así dejaría fuera a
// media casa el día que el router cambie una IP.
//
// Incluye los prefijos IPv6 APRENDIDOS al arrancar, no solo los literales: es
// la misma lección que costó una hora en ClasificarRed —el iPhone de casa,
// hablando IPv6 nativo, contado como Internet—, y aquí el precio de repetirla
// sería bloquear la casa entera.
func tocaLaCasa(t Tramo) bool {
	dentro := []netip.Prefix{
		prefijoLAN,
		prefijoTunel,
		netip.MustParsePrefix("127.0.0.0/8"),
		netip.MustParsePrefix("::1/128"),
		netip.MustParsePrefix("fe80::/10"),
	}
	dentro = append(dentro, prefijosPropios...)
	for _, p := range dentro {
		if t.Solapa(TramoDe(p)) {
			return true
		}
	}
	return false
}

// Cubre dice si alguna entrada vigente alcanza esta dirección, y CUÁL.
//
// # NO CUENTA NADA — misma corrección que en Cuarentena.Cubre
//
// Esto se llamaba Bloquea y sumaba el frenado en el acto, antes de que nadie
// hubiera cerrado la conexión. Ver el porqué entero allí; aquí basta con la
// consecuencia: contar es AnotarCierre, y a esa no se llega sin un Close() que
// haya devuelto nil.
//
// DEVUELVE EL ID y no solo un booleano porque la cifra que hay que subir
// después vive en UNA entrada concreta. Sin el identificador, quien contara
// tendría que volver a recorrer la lista y podría encontrar otra distinta si
// entre medias se retiró alguna — y entonces el acierto se le apuntaría a la
// entrada equivocada.
//
// GANA LA PRIMERA que la contenga, en orden de alta, y no la más específica:
// dos entradas que se solapan son dos decisiones de la misma persona sobre la
// misma dirección, y elegir «la mejor» exigiría una regla que nadie ha pedido.
// La primera es determinista y se puede explicar en una línea.
func (l *Lista) Cubre(ip netip.Addr, ahora time.Time) (string, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for i := range l.entradas {
		if l.entradas[i].Vigente(ahora) && l.entradas[i].Contiene(ip) {
			return l.entradas[i].ID, true
		}
	}
	return "", false
}

// AnotarCierre cuenta UNA conexión que ya se cerró de verdad, en la entrada
// que la cubrió.
//
// Que la entrada se haya retirado entre la decisión y el cierre no es un error
// y no se avisa, por lo mismo que en Cuarentena.AnotarCierre: el cierre
// ocurrió y ya no hay a quién apuntárselo.
func (l *Lista) AnotarCierre(id string, ahora time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	i := slices.IndexFunc(l.entradas, func(e Entrada) bool { return e.ID == id })
	if i < 0 {
		return
	}
	l.entradas[i].Frenados++
	l.entradas[i].UltimoFrenado = ahora
	l.sucio = true
}

// Vigentes devuelve las entradas en pie, de la más reciente a la más antigua.
func (l *Lista) Vigentes(ahora time.Time) []Entrada {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Entrada, 0, len(l.entradas))
	for _, e := range l.entradas {
		if e.Vigente(ahora) {
			out = append(out, e)
		}
	}
	slices.SortFunc(out, func(x, y Entrada) int { return y.Alta.Compare(x.Alta) })
	return out
}

// Retirar quita una entrada por su identificador.
func (l *Lista) Retirar(id string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	i := slices.IndexFunc(l.entradas, func(e Entrada) bool { return e.ID == id })
	if i < 0 {
		return ErrNoSeEncontro
	}
	l.entradas = slices.Delete(l.entradas, i, i+1)
	l.sucio = true
	return nil
}

// Mantener vuelca cada minuto y una última vez al cerrarse el canal.
//
// PURGA LAS CADUCADAS AL VOLCAR, y no en una tarea aparte: una entrada
// caducada ya no bloquea —lo decide Vigente— así que quitarla es solo higiene
// del archivo, y hacerlo donde ya se reescribe entero no añade un modo de
// fallo nuevo.
func (l *Lista) Mantener(hecho <-chan struct{}, alFallar func(error)) {
	t := time.NewTicker(intervaloVolcado)
	defer t.Stop()
	for {
		select {
		case <-hecho:
			if err := l.Volcar(); err != nil {
				alFallar(err)
			}
			return
		case ahora := <-t.C:
			l.purgar(ahora)
			if err := l.Volcar(); err != nil {
				alFallar(err)
			}
		}
	}
}

func (l *Lista) purgar(ahora time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	antes := len(l.entradas)
	l.entradas = slices.DeleteFunc(l.entradas, func(e Entrada) bool { return !e.Vigente(ahora) })
	if len(l.entradas) != antes {
		l.sucio = true
	}
}

// Volcar escribe la lista entera, de forma atómica.
func (l *Lista) Volcar() error {
	l.mu.Lock()
	if !l.sucio {
		l.mu.Unlock()
		return nil
	}
	orden := slices.Clone(l.entradas)
	l.sucio = false
	l.mu.Unlock()

	err := atomico.Escribir(l.ruta, ".lista-*", func(w io.Writer) error {
		fmt.Fprint(w, "# Bloqueos puestos a mano — una decisión por línea, en JSON.\n")
		fmt.Fprint(w, "# Cada una lleva su motivo escrito, quién la puso y cuántas veces ha servido.\n")
		enc := json.NewEncoder(w)
		for _, e := range orden {
			if err := enc.Encode(e); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		l.mu.Lock()
		l.sucio = true
		l.mu.Unlock()
	}
	return err
}

// MarshalJSON y UnmarshalJSON de Alcance, por el mismo motivo que en Senal: el
// archivo guarda la clave estable y no el número del enum.
func (a Alcance) MarshalJSON() ([]byte, error) { return json.Marshal(a.String()) }

func (a *Alcance) UnmarshalJSON(b []byte) error {
	var clave string
	if err := json.Unmarshal(b, &clave); err != nil {
		return err
	}
	v, ok := AlcanceDesde(clave)
	if !ok {
		return fmt.Errorf("alcance desconocido: %q", clave)
	}
	*a = v
	return nil
}
