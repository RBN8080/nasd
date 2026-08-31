package seguridad

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// La gráfica de actividad de /seguridad se pinta con el conteo POR DÍA que el
// anillo persiste (pordia.go), no recorriendo los eventos que la página tenga
// a mano. Estas pruebas sujetan las dos mitades de esa decisión: que la serie
// siga siendo honesta sobre hasta dónde alcanza, y que de verdad SOBREVIVA —
// al reinicio y a la rotación del anillo—, que es justo lo que no hacía.

// ahoraALasDoceDelMediodia fija la hora del «ahora» de cada prueba a mediodía
// para que ninguna quede a merced de la hora local en la que corra: medianoche
// es el único instante en que un desfase de zona horaria puede empujar
// «ahora» al día de al lado y desbaratar el cálculo de «hoy».
func ahoraALasDoceDelMediodia() time.Time {
	n := time.Now()
	return time.Date(n.Year(), n.Month(), n.Day(), 12, 0, 0, 0, n.Location())
}

// anilloEnDisco deja un anillo recién creado sobre un archivo temporal y
// devuelve también la ruta, porque media docena de estas pruebas necesitan
// reabrirlo para comprobar que lo escrito se recupera.
func anilloEnDisco(t *testing.T) (*Anillo, string) {
	t.Helper()
	ruta := filepath.Join(t.TempDir(), "seguridad")
	a, err := CargarAnillo(ruta)
	if err != nil {
		t.Fatalf("CargarAnillo: %v", err)
	}
	return a, ruta
}

func TestLaSerieTieneSiempreDiasGraficaColumnas(t *testing.T) {
	a, _ := anilloEnDisco(t)
	serie, desde := a.Serie(ahoraALasDoceDelMediodia(), nil)
	if len(serie) != DiasGrafica {
		t.Fatalf("la serie tiene %d columnas, se esperaban %d", len(serie), DiasGrafica)
	}
	if !desde.IsZero() {
		t.Errorf("sin eventos, «desde» debería ser cero y es %v", desde)
	}
	for _, d := range serie {
		if d.Rechazos != 0 {
			t.Errorf("día %v con %d rechazos sin haber anotado nada", d.Fecha, d.Rechazos)
		}
	}
}

// EL ÚLTIMO DÍA DE LA SERIE ES HOY, y el primero son DiasGrafica-1 días
// atrás: la ventana termina en el instante que se pide, no en la medianoche
// que ya pasó.
func TestLaSerieTerminaHoyYEmpiezaDiasGraficaAtras(t *testing.T) {
	a, _ := anilloEnDisco(t)
	ahora := ahoraALasDoceDelMediodia()
	serie, _ := a.Serie(ahora, nil)

	hoy := medianocheLocal(ahora)
	if ultimo := serie[len(serie)-1].Fecha; !ultimo.Equal(hoy) {
		t.Errorf("el último día es %v, se esperaba hoy (%v)", ultimo, hoy)
	}
	esperado := hoy.AddDate(0, 0, -(DiasGrafica - 1))
	if primero := serie[0].Fecha; !primero.Equal(esperado) {
		t.Errorf("el primer día es %v, se esperaba %v", primero, esperado)
	}
}

// DOS EVENTOS DEL MISMO DÍA CAEN EN LA MISMA COLUMNA, y una ráfaga de 20
// conexiones en cinco minutos —el DRIFTNET del 16/08 real— produce UN punto
// alto, no veinte columnas que no caben.
func TestDosEventosDelMismoDiaSeSumanEnUnaColumna(t *testing.T) {
	a, _ := anilloEnDisco(t)
	ahora := ahoraALasDoceDelMediodia()
	hoy := medianocheLocal(ahora)
	a.Anotar(ev(hoy.Add(2*time.Hour), "203.0.113.7", SinSesion, "/"))
	a.Anotar(ev(hoy.Add(3*time.Hour), "203.0.113.8", SinSesion, "/"))

	serie, _ := a.Serie(ahora, nil)
	if got := serie[len(serie)-1].Rechazos; got != 2 {
		t.Fatalf("la columna de hoy tiene %d rechazos, se esperaban 2", got)
	}
}

// «DESDE» ES EL DÍA MÁS ANTIGUO CON DATO, no el primero de la serie: una
// gráfica recién estrenada tiene once columnas vacías, y decir «desde hace
// doce días» prometería una profundidad que todavía no existe. Es la misma
// honestidad que PaquetesDesde aplica en panel_seguridad.go.
func TestDesdeEsElDiaMasAntiguoConDato(t *testing.T) {
	a, _ := anilloEnDisco(t)
	ahora := ahoraALasDoceDelMediodia()
	hoy := medianocheLocal(ahora)
	masAntiguo := hoy.AddDate(0, 0, -3)
	a.Anotar(ev(masAntiguo.Add(5*time.Hour), "203.0.113.7", SinSesion, "/"))
	a.Anotar(ev(hoy.Add(time.Hour), "203.0.113.8", SinSesion, "/"))

	_, desde := a.Serie(ahora, nil)
	if !desde.Equal(masAntiguo) {
		t.Errorf("desde = %v, se esperaba el día más antiguo con dato (%v)", desde, masAntiguo)
	}
}

// UN EVENTO ANTERIOR A LA VENTANA NO CUENTA EN NINGUNA COLUMNA NI EMPUJA
// «DESDE» HACIA ATRÁS: la gráfica declara DiasGrafica días y ni uno más. Si el
// anillo guarda historia más vieja, esa historia vive en la nota de
// «Historial: N conservados de M vistos» (seguridad.html), no aquí.
func TestUnEventoAnteriorALaVentanaNoCuenta(t *testing.T) {
	a, _ := anilloEnDisco(t)
	ahora := ahoraALasDoceDelMediodia()
	hoy := medianocheLocal(ahora)
	a.Anotar(ev(hoy.AddDate(0, 0, -(DiasGrafica+5)), "203.0.113.7", SinSesion, "/"))

	serie, desde := a.Serie(ahora, nil)
	for _, d := range serie {
		if d.Rechazos != 0 {
			t.Errorf("un evento de fuera de la ventana se contó en %v", d.Fecha)
		}
	}
	if !desde.IsZero() {
		t.Errorf("un evento de fuera de la ventana movió «desde» a %v", desde)
	}
}

// LA SERIE OBEDECE AL FILTRO DE ORIGEN, que es el que gobierna toda la página.
//
// El panel abre en «Internet» por encargo del responsable, y en este nodo la
// LAN es casi todo el tráfico: si la gráfica sumara las dos redes, dibujaría
// una curva que no corresponde a ninguna de las tablas que tiene debajo.
func TestLaSerieSeparaPorRed(t *testing.T) {
	a, _ := anilloEnDisco(t)
	ahora := ahoraALasDoceDelMediodia()
	hoy := medianocheLocal(ahora)
	a.Anotar(ev(hoy.Add(time.Hour), "203.0.113.7", SinSesion, "/"))
	for range 4 {
		a.Anotar(ev(hoy.Add(time.Hour), "192.168.1.50", SinSesion, "/"))
	}

	internet := RedInternet
	serie, _ := a.Serie(ahora, &internet)
	if got := serie[len(serie)-1].Rechazos; got != 1 {
		t.Errorf("con el filtro en Internet la columna de hoy tiene %d, se esperaba 1", got)
	}
	// Sin filtro se suman las redes, exactamente como hace «Cualquier origen»
	// en la tabla.
	serie, _ = a.Serie(ahora, nil)
	if got := serie[len(serie)-1].Rechazos; got != 5 {
		t.Errorf("sin filtro la columna de hoy tiene %d, se esperaban 5", got)
	}
}

// EL DEFECTO QUE ORIGINÓ TODO ESTO, convertido en prueba: el histórico de días
// anteriores no sobrevivía al reinicio.
//
// Antes la serie se calculaba de los eventos que la página tenía delante, así
// que dependía por completo de lo que el anillo conservara. Ahora el conteo
// baja a disco con el propio anillo y se recupera al arrancar.
func TestElHistoricoPorDiaSobreviveAlReinicio(t *testing.T) {
	a, ruta := anilloEnDisco(t)
	ahora := ahoraALasDoceDelMediodia()
	hoy := medianocheLocal(ahora)
	ayer := hoy.AddDate(0, 0, -1)
	anteayer := hoy.AddDate(0, 0, -2)

	a.Anotar(ev(anteayer.Add(9*time.Hour), "203.0.113.7", SinSesion, "/"))
	a.Anotar(ev(ayer.Add(9*time.Hour), "203.0.113.7", SinSesion, "/"))
	a.Anotar(ev(ayer.Add(10*time.Hour), "203.0.113.8", SinSesion, "/"))
	a.Anotar(ev(hoy.Add(9*time.Hour), "203.0.113.9", SinSesion, "/"))
	if err := a.Volcar(); err != nil {
		t.Fatalf("Volcar: %v", err)
	}

	otro, err := CargarAnillo(ruta) // el «reinicio»
	if err != nil {
		t.Fatalf("CargarAnillo tras el reinicio: %v", err)
	}
	serie, desde := otro.Serie(ahora, nil)
	esperado := map[string]int{
		anteayer.Format(formatoDia): 1,
		ayer.Format(formatoDia):     2,
		hoy.Format(formatoDia):      1,
	}
	for _, d := range serie {
		if got, quiere := d.Rechazos, esperado[d.Fecha.Format(formatoDia)]; got != quiere {
			t.Errorf("tras el reinicio, %s tiene %d rechazos y se esperaban %d",
				d.Fecha.Format(formatoDia), got, quiere)
		}
	}
	if !desde.Equal(anteayer) {
		t.Errorf("tras el reinicio, desde = %v; se esperaba %v", desde, anteayer)
	}
}

// LA OTRA MITAD DEL DEFECTO: el anillo OLVIDA, y el conteo por día no.
//
// Un barrido llena las 2000 posiciones del buffer y empuja fuera los días
// anteriores —el de DRIFTNET fueron 732 rechazos en una tarde—. Mientras la
// serie se derivaba de los eventos, esa ráfaga borraba las columnas ya
// dibujadas. El conteo no cabe en el anillo porque no crece con el tráfico.
func TestElHistoricoPorDiaSobreviveALaRotacionDelAnillo(t *testing.T) {
	a, ruta := anilloEnDisco(t)
	ahora := ahoraALasDoceDelMediodia()
	hoy := medianocheLocal(ahora)
	ayer := hoy.AddDate(0, 0, -1)

	a.Anotar(ev(ayer.Add(9*time.Hour), "203.0.113.7", SinSesion, "/"))
	// La ráfaga de hoy expulsa del buffer al único evento de ayer.
	for range Capacidad {
		a.Anotar(ev(hoy.Add(9*time.Hour), "203.0.113.8", SinSesion, "/"))
	}
	if quedan := a.Desde(ayer); len(quedan) != Capacidad {
		t.Fatalf("el buffer conserva %d eventos, se esperaban %d", len(quedan), Capacidad)
	}
	if err := a.Volcar(); err != nil {
		t.Fatalf("Volcar: %v", err)
	}

	otro, err := CargarAnillo(ruta)
	if err != nil {
		t.Fatalf("CargarAnillo: %v", err)
	}
	serie, _ := otro.Serie(ahora, nil)
	deAyer := serie[len(serie)-2]
	if deAyer.Rechazos != 1 {
		t.Errorf("la columna de ayer tiene %d rechazos tras la ráfaga; se esperaba 1", deAyer.Rechazos)
	}
	if hoyCol := serie[len(serie)-1]; hoyCol.Rechazos != Capacidad {
		t.Errorf("la columna de hoy tiene %d rechazos, se esperaban %d", hoyCol.Rechazos, Capacidad)
	}
}

// UN ARCHIVO SIN LAS LÍNEAS «# dia:» —el que dejó la versión anterior— no
// pierde la gráfica: el conteo se reconstruye con los eventos que el buffer sí
// trae. Es el mismo criterio con el que releer resuelve marcaTotal, y lo que
// hace que esto no nazca en blanco el día que se despliega.
func TestUnArchivoSinConteoSeReconstruyeConLosEventos(t *testing.T) {
	a, ruta := anilloEnDisco(t)
	ahora := ahoraALasDoceDelMediodia()
	hoy := medianocheLocal(ahora)
	a.Anotar(ev(hoy.AddDate(0, 0, -1).Add(9*time.Hour), "203.0.113.7", SinSesion, "/"))
	a.Anotar(ev(hoy.Add(9*time.Hour), "203.0.113.8", SinSesion, "/"))
	if err := a.Volcar(); err != nil {
		t.Fatalf("Volcar: %v", err)
	}

	// Se le quitan al archivo las líneas del conteo, dejándolo como lo
	// escribiría una versión anterior.
	crudo, err := os.ReadFile(ruta)
	if err != nil {
		t.Fatalf("leer: %v", err)
	}
	var sinConteo []string
	for _, l := range strings.Split(string(crudo), "\n") {
		if !strings.HasPrefix(l, marcaDia) {
			sinConteo = append(sinConteo, l)
		}
	}
	if err := os.WriteFile(ruta, []byte(strings.Join(sinConteo, "\n")), 0o600); err != nil {
		t.Fatalf("escribir: %v", err)
	}

	otro, err := CargarAnillo(ruta)
	if err != nil {
		t.Fatalf("CargarAnillo: %v", err)
	}
	serie, _ := otro.Serie(ahora, nil)
	if got := serie[len(serie)-1].Rechazos; got != 1 {
		t.Errorf("la columna de hoy tiene %d rechazos, se esperaba 1", got)
	}
	if got := serie[len(serie)-2].Rechazos; got != 1 {
		t.Errorf("la columna de ayer tiene %d rechazos, se esperaba 1", got)
	}
}

// UNA LÍNEA DE CONTEO ILEGIBLE NO TUMBA EL HISTORIAL. Al revés que un evento
// mal formado —que significa archivo corrupto y hay que enterarse—, esto es un
// resumen reconstruible: negarse a arrancar con el historial entero por una
// columna de una gráfica sería cambiar lo importante por lo accesorio.
func TestUnaLineaDeConteoIlegibleSeDescartaSinRomperNada(t *testing.T) {
	a, ruta := anilloEnDisco(t)
	ahora := ahoraALasDoceDelMediodia()
	a.Anotar(ev(ahora, "203.0.113.7", SinSesion, "/"))
	if err := a.Volcar(); err != nil {
		t.Fatalf("Volcar: %v", err)
	}
	crudo, err := os.ReadFile(ruta)
	if err != nil {
		t.Fatalf("leer: %v", err)
	}
	roto := marcaDia + "no-es-una-fecha internet=x\n" + string(crudo)
	if err := os.WriteFile(ruta, []byte(roto), 0o600); err != nil {
		t.Fatalf("escribir: %v", err)
	}

	otro, err := CargarAnillo(ruta)
	if err != nil {
		t.Fatalf("una línea de conteo ilegible tumbó la carga: %v", err)
	}
	serie, _ := otro.Serie(ahora, nil)
	if got := serie[len(serie)-1].Rechazos; got != 1 {
		t.Errorf("la columna de hoy tiene %d rechazos, se esperaba 1", got)
	}
}

// EL CONTEO NO CRECE SIN LÍMITE: al volcar se poda a DiasGrafica días. Sin
// esto, un nodo en marcha durante un año llevaría 365 líneas de cabecera en un
// archivo que se reescribe cada minuto.
func TestElConteoSePodaAlVolcar(t *testing.T) {
	a, ruta := anilloEnDisco(t)
	ahora := ahoraALasDoceDelMediodia()
	hoy := medianocheLocal(ahora)
	for i := range DiasGrafica + 8 {
		a.Anotar(ev(hoy.AddDate(0, 0, -i).Add(9*time.Hour), "203.0.113.7", SinSesion, "/"))
	}
	if err := a.Volcar(); err != nil {
		t.Fatalf("Volcar: %v", err)
	}
	crudo, err := os.ReadFile(ruta)
	if err != nil {
		t.Fatalf("leer: %v", err)
	}
	if n := strings.Count(string(crudo), marcaDia); n != DiasGrafica {
		t.Errorf("el archivo guarda %d días de conteo, se esperaban %d", n, DiasGrafica)
	}
}
