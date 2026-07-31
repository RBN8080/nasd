package web

import (
	"context"
	"io"
	"iter"

	"nasd/internal/almacen"
)

// almacenVacio implementa el puerto sin tocar disco. Las pruebas de sesión
// comprueban el CONTROL DE ACCESO: lo que importa es el código de estado
// antes de llegar al almacén, no lo que el almacén haría después.
type almacenVacio struct{}

func (almacenVacio) Listar(context.Context, almacen.RutaSegura) iter.Seq2[almacen.Entrada, error] {
	return func(yield func(almacen.Entrada, error) bool) {}
}

func (almacenVacio) Abrir(context.Context, almacen.RutaSegura) (io.ReadSeekCloser, almacen.Entrada, error) {
	return nil, almacen.Entrada{}, almacen.ErrNoExiste
}

func (almacenVacio) Crear(context.Context, almacen.RutaSegura) (almacen.EscrituraAtomica, error) {
	return nil, almacen.ErrNoExiste
}

func (almacenVacio) CrearDirectorio(context.Context, almacen.RutaSegura) error {
	return almacen.ErrNoExiste
}

func (almacenVacio) CrearReanudable(context.Context, almacen.RutaSegura, int64) (almacen.Parcial, almacen.EscrituraAtomica, error) {
	return almacen.Parcial{}, nil, almacen.ErrNoExiste
}

func (almacenVacio) Reanudables(context.Context) ([]almacen.Parcial, error) { return nil, nil }

func (almacenVacio) ReabrirParcial(context.Context, string) (almacen.Parcial, almacen.EscrituraAtomica, error) {
	return almacen.Parcial{}, nil, almacen.ErrNoExiste
}

func (almacenVacio) BorrarParcial(context.Context, string) error { return nil }

func (almacenVacio) Renombrar(context.Context, almacen.RutaSegura, almacen.RutaSegura) error {
	return almacen.ErrNoExiste
}

func (almacenVacio) Borrar(context.Context, almacen.RutaSegura) error { return almacen.ErrNoExiste }

func (almacenVacio) BorrarArbol(context.Context, almacen.RutaSegura) error {
	return almacen.ErrNoExiste
}

func (almacenVacio) Resumen(context.Context, almacen.RutaSegura) (almacen.Conteo, error) {
	return almacen.Conteo{}, almacen.ErrNoExiste
}
