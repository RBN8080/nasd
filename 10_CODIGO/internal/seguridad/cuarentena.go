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
	"sync"
	"time"

	"nasd/internal/atomico"
)

// Cuarentena — la respuesta automática por CONDUCTA OBSERVADA.
//
// # QUÉ PROBLEMA RESUELVE, Y CUÁL NO
//
// El panel de seguridad enseña quién toca el nodo, pero mirar exige que
// alguien mire. Un sondeo a las tres de la mañana se ve a la mañana siguiente,
// y para entonces ya pasó. Esto es lo único de este trabajo que actúa sin que
// nadie esté delante.
//
// NO es la defensa del nodo. La defensa son la postura TLS (web/tls.go), la
// contención de rutas y la credencial con derivación cara — controles que ya
// rechazaron las veinte direcciones de DRIFTNET del 16/08 sin ayuda de nadie.
// Esto reduce el RUIDO y acota el desgaste, que es un objetivo más modesto y
// conviene no confundirlo con el otro.
//
// # POR QUÉ POR CONDUCTA Y NO POR REPUTACIÓN
//
// Conducta es lo que este nodo VIO hacer; reputación es lo que un tercero dice
// del vecindario. La primera se justifica sola dentro de dos años y con el
// diario delante; la segunda envejece y hay que renovarla. La lista manual
// (lista.go) es el sitio de las decisiones por reputación, y allí exige un
// motivo escrito por una persona.
//
// # SE APARTA LA DIRECCIÓN, NUNCA EL RANGO
//
// Un automatismo no elige alcance. Apartar un /64 por conducta observada en
// UNA dirección es una inferencia sobre las otras 18 trillones, y el día que
// se equivoque dejará fuera al responsable desde la red móvil de un hotel.
// Ampliar el alcance es una decisión con consecuencias, y por eso vive en la
// lista manual, donde una persona la firma y la puede deshacer.
//
// # QUÉ SIGUE VIÉNDOSE DE UN APARTADO
//
// Todo menos lo que deje de ocurrir. La conexión se anota ANTES de cerrarse
// (web/seguridad.go), así que el apartado sigue apareciendo en el panel y su
// cuenta de frenados sube. Lo que ya no se ve son sus PETICIONES, porque deja
// de haberlas — y ese es exactamente el trato: se pierde el detalle de lo que
// pedía a cambio de que deje de pedirlo. Es una pérdida mucho menor que la de
// bloquear en el cortafuegos, donde no quedaría ni la conexión.

const (
	// DuracionCuarentena — cuánto dura un apartado, contado desde el último
	// hecho que lo justifica.
	//
	// 24 h y no una hora: el patrón medido en este nodo son barridos separados
	// por días (13/08 y 16/08), no ráfagas de minutos. Y no una semana, porque
	// una dirección de un operador rotatorio deja de ser la misma persona
	// mucho antes: pasado un día, seguir cerrándole la puerta es castigar a un
	// desconocido distinto.
	DuracionCuarentena = 24 * time.Hour

	// VentanaCuarentena es cuánto historial se mira en cada evaluación.
	//
	// Es más corta que la duración a propósito: se pregunta «¿está haciendo
	// esto AHORA?», no «¿lo hizo alguna vez?». Con una ventana larga, un
	// apartado ya caducado volvería a apartarse solo por hechos viejos que
	// siguen en el anillo, y no saldría nunca.
	VentanaCuarentena = time.Hour

	// umbralAccionFuerzaBruta — fallos de contraseña que hacen falta para
	// apartar, frente a los 5 que hacen falta para ENSEÑAR la señal.
	//
	// VER UNA SEÑAL Y ACTUAR SOBRE ELLA SON DOS PREGUNTAS DISTINTAS, y la
	// segunda exige más evidencia porque su coste es dejar a alguien fuera 24
	// horas. Los 5 de umbralFuerzaBruta son «esto merece una mirada»; estos 20
	// son «esto ya no es un despiste».
	//
	// El número sale del limitador, no del gusto: sesion.go corta a 5 intentos
	// por ventana de 5 minutos, así que llegar a 20 exige insistir durante
	// unos veinte minutos contra una puerta que ya está cerrada. Una persona
	// que no recuerda su contraseña no hace eso; deja de intentarlo y pregunta.
	// Importa porque hay una segunda persona usando este NAS desde fuera de
	// casa, y apartarla a las 24 horas por teclear mal cinco veces sería
	// convertir una protección en una avería.
	umbralAccionFuerzaBruta = 20

	// topeCuarentena acota cuántas direcciones caben a la vez.
	//
	// La cota existe porque esto crece con lo que haga un extraño, no con lo
	// que haga el responsable — la única clase de estado del programa que un
	// tercero puede empujar. Lleno, deja de apartar y no crece: perder un
	// apartado es barato, quedarse sin memoria en un nodo de 592 MB no.
	topeCuarentena = 256
)

// Apartado es una dirección a la que se le cierra la puerta, con por qué y
// hasta cuándo.
type Apartado struct {
	IP netip.Addr `json:"ip"`
	// Senal es la conducta que lo produjo. Se guarda por su clave estable
	// (Senal.String), no por su etiqueta.
	Senal Senal     `json:"senal"`
	Desde time.Time `json:"desde"`
	Hasta time.Time `json:"hasta"`
	// Frenados son las conexiones EFECTIVAMENTE CERRADAS, no las veces que
	// esta dirección coincidió con la política.
	//
	// La distinción no es pedante: hasta que se separó decisión de ejecución,
	// esta cifra subía ANTES del net.Conn.Close() y seguía subiendo aunque el
	// cierre fallara. Ahora solo la toca AnotarCierre, a la que no se llega
	// sin un Close() que haya devuelto nil. Ver Cubre y puerta.go.
	//
	// Es lo que permite retirar lo que no sirve: un apartado con cero
	// frenados no protegió de nada, y sin esta cifra nadie podría saberlo.
	//
	// NO SON PETICIONES. Después de cerrar la conexión no hay petición que
	// contar, así que esta columna y la de rechazos miden cosas distintas y no
	// se pueden comparar entre sí.
	Frenados int64 `json:"frenados"`
	// UltimoFrenado es cuándo sirvió por última vez. Mismo campo y mismo
	// motivo que en Entrada (lista.go): sin él, «esto acaba de frenar algo» no
	// se distingue de «esto frenó algo alguna vez».
	UltimoFrenado time.Time `json:"ultimo_frenado,omitzero"`
}

// Vigente dice si el apartado sigue en pie.
func (a Apartado) Vigente(ahora time.Time) bool { return ahora.Before(a.Hasta) }

// Cuarentena guarda los apartados vigentes, en memoria y respaldados por un
// archivo en /var/lib/nasd/ — el mismo StateDirectory y la misma escritura
// atómica que el anillo de rechazos (ADR-0024).
//
// # POR QUÉ ESTO SÍ SE PERSISTE Y EL LIMITADOR DE INTENTOS NO
//
// Los dos frenan a quien insiste, y solo uno baja a disco. El limitador de
// sesion.go vive en memoria y muere en cada arranque —15 en 14 días medidos—,
// lo que regala 5 intentos por reinicio: contra una contraseña de 14
// caracteres con 600 000 iteraciones de PBKDF2, eso no mueve la aguja, y
// persistir un mapa que cambia cada segundo sería estado nuevo sin requisito
// que lo pida. Esto dura 24 horas y su razón de ser es sobrevivir a la noche;
// un apartado que se olvida al reiniciar no habría servido de nada.
type Cuarentena struct {
	mu        sync.Mutex
	ruta      string
	apartados map[netip.Addr]*Apartado
	sucio     bool
}

// CargarCuarentena lee lo apartado que hubiera. Devuelve una cuarentena
// USABLE aunque el archivo esté ilegible, igual que CargarAnillo: quedarse sin
// esta pieza cuesta la respuesta automática, no el servicio.
func CargarCuarentena(ruta string) (*Cuarentena, error) {
	c := &Cuarentena{ruta: ruta, apartados: make(map[netip.Addr]*Apartado)}
	if err := c.releer(); err != nil {
		return c, err
	}
	return c, nil
}

func (c *Cuarentena) releer() error {
	f, err := os.Open(c.ruta)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("abrir la cuarentena %q: %w", c.ruta, err)
	}
	defer f.Close()

	s := bufio.NewScanner(f)
	for s.Scan() {
		linea := s.Bytes()
		if len(linea) == 0 || linea[0] == '#' {
			continue
		}
		var a Apartado
		if err := json.Unmarshal(linea, &a); err != nil {
			// Una línea ilegible no invalida las demás: es un registro de
			// observación, no un libro de cuentas.
			continue
		}
		if !a.IP.IsValid() {
			continue
		}
		c.apartados[a.IP] = &a
	}
	return s.Err()
}

// Evaluar mira los orígenes recientes y aparta a quien lo merezca. Devuelve
// SOLO los apartados NUEVOS, para que quien avise no repita el mismo aviso
// cada minuto mientras la conducta sigue.
//
// Recibe []Origen y no el anillo: quien decide la ventana es quien llama, y
// así esta función se prueba con orígenes escritos a mano.
func (c *Cuarentena) Evaluar(origenes []Origen, ahora time.Time) []Apartado {
	c.mu.Lock()
	defer c.mu.Unlock()

	// La purga va PRIMERO: un apartado caducado tiene que soltarse aunque hoy
	// no haya nada que apartar, y hacerlo aquí evita una tarea de limpieza
	// aparte que pudiera fallar por su cuenta.
	for ip, a := range c.apartados {
		if !a.Vigente(ahora) {
			delete(c.apartados, ip)
			c.sucio = true
		}
	}

	var nuevos []Apartado
	for _, o := range origenes {
		// Doble comprobación deliberada: PorOrigen ya solo emite señales desde
		// Internet, pero de esta lista sale una ACCIÓN, y que la casa quede
		// fuera no puede depender de lo que haga otra función.
		if !o.Red.DeFuera() {
			continue
		}
		senal, hay := senalQueAparta(o)
		if !hay {
			continue
		}
		if a, ya := c.apartados[o.IP]; ya {
			// Renovar, no volver a avisar: mientras siga, sigue apartado.
			a.Hasta = ahora.Add(DuracionCuarentena)
			a.Senal = senal
			c.sucio = true
			continue
		}
		if len(c.apartados) >= topeCuarentena {
			continue
		}
		a := &Apartado{
			IP:    o.IP,
			Senal: senal,
			Desde: ahora,
			Hasta: ahora.Add(DuracionCuarentena),
		}
		c.apartados[o.IP] = a
		c.sucio = true
		nuevos = append(nuevos, *a)
	}
	return nuevos
}

// senalQueAparta traduce «tiene señal» a «hay que apartarlo», que NO es la
// misma pregunta.
//
// # POR QUÉ SenalSoftwareAjeno NO APARTA
//
// Es la única de las tres que dispara con UN solo hecho —una ruta ajena—, y su
// propia Explicacion termina diciendo que es «el rastreo indiscriminado que
// recibe cualquier dirección pública, no algo dirigido a este nodo». ADR-0065
// ya le quitó el énfasis en el panel por ese motivo: una alerta cuyo texto dice
// que no es nada gasta la credibilidad de las que sí importan.
//
// Actuar sobre ella sería lo contrario de aquella decisión: la acción más
// fuerte disparada por la señal más débil, y encima con un umbral de UNO
// cuando las otras dos piden 8 y 20. Subirle el umbral tampoco vale: aquel
// mismo comentario lo descartó porque fijar un número sin tráfico medido es
// adivinar.
//
// En la práctica se pierde poco: un escáner que pide /wp-login.php pide
// también decenas de rutas que no existen, así que cae por exploración a las
// ocho. Lo que se evita es apartar a quien pidió UNA cosa y se fue.
func senalQueAparta(o Origen) (Senal, bool) {
	for _, s := range o.Senales {
		switch s {
		case SenalExploracion:
			return s, true
		case SenalFuerzaBruta:
			if o.PorMotivo[CredencialIncorrecta] >= umbralAccionFuerzaBruta {
				return s, true
			}
		}
	}
	return SenalExploracion, false
}

// Cubre dice si a esta dirección le corresponde que se le cierre la puerta.
//
// # NO CUENTA NADA, Y ESA ES TODA LA CORRECCIÓN
//
// Esto se llamaba Frena y hacía las dos cosas: decidía Y sumaba un frenado. El
// problema no era de estilo, era que volvía FALSO el propio panel. La
// secuencia real era:
//
//	coincide  ->  Frenados++  ->  net.Conn.Close()  ->  ...falla
//
// y el panel afirmaba igualmente haber frenado una conexión que seguía viva.
// Peor: quien poseía el net.Conn —el ConnState de web/seguridad.go— era el
// único que podía saber si el cierre ocurrió, y para cuando lo sabía la cifra
// ya estaba escrita.
//
// Ahora son tres actos separados, y el orden lo impone el tipo Cierre
// (puerta.go): decidir, ejecutar, contar. Contar es AnotarCierre, y solo se
// llega a ella pasando por un Close() que devolvió nil.
func (c *Cuarentena) Cubre(ip netip.Addr, ahora time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	a, hay := c.apartados[ip]
	return hay && a.Vigente(ahora)
}

// AnotarCierre cuenta UNA conexión que ya se cerró de verdad.
//
// Que la dirección haya podido caducar entre la decisión y el cierre no es un
// error y no se avisa: el cierre ocurrió y ya no hay apartado al que
// atribuirlo. Es una carrera de microsegundos que solo puede perder una
// unidad de una cifra de observación; inventar aquí un apartado para que
// cuadre sería lo contrario de lo que este contador existe para decir.
func (c *Cuarentena) AnotarCierre(ip netip.Addr, ahora time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	a, hay := c.apartados[ip]
	if !hay {
		return
	}
	a.Frenados++
	a.UltimoFrenado = ahora
	c.sucio = true
}

// Vigentes devuelve los apartados en pie, del más reciente al más antiguo.
func (c *Cuarentena) Vigentes(ahora time.Time) []Apartado {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Apartado, 0, len(c.apartados))
	for _, a := range c.apartados {
		if a.Vigente(ahora) {
			out = append(out, *a)
		}
	}
	slices.SortFunc(out, func(x, y Apartado) int { return y.Desde.Compare(x.Desde) })
	return out
}

// Soltar retira un apartado antes de tiempo. Devuelve si había algo que
// soltar, para que quien llame pueda decir la verdad en vez de dar por hecho
// que hizo algo.
func (c *Cuarentena) Soltar(ip netip.Addr) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, hay := c.apartados[ip]; !hay {
		return false
	}
	delete(c.apartados, ip)
	c.sucio = true
	return true
}

// Mantener vuelca cada minuto y una última vez al cerrarse el canal. Misma
// forma y mismo canal de parada que los dos anillos (main.go).
func (c *Cuarentena) Mantener(hecho <-chan struct{}, alFallar func(error)) {
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

// Vigilar evalúa la conducta reciente cada minuto y aparta a quien lo merezca.
//
// # POR QUÉ CADA MINUTO Y NO EN CADA RECHAZO
//
// Evaluar recorre los orígenes de la última hora, y hacerlo dentro de la
// petición pondría ese recorrido en el camino de cada 4xx — justo cuando quien
// lo dispara es alguien que está insistiendo. Sería regalarle al atacante el
// trabajo, que es el mismo motivo por el que el anillo no hace fsync por
// evento. Un minuto de retraso no cambia nada: el limitador ya frena los
// primeros cinco intentos por su cuenta y en el acto.
//
// # POR QUÉ LA MISMA CADENCIA QUE EL VOLCADO
//
// Para que un apartado nunca viva más de un ciclo sin estar en disco. Con dos
// ritmos distintos habría una ventana en la que el nodo aparta a alguien, se
// reinicia y lo olvida — que es precisamente lo que esta pieza existe para
// evitar.
//
// # POR QUÉ EL OBSERVADOR RECIBE LOS ORÍGENES Y NO SOLO LOS APARTADOS
//
// Porque desde ADR-0073 hay un SEGUNDO interesado en el mismo cálculo: la capa
// de avisos necesita exactamente los mismos []Origen para decidir qué merece
// salir del nodo.
//
// PorOrigen recorre la ventana entera, agrupa por dirección y deriva las
// señales de cada una. Hacerlo dos veces por minuto en un A53 —una para apartar
// y otra para avisar— sería pagar dos veces por el mismo trabajo, y encima
// dejaría abierta la posibilidad de que las dos lecturas vieran ventanas
// distintas y el panel contara una cosa y el aviso otra.
//
// Se calcula UNA vez y se reparte. El observador se llama SIEMPRE, aunque no
// haya apartados nuevos: quien decide si hay algo que avisar es él, no esto.
func Vigilar(hecho <-chan struct{}, a *Anillo, c *Cuarentena, observar func(origenes []Origen, nuevos []Apartado, ahora time.Time)) {
	t := time.NewTicker(intervaloVolcado)
	defer t.Stop()
	for {
		select {
		case <-hecho:
			return
		case ahora := <-t.C:
			origenes := PorOrigen(a.Desde(ahora.Add(-VentanaCuarentena)))
			// EL ORDEN IMPORTA Y ES ESTE: primero se APARTA, después se avisa.
			// Es la jerarquía del proyecto cumplida en dos líneas —ENFORCEMENT
			// antes que NOTIFICACIÓN, nunca al revés— y tiene además una
			// consecuencia práctica: el observador ve los apartados que ACABAN
			// de aplicarse, así que el aviso puede decir «cuarentena aplicada»
			// en vez de «se va a aplicar».
			nuevos := c.Evaluar(origenes, ahora)
			if observar != nil {
				observar(origenes, nuevos, ahora)
			}
		}
	}
}

// Volcar escribe la cuarentena entera, de forma atómica.
func (c *Cuarentena) Volcar() error {
	c.mu.Lock()
	if !c.sucio {
		c.mu.Unlock()
		return nil
	}
	orden := make([]Apartado, 0, len(c.apartados))
	for _, a := range c.apartados {
		orden = append(orden, *a)
	}
	c.sucio = false
	c.mu.Unlock()

	slices.SortFunc(orden, func(x, y Apartado) int { return x.Desde.Compare(y.Desde) })

	err := atomico.Escribir(c.ruta, ".cuarentena-*", func(w io.Writer) error {
		fmt.Fprint(w, "# Cuarentena automática por conducta — una dirección por línea, en JSON.\n")
		fmt.Fprint(w, "# La pone el nodo solo y caduca sola. Se suelta desde /seguridad.\n")
		enc := json.NewEncoder(w)
		for _, a := range orden {
			if err := enc.Encode(a); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		// Si no se pudo escribir, lo de memoria sigue siendo lo bueno y hay
		// que volver a intentarlo en el siguiente ciclo.
		c.mu.Lock()
		c.sucio = true
		c.mu.Unlock()
	}
	return err
}

// MarshalJSON y UnmarshalJSON de Senal existen para que el archivo guarde la
// CLAVE ESTABLE y no el número del enum.
//
// Con el número, insertar una señal nueva en medio del bloque const
// reinterpretaría en silencio todo lo ya escrito — el mismo defecto que Motivo
// evita con String/MotivoDesde desde el primer día.
func (s Senal) MarshalJSON() ([]byte, error) { return json.Marshal(s.String()) }

func (s *Senal) UnmarshalJSON(b []byte) error {
	var clave string
	if err := json.Unmarshal(b, &clave); err != nil {
		return err
	}
	v, ok := SenalDesde(clave)
	if !ok {
		return fmt.Errorf("señal desconocida: %q", clave)
	}
	*s = v
	return nil
}
