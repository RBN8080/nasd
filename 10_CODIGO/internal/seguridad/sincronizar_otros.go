//go:build !unix

package seguridad

// En sistemas no-Unix no existe el fsync de un directorio: Windows devuelve
// «acceso denegado» al intentar FlushFileBuffers sobre un manejador de
// directorio.
//
// ESTO SOLO EXISTE PARA PODER DESARROLLAR Y PROBAR EN EL HOST (D-06), igual
// que sus gemelos de fsposix, autenticacion y metricas. El objetivo de
// despliegue es linux/arm64, donde siempre se usa la implementación real.
//
// Consecuencia: ejecutando en Windows, la durabilidad del historial NO está
// garantizada. Las pruebas valen como prueba de lógica, no de durabilidad.
func sincronizarDir(string) error { return nil }
