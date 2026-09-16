// Package operacion talks to systemd: startup notification and watchdog.
//
// Charter §6.2 requires WatchdogSec in the units, and WatchdogSec without a
// heartbeat from the code restarts the service every interval. It is
// implemented with net.Dial over the NOTIFY_SOCKET socket: no dependencies (P8).
package operacion

import (
	"context"
	"log/slog"
	"net"
	"os"
	"strconv"
	"time"
)

// notificar envía un mensaje a systemd. Fuera de systemd no hay socket y la
// función no hace nada, que es lo correcto al ejecutar a mano.
func notificar(mensaje string) error {
	socket := os.Getenv("NOTIFY_SOCKET")
	if socket == "" {
		return nil
	}
	// Los sockets abstractos de Linux empiezan por '@'.
	if socket[0] == '@' {
		socket = "\x00" + socket[1:]
	}
	c, err := net.DialUnix("unixgram", nil, &net.UnixAddr{Name: socket, Net: "unixgram"})
	if err != nil {
		return err
	}
	defer c.Close()
	_, err = c.Write([]byte(mensaje))
	return err
}

// Listo avisa a systemd de que el servicio ya atiende peticiones (Type=notify).
func Listo(reg *slog.Logger) {
	if err := notificar("READY=1"); err != nil {
		reg.Warn("no se pudo notificar READY a systemd", "error", err)
	}
}

// Parando avisa de un apagado ordenado, para que systemd no lo tome por caída.
func Parando(reg *slog.Logger) {
	if err := notificar("STOPPING=1"); err != nil {
		reg.Warn("no se pudo notificar STOPPING a systemd", "error", err)
	}
}

// Latir mantiene vivo el watchdog mientras el contexto siga activo.
// Devuelve la función de parada.
func Latir(ctx context.Context, reg *slog.Logger) func() {
	usec, err := strconv.Atoi(os.Getenv("WATCHDOG_USEC"))
	if err != nil || usec <= 0 {
		return func() {} // sin watchdog configurado
	}
	// La recomendación de systemd es latir al doble de frecuencia que el
	// plazo, para no depender de un reloj perfecto.
	intervalo := time.Duration(usec/2) * time.Microsecond

	ctxLatido, cancelar := context.WithCancel(ctx)
	go func() {
		t := time.NewTicker(intervalo)
		defer t.Stop()
		for {
			select {
			case <-ctxLatido.Done():
				return
			case <-t.C:
				if err := notificar("WATCHDOG=1"); err != nil {
					reg.Warn("latido de watchdog fallido", "error", err)
				}
			}
		}
	}()
	return cancelar
}
