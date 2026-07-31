package fsposix

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"strings"

	"nasd/internal/almacen"
)

// Persistencia de subidas reanudables — cierra la divergencia con ADR-0027.
//
// EL PROBLEMA QUE RESUELVE: el registro de subidas vivía SOLO EN MEMORIA. Un
// reinicio del servicio —una actualización, un corte de luz, un
// Restart=on-failure— dejaba el archivo parcial en disco con todos sus bytes
// intactos y a nadie que supiera adónde iba.
//
// ADR-0027 ya lo preveía: «Lo único que sí necesita metadato es el nombre
// original y la ruta de destino, que no se deducen del tamaño». Aquí se
// implementa.
//
// LO QUE SIGUE SIN GUARDARSE, Y ES DELIBERADO: el desplazamiento. Ese ES el
// tamaño del archivo parcial, y por eso no puede desincronizarse de los datos
// ni perderse en un corte. El .meta guarda solo lo que no se deduce.

const sufijoMeta = ".meta"

// metaEnDisco es lo que se serializa junto al parcial. Deliberadamente
// mínimo: cuanto menos se guarde, menos puede mentir.
type metaEnDisco struct {
	Ruta  string `json:"ruta"`  // destino final, relativo a la raíz de datos
	Total int64  `json:"total"` // tamaño anunciado por el cliente
}

func (a *Almacen) rutaMeta(id string) string {
	return path.Join(subParciales, id+sufijoMeta)
}

func (a *Almacen) rutaParte(id string) string {
	return path.Join(subParciales, id+".part")
}

// CrearReanudable abre una escritura cuyo destino queda anotado en disco.
//
// El .meta se escribe y se sincroniza ANTES de devolver el escritor: si el
// servicio muriera entre ambas cosas, un parcial sin .meta es basura
// identificable, mientras que un parcial con .meta erróneo sería peor.
func (a *Almacen) CrearReanudable(ctx context.Context, r almacen.RutaSegura, total int64) (almacen.Parcial, almacen.EscrituraAtomica, error) {
	if err := ctx.Err(); err != nil {
		return almacen.Parcial{}, nil, err
	}
	if r.EsRaiz() {
		return almacen.Parcial{}, nil, almacen.ErrRutaInvalida
	}
	destino := real(r)

	// RF-23 desde el principio: no se empieza a subir 5 GB para descubrir al
	// final que el destino estaba ocupado.
	if _, err := a.raiz.Stat(destino); err == nil {
		return almacen.Parcial{}, nil, almacen.ErrYaExiste
	} else if !errors.Is(err, fs.ErrNotExist) {
		return almacen.Parcial{}, nil, traducir(err)
	}
	if fi, err := a.raiz.Stat(path.Dir(destino)); err != nil {
		return almacen.Parcial{}, nil, traducir(err)
	} else if !fi.IsDir() {
		return almacen.Parcial{}, nil, almacen.ErrNoEsDirectorio
	}

	id, err := identificador()
	if err != nil {
		return almacen.Parcial{}, nil, err
	}

	if err := a.escribirMeta(id, metaEnDisco{Ruta: r.Rel(), Total: total}); err != nil {
		return almacen.Parcial{}, nil, err
	}

	e, err := a.abrirParcial(id, destino)
	if err != nil {
		a.raiz.Remove(a.rutaMeta(id))
		return almacen.Parcial{}, nil, err
	}
	e.id = id

	return almacen.Parcial{ID: id, Ruta: r, Total: total, Escrito: 0}, e, nil
}

// ReabrirParcial recupera una subida por su identificador, colocando el
// desplazamiento al final de lo ya escrito.
func (a *Almacen) ReabrirParcial(ctx context.Context, id string) (almacen.Parcial, almacen.EscrituraAtomica, error) {
	if err := ctx.Err(); err != nil {
		return almacen.Parcial{}, nil, err
	}
	// El identificador viene del cliente: se valida antes de tocar el disco.
	if !idValido(id) {
		return almacen.Parcial{}, nil, almacen.ErrRutaInvalida
	}

	m, err := a.leerMeta(id)
	if err != nil {
		return almacen.Parcial{}, nil, err
	}
	ruta, err := almacen.NuevaRuta(m.Ruta)
	if err != nil {
		return almacen.Parcial{}, nil, err
	}

	f, err := a.raiz.OpenFile(a.rutaParte(id), os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return almacen.Parcial{}, nil, traducir(err)
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return almacen.Parcial{}, nil, traducir(err)
	}

	e := &escritura{
		a:        a,
		f:        f,
		id:       id,
		temporal: a.rutaParte(id),
		destino:  real(ruta),
		// El desplazamiento se toma del TAMAÑO REAL del archivo, nunca de un
		// metadato. Es la garantía de ADR-0027.
		escrito: fi.Size(),
	}
	return almacen.Parcial{
		ID: id, Ruta: ruta, Total: m.Total,
		Escrito: fi.Size(), Modificado: fi.ModTime(),
	}, e, nil
}

// Reanudables recorre estado/parciales/ y devuelve lo que sobrevivió.
//
// Un .part sin .meta no se puede reanudar —no se sabe adónde iba— y se
// informa como basura para que ADR-0029 lo expire; NO se borra aquí.
func (a *Almacen) Reanudables(ctx context.Context) ([]almacen.Parcial, error) {
	d, err := a.raiz.Open(subParciales)
	if err != nil {
		return nil, traducir(err)
	}
	defer d.Close()

	entradas, err := d.ReadDir(-1)
	if err != nil {
		return nil, traducir(err)
	}

	var out []almacen.Parcial
	for _, de := range entradas {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		if !strings.HasSuffix(de.Name(), sufijoMeta) {
			continue
		}
		id := strings.TrimSuffix(de.Name(), sufijoMeta)
		m, err := a.leerMeta(id)
		if err != nil {
			continue // .meta ilegible: se ignora, lo barrerá la expiración
		}
		ruta, err := almacen.NuevaRuta(m.Ruta)
		if err != nil {
			continue
		}
		fi, err := a.raiz.Stat(a.rutaParte(id))
		if err != nil {
			continue // .meta huérfano sin datos
		}
		out = append(out, almacen.Parcial{
			ID: id, Ruta: ruta, Total: m.Total,
			Escrito: fi.Size(), Modificado: fi.ModTime(),
		})
	}
	return out, nil
}

func (a *Almacen) escribirMeta(id string, m metaEnDisco) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	f, err := a.raiz.OpenFile(a.rutaMeta(id), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return traducir(err)
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		a.raiz.Remove(a.rutaMeta(id))
		return err
	}
	// fsync: si el destino no sobrevive a un corte, el parcial es irrecuperable
	// y habríamos cambiado un problema por otro peor (ADR-0024).
	if err := f.Sync(); err != nil {
		f.Close()
		a.raiz.Remove(a.rutaMeta(id))
		return fmt.Errorf("fsync del meta: %w", err)
	}
	if err := f.Close(); err != nil {
		return err
	}
	return a.sincronizarDirectorio(subParciales)
}

func (a *Almacen) leerMeta(id string) (metaEnDisco, error) {
	f, err := a.raiz.Open(a.rutaMeta(id))
	if err != nil {
		return metaEnDisco{}, traducir(err)
	}
	defer f.Close()
	var m metaEnDisco
	if err := json.NewDecoder(f).Decode(&m); err != nil {
		return metaEnDisco{}, fmt.Errorf("meta ilegible de %q: %w", id, err)
	}
	return m, nil
}

// idValido acota el identificador a lo que produce identificador(): 32
// caracteres hexadecimales. Impide que un cliente use el identificador para
// salirse de estado/parciales/ (04_SEGURIDAD.md §2).
func idValido(id string) bool {
	if len(id) != 32 {
		return false
	}
	for _, c := range id {
		if !(c >= '0' && c <= '9') && !(c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// BorrarParcial elimina un parcial y su metadato. Lo usa la expiración de
// ADR-0029, y SOLO ella: nunca se borra un parcial sin dejar constancia.
func (a *Almacen) BorrarParcial(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !idValido(id) {
		return almacen.ErrRutaInvalida
	}
	err1 := a.raiz.Remove(a.rutaParte(id))
	err2 := a.raiz.Remove(a.rutaMeta(id))
	if err1 != nil && !errors.Is(err1, fs.ErrNotExist) {
		return traducir(err1)
	}
	if err2 != nil && !errors.Is(err2, fs.ErrNotExist) {
		return traducir(err2)
	}
	return nil
}
