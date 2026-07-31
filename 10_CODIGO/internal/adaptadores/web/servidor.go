// Package web es el adaptador primario HTTP.
//
// No toca el disco directamente: todo pasa por el puerto almacen.Almacen.
// Jamás construye una ruta concatenando cadenas: la única puerta es
// almacen.NuevaRuta (ADR-0014, 04_SEGURIDAD.md §2).
package web

import (
	"context"
	"embed"
	"html/template"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"time"

	"nasd/internal/almacen"
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
}

type Opciones struct {
	Almacen          almacen.Almacen
	Registro         *slog.Logger
	PlazoInactividad time.Duration
}

func Nuevo(o Opciones) (*Servidor, error) {
	t, err := template.New("").Funcs(funciones()).ParseFS(recursos, "plantillas/*.html")
	if err != nil {
		return nil, err
	}
	s := &Servidor{
		almacen:          o.Almacen,
		reg:              o.Registro,
		plantillas:       t,
		plazoInactividad: o.PlazoInactividad,
		subidas:          nuevoRegistroDeSubidas(),
	}

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
	mux := http.NewServeMux()

	mux.HandleFunc("GET /{$}", s.verListado)
	mux.HandleFunc("GET /ver/{ruta...}", s.verListado)
	mux.HandleFunc("GET /descargar/{ruta...}", s.descargar)
	mux.HandleFunc("POST /subir", s.subirMultipart)
	mux.HandleFunc("POST /directorio", s.crearDirectorio)

	// Núcleo del protocolo tus — ADR-0027.
	mux.HandleFunc("POST /subidas", s.tusCrear)
	mux.HandleFunc("HEAD /subidas/{id}", s.tusEstado)
	mux.HandleFunc("PATCH /subidas/{id}", s.tusEnviar)

	mux.Handle("GET /estatico/", http.FileServerFS(recursos))

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
		s.reg.Info("peticion",
			"metodo", r.Method,
			"ruta", r.URL.Path,
			"estado", cap.estado,
			"bytes", cap.bytes,
			"ms", time.Since(inicio).Milliseconds(),
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
	}
}
