package web

// Miniaturas EXIF — rector §7.nonies.bis, 2026-08-15.
//
// El servidor NO decodifica imágenes: llama a un programa aparte en C,
// nas-miniatura (10_CODIGO/nas-miniatura/miniatura.c), que solo copia bytes
// —la miniatura de 160×120 que las fotos de iPhone ya llevan incrustada en
// su bloque EXIF— sin descomprimir un solo píxel. La razón está medida:
// MemoryMax=192M en 05_instalar_servicio.sh es del cgroup ENTERO, nasd y sus
// hijos juntos, y decodificar una foto de 12 MP entera ocupa ~36 MB de
// trabajo — no cabe con holgura. Extraer la miniatura incrustada usa unos
// pocos cientos de KB, y en la Pi de verdad se midió en 5 ms de punta a
// punta, indistinguible de arrancar el proceso.
//
// EL PROGRAMA EN C ES UN PROCESO APARTE A PROPÓSITO. Si se cae, nasd ni se
// entera: exec.CommandContext solo ve un código de salida distinto de 0.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"nasd/internal/almacen"
)

// rutaNasMiniatura es absoluta por el mismo motivo que rutaVcgencmd
// (internal/adaptadores/sistema/lector_linux.go): el PATH de un servicio
// systemd no es de fiar, y los dos binarios viven uno junto al otro —
// /usr/local/bin/nasd y /usr/local/bin/nas-miniatura, instalados por
// «make desplegar».
const rutaNasMiniatura = "/usr/local/bin/nas-miniatura"

// plazoMiniatura acota el subproceso. WatchdogSec=30s en el servicio
// significa que CUALQUIER bloqueo de 30s reinicia nasd entero; este plazo
// se queda muy por debajo para que un archivo hostil nunca llegue ni cerca
// de esa cifra. [R]
const plazoMiniatura = 3 * time.Second

// limiteCopiaEntrada acota cuánto se copia del almacén al archivo temporal
// que lee nas-miniatura. El programa en C solo mira los primeros 128 KB
// para metadatos y hasta 256 KB de miniatura (miniatura.c), así que copiar
// de más nunca ayuda al resultado —solo cuesta E/S—; el límite es generoso
// para cualquier foto real y acota el caso de un archivo enorme renombrado
// a .jpg. [R]
const limiteCopiaEntrada = 64 * 1024 * 1024

// ventanaMiniaturaFallida — mismo valor que ventanaIntentos (sesion.go) e
// intervaloMantenimiento (mantenimiento.go): no es una cifra nueva, es el
// mismo ritmo de cinco minutos que ya usa este proyecto para «un rato».
const ventanaMiniaturaFallida = 5 * time.Minute

// errSinMiniatura distingue el caso LEGÍTIMO —código de salida 1 de
// nas-miniatura: JPEG válido sin miniatura aprovechable— de un fallo de
// verdad. La mayoría de imágenes reales no traen miniatura EXIF, y eso no
// debe verse como ruido en el diario (P7).
var errSinMiniatura = errors.New("sin miniatura EXIF aprovechable")

// ejecutarNasMiniatura invoca el binario y traduce su contrato de salida
// (miniatura.c: 0 escribió, 1 sin miniatura, 2 fallo) al idioma de errores
// de Go.
//
// VARIABLE DE PAQUETE, no una llamada directa dentro de generarMiniatura, y
// es una costura de PRUEBA, no un punto de configuración de producción:
// nadie va a querer un ejecutor distinto en el nodo, así que esto NO es un
// campo de Opciones (P8, «sin lo que no hace falta»). Existe únicamente
// para que las pruebas puedan sustituirlo por un doble y ejercitar TODO lo
// demás —semáforo, temporal, rename atómico, caché, aislamiento por
// usuario— sin depender de un binario ARM64 que este host de pruebas no
// puede correr.
var ejecutarNasMiniatura = func(ctx context.Context, rutaEntrada, rutaSalida string) error {
	cmd := exec.CommandContext(ctx, rutaNasMiniatura, rutaEntrada, rutaSalida)
	if _, err := cmd.Output(); err != nil {
		var salida *exec.ExitError
		if errors.As(err, &salida) && salida.ExitCode() == 1 {
			return errSinMiniatura
		}
		return fmt.Errorf("nas-miniatura: %w", err)
	}
	return nil
}

// fallosMiniatura recuerda, un rato, qué claves acaban de fallar. Calcado de
// limitadorAcceso (sesion.go): mismo mapa con mutex, misma expiración
// perezosa al leer, y purgado por el MISMO ciclo de 5 min
// (mantenimiento.go) — es la estructura de larga vida número cinco de este
// paquete, y lleva purga desde que nace, no como una corrección posterior.
//
// Sin esto, un archivo sin miniatura aprovechable lanzaría un subproceso en
// CADA recarga de la carpeta que lo contiene.
type fallosMiniatura struct {
	mu     sync.Mutex
	vistos map[string]time.Time
}

func nuevoFallosMiniatura() *fallosMiniatura {
	return &fallosMiniatura{vistos: make(map[string]time.Time)}
}

func (f *fallosMiniatura) recienFallo(clave string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	cuando, ok := f.vistos[clave]
	if !ok {
		return false
	}
	if time.Since(cuando) > ventanaMiniaturaFallida {
		delete(f.vistos, clave)
		return false
	}
	return true
}

func (f *fallosMiniatura) anotar(clave string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.vistos[clave] = time.Now()
}

func (f *fallosMiniatura) purgar() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for k, cuando := range f.vistos {
		if time.Since(cuando) > ventanaMiniaturaFallida {
			delete(f.vistos, k)
			n++
		}
	}
	return n
}

// claveMiniatura identifica «esta versión de este archivo, para este
// usuario» en un solo hash.
//
// EL USUARIO VA EN LA CLAVE, Y NO ES UN EXTRA: ParaUsuario (ADR-0055) da a
// cada cuenta su PROPIA carpeta, así que la misma ruta relativa
// ("foto.jpg") es un archivo DISTINTO según quién la pida —comprobado
// contra TestBorrarSoloAlcanzaLaCarpetaPropia (fsposix/aislamiento_test.go),
// que prueba exactamente esa propiedad—. Sin el usuario aquí, dos cuentas
// con un archivo del mismo tamaño y fecha colisionarían en el mismo hueco
// de caché y una vería la miniatura de la otra.
//
// Incluir mtime y tamaño invalida sola la miniatura si el archivo cambia:
// no hace falta borrar nada a mano, y nunca hay que decidir cuándo caduca.
func claveMiniatura(usuario, ruta string, modificado time.Time, tamano int64) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s\x00%d\x00%d", usuario, ruta, modificado.UnixNano(), tamano)
	return hex.EncodeToString(h.Sum(nil))
}

// rutaCacheMiniatura reparte en subdirectorios de dos caracteres — mismo
// motivo que cualquier caché por hash: un solo directorio con cientos de
// miles de archivos degrada la mayoría de sistemas de archivos.
func (s *Servidor) rutaCacheMiniatura(clave string) string {
	return filepath.Join(s.dirMiniaturas, clave[:2], clave+".jpg")
}

// servirMiniatura entrega la miniatura EXIF de una imagen, generándola y
// cacheándola si hace falta. Cuelga de conAlmacen (servidor.go): el
// parámetro alm YA ESTÁ acotado a quien pregunta, así que esta función no
// necesita —y no debe— añadir ninguna comprobación de aislamiento propia.
func (s *Servidor) servirMiniatura(w http.ResponseWriter, r *http.Request, alm almacen.Almacen) {
	if s.dirMiniaturas == "" {
		http.NotFound(w, r)
		return
	}
	ruta, err := almacen.NuevaRuta(r.PathValue("ruta"))
	if err != nil {
		s.fallo(w, r, err)
		return
	}
	// Misma lista positiva que /contenido y /abrir (apertura.go): un tipo
	// que no está en abribles no llega ni a abrir un descriptor, y aquí
	// además se exige la CLASE imagen — un PDF o un vídeo no tienen
	// miniatura EXIF que buscar.
	tipo, ok := tipoAbrible(ruta.Nombre())
	if !ok || tipo.Clase != "imagen" {
		http.NotFound(w, r)
		return
	}

	lector, entrada, err := alm.Abrir(r.Context(), ruta)
	if err != nil {
		s.fallo(w, r, err)
		return
	}
	defer lector.Close()

	clave := claveMiniatura(usuarioDe(r), ruta.Rel(), entrada.Modificado, entrada.Tamano)
	rutaCache := s.rutaCacheMiniatura(clave)

	datos, err := os.ReadFile(rutaCache)
	switch {
	case err == nil:
		s.responderMiniatura(w, r, datos, entrada.Modificado)
		return
	case !errors.Is(err, fs.ErrNotExist):
		// No es «aún no está en caché»: es un problema de E/S de verdad.
		// Se registra y se sigue —intentar generarla es más útil que
		// rendirse aquí— pero no debe pasar en silencio (P7).
		s.reg.Warn("no se pudo leer la caché de miniaturas", "clave", clave, "error", err)
	}

	if s.miniaturaFallidas.recienFallo(clave) {
		http.NotFound(w, r)
		return
	}

	datos, err = s.generarMiniatura(r.Context(), lector, clave)
	if err != nil {
		if !errors.Is(err, errSinMiniatura) {
			s.reg.Warn("no se pudo generar la miniatura",
				"ruta", ruta.Rel(), "error", err)
		}
		s.miniaturaFallidas.anotar(clave)
		http.NotFound(w, r)
		return
	}
	s.responderMiniatura(w, r, datos, entrada.Modificado)
}

// generarMiniatura invoca nas-miniatura sobre una copia temporal de la
// entrada y deja el resultado cacheado en disco antes de devolverlo.
func (s *Servidor) generarMiniatura(ctx context.Context, lector io.ReadSeeker, clave string) ([]byte, error) {
	// Semáforo de CAPACIDAD 1: ver el comentario del campo en servidor.go.
	// Si el cliente se va mientras espera turno, ctx.Done() libera esta
	// espera sin dejar la petición colgada.
	select {
	case s.miniaturaSem <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-s.miniaturaSem }()

	dirTmp := filepath.Join(s.dirMiniaturas, "tmp")
	if err := os.MkdirAll(dirTmp, 0o700); err != nil {
		return nil, fmt.Errorf("directorio temporal de miniaturas: %w", err)
	}
	entrada, err := os.CreateTemp(dirTmp, "entrada-*")
	if err != nil {
		return nil, fmt.Errorf("archivo temporal de entrada: %w", err)
	}
	rutaEntrada := entrada.Name()
	defer os.Remove(rutaEntrada)

	if _, err := lector.Seek(0, io.SeekStart); err != nil {
		entrada.Close()
		return nil, err
	}
	_, errCopia := io.CopyN(entrada, lector, limiteCopiaEntrada)
	errCierre := entrada.Close()
	if errCopia != nil && !errors.Is(errCopia, io.EOF) {
		return nil, fmt.Errorf("copiar la entrada: %w", errCopia)
	}
	if errCierre != nil {
		return nil, errCierre
	}

	dirDestino := filepath.Join(s.dirMiniaturas, clave[:2])
	if err := os.MkdirAll(dirDestino, 0o700); err != nil {
		return nil, fmt.Errorf("directorio de destino: %w", err)
	}
	rutaDestino := s.rutaCacheMiniatura(clave)
	// Temporal-y-rename, MISMA disciplina que EscrituraAtomica (ADR-0024):
	// si nas-miniatura muere a medio escribir, o el plazo lo mata, lo que
	// queda a medias es el .tmp, nunca el nombre que el caché-hit del
	// principio de servirMiniatura llega a leer.
	rutaTmp := rutaDestino + ".tmp"
	defer os.Remove(rutaTmp) // no-op si ya se renombró

	ctxPlazo, cancelar := context.WithTimeout(ctx, plazoMiniatura)
	defer cancelar()
	// Los dos argumentos son RUTAS DE ARCHIVO que este mismo proceso acaba
	// de calcular, nunca texto que venga del cliente: no hay intérprete de
	// por medio y no hay nada que inyectar. Mismo criterio que vcgencmd
	// (internal/adaptadores/sistema/lector_linux.go).
	if err := ejecutarNasMiniatura(ctxPlazo, rutaEntrada, rutaTmp); err != nil {
		return nil, err
	}

	if err := os.Rename(rutaTmp, rutaDestino); err != nil {
		return nil, err
	}
	return os.ReadFile(rutaDestino)
}

func (s *Servidor) responderMiniatura(w http.ResponseWriter, r *http.Request, datos []byte, modificado time.Time) {
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	// La clave YA incluye usuario+ruta+mtime+tamaño (claveMiniatura): si
	// cualquiera de esos cambia, la URL entera cambia de hash. Un caché
	// LARGO en el navegador es seguro por construcción —nunca serviría una
	// miniatura vieja bajo la misma URL—, al contrario que /contenido
	// (Cache-Control: no-store), donde el mismo NOMBRE sí puede cambiar de
	// bytes con dos escritores independientes (ADR-0016).
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	http.ServeContent(w, r, "miniatura.jpg", modificado, bytes.NewReader(datos))
}
