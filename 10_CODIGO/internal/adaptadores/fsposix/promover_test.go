package fsposix

import (
	"os"
	"path/filepath"
	"testing"
)

// PROMOCIÓN DE LA CARPETA AL DAR DE BAJA
func TestPromoverCarpetaDeUsuarioLaSacaDeHomeUsers(t *testing.T) {
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

	if err := base.PromoverCarpetaDeUsuario("juan"); err != nil {
		t.Fatalf("PromoverCarpetaDeUsuario: %v", err)
	}

	// El archivo aparece en su sitio nuevo, con el mismo contenido.
	nuevo := filepath.Join(dir, "datos", "juan", "foto.jpg")
	if b, err := os.ReadFile(nuevo); err != nil || string(b) != "de juan" {
		t.Fatalf("el archivo no está en la raíz tras promover (%v)", err)
	}
	// Y ya no está en homeUsers/.
	if _, err := os.Stat(filepath.Join(dir, "datos", SubHomeUsers, "juan")); !os.IsNotExist(err) {
		t.Fatalf("homeUsers/juan seguía existiendo tras promover: %v", err)
	}

	// El superusuario ve la carpeta promovida como una más de su raíz.
	nombres := listar(t, base, ruta(t, ""))
	encontrada := false
	for _, n := range nombres {
		if n == "juan" {
			encontrada = true
		}
	}
	if !encontrada {
		t.Errorf("la raíz no lista «juan» tras promover: %v", nombres)
	}
}

// Quien nunca entró no tiene carpeta que promover —ParaUsuario la crea al
// primer acceso, no al alta— y eso no es un error.
func TestPromoverCarpetaDeUsuarioQueNuncaEntroNoEsError(t *testing.T) {
	base := nuevoVolumen(t)

	if err := base.PromoverCarpetaDeUsuario("nunca-entro"); err != nil {
		t.Fatalf("PromoverCarpetaDeUsuario de alguien sin carpeta: %v", err)
	}
	if _, err := os.Stat(filepath.Join(base.raiz.Name(), "datos", "nunca-entro")); !os.IsNotExist(err) {
		t.Errorf("se creó algo en la raíz para una cuenta sin carpeta")
	}
}

// Un choque de nombre en la raíz aborta la promoción, y no fusiona ni
// sobrescribe (RF-23, D-12): el archivo del usuario sigue intacto donde
// estaba, listo para reintentar tras resolver el choque.
func TestPromoverCarpetaDeUsuarioRehusaSiYaExisteAlgoEnLaRaiz(t *testing.T) {
	base := nuevoVolumen(t)

	// Algo del superusuario, ya en la raíz, con el mismo nombre que la
	// cuenta que se va a dar de baja.
	escribir(t, base, "juan", "un archivo del superusuario, no una carpeta")

	juan, err := base.ParaUsuario("juan")
	if err != nil {
		t.Fatalf("ParaUsuario: %v", err)
	}
	escribir(t, juan, "foto.jpg", "de juan")

	if err := base.PromoverCarpetaDeUsuario("juan"); err == nil {
		t.Fatal("promovió sobre un nombre que ya existía en la raíz, sin avisar")
	}

	// Lo que ya había en la raíz sigue siendo EXACTAMENTE eso: un archivo,
	// con su contenido, no reemplazado ni fusionado con la carpeta.
	raiz := filepath.Join(base.raiz.Name(), "datos", "juan")
	if fi, err := os.Stat(raiz); err != nil || fi.IsDir() {
		t.Fatalf("lo que ya había en la raíz cambió tras el choque (dir=%v, err=%v)", fi != nil && fi.IsDir(), err)
	}
	// Y el archivo del usuario sigue en homeUsers/, intacto: nada se movió.
	suyo := filepath.Join(base.raiz.Name(), "datos", SubHomeUsers, "juan", "foto.jpg")
	if b, err := os.ReadFile(suyo); err != nil || string(b) != "de juan" {
		t.Fatalf("el archivo del usuario se perdió o se movió tras el choque (%v)", err)
	}
}
