// Package metricas guarda, bajo demanda, el último uso de disco medido de
// cada cuenta — P-4, etapa 3 (00_RECTOR.md §7.quinquies).
//
// # POR QUÉ NO ES COMO internal/autenticacion
//
// autenticacion.Registro se relee por huella (Refrescar) porque «nasd
// --crear-usuario» lo escribe desde OTRO PROCESO mientras el servicio sigue
// arriba. Aquí no existe ese segundo escritor: la única puerta para tocar
// este archivo es el botón «Refrescar métricas», servido por este mismo
// proceso. La copia en memoria no puede quedarse vieja respecto de nadie
// más, así que no hace falta releer nunca después de arrancar.
//
// Por el mismo motivo se omite heredarDuenoDe (comparar con
// autenticacion/sincronizar_unix.go): ese paso existe para el caso en que el
// registro se escribe con «sudo» —como root— y lo lee el servicio —como
// nas—. Aquí no hay «sudo nasd --algo» que escriba este archivo: el dueño ya
// es correcto siempre, porque quien escribe es siempre el servicio.
package metricas

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"nasd/internal/atomico"
)

// Medida es el último uso de disco calculado para una cuenta.
type Medida struct {
	Bytes int64
	// Variacion es la diferencia con la medida anterior, con signo. nil si
	// esta fue la primera vez que se midió esa cuenta: 0 y «sin cambios» no
	// son lo mismo que «no había nada con qué comparar», y confundirlos
	// mentiría sobre lo que se sabe (mismo criterio que los punteros de
	// estado.go: «no disponible» no es cero).
	Variacion *int64
	Momento   time.Time
}

// Registro guarda la última medida de cada cuenta, en memoria y respaldada
// por un archivo de texto en /var/lib/nasd/ (igual que autenticacion.Registro,
// ADR-0055).
type Registro struct {
	mu      sync.Mutex
	ruta    string
	medidas map[string]Medida
}

// CargarRegistro lee el archivo. Que NO exista no es un error: un nodo
// recién instalado, o una cuenta a la que nunca se le ha pulsado
// «Refrescar métricas», simplemente no tiene medidas todavía.
func CargarRegistro(ruta string) (*Registro, error) {
	r := &Registro{ruta: ruta}
	if err := r.releer(); err != nil {
		return nil, err
	}
	return r, nil
}

// releer NO PISA LO QUE HAY EN MEMORIA hasta haber leído todo bien, igual
// que autenticacion.Registro: un archivo a medio escribir o tocado a mano no
// debe borrar medidas que ya eran válidas.
func (r *Registro) releer() error {
	f, err := os.Open(r.ruta)
	if errors.Is(err, os.ErrNotExist) {
		r.medidas = nil
		return nil
	}
	if err != nil {
		return fmt.Errorf("abrir el registro de métricas %q: %w", r.ruta, err)
	}
	defer f.Close()

	leidas := make(map[string]Medida)
	s := bufio.NewScanner(f)
	for n := 1; s.Scan(); n++ {
		linea := strings.TrimSpace(s.Text())
		if linea == "" || strings.HasPrefix(linea, "#") {
			continue
		}
		// SplitN con límite 4: el momento va en RFC3339, que lleva sus
		// propios «:» (la hora y el desplazamiento de zona). Partir por
		// TODOS los «:» rompería esa parte.
		partes := strings.SplitN(linea, ":", 4)
		if len(partes) != 4 {
			return fmt.Errorf("registro de métricas, línea %d: se esperaban 4 campos separados por «:»", n)
		}
		nombre, sBytes, sVariacion, sMomento := partes[0], partes[1], partes[2], partes[3]

		bytes, err := strconv.ParseInt(sBytes, 10, 64)
		if err != nil {
			return fmt.Errorf("registro de métricas, línea %d, cuenta %q: bytes inválidos: %w", n, nombre, err)
		}
		m := Medida{Bytes: bytes}
		if sVariacion != "-" {
			v, err := strconv.ParseInt(sVariacion, 10, 64)
			if err != nil {
				return fmt.Errorf("registro de métricas, línea %d, cuenta %q: variación inválida: %w", n, nombre, err)
			}
			m.Variacion = &v
		}
		momento, err := time.Parse(time.RFC3339, sMomento)
		if err != nil {
			return fmt.Errorf("registro de métricas, línea %d, cuenta %q: fecha inválida: %w", n, nombre, err)
		}
		m.Momento = momento
		leidas[nombre] = m
	}
	if err := s.Err(); err != nil {
		return fmt.Errorf("leer el registro de métricas: %w", err)
	}
	r.medidas = leidas
	return nil
}

// Todas devuelve una copia de la última medida de cada cuenta. Una sola
// función que entrega el lote entero, y no una consulta por cuenta, porque
// quien la usa —verAdministracion— pinta la tabla entera de una vez y no
// necesita 32 candados distintos para hacerlo.
func (r *Registro) Todas() map[string]Medida {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]Medida, len(r.medidas))
	maps.Copy(out, r.medidas)
	return out
}

// Actualizar publica una nueva medida por cuenta, DE UNA SOLA VEZ, y calcula
// la variación de cada una contra lo que hubiera antes.
//
// Un único archivo para todo el lote, no uno por cuenta ni una escritura por
// cuenta: la misma razón que ADR-0024 en otro terreno — un corte de
// corriente a mitad de un barrido de hasta 32 cuentas no debe dejar el
// archivo con solo la mitad publicada.
func (r *Registro) Actualizar(nuevas map[string]int64, momento time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	actualizadas := make(map[string]Medida, len(r.medidas)+len(nuevas))
	maps.Copy(actualizadas, r.medidas)
	for nombre, bytes := range nuevas {
		m := Medida{Bytes: bytes, Momento: momento}
		if previa, ok := r.medidas[nombre]; ok {
			v := bytes - previa.Bytes
			m.Variacion = &v
		}
		actualizadas[nombre] = m
	}

	previas := r.medidas
	r.medidas = actualizadas
	if err := r.guardar(); err != nil {
		r.medidas = previas
		return err
	}
	return nil
}

// guardar escribe el archivo entero de forma ATÓMICA — los cuatro pasos de
// ADR-0024, que desde el 2026-08-25 viven en internal/atomico.
//
// ANTES ESTABAN ESCRITOS A MANO AQUÍ, y era la segunda copia de la misma
// danza: la otra vivía en internal/seguridad. Con internal/aviso llegando como
// tercer llamador, mantener tres copias significaba que arreglar un paso en una
// dejaría las otras dos rotas hasta el siguiente corte de corriente.
func (r *Registro) guardar() error {
	// Orden alfabético y no de inserción: a diferencia de
	// autenticacion.Registro —que guarda un slice para conservar el orden
	// del alta, como un registro cronológico legible—, esto es una caché de
	// medición sin ningún orden «natural» que preservar. Alfabético es
	// determinista —dos publicaciones seguidas con los mismos datos
	// producen el mismo archivo— y es el más fácil de revisar a mano.
	nombres := make([]string, 0, len(r.medidas))
	for nombre := range r.medidas {
		nombres = append(nombres, nombre)
	}
	sort.Strings(nombres)

	return atomico.Escribir(r.ruta, ".uso-disco-*", func(w io.Writer) error {
		var b strings.Builder
		b.WriteString("# Uso de disco por cuenta, medido bajo demanda — P-4, etapa 3.\n")
		b.WriteString("# Una cuenta por línea: nombre:bytes:variación:momento-RFC3339.\n")
		b.WriteString("# variación es «-» si esa cuenta no tenía medida previa.\n")
		for _, nombre := range nombres {
			m := r.medidas[nombre]
			b.WriteString(nombre)
			b.WriteByte(':')
			b.WriteString(strconv.FormatInt(m.Bytes, 10))
			b.WriteByte(':')
			if m.Variacion == nil {
				b.WriteByte('-')
			} else {
				b.WriteString(strconv.FormatInt(*m.Variacion, 10))
			}
			b.WriteByte(':')
			b.WriteString(m.Momento.Format(time.RFC3339))
			b.WriteByte('\n')
		}
		_, err := io.WriteString(w, b.String())
		return err
	})
}
