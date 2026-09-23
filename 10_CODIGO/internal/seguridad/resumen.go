package seguridad

import (
	"cmp"
	"net/netip"
	"path"
	"slices"
	"strings"
	"time"
)

// Agregación y señales — etapa 2 del panel.
//
// # LA REGLA QUE GOBIERNA ESTE ARCHIVO
//
// Un Evento es un HECHO: el servidor negó esto, por esta regla, a este
// origen. Una Senal es una INFERENCIA sobre un conjunto de hechos, y puede
// equivocarse. No se guardan juntas, no se calculan juntas y no se pintan
// igual, porque confundirlas es exactamente lo que el responsable prohibió:
// «no clasifiques automáticamente todo rechazo como ataque».
//
// Las señales se derivan AL MIRAR y nunca se persisten. Consecuencia
// deliberada: afinar un umbral no reescribe la historia, porque la historia
// solo contiene hechos. Si las señales vivieran en el archivo, cambiar un
// número obligaría a reinterpretar —o peor, a creer— lo ya escrito.

// Senal es una sospecha derivada, con su porqué y su tasa de error propia.
type Senal uint8

const (
	SenalExploracion Senal = iota

	SenalFuerzaBruta

	SenalSoftwareAjeno

	SenalOperadorDesconocido
)

func (s Senal) String() string {
	switch s {
	case SenalExploracion:
		return "exploracion"
	case SenalFuerzaBruta:
		return "fuerza_bruta"
	case SenalSoftwareAjeno:
		return "software_ajeno"
	case SenalOperadorDesconocido:
		return "operador_desconocido"
	}
	return "desconocida"
}

func SenalDesde(s string) (Senal, bool) {
	for x := SenalExploracion; x <= SenalOperadorDesconocido; x++ {
		if x.String() == s {
			return x, true
		}
	}
	return SenalExploracion, false
}

func (s Senal) Etiqueta() string {
	switch s {
	case SenalExploracion:
		return "Exploración de rutas"
	case SenalFuerzaBruta:
		return "Contraseñas repetidas"
	case SenalSoftwareAjeno:
		return "Sondeo de software ajeno"
	case SenalOperadorDesconocido:
		return "Operador sin acceso previo"
	}
	return "Señal desconocida"
}

func (s Senal) Explicacion() string {
	switch s {
	case SenalExploracion:
		return "Este origen pidió al menos " + itoa(umbralExploracion) +
			" rutas distintas que no existen. Puede ser un escáner recorriendo un diccionario. " +
			"También lo produce un marcador antiguo, un cliente mal configurado o un enlace roto que se reintenta."
	case SenalFuerzaBruta:
		return "Este origen falló la contraseña al menos " + itoa(umbralFuerzaBruta) +
			" veces. El limitador ya lo frena a los " + itoa(5) + " intentos por su cuenta. " +
			"También lo produce alguien de casa que no recuerda su contraseña."
	case SenalSoftwareAjeno:
		return "Se pidieron rutas de programas que este NAS no ejecuta (PHP, paneles de administración ajenos, archivos de configuración). " +
			"Aquí no hay ninguno, así que no hay nada que comprometer por esa vía; " +
			"es el rastreo indiscriminado que recibe cualquier dirección pública, no algo dirigido a este nodo."
	case SenalOperadorDesconocido:
		return "Este origen pidió algo, se le negó, y viene de un operador desde el que NADIE ha iniciado sesión jamás en este NAS. " +
			"Los escáneres que ha recibido este nodo venían todos de empresas de alojamiento; las personas que lo usan desde fuera vienen de operadores móviles desde los que ya han entrado. " +
			"También lo produce alguien de la familia estrenando una red nueva: la primera vez, su operador todavía no consta, y basta con soltarlo desde este panel."
	}
	return ""
}

func (s Senal) Destacar() bool { return s != SenalSoftwareAjeno }

// Umbrales de las señales. Cada uno con su porqué, porque un número sin
// justificar se cambia por gusto y entonces la señal deja de significar nada.
const (
	// Ocho rutas DISTINTAS e inexistentes. Alguien navegando con enlaces
	// viejos falla una o dos; ocho distintas ya no es navegar.
	umbralExploracion = 8
	// Cinco: el MISMO número que maxIntentosFallidos del limitador de
	// sesion.go. Deliberadamente igual y no un valor propio: si el panel
	// avisara antes o después de lo que el servidor actúa, las dos cifras se
	// contradirían y habría que explicar cuál manda.
	umbralFuerzaBruta = 5
)

func RutaDeSoftwareAjeno(ruta string) bool {
	r := strings.ToLower(ruta)
	// Extensiones de programas que este binario no ejecuta.
	switch strings.ToLower(path.Ext(r)) {
	case ".php", ".asp", ".aspx", ".jsp", ".cgi", ".pl", ".sql", ".bak":
		return true
	}
	// Archivos y directorios que aquí no se publican nunca. Van con la barra
	// o al final para no marcar una carpeta legítima que empiece igual.
	for _, s := range []string{"/.env", "/.git", "/.aws", "/.ssh", "/wp-admin", "/wp-includes"} {
		if r == s || strings.HasPrefix(r, s+"/") || strings.Contains(r, s+"/") {
			return true
		}
	}
	return false
}

type Filtro struct {
	Desde  time.Time
	Motivo *Motivo
	Red    *Red
}

func (f Filtro) admite(e Evento) bool {
	if f.Motivo != nil && e.Motivo != *f.Motivo {
		return false
	}
	if f.Red != nil && e.Red != *f.Red {
		return false
	}
	return true
}

// Filtrados devuelve los eventos que pasan el filtro, del más reciente al más
// antiguo.
func (a *Anillo) Filtrados(f Filtro) []Evento {
	crudos := a.Desde(f.Desde)
	out := make([]Evento, 0, len(crudos))
	for _, e := range crudos {
		if f.admite(e) {
			out = append(out, e)
		}
	}
	return out
}

const TopeSondas = 10

type Sonda struct {
	Metodo string
	Ruta   string
	// Estado es el que el servidor DEVOLVIÓ de verdad. Va en la clave de
	// agrupación y no solo en la fila porque «/.git/config → 404» y
	// «/.git/config → 200» son dos hechos opuestos sobre el nodo, y sumarlos
	// borraría justo la diferencia que hay que ver (ver hallazgos.go).
	Estado  int
	Veces   int
	Primera time.Time
	Ultima  time.Time
}

// claveSonda agrupa. Los tres campos juntos, por lo dicho en Estado.
type claveSonda struct {
	metodo string
	ruta   string
	estado int
}

// Origen agrupa lo que hizo una misma dirección.
type Origen struct {
	IP        netip.Addr
	Red       Red
	Eventos   int
	Primera   time.Time
	Ultima    time.Time
	PorMotivo map[Motivo]int
	Cuentas   []string
	Senales   []Senal

	Evidencia []Sonda

	SondasVistas int

	RutasInexistentes int

	Gravedad Gravedad
}

func PorOrigen(eventos []Evento) []Origen {
	type acumulado struct {
		o       Origen
		cuentas map[string]bool
		ajenas  int

		sinRutaOK map[string]bool

		sondas map[claveSonda]*Sonda
	}
	porIP := make(map[netip.Addr]*acumulado)

	for _, e := range eventos {
		a, ok := porIP[e.Origen]
		if !ok {
			a = &acumulado{
				o: Origen{
					IP: e.Origen, Red: e.Red,
					Primera: e.Momento, Ultima: e.Momento,
					PorMotivo: make(map[Motivo]int),
				},
				cuentas:   make(map[string]bool),
				sinRutaOK: make(map[string]bool),
				sondas:    make(map[claveSonda]*Sonda),
			}
			porIP[e.Origen] = a
		}
		a.o.Eventos++
		a.o.PorMotivo[e.Motivo]++
		if e.Momento.Before(a.o.Primera) {
			a.o.Primera = e.Momento
		}
		if e.Momento.After(a.o.Ultima) {
			a.o.Ultima = e.Momento
		}
		if g := e.Motivo.Gravedad(); g > a.o.Gravedad {
			a.o.Gravedad = g
		}
		if e.Cuenta != "" {
			a.cuentas[e.Cuenta] = true
		}
		if e.Motivo == RutaInexistente {
			a.sinRutaOK[e.Ruta] = true
		}
		if RutaDeSoftwareAjeno(e.Ruta) {
			a.ajenas++
		}

		k := claveSonda{metodo: e.Metodo, ruta: e.Ruta, estado: e.Estado}
		sd, ok := a.sondas[k]
		if !ok {
			sd = &Sonda{
				Metodo: e.Metodo, Ruta: e.Ruta, Estado: e.Estado,
				Primera: e.Momento, Ultima: e.Momento,
			}
			a.sondas[k] = sd
		}
		sd.Veces++
		if e.Momento.Before(sd.Primera) {
			sd.Primera = e.Momento
		}
		if e.Momento.After(sd.Ultima) {
			sd.Ultima = e.Momento
		}
	}

	out := make([]Origen, 0, len(porIP))
	for _, a := range porIP {
		a.o.Cuentas = ordenado(a.cuentas)
		a.o.RutasInexistentes = len(a.sinRutaOK)
		a.o.SondasVistas = len(a.sondas)
		a.o.Evidencia = sondasOrdenadas(a.sondas, TopeSondas)

		if a.o.Red.DeFuera() {
			if len(a.sinRutaOK) >= umbralExploracion {
				a.o.Senales = append(a.o.Senales, SenalExploracion)
			}
			if a.o.PorMotivo[CredencialIncorrecta] >= umbralFuerzaBruta {
				a.o.Senales = append(a.o.Senales, SenalFuerzaBruta)
			}
			if a.ajenas > 0 {
				a.o.Senales = append(a.o.Senales, SenalSoftwareAjeno)
			}
		}
		out = append(out, a.o)
	}

	slices.SortFunc(out, func(x, y Origen) int {
		if c := cmp.Compare(y.Gravedad, x.Gravedad); c != 0 {
			return c
		}
		if c := cmp.Compare(y.Eventos, x.Eventos); c != 0 {
			return c
		}

		return strings.Compare(x.IP.String(), y.IP.String())
	})
	return out
}

// TopeRutas es cuántas rutas se listan en la tabla de rutas más rechazadas.
// Exportada porque el adaptador web la necesita para poder DECIR que recorta.
const TopeRutas = 10

// Cuenta es un par «algo» y «cuántas veces», para las listas del resumen.
type Cuenta struct {
	Valor string
	Veces int
}

// Resumen es la cabecera del panel: lo que se responde de un vistazo.
type Resumen struct {
	Eventos int
	// TotalHistorico son los rechazos vistos desde siempre, que puede ser
	// mucho mayor que lo que el anillo conserva. Se publica para que un
	// anillo lleno no se lea como «esto es todo lo que ha pasado».
	TotalHistorico int64
	Truncado       bool

	IPsUnicas  int
	DesdeFuera int

	RutasMasPedidas      []Cuenta
	RutasVistas          int
	AutenticacionFallida int
	// OrigenesConSenal son los que alguna inferencia marcó. Es una CUENTA de
	// sospechas, nunca de ataques confirmados.
	OrigenesConSenal int
}

// Resumir compone la cabecera a partir de los eventos ya filtrados.
func Resumir(eventos []Evento, origenes []Origen, totalHistorico int64) Resumen {
	r := Resumen{
		Eventos:        len(eventos),
		TotalHistorico: totalHistorico,
		IPsUnicas:      len(origenes),
	}
	rutas := make(map[string]int)
	for _, e := range eventos {
		rutas[e.Ruta]++
		if e.Motivo == CredencialIncorrecta {
			r.AutenticacionFallida++
		}
	}
	for _, o := range origenes {
		if o.Red.DeFuera() {
			r.DesdeFuera++
		}
		if len(o.Senales) > 0 {
			r.OrigenesConSenal++
		}
	}
	r.RutasMasPedidas = masFrecuentes(rutas, TopeRutas)
	r.RutasVistas = len(rutas)
	return r
}

func sondasOrdenadas(m map[claveSonda]*Sonda, tope int) []Sonda {
	out := make([]Sonda, 0, len(m))
	for _, sd := range m {
		out = append(out, *sd)
	}
	slices.SortFunc(out, func(x, y Sonda) int {
		if c := cmp.Compare(y.Veces, x.Veces); c != 0 {
			return c
		}
		if c := strings.Compare(x.Ruta, y.Ruta); c != 0 {
			return c
		}
		if c := strings.Compare(x.Metodo, y.Metodo); c != 0 {
			return c
		}
		return cmp.Compare(x.Estado, y.Estado)
	})
	if len(out) > tope {
		out = out[:tope]
	}
	return out
}

func masFrecuentes(m map[string]int, tope int) []Cuenta {
	out := make([]Cuenta, 0, len(m))
	for v, n := range m {
		out = append(out, Cuenta{Valor: v, Veces: n})
	}
	slices.SortFunc(out, func(x, y Cuenta) int {
		if c := cmp.Compare(y.Veces, x.Veces); c != 0 {
			return c
		}
		return strings.Compare(x.Valor, y.Valor) // determinismo, ver PorOrigen
	})
	if len(out) > tope {
		out = out[:tope]
	}
	return out
}

func ordenado(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for v := range m {
		out = append(out, v)
	}
	slices.Sort(out)
	return out
}

// itoa evita importar strconv solo para los textos de Explicacion.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func (o Origen) Destacar() bool {
	return o.Gravedad == Atencion || o.Red.DeFuera()
}

func (e Evento) Destacar() bool {
	return e.Motivo.Gravedad() == Atencion || e.Red.DeFuera()
}
