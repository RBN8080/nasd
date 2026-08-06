//go:build unix

package autenticacion

import (
	"fmt"
	"os"
)

// sincronizarDir ejecuta el fsync del DIRECTORIO tras publicar el registro
// con rename(). Es el paso 4 de ADR-0024, el que todo el mundo olvida.
//
// Sin él, tras un corte de corriente el archivo puede estar escrito y su
// entrada de directorio no — y aquí eso significa que nadie puede entrar.
// El nodo ya perdió la corriente una vez (05/08/2026).
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
