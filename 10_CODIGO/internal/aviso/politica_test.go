package aviso

import "testing"

// LA GARANTÍA QUE NO SE PUEDE PERDER: un rojo llega SIEMPRE.
//
// El encargo la fija sin matices —«NO puede ser silenciada por quiet hours,
// digest, modo silencio, agrupación ni volumen previo»— y es la clase de regla
// que se rompe sola al añadir un modo nuevo, no al escribirla mal el primer día.

func TestElRojoSeEntregaInmediatoEnTodosLosModos(t *testing.T) {
	// SE RECORRE EL ENUM ENTERO en vez de enumerar los tres modos a mano, y esa
	// es la mitad que importa: un cuarto modo escrito dentro de seis meses entra
	// en esta prueba SIN que nadie se acuerde de añadirlo aquí. Enumerarlos a
	// mano habría dejado la garantía dependiendo de la memoria del siguiente.
	for m := ModoNormal; m <= UltimoModo; m++ {
		if e := Decidir(Rojo, m); e != EntregaInmediata {
			t.Errorf("modo %q: el rojo salió como %q; DEBE ser inmediata", m, e)
		}
	}
}

func TestElSilencioRetieneElAmarilloPeroNoLoPierde(t *testing.T) {
	// Retenido NO es descartado: acaba en el resumen, y entero. Si esto
	// devolviera EntregaResumen, un amarillo silenciado se convertiría en una
	// línea de estadística y dejaría de poder leerse.
	if e := Decidir(Amarillo, ModoSilencio); e != EntregaRetenida {
		t.Errorf("amarillo en silencio salió como %q; se esperaba retenida", e)
	}
}

func TestLaPoliticaCompleta(t *testing.T) {
	// La tabla entera, escrita una vez, para que cualquier cambio de criterio
	// tenga que pasar por aquí y no por la cabeza de quien edite Decidir.
	for _, c := range []struct {
		severidad Severidad
		modo      Modo
		espera    Entrega
	}{
		{Verde, ModoNormal, EntregaResumen},
		{Verde, ModoSilencio, EntregaResumen},
		{Verde, ModoObservacion, EntregaInmediata},

		{Amarillo, ModoNormal, EntregaInmediata},
		{Amarillo, ModoSilencio, EntregaRetenida},
		{Amarillo, ModoObservacion, EntregaInmediata},

		// El naranja que nace en el nodo es «el canal estuvo mudo», y llega
		// cuando el canal vuelve. Retenerlo escondería justo el hecho de que
		// hubo un hueco, que es lo único que explica los avisos que faltan.
		{Naranja, ModoNormal, EntregaInmediata},
		{Naranja, ModoSilencio, EntregaInmediata},
		{Naranja, ModoObservacion, EntregaInmediata},

		{Rojo, ModoNormal, EntregaInmediata},
		{Rojo, ModoSilencio, EntregaInmediata},
		{Rojo, ModoObservacion, EntregaInmediata},
	} {
		t.Run(c.severidad.String()+"/"+c.modo.String(), func(t *testing.T) {
			if e := Decidir(c.severidad, c.modo); e != c.espera {
				t.Errorf("Decidir(%q, %q) = %q; se esperaba %q", c.severidad, c.modo, e, c.espera)
			}
		})
	}
}

func TestUnModoDesconocidoNoSeDegradaEnSilencio(t *testing.T) {
	// Un modo mal escrito en la configuración NO puede leerse como «normal» sin
	// decirlo: el responsable creería estar en silencio mientras el nodo le
	// escribe, o al revés, y ninguna de las dos se nota hasta que importa. El
	// booleano es lo que permite a la raíz de composición fallar al arrancar.
	for _, malo := range []string{"", "Normal", "quiet", "silencio ", "observación"} {
		if _, ok := ModoDesde(malo); ok {
			t.Errorf("ModoDesde(%q) lo dio por bueno; debe rechazarlo", malo)
		}
	}
	for m := ModoNormal; m <= UltimoModo; m++ {
		v, ok := ModoDesde(m.String())
		if !ok || v != m {
			t.Errorf("ModoDesde(%q) = (%v, %v); no sobrevive la ida y vuelta", m, v, ok)
		}
	}
}

func TestLasSeveridadesSobrevivenLaIdaYVuelta(t *testing.T) {
	// La clave estable es lo que se persiste en /var/lib/nasd/avisos. Si se
	// rompiera, el archivo de ayer se leería mal HOY y el nodo repetiría avisos
	// o —peor— se callaría uno.
	for s := Verde; s <= UltimaSeveridad; s++ {
		v, ok := SeveridadDesde(s.String())
		if !ok || v != s {
			t.Errorf("SeveridadDesde(%q) = (%v, %v); no sobrevive la ida y vuelta", s, v, ok)
		}
	}
	if _, ok := SeveridadDesde("critico"); ok {
		t.Error("una clave inventada se dio por buena; debe rechazarse")
	}
}

func TestSoloElVerdeLlegaEnSilencio(t *testing.T) {
	// El anti-fatiga que NO depende de nuestra política sino del proveedor: el
	// resumen aterriza sin vibrar y lo demás no.
	if Verde.Interrumpe() {
		t.Error("el verde interrumpe; debe llegar en silencio")
	}
	for _, s := range []Severidad{Amarillo, Naranja, Rojo} {
		if !s.Interrumpe() {
			t.Errorf("%q no interrumpe; debe sonar", s)
		}
	}
}
