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
)

// Conexiones entrantes de Internet — el hueco que el historial de rechazos no
// podía ver, y que solo se descubrió mirando el diario del nodo.
//
// # EL DEFECTO MEDIDO QUE ESTO CIERRA
//
// El 2026-08-16, entre las 11:49 y las 11:54, VEINTE direcciones de DRIFTNET
// (AS211298, GB) recorrieron el puerto 443 del nodo. El panel enseñó UNA.
//
// No fue un fallo del anillo de rechazos: fue su definición. Un Evento se
// anota en conRegistro (web/seguridad.go), que es middleware HTTP, así que
// solo puede existir si hubo una petición HTTP que rechazar. Diecinueve de
// aquellas veinte murieron ANTES de eso —ofrecieron TLS 1.0, listas de
// cifrados de hace veinte años, o cortaron sin decir nada— y crypto/tls las
// rechazó sin que net/http llegara a leer una línea.
//
// Conviene leerlo entero, porque las dos mitades importan: la postura TLS del
// nodo funcionó exactamente como se diseñó (MinVersion TLS 1.2, web/tls.go).
// Lo que falló fue VERLO. El barrido anterior, del 13/08 11:52–11:57, fue de
// quince direcciones y el panel habría enseñado CERO por el mismo motivo.
//
// # POR QUÉ NO ES UN Motivo MÁS DEL ANILLO DE RECHAZOS
//
// Porque no es un rechazo. Motivo está definido como «la regla que produjo el
// rechazo», y cada valor apunta a un punto concreto del código que decidió
// negar; una conexión recién aceptada todavía no niega nada. Meterla allí
// obligaría a que «Rechazos en la ventana» contara cosas que no son rechazos,
// y este paquete existe precisamente para no mezclar categorías — es el mismo
// argumento por el que Senal no es un Motivo (resumen.go).
//
// # SOLO DE INTERNET, Y ESO ES LO QUE LA HACE BARATA
//
// Anotar descarta todo lo que no sea RedInternet, y el filtro vive AQUÍ y no
// en quien llama para que no haya dos sitios capaces de equivocarse. Sin ese
// filtro esto anotaría cada conexión de cada aparato de la casa —el iPhone
// abre varias por página— y el anillo se llenaría de lo único que nadie
// necesita mirar, tapando justo lo que sí.
//
// Es además lo que el responsable pidió con estas palabras el 2026-08-16:
// «únicamente reporte conexiones entrantes en internet».

// Conexion es una conexión TCP aceptada desde Internet.
//
// Solo lleva DOS campos porque solo hay dos cosas que se sepan con certeza en
// el instante de aceptarla: cuándo y desde dónde. No hay método, ni ruta, ni
// código de respuesta, ni User-Agent — nada de eso ha llegado todavía, y
// puede que no llegue nunca. Rellenarlos con valores por omisión los haría
// pasar por datos (P5, mismo criterio que los punteros de web/estado.go).
//
// La red tampoco se guarda: por construcción es siempre RedInternet, porque
// Anotar no admite otra cosa.
type Conexion struct {
	Momento time.Time
	Origen  netip.Addr
}

// Conexiones es el anillo de conexiones entrantes de Internet.
//
// # ESTADO COMPARTIDO (ADR-0013)
//
// Es el QUINTO del programa, tras el registro de subidas, el de sesiones, los
// contadores y el anillo de rechazos. Lleva mutex por lo mismo que aquel: se
// escribe desde el bucle de aceptación de dos servidores a la vez mientras el
// panel recorre el buffer entero para pintarse.
//
// Comparte tope con el anillo de rechazos —Capacidad, 2000— y no estrena una
// constante propia: el cálculo que la justifica allí vale aquí con más margen,
// porque una Conexion son ~48 B frente a los ~400 B de un Evento.
type Conexiones struct {
	mu sync.Mutex
	// buf es de tamaño FIJO y reservado entero al construir, igual que el del
	// anillo de rechazos: anotar es asignar en una posición que ya existe.
	// Aquí importa más que allí — esto corre en el bucle de aceptación, antes
	// de que la conexión tenga hilo propio, así que una reserva de memoria
	// aquí retrasaría a todo el que esté entrando detrás.
	buf       []Conexion
	siguiente int
	total     int64
	sucio     bool

	ruta string
}

// CargarConexiones lee el archivo si existe y deja el anillo listo. Mismo
// tratamiento del error que CargarAnillo, y por el mismo motivo: un historial
// de observación ilegible cuesta el historial, no el NAS.
func CargarConexiones(ruta string) (*Conexiones, error) {
	c := &Conexiones{
		buf:  make([]Conexion, Capacidad),
		ruta: ruta,
	}
	if err := c.releer(); err != nil {
		return c, err
	}
	return c, nil
}

func (c *Conexiones) releer() error {
	f, err := os.Open(c.ruta)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("abrir el historial de conexiones %q: %w", c.ruta, err)
	}
	defer f.Close()

	var (
		leidas []Conexion
		total  int64
	)
	s := bufio.NewScanner(f)
	// Una línea aquí es mucho más corta que la de un Evento —dos campos, sin
	// texto libre del cliente— pero se deja el mismo buffer: un archivo
	// corrupto no debe distinguirse por reventar de una forma distinta.
	s.Buffer(make([]byte, 0, 8*1024), 8*1024)
	for n := 1; s.Scan(); n++ {
		linea := strings.TrimSpace(s.Text())
		if linea == "" || strings.HasPrefix(linea, "#") {
			if n, ok := totalDeCabecera(linea); ok {
				total = n
			}
			continue
		}
		var d conexionEnDisco
		if err := json.Unmarshal([]byte(linea), &d); err != nil {
			return fmt.Errorf("historial de conexiones, línea %d: %w", n, err)
		}
		cx, err := d.aConexion()
		if err != nil {
			return fmt.Errorf("historial de conexiones, línea %d: %w", n, err)
		}
		leidas = append(leidas, cx)
	}
	if err := s.Err(); err != nil {
		return fmt.Errorf("leer el historial de conexiones: %w", err)
	}

	if len(leidas) > Capacidad {
		leidas = leidas[len(leidas)-Capacidad:]
	}
	copy(c.buf, leidas)
	c.siguiente = len(leidas) % Capacidad
	// El maximo de los dos, por lo mismo que en Anillo.releer: un archivo sin
	// la marca es anterior al 2026-08-18, y un total menor que lo guardado
	// seria un archivo inconsistente.
	c.total = max(total, int64(len(leidas)))
	return nil
}

// Anotar registra una conexión entrante, SI viene de Internet.
//
// El descarte vive aquí y no en quien llama a propósito: hay un solo sitio
// que puede equivocarse en vez de uno por cada servidor que se enchufe, que
// es el mismo argumento con el que DeFuera existe como método (evento.go).
//
// Devuelve si la anotó. No lo necesita el servidor —que llama y sigue— pero
// sí las pruebas, que si no tendrían que afirmar sobre el tamaño del anillo
// para comprobar un descarte.
func (c *Conexiones) Anotar(ip netip.Addr, momento time.Time) bool {
	if !ClasificarRed(ip).DeFuera() {
		return false
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.buf[c.siguiente] = Conexion{Momento: momento, Origen: ip}
	c.siguiente = (c.siguiente + 1) % Capacidad
	c.total++
	c.sucio = true
	return true
}

// Total son las conexiones de Internet vistas desde siempre, no las que
// caben. El panel la publica por lo mismo que publica la del anillo de
// rechazos: un anillo lleno no debe leerse como «esto es todo lo que ha
// pasado».
func (c *Conexiones) Total() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.total
}

// Desde devuelve las conexiones posteriores a un instante, de la MÁS RECIENTE
// a la más antigua. Copias, por lo mismo que Anillo.Desde: quien mira no
// altera el historial.
func (c *Conexiones) Desde(t time.Time) []Conexion {
	c.mu.Lock()
	defer c.mu.Unlock()

	cuantas := int(min(c.total, int64(Capacidad)))
	out := make([]Conexion, 0, cuantas)
	for i := range cuantas {
		pos := (c.siguiente - 1 - i + Capacidad) % Capacidad
		cx := c.buf[pos]
		// El anillo está ordenado por naturaleza: la primera que cae fuera de
		// la ventana garantiza que las siguientes también.
		if cx.Momento.Before(t) {
			break
		}
		out = append(out, cx)
	}
	return out
}

// Mantener vuelca a disco cada intervaloVolcado hasta que se cancele, con un
// último volcado al salir. Idéntico a Anillo.Mantener y por el mismo motivo:
// un apagado ordenado no pierde nada, y solo un corte de corriente llega a
// perder lo del último minuto — que sigue en el diario (ADR-0037).
func (c *Conexiones) Mantener(hecho <-chan struct{}, alFallar func(error)) {
	t := time.NewTicker(intervaloVolcado)
	defer t.Stop()
	for {
		select {
		case <-hecho:
			if err := c.Volcar(); err != nil {
				alFallar(err)
			}
			return
		case <-t.C:
			if err := c.Volcar(); err != nil {
				alFallar(err)
			}
		}
	}
}

// Volcar escribe el anillo entero de forma atómica. El contenido se compone
// DENTRO del candado y se escribe FUERA, igual que en Anillo.Volcar: sostener
// el mutex durante un fsync bloquearía el bucle de aceptación.
func (c *Conexiones) Volcar() error {
	c.mu.Lock()
	if !c.sucio {
		c.mu.Unlock()
		return nil
	}
	cuantas := int(min(c.total, int64(Capacidad)))
	orden := make([]Conexion, 0, cuantas)
	for i := cuantas - 1; i >= 0; i-- {
		orden = append(orden, c.buf[(c.siguiente-1-i+Capacidad)%Capacidad])
	}
	// El total se copia DENTRO del candado, con el resto: la escritura ocurre
	// fuera, y leerlo alli seria una carrera con quien este anotando.
	total := c.total
	c.sucio = false
	c.mu.Unlock()

	err := escribirAtomico(c.ruta, ".conexiones-*", func(w io.Writer) error {
		fmt.Fprintf(w, "# Conexiones entrantes de Internet — anillo de %d, de la más antigua a la más reciente.\n", Capacidad)
		fmt.Fprint(w, "# Una por línea, en JSON. Solo instante y dirección: nada más se sabe al aceptar.\n")
		fmt.Fprintf(w, "%s%d\n", marcaTotal, total)
		enc := json.NewEncoder(w)
		for _, cx := range orden {
			if err := enc.Encode(deConexion(cx)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		// Volver a marcarlo sucio para que el siguiente intento lo reintente,
		// por lo mismo que Anillo.Volcar: un fallo transitorio de disco no
		// debe perder en silencio lo anotado.
		c.mu.Lock()
		c.sucio = true
		c.mu.Unlock()
		return fmt.Errorf("historial de conexiones: %w", err)
	}
	return nil
}

// conexionEnDisco es la forma serializada, separada del tipo de dominio por
// lo mismo que eventoEnDisco: renombrar un campo no debe romper en silencio
// los archivos ya escritos.
type conexionEnDisco struct {
	Momento string `json:"t"`
	Origen  string `json:"ip"`
}

func deConexion(c Conexion) conexionEnDisco {
	d := conexionEnDisco{Momento: c.Momento.Format(time.RFC3339)}
	if c.Origen.IsValid() {
		d.Origen = c.Origen.String()
	}
	return d
}

func (d conexionEnDisco) aConexion() (Conexion, error) {
	momento, err := time.Parse(time.RFC3339, d.Momento)
	if err != nil {
		return Conexion{}, fmt.Errorf("fecha inválida: %w", err)
	}
	c := Conexion{Momento: momento}
	if d.Origen != "" {
		ip, err := netip.ParseAddr(d.Origen)
		if err != nil {
			return Conexion{}, fmt.Errorf("dirección %q inválida: %w", d.Origen, err)
		}
		c.Origen = ip
	}
	return c, nil
}

// OrigenConectado agrupa las conexiones de una misma dirección.
//
// Es DELIBERADAMENTE más pobre que Origen (resumen.go): aquí no hay motivos,
// ni rutas, ni señales, porque no hubo petición de la que derivarlos. Enseñar
// las mismas columnas vacías daría a entender que faltan datos, cuando lo que
// pasa es que no existen.
type OrigenConectado struct {
	IP         netip.Addr
	Conexiones int
	Primera    time.Time
	Ultima     time.Time
}

// PorOrigenConectado agrupa por dirección, de la más insistente a la menos.
//
// Ese orden y no el cronológico porque la pregunta que contesta esta tabla es
// «quién ha estado tocando la puerta», no «qué fue lo último». La cronología
// exacta ya está en el diario del nodo, que es la fuente completa.
func PorOrigenConectado(conexiones []Conexion) []OrigenConectado {
	porIP := make(map[netip.Addr]*OrigenConectado)
	for _, c := range conexiones {
		o, ok := porIP[c.Origen]
		if !ok {
			o = &OrigenConectado{IP: c.Origen, Primera: c.Momento, Ultima: c.Momento}
			porIP[c.Origen] = o
		}
		o.Conexiones++
		if c.Momento.Before(o.Primera) {
			o.Primera = c.Momento
		}
		if c.Momento.After(o.Ultima) {
			o.Ultima = c.Momento
		}
	}

	out := make([]OrigenConectado, 0, len(porIP))
	for _, o := range porIP {
		out = append(out, *o)
	}
	slices.SortFunc(out, func(x, y OrigenConectado) int {
		if c := cmp.Compare(y.Conexiones, x.Conexiones); c != 0 {
			return c
		}
		// Tercer criterio para que el orden sea DETERMINISTA, por lo mismo que
		// en PorOrigen: sin él la tabla baila entre recargas sin que nada haya
		// cambiado.
		return strings.Compare(x.IP.String(), y.IP.String())
	})
	return out
}
