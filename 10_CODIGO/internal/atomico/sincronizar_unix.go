//go:build unix

package atomico

import (
	"fmt"
	"os"
)

// sincronizarDir ejecuta el fsync del DIRECTORIO tras publicar el archivo con
// rename(). Es el paso 4 de ADR-0024, el que todo el mundo olvida: sin él,
// tras un corte de corriente el archivo puede estar escrito y su entrada de
// directorio no.
func sincronizarDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("abrir %q para sincronizar: %w", dir, err)
	}
	defer d.Close()
	if err := d.Sync(); err != nil {
		return fmt.Errorf("fsync del directorio %q: %w", dir, err)
	}
	return nil
}
