package seguridad

import (
	"net/netip"
	"path/filepath"
	"testing"
	"time"
)

// LA MARCA TIENE QUE ENCENDERSE POR LO QUE NO SE HA VISTO Y APAGARSE AL MIRAR.
// Cualquiera de las dos mitades rota la vuelve inútil: si no se enciende, no
// avisa; si no se apaga, se ignora.

func novedadesDePrueba(t *testing.T) *Novedades {
	t.Helper()
	n, err := CargarNovedades(filepath.Join(t.TempDir(), "novedades"))
	if err != nil {
		t.Fatalf("CargarNovedades: %v", err)
	}
	return n
}

func conectado(ip string, primera time.Time) OrigenConectado {
	return OrigenConectado{IP: netip.MustParseAddr(ip), Conexiones: 1, Primera: primera, Ultima: primera}
}

func TestLasCuatroCosasQueEnciendenLaMarca(t *testing.T) {
	ahora := time.Now()
	visto := ahora.Add(-time.Hour)
	nuevo := ahora.Add(-time.Minute)

	for _, c := range []struct {
		nombre     string
		conectados []OrigenConectado
		apartados  []Apartado
		bloqueos   []Entrada
		hallazgos  []Hallazgo
		enciende   bool
	}{
		{"un origen de Internet nunca visto",
			[]OrigenConectado{conectado("203.0.113.7", nuevo)}, nil, nil, nil, true},
		{"un origen ya conocido que vuelve",
			[]OrigenConectado{conectado("203.0.113.7", ahora.Add(-2*time.Hour))}, nil, nil, nil, false},
		{"una cuarentena disparada",
			nil, []Apartado{{IP: netip.MustParseAddr("203.0.113.7"), Desde: nuevo}}, nil, nil, true},
		{"una cuarentena vieja que sigue en pie",
			nil, []Apartado{{IP: netip.MustParseAddr("203.0.113.7"), Desde: ahora.Add(-3 * time.Hour)}}, nil, nil, false},
		{"un bloqueo suyo que acaba de frenar algo",
			nil, nil, []Entrada{{ID: "x", UltimoFrenado: nuevo}}, nil, true},
		{"un bloqueo que no ha frenado nada",
			nil, nil, []Entrada{{ID: "x"}}, nil, false},
		// LA CUARTA, la única que pide ir a arreglar algo en vez de mirar: el
		// nodo respondió con contenido donde no publica nada.
		{"una ruta ajena que el nodo acaba de atender",
			nil, nil, nil, []Hallazgo{{Ruta: "/.git/config", Primera: nuevo, Ultima: nuevo}}, true},
		// Y la mitad que impide que la marca se quede encendida para siempre:
		// una ruta ya conocida que se vuelve a pedir NO es una novedad. Con
		// Ultima en vez de Primera, un escáner insistiendo la dejaría fija.
		{"una ruta ya vista que se vuelve a pedir",
			nil, nil, nil, []Hallazgo{{Ruta: "/.git/config", Primera: ahora.Add(-3 * time.Hour), Ultima: nuevo}}, false},
	} {
		t.Run(c.nombre, func(t *testing.T) {
			n := novedadesDePrueba(t)
			n.Visto(visto)
			n.Recalcular(c.conectados, c.apartados, c.bloqueos, c.hallazgos)
			if encendida := n.Cuantas() > 0; encendida != c.enciende {
				t.Errorf("marca encendida = %v; se esperaba %v", encendida, c.enciende)
			}
		})
	}
}

func TestMirarElPanelApagaLaMarca(t *testing.T) {
	n := novedadesDePrueba(t)
	ahora := time.Now()
	n.Visto(ahora.Add(-time.Hour))
	n.Recalcular([]OrigenConectado{conectado("203.0.113.7", ahora.Add(-time.Minute))}, nil, nil, nil)
	if n.Cuantas() == 0 {
		t.Fatal("no se encendió con un origen nuevo")
	}

	n.Visto(ahora)
	if n.Cuantas() != 0 {
		t.Error("se miró el panel y la marca sigue encendida")
	}
	// Y no vuelve a encenderse con lo MISMO: sin esto, el mismo origen la
	// encendería otra vez en el siguiente ciclo y quedaría fija para siempre.
	n.Recalcular([]OrigenConectado{conectado("203.0.113.7", ahora.Add(-time.Minute))}, nil, nil, nil)
	if n.Cuantas() != 0 {
		t.Error("volvió a encenderse con lo que ya se había visto")
	}
}

// LA RAZÓN DE PERSISTIR LA VISITA: con 15 arranques en 14 días, una marca que
// se olvida al reiniciar se encendería sola cada mañana con lo de siempre.
func TestLaVisitaSobreviveAlArranque(t *testing.T) {
	ruta := filepath.Join(t.TempDir(), "novedades")
	ahora := time.Now()

	uno, err := CargarNovedades(ruta)
	if err != nil {
		t.Fatalf("CargarNovedades: %v", err)
	}
	uno.Visto(ahora)
	if err := uno.Volcar(); err != nil {
		t.Fatalf("Volcar: %v", err)
	}

	otra, err := CargarNovedades(ruta)
	if err != nil {
		t.Fatalf("CargarNovedades tras reiniciar: %v", err)
	}
	// El MISMO origen de antes de la visita no debe encenderla.
	otra.Recalcular([]OrigenConectado{conectado("203.0.113.7", ahora.Add(-time.Hour))}, nil, nil, nil)
	if otra.Cuantas() != 0 {
		t.Error("tras reiniciar, la marca se encendió con lo que ya se había visto")
	}
	// Y uno posterior a la visita sí.
	otra.Recalcular([]OrigenConectado{conectado("198.51.100.4", ahora.Add(time.Minute))}, nil, nil, nil)
	if otra.Cuantas() == 0 {
		t.Error("tras reiniciar dejó de avisar de lo nuevo")
	}
}
