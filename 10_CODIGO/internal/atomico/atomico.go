// Package atomico publishes a whole file without ever leaving it half written -
// the four steps of ADR-0024.
//
// # WHY THIS PACKAGE EXISTS, AND WHY IT DID NOT BEFORE
//
// The dance is always the same: temporary file in the SAME directory, fsync of
// the contents, rename, fsync of the DIRECTORY. The fourth step is the one
// everybody forgets, and without it a power cut can leave the file written and
// its directory entry not - which is exactly the failure ADR-0024 was written
// against, on a node that had already lost power ten times in nine days.
//
// Until 2026-08-25 it lived DUPLICATED in two places: internal/seguridad
// (shared by its four histories) and internal/metricas (written by hand inside
// guardar). The comment in seguridad/anillo.go recorded why that was tolerated
// and what would move it out:
//
//	"unifying it would force a common package whose only content would be this
//	function, and that package does not exist yet because a third copy is not
//	enough to justify it."
//
// internal/aviso is the THIRD caller. It is extracted now, before writing that
// third copy and not after: with three, fixing one step in one would leave the
// other two broken and nobody would find out until the next outage.
//
// # WHAT THIS PACKAGE DOES NOT DO
//
// It knows no format. The caller writes whatever it likes to the io.Writer it
// receives - JSON Lines, colon-separated text, anything - and this package only
// ensures that content appears whole or does not appear. It is the only way it
// can serve four different histories without knowing any of them.
package atomico

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Escribir publica el contenido que produzca «escribir» en la ruta indicada.
//
// El patrón temporal se pide a quien llama —«.seguridad-*», «.uso-disco-*»—
// para que un temporal huérfano tras un fallo diga de quién era. os.CreateTemp
// lo crea en el MISMO directorio que el destino, que es lo que hace que el
// rename sea atómico: cruzar sistemas de archivos no lo es.
//
// Permisos 0600: estos archivos llevan direcciones de origen, medidas por
// cuenta y marcas de qué se ha avisado. No son asunto de nadie más que del
// servicio.
func Escribir(ruta, patronTmp string, escribir func(io.Writer) error) error {
	dir := filepath.Dir(ruta)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("preparar %q: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, patronTmp)
	if err != nil {
		return fmt.Errorf("crear el temporal de %q: %w", ruta, err)
	}
	nombreTmp := tmp.Name()
	defer os.Remove(nombreTmp) // no-op si el rename salió bien

	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("permisos de %q: %w", ruta, err)
	}

	w := bufio.NewWriter(tmp)
	if err := escribir(w); err != nil {
		tmp.Close()
		return fmt.Errorf("serializar %q: %w", ruta, err)
	}
	if err := w.Flush(); err != nil {
		tmp.Close()
		return fmt.Errorf("escribir %q: %w", ruta, err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sincronizar %q: %w", ruta, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("cerrar el temporal de %q: %w", ruta, err)
	}
	if err := os.Rename(nombreTmp, ruta); err != nil {
		return fmt.Errorf("publicar %q: %w", ruta, err)
	}
	return sincronizarDir(dir)
}
