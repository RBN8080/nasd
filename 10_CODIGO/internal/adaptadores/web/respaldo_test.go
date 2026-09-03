package web

import (
	"strings"
	"testing"
	"time"

	"nasd/internal/respaldo"
)

// El indicador del respaldo del equipo — 30_CLIENTE_RESPALDO §11.
//
// LO QUE ESTAS PRUEBAS PROTEGEN es una sola propiedad, y es la razón de existir
// del indicador: que un respaldo VIEJO no se pueda pintar de verde. El archivo
// que publica el cliente no envejece solo —sigue diciendo «Protegido» para
// siempre—, así que preguntar por su palabra antes que por su edad dejaría un
// equipo apagado hace una semana en verde. Es el punto ciego que §10.2 declara
// del icono del propio equipo.

func lectura(e respaldo.Estado) Instantanea {
	return Instantanea{Respaldo: respaldo.Lectura{Configurado: true, Presente: true, Estado: e}}
}

func unico(t *testing.T, i Instantanea, ahora time.Time) indicador {
	t.Helper()
	is := evaluarRespaldo(i, ahora)
	if len(is) != 1 {
		t.Fatalf("se esperaba un indicador; salieron %d", len(is))
	}
	return is[0]
}

// SIN CONFIGURAR NO SE PINTA. Un semáforo permanentemente gris enseña a ignorar
// el panel entero, que es lo que ADR-0065 corrigió retirando 24 elementos.
func TestElRespaldoNoSePintaSiNoEstaConfigurado(t *testing.T) {
	if is := evaluarRespaldo(Instantanea{}, time.Now()); len(is) != 0 {
		t.Errorf("sin cliente configurado no debe haber indicador; salieron %d", len(is))
	}
}

func TestUnRespaldoFrescoYBuenoEsVerde(t *testing.T) {
	ahora := time.Date(2026, 9, 2, 20, 0, 0, 0, time.Local)
	ind := unico(t, lectura(respaldo.Estado{
		Veredicto:       "Protegido",
		Detalle:         "8 raices al dia, 7 archivos copiados",
		Momento:         ahora.Add(-4 * time.Hour),
		HorasParaAvisar: 22,
		Raices:          8,
	}), ahora)

	if ind.Veredicto != vOK {
		t.Errorf("veredicto = %q; se esperaba ok", ind.Veredicto)
	}
	// Se reutiliza duracionLegible, el mismo formateador que ya escribe el
	// tiempo en pie del servicio: dos formas de decir «4 h» en la misma página
	// es como se acaban leyendo mal las dos.
	if !strings.Contains(ind.Valor, "4 h") {
		t.Errorf("el valor no dice la edad: %q", ind.Valor)
	}
}

// LA PRUEBA QUE JUSTIFICA EL INDICADOR ENTERO.
func TestUnProtegidoVIEJONoSePintaDeVerde(t *testing.T) {
	ahora := time.Date(2026, 9, 2, 20, 0, 0, 0, time.Local)
	ind := unico(t, lectura(respaldo.Estado{
		Veredicto:       "Protegido",
		Detalle:         "8 raices al dia",
		Momento:         ahora.Add(-30 * time.Hour),
		HorasParaAvisar: 22,
	}), ahora)

	if ind.Veredicto != vAtencion {
		t.Fatalf("un Protegido de hace 30 h salió como %q; DEBE ser atención", ind.Veredicto)
	}
	if ind.Accion == "" {
		t.Errorf("el charter §8 exige que toda alerta sea accionable, y esta no dice qué hacer")
	}
	if !strings.Contains(ind.Valor, "22 h") {
		t.Errorf("el valor no dice contra qué umbral se juzgó: %q", ind.Valor)
	}
}

// EL UMBRAL ES EL DEL CLIENTE, NO EL DEL NODO. Si el nodo se escribiera su
// propio número, el día que el cliente cambiara sus ventanas las dos pantallas
// se contradirían sin que nada fallara (05_OPERACION §4.1).
func TestMandaElUmbralQuePublicaElCliente(t *testing.T) {
	ahora := time.Date(2026, 9, 2, 20, 0, 0, 0, time.Local)
	// 30 h de antigüedad, pero el cliente promete 48: todavía no le tocaba.
	ind := unico(t, lectura(respaldo.Estado{
		Veredicto:       "Protegido",
		Momento:         ahora.Add(-30 * time.Hour),
		HorasParaAvisar: 48,
	}), ahora)
	if ind.Veredicto != vOK {
		t.Errorf("con el umbral del cliente en 48 h, 30 h no es tarde; salió %q", ind.Veredicto)
	}
}

// Y SI EL CLIENTE NO LO PUBLICA, SE USA EL DE REPUESTO Y SE DICE. Un umbral de
// repuesto que no se anuncia es indistinguible del criterio real.
func TestSinUmbralPublicadoSeUsaElDeRepuestoYSeAnuncia(t *testing.T) {
	ahora := time.Date(2026, 9, 2, 20, 0, 0, 0, time.Local)
	ind := unico(t, lectura(respaldo.Estado{
		Veredicto: "Protegido",
		Momento:   ahora.Add(-30 * time.Hour),
	}), ahora)

	if ind.Veredicto != vAtencion {
		t.Fatalf("veredicto = %q", ind.Veredicto)
	}
	if !strings.Contains(ind.Valor, "no publicó su umbral") {
		t.Errorf("no se anuncia que el umbral es de repuesto: %q", ind.Valor)
	}
}

func TestElFallonDelClienteEsRojoYElFrenoNoLoEs(t *testing.T) {
	ahora := time.Date(2026, 9, 2, 20, 0, 0, 0, time.Local)

	falla := unico(t, lectura(respaldo.Estado{
		Veredicto: "Falla", Detalle: "2 de 8 raices no se copiaron bien",
		Momento: ahora.Add(-time.Hour), HorasParaAvisar: 22,
	}), ahora)
	if falla.Veredicto != vFallo {
		t.Errorf("una corrida fallida salió como %q; se esperaba fallo", falla.Veredicto)
	}

	// El freno es una PARADA PRUDENTE que espera una decisión de una persona,
	// no una avería. El cliente ya lo separa así en su propio icono, y aquí no
	// se estrena un criterio distinto.
	freno := unico(t, lectura(respaldo.Estado{
		Veredicto: "Atencion", Detalle: "FRENO: 3 de 8 raices rebasan el umbral",
		Momento: ahora.Add(-time.Hour), HorasParaAvisar: 22,
	}), ahora)
	if freno.Veredicto != vAtencion {
		t.Errorf("el freno salió como %q; se esperaba atención", freno.Veredicto)
	}
}

// UNA CORRIDA FRESCA CON RAÍCES FALLIDAS NO ES VERDE aunque el cliente la haya
// dejado en Protegido: el contador manda sobre la palabra.
func TestUnasRaicesFallidasBajanElVerdeAunqueElClienteDigaProtegido(t *testing.T) {
	ahora := time.Date(2026, 9, 2, 20, 0, 0, 0, time.Local)
	ind := unico(t, lectura(respaldo.Estado{
		Veredicto: "Protegido", Momento: ahora.Add(-time.Hour),
		HorasParaAvisar: 22, Fallos: 2,
	}), ahora)
	if ind.Veredicto != vAtencion {
		t.Errorf("con 2 fallos salió %q; se esperaba atención", ind.Veredicto)
	}
}

// UNA MARCA DEL FUTURO NO SE ENSEÑA COMO «RECIÉN HECHO». La resta daría una
// edad negativa, que es una mentira tranquilizadora — el peor tipo.
func TestUnaMarcaDelFuturoSeDiceEnVezDeMentir(t *testing.T) {
	ahora := time.Date(2026, 9, 2, 20, 0, 0, 0, time.Local)
	ind := unico(t, lectura(respaldo.Estado{
		Veredicto: "Protegido", Momento: ahora.Add(3 * time.Hour), HorasParaAvisar: 22,
	}), ahora)

	if ind.Veredicto == vOK {
		t.Errorf("una marca del futuro se pintó de verde: %q", ind.Valor)
	}
	if !strings.Contains(ind.Valor, "por delante") {
		t.Errorf("no se explica el desajuste de relojes: %q", ind.Valor)
	}
}

func TestSinMomentoNoSePuedeAfirmarNada(t *testing.T) {
	ind := unico(t, lectura(respaldo.Estado{Veredicto: "Protegido", HorasParaAvisar: 22}), time.Now())
	if ind.Veredicto != vAtencion {
		t.Errorf("un estado sin marca de tiempo salió %q", ind.Veredicto)
	}
}

// LAS TRES AUSENCIAS SON TRES COSAS DISTINTAS y piden respuestas distintas.
func TestLasTresFormasDeNoTenerEstado(t *testing.T) {
	ahora := time.Now()

	sinPublicar := unico(t, Instantanea{Respaldo: respaldo.Lectura{Configurado: true}}, ahora)
	if sinPublicar.Veredicto != vDesconocido {
		t.Errorf("«todavía no ha corrido» salió como %q; se esperaba desconocido", sinPublicar.Veredicto)
	}

	ilegible := unico(t, Instantanea{Respaldo: respaldo.Lectura{
		Configurado: true, Presente: true, Error: respaldo.ErrVacio,
	}}, ahora)
	if ilegible.Veredicto != vAtencion {
		t.Errorf("un archivo presente e inservible salió como %q", ilegible.Veredicto)
	}

	roto := unico(t, Instantanea{Respaldo: respaldo.Lectura{
		Configurado: true, Error: errorDePermisos{},
	}}, ahora)
	if roto.Veredicto != vFallo {
		t.Errorf("un error de E/S salió como %q; se esperaba fallo", roto.Veredicto)
	}
	if roto.Accion == "" {
		t.Errorf("un fallo sin acción no es accionable (charter §8)")
	}
}

type errorDePermisos struct{}

func (errorDePermisos) Error() string { return "permiso denegado" }
