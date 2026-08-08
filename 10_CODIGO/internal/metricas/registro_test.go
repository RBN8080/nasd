package metricas

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func registroVacio(t *testing.T) *Registro {
	t.Helper()
	r, err := CargarRegistro(filepath.Join(t.TempDir(), "uso-disco"))
	if err != nil {
		t.Fatalf("CargarRegistro: %v", err)
	}
	return r
}

// Que el archivo no exista es lo normal en un nodo recién instalado, o en
// una cuenta a la que nunca se le ha pulsado «Refrescar métricas»: no puede
// impedir arrancar.
func TestUnRegistroQueNoExisteNoEsUnError(t *testing.T) {
	r, err := CargarRegistro(filepath.Join(t.TempDir(), "no-existe"))
	if err != nil {
		t.Fatalf("CargarRegistro sobre un archivo ausente: %v", err)
	}
	if len(r.Todas()) != 0 {
		t.Errorf("registro ausente con %d medidas", len(r.Todas()))
	}
}

// La primera vez que se mide una cuenta no hay nada con qué comparar:
// Variacion debe quedar en nil, no en 0 — «sin dato previo» y «sin cambios»
// son afirmaciones distintas.
func TestPrimeraMedidaNoTieneVariacion(t *testing.T) {
	r := registroVacio(t)
	if err := r.Actualizar(map[string]int64{"juan": 1000}, time.Now()); err != nil {
		t.Fatalf("Actualizar: %v", err)
	}
	m, ok := r.Todas()["juan"]
	if !ok {
		t.Fatal("la cuenta medida no aparece en Todas()")
	}
	if m.Bytes != 1000 {
		t.Errorf("Bytes = %d; se esperaba 1000", m.Bytes)
	}
	if m.Variacion != nil {
		t.Errorf("Variacion = %d; se esperaba nil en la primera medida", *m.Variacion)
	}
}

// Segunda medida: la variación es la diferencia con signo contra la
// anterior, en los tres casos —crece, encoge, se queda igual—.
func TestActualizarCalculaLaVariacionConSigno(t *testing.T) {
	r := registroVacio(t)
	momento := time.Now()
	if err := r.Actualizar(map[string]int64{
		"crece": 1000, "encoge": 1000, "igual": 1000,
	}, momento); err != nil {
		t.Fatalf("primer Actualizar: %v", err)
	}

	if err := r.Actualizar(map[string]int64{
		"crece": 1500, "encoge": 400, "igual": 1000,
	}, momento.Add(time.Hour)); err != nil {
		t.Fatalf("segundo Actualizar: %v", err)
	}

	casos := map[string]int64{"crece": 500, "encoge": -600, "igual": 0}
	medidas := r.Todas()
	for nombre, esperada := range casos {
		m, ok := medidas[nombre]
		if !ok {
			t.Fatalf("falta la cuenta %q tras la segunda medida", nombre)
		}
		if m.Variacion == nil {
			t.Fatalf("Variacion de %q es nil; se esperaba %d", nombre, esperada)
		}
		if *m.Variacion != esperada {
			t.Errorf("Variacion de %q = %d; se esperaba %d", nombre, *m.Variacion, esperada)
		}
	}
}

// Una cuenta que no viene en el nuevo lote conserva su última medida: un
// barrido que falló para una cuenta (ver refrescarMetricas, panel.go) no
// debe borrar lo que ya se sabía de ella.
func TestActualizarConservaLasCuentasAusentesDelLote(t *testing.T) {
	r := registroVacio(t)
	if err := r.Actualizar(map[string]int64{"ana": 500}, time.Now()); err != nil {
		t.Fatalf("primer Actualizar: %v", err)
	}
	if err := r.Actualizar(map[string]int64{"luis": 200}, time.Now()); err != nil {
		t.Fatalf("segundo Actualizar: %v", err)
	}
	if _, ok := r.Todas()["ana"]; !ok {
		t.Error("la medida de «ana» desapareció al refrescar solo «luis»")
	}
}

// Las medidas sobreviven a un reinicio del servicio: es el requisito que
// distingue esto de un simple caché en memoria (P-4, etapa 3).
func TestElRegistroSobreviveAUnaRecarga(t *testing.T) {
	ruta := filepath.Join(t.TempDir(), "uso-disco")
	uno, err := CargarRegistro(ruta)
	if err != nil {
		t.Fatalf("CargarRegistro: %v", err)
	}
	if err := uno.Actualizar(map[string]int64{"ana": 12345}, time.Now()); err != nil {
		t.Fatalf("Actualizar: %v", err)
	}

	dos, err := CargarRegistro(ruta)
	if err != nil {
		t.Fatalf("recargar: %v", err)
	}
	m, ok := dos.Todas()["ana"]
	if !ok || m.Bytes != 12345 {
		t.Errorf("la medida no sobrevivió a la recarga: %+v, ok=%v", m, ok)
	}
}

// Un archivo editado a mano con un campo que no cuadra no puede colarse en
// silencio (P5): se rechaza al cargar, igual que autenticacion.Registro con
// un nombre inválido.
func TestUnRegistroDeMetricasCorruptoNoSeCarga(t *testing.T) {
	ruta := filepath.Join(t.TempDir(), "uso-disco")
	if err := os.WriteFile(ruta, []byte("juan:no-es-un-numero:-:2026-08-07T10:00:00Z\n"), 0o600); err != nil {
		t.Fatalf("escribir: %v", err)
	}
	if _, err := CargarRegistro(ruta); err == nil {
		t.Fatal("cargó un registro con bytes no numéricos")
	}
}
