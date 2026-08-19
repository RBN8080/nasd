package seguridad

import (
	"bufio"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Toques: la capa que faltaba debajo de todo lo demás — RF-33, ADR-0066.
//
// # LAS TRES CAPAS, Y POR QUÉ HACÍA FALTA UNA TERCERA
//
// El panel ya conocía dos hechos, y los dos nacen DENTRO de nasd:
//
//	Petición rechazada  → anillo de rechazos (ADR-0061). Exige que alguien
//	                      llegara a pedir algo por HTTP.
//	Conexión aceptada   → anillo de conexiones (ADR-0064). Exige que el saludo
//	                      TCP llegara al 80 o al 443.
//
// Falta lo de más abajo: un paquete que el cortafuegos descarta no llega a
// nasd, así que un escáner recorriendo puertos cerrados era INVISIBLE. Ese
// hecho lo captura nas-sensor, un proceso aparte con CAP_NET_RAW, y este
// archivo es la puerta por la que entra.
//
// Cada capa es superconjunto de la siguiente: todo rechazo tuvo una conexión, y
// toda conexión empezó por un paquete. Por eso el panel las enseña en columnas
// de la misma fila y no en tres tablas.
//
// # NO HAY ESTADO COMPARTIDO AQUÍ, Y ES DELIBERADO
//
// A diferencia de Anillo y Conexiones, esto NO guarda nada en memoria: nasd no
// es quien escribe, así que no tiene nada que mantener. Se lee el archivo cada
// vez que se pinta la página — son como mucho Capacidad líneas cortas y la
// abre una persona, no un bucle. Consecuencia buscada: este paquete no estrena
// un sexto punto de estado compartido bajo ADR-0013, y nasd no cambia ni una
// de sus garantías de concurrencia por tener sensor.
//
// # EL FILTRO «SOLO INTERNET» VIVE AQUÍ, Y EN NINGÚN OTRO SITIO
//
// nas-sensor anota TODO lo que captura, venga de donde venga: no sabe qué es
// Internet y no le hace falta saberlo. Quien descarta lo de casa es LeerToques,
// con la misma ClasificarRed que usa el resto del panel — la que ya corrigió
// dos fallos reales (el IPv6 de casa contado como extraño, y fe80::…%2 por la
// zona). Reimplementar eso en C habría duplicado la lógica Y su historial de
// fallos; es el mismo argumento que Conexiones.Anotar lleva escrito, aplicado
// al otro lado de la frontera.

// Toque es un paquete que alguien envió al nodo para INICIAR algo: un TCP SYN
// sin ACK, o un echo request.
//
// Solo lleva cuatro campos porque son los únicos que se saben con certeza al
// ver pasar un paquete suelto. No hay ruta, ni código de respuesta, ni
// User-Agent: nada de eso ha llegado todavía y puede que no llegue nunca.
type Toque struct {
	Momento time.Time
	Origen  netip.Addr
	// Puerto es CERO en un ping, y el panel lo dice en vez de enseñar un 0 que
	// se leería como «puerto 0».
	Puerto uint16
	// Tipo es "syn" o "ping". Se guarda como el texto que escribe el sensor y
	// no como un número, para que el archivo se lea con «cat» en el nodo.
	Tipo string
}

// OrigenTocado agrupa los toques de una misma dirección.
//
// Es DELIBERADAMENTE más pobre que Origen: aquí no hay motivos, ni rutas, ni
// señales, porque no hubo petición de la que derivarlos. Es la misma decisión
// que OrigenConectado tomó una capa más arriba.
type OrigenTocado struct {
	IP     netip.Addr
	Toques int
	// Puertos son los DISTINTOS que se tocaron, ordenados. Es lo que convierte
	// «doce paquetes» en «doce puertos cerrados distintos», que es la
	// diferencia entre un cliente reintentando y alguien recorriendo el nodo.
	Puertos []uint16
	Primera time.Time
	Ultima  time.Time
}

// topeLineas acota lo que se lee de un archivo que escribe OTRO programa.
//
// El sensor no escribe más de Capacidad líneas, así que esto no debería
// dispararse nunca. Existe porque «no debería» no es una garantía cuando el
// escritor es un proceso distinto: sin tope, un archivo corrupto o de otra
// versión decidiría cuánta memoria reserva nasd.
const topeLineas = Capacidad * 4

// Historial es lo que hay del sensor: si está, qué trae y cuánto ha visto.
//
// HAY ES UN CAMPO Y NO SE DEDUCE DE QUE LA LISTA ESTÉ VACÍA, y la distinción no
// es teórica: «cero toques» y «sin sensor» significan lo contrario. Lo primero
// es una buena noticia que el panel debe enseñar; lo segundo es que no estamos
// mirando, y pintarlo como un cero sería afirmar algo que nadie ha comprobado.
// Es el mismo criterio con el que HayGeo decide si existe la columna de
// operador (RF-30, criterio 1).
type Historial struct {
	Hay    bool
	Toques []Toque
	// Total son los toques CAPTURADOS desde siempre, tal como el sensor los
	// publica en su cabecera. A diferencia de los otros dos anillos SOBREVIVE A
	// UN REINICIO, porque viaja en el archivo y no en memoria.
	Total int64
}

// LeerToques lee el historial que deja nas-sensor y devuelve SOLO lo de
// Internet posterior a «desde», del más reciente al más antiguo.
//
// UN ARCHIVO QUE NO EXISTE NO ES UN ERROR: es un nodo sin sensor instalado.
// Devuelve un Historial con Hay en falso y el panel esconde esas columnas.
func LeerToques(ruta string, desde time.Time) (Historial, error) {
	f, err := os.Open(ruta)
	if errors.Is(err, os.ErrNotExist) {
		return Historial{}, nil
	}
	if err != nil {
		return Historial{}, fmt.Errorf("abrir el historial de toques %q: %w", ruta, err)
	}
	defer f.Close()

	var (
		leidos []Toque
		total  int64
	)
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 0, 4*1024), 4*1024)
	for n := 1; s.Scan(); n++ {
		if n > topeLineas {
			return Historial{}, fmt.Errorf("historial de toques: más de %d líneas", topeLineas)
		}
		linea := strings.TrimSpace(s.Text())
		if linea == "" {
			continue
		}
		if strings.HasPrefix(linea, "#") {
			if n, ok := totalDeCabecera(linea); ok {
				total = n
			}
			continue
		}
		t, err := deLinea(linea)
		if err != nil {
			return Historial{}, fmt.Errorf("historial de toques, línea %d: %w", n, err)
		}
		leidos = append(leidos, t)
	}
	if err := s.Err(); err != nil {
		return Historial{}, fmt.Errorf("leer el historial de toques: %w", err)
	}

	// EL DESCARTE DE LO DE CASA, en un solo sitio. Va después de leer y no al
	// vuelo para que el orden del archivo mande y no el de las comprobaciones.
	out := make([]Toque, 0, len(leidos))
	for i := len(leidos) - 1; i >= 0; i-- { // del más reciente al más antiguo
		t := leidos[i]
		if !ClasificarRed(t.Origen).DeFuera() {
			continue
		}
		if !desde.IsZero() && t.Momento.Before(desde) {
			continue
		}
		out = append(out, t)
	}
	return Historial{Hay: true, Toques: out, Total: total}, nil
}

// deLinea analiza «instante origen puerto tipo».
//
// Formato de texto plano y no JSON porque quien escribe es C sin librerías:
// componer JSON a mano allí sería un riesgo gratuito cuando los cuatro campos
// no pueden contener un espacio. Ver el encabezado de nas-sensor/sensor.c.
func deLinea(linea string) (Toque, error) {
	campos := strings.Fields(linea)
	if len(campos) != 4 {
		return Toque{}, fmt.Errorf("se esperaban 4 campos y hay %d", len(campos))
	}
	momento, err := time.Parse(time.RFC3339, campos[0])
	if err != nil {
		return Toque{}, fmt.Errorf("instante ilegible: %w", err)
	}
	origen, err := netip.ParseAddr(campos[1])
	if err != nil {
		return Toque{}, fmt.Errorf("dirección ilegible: %w", err)
	}
	puerto, err := strconv.ParseUint(campos[2], 10, 16)
	if err != nil {
		return Toque{}, fmt.Errorf("puerto ilegible: %w", err)
	}
	if campos[3] != "syn" && campos[3] != "ping" {
		return Toque{}, fmt.Errorf("tipo desconocido %q", campos[3])
	}
	return Toque{
		Momento: momento,
		Origen:  origen,
		Puerto:  uint16(puerto),
		Tipo:    campos[3],
	}, nil
}

// topePuertos acota cuántos puertos distintos se recuerdan por origen.
//
// Un escaneo completo son 65 535, y guardarlos todos para pintarlos en una
// celda no ayuda a nadie: con los primeros ya se ve que es un recorrido, y la
// CUENTA de toques sigue siendo exacta. Es el mismo criterio que topeAgente,
// que acota el User-Agent para que el tope de memoria del anillo sea real.
const topePuertos = 12

// PorOrigenTocado agrupa por dirección, de la más insistente a la menos.
//
// Ese orden y no el cronológico porque la pregunta que contesta esta capa es
// «quién ha estado probando», no «qué fue lo último» — igual que
// PorOrigenConectado una capa más arriba.
func PorOrigenTocado(toques []Toque) []OrigenTocado {
	porIP := make(map[netip.Addr]*OrigenTocado)
	puertos := make(map[netip.Addr]map[uint16]bool)

	for _, t := range toques {
		o, ok := porIP[t.Origen]
		if !ok {
			o = &OrigenTocado{IP: t.Origen, Primera: t.Momento, Ultima: t.Momento}
			porIP[t.Origen] = o
			puertos[t.Origen] = make(map[uint16]bool)
		}
		o.Toques++
		if t.Momento.Before(o.Primera) {
			o.Primera = t.Momento
		}
		if t.Momento.After(o.Ultima) {
			o.Ultima = t.Momento
		}
		// El puerto 0 es el de un ping: no es un puerto y no se lista como si
		// lo fuera.
		if t.Puerto != 0 && len(puertos[t.Origen]) < topePuertos {
			puertos[t.Origen][t.Puerto] = true
		}
	}

	out := make([]OrigenTocado, 0, len(porIP))
	for ip, o := range porIP {
		for p := range puertos[ip] {
			o.Puertos = append(o.Puertos, p)
		}
		slices.Sort(o.Puertos)
		out = append(out, *o)
	}
	slices.SortFunc(out, func(x, y OrigenTocado) int {
		if c := y.Toques - x.Toques; c != 0 {
			return c
		}
		// Tercer criterio para que el orden sea DETERMINISTA, por lo mismo que
		// en PorOrigen: sin él la tabla baila entre recargas sin que nada haya
		// cambiado.
		return strings.Compare(x.IP.String(), y.IP.String())
	})
	return out
}
