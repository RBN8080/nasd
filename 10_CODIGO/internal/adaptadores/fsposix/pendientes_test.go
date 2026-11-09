package fsposix

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"nasd/internal/almacen"
)

// estado/parciales/ es uno para todas las cuentas. El .meta anota de quién es
// cada subida: cada cuenta ve las suyas y el superusuario, todas.
func TestCadaCuentaVeSoloSusSubidasAMedias(t *testing.T) {
	raiz := nuevoVolumen(t)
	ctx := context.Background()
	juan, err := raiz.ParaUsuario("juan")
	if err != nil {
		t.Fatal(err)
	}
	ana, err := raiz.ParaUsuario("ana")
	if err != nil {
		t.Fatal(err)
	}

	p, e, err := juan.CrearReanudable(ctx, ruta(t, "foto.jpg"), 10)
	if err != nil {
		t.Fatalf("CrearReanudable: %v", err)
	}
	io.WriteString(e, "123")
	e.Soltar()
	if _, e2, err := raiz.CrearReanudable(ctx, ruta(t, "propia.txt"), 5); err != nil {
		t.Fatalf("CrearReanudable del superusuario: %v", err)
	} else {
		e2.Soltar()
	}

	if ps, _ := juan.Reanudables(ctx); len(ps) != 1 || ps[0].ID != p.ID || ps[0].Cuenta != "juan" {
		t.Errorf("juan ve %+v; se esperaba solo su foto.jpg, de «juan»", ps)
	}
	if ps, _ := ana.Reanudables(ctx); len(ps) != 0 {
		t.Errorf("ana ve %d subidas ajenas", len(ps))
	}
	ps, _ := raiz.Reanudables(ctx)
	cuentas := map[string]bool{}
	for _, x := range ps {
		cuentas[x.Cuenta] = true
	}
	if len(ps) != 2 || !cuentas[""] || !cuentas["juan"] {
		t.Errorf("el superusuario ve %+v; se esperaban las dos, con su dueño", ps)
	}

	// Con el identificador en la mano tampoco: para ana no existe.
	if _, _, err := ana.ReabrirParcial(ctx, p.ID); !errors.Is(err, almacen.ErrNoExiste) {
		t.Errorf("ana reabrió la subida de juan: %v", err)
	}
}

// Si el superusuario termina la subida de otra cuenta, el archivo cae en la
// carpeta de esa cuenta y no en la raíz.
func TestLaSubidaAjenaSePublicaEnSuCarpeta(t *testing.T) {
	raiz := nuevoVolumen(t)
	ctx := context.Background()
	juan, _ := raiz.ParaUsuario("juan")

	p, e, err := juan.CrearReanudable(ctx, ruta(t, "nota.txt"), 4)
	if err != nil {
		t.Fatal(err)
	}
	io.WriteString(e, "ab")
	e.Soltar()

	_, e2, err := raiz.ReabrirParcial(ctx, p.ID)
	if err != nil {
		t.Fatalf("el superusuario no pudo reabrir: %v", err)
	}
	io.WriteString(e2, "cd")
	if err := e2.Confirmar(); err != nil {
		t.Fatalf("Confirmar: %v", err)
	}
	if !existe(t, juan, "nota.txt") {
		t.Error("no está en la carpeta de juan")
	}
	if existe(t, raiz, "nota.txt") {
		t.Error("se publicó en la raíz del superusuario")
	}
}

// Los .meta de antes de v1.135.0 no llevan dueño: son del superusuario, que
// es lo que eran en el nodo (los dos «PORTAPAPELES.txt» de 0 B).
func TestUnMetaSinDuenoEsDelSuperusuario(t *testing.T) {
	raiz := nuevoVolumen(t)
	ctx := context.Background()
	juan, _ := raiz.ParaUsuario("juan")
	id := "bc94146917c49f9c30dfa0a6bde87d1d"
	dir := filepath.Join(raiz.raiz.Name(), subParciales)
	if err := os.WriteFile(filepath.Join(dir, id+".meta"), []byte(`{"ruta":"PORTAPAPELES.txt","total":0}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, id+".part"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	if ps, _ := raiz.Reanudables(ctx); len(ps) != 1 || ps[0].Cuenta != "" {
		t.Fatalf("el superusuario ve %+v; se esperaba la suya", ps)
	}
	if ps, _ := juan.Reanudables(ctx); len(ps) != 0 {
		t.Errorf("juan ve una subida que no es suya")
	}
	_, e, err := raiz.ReabrirParcial(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Confirmar(); err != nil {
		t.Fatalf("Confirmar: %v", err)
	}
	if !existe(t, raiz, "PORTAPAPELES.txt") {
		t.Error("no se publicó")
	}
	if n := contarParciales(t, raiz); n != 0 {
		t.Errorf("quedaron %d restos en parciales/", n)
	}
}

// Si el destino aparece mientras se sube (RF-23), el .part se va y el .meta
// tiene que irse con él: sin .part, Reanudables no lo ve y la expiración
// tampoco, así que se quedaba para siempre.
func TestDestinoOcupadoNoDejaElMetaHuerfano(t *testing.T) {
	a := nuevoVolumen(t)
	ctx := context.Background()
	_, e, err := a.CrearReanudable(ctx, ruta(t, "x.txt"), 3)
	if err != nil {
		t.Fatal(err)
	}
	io.WriteString(e, "abc")
	if err := os.WriteFile(filepath.Join(a.raiz.Name(), subDatos, "x.txt"), []byte("smb"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := e.Confirmar(); !errors.Is(err, almacen.ErrYaExiste) {
		t.Fatalf("Confirmar debía dar ErrYaExiste; dio %v", err)
	}
	if n := contarParciales(t, a); n != 0 {
		t.Errorf("quedaron %d restos en parciales/; se esperaba ninguno", n)
	}
}
