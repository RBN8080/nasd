package web

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// generarPar escribe un certificado autofirmado con el nombre común indicado.
// Sirve para distinguir un par de otro sin mirar fechas.
func generarPar(t *testing.T, dir, nombre string) (string, string) {
	t.Helper()
	clave, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generar clave: %v", err)
	}
	plantilla := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: nombre},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, &plantilla, &plantilla, &clave.PublicKey, clave)
	if err != nil {
		t.Fatalf("crear certificado: %v", err)
	}
	rutaCert := filepath.Join(dir, "cert.pem")
	rutaClave := filepath.Join(dir, "clave.pem")

	pemCert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.WriteFile(rutaCert, pemCert, 0o600); err != nil {
		t.Fatalf("escribir certificado: %v", err)
	}
	derClave, err := x509.MarshalECPrivateKey(clave)
	if err != nil {
		t.Fatalf("serializar clave: %v", err)
	}
	pemClave := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: derClave})
	if err := os.WriteFile(rutaClave, pemClave, 0o600); err != nil {
		t.Fatalf("escribir clave: %v", err)
	}
	return rutaCert, rutaClave
}

func nombreComun(t *testing.T, c *CargadorCert) string {
	t.Helper()
	cert, err := c.obtener(nil)
	if err != nil {
		t.Fatalf("obtener certificado: %v", err)
	}
	hoja, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatalf("analizar hoja: %v", err)
	}
	return hoja.Subject.CommonName
}

// Es LA razón de existir de este cargador: si no recargara, una renovación no
// surtiría efecto hasta reiniciar el servicio, y reiniciar corta subidas.
func TestCargadorCertRecargaAlCambiarElArchivo(t *testing.T) {
	dir := t.TempDir()
	rutaCert, rutaClave := generarPar(t, dir, "antes")

	c, err := NuevoCargadorCert(rutaCert, rutaClave)
	if err != nil {
		t.Fatalf("crear cargador: %v", err)
	}
	if cn := nombreComun(t, c); cn != "antes" {
		t.Fatalf("certificado inicial: quería «antes», salió %q", cn)
	}

	// El sistema de archivos puede tener resolución de un segundo: sin esto,
	// el par nuevo podría quedar con la MISMA fecha y la prueba pasaría por
	// casualidad sin haber recargado nada.
	time.Sleep(1100 * time.Millisecond)
	generarPar(t, dir, "despues")

	if cn := nombreComun(t, c); cn != "despues" {
		t.Fatalf("tras renovar: quería «despues», salió %q — NO recargó", cn)
	}
}

// Un certificado que caduca es malo; quedarse sin ninguno y tumbar las
// conexiones es peor. Si la renovación deja los archivos a medias, se sigue
// sirviendo el anterior.
func TestCargadorCertConservaElAnteriorSiLaRecargaFalla(t *testing.T) {
	dir := t.TempDir()
	rutaCert, rutaClave := generarPar(t, dir, "bueno")

	c, err := NuevoCargadorCert(rutaCert, rutaClave)
	if err != nil {
		t.Fatalf("crear cargador: %v", err)
	}

	time.Sleep(1100 * time.Millisecond)
	if err := os.WriteFile(rutaCert, []byte("esto no es un PEM"), 0o600); err != nil {
		t.Fatalf("corromper certificado: %v", err)
	}

	if cn := nombreComun(t, c); cn != "bueno" {
		t.Fatalf("con el archivo roto: quería seguir con «bueno», salió %q", cn)
	}
}

// P5: prometer TLS y arrancar sin él sería peor que no arrancar.
func TestNuevoCargadorCertFallaSiNoExiste(t *testing.T) {
	dir := t.TempDir()
	if _, err := NuevoCargadorCert(filepath.Join(dir, "no.pem"), filepath.Join(dir, "tampoco.pem")); err == nil {
		t.Fatal("quería error con rutas inexistentes, no lo hubo")
	}
}

// EL DEFECTO REAL, convertido en prueba: hasta el 2026-08-06 el listener TLS
// se abría con la red "tcp" sobre "::", que en Go es doble pila y acepta
// también IPv4. Contra la IP de la LAN eso respondía TLS con un certificado
// que no la nombra —el de nas-ejemplo.duckdns.org—, y Safari en iOS, al abrir
// una imagen en pestaña nueva (RF-25) hacia una URL http://, encontraba esa
// respuesta al sondear HTTPS y abandonaba la navegación en vez de caer a
// HTTP. Brave no hace esa sonda, por eso solo fallaba en Safari.
//
// La prueba abre un EscucharTLS real en loopback y exige las dos cosas a la
// vez: que IPv6 (que sigue siendo la vía pública de la Fase 6, ADR-0048)
// siga aceptando, y que IPv4 quede rechazado en el propio socket, antes de
// llegar a TLS.
func TestEscucharTLSNuncaAceptaIPv4(t *testing.T) {
	ln, err := EscucharTLS("::1", 0) // puerto 0: el sistema asigna uno libre
	if err != nil {
		t.Fatalf("EscucharTLS: %v", err)
	}
	defer ln.Close()

	_, puerto, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatalf("leer el puerto asignado: %v", err)
	}

	aceptados := make(chan string, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		aceptados <- c.RemoteAddr().String()
	}()

	if _, err := net.DialTimeout("tcp4", net.JoinHostPort("127.0.0.1", puerto), time.Second); err == nil {
		t.Fatal("una conexión IPv4 se aceptó; el listener sigue en doble pila")
	}

	c6, err := net.DialTimeout("tcp6", net.JoinHostPort("::1", puerto), time.Second)
	if err != nil {
		t.Fatalf("una conexión IPv6 legítima falló: %v", err)
	}
	c6.Close()

	select {
	case <-aceptados:
	case <-time.After(2 * time.Second):
		t.Fatal("el listener no aceptó la conexión IPv6")
	}
}
