// Package autenticacion stores and checks the web credential.
//
// It is NOT part of the store's domain: authenticating is not an operation on
// files. It lives in its own package, and the web adapter uses it.
//
// D-14 / ADR-0021: this credential belongs to the web ONLY and is different
// from the Samba one. Compromising one grants no access to the other, which
// matters especially because ADR-0018 leaves v1 WITHOUT TLS.
package autenticacion

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Formato almacenado, en una sola línea:
//
//	pbkdf2-sha256$<iteraciones>$<sal-base64>$<derivada-base64>
//
// Se guarda el algoritmo y las iteraciones DENTRO del propio valor: el día
// que haya que subir el coste, las credenciales viejas se siguen verificando
// con sus parámetros y no hay migración forzosa.
const (
	algoritmo   = "pbkdf2-sha256"
	tamanoSal   = 16
	tamanoClave = 32
)

var (
	ErrFormato     = errors.New("credencial con formato inválido")
	ErrClaveCorta  = errors.New("la contraseña es demasiado corta")
	ErrSinConfigur = errors.New("no hay credencial configurada")
)

// LongitudMinima es el mínimo exigido al fijar la contraseña.
//
// Sin TLS (ADR-0018) y en una LAN doméstica, el ataque realista no es la
// fuerza bruta remota —hay un solo usuario y no hay Internet entrante— sino
// una contraseña adivinable. 12 es un mínimo defendible sin ser hostil. [R]
const LongitudMinima = 12

// Derivar produce la línea a guardar en el archivo de credencial.
func Derivar(clave string, iteraciones int) (string, error) {
	if len([]rune(clave)) < LongitudMinima {
		return "", fmt.Errorf("%w: mínimo %d caracteres", ErrClaveCorta, LongitudMinima)
	}
	sal := make([]byte, tamanoSal)
	if _, err := rand.Read(sal); err != nil {
		return "", fmt.Errorf("generar la sal: %w", err)
	}
	dk, err := pbkdf2.Key(sha256.New, clave, sal, iteraciones, tamanoClave)
	if err != nil {
		return "", fmt.Errorf("derivar: %w", err)
	}
	return fmt.Sprintf("%s$%d$%s$%s",
		algoritmo, iteraciones,
		base64.RawStdEncoding.EncodeToString(sal),
		base64.RawStdEncoding.EncodeToString(dk),
	), nil
}

// Verificar comprueba una contraseña contra la línea almacenada.
//
// La comparación es en TIEMPO CONSTANTE: comparar con == filtraría, por el
// tiempo de respuesta, cuántos bytes iniciales se acertaron.
func Verificar(almacenado, clave string) bool {
	partes := strings.Split(strings.TrimSpace(almacenado), "$")
	if len(partes) != 4 || partes[0] != algoritmo {
		return false
	}
	iteraciones, err := strconv.Atoi(partes[1])
	if err != nil || iteraciones < 1 {
		return false
	}
	sal, err := base64.RawStdEncoding.DecodeString(partes[2])
	if err != nil {
		return false
	}
	esperada, err := base64.RawStdEncoding.DecodeString(partes[3])
	if err != nil {
		return false
	}
	dk, err := pbkdf2.Key(sha256.New, clave, sal, iteraciones, len(esperada))
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(dk, esperada) == 1
}

// Valida comprueba que una línea almacenada tenga forma utilizable.
// Se llama al arrancar: mejor no arrancar que aceptar peticiones con una
// credencial ilegible que rechazaría todo (P5).
func Valida(almacenado string) error {
	partes := strings.Split(strings.TrimSpace(almacenado), "$")
	if len(partes) != 4 {
		return fmt.Errorf("%w: se esperaban 4 campos separados por $", ErrFormato)
	}
	if partes[0] != algoritmo {
		return fmt.Errorf("%w: algoritmo %q desconocido", ErrFormato, partes[0])
	}
	if n, err := strconv.Atoi(partes[1]); err != nil || n < 1 {
		return fmt.Errorf("%w: iteraciones inválidas", ErrFormato)
	}
	if _, err := base64.RawStdEncoding.DecodeString(partes[2]); err != nil {
		return fmt.Errorf("%w: sal ilegible", ErrFormato)
	}
	if _, err := base64.RawStdEncoding.DecodeString(partes[3]); err != nil {
		return fmt.Errorf("%w: derivada ilegible", ErrFormato)
	}
	return nil
}
