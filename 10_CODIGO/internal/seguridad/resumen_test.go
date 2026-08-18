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

// LA PRUEBA MÁS IMPORTANTE DE ESTE ARCHIVO, y existe por una instrucción
// explícita del responsable: «no clasifiques automáticamente todo rechazo
// como ataque».
//
// El caso que la motiva es real y frecuente: un móvil que reconecta y pide
// varias veces la misma página sin cookie produce un puñado de 401. Si eso
// disparara una señal, el panel gritaría todos los días y en un mes nadie lo
// miraría — que es el modo de fallo que 00_RECTOR.md §12.5 lleva persiguiendo
// en los verificadores de este proyecto.
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

// El umbral de fuerza bruta es el MISMO que el del limitador que de verdad
// bloquea (maxIntentosFallidos = 5 en web/sesion.go). Si divergieran, el
// panel avisaría antes o después de lo que el servidor hace, y habría que
// explicar cuál de las dos cifras manda.
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
		if !rutaDeSoftwareAjeno(r) {
			t.Errorf("%q debería marcarse: este NAS no ejecuta nada de eso", r)
		}
	}

	// Y LO QUE NO PUEDE MARCARSE, que es la mitad que evita el falso
	// positivo: son nombres de archivo perfectamente legítimos en un NAS
	// doméstico, y marcarlos convertiría subir un PDF en una alarma.
	propias := []string{
		"/", "/ver/fotos", "/descargar/informe.pdf", "/abrir/musica.mp3",
		"/estado", "/administracion", "/estatico/estilo.css",
		"/ver/proyectos/entorno-de-pruebas", // contiene «entorno», no «.env»
		"/descargar/git-guia.pdf",           // contiene «git», no «/.git»
	}
	for _, r := range propias {
		if rutaDeSoftwareAjeno(r) {
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

// Filtrar por «desconocido» tiene que poder distinguirse de «no filtrar», y por
// eso el campo es un puntero: MotivoDesconocido es el valor cero de Motivo.
//
// Esta prueba defendía la misma propiedad sobre Gravedad, que era el otro campo
// con valor cero legítimo. Al retirarse aquel filtro (ADR-0065) la propiedad no
// desaparece —sigue habiendo un puntero que la necesita—, así que la prueba se
// muda al campo que la conserva en vez de borrarse. Un motivo desconocido no es
// una rareza: conRegistro anota así todo 4xx que ninguna guarda clasificó, y
// verlo en el panel es lo que hace que se le ponga nombre.
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
// explicación. Es la instrucción del responsable convertida en invariante
// comprobable, y vive aquí —sobre los textos del dominio— y no sobre el HTML
// del panel, porque el aviso de cabecera de esa página SÍ usa la palabra, y
// legítimamente: dice «esto registra rechazos, no ataques».
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

// LAS SEÑALES NO SE DISPARAN DESDE CASA, y esta prueba existe por un defecto
// visto en la PRIMERA captura del panel en producción: «Sondeo de software que
// aquí no existe» saltó sobre el propio nodo, porque la verificación del
// despliegue había probado /wp-login.php con curl. Una alarma roja sobre uno
// mismo, en la primera pantalla que vio el responsable.
//
// Desde la LAN, el túnel o el propio nodo, quien pide ya tiene la casa o la
// clave. Una sospecha de intrusión desde ahí no informa y gasta la
// credibilidad de las que sí importan.
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

// EL RUIDO QUE TAPA LO QUE IMPORTA, visto en la segunda captura del panel en
// producción: casi toda la tabla salía en naranja porque RutaInexistente es
// «aviso» y la producían los favicon.ico del propio iPhone. Destacar() separa
// «esto es un hecho de gravedad aviso» —que Motivo.Gravedad() sigue diciendo
// igual, y el filtro por gravedad lo sigue usando entero— de «esto merece
// llamar la atención en la tabla».
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
