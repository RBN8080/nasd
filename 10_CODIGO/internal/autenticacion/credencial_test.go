package autenticacion

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// Iteraciones bajas a propósito: aquí se prueba la LÓGICA, no el coste. El
// coste real está medido en el nodo (3 232 ms con 600 000) y fijado en el
// adaptador web.
const itPrueba = 1000

func TestDerivarYVerificar(t *testing.T) {
	clave := "una-contraseña-larga-ñ"
	linea, err := Derivar(clave, itPrueba)
	if err != nil {
		t.Fatalf("Derivar: %v", err)
	}
	if !strings.HasPrefix(linea, "pbkdf2-sha256$1000$") {
		t.Errorf("formato inesperado: %q", linea)
	}
	if strings.Contains(linea, clave) {
		t.Fatal("LA CONTRASEÑA APARECE EN CLARO en la línea almacenada")
	}
	if !Verificar(linea, clave) {
		t.Error("la contraseña correcta no verifica")
	}
	for _, mala := range []string{"", "otra-cosa-larga-aqui", clave + "x", strings.ToUpper(clave)} {
		if Verificar(linea, mala) {
			t.Errorf("verificó una contraseña incorrecta: %q", mala)
		}
	}
}

// Dos derivaciones de la MISMA contraseña deben dar líneas distintas: si la
// sal no fuera aleatoria, dos usuarios con igual contraseña tendrían igual
// hash y una tabla precalculada valdría para ambos.
func TestSalAleatoria(t *testing.T) {
	a, _ := Derivar("misma-contraseña-larga", itPrueba)
	b, _ := Derivar("misma-contraseña-larga", itPrueba)
	if a == b {
		t.Fatal("la sal no es aleatoria: dos derivaciones idénticas")
	}
	if !Verificar(a, "misma-contraseña-larga") || !Verificar(b, "misma-contraseña-larga") {
		t.Error("ambas debían verificar")
	}
}

func TestRechazaClaveCorta(t *testing.T) {
	if _, err := Derivar("corta", itPrueba); !errors.Is(err, ErrClaveCorta) {
		t.Fatalf("se esperaba ErrClaveCorta; llegó %v", err)
	}
}

// Una credencial corrupta NUNCA debe dejar pasar a nadie. El modo de fallo
// seguro es rechazar, no aceptar.
func TestCredencialCorruptaNoDejaPasar(t *testing.T) {
	malas := []string{
		"", "basura", "pbkdf2-sha256$", "pbkdf2-sha256$abc$x$y",
		"pbkdf2-sha256$1000$***$***",
		"md5$1000$c2Fs$aGFzaA", // algoritmo desconocido
		"pbkdf2-sha256$0$c2Fs$aGFzaA",
	}
	for _, m := range malas {
		if Verificar(m, "loquesea") {
			t.Errorf("una credencial corrupta dejó pasar: %q", m)
		}
		if err := Valida(m); err == nil {
			t.Errorf("Valida aceptó una credencial corrupta: %q", m)
		}
	}
}

func TestValidaAceptaLaBuena(t *testing.T) {
	linea, _ := Derivar("contraseña-valida-larga", itPrueba)
	if err := Valida(linea); err != nil {
		t.Fatalf("Valida rechazó una credencial correcta: %v", err)
	}
}

func TestSesionesCicloCompleto(t *testing.T) {
	s := NuevasSesiones(time.Hour)

	if s.Valida("") || s.Valida("inventado") {
		t.Fatal("validó un testigo que no existe")
	}
	t1, err := s.Abrir("admin")
	if err != nil {
		t.Fatalf("Abrir: %v", err)
	}
	t2, _ := s.Abrir("juan")
	if t1 == t2 {
		t.Fatal("dos sesiones con el mismo testigo: el azar no es azar")
	}
	if !s.Valida(t1) || !s.Valida(t2) {
		t.Error("las sesiones recién abiertas deben ser válidas")
	}
	if s.Abiertas() != 2 {
		t.Errorf("Abiertas() = %d; se esperaban 2", s.Abiertas())
	}
	s.Cerrar(t1)
	if s.Valida(t1) {
		t.Error("una sesión cerrada sigue valiendo")
	}
	if !s.Valida(t2) {
		t.Error("cerrar una sesión afectó a otra")
	}
}

func TestSesionCaducaYSePurga(t *testing.T) {
	s := NuevasSesiones(10 * time.Millisecond)
	tok, _ := s.Abrir("juan")
	if !s.Valida(tok) {
		t.Fatal("debía ser válida al abrirla")
	}
	time.Sleep(30 * time.Millisecond)
	if s.Valida(tok) {
		t.Error("una sesión caducada sigue valiendo")
	}

	// Y la purga debe vaciar el mapa: sin ella crecería sin fin, que es la
	// misma fuga que ya costó una revisión con las subidas.
	s2 := NuevasSesiones(10 * time.Millisecond)
	for range 5 {
		s2.Abrir("juan")
	}
	time.Sleep(30 * time.Millisecond)
	if n := s2.Purgar(); n != 5 {
		t.Errorf("purgadas = %d; se esperaban 5", n)
	}
	if s2.Abiertas() != 0 {
		t.Errorf("quedaron %d sesiones tras purgar", s2.Abiertas())
	}
}

// Una sesión SIN DUEÑO no puede existir: es a lo que después habría que
// asignarle una carpeta adivinando, y adivinar ahí significa enseñarle a
// alguien la carpeta de otro (ADR-0055).
func TestNoSeAbreUnaSesionSinUsuario(t *testing.T) {
	s := NuevasSesiones(time.Hour)
	if _, err := s.Abrir(""); err == nil {
		t.Fatal("se abrió una sesión sin usuario")
	}
	if s.Abiertas() != 0 {
		t.Errorf("quedó registrada una sesión sin dueño: %d abiertas", s.Abiertas())
	}
}

// La sesión recuerda de quién es, y eso es lo que decide qué carpeta se ve.
func TestLaSesionRecuerdaDeQuienEs(t *testing.T) {
	s := NuevasSesiones(time.Hour)
	tok, err := s.Abrir("juan")
	if err != nil {
		t.Fatalf("Abrir: %v", err)
	}
	quien, ok := s.Usuario(tok)
	if !ok || quien != "juan" {
		t.Errorf("Usuario() = %q, %v; se esperaba juan", quien, ok)
	}
	// Un testigo inventado no tiene dueño, y sobre todo no hereda ninguno.
	if quien, ok := s.Usuario("inventado"); ok || quien != "" {
		t.Errorf("un testigo inventado devolvió %q, %v", quien, ok)
	}
	s.Cerrar(tok)
	if _, ok := s.Usuario(tok); ok {
		t.Error("una sesión cerrada sigue teniendo dueño")
	}
}

// El panel de administración (P-4, etapa 2) usa esto para mostrar quién
// tiene sesión abierta ahora mismo.
func TestActivosPorUsuario(t *testing.T) {
	s := NuevasSesiones(time.Hour)
	if activos := s.ActivosPorUsuario(); len(activos) != 0 {
		t.Fatalf("sin sesiones abiertas, ActivosPorUsuario() = %v", activos)
	}

	s.Abrir("juan")
	tokAna, _ := s.Abrir("ana")
	// Dos sesiones de la MISMA cuenta —dos aparatos— cuentan como una sola
	// entrada: la pregunta es «¿tiene sesión?», no «¿cuántas?».
	s.Abrir("juan")

	activos := s.ActivosPorUsuario()
	if len(activos) != 2 || !activos["juan"] || !activos["ana"] {
		t.Errorf("ActivosPorUsuario() = %v; se esperaban juan y ana", activos)
	}
	if activos["luis"] {
		t.Error("una cuenta sin sesión salió como activa")
	}

	s.Cerrar(tokAna)
	if activos := s.ActivosPorUsuario(); activos["ana"] {
		t.Error("ana seguía activa tras cerrar su única sesión")
	}
}

// Y las caducadas no cuentan, aunque nadie las haya purgado todavía.
func TestActivosPorUsuarioNoIncluyeCaducadas(t *testing.T) {
	s := NuevasSesiones(10 * time.Millisecond)
	s.Abrir("juan")
	time.Sleep(30 * time.Millisecond)
	if activos := s.ActivosPorUsuario(); activos["juan"] {
		t.Error("una sesión caducada salió como activa")
	}
}
