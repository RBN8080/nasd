package geoip

import "testing"

func TestNombreDePaisTraduceLosOrigenesHabituales(t *testing.T) {
	casos := map[string]string{
		"MX": "México",
		"US": "Estados Unidos",
		"GB": "Reino Unido",
		"CN": "China",
		"DE": "Alemania",
		"RU": "Rusia",
		"SG": "Singapur", // sin forma propia en el mapa, pero con nombre igual
		"HK": "Hong Kong",
	}
	for iso, quiere := range casos {
		if got := NombreDePais(iso); got != quiere {
			t.Errorf("NombreDePais(%q) = %q, quería %q", iso, got, quiere)
		}
	}
}

// TestNombreDePaisNuncaDevuelveVacioParaUnCodigoReal es la garantía que la
// plantilla necesita: sin ella, un código que la tabla no conoce obligaría a
// seguridad.html a decidir qué pintar en su lugar. La cadena vacía queda
// fuera a propósito — no es un código, es la ausencia de país, y esa
// decisión la toman HayGeo y TieneGeo, no esta función.
func TestNombreDePaisNuncaDevuelveVacioParaUnCodigoReal(t *testing.T) {
	for _, iso := range []string{"ZZ", "XX", "AQ"} {
		if got := NombreDePais(iso); got == "" {
			t.Errorf("NombreDePais(%q) devolvió una cadena vacía", iso)
		}
	}
}

func TestNombreDePaisSeCaeAlCodigoSiNoLoConoce(t *testing.T) {
	if got := NombreDePais("ZZ"); got != "ZZ" {
		t.Errorf(`NombreDePais("ZZ") = %q, se esperaba el propio código`, got)
	}
}

// TestTaiwanNoSeConfundeConChina cubre la corrección deliberada sobre la
// fuente: Natural Earth trae el nombre oficial largo («República de China»),
// que en una lista que se lee de un vistazo se confunde con «China» —justo
// lo contrario de lo que esta tabla existe para evitar.
func TestTaiwanNoSeConfundeConChina(t *testing.T) {
	if got := NombreDePais("TW"); got != "Taiwán" {
		t.Errorf(`NombreDePais("TW") = %q, se esperaba "Taiwán"`, got)
	}
}

// TestNingunNombreEstaVacioEnLaTabla — un valor vacío en el mapa sería un
// error de generación silencioso: la plantilla pintaría un hueco donde
// debería ir un nombre.
func TestNingunNombreEstaVacioEnLaTabla(t *testing.T) {
	for iso, nombre := range nombresDePais {
		if nombre == "" {
			t.Errorf("el código %q tiene nombre vacío en la tabla", iso)
		}
		if len(iso) != 2 {
			t.Errorf("la clave %q no parece un ISO-3166-1 alfa-2", iso)
		}
	}
}
