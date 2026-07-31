package web

import "testing"

// Regresión del defecto encontrado en producción el 2026-07-30.
//
// El cliente enviaba «Nas-Destino» SIN codificar. Las cabeceras HTTP solo
// admiten Latin-1, así que subir desde una carpeta con acentos hacía que
// fetch() lanzara «String contains non ISO-8859-1 code point» y la subida
// ni siquiera saliera del navegador.
//
// Aquí se comprueba el lado del servidor: que decodificar() deshaga
// exactamente lo que produce encodeURIComponent.
func TestDecodificarCabeceras(t *testing.T) {
	casos := map[string]string{
		// Lo que encodeURIComponent genera para cada caso.
		"Fotos%202026":                      "Fotos 2026",
		"cumplea%C3%B1os":                   "cumpleaños",
		"Vacaciones%20%C3%91":               "Vacaciones Ñ",
		"a%2Fb":                             "a/b",
		"factura%20(1).pdf":                 "factura (1).pdf",
		"imager-1.9.6.exe":                  "imager-1.9.6.exe",
		"":                                  "",
		"acentu%C3%A1do%2Fni%C3%B1os/x.jpg": "acentuádo/niños/x.jpg",
	}
	for entrada, esperado := range casos {
		if got := decodificar(entrada); got != esperado {
			t.Errorf("decodificar(%q) = %q; se esperaba %q", entrada, got, esperado)
		}
	}
}

// PathUnescape y no QueryUnescape: el segundo convierte «+» en espacio, y
// un archivo llamado «a+b.jpg» se habría guardado como «a b.jpg».
func TestDecodificarNoConvierteMasEnEspacio(t *testing.T) {
	if got := decodificar("a+b.jpg"); got != "a+b.jpg" {
		t.Errorf("decodificar(\"a+b.jpg\") = %q; el «+» no debe volverse espacio", got)
	}
	// Y el «+» codificado por encodeURIComponent sí debe volver a «+».
	if got := decodificar("a%2Bb.jpg"); got != "a+b.jpg" {
		t.Errorf("decodificar(\"a%%2Bb.jpg\") = %q; se esperaba \"a+b.jpg\"", got)
	}
}
