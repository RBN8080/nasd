package autenticacion

import (
	"bufio"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Registro de usuarios — ADR-0055.
//
// # POR QUÉ EL SUPERUSUARIO NO ESTÁ AQUÍ
//
// Su credencial sigue donde estaba: un archivo aparte que systemd entrega por
// LoadCredential= y que el servicio NO puede escribir. Esa asimetría es
// deliberada. El registro de abajo SÍ es escribible —el panel da de alta y de
// baja—, y un archivo escribible por el servicio es un archivo que un defecto
// del servicio puede corromper. La llave del dueño no se pone en un sitio que
// el propio programa pueda estropear.
//
// Consecuencia práctica: aunque este registro se pierda entero, el
// superusuario sigue entrando y puede rehacerlo.
//
// # DÓNDE VIVE
//
// En /var/lib/nasd/, que es lo que systemd llama StateDirectory: estado
// persistente y escribible de un servicio (systemd.exec(5)). NO va en el disco
// de datos, por dos motivos: allí lo vería SMB, y ese disco está pensado para
// sobrevivir a la placa y viajar — que es justo lo que no se quiere de unas
// credenciales.

// ErrNombreInvalido lo devuelve NombreValido. Se distingue de los demás
// porque el panel tiene que poder explicárselo a quien da de alta.
var ErrNombreInvalido = errors.New("nombre de usuario inválido")

// ErrYaExiste — dos usuarios con el mismo nombre serían dos personas
// apuntando a la misma carpeta.
var ErrYaExiste = errors.New("ya existe un usuario con ese nombre")

// ErrNoExiste — baja o consulta de alguien que no está.
var ErrNoExiste = errors.New("no existe ese usuario")

// NombreSuperusuario es el nombre con el que el responsable se identifica en
// el formulario de acceso.
//
// Hacía falta uno en cuanto el acceso dejó de ser «una contraseña y ya»: si se
// pide un nombre, él también tiene que teclear alguno. No está en el registro
// —su credencial vive aparte, ver arriba— y por eso mismo el registro tiene
// que RECHAZARLO: una cuenta llamada igual no podría entrar nunca, porque el
// acceso resolvería ese nombre contra la credencial del superusuario antes de
// mirar el registro. Sería una cuenta fantasma, con carpeta creada y sin dueño
// posible.
const NombreSuperusuario = "admin"

// maxUsuarios acota el registro. No es una regla de producto: es que el
// acceso compara la contraseña contra CADA usuario cuando no sabe cuál es, y
// cada comparación cuesta ~3.6 s en el nodo (medido el 2026-08-06). Sin tope,
// el propio registro se convierte en el amplificador.
const maxUsuarios = 32

// Usuario es una cuenta del registro. La contraseña NUNCA se guarda: solo su
// línea derivada, con el mismo formato que la del superusuario.
type Usuario struct {
	Nombre     string
	Credencial string
}

// LongitudMinimaUsuario es el mínimo para una cuenta de usuario.
//
// Es MÁS estricto que LongitudMinima (12), y a propósito: aquel número se
// razonó cuando la web iba sin TLS y sin Internet entrante —lo dice su propio
// comentario—, y hoy ninguna de las dos cosas es cierta. Una cuenta nueva es
// alcanzable desde Internet desde el primer minuto, así que se le exige lo
// mismo que a la del superusuario (ADR-0047).
const LongitudMinimaUsuario = 14

// Registro es el conjunto de cuentas, cargado en memoria y respaldado por un
// archivo de texto. Se mantiene en un slice y no en un mapa para que el orden
// del archivo sea estable entre escrituras: un archivo que se reordena solo
// es un archivo que no se puede revisar de un vistazo.
type Registro struct {
	ruta     string
	usuarios []Usuario
	// iteraciones es el coste con el que se derivan las cuentas nuevas Y con
	// el que se gasta el tiempo cuando el usuario no existe: si el relleno
	// costara distinto que una cuenta real, la diferencia sería medible.
	iteraciones int
	// relleno es una línea SINTÁCTICAMENTE válida cuya contraseña no conoce
	// nadie. Se construye con bytes aleatorios y sin derivar nada: el coste
	// no está en fabricarla, está en que Verificar la procese entera.
	relleno string
}

// NombreValido decide qué se acepta como nombre de usuario.
//
// ESTA FUNCIÓN ES SEGURIDAD, no cortesía: el nombre se convierte en el nombre
// de una carpeta dentro del volumen. Por eso es una lista POSITIVA y estrecha
// en lugar de una lista de caracteres prohibidos — con una lista negativa
// siempre falta uno, y aquí el que falte se paga en el sistema de archivos.
//
// Solo minúsculas, dígitos, guion y guion bajo; empieza por letra; de 2 a 32.
// Eso descarta de una vez «.», «..», «/», «\», los espacios, los caracteres
// de control y cualquier truco de Unicode que se parezca a un separador.
//
// Y SOLO MINÚSCULAS por un motivo medido, no estético: ext4 distingue
// mayúsculas, así que «Juan» y «juan» serían dos carpetas distintas y dos
// cuentas que se confunden al teclearlas por SSH o por SMB.
func NombreValido(nombre string) error {
	if nombre == NombreSuperusuario {
		return fmt.Errorf("%w: %q está reservado para el superusuario",
			ErrNombreInvalido, NombreSuperusuario)
	}
	if len(nombre) < 2 || len(nombre) > 32 {
		return fmt.Errorf("%w: debe tener entre 2 y 32 caracteres", ErrNombreInvalido)
	}
	for i, r := range nombre {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9', r == '-', r == '_':
			if i == 0 {
				return fmt.Errorf("%w: debe empezar por una letra minúscula", ErrNombreInvalido)
			}
		default:
			return fmt.Errorf("%w: solo minúsculas, dígitos, «-» y «_»", ErrNombreInvalido)
		}
	}
	return nil
}

// CargarRegistro lee el archivo. Que NO exista no es un error: un nodo recién
// instalado no tiene usuarios todavía, y fallar ahí impediría arrancar por no
// haber creado a nadie.
func CargarRegistro(ruta string, iteraciones int) (*Registro, error) {
	relleno, err := lineaDeRelleno(iteraciones)
	if err != nil {
		return nil, err
	}
	r := &Registro{ruta: ruta, iteraciones: iteraciones, relleno: relleno}
	f, err := os.Open(ruta)
	if errors.Is(err, os.ErrNotExist) {
		return r, nil
	}
	if err != nil {
		return nil, fmt.Errorf("abrir el registro de usuarios %q: %w", ruta, err)
	}
	defer f.Close()

	s := bufio.NewScanner(f)
	for n := 1; s.Scan(); n++ {
		linea := strings.TrimSpace(s.Text())
		if linea == "" || strings.HasPrefix(linea, "#") {
			continue
		}
		nombre, credencial, ok := strings.Cut(linea, ":")
		if !ok {
			return nil, fmt.Errorf("registro de usuarios, línea %d: falta el separador «:»", n)
		}
		// Se valida AL LEER y no solo al escribir: un archivo editado a mano
		// —que es como se arregla esto cuando algo va mal— no puede colar un
		// nombre que luego se use como carpeta.
		if err := NombreValido(nombre); err != nil {
			return nil, fmt.Errorf("registro de usuarios, línea %d: %w", n, err)
		}
		if err := Valida(credencial); err != nil {
			return nil, fmt.Errorf("registro de usuarios, línea %d, usuario %q: %w", n, nombre, err)
		}
		if _, ya := r.Buscar(nombre); ya {
			return nil, fmt.Errorf("registro de usuarios, línea %d: %w: %q", n, ErrYaExiste, nombre)
		}
		r.usuarios = append(r.usuarios, Usuario{Nombre: nombre, Credencial: credencial})
	}
	if err := s.Err(); err != nil {
		return nil, fmt.Errorf("leer el registro de usuarios: %w", err)
	}
	return r, nil
}

// Lista devuelve las cuentas en el orden del archivo. Copia, para que quien
// la reciba no pueda alterar el registro por descuido.
func (r *Registro) Lista() []Usuario {
	out := make([]Usuario, len(r.usuarios))
	copy(out, r.usuarios)
	return out
}

// Cuantos devuelve el número de cuentas.
func (r *Registro) Cuantos() int { return len(r.usuarios) }

// Buscar localiza una cuenta por nombre.
func (r *Registro) Buscar(nombre string) (Usuario, bool) {
	for _, u := range r.usuarios {
		if u.Nombre == nombre {
			return u, true
		}
	}
	return Usuario{}, false
}

// Verifica comprueba la contraseña de un usuario.
//
// CUANDO EL USUARIO NO EXISTE SE VERIFICA IGUALMENTE contra una credencial de
// relleno. Sin eso, un nombre inexistente respondería al instante y uno real
// tardaría los ~3.6 s de la derivación: esa diferencia es un enumerador de
// nombres de usuario, y bastaría un cronómetro para usarlo (CWE-208).
func (r *Registro) Verifica(nombre, clave string) bool {
	u, existe := r.Buscar(nombre)
	if !existe {
		Verificar(r.relleno, clave)
		return false
	}
	return Verificar(u.Credencial, clave)
}

// lineaDeRelleno fabrica una credencial SINTÁCTICAMENTE válida cuya
// contraseña no conoce ni puede conocer nadie.
//
// No se deriva nada aquí, y no hace falta: el coste que interesa igualar no
// está en fabricar la línea, está en que Verificar la procese entera —mismas
// iteraciones, misma longitud de clave— antes de fallar la comparación. Si en
// vez de esto se llamara a Derivar, arrancar el servicio costaría los ~3.6 s
// de una derivación real sin ganar nada.
//
// Sal y «esperada» son bytes aleatorios del mismo tamaño que los de verdad,
// para que ni el tamaño de la línea distinga a un usuario inexistente.
func lineaDeRelleno(iteraciones int) (string, error) {
	sal := make([]byte, tamanoSal)
	if _, err := rand.Read(sal); err != nil {
		return "", fmt.Errorf("generar la sal de relleno: %w", err)
	}
	dk := make([]byte, tamanoClave)
	if _, err := rand.Read(dk); err != nil {
		return "", fmt.Errorf("generar el relleno: %w", err)
	}
	return fmt.Sprintf("%s$%d$%s$%s", algoritmo, iteraciones,
		base64.RawStdEncoding.EncodeToString(sal),
		base64.RawStdEncoding.EncodeToString(dk)), nil
}

// Alta añade una cuenta y guarda el registro.
func (r *Registro) Alta(nombre, clave string) error {
	if err := NombreValido(nombre); err != nil {
		return err
	}
	if _, ya := r.Buscar(nombre); ya {
		return fmt.Errorf("%w: %q", ErrYaExiste, nombre)
	}
	if len(r.usuarios) >= maxUsuarios {
		return fmt.Errorf("el registro admite como máximo %d usuarios", maxUsuarios)
	}
	// Derivar ya exige LongitudMinima (12); aquí se pide más, y el porqué
	// está junto a la constante.
	if len([]rune(clave)) < LongitudMinimaUsuario {
		return fmt.Errorf("%w: mínimo %d caracteres para una cuenta de usuario",
			ErrClaveCorta, LongitudMinimaUsuario)
	}
	linea, err := Derivar(clave, r.iteraciones)
	if err != nil {
		return err
	}
	r.usuarios = append(r.usuarios, Usuario{Nombre: nombre, Credencial: linea})
	if err := r.guardar(); err != nil {
		// Se deshace en memoria: si no se pudo guardar, el registro que
		// quedará al reiniciar es el de antes, y el de ahora debe coincidir.
		r.usuarios = r.usuarios[:len(r.usuarios)-1]
		return err
	}
	return nil
}

// Baja retira una cuenta y guarda el registro.
//
// NO toca ningún archivo del usuario: qué pasa con su carpeta lo decide quien
// llame, y la decisión está tomada —se aparta, no se destruye— porque sin
// papelera (D-15) ni segunda copia (D-12) un borrado aquí no se deshace.
func (r *Registro) Baja(nombre string) error {
	for i, u := range r.usuarios {
		if u.Nombre != nombre {
			continue
		}
		previo := r.usuarios
		r.usuarios = append(append([]Usuario{}, r.usuarios[:i]...), r.usuarios[i+1:]...)
		if err := r.guardar(); err != nil {
			r.usuarios = previo
			return err
		}
		return nil
	}
	return fmt.Errorf("%w: %q", ErrNoExiste, nombre)
}

// guardar escribe el archivo entero de forma ATÓMICA.
//
// Misma secuencia que ADR-0024, y por el mismo motivo llevado a otro terreno:
// un corte de corriente a mitad de escritura dejaría un registro truncado, y
// un registro truncado es gente que no puede entrar. El nodo ya perdió la
// corriente una vez (05/08/2026), así que esto no es hipotético.
func (r *Registro) guardar() error {
	dir := filepath.Dir(r.ruta)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("preparar %q: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".usuarios-*")
	if err != nil {
		return fmt.Errorf("crear el temporal del registro: %w", err)
	}
	nombreTmp := tmp.Name()
	defer os.Remove(nombreTmp) // no-op si el rename salió bien

	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("permisos del registro: %w", err)
	}
	var b strings.Builder
	b.WriteString("# Registro de usuarios de nasd — ADR-0055.\n")
	b.WriteString("# Una cuenta por línea: nombre:credencial-derivada.\n")
	b.WriteString("# El superusuario NO está aquí; su credencial va aparte.\n")
	for _, u := range r.usuarios {
		b.WriteString(u.Nombre)
		b.WriteByte(':')
		b.WriteString(u.Credencial)
		b.WriteByte('\n')
	}
	if _, err := tmp.WriteString(b.String()); err != nil {
		tmp.Close()
		return fmt.Errorf("escribir el registro: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sincronizar el registro: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("cerrar el temporal del registro: %w", err)
	}
	// Antes de publicar, no después: si el rename ya ocurrió y el chown
	// fallara, el registro bueno quedaría instalado y sin dueño correcto.
	if err := heredarDuenoDe(dir, nombreTmp); err != nil {
		return err
	}
	if err := os.Rename(nombreTmp, r.ruta); err != nil {
		return fmt.Errorf("publicar el registro: %w", err)
	}
	// El paso que todo el mundo olvida: sin esto el rename puede no haber
	// llegado al disco aunque el archivo sí (ADR-0024).
	return sincronizarDir(dir)
}
