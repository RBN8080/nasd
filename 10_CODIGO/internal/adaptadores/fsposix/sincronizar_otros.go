//go:build !unix

package fsposix

import "os"

// En sistemas no-Unix no existe el fsync de un directorio: Windows devuelve
// «acceso denegado» al intentar FlushFileBuffers sobre un manejador de
// directorio.
//
// ESTO SOLO EXISTE PARA PODER DESARROLLAR Y PROBAR EN EL HOST (D-06).
// El objetivo de despliegue es linux/arm64, donde SIEMPRE se usa la
// implementación real de sincronizar_unix.go.
//
// Consecuencia que hay que tener presente: ejecutando en Windows, la garantía
// de durabilidad de RNF-08 NO se cumple. Las pruebas de atomicidad valen como
// prueba de lógica, no de durabilidad; esa se mide en el nodo cortando la
// corriente de verdad (supuesto S-01 de 03_ESTUDIO_TECNICO.md §10).
func sincronizarDirectorio(_ *os.Root, _ string) error {
	return nil
}
