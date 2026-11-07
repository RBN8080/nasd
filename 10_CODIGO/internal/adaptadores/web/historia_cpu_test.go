package web

import (
	"context"
	"net/http"
	"runtime"
	"strings"
	"testing"
	"time"

	"nasd/internal/adaptadores/sistema"
)

// Pruebas de la historia de la minigráfica de CPU (historia_cpu.go).

// EL ANILLO GUARDA LAS ÚLTIMAS Y LAS DA EN ORDEN: 130 muestras en un anillo de
// 120 dejan de la 10 a la 129, de la más vieja a la más nueva.
func TestLaHistoriaDeCPUGuardaLasUltimasEnOrden(t *testing.T) {
	var a anilloCPU
	t0 := time.Now()
	for i := range 130 {
		a.anotar(t0.Add(time.Duration(i)*intervaloVivo), float64(i))
	}
	s := a.serie(t0.Add(129 * intervaloVivo))
	if len(s) != muestrasCPU {
		t.Fatalf("la serie trae %d muestras y se esperaban %d", len(s), muestrasCPU)
	}
	if s[0] != 10 || s[len(s)-1] != 129 {
		t.Errorf("la serie va de %v a %v; se esperaba de 10 a 129", s[0], s[len(s)-1])
	}
}

// LO DE HACE MÁS DE 30 s NO SE DIBUJA: tras un hueco la gráfica juntaría dos
// momentos como si fueran seguidos.
func TestLaHistoriaDeCPUNoEntregaLoViejo(t *testing.T) {
	var a anilloCPU
	t0 := time.Now()
	a.anotar(t0, 1)
	a.anotar(t0.Add(20*time.Second), 2)
	if s := a.serie(t0.Add(40 * time.Second)); len(s) != 1 || s[0] != 2 {
		t.Errorf("a los 40 s debía quedar solo la muestra de los 20 s y quedó %v", s)
	}
	if s := a.serie(t0.Add(time.Hour)); len(s) != 0 {
		t.Errorf("una hora después no debe quedar nada que dibujar: %v", s)
	}
}

// LA PÁGINA TRAE LA HISTORIA: /estado y /resumen ponen en la minigráfica lo
// guardado, y la gráfica ya no empieza vacía en cada visita.
func TestLaMinigraficaLlegaConSuHistoria(t *testing.T) {
	s := servidorConAuth(t)
	cookie := superusuarioEn(t, s)
	ahora := time.Now()
	s.historiaCPU.anotar(ahora.Add(-2*intervaloVivo), 42)
	s.historiaCPU.anotar(ahora.Add(-intervaloVivo), 7.5)
	for _, ruta := range []string{"/estado", "/resumen"} {
		cuerpo := pedirDesde(t, s.Rutas(), http.MethodGet, ruta, "192.168.1.18:5000", cookie).Body.String()
		if !strings.Contains(cuerpo, `data-serie="cpu" data-muestras="42.0 7.5`) {
			t.Errorf("%s no trae la historia de CPU en la minigráfica", ruta)
		}
	}
}

// SIN NADA MEDIDO NO HAY GRÁFICA: ni un hueco reservado que nunca se llena ni
// uno que aparece después y empuja lo de debajo.
func TestSinCPUMedidaNoHayMinigrafica(t *testing.T) {
	s := servidorConAuth(t)
	if got := s.serieCPU(sistema.Vivo{}); got != "" {
		t.Errorf("sin historia ni lectura la serie debía salir vacía y salió %q", got)
	}
	if got := s.serieCPU(sistema.Vivo{CPU: 12.34, CPUOK: true}); got != "12.3" {
		t.Errorf("con solo la lectura de esta carga se esperaba «12.3» y salió %q", got)
	}
	var b strings.Builder
	fila := filaViva{Clave: "cpu", Nombre: "Uso de CPU", Valor: sinMedida}
	if err := s.plantillas.ExecuteTemplate(&b, "fila-dato", fila); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b.String(), `class="spark"`) {
		t.Error("una fila de CPU sin nada medido no debe llevar minigráfica")
	}
}

// EL MUESTREO ANOTA SOLO Y PARA CON SU CONTEXTO. Anotar solo se comprueba en
// Linux, donde hay /proc/stat; fuera, MedirCPU vuelve en el acto.
func TestMedirCPUAnotaYParaConSuContexto(t *testing.T) {
	s := servidorConAuth(t)
	ctx, cancelar := context.WithCancel(context.Background())
	hecho := make(chan struct{})
	go func() { s.MedirCPU(ctx); close(hecho) }()
	if runtime.GOOS == "linux" {
		time.Sleep(5 * intervaloVivo)
		if len(s.historiaCPU.serie(time.Now())) == 0 {
			t.Error("tras más de un segundo de muestreo la historia sigue vacía")
		}
	}
	cancelar()
	select {
	case <-hecho:
	case <-time.After(2 * time.Second):
		t.Fatal("MedirCPU no paró al cancelar su contexto")
	}
}
