//go:build !linux

package sistema

import (
	"context"
	"time"
)

// ESTO SOLO EXISTE PARA PODER DESARROLLAR Y PROBAR EN EL HOST (D-06), igual
// que fsposix/sincronizar_otros.go. El objetivo de despliegue es linux/arm64.
//
// No se simula nada: se devuelve Disponible=false con el motivo escrito. Un
// /estado que mostrara métricas inventadas en Windows sería peor que uno que
// dice que no las tiene, y la mitad de los defectos que este proyecto ha
// encontrado en sus propios verificadores eran exactamente eso (§12.5).
func Leer(_ context.Context, puntoDatos string) Nodo {
	return Nodo{
		Momento:    time.Now(),
		Disponible: false,
		Datos:      Volumen{Punto: puntoDatos},
		Arranque:   Volumen{Punto: "/"},
		Avisos:     []string{ErrNoDisponible.Error()},
	}
}
