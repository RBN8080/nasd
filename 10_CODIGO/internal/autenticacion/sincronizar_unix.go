//go:build unix

package autenticacion

import (
	"fmt"
	"os"
	"syscall"
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

// heredarDuenoDe le pone al archivo el mismo dueño que su directorio.
//
// EXISTE POR UNA TRAMPA CONCRETA DEL DESPLIEGUE, no por elegancia. Las cuentas
// se dan de alta desde la terminal con «sudo nasd --crear-usuario», o sea como
// root, pero el servicio corre como el usuario nas. Sin esto el registro
// quedaría root:root con permisos 0600 y el servicio no podría leerlo: nadie
// entraría, y el síntoma —«contraseña incorrecta» para una contraseña buena—
// no señala en absoluto hacia el dueño de un archivo.
//
// El directorio es la referencia correcta porque lo crea systemd con
// StateDirectory=, ya con el dueño del servicio.
//
// Cuando el que escribe es el propio servicio, el dueño ya coincide y esto no
// hace nada. Si no coincide y no se puede corregir, se devuelve el error: un
// registro que el servicio no puede leer es tan grave como no haberlo escrito.
func heredarDuenoDe(dir, archivo string) error {
	di, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("consultar el dueño de %q: %w", dir, err)
	}
	dst, ok := di.Sys().(*syscall.Stat_t)
	if !ok {
		return nil // sistema Unix sin Stat_t: no hay a quién copiar
	}
	ai, err := os.Stat(archivo)
	if err != nil {
		return fmt.Errorf("consultar el dueño de %q: %w", archivo, err)
	}
	ast, ok := ai.Sys().(*syscall.Stat_t)
	if ok && ast.Uid == dst.Uid && ast.Gid == dst.Gid {
		return nil
	}
	if err := os.Chown(archivo, int(dst.Uid), int(dst.Gid)); err != nil {
		return fmt.Errorf("dar a %q el dueño de %q: %w", archivo, dir, err)
	}
	return nil
}
