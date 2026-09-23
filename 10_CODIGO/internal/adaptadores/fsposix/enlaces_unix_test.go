//go:build unix

package fsposix

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"nasd/internal/almacen"
)

// RNF-06, capa 2
func TestEnlaceFueraDelVolumenNoSeSirve(t *testing.T) {
	a := nuevoVolumen(t)
	ctx := context.Background()

	destinoFuera := filepath.Join(t.TempDir(), "secreto.txt")
	if err := os.WriteFile(destinoFuera, []byte("SECRETO"), 0o600); err != nil {
		t.Fatalf("preparar el objetivo: %v", err)
	}
	enlace := filepath.Join(a.raiz.Name(), subDatos, "fuga")
	if err := os.Symlink(destinoFuera, enlace); err != nil {
		t.Fatalf("crear el enlace: %v", err)
	}

	// La capa 1 acepta la ruta: no tiene nada sospechoso.
	r, err := almacen.NuevaRuta("fuga")
	if err != nil {
		t.Fatalf("la capa 1 rechazó una ruta legítima: %v", err)
	}

	_, _, err = a.Abrir(ctx, r)
	if err == nil {
		t.Fatal("FUGA: se abrió un enlace que apunta fuera del volumen")
	}
	if !errors.Is(err, almacen.ErrEnlaceExterno) {
		t.Errorf("error = %v; se esperaba ErrEnlaceExterno para que el "+
			"adaptador HTTP responda 404 y no 500", err)
	}
}

// Un enlace que apunta DENTRO del volumen es legítimo y debe funcionar.
func TestEnlaceDentroDelVolumenSiSeSirve(t *testing.T) {
	a := nuevoVolumen(t)
	ctx := context.Background()

	e, _ := a.Crear(ctx, ruta(t, "real.txt"))
	e.Write([]byte("contenido"))
	if err := e.Confirmar(); err != nil {
		t.Fatalf("Confirmar: %v", err)
	}
	if err := os.Symlink("real.txt", filepath.Join(a.raiz.Name(), subDatos, "atajo")); err != nil {
		t.Fatalf("crear el enlace: %v", err)
	}

	lector, _, err := a.Abrir(ctx, ruta(t, "atajo"))
	if err != nil {
		t.Fatalf("un enlace interno debía poder abrirse: %v", err)
	}
	lector.Close()
}
