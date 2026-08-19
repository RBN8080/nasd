package web

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
)

// Entrega de los recursos estáticos: caché por ETag y gzip precalculado.
//
// # EL PROBLEMA ERA LA ENTREGA, NO EL TAMAÑO
//
// `http.FileServerFS(recursos)` servía esto sin una sola cabecera de caché.
// Medido contra el nodo el 2026-08-19: ni `Cache-Control`, ni `ETag`, ni
// `Last-Modified` —embed.FS reporta mtime CERO, así que ServeContent la omite—,
// y sin compresión. `If-Modified-Since` devolvía 200, no 304. Consecuencia:
// cada carga de /listado rebajaba 55 895 bytes enteros por la WAN, al teléfono.
//
// Eso corrige de paso una salvedad que el rector arrastraba desde el 17/08:
// «43.5 KB de CSS ya es mucho y se sirve en cada carga». La primera mitad era
// falsa —el 67 % de ese archivo son comentarios, y las reglas son 14 KB— y la
// segunda era cierta pero se curaba aquí, no recortando hojas de estilo.
//
// # SE PRECALCULA AL ARRANCAR, NO POR PETICIÓN
//
// Los recursos van incrustados con //go:embed: no cambian en toda la vida del
// proceso. Así que comprimir en cada petición sería pagar CPU una y otra vez
// por un resultado idéntico. Se recorre el árbol una vez, se guarda cada
// archivo en crudo y en gzip con su ETag, y a partir de ahí servir es buscar en
// un mapa y copiar bytes. Cero asignación en el camino caliente y un tope de
// memoria conocido: lo que ocupa `estatico/` más su versión comprimida.
//
// El mapa se construye una sola vez con sync.OnceValue y NO se vuelve a
// escribir, así que se puede leer desde varias peticiones a la vez sin cerrojo.
// Es la misma disciplina de huellaDelBinario (version.go).
//
// # POR QUÉ «no-cache» Y NO UNA HUELLA EN LA URL
//
// «no-cache» no significa «no guardes»: significa «guarda, pero pregunta antes
// de usarlo». Con un ETag fuerte, la recarga responde 304 SIN CUERPO, que es
// donde está casi todo el ahorro.
//
// La alternativa clásica —«estilo.css?v=<huella>» con max-age largo— ahorraría
// además el viaje de ida y vuelta, y se descarta por dos razones concretas:
//
//  1. Rompe la prueba de estatico_test.go, que captura href="(/estatico/[^"]+)"
//     y exige que ESE archivo exista en el embed.FS. Esa prueba está puesta
//     justamente para detectar «¿cambió la forma de referenciarlos?».
//  2. Con URL fija y max-age largo, tras desplegar el navegador seguiría
//     sirviendo el CSS viejo hasta que caducara. El ciclo de trabajo declarado
//     en NAS_OPERACION.txt §8 es editar y recargar; una caché que retiene lo
//     viejo convierte ese ciclo en una caza de fantasmas.
//
// Se prefiere un viaje de ida y vuelta con 304 antes que un minuto sirviendo
// algo que ya no es verdad.
type recursoEstatico struct {
	crudo []byte
	// gzip está VACÍO cuando comprimir no compensaba. Ver comprimible.
	gzip []byte
	etag string
	tipo string
}

// tamanoMinimoGzip es el umbral por debajo del cual no se comprime.
//
// Por debajo de ~1 KB la cabecera y el diccionario de gzip se comen la ganancia
// —y a veces el resultado es MAYOR que el original—. El icono SVG del proyecto
// ronda ese tamaño, así que no es una precaución teórica.
const tamanoMinimoGzip = 1024

// comprimible dice si el tipo de contenido gana algo al comprimirse.
//
// Solo texto. Comprimir un JPEG o un PNG gasta CPU para producir algo más
// grande: ya vienen comprimidos.
func comprimible(tipo string) bool {
	return strings.HasPrefix(tipo, "text/") ||
		strings.Contains(tipo, "javascript") ||
		strings.Contains(tipo, "json") ||
		strings.Contains(tipo, "svg")
}

// estaticos es el mapa de ruta HTTP a recurso, calculado una sola vez.
var estaticos = sync.OnceValue(func() map[string]recursoEstatico {
	m := make(map[string]recursoEstatico)
	err := fs.WalkDir(recursos, "estatico", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		datos, err := recursos.ReadFile(p)
		if err != nil {
			return err
		}
		tipo := mime.TypeByExtension(path.Ext(p))
		if tipo == "" {
			tipo = "application/octet-stream"
		}

		// ETag FUERTE —sin el prefijo W/— porque se compara byte a byte con lo
		// que hay en el binario: si el contenido es el mismo, la respuesta es
		// exactamente la misma. base64 y no hexadecimal para que quepa en una
		// cabecera sin ocupar 64 caracteres.
		suma := sha256.Sum256(datos)
		r := recursoEstatico{
			crudo: datos,
			etag:  `"` + base64.RawURLEncoding.EncodeToString(suma[:16]) + `"`,
			tipo:  tipo,
		}

		if comprimible(tipo) && len(datos) >= tamanoMinimoGzip {
			var b bytes.Buffer
			// BestCompression y no el nivel por omisión: esto se paga UNA vez
			// al arrancar y se cobra en cada petición durante días.
			z, err := gzip.NewWriterLevel(&b, gzip.BestCompression)
			if err != nil {
				return err
			}
			if _, err := z.Write(datos); err != nil {
				return err
			}
			if err := z.Close(); err != nil {
				return err
			}
			// Solo se guarda si de verdad salió más pequeño. Con el umbral de
			// arriba esto no debería fallar nunca, y por eso mismo se comprueba
			// en vez de suponerlo.
			if b.Len() < len(datos) {
				r.gzip = bytes.Clone(b.Bytes())
			}
		}
		m["/"+p] = r
		return nil
	})
	if err != nil {
		// Los recursos van incrustados en el binario: si no se pueden leer, el
		// binario está mal construido y ninguna página se vería bien. Fallar al
		// arrancar es preferible a servir un sitio sin estilos.
		panic(fmt.Sprintf("preparar los recursos estáticos: %v", err))
	}
	return m
})

// servirEstatico entrega un recurso incrustado con su ETag y, si el cliente lo
// admite, ya comprimido.
func servirEstatico(w http.ResponseWriter, r *http.Request) {
	r0, ok := estaticos()[path.Clean(r.URL.Path)]
	if !ok {
		http.NotFound(w, r)
		return
	}

	h := w.Header()
	h.Set("Content-Type", r0.tipo)
	h.Set("ETag", r0.etag)
	h.Set("Cache-Control", "no-cache")
	// SIN Vary NO SE PUEDE SERVIR gzip CONDICIONALMENTE. Una caché intermedia
	// —o el propio navegador— guardaría la respuesta comprimida y se la daría
	// después a un cliente que no la pidió, que vería basura binaria.
	h.Set("Vary", "Accept-Encoding")

	// La revalidación va ANTES de elegir codificación: el ETag identifica el
	// contenido, no su envoltorio, así que un 304 es correcto le llegue como le
	// llegue. RFC 9110 §13.1.2 pide comparación débil aquí, y por eso basta con
	// que el valor aparezca en la lista.
	if coincideEtag(r.Header.Get("If-None-Match"), r0.etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	cuerpo := r0.crudo
	if len(r0.gzip) > 0 && aceptaGzip(r.Header.Get("Accept-Encoding")) {
		h.Set("Content-Encoding", "gzip")
		cuerpo = r0.gzip
	}
	// Content-Length a mano y no por ServeContent: aquí no hay rangos que
	// atender —nadie pide el trozo 200-400 de una hoja de estilos— y con la
	// longitud puesta, net/http no necesita trocear la respuesta.
	h.Set("Content-Length", strconv.Itoa(len(cuerpo)))
	if r.Method == http.MethodHead {
		return
	}
	// El error se descarta a propósito: significa que el cliente se fue a medio
	// archivo, que es rutina en un móvil cambiando de red y no un fallo nuestro.
	_, _ = w.Write(cuerpo)
}

// coincideEtag dice si el ETag está en una cabecera If-None-Match.
//
// «*» vale para cualquier representación existente (RFC 9110 §13.1.2). La lista
// puede traer varios valores separados por coma, y también un ETag débil
// «W/"..."» que aquí se acepta: la comparación débil es la que corresponde a
// una petición condicional de este tipo.
func coincideEtag(cabecera, etag string) bool {
	if cabecera == "" {
		return false
	}
	for _, v := range strings.Split(cabecera, ",") {
		v = strings.TrimSpace(v)
		if v == "*" || strings.TrimPrefix(v, "W/") == etag {
			return true
		}
	}
	return false
}

// aceptaGzip dice si el cliente admite gzip.
//
// NO basta con buscar la palabra: «gzip;q=0» significa exactamente lo
// contrario, y es la forma que tiene un cliente de rechazarlo (RFC 9110 §12.5.3).
func aceptaGzip(cabecera string) bool {
	for _, parte := range strings.Split(cabecera, ",") {
		campos := strings.Split(strings.TrimSpace(parte), ";")
		if strings.TrimSpace(campos[0]) != "gzip" {
			continue
		}
		for _, p := range campos[1:] {
			if strings.ReplaceAll(strings.TrimSpace(p), " ", "") == "q=0" {
				return false
			}
		}
		return true
	}
	return false
}
