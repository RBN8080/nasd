package seguridad

import (
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func evento(momento time.Time, ip string, m Motivo) Evento {
	return Evento{
		Momento: momento,
		Origen:  netip.MustParseAddr(ip),
		Metodo:  "GET",
		Ruta:    "/",
		Estado:  401,
		Motivo:  m,
	}
}

// TestElAnilloNoCreceNunca es la prueba de la única promesa que hace esta
// estructura: la retención está acotada POR CONSTRUCCIÓN, no por una tarea de
// limpieza que pueda fallar.
func TestElAnilloNoCreceNunca(t *testing.T) {
	a := &Anillo{buf: make([]Evento, Capacidad)}
	base := time.Now()
	for i := range Capacidad * 3 {
		a.Anotar(evento(base.Add(time.Duration(i)*time.Second), "203.0.113.7", SinSesion))
	}

	vivos := a.Desde(time.Time{})
	if len(vivos) != Capacidad {
		t.Fatalf("el anillo guarda %d eventos y su capacidad es %d", len(vivos), Capacidad)
	}
	// Y el total NO se confunde con lo que cabe: sin esta cifra, un anillo
	// lleno se leería como si eso fuera todo lo que ha pasado.
	if a.Total() != int64(Capacidad*3) {
		t.Fatalf("total = %d, se esperaban %d", a.Total(), Capacidad*3)
	}
}

// TestDesdeDevuelveDelMasRecienteAlMasAntiguo fija el orden, que es el de una
// vista cronológica de seguridad y no el de inserción.
func TestDesdeDevuelveDelMasRecienteAlMasAntiguo(t *testing.T) {
	a := &Anillo{buf: make([]Evento, Capacidad)}
	base := time.Date(2026, 8, 14, 10, 0, 0, 0, time.UTC)
	for i := range 5 {
		a.Anotar(evento(base.Add(time.Duration(i)*time.Minute), "203.0.113.7", SinSesion))
	}

	got := a.Desde(time.Time{})
	if len(got) != 5 {
		t.Fatalf("se esperaban 5 eventos, hay %d", len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i].Momento.After(got[i-1].Momento) {
			t.Fatalf("el evento %d es posterior al %d: el orden no es descendente", i, i-1)
		}
	}
}

// TestDesdeCortaEnLaVentana comprueba el filtro temporal, que es lo que hace
// posible el «últimas 24 horas» del panel sin recorrer el anillo entero.
func TestDesdeCortaEnLaVentana(t *testing.T) {
	a := &Anillo{buf: make([]Evento, Capacidad)}
	ahora := time.Now()
	a.Anotar(evento(ahora.Add(-48*time.Hour), "203.0.113.7", SinSesion))
	a.Anotar(evento(ahora.Add(-30*time.Hour), "203.0.113.7", SinSesion))
	a.Anotar(evento(ahora.Add(-2*time.Hour), "203.0.113.7", SinSesion))
	a.Anotar(evento(ahora.Add(-1*time.Minute), "203.0.113.7", SinSesion))

	got := a.Desde(ahora.Add(-24 * time.Hour))
	if len(got) != 2 {
		t.Fatalf("en las últimas 24 h se esperaban 2 eventos, hay %d", len(got))
	}
}

// TestElHistorialSobreviveAUnReinicio es la razón de existir de la
// persistencia: los contadores de web/contadores.go se ponen a cero al
// reiniciar, y con P-11 el nodo se reinicia solo. Si esto no sobreviviera,
// el panel tendría el mismo defecto que viene a corregir.
func TestElHistorialSobreviveAUnReinicio(t *testing.T) {
	ruta := filepath.Join(t.TempDir(), "seguridad")
	a, err := CargarAnillo(ruta)
	if err != nil {
		t.Fatalf("cargar: %v", err)
	}
	base := time.Date(2026, 8, 14, 10, 0, 0, 0, time.UTC)
	a.Anotar(Evento{
		Momento: base,
		Origen:  netip.MustParseAddr("203.0.113.7"),
		Metodo:  "POST",
		Ruta:    "/acceso",
		Estado:  401,
		Motivo:  CredencialIncorrecta,
		Agente:  "curl/8.5.0",
		Cuenta:  "admin",
	})
	if err := a.Volcar(); err != nil {
		t.Fatalf("volcar: %v", err)
	}

	otro, err := CargarAnillo(ruta)
	if err != nil {
		t.Fatalf("recargar: %v", err)
	}
	got := otro.Desde(time.Time{})
	if len(got) != 1 {
		t.Fatalf("tras recargar hay %d eventos, se esperaba 1", len(got))
	}
	e := got[0]
	if e.Motivo != CredencialIncorrecta || e.Cuenta != "admin" ||
		e.Ruta != "/acceso" || e.Agente != "curl/8.5.0" || e.Estado != 401 {
		t.Fatalf("el evento no se conservó íntegro: %+v", e)
	}
	if !e.Momento.Equal(base) {
		t.Fatalf("momento = %v, se esperaba %v", e.Momento, base)
	}
	// La red NO se guarda en disco: se recalcula al leer, para que el archivo
	// y el código no puedan discrepar si cambia un prefijo.
	if e.Red != RedInternet {
		t.Fatalf("red = %v, se esperaba internet", e.Red)
	}
}

// TestUnHistorialRotoNoImpideArrancar fija una decisión que se tomó distinta
// de la de autenticacion, y conviene que quede sujeta: un registro de
// usuarios roto SÍ debe impedir arrancar —deja a gente fuera—, pero un
// historial de observación roto solo cuesta el historial. Cambiar un NAS sano
// por un archivo de registro sería el peor negocio posible.
func TestUnHistorialRotoNoImpideArrancar(t *testing.T) {
	ruta := filepath.Join(t.TempDir(), "seguridad")
	if err := os.WriteFile(ruta, []byte("esto no es JSON\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	a, err := CargarAnillo(ruta)
	if err == nil {
		t.Fatal("un archivo ilegible debe reportarse como error")
	}
	if a == nil {
		t.Fatal("debe devolverse un anillo usable aunque el archivo esté roto")
	}
	a.Anotar(evento(time.Now(), "203.0.113.7", SinSesion))
	if len(a.Desde(time.Time{})) != 1 {
		t.Fatal("el anillo devuelto tras un archivo roto no es usable")
	}
}

// TestLoQueSeGuardaEstaAcotado prueba que el tope de memoria del anillo es
// REAL. Sin truncado, una cabecera de 64 KB —que MaxHeaderBytes admite—
// multiplicada por la capacidad convertiría este registro en el problema.
func TestLoQueSeGuardaEstaAcotado(t *testing.T) {
	a := &Anillo{buf: make([]Evento, Capacidad)}
	a.Anotar(Evento{
		Momento: time.Now(),
		Origen:  netip.MustParseAddr("203.0.113.7"),
		Ruta:    "/" + strings.Repeat("a", 4000),
		Agente:  strings.Repeat("b", 64000),
		Motivo:  RutaInexistente,
	})
	e := a.Desde(time.Time{})[0]
	if len(e.Ruta) > topeRuta {
		t.Fatalf("la ruta guardada mide %d y el tope es %d", len(e.Ruta), topeRuta)
	}
	if len(e.Agente) > topeAgente {
		t.Fatalf("el agente guardado mide %d y el tope es %d", len(e.Agente), topeAgente)
	}
}

// TestTodosLosMotivosSobrevivenAlDisco es la prueba que protege el historial
// ya escrito. El motivo se persiste por su CLAVE y no por su número: sin eso,
// insertar una constante nueva en medio del bloque const reinterpretaría en
// silencio todos los eventos guardados. Esta prueba falla el día que alguien
// añada un motivo y olvide su clave o su recuperación.
//
// # EL TOPE ERA UN LITERAL Y POR ESO NO SIRVIÓ
//
// Decía «m <= PeticionMalformada», así que al añadirse SoloDesdeDentro el
// bucle dejó de llegar al último motivo y esta prueba pasó a comprobar todos
// menos el nuevo — que es justo el que hay que comprobar. El mismo literal
// escrito en opcionesDeMotivo (panel_seguridad.go) dejó ese motivo fuera del
// desplegable de filtros durante cinco días sin que nada fallara.
//
// Ahora el tope es UltimoMotivo y vive en un solo sitio.
func TestTodosLosMotivosSobrevivenAlDisco(t *testing.T) {
	claves := map[string]Motivo{}
	for m := MotivoDesconocido; m <= UltimoMotivo; m++ {
		clave := m.String()
		vuelta, ok := MotivoDesde(clave)
		if !ok {
			t.Fatalf("el motivo %d produce la clave %q, que no se reconoce de vuelta", m, clave)
		}
		if vuelta != m {
			t.Fatalf("el motivo %d vuelve como %d por la clave %q", m, vuelta, clave)
		}
		if m != MotivoDesconocido && clave == "desconocido" {
			t.Fatalf("el motivo %d no tiene clave propia: cae en «desconocido»", m)
		}
		if otro, repetida := claves[clave]; repetida {
			t.Fatalf("los motivos %d y %d comparten la clave %q: el historial los confundiría", m, otro, clave)
		}
		claves[clave] = m
		if m.Etiqueta() == "" {
			t.Fatalf("el motivo %q no tiene etiqueta que enseñar", clave)
		}
	}
	if _, ok := MotivoDesde("un_motivo_de_otra_version"); ok {
		t.Fatal("una clave desconocida no debe darse por buena")
	}
	// Y el tope tiene que ser DE VERDAD el último: si alguien añade un motivo
	// detrás de UltimoMotivo y no mueve la constante, el bucle de arriba no lo
	// vería y esta prueba volvería a mentir como mintió con SoloDesdeDentro.
	if (UltimoMotivo + 1).String() != "desconocido" {
		t.Fatalf("hay un motivo más allá de UltimoMotivo (%q): mueve la constante",
			(UltimoMotivo + 1).String())
	}
}

// EL TOTAL HISTORICO TIENE QUE SOBREVIVIR A UN REINICIO, y hasta el 2026-08-18
// no lo hacia: releer() ponia «total = len(leidos)» y el archivo no guardaba la
// cifra, asi que tras arrancar el panel decia «de N vistas desde que existe
// este registro» con N acotado a Capacidad.
//
// No era teorico: en este nodo se midieron 15 arranques en 14 dias, asi que esa
// frase era falsa casi siempre. Decia «desde el ultimo corte de luz» creyendo
// decir «desde siempre».
func TestElTotalHistoricoSobreviveAUnReinicio(t *testing.T) {
	ruta := filepath.Join(t.TempDir(), "seguridad")
	a, err := CargarAnillo(ruta)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now()
	const vistos = Capacidad + 500
	for i := range vistos {
		a.Anotar(evento(base.Add(time.Duration(i)*time.Millisecond), "203.0.113.7", SinSesion))
	}
	if err := a.Volcar(); err != nil {
		t.Fatal(err)
	}

	otro, err := CargarAnillo(ruta) // el «reinicio»
	if err != nil {
		t.Fatal(err)
	}
	if got := otro.Total(); got != vistos {
		t.Errorf("tras reiniciar, el total dice %d y se habian visto %d", got, vistos)
	}
	// Y lo CONSERVADO sigue siendo el anillo, no el total: son dos cifras
	// distintas y el panel las enseña como tales.
	if got := len(otro.Desde(time.Time{})); got != Capacidad {
		t.Errorf("se conservan %d eventos, deberian ser %d", got, Capacidad)
	}
}

func TestElTotalDeConexionesSobreviveAUnReinicio(t *testing.T) {
	ruta := filepath.Join(t.TempDir(), "conexiones")
	c, err := CargarConexiones(ruta)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now()
	const vistas = Capacidad + 300
	for i := range vistas {
		c.Anotar(netip.MustParseAddr("203.0.113.7"), base.Add(time.Duration(i)*time.Millisecond))
	}
	if err := c.Volcar(); err != nil {
		t.Fatal(err)
	}

	otro, err := CargarConexiones(ruta)
	if err != nil {
		t.Fatal(err)
	}
	if got := otro.Total(); got != vistas {
		t.Errorf("tras reiniciar, el total dice %d y se habian visto %d", got, vistas)
	}
}

// UN ARCHIVO ESCRITO POR LA VERSION ANTERIOR no trae la marca. No es un error:
// se cae a lo unico que se puede afirmar —lo guardado— y desde el primer
// volcado la cifra ya es la real.
//
// El archivo viejo se FABRICA con el serializador de verdad y quitandole la
// linea de la marca, en vez de escribir el JSON a mano: escribirlo a mano ya
// fallo una vez al inventarse los nombres de los campos, y una prueba que se
// cae por su propio andamio no dice nada del codigo.
//
// Sin esta prueba, el arreglo podria haber dejado ilegibles los dos archivos
// que YA existen en el nodo.
func TestUnHistorialSinLaMarcaSigueLeyendose(t *testing.T) {
	ruta := filepath.Join(t.TempDir(), "seguridad")
	a, err := CargarAnillo(ruta)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now()
	for i := range 3 {
		a.Anotar(evento(base.Add(time.Duration(i)*time.Second), "203.0.113.7", SinSesion))
	}
	if err := a.Volcar(); err != nil {
		t.Fatal(err)
	}

	crudo, err := os.ReadFile(ruta)
	if err != nil {
		t.Fatal(err)
	}
	var sinMarca []string
	for _, l := range strings.Split(string(crudo), "\n") {
		if !strings.HasPrefix(l, marcaTotal) {
			sinMarca = append(sinMarca, l)
		}
	}
	if len(sinMarca) == len(strings.Split(string(crudo), "\n")) {
		t.Fatal("el volcado no escribió la marca; esta prueba no probaría nada")
	}
	if err := os.WriteFile(ruta, []byte(strings.Join(sinMarca, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}

	otro, err := CargarAnillo(ruta)
	if err != nil {
		t.Fatalf("un historial anterior a la marca debe seguir leyéndose: %v", err)
	}
	if got := otro.Total(); got != 3 {
		t.Errorf("sin marca, el total debe caer a lo guardado (3) y dice %d", got)
	}
}
