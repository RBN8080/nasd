package fsposix

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nasd/internal/almacen"
)

// AISLAMIENTO POR USUARIO — ADR-0055.
//
// Estas son las pruebas que más importan de todo el proyecto: si una falla,
// un usuario puede leer o destruir archivos de otro. Se prueban CONTRA EL
// DISCO de verdad, no contra un talón, porque lo que se afirma es dónde
// acaban los bytes.

func escribir(t *testing.T, a *Almacen, nombre, contenido string) {
	t.Helper()
	e, err := a.Crear(context.Background(), ruta(t, nombre))
	if err != nil {
		t.Fatalf("Crear(%q): %v", nombre, err)
	}
	if _, err := io.WriteString(e, contenido); err != nil {
		t.Fatalf("Write(%q): %v", nombre, err)
	}
	if err := e.Confirmar(); err != nil {
		t.Fatalf("Confirmar(%q): %v", nombre, err)
	}
}

func listar(t *testing.T, a *Almacen, r almacen.RutaSegura) []string {
	t.Helper()
	var out []string
	for e, err := range a.Listar(context.Background(), r) {
		if err != nil {
			t.Fatalf("Listar: %v", err)
		}
		out = append(out, e.Nombre)
	}
	return out
}

// Lo que escribe un usuario acaba DENTRO de su carpeta, no en la raíz común.
func TestLoQueEscribeUnUsuarioCaeEnSuCarpeta(t *testing.T) {
	dir := t.TempDir()
	base, err := AbrirVolumen(dir)
	if err != nil {
		t.Fatalf("AbrirVolumen: %v", err)
	}
	defer base.Close()

	juan, err := base.ParaUsuario("juan")
	if err != nil {
		t.Fatalf("ParaUsuario: %v", err)
	}
	escribir(t, juan, "foto.jpg", "de juan")

	// El efecto se comprueba en el disco, no en la respuesta de la API.
	suyo := filepath.Join(dir, "datos", SubHomeUsers, "juan", "foto.jpg")
	if b, err := os.ReadFile(suyo); err != nil || string(b) != "de juan" {
		t.Fatalf("el archivo no está en la carpeta del usuario (%v)", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "datos", "foto.jpg")); err == nil {
		t.Fatal("el archivo se escribió TAMBIÉN en la raíz común: no hay aislamiento")
	}
}

// UN USUARIO NO VE LO DEL SUPERUSUARIO. Es la propiedad central.
func TestUnUsuarioNoVeLosArchivosDelSuperusuario(t *testing.T) {
	base := nuevoVolumen(t)
	escribir(t, base, "declaracion-de-hacienda.pdf", "privado")

	juan, err := base.ParaUsuario("juan")
	if err != nil {
		t.Fatalf("ParaUsuario: %v", err)
	}
	escribir(t, juan, "suyo.txt", "mio")

	visto := listar(t, juan, almacen.Raiz())
	for _, n := range visto {
		if n == "declaracion-de-hacienda.pdf" {
			t.Fatal("UN USUARIO VE LOS ARCHIVOS DEL SUPERUSUARIO")
		}
	}
	if len(visto) != 1 || visto[0] != "suyo.txt" {
		t.Fatalf("la raíz del usuario debería tener solo lo suyo, tiene %v", visto)
	}
}

// DOS USUARIOS NO SE VEN ENTRE SÍ.
func TestDosUsuariosNoSeVenEntreSi(t *testing.T) {
	base := nuevoVolumen(t)
	juan, err := base.ParaUsuario("juan")
	if err != nil {
		t.Fatalf("ParaUsuario(juan): %v", err)
	}
	ana, err := base.ParaUsuario("ana")
	if err != nil {
		t.Fatalf("ParaUsuario(ana): %v", err)
	}
	escribir(t, juan, "de-juan.txt", "j")
	escribir(t, ana, "de-ana.txt", "a")

	if v := listar(t, ana, almacen.Raiz()); len(v) != 1 || v[0] != "de-ana.txt" {
		t.Fatalf("ana ve %v; debería ver solo lo suyo", v)
	}
	if v := listar(t, juan, almacen.Raiz()); len(v) != 1 || v[0] != "de-juan.txt" {
		t.Fatalf("juan ve %v; debería ver solo lo suyo", v)
	}
}

// El superusuario SÍ ve el contenedor y, dentro, a todos.
func TestElSuperusuarioSiVeLasCarpetasDeLosUsuarios(t *testing.T) {
	base := nuevoVolumen(t)
	if _, err := base.ParaUsuario("juan"); err != nil {
		t.Fatalf("ParaUsuario: %v", err)
	}

	raiz := listar(t, base, almacen.Raiz())
	if len(raiz) != 1 || raiz[0] != SubHomeUsers {
		t.Fatalf("el superusuario ve %v; se esperaba el contenedor %q", raiz, SubHomeUsers)
	}
	dentro := listar(t, base, ruta(t, SubHomeUsers))
	if len(dentro) != 1 || dentro[0] != "juan" {
		t.Fatalf("dentro del contenedor hay %v; se esperaba «juan»", dentro)
	}
}

// Ninguna ruta que un usuario pueda pedir sale de su carpeta. RutaSegura ya
// rechaza «..», así que aquí se comprueba lo que SÍ es construible: rutas
// profundas, y que todas caen dentro del prefijo.
func TestNingunaRutaDeUnUsuarioSaleDeSuCarpeta(t *testing.T) {
	base := nuevoVolumen(t)
	juan, err := base.ParaUsuario("juan")
	if err != nil {
		t.Fatalf("ParaUsuario: %v", err)
	}

	esperado := "datos/" + SubHomeUsers + "/juan"
	if got := juan.real(almacen.Raiz()); got != esperado {
		t.Fatalf("la raíz del usuario se traduce a %q; se esperaba %q", got, esperado)
	}
	for _, r := range []string{"a", "a/b", "a/b/c/d/e", "carpeta con espacios/x.txt"} {
		got := juan.real(ruta(t, r))
		if !strings.HasPrefix(got, esperado+"/") {
			t.Errorf("la ruta %q se tradujo a %q, FUERA de %q", r, got, esperado)
		}
	}
}

// RutaSegura es la capa 1 y tiene que seguir rechazando lo evidente: si esto
// dejara de valer, el prefijo por sí solo no bastaría.
func TestLaCapaUnoSigueRechazandoLasFugas(t *testing.T) {
	for _, s := range []string{"..", "../x", "a/../..", "/etc/passwd", `..\x`} {
		if _, err := almacen.NuevaRuta(s); err == nil {
			t.Errorf("NuevaRuta aceptó %q", s)
		}
	}
}

// El nombre del usuario NO puede convertirse en una ruta. Es la segunda capa
// de la misma defensa que el registro: si alguien relaja aquella, esta queda.
func TestParaUsuarioRechazaNombresQueSonRutas(t *testing.T) {
	base := nuevoVolumen(t)
	for _, n := range []string{"", "..", ".", "a/b", "../fuera", `a\b`, "Juan",
		"juan.", "ju an", "juan\x00", strings.Repeat("x", 33)} {
		if _, err := base.ParaUsuario(n); err == nil {
			t.Errorf("ParaUsuario aceptó %q como nombre de carpeta", n)
		}
	}
}

// Borrar en la carpeta de un usuario borra LO SUYO, no lo del vecino con el
// mismo nombre de archivo.
func TestBorrarSoloAlcanzaLaCarpetaPropia(t *testing.T) {
	base := nuevoVolumen(t)
	escribir(t, base, "informe.txt", "del jefe")

	juan, err := base.ParaUsuario("juan")
	if err != nil {
		t.Fatalf("ParaUsuario: %v", err)
	}
	escribir(t, juan, "informe.txt", "de juan")

	if err := juan.Borrar(context.Background(), ruta(t, "informe.txt")); err != nil {
		t.Fatalf("Borrar: %v", err)
	}
	// El del superusuario sobrevive.
	if _, err := base.Estado(context.Background(), ruta(t, "informe.txt")); err != nil {
		t.Fatalf("EL BORRADO DE UN USUARIO ALCANZÓ AL SUPERUSUARIO: %v", err)
	}
}

// Mover dentro de la carpeta de un usuario funciona y sigue dentro.
func TestMoverDentroDeLaCarpetaDeUnUsuarioSigueDentro(t *testing.T) {
	dir := t.TempDir()
	base, err := AbrirVolumen(dir)
	if err != nil {
		t.Fatalf("AbrirVolumen: %v", err)
	}
	defer base.Close()

	juan, err := base.ParaUsuario("juan")
	if err != nil {
		t.Fatalf("ParaUsuario: %v", err)
	}
	if err := juan.CrearDirectorio(context.Background(), ruta(t, "destino")); err != nil {
		t.Fatalf("CrearDirectorio: %v", err)
	}
	escribir(t, juan, "x.txt", "hola")
	if err := juan.Renombrar(context.Background(), ruta(t, "x.txt"), ruta(t, "destino/x.txt")); err != nil {
		t.Fatalf("Renombrar: %v", err)
	}

	dentro := filepath.Join(dir, "datos", SubHomeUsers, "juan", "destino", "x.txt")
	if _, err := os.Stat(dentro); err != nil {
		t.Fatalf("el archivo movido no está donde debe: %v", err)
	}
}

// LA RAÍZ DE UN USUARIO NO SE TOCA POR LA VÍA NORMAL, ni siendo el
// superusuario. Lo pidió así el responsable, y la regla vive en el servidor:
// esconder el botón del listado no impediría la petición.
func TestElSuperusuarioNoPuedeDestruirLaCarpetaDeUnUsuarioPorLaViaNormal(t *testing.T) {
	base := nuevoVolumen(t)
	juan, err := base.ParaUsuario("juan")
	if err != nil {
		t.Fatalf("ParaUsuario: %v", err)
	}
	escribir(t, juan, "importante.txt", "no se pierde por un clic")
	ctx := context.Background()

	casos := map[string]func() error{
		"borrar el contenedor":       func() error { return base.Borrar(ctx, ruta(t, SubHomeUsers)) },
		"talar el contenedor":        func() error { return base.BorrarArbol(ctx, ruta(t, SubHomeUsers)) },
		"borrar la raíz de juan":     func() error { return base.Borrar(ctx, ruta(t, SubHomeUsers+"/juan")) },
		"talar la raíz de juan":      func() error { return base.BorrarArbol(ctx, ruta(t, SubHomeUsers+"/juan")) },
		"mover la raíz de juan":      func() error { return base.Renombrar(ctx, ruta(t, SubHomeUsers+"/juan"), ruta(t, "botin")) },
		"mover algo ENCIMA de juan":  func() error { return base.Renombrar(ctx, ruta(t, "otra"), ruta(t, SubHomeUsers+"/juan")) },
		"crear el contenedor a mano": func() error { return base.CrearDirectorio(ctx, ruta(t, SubHomeUsers)) },
	}
	for nombre, hacer := range casos {
		if err := hacer(); !errors.Is(err, almacen.ErrReservado) {
			t.Errorf("%s: se esperaba ErrReservado y salió %v", nombre, err)
		}
	}

	// Y el archivo sigue ahí después de todos los intentos.
	if _, err := juan.Estado(ctx, ruta(t, "importante.txt")); err != nil {
		t.Fatalf("algún intento llegó a destruir datos: %v", err)
	}
}

// PERO LAS SUBCARPETAS DE UN USUARIO SÍ SE ADMINISTRAN, por él y por el
// superusuario. Es exactamente lo que pidió el responsable, y la diferencia
// con lo de arriba es un solo nivel de profundidad.
func TestLasSubcarpetasDeUnUsuarioSiSeAdministran(t *testing.T) {
	base := nuevoVolumen(t)
	juan, err := base.ParaUsuario("juan")
	if err != nil {
		t.Fatalf("ParaUsuario: %v", err)
	}
	ctx := context.Background()
	if err := juan.CrearDirectorio(ctx, ruta(t, "vacaciones")); err != nil {
		t.Fatalf("el usuario no pudo crear una carpeta suya: %v", err)
	}
	// El superusuario la borra desde fuera, por su ruta completa.
	if err := base.BorrarArbol(ctx, ruta(t, SubHomeUsers+"/juan/vacaciones")); err != nil {
		t.Fatalf("el superusuario no pudo administrar una subcarpeta: %v", err)
	}
	// Y el propio usuario también puede con las suyas.
	if err := juan.CrearDirectorio(ctx, ruta(t, "otra")); err != nil {
		t.Fatalf("CrearDirectorio: %v", err)
	}
	if err := juan.Borrar(ctx, ruta(t, "otra")); err != nil {
		t.Fatalf("el usuario no pudo borrar su propia carpeta: %v", err)
	}
}

// La reserva se compara POR COMPONENTES: una carpeta del superusuario cuyo
// nombre empiece igual es legítima y no puede quedar bloqueada.
func TestUnaCarpetaQueSoloEmpiezaIgualNoQuedaBloqueada(t *testing.T) {
	base := nuevoVolumen(t)
	ctx := context.Background()
	for _, n := range []string{SubHomeUsers + "XYZ", "mi" + SubHomeUsers} {
		if err := base.CrearDirectorio(ctx, ruta(t, n)); err != nil {
			t.Errorf("bloqueó %q, que no es una ruta reservada: %v", n, err)
		}
	}
}

// Un usuario puede tener una carpeta suya llamada como el contenedor: dentro
// de su raíz ese nombre no significa nada especial.
func TestUnUsuarioPuedeTenerUnaCarpetaLlamadaComoElContenedor(t *testing.T) {
	base := nuevoVolumen(t)
	juan, err := base.ParaUsuario("juan")
	if err != nil {
		t.Fatalf("ParaUsuario: %v", err)
	}
	if err := juan.CrearDirectorio(context.Background(), ruta(t, SubHomeUsers)); err != nil {
		t.Fatalf("a un usuario se le prohibió un nombre de carpeta suyo: %v", err)
	}
}
