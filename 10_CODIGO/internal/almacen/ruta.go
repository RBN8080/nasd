// Package almacen es el NÚCLEO del dominio (ADR-0014).
//
// REGLA DE DEPENDENCIA: este paquete no importa net/http, os, ni ninguna
// infraestructura. Solo la biblioteca estándar sin efectos de E/S.
// Hay una prueba que lo verifica: no es una buena intención.
package almacen

import (
	"path"
	"strings"
)

// RutaSegura es una ruta garantizada dentro de la raíz de datos.
//
// El campo es privado y NuevaRuta es el único constructor: fuera de este
// paquete es imposible fabricar una RutaSegura que no haya pasado la
// validación. RNF-06 y CWE-22 dejan de depender de que alguien se acuerde
// de comprobar, y pasan a ser una propiedad del sistema de tipos.
//
// Esta es la capa 1 de las tres de 04_SEGURIDAD.md §2. La capa 2 (os.Root,
// que además cubre los enlaces simbólicos) vive en el adaptador.
type RutaSegura struct {
	rel string // limpia, relativa, nunca vacía; la raíz es "."
}

// Raiz devuelve la ruta de la raíz de datos.
func Raiz() RutaSegura { return RutaSegura{rel: "."} }

// Rel devuelve la ruta relativa a la raíz de datos, con "/" como separador.
// Nunca empieza por "/" ni contiene "..".
func (r RutaSegura) Rel() string { return r.rel }

// EsRaiz indica si la ruta es la raíz de datos.
func (r RutaSegura) EsRaiz() bool { return r.rel == "." }

// Nombre devuelve el último componente.
func (r RutaSegura) Nombre() string {
	if r.EsRaiz() {
		return "/"
	}
	return path.Base(r.rel)
}

// Padre devuelve el directorio contenedor. La raíz es su propio padre:
// subir desde la raíz no puede salirse (RF-08).
func (r RutaSegura) Padre() RutaSegura {
	if r.EsRaiz() {
		return r
	}
	return RutaSegura{rel: path.Dir(r.rel)}
}

// Hija devuelve la ruta de un elemento contenido en r.
// El nombre se valida como componente único: no puede contener separadores.
func (r RutaSegura) Hija(nombre string) (RutaSegura, error) {
	if err := validarComponente(nombre); err != nil {
		return RutaSegura{}, err
	}
	if r.EsRaiz() {
		return RutaSegura{rel: nombre}, nil
	}
	return RutaSegura{rel: r.rel + "/" + nombre}, nil
}

// Ascendencia devuelve la cadena de rutas desde la raíz hasta r, incluida.
// Sirve para las migas de pan de la interfaz.
func (r RutaSegura) Ascendencia() []RutaSegura {
	if r.EsRaiz() {
		return []RutaSegura{r}
	}
	partes := strings.Split(r.rel, "/")
	out := make([]RutaSegura, 0, len(partes)+1)
	out = append(out, Raiz())
	acc := ""
	for _, p := range partes {
		if acc == "" {
			acc = p
		} else {
			acc += "/" + p
		}
		out = append(out, RutaSegura{rel: acc})
	}
	return out
}

// NuevaRuta es la ÚNICA forma de obtener una RutaSegura a partir de datos
// que vienen del exterior.
//
// Rechaza, sin intentar corregir (P5 — fallo explícito, no saneo silencioso):
//   - rutas absolutas, en forma POSIX o de Windows
//   - cualquier componente "." o ".."
//   - componentes vacíos
//   - el byte NUL y los caracteres de control
//
// Nota: el saneo por sustitución es una fuente clásica de fallos ("....//"
// se convierte en "../" al eliminar "../" una sola vez). Aquí no se sustituye
// nada: se valida y se rechaza.
func NuevaRuta(entradaDelUsuario string) (RutaSegura, error) {
	s := entradaDelUsuario

	if s == "" || s == "/" || s == "." {
		return Raiz(), nil
	}
	if strings.ContainsRune(s, 0) {
		return RutaSegura{}, ErrRutaInvalida
	}
	// Rutas absolutas de Windows: "C:\..." o "\\servidor\...".
	if strings.HasPrefix(s, "/") || strings.HasPrefix(s, `\`) || tieneUnidadWindows(s) {
		return RutaSegura{}, ErrRutaInvalida
	}
	// La barra invertida es un carácter válido en un nombre POSIX, pero
	// aquí solo llegan rutas de una URL: tratarla como separador evita
	// ambigüedad con los clientes de Windows.
	if strings.ContainsRune(s, '\\') {
		return RutaSegura{}, ErrRutaInvalida
	}

	partes := strings.Split(s, "/")
	limpias := make([]string, 0, len(partes))
	for i, p := range partes {
		// Una barra final ("carpeta/") es admisible.
		if p == "" && i == len(partes)-1 {
			continue
		}
		if err := validarComponente(p); err != nil {
			return RutaSegura{}, err
		}
		limpias = append(limpias, p)
	}
	if len(limpias) == 0 {
		return Raiz(), nil
	}

	rel := path.Join(limpias...)
	// Cinturón y tirantes: path.Join ya no puede producir ".." aquí, pero
	// si algún día cambiara la validación de arriba, esto sigue en pie.
	if rel == ".." || strings.HasPrefix(rel, "../") || path.IsAbs(rel) {
		return RutaSegura{}, ErrRutaInvalida
	}
	return RutaSegura{rel: rel}, nil
}

// validarComponente comprueba un único componente de ruta: un nombre de
// archivo o de directorio. Se usa también al crear directorios y al aceptar
// el nombre de un archivo subido (CWE-434, 04_SEGURIDAD.md §4).
func validarComponente(p string) error {
	switch p {
	case "":
		return ErrRutaInvalida // "a//b" — componente vacío
	case ".", "..":
		return ErrRutaInvalida
	}
	if strings.ContainsAny(p, "/\\") {
		return ErrRutaInvalida
	}
	for _, r := range p {
		if r < 0x20 || r == 0x7f {
			return ErrRutaInvalida // controles y NUL
		}
	}
	if len(p) > 255 { // límite de nombre de ext4
		return ErrRutaInvalida
	}
	return nil
}

// NombreDeArchivo valida el nombre propuesto para un archivo subido.
// Toma SOLO el último componente: un cliente que envíe "../../x" en el
// nombre de la parte multipart no consigue nada.
func NombreDeArchivo(propuesto string) (string, error) {
	propuesto = strings.TrimSpace(propuesto)
	// Los clientes de Windows envían la ruta completa en algunos casos.
	if i := strings.LastIndexAny(propuesto, `/\`); i >= 0 {
		propuesto = propuesto[i+1:]
	}
	if err := validarComponente(propuesto); err != nil {
		return "", err
	}
	return propuesto, nil
}

func tieneUnidadWindows(s string) bool {
	if len(s) < 2 || s[1] != ':' {
		return false
	}
	c := s[0]
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}
