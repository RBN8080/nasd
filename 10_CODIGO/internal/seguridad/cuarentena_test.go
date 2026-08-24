package seguridad

import (
	"net/netip"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// LA CALIBRACIÓN ES LA PIEZA, NO EL MECANISMO.
//
// Apartar a alguien es fácil; lo difícil es no apartar a quien no toca. Este
// NAS lo usa una segunda persona que entra desde fuera de casa, así que un
// umbral mal puesto no produce «una alerta de más»: produce que alguien se
// quede sin sus archivos 24 horas por teclear mal la contraseña.

func cuarentenaDePrueba(t *testing.T) *Cuarentena {
	t.Helper()
	c, err := CargarCuarentena(filepath.Join(t.TempDir(), "cuarentena"))
	if err != nil {
		t.Fatalf("CargarCuarentena: %v", err)
	}
	return c
}

// rechazo compone un evento como lo compondría el servidor, con su red
// clasificada por la función de verdad y no a mano.
func rechazo(ip string, m Motivo, ruta string, cuando time.Time) Evento {
	dir := netip.MustParseAddr(ip)
	return Evento{
		Momento: cuando, Origen: dir, Red: ClasificarRed(dir),
		Metodo: "GET", Ruta: ruta, Estado: 401, Motivo: m,
	}
}

const deFuera = "203.0.113.7"

// Ocho rutas distintas que no existen ya son un escáner, no una navegación.
func TestLaExploracionAparta(t *testing.T) {
	c := cuarentenaDePrueba(t)
	ahora := time.Now()

	var eventos []Evento
	for i := range umbralExploracion {
		eventos = append(eventos, rechazo(deFuera, RutaInexistente, "/inventada-"+strconv.Itoa(i), ahora))
	}

	nuevos := c.Evaluar(PorOrigen(eventos), ahora)
	if len(nuevos) != 1 {
		t.Fatalf("apartados nuevos = %d; se esperaba 1", len(nuevos))
	}
	if nuevos[0].Senal != SenalExploracion {
		t.Errorf("apartado por %v; se esperaba exploración", nuevos[0].Senal)
	}
	if !c.Cubre(netip.MustParseAddr(deFuera), ahora) {
		t.Error("apartado y sin embargo no se le cierra la puerta")
	}
}

// LA PRUEBA QUE PROTEGE A LA SEGUNDA PERSONA: la señal se enseña a los 5
// fallos, pero apartar exige 20. Diecinueve no bastan, y eso es deliberado.
func TestDiecinueveFallosNoApartanYVeinteSi(t *testing.T) {
	for _, c := range []struct {
		fallos int
		aparta bool
	}{
		{umbralFuerzaBruta, false},           // 5: la señal SÍ, la acción no
		{umbralAccionFuerzaBruta - 1, false}, // 19
		{umbralAccionFuerzaBruta, true},      // 20
	} {
		t.Run(strconv.Itoa(c.fallos)+" fallos", func(t *testing.T) {
			q := cuarentenaDePrueba(t)
			ahora := time.Now()

			var eventos []Evento
			for range c.fallos {
				eventos = append(eventos, rechazo(deFuera, CredencialIncorrecta, "/acceso", ahora))
			}
			origenes := PorOrigen(eventos)

			// La SEÑAL tiene que estar desde los cinco: lo que cambia con el
			// umbral de acción es lo que se HACE con ella, no lo que se ve.
			if c.fallos >= umbralFuerzaBruta {
				if len(origenes[0].Senales) == 0 {
					t.Fatal("con esos fallos el panel debería enseñar ya la señal")
				}
			}

			nuevos := q.Evaluar(origenes, ahora)
			if aparto := len(nuevos) > 0; aparto != c.aparta {
				t.Errorf("con %d fallos apartó=%v; se esperaba %v", c.fallos, aparto, c.aparta)
			}
		})
	}
}

// EL SOFTWARE AJENO NO APARTA, Y ES UNA DECISIÓN ESCRITA.
//
// Dispara con UNA sola ruta y su propia Explicacion dice que es rastreo
// indiscriminado, «no algo dirigido a este nodo»; ADR-0065 ya le quitó el
// énfasis por eso. Actuar sobre ella sería la acción más fuerte disparada por
// la señal más débil. Si algún día se decide lo contrario, que sea cambiando
// esta prueba a propósito y no por descuido.
func TestElSoftwareAjenoNoAparta(t *testing.T) {
	c := cuarentenaDePrueba(t)
	ahora := time.Now()

	origenes := PorOrigen([]Evento{rechazo(deFuera, RutaInexistente, "/wp-login.php", ahora)})
	if len(origenes[0].Senales) == 0 {
		t.Fatal("la señal debería seguir enseñándose en el panel")
	}
	if nuevos := c.Evaluar(origenes, ahora); len(nuevos) != 0 {
		t.Errorf("apartó por software ajeno: %+v", nuevos)
	}
}

// La casa y el túnel no se apartan nunca, ni aunque el patrón coincida.
func TestNoSeApartaLoQueNoVieneDeInternet(t *testing.T) {
	for _, ip := range []string{"192.168.1.18", "10.77.0.3", "127.0.0.1"} {
		c := cuarentenaDePrueba(t)
		ahora := time.Now()
		var eventos []Evento
		for i := range umbralExploracion * 2 {
			eventos = append(eventos, rechazo(ip, RutaInexistente, "/inventada-"+strconv.Itoa(i), ahora))
		}
		if nuevos := c.Evaluar(PorOrigen(eventos), ahora); len(nuevos) != 0 {
			t.Errorf("%s: apartó a alguien de dentro", ip)
		}
	}
}

// Caduca sola: no hay tarea de limpieza que pueda fallar por su cuenta.
func TestLaCuarentenaCaducaSola(t *testing.T) {
	c := cuarentenaDePrueba(t)
	ahora := time.Now()
	dir := netip.MustParseAddr(deFuera)

	var eventos []Evento
	for i := range umbralExploracion {
		eventos = append(eventos, rechazo(deFuera, RutaInexistente, "/inventada-"+strconv.Itoa(i), ahora))
	}
	c.Evaluar(PorOrigen(eventos), ahora)

	despues := ahora.Add(DuracionCuarentena + time.Minute)
	if c.Cubre(dir, despues) {
		t.Error("sigue frenando después de caducar")
	}
	if len(c.Vigentes(despues)) != 0 {
		t.Error("un apartado caducado sigue contando como vigente")
	}
	// Y la purga lo retira de verdad en la siguiente evaluación, sin que
	// nadie tenga que barrer aparte.
	c.Evaluar(nil, despues)
	if len(c.Vigentes(ahora)) != 0 {
		t.Error("el apartado caducado no se purgó al evaluar")
	}
}

// Mientras la conducta sigue, el apartado se renueva pero NO se vuelve a
// avisar: si no, el aviso del panel se encendería cada minuto para siempre.
func TestRenovarNoVuelveAAvisar(t *testing.T) {
	c := cuarentenaDePrueba(t)
	ahora := time.Now()

	var eventos []Evento
	for i := range umbralExploracion {
		eventos = append(eventos, rechazo(deFuera, RutaInexistente, "/inventada-"+strconv.Itoa(i), ahora))
	}
	origenes := PorOrigen(eventos)

	if n := len(c.Evaluar(origenes, ahora)); n != 1 {
		t.Fatalf("primera evaluación: %d apartados nuevos; se esperaba 1", n)
	}
	luego := ahora.Add(time.Minute)
	if n := len(c.Evaluar(origenes, luego)); n != 0 {
		t.Errorf("segunda evaluación: %d apartados nuevos; se esperaba 0", n)
	}
	// Renovado, no olvidado: cuenta desde el último hecho.
	v := c.Vigentes(luego)
	if len(v) != 1 || !v[0].Hasta.After(ahora.Add(DuracionCuarentena)) {
		t.Errorf("el apartado no se renovó con la conducta que sigue: %+v", v)
	}
}

// LA CIFRA QUE PERMITE RETIRAR LO QUE NO SIRVE: cuántas conexiones ha cerrado.
//
// LA COMPROBACIÓN QUE ANTES NO EXISTÍA es la segunda mitad: MIRAR no cuenta.
// Hasta que se separó decisión de ejecución, preguntar «¿a este se le cierra la
// puerta?» ya sumaba un frenado, así que la cifra era «coincidencias con la
// política» y el panel la presentaba como conexiones cerradas.
func TestSoloElCierreEjecutadoCuenta(t *testing.T) {
	c := cuarentenaDePrueba(t)
	ahora := time.Now()
	dir := netip.MustParseAddr(deFuera)

	var eventos []Evento
	for i := range umbralExploracion {
		eventos = append(eventos, rechazo(deFuera, RutaInexistente, "/inventada-"+strconv.Itoa(i), ahora))
	}
	c.Evaluar(PorOrigen(eventos), ahora)

	// Mil consultas no son un frenado: son mil consultas.
	for range 1000 {
		c.Cubre(dir, ahora)
	}
	if v := c.Vigentes(ahora); len(v) != 1 || v[0].Frenados != 0 {
		t.Fatalf("consultar la política subió el contador: %+v", v)
	}

	for range 3 {
		c.AnotarCierre(dir, ahora)
	}
	// Y a quien no está apartado no se le cuenta nada.
	c.AnotarCierre(netip.MustParseAddr("198.51.100.9"), ahora)
	if c.Cubre(netip.MustParseAddr("198.51.100.9"), ahora) {
		t.Error("cubre una dirección que no está apartada")
	}

	v := c.Vigentes(ahora)
	if len(v) != 1 || v[0].Frenados != 3 {
		t.Errorf("frenados = %+v; se esperaban 3", v)
	}
	if v[0].UltimoFrenado.IsZero() {
		t.Error("se contó el cierre y no se guardó cuándo fue")
	}
}

func TestSoltarRetiraAntesDeTiempo(t *testing.T) {
	c := cuarentenaDePrueba(t)
	ahora := time.Now()
	dir := netip.MustParseAddr(deFuera)

	var eventos []Evento
	for i := range umbralExploracion {
		eventos = append(eventos, rechazo(deFuera, RutaInexistente, "/inventada-"+strconv.Itoa(i), ahora))
	}
	c.Evaluar(PorOrigen(eventos), ahora)

	if !c.Soltar(dir) {
		t.Fatal("Soltar dijo que no había nada que soltar")
	}
	if c.Cubre(dir, ahora) {
		t.Error("sigue frenando tras soltarlo")
	}
	if c.Soltar(dir) {
		t.Error("Soltar dijo que soltó algo dos veces")
	}
}

// LA RAZÓN DE SER DE ESTA PIEZA: sobrevivir al arranque. Son 15 en 14 días
// medidos en este nodo, así que un apartado que se olvida al reiniciar no
// habría servido de nada.
func TestLaCuarentenaSobreviveAlArranque(t *testing.T) {
	ruta := filepath.Join(t.TempDir(), "cuarentena")
	ahora := time.Now()
	dir := netip.MustParseAddr(deFuera)

	uno, err := CargarCuarentena(ruta)
	if err != nil {
		t.Fatalf("CargarCuarentena: %v", err)
	}
	var eventos []Evento
	for i := range umbralExploracion {
		eventos = append(eventos, rechazo(deFuera, RutaInexistente, "/inventada-"+strconv.Itoa(i), ahora))
	}
	uno.Evaluar(PorOrigen(eventos), ahora)
	uno.AnotarCierre(dir, ahora)
	if err := uno.Volcar(); err != nil {
		t.Fatalf("Volcar: %v", err)
	}

	otro, err := CargarCuarentena(ruta)
	if err != nil {
		t.Fatalf("CargarCuarentena tras el reinicio: %v", err)
	}
	if !otro.Cubre(dir, ahora) {
		t.Fatal("tras reiniciar, el apartado se perdió")
	}
	v := otro.Vigentes(ahora)
	if len(v) != 1 {
		t.Fatalf("vigentes tras reiniciar = %d; se esperaba 1", len(v))
	}
	// La señal viaja por su CLAVE estable, no por el número del enum ni por
	// la etiqueta: es lo que permite reescribir un rótulo sin dejar ilegible
	// lo ya guardado.
	if v[0].Senal != SenalExploracion {
		t.Errorf("la señal no sobrevivió al archivo: %v", v[0].Senal)
	}
	// LA CUENTA SOBREVIVE Y SIGUE SUMANDO. Antes esta comprobación esperaba 2
	// —uno de antes del reinicio y otro de la propia consulta de arriba—, y esa
	// segunda unidad era el defecto: preguntar «¿a este se le cierra la
	// puerta?» sumaba un frenado sin que se hubiera cerrado nada. Ahora consultar
	// es gratis, así que sigue en 1, y solo un cierre ejecutado la mueve.
	if v[0].Frenados != 1 {
		t.Errorf("frenados tras reiniciar = %d; se esperaba 1", v[0].Frenados)
	}
	otro.AnotarCierre(dir, ahora)
	if n := otro.Vigentes(ahora)[0].Frenados; n != 2 {
		t.Errorf("frenados tras cerrar otra conexión = %d; se esperaban 2", n)
	}
}
