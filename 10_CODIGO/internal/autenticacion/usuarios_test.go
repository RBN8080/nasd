package autenticacion

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Iteraciones bajas: aquí se prueba la LÓGICA del registro, no el coste.
const iteracionesDePrueba = 1000

const claveDePrueba = "una-clave-de-catorce-o-mas"

func registroVacio(t *testing.T) *Registro {
	t.Helper()
	r, err := CargarRegistro(filepath.Join(t.TempDir(), "usuarios"), iteracionesDePrueba)
	if err != nil {
		t.Fatalf("CargarRegistro: %v", err)
	}
	return r
}

// LA PRUEBA MÁS IMPORTANTE DE ESTE ARCHIVO. El nombre se convierte en el
// nombre de una carpeta dentro del volumen, así que todo lo que aquí se cuele
// se cuela en el sistema de archivos.
func TestElNombreDeUsuarioNoPuedeSerUnaRuta(t *testing.T) {
	prohibidos := []string{
		".", "..", "../otro", "a/b", `a\b`, "/juan", "juan/",
		"", "a", strings.Repeat("x", 33),
		"Juan",     // mayúsculas: ext4 las distingue y «Juan» ≠ «juan»
		"1juan",    // empieza por dígito
		"-juan",    // empieza por guion: parecería una opción de línea de órdenes
		"juan.",    // el punto abre la puerta a «.» y «..»
		"ju an",    // espacio
		"juan\x00", // nulo: trunca la ruta en las llamadas al sistema
		"juan\n",   // salto: partiría el archivo del registro en dos líneas
		"juan:x",   // dos puntos: es el separador del archivo
		"juán",     // no ASCII: dos formas Unicode distintas se ven igual
		"ｊｕａｎ",     // ancho completo: se lee «juan» y no lo es
	}
	for _, n := range prohibidos {
		if err := NombreValido(n); err == nil {
			t.Errorf("se aceptó el nombre %q, que acabaría siendo una carpeta", n)
		}
	}

	for _, n := range []string{"juan", "ana-maria", "usuario_1", "ab", "x9"} {
		if err := NombreValido(n); err != nil {
			t.Errorf("se rechazó el nombre válido %q: %v", n, err)
		}
	}
}

func TestAltaBajaYVerificacion(t *testing.T) {
	r := registroVacio(t)

	if err := r.Alta("juan", claveDePrueba); err != nil {
		t.Fatalf("Alta: %v", err)
	}
	if !r.Verifica("juan", claveDePrueba) {
		t.Error("no verifica la contraseña que se acaba de fijar")
	}
	if r.Verifica("juan", "otra-cosa-larga-igual") {
		t.Error("verificó una contraseña incorrecta")
	}
	if r.Verifica("nadie", claveDePrueba) {
		t.Error("verificó a un usuario que no existe")
	}

	if err := r.Alta("juan", claveDePrueba); err == nil {
		t.Error("aceptó dos cuentas con el mismo nombre: serían dos personas en una carpeta")
	}
	if err := r.Baja("juan"); err != nil {
		t.Fatalf("Baja: %v", err)
	}
	if _, hay := r.Buscar("juan"); hay {
		t.Error("sigue existiendo tras la baja")
	}
}

// La contraseña NUNCA se guarda; solo su derivación. Si esto falla, el
// registro se convierte en una lista de contraseñas en claro.
func TestElArchivoNoContieneLaContrasena(t *testing.T) {
	ruta := filepath.Join(t.TempDir(), "usuarios")
	r, err := CargarRegistro(ruta, iteracionesDePrueba)
	if err != nil {
		t.Fatalf("CargarRegistro: %v", err)
	}
	if err := r.Alta("juan", claveDePrueba); err != nil {
		t.Fatalf("Alta: %v", err)
	}

	b, err := os.ReadFile(ruta)
	if err != nil {
		t.Fatalf("leer el registro: %v", err)
	}
	if strings.Contains(string(b), claveDePrueba) {
		t.Fatal("LA CONTRASEÑA ESTÁ EN CLARO EN EL ARCHIVO")
	}
	if !strings.Contains(string(b), "juan:pbkdf2-sha256$") {
		t.Errorf("no se guardó la línea derivada:\n%s", b)
	}
}

// El registro tiene que sobrevivir a un reinicio: se guarda y se relee.
func TestElRegistroSobreviveAUnaRecarga(t *testing.T) {
	ruta := filepath.Join(t.TempDir(), "usuarios")
	uno, err := CargarRegistro(ruta, iteracionesDePrueba)
	if err != nil {
		t.Fatalf("CargarRegistro: %v", err)
	}
	if err := uno.Alta("ana", claveDePrueba); err != nil {
		t.Fatalf("Alta: %v", err)
	}

	dos, err := CargarRegistro(ruta, iteracionesDePrueba)
	if err != nil {
		t.Fatalf("recargar: %v", err)
	}
	if !dos.Verifica("ana", claveDePrueba) {
		t.Error("la cuenta no sobrevivió a la recarga")
	}
}

// Un archivo editado a mano es como se arregla esto cuando algo va mal, así
// que un nombre inválido metido ahí NO puede colarse hasta el sistema de
// archivos: se rechaza al cargar, ruidosamente (P5).
func TestUnRegistroConUnNombreInvalidoNoSeCarga(t *testing.T) {
	ruta := filepath.Join(t.TempDir(), "usuarios")
	linea, err := Derivar(claveDePrueba, iteracionesDePrueba)
	if err != nil {
		t.Fatalf("Derivar: %v", err)
	}
	if err := os.WriteFile(ruta, []byte("../fuera:"+linea+"\n"), 0o600); err != nil {
		t.Fatalf("escribir: %v", err)
	}

	if _, err := CargarRegistro(ruta, iteracionesDePrueba); err == nil {
		t.Fatal("cargó un registro con «../fuera» como nombre de usuario")
	}
}

// Que el archivo no exista es lo normal en un nodo recién instalado: no puede
// impedir arrancar, o no habría forma de crear al primer usuario.
func TestUnRegistroQueNoExisteNoEsUnError(t *testing.T) {
	r, err := CargarRegistro(filepath.Join(t.TempDir(), "no-existe"), iteracionesDePrueba)
	if err != nil {
		t.Fatalf("CargarRegistro sobre un archivo ausente: %v", err)
	}
	if r.Cuantos() != 0 {
		t.Errorf("registro ausente con %d cuentas", r.Cuantos())
	}
}

// Una cuenta nueva es alcanzable desde Internet desde el primer minuto.
func TestUnaCuentaNuevaExigeCatorceCaracteres(t *testing.T) {
	r := registroVacio(t)
	if err := r.Alta("juan", "trece-carac"); err == nil {
		t.Error("aceptó una contraseña por debajo del mínimo de una cuenta")
	}
}

// El registro se guarda solo con permisos 0600: legible por el servicio y por
// nadie más. Un registro legible por todo el sistema es una lista de objetivos.
func TestElRegistroNoEsLegiblePorTerceros(t *testing.T) {
	// Se omite fuera de Unix, y no por comodidad: Windows no aplica los bits
	// POSIX —devuelve 0666 pase lo que pase— así que aquí la prueba no
	// mediría nada. El objetivo de despliegue es linux/arm64 (D-06), y ahí
	// sí corre. Es el mismo criterio que sincronizar_otros.go.
	if runtime.GOOS == "windows" {
		t.Skip("los permisos POSIX no se aplican en Windows; esta garantía se mide en el nodo")
	}
	ruta := filepath.Join(t.TempDir(), "usuarios")
	r, err := CargarRegistro(ruta, iteracionesDePrueba)
	if err != nil {
		t.Fatalf("CargarRegistro: %v", err)
	}
	if err := r.Alta("juan", claveDePrueba); err != nil {
		t.Fatalf("Alta: %v", err)
	}
	fi, err := os.Stat(ruta)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if modo := fi.Mode().Perm(); modo&0o077 != 0 {
		t.Errorf("el registro tiene permisos %o: lo pueden leer otros", modo)
	}
}

// El nombre del superusuario no puede ser también el de una cuenta.
//
// Si lo fuera, esa cuenta sería un fantasma: existiría en el registro, tendría
// carpeta creada, y NO PODRÍA ENTRAR NUNCA, porque el acceso resuelve ese
// nombre contra la credencial del superusuario antes de mirar aquí. Se rechaza
// al darla de alta Y al leer el archivo, porque el archivo se edita a mano
// cuando algo va mal.
func TestNingunaCuentaPuedeLlamarseComoElSuperusuario(t *testing.T) {
	r := registroVacio(t)
	if err := r.Alta(NombreSuperusuario, claveDePrueba); err == nil {
		t.Fatalf("se dio de alta una cuenta llamada %q", NombreSuperusuario)
	}

	// Y colado a mano en el archivo, tampoco: el registro no carga.
	ruta := filepath.Join(t.TempDir(), "usuarios")
	linea, err := Derivar(claveDePrueba, iteracionesDePrueba)
	if err != nil {
		t.Fatalf("Derivar: %v", err)
	}
	if err := os.WriteFile(ruta, []byte(NombreSuperusuario+":"+linea+"\n"), 0o600); err != nil {
		t.Fatalf("escribir el registro a mano: %v", err)
	}
	if _, err := CargarRegistro(ruta, iteracionesDePrueba); err == nil {
		t.Errorf("se cargó un registro con una cuenta llamada %q", NombreSuperusuario)
	}
}
