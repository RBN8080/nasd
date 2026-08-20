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
	ErrSinMotivo    = errors.New("un bloqueo sin motivo escrito no se acepta")
	ErrSinAlcance   = errors.New("no hay nada que bloquear")
	ErrTocaLaCasa   = errors.New("ese alcance incluye la red de casa, el túnel o el propio nodo")
	ErrTeDejaFuera  = errors.New("ese alcance incluye la dirección desde la que está mirando")
	ErrDemasiadas   = errors.New("la lista está llena")
	ErrNoSeEncontro = errors.New("esa entrada ya no está en la lista")
)

// topeLista acota cuántas entradas caben. A diferencia del de la cuarentena,
// este no lo empuja un extraño sino el responsable, así que es holgado: existe
// para que un archivo corrupto no se convierta en memoria sin fin.
const topeLista = 512

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
	// Frenados son las conexiones que esta entrada ha cerrado.
	//
	// Es la cifra que permite RETIRAR lo que no sirve. Sin ella, una lista solo
	// crece: nadie quita una regla si no puede saber si alguna vez hizo algo.
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
	return s.Err()
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

// Bloquea dice si a esta dirección se le cierra la puerta por una entrada de
// la lista, y lo cuenta en la entrada que lo hizo.
func (l *Lista) Bloquea(ip netip.Addr, ahora time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for i := range l.entradas {
		if !l.entradas[i].Vigente(ahora) {
			continue
		}
		if l.entradas[i].Contiene(ip) {
			l.entradas[i].Frenados++
			l.entradas[i].UltimoFrenado = ahora
			l.sucio = true
			return true
		}
	}
	return false
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

	err := escribirAtomico(l.ruta, ".lista-*", func(w io.Writer) error {
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
