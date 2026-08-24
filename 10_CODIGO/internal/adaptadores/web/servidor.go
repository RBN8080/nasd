// Package web es el adaptador primario HTTP.
//
// No toca el disco directamente: todo pasa por el puerto almacen.Almacen.
// Jamás construye una ruta concatenando cadenas: la única puerta es
// almacen.NuevaRuta (ADR-0014, 04_SEGURIDAD.md §2).
package web

import (
	"context"
	"embed"
	"fmt"
	"html/template"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"nasd/internal/almacen"
	"nasd/internal/autenticacion"
	"nasd/internal/geoip"
	"nasd/internal/metricas"
	"nasd/internal/seguridad"
)

//go:embed plantillas/*.html estatico/*
var recursos embed.FS

// Servidor arma el adaptador HTTP completo.
type Servidor struct {
	// almacenRaiz ve el volumen ENTERO: es el del superusuario.
	//
	// NO SE USA PARA ATENDER PETICIONES. Lo que un manejador ve llega por
	// parámetro, ya acotado a quien pregunta (ver conAlmacen en sesion.go).
	// Aquí queda para lo que es de la casa y no de nadie: revisar al arrancar
	// qué subidas quedaron a medias y expirarlas en el ciclo de mantenimiento.
	almacenRaiz almacen.Almacen
	// abrirAlmacen devuelve la vista del volumen acotada a un usuario. Es una
	// función y no el adaptador entero para que este paquete no dependa de
	// fsposix: quien las une es la raíz de composición (cmd/nasd).
	abrirAlmacen func(usuario string) (almacen.Almacen, error)
	// promoverUsuario saca la carpeta de una cuenta de homeUsers/ y la deja
	// en la raíz del superusuario, con el mismo nombre. Lo llama SOLO la
	// baja (panel.go), justo antes de retirar la cuenta del registro —
	// corrección del 06/08: la carpeta debe quedar a la vista, no perdida
	// dentro de un sitio que el propio panel deja de mostrar.
	promoverUsuario func(nombre string) error

	reg              *slog.Logger
	plantillas       *template.Template
	plazoInactividad time.Duration
	subidas          *registroDeSubidas

	// Autenticación — RF-15. credencial es la línea derivada, NUNCA la
	// contraseña en claro: esta nunca entra en el proceso más allá del
	// instante de verificarla.
	//
	// credencial es la del SUPERUSUARIO y no está en usuarios: su llave vive
	// en un archivo que el servicio no puede escribir, y el registro sí es
	// escribible. La asimetría es a propósito (ADR-0055).
	credencial     string
	usuarios       *autenticacion.Registro
	sesiones       *autenticacion.Sesiones
	limitador      *limitadorAcceso
	duracionSesion time.Duration
	// metricas guarda el uso de disco medido bajo demanda por cuenta —
	// P-4, etapa 3. Se lee en cada carga de /administracion (barato: ya
	// está en memoria) y se escribe SOLO al pulsar «Refrescar métricas»
	// (caro: recorre el árbol de cada cuenta), nunca al servir la página.
	metricas *metricas.Registro

	// Observabilidad — Fase 4, charter §8.
	contadores *contadores
	// seguridad guarda los rechazos con su motivo — el detalle que el
	// contador único de contadores.cliente no podía dar. Acotado por
	// construcción: es un anillo, no puede crecer.
	seguridad *seguridad.Anillo
	// conexiones guarda toda conexión entrante de INTERNET, hable HTTP o no.
	// Es lo único que ve lo que muere en el saludo TLS, que es donde murieron
	// 19 de los 20 sondeos del 2026-08-16 — ver internal/seguridad/conexiones.go.
	conexiones *seguridad.Conexiones
	// cuarentena decide a quién se le cierra la puerta sin que nadie mire.
	// Se consulta en anotarConexion, que es por donde pasa toda conexión de
	// los dos servidores.
	cuarentena *seguridad.Cuarentena
	// lista son los bloqueos manuales. Se consulta en el mismo sitio que la
	// cuarentena y por la misma puerta.
	lista *seguridad.Lista
	// novedades cuenta lo que ha pasado desde la última visita al panel.
	novedades *seguridad.Novedades
	// hallazgos guarda las rutas que este NAS no publica y aun así atendió con
	// contenido. Es la única estructura del panel que habla del SERVIDOR y no
	// de quien pide — ver internal/seguridad/hallazgos.go.
	hallazgos *seguridad.Hallazgos
	// cierresFallidos son los net.Conn.Close() que no se pudieron completar en
	// la puerta.
	//
	// Existe para que «Frenados» pueda ser verdad SIN convertir el diario en el
	// amplificador de quien insiste: el volumen lo lleva este contador y el
	// diario recibe una sola línea por arranque (anotarCierreFallido). Es
	// atómico y no lleva candado por lo mismo que los de contadores.go: es un
	// indicador, no contabilidad.
	cierresFallidos atomic.Int64
	// geo resuelve un origen de Internet a su país y su operador. PUEDE SER
	// NULA: sin base instalada el panel funciona igual, solo que sin ese dato.
	geo *geoip.BaseDatos
	// rutaToques es el historial que deja nas-sensor (RF-33, ADR-0066). Es una
	// RUTA y no una estructura porque nasd no es quien escribe: lee el archivo
	// al pintar la página y no guarda nada entre cargas. PUEDE ir vacía —nodo
	// sin sensor—, y entonces el panel esconde esas columnas.
	rutaToques string
	// muestreador alimenta el flujo en vivo de /estado (ADR-0051). Solo mide
	// mientras haya alguien mirando: sin espectadores no cuesta nada.
	muestreador *muestreador[marcoVivo]
	// cuentas alimenta el flujo en vivo de /administracion (P-7, ADR-0056).
	// Es OTRA instancia del mismo motor, no otro motor: así el panel no paga
	// las lecturas de /proc que solo necesita /estado, y /estado no paga el
	// recorrido del registro de usuarios. Cada página arranca y para su propio
	// bucle según quién la esté mirando.
	cuentas *muestreador[marcoCuentas]
	// veredictosPrevios recuerda el último veredicto de cada indicador para
	// alertar solo en los CAMBIOS. Lo toca únicamente la goroutine de
	// mantenimiento: ver anunciar().
	veredictosPrevios map[string]veredicto
	// volumen es el punto de montaje del disco de datos. Se guarda para poder
	// medirlo; el adaptador NUNCA lo usa para construir rutas —esa puerta es
	// almacen.NuevaRuta y no hay otra (ADR-0014)—.
	volumen string

	// dirMiniaturas es donde se cachean las miniaturas EXIF (miniatura.go).
	// VACÍO significa «función no configurada»: servirMiniatura responde 404
	// sin intentar nada, el mismo tratamiento que geo cuando es nulo — una
	// pieza opcional se apaga sola, no rompe el arranque (P5).
	dirMiniaturas string
	// miniaturaSem es un semáforo de CAPACIDAD 1, no un límite arbitrario.
	// MemoryMax=192M en 05_instalar_servicio.sh es del cgroup ENTERO,
	// nasd y sus hijos juntos: dos generaciones a la vez ya arriesgan el
	// techo, así que la concurrencia no se ajusta, se elimina.
	miniaturaSem chan struct{}
	// miniaturaFallidas recuerda, un rato, qué claves acaban de fallar —
	// mismo patrón que limitadorAcceso (sesion.go), purgado por el mismo
	// ciclo de 5 min (mantenimiento.go). Sin esto, un archivo sin miniatura
	// aprovechable lanzaría un subproceso en CADA recarga de su carpeta.
	miniaturaFallidas *fallosMiniatura
}

type Opciones struct {
	Almacen almacen.Almacen
	// AlmacenDe acota el volumen a la carpeta de un usuario (ADR-0055). La
	// pone la raíz de composición; sin ella no podría entrar nadie que no sea
	// el superusuario, así que Nuevo la exige.
	AlmacenDe func(usuario string) (almacen.Almacen, error)
	// PromoverUsuario saca la carpeta de una cuenta de homeUsers/ al darla de
	// baja (etapa 2, corrección del 06/08). Sin ella, la baja quitaría el
	// acceso pero la carpeta seguiría viviendo en un sitio que el panel ya
	// no enseña — el defecto que esto corrige. Nuevo la exige por lo mismo
	// que exige AlmacenDe: la alternativa es una baja que se ve completa y
	// no lo está.
	PromoverUsuario  func(nombre string) error
	Registro         *slog.Logger
	PlazoInactividad time.Duration
	// Credencial es la línea derivada que produce «nasd --generar-credencial».
	// Si está vacía, Nuevo falla: NO existe un modo sin autenticar (RF-15).
	Credencial string
	// Usuarios es el registro de cuentas. Puede estar VACÍO —un nodo recién
	// instalado no tiene usuarios— pero no puede faltar: sin él, dar de alta a
	// alguien no serviría de nada y el fallo aparecería al intentar entrar.
	Usuarios       *autenticacion.Registro
	DuracionSesion time.Duration
	// InactividadSesion — ADR-0059. Tope deslizante de la sesión, aparte del
	// absoluto de DuracionSesion. Cero desactiva ese segundo reloj; ver
	// autenticacion.NuevasSesiones.
	InactividadSesion time.Duration
	// Volumen es el punto de montaje del disco de datos (ADR-0019), necesario
	// para informar de su ocupación y su salud en /estado.
	Volumen string
	// DirMiniaturas es donde se cachean las miniaturas EXIF (miniatura.go),
	// normalmente cfg.RutaMiniaturas(). A DIFERENCIA de Metricas/Seguridad,
	// PUEDE ir vacío sin que Nuevo falle: es una pieza opcional —mismo trato
	// que GeoIP— y exigirla habría roto cada prueba de este paquete que
	// construye Opciones sin conocer este campo.
	DirMiniaturas string
	// Metricas guarda el uso de disco medido bajo demanda por cuenta —
	// P-4, etapa 3. Puede estar VACÍO —un nodo recién instalado, o donde
	// nadie ha pulsado «Refrescar métricas» todavía— pero no puede faltar,
	// por la misma razón que Usuarios: sin él, /administracion no tendría
	// dónde leer ni dónde publicar lo que mida.
	Metricas *metricas.Registro
	// Seguridad es el historial de rechazos. Obligatorio por el mismo
	// criterio que las anteriores (P5): si faltara, el servidor arrancaría
	// entero y sin ruido, y el defecto solo aparecería al abrir un panel que
	// diría «no ha pasado nada» — una degradación silenciosa, y encima en la
	// pieza cuyo único trabajo es no callarse.
	Seguridad *seguridad.Anillo
	// RutaToques es el historial de nas-sensor. A DIFERENCIA de Seguridad y
	// Conexiones puede ir VACÍA sin que Nuevo falle: el sensor es una pieza
	// opcional, igual que GeoIP, y exigirla habría roto cada prueba de este
	// paquete que construye Opciones sin conocer este campo.
	RutaToques string
	// Conexiones es el historial de conexiones entrantes de Internet.
	// Obligatorio por el MISMO criterio que Seguridad, y con más motivo: si
	// faltara, el panel volvería a enseñar «1» donde hubo 20 y lo haría sin
	// una sola línea en el diario. Es la degradación silenciosa que esta
	// pieza existe para cerrar, así que no puede ser opcional.
	Conexiones *seguridad.Conexiones
	// Novedades es la marca de «hay algo que no había visto» de la barra.
	// Obligatoria: si faltara, la marca no se encendería nunca y nadie se
	// enteraría de que no se enciende.
	Novedades *seguridad.Novedades
	// Hallazgos guarda las respuestas inesperadas del propio servidor.
	//
	// OBLIGATORIA, y con el argumento más fuerte de toda esta estructura: es lo
	// único del panel que no se puede volver a observar. Un rechazo perdido lo
	// repite el siguiente escáner; un «este nodo devolvió 200 en /.git/config»
	// que nadie anotó no vuelve. Si pudiera faltar, el panel diría «ninguna
	// respuesta inesperada» sin haber mirado, que es la degradación silenciosa
	// que P5 obliga a convertir en un arranque fallido.
	Hallazgos *seguridad.Hallazgos
	// Lista son los bloqueos puestos a mano. Obligatoria por el mismo criterio
	// que Cuarentena: si faltara, el panel ofrecería un botón de bloquear que
	// no bloquea nada, que es peor que no ofrecerlo.
	Lista *seguridad.Lista
	// Cuarentena es la lista de direcciones apartadas por conducta.
	//
	// OBLIGATORIA, por el mismo criterio que Seguridad y Conexiones: es un
	// CONTROL, no un adorno. Si pudiera faltar, el nodo dejaría de defenderse
	// solo por la noche y no habría una sola línea en ningún sitio que lo
	// dijera — la degradación silenciosa que P5 obliga a convertir en un
	// arranque fallido.
	Cuarentena *seguridad.Cuarentena
	// GeoIP resuelve un origen de Internet a su país y su operador.
	//
	// ES LA ÚNICA DEPENDENCIA OPCIONAL DE TODA ESTA ESTRUCTURA, y a propósito:
	// un nodo al que todavía no se le ha instalado la base funciona
	// exactamente igual, solo que el panel no dice de dónde viene cada
	// origen. Nula es un valor válido —geoip.BaseDatos admite receptor nulo—
	// y por eso Nuevo NO la exige, al revés que Usuarios, Metricas o
	// Seguridad, cuya ausencia sí rompería algo en silencio.
	GeoIP *geoip.BaseDatos
}

func Nuevo(o Opciones) (*Servidor, error) {
	t, err := template.New("").Funcs(funciones()).ParseFS(recursos, "plantillas/*.html")
	if err != nil {
		return nil, err
	}
	// P5: las dependencias obligatorias se comprueban al construir, no se
	// desreferencian a ciegas. Un almacén nulo reventaba más abajo con un
	// pánico opaco; lo destapó una prueba, no producción.
	if o.Almacen == nil {
		return nil, fmt.Errorf("web.Nuevo: falta el almacén")
	}
	if o.Registro == nil {
		return nil, fmt.Errorf("web.Nuevo: falta el registro")
	}
	// Las dos piezas del acceso por usuario, exigidas aquí y no descubiertas
	// al primer intento de entrar. Sin AlmacenDe, un usuario válido entraría y
	// se encontraría un error interno; sin Usuarios, ninguna cuenta existiría
	// y la única pista sería «contraseña incorrecta» para una contraseña
	// buena. Las dos son degradaciones silenciosas de las que P5 prohíbe.
	if o.AlmacenDe == nil {
		return nil, fmt.Errorf("web.Nuevo: falta AlmacenDe (ADR-0055)")
	}
	if o.Usuarios == nil {
		return nil, fmt.Errorf("web.Nuevo: falta el registro de usuarios (ADR-0055)")
	}
	// Sin esto, dar de baja quitaría el acceso y dejaría la carpeta perdida
	// dentro de homeUsers/ — el defecto de la etapa 2 que se corrigió el
	// 06/08. Exigirla aquí es la misma lógica que AlmacenDe y Usuarios.
	if o.PromoverUsuario == nil {
		return nil, fmt.Errorf("web.Nuevo: falta PromoverUsuario (P-4, etapa 2)")
	}
	if o.Metricas == nil {
		return nil, fmt.Errorf("web.Nuevo: falta Metricas (P-4, etapa 3)")
	}
	if o.Seguridad == nil {
		return nil, fmt.Errorf("web.Nuevo: falta Seguridad (panel de seguridad, etapa 1)")
	}
	if o.Conexiones == nil {
		return nil, fmt.Errorf("web.Nuevo: falta Conexiones (panel de seguridad, ADR-0064)")
	}
	if o.Cuarentena == nil {
		return nil, fmt.Errorf("web.Nuevo: falta Cuarentena (respuesta automática por conducta)")
	}
	if o.Lista == nil {
		return nil, fmt.Errorf("web.Nuevo: falta Lista (bloqueos puestos a mano)")
	}
	if o.Novedades == nil {
		return nil, fmt.Errorf("web.Nuevo: falta Novedades (la marca de la barra)")
	}
	if o.Hallazgos == nil {
		return nil, fmt.Errorf("web.Nuevo: falta Hallazgos (respuestas inesperadas del servidor)")
	}

	// P5: sin credencial no se arranca. Un modo «sin autenticar» dejaría el
	// disco entero administrable por cualquiera en la LAN, que es justo lo
	// que RN-06 ordena evitar. Mejor no arrancar que arrancar inseguro.
	if err := autenticacion.Valida(o.Credencial); err != nil {
		return nil, fmt.Errorf("credencial de la web: %w", err)
	}

	s := &Servidor{
		almacenRaiz:       o.Almacen,
		abrirAlmacen:      o.AlmacenDe,
		promoverUsuario:   o.PromoverUsuario,
		usuarios:          o.Usuarios,
		reg:               o.Registro,
		plantillas:        t,
		plazoInactividad:  o.PlazoInactividad,
		subidas:           nuevoRegistroDeSubidas(),
		credencial:        o.Credencial,
		sesiones:          autenticacion.NuevasSesiones(o.DuracionSesion, o.InactividadSesion),
		limitador:         nuevoLimitador(),
		duracionSesion:    o.DuracionSesion,
		contadores:        nuevosContadores(),
		veredictosPrevios: make(map[string]veredicto),
		volumen:           o.Volumen,
		metricas:          o.Metricas,
		seguridad:         o.Seguridad,
		conexiones:        o.Conexiones,
		cuarentena:        o.Cuarentena,
		lista:             o.Lista,
		novedades:         o.Novedades,
		hallazgos:         o.Hallazgos,
		rutaToques:        o.RutaToques,
		geo:               o.GeoIP,
		dirMiniaturas:     o.DirMiniaturas,
		miniaturaSem:      make(chan struct{}, 1),
		miniaturaFallidas: nuevoFallosMiniatura(),
	}
	s.muestreador = nuevoMuestreador(s.abrirLectorVivo)
	s.cuentas = nuevoMuestreador(s.abrirLectorCuentas)

	// Al arrancar se mira qué subidas dejó a medias el proceso anterior.
	// No se reabren aquí —eso ocurre al primer HEAD o PATCH— pero se informa,
	// porque un montón de parciales acumulados es el síntoma de ADR-0029 y
	// debe verse en el registro sin tener que ir a mirar el disco (P7).
	if ps, err := s.almacenRaiz.Reanudables(context.Background()); err != nil {
		o.Registro.Warn("no se pudo revisar las subidas a medias", "error", err)
	} else if len(ps) > 0 {
		var bytes int64
		for _, p := range ps {
			bytes += p.Escrito
		}
		o.Registro.Info("subidas a medias encontradas al arrancar",
			"cuantas", len(ps), "bytes", bytes)
	}

	return s, nil
}

func (s *Servidor) Rutas() http.Handler {
	// Lo protegido: TODO menos el formulario de acceso y los assets que ese
	// formulario necesita para dibujarse.
	protegido := http.NewServeMux()
	mux := http.NewServeMux()

	mux.HandleFunc("GET /acceso", s.mostrarAcceso)
	mux.HandleFunc("POST /acceso", s.procesarAcceso)
	mux.HandleFunc("POST /salir", s.salir)
	mux.HandleFunc("GET /estatico/", servirEstatico)
	mux.Handle("/", s.exigirSesion(protegido))

	// TODO LO QUE TOCA ARCHIVOS VA ENVUELTO EN conAlmacen, que le entrega al
	// manejador el volumen ya acotado a quien pregunta (ADR-0055). Esta tabla
	// es, además, la lista de lo que puede ver archivos: lo que no aparezca
	// aquí envuelto, no los ve.
	protegido.HandleFunc("GET /{$}", s.conAlmacen(s.verListado))
	protegido.HandleFunc("GET /ver/{ruta...}", s.conAlmacen(s.verListado))
	protegido.HandleFunc("GET /descargar/{ruta...}", s.conAlmacen(s.descargar))

	// Apertura en el navegador — RF-25, ADR-0052. Son DOS extremos y no uno:
	// /abrir entrega la página del visor y /contenido los bytes pasivos. Ver
	// apertura.go para por qué esa separación es lo que hace cumplible el
	// mensaje de RF-25. /descargar queda intacto y sigue siendo el único que
	// ordena «attachment».
	protegido.HandleFunc("GET /abrir/{ruta...}", s.conAlmacen(s.abrirEnNavegador))
	protegido.HandleFunc("GET /contenido/{ruta...}", s.conAlmacen(s.servirContenido))
	// Miniatura EXIF incrustada — rector §7.nonies.bis, miniatura.go. Cuelga
	// del MISMO conAlmacen que /contenido: hereda sesión y aislamiento por
	// usuario (ADR-0055) sin una línea nueva de control de acceso.
	protegido.HandleFunc("GET /miniatura/{ruta...}", s.conAlmacen(s.servirMiniatura))
	protegido.HandleFunc("POST /subir", s.conAlmacen(s.subirMultipart))
	protegido.HandleFunc("POST /directorio", s.conAlmacen(s.crearDirectorio))

	// Administración — Fase 3. Después de RF-15, por RN-06.
	//
	// MOVER TIENE EXTREMO PROPIO desde el 2026-08-06 (ADR-0054).
	// Compartirlo con renombrar obligaba a adivinar la intención mirando si
	// el texto llevaba una barra, y en uso real se adivinó mal. /renombrar
	// conserva SOLO el renombrado, que es lo que su nombre dice.
	//
	// LAS CINCO VAN ENVUELTAS EN soloDesdeDentro: al superusuario se le niegan
	// desde Internet, porque la confirmación de RF-18 es «la única barrera del
	// sistema» y desde fuera está a una contraseña de distancia (ver sesion.go).
	// Un usuario normal pasa de largo por esa envoltura y no nota nada.
	//
	// LAS PÁGINAS DE PASO TAMBIÉN, y no es celo: negar en el POST y no en el
	// GET dejaría rellenar el formulario entero para fallar al final, que es
	// la forma de que una regla correcta se lea como una avería.
	protegido.HandleFunc("GET /mover/{ruta...}", s.soloDesdeDentro(s.conAlmacen(s.verMover)))          // RF-17, paso 1
	protegido.HandleFunc("POST /mover", s.soloDesdeDentro(s.conAlmacen(s.mover)))                      // RF-17, paso 2
	protegido.HandleFunc("POST /renombrar", s.soloDesdeDentro(s.conAlmacen(s.renombrar)))              // RF-16
	protegido.HandleFunc("GET /borrar/{ruta...}", s.soloDesdeDentro(s.conAlmacen(s.confirmarBorrado))) // RF-18, paso 1
	protegido.HandleFunc("POST /borrar", s.soloDesdeDentro(s.conAlmacen(s.borrar)))                    // RF-18, paso 2

	// Observabilidad — Fase 4, RF-24. Va DENTRO de lo protegido: ver estado.go.
	//
	// Y DESDE ADR-0055, SOLO PARA EL SUPERUSUARIO, por decisión del
	// responsable. El motivo se sostiene solo: publica temperatura, capacidad,
	// ritmo de uso y subidas a medias —de TODO el nodo, no de la carpeta de
	// quien mira—, así que a un usuario normal no le informa de nada suyo y a
	// cambio le entrega reconocimiento del sistema entero.
	protegido.HandleFunc("GET /estado", s.soloSuperusuario(s.verEstado))
	// El flujo en vivo de esa misma pantalla — ADR-0051. Se cierra IGUAL y por
	// separado: publica exactamente lo mismo y encima de forma continua.
	// Dejarlo fuera abriría por la puerta de al lado lo que la línea de arriba
	// cierra, que es la clase de asimetría que D-21 obliga a comprobar vía por
	// vía en lugar de darla por hecha.
	protegido.HandleFunc("GET /estado/flujo", s.soloSuperusuario(s.flujoDeEstado))

	// Panel de administración — P-4, etapa 2 (ADR-0055). SOLO el
	// superusuario, misma envoltura que /estado. Ninguna de las cuatro
	// primeras toca archivos —dar de alta o de baja una cuenta no es tocar
	// el disco—, así que ninguna lleva conAlmacen; refrescarMetricas SÍ lee
	// disco (P-4, etapa 3), pero por su propia puerta —s.abrirAlmacen, una
	// vez por cuenta— y no por el almacén acotado a quien pregunta.
	//
	// EL PANEL ENTERO VA TAMBIÉN EN soloDesdeDentro, no solo el alta y la baja.
	// Decidir quién puede entrar es la operación más consecuente de esta web
	// —lo dice ya 04_SEGURIDAD §2.ter al exigirle reautenticación—, así que la
	// autoridad que la ejerce se acota igual que la que destruye. Y se envuelve
	// vía por vía, incluidas la lectura y el refresco: dejar una sola abierta
	// sería la asimetría que D-21 obliga a comprobar en vez de suponer.
	protegido.HandleFunc("GET /administracion", s.soloSuperusuario(s.soloDesdeDentro(s.verAdministracion)))
	protegido.HandleFunc("POST /administracion/alta", s.soloSuperusuario(s.soloDesdeDentro(s.altaUsuario)))
	protegido.HandleFunc("GET /administracion/baja/{nombre}", s.soloSuperusuario(s.soloDesdeDentro(s.confirmarBaja)))
	protegido.HandleFunc("POST /administracion/baja", s.soloSuperusuario(s.soloDesdeDentro(s.bajaUsuario)))
	protegido.HandleFunc("POST /administracion/refrescar", s.soloSuperusuario(s.soloDesdeDentro(s.refrescarMetricas)))
	// El flujo en vivo del panel — P-7, ADR-0056. Se cierra igual que la
	// página que alimenta y por el mismo motivo que /estado/flujo: publica
	// exactamente lo mismo y encima de forma continua, así que dejarlo fuera
	// abriría por la puerta de al lado lo que la línea de /administracion
	// cierra (D-21).
	protegido.HandleFunc("GET /administracion/flujo", s.soloSuperusuario(s.soloDesdeDentro(s.flujoDeCuentas)))

	// Panel de seguridad — etapa 2. MISMA envoltura que /estado y
	// /administracion, y aquí el motivo es aún más fuerte: publica las
	// direcciones de origen de todo el que ha tocado el nodo.
	//
	// UNA SOLA RUTA Y NINGÚN FLUJO, al contrario que las dos anteriores: no
	// hay nada que cerrar «por la puerta de al lado» (D-21) porque no existe
	// una segunda vía que publique lo mismo. Si algún día se le añade flujo,
	// entra aquí envuelto igual y el mismo día.
	protegido.HandleFunc("GET /seguridad", s.soloSuperusuario(s.verSeguridad))
	// La primera ACCIÓN de este panel: soltar a quien el nodo apartó solo.
	// No lleva soloDesdeDentro a propósito —ver soltarApartado—: es reversible
	// y su caso de uso es justo el de alguien que está fuera de casa.
	protegido.HandleFunc("POST /seguridad/soltar", s.soloSuperusuario(s.soltarApartado))
	// El bloqueo manual: proponer, aplicar y retirar. Tampoco llevan
	// soloDesdeDentro —ver soltarApartado— y las tres exigen testigo CSRF
	// porque las tres cambian algo.
	protegido.HandleFunc("GET /seguridad/bloquear", s.soloSuperusuario(s.verBloqueo))
	protegido.HandleFunc("POST /seguridad/bloquear", s.soloSuperusuario(s.bloquear))
	protegido.HandleFunc("POST /seguridad/retirar", s.soloSuperusuario(s.retirarBloqueo))

	// Núcleo del protocolo tus — ADR-0027.
	protegido.HandleFunc("POST /subidas", s.conAlmacen(s.tusCrear))
	protegido.HandleFunc("HEAD /subidas/{id}", s.conAlmacen(s.tusEstado))
	protegido.HandleFunc("PATCH /subidas/{id}", s.conAlmacen(s.tusEnviar))
	protegido.HandleFunc("DELETE /subidas/{id}", s.conAlmacen(s.tusDescartar))

	return s.conRegistro(s.conCabecerasSeguridad(mux))
}

// conCabecerasSeguridad fija cabeceras de aislamiento en TODA la web propia
// del NAS — ADR-0060, tras el pentest externo del 2026-08-13.
//
// Hasta ahora solo /contenido las llevaba (ADR-0052): el NAS se defendía del
// contenido SUBIDO pero no había declarado política sobre sus propias
// páginas. El caso concreto que esto cierra es clickjacking sobre
// /administracion, donde se dan de alta y de baja cuentas.
//
// SE FIJAN ANTES de llamar al manejador siguiente, a propósito: un manejador
// que ponga sus propias cabeceras (Set, no Add) GANA, porque las escribe
// después. Dos manejadores dependen de esto para relajar EXACTAMENTE un
// campo sin heredar una CSP casi vacía por sobrescribir el valor entero:
//   - renderVisor (apertura.go) necesita object-src 'self' — es la única
//     página con un <object> (el PDF, visor.html).
//   - servirContenido (apertura.go) relaja el framing a SAMEORIGIN, porque
//     visor.html embebe /contenido del propio origen en <object> e <iframe>;
//     conserva además su CSP más estricta, «default-src 'none'; sandbox»,
//     sin que esta la pise.
func (s *Servidor) conCabecerasSeguridad(siguiente http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", cspBase("'none'"))
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		siguiente.ServeHTTP(w, r)
	})
}

// cspBase es la política por defecto de toda la web propia, con object-src
// parametrizado: es el único campo que una página necesita relajar (el
// visor de PDF), y una función evita que esa copia y la de aquí diverjan
// solas con el tiempo si alguien cambia una y no la otra.
func cspBase(objectSrc string) string {
	return "default-src 'self'; script-src 'self'; style-src 'self'; " +
		"img-src 'self'; frame-ancestors 'none'; base-uri 'none'; " +
		"form-action 'self'; object-src " + objectSrc
}

// HTTPServer construye el http.Server con los plazos de ADR-0026.
//
// ATENCIÓN — ReadTimeout y WriteTimeout se dejan SIN FIJAR A PROPÓSITO.
// No es un descuido, y no debe «arreglarse»:
//
//	Son plazos ABSOLUTOS. Un WriteTimeout de 30 s corta cualquier descarga
//	de más de ~600 MB a 20 MB/s, y RF-09 exige 4 GB íntegros — a 31 MB/s
//	son ~2.2 minutos de escritura continua, y bastante más por WiFi.
//	Cualquier valor compatible con eso sería tan largo que ya no protegería
//	de nada.
//
// RNF-12 (plazo declarado en toda E/S) se cumple con un plazo POR ACTIVIDAD,
// renovado con cada avance: ver plazos.go. Eso sí distingue una transferencia
// legítima y lenta de una conexión colgada.
func (s *Servidor) HTTPServer(direccion string, puerto int) *http.Server {
	return &http.Server{
		Addr:              net.JoinHostPort(direccion, strconv.Itoa(puerto)),
		Handler:           s.Rutas(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 16,
		ErrorLog:          slog.NewLogLogger(s.reg.Handler(), slog.LevelWarn),
		// ConnState ve lo que conRegistro NO PUEDE ver: una conexión que se
		// acepta y muere en el saludo TLS nunca produce una petición HTTP, así
		// que nunca llega al middleware. Ver anotarConexion.
		ConnState: s.anotarConexion,
	}
}

// conRegistro emite una entrada estructurada por petición (RNF-13).
//
// 04_SEGURIDAD.md §6: se registran rutas —RF-19 las exige— pero nunca
// cabeceras de autorización ni cookies.
func (s *Servidor) conRegistro(siguiente http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inicio := time.Now()
		cap := &capturaDeEstado{ResponseWriter: w, estado: http.StatusOK}
		r, marcador := conMarcador(r)
		siguiente.ServeHTTP(cap, r)
		duracion := time.Since(inicio)

		// Los indicadores se anotan AQUÍ y no en cada manejador, a propósito:
		// así ninguna ruta futura puede quedarse fuera de la medición por
		// olvido. Es el mismo motivo por el que el registro vive aquí.
		s.contadores.anotarRespuesta(cap.estado, cap.bytes, duracion,
			clasificar(r.Method, r.URL.Path))

		// Y el historial de seguridad, por ese MISMO motivo y en ese mismo
		// sitio: un rechazo futuro que nadie clasifique aparecerá igualmente,
		// como «desconocido», en vez de desaparecer.
		//
		// El corte es «>= 400» y no una lista de rutas o de motivos, para que
		// coincida exactamente con lo que el contador de contadores.cliente
		// lleva contando desde la Fase 4: este historial explica ESE número,
		// y si contaran cosas distintas el panel se contradiría con /estado.
		if cap.estado >= 400 {
			s.anotarRechazo(r, marcador, cap.estado, inicio)
		}

		// LA OTRA MITAD, Y LA QUE FALTABA: una respuesta CON CONTENIDO en una
		// ruta que este NAS no publica.
		//
		// El corte de arriba —«>= 400»— es correcto para lo que aquel anillo
		// es, y por construcción deja fuera el caso más grave que este servidor
		// puede producir: «/.git/config → 200». Un 404 ahí es un sondeo y no
		// pasó nada; un 200 dice algo del NODO y no de quien pidió. Van a sitios
		// distintos porque son cosas distintas — ver seguridad/hallazgos.go.
		//
		// El coste en el camino de cada petición es dos comparaciones de
		// enteros para casi todo lo que se sirve: RespuestaInesperada empieza
		// por el estado y descarta ahí mismo cualquier cosa que no sea 200 ni
		// 206, y a continuación los prefijos del espacio del usuario, que es
		// por donde salen las descargas y las miniaturas.
		if seguridad.RespuestaInesperada(r.URL.Path, cap.estado) {
			s.anotarHallazgo(r, cap.estado, inicio)
		}

		// «origen» faltaba, y era el agujero: de todos los 4xx del nodo no
		// quedaba constancia de QUIÉN los pedía en ningún sitio —solo el
		// acceso fallido, el cierre de sesión y las acciones del panel lo
		// anotaban—. Sin eso, ninguna clasificación posterior es
		// reconstruible a partir del diario, que por ADR-0037 es la fuente
		// persistente de la verdad.
		s.reg.Info("peticion",
			"metodo", r.Method,
			"ruta", r.URL.Path,
			"estado", cap.estado,
			"bytes", cap.bytes,
			"ms", duracion.Milliseconds(),
			"origen", origenDe(r),
		)
	})
}

// capturaDeEstado mira qué se respondió sin cambiar lo que se responde.
//
// # POR QUÉ NO BASTA CON GUARDAR EL ÚLTIMO WriteHeader
//
// Así estaba, y divergía del cliente en un caso REAL. Medido contra un
// net/http de verdad el 2026-08-24:
//
//	WriteHeader(404); WriteHeader(500)  ->  el cliente recibe 404
//
// net/http descarta la segunda llamada —deja el aviso «superfluous
// response.WriteHeader call» en el diario— porque la cabecera ya se envió. El
// envoltorio, en cambio, se quedaba con la última y anotaba 500. Consecuencia:
// el panel de seguridad y los contadores de /estado habrían clasificado como
// error del servidor una respuesta que el cliente vio como «no existe», y el
// SLI-1 —que cuenta 5xx— se habría manchado con un rechazo perfectamente
// normal. Un manejador que llama dos veces es un descuido; que el registro lo
// convierta en otro suceso es un defecto de esta pieza.
//
// La regla ahora es la de net/http, no una propia: manda el PRIMER estado que
// fija la cabecera, y a partir de ahí no cambia.
//
// # Y POR QUÉ «el primero» NO ES SUFICIENTE POR SÍ SOLO
//
// Porque las respuestas informativas 1xx NO fijan la cabecera: net/http las
// envía y sigue esperando el estado final. Con la regla ingenua del primero,
// un «WriteHeader(103); WriteHeader(404)» quedaría registrado como 103
// mientras el cliente recibe 404 — la misma clase de mentira, por el otro
// lado. Comprobado en la misma medición: ahí el cliente recibe 404.
//
// La única 1xx que SÍ es final es 101 (Switching Protocols), y por eso lleva
// su excepción, exactamente igual que en net/http.
type capturaDeEstado struct {
	http.ResponseWriter
	estado int
	bytes  int64
	// fijado dice si la cabecera ya quedó comprometida. Es lo que distingue
	// «nadie ha llamado a WriteHeader todavía» —y entonces el 200 inicial es
	// una previsión— de «ya se envió»: sin esta marca no se puede saber cuál
	// de las dos cosas significa el valor de estado.
	fijado bool
}

func (c *capturaDeEstado) WriteHeader(e int) {
	// SE REENVÍA SIEMPRE, incluso lo que aquí se ignora. Este envoltorio
	// observa; decidir qué hacer con una segunda llamada es de net/http, y
	// tragársela aquí cambiaría el comportamiento del servidor —y silenciaría
	// su aviso de «superfluous»— para arreglar un problema que es de medición.
	defer c.ResponseWriter.WriteHeader(e)

	if c.fijado {
		return
	}
	// 1xx informativo: no fija nada, el estado final llega después. 101 sí es
	// final; misma excepción y mismo motivo que en net/http.
	if e >= 100 && e <= 199 && e != http.StatusSwitchingProtocols {
		return
	}
	c.estado = e
	c.fijado = true
}

func (c *capturaDeEstado) Write(p []byte) (int, error) {
	// Escribir sin WriteHeader es el 200 IMPLÍCITO de net/http, y aquí se hace
	// explícito: a partir de este momento el estado ya no puede cambiar, igual
	// que no puede cambiar para el cliente.
	c.fijado = true
	n, err := c.ResponseWriter.Write(p)
	c.bytes += int64(n)
	return n, err
}

// Unwrap permite que http.NewResponseController alcance el ResponseWriter
// real a través de esta envoltura. Sin esto, los plazos por actividad de
// plazos.go dejarían de funcionar en silencio.
func (c *capturaDeEstado) Unwrap() http.ResponseWriter { return c.ResponseWriter }

// formatoFecha es el ÚNICO sitio donde se escribe cómo se ve una fecha en las
// páginas. Lo comparten «fecha» e «intervalo»: con el literal repetido, cambiar
// uno dejaría dos formatos distintos en la misma tabla.
const formatoFecha = "2006-01-02 15:04"

func funciones() template.FuncMap {
	return template.FuncMap{
		"tamano": func(n int64) string {
			const u = 1024
			if n < u {
				return strconv.FormatInt(n, 10) + " B"
			}
			div, exp := int64(u), 0
			for m := n / u; m >= u; m /= u {
				div *= u
				exp++
			}
			return strconv.FormatFloat(float64(n)/float64(div), 'f', 1, 64) +
				" " + []string{"KB", "MB", "GB", "TB"}[exp]
		},
		// .Local() Y NO EL VALOR TAL CUAL, y hace falta desde ADR-0066.
		//
		// Los dos anillos guardan la hora con el desfase local, porque nacen de
		// time.Now() dentro de nasd. El historial del sensor la guarda en UTC,
		// porque lo escribe otro programa que no tiene por que compartir zona.
		// Format pinta cada una EN SU PROPIA zona, asi que el mismo instante
		// salia con dos horas distintas segun de que capa viniera.
		//
		// No es teorico: el 18/08 a las 20:23 llego el primer origen de Internet
		// y su fila iba a pintarse «2026-08-19 02:23 -> 2026-08-18 20:23» -- un
		// intervalo corriendo HACIA ATRAS, con el SYN dos segundos DESPUES de la
		// peticion que provoco. Se vio al cruzar las tres capas a mano, no en
		// las pruebas: hasta entonces ninguna fila habia tenido datos de las dos
		// procedencias a la vez.
		"fecha": func(t time.Time) string { return t.Local().Format(formatoFecha) },

		// «Primera» y «Última» eran DOS columnas en el panel de seguridad y son
		// un intervalo: se funden en una (ADR-0065).
		//
		// CUANDO LOS DOS EXTREMOS CAEN EN EL MISMO MINUTO SE ESCRIBE UNO SOLO,
		// y no es cosmético: un sondeo entero cabe en un minuto —los veinte de
		// DRIFTNET tardaron cinco—, así que el caso corriente pintaba la misma
		// fecha dos veces con una flecha en medio. Repetir un dato para no
		// decir nada es exactamente el relleno que este trabajo retira.
		"intervalo": func(desde, hasta time.Time) string {
			// .Local() en los dos extremos, por lo mismo que en «fecha»: sin
			// eso, un intervalo que empieza en el sensor y acaba en un anillo
			// mezcla UTC con hora local y se lee al reves.
			ini, fin := desde.Local().Format(formatoFecha), hasta.Local().Format(formatoFecha)
			if ini == fin {
				return ini
			}
			return ini + " → " + fin
		},

		// RF-25: el listado necesita saber, por cada entrada, si su nombre
		// lleva a un visor o al mensaje, y con qué elemento se dibujaría.
		"apertura": func(nombre string) tipoDeArchivo {
			tipo, _ := tipoAbrible(nombre)
			return tipo
		},
		// Y las rutas de las URL se escapan POR COMPONENTE. html/template no
		// puede hacerlo por su cuenta: dentro de un href no distingue el «#»
		// de un nombre de archivo del que abre un fragmento. Ver apertura.go.
		"rutaURL": escaparRutaURL,
		"listado": urlDeListado,
		// Navegar sin perder la columna por la que se está ordenando (P-3).
		"listadoCon": urlDeListadoCon,
		// Lo mismo para el visor: el nombre del listado y las dos flechas de
		// la galería llevan el orden consigo, o pasar fotos seguiría un
		// criterio distinto del que se estaba viendo.
		"abrirCon": urlDeAbrirCon,
		// El navegador de carpetas de RF-17 enlaza a sí mismo en cada nivel,
		// llevando siempre QUÉ se mueve y DÓNDE se está mirando. Construirlo
		// en la plantilla a mano obligaría a escapar la consulta allí, que es
		// justo donde se olvida — ver escaparConsulta en mover.go.
		"moverA": urlDeMover,
	}
}
