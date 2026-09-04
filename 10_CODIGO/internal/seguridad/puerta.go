package seguridad

import (
	"net/netip"
	"time"
)

// La puerta — quién cierra, quién se lleva el mérito, y cuándo se cuenta.
//
// # EL DEFECTO QUE ESTE ARCHIVO CORRIGE
//
// Hasta aquí, el ConnState de web/seguridad.go decidía y contaba en la misma
// línea:
//
//	if s.cuarentena.Frena(ip, ahora) || s.lista.Bloquea(ip, ahora) {
//	    _ = c.Close()
//	}
//
// Tres cosas mal en una línea, y las tres silenciosas:
//
//  1. SE CONTABA ANTES DE CERRAR. Frena sumaba el frenado y devolvía true; el
//     Close() venía después y su error se descartaba. Si el cierre fallaba, el
//     panel afirmaba igualmente haber frenado una conexión que seguía viva.
//     «Frenados» era, en realidad, «coincidencias con la política».
//  2. LA ATRIBUCIÓN ERA UN ACCIDENTE DEL «||». El cortocircuito le daba
//     siempre el mérito a la cuarentena y dejaba a la lista manual sin
//     evaluar. No estaba escrito en ningún sitio: era una consecuencia del
//     operador, y habría cambiado al reordenar los dos términos.
//  3. NO SE PODÍA SABER si además coincidían las dos.
//
// Aquí se separan los tres actos —decidir, ejecutar, contar— y el orden lo
// impone el tipo: a la contabilidad solo se llega con un Cierre en la mano, y
// un Cierre solo lo devuelve Decidir.
//
// # POR QUÉ NO ES UN MOTOR DE REGLAS
//
// Hay dos políticas y no va a haber una tercera sin su propio ADR. Un registro
// de controles con prioridades configurables sería infraestructura para un
// problema que tiene exactamente dos casos, y este proyecto no escribe código
// por si acaso.

// Politica dice QUÉ control cubrió una conexión.
type Politica uint8

const (
	PoliticaNinguna Politica = iota
	// PoliticaLista — un bloqueo puesto a mano (lista.go).
	PoliticaLista
	// PoliticaCuarentena — un apartado automático por conducta (cuarentena.go).
	PoliticaCuarentena
)

func (p Politica) String() string {
	switch p {
	case PoliticaLista:
		return "lista"
	case PoliticaCuarentena:
		return "cuarentena"
	}
	return "ninguna"
}

func (p Politica) Etiqueta() string {
	switch p {
	case PoliticaLista:
		return "Bloqueo puesto a mano"
	case PoliticaCuarentena:
		return "Cuarentena por conducta"
	}
	return "Ninguna"
}

// Cierre es la DECISIÓN de cerrarle la puerta a una conexión, todavía sin
// contar.
//
// # POR QUÉ ES UN VALOR Y NO UN BOOLEANO
//
// Porque contar tiene que ocurrir DESPUÉS del cierre, y quien cierra es otra
// capa: la que posee el net.Conn. Un booleano obligaría a esa capa a volver a
// preguntar quién había coincidido —y podría obtener otra respuesta si entre
// medias caducó un apartado—, o a contar por su cuenta, que es repartir por
// dos sitios la regla que este archivo existe para tener en uno.
//
// Es una copia por valor, sin candados: lo único que guarda son dos punteros a
// estructuras que ya llevan el suyo, más la clave con la que apuntar el
// acierto.
type Cierre struct {
	responsable Politica
	// solapada es la OTRA política que también alcanzaba a esta dirección, o
	// PoliticaNinguna si solo coincidió una. Se guarda para poder DECIRLO: sin
	// esto, un apartado con cero frenados que está además bloqueado a mano se
	// lee como «no ha servido de nada» cuando lo que pasa es que la puerta ya
	// estaba cerrada por otro.
	solapada   Politica
	cuarentena *Cuarentena
	lista      *Lista
	// claveApartado es la CLAVE del apartado que cubrió, no la dirección que
	// llamó. Se llamaba «ip» y era lo mismo mientras un apartado solo alcanzaba
	// a una dirección; desde que puede alcanzar a un operador entero, la
	// dirección que llama puede no tener apartado propio y apuntarle el frenado
	// a ella lo perdería en silencio. Es el papel que idEntrada ya cumplía para
	// la lista.
	claveApartado netip.Addr
	idEntrada     string
}

// Cierra dice si hay que colgarle a esta conexión.
func (c Cierre) Cierra() bool { return c.responsable != PoliticaNinguna }

// Responsable es la política a la que se le atribuye el cierre.
func (c Cierre) Responsable() Politica { return c.responsable }

// Solapada es la otra política que también coincidía, si la hubo.
func (c Cierre) Solapada() Politica { return c.solapada }

// Ejecutado contabiliza UN cierre que YA OCURRIÓ.
//
// # SOLO SE LLAMA DESPUÉS DE UN Close() QUE DEVOLVIÓ nil
//
// Es lo que hace verdadera la palabra «Frenados» del panel. El límite de esa
// afirmación conviene tenerlo escrito: lo que se demuestra es que
// net.Conn.Close() no devolvió error, no que el otro extremo se enterara. Para
// un socket TCP eso significa que el descriptor se cerró y que el núcleo
// mandará FIN o RST; es lo máximo que este proceso puede afirmar, y por eso se
// dice así en vez de prometer más.
//
// # UNA CONEXIÓN, UN FRENADO
//
// Aunque coincidan las dos políticas, aquí solo sube UNA cifra: la del
// responsable. Contar en las dos convertiría una conexión TCP —que se cierra
// una sola vez— en «dos frenados», y el panel estaría sumando una ficción.
func (c Cierre) Ejecutado(ahora time.Time) {
	switch c.responsable {
	case PoliticaLista:
		c.lista.AnotarCierre(c.idEntrada, ahora)
	case PoliticaCuarentena:
		c.cuarentena.AnotarCierre(c.claveApartado, ahora)
	}
}

// Decidir mira los dos controles y dice cuál cierra esta conexión, SIN contar
// nada todavía.
//
// # LA PRECEDENCIA: MANDA LA DECISIÓN HUMANA
//
// Cuando las dos coinciden, el cierre se le atribuye a la LISTA MANUAL. No es
// una preferencia estética; es la elección cuyo error hace menos daño:
//
//   - Si el mérito fuera de la cuarentena, la entrada manual enseñaría cero
//     frenados mientras trabaja, y su propio comentario dice que esa cifra es
//     «la que permite RETIRAR lo que no sirve». Se retiraría una regla que sí
//     sirve, y el fallo aparecería 24 h después, al caducar el apartado.
//   - Siendo de la lista, el apartado enseña cero frenados — y es VERDAD: con
//     la puerta ya cerrada a mano, ese apartado no está frenando nada.
//     Soltarlo no tiene consecuencia, porque el bloqueo sigue en pie.
//
// Encima coincide con la jerarquía del proyecto —hecho, inferencia, DECISIÓN,
// enforcement—: la lista es una decisión firmada por una persona; la
// cuarentena, la consecuencia automática de una inferencia. Atribuir el cierre
// al escalón que alguien firmó es lo honesto.
//
// # LAS DOS SE PREGUNTAN SIEMPRE, Y NO CON «||»
//
// El cortocircuito era justo lo que impedía saber si coincidían las dos. El
// coste de preguntar siempre es recorrer una lista que en este nodo tiene cero
// o una entrada, y que topeLista acota en 512; para la conexión que NO se
// bloquea —el caso normal— no hay coste nuevo, porque antes también se
// evaluaban las dos.
func Decidir(c *Cuarentena, l *Lista, ip netip.Addr, ahora time.Time) Cierre {
	id, porLista := l.Cubre(ip, ahora)
	clave, porCuarentena := c.Cubre(ip, ahora)

	switch {
	case porLista:
		cierre := Cierre{responsable: PoliticaLista, lista: l, idEntrada: id}
		if porCuarentena {
			cierre.solapada = PoliticaCuarentena
			cierre.cuarentena, cierre.claveApartado = c, clave
		}
		return cierre
	case porCuarentena:
		return Cierre{responsable: PoliticaCuarentena, cuarentena: c, claveApartado: clave}
	}
	return Cierre{}
}
