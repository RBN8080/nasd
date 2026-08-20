package seguridad

import (
	"errors"
	"net/netip"
	"path/filepath"
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
	for range 3 {
		if !l.Bloquea(dentro, ahora) {
			t.Fatal("no bloqueó a quien cae dentro del tramo")
		}
	}
	if l.Bloquea(fuera, ahora) {
		t.Error("bloqueó a quien está fuera del tramo")
	}

	v := l.Vigentes(ahora)
	if len(v) != 1 || v[0].Frenados != 3 {
		t.Errorf("frenados = %+v; se esperaban 3", v)
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
	if !l.Bloquea(dentro, ahora) {
		t.Fatal("no bloqueó estando vigente")
	}
	despues := ahora.Add(25 * time.Hour)
	if l.Bloquea(dentro, despues) {
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
	if !l.Bloquea(netip.MustParseAddr("198.51.100.7"), ahora.Add(10*365*24*time.Hour)) {
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
	if l.Bloquea(netip.MustParseAddr("2602:fa5d:5::dead"), ahora) {
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
	uno.Bloquea(netip.MustParseAddr("2602:fa5d:5::dead"), ahora)
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
	if !otra.Bloquea(netip.MustParseAddr("2602:fa5d:5::dead"), ahora) {
		t.Error("tras reiniciar dejó de bloquear")
	}
}
