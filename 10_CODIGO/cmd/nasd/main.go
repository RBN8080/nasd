// Comando nasd — servidor de archivos del proyecto NAS.
//
// Composición: lee la configuración, arma los adaptadores y arranca.
// Toda la lógica vive en internal/; aquí solo se enchufan las piezas.
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"nasd/internal/adaptadores/fsposix"
	"nasd/internal/adaptadores/operacion"
	"nasd/internal/adaptadores/web"
	"nasd/internal/config"
)

func main() {
	if err := ejecutar(); err != nil {
		// P5: fallo ruidoso, temprano y trazable. Nunca degradación silenciosa.
		slog.Error("nasd no pudo arrancar", "error", err)
		os.Exit(1)
	}
}

func ejecutar() error {
	rutaConfig := flag.String("config", "", "ruta del archivo TOML de configuración")
	flag.Parse()

	// RNF-13: registro estructurado en JSON hacia journald por la salida
	// estándar, que es como systemd lo recoge.
	reg := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(reg)

	cfg, err := config.Cargar(*rutaConfig)
	if err != nil {
		return err
	}

	// Sin volumen no hay nada que servir: es mejor no arrancar que aceptar
	// peticiones que van a fracasar después (P5, ADR-0019).
	alm, err := fsposix.AbrirVolumen(cfg.Volumen)
	if err != nil {
		return err
	}
	defer alm.Close()

	s, err := web.Nuevo(web.Opciones{
		Almacen:          alm,
		Registro:         reg,
		PlazoInactividad: cfg.PlazoInactividad,
	})
	if err != nil {
		return err
	}

	srv := s.HTTPServer(cfg.Direccion, cfg.Puerto)

	ctx, parar := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer parar()

	errores := make(chan error, 1)
	go func() {
		reg.Info("nasd escuchando",
			"direccion", srv.Addr,
			"volumen", cfg.Volumen,
			"plazo_inactividad_s", int(cfg.PlazoInactividad.Seconds()),
		)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errores <- err
		}
	}()

	// Charter §6.2: WatchdogSec exige Type=notify y latido desde el código.
	operacion.Listo(reg)
	pararLatido := operacion.Latir(ctx, reg)
	defer pararLatido()

	select {
	case err := <-errores:
		return err
	case <-ctx.Done():
	}

	reg.Info("apagando de forma ordenada")
	operacion.Parando(reg)

	// Las transferencias en curso pueden ser de 4 GB: no se cortan de golpe.
	// El plazo por actividad ya impide que una conexión colgada lo alargue
	// indefinidamente (ADR-0026).
	ctxApagado, cancelar := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelar()
	return srv.Shutdown(ctxApagado)
}
