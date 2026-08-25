package aviso

import (
	"strings"
	"testing"
	"time"

	"nasd/internal/seguridad"
)

// El mensaje es lo único que ve el responsable. Estas pruebas cubren las tres
// formas en que puede traicionarle: inyectando marcado que escribió un extraño,
// pasándose del límite del proveedor y afirmando más de lo observado.

func TestUnaRutaConMarcadoHTMLLlegaLiteral(t *testing.T) {
	// LAS RUTAS LAS ELIGE QUIEN SONDEA. Una petición a «/<b>x&y.php» es
	// perfectamente construible, y el mensaje sale con parse_mode=HTML.
	//
	// No es un riesgo teórico en este proyecto: el 2026-08-14 un comentario HTML
	// del propio código citando «{{fecha}}» fue EJECUTADO por html/template y
	// produjo un 200 OK con la página cortada a la mitad. Aquella vez el texto
	// era nuestro; aquí lo escribe un extraño.
	ahora := time.Now()
	o := explorador("203.0.113.7", 9, ahora)
	o.Evidencia = []seguridad.Sonda{
		{Metodo: "GET", Ruta: "/<b>negrita</b>&amigo.php", Estado: 404, Veces: 1},
	}

	texto := Mensaje(Evaluar(Entrada{Origenes: []OrigenVisto{o}}, ahora)[0]).Texto

	// El marcado del atacante llega ESCAPADO, no interpretado.
	if strings.Contains(texto, "<b>negrita</b>") {
		t.Errorf("el marcado de la ruta llegó SIN escapar; se puede inyectar\n%s", texto)
	}
	if !strings.Contains(texto, "&lt;b&gt;negrita&lt;/b&gt;") {
		t.Errorf("la ruta no aparece escapada; se perdió la evidencia\n%s", texto)
	}
	// El «&» suelto también: sin escapar, Telegram rechaza el mensaje ENTERO
	// con un 400 y el aviso no llega.
	if !strings.Contains(texto, "&amp;amigo") {
		t.Errorf("el «&» de la ruta no se escapó; el proveedor rechazaría el mensaje\n%s", texto)
	}
}

func TestElMarcadoPropioSiSobrevive(t *testing.T) {
	// La otra mitad: si se escapara TODO, incluido el marcado que pone este
	// paquete, el mensaje llegaría con las etiquetas a la vista. La lista
	// positiva es que el marcado lo pone mensaje.go y nada más.
	ahora := time.Now()
	texto := Mensaje(Evaluar(Entrada{Origenes: []OrigenVisto{explorador("203.0.113.7", 9, ahora)}}, ahora)[0]).Texto
	if !strings.Contains(texto, "<b>") || !strings.Contains(texto, "</b>") {
		t.Errorf("el marcado propio no llegó; el mensaje sale en texto plano\n%s", texto)
	}
}

func TestElMensajeNoPasaDelTopeYDiceQueRecorta(t *testing.T) {
	// Telegram corta en 4096 caracteres y devuelve 400 si se pasa: un aviso
	// demasiado largo NO llega recortado, no llega EN ABSOLUTO.
	ahora := time.Now()
	s := Situacion{
		Clase: ClaseExploracion, Severidad: Amarillo, Sujeto: "203.0.113.7",
		Primera: ahora.Add(-time.Hour), Ultima: ahora, Veces: 3,
	}
	// Evidencia desmedida: es lo que produciría un fallo aguas arriba, y el
	// formateador tiene que sobrevivir a él en vez de confiar en que no ocurra.
	for i := range 400 {
		s.conHecho("Sonda", strings.Repeat("x", 60)+string(rune('a'+i%26)))
	}

	a := Mensaje(s)
	if n := len([]rune(a.Texto)); n > topeMensaje {
		t.Errorf("el mensaje mide %d caracteres; el tope es %d", n, topeMensaje)
	}
	// Y DICE que recorta. Un mensaje cortado en seco se lee como una avería;
	// uno que lo dice se lee como lo que es — la misma honestidad con la que el
	// panel publica SondasVistas junto a su tabla.
	if !strings.Contains(a.Texto, "recortado") {
		t.Errorf("el mensaje se recortó en silencio\n%s", a.Texto)
	}
}

func TestUnMensajeNormalNoSeRecorta(t *testing.T) {
	// La otra mitad: si el tope estuviera mal calculado, TODO se recortaría y la
	// prueba de arriba pasaría igual sin demostrar nada.
	ahora := time.Now()
	o := explorador("203.0.113.7", 9, ahora)
	o.Apartado = apartadoDe("203.0.113.7", ahora)
	a := Mensaje(Evaluar(Entrada{Origenes: []OrigenVisto{o}}, ahora)[0])
	if strings.Contains(a.Texto, "recortado") {
		t.Errorf("un aviso corriente se recortó\n%s", a.Texto)
	}
	if n := len([]rune(a.Texto)); n > 900 {
		t.Errorf("un aviso corriente mide %d caracteres; debería ser compacto\n%s", n, a.Texto)
	}
}

func TestTodoAvisoDiceQueHacer(t *testing.T) {
	// El charter §8 exige que toda alerta sea accionable. Una rejilla de cifras
	// no lo es: obliga a recordar qué valor era preocupante.
	ahora := time.Now()
	for c := ClaseSondeoAjeno; c <= UltimaClase; c++ {
		s := Situacion{Clase: c, Severidad: c.SeveridadBase(), Primera: ahora, Ultima: ahora}
		texto := Mensaje(s).Texto
		if !strings.Contains(texto, "Acción recomendada") {
			t.Errorf("clase %q: el aviso no dice qué hacer\n%s", c, texto)
		}
		if strings.TrimSpace(c.Recomendacion()) == "" {
			t.Errorf("clase %q: recomendación vacía", c)
		}
	}
}

func TestLaAccionConcretaGanaALaGenerica(t *testing.T) {
	// Cada indicador de /estado ya lleva escrito su propio «qué hacer» desde la
	// Fase 4. Reescribirlo aquí habría creado un segundo texto para el mismo
	// indicador, que es como se acaba con dos consejos que se contradicen.
	ahora := time.Now()
	s := DeAveria(Averia{
		Clave: "limitacion", Nombre: "Limitación del SoC", Valor: "activa",
		Accion: "Comprobar a mano por SSH: «vcgencmd get_throttled»", Grave: true,
	}, ahora)
	texto := Mensaje(s).Texto
	if !strings.Contains(texto, "vcgencmd get_throttled") {
		t.Errorf("la acción concreta del indicador no llegó al aviso\n%s", texto)
	}
	if s.Severidad != Rojo {
		t.Errorf("severidad = %q; un veredicto de fallo es rojo", s.Severidad)
	}
}

func TestSoloElRojoPideRevision(t *testing.T) {
	// Un amarillo significa, por definición, que los controles siguieron
	// funcionando. Decir «revisión requerida» ahí sería alarmar por algo que el
	// nodo ya manejó — y gastar la credibilidad de los que sí importan.
	ahora := time.Now()
	amarillo := Mensaje(Situacion{Clase: ClaseExploracion, Severidad: Amarillo, Primera: ahora, Ultima: ahora}).Texto
	if !strings.Contains(amarillo, "Estado del NAS: Protegido") {
		t.Errorf("un amarillo no dice «Protegido»\n%s", amarillo)
	}
	rojo := Mensaje(Situacion{Clase: ClaseRutaExpuesta, Severidad: Rojo, Primera: ahora, Ultima: ahora}).Texto
	if !strings.Contains(rojo, "Estado del NAS: Revisión requerida") {
		t.Errorf("un rojo no pide revisión\n%s", rojo)
	}
}

func TestNingunAvisoUsaLenguajeAlarmista(t *testing.T) {
	// El encargo lo pide textualmente: «no utilizar lenguaje sensacionalista, no
	// utilizar afirmaciones que excedan la evidencia». Se comprueba sobre TODAS
	// las clases, para que una futura no se cuele.
	ahora := time.Now()
	prohibidas := []string{"!!", "urgente", "peligro", "hackeado", "atacado", "alarma"}
	for c := ClaseSondeoAjeno; c <= UltimaClase; c++ {
		s := Situacion{Clase: c, Severidad: c.SeveridadBase(), Primera: ahora, Ultima: ahora}
		texto := strings.ToLower(Mensaje(s).Titulo + " " + Mensaje(s).Texto)
		for _, p := range prohibidas {
			if strings.Contains(texto, p) {
				t.Errorf("clase %q: el aviso contiene %q\n%s", c, p, texto)
			}
		}
	}
}

func TestElCanalRecuperadoEsNaranjaYNoHablaDeSeguridad(t *testing.T) {
	// NARANJA debe permanecer semánticamente separado de intrusión. Este es el
	// único naranja que nace en el nodo, y tiene que decir explícitamente que no
	// habla del NAS sino del canal.
	ahora := time.Now()
	a := MensajeCanalRecuperado(ahora.Add(-90*time.Minute), 12, ahora)
	if a.Severidad != Naranja {
		t.Errorf("severidad = %q; se esperaba naranja", a.Severidad)
	}
	if !strings.Contains(a.Texto, "es el canal, no el NAS") {
		t.Errorf("el aviso no separa el canal de la seguridad del nodo\n%s", a.Texto)
	}
	if !strings.Contains(a.Texto, "1 h") && !strings.Contains(a.Texto, "90 min") {
		t.Errorf("el aviso no dice cuánto duró el hueco\n%s", a.Texto)
	}
}
