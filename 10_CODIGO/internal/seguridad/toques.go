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

const topeLineas = Capacidad * 4

type Historial struct {
	Hay    bool
	Toques []Toque

	Total int64

	Desde time.Time
}

func (h Historial) EnVentana(desde time.Time) []Toque {
	if desde.IsZero() {
		return h.Toques
	}
	out := make([]Toque, 0, len(h.Toques))
	for _, t := range h.Toques {
		if !t.Momento.Before(desde) {
			out = append(out, t)
		}
	}
	return out
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
		primer time.Time
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
		// La profundidad se mide ANTES de descartar lo de casa: lo que interesa
		// es desde cuándo hay registro, no desde cuándo hay registro de fuera.
		// Se toma el mínimo en vez de fiarse de que la primera línea sea la más
		// antigua, porque el orden lo garantiza otro programa.
		if primer.IsZero() || t.Momento.Before(primer) {
			primer = t.Momento
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
	return Historial{Hay: true, Toques: out, Total: total, Desde: primer}, nil
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
