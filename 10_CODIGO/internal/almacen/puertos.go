package almacen

import (
	"context"
	"io"
	"iter"
)

// Almacen es el puerto de salida del núcleo (02_ARQUITECTURA.md §7.1).
//
// Tres garantías están codificadas en las firmas, no en la disciplina de
// quien escriba el código:
//
//  1. io.Reader / io.Writer en todo el camino de datos. Nunca []byte con
//     contenido de archivo: materializar uno exigiría CAMBIAR ESTA INTERFAZ,
//     y eso se ve en revisión. RNF-01, ADR-0025.
//  2. Listar devuelve un ITERADOR, no un slice: D-11 dejó la estructura de
//     carpetas libre, así que ningún directorio tiene tamaño acotado y su
//     tamaño lo controla el usuario. RNF-04, ADR-0011.
//  3. context en toda operación: si el cliente abandona, el trabajo se
//     cancela en lugar de seguir consumiendo bus. RES-02, ADR-0026.
type Almacen interface {
	Listar(ctx context.Context, r RutaSegura) iter.Seq2[Entrada, error]

	// Abrir devuelve un ReadSeekCloser: el Seek habilita RFC 7233 (RF-10)
	// sin leer el archivo, y es exactamente lo que http.ServeContent pide.
	Abrir(ctx context.Context, r RutaSegura) (io.ReadSeekCloser, Entrada, error)

	// Crear devuelve un escritor que solo publica al confirmar (RNF-08).
	// Devuelve ErrYaExiste si el destino está ocupado (RF-23).
	Crear(ctx context.Context, r RutaSegura) (EscrituraAtomica, error)

	CrearDirectorio(ctx context.Context, r RutaSegura) error

	// Renombrar cubre RF-16 y RF-17: renombrar y mover son la MISMA
	// operación —un rename()— y dentro del volumen es atómica.
	//
	// Devuelve ErrYaExiste si el destino está ocupado: RF-23 vale igual aquí
	// que al subir. Mover algo encima de otra cosa la destruiría, y con
	// copia única (D-12) no habría de dónde recuperarla.
	Renombrar(ctx context.Context, origen, destino RutaSegura) error

	// Borrar elimina un archivo, o un directorio SI ESTÁ VACÍO.
	// Sobre un directorio con contenido devuelve ErrNoVacio.
	Borrar(ctx context.Context, r RutaSegura) error

	// BorrarArbol elimina un directorio CON TODO SU CONTENIDO.
	//
	// Tiene nombre propio a propósito: es la operación más destructiva del
	// producto y no debe poder invocarse por descuido al escribir Borrar.
	// Sin papelera (D-15) y con copia única (D-12), esto no se deshace.
	BorrarArbol(ctx context.Context, r RutaSegura) error

	// Resumen cuenta lo que hay bajo una ruta: cuántas entradas y cuántos
	// bytes. Existe para que la confirmación de RF-18 pueda decir QUÉ se va
	// a destruir en lugar de un «¿seguro?» a ciegas.
	Resumen(ctx context.Context, r RutaSegura) (Conteo, error)
	// Estado() se RETIRÓ del puerto el 2026-07-31: no lo usaba ningún
	// adaptador. P8 — ante la duda, se omite. Sigue existiendo como método
	// del adaptador POSIX, donde lo usan las pruebas.

	// CrearReanudable abre una escritura cuyo DESTINO queda anotado en disco
	// junto al parcial, de modo que sobreviva al reinicio del servicio.
	//
	// Sin esto, el destino vive solo en memoria y un reinicio deja el parcial
	// huérfano: los bytes siguen ahí, pero nadie sabe adónde iban.
	CrearReanudable(ctx context.Context, r RutaSegura, total int64) (Parcial, EscrituraAtomica, error)

	// Reanudables devuelve las subidas incompletas que hay en disco. Se llama
	// al arrancar, para reconstruir lo que el proceso anterior sabía.
	Reanudables(ctx context.Context) ([]Parcial, error)

	// ReabrirParcial recupera una escritura por su identificador, con el
	// desplazamiento situado al final de lo ya escrito.
	ReabrirParcial(ctx context.Context, id string) (Parcial, EscrituraAtomica, error)

	// BorrarParcial elimina un parcial abandonado. Lo usa la expiración de
	// ADR-0029, que NUNCA borra en silencio: quien llame debe registrarlo.
	BorrarParcial(ctx context.Context, id string) error
}

// EscrituraAtomica: o el archivo existe completo, o no existe. RNF-05, RNF-08.
//
// La secuencia la fija ADR-0024: escribir en un temporal del MISMO sistema de
// archivos, fsync del archivo, rename, y fsync del directorio destino — este
// último es el paso que todo el mundo olvida.
type EscrituraAtomica interface {
	io.Writer

	// Confirmar publica el archivo de forma atómica y duradera.
	// Solo cuando devuelve nil se puede responder 200: el código de estado
	// es una promesa, y con copia única (D-12) es lo único que hay.
	Confirmar() error

	// Descartar cierra y elimina el temporal. Idempotente.
	Descartar() error

	// Soltar cierra el descriptor PERO CONSERVA el parcial y su metadato.
	//
	// Es lo que permite desalojar de memoria una subida abandonada sin
	// destruirla: se libera el descriptor —que es un recurso finito— y la
	// subida sigue siendo reanudable con ReabrirParcial.
	//
	// La diferencia con Descartar no es un matiz: confundirlas destruiría
	// datos que el usuario esperaba reanudar, que es justo lo que ADR-0029
	// advierte que no se haga.
	Soltar() error

	// Escrito devuelve los bytes ya confirmados en el temporal.
	// Es el Upload-Offset de ADR-0027: no hay metadato de progreso que
	// pueda desincronizarse porque el desplazamiento ES el tamaño.
	Escrito() int64
}
