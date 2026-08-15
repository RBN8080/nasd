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
	// SenalExploracion — el mismo origen pidió muchas rutas DISTINTAS que no
	// existen. Es el patrón de un escáner recorriendo un diccionario.
	SenalExploracion Senal = iota
	// SenalFuerzaBruta — credenciales incorrectas repetidas desde un origen.
	SenalFuerzaBruta
	// SenalSoftwareAjeno — se pidieron rutas de programas que este NAS no
	// ejecuta NI HA EJECUTADO NUNCA. Ver rutaDeSoftwareAjeno.
	SenalSoftwareAjeno
)

func (s Senal) Etiqueta() string {
	switch s {
	case SenalExploracion:
		return "Exploración automatizada"
	case SenalFuerzaBruta:
		return "Intentos repetidos de contraseña"
	case SenalSoftwareAjeno:
		return "Sondeo de software que aquí no existe"
	}
	return "Señal desconocida"
}

// Explicacion dice en qué se basa la sospecha Y con qué se puede confundir.
//
// La segunda mitad no es cortesía: una señal sin su falso positivo escrito al
// lado se lee como un veredicto, y este panel no emite veredictos.
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
	}
	return ""
}

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

// rutaDeSoftwareAjeno decide si una ruta pide algo que este NAS no tiene.
//
// # POR QUÉ NO ES UNA LISTA DE RUTAS CONOCIDAS
//
// Una lista de «/wp-login.php, /phpmyadmin, …» caduca sola: cada mes aparecen
// sondas nuevas y la lista se queda corta sin avisar, que es el modo de fallo
// que este proyecto persigue.
//
// Esto se apoya en un HECHO comprobable del propio nodo: nasd es un binario
// de Go con una tabla de rutas fija (web.Rutas()) y NO ejecuta PHP, ni ASP,
// ni CGI, ni tiene repositorio ni volcados de base de datos publicados. Una
// petición con esas extensiones no puede ser un enlace roto de este NAS,
// porque este NAS nunca ha servido nada así. La afirmación se sostiene sobre
// lo que el nodo es, no sobre lo que alguien recuerde apuntar.
func rutaDeSoftwareAjeno(ruta string) bool {
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

// Filtro acota lo que se mira. Los punteros distinguen «sin filtrar» de
// «filtrar por el valor cero», que en Motivo y Gravedad son valores legítimos
// —MotivoDesconocido y Rutina— y se perderían con un valor plano.
type Filtro struct {
	Desde    time.Time
	Hasta    time.Time
	IP       string
	Ruta     string
	Motivo   *Motivo
	Gravedad *Gravedad
	Red      *Red
}

func (f Filtro) admite(e Evento) bool {
	if !f.Hasta.IsZero() && e.Momento.After(f.Hasta) {
		return false
	}
	// Por PREFIJO y no por igualdad: así «203.0.113» encuentra la red entera
	// sin tener que teclear cada dirección.
	if f.IP != "" && !strings.HasPrefix(e.Origen.String(), f.IP) {
		return false
	}
	if f.Ruta != "" && !strings.Contains(strings.ToLower(e.Ruta), strings.ToLower(f.Ruta)) {
		return false
	}
	if f.Motivo != nil && e.Motivo != *f.Motivo {
		return false
	}
	if f.Gravedad != nil && e.Motivo.Gravedad() != *f.Gravedad {
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

// Origen agrupa lo que hizo una misma dirección.
type Origen struct {
	IP  netip.Addr
	Red Red
	// Eventos es cuántos rechazos, no cuántas peticiones: una descarga
	// correcta desde esta misma IP no cuenta aquí.
	Eventos int
	Primera time.Time
	Ultima  time.Time
	// PorMotivo lleva el desglose. Es lo que permite ver que un origen con
	// 300 eventos son 300 «sin sesión» de un móvil reconectando, y no 300
	// intentos de contraseña.
	PorMotivo map[Motivo]int
	// RutasDistintas es la base de SenalExploracion, y se guarda el número y
	// no el conjunto: para pintar la fila basta la cifra.
	RutasDistintas int
	Agentes        []string
	Cuentas        []string
	Senales        []Senal
	// Gravedad es la MAYOR de sus eventos, no la media: un origen con 200
	// rechazos de rutina y uno de atención merece mirarse, y promediar lo
	// escondería.
	Gravedad Gravedad
}

// PorOrigen agrupa los eventos por dirección y deriva las señales.
//
// Ordena por gravedad y luego por número de eventos, no por fecha: quien abre
// este panel quiere ver primero lo que más pide su atención, no lo último que
// pasó — para eso está la vista cronológica.
func PorOrigen(eventos []Evento) []Origen {
	type acumulado struct {
		o         Origen
		rutas     map[string]bool
		agentes   map[string]bool
		cuentas   map[string]bool
		ajenas    int
		sinRutaOK map[string]bool
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
				rutas:     make(map[string]bool),
				agentes:   make(map[string]bool),
				cuentas:   make(map[string]bool),
				sinRutaOK: make(map[string]bool),
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
		a.rutas[e.Ruta] = true
		if e.Agente != "" {
			a.agentes[e.Agente] = true
		}
		if e.Cuenta != "" {
			a.cuentas[e.Cuenta] = true
		}
		if e.Motivo == RutaInexistente {
			a.sinRutaOK[e.Ruta] = true
		}
		if rutaDeSoftwareAjeno(e.Ruta) {
			a.ajenas++
		}
	}

	out := make([]Origen, 0, len(porIP))
	for _, a := range porIP {
		a.o.RutasDistintas = len(a.rutas)
		a.o.Agentes = ordenado(a.agentes)
		a.o.Cuentas = ordenado(a.cuentas)

		// LAS INFERENCIAS, aquí y en ningún otro sitio.
		//
		// SOLO DESDE INTERNET, y lo enseñó la primera captura del panel en
		// produccion: «Sondeo de software que aquí no existe» saltó sobre el
		// PROPIO NODO porque el despliegue había probado /wp-login.php con
		// curl. Una alarma roja sobre uno mismo, en la primera pantalla que
		// vio el responsable.
		//
		// El argumento no es cosmético. Desde la LAN, el túnel o el propio
		// nodo, quien pide YA tiene la casa o la clave: el túnel es mudo sin
		// ella (06_ACCESO_REMOTO §3) y a la LAN se entra por la puerta. Una
		// sospecha de intrusión desde ahí no informa de nada y gasta la
		// credibilidad de las que sí importan — el defecto que 00_RECTOR.md
		// §12.5 lleva persiguiendo en los verificadores de este proyecto.
		//
		// Los HECHOS se siguen registrando igual desde cualquier red: lo que
		// no se emite es la interpretación.
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
		// Tercer criterio para que el orden sea DETERMINISTA: sin él, dos
		// orígenes empatados salen en el orden aleatorio del recorrido del
		// mapa y la tabla baila entre recargas sin que nada haya cambiado.
		return strings.Compare(x.IP.String(), y.IP.String())
	})
	return out
}

// Cuenta es un par «algo» y «cuántas veces», para las listas del resumen.
type Cuenta struct {
	Valor string
	Veces int
}

// Resumen es la cabecera del panel: lo que se responde de un vistazo.
type Resumen struct {
	Ventana time.Duration
	Eventos int
	// TotalHistorico son los rechazos vistos desde siempre, que puede ser
	// mucho mayor que lo que el anillo conserva. Se publica para que un
	// anillo lleno no se lea como «esto es todo lo que ha pasado».
	TotalHistorico int64
	Truncado       bool

	IPsUnicas   int
	DesdeFuera  int
	PorGravedad map[Gravedad]int
	PorMotivo   map[Motivo]int
	PorRed      map[Red]int

	RutasMasPedidas      []Cuenta
	AutenticacionFallida int
	// OrigenesConSenal son los que alguna inferencia marcó. Es una CUENTA de
	// sospechas, nunca de ataques confirmados.
	OrigenesConSenal int
}

// Resumir compone la cabecera a partir de los eventos ya filtrados.
func Resumir(eventos []Evento, origenes []Origen, ventana time.Duration, totalHistorico int64) Resumen {
	r := Resumen{
		Ventana:        ventana,
		Eventos:        len(eventos),
		TotalHistorico: totalHistorico,
		IPsUnicas:      len(origenes),
		PorGravedad:    make(map[Gravedad]int),
		PorMotivo:      make(map[Motivo]int),
		PorRed:         make(map[Red]int),
	}
	rutas := make(map[string]int)
	for _, e := range eventos {
		r.PorGravedad[e.Motivo.Gravedad()]++
		r.PorMotivo[e.Motivo]++
		r.PorRed[e.Red]++
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
	r.RutasMasPedidas = masFrecuentes(rutas, 10)
	return r
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

// Destacar dice si una fila merece color en el panel.
//
// # POR QUÉ NO BASTA CON LA GRAVEDAD DEL MOTIVO
//
// Se vio en la primera captura real: casi toda la tabla salía en naranja
// porque RutaInexistente es «aviso», y lo que la producía eran los
// /favicon.ico y /apple-touch-icon.png que el iPhone y el Chrome del
// responsable piden solos. Un panel donde casi todo está resaltado no resalta
// nada — el mismo defecto que 00_RECTOR.md §12.5 persigue en los
// verificadores de este proyecto.
//
// La gravedad del motivo NO cambia: es un hecho y sigue siendo la que es, y
// el filtro por gravedad la sigue usando entera. Lo que cambia es el ÉNFASIS,
// que es una decisión de presentación y por eso vive aquí y no en Motivo:
//
//   - Atención SIEMPRE se destaca, venga de donde venga. Son CSRF, permiso
//     insuficiente, límite de intentos y recurso reservado: a ninguna se
//     llega navegando, así que desde la propia LAN también dicen algo.
//   - Desde Internet se destaca TODO, incluida la Rutina. No es una
//     inconsistencia: el resumen ya trata «alguna dirección de Internet»
//     como el propio objetivo del panel («ninguna» es el estado sano), así
//     que un simple «sin sesión» desde fuera ya es la señal — es la única
//     vez que se ve a alguien tocando la puerta, sea quien sea.
//   - Aviso y Rutina desde casa NO se destacan. Una ruta inexistente o una
//     contraseña fallada las produce cualquiera de casa todos los días.
func (o Origen) Destacar() bool {
	return o.Gravedad == Atencion || o.Red.DeFuera()
}

// Destacar, para una fila de la cronología. Mismo criterio y por eso mismo
// nombre: si divergieran, la tabla de arriba y la de abajo resaltarían cosas
// distintas del mismo suceso.
func (e Evento) Destacar() bool {
	return e.Motivo.Gravedad() == Atencion || e.Red.DeFuera()
}
