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
	"time"

	"nasd/internal/almacen"
	"nasd/internal/autenticacion"
)

//go:embed plantillas/*.html estatico/*
var recursos embed.FS

// Servidor arma el adaptador HTTP completo.
type Servidor struct {
	almacen          almacen.Almacen
	reg              *slog.Logger
	plantillas       *template.Template
	plazoInactividad time.Duration
	subidas          *registroDeSubidas

	// Autenticación — RF-15. credencial es la línea derivada, NUNCA la
	// contraseña en claro: esta nunca entra en el proceso más allá del
	// instante de verificarla.
	credencial     string
	sesiones       *autenticacion.Sesiones
	limitador      *limitadorAcceso
	duracionSesion time.Duration

	// Observabilidad — Fase 4, charter §8.
	contadores *contadores
	// muestreador alimenta el flujo en vivo de /estado (ADR-0051). Solo mide
	// mientras haya alguien mirando: sin espectadores no cuesta nada.
	muestreador *muestreador
	// veredictosPrevios recuerda el último veredicto de cada indicador para
	// alertar solo en los CAMBIOS. Lo toca únicamente la goroutine de
	// mantenimiento: ver anunciar().
	veredictosPrevios map[string]veredicto
	// volumen es el punto de montaje del disco de datos. Se guarda para poder
	// medirlo; el adaptador NUNCA lo usa para construir rutas —esa puerta es
	// almacen.NuevaRuta y no hay otra (ADR-0014)—.
	volumen string
}

type Opciones struct {
	Almacen          almacen.Almacen
	Registro         *slog.Logger
	PlazoInactividad time.Duration
	// Credencial es la línea derivada que produce «nasd --generar-credencial».
	// Si está vacía, Nuevo falla: NO existe un modo sin autenticar (RF-15).
	Credencial     string
	DuracionSesion time.Duration
	// Volumen es el punto de montaje del disco de datos (ADR-0019), necesario
	// para informar de su ocupación y su salud en /estado.
	Volumen string
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

	// P5: sin credencial no se arranca. Un modo «sin autenticar» dejaría el
	// disco entero administrable por cualquiera en la LAN, que es justo lo
	// que RN-06 ordena evitar. Mejor no arrancar que arrancar inseguro.
	if err := autenticacion.Valida(o.Credencial); err != nil {
		return nil, fmt.Errorf("credencial de la web: %w", err)
	}

	s := &Servidor{
		almacen:           o.Almacen,
		reg:               o.Registro,
		plantillas:        t,
		plazoInactividad:  o.PlazoInactividad,
		subidas:           nuevoRegistroDeSubidas(),
		credencial:        o.Credencial,
		sesiones:          autenticacion.NuevasSesiones(o.DuracionSesion),
		limitador:         nuevoLimitador(),
		duracionSesion:    o.DuracionSesion,
		contadores:        nuevosContadores(),
		veredictosPrevios: make(map[string]veredicto),
		volumen:           o.Volumen,
	}
	s.muestreador = nuevoMuestreador(s.marcoDelServidor)

	// Al arrancar se mira qué subidas dejó a medias el proceso anterior.
	// No se reabren aquí —eso ocurre al primer HEAD o PATCH— pero se informa,
	// porque un montón de parciales acumulados es el síntoma de ADR-0029 y
	// debe verse en el registro sin tener que ir a mirar el disco (P7).
	if ps, err := s.almacen.Reanudables(context.Background()); err != nil {
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
	mux.Handle("GET /estatico/", http.FileServerFS(recursos))
	mux.Handle("/", s.exigirSesion(protegido))

	protegido.HandleFunc("GET /{$}", s.verListado)
	protegido.HandleFunc("GET /ver/{ruta...}", s.verListado)
	protegido.HandleFunc("GET /descargar/{ruta...}", s.descargar)

	// Apertura en el navegador — RF-25, ADR-0052. Son DOS extremos y no uno:
	// /abrir entrega la página del visor y /contenido los bytes pasivos. Ver
	// apertura.go para por qué esa separación es lo que hace cumplible el
	// mensaje de RF-25. /descargar queda intacto y sigue siendo el único que
	// ordena «attachment».
	protegido.HandleFunc("GET /abrir/{ruta...}", s.abrirEnNavegador)
	protegido.HandleFunc("GET /contenido/{ruta...}", s.servirContenido)
	protegido.HandleFunc("POST /subir", s.subirMultipart)
	protegido.HandleFunc("POST /directorio", s.crearDirectorio)

	// Administración — Fase 3. Después de RF-15, por RN-06.
	//
	// MOVER TIENE EXTREMO PROPIO desde el 2026-08-06 (ADR-0054).
	// Compartirlo con renombrar obligaba a adivinar la intención mirando si
	// el texto llevaba una barra, y en uso real se adivinó mal. /renombrar
	// conserva SOLO el renombrado, que es lo que su nombre dice.
	protegido.HandleFunc("GET /mover/{ruta...}", s.verMover)          // RF-17, paso 1
	protegido.HandleFunc("POST /mover", s.mover)                      // RF-17, paso 2
	protegido.HandleFunc("POST /renombrar", s.renombrar)              // RF-16
	protegido.HandleFunc("GET /borrar/{ruta...}", s.confirmarBorrado) // RF-18, paso 1
	protegido.HandleFunc("POST /borrar", s.borrar)                    // RF-18, paso 2

	// Observabilidad — Fase 4, RF-24. Va DENTRO de lo protegido: ver estado.go.
	protegido.HandleFunc("GET /estado", s.verEstado)
	// El flujo en vivo de esa misma pantalla — ADR-0051. Detrás de la sesión
	// por el mismo motivo que /estado: publica temperatura, capacidad y ritmo
	// de uso, que son reconocimiento gratis para quien no tenga que verlos.
	protegido.HandleFunc("GET /estado/flujo", s.flujoDeEstado)

	// Núcleo del protocolo tus — ADR-0027.
	protegido.HandleFunc("POST /subidas", s.tusCrear)
	protegido.HandleFunc("HEAD /subidas/{id}", s.tusEstado)
	protegido.HandleFunc("PATCH /subidas/{id}", s.tusEnviar)
	protegido.HandleFunc("DELETE /subidas/{id}", s.tusDescartar)

	return s.conRegistro(mux)
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
		siguiente.ServeHTTP(cap, r)
		duracion := time.Since(inicio)

		// Los indicadores se anotan AQUÍ y no en cada manejador, a propósito:
		// así ninguna ruta futura puede quedarse fuera de la medición por
		// olvido. Es el mismo motivo por el que el registro vive aquí.
		s.contadores.anotarRespuesta(cap.estado, cap.bytes, duracion,
			clasificar(r.Method, r.URL.Path))

		s.reg.Info("peticion",
			"metodo", r.Method,
			"ruta", r.URL.Path,
			"estado", cap.estado,
			"bytes", cap.bytes,
			"ms", duracion.Milliseconds(),
		)
	})
}

type capturaDeEstado struct {
	http.ResponseWriter
	estado int
	bytes  int64
}

func (c *capturaDeEstado) WriteHeader(e int) {
	c.estado = e
	c.ResponseWriter.WriteHeader(e)
}

func (c *capturaDeEstado) Write(p []byte) (int, error) {
	n, err := c.ResponseWriter.Write(p)
	c.bytes += int64(n)
	return n, err
}

// Unwrap permite que http.NewResponseController alcance el ResponseWriter
// real a través de esta envoltura. Sin esto, los plazos por actividad de
// plazos.go dejarían de funcionar en silencio.
func (c *capturaDeEstado) Unwrap() http.ResponseWriter { return c.ResponseWriter }

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
		"fecha": func(t time.Time) string { return t.Format("2006-01-02 15:04") },

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
		// El navegador de carpetas de RF-17 enlaza a sí mismo en cada nivel,
		// llevando siempre QUÉ se mueve y DÓNDE se está mirando. Construirlo
		// en la plantilla a mano obligaría a escapar la consulta allí, que es
		// justo donde se olvida — ver escaparConsulta en mover.go.
		"moverA": urlDeMover,
	}
}
