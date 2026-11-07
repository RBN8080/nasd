package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// Pruebas de la consola 2 (ADR-0105): lo que el rediseño añade o retira y que
// no es solo aspecto.

// EL MAPA SE ENCIENDE CON LO NUEVO Y SOLO CON LO NUEVO. Lo que ya estaba en el
// anillo al abrir el flujo está pintado en el mapa que trajo la página; si
// viajara otra vez, cada pestaña recién abierta encendería medio mundo.
func TestLasLlegadasTraenSoloLoNuevoDeInternetConSuPais(t *testing.T) {
	s := servidorConGeo(t)
	tocarDesde(t, s, "203.0.113.7")
	// Las pausas separan los instantes: el reloj de Windows tiene poca
	// resolución y el rechazo podía quedar anotado en el MISMO instante que
	// «desde», que es justo el borde que se excluye.
	time.Sleep(20 * time.Millisecond)
	desde := time.Now()
	time.Sleep(20 * time.Millisecond)
	tocarDesde(t, s, "203.0.113.8")
	tocarDesde(t, s, "192.168.1.20") // de casa: el mapa no la sitúa

	f, _, _ := filtroDeSeguridad(url.Values{"red": {""}})
	llegadas, ultimo := s.llegadasDesde(f, desde)
	if len(llegadas) != 1 {
		t.Fatalf("se esperaba UNA llegada de Internet y vinieron %d: %+v", len(llegadas), llegadas)
	}
	l := llegadas[0]
	if l.ISO != "CN" || l.Nombre == "" {
		t.Errorf("la llegada no trae su país: %+v", l)
	}
	if l.X <= 0 || l.X >= 1000 || l.Y <= 0 || l.Y >= 501 {
		t.Errorf("el punto (%v, %v) cae fuera del dibujo", l.X, l.Y)
	}
	if !ultimo.After(desde) {
		t.Error("«desde» no avanzó: el marco siguiente volvería a encender lo mismo")
	}
	otra, ultimo2 := s.llegadasDesde(f, ultimo)
	if len(otra) != 0 || !ultimo2.Equal(ultimo) {
		t.Errorf("sin nada nuevo no debe viajar nada ni moverse «desde»: %+v", otra)
	}
}

// SIN BASE DE OPERADORES NO HAY PAÍS QUE ENCENDER, y no viaja nada.
func TestSinBaseNoHayLlegadas(t *testing.T) {
	s := servidorConAuth(t)
	desde := time.Now()
	time.Sleep(20 * time.Millisecond)
	tocarDesde(t, s, "203.0.113.9")
	f, _, _ := filtroDeSeguridad(url.Values{})
	if llegadas, _ := s.llegadasDesde(f, desde); len(llegadas) != 0 {
		t.Errorf("sin base viajaron llegadas: %+v", llegadas)
	}
}

// EL PAÍS SE ENCIENDE EN SU TROZO MÁS GRANDE Y NO EN EL CENTRO DE SU CAJA. Con
// la caja entera, Estados Unidos caería en el Pacífico por Alaska, Francia en el
// Atlántico por la Guayana y Rusia en Europa por Chukotka.
func TestElPaisSeEnciendeEnSuTrozoMasGrandeYNoEnSuCaja(t *testing.T) {
	c := centrosDePais()
	us, ca, fr, ru := c["US"], c["CA"], c["FR"], c["RU"]
	if us == ([2]float64{}) || ca == ([2]float64{}) || fr == ([2]float64{}) || ru == ([2]float64{}) {
		t.Fatal("faltan países en la tabla de centros")
	}
	if us[1] < ca[1]+40 {
		t.Errorf("Estados Unidos (%v) se enciende a la altura de Canadá (%v)", us, ca)
	}
	if fr[0] < 480 {
		t.Errorf("Francia se enciende en el Atlántico: %v", fr)
	}
	if ru[0] < 650 {
		t.Errorf("Rusia se enciende en Europa: %v", ru)
	}
}

// LA VENTANA ES UN ENLACE QUE CONSERVA EL RESTO DEL FILTRO, como la capa del
// mapa: cambiar de 24 h a 7 días no puede perder el motivo elegido.
func TestLaVentanaEsUnEnlaceQueConservaElRestoDelFiltro(t *testing.T) {
	s := servidorConAuth(t)
	cuerpo := panelSeguridad(t, s, "?motivo=credencial_incorrecta")
	if !strings.Contains(cuerpo, `href="/seguridad?horas=168&amp;motivo=credencial_incorrecta"`) {
		t.Error("el enlace de «7 d» no conserva el motivo")
	}
	for _, corta := range []string{">1 h<", ">24 h<", ">7 d<", ">Todo<"} {
		if !strings.Contains(cuerpo, corta) {
			t.Errorf("falta la opción %s en el control de la ventana", corta)
		}
	}
}

// LA BARRA DE USO SE MIDE CONTRA LA CUENTA QUE MÁS OCUPA, y una cuenta que
// ocupa algo nunca se pinta vacía.
func TestLasBarrasDeUsoSeMidenContraLaCuentaQueMasOcupa(t *testing.T) {
	filas := []filaUsuario{{bytes: 400}, {bytes: 100}, {bytes: 1}, {bytes: 0}}
	barrasDeUso(filas)
	quiero := []float64{100, 25, 2, 0}
	for i, f := range filas {
		if f.Barra != quiero[i] {
			t.Errorf("fila %d: barra %v, se esperaba %v", i, f.Barra, quiero[i])
		}
	}
}

// EL MARCO NO LLEVA TIRA DE ESTADO NI VERSIÓN, y la línea del binario de
// /estado va sin rótulo (decisiones del responsable del 2026-11-07).
func TestElMarcoQuedaLimpioYLaLineaDelBinarioVaSinRotulo(t *testing.T) {
	s := servidorConAuth(t)
	cookie := superusuarioEn(t, s)
	cuerpo := pedirDesde(t, s.Rutas(), http.MethodGet, "/estado", "192.168.1.18:5000", cookie).Body.String()
	if strings.Contains(cuerpo, `class="estado"`) || strings.Contains(cuerpo, `class="der"`) {
		t.Error("vuelve a pintarse la tira de estado del pie")
	}
	if !strings.Contains(cuerpo, `<p class="binario"><b>nasd`) {
		t.Error("falta la línea del binario, o no empieza por «nasd»")
	}
	if strings.Contains(cuerpo, ">Binario<") {
		t.Error("la línea del binario vuelve a llevar rótulo")
	}
}

// CUENTAS SIN NOTAS AL PIE.
func TestCuentasSinNotasAlPie(t *testing.T) {
	s := servidorConAuth(t)
	cookie := superusuarioEn(t, s)
	cuerpo := pedirDesde(t, s.Rutas(), http.MethodGet, "/administracion", "192.168.1.18:5000", cookie).Body.String()
	if !strings.Contains(cuerpo, `<div class="t-cta">`) {
		t.Fatal("no se renderizó la tabla de cuentas")
	}
	if strings.Contains(cuerpo, `class="notas"`) {
		t.Error("vuelven a pintarse las notas del pie de Cuentas")
	}
}
