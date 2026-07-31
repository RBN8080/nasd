package fsposix

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nasd/internal/almacen"
)

func nuevoVolumen(t *testing.T) *Almacen {
	t.Helper()
	a, err := AbrirVolumen(t.TempDir())
	if err != nil {
		t.Fatalf("AbrirVolumen: %v", err)
	}
	t.Cleanup(func() { a.Close() })
	return a
}

func ruta(t *testing.T, s string) almacen.RutaSegura {
	t.Helper()
	r, err := almacen.NuevaRuta(s)
	if err != nil {
		t.Fatalf("NuevaRuta(%q): %v", s, err)
	}
	return r
}

// RNF-08: o el archivo existe completo, o no existe.
func TestEscrituraAtomicaPublica(t *testing.T) {
	a := nuevoVolumen(t)
	ctx := context.Background()

	e, err := a.Crear(ctx, ruta(t, "foto.jpg"))
	if err != nil {
		t.Fatalf("Crear: %v", err)
	}
	if _, err := io.WriteString(e, "contenido"); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Antes de Confirmar, en datos/ no hay NADA. Es RNF-05: no puede existir
	// un archivo incompleto indistinguible de uno correcto.
	if _, err := a.Estado(ctx, ruta(t, "foto.jpg")); !errors.Is(err, almacen.ErrNoExiste) {
		t.Fatalf("antes de Confirmar el destino no debía existir; err = %v", err)
	}

	if err := e.Confirmar(); err != nil {
		t.Fatalf("Confirmar: %v", err)
	}
	ent, err := a.Estado(ctx, ruta(t, "foto.jpg"))
	if err != nil {
		t.Fatalf("Estado tras confirmar: %v", err)
	}
	if ent.Tamano != int64(len("contenido")) {
		t.Errorf("tamaño = %d; se esperaba %d", ent.Tamano, len("contenido"))
	}
}

// RNF-05: una subida interrumpida no deja rastro en datos/.
func TestDescartarNoDejaRastro(t *testing.T) {
	a := nuevoVolumen(t)
	ctx := context.Background()

	e, err := a.Crear(ctx, ruta(t, "a medias.bin"))
	if err != nil {
		t.Fatalf("Crear: %v", err)
	}
	io.WriteString(e, "mitad")
	if err := e.Descartar(); err != nil {
		t.Fatalf("Descartar: %v", err)
	}
	if _, err := a.Estado(ctx, ruta(t, "a medias.bin")); !errors.Is(err, almacen.ErrNoExiste) {
		t.Fatalf("no debía quedar nada en datos/; err = %v", err)
	}
	// Y tampoco basura en estado/parciales/ (ADR-0029).
	n := contarParciales(t, a)
	if n != 0 {
		t.Errorf("quedaron %d parciales tras Descartar; se esperaban 0", n)
	}
}

// RF-23: no se sobrescribe sin decisión explícita del usuario.
func TestNoSobrescribe(t *testing.T) {
	a := nuevoVolumen(t)
	ctx := context.Background()

	e, _ := a.Crear(ctx, ruta(t, "foto.jpg"))
	io.WriteString(e, "original")
	if err := e.Confirmar(); err != nil {
		t.Fatalf("Confirmar: %v", err)
	}

	if _, err := a.Crear(ctx, ruta(t, "foto.jpg")); !errors.Is(err, almacen.ErrYaExiste) {
		t.Fatalf("Crear sobre un destino existente debía dar ErrYaExiste; dio %v", err)
	}

	// El contenido original sigue intacto.
	lector, _, err := a.Abrir(ctx, ruta(t, "foto.jpg"))
	if err != nil {
		t.Fatalf("Abrir: %v", err)
	}
	defer lector.Close()
	b, _ := io.ReadAll(lector)
	if string(b) != "original" {
		t.Errorf("contenido = %q; se esperaba %q", b, "original")
	}
}

// ADR-0028, capa 2: si el destino aparece por SMB MIENTRAS subimos, tampoco
// se sobrescribe. La comprobación se repite justo antes de publicar.
func TestNoSobrescribeSiApareceDurante(t *testing.T) {
	a := nuevoVolumen(t)
	ctx := context.Background()

	e, err := a.Crear(ctx, ruta(t, "carrera.jpg"))
	if err != nil {
		t.Fatalf("Crear: %v", err)
	}
	io.WriteString(e, "de la web")

	// Simula a Samba creando el mismo archivo por debajo, sin pasar por el
	// programa: exactamente RES-10.
	porSMB := filepath.Join(a.raiz.Name(), subDatos, "carrera.jpg")
	if err := os.WriteFile(porSMB, []byte("por SMB"), 0o600); err != nil {
		t.Fatalf("simular SMB: %v", err)
	}

	if err := e.Confirmar(); !errors.Is(err, almacen.ErrYaExiste) {
		t.Fatalf("Confirmar debía dar ErrYaExiste; dio %v", err)
	}
	b, _ := os.ReadFile(porSMB)
	if string(b) != "por SMB" {
		t.Errorf("se pisó el archivo escrito por la otra vía: %q", b)
	}
	if n := contarParciales(t, a); n != 0 {
		t.Errorf("quedaron %d parciales; se esperaban 0", n)
	}
}

func TestListarYCrearDirectorio(t *testing.T) {
	a := nuevoVolumen(t)
	ctx := context.Background()

	if err := a.CrearDirectorio(ctx, ruta(t, "fotos")); err != nil {
		t.Fatalf("CrearDirectorio: %v", err)
	}
	e, _ := a.Crear(ctx, ruta(t, "fotos/x.jpg"))
	io.WriteString(e, "x")
	if err := e.Confirmar(); err != nil {
		t.Fatalf("Confirmar: %v", err)
	}

	var nombres []string
	for ent, err := range a.Listar(ctx, ruta(t, "fotos")) {
		if err != nil {
			t.Fatalf("Listar: %v", err)
		}
		nombres = append(nombres, ent.Nombre)
	}
	if strings.Join(nombres, ",") != "x.jpg" {
		t.Errorf("listado = %v; se esperaba [x.jpg]", nombres)
	}
}

// RNF-06, capa 2: el adaptador jamás debe poder salir del volumen.
// almacen.RutaSegura ya lo impide (capa 1), pero os.Root es la red de abajo.
func TestNoSeSaleDelVolumen(t *testing.T) {
	a := nuevoVolumen(t)
	ctx := context.Background()
	// Se construye a mano una ruta que la capa 1 nunca produciría, para
	// comprobar que os.Root la rechaza igualmente.
	if _, err := a.raiz.Open("../../etc/passwd"); err == nil {
		t.Fatal("os.Root permitió salir del volumen")
	}
	if _, err := a.Estado(ctx, almacen.Raiz()); err != nil {
		t.Fatalf("la raíz debía existir: %v", err)
	}
}

func contarParciales(t *testing.T, a *Almacen) int {
	t.Helper()
	d, err := a.raiz.Open(subParciales)
	if err != nil {
		t.Fatalf("abrir parciales: %v", err)
	}
	defer d.Close()
	e, err := d.ReadDir(-1)
	if err != nil {
		t.Fatalf("leer parciales: %v", err)
	}
	return len(e)
}
