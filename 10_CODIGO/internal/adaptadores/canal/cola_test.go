package canal

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"nasd/internal/aviso"
)

// La cola tiene UNA responsabilidad y es la que sostiene toda la arquitectura:
// que un proveedor lento o caído NUNCA haga esperar a quien produce el aviso.
// Quien produce es el mismo ciclo que evalúa la cuarentena, así que si esto
// falla, el enforcement pasa a depender del tiempo de respuesta de un tercero.

// canalFalso finge ser un proveedor, con el comportamiento que pida la prueba.
type canalFalso struct {
	mu        sync.Mutex
	recibidos []aviso.Aviso
	err       error
	// tarda simula un proveedor lento. Es lo que convierte «no bloquea» de una
	// afirmación en algo comprobable.
	tarda time.Duration
}

func (c *canalFalso) Nombre() string { return "falso" }

func (c *canalFalso) Enviar(ctx context.Context, a aviso.Aviso) error {
	if c.tarda > 0 {
		select {
		case <-time.After(c.tarda):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.recibidos = append(c.recibidos, a)
	return c.err
}

func (c *canalFalso) cuantos() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.recibidos)
}

func TestEncolarNoBloqueaAunqueElProveedorEsteColgado(t *testing.T) {
	// LA PROPIEDAD QUE SOSTIENE LA ARQUITECTURA. Si Encolar bloqueara, un
	// proveedor que tarda diez segundos retrasaría diez segundos la evaluación
	// de conducta del nodo — y entonces NOTIFICACIÓN estaría delante de
	// ENFORCEMENT, que es exactamente lo que el diseño prohíbe.
	lento := &canalFalso{tarda: time.Hour}
	c := NuevaCola(lento, silencioso())

	ctx, cancelar := context.WithCancel(context.Background())
	defer cancelar()
	go c.Atender(ctx)

	inicio := time.Now()
	for range topeCola * 2 {
		c.Encolar(avisoDePrueba())
	}
	if d := time.Since(inicio); d > time.Second {
		t.Errorf("encolar %d avisos con el proveedor colgado tardó %v; no debe bloquear NUNCA", topeCola*2, d)
	}
}

func TestLaColaLlenaDescartaYCuentaEnVezDeCrecer(t *testing.T) {
	// Quien empuja este tamaño es un extraño. Sin tope, la memoria de nasd
	// dependería de cuánto tráfico externo haya, en un nodo de 592 MB.
	//
	// NO SE ARRANCA a Atender: así la cola no se vacía y se puede comprobar el
	// tope de verdad, en vez de una carrera con el emisor.
	c := NuevaCola(&canalFalso{}, silencioso())
	const demas = 30
	for range topeCola + demas {
		c.Encolar(avisoDePrueba())
	}

	s := c.Salud()
	if s.EnCola > topeCola {
		t.Errorf("la cola guarda %d avisos; el tope es %d", s.EnCola, topeCola)
	}
	// Y LO DICE. Un descarte silencioso convertiría «no me llegó el aviso» en
	// un misterio; contado, es una línea del indicador de /estado.
	if s.Descartados != demas {
		t.Errorf("descartados = %d; se esperaban %d", s.Descartados, demas)
	}
}

func TestUnProveedorCaidoNoImpideQueSigaFuncionandoTodoLoDemas(t *testing.T) {
	// El nodo detecta, infiere, decide, aparta, bloquea y registra igual con el
	// proveedor caído. Aquí se comprueba la parte que le toca a esta pieza: que
	// los fallos se cuenten y no se propaguen a nadie.
	roto := &canalFalso{err: errors.New("proveedor caído")}
	c := NuevaCola(roto, silencioso())

	for range 5 {
		c.entregar(context.Background(), avisoDePrueba())
	}
	s := c.Salud()
	if s.Fallidos != 5 {
		t.Errorf("fallidos = %d; se esperaban 5", s.Fallidos)
	}
	if s.Entregados != 0 {
		t.Errorf("entregados = %d; se esperaban 0", s.Entregados)
	}
	if s.UltimoError == "" {
		t.Error("no se conservó el último error; el indicador de /estado no podría decir qué pasa")
	}
}

func TestUnSecretoRechazadoSePublicaAparte(t *testing.T) {
	// Es el único fallo que no se arregla esperando: pide ir a cambiar el
	// secreto. Se publica aparte para que el indicador diga QUÉ HACER en vez de
	// solo que algo falla.
	c := NuevaCola(&canalFalso{err: ErrSecretoRechazado}, silencioso())
	c.entregar(context.Background(), avisoDePrueba())

	if !c.Salud().SecretoRechazado {
		t.Error("un ErrSecretoRechazado no se distinguió de un fallo cualquiera")
	}

	// Y se apaga solo al primer envío correcto: el responsable no tiene que ir
	// a rearmar nada después de arreglar el token.
	c.canal = &canalFalso{}
	c.entregar(context.Background(), avisoDePrueba())
	if c.Salud().SecretoRechazado {
		t.Error("el indicador siguió encendido tras un envío correcto")
	}
}

func TestUnaCaidaLargaProduceElAvisoDeCanalRecuperado(t *testing.T) {
	// EL ÚNICO NARANJA QUE NACE EN EL NODO, y solo se puede entregar al volver:
	// mientras el canal estaba caído no había por dónde decirlo.
	falso := &canalFalso{}
	c := NuevaCola(falso, silencioso())

	// Se simula una racha que empezó hace más del umbral.
	c.mu.Lock()
	c.caidoDesde = time.Now().Add(-umbralCanalCaido - time.Minute)
	c.fallosEnRacha = 12
	c.mu.Unlock()

	c.entregar(context.Background(), avisoDePrueba())

	// El aviso de recuperación se encola: no se manda dentro de entregar, para
	// no anidar envíos.
	if c.Salud().EnCola != 1 {
		t.Fatalf("no se encoló el aviso de canal recuperado (EnCola=%d)", c.Salud().EnCola)
	}
	recuperado := <-c.entradas
	if recuperado.Severidad != aviso.Naranja {
		t.Errorf("severidad = %q; el canal recuperado es NARANJA, no seguridad", recuperado.Severidad)
	}
	if !strings.Contains(recuperado.Texto, "es el canal, no el NAS") {
		t.Errorf("el aviso no separa el canal de la seguridad del nodo\n%s", recuperado.Texto)
	}
}

func TestUnaCaidaCortaNoDiceNada(t *testing.T) {
	// Un aviso de «el canal parpadeó» sería justo el ruido que toda esta capa
	// existe para quitar. Por debajo del umbral, se recupera en silencio.
	c := NuevaCola(&canalFalso{}, silencioso())
	c.mu.Lock()
	c.caidoDesde = time.Now().Add(-time.Minute)
	c.fallosEnRacha = 1
	c.mu.Unlock()

	c.entregar(context.Background(), avisoDePrueba())
	if n := c.Salud().EnCola; n != 0 {
		t.Errorf("una caída de un minuto produjo %d avisos; se esperaban 0", n)
	}
}

func TestApagarNoCuentaComoAveriaDelCanal(t *testing.T) {
	// Al apagar el servicio, los envíos en curso se cancelan. Eso es un apagado
	// ordenado, no una avería, y contarlo dejaría el indicador de /estado en
	// rojo tras cada reinicio — quince en catorce días medidos.
	ctx, cancelar := context.WithCancel(context.Background())
	cancelar()

	c := NuevaCola(&canalFalso{err: context.Canceled}, silencioso())
	c.entregar(ctx, avisoDePrueba())

	if s := c.Salud(); s.Fallidos != 0 {
		t.Errorf("fallidos = %d tras un apagado ordenado; se esperaban 0", s.Fallidos)
	}
}

func TestElDuplicadoAnotaSiempreEnElDiarioAunqueElProveedorFalle(t *testing.T) {
	// La constancia local no puede depender de que el mensaje salga. Si el
	// responsable dice «no me llegó nada» y el diario tiene la línea, el fallo
	// está en el camino y no en la detección; sin la copia, las dos hipótesis
	// serían indistinguibles.
	principal := &canalFalso{err: errors.New("caído")}
	copia := &canalFalso{}
	d := NuevoDuplicado(principal, copia)

	if err := d.Enviar(context.Background(), avisoDePrueba()); err == nil {
		t.Error("el duplicado ocultó el fallo del canal principal")
	}
	if copia.cuantos() != 1 {
		t.Errorf("la copia local recibió %d avisos; se esperaba 1", copia.cuantos())
	}
	// Y el nombre es el del principal: es quien decide si un aviso llega.
	if d.Nombre() != principal.Nombre() {
		t.Errorf("Nombre() = %q; se esperaba el del canal principal", d.Nombre())
	}
}

func TestElDiarioAnotaConElNivelDeLaSeveridad(t *testing.T) {
	// Los niveles son los MISMOS que ya usa anunciar() para los veredictos de
	// /estado. No se estrena un vocabulario nuevo en el diario por tener una
	// capa nueva.
	for _, c := range []struct {
		severidad aviso.Severidad
		nivel     string
	}{
		{aviso.Rojo, "ERROR"},
		{aviso.Amarillo, "WARN"},
		{aviso.Naranja, "WARN"},
		{aviso.Verde, "INFO"},
	} {
		var salida strings.Builder
		d := NuevoDiario(registroEn(&salida))
		_ = d.Enviar(context.Background(), aviso.Aviso{Severidad: c.severidad, Titulo: "x"})
		if !strings.Contains(salida.String(), c.nivel) {
			t.Errorf("severidad %q se anotó como %q; se esperaba %s", c.severidad, salida.String(), c.nivel)
		}
		// Y NUNCA el texto del mensaje: lleva marcado HTML y saltos de línea, y
		// una línea de journald con eso dentro es ilegible justo el día que hay
		// que leerla.
		if strings.Contains(salida.String(), "<b>") {
			t.Errorf("el marcado HTML entró en el diario: %s", salida.String())
		}
	}
}
