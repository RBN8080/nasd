// Command preparar-mapa - turns the Natural Earth 110m country GeoJSON into
// the world SVG that /seguridad paints under "Procedencia".
//
// IT DOES NOT RUN ON THE NODE. It is a host binary, invoked by "make mapa"
// when mundo.svg has to be regenerated - which should almost never happen,
// because borders do not change at the rate the geoip country and network data
// does. The result is committed to the repository and travels embedded in nasd
// like any other static asset (ADR-0017, ADR-0089): this command is the
// preparation step, not a part of the service.
//
// Usage:
//
//	go run ./cmd/preparar-mapa <ne_110m_admin_0_countries.geojson> <output.svg>
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strings"

	"nasd/internal/mapa"
)

// direccionesAncla son países que tienen que aparecer en el mapa resultante
// para que la conversión se dé por buena — el mismo criterio de
// autocomprobación que prepararBaseGeoIP aplica a la base de país/operador:
// un archivo «sin errores» puede estar vacío o incompleto, y solo un dato
// verificable por otra vía lo distingue de uno correcto.
//
// Cuatro grandes que cubren cuatro continentes —si faltara alguno, la
// proyección o el filtro de ISO2 estarían rotos de forma general— y los dos
// puntos manuales, que dependen de una lista que vive aparte y podría
// desincronizarse sin que nada más lo note.
var direccionesAncla = []string{"MX", "US", "CN", "GB", "SG", "HK"}

// fuenteDocumentada es la URL estable que «make mapa» documenta como origen
// del GeoJSON — ver el objetivo «mapa» del Makefile, que la descarga con
// curl antes de invocar este comando. Se escribe en la cabecera de
// mundo.svg junto a la huella del archivo REALMENTE usado, para que un
// archivo de entrada distinto del documentado se note en el propio
// comentario y no solo en el histórico de git.
const fuenteDocumentada = "https://raw.githubusercontent.com/nvkelso/natural-earth-vector/master/geojson/ne_110m_admin_0_countries.geojson"

func main() {
	if err := ejecutar(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "preparar-mapa:", err)
		os.Exit(1)
	}
}

func ejecutar(args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("uso: preparar-mapa <ne_110m_admin_0_countries.geojson> <salida.svg>")
	}
	rutaEntrada, rutaSalida := args[0], args[1]

	crudo, err := os.ReadFile(rutaEntrada)
	if err != nil {
		return fmt.Errorf("leer %q: %w", rutaEntrada, err)
	}
	suma := sha256.Sum256(crudo)
	huella := hex.EncodeToString(suma[:])

	tmp, err := os.CreateTemp(".", ".mundo-svg-*")
	if err != nil {
		return fmt.Errorf("crear el temporal: %w", err)
	}
	nombreTmp := tmp.Name()
	defer os.Remove(nombreTmp) // no-op si el rename sale bien

	n, err := mapa.Preparar(strings.NewReader(string(crudo)), tmp, fuenteDocumentada, huella)
	cerrarErr := tmp.Close()
	if err != nil {
		return fmt.Errorf("convertir: %w", err)
	}
	if cerrarErr != nil {
		return fmt.Errorf("cerrar el temporal: %w", cerrarErr)
	}
	fmt.Printf("mapa preparado: %d países/territorios\n", n)

	// Autocomprobación contra el propio archivo escrito, releído — el mismo
	// gesto que prepararBaseGeoIP hace tras convertir el TSV de IPtoASN: lo
	// que importa no es que la conversión «terminara sin error», es que el
	// resultado contenga lo que se espera de él.
	svg, err := os.ReadFile(nombreTmp)
	if err != nil {
		return fmt.Errorf("releer el SVG recién escrito: %w", err)
	}
	var faltan []string
	for _, iso := range direccionesAncla {
		if !strings.Contains(string(svg), "id=\""+iso+"\"") {
			faltan = append(faltan, iso)
		}
	}
	if len(faltan) > 0 {
		return fmt.Errorf("faltan países ancla en el resultado: %s — revise el GeoJSON de entrada",
			strings.Join(faltan, ", "))
	}
	fmt.Println("comprobación contra países ancla: " + strings.Join(direccionesAncla, ", ") + " — todos presentes")

	if err := os.Rename(nombreTmp, rutaSalida); err != nil {
		return fmt.Errorf("publicar %q: %w", rutaSalida, err)
	}
	fmt.Printf("escrito en %s (%d bytes)\n", rutaSalida, len(svg))
	return nil
}
