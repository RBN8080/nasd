// Package atomico publica un archivo entero sin dejarlo nunca a medias — los
// cuatro pasos de ADR-0024.
//
// # POR QUÉ EXISTE ESTE PAQUETE, Y POR QUÉ NO EXISTÍA ANTES
//
// La danza es siempre la misma: temporal en el MISMO directorio, fsync del
// contenido, rename, fsync del DIRECTORIO. El cuarto paso es el que todo el
// mundo olvida, y sin él un corte de corriente puede dejar el archivo escrito
// y su entrada de directorio no — que es exactamente el fallo contra el que
// ADR-0024 se escribió, en un nodo al que ya se le fue la luz diez veces en
// nueve días.
//
// Hasta el 2026-08-25 vivía DUPLICADA en dos sitios: internal/seguridad
// (compartida por sus cuatro historiales) y internal/metricas (escrita a
// mano dentro de guardar). El comentario de seguridad/anillo.go dejó dicho
// por qué se toleraba y qué la sacaría de ahí:
//
//	«unificarla obligaría a un paquete común que solo tendría esta función
//	dentro, y ese paquete no existe todavía porque una tercera copia no basta
//	para justificarlo.»
//
// internal/aviso es el TERCER llamador. Se extrae ahora, antes de escribir la
// tercera copia y no después: con tres, arreglar un paso en una dejaría las
// otras dos rotas y nadie se enteraría hasta el siguiente apagón.
//
// # LO QUE ESTE PAQUETE NO HACE
//
// No conoce ningún formato. Quien llama escribe lo que quiera en el io.Writer
// que recibe —JSON Lines, texto con dos puntos, lo que sea— y este paquete
// solo se ocupa de que ese contenido aparezca entero o no aparezca. Es la
// única forma de que sirva a cuatro historiales distintos sin saber de
// ninguno.
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
