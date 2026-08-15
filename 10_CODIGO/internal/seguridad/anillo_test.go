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
func TestTodosLosMotivosSobrevivenAlDisco(t *testing.T) {
	for m := MotivoDesconocido; m <= PeticionMalformada; m++ {
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
		if m.Etiqueta() == "" {
			t.Fatalf("el motivo %q no tiene etiqueta que enseñar", clave)
		}
	}
	if _, ok := MotivoDesde("un_motivo_de_otra_version"); ok {
		t.Fatal("una clave desconocida no debe darse por buena")
	}
}
