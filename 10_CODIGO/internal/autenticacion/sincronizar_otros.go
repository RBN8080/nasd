//go:build !unix

package autenticacion

// En sistemas no-Unix no existe el fsync de un directorio: Windows devuelve
// «acceso denegado» al intentar FlushFileBuffers sobre un manejador de
// directorio.
//
// ESTO SOLO EXISTE PARA PODER DESARROLLAR Y PROBAR EN EL HOST (D-06), igual
// que su gemelo de fsposix. El objetivo de despliegue es linux/arm64, donde
// siempre se usa la implementación real.
//
// Consecuencia: ejecutando en Windows, la durabilidad del registro NO está
// garantizada. Las pruebas valen como prueba de lógica, no de durabilidad.
func sincronizarDir(string) error { return nil }

// heredarDuenoDe no tiene equivalente: Windows no usa uid/gid, y el problema
// que resuelve —crear el registro como root y que lo lea el servicio— solo
// existe en el nodo. Ver el comentario de su gemelo, que es donde está el
// motivo entero.
func heredarDuenoDe(string, string) error { return nil }
