// Package fsposix es el adaptador secundario: traduce el puerto almacen.Almacen
// a llamadas al sistema sobre ext4 (ADR-0007, ADR-0014).
//
// No interpreta reglas de producto. Solo semántica de sistema de archivos.
package fsposix

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"iter"
	"os"
	"path"

	"nasd/internal/almacen"
)

// Disposición del volumen — ADR-0019. La raíz de os.Root es el punto de
// montaje completo, NO datos/, porque rename() solo es atómico dentro del
// mismo árbol de os.Root y el temporal vive en estado/parciales/.
//
// La contención a datos/ la da almacen.RutaSegura (capa 1); os.Root cierra
// el volumen entero e incluye los enlaces simbólicos (capa 2).
// 04_SEGURIDAD.md §2.
const (
	subDatos     = "datos"
	subParciales = "estado/parciales"
)

// Almacen implementa almacen.Almacen sobre un volumen POSIX.
type Almacen struct {
	raiz *os.Root // anclado en el punto de montaje, p. ej. /srv/nas
	// residuo acumula identificadores cuyo .meta no se pudo retirar. No es
	// crítico —el archivo ya se publicó— pero no se calla (P5).
	residuo []string
}

// Residuo devuelve los identificadores cuyo metadato quedó sin retirar.
func (a *Almacen) Residuo() []string { return a.residuo }

var _ almacen.Almacen = (*Almacen)(nil)

// Abrir ancla el almacén en el punto de montaje del volumen.
// Falla ruidosamente si la disposición de ADR-0019 no existe: sin volumen no
// hay nada que servir, y es mejor no arrancar que aceptar peticiones que
// fracasarán después (P5).
func AbrirVolumen(puntoDeMontaje string) (*Almacen, error) {
	raiz, err := os.OpenRoot(puntoDeMontaje)
	if err != nil {
		return nil, fmt.Errorf("anclar el volumen en %q: %w", puntoDeMontaje, err)
	}
	a := &Almacen{raiz: raiz}
	for _, sub := range []string{subDatos, subParciales} {
		if err := raiz.MkdirAll(sub, 0o700); err != nil {
			raiz.Close()
			return nil, fmt.Errorf("preparar %q: %w", sub, err)
		}
	}
	return a, nil
}

func (a *Almacen) Close() error { return a.raiz.Close() }

// real traduce una RutaSegura a la ruta relativa dentro de os.Root.
func real(r almacen.RutaSegura) string {
	if r.EsRaiz() {
		return subDatos
	}
	return path.Join(subDatos, r.Rel())
}

func (a *Almacen) Estado(ctx context.Context, r almacen.RutaSegura) (almacen.Entrada, error) {
	if err := ctx.Err(); err != nil {
		return almacen.Entrada{}, err
	}
	fi, err := a.raiz.Stat(real(r))
	if err != nil {
		return almacen.Entrada{}, a.traducirEn(real(r), err)
	}
	return aEntrada(r, fi), nil
}

// Listar recorre un directorio emitiendo entradas conforme las lee.
//
// Nunca acumula el directorio entero: D-11 dejó la estructura libre, así que
// el número de entradas lo controla el usuario y no tiene cota (RNF-04).
func (a *Almacen) Listar(ctx context.Context, r almacen.RutaSegura) iter.Seq2[almacen.Entrada, error] {
	return func(yield func(almacen.Entrada, error) bool) {
		d, err := a.raiz.Open(real(r))
		if err != nil {
			yield(almacen.Entrada{}, a.traducirEn(real(r), err))
			return
		}
		defer d.Close()

		if fi, err := d.Stat(); err != nil {
			yield(almacen.Entrada{}, traducir(err))
			return
		} else if !fi.IsDir() {
			yield(almacen.Entrada{}, almacen.ErrNoEsDirectorio)
			return
		}

		for {
			if err := ctx.Err(); err != nil {
				yield(almacen.Entrada{}, err)
				return
			}
			// En bloques: ni una llamada por entrada ni el directorio entero.
			lote, err := d.ReadDir(128)
			for _, de := range lote {
				hija, err := r.Hija(de.Name())
				if err != nil {
					// Un nombre que el kernel acepta pero el dominio no.
					// Se omite en lugar de romper el listado entero.
					continue
				}
				fi, err := de.Info()
				if err != nil {
					if !yield(almacen.Entrada{}, traducir(err)) {
						return
					}
					continue
				}
				if !yield(aEntrada(hija, fi), nil) {
					return
				}
			}
			if err == io.EOF {
				return
			}
			if err != nil {
				yield(almacen.Entrada{}, traducir(err))
				return
			}
		}
	}
}

func (a *Almacen) Abrir(ctx context.Context, r almacen.RutaSegura) (io.ReadSeekCloser, almacen.Entrada, error) {
	if err := ctx.Err(); err != nil {
		return nil, almacen.Entrada{}, err
	}
	f, err := a.raiz.Open(real(r))
	if err != nil {
		return nil, almacen.Entrada{}, a.traducirEn(real(r), err)
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, almacen.Entrada{}, traducir(err)
	}
	if fi.IsDir() {
		f.Close()
		return nil, almacen.Entrada{}, almacen.ErrEsDirectorio
	}
	return f, aEntrada(r, fi), nil
}

func (a *Almacen) CrearDirectorio(ctx context.Context, r almacen.RutaSegura) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := a.raiz.Mkdir(real(r), 0o700); err != nil {
		return traducir(err)
	}
	// El directorio nuevo también debe sobrevivir a un corte: se sincroniza
	// el padre, que es quien contiene la entrada recién creada.
	return a.sincronizarDirectorio(path.Dir(real(r)))
}

// Crear abre una escritura atómica hacia r.
//
// RF-23: si el destino ya existe, NO se sobrescribe. Devuelve ErrYaExiste y
// deja la decisión al usuario. Con copia única (D-12) sobrescribir en
// silencio destruiría la única copia de algo, y además es el control más
// eficaz contra la concurrencia SMB/web (ADR-0028, capa 1).
func (a *Almacen) Crear(ctx context.Context, r almacen.RutaSegura) (almacen.EscrituraAtomica, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if r.EsRaiz() {
		return nil, almacen.ErrRutaInvalida
	}
	destino := real(r)
	if _, err := a.raiz.Stat(destino); err == nil {
		return nil, almacen.ErrYaExiste
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, traducir(err)
	}
	// El directorio contenedor tiene que existir ya.
	if fi, err := a.raiz.Stat(path.Dir(destino)); err != nil {
		return nil, traducir(err)
	} else if !fi.IsDir() {
		return nil, almacen.ErrNoEsDirectorio
	}

	id, err := identificador()
	if err != nil {
		return nil, err
	}
	return a.abrirParcial(id, destino)
}

// escritura implementa almacen.EscrituraAtomica siguiendo ADR-0024.
type escritura struct {
	a        *Almacen
	f        *os.File
	id       string // vacío si la escritura no es reanudable
	temporal string
	destino  string
	escrito  int64
	cerrada  bool
}

func (a *Almacen) abrirParcial(id, destino string) (*escritura, error) {
	temporal := path.Join(subParciales, id+".part")
	f, err := a.raiz.OpenFile(temporal, os.O_CREATE|os.O_EXCL|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, traducir(err)
	}
	return &escritura{a: a, f: f, temporal: temporal, destino: destino}, nil
}

func (e *escritura) Write(p []byte) (int, error) {
	n, err := e.f.Write(p)
	e.escrito += int64(n)
	return n, err
}

func (e *escritura) Escrito() int64 { return e.escrito }

// Confirmar ejecuta la secuencia completa de ADR-0024.
//
// El paso 4 —fsync del DIRECTORIO destino— es el que se olvida. Sin él, tras
// un corte de corriente el archivo puede estar escrito y la entrada de
// directorio no, con lo que el archivo simplemente no aparece. Eso degrada al
// lado seguro (RNF-05 se cumpliría igual), pero si vamos a responder 200 hay
// que haber cumplido la promesa.
func (e *escritura) Confirmar() error {
	if e.cerrada {
		return errors.New("escritura ya cerrada")
	}
	// 1. fsync del archivo: los datos llegan al disco.
	if err := e.f.Sync(); err != nil {
		e.Descartar()
		return fmt.Errorf("fsync del temporal: %w", err)
	}
	if err := e.f.Close(); err != nil {
		e.cerrada = true
		return fmt.Errorf("cerrar el temporal: %w", err)
	}
	e.cerrada = true

	// 2. Última comprobación de RF-23: el destino pudo aparecer por SMB
	//    mientras subíamos. No se sobrescribe.
	if _, err := e.a.raiz.Stat(e.destino); err == nil {
		e.a.raiz.Remove(e.temporal)
		return almacen.ErrYaExiste
	} else if !errors.Is(err, fs.ErrNotExist) {
		return traducir(err)
	}

	// 3. rename: publicación atómica.
	if err := e.a.raiz.Rename(e.temporal, e.destino); err != nil {
		return fmt.Errorf("publicar: %w", traducir(err))
	}

	// 4. fsync del directorio destino: la ENTRADA llega al disco.
	if err := e.a.sincronizarDirectorio(path.Dir(e.destino)); err != nil {
		return err
	}

	// 5. El .meta ya no describe nada: se retira. Dejarlo haría que el
	//    servicio creyera al arrancar que hay una subida a medias.
	e.retirarMeta()
	return nil
}

// retirarMeta elimina el metadato de reanudación, si lo había.
func (e *escritura) retirarMeta() {
	if e.id == "" {
		return
	}
	if err := e.a.raiz.Remove(e.a.rutaMeta(e.id)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		// No es motivo para fallar la publicación —el archivo ya está—, pero
		// tampoco se calla: quedaría un .meta huérfano.
		e.a.residuo = append(e.a.residuo, e.id)
	}
}

func (e *escritura) Descartar() error {
	if !e.cerrada {
		e.f.Close()
		e.cerrada = true
	}
	e.retirarMeta()
	err := e.a.raiz.Remove(e.temporal)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// Soltar cierra el descriptor conservando el parcial y su .meta.
func (e *escritura) Soltar() error {
	if e.cerrada {
		return nil
	}
	e.cerrada = true
	return e.f.Close()
}

// sincronizarDirectorio fuerza la escritura de la entrada de directorio.
// La implementación depende del sistema: ver sincronizar_unix.go.
func (a *Almacen) sincronizarDirectorio(dir string) error {
	return sincronizarDirectorio(a.raiz, dir)
}

func identificador() (string, error) {
	// crypto/rand, nunca datos del cliente: además de evitar colisiones,
	// quita de raíz un vector de salto de ruta en estado/parciales/.
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generar identificador: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

func aEntrada(r almacen.RutaSegura, fi fs.FileInfo) almacen.Entrada {
	return almacen.Entrada{
		Ruta:        r,
		Nombre:      r.Nombre(),
		EsDirectori: fi.IsDir(),
		Tamano:      fi.Size(),
		Modificado:  fi.ModTime(),
	}
}

// traducir convierte errores del sistema en errores del dominio.
// El núcleo no debe conocer fs.ErrNotExist ni ningún errno.
func traducir(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, fs.ErrNotExist):
		return almacen.ErrNoExiste
	case errors.Is(err, fs.ErrExist):
		return almacen.ErrYaExiste
	default:
		return err
	}
}

// traducirEn hace lo mismo que traducir, pero distinguiendo el caso del
// enlace simbólico que apunta fuera del volumen.
//
// os.Root rechaza esos enlaces con un error NO EXPORTADO ("path escapes from
// parent"), así que no se puede comparar con errors.Is. En lugar de acoplarse
// a un texto de la biblioteca estándar —que puede cambiar sin aviso— se
// pregunta al sistema de archivos: si la entrada existe y ES un enlace, el
// fallo de apertura solo puede ser porque escapa o porque está roto. En ambos
// casos la respuesta correcta hacia el cliente es la misma.
func (a *Almacen) traducirEn(rutaReal string, err error) error {
	if err == nil {
		return nil
	}
	if fi, e := a.raiz.Lstat(rutaReal); e == nil && fi.Mode()&fs.ModeSymlink != 0 {
		return almacen.ErrEnlaceExterno
	}
	return traducir(err)
}
