package web

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"runtime"
	"runtime/debug"
	"sync"
	"time"
)

// Versión del binario en ejecución, para la línea del binario de /estado.
//
// Sale de la información que Go incrusta al compilar, NO de una constante
// escrita a mano: una cadena de versión mantenida a mano miente el día que
// alguien olvida subirla, y este proyecto ya persigue esa clase de afirmación
// desde ADR-0038. Lo que se muestra es lo que el binario dice de sí mismo.
//
// Depende de que la compilación ocurra dentro del repositorio, que es el caso:
// el Makefile compila desde 10_CODIGO y «desplegar» exige el árbol limpio.

// versionDelBinario devuelve los campos de la línea del binario por separado,
// en el orden que pidió el responsable el 2026-11-07:
// «nasd b18440ec40d3» · «go1.27.3» · «linux/arm64» · «0ba159d» · «05/08/2026 - 18:52:31».
//
// SE DEVUELVEN SUELTOS Y NO YA UNIDOS porque cada uno tiene que poder envolver
// entero: unida, el teléfono partiría la huella por la mitad. La plantilla los
// une con su separador.
//
// La fecha es la del commit (vcs.time), en hora local del nodo. Si el árbol
// tenía cambios sin commitear al compilar, se dice: en producción no debería
// verse nunca —«make desplegar» exige el árbol limpio—.
func versionDelBinario() []string {
	nombre := "nasd"
	if h := huellaDelBinario(); h != "" {
		nombre += " " + h
	}
	partes := []string{nombre, runtime.Version(), runtime.GOOS + "/" + runtime.GOARCH}

	info, ok := debug.ReadBuildInfo()
	if !ok {
		return partes
	}
	var revision, cuando string
	var sucio bool
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value
		case "vcs.time":
			cuando = s.Value
		case "vcs.modified":
			sucio = s.Value == "true"
		}
	}
	if revision != "" {
		// Siete caracteres, como abrevia git: se pega tal cual en «git show».
		if len(revision) > 7 {
			revision = revision[:7]
		}
		partes = append(partes, revision)
	}
	if t, err := time.Parse(time.RFC3339, cuando); err == nil {
		partes = append(partes, t.Local().Format(formatoFechaBinario))
	}
	if sucio {
		partes = append(partes, "ÁRBOL MODIFICADO")
	}
	return partes
}

// formatoFechaBinario es dd/mm/aaaa - HH:MM:SS.
const formatoFechaBinario = "02/01/2006 - 15:04:05"

// huellaDelBinario es el SHA-256 del ejecutable que está corriendo, abreviado.
//
// # PARA QUÉ
//
// Es el mismo número que «make desplegar» compara a los dos lados del cable
// para saber que lo instalado es lo compilado. Tenerlo en el pie permite
// comprobarlo desde el navegador, sin entrar por SSH: si la pantalla y el
// «sha256sum» del artefacto local coinciden, el nodo está corriendo eso.
//
// # POR QUÉ SE PUEDE ENSEÑAR
//
// La página está detrás de la sesión (ver la cabecera de estado.go). Y aunque
// no lo estuviera, un resumen criptográfico no es reversible: no dice dónde
// está el archivo, ni qué contiene, ni ayuda a construir nada. Lo que revela es
// exactamente la afirmación que interesa —«corro este binario y no otro»—, que
// es la misma clase de dato que ya publica el hash de commit de la línea de al
// lado. No se muestra la RUTA del ejecutable: eso sí sería contar la
// distribución del sistema de archivos a cambio de nada.
//
// # POR QUÉ UNA SOLA VEZ
//
// Son ~11 MB leídos del medio de arranque, que en este nodo es un consumible
// (P9) y va por el mismo bus USB 2.0 que el disco de datos (RES-02). Se hace al
// primer render y no en cada uno, y no al arrancar para no retrasar el aviso de
// listo a systemd. Un binario no cambia bajo sus propios pies: reemplazarlo
// obliga a reiniciar el servicio, y entonces esto se vuelve a calcular.
var huellaDelBinario = sync.OnceValue(func() string {
	ruta, err := os.Executable()
	if err != nil {
		return ""
	}
	f, err := os.Open(ruta)
	if err != nil {
		// Sin huella el pie sigue diciendo la revisión, que es lo importante.
		// Se prefiere una línea más corta a una línea que se invente algo.
		return ""
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return ""
	}
	// Doce caracteres. Es lo que caben sin que la línea del pie se parta en el
	// teléfono, y siguen siendo 48 bits: de sobra para distinguir a ojo dos
	// binarios de este proyecto, que es para lo único que sirve la abreviatura.
	// Quien necesite la palabra entera la tiene en el artefacto y en el nodo.
	return hex.EncodeToString(h.Sum(nil))[:12]
})
