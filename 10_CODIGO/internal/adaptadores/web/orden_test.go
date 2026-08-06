package web

import (
	"testing"

	"nasd/internal/almacen"
)

func entrada(t *testing.T, nombre string, dir bool) almacen.Entrada {
	t.Helper()
	r, err := almacen.NuevaRuta(nombre)
	if err != nil {
		t.Fatalf("NuevaRuta(%q): %v", nombre, err)
	}
	return almacen.Entrada{Ruta: r, Nombre: nombre, EsDirectori: dir}
}

func nombres(es []almacen.Entrada) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.Nombre
	}
	return out
}

func iguales(a []string, b ...string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// EL CASO QUE MOTIVÓ ESTO. El responsable subió sus carpetas de la escuela
// —«03_3er_Cuatrimestre», «07_7mo_Cuatrimestre»…— y aparecían mezcladas entre
// los archivos y sin seguir su numeración.
func TestCarpetasPrimeroYEnOrden(t *testing.T) {
	es := []almacen.Entrada{
		entrada(t, "IMG_7484.MOV", false),
		entrada(t, "07_7mo_Cuatrimestre", true),
		entrada(t, "IMG_0653.MOV", false),
		entrada(t, "03_3er_Cuatrimestre", true),
		entrada(t, "00_Administrativo", true),
		entrada(t, "2024-12-26-19-22-14.mp4", false),
	}
	ordenar(es)

	if got := nombres(es); !iguales(got,
		"00_Administrativo", "03_3er_Cuatrimestre", "07_7mo_Cuatrimestre",
		"2024-12-26-19-22-14.mp4", "IMG_0653.MOV", "IMG_7484.MOV") {
		t.Fatalf("orden incorrecto:\n%v", got)
	}
}

// El orden alfabético pone «10» antes que «2» porque compara carácter a
// carácter. Con carpetas numeradas eso es justo lo que molesta.
func TestElNumeroSeComparaComoNumero(t *testing.T) {
	es := []almacen.Entrada{
		entrada(t, "10_decimo", true),
		entrada(t, "2_segundo", true),
		entrada(t, "1_primero", true),
		entrada(t, "20_veinte", true),
		entrada(t, "3_tercero", true),
	}
	ordenar(es)

	if got := nombres(es); !iguales(got,
		"1_primero", "2_segundo", "3_tercero", "10_decimo", "20_veinte") {
		t.Fatalf("los números no se ordenaron como números:\n%v", got)
	}
}

// Un nombre puede traer más dígitos de los que cabe en un int64. Convertirlo
// habría desbordado en silencio, que es peor que ordenarlo mal.
func TestUnNumeroEnormeNoDesborda(t *testing.T) {
	enorme := "99999999999999999999999999999999999_x"
	otro := "99999999999999999999999999999999998_x"
	if compararNatural(otro, enorme) >= 0 {
		t.Fatal("con 35 dígitos el orden debe seguir siendo correcto")
	}
}

func TestDetallesDeLaComparacion(t *testing.T) {
	casos := []struct {
		a, b   string
		quiero string // "<", ">" o "="
	}{
		{"archivo2.txt", "archivo10.txt", "<"},
		{"IMG_0653", "IMG_7484", "<"},
		// «007» y «7» valen lo mismo COMO NÚMERO, pero son nombres distintos y
		// no pueden empatar: un comparador que devuelve 0 para dos entradas
		// diferentes deja el orden a merced de cómo las leyó el disco. El
		// desempate es por el texto tal cual, que es determinista.
		{"007_x", "7_x", "<"},
		// Sin distinguir mayúsculas: «Fotos» y «fotos» quedan juntos en vez de
		// separados por todo el bloque de minúsculas de la tabla ASCII.
		{"Fotos", "fotos", "<"},
		{"anexo", "Bases", "<"},
		// El más corto va primero cuando uno es prefijo del otro.
		{"nota", "notas", "<"},
	}
	for _, c := range casos {
		got := compararNatural(c.a, c.b)
		ok := (c.quiero == "<" && got < 0) ||
			(c.quiero == ">" && got > 0) ||
			(c.quiero == "=" && got == 0)
		if !ok {
			t.Errorf("compararNatural(%q, %q) = %d; se esperaba %q", c.a, c.b, got, c.quiero)
		}
	}
}

// El orden no puede depender del orden en que el disco devolvió las entradas:
// dos listados seguidos del mismo directorio deben verse iguales.
func TestElOrdenEsDeterminista(t *testing.T) {
	crear := func() []almacen.Entrada {
		return []almacen.Entrada{
			entrada(t, "b.txt", false),
			entrada(t, "a.txt", false),
			entrada(t, "carpeta", true),
		}
	}
	uno, dos := crear(), crear()
	// Se altera el orden de partida del segundo, como haría otro readdir.
	dos[0], dos[2] = dos[2], dos[0]

	ordenar(uno)
	ordenar(dos)
	if a, b := nombres(uno), nombres(dos); !iguales(a, b...) {
		t.Fatalf("el orden depende de cómo llegaron las entradas:\n%v\n%v", a, b)
	}
}
