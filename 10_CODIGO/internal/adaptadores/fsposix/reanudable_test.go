package fsposix

import (
	"context"
	"errors"
	"io"
	"testing"

	"nasd/internal/almacen"
)

// Cierra la divergencia con ADR-0027 encontrada el 2026-07-31.
func TestSubidaSobreviveAlReinicioDelServicio(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	a1, err := AbrirVolumen(dir)
	if err != nil {
		t.Fatalf("AbrirVolumen: %v", err)
	}

	parcial, e, err := a1.CrearReanudable(ctx, ruta(t, "video.mp4"), 10)
	if err != nil {
		t.Fatalf("CrearReanudable: %v", err)
	}
	if _, err := io.WriteString(e, "12345"); err != nil { // 5 de 10 bytes
		t.Fatalf("Write: %v", err)
	}
	id := parcial.ID

	// «Se reinicia el servicio»: el registro en memoria se pierde por
	// completo. Lo único que queda es lo que haya en disco.
	a1.Close()

	a2, err := AbrirVolumen(dir)
	if err != nil {
		t.Fatalf("reabrir el volumen: %v", err)
	}
	defer a2.Close()

	ps, err := a2.Reanudables(ctx)
	if err != nil {
		t.Fatalf("Reanudables: %v", err)
	}
	if len(ps) != 1 {
		t.Fatalf("se esperaba 1 subida a medias; hay %d", len(ps))
	}
	if ps[0].Ruta.Rel() != "video.mp4" {
		t.Errorf("destino recuperado = %q; se esperaba video.mp4", ps[0].Ruta.Rel())
	}
	if ps[0].Escrito != 5 {
		t.Errorf("desplazamiento = %d; se esperaba 5", ps[0].Escrito)
	}
	if ps[0].Total != 10 {
		t.Errorf("total = %d; se esperaba 10", ps[0].Total)
	}

	// Y se continúa desde donde iba.
	_, e2, err := a2.ReabrirParcial(ctx, id)
	if err != nil {
		t.Fatalf("ReabrirParcial: %v", err)
	}
	if e2.Escrito() != 5 {
		t.Fatalf("Escrito() tras reabrir = %d; se esperaba 5", e2.Escrito())
	}
	if _, err := io.WriteString(e2, "67890"); err != nil {
		t.Fatalf("Write tras reabrir: %v", err)
	}
	if err := e2.Confirmar(); err != nil {
		t.Fatalf("Confirmar: %v", err)
	}

	// El archivo publicado debe tener las DOS mitades, en orden.
	lector, ent, err := a2.Abrir(ctx, ruta(t, "video.mp4"))
	if err != nil {
		t.Fatalf("Abrir el publicado: %v", err)
	}
	defer lector.Close()
	b, _ := io.ReadAll(lector)
	if string(b) != "1234567890" {
		t.Errorf("contenido = %q; se esperaba 1234567890", b)
	}
	if ent.Tamano != 10 {
		t.Errorf("tamaño = %d; se esperaba 10", ent.Tamano)
	}

	// Y no debe quedar rastro: ni .part ni .meta.
	if n := contarParciales(t, a2); n != 0 {
		t.Errorf("quedaron %d restos en parciales/; se esperaban 0", n)
	}
	if ps, _ := a2.Reanudables(ctx); len(ps) != 0 {
		t.Errorf("Reanudables sigue devolviendo %d tras confirmar", len(ps))
	}
}

// Descartar también debe llevarse el .meta por delante.
func TestDescartarRetiraElMeta(t *testing.T) {
	a := nuevoVolumen(t)
	ctx := context.Background()

	_, e, err := a.CrearReanudable(ctx, ruta(t, "x.bin"), 100)
	if err != nil {
		t.Fatalf("CrearReanudable: %v", err)
	}
	io.WriteString(e, "algo")
	if err := e.Descartar(); err != nil {
		t.Fatalf("Descartar: %v", err)
	}
	if n := contarParciales(t, a); n != 0 {
		t.Errorf("quedaron %d restos tras Descartar", n)
	}
}

// El identificador viene del cliente: no puede servir para salirse de
// estado/parciales/ (04_SEGURIDAD.md §2).
func TestReabrirRechazaIdentificadoresInventados(t *testing.T) {
	a := nuevoVolumen(t)
	ctx := context.Background()
	malos := []string{
		"../../etc/passwd",
		"..",
		"",
		"a",
		"ZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZ",  // no hexadecimal
		"0123456789abcdef0123456789abcdeff", // 33 caracteres
	}
	for _, m := range malos {
		if _, _, err := a.ReabrirParcial(ctx, m); err == nil {
			t.Errorf("ReabrirParcial(%q) debía fallar", m)
		}
	}
}

// RF-23 se comprueba al CREAR, no tras transferir 5 GB.
func TestCrearReanudableRespetaNoSobrescribir(t *testing.T) {
	a := nuevoVolumen(t)
	ctx := context.Background()

	e, _ := a.Crear(ctx, ruta(t, "ya.bin"))
	io.WriteString(e, "original")
	if err := e.Confirmar(); err != nil {
		t.Fatalf("Confirmar: %v", err)
	}
	if _, _, err := a.CrearReanudable(ctx, ruta(t, "ya.bin"), 999); !errors.Is(err, almacen.ErrYaExiste) {
		t.Fatalf("se esperaba ErrYaExiste; llegó %v", err)
	}
}
