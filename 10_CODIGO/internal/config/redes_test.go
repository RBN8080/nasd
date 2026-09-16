package config

import (
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// conTOML escribe un TOML mínimo y lo carga. Mínimo a propósito: estas pruebas
// hablan de las dos claves de red y de nada más, así que todo lo demás tiene
// que venir de porDefecto() para que un cambio ajeno no las haga fallar.
func conTOML(t *testing.T, cuerpo string) (Config, error) {
	t.Helper()
	ruta := filepath.Join(t.TempDir(), "nasd.toml")
	if err := os.WriteFile(ruta, []byte(cuerpo), 0o600); err != nil {
		t.Fatalf("escribir el TOML de prueba: %v", err)
	}
	return Cargar(ruta)
}

// LA PRUEBA QUE PROTEGE A PRODUCCIÓN, y es la razón de ser de este archivo.
//
// ADR-0095 quitó de internal/seguridad los dos prefijos literales. El riesgo
// de ese cambio no es que falle: es que cambie EN SILENCIO lo que el nodo que
// ya está en marcha considera «su casa». El primer caso de la tabla es el nodo
// real —escucha en 192.168.1.38 (ADR-0018)— y debe seguir dando exactamente el
// prefijo que antes estaba escrito a mano.
func TestLaRedDeCasaSeDerivaDeLaDireccionDeEscucha(t *testing.T) {
	for _, caso := range []struct {
		nombre    string
		direccion string
		espera    string // vacío = no se debe adivinar nada
	}{
		{"el nodo de producción sigue dando lo que antes era literal", "192.168.1.38", "192.168.1.0/24"},
		{"otra casa cualquiera acierta sola", "10.13.37.9", "10.13.37.0/24"},
		{"y otra más, sin que nadie configure nada", "172.20.4.200", "172.20.4.0/24"},

		// Los tres casos en los que callarse es lo correcto. Ver lanDe.
		{"el bucle local NO es una casa: sería el propio nodo contado como LAN", "127.0.0.1", ""},
		{"IPv6 no se adivina: de eso se encarga AprenderRedesPropias", "fd00::1", ""},
		{"una dirección pública no convierte a su /24 en familia", "203.0.113.7", ""},
	} {
		t.Run(caso.nombre, func(t *testing.T) {
			obtenido := lanDe(caso.direccion)
			if caso.espera == "" {
				if obtenido.IsValid() {
					t.Fatalf("lanDe(%q) = %s; no debía adivinar nada", caso.direccion, obtenido)
				}
				return
			}
			if obtenido.String() != caso.espera {
				t.Fatalf("lanDe(%q) = %s; se esperaba %s", caso.direccion, obtenido, caso.espera)
			}
		})
	}
}

// Y lo mismo por el camino de verdad, el que recorre un nodo al arrancar: sin
// tocar el TOML, la red sale de la dirección de escucha.
func TestSinDeclararlaLaRedSaleDeLaDireccion(t *testing.T) {
	c, err := conTOML(t, "[red]\ndireccion = \"10.13.37.9\"\n")
	if err != nil {
		t.Fatalf("cargar: %v", err)
	}
	if c.RedLAN.String() != "10.13.37.0/24" {
		t.Errorf("RedLAN = %s; se esperaba 10.13.37.0/24 derivado de la dirección", c.RedLAN)
	}
	// El túnel no se deriva de nada: es el que reparte 12_wireguard.sh.
	if c.RedTunel.String() != "10.77.0.0/24" {
		t.Errorf("RedTunel = %s; se esperaba el valor por omisión 10.77.0.0/24", c.RedTunel)
	}
}

// Lo declarado manda sobre lo derivado, que es lo que permite un nodo cuya LAN
// no es un /24 o que escucha en una red distinta de la que sirve.
func TestLaRedDeclaradaGanaALaDerivada(t *testing.T) {
	c, err := conTOML(t, "[red]\ndireccion = \"192.168.1.38\"\nlan = \"192.168.0.0/16\"\ntunel = \"10.99.0.0/24\"\n")
	if err != nil {
		t.Fatalf("cargar: %v", err)
	}
	if c.RedLAN.String() != "192.168.0.0/16" {
		t.Errorf("RedLAN = %s; lo declarado en el TOML debe ganar a la derivación", c.RedLAN)
	}
	if c.RedTunel.String() != "10.99.0.0/24" {
		t.Errorf("RedTunel = %s; se esperaba 10.99.0.0/24", c.RedTunel)
	}
}

// Un prefijo escrito con bits de host puestos —«192.168.1.38/24»— es lo que
// escribe quien copia la dirección del nodo y le añade la máscara. Se acepta y
// se normaliza en vez de rechazarse: el operador quiso decir la red, y
// rechazarlo dejaría el NAS sin arrancar por un detalle de notación.
func TestUnPrefijoConBitsDeHostSeNormaliza(t *testing.T) {
	c, err := conTOML(t, "[red]\nlan = \"192.168.1.38/24\"\n")
	if err != nil {
		t.Fatalf("cargar: %v", err)
	}
	if c.RedLAN.String() != "192.168.1.0/24" {
		t.Errorf("RedLAN = %s; se esperaba 192.168.1.0/24 normalizado", c.RedLAN)
	}
}

// Dos redes solapadas no fallan solas: el túnel se contaría entero como red
// local y el panel lo diría con toda seguridad. Por eso se rechaza al arrancar
// (P5) en vez de dejarlo mentir.
func TestRedesQueSeSolapanNoArrancan(t *testing.T) {
	_, err := conTOML(t, "[red]\nlan = \"10.0.0.0/8\"\ntunel = \"10.77.0.0/24\"\n")
	if err == nil {
		t.Fatal("una LAN que contiene al túnel debía rechazarse: el túnel se contaría como red local")
	}
}

// Un prefijo ilegible se rechaza nombrando la clave. Sin el nombre, el
// operador tiene que adivinar cuál de las dos escribió mal.
func TestUnPrefijoIlegibleSeRechazaConSuNombre(t *testing.T) {
	for _, caso := range []struct{ clave, cuerpo string }{
		{"red.lan", "[red]\nlan = \"esto no es una red\"\n"},
		{"red.tunel", "[red]\ntunel = \"10.77.0.0\"\n"}, // sin máscara: no es un prefijo
	} {
		_, err := conTOML(t, caso.cuerpo)
		if err == nil {
			t.Fatalf("%s ilegible debía rechazarse", caso.clave)
		}
		if got := err.Error(); !strings.Contains(got, caso.clave) {
			t.Errorf("el error no nombra la clave %s: %q", caso.clave, got)
		}
	}
}

// El ejemplo del repositorio deja las dos claves COMENTADAS a propósito: son
// opcionales y la derivación acierta sola. Si alguien las descomenta ahí, un
// nodo nuevo se llevaría la red de otra casa sin enterarse.
func TestElEjemploNoDeclaraLasRedes(t *testing.T) {
	c, err := Cargar("../../despliegue/nasd.toml.ejemplo")
	if err != nil {
		t.Fatalf("el ejemplo del repositorio no se puede leer: %v", err)
	}
	// La dirección del ejemplo es la del nodo, así que la derivación tiene que
	// dar su misma red. Comprobarlo aquí ata el ejemplo a la derivación: si
	// alguien cambia una de las dos cosas, esto lo dice.
	esperada := lanDe(c.Direccion)
	if !esperada.IsValid() {
		t.Fatalf("la dirección del ejemplo (%q) no permite derivar ninguna red", c.Direccion)
	}
	if c.RedLAN != esperada {
		t.Errorf("RedLAN = %s; el ejemplo no debe declarar red.lan, sino dejar que se derive (%s)",
			c.RedLAN, esperada)
	}
	if c.RedTunel != netip.MustParsePrefix("10.77.0.0/24") {
		t.Errorf("RedTunel = %s; el ejemplo no debe declarar red.tunel", c.RedTunel)
	}
}
