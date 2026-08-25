package aviso

import (
	"strings"
	"testing"
	"time"

	"nasd/internal/seguridad"
)

// El resumen tiene DOS trabajos y las pruebas cubren los dos: absorber el
// volumen sin mensajes, y demostrar que el canal sigue vivo cuando no hay nada
// que contar.

func TestElResumenSeEmiteAunqueTodoSeaCero(t *testing.T) {
	// Este nodo recibió 8 peticiones de Internet en 21 días medidos. Con ese
	// volumen, un canal que solo habla cuando hay problemas puede pasar semanas
	// mudo — y un canal mudo es indistinguible de uno roto. El resumen es al
	// canal lo que el latido es al nodo.
	ahora := time.Now()
	r := Resumir(Entrada{Ventana: 24 * time.Hour}, nil, ahora)

	if !r.Tranquilo() {
		t.Fatal("un resumen sin actividad no se declara tranquilo")
	}
	a := MensajeResumen(r)
	if strings.TrimSpace(a.Texto) == "" {
		t.Fatal("el resumen de un día tranquilo llegó vacío")
	}
	if !a.Silencioso {
		t.Error("el resumen debe llegar SIN sonar: es la prueba de vida, no una alerta")
	}
	if a.Severidad != Verde {
		t.Errorf("severidad del resumen = %q; se esperaba verde", a.Severidad)
	}
	// Una sola línea, no ocho ceros: ocho ceros se dejan de leer a la tercera
	// vez y el resumen pierde su segunda función por exceso de celo.
	if strings.Contains(a.Texto, ": 0") {
		t.Errorf("el resumen tranquilo enumera ceros en vez de decirlo en una línea\n%s", a.Texto)
	}
}

func TestElResumenCuentaElVolumenSinNarrarlo(t *testing.T) {
	ahora := time.Now()
	o1 := explorador("203.0.113.7", 9, ahora)
	o2 := rutinario("203.0.113.8", ahora)
	o3 := anomalo("203.0.113.9", ahora)
	o1.Apartado = apartadoDe("203.0.113.7", ahora)

	e := Entrada{
		Origenes:           []OrigenVisto{o1, o2, o3},
		Hallazgos:          []seguridad.Hallazgo{hallazgo("/.git/config", ahora)},
		ConexionesInternet: 127,
		Ventana:            6 * time.Hour,
	}
	r := Resumir(e, nil, ahora)

	for _, c := range []struct {
		nombre string
		tiene  int
		espera int
	}{
		{"conexiones de Internet", r.ConexionesInternet, 127},
		{"orígenes distintos", r.OrigenesDeInternet, 3},
		{"orígenes con señal", r.OrigenesConSenal, 1},
		{"cuarentenas", r.Cuarentenas, 1},
		{"hallazgos", r.Hallazgos, 1},
		{"rutas inexistentes", r.RutasInexistentes, 9},
		// Las sondas se cuentan por PETICIONES y no por rutas distintas: la
		// pregunta es «cuánto se está buscando aquí», no «cuántas cosas».
		{"sondas de software ajeno", r.SondasAjenas, 5},
	} {
		if c.tiene != c.espera {
			t.Errorf("%s = %d; se esperaba %d", c.nombre, c.tiene, c.espera)
		}
	}
	if r.Tranquilo() {
		t.Error("un resumen con actividad se declaró tranquilo")
	}
}

func TestLoRetenidoLlegaEnteroYNoComoUnNumero(t *testing.T) {
	// Es lo que distingue este resumen de una estadística: una situación
	// amarilla que el modo silencio no dejó salir tiene que poder LEERSE, con su
	// origen y su evidencia. Fundirla con los contadores la convertiría en una
	// línea que no dice nada.
	ahora := time.Now()
	retenida := Evaluar(Entrada{Origenes: []OrigenVisto{explorador("203.0.113.7", 9, ahora)}}, ahora)[0]

	r := Resumir(Entrada{Ventana: 24 * time.Hour}, []Situacion{retenida}, ahora)
	texto := MensajeResumen(r).Texto

	if !strings.Contains(texto, "Retenido por el modo silencio") {
		t.Fatalf("lo retenido no aparece en el resumen\n%s", texto)
	}
	if !strings.Contains(texto, "203.0.113.7") {
		t.Errorf("lo retenido llegó sin su origen\n%s", texto)
	}
	if !strings.Contains(texto, "/wp-login.php") {
		t.Errorf("lo retenido llegó sin su evidencia\n%s", texto)
	}
}

func TestLaRetencionNoCreceSinLimiteYDiceQueRecorta(t *testing.T) {
	// El modo silencio puede coincidir con un barrido, y entonces quien decide
	// cuántas situaciones se acumulan es un extraño.
	var ret Retencion
	ahora := time.Now()
	for i := range topeRetenidas + 15 {
		ret.Guardar(Situacion{
			Clase: ClaseExploracion, Severidad: Amarillo,
			Sujeto: direccion(i), Primera: ahora, Ultima: ahora,
		})
	}
	ss, omitidas := ret.Vaciar()
	if len(ss) != topeRetenidas {
		t.Errorf("se retuvieron %d situaciones; el tope es %d", len(ss), topeRetenidas)
	}
	if omitidas != 15 {
		t.Errorf("omitidas = %d; se esperaban 15", omitidas)
	}

	r := Resumir(Entrada{Ventana: 24 * time.Hour}, ss, ahora)
	r.RetenidasOmitidas += omitidas
	if texto := MensajeResumen(r).Texto; !strings.Contains(texto, "no caben") {
		t.Errorf("el resumen no dice que recorta\n%s", texto)
	}

	// Y vaciar deja la retención limpia: lo retenido se entrega una vez.
	if ret.Cuantas() != 0 {
		t.Errorf("tras vaciar quedan %d retenidas; se esperaban 0", ret.Cuantas())
	}
}

func TestElResumenPublicaCuantoRuidoAbsorbio(t *testing.T) {
	// Es lo que hace COMPROBABLE la promesa de esta capa. Sin esta cifra,
	// «reducimos el ruido» es una afirmación que nadie puede verificar.
	ahora := time.Now()
	r := Resumir(Entrada{Ventana: 24 * time.Hour}, nil, ahora)
	r.Avisadas, r.Absorbidas = 3, 517

	texto := MensajeResumen(r).Texto
	if !strings.Contains(texto, "3") || !strings.Contains(texto, "517") {
		t.Errorf("el resumen no publica la relación entre observaciones y avisos\n%s", texto)
	}
}

func TestLaCasaNoEntraEnElVolumenDelResumen(t *testing.T) {
	// La misma barandilla que en Evaluar, repetida donde se cuenta: si la LAN
	// entrara aquí, el resumen diario contaría los cientos de peticiones de los
	// teléfonos de casa y se volvería ilegible en un día.
	ahora := time.Now()
	deCasa := rutinario("192.168.1.18", ahora)
	deCasa.Red = seguridad.RedLocal
	deCasa.Eventos = 500

	r := Resumir(Entrada{Origenes: []OrigenVisto{deCasa}, Ventana: 24 * time.Hour}, nil, ahora)
	if r.Rechazos != 0 || r.OrigenesDeInternet != 0 {
		t.Errorf("la casa entró en el resumen: %d rechazos, %d orígenes", r.Rechazos, r.OrigenesDeInternet)
	}
}
