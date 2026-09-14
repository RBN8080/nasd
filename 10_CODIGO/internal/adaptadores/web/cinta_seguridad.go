package web

// cinta_seguridad.go — la banda en vivo de la fila de órdenes de /seguridad.
//
// # QUÉ ES Y QUÉ SUSTITUYE
//
// Es una marquesina anclada en `.ordenes`, es decir DENTRO de `.top`, que es
// una fila de la rejilla del cromo y vive FUERA de `.cuerpo` —el único
// elemento con `overflow-y:auto`—. Por eso es persistente al desplazamiento
// sin `position:sticky`, sin z-index que pelear con la columna de detalle y
// sin volver a pisar la trampa de `overflow` que en agosto recortó los
// desplegables de esta misma fila.
//
// Y absorbe la pastilla `#latido`, que se retira de esta página. Eso NO es un
// adorno: `#latido` era el único indicador de si el flujo en vivo seguía
// conectado, y quitarlo sin sustituto habría dejado el panel congelado
// pareciendo vivo — el modo de fallo que ADR-0083 y 00_RECTOR.md §12.5
// persiguen. El piloto de esta cinta dice MÁS que la pastilla: lleva la hora
// del último marco recibido, así que un panel congelado se delata solo con un
// reloj detenido.
//
// # POR QUÉ ESTOS TRES SEGMENTOS Y NO OTROS — LA REGLA ES NO REPETIR
//
// La cinta solo lleva lo que NO depende del filtro, y solo si contesta una
// pregunta que la página no contesta o que solo contesta en un estado que
// puede no darse. Repetir lo que ya está a la vista no es redundancia
// inofensiva: crea un SEGUNDO sitio que decide cómo se escribe un dato, y el
// día que uno de los dos cambie no fallará a gritos.
//
//   - CONTENCIÓN VIGENTE. Es lo único de la página que pide decidir algo, y
//     se pierde de vista en cuanto uno baja a Orígenes o a la Cronología. Sus
//     cuentas viven repartidas en tres rótulos distintos y el TOTAL no está
//     escrito en ninguna parte. Se solapa con la sección que tiene debajo
//     mientras se mira la primera pantalla; es un solape aceptado a sabiendas
//     y declarado aquí, no un descuido.
//   - ÚLTIMO RECHAZO, CON FECHA. Es la respuesta a la noche del 03/09
//     (ADR-0083). Hoy esa fecha solo se pinta cuando la cronología sale
//     VACÍA: con una sola fila en la tabla, nadie puede contestar «¿y lo
//     último cuándo fue?» sin desplegar la cronología entera, que va plegada
//     y al final.
//   - ÚLTIMA PUERTA CERRADA. El único segmento sin ningún solape: hoy solo
//     existe dentro del estado vacío de la cronología, así que con una sola
//     fila en la tabla el dato NO EXISTE en la página. A un frenado se le
//     cuelga antes de que hable HTTP y por eso desaparece de las tablas; sin
//     esta línea su silencio se lee como que se fue.
//
// # LO QUE SE DEJÓ FUERA, Y POR QUÉ
//
//   - Las cinco cifras de «Volumen observado»: están a cien píxeles, dependen
//     del filtro y YA se refrescan con este mismo flujo. Meterlas obligaría a
//     la cinta a perseguir el filtro y crearía un segundo criterio para
//     escribir un «—».
//   - El estado de `nas-sensor`: lo dice la tira de estado al pie
//     (`tiraDeEstado`). Sería repetir el pie tres renglones más arriba.
//     QUEDA ANOTADO UN HUECO QUE NO ES DE ESTE TRABAJO: esa tira está en
//     `display:none` por debajo de 880 px, así que en el teléfono no se ve.
//     Se deja como hallazgo, no se arregla aquí.
//   - Países, rutas y la gráfica: historia dentro de una ventana. No
//     contestan ninguna pregunta que uno se haga estando desplazado abajo.
//
// # UNA SOLA FUENTE PARA LA PÁGINA Y PARA EL FLUJO
//
// `componerCinta` es una función PURA y es la única que escribe estos textos.
// La llaman las dos: `verSeguridad` al pintar y `marcoDeSeguridad` en cada
// marco del flujo. Es la misma disciplina de `filaViva`/`filaCuenta`
// (ADR-0051, ADR-0056): si el texto se compusiera en dos sitios, la cinta
// cambiaría de palabra sola al primer tic sin que nada fallara a gritos.

import (
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"nasd/internal/seguridad"
)

// Las claves de los segmentos son las anclas del DOM —`data-seg`—, no
// rótulos: seguridad.js opera por ellas sobre `.vl` y no toca nada más de la
// cinta. Mismo contrato que `data-cifra` en esta página, `data-clave` en
// /estado y `data-usuario` en /administracion. Si cambian aquí sin cambiar
// allí, el segmento deja de refrescarse y no falla nada a gritos.
const (
	cintaContencion = "contencion"
	cintaRechazo    = "rechazo"
	cintaFrenado    = "frenado"
)

// formatoHora es el reloj del piloto. SEGUNDOS INCLUIDOS a propósito: el dato
// llega cada 2 s, pero el reloj avanza de segundo en segundo mientras el flujo
// lo confirme, y con esa resolución un panel congelado se nota al instante —
// el reloj se queda quieto delante de quien mira.
const formatoHora = "15:04:05"

// formatoHoraCorta es la del estado «foto»: sin JavaScript no hay nada que
// contar por segundos, y anunciar segundos sobre una foto fija sería prometer
// una precisión que no existe.
const formatoHoraCorta = "15:04"

// segmentoCinta es UN trozo de la cinta con su rótulo fijo y su valor ya
// escrito.
//
// EL VALOR VIAJA COMO TEXTO, no como números, por ADR-0017: quien decide que
// una fecha se escribe «2026-09-14 19:36» y que un grupo vacío se dice «sin
// bloqueos» es el servidor, igual que en el resto de la página.
//
// La ETIQUETA no viaja en el flujo —no cambia nunca— pero la CLASE sí: la
// contención se pinta en ámbar solo cuando hay algo que contener, y eso
// cambia mientras la página está abierta.
type segmentoCinta struct {
	Clave    string `json:"clave"`
	Etiqueta string `json:"-"`
	Valor    string `json:"valor"`
	Clase    string `json:"clase"`
}

// cintaSeguridad es la cinta entera, lista para pintar.
type cintaSeguridad struct {
	Segmentos []segmentoCinta
	// Velocidad es la clase «v1…v6» que le pone su `animation-duration`.
	//
	// VIAJA COMO CLASE Y NUNCA COMO ESTILO EN LÍNEA. La CSP es
	// «style-src 'self'» sin 'unsafe-inline' (ADR-0060): un
	// `style="animation-duration:42s"` se descartaría EN SILENCIO y la cinta
	// se quedaría quieta sin que nada fallara a gritos. Es el mismo mecanismo
	// que los cubos «b0…b5» del mapa (ADR-0089) y que el medidor de disco del
	// rail.
	Velocidad string
	// Foto y FotoNota son lo que el piloto dice ANTES de que JavaScript
	// conecte el flujo — o para siempre, si no llega a ejecutarse.
	//
	// ESTO ES UNA MEJORA SOBRE LA PASTILLA QUE SUSTITUYE, no una copia.
	// `#latido` nacía OCULTO: sin JavaScript la página era una foto fija y no
	// lo decía en ninguna parte. Aquí nace diciendo de cuándo es la foto y que
	// no se refresca sola, que es lo que ADR-0083 exige de cualquier silencio.
	Foto     string
	FotoNota string
	// Hora es el reloj del último marco, para el flujo.
	Hora string
}

// hechosDeCinta son los datos que la cinta necesita, YA LEÍDOS por quien la
// llama.
//
// Se pasan en vez de leerse aquí dentro para que la cinta no vuelva a tocar el
// estado compartido: `verSeguridad` y `marcoDeSeguridad` ya tienen los
// apartados, los bloqueos y los hallazgos de ESTE instante en la mano. Leerlos
// otra vez daría dos fotos distintas y la misma página podría enseñar una
// cuenta arriba y otra abajo.
type hechosDeCinta struct {
	Apartados int
	Bloqueos  int
	Hallazgos int
	Rechazo   ultimoHecho
	Frenado   ultimoHecho
	// Red es la etiqueta de la red que se está mirando, para que el segmento
	// del rechazo diga DE DÓNDE habla. El valor sale del mismo filtro que la
	// página, así que los dos no pueden discrepar.
	Red   string
	Ahora time.Time
}

// componerCinta escribe la cinta. Es pura: mismos hechos, mismo texto.
func componerCinta(h hechosDeCinta) cintaSeguridad {
	vigente := h.Apartados + h.Bloqueos + h.Hallazgos
	// El ámbar es el color de lo que hay que mirar en esta consola
	// (ADR-0061). Sin nada vigente no hay nada que mirar, y un ámbar
	// permanente enseña a ignorar el panel (ADR-0065).
	claseContencion := ""
	if vigente > 0 {
		claseContencion = "sg-av"
	}

	segs := []segmentoCinta{
		{
			Clave:    cintaContencion,
			Etiqueta: "Contención vigente",
			Valor:    fraseDeContencion(h.Apartados, h.Bloqueos, h.Hallazgos),
			Clase:    claseContencion,
		},
		{
			Clave:    cintaRechazo,
			Etiqueta: "Último rechazo · " + h.Red,
			Valor:    fechaYQue(h.Rechazo),
		},
		{
			Clave:    cintaFrenado,
			Etiqueta: "Última puerta cerrada",
			Valor:    fechaYQue(h.Frenado),
		},
	}

	// UN SEGMENTO SIN VALOR NO SE BORRA DE LA LISTA, SE DEJA VACÍO, y lo
	// esconde el CSS. Si se omitiera, el nodo no existiría en el DOM y el
	// flujo no tendría dónde escribirlo cuando ese hecho apareciera: es el
	// límite que `cuentas.js` ya declaró para las filas que no están en la
	// página. Con el hueco reservado, la primera puerta cerrada del día hace
	// aparecer su segmento sin recargar.
	return cintaSeguridad{
		Segmentos: segs,
		Velocidad: velocidadDeCinta(segs),
		Foto:      "foto de las " + h.Ahora.Local().Format(formatoHoraCorta),
		FotoNota:  "no se refresca sola",
		Hora:      h.Ahora.Local().Format(formatoHora),
	}
}

// fechaYQue escribe «2026-09-14 19:36 · 2a01:…», o cadena vacía si no hay tal
// hecho. NO se inventa un cero: un instante cero se pintaría como el año 1, y
// decir una fecha falsa es peor que no decir ninguna — es exactamente el
// defecto que este mismo trabajo corrige en la columna «Frenados».
func fechaYQue(h ultimoHecho) string {
	if !h.Hay || h.Cuando.IsZero() {
		return ""
	}
	// .Local() por lo mismo que en la función «fecha» de las plantillas: el
	// anillo guarda con desfase local y el historial del sensor en UTC, así
	// que sin esto el mismo instante sale con dos horas distintas según de qué
	// capa venga (ver servidor.go).
	return h.Cuando.Local().Format(formatoFecha) + " · " + h.Que
}

// fraseDeContencion enumera SIEMPRE los tres grupos, con su cuenta o con un
// «sin».
//
// # POR QUÉ SE NOMBRAN TAMBIÉN LOS VACÍOS, AL REVÉS QUE fraseDeVacios
//
// Aquella frase vive DEBAJO de las tres tablas, que están a la vista: nombrar
// allí lo que sí hay sería repetir lo que se acaba de leer. Esta se lee sin
// las tablas delante —ese es su motivo de existir—, y una enumeración a la que
// le faltan grupos no se distingue de una a la que le falta un dato: «4
// apartados» a secas no dice si hay cero bloqueos o si no se están mirando.
//
// El singular y el plural se escriben, no se deducen con una «(s)»: esta
// consola no abrevia a costa del idioma.
func fraseDeContencion(apartados, bloqueos, hallazgos int) string {
	partes := []string{
		cuantos(apartados, "apartado", "apartados"),
		cuantos(bloqueos, "bloqueo", "bloqueos"),
		cuantos(hallazgos, "respuesta inesperada", "respuestas inesperadas"),
	}
	return strings.Join(partes, " · ")
}

// cuantos escribe «sin bloqueos», «1 bloqueo» o «5 bloqueos».
func cuantos(n int, singular, plural string) string {
	switch n {
	case 0:
		return "sin " + plural
	case 1:
		return "1 " + singular
	default:
		return strconv.Itoa(n) + " " + plural
	}
}

// velocidadDeCinta elige la clase de duración por LONGITUD DEL TEXTO.
//
// # POR QUÉ POR LONGITUD Y NO POR ANCHO DE PANTALLA
//
// Porque la pista lleva el texto DOS veces y se anima de `translateX(0)` a
// `translateX(-50%)`: la distancia recorrida es el ancho de UNA copia, es
// decir el ancho del texto, y no el de la ventana. Con la duración atada a la
// longitud, la velocidad en píxeles por segundo queda casi constante en el
// teléfono y en el escritorio, sin que nadie mida nada.
//
// Los cortes son aproximados a propósito: el ancho real de un carácter depende
// de la tipografía, y afinarlo exigiría medir en el navegador y escribir un
// estilo —que la CSP descarta—. Lo que sí hace seguridad.js es DETENER la
// cinta cuando el texto ya cabe entero, que es el único caso en el que
// equivocarse molesta.
func velocidadDeCinta(segs []segmentoCinta) string {
	runas := 0
	for _, s := range segs {
		if s.Valor == "" {
			continue
		}
		runas += utf8.RuneCountInString(s.Etiqueta) + utf8.RuneCountInString(s.Valor)
	}
	switch {
	case runas < 70:
		return "v1"
	case runas < 110:
		return "v2"
	case runas < 160:
		return "v3"
	case runas < 220:
		return "v4"
	case runas < 300:
		return "v5"
	default:
		return "v6"
	}
}

// etiquetaDeRed es el nombre de la red que se está mirando. Vive aquí y no en
// la plantilla para que el segmento del rechazo y el rótulo del filtro no
// puedan discrepar sobre cómo se llama «Cualquier origen».
func etiquetaDeRed(r *seguridad.Red) string {
	if r == nil {
		return "cualquier origen"
	}
	return r.Etiqueta()
}
