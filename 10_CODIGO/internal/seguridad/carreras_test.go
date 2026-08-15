package seguridad

import (
	"net/netip"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// TestAnotarYMirarALaVez existe porque este es el CUARTO estado compartido
// del programa (ADR-0013) y el que más manos toca: lo escribe cada petición
// rechazada, desde su propia goroutine del servidor, mientras el panel del
// superusuario recorre el buffer entero para pintar la página y el ciclo de
// mantenimiento lo vuelca a disco.
//
// Las tres cosas a la vez son el caso real, no uno rebuscado: basta con que
// llegue un sondeo mientras se mira el panel.
//
// Se ejecuta con «make carreras» (-race). Sin el detector esta prueba pasa
// aunque el candado esté mal puesto, así que su valor está ahí.
func TestAnotarYMirarALaVez(t *testing.T) {
	ruta := filepath.Join(t.TempDir(), "seguridad")
	a, err := CargarAnillo(ruta)
	if err != nil {
		t.Fatal(err)
	}

	const escritores = 8
	const porEscritor = 500

	var wg sync.WaitGroup
	for range escritores {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range porEscritor {
				a.Anotar(Evento{
					Momento: time.Now(),
					Origen:  netip.MustParseAddr("203.0.113.7"),
					Metodo:  "GET",
					Ruta:    "/wp-login.php",
					Estado:  401,
					Motivo:  RutaInexistente,
					Agente:  "zgrab/0.x",
				})
			}
		}()
	}

	// Un lector que hace lo mismo que hará el panel.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 200 {
			_ = a.Desde(time.Now().Add(-24 * time.Hour))
			_ = a.Total()
		}
	}()

	// Y el volcado periódico, que es quien compone dentro del candado y
	// escribe fuera.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 20 {
			if err := a.Volcar(); err != nil {
				t.Errorf("volcar: %v", err)
				return
			}
		}
	}()

	wg.Wait()

	if got := a.Total(); got != escritores*porEscritor {
		t.Fatalf("total = %d, se esperaban %d: se perdieron anotaciones",
			got, escritores*porEscritor)
	}
}
