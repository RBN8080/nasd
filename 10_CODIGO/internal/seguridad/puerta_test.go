package seguridad

import (
	"net/netip"
	"testing"
	"time"
)

// LA PUERTA, AL NIVEL DEL DOMINIO: quién cierra, quién se lleva el mérito y
// cuándo sube la cifra.
//
// Lo que se prueba aquí no se puede probar contra el HTML ni contra un
// servidor: son propiedades de la CONTABILIDAD, y una prueba de integración
// las daría por buenas mientras la página enseñara algún número.

func apartadoDePrueba(t *testing.T, c *Cuarentena, ip string, ahora time.Time) netip.Addr {
	t.Helper()
	dir := netip.MustParseAddr(ip)
	var eventos []Evento
	for i := range umbralExploracion {
		eventos = append(eventos, rechazo(ip, RutaInexistente, "/inventada-"+itoa(i), ahora))
	}
	if n := len(c.Evaluar(PorOrigen(eventos), ahora)); n != 1 {
		t.Fatalf("no se pudo apartar a %s: %d apartados nuevos", ip, n)
	}
	return dir
}

// D9 — UNA CONEXIÓN CERRADA SUBE EXACTAMENTE UNA VEZ EL CONTADOR.
func TestUnCierreCuentaUnaSolaVez(t *testing.T) {
	c, l := cuarentenaDePrueba(t), listaDePrueba(t)
	ahora := time.Now()
	dir := apartadoDePrueba(t, c, deFuera, ahora)

	cierre := Decidir(c, l, dir, ahora)
	if !cierre.Cierra() {
		t.Fatal("no se decidió cerrar a un apartado")
	}
	if cierre.Responsable() != PoliticaCuarentena {
		t.Errorf("responsable = %v; se esperaba la cuarentena", cierre.Responsable())
	}
	// DECIDIR NO CUENTA. Es la mitad del defecto que esto corrige: antes, la
	// misma llamada que decidía sumaba el frenado, y el cierre venía después
	// —o no venía—.
	if v := c.Vigentes(ahora); v[0].Frenados != 0 {
		t.Fatalf("decidir ya contó %d frenados", v[0].Frenados)
	}

	cierre.Ejecutado(ahora)
	if v := c.Vigentes(ahora); v[0].Frenados != 1 {
		t.Errorf("frenados = %d; se esperaba exactamente 1", v[0].Frenados)
	}
}

// D10 — LA MISMA DIRECCIÓN EN LOS DOS CONTROLES.
//
// # LA FICCIÓN QUE ESTA PRUEBA IMPIDE
//
// Una conexión TCP se cierra UNA vez. Si las dos políticas contaran, el panel
// diría «2 frenados» donde hubo un cierre, y esa cifra no se podría contrastar
// con nada. El «||» de la versión anterior evitaba el doble conteo por
// accidente —cortocircuitaba— pero dejaba la atribución sin decidir y sin
// escribir: bastaba con reordenar los dos términos para cambiarla.
func TestUnaConexionCubiertaPorLosDosControlesNoCuentaDosVeces(t *testing.T) {
	c, l := cuarentenaDePrueba(t), listaDePrueba(t)
	ahora := time.Now()
	dir := apartadoDePrueba(t, c, deFuera, ahora)

	e, err := l.Anadir(entradaDe("203.0.113.0/24", "reputación del rango"), netip.Addr{}, ahora)
	if err != nil {
		t.Fatalf("Anadir: %v", err)
	}

	cierre := Decidir(c, l, dir, ahora)
	if !cierre.Cierra() {
		t.Fatal("no se decidió cerrar")
	}
	// LA PRECEDENCIA DOCUMENTADA: manda la decisión que firmó una persona.
	if cierre.Responsable() != PoliticaLista {
		t.Errorf("responsable = %v; la precedencia dice que manda la lista manual",
			cierre.Responsable())
	}
	// Y SE SABE QUE COINCIDÍAN LAS DOS. Sin esto, un apartado con cero frenados
	// se leería como «no ha servido de nada» cuando lo que pasa es que la
	// puerta ya estaba cerrada por otro.
	if cierre.Solapada() != PoliticaCuarentena {
		t.Errorf("solapada = %v; también coincidía la cuarentena", cierre.Solapada())
	}

	cierre.Ejecutado(ahora)

	if n := l.Vigentes(ahora)[0].Frenados; n != 1 {
		t.Errorf("frenados de la lista = %d; se esperaba 1", n)
	}
	if n := c.Vigentes(ahora)[0].Frenados; n != 0 {
		t.Errorf("frenados de la cuarentena = %d; una conexión no puede contar dos veces", n)
	}
	// La suma de los dos contadores tiene que seguir siendo UNO: es la
	// comprobación que traduce «no inventes una ficción» a un número.
	total := l.Vigentes(ahora)[0].Frenados + c.Vigentes(ahora)[0].Frenados
	if total != 1 {
		t.Errorf("frenados en total = %d; hubo UNA conexión cerrada", total)
	}
	if e.ID == "" {
		t.Error("la entrada se guardó sin identificador")
	}
}

// Con SOLO la lista, el mérito es suyo y la cuarentena ni aparece.
func TestSoloLaListaCierraYSoloEllaCuenta(t *testing.T) {
	c, l := cuarentenaDePrueba(t), listaDePrueba(t)
	ahora := time.Now()
	if _, err := l.Anadir(entradaDe("203.0.113.0/24", "reputación del rango"), netip.Addr{}, ahora); err != nil {
		t.Fatalf("Anadir: %v", err)
	}

	cierre := Decidir(c, l, netip.MustParseAddr(deFuera), ahora)
	if cierre.Responsable() != PoliticaLista || cierre.Solapada() != PoliticaNinguna {
		t.Fatalf("responsable=%v solapada=%v", cierre.Responsable(), cierre.Solapada())
	}
	cierre.Ejecutado(ahora)
	if n := l.Vigentes(ahora)[0].Frenados; n != 1 {
		t.Errorf("frenados = %d; se esperaba 1", n)
	}
}

// D1 — a quien no cubre ninguna política no se le cierra nada.
func TestSinPoliticaNoSeCierraNada(t *testing.T) {
	c, l := cuarentenaDePrueba(t), listaDePrueba(t)
	ahora := time.Now()

	cierre := Decidir(c, l, netip.MustParseAddr("198.51.100.9"), ahora)
	if cierre.Cierra() {
		t.Fatal("se decidió cerrar a quien no cubre ninguna política")
	}
	if cierre.Responsable() != PoliticaNinguna {
		t.Errorf("responsable = %v; se esperaba ninguna", cierre.Responsable())
	}
	// Y Ejecutado sobre un cierre vacío no puede tocar nada. Es el caso que
	// ocurriría si alguien llamara sin comprobar Cierra(): tiene que ser inerte
	// en vez de sumar un frenado inventado.
	cierre.Ejecutado(ahora)
	if len(c.Vigentes(ahora)) != 0 || len(l.Vigentes(ahora)) != 0 {
		t.Error("un cierre vacío tocó algo")
	}
}

// Cerrar es idempotente para el CONTADOR solo en el sentido de que cada
// llamada cuenta una: dos conexiones cerradas son dos frenados, y esa es la
// propiedad que hace comparable la cifra con el anillo de conexiones.
func TestCadaConexionCerradaSumaLaSuya(t *testing.T) {
	c, l := cuarentenaDePrueba(t), listaDePrueba(t)
	ahora := time.Now()
	dir := apartadoDePrueba(t, c, deFuera, ahora)

	for range 4 {
		cierre := Decidir(c, l, dir, ahora)
		if !cierre.Cierra() {
			t.Fatal("dejó de cerrar a mitad")
		}
		cierre.Ejecutado(ahora)
	}
	if n := c.Vigentes(ahora)[0].Frenados; n != 4 {
		t.Errorf("frenados = %d; se cerraron 4 conexiones", n)
	}
}
