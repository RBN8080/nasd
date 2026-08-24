package seguridad

import (
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// LAS BARANDILLAS SON LA PIEZA. Un bloqueo mal puesto no da un error: deja
// fuera a alguien, y puede ser a quien lo está poniendo.

func listaDePrueba(t *testing.T) *Lista {
	t.Helper()
	l, err := CargarLista(filepath.Join(t.TempDir(), "lista"))
	if err != nil {
		t.Fatalf("CargarLista: %v", err)
	}
	return l
}

// entradaDe compone lo que compondría el panel a partir de un prefijo.
func entradaDe(prefijo, motivo string) Entrada {
	return Entrada{
		Alcance:  AlcanceRango,
		Etiqueta: prefijo,
		Tramos:   []Tramo{TramoDe(netip.MustParsePrefix(prefijo))},
		Motivo:   motivo,
		Autor:    "raiz",
	}
}

// EL CASO MEDIDO QUE ORIGINÓ TODO ESTO (IDEAS §28.3): un /48 cubría UNA de las
// once redes de AS44382, y el /44 las cubre todas. Si el tramo no se calcula
// bien, el bloqueo parece puesto y no bloquea.
func TestElAlcanceDeUnPrefijoEsElMedidoContraElNodo(t *testing.T) {
	corto := TramoDe(netip.MustParsePrefix("2602:fa5d::/48"))
	largo := TramoDe(netip.MustParsePrefix("2602:fa5d::/44"))

	for _, c := range []struct {
		ip                  string
		en48, en44Comprueba bool
	}{
		{"2602:fa5d::8b", true, true},
		{"2602:fa5d:1::1", false, true},
		{"2602:fa5d:5::dead", false, true},
		{"2602:fa5d:9:ffff::1", false, true},
		{"2602:fa5d:10::1", false, false}, // fuera del /44: exige su propio /48
	} {
		ip := netip.MustParseAddr(c.ip)
		if got := corto.Contiene(ip); got != c.en48 {
			t.Errorf("/48 contiene %s = %v; se esperaba %v", c.ip, got, c.en48)
		}
		if got := largo.Contiene(ip); got != c.en44Comprueba {
			t.Errorf("/44 contiene %s = %v; se esperaba %v", c.ip, got, c.en44Comprueba)
		}
	}
}

// Un tramo de IPv4 tiene que comportarse igual, y no es gratis: dentro se
// trabaja en 16 bytes, donde un /24 es en realidad un /120.
func TestUnPrefijoIPv4TambienSeConvierteBien(t *testing.T) {
	t4 := TramoDe(netip.MustParsePrefix("203.0.113.0/24"))
	if !t4.Contiene(netip.MustParseAddr("203.0.113.255")) {
		t.Error("el último de un /24 quedó fuera de su propio tramo")
	}
	if t4.Contiene(netip.MustParseAddr("203.0.114.0")) {
		t.Error("el tramo de un /24 se desbordó al siguiente")
	}
}

func TestSinMotivoNoSeBloquea(t *testing.T) {
	l := listaDePrueba(t)
	_, err := l.Anadir(entradaDe("203.0.113.0/24", "   "), netip.Addr{}, time.Now())
	if !errors.Is(err, ErrSinMotivo) {
		t.Errorf("aceptó un bloqueo sin motivo: %v", err)
	}
	if len(l.Vigentes(time.Now())) != 0 {
		t.Error("y además lo guardó")
	}
}

// LA BARANDILLA QUE IMPIDE EL DESASTRE MÁS CARO: bloquear la casa.
func TestNoSePuedeBloquearLaCasaNiElTunel(t *testing.T) {
	// El IPv6 de casa se APRENDE al arrancar, así que la barandilla tiene que
	// mirarlo también — es la lección que ya costó una hora en ClasificarRed.
	AprenderRedesPropias([]netip.Addr{netip.MustParseAddr("3fff:2a0:101e:3d82::38")})
	t.Cleanup(func() { AprenderRedesPropias(nil) })

	for _, prefijo := range []string{
		"192.168.1.0/24",          // la LAN entera
		"192.168.1.38/32",         // el propio nodo
		"192.168.0.0/16",          // un tramo mayor que la contiene
		"10.77.0.0/24",            // el túnel
		"127.0.0.1/32",            // el bucle local
		"3fff:2a0:101e:3d82::/64", // el IPv6 de casa, aprendido
		"::/0",                    // todo Internet, que también incluye la casa
	} {
		l := listaDePrueba(t)
		_, err := l.Anadir(entradaDe(prefijo, "un motivo cualquiera"), netip.Addr{}, time.Now())
		if !errors.Is(err, ErrTocaLaCasa) {
			t.Errorf("%s: se aceptó bloquear la casa (%v)", prefijo, err)
		}
	}
}

// LA SEGUNDA BARANDILLA: no dejarse fuera uno mismo.
func TestNoSePuedeBloquearLaRedDesdeLaQueSeEstaMirando(t *testing.T) {
	l := listaDePrueba(t)
	yo := netip.MustParseAddr("2602:fa5d:1::99")

	_, err := l.Anadir(entradaDe("2602:fa5d::/44", "reputación del operador"), yo, time.Now())
	if !errors.Is(err, ErrTeDejaFuera) {
		t.Errorf("se aceptó un bloqueo que deja fuera a quien lo pide: %v", err)
	}

	// Y desde otra red, el MISMO bloqueo entra sin problema.
	otro := netip.MustParseAddr("198.51.100.4")
	if _, err := l.Anadir(entradaDe("2602:fa5d::/44", "reputación del operador"), otro, time.Now()); err != nil {
		t.Errorf("el mismo bloqueo desde otra red falló: %v", err)
	}
}

func TestUnaEntradaBloqueaYCuentaLoQueFrena(t *testing.T) {
	l := listaDePrueba(t)
	ahora := time.Now()
	if _, err := l.Anadir(entradaDe("2602:fa5d::/44", "barrido del 16/08"), netip.Addr{}, ahora); err != nil {
		t.Fatalf("Anadir: %v", err)
	}

	dentro := netip.MustParseAddr("2602:fa5d:5::dead")
	fuera := netip.MustParseAddr("2a06:4883:5000::65")

	// MIRAR NO CUENTA — la mitad que antes no se podía comprobar porque no
	// existía: Bloquea decidía y sumaba a la vez, así que «Frenados» eran
	// coincidencias con la política y no conexiones cerradas.
	id, ok := l.Cubre(dentro, ahora)
	if !ok {
		t.Fatal("no cubre a quien cae dentro del tramo")
	}
	for range 1000 {
		l.Cubre(dentro, ahora)
	}
	if v := l.Vigentes(ahora); v[0].Frenados != 0 {
		t.Fatalf("consultar la política subió el contador: %d", v[0].Frenados)
	}
	if _, ok := l.Cubre(fuera, ahora); ok {
		t.Error("cubre a quien está fuera del tramo")
	}

	for range 3 {
		l.AnotarCierre(id, ahora)
	}
	v := l.Vigentes(ahora)
	if len(v) != 1 || v[0].Frenados != 3 {
		t.Errorf("frenados = %+v; se esperaban 3", v)
	}
	if v[0].UltimoFrenado.IsZero() {
		t.Error("se contó el cierre y no se guardó cuándo fue")
	}
}

// La caducidad por omisión es lo que impide que la lista envejezca sola.
// Permanente se puede, pero hay que elegirlo.
func TestUnaEntradaCaducadaDejaDeBloquear(t *testing.T) {
	l := listaDePrueba(t)
	ahora := time.Now()

	e := entradaDe("2602:fa5d::/44", "temporal")
	e.Caduca = ahora.Add(24 * time.Hour)
	if _, err := l.Anadir(e, netip.Addr{}, ahora); err != nil {
		t.Fatalf("Anadir: %v", err)
	}

	dentro := netip.MustParseAddr("2602:fa5d:5::dead")
	if _, ok := l.Cubre(dentro, ahora); !ok {
		t.Fatal("no bloqueó estando vigente")
	}
	despues := ahora.Add(25 * time.Hour)
	if _, ok := l.Cubre(dentro, despues); ok {
		t.Error("sigue bloqueando después de caducar")
	}
	if len(l.Vigentes(despues)) != 0 {
		t.Error("una entrada caducada sigue contando como vigente")
	}

	// Y una SIN caducidad no caduca nunca, que es lo que «permanente»
	// significa y por eso hay que elegirlo a mano.
	if _, err := l.Anadir(entradaDe("198.51.100.0/24", "permanente"), netip.Addr{}, ahora); err != nil {
		t.Fatalf("Anadir permanente: %v", err)
	}
	if _, ok := l.Cubre(netip.MustParseAddr("198.51.100.7"), ahora.Add(10*365*24*time.Hour)); !ok {
		t.Error("una entrada sin caducidad dejó de bloquear")
	}
}

func TestRetirarQuitaLaEntrada(t *testing.T) {
	l := listaDePrueba(t)
	ahora := time.Now()
	e, err := l.Anadir(entradaDe("2602:fa5d::/44", "prueba"), netip.Addr{}, ahora)
	if err != nil {
		t.Fatalf("Anadir: %v", err)
	}
	if err := l.Retirar(e.ID); err != nil {
		t.Fatalf("Retirar: %v", err)
	}
	if _, ok := l.Cubre(netip.MustParseAddr("2602:fa5d:5::dead"), ahora); ok {
		t.Error("sigue bloqueando tras retirarla")
	}
	if err := l.Retirar(e.ID); !errors.Is(err, ErrNoSeEncontro) {
		t.Errorf("retirar dos veces la misma entrada: %v", err)
	}
}

// El motivo escrito es lo que hace legible la lista dentro de seis meses, así
// que tiene que sobrevivir al disco igual que el alcance.
func TestLaListaSobreviveAlArranqueConSuMotivo(t *testing.T) {
	ruta := filepath.Join(t.TempDir(), "lista")
	ahora := time.Now()

	uno, err := CargarLista(ruta)
	if err != nil {
		t.Fatalf("CargarLista: %v", err)
	}
	e := entradaDe("2602:fa5d::/44", "reputación del ASN, no conducta observada aquí")
	e.Alcance = AlcanceOperador
	e.Etiqueta = "AS44382 · WHITELABEL · US"
	e.BaseGeo = ahora.Add(-30 * 24 * time.Hour)
	if _, err := uno.Anadir(e, netip.Addr{}, ahora); err != nil {
		t.Fatalf("Anadir: %v", err)
	}
	if id, ok := uno.Cubre(netip.MustParseAddr("2602:fa5d:5::dead"), ahora); ok {
		uno.AnotarCierre(id, ahora)
	}
	if err := uno.Volcar(); err != nil {
		t.Fatalf("Volcar: %v", err)
	}

	otra, err := CargarLista(ruta)
	if err != nil {
		t.Fatalf("CargarLista tras reiniciar: %v", err)
	}
	v := otra.Vigentes(ahora)
	if len(v) != 1 {
		t.Fatalf("entradas tras reiniciar = %d; se esperaba 1", len(v))
	}
	if v[0].Motivo == "" {
		t.Error("el motivo escrito no sobrevivió al disco")
	}
	if v[0].Alcance != AlcanceOperador {
		t.Errorf("el alcance no sobrevivió: %v", v[0].Alcance)
	}
	if v[0].Frenados != 1 {
		t.Errorf("frenados tras reiniciar = %d; se esperaba 1", v[0].Frenados)
	}
	if _, ok := otra.Cubre(netip.MustParseAddr("2602:fa5d:5::dead"), ahora); !ok {
		t.Error("tras reiniciar dejó de bloquear")
	}
}

// E3 — EL ALCANCE «OPERADOR» SON VARIOS TRAMOS, Y ESA ES SU RAZÓN DE SER.
//
// El caso medido (IDEAS §28.3): un /48 cubría 1 de los 11 rangos de AS44382.
// Una entrada de operador que solo guardara uno nacería inútil.
func TestUnaEntradaDeOperadorCubreTodosSusTramos(t *testing.T) {
	l := listaDePrueba(t)
	ahora := time.Now()

	e := Entrada{
		Alcance:  AlcanceOperador,
		Etiqueta: "AS44382 · WHITELABEL · US",
		Tramos: []Tramo{
			TramoDe(netip.MustParsePrefix("2602:fa5d::/44")),
			TramoDe(netip.MustParsePrefix("2602:fa5d:10::/48")),
		},
		Motivo:  "reputación del ASN, no conducta observada aquí",
		Autor:   "raiz",
		BaseGeo: ahora,
	}
	if _, err := l.Anadir(e, netip.Addr{}, ahora); err != nil {
		t.Fatalf("Anadir: %v", err)
	}
	// Las dos direcciones, cada una de un tramo distinto.
	for _, ip := range []string{"2602:fa5d:5::dead", "2602:fa5d:10::1"} {
		if _, ok := l.Cubre(netip.MustParseAddr(ip), ahora); !ok {
			t.Errorf("%s no quedó cubierta por el operador", ip)
		}
	}
	// Y una vecina que NO es suya sigue fuera: el alcance es el que se decidió,
	// no «todo lo que se parezca».
	if _, ok := l.Cubre(netip.MustParseAddr("2602:fa5d:20::1"), ahora); ok {
		t.Error("el operador cubrió un tramo que no se le dio")
	}
}

// E4 — LA ENTRADA DE OPERADOR ES UNA FOTO, NO UNA REGLA VIVA.
//
// # LA PROPIEDAD QUE ESTA PRUEBA DEFIENDE
//
// Si mañana el operador anuncia un rango nuevo, la entrada creada ayer NO debe
// empezar a bloquearlo. Una regla viva seguiría al operador —y parecería más
// lista— pero cambiaría lo bloqueado SIN QUE NADIE LO DECIDIERA, que es
// exactamente el modo de fallo que este proyecto persigue: una decisión que
// deja de significar lo que se firmó, en silencio.
//
// Se comprueba sobre la entrada YA GUARDADA Y RELEÍDA, no sobre la de memoria:
// lo que hay que demostrar es que el alcance viaja congelado en el archivo y no
// se recompone al arrancar.
func TestLaEntradaDeOperadorNoSigueAlOperador(t *testing.T) {
	ruta := filepath.Join(t.TempDir(), "lista")
	ahora := time.Now()
	baseVieja := ahora.Add(-30 * 24 * time.Hour)

	uno, err := CargarLista(ruta)
	if err != nil {
		t.Fatalf("CargarLista: %v", err)
	}
	// La base de aquel día anunciaba UN solo tramo de este operador.
	if _, err := uno.Anadir(Entrada{
		Alcance:  AlcanceOperador,
		Etiqueta: "AS44382 · WHITELABEL · US",
		Tramos:   []Tramo{TramoDe(netip.MustParsePrefix("2602:fa5d::/44"))},
		Motivo:   "reputación del ASN",
		Autor:    "raiz",
		BaseGeo:  baseVieja,
	}, netip.Addr{}, ahora); err != nil {
		t.Fatalf("Anadir: %v", err)
	}
	if err := uno.Volcar(); err != nil {
		t.Fatalf("Volcar: %v", err)
	}

	// Hoy el operador anuncia además 2602:fa5d:10::/48. La entrada histórica NO
	// debe cubrirlo: no es lo que la persona decidió con la base que tenía.
	otra, err := CargarLista(ruta)
	if err != nil {
		t.Fatalf("CargarLista tras reiniciar: %v", err)
	}
	nueva := netip.MustParseAddr("2602:fa5d:10::1")
	if _, ok := otra.Cubre(nueva, ahora); ok {
		t.Error("la entrada histórica empezó a bloquear un tramo que el operador anunció después")
	}
	// Y lo que SÍ decidió sigue bloqueado, que es la otra mitad: congelar el
	// alcance no puede significar perderlo.
	if _, ok := otra.Cubre(netip.MustParseAddr("2602:fa5d:5::dead"), ahora); !ok {
		t.Error("la entrada histórica dejó de cubrir lo que sí se decidió")
	}

	// E5 — LA FECHA DE LA FOTO SOBREVIVE, que es lo único que permite saber de
	// cuándo es el alcance y decidir si conviene rehacerlo.
	v := otra.Vigentes(ahora)
	if len(v) != 1 {
		t.Fatalf("entradas = %d", len(v))
	}
	if v[0].BaseGeo.IsZero() {
		t.Fatal("la fecha de la base no sobrevivió: la foto envejece en silencio")
	}
	if !v[0].BaseGeo.Truncate(time.Second).Equal(baseVieja.Truncate(time.Second)) {
		t.Errorf("BaseGeo = %v; se esperaba %v", v[0].BaseGeo, baseVieja)
	}
	if v[0].Alcance != AlcanceOperador {
		t.Errorf("el alcance no sobrevivió: %v", v[0].Alcance)
	}
}

// D5/D6 — EL MATCHING, EN LAS DOS FAMILIAS Y CONTRA LAS BARANDILLAS.
//
// Las direcciones de casa se prueban CONTRA Anadir y no contra Cubre: la
// barandilla tiene que impedir que la entrada exista, no confiar en que el
// matching falle después. Es la diferencia entre una regla que no se puede
// poner y una que se pone y no se dispara por suerte.
func TestElMatchingAcierta(t *testing.T) {
	l := listaDePrueba(t)
	ahora := time.Now()
	for _, prefijo := range []string{"203.0.113.0/24", "2602:fa5d::/44"} {
		if _, err := l.Anadir(entradaDe(prefijo, "prueba de matching"), netip.Addr{}, ahora); err != nil {
			t.Fatalf("Anadir %s: %v", prefijo, err)
		}
	}

	for _, c := range []struct {
		ip     string
		dentro bool
	}{
		{"203.0.113.1", true},        // primera del tramo IPv4
		{"203.0.113.255", true},      // última del tramo IPv4
		{"203.0.114.1", false},       // la de al lado, fuera
		{"2602:fa5d::1", true},       // primera del tramo IPv6
		{"2602:fa5d:f:ff::ff", true}, // dentro del /44
		{"2602:fa5d:10::1", false},   // justo fuera del /44
		{"::ffff:203.0.113.1", true}, // IPv4 mapeada: la MISMA dirección
		{"192.168.1.18", false},      // la casa
		{"10.77.0.2", false},         // el túnel
		{"127.0.0.1", false},         // el propio nodo
		{"fe80::1", false},           // local de enlace
	} {
		_, ok := l.Cubre(netip.MustParseAddr(c.ip), ahora)
		if ok != c.dentro {
			t.Errorf("Cubre(%s) = %v; se esperaba %v", c.ip, ok, c.dentro)
		}
	}
}

// EL DEFECTO MEDIDO EN EL NODO EL 2026-08-24, reproducido.
//
// # QUÉ PASABA
//
// El diario decía «la lista de bloqueos no se pudo leer; se empieza vacía» con
// el error «bufio.Scanner: token too long». La causa, medida sobre el archivo
// real: la entrada de «AS13335 · CLOUDFLARENET · US» son **1517 tramos** en una
// sola línea de **102 572 bytes**, y un bufio.Scanner sin configurar admite
// como mucho 64 KB.
//
// El resultado no era un error visible: era un bloqueo que la persona había
// creado, que el archivo conservaba entero, y que **no bloqueaba nada** desde
// el primer reinicio. Es exactamente el modo de fallo que este proyecto
// persigue — una afirmación que deja de ser verdad sin avisar—, y encima sobre
// un control.
//
// Se usan 1600 tramos, por encima de los 1517 reales, para que la prueba siga
// valiendo si esa entrada crece.
func TestUnBloqueoDeOperadorGrandeSobreviveAlArranque(t *testing.T) {
	ruta := filepath.Join(t.TempDir(), "lista")
	ahora := time.Now()

	tramos := make([]Tramo, 0, 1600)
	for i := range 1600 {
		// Direcciones IPv6 largas a propósito: son las que hacen la línea
		// grande, y son las que de verdad tiene la entrada del nodo.
		p := netip.MustParsePrefix("2606:4700:" + strconv.FormatInt(int64(i), 16) + "::/48")
		tramos = append(tramos, TramoDe(p))
	}

	uno, err := CargarLista(ruta)
	if err != nil {
		t.Fatalf("CargarLista: %v", err)
	}
	// Una entrada PEQUEÑA primero: el Scanner se detiene en la línea que no
	// puede leer, así que si el orden se invirtiera se perderían también las de
	// detrás. Aquí se comprueba que no se pierde ninguna de las dos.
	if _, err := uno.Anadir(entradaDe("203.0.113.0/24", "una pequeña, delante"), netip.Addr{}, ahora); err != nil {
		t.Fatalf("Anadir la pequeña: %v", err)
	}
	if _, err := uno.Anadir(Entrada{
		Alcance:  AlcanceOperador,
		Etiqueta: "AS13335 · CLOUDFLARENET · US",
		Tramos:   tramos,
		Motivo:   "reputación del operador",
		Autor:    "admin",
		BaseGeo:  ahora,
	}, netip.Addr{}, ahora); err != nil {
		t.Fatalf("Anadir la grande: %v", err)
	}
	if err := uno.Volcar(); err != nil {
		t.Fatalf("Volcar: %v", err)
	}

	otra, err := CargarLista(ruta)
	if err != nil {
		t.Fatalf("CargarLista tras reiniciar: %v", err)
	}
	v := otra.Vigentes(ahora)
	if len(v) != 2 {
		t.Fatalf("entradas tras reiniciar = %d; se esperaban 2 — la grande se perdió en silencio", len(v))
	}
	// Y LO QUE DE VERDAD IMPORTA: que siga bloqueando. Una entrada que se relee
	// pero con los tramos recortados sería el mismo defecto con otra cara.
	dentro := tramos[1599].Desde
	if _, ok := otra.Cubre(dentro, ahora); !ok {
		t.Errorf("el último tramo del operador no sobrevivió: %v", dentro)
	}
	if _, ok := otra.Cubre(netip.MustParseAddr("203.0.113.7"), ahora); !ok {
		t.Error("la entrada pequeña se perdió detrás de la grande")
	}
}

// LA OTRA MITAD DE LA CORRECCIÓN: lo que no se pueda releer, no se acepta.
//
// Sin esta barandilla el par escritor/lector puede volver a desajustarse, y el
// síntoma sería otra vez un bloqueo aceptado por el panel que desaparece al
// reiniciar. Un bloqueo o entra y funciona, o se niega diciendo por qué.
func TestNoSeAceptaUnBloqueoQueNoSePodriaVolverALeer(t *testing.T) {
	l := listaDePrueba(t)
	ahora := time.Now()

	// Bastantes tramos para pasarse del millón de bytes por línea.
	enorme := make([]Tramo, 0, 20000)
	for i := range 20000 {
		p := netip.MustParsePrefix("2606:4700:" + strconv.FormatInt(int64(i), 16) + "::/48")
		enorme = append(enorme, TramoDe(p))
	}

	_, err := l.Anadir(Entrada{
		Alcance:  AlcanceOperador,
		Etiqueta: "un operador enorme",
		Tramos:   enorme,
		Motivo:   "reputación",
		Autor:    "admin",
	}, netip.Addr{}, ahora)
	if !errors.Is(err, ErrDemasiadosTramos) {
		t.Fatalf("Anadir devolvió %v; se esperaba ErrDemasiadosTramos", err)
	}
	if len(l.Vigentes(ahora)) != 0 {
		t.Error("se guardó una entrada que no se podría volver a leer")
	}
}

// UNA LÍNEA ILEGIBLE NO PUEDE HACER QUE EL DIARIO MIENTA.
//
// El mensaje que había —«se empieza vacía»— apareció en el nodo con OCHO
// bloqueos cargados y vigentes, porque CargarLista devuelve lo leído JUNTO al
// error. Quien leyera el diario habría creído que el nodo estaba sin defensa
// manual mientras el panel enseñaba ocho entradas.
func TestElErrorDeLecturaDiceCuantasSobrevivieron(t *testing.T) {
	ruta := filepath.Join(t.TempDir(), "lista")
	ahora := time.Now()

	uno, err := CargarLista(ruta)
	if err != nil {
		t.Fatalf("CargarLista: %v", err)
	}
	if _, err := uno.Anadir(entradaDe("203.0.113.0/24", "la buena"), netip.Addr{}, ahora); err != nil {
		t.Fatalf("Anadir: %v", err)
	}
	if err := uno.Volcar(); err != nil {
		t.Fatalf("Volcar: %v", err)
	}
	// Se añade a mano una línea más larga de lo que se puede releer. No se
	// puede provocar por la vía normal —Anadir ya lo impide— y por eso se
	// escribe directamente en el archivo: lo que se prueba es qué dice el nodo
	// ante un archivo que YA venía así, que es el caso real del 24/08.
	f, err := os.OpenFile(ruta, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("abrir para añadir: %v", err)
	}
	larga := `{"id":"x","tramos":[` +
		strings.Repeat(`{"desde":"::1","hasta":"::2"},`, 60000) +
		`{"desde":"::1","hasta":"::2"}]}` + "\n"
	if _, err := f.WriteString(larga); err != nil {
		t.Fatalf("escribir la línea larga: %v", err)
	}
	f.Close()

	otra, err := CargarLista(ruta)
	if err == nil {
		t.Fatal("una línea ilegible pasó sin denunciarse")
	}
	// LO QUE NO PUEDE PASAR: que el error dé a entender que no queda nada.
	if len(otra.Vigentes(ahora)) != 1 {
		t.Fatalf("entradas vigentes = %d; la buena tenía que sobrevivir", len(otra.Vigentes(ahora)))
	}
	if !strings.Contains(err.Error(), "quedan vigentes") {
		t.Errorf("el error no dice cuántas sobrevivieron: %v", err)
	}
}
