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
	"nasd/internal/almacen"
	"nasd/internal/autenticacion"
	"nasd/internal/config"
	"nasd/internal/metricas"
	"nasd/internal/seguridad"
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
	crearUsuario := flag.String("crear-usuario", "",
		"da de alta una cuenta con ese nombre; lee su contraseña de la entrada estándar")
	borrarUsuario := flag.String("borrar-usuario", "",
		"da de baja una cuenta; NO toca sus archivos")
	flag.Parse()

	if *generar {
		return generarCredencial()
	}
	if *crearUsuario != "" {
		return altaDeUsuario(*rutaConfig, *crearUsuario)
	}
	if *borrarUsuario != "" {
		return bajaDeUsuario(*rutaConfig, *borrarUsuario)
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

	// Registro de cuentas — ADR-0055. Que el archivo no exista NO es un error:
	// un nodo recién instalado no tiene usuarios y el superusuario, que no está
	// aquí, entra igual.
	usuarios, err := autenticacion.CargarRegistro(cfg.RutaUsuarios(), web.IteracionesPBKDF2)
	if err != nil {
		return err
	}
	reg.Info("registro de usuarios cargado",
		"ruta", cfg.RutaUsuarios(), "cuentas", usuarios.Cuantos())

	// Métricas de uso de disco por cuenta — P-4, etapa 3. Que el archivo no
	// exista tampoco es un error aquí, por la misma razón que el registro de
	// usuarios: un nodo recién instalado no tiene ninguna medida todavía.
	metricasUso, err := metricas.CargarRegistro(cfg.RutaUsoDisco())
	if err != nil {
		return err
	}

	// Historial de rechazos — panel de seguridad, etapa 1.
	//
	// UN ARCHIVO ILEGIBLE AQUÍ NO IMPIDE ARRANCAR, y es una decisión distinta
	// de la de las dos cargas anteriores: un registro de usuarios roto deja a
	// gente sin poder entrar, así que allí se falla a gritos. Un historial de
	// OBSERVACIÓN roto solo cuesta el historial, y cambiar un NAS sano por un
	// archivo de registro sería el peor negocio posible. Se anota y se sigue
	// con el anillo vacío, que CargarAnillo devuelve usable a propósito.
	historial, err := seguridad.CargarAnillo(cfg.RutaSeguridad())
	if err != nil {
		reg.Error("el historial de seguridad no se pudo leer; se empieza vacío",
			"ruta", cfg.RutaSeguridad(), "error", err)
	}

	s, err := web.Nuevo(web.Opciones{
		Almacen: alm,
		// AQUÍ se unen el aislamiento del adaptador POSIX y la web, y en
		// ningún otro sitio: es lo que permite que el paquete web no sepa que
		// existe fsposix. La conversión de tipo la hace esta función —el
		// adaptador devuelve su propio tipo y el puerto pide la interfaz—, que
		// es trabajo de la raíz de composición y de nadie más.
		AlmacenDe: func(usuario string) (almacen.Almacen, error) {
			return alm.ParaUsuario(usuario)
		},
		// La baja llama a esto ANTES de retirar la cuenta del registro
		// (etapa 2, corrección del 06/08): la carpeta sale de homeUsers/ y
		// queda como una más de la raíz.
		PromoverUsuario:   alm.PromoverCarpetaDeUsuario,
		Registro:          reg,
		PlazoInactividad:  cfg.PlazoInactividad,
		Credencial:        credencial,
		Usuarios:          usuarios,
		DuracionSesion:    cfg.DuracionSesion,
		InactividadSesion: cfg.InactividadSesion,
		Volumen:           cfg.Volumen,
		Metricas:          metricasUso,
		Seguridad:         historial,
	})
	if err != nil {
		return err
	}

	srv := s.HTTPServer(cfg.Direccion, cfg.Puerto)

	// Ciclo de limpieza de subidas abandonadas (ADR-0029). Fuera del camino
	// de las peticiones, para no bloquear listados ni calentar la CPU.
	pararMantenimiento := s.Mantener(context.Background())
	defer pararMantenimiento()

	// El historial baja a disco cada minuto, no en cada rechazo: un sondeo
	// puede producir decenas por segundo, y un fsync por cada uno convertiría
	// este registro en el amplificador que castiga el disco. El volcado FINAL
	// lo hace Mantener al cerrarse el canal, así que un apagado ordenado —el
	// reinicio diario de P-11 incluido— no pierde nada.
	pararHistorial := make(chan struct{})
	defer close(pararHistorial)
	go historial.Mantener(pararHistorial, func(err error) {
		reg.Error("no se pudo volcar el historial de seguridad", "error", err)
	})

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
		// web.EscucharTLS y no srvTLS.ListenAndServeTLS: el listener se abre
		// a mano porque tiene que pedir la red "tcp6", no "tcp". Ver el
		// comentario de EscucharTLS — la diferencia es la que le cerró a
		// Safari en iOS una sonda HTTPS contra la IP de la LAN que nadie
		// había pedido.
		ln, err := web.EscucharTLS(cfg.DireccionTLS, cfg.PuertoTLS)
		if err != nil {
			return fmt.Errorf("abrir el puerto TLS: %w", err)
		}
		srvTLS = s.HTTPServer(cfg.DireccionTLS, cfg.PuertoTLS)
		srvTLS.TLSConfig = cargador.Config()
		go func() {
			reg.Info("nasd escuchando por TLS", "direccion", srvTLS.Addr)
			// Los certificados ya van en TLSConfig; por eso las rutas van
			// vacías aquí.
			if err := srvTLS.ServeTLS(ln, "", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
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

// altaDeUsuario da de alta una cuenta desde la terminal — ADR-0055.
//
// POR QUÉ EMPIEZA POR AQUÍ Y NO POR EL PANEL DE LA WEB: la primera cuenta hay
// que crearla antes de que exista pantalla alguna donde crearla, y una vía por
// terminal sigue haciendo falta después, para el día en que la web no arranque.
//
// La contraseña se lee de la ENTRADA ESTÁNDAR y no de un argumento, por el
// mismo motivo que en --generar-credencial: un argumento queda en el historial
// del intérprete y en la lista de procesos, a la vista de cualquier usuario del
// sistema (P4).
//
// Uso:
//
//	printf '%s' 'la contraseña' | sudo nasd --config /etc/nasd/nasd.toml --crear-usuario juan
func altaDeUsuario(rutaConfig, nombre string) error {
	cfg, err := config.Cargar(rutaConfig)
	if err != nil {
		return err
	}
	// Se valida ANTES de leer nada: si el nombre no vale, mejor decirlo sin
	// haber pedido una contraseña que no se va a usar.
	if err := autenticacion.NombreValido(nombre); err != nil {
		return err
	}
	reg, err := autenticacion.CargarRegistro(cfg.RutaUsuarios(), web.IteracionesPBKDF2)
	if err != nil {
		return err
	}
	b, err := io.ReadAll(io.LimitReader(os.Stdin, 4096))
	if err != nil {
		return fmt.Errorf("leer la contraseña de la entrada estándar: %w", err)
	}
	// Se recortan solo los saltos finales que añade el intérprete. Los espacios
	// NO se tocan: pueden ser parte de la contraseña.
	clave := strings.TrimRight(string(b), "\r\n")
	if err := reg.Alta(nombre, clave); err != nil {
		return err
	}
	// Aviso, no silencio: en el nodo esto tarda ~3.6 s por la derivación, y sin
	// una línea de salida parece que se ha colgado.
	fmt.Printf("Cuenta %q creada en %s. Su carpeta se prepara al entrar por primera vez.\n",
		nombre, cfg.RutaUsuarios())
	return nil
}

// bajaDeUsuario retira una cuenta del registro — ADR-0055.
//
// NO SE DESTRUYE NI UN ARCHIVO DEL USUARIO, y eso es la decisión del
// responsable, no una omisión. Sin papelera (D-15) ni segunda copia (D-12),
// un borrado aquí no se desharía. **Corrección del 06/08:** su carpeta ya no
// se queda dentro de homeUsers/ —un sitio que el panel deja de mostrar—, sale
// a la raíz del volumen, junto a lo demás. La promoción va ANTES que la baja
// del registro: si no se puede promover (por ejemplo, un choque de nombre en
// la raíz), la cuenta sigue activa y se avisa del motivo, en vez de dejar el
// registro y el disco contando historias distintas.
//
// Es también la única forma de CAMBIAR una contraseña mientras no exista un
// campo propio para ello: baja y alta. Como la carpeta sobrevive —solo
// cambia de sitio—, la persona vuelve a entrar y se encuentra lo suyo.
func bajaDeUsuario(rutaConfig, nombre string) error {
	cfg, err := config.Cargar(rutaConfig)
	if err != nil {
		return err
	}
	alm, err := fsposix.AbrirVolumen(cfg.Volumen)
	if err != nil {
		return err
	}
	defer alm.Close()
	reg, err := autenticacion.CargarRegistro(cfg.RutaUsuarios(), web.IteracionesPBKDF2)
	if err != nil {
		return err
	}
	if err := alm.PromoverCarpetaDeUsuario(nombre); err != nil {
		return fmt.Errorf("no se pudo promover la carpeta de %q, la cuenta SIGUE activa: %w", nombre, err)
	}
	if err := reg.Baja(nombre); err != nil {
		return err
	}
	fmt.Printf("Cuenta %q retirada del registro. Si ya había entrado alguna vez, "+
		"su carpeta sigue intacta y ahora vive en la raíz del volumen, junto a "+
		"homeUsers/, en vez de dentro.\n", nombre)
	return nil
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
