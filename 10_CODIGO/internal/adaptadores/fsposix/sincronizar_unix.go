//go:build unix

package fsposix

import (
	"fmt"
	"os"
)

// sincronizarDirectorio ejecuta el paso 4 de ADR-0024: el fsync del
// DIRECTORIO destino, que es el que todo el mundo olvida.
//
// rename() es atómico frente a otros observadores, pero eso no es durabilidad.
// Sin este fsync, tras un corte de corriente el archivo puede estar escrito y
// su entrada de directorio no, con lo que el archivo simplemente no aparece.
//
// Ese modo de fallo degrada al lado seguro —RNF-05 se cumpliría igual—, pero
// si vamos a responder 200 hay que haber cumplido la promesa: con copia única
// (D-12) ese contrato es lo único que hay.
func sincronizarDirectorio(raiz *os.Root, dir string) error {
	d, err := raiz.Open(dir)
	if err != nil {
		return fmt.Errorf("abrir el directorio para sincronizar: %w", err)
	}
	defer d.Close()
	if err := d.Sync(); err != nil {
		return fmt.Errorf("fsync del directorio %q: %w", dir, err)
	}
	return nil
}
