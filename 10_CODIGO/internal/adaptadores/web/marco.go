package web

import (
	"net/http"

	"nasd/internal/autenticacion"
)

// marco.go — el cromo compartido de la consola (ADR-0075): rail en el
// navegador, pestañas al pie en el teléfono, y el título de cada página.
//
// # POR QUÉ EXISTE ESTE ARCHIVO Y NO CINCO COPIAS
//
// Antes de esto, «Estado», «Administración» y «Seguridad» aparecían o no en
// la barra de CADA plantilla, con el mismo condicional repetido a mano en
// listado.html, estado.html, administracion.html y seguridad.html — la
// misma clase de defecto que _cabeza.html ya documentó una vez para el
// <head>: cuatro copias del mismo enlace son la tercera vez que alguien
// arregla una y se olvida de las otras tres.
//
// # ESTO NO DECIDE PERMISOS, LOS LEE
//
// modulos no vuelve a derivar autoridad: cada Visible/Marca llama a las
// MISMAS funciones que ya deciden quién entra por la puerta real —
// usuarioDe, acotadoPorRed (sesion.go) — y a s.novedades.Cuantas(), que ya
// es la única fuente de esa cifra. Si el rail dijera una cosa y el servidor
// hiciera otra, sería exactamente la asimetría que D-21 obliga a comprobar
// vía por vía. Esconder un enlace aquí sigue siendo cortesía, nunca control:
// quien teclee la ruta a mano se topa con soloSuperusuario o soloDesdeDentro
// de todos modos.
//
// # AÑADIR UN MÓDULO
//
// Una entrada en `modulos` y una plantilla que la use. Nada en marco.go, en
// _marco.html ni en el CSS del rail conoce el NOMBRE de ningún módulo — lo
// único que conocen es esta lista, recorrida.

// itemMarco es un módulo YA resuelto para una petición concreta: visible o
// no, activo o no, con su marca si la tiene.
type itemMarco struct {
	Clave  string
	Rotulo string
	Ruta   string
	Activo bool
	// Marca es el contador de la pestaña — 0 no se pinta (lo decide la
	// plantilla, ver estilo.css «.ct:empty» no hace falta: la condición va en
	// el {{if}}).
	Marca int
	// Grupo separa los módulos "de sistema" en el rail con un rótulo propio.
	// Cadena vacía = sin separador encima.
	Grupo string
}

// marco es lo que _marco.html necesita para pintar el cromo alrededor de una
// página. Csrf viaja aquí porque el formulario de «Salir» vive en el propio
// marco y no en cada plantilla — antes se repetía en las cuatro.
type marco struct {
	Titulo string
	Sub    string
	Items  []itemMarco
	Csrf   string
}

// modulo es UNA fila de la tabla del rail, antes de resolverla contra una
// petición concreta.
type modulo struct {
	clave, rotulo, ruta, grupo string
	// visible es nil para «siempre visible» (Archivos, hoy el único caso).
	visible func(r *http.Request) bool
	// marca es nil para «sin contador».
	marca func(s *Servidor, r *http.Request) int
}

// esSuperusuario y puedeAdministrar son las DOS reglas que ya gobiernan las
// rutas reales (ver sesion.go: soloSuperusuario, soloDesdeDentro,
// acotadoPorRed). Se nombran aquí para que la tabla de abajo se lea como una
// frase y no como una expresión.
func esSuperusuario(r *http.Request) bool {
	return usuarioDe(r) == autenticacion.NombreSuperusuario
}

func puedeAdministrarDesde(r *http.Request) bool {
	return esSuperusuario(r) && !acotadoPorRed(r)
}

// modulos es la ÚNICA tabla que sabe qué módulos existen, en qué orden y
// quién los ve. El resto del cromo solo la recorre.
//
// «Resumen» vive en su propia ruta —/resumen— y NO en «/»: «/» es, para las
// dos cuentas, el listado de archivos, y urlDeListado(raíz) == "/" es lo que
// usan TODAS las migas de pan y el enlace «subir» del listado. Servir un
// panel distinto en «/» solo para el superusuario habría roto ese enlace
// justamente para la cuenta que más navega — «subir» desde una carpeta de
// primer nivel habría llevado a Resumen en vez de a la raíz del propio
// árbol. Un módulo nuevo con ruta propia no tiene ese problema; reaprovechar
// una ruta que ya significa otra cosa, sí.
var modulos = []modulo{
	{clave: "resumen", rotulo: "Resumen", ruta: "/resumen", visible: esSuperusuario},
	{clave: "archivos", rotulo: "Mis archivos", ruta: "/"},
	{clave: "estado", rotulo: "Estado", ruta: "/estado", grupo: "Sistema", visible: esSuperusuario},
	{clave: "seguridad", rotulo: "Seguridad", ruta: "/seguridad", grupo: "Sistema", visible: esSuperusuario,
		marca: func(s *Servidor, r *http.Request) int { return s.novedades.Cuantas() }},
	{clave: "cuentas", rotulo: "Cuentas", ruta: "/administracion", grupo: "Sistema", visible: puedeAdministrarDesde},
}

// construirMarco resuelve la tabla contra ESTA petición. Cada handler la
// llama con la clave del módulo que está sirviendo y el título de su
// página — lo mismo que hoy hace cada uno con EsSuperusuario/
// PuedeAdministrar/Novedades, solo que en un sitio y no en cuatro.
func (s *Servidor) construirMarco(r *http.Request, activo, titulo, sub string) marco {
	m := marco{Titulo: titulo, Sub: sub, Csrf: s.csrfDe(r)}
	for _, def := range modulos {
		if def.visible != nil && !def.visible(r) {
			continue
		}
		item := itemMarco{
			Clave:  def.clave,
			Rotulo: def.rotulo,
			Ruta:   def.ruta,
			Grupo:  def.grupo,
			Activo: def.clave == activo,
		}
		if def.marca != nil {
			item.Marca = def.marca(s, r)
		}
		m.Items = append(m.Items, item)
	}
	return m
}
