package seguridad

import (
	"testing"
	"time"
)

// La gráfica de actividad de /seguridad (ADR-0075) se pinta con lo que el
// anillo recuerda, y nada más: sin capa de acumulados por hora —requisito de
// una fase futura— no hay «últimos 90 días» que enseñar. Estas pruebas
// sujetan esa honestidad: la serie tiene siempre DiasGrafica columnas, pero
// «desde» solo promete hasta donde de verdad hay datos detrás.

// ahoraALasDoceDelMediodia fija la hora del «ahora» de cada prueba a mediodía
// para que ninguna quede a merced de la hora local en la que corra: medianoche
// es el único instante en que un desfase de zona horaria puede empujar
// «ahora» al día de al lado y desbaratar el cálculo de «hoy».
func ahoraALasDoceDelMediodia() time.Time {
	n := time.Now()
	return time.Date(n.Year(), n.Month(), n.Day(), 12, 0, 0, 0, n.Location())
}

func TestLaSerieTieneSiempreDiasGraficaColumnas(t *testing.T) {
	serie, desde := PorDia(nil, ahoraALasDoceDelMediodia())
	if len(serie) != DiasGrafica {
		t.Fatalf("la serie tiene %d columnas, se esperaban %d", len(serie), DiasGrafica)
	}
	if !desde.IsZero() {
		t.Errorf("sin eventos, «desde» debería ser cero y es %v", desde)
	}
	for _, d := range serie {
		if d.Rechazos != 0 {
			t.Errorf("día %v con %d rechazos sin haber dado ningún evento", d.Fecha, d.Rechazos)
		}
	}
}

// EL ÚLTIMO DÍA DE LA SERIE ES HOY, y el primero son DiasGrafica-1 días
// atrás: la ventana termina en el instante que se pide, no en la medianoche
// que ya pasó.
func TestLaSerieTerminaHoyYEmpiezaDiasGraficaAtras(t *testing.T) {
	ahora := ahoraALasDoceDelMediodia()
	serie, _ := PorDia(nil, ahora)

	hoy := ahora.Truncate(24 * time.Hour)
	ultimo := serie[len(serie)-1].Fecha
	primero := serie[0].Fecha
	if !ultimo.Equal(hoy) {
		t.Errorf("el último día es %v, se esperaba hoy (%v)", ultimo, hoy)
	}
	if esperado := hoy.AddDate(0, 0, -(DiasGrafica - 1)); !primero.Equal(esperado) {
		t.Errorf("el primer día es %v, se esperaba %v", primero, esperado)
	}
}

// DOS EVENTOS DEL MISMO DÍA CAEN EN LA MISMA COLUMNA, y una ráfaga de 20
// conexiones en cinco minutos —el DRIFTNET del 16/08 real— produce UNA barra
// alta, no veinte columnas que no caben.
func TestDosEventosDelMismoDiaSeSumanEnUnaColumna(t *testing.T) {
	ahora := ahoraALasDoceDelMediodia()
	hoy := ahora.Truncate(24 * time.Hour)
	eventos := []Evento{
		ev(hoy.Add(2*time.Hour), "203.0.113.7", SinSesion, "/"),
		ev(hoy.Add(3*time.Hour), "203.0.113.8", SinSesion, "/"),
	}
	serie, _ := PorDia(eventos, ahora)
	if got := serie[len(serie)-1].Rechazos; got != 2 {
		t.Fatalf("la columna de hoy tiene %d rechazos, se esperaban 2", got)
	}
}

// «DESDE» ES EL MÁS ANTIGUO DE LOS EVENTOS QUE DE VERDAD ENTRAN EN LA SERIE,
// no una fecha calculada de la ventana. Es la misma honestidad que
// PaquetesDesde ya aplica en panel_seguridad.go: una cifra sin su alcance es
// una afirmación sin respaldo.
func TestDesdeEsElEventoMasAntiguoDentroDeLaVentana(t *testing.T) {
	ahora := ahoraALasDoceDelMediodia()
	hoy := ahora.Truncate(24 * time.Hour)
	masAntiguo := hoy.AddDate(0, 0, -3).Add(5 * time.Hour)
	eventos := []Evento{
		ev(masAntiguo, "203.0.113.7", SinSesion, "/"),
		ev(hoy.Add(1*time.Hour), "203.0.113.8", SinSesion, "/"),
	}
	_, desde := PorDia(eventos, ahora)
	if !desde.Equal(masAntiguo) {
		t.Errorf("desde = %v, se esperaba el más antiguo (%v)", desde, masAntiguo)
	}
}

// UN EVENTO ANTERIOR A LA VENTANA NO CUENTA EN NINGUNA COLUMNA NI EMPUJA
// «DESDE» HACIA ATRÁS: la gráfica declara DiasGrafica días y ni uno más. Si
// el anillo guarda historia más vieja, esa historia vive en la nota de
// «Historial: N conservados de M vistos» (seguridad.html), no aquí.
func TestUnEventoAnteriorALaVentanaNoCuenta(t *testing.T) {
	ahora := ahoraALasDoceDelMediodia()
	hoy := ahora.Truncate(24 * time.Hour)
	fueraDeVentana := hoy.AddDate(0, 0, -(DiasGrafica + 5))
	eventos := []Evento{
		ev(fueraDeVentana, "203.0.113.7", SinSesion, "/"),
	}
	serie, desde := PorDia(eventos, ahora)
	for _, d := range serie {
		if d.Rechazos != 0 {
			t.Errorf("un evento de fuera de la ventana se contó en %v", d.Fecha)
		}
	}
	if !desde.IsZero() {
		t.Errorf("un evento de fuera de la ventana movió «desde» a %v", desde)
	}
}
