package seguridad

import (
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func ev(t time.Time, ip string, m Motivo, ruta string) Evento {
	e := Evento{
		Momento: t,
		Origen:  netip.MustParseAddr(ip),
		Metodo:  "GET",
		Ruta:    ruta,
		Estado:  401,
		Motivo:  m,
	}
	e.Red = ClasificarRed(e.Origen)
	return e
}

func TestElUsoNormalNoLevantaNingunaSenal(t *testing.T) {
	base := time.Now()
	var eventos []Evento
	// El iPhone del responsable, por el túnel, sin cookie tras un rato.
	for i := range 30 {
		eventos = append(eventos, ev(base.Add(time.Duration(i)*time.Second),
			"10.77.0.3", SinSesion, "/"))
	}
	// Y alguien de casa que falla la contraseña un par de veces.
	for i := range 2 {
		eventos = append(eventos, ev(base.Add(time.Duration(i)*time.Minute),
			"192.168.1.18", CredencialIncorrecta, "/acceso"))
	}

	for _, o := range PorOrigen(eventos) {
		if len(o.Senales) != 0 {
			t.Fatalf("el uso normal de %v levantó la señal %q",
				o.IP, o.Senales[0].Etiqueta())
		}
	}
}

// Y al revés: un escáner de verdad sí se marca. Sin esta mitad, la anterior
// se cumpliría no señalando nunca nada.
func TestUnEscanerDeVerdadSiSeMarca(t *testing.T) {
	base := time.Now()
	var eventos []Evento
	for i := range umbralExploracion + 2 {
		eventos = append(eventos, ev(base.Add(time.Duration(i)*time.Second),
			"203.0.113.7", RutaInexistente, "/ruta-inventada-"+strconv.Itoa(i)))
	}

	o := PorOrigen(eventos)
	if len(o) != 1 {
		t.Fatalf("se esperaba un solo origen, hay %d", len(o))
	}
	if !slices.Contains(o[0].Senales, SenalExploracion) {
		t.Fatalf("un escáner con %d rutas distintas no levantó la señal", umbralExploracion+2)
	}
}

// Muchas peticiones a LA MISMA ruta inexistente no son exploración: eso es un
// cliente atascado reintentando, no un diccionario. La señal cuenta rutas
// DISTINTAS, y esta prueba fija esa diferencia.
func TestReintentarLaMismaRutaNoEsExploracion(t *testing.T) {
	base := time.Now()
	var eventos []Evento
	for i := range 200 {
		eventos = append(eventos, ev(base.Add(time.Duration(i)*time.Second),
			"203.0.113.7", RutaInexistente, "/favicon.ico"))
	}
	o := PorOrigen(eventos)
	if slices.Contains(o[0].Senales, SenalExploracion) {
		t.Fatal("200 peticiones a UNA sola ruta se marcaron como exploración")
	}
}

func TestElUmbralDeFuerzaBrutaCoincideConElLimitadorReal(t *testing.T) {
	if umbralFuerzaBruta != 5 {
		t.Fatalf("umbralFuerzaBruta = %d y el limitador de web/sesion.go bloquea a los 5",
			umbralFuerzaBruta)
	}
	base := time.Now()
	var eventos []Evento
	for i := range umbralFuerzaBruta {
		eventos = append(eventos, ev(base.Add(time.Duration(i)*time.Second),
			"203.0.113.7", CredencialIncorrecta, "/acceso"))
	}
	if !slices.Contains(PorOrigen(eventos)[0].Senales, SenalFuerzaBruta) {
		t.Fatal("al alcanzar el umbral del limitador la señal no aparece")
	}
}

// El sondeo de software ajeno se decide por lo que este NAS ES, no por una
// lista de sondas conocidas que caducaría sola.
func TestSoftwareAjenoSeDecidePorLoQueElNodoNoEjecuta(t *testing.T) {
	ajenas := []string{
		"/wp-login.php", "/index.PHP", "/admin.asp", "/shell.jsp",
		"/cgi-bin/test.cgi", "/copia.sql", "/.env", "/.git/config",
		"/algo/.ssh/id_rsa", "/wp-admin/install.php",
	}
	for _, r := range ajenas {
		if !RutaDeSoftwareAjeno(r) {
			t.Errorf("%q debería marcarse: este NAS no ejecuta nada de eso", r)
		}
	}

	propias := []string{
		"/", "/ver/fotos", "/descargar/informe.pdf", "/abrir/musica.mp3",
		"/estado", "/administracion", "/estatico/estilo.css",
		"/ver/proyectos/entorno-de-pruebas", // contiene «entorno», no «.env»
		"/descargar/git-guia.pdf",           // contiene «git», no «/.git»
	}
	for _, r := range propias {
		if RutaDeSoftwareAjeno(r) {
			t.Errorf("%q se marcó como software ajeno y es una ruta legítima del NAS", r)
		}
	}
}

// La gravedad de un origen es la MAYOR de sus eventos, no la media: un origen
// con 200 rechazos de rutina y uno de atención tiene que salir arriba.
func TestLaGravedadDeUnOrigenEsLaMayorDeSusEventos(t *testing.T) {
	base := time.Now()
	var eventos []Evento
	for i := range 200 {
		eventos = append(eventos, ev(base.Add(time.Duration(i)*time.Second),
			"203.0.113.7", SinSesion, "/"))
	}
	eventos = append(eventos, ev(base, "203.0.113.7", TestigoCSRF, "/borrar"))

	if got := PorOrigen(eventos)[0].Gravedad; got != Atencion {
		t.Fatalf("gravedad = %q; un solo evento de atención entre 200 de rutina debe mandar", got)
	}
}

// El orden tiene que ser determinista: sin un tercer criterio de desempate,
// dos orígenes iguales salen en el orden aleatorio del recorrido del mapa y
// la tabla baila entre recargas sin que nada haya cambiado.
func TestElOrdenNoBailaEntreRecargas(t *testing.T) {
	base := time.Now()
	eventos := []Evento{
		ev(base, "203.0.113.7", SinSesion, "/"),
		ev(base, "203.0.113.8", SinSesion, "/"),
		ev(base, "203.0.113.9", SinSesion, "/"),
	}
	primero := PorOrigen(eventos)
	for range 20 {
		otro := PorOrigen(eventos)
		for i := range primero {
			if primero[i].IP != otro[i].IP {
				t.Fatalf("el orden cambió entre dos agrupaciones idénticas: %v vs %v",
					primero[i].IP, otro[i].IP)
			}
		}
	}
}

func TestFiltrosAcotanLoQueSeMira(t *testing.T) {
	base := time.Now()
	a := &Anillo{buf: make([]Evento, Capacidad)}
	a.Anotar(ev(base, "203.0.113.7", RutaInexistente, "/wp-login.php"))
	a.Anotar(ev(base, "10.77.0.3", SinSesion, "/"))
	a.Anotar(ev(base, "192.168.1.18", CredencialIncorrecta, "/acceso"))

	motivo := SinSesion
	casos := []struct {
		nombre string
		f      Filtro
		quiero int
	}{
		{"sin filtro", Filtro{}, 3},
		{"por motivo", Filtro{Motivo: &motivo}, 1},
		{"por red", Filtro{Red: ptr(RedInternet)}, 1},
	}
	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			if got := len(a.Filtrados(c.f)); got != c.quiero {
				t.Fatalf("%d eventos, se esperaban %d", got, c.quiero)
			}
		})
	}
}

func TestFiltrarPorDesconocidoNoEsLoMismoQueNoFiltrar(t *testing.T) {
	base := time.Now()
	a := &Anillo{buf: make([]Evento, Capacidad)}
	a.Anotar(ev(base, "203.0.113.7", MotivoDesconocido, "/"))
	a.Anotar(ev(base, "203.0.113.7", TestigoCSRF, "/borrar"))

	if got := len(a.Filtrados(Filtro{Motivo: ptr(MotivoDesconocido)})); got != 1 {
		t.Fatalf("filtrando por desconocido salen %d eventos, se esperaba 1", got)
	}
	if got := len(a.Filtrados(Filtro{})); got != 2 {
		t.Fatalf("sin filtrar salen %d eventos, se esperaban 2", got)
	}
}

// El resumen no puede dejar creer que el anillo es toda la historia.
func TestElResumenDiceCuantoNoEstaMostrando(t *testing.T) {
	a := &Anillo{buf: make([]Evento, Capacidad)}
	base := time.Now()
	for i := range Capacidad + 500 {
		a.Anotar(ev(base.Add(time.Duration(i)*time.Millisecond), "203.0.113.7", SinSesion, "/"))
	}
	eventos := a.Filtrados(Filtro{})
	r := Resumir(eventos, PorOrigen(eventos), a.Total())

	if r.Eventos != Capacidad {
		t.Fatalf("el resumen muestra %d eventos y el anillo guarda %d", r.Eventos, Capacidad)
	}
	if r.TotalHistorico != int64(Capacidad+500) {
		t.Fatalf("total histórico = %d, se esperaban %d", r.TotalHistorico, Capacidad+500)
	}
}

func ptr[T any](v T) *T { return &v }

// NINGUNA SEÑAL PUEDE AFIRMAR UN ATAQUE, ni en su etiqueta ni en su
// explicación.
//
// El aviso de cabecera que decía «rechazos, no ataques» se retiró del panel el
// 2026-08-24 por decisión del responsable. Esta prueba no dependía de él y
// ahora es la ÚNICA que sujeta la regla, que es donde debía estar: lo que no se
// puede afirmar es un ataque de un origen CONCRETO.
//
// Lo que se prohíbe es AFIRMARLO de un origen concreto.
func TestNingunaSenalAfirmaUnAtaque(t *testing.T) {
	senales := []Senal{SenalExploracion, SenalFuerzaBruta, SenalSoftwareAjeno}
	for _, s := range senales {
		texto := strings.ToLower(s.Etiqueta() + " " + s.Explicacion())
		for _, prohibido := range []string{"ataque", "atacante", "intrusión", "intruso", "malicioso"} {
			if strings.Contains(texto, prohibido) {
				t.Errorf("la señal %q usa %q, y solo es una sospecha derivada",
					s.Etiqueta(), prohibido)
			}
		}
		// Y la otra mitad, que es la que la hace honesta: toda señal dice con
		// qué se puede confundir. Una sospecha sin su falso positivo escrito
		// al lado se lee como un veredicto.
		if s.Explicacion() == "" {
			t.Errorf("la señal %q no explica en qué se basa", s.Etiqueta())
		}
		if !strings.Contains(s.Explicacion(), "También lo produce") &&
			!strings.Contains(s.Explicacion(), "no hay nada que comprometer") {
			t.Errorf("la señal %q no dice con qué se puede confundir ni acota su alcance",
				s.Etiqueta())
		}
	}
}

func TestLasSenalesSoloSeDisparanDesdeInternet(t *testing.T) {
	base := time.Now()
	// El mismo comportamiento —claramente de sondeo— desde cada red.
	porRed := map[string]string{
		"nodo":  "127.0.0.1",
		"lan":   "192.168.1.18",
		"tunel": "10.77.0.3",
	}
	for nombre, ip := range porRed {
		t.Run("desde "+nombre, func(t *testing.T) {
			var eventos []Evento
			for i := range umbralExploracion + 5 {
				eventos = append(eventos, ev(base.Add(time.Duration(i)*time.Second),
					ip, RutaInexistente, "/wp-login-"+strconv.Itoa(i)+".php"))
			}
			for i := range umbralFuerzaBruta + 3 {
				eventos = append(eventos, ev(base.Add(time.Duration(i)*time.Minute),
					ip, CredencialIncorrecta, "/acceso"))
			}
			o := PorOrigen(eventos)
			if len(o[0].Senales) != 0 {
				t.Fatalf("desde %s (%s) se levantó la señal %q",
					nombre, ip, o[0].Senales[0].Etiqueta())
			}
			// Pero los HECHOS se siguen registrando igual: lo que no se emite
			// es la interpretación, no el dato.
			if o[0].PorMotivo[CredencialIncorrecta] != umbralFuerzaBruta+3 {
				t.Errorf("los hechos desde %s no se registraron completos", nombre)
			}
		})
	}

	// Y desde Internet, el MISMO comportamiento sí se marca.
	var deFuera []Evento
	for i := range umbralExploracion + 5 {
		deFuera = append(deFuera, ev(base.Add(time.Duration(i)*time.Second),
			"203.0.113.7", RutaInexistente, "/wp-login-"+strconv.Itoa(i)+".php"))
	}
	if len(PorOrigen(deFuera)[0].Senales) == 0 {
		t.Fatal("desde Internet el mismo sondeo NO levantó ninguna señal")
	}
}

func TestDestacarSoloResaltaLoQueMereceAtencion(t *testing.T) {
	base := time.Now()

	casos := []struct {
		nombre  string
		red     string
		motivo  Motivo
		destaca bool
	}{
		{"aviso desde casa: NO destaca", "192.168.1.18", RutaInexistente, false},
		{"aviso desde el túnel: NO destaca", "10.77.0.3", CredencialIncorrecta, false},
		{"aviso desde el propio nodo: NO destaca", "127.0.0.1", RutaInexistente, false},
		{"aviso desde Internet: SÍ destaca", "203.0.113.7", RutaInexistente, true},
		{"atención desde casa: SÍ destaca", "192.168.1.18", TestigoCSRF, true},
		{"atención desde Internet: SÍ destaca", "203.0.113.7", PermisoInsuficiente, true},
		// Rutina desde Internet SÍ destaca, y no es una inconsistencia: el
		// resumen ya trata «alguna dirección de Internet» como la señal —
		// «ninguna» es el estado sano—, así que hasta un simple «sin sesión»
		// desde fuera es la única vez que se ve a alguien tocando la puerta.
		{"rutina desde Internet: SÍ destaca", "203.0.113.7", SinSesion, true},
	}
	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			e := ev(base, c.red, c.motivo, "/algo")
			if got := e.Destacar(); got != c.destaca {
				t.Errorf("Evento.Destacar() = %v, se esperaba %v", got, c.destaca)
			}
			o := PorOrigen([]Evento{e})[0]
			if got := o.Destacar(); got != c.destaca {
				t.Errorf("Origen.Destacar() = %v, se esperaba %v", got, c.destaca)
			}
		})
	}
}

// A1 — UNA PETICIÓN A /.git/config, Y LO QUE SIGNIFICA EXACTAMENTE.
//
// El encargo lo pide entero y en una sola prueba porque el valor está en las
// cinco afirmaciones JUNTAS: cada una sola se cumpliría con un diseño peor.
func TestUnaPeticionAGitConfigNoEsUnAtaque(t *testing.T) {
	ahora := time.Now()
	e := ev(ahora, "203.0.113.7", RutaInexistente, "/.git/config")
	e.Estado = 404
	origenes := PorOrigen([]Evento{e})
	if len(origenes) != 1 {
		t.Fatalf("orígenes = %d", len(origenes))
	}
	o := origenes[0]

	// 1. Queda registrado como RECHAZO, con su método, su ruta y su estado.
	if o.Eventos != 1 || o.PorMotivo[RutaInexistente] != 1 {
		t.Errorf("el rechazo no se registró: %+v", o)
	}
	if len(o.Evidencia) != 1 {
		t.Fatalf("evidencia = %d líneas; se esperaba 1", len(o.Evidencia))
	}
	sd := o.Evidencia[0]
	if sd.Metodo != "GET" || sd.Ruta != "/.git/config" || sd.Estado != 404 || sd.Veces != 1 {
		t.Errorf("la evidencia no conserva el hecho: %+v", sd)
	}

	// 2. SUSTENTA la señal de software ajeno, que es una inferencia.
	if !contieneSenal(o.Senales, SenalSoftwareAjeno) {
		t.Error("no sustenta SenalSoftwareAjeno")
	}

	// 3. NO sustenta exploración: una ruta no son ocho.
	if contieneSenal(o.Senales, SenalExploracion) {
		t.Error("una sola ruta produjo señal de exploración")
	}

	// 4. NO significa compromiso: no hay hallazgo, porque el servidor negó.
	if RespuestaInesperada("/.git/config", 404) {
		t.Error("un 404 se contó como respuesta inesperada del servidor")
	}

	// 5. NO produce cuarentena por sí sola. Es la calibración de ADR-0070: la
	// acción más fuerte no la puede disparar la señal más débil.
	c := cuarentenaDePrueba(t)
	if n := len(c.Evaluar(origenes, ahora)); n != 0 {
		t.Errorf("una petición aislada apartó a alguien: %d apartados", n)
	}
}

// LA EVIDENCIA ES LO QUE CONVIERTE UNA AFIRMACIÓN EN UNA QUE SE PUEDE
// CONTRASTAR.
//
// Hasta aquí el panel decía «Exploración automatizada» y quien lo leyera tenía
// que creerse el umbral. Esto comprueba que se puede contestar «¿qué estaba
// buscando?» sin salir de la página.
func TestLaEvidenciaExplicaLaSenal(t *testing.T) {
	ahora := time.Now()
	var eventos []Evento
	// Ocho rutas distintas, y una de ellas pedida cuatro veces: es el patrón
	// que hay que poder leer de un vistazo.
	for i := range umbralExploracion {
		e := ev(ahora, "203.0.113.7", RutaInexistente, "/inventada-"+strconv.Itoa(i)+".php")
		e.Estado = 404
		eventos = append(eventos, e)
	}
	for range 3 {
		e := ev(ahora, "203.0.113.7", RutaInexistente, "/inventada-0.php")
		e.Estado = 404
		eventos = append(eventos, e)
	}

	o := PorOrigen(eventos)[0]
	if !contieneSenal(o.Senales, SenalExploracion) {
		t.Fatal("ocho rutas distintas no produjeron exploración")
	}
	// LA ENTRADA DEL UMBRAL, publicada: es lo que contesta «¿por qué dice
	// exploración?» sin obligar a nadie a contar filas.
	if o.RutasInexistentes != umbralExploracion {
		t.Errorf("rutas inexistentes = %d; se esperaban %d", o.RutasInexistentes, umbralExploracion)
	}
	if o.SondasVistas != umbralExploracion {
		t.Errorf("sondas distintas = %d; se esperaban %d", o.SondasVistas, umbralExploracion)
	}
	// La más repetida va PRIMERA: es la que dice qué buscaba con más ganas.
	if o.Evidencia[0].Ruta != "/inventada-0.php" || o.Evidencia[0].Veces != 4 {
		t.Errorf("la evidencia no ordena por repetición: %+v", o.Evidencia[0])
	}
}

// LA EVIDENCIA ESTÁ ACOTADA. La escribe un tercero —las rutas las elige quien
// sondea—, así que sin tope sería la única estructura del panel cuyo tamaño
// decide alguien de fuera.
func TestLaEvidenciaNoCreceConLoQueHagaUnExtrano(t *testing.T) {
	ahora := time.Now()
	var eventos []Evento
	for i := range 500 {
		e := ev(ahora, "203.0.113.7", RutaInexistente, "/x-"+strconv.Itoa(i))
		e.Estado = 404
		eventos = append(eventos, e)
	}
	o := PorOrigen(eventos)[0]
	if len(o.Evidencia) != TopeSondas {
		t.Errorf("evidencia = %d líneas; el tope es %d", len(o.Evidencia), TopeSondas)
	}
	// Y SE DICE QUE SE RECORTA, en vez de dar a entender que eso es todo: la
	// misma honestidad que ya tienen la cronología y la tabla de rutas.
	if o.SondasVistas != 500 {
		t.Errorf("sondas vistas = %d; se esperaban 500", o.SondasVistas)
	}
}

// EL MISMO HECHO CON DOS ESTADOS DISTINTOS SON DOS LÍNEAS, no una sumada.
//
// «/.git/config → 404» y «/.git/config → 200» son afirmaciones opuestas sobre
// el nodo. Agruparlas por ruta a secas borraría justo la diferencia que este
// trabajo existe para enseñar.
func TestLaEvidenciaNoMezclaEstadosDistintos(t *testing.T) {
	ahora := time.Now()
	uno := ev(ahora, "203.0.113.7", RutaInexistente, "/.git/config")
	uno.Estado = 404
	otro := ev(ahora, "203.0.113.7", PermisoInsuficiente, "/.git/config")
	otro.Estado = 403

	o := PorOrigen([]Evento{uno, otro})[0]
	if len(o.Evidencia) != 2 {
		t.Fatalf("evidencia = %d líneas; dos estados distintos son dos hechos", len(o.Evidencia))
	}
}

// EL ORDEN DE LA EVIDENCIA NO BAILA ENTRE RECARGAS. Justificar una inferencia
// con una tabla que cambia sola no justifica nada — mismo criterio que el
// tercer desempate de PorOrigen.
func TestElOrdenDeLaEvidenciaEsDeterminista(t *testing.T) {
	ahora := time.Now()
	var eventos []Evento
	for _, ruta := range []string{"/a", "/b", "/c", "/d", "/e"} {
		e := ev(ahora, "203.0.113.7", RutaInexistente, ruta)
		e.Estado = 404
		eventos = append(eventos, e)
	}

	var primera []string
	for i := range 30 {
		o := PorOrigen(eventos)[0]
		var rutas []string
		for _, sd := range o.Evidencia {
			rutas = append(rutas, sd.Ruta)
		}
		if i == 0 {
			primera = rutas
			continue
		}
		if !slices.Equal(primera, rutas) {
			t.Fatalf("la evidencia cambió de orden: %v vs %v", primera, rutas)
		}
	}
}
