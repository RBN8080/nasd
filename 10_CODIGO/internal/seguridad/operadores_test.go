package seguridad

import (
	"net/netip"
	"path/filepath"
	"testing"
	"time"
)

// LO QUE ESTAS PRUEBAS PROTEGEN, Y NO ES EL MECANISMO.
//
// Bajar el umbral a UN rechazo es trivial de escribir y peligrosísimo de
// calibrar: con él, el guardia por fin ve a los escáneres que este nodo recibe
// de verdad —todos pidieron «/» una sola vez— pero pasa a poder echar a la
// segunda persona que usa el NAS desde fuera de casa. La barandilla no es un
// número: es haber entrado alguna vez con contraseña.
//
// Las direcciones no son inventadas. Son las del historial real del nodo: el
// 2a06:4883/2a06:4884 de DRIFTNET, que barrió desde DOS direcciones con tres
// días entre medias, y el 3fff:2f0 de los móviles de casa, que son los
// ÚNICOS orígenes de Internet que produjeron más de un rechazo en toda la vida
// del nodo. Una regla de «más de uno» los habría echado a ellos y dejado pasar
// a DRIFTNET.

const (
	driftnetUna  = "2a06:4883:5000::65"
	driftnetOtra = "2a06:4884:1000::5"
	familiaMovil = "3fff:2f0:9020:8dc9:fe18:5954:c0b8:a4f3"

	asnDriftnet uint32 = 211298
	asnMovil    uint32 = 64497
)

// deQuienFalso resuelve direcciones a operadores según un mapa escrito a mano.
//
// NO se usa la base real: pesa 50 MB, hay que instalarla aparte y ataría estas
// pruebas a un archivo que no está en el repositorio. Lo que se está probando
// es la POLÍTICA —a quién se aparta y a quién no—, no la búsqueda binaria de
// geoip, que tiene las suyas.
type deQuienFalso map[string]uint32

func (d deQuienFalso) ASN(ip netip.Addr) (uint32, bool) {
	asn, hay := d[ip.String()]
	return asn, hay && asn != 0
}

func operadoresDePrueba(t *testing.T) *Operadores {
	t.Helper()
	o, _, err := CargarOperadores(filepath.Join(t.TempDir(), "operadores-conocidos"))
	if err != nil {
		t.Fatalf("CargarOperadores: %v", err)
	}
	return o
}

// conOperadores arma una cuarentena que ya sabe razonar por operador.
func conOperadores(t *testing.T, mapa deQuienFalso) (*Cuarentena, *Operadores) {
	t.Helper()
	c := cuarentenaDePrueba(t)
	o := operadoresDePrueba(t)
	c.ConOperadores(mapa, o)
	return c, o
}

// UN SOLO RECHAZO BASTA si el operador no ha visto entrar a nadie. Es la regla
// entera, y es lo que la cuarentena no hacía: en toda la vida del nodo no
// apartó a NADIE, porque exigía ocho rutas distintas y los diez orígenes
// hostiles medidos pidieron «/» una vez y se fueron.
func TestUnSoloRechazoApartaAlOperador(t *testing.T) {
	c, _ := conOperadores(t, deQuienFalso{driftnetUna: asnDriftnet})
	ahora := time.Now()

	eventos := []Evento{rechazo(driftnetUna, SinSesion, "/", ahora)}
	nuevos := c.Evaluar(PorOrigen(eventos), ahora)

	if len(nuevos) != 1 {
		t.Fatalf("apartados nuevos = %d; se esperaba 1 con un solo rechazo", len(nuevos))
	}
	if nuevos[0].Senal != SenalOperadorDesconocido {
		t.Errorf("apartado por %v; se esperaba operador desconocido", nuevos[0].Senal)
	}
	if nuevos[0].ASN != asnDriftnet {
		t.Errorf("ASN del apartado = %d; se esperaba %d", nuevos[0].ASN, asnDriftnet)
	}
}

// LA PRUEBA QUE PROTEGE A LA FAMILIA, y la más importante de este archivo.
//
// Tres rechazos de sesión desde el móvil de casa —exactamente lo que hay en el
// historial real— no pueden apartar a nadie si desde ese operador ya entró
// alguien con contraseña. Sin esta barandilla, el umbral de uno los habría
// dejado fuera de forma indefinida.
func TestNoSeApartaAUnOperadorConocido(t *testing.T) {
	c, conocidos := conOperadores(t, deQuienFalso{familiaMovil: asnMovil})
	ahora := time.Now()
	conocidos.Anotar(asnMovil, ahora.Add(-24*time.Hour))

	eventos := []Evento{
		rechazo(familiaMovil, SesionInvalida, "/ver/Telefono B2 2017", ahora),
		rechazo(familiaMovil, SesionInvalida, "/", ahora),
		rechazo(familiaMovil, SesionInvalida, "/", ahora),
	}
	nuevos := c.Evaluar(PorOrigen(eventos), ahora)

	if len(nuevos) != 0 {
		t.Fatalf("se apartó a un operador conocido (%d apartados); es la familia", len(nuevos))
	}
	if _, cubre := c.Cubre(netip.MustParseAddr(familiaMovil), ahora); cubre {
		t.Error("se le cierra la puerta a un operador desde el que ya entró alguien")
	}
}

// ROTAR DE DIRECCIÓN DEJA DE SERVIR, que es la razón entera de apartar por
// operador. DRIFTNET barrió desde ::65 y luego desde otra del mismo AS: con
// apartados por dirección, la segunda entraba como si nada.
func TestOtraDireccionDelMismoOperadorQuedaCubierta(t *testing.T) {
	c, _ := conOperadores(t, deQuienFalso{
		driftnetUna:  asnDriftnet,
		driftnetOtra: asnDriftnet,
	})
	ahora := time.Now()

	c.Evaluar(PorOrigen([]Evento{rechazo(driftnetUna, SinSesion, "/", ahora)}), ahora)

	if _, cubre := c.Cubre(netip.MustParseAddr(driftnetOtra), ahora); !cubre {
		t.Fatal("otra dirección del mismo operador entra como si nada: rotar sigue funcionando")
	}
}

// EL APARTADO POR OPERADOR NO CADUCA, y es lo que el responsable pidió. Un
// operador sigue siendo el mismo dentro de un año, al revés que una dirección
// —el argumento de las 24 h de ADR-0067 vale para aquella y no para este—.
func TestElApartadoPorOperadorNoCaduca(t *testing.T) {
	c, _ := conOperadores(t, deQuienFalso{driftnetUna: asnDriftnet})
	ahora := time.Now()

	c.Evaluar(PorOrigen([]Evento{rechazo(driftnetUna, SinSesion, "/", ahora)}), ahora)

	mucho := ahora.Add(30 * 24 * time.Hour)
	if _, cubre := c.Cubre(netip.MustParseAddr(driftnetUna), mucho); !cubre {
		t.Error("el apartado por operador caducó; se pidió indefinido")
	}
}

// EL FRENADO SE LE APUNTA AL APARTADO QUE EXISTE, no a la dirección que llamó.
//
// Es la mitad que se rompería en silencio: la segunda dirección de un operador
// no tiene apartado propio, así que contar por ella perdería el frenado y el
// panel diría que ese apartado no ha servido de nada — que es justo la cifra
// con la que se decide retirarlo.
func TestElFrenadoSeLeApuntaAlApartadoDelOperador(t *testing.T) {
	c, _ := conOperadores(t, deQuienFalso{
		driftnetUna:  asnDriftnet,
		driftnetOtra: asnDriftnet,
	})
	l := listaDePrueba(t)
	ahora := time.Now()

	c.Evaluar(PorOrigen([]Evento{rechazo(driftnetUna, SinSesion, "/", ahora)}), ahora)

	cierre := Decidir(c, l, netip.MustParseAddr(driftnetOtra), ahora)
	if !cierre.Cierra() {
		t.Fatal("no se decide cerrar a otra dirección del operador apartado")
	}
	cierre.Ejecutado(ahora)

	vigentes := c.Vigentes(ahora)
	if len(vigentes) != 1 {
		t.Fatalf("apartados vigentes = %d; se esperaba 1", len(vigentes))
	}
	if vigentes[0].Frenados != 1 {
		t.Errorf("frenados = %d; se esperaba 1 — el cierre se perdió", vigentes[0].Frenados)
	}
}

// SIN BASE DE OPERADORES, EL GUARDIA ES EL DE ANTES. Es lo que hace el cambio
// reversible quitando una línea de main.go, y lo que mantiene válidas las
// pruebas de conducta que ya existían.
func TestSinBaseDeOperadoresElGuardiaActuaComoAntes(t *testing.T) {
	c := cuarentenaDePrueba(t) // sin ConOperadores
	ahora := time.Now()

	nuevos := c.Evaluar(PorOrigen([]Evento{rechazo(driftnetUna, SinSesion, "/", ahora)}), ahora)
	if len(nuevos) != 0 {
		t.Fatalf("apartó con un solo rechazo y sin base de operadores (%d)", len(nuevos))
	}
}

// NO SABER DE QUIÉN ES UNA DIRECCIÓN NO ES MOTIVO PARA APARTARLA. La base no
// cubre todo Internet, y convertir un hueco de datos en una acción echaría a
// gente por lo que falta, no por lo que hizo.
func TestSinSaberDeQuienEsDecideLaConducta(t *testing.T) {
	c, _ := conOperadores(t, deQuienFalso{}) // la base no cubre nada
	ahora := time.Now()

	nuevos := c.Evaluar(PorOrigen([]Evento{rechazo(driftnetUna, SinSesion, "/", ahora)}), ahora)
	if len(nuevos) != 0 {
		t.Fatalf("apartó a una dirección de operador desconocido (%d)", len(nuevos))
	}
}

func TestLosOperadoresConocidosSobrevivenAlArranque(t *testing.T) {
	ruta := filepath.Join(t.TempDir(), "operadores-conocidos")
	ahora := time.Now()

	o, primeraVez, err := CargarOperadores(ruta)
	if err != nil {
		t.Fatalf("CargarOperadores: %v", err)
	}
	if !primeraVez {
		t.Error("un archivo que no existe tiene que decir que es la primera vez")
	}
	o.Anotar(asnMovil, ahora)
	if err := o.Volcar(); err != nil {
		t.Fatalf("Volcar: %v", err)
	}

	otro, primeraVez, err := CargarOperadores(ruta)
	if err != nil {
		t.Fatalf("CargarOperadores tras el reinicio: %v", err)
	}
	if primeraVez {
		t.Error("el archivo existe y aun así dice que es la primera vez: se sembraría de nuevo")
	}
	if !otro.Conocido(asnMovil) {
		t.Fatal("tras reiniciar, el operador conocido se perdió: el guardia apartaría a la familia")
	}
}

// LA SIEMBRA, que es lo que evita la trampa del arranque en frío: sin ella, el
// día que se estrena la barandilla no consta nadie y el primero en llegar
// —quizá de la familia— se lleva el apartado antes de poder escribir la
// contraseña que le habría hecho constar.
func TestSeSiembranLosOperadoresDeSesionesCaducadas(t *testing.T) {
	o := operadoresDePrueba(t)
	ahora := time.Now()
	mapa := deQuienFalso{familiaMovil: asnMovil, driftnetUna: asnDriftnet}

	// Una sesión caducada PRUEBA que por ahí entró alguien; «sin sesión» no.
	eventos := []Evento{
		rechazo(familiaMovil, SesionCaducada, "/estado", ahora.Add(-time.Hour)),
		rechazo(driftnetUna, SinSesion, "/", ahora.Add(-time.Hour)),
	}
	if n := o.Sembrar(eventos, mapa, ahora); n != 1 {
		t.Fatalf("sembrados = %d; se esperaba 1", n)
	}
	if !o.Conocido(asnMovil) {
		t.Error("la sesión caducada de la familia no sembró su operador")
	}
	if o.Conocido(asnDriftnet) {
		t.Error("un «sin sesión» sembró un operador: eso lo puede provocar cualquiera")
	}
}

// UNA COOKIE INVENTADA NO PUEDE HACERTE CONSTAR. Es la diferencia que evento.go
// ya escribió entre las dos: caducada es alguien que ESTUVO; inválida «no
// prueba que esa sesión existiera nunca». Aceptarla convertiría la barandilla
// en una puerta que se abre sola mandando una cookie cualquiera.
func TestUnaSesionInvalidaNoSiembraOperador(t *testing.T) {
	o := operadoresDePrueba(t)
	ahora := time.Now()

	eventos := []Evento{rechazo(driftnetUna, SesionInvalida, "/", ahora)}
	if n := o.Sembrar(eventos, deQuienFalso{driftnetUna: asnDriftnet}, ahora); n != 0 {
		t.Fatalf("sembrados = %d; una cookie que el gestor no conoce no prueba nada", n)
	}
}

// LA CASA NO SE APARTA NUNCA, pase lo que pase y venga de donde venga la
// política. Es la misma barandilla que Evaluar ya tenía, repetida aquí porque
// ahora hay un camino nuevo que llega a la misma decisión.
func TestLaCasaNoSeApartaNiConLaReglaDelOperador(t *testing.T) {
	c, _ := conOperadores(t, deQuienFalso{"192.168.1.18": 65000})
	ahora := time.Now()

	nuevos := c.Evaluar(PorOrigen([]Evento{rechazo("192.168.1.18", SinSesion, "/", ahora)}), ahora)
	if len(nuevos) != 0 {
		t.Fatalf("se apartó a alguien de casa (%d)", len(nuevos))
	}
}
