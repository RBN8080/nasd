package web

import (
	"bufio"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"nasd/internal/adaptadores/sistema"
	"nasd/internal/autenticacion"
)

// abrirLectorDePrueba es un lector determinista: no toca el nodo, así que estas
// pruebas miden el reparto y el ciclo de vida, no la lectura de /proc.
func abrirLectorDePrueba() func() marcoVivo {
	return func() marcoVivo {
		return marcoVivo{Nodo: []filaViva{{Clave: "temperatura", Valor: "50.0 °C"}}}
	}
}

// El muestreo NO puede seguir corriendo cuando nadie mira. Es la mitad del
// argumento por el que este flujo es aceptable en un nodo de 1 GB: si el
// bucle sobreviviera a la última desconexión, la pantalla de estado costaría
// cuatro lecturas por segundo para siempre, las mirara alguien o no.
func TestSinEspectadoresElMuestreoSePara(t *testing.T) {
	m := nuevoMuestreador(abrirLectorDePrueba)

	marcos, cancelar := m.suscribir()
	select {
	case <-marcos:
	case <-time.After(2 * time.Second):
		t.Fatal("no llegó ningún marco con un espectador suscrito")
	}

	m.mu.Lock()
	activo := m.activo
	m.mu.Unlock()
	if !activo {
		t.Fatal("con un espectador suscrito el muestreador debe estar activo")
	}

	cancelar()

	// El bucle se entera en su siguiente tic, no en el acto. Se espera a que
	// se apague de verdad en lugar de suponerlo.
	plazo := time.Now().Add(2 * time.Second)
	for time.Now().Before(plazo) {
		m.mu.Lock()
		activo = m.activo
		m.mu.Unlock()
		if !activo {
			return
		}
		time.Sleep(intervaloVivo)
	}
	t.Fatal("el muestreo siguió corriendo después de irse el último espectador")
}

// Y al volver a haber espectadores tiene que arrancar OTRA VEZ. Sin esto, la
// segunda visita a /estado mostraría una página congelada para siempre.
func TestElMuestreoVuelveAArrancar(t *testing.T) {
	m := nuevoMuestreador(abrirLectorDePrueba)

	_, cancelar := m.suscribir()
	cancelar()

	// Se espera a que el bucle anterior se apague del todo, para comprobar el
	// arranque en frío y no que el primero siguiera vivo por casualidad.
	plazo := time.Now().Add(2 * time.Second)
	for time.Now().Before(plazo) {
		m.mu.Lock()
		activo := m.activo
		m.mu.Unlock()
		if !activo {
			break
		}
		time.Sleep(intervaloVivo)
	}

	marcos, cancelar2 := m.suscribir()
	defer cancelar2()
	select {
	case <-marcos:
	case <-time.After(2 * time.Second):
		t.Fatal("el muestreo no volvió a arrancar con un espectador nuevo")
	}
}

// Un espectador que no lee —pestaña en segundo plano, red atascada— NO puede
// congelar a los demás. Por eso el reparto descarta marcos en vez de esperar.
func TestUnEspectadorLentoNoDetieneALosDemas(t *testing.T) {
	m := nuevoMuestreador(abrirLectorDePrueba)

	// El lento se suscribe y jamás lee de su canal.
	_, cancelarLento := m.suscribir()
	defer cancelarLento()

	rapido, cancelarRapido := m.suscribir()
	defer cancelarRapido()

	// Tres marcos seguidos: más que la capacidad del canal del lento, así que
	// si el reparto bloqueara, aquí se notaría.
	for i := range 3 {
		select {
		case <-rapido:
		case <-time.After(2 * time.Second):
			t.Fatalf("el marco %d no llegó: un espectador lento está frenando al resto", i+1)
		}
	}
}

// Cancelar dos veces no puede romper nada: el manejador llama a cancelar por
// defer, y una salida por error podría llamarlo antes.
func TestCancelarDosVecesEsInofensivo(t *testing.T) {
	m := nuevoMuestreador(abrirLectorDePrueba)
	_, cancelar := m.suscribir()
	cancelar()
	cancelar()
}

// EL LECTOR SE PIDE EN CADA ARRANQUE DEL BUCLE, NO UNA SOLA VEZ. Es la razón de
// que «abrir» sea una función que devuelve otra función, y es lo primero que se
// pierde si alguien «simplifica» esa firma a un simple func() T.
//
// Por qué importa: el lector de /estado guarda la muestra de CPU anterior para
// poder restar. Si naciera una sola vez al arrancar el servidor, tras un rato
// sin nadie mirando la primera resta se haría contra una lectura de hace horas
// y el marco inicial daría un porcentaje falso justo al abrir la pantalla —un
// defecto que no rompe nada a gritos y que se vería como un número raro que
// «se arregla solo» un cuarto de segundo después.
func TestCadaArranqueDelBucleEstrenaLector(t *testing.T) {
	var mu sync.Mutex
	aperturas := 0

	m := nuevoMuestreador(func() func() marcoVivo {
		mu.Lock()
		aperturas++
		mu.Unlock()
		return func() marcoVivo { return marcoVivo{} }
	})

	contar := func() int {
		mu.Lock()
		defer mu.Unlock()
		return aperturas
	}

	// Primer ciclo completo: se suscribe, llega un marco, se va.
	marcos, cancelar := m.suscribir()
	select {
	case <-marcos:
	case <-time.After(2 * time.Second):
		t.Fatal("no llegó ningún marco en el primer arranque")
	}
	cancelar()

	esperarApagado(t, m)
	if n := contar(); n != 1 {
		t.Fatalf("tras el primer arranque el lector debía haberse abierto 1 vez, y fueron %d", n)
	}

	// Segundo ciclo: el bucle arranca de nuevo y DEBE pedir un lector nuevo.
	marcos2, cancelar2 := m.suscribir()
	defer cancelar2()
	select {
	case <-marcos2:
	case <-time.After(2 * time.Second):
		t.Fatal("no llegó ningún marco en el segundo arranque")
	}

	if n := contar(); n != 2 {
		t.Fatalf("el segundo arranque debía estrenar lector (2 aperturas), y hubo %d: el estado del lector anterior se está reutilizando", n)
	}
}

// esperarApagado bloquea hasta que el bucle se para de verdad, en lugar de
// suponerlo: se entera en su siguiente tic, no en el acto.
func esperarApagado(t *testing.T, m *muestreador[marcoVivo]) {
	t.Helper()
	plazo := time.Now().Add(2 * time.Second)
	for time.Now().Before(plazo) {
		m.mu.Lock()
		activo := m.activo
		m.mu.Unlock()
		if !activo {
			return
		}
		time.Sleep(intervaloVivo)
	}
	t.Fatal("el muestreo no se paró tras irse el último espectador")
}

// ---------------------------------------------------------------------------
// De extremo a extremo, contra un servidor HTTP de verdad
// ---------------------------------------------------------------------------

func servidorConPlazo(t *testing.T, plazo time.Duration) *Servidor {
	t.Helper()
	linea, err := autenticacion.Derivar(claveDePrueba, 1000)
	if err != nil {
		t.Fatalf("Derivar: %v", err)
	}
	s, err := Nuevo(Opciones{
		Almacen:          almacenVacio{},
		AlmacenDe:        almacenPorUsuarioDePrueba(almacenVacio{}),
		PromoverUsuario:  promoverUsuarioDePrueba,
		Registro:         slog.New(slog.NewJSONHandler(io.Discard, nil)),
		PlazoInactividad: plazo,
		Credencial:       linea,
		Usuarios:         registroDePrueba(t),
		DuracionSesion:   time.Hour,
		Metricas:         metricasDePrueba(t),
	})
	if err != nil {
		t.Fatalf("Nuevo: %v", err)
	}
	return s
}

// LA TRAMPA QUE HABÍA QUE COMPROBAR Y NO DAR POR HECHA.
//
// La configuración trae PlazoInactividad = 60 s (ADR-0026). Si ese plazo fuera
// ABSOLUTO en lugar de por actividad, el flujo se cortaría solo cada minuto y
// la página parecería colgarse sin motivo. El razonamiento dice que se renueva
// con cada escritura; esta prueba lo MIDE, con un plazo corto para no tardar un
// minuto: se exige que sigan llegando marcos bastante después de agotarlo.
func TestElFlujoSobreviveAlPlazoDeInactividad(t *testing.T) {
	const plazo = 300 * time.Millisecond
	s := servidorConPlazo(t, plazo)

	srv := httptest.NewServer(s.Rutas())
	defer srv.Close()

	cookie := abrirSesionDePrueba(t, s.Rutas())

	r, err := http.NewRequest("GET", srv.URL+"/estado/flujo", nil)
	if err != nil {
		t.Fatal(err)
	}
	r.AddCookie(cookie)
	r.Header.Set("Accept", "text/event-stream")

	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatalf("no se pudo abrir el flujo: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /estado/flujo -> %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("Content-Type = %q; SSE exige text/event-stream", ct)
	}
	// Un flujo cacheado no es un flujo.
	if cc := resp.Header.Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Fatalf("Cache-Control = %q", cc)
	}

	// Se leen marcos durante MÁS del triple del plazo. Con un plazo absoluto,
	// la conexión moriría antes de llegar al final de este bucle.
	lector := bufio.NewReader(resp.Body)
	limite := time.Now().Add(4 * plazo)
	var marcos int
	for time.Now().Before(limite) {
		linea, err := lector.ReadString('\n')
		if err != nil {
			t.Fatalf("el flujo se cortó tras %d marcos y %v de vida: %v",
				marcos, plazo, err)
		}
		datos, hay := strings.CutPrefix(strings.TrimSpace(linea), "data: ")
		if !hay {
			continue // la línea en blanco que separa un evento del siguiente
		}
		var marco marcoVivo
		if err := json.Unmarshal([]byte(datos), &marco); err != nil {
			t.Fatalf("marco ilegible: %v\n%s", err, datos)
		}
		if len(marco.Servicio) == 0 {
			t.Fatal("un marco sin filas de servicio no sirve para nada")
		}
		marcos++
	}

	// Con 250 ms de intervalo y 4×300 ms de escucha deberían caber unos cinco.
	// Se exige menos de la cuenta a propósito: lo que se está probando es que
	// el flujo NO se corta, no la puntualidad del reloj de la máquina de CI.
	if marcos < 3 {
		t.Fatalf("solo llegaron %d marcos en %v; el flujo no está emitiendo al ritmo previsto",
			marcos, 4*plazo)
	}
}

// La página y el flujo tienen que decir lo mismo, y no por disciplina de quien
// edite después: por construcción, porque los compone la misma función.
//
// Esta prueba fija ese contrato. Si alguien añade una fila a la plantilla sin
// pasar por filasVivas, la fila se quedará quieta para siempre en la pantalla
// —sin fallar, que es lo peor— y esto lo detecta antes.
func TestCadaFilaVivaDeLaPaginaTieneAnclaEnElFlujo(t *testing.T) {
	s := servidorConAuth(t)
	h := s.Rutas()
	cookie := abrirSesionDePrueba(t, h)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/estado", nil)
	r.AddCookie(cookie)
	h.ServeHTTP(w, r)
	cuerpo := w.Body.String()

	marco := s.marcoDelServidor(sistema.Vivo{})
	filas := append(append([]filaViva{}, marco.Nodo...), marco.Servicio...)
	if len(filas) == 0 {
		t.Fatal("el marco no trae ninguna fila")
	}

	for _, f := range filas {
		if f.Clave == "" {
			t.Fatalf("fila sin clave: %+v", f)
		}
		// El ancla del navegador es data-clave. Si no está en el HTML, el
		// flujo llega pero no encuentra dónde escribir.
		if !strings.Contains(cuerpo, `data-clave="`+f.Clave+`"`) {
			t.Fatalf("la fila %q viaja en el flujo pero no tiene ancla en la página", f.Clave)
		}
		if f.Nombre == "" {
			t.Fatalf("la fila %q no tiene rótulo que enseñar", f.Clave)
		}
	}
}

// El rótulo de cada indicador debe seguir siendo el mismo en la página y en el
// diario: evaluar() es la única fuente de veredictos, y partirla en dos mitades
// por coste no puede haber duplicado ni perdido ninguno.
func TestLaParticionPorCosteNoPierdeIndicadores(t *testing.T) {
	n := nodoSano()
	i := Instantanea{Peticiones: 10, SubidasConfirmadas: 1, Listados: 3}

	completo := evaluar(n, i)
	vivos := evaluarVivos(n.Vivo, i)
	lentos := evaluarLentos(n)

	if len(completo) != len(vivos)+len(lentos) {
		t.Fatalf("evaluar() da %d indicadores y las dos mitades suman %d",
			len(completo), len(vivos)+len(lentos))
	}

	vistas := make(map[string]bool, len(completo))
	for _, ind := range completo {
		if ind.Clave == "" {
			t.Fatalf("indicador sin clave: %+v", ind)
		}
		if vistas[ind.Clave] {
			t.Fatalf("la clave %q aparece dos veces; el flujo actualizaría una fila con el valor de otra", ind.Clave)
		}
		vistas[ind.Clave] = true
	}
}
