package aviso

import (
	"strings"
	"testing"
	"time"

	"nasd/internal/seguridad"
)

// La clasificación, la anomalía sin detector y la frontera de privacidad. Las
// tres cosas que, si se rompen, convierten esta capa en lo que el encargo
// prohibió: un sistema que inventa ataques o que manda de más.

func TestLaSeveridadDescribeLoQueOcurrioYNoElVolumen(t *testing.T) {
	// La regla, textual del encargo: «cientos de peticiones pueden seguir siendo
	// verdes si el NAS las rechaza correctamente; un único evento puede ser rojo
	// si rompe una invariante».
	ahora := time.Now()

	// 300 rechazos rutinarios desde Internet: NINGUNA situación. Es volumen.
	ruidoso := rutinario("203.0.113.7", ahora)
	ruidoso.Eventos = 300
	ruidoso.PorMotivo[seguridad.SinSesion] = 300
	if ss := Evaluar(Entrada{Origenes: []OrigenVisto{ruidoso}}, ahora); len(ss) != 0 {
		t.Errorf("300 rechazos rutinarios produjeron %d situaciones; se esperaban 0", len(ss))
	}

	// UN hallazgo: rojo.
	uno := Evaluar(Entrada{Hallazgos: []seguridad.Hallazgo{hallazgo("/.git/config", ahora)}}, ahora)
	if len(uno) != 1 || uno[0].Severidad != Rojo {
		t.Fatalf("un hallazgo produjo %d situaciones (%v); se esperaba 1 roja", len(uno), severidadDe(uno))
	}
}

func TestLaClasificacionDeCadaConducta(t *testing.T) {
	ahora := time.Now()
	for _, c := range []struct {
		nombre string
		origen OrigenVisto
		clase  Clase
		sev    Severidad
		hay    bool
	}{
		{"exploración", explorador("203.0.113.7", 9, ahora), ClaseExploracion, Amarillo, true},
		{"fuerza bruta", fuerzaBruta("203.0.113.8", 25, ahora), ClaseFuerzaBruta, Amarillo, true},
		{"anomalía sin detector", anomalo("203.0.113.9", ahora), ClaseAnomalia, Verde, true},
		{"rechazo corriente: volumen, no historia", rutinario("203.0.113.10", ahora), 0, 0, false},
	} {
		t.Run(c.nombre, func(t *testing.T) {
			ss := Evaluar(Entrada{Origenes: []OrigenVisto{c.origen}}, ahora)
			if !c.hay {
				if len(ss) != 0 {
					t.Fatalf("produjo %d situaciones; se esperaban 0", len(ss))
				}
				return
			}
			if len(ss) != 1 {
				t.Fatalf("produjo %d situaciones; se esperaba 1", len(ss))
			}
			if ss[0].Clase != c.clase {
				t.Errorf("clase = %q; se esperaba %q", ss[0].Clase, c.clase)
			}
			if ss[0].Severidad != c.sev {
				t.Errorf("severidad = %q; se esperaba %q", ss[0].Severidad, c.sev)
			}
		})
	}
}

func TestLaFuerzaBrutaGanaALaExploracionCuandoCoincidenLasDos(t *testing.T) {
	// La precedencia está ESCRITA y no es un accidente del orden del slice —la
	// lección de seguridad/puerta.go, donde la atribución de un cierre resultó
	// ser una consecuencia no escrita del cortocircuito de un «||»—.
	ahora := time.Now()
	o := explorador("203.0.113.7", 9, ahora)
	o.Senales = []seguridad.Senal{seguridad.SenalExploracion, seguridad.SenalFuerzaBruta}
	o.PorMotivo[seguridad.CredencialIncorrecta] = 25

	ss := Evaluar(Entrada{Origenes: []OrigenVisto{o}}, ahora)
	if len(ss) != 1 {
		t.Fatalf("dos señales produjeron %d situaciones; se esperaba 1", len(ss))
	}
	if ss[0].Clase != ClaseFuerzaBruta {
		t.Errorf("clase = %q; la fuerza bruta debe ganar", ss[0].Clase)
	}
	// Y la otra señal NO se pierde: se cuenta como hecho en el mismo mensaje,
	// en vez de emitir un segundo aviso de la misma historia.
	if !tieneHecho(ss[0], "Señales") {
		t.Error("la segunda señal no aparece en la evidencia; se perdió información")
	}
}

func TestUnEventoSinSenalConocidaEsAnomaliaYNoUnAtaqueInventado(t *testing.T) {
	ahora := time.Now()
	ss := Evaluar(Entrada{Origenes: []OrigenVisto{anomalo("203.0.113.9", ahora)}}, ahora)
	if len(ss) != 1 {
		t.Fatalf("produjo %d situaciones; se esperaba 1", len(ss))
	}
	s := ss[0]
	if s.Clase != ClaseAnomalia {
		t.Fatalf("clase = %q; se esperaba anomalia", s.Clase)
	}
	// EL TÍTULO NO PUEDE NOMBRAR UN ATAQUE. Es la regla del encargo —«no
	// inventes una categoría de ataque, no atribuyas intención al origen sin
	// evidencia»— comprobada sobre el texto que llega al teléfono.
	for _, prohibida := range []string{"ataque", "intrusión", "intrusion", "compromiso", "hacker", "exploit"} {
		if strings.Contains(strings.ToLower(s.Clase.Titulo()+s.Recomendacion()), prohibida) {
			t.Errorf("el texto de una anomalía contiene %q; no se puede afirmar eso", prohibida)
		}
	}
}

func TestLaAnomaliaSubeAAmarilloConLaEvidenciaYNoAntes(t *testing.T) {
	// La costura por la que un detector futuro se promueve: mientras solo sea
	// rara es verde, y sube cuando alguno de sus rechazos es de los que NO se
	// alcanzan navegando. No se estrena un umbral: se reutiliza el juicio que
	// seguridad.Gravedad ya emitió sobre el hecho suelto.
	ahora := time.Now()

	suave := anomalo("203.0.113.9", ahora)
	if ss := Evaluar(Entrada{Origenes: []OrigenVisto{suave}}, ahora); ss[0].Severidad != Verde {
		t.Errorf("anomalía suave = %q; se esperaba verde", ss[0].Severidad)
	}

	grave := anomalo("203.0.113.9", ahora)
	grave.Gravedad = seguridad.Atencion
	if ss := Evaluar(Entrada{Origenes: []OrigenVisto{grave}}, ahora); ss[0].Severidad != Amarillo {
		t.Errorf("anomalía con hecho de atención = %q; se esperaba amarillo", ss[0].Severidad)
	}
}

func TestLaCasaNoProduceNuncaUnAviso(t *testing.T) {
	// La misma barandilla que Cuarentena.Evaluar repite antes de apartar, y por
	// el mismo motivo: de esta lista sale algo que llega al teléfono del
	// responsable, y que la casa quede fuera no puede depender de que otra
	// función haya hecho bien su parte.
	ahora := time.Now()
	for _, red := range []seguridad.Red{seguridad.RedNodo, seguridad.RedLocal, seguridad.RedTunel} {
		o := explorador("192.168.1.18", 30, ahora)
		o.Red = red
		if ss := Evaluar(Entrada{Origenes: []OrigenVisto{o}}, ahora); len(ss) != 0 {
			t.Errorf("red %q produjo %d situaciones; la casa no avisa nunca", red, len(ss))
		}
	}
}

func TestUnCierreQueFalloEsRojo(t *testing.T) {
	// Es la única forma de que el panel esté diciendo MENOS de lo que pasa: se
	// decidió cerrar una conexión y siguió viva.
	ahora := time.Now()
	ss := Evaluar(Entrada{CierresFallidos: 3}, ahora)
	if len(ss) != 1 || ss[0].Clase != ClaseEnforcementFallido || ss[0].Severidad != Rojo {
		t.Fatalf("cierres fallidos produjeron %d situaciones (%v); se esperaba 1 roja", len(ss), severidadDe(ss))
	}
}

func TestLasSituacionesSalenDeLaMasGraveALaMenos(t *testing.T) {
	// El determinismo no es cosmético: el Registro recorre esta lista para
	// decidir qué notifica, y un orden que baila haría que dos ejecuciones con
	// los mismos hechos produjeran avisos en distinto orden.
	ahora := time.Now()
	ss := Evaluar(Entrada{
		Origenes:  []OrigenVisto{anomalo("203.0.113.9", ahora), explorador("203.0.113.7", 9, ahora)},
		Hallazgos: []seguridad.Hallazgo{hallazgo("/.git/config", ahora)},
	}, ahora)
	if len(ss) != 3 {
		t.Fatalf("produjo %d situaciones; se esperaban 3", len(ss))
	}
	for i := 1; i < len(ss); i++ {
		if ss[i].Severidad > ss[i-1].Severidad {
			t.Fatalf("posición %d (%q) es más grave que la anterior (%q); el orden está mal",
				i, ss[i].Severidad, ss[i-1].Severidad)
		}
	}
}

// ---------------------------------------------------------------------------
// LA FRONTERA DE PRIVACIDAD
// ---------------------------------------------------------------------------

func TestNuncaSaleDelNodoUnaRutaDelUsuario(t *testing.T) {
	// EL CASO ES REAL, no inventado: el 2026-08-24 este nodo registró una
	// petición externa a «/abrir/Telefono B2 2017/Mensajeria Video/
	// VID-20000101-0001.mp4» — una ruta de la carpeta de una persona de la
	// casa. Mandarla a un tercero por comodidad de diagnóstico sería deshacer
	// por la puerta de al lado lo que 04_SEGURIDAD §6 protege en el diario.
	ahora := time.Now()
	o := explorador("203.0.113.7", 9, ahora)
	o.Evidencia = []seguridad.Sonda{
		{Metodo: "GET", Ruta: "/abrir/Telefono B2 2017/Mensajeria Video/VID-20000101-0001.mp4", Estado: 404, Veces: 4},
		{Metodo: "GET", Ruta: "/contenido/Fotos/DOC-escaneado.jpg", Estado: 404, Veces: 3},
		{Metodo: "GET", Ruta: "/descargar/recibo.pdf", Estado: 404, Veces: 2},
		{Metodo: "GET", Ruta: "/wp-login.php", Estado: 404, Veces: 1},
	}

	ss := Evaluar(Entrada{Origenes: []OrigenVisto{o}}, ahora)
	texto := todoElTexto(ss[0])
	for _, prohibida := range []string{"Mensajeria", "Telefono B2", "DOC", "recibo", "Fotos", "abrir", "contenido", "descargar"} {
		if strings.Contains(texto, prohibida) {
			t.Errorf("una ruta del usuario salió del nodo: el aviso contiene %q\n%s", prohibida, texto)
		}
	}
	// Y la ruta ajena SÍ sale: es lo que hace útil el aviso, y no dice nada de
	// nadie de la casa.
	if !strings.Contains(texto, "/wp-login.php") {
		t.Errorf("la ruta ajena no salió; el aviso pierde su evidencia\n%s", texto)
	}
}

func TestNuncaSaleElNombreDeCuentaNiElUserAgent(t *testing.T) {
	// La cuenta es el nombre de un familiar y el User-Agent identifica sus
	// aparatos. Los dos están en el anillo —hacen falta en el panel— y ninguno
	// puede cruzar hacia un tercero.
	ahora := time.Now()
	o := fuerzaBruta("203.0.113.8", 25, ahora)
	o.Cuentas = []string{"juan", "ma"}

	ss := Evaluar(Entrada{Origenes: []OrigenVisto{o}}, ahora)
	texto := todoElTexto(ss[0])
	for _, prohibido := range []string{"juan", "ana\"", "Mozilla", "iPhone", "Android"} {
		if strings.Contains(texto, prohibido) {
			t.Errorf("el aviso contiene %q, que no debe salir del nodo\n%s", prohibido, texto)
		}
	}
}

func TestElHallazgoSiEnsenaSuRutaPorqueEsAjenaPorConstruccion(t *testing.T) {
	// La excepción, y es segura por construcción: seguridad.RespuestaInesperada
	// solo produce hallazgos para rutas de la familia ajena y FUERA del espacio
	// de archivos del usuario. Una ruta ahí es «/.git/config», nunca
	// «/abrir/Fotos/...».
	ahora := time.Now()
	ss := Evaluar(Entrada{Hallazgos: []seguridad.Hallazgo{hallazgo("/.git/config", ahora)}}, ahora)
	texto := todoElTexto(ss[0])
	if !strings.Contains(texto, "/.git/config") {
		t.Errorf("el hallazgo no dice QUÉ ruta se expuso; el aviso es inútil\n%s", texto)
	}
	// Y dice lo que NO demuestra, para que nadie concluya de madrugada lo que
	// el nodo no ha afirmado.
	if !strings.Contains(strings.ToLower(texto), "no prueba intrusión") {
		t.Errorf("el hallazgo no acota lo que NO demuestra\n%s", texto)
	}
}

func tieneHecho(s Situacion, rotulo string) bool {
	for _, h := range s.Hechos {
		if h.Rotulo == rotulo {
			return true
		}
	}
	return false
}

// todoElTexto es lo que de verdad sale del nodo: el mensaje compuesto, no los
// campos sueltos. Las pruebas de privacidad miran esto y no la estructura,
// porque una fuga por el formateador contaría igual.
func todoElTexto(s Situacion) string {
	a := Mensaje(s)
	return a.Titulo + "\n" + a.Texto
}
