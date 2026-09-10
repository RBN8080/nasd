package web

import (
	"math"
	"strings"
	"testing"
	"time"

	"nasd/internal/seguridad"
)

// serieDePrueba arma los doce días con los valores dados, del más antiguo al
// más reciente.
func serieDePrueba(rechazos ...int) []seguridad.Dia {
	dias := make([]seguridad.Dia, len(rechazos))
	base := time.Date(2026, 8, 29, 0, 0, 0, 0, time.Local)
	for i, n := range rechazos {
		dias[i] = seguridad.Dia{Fecha: base.AddDate(0, 0, i), Rechazos: n}
	}
	return dias
}

// EL EJE CUENTA COSAS ENTERAS, Y POR ESO NO PUEDE LLEVAR DECIMALES. No existe
// media conexión ni medio paquete. La primera maqueta ponía la marca de en
// medio al 50 % de la altura pase lo que pase, y con un máximo de 5 eso
// escribía «2.5»; el responsable lo paró ahí.
//
// El tipo ya obliga a que el valor sea int, así que lo que esta prueba comprueba
// es lo otro: que ningún máximo, por raro que sea, produzca un eje sin el cero,
// sin su máximo, o con dos marcas repetidas.
func TestElEjeDeValoresEsSiempreDeEnteros(t *testing.T) {
	for _, max := range []int{0, 1, 2, 3, 5, 7, 9, 22, 47, 104, 491, 953, 2513, 63072} {
		for _, raiz := range []bool{false, true} {
			marcas := marcasDelEje(max, raiz)
			if len(marcas) == 0 {
				t.Fatalf("max=%d raiz=%v: eje sin marcas", max, raiz)
			}
			vistos := map[int]bool{}
			for _, m := range marcas {
				if vistos[m.Valor] {
					t.Errorf("max=%d raiz=%v: la marca %d sale dos veces", max, raiz, m.Valor)
				}
				vistos[m.Valor] = true
				if m.Valor < 0 || m.Valor > max && max > 0 {
					t.Errorf("max=%d raiz=%v: la marca %d se sale del rango", max, raiz, m.Valor)
				}
			}
			if !vistos[0] {
				t.Errorf("max=%d raiz=%v: el eje no dice dónde está el suelo", max, raiz)
			}
			if max > 0 && !vistos[max] {
				t.Errorf("max=%d raiz=%v: el eje no rotula su máximo", max, raiz)
			}
		}
	}
}

// CADA NÚMERO ESTÁ DONDE DE VERDAD CAE SU VALOR, y no repartido a ojo por la
// altura. Es la mitad que hace honesto al eje: un rótulo colocado a intervalos
// iguales sobre una escala de raíz diría que 100 y 400 distan lo mismo que 400
// y 900, que es exactamente lo contrario de lo que la escala hace.
func TestCadaMarcaDelEjeCaeDondeDiceSuValor(t *testing.T) {
	for _, max := range []int{5, 22, 104, 953} {
		for _, raiz := range []bool{false, true} {
			for _, m := range marcasDelEje(max, raiz) {
				quiero := alturaEnGrafica(m.Valor, max, raiz)
				if math.Abs(m.YGrafica-quiero) > 0.01 {
					t.Errorf("max=%d raiz=%v marca=%d: está en %g y su valor cae en %g",
						max, raiz, m.Valor, m.YGrafica, quiero)
				}
				// Y la posición en el eje es la MISMA altura, reescalada al
				// viewBox del eje. Si las dos se calcularan por separado, el
				// número y su línea de rejilla se despegarían.
				if quieroEje := quiero / altoGrafica * altoEje; math.Abs(m.Y-quieroEje) > 0.6 {
					t.Errorf("max=%d marca=%d: el número está en %g y su rejilla en %g",
						max, m.Valor, m.Y, quieroEje)
				}
			}
		}
	}
}

// LA ESCALA SE ELIGE POR LA FORMA DE LA SERIE, y las dos series de esta prueba
// son REALES, leídas del nodo el 2026-09-10.
func TestLaEscalaSoloSeVuelveRaizCuandoElPicoAplastaAlResto(t *testing.T) {
	// Rechazos de la red de casa, del 29/08 al 09/09: días corrientes de 20 a
	// 104 y dos barridos de 491 y 953. Con escala lineal, los once días
	// normales quedan pegados al suelo.
	casa := []int{51, 23, 60, 45, 26, 20, 104, 491, 62, 33, 22, 953}
	if !escalaDe(casa) {
		t.Error("con un pico de 953 sobre una mediana de ~46 la escala tiene que ser de raíz")
	}
	// Rechazos de Internet en los mismos doce días: de 0 a 5. Aquí la raíz no
	// rescata nada y solo apelotona los números del eje.
	internet := []int{0, 1, 5, 0, 0, 0, 2, 0, 1, 4, 0, 0}
	if escalaDe(internet) {
		t.Error("con un máximo de 5 la escala tiene que quedarse lineal")
	}
	// Y sin días suficientes no hay «lo corriente» contra lo que comparar: dos
	// puntos y un pico no son una distribución.
	if escalaDe([]int{0, 0, 0, 0, 900, 0, 0, 0, 0, 0, 1, 0}) {
		t.Error("con dos días de actividad no se puede afirmar que un pico aplaste nada")
	}
}

// LA PÁGINA NO PUEDE ENSEÑAR UN EJE CON DECIMALES, y esto lo comprueba sobre el
// HTML servido y no sobre la función: entre las dos hay una plantilla.
func TestElEjeServidoNoLlevaDecimales(t *testing.T) {
	s := servidorConAuth(t)
	conSensor(t, s, time.Now().Add(-30*time.Hour), time.Now().Add(-29*time.Hour),
		time.Now().Add(-28*time.Hour), time.Now().Add(-2*time.Hour), time.Now().Add(-time.Hour))

	cuerpo := panelSeguridad(t, s, "")
	for resto := cuerpo; ; {
		i := strings.Index(resto, `<text class="gyt"`)
		if i < 0 {
			break
		}
		resto = resto[i:]
		fin := strings.Index(resto, "</text>")
		if fin < 0 {
			t.Fatal("un rótulo del eje sin cerrar")
		}
		marca := resto[:fin]
		valor := marca[strings.LastIndex(marca, ">")+1:]
		if strings.ContainsAny(valor, ".,") {
			t.Errorf("el eje sirve un rótulo con decimales: %q", valor)
		}
		resto = resto[fin:]
	}
}

// EL PANEL DE PAQUETES SOLO EXISTE MIRANDO INTERNET, y cuando no está, el de
// rechazos no se queda con su eje: cada panel calcula el suyo.
func TestCadaPanelTieneSuPropioMaximo(t *testing.T) {
	serie := serieDePrueba(0, 1, 5, 0, 0, 0, 2, 0, 1, 4, 0, 0)
	for i := range serie {
		serie[i].Toques = serie[i].Rechazos * 7
	}
	paneles := graficaDeActividad(serie, true)
	if len(paneles) != 2 {
		t.Fatalf("con sensor tienen que salir dos paneles; salieron %d", len(paneles))
	}
	if paneles[0].Rotulo != "Paquetes" || paneles[1].Rotulo != "Rechazos" {
		t.Fatalf("el orden de los paneles cambió: %q y %q", paneles[0].Rotulo, paneles[1].Rotulo)
	}
	if paneles[0].Max != 35 || paneles[1].Max != 5 {
		t.Errorf("los máximos no son los de cada serie: %d y %d", paneles[0].Max, paneles[1].Max)
	}
	// El de contexto rellena su área; el que se lee, no.
	if paneles[0].Relleno == "" {
		t.Error("la capa de paquetes tiene que cerrar su área")
	}
	if paneles[1].Relleno != "" {
		t.Error("la capa de rechazos es un trazo limpio, sin relleno")
	}
}
