package web

// Apertura en el navegador — RF-25, ADR-0052.
//
// Separa dos intenciones que hasta D-23 eran la misma: el nombre del archivo
// ABRE, y solo el botón «Descargar» del menú DESCARGA.
//
// EL DEFECTO QUE ORIGINA ESTE ARCHIVO NO ESTABA EN NINGÚN NAVEGADOR. El
// listado apuntaba el nombre a «/descargar», y ese extremo ordena
// «Content-Disposition: attachment» (RFC 6266 §4.2). Brave hacía exactamente
// lo que se le pedía. Safari parecía distinto porque en iOS interpone Quick
// Look sobre algunas descargas, así que el mismo «attachment» se ve como una
// vista previa. La discrepancia era una diferencia de PRESENTACIÓN sobre una
// respuesta idéntica, y por eso el arreglo es del servidor y no del cliente.

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"

	"nasd/internal/almacen"
)

// tipoDeArchivo es lo que el servidor está dispuesto a servir en línea.
//
// El MIME sale de esta tabla y NUNCA del contenido ni de lo que declare el
// cliente. Es la mitad servidor del control de 04_SEGURIDAD.md §4: un tipo
// declarado explícitamente más «nosniff» le retira al navegador la libertad
// de adivinar, que es el origen de la clase entera de defectos por
// interpretación de MIME (RFC 9110 §8.3; OWASP, «Unrestricted File Upload»).
type tipoDeArchivo struct {
	// Clase decide QUÉ elemento dibuja el visor. Vacía: no se abre en línea.
	Clase string
	MIME  string
}

// abribles es una LISTA POSITIVA, y esa es la decisión, no un detalle.
//
// Una lista negativa —«todo menos .html y .svg»— deja pasar lo que nadie
// pensó el día que se escribió. Una positiva solo sirve lo que alguien
// examinó, y el coste de olvidar una extensión es que el archivo se descargue
// en vez de abrirse: el lado correcto por el que equivocarse.
//
// QUEDAN FUERA A PROPÓSITO, y conviene que se lea por qué:
//
//   - HTML y XML: se ejecutarían en el ORIGEN del NAS, con la sesión del
//     responsable a mano. Es XSS almacenado sobre contenido subido (CWE-79).
//   - SVG: es XML con <script> dentro. Que su extensión parezca de imagen es
//     justamente lo que lo hace peligroso, no lo que lo hace inofensivo.
//   - JavaScript y CSS: ejecutables por definición.
//   - Todo lo demás —ZIP, DOCX, binarios—: ningún navegador los muestra, así
//     que la única respuesta honesta es el mensaje de RF-25.
//
// HEIC y HEIF SÍ están, y no por descuido: son el formato de las fotos del
// iPhone, que es el origen principal de lo que hay en este disco. Safari los
// decodifica y Brave hoy no; esa diferencia la resuelve el navegador en el
// visor, no una tabla del servidor.
var abribles = map[string]tipoDeArchivo{
	".jpg":  {"imagen", "image/jpeg"},
	".jpeg": {"imagen", "image/jpeg"},
	".png":  {"imagen", "image/png"},
	".gif":  {"imagen", "image/gif"},
	".webp": {"imagen", "image/webp"},
	".avif": {"imagen", "image/avif"},
	".bmp":  {"imagen", "image/bmp"},
	".heic": {"imagen", "image/heic"},
	".heif": {"imagen", "image/heif"},

	".mp4":  {"video", "video/mp4"},
	".m4v":  {"video", "video/mp4"},
	".mov":  {"video", "video/quicktime"},
	".webm": {"video", "video/webm"},

	".mp3":  {"audio", "audio/mpeg"},
	".m4a":  {"audio", "audio/mp4"},
	".aac":  {"audio", "audio/aac"},
	".wav":  {"audio", "audio/wav"},
	".ogg":  {"audio", "audio/ogg"},
	".flac": {"audio", "audio/flac"},

	".pdf": {"pdf", "application/pdf"},

	".txt": {"texto", "text/plain; charset=utf-8"},
	".md":  {"texto", "text/plain; charset=utf-8"},
	".csv": {"texto", "text/plain; charset=utf-8"},
	".log": {"texto", "text/plain; charset=utf-8"},
}

func tipoAbrible(nombre string) (tipoDeArchivo, bool) {
	tipo, ok := abribles[strings.ToLower(path.Ext(nombre))]
	return tipo, ok
}

// escaparRutaURL codifica cada componente SIN convertir los separadores en
// datos, que es lo que hace url.PathEscape sobre la ruta entera.
//
// Hace falta porque html/template, dentro de un «href», normaliza la URL pero
// NO escapa «#» ni «?»: son sintaxis legítima de URL y no puede saber que
// aquí forman parte de un nombre. Un archivo llamado «nota #1?.txt» producía
// un enlace cuyo camino terminaba en «nota », con el resto convertido en
// fragmento y consulta. Ocurría ya en /ver, /descargar y /borrar (RFC 3986 §3.3).
func escaparRutaURL(ruta string) string {
	segmentos := strings.Split(ruta, "/")
	for i := range segmentos {
		segmentos[i] = url.PathEscape(segmentos[i])
	}
	return strings.Join(segmentos, "/")
}

// urlDeListado es la única forma de construir el enlace a un listado.
func urlDeListado(r almacen.RutaSegura) string {
	if r.EsRaiz() {
		return "/"
	}
	return "/ver/" + escaparRutaURL(r.Rel())
}

// urlDeListadoCon es urlDeListado llevándose el orden puesto.
//
// El responsable pidió que ordenar valga «también dentro de todas las
// subcarpetas». Como el orden vive en la URL y no en el servidor (regla R1,
// ADR-0015: el listado ES el sistema de archivos y no guarda estado), la
// única forma de que sobreviva a un clic es que cada enlace de navegación lo
// lleve consigo. Por eso lo usan las migas, las filas de carpeta y «subir».
//
// El orden por nombre no se escribe: es el de por omisión, y así los enlaces
// del uso normal quedan limpios.
func urlDeListadoCon(r almacen.RutaSegura, c criterio) string {
	u := urlDeListado(r)
	if c == porNombre {
		return u
	}
	return u + "?orden=" + string(c)
}

type vistaVisor struct {
	Ruta   almacen.RutaSegura
	Nombre string
	// Tipo vacío significa «no se abre»: la plantilla dibuja entonces el
	// mensaje de RF-25 y nada más.
	Tipo tipoDeArchivo
}

// abrirEnNavegador entrega la PÁGINA del visor, nunca los bytes del archivo.
//
// Esa separación es la que permite cumplir RF-25 al pie de la letra: cuando el
// navegador no puede con el formato, lo que hay en pantalla ya es una página
// nuestra donde escribir el mensaje acordado, y no una descarga a medio
// empezar ni una pestaña en blanco.
func (s *Servidor) abrirEnNavegador(w http.ResponseWriter, r *http.Request) {
	ruta, err := almacen.NuevaRuta(r.PathValue("ruta"))
	if err != nil {
		s.fallo(w, r, err)
		return
	}
	tipo, ok := tipoAbrible(ruta.Nombre())
	if !ok {
		s.mostrarNoCompatible(w, ruta)
		return
	}

	// Se abre —y se cierra— para dos cosas: confirmar que el archivo existe
	// antes de prometer un visor, y mirar la cabecera cuando es un PDF.
	lector, entrada, err := s.almacen.Abrir(r.Context(), ruta)
	if err != nil {
		s.fallo(w, r, err)
		return
	}
	defer lector.Close()

	if tipo.Clase == "pdf" {
		plausible, err := pdfPlausible(lector)
		if err != nil {
			s.fallo(w, r, err)
			return
		}
		if !plausible {
			s.mostrarNoCompatible(w, ruta)
			return
		}
	}

	s.renderVisor(w, vistaVisor{Ruta: ruta, Nombre: entrada.Nombre, Tipo: tipo}, http.StatusOK)
}

// servirContenido es la ruta de BYTES PASIVOS. Solo entrega lo que está en la
// lista positiva, con el MIME de esa lista y marcado «inline».
func (s *Servidor) servirContenido(w http.ResponseWriter, r *http.Request) {
	ruta, err := almacen.NuevaRuta(r.PathValue("ruta"))
	if err != nil {
		s.fallo(w, r, err)
		return
	}
	// La lista se consulta ANTES de tocar el almacén: un tipo que no se sirve
	// en línea no llega ni a abrir un descriptor, aunque lo pidan a mano.
	tipo, ok := tipoAbrible(ruta.Nombre())
	if !ok {
		s.mostrarNoCompatible(w, ruta)
		return
	}
	lector, entrada, err := s.almacen.Abrir(r.Context(), ruta)
	if err != nil {
		s.fallo(w, r, err)
		return
	}
	defer lector.Close()

	if tipo.Clase == "pdf" {
		plausible, err := pdfPlausible(lector)
		if err != nil {
			s.fallo(w, r, err)
			return
		}
		if !plausible {
			s.mostrarNoCompatible(w, ruta)
			return
		}
	}

	w.Header().Set("Content-Type", tipo.MIME)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	w.Header().Set("Content-Disposition", "inline; filename*=UTF-8''"+escaparURL(entrada.Nombre))

	// SIN SANDBOX PARA EL PDF, Y NO ES UN OLVIDO.
	//
	// «default-src 'none'; sandbox» es el aislamiento correcto para un
	// documento subido: origen opaco y nada de guiones. Sobre application/pdf
	// no se aplica porque el visor de PDF de los navegadores no es contenido
	// de la página sino un componente interno, y restringirlo por esta vía
	// deja el visor en blanco — es decir, rompe justo lo que RF-25 pide que
	// funcione. Para el PDF el control es el MIME fijo, «nosniff» y que el
	// documento no se ejecuta en el origen del NAS.
	if tipo.Clase != "pdf" {
		w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	}

	// Mismo motivo que en el listado: con dos escritores independientes
	// (ADR-0016) los bytes pueden cambiar bajo el mismo nombre, y una copia
	// cacheada mostraría lo que ya no está en el disco.
	w.Header().Set("Cache-Control", "no-store, must-revalidate")

	// ServeContent aporta RFC 7233 —206 y rangos— sin escribirlo, y aquí no
	// es un extra: sin rangos no se puede saltar dentro de un vídeo.
	// El envoltorio con plazo por actividad es el de RNF-12, igual que en
	// descargar(); ver manejadores.go para por qué se conserva.
	http.ServeContent(escrituraDelegada{w, escrituraConPlazo(w, s.plazoInactividad)},
		r, entrada.Nombre, entrada.Modificado, lector)
}

func (s *Servidor) mostrarNoCompatible(w http.ResponseWriter, ruta almacen.RutaSegura) {
	s.renderVisor(w, vistaVisor{Ruta: ruta, Nombre: ruta.Nombre()}, http.StatusUnsupportedMediaType)
}

func (s *Servidor) renderVisor(w http.ResponseWriter, v vistaVisor, estado int) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store, must-revalidate")
	w.WriteHeader(estado)
	if err := s.plantillas.ExecuteTemplate(w, "visor.html", v); err != nil {
		s.reg.Error("render del visor", "error", err)
	}
}

// pdfPlausible mira la cabecera, y existe por un motivo de INTERFAZ, no de
// seguridad.
//
// <object type="application/pdf"> no emite ningún evento de error
// interoperable cuando el visor nativo no consigue abrir el documento: la
// página no tiene forma de enterarse. Si el servidor no descarta aquí lo que
// evidentemente no es un PDF, RF-25 se incumple con un recuadro en blanco en
// lugar del mensaje acordado.
//
// ISO 32000-1 §7.5.2 prescribe «%PDF-» al principio del archivo. Se admite un
// prefijo corto porque los visores aceptan documentos heredados que lo llevan,
// y esta comprobación no es un validador: solo separa un PDF de algo que no
// lo es en absoluto.
func pdfPlausible(lector io.ReadSeeker) (bool, error) {
	var cabecera [1024]byte
	n, err := io.ReadFull(lector, cabecera[:])
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return false, err
	}
	if _, err := lector.Seek(0, io.SeekStart); err != nil {
		return false, err
	}
	return bytes.Contains(cabecera[:n], []byte("%PDF-")), nil
}
