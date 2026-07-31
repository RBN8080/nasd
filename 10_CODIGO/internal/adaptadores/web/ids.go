package web

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// generarID produce un identificador de subida aleatorio.
//
// crypto/rand, nunca datos del cliente: además de evitar colisiones, quita
// de raíz un vector de salto de ruta sobre estado/parciales/ (ADR-0027).
func generarID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generar identificador: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}
