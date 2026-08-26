package web

import (
	"strings"
	"time"

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

// ——— P-3: ordenar por las otras dos columnas (06/08/2026) ———

// conDatos añade tamaño y fecha a una entrada, que es lo que las dos columnas
// nuevas comparan.
func conDatos(t *testing.T, nombre string, dir bool, tamano int64, dias int) almacen.Entrada {
	t.Helper()
	e := entrada(t, nombre, dir)
	e.Tamano = tamano
	e.Modificado = time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC).AddDate(0, 0, -dias)
	return e
}

// «Tamaño: de mayor a menor», y las carpetas siguen arriba.
func TestPorTamanoVaDeMayorAMenor(t *testing.T) {
	es := []almacen.Entrada{
		conDatos(t, "pequeno.txt", false, 10, 0),
		conDatos(t, "enorme.mov", false, 9_000_000, 0),
		conDatos(t, "Videos", true, 4096, 0),
		conDatos(t, "medio.jpg", false, 5_000, 0),
	}
	ordenarPor(es, porTamano)

	if got := nombres(es); !iguales(got, "Videos", "enorme.mov", "medio.jpg", "pequeno.txt") {
		t.Fatalf("no ordenó de mayor a menor con las carpetas arriba:\n%v", got)
	}
}

// LAS CARPETAS NO SE ORDENAN POR TAMAÑO, y es deliberado: la tabla no muestra
// ninguno para ellas, así que ordenarlas por un número invisible se vería como
// un orden aleatorio. Se quedan por nombre.
func TestLasCarpetasNoSeOrdenanPorUnTamanoQueNoSeVe(t *testing.T) {
	es := []almacen.Entrada{
		conDatos(t, "Zeta", true, 90_000, 0),
		conDatos(t, "Alfa", true, 10, 0),
		conDatos(t, "archivo.bin", false, 5, 0),
	}
	ordenarPor(es, porTamano)

	if got := nombres(es); !iguales(got, "Alfa", "Zeta", "archivo.bin") {
		t.Fatalf("las carpetas se ordenaron por un tamaño que la tabla no enseña:\n%v", got)
	}
}

// «Modificado: de más reciente arriba a más antiguo abajo».
func TestPorModificadoPoneLoRecienteArriba(t *testing.T) {
	es := []almacen.Entrada{
		conDatos(t, "viejo.txt", false, 1, 30),
		conDatos(t, "hoy.txt", false, 1, 0),
		conDatos(t, "semana.txt", false, 1, 7),
	}
	ordenarPor(es, porModificado)

	if got := nombres(es); !iguales(got, "hoy.txt", "semana.txt", "viejo.txt") {
		t.Fatalf("no puso lo más reciente arriba:\n%v", got)
	}
}

// Dos archivos del mismo tamaño no pueden bailar entre recargas: el desempate
// es por nombre, no el orden en que los devolvió el disco.
func TestElEmpateDeTamanoSeRompeSiempreIgual(t *testing.T) {
	uno := []almacen.Entrada{
		conDatos(t, "b.bin", false, 100, 0),
		conDatos(t, "a.bin", false, 100, 0),
	}
	dos := []almacen.Entrada{
		conDatos(t, "a.bin", false, 100, 0),
		conDatos(t, "b.bin", false, 100, 0),
	}
	ordenarPor(uno, porTamano)
	ordenarPor(dos, porTamano)

	if a, b := nombres(uno), nombres(dos); !iguales(a, b...) {
		t.Fatalf("el empate depende de cómo llegaron las entradas:\n%v\n%v", a, b)
	}
}

// Un parámetro escrito a mano no debe dejar el listado en un estado que la
// interfaz no sabe dibujar: cae al orden de siempre.
func TestUnCriterioDesconocidoCaeEnElOrdenPorNombre(t *testing.T) {
	for _, s := range []string{"", "tamaño", "fecha", "../../etc", "NOMBRE"} {
		if got := criterioDe(s); got != porNombre {
			t.Errorf("criterioDe(%q) = %q; se esperaba el orden por nombre", s, got)
		}
	}
}

// El responsable pidió que ordenar valga «también dentro de todas las
// subcarpetas». Como el orden vive en la URL y no en el servidor (regla R1,
// ADR-0015), la única forma de que sobreviva a un clic es que CADA enlace de
// navegación lo lleve puesto: filas de carpeta, migas y «subir».
func TestElOrdenSobreviveAlEntrarEnUnaSubcarpeta(t *testing.T) {
	s, a := servidorDeMover(t)
	a.agregar(t, "Videos", true)
	a.agregar(t, "notas.txt", false)

	cuerpo := peticionConSesion(t, s, "/?orden=tamano").Body.String()

	if !strings.Contains(cuerpo, `href="/ver/Videos?orden=tamano"`) {
		t.Errorf("entrar en una subcarpeta pierde el orden elegido:\n%s", cuerpo)
	}
	// Y el encabezado activo se marca, para saber qué se está viendo.
	if !strings.Contains(cuerpo, `class="act" href="/?orden=tamano"`) {
		t.Error("no se señala por qué columna se está ordenando")
	}
}

// El orden por nombre es el de por omisión y NO se escribe en los enlaces:
// el uso normal no tiene por qué arrastrar un parámetro redundante.
func TestElOrdenPorNombreNoEnsuciaLosEnlaces(t *testing.T) {
	s, a := servidorDeMover(t)
	a.agregar(t, "Videos", true)

	cuerpo := peticionConSesion(t, s, "/").Body.String()

	if strings.Contains(cuerpo, "?orden=nombre") {
		t.Error("arrastra «?orden=nombre», que es lo mismo que no poner nada")
	}
	if !strings.Contains(cuerpo, `href="/ver/Videos"`) {
		t.Errorf("el enlace normal de carpeta cambió:\n%s", cuerpo)
	}
}
