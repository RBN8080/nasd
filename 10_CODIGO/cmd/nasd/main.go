// Comando nasd — servidor de archivos del proyecto NAS.
//
// Composición: lee la configuración, arma los adaptadores y arranca.
// Toda la lógica vive en internal/; aquí solo se enchufan las piezas.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"nasd/internal/adaptadores/fsposix"
	"nasd/internal/adaptadores/operacion"
	"nasd/internal/adaptadores/web"
	"nasd/internal/autenticacion"
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
	generar := flag.Bool("generar-credencial", false,
		"lee una contraseña de la entrada estándar y escribe su línea derivada")
	flag.Parse()

	if *generar {
		return generarCredencial()
	}

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

	credencial, origen, err := leerCredencial()
	if err != nil {
		return err
	}
	reg.Info("credencial de la web cargada", "origen", origen)

	s, err := web.Nuevo(web.Opciones{
		Almacen:          alm,
		Registro:         reg,
		PlazoInactividad: cfg.PlazoInactividad,
		Credencial:       credencial,
		DuracionSesion:   cfg.DuracionSesion,
		Volumen:          cfg.Volumen,
	})
	if err != nil {
		return err
	}

	srv := s.HTTPServer(cfg.Direccion, cfg.Puerto)

	// Ciclo de limpieza de subidas abandonadas (ADR-0029). Fuera del camino
	// de las peticiones, para no bloquear listados ni calentar la CPU.
	pararMantenimiento := s.Mantener(context.Background())
	defer pararMantenimiento()

	ctx, parar := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer parar()

	// Dos servidores como mucho: el de siempre en la LAN y, si hay
	// certificado, el de TLS hacia Internet. Comparten el mismo Handler, así
	// que no hay dos webs: es la misma, servida por dos puertos.
	errores := make(chan error, 2)
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

	// TLS — ADR-0046. Termina aquí y no en un proxy: el limitador de intentos
	// usa r.RemoteAddr, y tras un proxy todas las peticiones llegarían desde
	// 127.0.0.1 (07_AUDITORIAS §7.2).
	var srvTLS *http.Server
	if cfg.TLSActivo() {
		cargador, err := web.NuevoCargadorCert(cfg.Certificado, cfg.ClaveTLS)
		if err != nil {
			// P5: si se prometió TLS y no se puede dar, no se arranca
			// sirviendo HTTP como si nada.
			return fmt.Errorf("TLS configurado pero no utilizable: %w", err)
		}
		srvTLS = s.HTTPServer(cfg.DireccionTLS, cfg.PuertoTLS)
		srvTLS.TLSConfig = cargador.Config()
		go func() {
			reg.Info("nasd escuchando por TLS", "direccion", srvTLS.Addr)
			// Los certificados ya van en TLSConfig; por eso las rutas van
			// vacías aquí.
			if err := srvTLS.ListenAndServeTLS("", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errores <- err
			}
		}()
	} else {
		reg.Warn("TLS APAGADO: sin certificado configurado, la web solo va por HTTP en la LAN")
	}

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
	// El de TLS primero: es el que puede tener clientes de Internet, y su
	// error no debe tapar el del servidor principal.
	if srvTLS != nil {
		if err := srvTLS.Shutdown(ctxApagado); err != nil {
			reg.Error("apagando el servidor TLS", "error", err)
		}
	}
	return srv.Shutdown(ctxApagado)
}

// leerCredencial obtiene la línea derivada de la contraseña de la web.
//
// P4 del charter: el secreto NO vive en el repositorio ni en el TOML. El
// mecanismo declarado es LoadCredential= de systemd, que deja el archivo en
// un tmpfs privado del servicio, legible solo por él.
//
// Si no hay credencial, el servicio NO ARRANCA. No existe un modo sin
// autenticar: dejarlo abierto pondría el disco entero al alcance de
// cualquiera en la LAN, que es justo lo que RN-06 ordena evitar (P5).
func leerCredencial() (string, string, error) {
	if dir := os.Getenv("CREDENTIALS_DIRECTORY"); dir != "" {
		ruta := filepath.Join(dir, "web")
		if b, err := os.ReadFile(ruta); err == nil {
			return strings.TrimSpace(string(b)), "systemd LoadCredential", nil
		}
	}
	// Camino alternativo, para desarrollo y para las pruebas del nodo.
	if ruta := os.Getenv("NASD_CREDENCIAL"); ruta != "" {
		b, err := os.ReadFile(ruta)
		if err != nil {
			return "", "", fmt.Errorf("leer NASD_CREDENCIAL %q: %w", ruta, err)
		}
		return strings.TrimSpace(string(b)), "NASD_CREDENCIAL=" + ruta, nil
	}
	return "", "", errors.New(
		"no hay credencial de la web configurada. " +
			"Genérela con «nasd --generar-credencial» y entréguesela por " +
			"LoadCredential=web:/etc/nasd/credencial en la unidad systemd")
}

// generarCredencial lee la contraseña de la ENTRADA ESTÁNDAR y escribe su
// línea derivada por la salida estándar.
//
// Se lee de stdin y no de un argumento a propósito: un argumento quedaría en
// el historial del intérprete y en la lista de procesos, visible para
// cualquier usuario del sistema (P4).
func generarCredencial() error {
	b, err := io.ReadAll(io.LimitReader(os.Stdin, 4096))
	if err != nil {
		return fmt.Errorf("leer la contraseña de la entrada estándar: %w", err)
	}
	// Se recortan solo los saltos finales que añade el intérprete al leer una
	// línea. Los espacios NO se tocan: pueden ser parte de la contraseña.
	clave := strings.TrimRight(string(b), "\r\n")
	linea, err := autenticacion.Derivar(clave, web.IteracionesPBKDF2)
	if err != nil {
		return err
	}
	fmt.Println(linea)
	return nil
}
