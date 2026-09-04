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

// Operadores recuerda desde qué OPERADORES ha entrado alguien con contraseña.
//
// # PARA QUÉ, Y ES UNA SOLA COSA
//
// Es la barandilla de la cuarentena automática. Desde ADR-0083 basta UN rechazo
// para apartar a un origen de Internet, y ese umbral —que es lo que por fin
// hace que el guardia vea a los escáneres reales de este nodo— dejaría fuera a
// una persona en cuanto abriera la portada sin haber iniciado sesión todavía.
// Lo que impide eso es esto: a un operador desde el que alguien YA entró no se
// le aparta nunca, por muchos rechazos que produzca.
//
// # POR QUÉ EL OPERADOR Y NO LA DIRECCIÓN
//
// Porque la dirección no se repite. Medido en el historial de este nodo: la
// familia entra desde móviles cuyo IPv6 cambia, y DRIFTNET barrió desde dos
// direcciones distintas del mismo AS con tres días de separación. Guardar
// direcciones sería guardar algo que ya no vale la próxima vez; el operador
// dura.
//
// # FRENTE A LA SEÑAL QUE novedades.go DESCARTÓ A PROPÓSITO
//
// Aquel archivo rechazó «acceso correcto desde una red desconocida» con este
// motivo: «hay una segunda persona entrando desde fuera de casa, y se
// encendería por ella hasta que sus redes fueran conocidas». Es el MISMO dato y
// la decisión sigue siendo válida — aquí no se avisa de nada.
//
// La diferencia es para qué se usa: allí serviría para ACUSAR a esa segunda
// persona; aquí sirve para PROTEGERLA. Un operador que consta solo puede
// quitarle trabajo al guardia, nunca dárselo.
//
// # LO QUE ESTO NO ES
//
// No es una lista de permitidos y no abre ninguna puerta: constar aquí no da
// acceso a nada — para entrar sigue haciendo falta la contraseña, con sus 600
// 000 iteraciones de PBKDF2. Lo único que concede es que la cuarentena
// AUTOMÁTICA no actúe sola. Un bloqueo puesto a mano (lista.go) sigue cerrando
// la puerta a quien sea, conste aquí o no: la decisión de una persona manda
// sobre la inferencia de un programa, igual que en puerta.go.
//
// # NO LLEVA TOPE, Y ESO ESTÁ RAZONADO
//
// Los demás almacenes de este paquete lo llevan porque crecen con lo que haga
// un extraño. Este no: una línea solo se escribe tras una contraseña correcta,
// así que llenarlo exige ya haber entrado — y quien ha entrado no necesita
// llenar un archivo para hacer daño. Crece con las redes desde las que la
// familia usa el NAS, que son unas pocas.
type Operadores struct {
	mu     sync.Mutex
	ruta   string
	vistos map[uint32]time.Time
	sucio  bool
}

// DeQuienEs dice de qué operador es una dirección.
//
// # POR QUÉ UNA INTERFAZ Y NO internal/geoip
//
// Porque este paquete es el DOMINIO y no conoce adaptadores: la separación está
// escrita en web/avisos.go —«el país y el operador ... s.geo, que
// internal/seguridad no conoce»— y meter geoip aquí «habría obligado a aquel
// paquete a dejar de ser dominio» (ADR-0014).
//
// Declarando aquí lo que hace falta, la regla se respeta al revés y sin
// excepciones: el dominio dice QUÉ necesita, y quien tiene la base de 577 871
// rangos la satisface sin que este archivo la importe.
//
// El booleano no es un adorno: la base no cubre todas las direcciones, y un
// ASN 0 —«no se sabe de quién es»— no puede tratarse como un operador más.
type DeQuienEs interface {
	ASN(ip netip.Addr) (uint32, bool)
}

// operador es una línea del archivo.
type operador struct {
	ASN uint32 `json:"asn"`
	// Desde es cuándo se vio la primera sesión iniciada desde este operador.
	// Se guarda para poder EXPLICAR por qué a alguien no se le aparta, que es
	// media función de esta pieza.
	Desde time.Time `json:"desde"`
}

// CargarOperadores lee los operadores conocidos. Devuelve algo USABLE aunque el
// archivo esté ilegible, igual que CargarAnillo y CargarCuarentena.
//
// El segundo valor dice si el archivo NO EXISTÍA, para que quien llame pueda
// sembrarlo una sola vez (ver Sembrar). No se hace aquí porque sembrar necesita
// el historial de rechazos y la base de operadores, y este constructor no los
// conoce ni debe.
func CargarOperadores(ruta string) (*Operadores, bool, error) {
	o := &Operadores{ruta: ruta, vistos: make(map[uint32]time.Time)}
	f, err := os.Open(ruta)
	if errors.Is(err, os.ErrNotExist) {
		return o, true, nil
	}
	if err != nil {
		return o, false, fmt.Errorf("abrir los operadores conocidos %q: %w", ruta, err)
	}
	defer f.Close()

	s := bufio.NewScanner(f)
	for s.Scan() {
		linea := s.Bytes()
		if len(linea) == 0 || linea[0] == '#' {
			continue
		}
		var e operador
		if err := json.Unmarshal(linea, &e); err != nil {
			// Una línea ilegible no invalida las demás. Aquí el coste de
			// perderla es que a ese operador se le podría apartar: molesto y
			// reversible desde el panel, nunca una puerta abierta.
			continue
		}
		if e.ASN == 0 {
			continue
		}
		o.vistos[e.ASN] = e.Desde
	}
	return o, false, s.Err()
}

// Anotar deja constancia de que alguien entró con contraseña desde este
// operador. Devuelve si era nuevo, para que quien llame pueda decirlo sin
// suponerlo.
//
// ASN 0 se ignora: significa «la base no sabe de quién es esta dirección», y
// tratarlo como un operador haría constar a TODOS los desconocidos como uno
// solo — es decir, apagaría el guardia entero con una sola sesión.
func (o *Operadores) Anotar(asn uint32, ahora time.Time) bool {
	if asn == 0 {
		return false
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if _, ya := o.vistos[asn]; ya {
		return false
	}
	o.vistos[asn] = ahora
	o.sucio = true
	return true
}

// Conocido dice si desde este operador ha entrado alguien alguna vez.
//
// Un ASN 0 NUNCA es conocido: no saber de quién es una dirección no es un
// motivo para confiar en ella.
func (o *Operadores) Conocido(asn uint32) bool {
	if asn == 0 {
		return false
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	_, hay := o.vistos[asn]
	return hay
}

// Cuantos son los operadores que constan. Para poder decirlo en el panel en vez
// de que la barandilla trabaje sin que se vea.
func (o *Operadores) Cuantos() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.vistos)
}

// Sembrar rellena el archivo la PRIMERA vez, a partir del historial ya escrito.
//
// # POR QUÉ HACE FALTA: LA TRAMPA DEL ARRANQUE EN FRÍO
//
// Sin esto, el día que se estrena la barandilla no consta ningún operador, así
// que el primero en llegar desde fuera —que puede ser perfectamente alguien de
// la familia— produce su primer rechazo, se le aparta a él y a su operador
// entero, y ya no puede llegar a escribir la contraseña que le habría hecho
// constar. La protección solo se activaría para quien ya no la necesita.
//
// # DE DÓNDE SALE EL DATO, SIN INVENTAR NADA
//
// De un hecho que el anillo lleva guardando desde siempre: SesionCaducada. Su
// propio comentario lo dice —«caducada es alguien que ESTUVO»—, y solo se emite
// ante una cookie que este servidor entregó tras validar una contraseña. Es
// decir, es prueba de que por ahí entró alguien.
//
// SesionInvalida NO cuenta, y la diferencia es justo la que evento.go ya
// escribió: una cookie que el gestor no conoce «no prueba que esa sesión
// existiera nunca». Aceptarla dejaría que cualquiera se hiciera constar
// mandándose una cookie inventada, que es exactamente la puerta que esta pieza
// no puede tener.
//
// Devuelve cuántos operadores quedaron sembrados.
func (o *Operadores) Sembrar(eventos []Evento, quien DeQuienEs, ahora time.Time) int {
	if quien == nil {
		return 0
	}
	sembrados := 0
	for _, e := range eventos {
		if e.Motivo != SesionCaducada || !ClasificarRed(e.Origen).DeFuera() {
			continue
		}
		asn, ok := quien.ASN(e.Origen)
		if !ok {
			continue
		}
		// El instante del evento y no «ahora»: lo que se afirma es cuándo se vio
		// la prueba, y esa fecha ya está escrita.
		cuando := e.Momento
		if cuando.IsZero() {
			cuando = ahora
		}
		if o.Anotar(asn, cuando) {
			sembrados++
		}
	}
	return sembrados
}

// Mantener vuelca cada minuto y una última vez al cerrarse el canal. Misma
// forma y mismo canal de parada que los demás almacenes (main.go).
func (o *Operadores) Mantener(hecho <-chan struct{}, alFallar func(error)) {
	t := time.NewTicker(intervaloVolcado)
	defer t.Stop()
	for {
		select {
		case <-hecho:
			if err := o.Volcar(); err != nil {
				alFallar(err)
			}
			return
		case <-t.C:
			if err := o.Volcar(); err != nil {
				alFallar(err)
			}
		}
	}
}

// Volcar escribe los operadores conocidos, de forma atómica (ADR-0024).
func (o *Operadores) Volcar() error {
	o.mu.Lock()
	if !o.sucio {
		o.mu.Unlock()
		return nil
	}
	orden := make([]operador, 0, len(o.vistos))
	for asn, desde := range o.vistos {
		orden = append(orden, operador{ASN: asn, Desde: desde})
	}
	o.sucio = false
	o.mu.Unlock()

	// Ordenado por ASN y no por fecha: así dos volcados del mismo contenido
	// producen el mismo archivo, y un diff dice algo.
	slices.SortFunc(orden, func(x, y operador) int { return int(x.ASN) - int(y.ASN) })

	err := atomico.Escribir(o.ruta, ".operadores-*", func(w io.Writer) error {
		if _, err := fmt.Fprint(w, "# Operadores desde los que alguien ha entrado CON CONTRASEÑA.\n"); err != nil {
			return err
		}
		if _, err := fmt.Fprint(w, "# No abre ninguna puerta: solo impide que la cuarentena automática aparte sola a estas redes.\n"); err != nil {
			return err
		}
		for _, e := range orden {
			linea, err := json.Marshal(e)
			if err != nil {
				return err
			}
			if _, err := fmt.Fprintf(w, "%s\n", linea); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		o.mu.Lock()
		o.sucio = true
		o.mu.Unlock()
		return err
	}
	return nil
}
