package web

import (
	"strings"
	"unicode"

	"nasd/internal/almacen"
)

// Orden del listado — carpetas primero y numeración natural.
//
// # POR QUÉ SE ORDENA AQUÍ Y NO EN EL ALMACÉN
//
// El puerto Almacen entrega un ITERADOR a propósito: D-11 dejó los directorios
// sin cota y RNF-04 exige que el servicio no crezca con ellos. Ordenar exige
// tener todas las entradas a la vez, así que hacerlo allí obligaría a acumular
// un directorio de tamaño desconocido — justo lo que aquel diseño evita.
//
// Aquí no cuesta nada nuevo: el manejador YA acumula hasta
// maxEntradasPorPagina (2000) para poder renderizar. Se ordena lo que ya está
// en memoria, y el almacén sigue siendo un flujo sin cota.
//
// # EL LÍMITE QUE ESTO TIENE, DICHO EN VOZ ALTA
//
// El corte a 2000 ocurre ANTES de ordenar, porque ordenar lo que aún no se ha
// leído es imposible. En un directorio con más de 2000 entradas se ordenan las
// 2000 que devolvió el sistema de archivos, que NO son las 2000 primeras del
// alfabeto. La vista ya avisa de que el listado va recortado.
//
// No se disimula porque el orden hace que el listado PAREZCA completo y
// autoritativo, y esa apariencia es nueva: sin orden, nadie daba por hecho que
// estuviera todo.

// criterio es por cuál de las tres columnas se ordena el listado.
//
// SIN ALTERNANCIA, por petición explícita del responsable: cada encabezado
// tiene UNA dirección fija y pulsarlo dos veces no invierte nada. El motivo
// es que la dirección útil de cada columna no es discutible —los archivos se
// buscan de la A a la Z, los grandes son los que estorban, y lo reciente es
// lo que se acaba de tocar—, y una flechita que cambia de sentido obliga a
// mirar el encabezado para saber qué se está viendo.
type criterio string

const (
	porNombre     criterio = "nombre"
	porTamano     criterio = "tamano"
	porModificado criterio = "modificado"
)

// criterioDe traduce lo que llega por la URL. Cualquier cosa que no reconozca
// cae en el orden por nombre, que es el de siempre: un parámetro escrito a
// mano no debe dejar el listado en un estado que la interfaz no sabe dibujar.
func criterioDe(s string) criterio {
	switch criterio(s) {
	case porTamano:
		return porTamano
	case porModificado:
		return porModificado
	default:
		return porNombre
	}
}

// ordenar aplica el orden por nombre, que es el de siempre y el de por
// omisión. Se conserva como envoltorio para no tocar sus llamadas.
func ordenar(es []almacen.Entrada) { ordenarPor(es, porNombre) }

// ordenarPor deja SIEMPRE los directorios delante —«carpetas siempre arriba
// primero», dicho por el responsable— y aplica el criterio dentro de cada
// grupo. Es estable respecto al criterio, no respecto a la lectura del disco.
func ordenarPor(es []almacen.Entrada, c criterio) {
	sortStable(es, func(a, b almacen.Entrada) bool { return antesQue(a, b, c) })
}

// antesQue es EL criterio de orden del listado, escrito una sola vez.
//
// Estaba dentro de ordenarPor como función anónima. Sale a la luz porque el
// visor necesita el MISMO criterio SIN ordenar nada: para saber cuál es la foto
// anterior y cuál la siguiente basta comparar de dos en dos sobre el flujo del
// directorio (ver vecinosDe en apertura.go). Con el criterio escrito en dos
// sitios, ordenar el listado por fecha y pasar fotos por fecha podrían
// discrepar, y ese defecto no se ve: se manifiesta como que las flechas «se
// saltan una».
//
// Es un orden TOTAL sobre nombres distintos —el desempate final compara el
// texto tal cual—, y de eso depende que vecinosDe encuentre exactamente un
// anterior y un siguiente en lugar de un empate ambiguo.
func antesQue(a, b almacen.Entrada, c criterio) bool {
	if a.EsDirectori != b.EsDirectori {
		return a.EsDirectori
	}
	switch c {
	case porTamano:
		// LAS CARPETAS NO SE ORDENAN POR TAMAÑO, y no es un olvido: la
		// tabla no muestra ninguno para ellas —el dato que trae el
		// sistema de archivos es el peso de la entrada de directorio, no
		// el de su contenido—, así que ordenarlas por él saldría como un
		// orden aleatorio sin columna que lo explique. Se quedan por
		// nombre, que es el único orden suyo que el usuario puede ver.
		if a.EsDirectori {
			break
		}
		if a.Tamano != b.Tamano {
			return a.Tamano > b.Tamano
		}
	case porModificado:
		if !a.Modificado.Equal(b.Modificado) {
			return a.Modificado.After(b.Modificado)
		}
	}
	// Desempate —y criterio único de porNombre—: natural y estable, para
	// que dos archivos del mismo tamaño no bailen entre recargas.
	return compararNatural(a.Nombre, b.Nombre) < 0
}

// sortStable es una inserción binaria simple. Con el techo de 2000 entradas de
// maxEntradasPorPagina no hay motivo para nada más elaborado, y así el criterio
// de arriba se lee de una vez sin envoltorios.
func sortStable(es []almacen.Entrada, menor func(a, b almacen.Entrada) bool) {
	for i := 1; i < len(es); i++ {
		e := es[i]
		j := i - 1
		for j >= 0 && menor(e, es[j]) {
			es[j+1] = es[j]
			j--
		}
		es[j+1] = e
	}
}

// compararNatural ordena «2_x» ANTES que «10_x».
//
// El orden alfabético compara carácter a carácter, así que «10» va antes que
// «2» porque '1' < '2'. Con carpetas numeradas —que es como el responsable
// organiza las suyas— eso las desordena justo donde el número importa.
//
// Aquí, cuando ambos nombres llegan a un tramo de dígitos, ese tramo se compara
// como NÚMERO. El resto se compara sin distinguir mayúsculas, para que
// «Fotos» y «fotos» queden juntos en lugar de separados por todo el bloque de
// minúsculas de la tabla ASCII.
//
// Devuelve <0, 0 o >0, como cualquier comparador.
func compararNatural(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	i, j := 0, 0

	for i < len(ra) && j < len(rb) {
		if unicode.IsDigit(ra[i]) && unicode.IsDigit(rb[j]) {
			// Se toman los dos tramos de dígitos enteros y se comparan por
			// valor. NO se convierten a entero: un nombre puede traer cien
			// dígitos y desbordaría. Comparar longitud y luego texto da el
			// mismo resultado sin ese riesgo.
			ini, fin := i, j
			for i < len(ra) && unicode.IsDigit(ra[i]) {
				i++
			}
			for j < len(rb) && unicode.IsDigit(rb[j]) {
				j++
			}
			na := strings.TrimLeft(string(ra[ini:i]), "0")
			nb := strings.TrimLeft(string(rb[fin:j]), "0")
			if len(na) != len(nb) {
				return len(na) - len(nb)
			}
			if c := strings.Compare(na, nb); c != 0 {
				return c
			}
			// Mismo valor: «007» y «7». Se sigue comparando el resto del
			// nombre, y el desempate final lo da la longitud del tramo.
			continue
		}

		ca, cb := unicode.ToLower(ra[i]), unicode.ToLower(rb[j])
		if ca != cb {
			if ca < cb {
				return -1
			}
			return 1
		}
		i++
		j++
	}

	// Uno es prefijo del otro: el más corto va primero.
	switch {
	case i < len(ra):
		return 1
	case j < len(rb):
		return -1
	}
	// Iguales ignorando mayúsculas. Se desempata con el texto tal cual para
	// que el orden sea determinista y no dependa del orden de lectura.
	return strings.Compare(a, b)
}
