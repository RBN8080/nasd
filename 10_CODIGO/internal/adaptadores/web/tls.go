package web

import (
	"crypto/tls"
	"fmt"
	"os"
	"sync"
	"time"
)

// CargadorCert sirve el certificado a crypto/tls releyéndolo del disco cuando
// cambia, sin reiniciar el servicio.
//
// POR QUÉ RECARGA EN CALIENTE Y NO SE LEE UNA VEZ AL ARRANCAR:
// el certificado de Let's Encrypt dura 90 días y 15_tls.sh lo renueva por
// temporizador. Si nasd solo lo leyera al arrancar, la renovación no surtiría
// efecto hasta el siguiente reinicio, y el reinicio corta subidas en curso.
// Con esto, la renovación es invisible.
//
// La comprobación es por fecha de modificación, no por contenido: releer dos
// archivos en cada saludo TLS sería trabajo inútil en un A53.
type CargadorCert struct {
	rutaCert  string
	rutaClave string

	mu       sync.RWMutex
	cert     *tls.Certificate
	modCert  time.Time
	modClave time.Time
}

// NuevoCargadorCert lee el par por primera vez. Falla si no puede (P5): un
// servicio que promete TLS y arranca sin certificado es peor que uno que no
// arranca.
func NuevoCargadorCert(rutaCert, rutaClave string) (*CargadorCert, error) {
	c := &CargadorCert{rutaCert: rutaCert, rutaClave: rutaClave}
	if err := c.recargar(); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *CargadorCert) recargar() error {
	iCert, err := os.Stat(c.rutaCert)
	if err != nil {
		return fmt.Errorf("certificado %s: %w", c.rutaCert, err)
	}
	iClave, err := os.Stat(c.rutaClave)
	if err != nil {
		return fmt.Errorf("clave TLS %s: %w", c.rutaClave, err)
	}

	par, err := tls.LoadX509KeyPair(c.rutaCert, c.rutaClave)
	if err != nil {
		return fmt.Errorf("cargar par TLS: %w", err)
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.cert = &par
	c.modCert = iCert.ModTime()
	c.modClave = iClave.ModTime()
	return nil
}

// obtener es el GetCertificate de tls.Config.
//
// Si la recarga falla —renovación a medias, permisos rotos— se sigue sirviendo
// el certificado anterior en vez de tumbar la conexión: uno caducando es mucho
// mejor que ninguno, y el fallo lo canta el verificador, no el usuario.
func (c *CargadorCert) obtener(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	c.mu.RLock()
	cert, modCert, modClave := c.cert, c.modCert, c.modClave
	c.mu.RUnlock()

	iCert, err1 := os.Stat(c.rutaCert)
	iClave, err2 := os.Stat(c.rutaClave)
	if err1 == nil && err2 == nil &&
		(!iCert.ModTime().Equal(modCert) || !iClave.ModTime().Equal(modClave)) {
		if err := c.recargar(); err == nil {
			c.mu.RLock()
			cert = c.cert
			c.mu.RUnlock()
		}
	}
	return cert, nil
}

// Config devuelve el tls.Config del servidor.
//
// TLS 1.2 como suelo y no 1.3: la Fase 6 existe para entrar desde un equipo
// prestado cualquiera, y ese equipo puede ser viejo. Subir el suelo dejaría
// fuera justo el caso de uso que motivó la fase.
//
// No se fija CipherSuites: desde Go 1.17 la selección por omisión ya excluye
// las suites rotas, y congelar una lista a mano envejece peor que la
// biblioteca estándar.
func (c *CargadorCert) Config() *tls.Config {
	return &tls.Config{
		GetCertificate: c.obtener,
		MinVersion:     tls.VersionTLS12,
	}
}
