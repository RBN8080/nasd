package aviso

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"nasd/internal/atomico"
)

// Registro — lo que convierte una tormenta de hechos en un aviso.
//
// # EL PROBLEMA, MEDIDO Y NO SUPUESTO
//
// El 2026-08-16, entre las 11:49 y las 11:54, veinte direcciones de DRIFTNET
// recorrieron el puerto 443 de este nodo. Evaluar produce una situación por
// origen en CADA ciclo de vigilancia, es decir cada minuto: sin esta pieza,
// aquel barrido de cinco minutos habría mandado cien mensajes.
//
// Y el nodo se reinicia mucho: quince veces en catorce días medidos. Un contador
// en memoria se pondría a cero en cada arranque y volvería a avisar de todo lo
// que ya se avisó — por eso esto baja a disco, igual que la cuarentena y por la
// misma razón: su valor está en sobrevivir a la noche.
//
// # LAS TRES REGLAS, Y LA TERCERA ES LA QUE HACE FALTA DE VERDAD
//
//	NUEVA          -> se avisa
//	REPETIDA       -> se absorbe: sube Veces, no se avisa
//	MÁS GRAVE      -> se avisa OTRA VEZ, porque la historia cambió
//
// La tercera es la que permite que una situación crezca sin generar un mensaje
// por evento: un origen que empieza como anomalía verde y acaba disparando
// SenalExploracion produce DOS avisos en toda su vida —el de aparición y el de
// promoción—, no uno por cada minuto que siga insistiendo.
//
// # UNA SITUACIÓN NUNCA DEGRADA
//
// Si la severidad observada baja, se absorbe y la conocida se queda con la más
// alta. Bajar habría permitido un ciclo perverso: rojo, amarillo, rojo, y tres
// avisos donde hubo una sola historia. Lo que cierra una situación es que deje
// de haber evidencia durante VidaSituacion, no que un ciclo la vea más floja.

const (
	// VidaSituacion es cuánto se recuerda una situación sin evidencia nueva.
	//
	// # DE DÓNDE SALE EL NÚMERO, QUE NO ES UN GUSTO
	//
	// Tiene que caer entre dos cosas medidas en este nodo:
	//
	//   - Un barrido dura MINUTOS (DRIFTNET: cinco). Todo lo de dentro es la
	//     misma historia y debe ser UN aviso.
	//   - Dos barridos distintos vinieron separados por DÍAS (13/08 y 16/08).
	//     Eso son dos historias y merecen dos avisos.
	//
	// Seis horas está holgadamente entre las dos, y además por encima de la
	// ventana de evaluación de la cuarentena (una hora), de modo que una
	// dirección que se sigue apartando nunca «reaparece» por haberse olvidado
	// mientras aún estaba activa.
	VidaSituacion = 6 * time.Hour

	// intervaloVolcado — cada cuánto baja a disco. EL MISMO minuto que los
	// historiales de internal/seguridad, y deliberadamente no un ritmo propio:
	// con dos cadencias habría una ventana en la que se avisa de algo, el nodo
	// se reinicia y se vuelve a avisar de lo mismo.
	intervaloVolcado = time.Minute

	// topeConocidas acota cuántas situaciones se recuerdan a la vez.
	//
	// La cota existe porque esto crece con lo que haga un EXTRAÑO —cada
	// dirección de Internet que produzca una historia estrena una clave—, que
	// es la única clase de estado que un tercero puede empujar. Es el mismo
	// argumento de topeCuarentena.
	//
	// 512 y no los 256 de aquella: aquí caben además los hallazgos y las
	// averías, y sobre todo tiene que caber TODO lo que la cuarentena pueda
	// apartar. Un tope menor que el suyo dejaría de recordar avisos de
	// direcciones que el nodo sí sigue apartando, y volvería a avisar de ellas.
	//
	// Lleno, deja de admitir claves nuevas y no crece: perder la memoria de un
	// aviso cuesta un mensaje repetido; quedarse sin memoria en un nodo de
	// 592 MB cuesta el servicio.
	topeConocidas = 512
)

// conocida es lo que se recuerda de una situación ya vista.
//
// NO se guarda la situación entera —ni sus hechos, ni su texto—: guardar la
// evidencia aquí sería un segundo sitio donde el historial puede decir algo
// distinto del panel, que es exactamente lo que seguridad/hallazgos.go razonó
// al decidir no duplicar. Lo único que hace falta recordar es qué se dijo ya y
// cuándo.
type conocida struct {
	Clave string `json:"clave"`
	// Severidad es la MÁS ALTA que ya se notificó, no la última observada. Es
	// lo que hace que una promoción avise y una repetición no.
	Severidad Severidad `json:"severidad"`
	Primera   time.Time `json:"primera"`
	Ultima    time.Time `json:"ultima"`
	Veces     int       `json:"veces"`
}

// Registro recuerda qué situaciones se han avisado ya.
//
// # ESTADO COMPARTIDO (ADR-0013)
//
// Lleva mutex por lo mismo que los anillos: lo escribe el ciclo de vigilancia
// cada minuto mientras el ciclo de volcado lo recorre entero. Hoy son la misma
// goroutine en la práctica, pero el volcado se lanza aparte y esa garantía no
// debe depender de cómo esté cableado main.go.
type Registro struct {
	mu        sync.Mutex
	ruta      string
	conocidas map[string]*conocida
	sucio     bool
	// avisadas y absorbidas son para poder DECIR cuánto ruido se está
	// quitando. Sin esas dos cifras, «esto reduce las notificaciones» es una
	// afirmación que nadie puede comprobar.
	avisadas   int64
	absorbidas int64
}

// CargarRegistro lee lo que hubiera. Devuelve un Registro USABLE aunque el
// archivo esté ilegible, igual que CargarAnillo y CargarCuarentena.
//
// EL COSTE DE EMPEZAR VACÍO ESTÁ ACOTADO Y ES EL BUENO: se vuelve a avisar de
// lo que sigue vivo. Lo contrario —negarse a arrancar, o callar por si acaso—
// cambiaría un servicio sano por un archivo, o dejaría al responsable sin
// enterarse de algo que sigue pasando.
func CargarRegistro(ruta string) (*Registro, error) {
	r := &Registro{ruta: ruta, conocidas: make(map[string]*conocida)}
	if err := r.releer(); err != nil {
		return r, err
	}
	return r, nil
}

func (r *Registro) releer() error {
	f, err := os.Open(r.ruta)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("abrir la marca de avisos %q: %w", r.ruta, err)
	}
	defer f.Close()

	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 0, 8*1024), 8*1024)
	for s.Scan() {
		linea := strings.TrimSpace(s.Text())
		if linea == "" || strings.HasPrefix(linea, "#") {
			continue
		}
		var c conocida
		if err := json.Unmarshal([]byte(linea), &c); err != nil {
			// Una línea ilegible no invalida las demás: es una marca de
			// observación, no un libro de cuentas. Mismo criterio que la
			// cuarentena y los hallazgos.
			continue
		}
		if c.Clave == "" || len(r.conocidas) >= topeConocidas {
			continue
		}
		r.conocidas[c.Clave] = &c
	}
	return s.Err()
}

// Absorber es la decisión: de todo lo que está pasando, qué merece un mensaje.
//
// Devuelve SOLO lo que hay que notificar —lo nuevo y lo que ha empeorado—, ya
// enriquecido con la cuenta acumulada y con la primera observación real, para
// que el mensaje pueda decir «4 observaciones desde las 03:12» en vez de fingir
// que acaba de empezar.
func (r *Registro) Absorber(situaciones []Situacion, ahora time.Time) []Situacion {
	r.mu.Lock()
	defer r.mu.Unlock()

	// La purga va PRIMERO, igual que en Cuarentena.Evaluar: una situación
	// caducada tiene que olvidarse aunque hoy no haya nada que absorber, y
	// hacerlo aquí evita una tarea de limpieza aparte que pudiera fallar sola.
	for k, c := range r.conocidas {
		if ahora.Sub(c.Ultima) > VidaSituacion {
			delete(r.conocidas, k)
			r.sucio = true
		}
	}

	var avisar []Situacion
	for _, s := range situaciones {
		k := s.Clave()
		c, hay := r.conocidas[k]
		if !hay {
			if len(r.conocidas) >= topeConocidas {
				// Lleno: no se recuerda, así que la próxima vez se volverá a
				// avisar. Se prefiere repetir un aviso a dejar de darlo.
				continue
			}
			r.conocidas[k] = &conocida{
				Clave: k, Severidad: s.Severidad,
				Primera: s.Primera, Ultima: s.Ultima, Veces: 1,
			}
			r.sucio = true
			r.avisadas++
			s.Veces = 1
			avisar = append(avisar, s)
			continue
		}

		c.Veces++
		if s.Ultima.After(c.Ultima) {
			c.Ultima = s.Ultima
		}
		if s.Primera.Before(c.Primera) {
			c.Primera = s.Primera
		}
		r.sucio = true

		if s.Severidad > c.Severidad {
			// PROMOCIÓN: la historia empeoró y hay que contarlo otra vez.
			c.Severidad = s.Severidad
			r.avisadas++
			s.Veces = c.Veces
			s.Primera = c.Primera
			avisar = append(avisar, s)
			continue
		}
		// Absorbida. El hecho sigue entero en el panel; lo que no hay es un
		// segundo mensaje.
		r.absorbidas++
	}
	return avisar
}

// Cuentas devuelve cuántos avisos se han emitido y cuántas observaciones se han
// absorbido sin emitir nada.
//
// Es lo que permite AFIRMAR que esta capa reduce el ruido, en vez de suponerlo:
// con 500 observaciones absorbidas y 3 avisadas, la cifra habla sola. Lo publica
// el indicador de /estado.
func (r *Registro) Cuentas() (avisadas, absorbidas int64) {
	if r == nil {
		return 0, 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.avisadas, r.absorbidas
}

// Vigentes son las situaciones que el registro recuerda ahora mismo. Solo para
// observación y pruebas: la verdad de los hechos está en internal/seguridad.
func (r *Registro) Vigentes() int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.conocidas)
}

// Mantener vuelca cada minuto y una última vez al cerrarse el canal. Misma
// forma y mismo canal de parada que los historiales de internal/seguridad, para
// que un apagado ordenado no pierda nada por ninguna vía.
func (r *Registro) Mantener(hecho <-chan struct{}, alFallar func(error)) {
	t := time.NewTicker(intervaloVolcado)
	defer t.Stop()
	for {
		select {
		case <-hecho:
			if err := r.Volcar(); err != nil {
				alFallar(err)
			}
			return
		case <-t.C:
			if err := r.Volcar(); err != nil {
				alFallar(err)
			}
		}
	}
}

// Volcar escribe la marca entera, de forma atómica. El contenido se compone
// DENTRO del candado y se escribe FUERA, igual que en los anillos: el fsync de
// un archivo en una tarjeta puede tardar, y sostener el mutex mientras tanto
// bloquearía el ciclo de vigilancia.
func (r *Registro) Volcar() error {
	r.mu.Lock()
	if !r.sucio {
		r.mu.Unlock()
		return nil
	}
	orden := make([]conocida, 0, len(r.conocidas))
	for _, c := range r.conocidas {
		orden = append(orden, *c)
	}
	r.sucio = false
	r.mu.Unlock()

	slices.SortFunc(orden, func(x, y conocida) int { return strings.Compare(x.Clave, y.Clave) })

	err := atomico.Escribir(r.ruta, ".avisos-*", func(w io.Writer) error {
		fmt.Fprint(w, "# Situaciones ya avisadas — una por línea, en JSON.\n")
		fmt.Fprint(w, "# NO es el historial de seguridad: es solo la marca de qué se comunicó ya,\n")
		fmt.Fprint(w, "# para no repetir el mismo aviso tras cada reinicio. Los hechos están en\n")
		fmt.Fprint(w, "# seguridad, conexiones y hallazgos (04_SEGURIDAD §6.bis).\n")
		enc := json.NewEncoder(w)
		for _, c := range orden {
			if err := enc.Encode(c); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		// Lo de memoria sigue siendo lo bueno y hay que reintentarlo en el
		// siguiente ciclo, igual que en la cuarentena y los anillos.
		r.mu.Lock()
		r.sucio = true
		r.mu.Unlock()
	}
	return err
}
