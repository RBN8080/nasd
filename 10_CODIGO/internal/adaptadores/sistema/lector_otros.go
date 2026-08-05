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
		Vivo:     Vivo{Momento: time.Now(), Disponible: false},
		Datos:    Volumen{Punto: puntoDatos},
		Arranque: Volumen{Punto: "/"},
		Avisos:   []string{ErrNoDisponible.Error()},
	}
}

// LeerCPU y LeerVivo existen aquí por el mismo motivo que Leer: que el
// programa compile y se pueda probar en el host (D-06). No inventan nada.
//
// Consecuencia visible y correcta: en el servidor de pruebas del PC
// (NAS_OPERACION.txt §8) el flujo de estado se conecta y late, pero no trae
// ninguna medida. Sirve para juzgar el ASPECTO, que es justo para lo que ese
// servidor existe; las cifras se juzgan en la Pi.
func LeerCPU() MuestraCPU { return MuestraCPU{} }

func LeerVivo(_ MuestraCPU) (Vivo, MuestraCPU) {
	return Vivo{Momento: time.Now(), Disponible: false}, MuestraCPU{}
}
