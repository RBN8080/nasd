package config

import (
	"strings"
	"testing"
)

func TestLeerTOML(t *testing.T) {
	entrada := `
# comentario
volumen = "/srv/nas"

[red]
direccion = "192.168.1.38"
puerto = 8080   # comentario al final

[plazos]
inactividad_s = 60
`
	v, err := leerTOML(strings.NewReader(entrada))
	if err != nil {
		t.Fatalf("leerTOML falló: %v", err)
	}
	quiero := map[string]string{
		"volumen":              "/srv/nas",
		"red.direccion":        "192.168.1.38",
		"red.puerto":           "8080",
		"plazos.inactividad_s": "60",
	}
	for k, esperado := range quiero {
		if v[k] != esperado {
			t.Errorf("%s = %q; se esperaba %q", k, v[k], esperado)
		}
	}
}

// P5: lo no admitido falla, no se ignora en silencio.
func TestLeerTOMLRechaza(t *testing.T) {
	malos := []string{
		`clave = [1, 2]`,
		`clave = {a = 1}`,
		`[a.b]`,
		`sin_igual`,
		`[seccion`,
	}
	for _, m := range malos {
		if _, err := leerTOML(strings.NewReader(m)); err == nil {
			t.Errorf("leerTOML(%q) debía fallar", m)
		}
	}
}

func TestValidarPuerto(t *testing.T) {
	c := porDefecto()
	c.Puerto = 80 // exigiría privilegio elevado — ADR-0018
	if err := c.validar(); err == nil {
		t.Error("el puerto 80 debía rechazarse")
	}
}
