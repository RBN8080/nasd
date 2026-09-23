package seguridad

import (
	"net/netip"
	"testing"
)

// enOtraCasa configura las dos redes y las restaura al terminar. Es estado de
// paquete (ver ConfigurarRedes), así que sin esto una prueba le cambiaría la
// casa a las demás.
func enOtraCasa(t *testing.T, lan, tunel string) {
	t.Helper()
	viejaLAN, viejoTunel := prefijoLAN, prefijoTunel
	t.Cleanup(func() { prefijoLAN, prefijoTunel = viejaLAN, viejoTunel })
	ConfigurarRedes(netip.MustParsePrefix(lan), netip.MustParsePrefix(tunel))
}

// La primera mitad de la prueba es el control positivo: sin configurar, el
// fallo se reproduce. Sin esa mitad, la segunda no demuestra nada.
func TestOtraCasaDejaDeContarseComoInternet(t *testing.T) {
	deLaOtraCasa := netip.MustParseAddr("10.13.37.20")

	if got := ClasificarRed(deLaOtraCasa); got != RedInternet {
		t.Fatalf("control positivo: con la red por omisión, %s debía salir como Internet y salió %q",
			deLaOtraCasa, got.Etiqueta())
	}

	enOtraCasa(t, "10.13.37.0/24", "10.77.0.0/24")

	if got := ClasificarRed(deLaOtraCasa); got != RedLocal {
		t.Errorf("tras declarar su red, %s se clasificó como %q", deLaOtraCasa, got.Etiqueta())
	}
	// Y la de antes pasa a ser de fuera, que es justo lo correcto: en esa casa
	// 192.168.1.x no es nadie conocido.
	if got := ClasificarRed(netip.MustParseAddr("192.168.1.38")); got != RedInternet {
		t.Errorf("192.168.1.38 se clasificó como %q en una casa que es 10.13.37.0/24", got.Etiqueta())
	}
	// El túnel no se ha tocado y tiene que seguir en pie.
	if got := ClasificarRed(netip.MustParseAddr("10.77.0.3")); got != RedTunel {
		t.Errorf("el túnel se clasificó como %q", got.Etiqueta())
	}
}

// LA SEGUNDA CONSECUENCIA, la que se olvida: las listas de bloqueo.
//
// tocaLaCasa (lista.go) usa estos mismos prefijos para negarse a bloquear un
// tramo que contenga a los de casa. Si la red configurada no llegara hasta
// ahí, un tramo que incluyera a la familia entera se podría bloquear sin que
// nada avisara — y el precio de ese fallo es dejar a la casa fuera de su
// propio NAS. Es la misma lección que ya costó una hora con el IPv6.
func TestLaCasaNUEVAtampocoSePuedeBloquear(t *testing.T) {
	suTramo := TramoDe(netip.MustParsePrefix("10.13.0.0/16"))

	if tocaLaCasa(suTramo) {
		t.Fatalf("control positivo: con la red por omisión, 10.13.0.0/16 no debía tocar la casa")
	}

	enOtraCasa(t, "10.13.37.0/24", "10.77.0.0/24")

	if !tocaLaCasa(suTramo) {
		t.Error("un tramo que contiene la LAN configurada se pudo bloquear: dejaría a la casa fuera del NAS")
	}
}

// UN PREFIJO INVÁLIDO SIGNIFICA «NO SÉ», NO «NO HAY CASA».
//
// config.lanDe se niega a adivinar la LAN en tres casos y devuelve un prefijo
// inválido. Si eso vaciara el prefijo, el resultado sería exactamente el
// fallo que ADR-0095 arregla: la casa entera contada como Internet. Se
// conserva lo que hubiera.
func TestUnPrefijoInvalidoNoBorraElQueHabia(t *testing.T) {
	viejaLAN, viejoTunel := prefijoLAN, prefijoTunel
	t.Cleanup(func() { prefijoLAN, prefijoTunel = viejaLAN, viejoTunel })

	lan, tunel := ConfigurarRedes(netip.Prefix{}, netip.Prefix{})

	if lan != viejaLAN || tunel != viejoTunel {
		t.Fatalf("un prefijo inválido cambió la configuración: lan %s→%s, tunel %s→%s",
			viejaLAN, lan, viejoTunel, tunel)
	}
	if got := ClasificarRed(netip.MustParseAddr("192.168.1.38")); got != RedLocal {
		t.Errorf("la casa se perdió al configurar con un prefijo inválido: %q", got.Etiqueta())
	}
}

// Se normaliza al fijar, por lo mismo que en config: quien escribe la
// dirección del nodo con su máscara quiso decir la red.
func TestSeNormalizaAlFijar(t *testing.T) {
	viejaLAN, viejoTunel := prefijoLAN, prefijoTunel
	t.Cleanup(func() { prefijoLAN, prefijoTunel = viejaLAN, viejoTunel })

	lan, _ := ConfigurarRedes(netip.MustParsePrefix("10.13.37.20/24"), netip.Prefix{})
	if lan.String() != "10.13.37.0/24" {
		t.Errorf("ConfigurarRedes dejó %s; se esperaba 10.13.37.0/24", lan)
	}
}
