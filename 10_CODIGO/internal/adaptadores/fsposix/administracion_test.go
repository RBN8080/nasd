package fsposix

import (
	"context"
	"errors"
	"io"
	"testing"

	"nasd/internal/almacen"
)

func crear(t *testing.T, a *Almacen, p, contenido string) {
	t.Helper()
	e, err := a.Crear(context.Background(), ruta(t, p))
	if err != nil {
		t.Fatalf("Crear %q: %v", p, err)
	}
	io.WriteString(e, contenido)
	if err := e.Confirmar(); err != nil {
		t.Fatalf("Confirmar %q: %v", p, err)
	}
}

func existe(t *testing.T, a *Almacen, p string) bool {
	t.Helper()
	_, err := a.Estado(context.Background(), ruta(t, p))
	return err == nil
}

// RF-16: renombrar dentro de la misma carpeta.
func TestRenombrar(t *testing.T) {
	a := nuevoVolumen(t)
	ctx := context.Background()
	crear(t, a, "viejo.txt", "contenido")

	if err := a.Renombrar(ctx, ruta(t, "viejo.txt"), ruta(t, "nuevo.txt")); err != nil {
		t.Fatalf("Renombrar: %v", err)
	}
	if existe(t, a, "viejo.txt") {
		t.Error("el original sigue ahí")
	}
	if !existe(t, a, "nuevo.txt") {
		t.Fatal("el renombrado no aparece")
	}
	lector, _, _ := a.Abrir(ctx, ruta(t, "nuevo.txt"))
	defer lector.Close()
	b, _ := io.ReadAll(lector)
	if string(b) != "contenido" {
		t.Errorf("contenido = %q; renombrar no debe alterar los datos", b)
	}
}

// RF-17: mover entre carpetas es la misma operación.
func TestMoverEntreCarpetas(t *testing.T) {
	a := nuevoVolumen(t)
	ctx := context.Background()
	a.CrearDirectorio(ctx, ruta(t, "destino"))
	crear(t, a, "foto.jpg", "datos")

	if err := a.Renombrar(ctx, ruta(t, "foto.jpg"), ruta(t, "destino/foto.jpg")); err != nil {
		t.Fatalf("mover: %v", err)
	}
	if existe(t, a, "foto.jpg") || !existe(t, a, "destino/foto.jpg") {
		t.Error("el movimiento no dejó el archivo donde debía")
	}
}

// RF-23 vale también al mover: NO se sobrescribe.
func TestMoverNoSobrescribe(t *testing.T) {
	a := nuevoVolumen(t)
	ctx := context.Background()
	crear(t, a, "a.txt", "AAA")
	crear(t, a, "b.txt", "BBB")

	if err := a.Renombrar(ctx, ruta(t, "a.txt"), ruta(t, "b.txt")); !errors.Is(err, almacen.ErrYaExiste) {
		t.Fatalf("se esperaba ErrYaExiste; llegó %v", err)
	}
	lector, _, _ := a.Abrir(ctx, ruta(t, "b.txt"))
	defer lector.Close()
	b, _ := io.ReadAll(lector)
	if string(b) != "BBB" {
		t.Error("SE DESTRUYÓ el destino: mover pisó un archivo existente")
	}
	if !existe(t, a, "a.txt") {
		t.Error("el origen desapareció pese a fallar la operación")
	}
}

func TestNoMoverUnaCarpetaDentroDeSiMisma(t *testing.T) {
	a := nuevoVolumen(t)
	ctx := context.Background()
	a.CrearDirectorio(ctx, ruta(t, "padre"))
	a.CrearDirectorio(ctx, ruta(t, "padre/hijo"))

	err := a.Renombrar(ctx, ruta(t, "padre"), ruta(t, "padre/hijo/padre"))
	if !errors.Is(err, almacen.ErrDentroDeSiMismo) {
		t.Fatalf("se esperaba ErrDentroDeSiMismo; llegó %v", err)
	}
}

// La raíz de datos no se renombra ni se borra: es el propio almacén.
func TestLaRaizEsIntocable(t *testing.T) {
	a := nuevoVolumen(t)
	ctx := context.Background()
	if err := a.Borrar(ctx, almacen.Raiz()); !errors.Is(err, almacen.ErrRutaInvalida) {
		t.Errorf("Borrar la raíz: se esperaba ErrRutaInvalida, llegó %v", err)
	}
	if err := a.BorrarArbol(ctx, almacen.Raiz()); !errors.Is(err, almacen.ErrRutaInvalida) {
		t.Errorf("BorrarArbol la raíz: se esperaba ErrRutaInvalida, llegó %v", err)
	}
	if err := a.Renombrar(ctx, almacen.Raiz(), ruta(t, "x")); !errors.Is(err, almacen.ErrRutaInvalida) {
		t.Errorf("Renombrar la raíz: se esperaba ErrRutaInvalida, llegó %v", err)
	}
}

// LA PRUEBA QUE MÁS IMPORTA de este bloque.
//
// Borrar NO debe poder llevarse un árbol por delante: sin papelera (D-15) y
// con copia única (D-12), un borrado recursivo accidental es la pérdida más
// grave que puede sufrir este producto. Talar un árbol exige pedirlo por su
// nombre, con BorrarArbol.
func TestBorrarNoSeLlevaUnArbolPorDelante(t *testing.T) {
	a := nuevoVolumen(t)
	ctx := context.Background()
	a.CrearDirectorio(ctx, ruta(t, "fotos"))
	crear(t, a, "fotos/importante.jpg", "irremplazable")

	err := a.Borrar(ctx, ruta(t, "fotos"))
	if !errors.Is(err, almacen.ErrNoVacio) {
		t.Fatalf("Borrar sobre una carpeta con contenido debía dar ErrNoVacio; dio %v", err)
	}
	if !existe(t, a, "fotos/importante.jpg") {
		t.Fatal("SE DESTRUYÓ EL CONTENIDO con la operación no recursiva")
	}
}

func TestBorrarArchivoYCarpetaVacia(t *testing.T) {
	a := nuevoVolumen(t)
	ctx := context.Background()
	crear(t, a, "suelto.txt", "x")
	a.CrearDirectorio(ctx, ruta(t, "vacia"))

	if err := a.Borrar(ctx, ruta(t, "suelto.txt")); err != nil {
		t.Fatalf("Borrar archivo: %v", err)
	}
	if err := a.Borrar(ctx, ruta(t, "vacia")); err != nil {
		t.Fatalf("Borrar carpeta vacía: %v", err)
	}
	if existe(t, a, "suelto.txt") || existe(t, a, "vacia") {
		t.Error("algo sobrevivió al borrado")
	}
}

func TestBorrarArbolCompleto(t *testing.T) {
	a := nuevoVolumen(t)
	ctx := context.Background()
	a.CrearDirectorio(ctx, ruta(t, "arbol"))
	a.CrearDirectorio(ctx, ruta(t, "arbol/rama"))
	crear(t, a, "arbol/a.txt", "1")
	crear(t, a, "arbol/rama/b.txt", "22")
	crear(t, a, "fuera.txt", "no tocar")

	if err := a.BorrarArbol(ctx, ruta(t, "arbol")); err != nil {
		t.Fatalf("BorrarArbol: %v", err)
	}
	if existe(t, a, "arbol") {
		t.Error("el árbol sigue ahí")
	}
	if !existe(t, a, "fuera.txt") {
		t.Fatal("SE LLEVÓ POR DELANTE algo que estaba fuera del árbol")
	}
}

// El resumen alimenta la confirmación de RF-18: si miente, el usuario
// decide con información falsa sobre algo irreversible.
func TestResumenCuentaLoQueHay(t *testing.T) {
	a := nuevoVolumen(t)
	ctx := context.Background()
	a.CrearDirectorio(ctx, ruta(t, "c"))
	a.CrearDirectorio(ctx, ruta(t, "c/sub"))
	crear(t, a, "c/uno.txt", "12345")       // 5 bytes
	crear(t, a, "c/sub/dos.txt", "1234567") // 7 bytes

	got, err := a.Resumen(ctx, ruta(t, "c"))
	if err != nil {
		t.Fatalf("Resumen: %v", err)
	}
	if !got.EsDirectorio {
		t.Error("no marcó que es un directorio")
	}
	if got.Archivos != 2 {
		t.Errorf("archivos = %d; se esperaban 2", got.Archivos)
	}
	if got.Directorios != 1 {
		t.Errorf("directorios = %d; se esperaba 1", got.Directorios)
	}
	if got.Bytes != 12 {
		t.Errorf("bytes = %d; se esperaban 12", got.Bytes)
	}

	// Y sobre un archivo suelto.
	f, err := a.Resumen(ctx, ruta(t, "c/uno.txt"))
	if err != nil {
		t.Fatalf("Resumen de archivo: %v", err)
	}
	if f.EsDirectorio || f.Archivos != 1 || f.Bytes != 5 {
		t.Errorf("resumen de archivo incorrecto: %+v", f)
	}
}
