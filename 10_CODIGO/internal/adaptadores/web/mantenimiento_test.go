package web

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"nasd/internal/almacen"
)

// escritorFalso registra si se le pidió SOLTAR o DESCARTAR.
//
// La distinción es el corazón de estos arreglos: soltar libera el
// descriptor y conserva el parcial; descartar lo destruye. Confundirlas
// borraría datos que el usuario esperaba reanudar.
type escritorFalso struct {
	soltado    bool
	descartado bool
}

func (e *escritorFalso) Write(p []byte) (int, error) { return len(p), nil }
func (e *escritorFalso) Confirmar() error            { return nil }
func (e *escritorFalso) Descartar() error            { e.descartado = true; return nil }
func (e *escritorFalso) Soltar() error               { e.soltado = true; return nil }
func (e *escritorFalso) Escrito() int64              { return 0 }

// Regresión del hallazgo 1: 20 subidas abandonadas dejaban 20 descriptores
// abiertos que no se liberaban nunca.
func TestDesalojoLiberaSinDestruir(t *testing.T) {
	r := nuevoRegistroDeSubidas()

	viejo := &escritorFalso{}
	nuevo := &escritorFalso{}
	r.guardar("viejo", &subidaEnCurso{escritor: viejo})
	r.guardar("nuevo", &subidaEnCurso{escritor: nuevo})

	// Se envejece la primera a mano.
	r.mu.Lock()
	r.m["viejo"].ultimoUso = time.Now().Add(-time.Hour)
	r.mu.Unlock()

	if n := r.desalojarInactivas(10 * time.Minute); n != 1 {
		t.Fatalf("desalojadas = %d; se esperaba 1", n)
	}
	if !viejo.soltado {
		t.Error("la subida inactiva debía SOLTARSE para liberar el descriptor")
	}
	if viejo.descartado {
		t.Error("DESTRUIDA: el desalojo jamás debe descartar el parcial; " +
			"seguiría siendo reanudable")
	}
	if nuevo.soltado || nuevo.descartado {
		t.Error("se tocó una subida activa")
	}
	if _, ok := r.buscar("viejo"); ok {
		t.Error("la subida desalojada sigue en memoria")
	}
	if _, ok := r.buscar("nuevo"); !ok {
		t.Error("desapareció una subida activa")
	}
}

// buscar() debe refrescar el uso: una subida que recibe bytes no se desaloja.
func TestBuscarRefrescaElUso(t *testing.T) {
	r := nuevoRegistroDeSubidas()
	e := &escritorFalso{}
	r.guardar("x", &subidaEnCurso{escritor: e})

	r.mu.Lock()
	r.m["x"].ultimoUso = time.Now().Add(-time.Hour)
	r.mu.Unlock()

	r.buscar("x") // llega un PATCH

	if n := r.desalojarInactivas(10 * time.Minute); n != 0 {
		t.Fatalf("se desalojó una subida recién usada (%d)", n)
	}
}

func TestCuantasCuenta(t *testing.T) {
	r := nuevoRegistroDeSubidas()
	if r.cuantas() != 0 {
		t.Fatal("un registro nuevo debe estar vacío")
	}
	for _, id := range []string{"a", "b", "c"} {
		r.guardar(id, &subidaEnCurso{escritor: &escritorFalso{}})
	}
	if r.cuantas() != 3 {
		t.Errorf("cuantas() = %d; se esperaban 3", r.cuantas())
	}
	r.borrar("b")
	if r.cuantas() != 2 {
		t.Errorf("cuantas() tras borrar = %d; se esperaban 2", r.cuantas())
	}
}

// almacenFalso permite probar la expiración sin tocar disco.
type almacenFalso struct {
	parciales []almacen.Parcial
	borrados  []string
}

func (a *almacenFalso) Reanudables(context.Context) ([]almacen.Parcial, error) {
	return a.parciales, nil
}
func (a *almacenFalso) BorrarParcial(_ context.Context, id string) error {
	a.borrados = append(a.borrados, id)
	return nil
}

// Regresión del hallazgo 3: ADR-0029 estaba decidido y sin construir.
//
// Y comprueba la regla que más importa de aquel ADR: se expira por
// INACTIVIDAD, no por antigüedad. Una subida de 5 GB puede durar días.
func TestExpiraPorInactividadYNoTocaLoVivo(t *testing.T) {
	ahora := time.Now()
	af := &almacenFalso{parciales: []almacen.Parcial{
		{ID: "reciente", Escrito: 100, Modificado: ahora.Add(-time.Hour)},
		{ID: "olvidado", Escrito: 200, Modificado: ahora.Add(-30 * 24 * time.Hour)},
		{ID: "enmemoria", Escrito: 300, Modificado: ahora.Add(-30 * 24 * time.Hour)},
	}}

	s := servidorDePrueba()
	s.subidas.guardar("enmemoria", &subidaEnCurso{escritor: &escritorFalso{}})

	s.expirarParciales(context.Background(), af)

	if len(af.borrados) != 1 || af.borrados[0] != "olvidado" {
		t.Fatalf("borrados = %v; se esperaba solo [olvidado]", af.borrados)
	}
}

func registroSilencioso() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

// servidorDePrueba arma el mínimo de un Servidor sin pasar por Nuevo, que
// exige credencial y plantillas.
//
// Existe para que las piezas obligatorias se monten en UN SOLO SITIO. Al
// añadir los contadores de la Fase 4, la prueba de expiración empezó a
// reventar con un pánico de puntero nulo porque construía el Servidor a mano:
// un literal de prueba no se entera de que la estructura ha crecido.
func servidorDePrueba() *Servidor {
	return &Servidor{
		reg:               registroSilencioso(),
		subidas:           nuevoRegistroDeSubidas(),
		contadores:        nuevosContadores(),
		veredictosPrevios: make(map[string]veredicto),
	}
}

// Regresión del defecto A, detectado en la SEGUNDA iteración de revisión.
//
// El barrido llamaba a buscar() para saber si un parcial estaba vivo, y
// buscar() refresca el uso. Cada pasada rejuvenecía las entradas, así que el
// desalojo no se disparaba NUNCA: la fuga seguía existiendo, solo que topada
// por el límite de concurrencia. El propio control que protege lo vivo
// mataba la limpieza.
func TestExisteNoRefrescaElUso(t *testing.T) {
	r := nuevoRegistroDeSubidas()
	r.guardar("x", &subidaEnCurso{escritor: &escritorFalso{}})

	r.mu.Lock()
	r.m["x"].ultimoUso = time.Now().Add(-time.Hour)
	r.mu.Unlock()

	if !r.existe("x") {
		t.Fatal("existe() debía encontrarla")
	}
	// Tras consultarla con existe(), debe seguir siendo desalojable.
	if n := r.desalojarInactivas(10 * time.Minute); n != 1 {
		t.Fatalf("desalojadas = %d; existe() rejuveneció la entrada y "+
			"el desalojo no se dispararía nunca", n)
	}
}

// Regresión del defecto B: dos PATCH simultáneos sobre una subida
// desalojada abrían DOS descriptores en O_APPEND sobre el mismo archivo.
func TestRecuperacionConcurrenteAbreUnaSolaVez(t *testing.T) {
	r := nuevoRegistroDeSubidas()

	var aperturas atomic.Int32
	abrir := func() (*subidaEnCurso, error) {
		aperturas.Add(1)
		time.Sleep(2 * time.Millisecond) // simula el coste de abrir el archivo
		return &subidaEnCurso{escritor: &escritorFalso{}}, nil
	}

	var wg sync.WaitGroup
	vistos := make([]*subidaEnCurso, 20)
	for i := range vistos {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s, err := r.obtenerOAbrir("mismo", abrir)
			if err != nil {
				t.Error(err)
				return
			}
			vistos[i] = s
		}(i)
	}
	wg.Wait()

	if n := aperturas.Load(); n != 1 {
		t.Fatalf("el parcial se abrió %d veces; debía abrirse UNA. "+
			"Varios descriptores en O_APPEND sobre el mismo archivo "+
			"entrelazarían los bytes", n)
	}
	for i, v := range vistos {
		if v != vistos[0] {
			t.Fatalf("la goroutine %d obtuvo una subida distinta", i)
		}
	}
}
