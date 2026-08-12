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
	"strings"

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

	// SubHomeUsers es la carpeta, DENTRO de datos/, bajo la que cuelga la
	// raíz de cada usuario (ADR-0055).
	//
	// Va dentro de datos/ a propósito: así el superusuario la ve igual por la
	// web y por SMB, que es lo que se decidió —él ve todo—. Y su nombre queda
	// RESERVADO: nadie puede crear, renombrar ni mover nada hacia él en la
	// raíz de datos, porque una carpeta suya llamada igual dejaría a los
	// usuarios compartiendo espacio con sus archivos.
	SubHomeUsers = "homeUsers"
)

// esReservado decide si una ruta es intocable por la vía normal: SOLO el
// contenedor de usuarios en sí (ADR-0058, supersede en esto a ADR-0055).
//
// LA RAÍZ DE UN USUARIO YA NO ESTÁ AQUÍ. Hasta el 2026-08-12 esReservado
// también bloqueaba «homeUsers/<quien>», y ese bloqueo era indistinguible
// —para quien lo sufría— de un fallo real: la capa web lo traducía en 500
// «error interno» (P-9), cuando era una negativa deliberada. El responsable
// decidió que el superusuario puede administrar la carpeta de un usuario
// igual que ya podía por SMB, que nunca conoció esta regla.
//
// EL CONTENEDOR SÍ SE QUEDA, y no por restringir al superusuario sino por
// protegerlo A ÉL: ParaUsuario hace MkdirAll(homeUsers/<nombre>) en cada
// entrada de sesión. Si «homeUsers» se borra o se sustituye por un archivo,
// ESE MkdirAll falla y nadie puede volver a entrar — no es «no te dejo», es
// «esto rompe el arranque de sesión de todos».
//
// SOLO APLICA AL ALMACÉN SIN PREFIJO, es decir, al del superusuario. Un
// usuario nunca puede nombrar esas rutas —su prefijo lo mete dentro de su
// propia carpeta—, así que comprobarlo para él sería prohibirle una carpeta
// suya que se llamara igual.
//
// Se compara por COMPONENTES y no con strings.HasPrefix: una carpeta llamada
// «homeUsersXYZ» empieza igual y es un nombre perfectamente legítimo del
// superusuario. Confundirlas la dejaría bloqueada sin motivo.
func (a *Almacen) esReservado(r almacen.RutaSegura) bool {
	if a.prefijo != "" || r.EsRaiz() {
		return false
	}
	partes := strings.Split(r.Rel(), "/")
	// SOLO «homeUsers» exacto, el contenedor. La raíz de un usuario
	// («homeUsers/<quien>») y todo lo de más adentro se administra ya
	// desde la vía normal.
	return partes[0] == SubHomeUsers && len(partes) == 1
}

// componenteSeguro comprueba que un nombre puede usarse como UN componente de
// ruta, y solo uno. Lista positiva, por el mismo motivo que en el registro:
// con una lista de prohibidos siempre falta uno, y el que falte se paga aquí.
func componenteSeguro(nombre string) error {
	if nombre == "" || len(nombre) > 32 {
		return fmt.Errorf("%w: longitud fuera de rango", almacen.ErrRutaInvalida)
	}
	for _, r := range nombre {
		esValido := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_'
		if !esValido {
			return fmt.Errorf("%w: %q no vale como nombre de carpeta de usuario",
				almacen.ErrRutaInvalida, nombre)
		}
	}
	return nil
}

// Almacen implementa almacen.Almacen sobre un volumen POSIX.
type Almacen struct {
	raiz *os.Root // anclado en el punto de montaje, p. ej. /srv/nas
	// prefijo acota TODO lo que ve este almacén a un subárbol de datos/.
	// Vacío para el superusuario; «homeUsers/juan» para un usuario (ADR-0055).
	//
	// AQUÍ VIVE EL AISLAMIENTO ENTERO, y por eso está en un solo campo que
	// solo lee una función —real()—: una propiedad de seguridad repartida por
	// veinte sitios no se puede auditar, y esta se lee en una pantalla.
	prefijo string
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
//
// ES EL ÚNICO SITIO DONDE UNA RUTA DEL DOMINIO SE CONVIERTE EN UNA RUTA DEL
// DISCO, y por tanto el único punto donde se aplica el aislamiento por
// usuario. Si alguna vez hace falta otra traducción, se añade AQUÍ; una
// segunda función que haga esto mismo sería una segunda oportunidad de
// olvidarse del prefijo.
//
// POR QUÉ ESTO CONTIENE, y no hace falta creerlo por fe: almacen.RutaSegura
// garantiza que Rel() es relativa, sin «..» y sin barra inicial (04_SEGURIDAD
// §2, capa 1). Sobre esa garantía, path.Join no puede producir nada fuera de
// «datos/<prefijo>». Y por debajo sigue os.Root, anclado en el punto de
// montaje, que cierra el volumen entero incluidos los enlaces simbólicos que
// apunten fuera (capa 2).
//
// LO QUE ESTO NO CIERRA, dicho en voz alta: un enlace simbólico creado DENTRO
// de datos/ que apunte a otro punto de datos/ sigue siendo válido para
// os.Root, porque no escapa del volumen. Crearlo exige SMB o SSH, es decir,
// ser el superusuario — que ya lo ve todo—; el riesgo real es que se lo
// ponga a un usuario sin querer. Ver ADR-0055 y 04_SEGURIDAD.md §2.bis.
func (a *Almacen) real(r almacen.RutaSegura) string {
	if r.EsRaiz() {
		return path.Join(subDatos, a.prefijo)
	}
	return path.Join(subDatos, a.prefijo, r.Rel())
}

// ParaUsuario devuelve una vista del MISMO volumen acotada a la carpeta de un
// usuario, creándola si no existe.
//
// Comparte el os.Root a propósito, en vez de abrir uno nuevo anclado en la
// carpeta del usuario. El motivo es RNF-08: publicar una subida es un
// rename() desde estado/parciales/ hasta datos/…, y rename() solo es atómico
// dentro del mismo árbol. Un os.Root por usuario haría ese rename imposible y
// habría que sustituirlo por copiar y borrar, que NO es atómico — se cambiaría
// una garantía de durabilidad medida por una de aislamiento que RutaSegura ya
// da. No compensa.
func (a *Almacen) ParaUsuario(nombre string) (*Almacen, error) {
	// SE VUELVE A VALIDAR AQUÍ AUNQUE EL REGISTRO YA LO HAGA, y no es
	// redundancia por descuido: son dos invariantes distintas comprobadas en
	// dos capas. autenticacion.NombreValido decide qué es un nombre de
	// CUENTA aceptable; esto decide qué es un COMPONENTE DE RUTA seguro, que
	// es lo que le importa al sistema de archivos. Si alguien relaja una, la
	// otra sigue en pie — que es justo lo que se pide de un límite de
	// seguridad. Además evita que este adaptador dependa del de autenticación.
	if err := componenteSeguro(nombre); err != nil {
		return nil, err
	}
	prefijo := path.Join(SubHomeUsers, nombre)
	if err := a.raiz.MkdirAll(path.Join(subDatos, prefijo), 0o700); err != nil {
		return nil, fmt.Errorf("preparar la carpeta de %q: %w", nombre, err)
	}
	// Copia superficial: mismo os.Root, distinto prefijo. El residuo queda
	// por instancia, y no se comparte porque nada lo consulta en producción.
	return &Almacen{raiz: a.raiz, prefijo: prefijo}, nil
}

// PromoverCarpetaDeUsuario saca la carpeta de un usuario de homeUsers/ y la
// deja como una carpeta más de la raíz del superusuario, con el mismo nombre
// — corrección del 06/08 sobre P-4/etapa 2: dar de baja dejaba la cuenta sin
// acceso pero su carpeta seguía viviendo DENTRO de homeUsers/, un sitio que
// el propio panel deja de mostrar (esReservado no se toca: sigue protegiendo
// la vía normal). El responsable la quiere a la vista, junto a lo demás.
//
// NO PASA POR Renombrar/Mover: esReservado los bloquearía a propósito, y con
// razón — para la vía normal, homeUsers/<quien> es intocable. Esta función
// ES la excepción, y solo la dispara una baja.
//
// Si la persona nunca entró, ParaUsuario nunca creó su carpeta —se crea al
// primer acceso, no al alta— y no hay nada que mover: no es un error.
//
// Si YA existe algo en la raíz con ese nombre, se rehúsa en vez de fusionar
// o sobrescribir (RF-23, D-12): quien llame decide qué hacer, y lo correcto
// es abortar la baja entera antes que arriesgar un archivo ajeno.
func (a *Almacen) PromoverCarpetaDeUsuario(nombre string) error {
	if err := componenteSeguro(nombre); err != nil {
		return err
	}
	origen := path.Join(subDatos, SubHomeUsers, nombre)
	destino := path.Join(subDatos, nombre)

	if _, err := a.raiz.Stat(origen); errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return traducir(err)
	}
	if _, err := a.raiz.Stat(destino); err == nil {
		return fmt.Errorf("%w: ya existe %q en la raíz, junto a homeUsers", almacen.ErrYaExiste, nombre)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return traducir(err)
	}

	if err := a.raiz.Rename(origen, destino); err != nil {
		return fmt.Errorf("promover la carpeta de %q: %w", nombre, traducir(err))
	}
	// Los dos padres cambiaron de contenido: el de origen perdió una entrada,
	// el de destino ganó una. Los dos se sincronizan, mismo criterio que
	// ADR-0024.
	if err := a.sincronizarDirectorio(path.Dir(origen)); err != nil {
		return err
	}
	return a.sincronizarDirectorio(path.Dir(destino))
}

func (a *Almacen) Estado(ctx context.Context, r almacen.RutaSegura) (almacen.Entrada, error) {
	if err := ctx.Err(); err != nil {
		return almacen.Entrada{}, err
	}
	fi, err := a.raiz.Stat(a.real(r))
	if err != nil {
		return almacen.Entrada{}, a.traducirEn(a.real(r), err)
	}
	return aEntrada(r, fi), nil
}

// Listar recorre un directorio emitiendo entradas conforme las lee.
//
// Nunca acumula el directorio entero: D-11 dejó la estructura libre, así que
// el número de entradas lo controla el usuario y no tiene cota (RNF-04).
func (a *Almacen) Listar(ctx context.Context, r almacen.RutaSegura) iter.Seq2[almacen.Entrada, error] {
	return func(yield func(almacen.Entrada, error) bool) {
		d, err := a.raiz.Open(a.real(r))
		if err != nil {
			yield(almacen.Entrada{}, a.traducirEn(a.real(r), err))
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
	f, err := a.raiz.Open(a.real(r))
	if err != nil {
		return nil, almacen.Entrada{}, a.traducirEn(a.real(r), err)
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
	// ADR-0058: solo el CONTENEDOR de usuarios se protege aquí, para no
	// romper el MkdirAll de ParaUsuario. La regla vive en el servidor, no
	// en la interfaz: esconder un botón no impide la petición.
	if a.esReservado(r) {
		return almacen.ErrReservado
	}
	if err := a.raiz.Mkdir(a.real(r), 0o700); err != nil {
		return traducir(err)
	}
	// El directorio nuevo también debe sobrevivir a un corte: se sincroniza
	// el padre, que es quien contiene la entrada recién creada.
	return a.sincronizarDirectorio(path.Dir(a.real(r)))
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
	destino := a.real(r)
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
