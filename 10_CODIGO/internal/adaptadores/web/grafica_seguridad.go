package web

// La gráfica de «Actividad por día» del panel de seguridad — ADR-0075.
//
// Vive aquí y no en internal/seguridad porque es una decisión de PRESENTACIÓN
// —coordenadas de un SVG concreto—, y aquel paquete no sabe de HTML: el mismo
// corte que filaOrigen aplica frente a seguridad.Origen.

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"nasd/internal/seguridad"
)

// La geometría del trazo, en unidades del viewBox. Cien de ancho y cien de
// alto, y que el SVG se estire al ancho real con preserveAspectRatio="none".
//
// margenGrafica reserva arriba y abajo la mitad del grosor del trazo. Sin él,
// el día más alto y los días en cero se dibujarían JUSTO sobre el borde del
// viewBox y el navegador les recortaría media línea: el pico saldría más
// delgado que el resto de la curva sin que nada lo explicara.
const (
	anchoSlotGrafica = 100.0 / seguridad.DiasGrafica
	altoGrafica      = 100.0
	margenGrafica    = 4.0
)

// EL EJE DE VALORES SE DIBUJA EN SU PROPIO SVG, Y ESE ES EL TRUCO ENTERO.
//
// # POR QUÉ NO PUEDE IR DENTRO DE LA GRÁFICA
//
// El SVG del trazo se estira con preserveAspectRatio="none" para ocupar el
// ancho real del panel, así que su transformación es ANISÓTROPA: a 1100 px de
// ancho, una unidad horizontal mide once veces lo que una vertical. Un <text>
// ahí dentro sale con las letras estiradas a lo ancho. Es el mismo motivo por
// el que esta gráfica nunca ha tenido círculos en los vértices —un <circle>
// saldría convertido en una elipse tumbada— y por el que el trazo necesita
// vector-effect="non-scaling-stroke".
//
// # POR QUÉ TAMPOCO PUEDE IR EN HTML CON «style»
//
// Colocar cada rótulo a su altura exigiría «style="top:37.4%"», y la CSP de
// este servidor es «style-src 'self'» sin 'unsafe-inline' (ADR-0060): el
// navegador lo descarta EN SILENCIO y los rótulos se apilarían en la esquina
// sin que nada fallara a gritos. Una clase por porcentaje —cincuenta reglas
// para colocar tres números— es la otra salida, y es peor.
//
// # LA SALIDA: UN SVG PROPIO CON ESCALA 1:1
//
// El eje es un SVG estrecho cuyo viewBox mide EXACTAMENTE lo que mide su
// caja en píxeles (anchoEje × altoEje). Con las dos medidas iguales la escala
// es 1:1 en los dos ejes, así que el texto sale sin deformar y la posición
// vertical es exacta. Las coordenadas las sigue calculando el servidor y
// viajan en atributos, como exige ADR-0017.
//
// De ahí sale la única atadura: la gráfica y su eje tienen que medir lo mismo
// de alto, y ese alto es altoEje. Está en estilo.css como «.grl,.gry{height}»,
// y si alguien cambia uno tiene que cambiar el otro.
// altoEje tiene que coincidir con el alto que estilo.css da a «.grl,.gry», y el
// viewBox del eje en seguridad.html es «0 0 46 112»: el 46 es el ancho de la
// columna del eje en esa misma regla de CSS. Son tres sitios y no dos porque el
// viewBox de un SVG es un atributo y no se puede leer de una variable de CSS.
const altoEje = 112.0

// marcaEje es un número del eje de valores con su sitio ya resuelto en los dos
// sistemas de coordenadas: el del eje, donde se escribe, y el de la gráfica,
// donde se dibuja su línea de rejilla.
type marcaEje struct {
	Valor int
	// Y es la altura en el viewBox del EJE (0..altoEje), para el <text>.
	Y float64
	// YGrafica es la misma altura en el viewBox de la GRÁFICA (0..100), para
	// la línea de rejilla. Salen del mismo cálculo a propósito: dos fórmulas
	// separadas se despegarían la una de la otra en cuanto alguien tocara una.
	YGrafica float64
}

// diaGrafica es UN día, con su geometría YA CALCULADA en unidades del viewBox.
//
// LA GEOMETRÍA VA EN ATRIBUTOS Y NO EN CSS por lo mismo que el eje: la CSP
// descartaría en silencio cualquier «style». Los atributos x, y, points y
// width de un SVG no son CSS y la política no los toca.
type diaGrafica struct {
	// X y Ancho son la banda vertical del día: la zona sensible que lleva el
	// <title> del que sale el rótulo al posar el cursor. Una línea no tiene
	// superficie que señalar, así que la banda se dibuja aparte y transparente.
	X, Ancho float64
	// Titulo es lo que sale al posar el cursor: «06/09 · 22 paquetes».
	// Etiqueta es el número de día del eje de abajo.
	Titulo   string
	Etiqueta string
}

// panelGrafica es UNA serie con SU PROPIO eje de valores.
//
// # POR QUÉ SON DOS PANELES Y NO DOS LÍNEAS EN UNO
//
// Hasta el 2026-09-10 las dos capas compartían una escala y un marco. Aquella
// decisión tenía un argumento escrito y bueno: compartiendo escala, la
// DISTANCIA entre las dos líneas es la parte del ruido que nunca llegó a pedir
// nada, y eso se leía de un vistazo.
//
// Se pierde, y a cambio se gana lo que el responsable pidió: un eje de
// valores. No se pueden tener las dos cosas. En cuanto el eje existe tiene que
// decir a qué serie se refiere, y paquetes y rechazos tienen rangos que no se
// parecen —22 contra 5 en un día normal de este nodo—: un solo eje o miente
// sobre una de las dos, o exige dos escalas verticales en el mismo marco, que
// es el error clásico de los cuadros de mando. La salida es un eje por marco.
//
// LO QUE NO SE PIERDE es la comparación día a día: las dos gráficas comparten
// el eje de días, están una encima de otra y sus bandas sensibles dan las dos
// cifras del mismo día. Lo que ya no se lee es la DIFERENCIA como una
// distancia en píxeles.
type panelGrafica struct {
	// Rotulo nombra la serie, y hace de leyenda: con un panel por serie, el
	// título del panel dice de qué habla y no hace falta una leyenda aparte.
	Rotulo string
	// Clase es la del trazo: «gp» para la capa de contexto —que además rellena
	// su área— y «gl» para la serie que se lee, un trazo limpio.
	Clase string
	// Escala es «lineal» o «√», y se ESCRIBE en la pantalla. Una escala que no
	// es proporcional y no se anuncia es una gráfica que miente sobre las
	// alturas; ver escalaDe.
	Escala string
	Max    int
	Marcas []marcaEje
	Trazo  string
	// Relleno es el área bajo el trazo, vacía cuando el panel no la dibuja.
	Relleno string
	Dias    []diaGrafica
	// Hay dice si esta serie tiene algo que dibujar. Un panel entero en cero se
	// pinta igual —el suelo es un dato: significa que no pasó nada—, pero el
	// eje no puede inventarse un máximo.
	Hay bool
}

// escalaDe decide si la serie se dibuja con escala lineal o de raíz cuadrada.
//
// # LA REGLA, Y POR QUÉ NO ES SIEMPRE LA MISMA
//
// Con escala lineal, un barrido aplasta contra el suelo todo lo demás: los 953
// rechazos del 09/09 en la red de casa contra una treintena los días normales
// dejan once días indistinguibles del cero. La raíz cuadrada conserva el orden
// y el pico, y devuelve relieve a los días corrientes.
//
// Pero la raíz NO es gratis: reparte la altura de forma desigual, y en una
// serie que va de 0 a 5 —los rechazos de Internet de este nodo— no hay nada
// que rescatar y sí un eje con los números apelotonados arriba. Por eso la
// escala se elige por la FORMA de la serie y no por gusto, y el panel escribe
// cuál está usando.
//
// El umbral es el pico contra la MEDIANA de los días con actividad, no contra
// la media: la media ya la arrastra el propio pico que se quiere detectar.
// Diez veces es donde la escala lineal deja de poder distinguir el día
// corriente del día en blanco.
func escalaDe(valores []int) bool {
	var vivos []int
	max := 0
	for _, v := range valores {
		if v > max {
			max = v
		}
		if v > 0 {
			vivos = append(vivos, v)
		}
	}
	// Sin al menos tres días con actividad no hay «lo corriente» contra lo que
	// comparar el pico, y la raíz solo serviría para deformar dos puntos.
	if len(vivos) < 3 {
		return false
	}
	slices.Sort(vivos)
	mediana := vivos[len(vivos)/2]
	return mediana > 0 && max > 10*mediana
}

// escalones son los saltos «redondos» de los que sale cada número del eje.
// Todos enteros, porque todo lo que esta gráfica cuenta son cosas enteras: no
// existe media conexión ni medio paquete. Es la corrección que pidió el
// responsable al ver un «2.5» en la maqueta.
var escalones = []int{1, 2, 5, 10, 20, 25, 50, 100, 200, 250, 500, 1000, 2000, 5000, 10000}

// marcasDelEje devuelve los números del eje: siempre el 0, siempre el máximo
// real, y entre medias los escalones redondos que quepan sin amontonarse.
//
// EL MÁXIMO REAL Y NO UN TOPE REDONDEADO. Un eje que subiera a 1000 para un
// pico de 953 desaprovecharía altura y, sobre todo, obligaría a leer el valor
// del pico en el rótulo del cursor teniendo un eje delante. Aquí el trazo toca
// el techo y el techo lleva su cifra.
//
// SEPARACIÓN MÍNIMA EN PÍXELES, no en valor: con escala de raíz los números
// altos se juntan por arriba, y dos rótulos pegados no se leen ni informan. Se
// descarta el que caiga demasiado cerca de uno ya puesto, empezando por los
// grandes, que son los que el ojo busca primero.
func marcasDelEje(max int, raiz bool) []marcaEje {
	altura := func(n int) float64 {
		return alturaEnGrafica(n, max, raiz)
	}
	marca := func(n int) marcaEje {
		y := altura(n)
		return marcaEje{
			Valor:    n,
			Y:        redondear(y / altoGrafica * altoEje),
			YGrafica: y,
		}
	}
	if max <= 0 {
		return []marcaEje{marca(0)}
	}

	// El máximo y el cero son innegociables: uno dice hasta dónde llegó y el
	// otro dónde está el suelo.
	puestas := []marcaEje{marca(max), marca(0)}
	const separacion = 26.0 // unidades del viewBox del eje, ~26 px
	cabe := func(y float64) bool {
		for _, m := range puestas {
			if math.Abs(m.Y-y) < separacion {
				return false
			}
		}
		return true
	}
	// De mayor a menor: si solo cabe uno, que sea el más alto de los
	// intermedios, que es el que más acota la lectura del pico.
	for i := len(escalones) - 1; i >= 0; i-- {
		if len(puestas) >= 4 {
			break
		}
		n := escalones[i]
		if n >= max {
			continue
		}
		if y := redondear(altura(n) / altoGrafica * altoEje); cabe(y) {
			puestas = append(puestas, marca(n))
		}
	}
	// De arriba abajo, que es como se lee un eje.
	slices.SortFunc(puestas, func(a, b marcaEje) int { return cmp.Compare(a.Y, b.Y) })
	return puestas
}

// alturaEnGrafica es la única fórmula que convierte un valor en una altura.
// Dos fórmulas iguales escritas dos veces son dos que alguien acaba tocando
// por separado, y entonces la rejilla deja de coincidir con el trazo.
func alturaEnGrafica(n, max int, raiz bool) float64 {
	util := altoGrafica - 2*margenGrafica
	suelo := altoGrafica - margenGrafica
	if max <= 0 || n <= 0 {
		return suelo
	}
	fraccion := float64(n) / float64(max)
	if raiz {
		fraccion = math.Sqrt(float64(n)) / math.Sqrt(float64(max))
	}
	return redondear(suelo - fraccion*util)
}

// panelDeSerie arma un panel a partir de una serie de valores por día.
func panelDeSerie(serie []seguridad.Dia, valores []int, rotulo, clase string, conArea bool, titulo func(seguridad.Dia) string) panelGrafica {
	max := 0
	for _, v := range valores {
		if v > max {
			max = v
		}
	}
	raiz := escalaDe(valores)
	escala := "lineal"
	if raiz {
		escala = "escala √"
	}

	p := panelGrafica{
		Rotulo: rotulo,
		Clase:  clase,
		Escala: escala,
		Max:    max,
		Marcas: marcasDelEje(max, raiz),
		Hay:    max > 0,
		Dias:   make([]diaGrafica, len(serie)),
	}

	puntos := make([]string, len(serie))
	for i, d := range serie {
		cx := redondear(float64(i)*anchoSlotGrafica + anchoSlotGrafica/2)
		y := alturaEnGrafica(valores[i], max, raiz)
		puntos[i] = fmt.Sprintf("%g,%g", cx, y)
		p.Dias[i] = diaGrafica{
			X:        redondear(float64(i) * anchoSlotGrafica),
			Ancho:    redondear(anchoSlotGrafica),
			Titulo:   titulo(d),
			Etiqueta: strconv.Itoa(d.Fecha.Day()),
		}
	}
	p.Trazo = strings.Join(puntos, " ")

	// El área es el MISMO trazo cerrado contra el suelo, sin recalcular ni un
	// punto: dos listas compuestas por separado podrían despegarse la una de la
	// otra en cuanto alguien tocara una de las dos fórmulas.
	if conArea && len(serie) > 0 {
		suelo := altoGrafica - margenGrafica
		primera := redondear(anchoSlotGrafica / 2)
		ultima := redondear(float64(len(serie)-1)*anchoSlotGrafica + anchoSlotGrafica/2)
		p.Relleno = fmt.Sprintf("%g,%g %s %g,%g", primera, suelo, p.Trazo, ultima, suelo)
	}
	return p
}

// graficaDeActividad convierte la serie del anillo en los paneles de la
// pantalla: el de paquetes —capa de contexto, con área— y el de rechazos.
//
// LO QUE ESTA GRÁFICA NO AFIRMA, Y CONVIENE NO LEERLE: que una serie contenga
// a la otra. La escalera paquete → conexión → rechazo ordena hasta dónde LLEGÓ
// cada origen, no las magnitudes: una sola conexión puede llevar muchas
// peticiones, así que un día con más rechazos que paquetes es NORMAL —los 732
// rechazos de DRIFTNET en una tarde no fueron 732 saludos TCP—.
func graficaDeActividad(serie []seguridad.Dia, conPaquetes bool) []panelGrafica {
	if len(serie) == 0 {
		return nil
	}

	rechazos := make([]int, len(serie))
	paquetes := make([]int, len(serie))
	for i, d := range serie {
		rechazos[i] = d.Rechazos
		paquetes[i] = d.Toques
	}

	var paneles []panelGrafica
	if conPaquetes {
		paneles = append(paneles, panelDeSerie(serie, paquetes, "Paquetes", "gp", true,
			func(d seguridad.Dia) string {
				return fmt.Sprintf("%s · %d paquetes", d.Fecha.Format("02/01"), d.Toques)
			}))
	}
	paneles = append(paneles, panelDeSerie(serie, rechazos, "Rechazos", "gl", false,
		func(d seguridad.Dia) string {
			return fmt.Sprintf("%s · %d rechazos", d.Fecha.Format("02/01"), d.Rechazos)
		}))
	return paneles
}
