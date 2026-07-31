package almacen

import "testing"

// TestNuevaRutaRechaza es la prueba de RNF-06 / CWE-22 exigida por
// 04_SEGURIDAD.md §2. Si alguna de estas entradas produce una RutaSegura,
// el servidor de archivos tiene la vulnerabilidad número uno de su clase.
func TestNuevaRutaRechaza(t *testing.T) {
	casos := []string{
		"../etc/passwd",
		"..",
		"a/../../b",
		"fotos/../../etc/shadow",
		"/etc/passwd",
		"//etc/passwd",
		`\etc\passwd`,
		`C:\Windows`,
		`c:/windows`,
		`\\servidor\recurso`,
		"a//b",           // componente vacío
		"a/./b",          // componente "."
		"....//etc",      // el clásico que rompe el saneo por sustitución
		"fotos/\x00.jpg", // NUL
		"fotos/\x07x",    // control
		"a/..",
		"a/../..",
	}
	for _, c := range casos {
		if r, err := NuevaRuta(c); err == nil {
			t.Errorf("NuevaRuta(%q) debía fallar; devolvió %q", c, r.Rel())
		}
	}
}

func TestNuevaRutaAcepta(t *testing.T) {
	casos := map[string]string{
		"":                     ".",
		"/":                    ".",
		".":                    ".",
		"fotos":                "fotos",
		"fotos/":               "fotos",
		"fotos/2026/enero.jpg": "fotos/2026/enero.jpg",
		"con espacio.jpg":      "con espacio.jpg",
		"acentuado ñÑ áé.png":  "acentuado ñÑ áé.png",
		"..punto":              "..punto", // empieza por .. pero NO es ".."
		"...":                  "...",
	}
	for entrada, esperada := range casos {
		r, err := NuevaRuta(entrada)
		if err != nil {
			t.Errorf("NuevaRuta(%q) falló: %v", entrada, err)
			continue
		}
		if r.Rel() != esperada {
			t.Errorf("NuevaRuta(%q) = %q; se esperaba %q", entrada, r.Rel(), esperada)
		}
	}
}

// RF-08: subir de nivel desde la raíz no puede exponer / ni /home ni /etc.
func TestPadreDeRaizEsRaiz(t *testing.T) {
	if p := Raiz().Padre(); !p.EsRaiz() {
		t.Fatalf("el padre de la raíz debe ser la raíz; fue %q", p.Rel())
	}
	r, _ := NuevaRuta("a")
	if p := r.Padre(); !p.EsRaiz() {
		t.Fatalf("el padre de \"a\" debe ser la raíz; fue %q", p.Rel())
	}
}

func TestHijaRechazaSeparadores(t *testing.T) {
	r := Raiz()
	for _, n := range []string{"a/b", "..", ".", "", `a\b`, "x\x00"} {
		if _, err := r.Hija(n); err == nil {
			t.Errorf("Hija(%q) debía fallar", n)
		}
	}
}

// CWE-434: el nombre de la parte multipart lo controla el cliente.
func TestNombreDeArchivo(t *testing.T) {
	ok := map[string]string{
		"foto.jpg":                  "foto.jpg",
		`C:\Users\usuario\foto.jpg`: "foto.jpg", // clientes de Windows
		"/tmp/foto.jpg":             "foto.jpg",
		"../../../../etc/passwd":    "passwd", // solo el último componente
		"  espacios.png  ":          "espacios.png",
	}
	for entrada, esperado := range ok {
		n, err := NombreDeArchivo(entrada)
		if err != nil {
			t.Errorf("NombreDeArchivo(%q) falló: %v", entrada, err)
			continue
		}
		if n != esperado {
			t.Errorf("NombreDeArchivo(%q) = %q; se esperaba %q", entrada, n, esperado)
		}
	}
	for _, malo := range []string{"", "..", ".", "/", `\`, "x\x00y"} {
		if _, err := NombreDeArchivo(malo); err == nil {
			t.Errorf("NombreDeArchivo(%q) debía fallar", malo)
		}
	}
}

func TestAscendencia(t *testing.T) {
	r, _ := NuevaRuta("a/b/c")
	asc := r.Ascendencia()
	quiero := []string{".", "a", "a/b", "a/b/c"}
	if len(asc) != len(quiero) {
		t.Fatalf("ascendencia = %d elementos; se esperaban %d", len(asc), len(quiero))
	}
	for i, q := range quiero {
		if asc[i].Rel() != q {
			t.Errorf("ascendencia[%d] = %q; se esperaba %q", i, asc[i].Rel(), q)
		}
	}
}
