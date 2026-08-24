package seguridad

import (
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// LA DISTINCIÓN QUE ESTE ARCHIVO EXISTE PARA PROTEGER, en dos líneas:
//
//	GET /.git/config -> 404   sondeo. No pasó nada.
//	GET /.git/config -> 200   el NODO respondió donde no publica nada.
//
// Son hechos opuestos sobre la misma petición, y hasta este trabajo el segundo
// no se registraba en ninguna parte: el anillo de rechazos solo se alimenta de
// respuestas >= 400.

func hallazgosDePrueba(t *testing.T) *Hallazgos {
	t.Helper()
	h, err := CargarHallazgos(filepath.Join(t.TempDir(), "hallazgos"))
	if err != nil {
		t.Fatalf("CargarHallazgos: %v", err)
	}
	return h
}

// LA REGLA ENTERA, CASO POR CASO — B1, B2, B3 y B4 del encargo.
//
// Se prueba la FUNCIÓN y no el panel a propósito: lo que hay que fijar aquí es
// la definición de «respuesta inesperada», y una prueba sobre el HTML pasaría
// igual con la definición equivocada mientras la palabra apareciera en la
// página.
func TestQueCuentaComoRespuestaInesperada(t *testing.T) {
	for _, c := range []struct {
		ruta   string
		estado int
		quiero bool
		porque string
	}{
		// B1 — el caso que da sentido a todo esto.
		{"/.git/config", 200, true, "una ruta ajena atendida con contenido"},
		{"/wp-login.php", 200, true, "PHP en un binario de Go que no ejecuta PHP"},
		{"/.env", 200, true, "un archivo de configuración que aquí no se publica"},
		// EL MÉTODO NO FILTRA: un HEAD confirma que la ruta existe igual que un
		// GET, y es la forma más barata de comprobarlo. Ver RespuestaInesperada.
		{"/.git/config", 206, true, "entrega parcial es entrega"},

		// B2 — lo legítimo NO lo produce. Es la mitad que impide que esto se
		// convierta en «todo lo que no es un rechazo es exposición».
		{"/estado", 200, false, "una ruta propia del NAS"},
		{"/", 200, false, "la portada"},
		{"/estatico/estilo.css", 200, false, "un recurso propio"},
		{"/acceso", 200, false, "el formulario de acceso"},

		// B3 — un 404 en ruta sensible sigue siendo sondeo, no exposición.
		{"/.git/config", 404, false, "un rechazo no es una exposición"},
		{"/wp-login.php", 401, false, "sin sesión tampoco es una exposición"},
		{"/.env", 403, false, "negado tampoco"},

		// B4 — LOS BORDES QUE EL DISEÑO FIJÓ, escritos aquí para que cambiarlos
		// exija cambiar esta prueba y no se deslicen sin querer.
		{"/.git/config", 301, false, "una redirección no entrega contenido"},
		{"/.git/config", 302, false, "ni esta"},
		{"/.git/config", 303, false, "ni la del patrón POST-redirect-GET"},
		{"/.git/config", 307, false, "ni la temporal"},
		{"/.git/config", 308, false, "ni la permanente"},
		{"/.git/config", 304, false, "«no modificado» tampoco entrega bytes"},
		{"/.git/config", 204, false, "«sin contenido» no lo es; ningún GET del NAS lo devuelve"},
		{"/.git/config", 500, false, "un error del servidor no es una entrega"},

		// EL ESPACIO DEL USUARIO. Cualquier nombre es legítimo bajo estas rutas
		// porque lo eligió quien subió el archivo: nada impide que el
		// responsable tenga un «respaldo.bak» o una carpeta «.git» suyos, y
		// alarmar por ellos sería disparar la única alarma seria del panel con
		// el funcionamiento normal del NAS.
		{"/contenido/respaldo.bak", 200, false, "un archivo del usuario, servido como debe"},
		{"/descargar/notas.php", 200, false, "lo mismo por la vía de descarga"},
		{"/abrir/copia.sql", 200, false, "y por la de apertura en el navegador"},
		{"/miniatura/foto.bak", 200, false, "y por la de miniaturas"},
		{"/ver/.git/registro", 200, false, "una carpeta del usuario que se llama así"},
	} {
		t.Run(strconv.Itoa(c.estado)+" "+c.ruta, func(t *testing.T) {
			if got := RespuestaInesperada(c.ruta, c.estado); got != c.quiero {
				t.Errorf("RespuestaInesperada(%q, %d) = %v; se esperaba %v — %s",
					c.ruta, c.estado, got, c.quiero, c.porque)
			}
		})
	}
}

// UNA SOLA FUENTE DE VERDAD para «software ajeno»: la familia se reconoce
// igual la mire quien la mire.
//
// Sin esto, la lista se copiaría a la tercera capa y divergiría el día que
// alguien añadiera una extensión a una sola de ellas — que es el modo de fallo
// que RutaDeSoftwareAjeno se exportó para cerrar.
func TestLaFamiliaAjenaSeReconoceIgualEnLasTresCapas(t *testing.T) {
	for _, ruta := range []string{"/.git/config", "/wp-login.php", "/.env", "/copia.sql"} {
		if !RutaDeSoftwareAjeno(ruta) {
			t.Fatalf("%q no se reconoce como familia ajena", ruta)
		}
		// 1) La señal, sobre un rechazo desde Internet.
		o := PorOrigen([]Evento{rechazo(deFuera, RutaInexistente, ruta, time.Now())})
		if len(o) != 1 || !contieneSenal(o[0].Senales, SenalSoftwareAjeno) {
			t.Errorf("%q no produce SenalSoftwareAjeno", ruta)
		}
		// 2) El hallazgo, sobre la MISMA ruta atendida con contenido.
		if !RespuestaInesperada(ruta, 200) {
			t.Errorf("%q atendida con 200 no produce hallazgo", ruta)
		}
	}
}

func contieneSenal(ss []Senal, x Senal) bool {
	for _, s := range ss {
		if s == x {
			return true
		}
	}
	return false
}

// EL HALLAZGO AGREGA POR RUTA, no guarda un suceso por petición.
//
// La pregunta que contesta esta estructura es «¿QUÉ publica el nodo que no
// debería?», y esa tiene tantas respuestas como rutas distintas. Con un anillo
// de sucesos, mil peticiones a la misma ruta taparían la segunda ruta expuesta
// — que es justo la que aportaría información nueva.
func TestElHallazgoAgregaPorRutaYConservaLosExtremos(t *testing.T) {
	h := hallazgosDePrueba(t)
	base := time.Now()
	ip := netip.MustParseAddr(deFuera)

	for i := range 5 {
		h.Anotar(Hallazgo{
			Clase: RutaAjenaAtendida, Metodo: "GET", Ruta: "/.git/config", Estado: 200,
			Ultima: base.Add(time.Duration(i) * time.Minute), UltimoOrigen: ip,
		})
	}
	h.Anotar(Hallazgo{
		Clase: RutaAjenaAtendida, Metodo: "GET", Ruta: "/.env", Estado: 200,
		Ultima: base.Add(time.Hour), UltimoOrigen: ip,
	})

	todos := h.Todos()
	if len(todos) != 2 {
		t.Fatalf("rutas distintas = %d; se esperaban 2", len(todos))
	}
	// Del más reciente al más antiguo: /.env es la última.
	if todos[0].Ruta != "/.env" {
		t.Errorf("primero %q; se esperaba el más reciente", todos[0].Ruta)
	}
	if todos[1].Veces != 5 {
		t.Errorf("veces = %d; se esperaban 5", todos[1].Veces)
	}
	if !todos[1].Primera.Equal(base) {
		t.Error("la primera observación no se conservó")
	}
	if !todos[1].Ultima.Equal(base.Add(4 * time.Minute)) {
		t.Error("la última observación no se conservó")
	}
	// Y el TOTAL cuenta sucesos, no rutas: seis peticiones, dos filas.
	if h.Total() != 6 {
		t.Errorf("total = %d; se esperaban 6 sucesos", h.Total())
	}
}

// LA COTA. A diferencia de la del anillo, esta estructura solo crece cuando el
// NODO se porta mal, pero la cota está igual: nada del programa puede crecer
// sin tope en un nodo de 592 MB.
func TestLosHallazgosNoCrecenSinTope(t *testing.T) {
	h := hallazgosDePrueba(t)
	ahora := time.Now()
	for i := range topeHallazgos * 3 {
		h.Anotar(Hallazgo{
			Clase: RutaAjenaAtendida, Metodo: "GET",
			Ruta: "/inventada-" + strconv.Itoa(i) + ".php", Estado: 200, Ultima: ahora,
		})
	}
	if n := len(h.Todos()); n != topeHallazgos {
		t.Errorf("rutas guardadas = %d; el tope es %d", n, topeHallazgos)
	}
	// LLENO NO SE CALLA: el total sigue subiendo, así que el panel puede decir
	// que hubo más de lo que enseña en vez de dar a entender que eso es todo.
	if h.Total() != int64(topeHallazgos*3) {
		t.Errorf("total = %d; se esperaban %d", h.Total(), topeHallazgos*3)
	}
}

// LA RAZÓN DE PERSISTIRLO: es lo único del panel que no se puede volver a
// observar. Un rechazo perdido lo repite el siguiente escáner; un «este nodo
// devolvió 200 en /.git/config» que nadie anotó no vuelve.
func TestLosHallazgosSobrevivenAlArranque(t *testing.T) {
	ruta := filepath.Join(t.TempDir(), "hallazgos")
	ahora := time.Now().Truncate(time.Second)

	uno, err := CargarHallazgos(ruta)
	if err != nil {
		t.Fatalf("CargarHallazgos: %v", err)
	}
	uno.Anotar(Hallazgo{
		Clase: RutaAjenaAtendida, Metodo: "GET", Ruta: "/.git/config", Estado: 200,
		Ultima: ahora, UltimoOrigen: netip.MustParseAddr(deFuera),
	})
	uno.Anotar(Hallazgo{
		Clase: RutaAjenaAtendida, Metodo: "GET", Ruta: "/.git/config", Estado: 200,
		Ultima: ahora, UltimoOrigen: netip.MustParseAddr(deFuera),
	})
	if err := uno.Volcar(); err != nil {
		t.Fatalf("Volcar: %v", err)
	}

	otro, err := CargarHallazgos(ruta)
	if err != nil {
		t.Fatalf("CargarHallazgos tras reiniciar: %v", err)
	}
	v := otro.Todos()
	if len(v) != 1 {
		t.Fatalf("hallazgos tras reiniciar = %d; se esperaba 1", len(v))
	}
	if v[0].Veces != 2 {
		t.Errorf("veces tras reiniciar = %d; se esperaban 2", v[0].Veces)
	}
	if v[0].Ruta != "/.git/config" || v[0].Estado != 200 || v[0].Metodo != "GET" {
		t.Errorf("la petición no sobrevivió entera: %+v", v[0])
	}
	if v[0].UltimoOrigen.String() != deFuera {
		t.Errorf("el origen no sobrevivió: %v", v[0].UltimoOrigen)
	}
	// La RED se deriva al releer y no se guarda: los prefijos IPv6 propios se
	// aprenden en cada arranque, y una red congelada en disco volvería a contar
	// la casa como Internet el día que el proveedor rote la delegación.
	if v[0].UltimaRed != RedInternet {
		t.Errorf("la red no se reclasificó al releer: %v", v[0].UltimaRed)
	}
	// Y el total, por la misma marca de cabecera que los dos anillos.
	if otro.Total() != 2 {
		t.Errorf("total tras reiniciar = %d; se esperaban 2", otro.Total())
	}
}

// LA CLAVE ESTABLE, por lo mismo que en Motivo, Senal y Alcance: el archivo
// guarda el texto y no el número del enum, para que insertar una clase nueva
// en medio no reinterprete en silencio lo ya escrito.
func TestLaClaseViajaPorSuClaveEstable(t *testing.T) {
	ruta := filepath.Join(t.TempDir(), "hallazgos")
	h, err := CargarHallazgos(ruta)
	if err != nil {
		t.Fatalf("CargarHallazgos: %v", err)
	}
	h.Anotar(Hallazgo{Clase: RutaAjenaAtendida, Metodo: "GET", Ruta: "/.env", Estado: 200, Ultima: time.Now()})
	if err := h.Volcar(); err != nil {
		t.Fatalf("Volcar: %v", err)
	}
	crudo, err := os.ReadFile(ruta)
	if err != nil {
		t.Fatalf("leer el archivo: %v", err)
	}
	if !strings.Contains(string(crudo), `"clase":"ruta_ajena_atendida"`) {
		t.Errorf("la clase no se guardó por su clave estable:\n%s", crudo)
	}
}

// PRIVACIDAD — F del encargo. La función nueva NO empieza a guardar nada que
// 04_SEGURIDAD §6 mantenga fuera.
//
// Se comprueba sobre el ARCHIVO y no sobre la estructura: lo que importa es
// qué acaba en disco, y una prueba sobre los campos pasaría igual si alguien
// añadiera un volcado de la petición entera al serializar.
func TestElHallazgoNoPersisteNadaSensible(t *testing.T) {
	ruta := filepath.Join(t.TempDir(), "hallazgos")
	h, err := CargarHallazgos(ruta)
	if err != nil {
		t.Fatalf("CargarHallazgos: %v", err)
	}
	h.Anotar(Hallazgo{
		Clase: RutaAjenaAtendida, Metodo: "GET", Ruta: "/.git/config", Estado: 200,
		Ultima: time.Now(), UltimoOrigen: netip.MustParseAddr(deFuera),
	})
	if err := h.Volcar(); err != nil {
		t.Fatalf("Volcar: %v", err)
	}
	crudo, err := os.ReadFile(ruta)
	if err != nil {
		t.Fatalf("leer el archivo: %v", err)
	}
	for _, prohibido := range []string{
		"cookie", "Cookie", "authorization", "Authorization",
		"cuerpo", "body", "contrasena", "contraseña", "password",
		"agente", "user-agent", "User-Agent", "cabecera",
	} {
		if strings.Contains(string(crudo), prohibido) {
			t.Errorf("el archivo de hallazgos contiene %q:\n%s", prohibido, crudo)
		}
	}
}

// La ruta se acota igual que en el anillo de rechazos: quien la elige es un
// tercero, y una URL larguísima es precisamente lo que hace un sondeo.
func TestLaRutaDelHallazgoSeAcota(t *testing.T) {
	h := hallazgosDePrueba(t)
	h.Anotar(Hallazgo{
		Clase: RutaAjenaAtendida, Metodo: "GET",
		Ruta: "/.git/" + strings.Repeat("a", 4000), Estado: 200, Ultima: time.Now(),
	})
	v := h.Todos()
	if len(v) != 1 {
		t.Fatalf("hallazgos = %d", len(v))
	}
	if len(v[0].Ruta) > topeRuta {
		t.Errorf("la ruta guardada mide %d; el tope es %d", len(v[0].Ruta), topeRuta)
	}
}

// LO QUE EL HALLAZGO NO AFIRMA. Es la mitad que impide que la etiqueta se lea
// como un veredicto: el servidor no conoce explotación, ni intrusión, ni
// compromiso — conoce que respondió donde no debía.
func TestElHallazgoNoAfirmaCompromiso(t *testing.T) {
	texto := RutaAjenaAtendida.Etiqueta() + " " + RutaAjenaAtendida.Explicacion()
	minusculas := strings.ToLower(texto)
	for _, prohibida := range []string{"exploit", "intrusión", "intrusion", "compromet", "hackead"} {
		// «NO demuestra … compromiso» sí puede aparecer: lo que no puede es
		// afirmarlo. Se comprueba que cada aparición vaya negada.
		i := strings.Index(minusculas, prohibida)
		if i >= 0 && !strings.Contains(minusculas[:i], "no demuestra") {
			t.Errorf("el texto afirma %q sin negarlo: %s", prohibida, texto)
		}
	}
	if !strings.Contains(minusculas, "no demuestra") {
		t.Errorf("la explicación no dice lo que NO puede afirmar: %s", texto)
	}
}
