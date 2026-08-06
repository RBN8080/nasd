package fsposix

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"path"
	"strings"

	"nasd/internal/almacen"
)

// Operaciones de administración — RF-16, RF-17, RF-18.
//
// Llegan en la Fase 3 y no antes por RN-06: son destructivas, y entregarlas
// sin autenticación habría dejado el disco entero al alcance de cualquiera
// en la LAN. La autenticación (RF-15) ya está puesta.

// Renombrar cubre RF-16 y RF-17: renombrar y mover son la misma operación.
//
// Dentro del volumen, rename() es ATÓMICO: o el elemento está en el destino
// o sigue en el origen, nunca en los dos ni en ninguno (ADR-0024).
func (a *Almacen) Renombrar(ctx context.Context, origen, destino almacen.RutaSegura) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if origen.EsRaiz() || destino.EsRaiz() {
		return almacen.ErrRutaInvalida
	}
	// ADR-0055: ni el contenedor de usuarios ni la raíz de uno de ellos se
	// tocan por la vía normal. La regla vive AQUÍ, en el servidor, no en la
	// interfaz: esconder un botón no impide la petición.
	if a.esReservado(origen) || a.esReservado(destino) {
		return almacen.ErrReservado
	}
	if origen.Rel() == destino.Rel() {
		return nil // nada que hacer
	}

	// Mover un directorio dentro de su propio subárbol dejaría un ciclo. El
	// kernel lo rechaza con EINVAL, pero el error del dominio es más claro
	// y se comprueba antes de tocar nada.
	if strings.HasPrefix(destino.Rel()+"/", origen.Rel()+"/") {
		return almacen.ErrDentroDeSiMismo
	}

	rOrigen, rDestino := a.real(origen), a.real(destino)

	// RF-23 vale aquí igual que al subir: NO se sobrescribe. Mover algo
	// encima de otra cosa la destruiría en silencio, y sin papelera (D-15)
	// ni segunda copia (D-12) no habría de dónde recuperarla.
	if _, err := a.raiz.Stat(rDestino); err == nil {
		return almacen.ErrYaExiste
	} else if !errors.Is(err, fs.ErrNotExist) {
		return traducir(err)
	}
	// El directorio contenedor del destino debe existir ya: crearlo por
	// nuestra cuenta sería adivinar la intención del usuario.
	if fi, err := a.raiz.Stat(path.Dir(rDestino)); err != nil {
		return traducir(err)
	} else if !fi.IsDir() {
		return almacen.ErrNoEsDirectorio
	}

	if err := a.raiz.Rename(rOrigen, rDestino); err != nil {
		return traducir(err)
	}
	// Los dos directorios afectados deben sobrevivir a un corte: el que
	// pierde la entrada y el que la gana (ADR-0024).
	if err := a.sincronizarDirectorio(path.Dir(rOrigen)); err != nil {
		return err
	}
	if path.Dir(rOrigen) != path.Dir(rDestino) {
		return a.sincronizarDirectorio(path.Dir(rDestino))
	}
	return nil
}

// Borrar elimina un archivo, o un directorio SI ESTÁ VACÍO.
//
// Un directorio con contenido devuelve ErrNoVacio: destruir un árbol entero
// exige pedirlo por su nombre, con BorrarArbol.
func (a *Almacen) Borrar(ctx context.Context, r almacen.RutaSegura) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if r.EsRaiz() {
		return almacen.ErrRutaInvalida // la raíz de datos no se borra
	}
	// ADR-0055: ni el contenedor de usuarios ni la raíz de uno de ellos se
	// tocan por la vía normal. La regla vive AQUÍ, en el servidor, no en la
	// interfaz: esconder un botón no impide la petición.
	if a.esReservado(r) {
		return almacen.ErrReservado
	}
	destino := a.real(r)

	fi, err := a.raiz.Stat(destino)
	if err != nil {
		return a.traducirEn(destino, err)
	}
	if fi.IsDir() {
		vacio, err := a.directorioVacio(destino)
		if err != nil {
			return err
		}
		if !vacio {
			return almacen.ErrNoVacio
		}
	}
	if err := a.raiz.Remove(destino); err != nil {
		return traducir(err)
	}
	return a.sincronizarDirectorio(path.Dir(destino))
}

// BorrarArbol elimina un directorio con todo su contenido.
//
// LA OPERACIÓN MÁS DESTRUCTIVA DEL PRODUCTO. Sin papelera (D-15) y con
// copia única (D-12), lo que se va aquí no vuelve. El adaptador web solo la
// invoca tras una confirmación que muestra QUÉ se va a destruir.
func (a *Almacen) BorrarArbol(ctx context.Context, r almacen.RutaSegura) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if r.EsRaiz() {
		return almacen.ErrRutaInvalida
	}
	// ADR-0055: ni el contenedor de usuarios ni la raíz de uno de ellos se
	// tocan por la vía normal. La regla vive AQUÍ, en el servidor, no en la
	// interfaz: esconder un botón no impide la petición.
	if a.esReservado(r) {
		return almacen.ErrReservado
	}
	destino := a.real(r)
	if fi, err := a.raiz.Stat(destino); err != nil {
		return a.traducirEn(destino, err)
	} else if !fi.IsDir() {
		// Sobre un archivo se comporta como Borrar: no hay árbol que talar.
		return a.Borrar(ctx, r)
	}
	if err := a.borrarRecursivo(ctx, destino); err != nil {
		return err
	}
	return a.sincronizarDirectorio(path.Dir(destino))
}

// borrarRecursivo baja primero y borra al subir: un directorio no se puede
// eliminar hasta que está vacío.
func (a *Almacen) borrarRecursivo(ctx context.Context, dir string) error {
	d, err := a.raiz.Open(dir)
	if err != nil {
		return traducir(err)
	}
	entradas, err := d.ReadDir(-1)
	d.Close()
	if err != nil {
		return traducir(err)
	}
	for _, e := range entradas {
		if err := ctx.Err(); err != nil {
			return err
		}
		hijo := path.Join(dir, e.Name())
		if e.IsDir() {
			if err := a.borrarRecursivo(ctx, hijo); err != nil {
				return err
			}
			continue
		}
		if err := a.raiz.Remove(hijo); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return traducir(err)
		}
	}
	if err := a.raiz.Remove(dir); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return traducir(err)
	}
	return nil
}

// Resumen cuenta lo que cuelga de una ruta, para que la confirmación de
// RF-18 diga qué se va a destruir en lugar de preguntar a ciegas.
func (a *Almacen) Resumen(ctx context.Context, r almacen.RutaSegura) (almacen.Conteo, error) {
	destino := a.real(r)
	fi, err := a.raiz.Stat(destino)
	if err != nil {
		return almacen.Conteo{}, a.traducirEn(destino, err)
	}
	if !fi.IsDir() {
		return almacen.Conteo{Archivos: 1, Bytes: fi.Size()}, nil
	}
	c := almacen.Conteo{EsDirectorio: true}
	if err := a.contar(ctx, destino, &c); err != nil {
		return almacen.Conteo{}, err
	}
	return c, nil
}

func (a *Almacen) contar(ctx context.Context, dir string, c *almacen.Conteo) error {
	d, err := a.raiz.Open(dir)
	if err != nil {
		return traducir(err)
	}
	entradas, err := d.ReadDir(-1)
	d.Close()
	if err != nil {
		return traducir(err)
	}
	for _, e := range entradas {
		if err := ctx.Err(); err != nil {
			return err
		}
		if e.IsDir() {
			c.Directorios++
			if err := a.contar(ctx, path.Join(dir, e.Name()), c); err != nil {
				return err
			}
			continue
		}
		c.Archivos++
		if fi, err := e.Info(); err == nil {
			c.Bytes += fi.Size()
		}
	}
	return nil
}

func (a *Almacen) directorioVacio(dir string) (bool, error) {
	d, err := a.raiz.Open(dir)
	if err != nil {
		return false, traducir(err)
	}
	defer d.Close()
	// ReadDir devuelve io.EOF cuando el directorio está vacío. Se compara con
	// errors.Is y no con el texto del error, que puede cambiar.
	entradas, err := d.ReadDir(1)
	if errors.Is(err, io.EOF) {
		return true, nil
	}
	if err != nil {
		return false, traducir(err)
	}
	return len(entradas) == 0, nil
}
