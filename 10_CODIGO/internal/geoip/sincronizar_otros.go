//go:build !unix

package geoip

// En sistemas no-Unix no existe el fsync de un directorio: Windows devuelve
// «acceso denegado» al intentar FlushFileBuffers sobre un manejador de
// directorio.
//
// ESTO SOLO EXISTE PARA PODER PREPARAR Y PROBAR LA BASE EN EL HOST (D-06),
// igual que sus gemelos de fsposix, autenticacion, metricas y seguridad. El
// objetivo de despliegue es linux/arm64, donde siempre se usa la
// implementación real.
func sincronizarDir(string) error { return nil }
