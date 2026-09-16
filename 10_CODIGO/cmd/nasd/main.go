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
	"net/netip"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"nasd/internal/adaptadores/canal"
	"nasd/internal/adaptadores/fsposix"
	"nasd/internal/adaptadores/operacion"
	"nasd/internal/adaptadores/web"
	"nasd/internal/almacen"
	"nasd/internal/autenticacion"
	"nasd/internal/aviso"
	"nasd/internal/config"
	"nasd/internal/geoip"
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
	prepararGeoIP := flag.Bool("preparar-geoip", false,
		"convierte los TSV de IPtoASN en la base que lee el panel de seguridad; "+
			"uso: nasd --preparar-geoip <salida> <ip2asn-v4.tsv> <ip2asn-v6.tsv>")
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
	if *prepararGeoIP {
		return prepararBaseGeoIP(flag.Args())
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

	// LAS DOS REDES IPv4 DE CASA, antes que nada de lo que las use.
	//
	// Van aquí por lo mismo que AprenderRedesPropias justo debajo: al cargar
	// el historial se reclasifica cada evento guardado, así que configurarlas
	// después dejaría todo lo ya escrito recuperado con la red equivocada.
	lan, tunel := seguridad.ConfigurarRedes(cfg.RedLAN, cfg.RedTunel)
	reg.Info("redes de casa", "lan", lan.String(), "tunel", tunel.String())

	// Y SE COMPRUEBA LO ÚNICO QUE SE PUEDE COMPROBAR SIN SALIR DEL PROCESO:
	// que el servicio escucha dentro de la red que dice ser su casa. Si no,
	// nada falla —esa es la desgracia— pero el nodo cuenta como extraño a
	// todo el mundo, empezando por sí mismo. Es barato y ataja el único modo
	// de fallo silencioso que queda tras ADR-0095.
	if propia, err := netip.ParseAddr(cfg.Direccion); err == nil && lan.IsValid() && !lan.Contains(propia) {
		reg.Warn("la red declarada NO contiene la dirección de escucha: la casa entera se contará como Internet",
			"lan", lan.String(), "direccion", cfg.Direccion, "arreglo", "corrija red.lan en el TOML")
	}

	// QUÉ ES «DE CASA» EN IPv6 SE LE PREGUNTA AL SISTEMA, no se escribe aquí.
	//
	// Lo destapó el historial real la primera tarde: el iPhone del
	// responsable, en su propia Wi-Fi y hablando IPv6 nativo, salía
	// clasificado como INTERNET, porque el paquete solo conocía las dos redes
	// IPv4. Escribir aquí el prefijo delegado por el proveedor sería una
	// constante que deja de ser verdad EN SILENCIO el día que rote.
	//
	// Va antes de cargar el historial a propósito: al releerlo se reclasifica
	// cada evento guardado (ver eventoEnDisco.aEvento), así que si esto
	// corriera después, todo lo ya escrito se recuperaría mal clasificado.
	if propias := seguridad.AprenderRedesPropias(direccionesDelNodo()); len(propias) > 0 {
		reg.Info("redes propias aprendidas", "prefijos", fmt.Sprint(propias))
	} else {
		// Se dice a gritos: sin esto, todo el IPv6 de casa se cuenta como
		// Internet y la única cifra que le importa al responsable miente.
		reg.Warn("no se aprendió ninguna red IPv6 propia: el tráfico IPv6 de casa se contará como Internet")
	}

	// Base de país y operador — etapa 4. Es OPCIONAL, al contrario que las
	// tres cargas anteriores: un nodo al que todavía no se le ha ejecutado
	// 18_geoip.sh funciona exactamente igual, solo que el panel no enseña de
	// dónde viene cada origen. Por eso un fallo aquí se anota y se sigue, y
	// geoip.Buscar admite un receptor nulo a propósito.
	var baseGeo *geoip.BaseDatos
	if b, err := geoip.Abrir(cfg.RutaGeoIP()); err != nil {
		if os.IsNotExist(err) {
			reg.Info("sin base de país/operador: el panel no mostrará de dónde viene cada origen",
				"ruta", cfg.RutaGeoIP(), "instalar_con", "20_APROVISIONAMIENTO/18_geoip.sh")
		} else {
			reg.Error("la base de país/operador no se pudo abrir; se sigue sin ella",
				"ruta", cfg.RutaGeoIP(), "error", err)
		}
	} else {
		baseGeo = b
		defer baseGeo.Cerrar()
		reg.Info("base de país/operador cargada", "rangos", baseGeo.Cuantos())
	}

	// Historial de rechazos — panel de seguridad, etapa 1.
	//
	// UN ARCHIVO ILEGIBLE AQUÍ NO IMPIDE ARRANCAR, y es una decisión distinta
	// de la de las cargas de usuarios y métricas: un registro de usuarios roto
	// deja a gente sin poder entrar, así que allí se falla a gritos. Un
	// historial de OBSERVACIÓN roto solo cuesta el historial, y cambiar un NAS
	// sano por un archivo de registro sería el peor negocio posible. Se anota
	// y se sigue con el anillo vacío, que CargarAnillo devuelve usable a
	// propósito.
	historial, err := seguridad.CargarAnillo(cfg.RutaSeguridad())
	if err != nil {
		reg.Error("el historial de seguridad no se pudo leer; se empieza vacío",
			"ruta", cfg.RutaSeguridad(), "error", err)
	}

	// El historial de CONEXIONES entrantes de Internet (ADR-0064), que es lo
	// único capaz de ver lo que muere en el saludo TLS. Mismo tratamiento del
	// error y por el mismo motivo que el anterior.
	conexiones, err := seguridad.CargarConexiones(cfg.RutaConexiones())
	if err != nil {
		reg.Error("el historial de conexiones no se pudo leer; se empieza vacío",
			"ruta", cfg.RutaConexiones(), "error", err)
	}

	// La cuarentena por conducta. MISMO tratamiento del error que los dos
	// anillos —se anota y se sigue— pero por un motivo distinto y que conviene
	// no confundir: allí se pierde historial, aquí se pierde una DEFENSA. Aun
	// así no se falla el arranque, porque un NAS que no arranca protege menos
	// que uno que arranca sin la respuesta automática: las que de verdad
	// defienden —TLS, credencial, contención de rutas— siguen enteras.
	cuarentena, err := seguridad.CargarCuarentena(cfg.RutaCuarentena())
	if err != nil {
		reg.Error("la cuarentena no se pudo leer; se empieza sin nadie apartado",
			"ruta", cfg.RutaCuarentena(), "error", err)
	}

	// Los bloqueos puestos a mano. Mismo tratamiento del error: se anota y se
	// sigue con lo que se haya podido leer. Perderla cuesta los bloqueos, no el
	// servicio.
	//
	// EL MENSAJE NO DICE «se empieza vacía», y no es un matiz de estilo: el
	// 2026-08-24 esa frase apareció en el diario del nodo con OCHO bloqueos
	// cargados y vigentes, porque CargarLista devuelve lo leído junto al error.
	// Quien leyera el diario habría creído que el nodo estaba sin defensa
	// manual, y quien mirara el panel habría visto ocho entradas: dos fuentes
	// contradiciéndose sobre lo mismo. El error de CargarLista ya dice cuántas
	// sobrevivieron; aquí solo hay que no desmentirlo.
	lista, err := seguridad.CargarLista(cfg.RutaLista())
	if err != nil {
		reg.Error("la lista de bloqueos se leyó a medias; revise el archivo",
			"ruta", cfg.RutaLista(), "error", err)
	}

	// LOS OPERADORES CONOCIDOS — la barandilla de la cuarentena automática.
	//
	// Va DESPUÉS de la lista y ANTES de enchufarle nada a la cuarentena, porque
	// el orden es el de las dependencias: sembrarlo necesita el historial ya
	// cargado, y la cuarentena necesita esto para poder decidir.
	//
	// Si falla, se sigue: el coste de quedarse sin este archivo NO es quedarse
	// sin defensa, es lo contrario —el guardia apartaría de más—, y por eso el
	// mensaje lo dice con esas palabras en vez de con las de la cuarentena.
	operadores, primeraVez, err := seguridad.CargarOperadores(cfg.RutaOperadores())
	if err != nil {
		reg.Error("los operadores conocidos no se pudieron leer; el guardia podría apartar de más",
			"ruta", cfg.RutaOperadores(), "error", err)
	}
	// LA SIEMBRA, UNA SOLA VEZ Y SOLO SI EL ARCHIVO NO EXISTÍA. Sin esto, el
	// día que se estrena la barandilla no consta nadie, y el primero en llegar
	// desde fuera —que puede ser de la familia— se lleva un apartado antes de
	// poder escribir la contraseña que le habría hecho constar.
	if primeraVez {
		n := operadores.Sembrar(historial.Desde(time.Time{}), baseGeo, time.Now())
		reg.Info("operadores conocidos sembrados desde el historial",
			"operadores", n, "criterio", "sesiones caducadas, que prueban que por ahí entró alguien")
	}
	// Y aquí es donde el guardia deja de razonar solo por dirección. Sin esta
	// línea se comporta exactamente como antes: por dirección, con caducidad y
	// con los umbrales de conducta.
	cuarentena.ConOperadores(baseGeo, operadores)

	// La marca de novedades. Solo guarda CUÁNDO se miró por última vez; el
	// resto se deriva de lo que ya sobrevive al reinicio.
	novedades, err := seguridad.CargarNovedades(cfg.RutaNovedades())
	if err != nil {
		reg.Error("la marca de novedades no se pudo leer; se empieza sin ella",
			"ruta", cfg.RutaNovedades(), "error", err)
	}

	// Las respuestas inesperadas del propio servidor. Mismo tratamiento del
	// error que todo lo anterior —se anota y se sigue— y por un motivo propio:
	// arrancar sin este archivo cuesta la memoria de lo ya encontrado, pero
	// negarse a arrancar dejaría al nodo sin servicio por no poder leer una
	// lista que en un nodo sano está vacía.
	hallazgos, err := seguridad.CargarHallazgos(cfg.RutaHallazgos())
	if err != nil {
		reg.Error("los hallazgos no se pudieron leer; se empieza sin ninguno",
			"ruta", cfg.RutaHallazgos(), "error", err)
	}

	// LA CAPA DE AVISOS — ADR-0073 y ADR-0074.
	//
	// ES OPCIONAL Y SE DICE CUANDO NO ESTÁ. Un nodo sin el archivo de secretos
	// funciona exactamente igual: detecta, infiere, aparta, bloquea y registra
	// lo mismo, y los avisos van al diario en vez de al teléfono. Es el mismo
	// trato que GeoIP y por el mismo motivo — esto es comunicación, no un
	// control, y negarse a arrancar por no poder avisar cambiaría un servicio
	// sano por un mensaje.
	//
	// EL ORDEN DE ESTAS PIEZAS NO ES ARBITRARIO: el canal se construye antes
	// que el servidor porque el servidor necesita poder emitir; el latido se
	// construye DESPUÉS porque su cuerpo lo compone el servidor.
	modoAvisos, ok := aviso.ModoDesde(cfg.ModoAvisos)
	if !ok {
		// P5: un modo mal escrito no se degrada a «normal» en silencio. El
		// responsable creería estar en silencio mientras el nodo le escribe, o
		// al revés, y ninguna de las dos se nota hasta que importa.
		return fmt.Errorf("avisos.modo %q: use «normal», «silencio» u «observacion»", cfg.ModoAvisos)
	}

	registroAvisos, err := aviso.CargarRegistro(cfg.RutaAvisos())
	if err != nil {
		reg.Error("la marca de avisos no se pudo leer; se empieza sin memoria de lo ya avisado",
			"ruta", cfg.RutaAvisos(), "error", err)
	}

	secretos, origenSecretos, err := leerSecretosDeAvisos()
	if err != nil {
		// Un archivo ILEGIBLE sí se dice a gritos, al contrario que uno
		// AUSENTE: ausente significa «no configurado» y es un estado válido;
		// ilegible significa que alguien quiso configurarlo y no funcionó, y
		// callarlo dejaría al responsable creyendo que le van a avisar.
		return fmt.Errorf("archivo de secretos de avisos: %w", err)
	}

	// El diario SIEMPRE está. Es lo que hace que un nodo sin proveedor no sea
	// un nodo mudo: sigue habiendo constancia local de cada aviso.
	var destino canal.Canal = canal.NuevoDiario(reg)
	if secretos.TelegramToken != "" {
		tg, err := canal.NuevoTelegram(secretos.TelegramToken, secretos.TelegramChat)
		if err != nil {
			return fmt.Errorf("canal de avisos: %w", err)
		}
		destino = canal.NuevoDuplicado(tg, canal.NuevoDiario(reg))
		reg.Info("canal de avisos configurado", "canal", destino.Nombre(), "origen", origenSecretos)
	} else {
		reg.Warn("SIN CANAL DE AVISOS EXTERIOR: los avisos solo van al diario de este nodo",
			"configurar_con", "20_APROVISIONAMIENTO/20_avisos.sh")
	}
	cola := canal.NuevaCola(destino, reg)

	// El latido se declara aquí y se construye más abajo: su cuerpo lo compone
	// el servidor, que todavía no existe. La salud se pregunta por closure, y
	// Salud() admite receptor nulo a propósito para que esto sea seguro antes
	// de asignarlo.
	var latido *canal.Latido

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
		Conexiones:        conexiones,
		Cuarentena:        cuarentena,
		Lista:             lista,
		Novedades:         novedades,
		Hallazgos:         hallazgos,
		RutaToques:        cfg.RutaToques(),
		GeoIP:             baseGeo,
		Operadores:        operadores,
		// Miniaturas EXIF — rector §7.nonies.bis. Siempre se pasa la ruta
		// derivada, igual que RutaGeoIP: si nas-miniatura no está instalado
		// en el nodo, el subproceso simplemente falla al primer intento y
		// esa foto se sirve sin miniatura, sin que arrancar dependa de nada
		// más (mismo criterio «opcional» que GeoIP).
		DirMiniaturas: cfg.RutaMiniaturas(),

		// La capa de avisos. Emitir es Encolar y no un envío directo: quien
		// produce un aviso NO puede quedarse esperando a un POST, porque quien
		// lo produce es el mismo ciclo que evalúa la cuarentena (ADR-0073).
		Avisos:         registroAvisos,
		Emitir:         cola.Encolar,
		ModoAvisos:     modoAvisos,
		PeriodoResumen: cfg.PeriodoResumen,
		SaludCanal:     cola.Salud,
		SaludLatido:    func() aviso.SaludLatido { return latido.Salud() },
		// El cliente de respaldo publica su estado dentro del volumen. Vacío
		// mientras nadie lo configure, y entonces el indicador no existe.
		RutaEstadoRespaldo: cfg.RutaEstadoRespaldo,
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
	// El de conexiones comparte el canal de parada: los dos se vuelcan en el
	// mismo apagado ordenado, y dos canales para un solo momento serían dos
	// sitios donde olvidarse de cerrar uno.
	go conexiones.Mantener(pararHistorial, func(err error) {
		reg.Error("no se pudo volcar el historial de conexiones", "error", err)
	})
	// La cuarentena y la lista comparten el mismo canal de parada por el mismo
	// motivo: un solo apagado ordenado, no tres sitios donde olvidarse de uno.
	go cuarentena.Mantener(pararHistorial, func(err error) {
		reg.Error("no se pudo volcar la cuarentena", "error", err)
	})
	go lista.Mantener(pararHistorial, func(err error) {
		reg.Error("no se pudo volcar la lista de bloqueos", "error", err)
	})
	// Los operadores conocidos, en la misma cadencia y por el mismo canal. Que
	// esto llegue a disco importa más de lo que parece: si se pierde, la
	// próxima vez que alguien de la familia abra el panel desde el móvil su
	// operador ya no consta y el guardia lo aparta.
	go operadores.Mantener(pararHistorial, func(err error) {
		reg.Error("no se pudieron volcar los operadores conocidos", "error", err)
	})
	// Los hallazgos comparten el mismo canal de parada que todo lo demás. Se
	// vuelcan en la misma cadencia aunque casi nunca cambien: el volcado sale
	// gratis cuando no hay nada sucio, y tener un ritmo propio solo añadiría un
	// sitio más donde olvidarse de cerrar.
	go hallazgos.Mantener(pararHistorial, func(err error) {
		reg.Error("no se pudieron volcar los hallazgos", "error", err)
	})
	go novedades.Mantener(pararHistorial, conexiones, cuarentena, lista, hallazgos, func(err error) {
		reg.Error("no se pudo volcar la marca de novedades", "error", err)
	})

	// La marca de avisos baja a disco en la misma cadencia y por el mismo canal
	// de parada que todo lo demás: con dos ritmos habría una ventana en la que
	// se avisa de algo, el nodo se reinicia y se vuelve a avisar de lo mismo.
	go registroAvisos.Mantener(pararHistorial, func(err error) {
		reg.Error("no se pudo volcar la marca de avisos", "error", err)
	})

	// Y la vigilancia, que es lo único de todo esto que ACTÚA sin que nadie
	// mire: cada minuto mira la conducta de la última hora y aparta a quien lo
	// merece. Se avisa al diario de cada apartado nuevo —no de cada renovación,
	// que sería una línea por minuto— porque una defensa que actúa sola y en
	// silencio es indistinguible de una que no actúa.
	//
	// DESDE ADR-0073 EL OBSERVADOR HACE DOS COSAS, y el orden importa: primero
	// deja constancia local, después decide qué sale del nodo. Los orígenes
	// llegan YA agregados —PorOrigen se calcula una sola vez dentro de
	// Vigilar—, así que evaluar avisos no cuesta un segundo recorrido.
	go seguridad.Vigilar(pararHistorial, historial, cuarentena,
		func(origenes []seguridad.Origen, nuevos []seguridad.Apartado, ahora time.Time) {
			for _, a := range nuevos {
				reg.Warn("origen apartado por conducta",
					"origen", a.IP.String(), "senal", a.Senal.String(),
					"hasta", a.Hasta.Format(time.RFC3339))
			}
			s.EvaluarAvisos(origenes, nuevos, ahora)
		})

	ctx, parar := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer parar()

	// LA ENTREGA DE AVISOS, en su propia goroutine. Encolar no bloquea nunca;
	// esperar al proveedor es trabajo de aquí y de nadie más (ADR-0073).
	go cola.Atender(ctx)

	// EL LATIDO AL TESTIGO EXTERNO — ADR-0074.
	//
	// Se construye aquí y no arriba porque su cuerpo lo compone el servidor,
	// que hasta ahora no existía. Sin latido configurado el nodo funciona
	// igual, pero se dice A GRITOS: sin él, que la Raspberry se apague no lo
	// detecta NADIE, y esa es justamente la carencia que este trabajo vino a
	// cerrar.
	if secretos.LatidoURL != "" {
		l, err := canal.NuevoLatido(secretos.LatidoURL, cfg.IntervaloLatido, s.CuerpoDelLatido, reg)
		if err != nil {
			return fmt.Errorf("latido al testigo externo: %w", err)
		}
		latido = l
		go latido.Mantener(ctx)
		reg.Info("latido al testigo externo activo",
			"intervalo_s", int(cfg.IntervaloLatido.Seconds()), "origen", origenSecretos)
	} else {
		reg.Warn("SIN TESTIGO EXTERNO: si este nodo deja de funcionar, nadie fuera se enterará",
			"configurar_con", "20_APROVISIONAMIENTO/20_avisos.sh")
	}

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

// prepararBaseGeoIP implementa «nasd --preparar-geoip». Lo llama el
// temporizador mensual de 18_geoip.sh, no una persona.
//
// SE AUTOCOMPRUEBA Y LO IMPRIME, y esa es la mitad que importa: una base
// preparada «sin errores» puede estar perfectamente vacía o desordenada. Al
// terminar resuelve cuatro direcciones cuya respuesta se conoce POR OTRA VÍA
// —la IP pública de este nodo y su AAAA, más dos resolutores DNS públicos— y
// las escribe. Así el operador ve el dato en vez de confiar en un «hecho».
func prepararBaseGeoIP(args []string) error {
	if len(args) != 3 {
		return fmt.Errorf("uso: nasd --preparar-geoip <salida> <ip2asn-v4.tsv> <ip2asn-v6.tsv>")
	}
	salida, rutaV4, rutaV6 := args[0], args[1], args[2]

	v4, err := os.Open(rutaV4)
	if err != nil {
		return err
	}
	defer v4.Close()
	v6, err := os.Open(rutaV6)
	if err != nil {
		return err
	}
	defer v6.Close()

	n, err := geoip.Preparar(v4, v6, salida)
	if err != nil {
		return err
	}
	fmt.Printf("base de país/operador escrita en %s: %d rangos\n", salida, n)

	// Un archivo con cuatro rangos se escribe «sin errores» igual que uno con
	// medio millón. Un mínimo grosero distingue «funcionó» de «no leyó nada».
	const minimoRazonable = 100_000
	if n < minimoRazonable {
		return fmt.Errorf("solo %d rangos, se esperaban más de %d: revise los TSV de entrada",
			n, minimoRazonable)
	}

	b, err := geoip.Abrir(salida)
	if err != nil {
		return fmt.Errorf("la base recién escrita no se puede abrir: %w", err)
	}
	defer b.Cerrar()

	fmt.Println("comprobación contra direcciones de respuesta conocida:")
	for _, ancla := range []struct{ ip, esperado string }{
		{"198.51.100.0", "la IP pública de este nodo (ADR-0044)"},
		{"3fff:2a0:101e:3d82::38", "el AAAA de este nodo"},
		{"8.8.8.8", "Google"},
		{"1.1.1.1", "Cloudflare"},
	} {
		ip, err := netip.ParseAddr(ancla.ip)
		if err != nil {
			return err
		}
		info, ok := b.Buscar(ip)
		if !ok {
			return fmt.Errorf("%s (%s) no resuelve: la base está mal", ancla.ip, ancla.esperado)
		}
		fmt.Printf("  %-24s AS%-7d %-3s %s\n", ancla.ip, info.ASN, info.Pais, info.Nombre)
	}
	return nil
}

// secretosDeAvisos son las tres credenciales de la capa de avisos (ADR-0073).
//
// Las TRES son secretos, y no del mismo calibre:
//
//	TelegramToken  quien lo tenga publica en ese chat y lee lo que se envíe AL
//	               BOT. No abre ninguna otra conversación del responsable.
//	LatidoURL      quien la tenga puede FALSIFICAR LATIDOS y mantener el
//	               testigo en verde con el nodo muerto. Es la que más daño hace
//	               si se filtra, y es exactamente el escenario del atacante con
//	               privilegios: ver la cabecera de canal/latido.go.
//	TelegramChat   por sí solo no hace nada.
type secretosDeAvisos struct {
	TelegramToken string
	TelegramChat  string
	LatidoURL     string
}

// leerSecretosDeAvisos obtiene el archivo que entrega systemd por
// LoadCredential=avisos, en un tmpfs privado del servicio.
//
// # MISMO MECANISMO QUE LA CREDENCIAL DE LA WEB, Y NO ES CASUALIDAD
//
// P4 del charter: el secreto no vive en el repositorio ni en el TOML. Aquí se
// reutiliza literalmente la vía de leerCredencial —$CREDENTIALS_DIRECTORY, con
// una variable de entorno como camino alternativo para desarrollo— en vez de
// inventar una segunda forma de entregar secretos a este mismo proceso.
//
// # QUE NO EXISTA NO ES UN ERROR, QUE ESTÉ ROTO SÍ
//
// Ausente significa «esta capa no está configurada», que es un estado válido:
// el nodo arranca y los avisos van al diario. Ilegible o mal formado significa
// que alguien QUISO configurarla y no funcionó, y eso se falla a gritos (P5):
// callarlo dejaría al responsable creyendo que le van a avisar cuando no.
//
// EL ARCHIVO NO SE REGISTRA NUNCA, ni su contenido ni sus valores. Lo único que
// sale al diario es de DÓNDE se leyó.
func leerSecretosDeAvisos() (secretosDeAvisos, string, error) {
	ruta := ""
	origen := ""
	if dir := os.Getenv("CREDENTIALS_DIRECTORY"); dir != "" {
		if candidata := filepath.Join(dir, "avisos"); existe(candidata) {
			ruta, origen = candidata, "systemd LoadCredential"
		}
	}
	if ruta == "" {
		if v := os.Getenv("NASD_AVISOS"); v != "" {
			ruta, origen = v, "NASD_AVISOS="+v
		}
	}
	if ruta == "" {
		return secretosDeAvisos{}, "", nil // no configurada
	}

	f, err := os.Open(ruta)
	if err != nil {
		return secretosDeAvisos{}, origen, fmt.Errorf("abrir %q: %w", ruta, err)
	}
	defer f.Close()

	v, err := config.LeerPares(f)
	if err != nil {
		return secretosDeAvisos{}, origen, fmt.Errorf("%s: %w", ruta, err)
	}
	sec := secretosDeAvisos{
		TelegramToken: strings.TrimSpace(v["telegram_token"]),
		TelegramChat:  strings.TrimSpace(v["telegram_chat"]),
		LatidoURL:     strings.TrimSpace(v["latido_url"]),
	}
	// Media configuración es la forma más cara de descubrir un error, porque
	// arranca y parece funcionar. Mismo criterio que TLS en config.validar:
	// o están las dos o no está ninguna.
	if (sec.TelegramToken == "") != (sec.TelegramChat == "") {
		return sec, origen, fmt.Errorf(
			"%s: telegram_token y telegram_chat se configuran juntos o ninguno", ruta)
	}
	return sec, origen, nil
}

func existe(ruta string) bool {
	_, err := os.Stat(ruta)
	return err == nil
}
