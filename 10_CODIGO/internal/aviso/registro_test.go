package aviso

import (
	"testing"
	"time"

	"nasd/internal/seguridad"
)

// LA PROMESA DE ESTA CAPA ES UNA SOLA: que 500 hechos no produzcan 500
// mensajes. Si estas pruebas fallan, la capa no reduce ruido — lo traslada.

func TestUnaTormentaDeEventosNoProduceUnaTormentaDeAvisos(t *testing.T) {
	// El caso REAL que esto modela: el 2026-08-16, veinte direcciones de
	// DRIFTNET recorrieron el 443 durante cinco minutos. La vigilancia corre
	// CADA MINUTO, así que sin deduplicación un barrido de una hora produciría
	// un mensaje por origen y por minuto.
	r := registroDePrueba(t)
	base := time.Now()

	var avisos int
	const ciclos = 60
	for ciclo := range ciclos {
		ahora := base.Add(time.Duration(ciclo) * time.Minute)
		// La conducta CRECE en cada ciclo —más rutas, más eventos—, que es lo
		// que hace la prueba honesta: no se está deduplicando un valor idéntico,
		// se está reconociendo la misma HISTORIA aunque sus cifras cambien.
		e := Entrada{Origenes: []OrigenVisto{explorador("203.0.113.7", 8+ciclo, ahora)}}
		avisos += len(r.Absorber(Evaluar(e, ahora), ahora))
	}

	if avisos != 1 {
		t.Errorf("%d ciclos de la misma conducta produjeron %d avisos; se esperaba 1", ciclos, avisos)
	}
	_, absorbidas := r.Cuentas()
	if absorbidas != ciclos-1 {
		t.Errorf("absorbidas = %d; se esperaban %d", absorbidas, ciclos-1)
	}
}

func TestVariosOrigenesALaVezSonVariosAvisosYNoUnoSolo(t *testing.T) {
	// La otra mitad, y hay que comprobarla o la deduplicación podría estar
	// «funcionando» por juntar cosas que no son la misma: veinte direcciones
	// distintas son veinte historias, aunque lleguen en el mismo minuto.
	r := registroDePrueba(t)
	ahora := time.Now()

	var origenes []OrigenVisto
	for i := range 20 {
		origenes = append(origenes, explorador(direccion(i), 9, ahora))
	}
	avisar := r.Absorber(Evaluar(Entrada{Origenes: origenes}, ahora), ahora)
	if len(avisar) != 20 {
		t.Errorf("20 orígenes distintos produjeron %d avisos; se esperaban 20", len(avisar))
	}
}

func TestUnaSituacionPromueveYAvisaUnaSolaVezPorEscalon(t *testing.T) {
	// La regla que permite que una historia crezca sin un mensaje por evento:
	// se avisa al aparecer y se avisa al EMPEORAR, y nada más.
	r := registroDePrueba(t)
	base := time.Now()
	const ip = "203.0.113.7"

	// Ciclo 1: anomalía verde — hay rechazos raros pero ninguna señal.
	sube := r.Absorber(Evaluar(Entrada{Origenes: []OrigenVisto{anomalo(ip, base)}}, base), base)
	if len(sube) != 1 || sube[0].Severidad != Verde {
		t.Fatalf("primer ciclo: %d avisos, severidad %v; se esperaba 1 verde", len(sube), severidadDe(sube))
	}

	// Ciclos 2 y 3: la MISMA anomalía. No debe volver a avisar.
	for i := 1; i <= 2; i++ {
		ahora := base.Add(time.Duration(i) * time.Minute)
		if n := len(r.Absorber(Evaluar(Entrada{Origenes: []OrigenVisto{anomalo(ip, ahora)}}, ahora), ahora)); n != 0 {
			t.Fatalf("ciclo %d: %d avisos; se esperaba 0 (repetición)", i+1, n)
		}
	}

	// Ciclo 4: la misma anomalía cruza a Atencion. AHORA sí debe avisar.
	ahora := base.Add(3 * time.Minute)
	grave := anomalo(ip, ahora)
	grave.Gravedad = seguridad.Atencion
	sube = r.Absorber(Evaluar(Entrada{Origenes: []OrigenVisto{grave}}, ahora), ahora)
	if len(sube) != 1 || sube[0].Severidad != Amarillo {
		t.Fatalf("promoción: %d avisos, severidad %v; se esperaba 1 amarillo", len(sube), severidadDe(sube))
	}
	// Y la cuenta acumulada tiene que viajar CON la promoción: el mensaje dice
	// «4 observaciones desde las …», no «acaba de empezar».
	if sube[0].Veces != 4 {
		t.Errorf("Veces = %d; se esperaba 4 (la historia lleva cuatro ciclos)", sube[0].Veces)
	}
}

func TestUnaSituacionNuncaDegradaDentroDeSuVida(t *testing.T) {
	// Sin esta regla habría un ciclo perverso: amarillo, verde, amarillo, y tres
	// mensajes donde hubo una sola historia. Lo que cierra una situación es que
	// deje de haber evidencia, no que un ciclo la vea más floja.
	r := registroDePrueba(t)
	base := time.Now()
	const ip = "203.0.113.7"

	fuerte := Entrada{Origenes: []OrigenVisto{explorador(ip, 12, base)}}
	if n := len(r.Absorber(Evaluar(fuerte, base), base)); n != 1 {
		t.Fatalf("primer aviso: %d; se esperaba 1", n)
	}

	// Se fabrica a mano una situación de la MISMA clave con severidad menor.
	// Absorber tiene que tragársela sin avisar y sin bajar lo ya notificado.
	floja := Situacion{
		Clase: ClaseExploracion, Severidad: Verde, Sujeto: ip,
		Primera: base, Ultima: base.Add(time.Minute),
	}
	ahora := base.Add(time.Minute)
	if n := len(r.Absorber([]Situacion{floja}, ahora)); n != 0 {
		t.Errorf("una severidad MENOR avisó %d veces; se esperaba 0", n)
	}

	// Y al volver la severidad original tampoco avisa: nunca llegó a bajar, así
	// que esto no es una promoción.
	ahora = base.Add(2 * time.Minute)
	otraVez := Entrada{Origenes: []OrigenVisto{explorador(ip, 20, ahora)}}
	if n := len(r.Absorber(Evaluar(otraVez, ahora), ahora)); n != 0 {
		t.Errorf("volver a la severidad anterior avisó %d veces; se esperaba 0", n)
	}
}

func TestReiniciarNoRenotificaLoYaAvisado(t *testing.T) {
	// Quince reinicios en catorce días medidos en este nodo. Con la memoria en
	// RAM, cada uno volvería a avisar de todo lo que sigue vivo.
	ruta := t.TempDir() + "/avisos"
	ahora := time.Now()
	e := Entrada{Origenes: []OrigenVisto{explorador("203.0.113.7", 9, ahora)}}

	primero, err := CargarRegistro(ruta)
	if err != nil {
		t.Fatalf("CargarRegistro: %v", err)
	}
	if n := len(primero.Absorber(Evaluar(e, ahora), ahora)); n != 1 {
		t.Fatalf("antes del reinicio: %d avisos; se esperaba 1", n)
	}
	if err := primero.Volcar(); err != nil {
		t.Fatalf("Volcar: %v", err)
	}

	// El nodo se reinicia: estructura nueva, mismo archivo.
	segundo, err := CargarRegistro(ruta)
	if err != nil {
		t.Fatalf("CargarRegistro tras el reinicio: %v", err)
	}
	despues := ahora.Add(time.Minute)
	e2 := Entrada{Origenes: []OrigenVisto{explorador("203.0.113.7", 9, despues)}}
	if n := len(segundo.Absorber(Evaluar(e2, despues), despues)); n != 0 {
		t.Errorf("tras el reinicio se volvió a avisar %d veces; se esperaba 0", n)
	}
}

func TestUnaSituacionOlvidadaVuelveAAvisar(t *testing.T) {
	// La otra mitad de la deduplicación, y sin ella el sistema se volvería
	// mudo: los dos barridos de DRIFTNET llegaron con TRES DÍAS de diferencia
	// (13/08 y 16/08). Eso son dos historias y merecen dos avisos.
	r := registroDePrueba(t)
	base := time.Now()
	e := Entrada{Origenes: []OrigenVisto{explorador("203.0.113.7", 9, base)}}
	if n := len(r.Absorber(Evaluar(e, base), base)); n != 1 {
		t.Fatalf("primer barrido: %d avisos; se esperaba 1", n)
	}

	tresDias := base.Add(72 * time.Hour)
	e2 := Entrada{Origenes: []OrigenVisto{explorador("203.0.113.7", 9, tresDias)}}
	if n := len(r.Absorber(Evaluar(e2, tresDias), tresDias)); n != 1 {
		t.Errorf("segundo barrido tres días después: %d avisos; se esperaba 1", n)
	}
}

func TestUnApartadoRenovadoNoVuelveAAvisar(t *testing.T) {
	// seguridad.Cuarentena.Evaluar ya devuelve SOLO los apartados nuevos, pero
	// la conducta que los provoca sigue ahí ciclo tras ciclo. Es la conducta,
	// no el apartado, la que podría repetir el aviso.
	r := registroDePrueba(t)
	base := time.Now()
	const ip = "203.0.113.7"

	conApartado := explorador(ip, 9, base)
	conApartado.Apartado = apartadoDe(ip, base)
	if n := len(r.Absorber(Evaluar(Entrada{Origenes: []OrigenVisto{conApartado}}, base), base)); n != 1 {
		t.Fatalf("aviso de cuarentena: %d; se esperaba 1", n)
	}

	// Ciclos siguientes: la conducta sigue, el apartado ya no es nuevo.
	for i := 1; i <= 10; i++ {
		ahora := base.Add(time.Duration(i) * time.Minute)
		sigue := Entrada{Origenes: []OrigenVisto{explorador(ip, 9+i, ahora)}}
		if n := len(r.Absorber(Evaluar(sigue, ahora), ahora)); n != 0 {
			t.Fatalf("ciclo %d: %d avisos; se esperaba 0", i, n)
		}
	}
}

func TestElRegistroNoCreceSinLimite(t *testing.T) {
	// Las claves las empuja un EXTRAÑO: cada dirección de Internet que produzca
	// una historia estrena una. Sin tope, el tamaño de esta estructura lo
	// decidiría alguien de fuera, en un nodo de 592 MB.
	r := registroDePrueba(t)
	ahora := time.Now()

	var origenes []OrigenVisto
	for i := range topeConocidas + 200 {
		origenes = append(origenes, explorador(direccion(i), 9, ahora))
	}
	r.Absorber(Evaluar(Entrada{Origenes: origenes}, ahora), ahora)

	if v := r.Vigentes(); v > topeConocidas {
		t.Errorf("el registro guarda %d situaciones; el tope es %d", v, topeConocidas)
	}
}
