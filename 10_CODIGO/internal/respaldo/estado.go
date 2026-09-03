// Package respaldo lee lo que el cliente de respaldo de Windows deja escrito
// en este nodo, y nada más.
//
// # POR QUÉ EL NODO MIRA ESTO, SI EL EQUIPO YA TIENE SU PROPIO INDICADOR
//
// Porque el icono de la barra del equipo vive DENTRO del equipo, y el modo de
// fallo que más importa —que el equipo se apague, se cuelgue o deje de correr—
// se lleva por delante al testigo junto con lo vigilado. 30_CLIENTE_RESPALDO
// §10.2 lo dice de su propio icono: «el silencio del icono nunca es prueba de
// nada».
//
// El nodo está encendido siempre y ya recibe el archivo, así que puede afirmar
// una cosa que el equipo apagado no puede afirmar de sí mismo: que lo último
// que llegó aquí es viejo.
//
// # ESTE PAQUETE NO DECIDE NADA
//
// Convierte texto en valores y dice qué pudo leer. El veredicto lo emite
// evaluarRespaldo() en el adaptador web, con la MISMA función evaluar() que
// pinta /estado y que alimenta el ciclo de avisos — igual que todos los demás
// indicadores. No hay un segundo criterio.
//
// # LO QUE LEE ES TEXTO AJENO, Y SE TRATA COMO TAL
//
// El archivo lo escribe un cliente Windows sobre SMB. Todo lo que entra por
// aquí está acotado: número de líneas, longitud de clave y de valor. Un archivo
// gigante o con basura tiene que producir «no se pudo leer», nunca un consumo
// de memoria proporcional a lo que alguien decida escribir.
package respaldo

import (
	"bufio"
	"errors"
	"io"
	"strconv"
	"strings"
	"time"
)

// Los topes. Son [R] —criterio de ingeniería— y existen porque este es el único
// sitio del producto donde entra un archivo escrito por otra máquina.
//
// El ESTADO.txt real medido el 2026-09-02 tiene 20 líneas y 480 bytes; los
// topes dejan un orden de magnitud de holgura y siguen siendo insignificantes
// en RAM, que es lo que RNF-01 exige del servicio.
const (
	topeLineas     = 200
	topeClave      = 64
	topeValor      = 512
	topeBytesTotal = 64 * 1024
)

// ErrVacio distingue «el archivo está y no dice nada» de «el archivo no está».
// Sin esa distinción, un archivo truncado a cero bytes por un apagón se leería
// igual que uno que nunca se escribió.
var ErrVacio = errors.New("respaldo: el estado publicado está vacío")

// Estado es lo que el cliente publicó de su última corrida.
//
// Los campos son los que el nodo necesita para juzgar, NO todos los que el
// archivo trae: el cliente escribe además claves de la copia fría y de sus
// comprobaciones, que son suyas y se leen en su propio tablero. Traerlas aquí
// sería que el nodo opinara sobre un disco que no ve.
type Estado struct {
	// Equipo es de qué máquina habla. El árbol del nodo espeja una ruta por
	// equipo (30_CLIENTE_RESPALDO §8), así que un día puede haber más de uno.
	Equipo string
	// Veredicto es la palabra del CLIENTE: Protegido, Copiando, Atencion,
	// Falla o SinDatos. Se conserva tal cual y no se traduce aquí: traducir es
	// decidir, y decidir es del adaptador.
	Veredicto string
	Detalle   string
	// Momento es cuándo se escribió, en el reloj del EQUIPO.
	//
	// El cliente lo escribe sin zona horaria («2026-09-02T15:40:25») porque su
	// propio icono lo lee con el mismo reloj. Aquí se interpreta en la zona
	// local del nodo, que es la misma casa y el mismo huso; si algún día
	// dejaran de serlo, la edad saldría desplazada. Por eso el adaptador
	// comprueba que no venga del FUTURO y, si viene, lo dice en vez de enseñar
	// una edad inventada.
	Momento time.Time

	Raices    int
	Copias    int
	Fallos    int
	Huerfanos int
	// Centinelas es «8/8» tal cual. Es una fracción, no un número, y partirla
	// aquí obligaría a decidir qué hacer con un formato que no cuadre.
	Centinelas string

	// Ventana es a qué ventana de la cadencia perteneció la corrida, o «a mano»
	// si la lanzó una persona (30_CLIENTE_RESPALDO §12.quindecies).
	Ventana string

	// HorasParaAvisar es el umbral que el PROPIO CLIENTE promete, publicado por
	// él porque vive en su archivo de configuración y este nodo no lo puede
	// leer.
	//
	// CERO SIGNIFICA «NO LO PUBLICÓ», y entonces el adaptador usa su valor por
	// omisión y lo dice. No se escribe aquí un 22 de repuesto: sería un segundo
	// criterio para la misma pregunta, que es justo lo que publicar el umbral
	// viene a impedir.
	HorasParaAvisar int
	// HuecoMaximoHoras es el hueco recibol entre corridas que produce la
	// cadencia del cliente. Se lee para poder ENSEÑARLO junto al umbral: un
	// umbral sin el hueco que lo justifica es un número suelto.
	HuecoMaximoHoras float64
}

// Leer analiza el ESTADO.txt publicado.
//
// El formato es «clave=valor», una por línea, con «#» para los comentarios: lo
// eligió el cliente para que una persona pueda leerlo en una emergencia sin
// analizador. Aquí se respeta tal cual y no se pide JSON, porque el archivo se
// escribe una vez cada ocho horas y se lee con los ojos el día malo.
//
// LO DESCONOCIDO SE IGNORA EN SILENCIO, y es deliberado: el cliente publica
// claves que son suyas —la copia fría, las huellas, la semilla— y el día que
// añada una más, este nodo no tiene por qué dejar de funcionar.
//
// LO CONOCIDO PERO ILEGIBLE TAMPOCO REVIENTA: un «raices=muchas» deja el campo
// a cero en vez de tumbar la lectura entera. Perder un contador es un dato
// menos; perder el archivo entero es quedarse sin saber si hubo respaldo.
func Leer(r io.Reader) (Estado, error) {
	var e Estado
	sc := bufio.NewScanner(io.LimitReader(r, topeBytesTotal))
	sc.Buffer(make([]byte, 0, 4096), topeValor+topeClave+2)

	lineas, utiles := 0, 0
	for sc.Scan() {
		lineas++
		if lineas > topeLineas {
			break
		}
		linea := strings.TrimSpace(sc.Text())
		if linea == "" || strings.HasPrefix(linea, "#") {
			continue
		}
		clave, valor, hay := strings.Cut(linea, "=")
		if !hay {
			continue
		}
		clave = strings.TrimSpace(clave)
		valor = strings.TrimSpace(valor)
		if clave == "" || len(clave) > topeClave || len(valor) > topeValor {
			continue
		}
		utiles++

		switch clave {
		case "equipo":
			e.Equipo = valor
		case "estado":
			e.Veredicto = valor
		case "detalle":
			e.Detalle = valor
		case "momento":
			e.Momento = momentoDelCliente(valor)
		case "ventana":
			e.Ventana = valor
		case "centinelas":
			e.Centinelas = valor
		case "raices":
			e.Raices = entero(valor)
		case "copias":
			e.Copias = entero(valor)
		case "fallos":
			e.Fallos = entero(valor)
		case "huerfanos":
			e.Huerfanos = entero(valor)
		case "horas_para_avisar":
			e.HorasParaAvisar = entero(valor)
		case "hueco_maximo_horas":
			if f, err := strconv.ParseFloat(valor, 64); err == nil && f > 0 {
				e.HuecoMaximoHoras = f
			}
		}
	}
	if err := sc.Err(); err != nil {
		return Estado{}, err
	}
	// SIN VEREDICTO NO HAY ESTADO. Un archivo con líneas pero sin la clave que
	// dice cómo quedó la corrida no es un estado a medias: es un archivo que no
	// se puede usar, y decirlo es mejor que devolver un cero que se lea como
	// «SinDatos» legítimo.
	if utiles == 0 || e.Veredicto == "" {
		return Estado{}, ErrVacio
	}
	return e, nil
}

// momentoDelCliente admite las dos formas que puede tener la marca.
//
// La que escribe hoy el cliente es la ordenable sin zona, que es lo que
// «Get-Date -Format 's'» produce en PowerShell. Se acepta además RFC 3339 por
// si algún día la escribiera con zona: costaría una línea entonces y una
// depuración larga hoy, cuando el síntoma sería una edad que no cuadra.
//
// Devuelve el cero si no se reconoce. Quien juzga distingue el cero de una
// fecha, y ese caso lo enseña como «no se pudo leer cuándo».
func momentoDelCliente(v string) time.Time {
	if t, err := time.ParseInLocation("2006-01-02T15:04:05", v, time.Local); err == nil {
		return t
	}
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t
	}
	return time.Time{}
}

// entero devuelve 0 para lo que no lo sea. Ver el comentario de Leer sobre por
// qué un contador ilegible no tumba la lectura.
func entero(v string) int {
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 0
	}
	return n
}
