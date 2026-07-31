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

// Esta prueba CAMBIÓ el 2026-07-31 con ADR-0032, que supersede a ADR-0018
// en lo del puerto.
//
// Antes exigía rechazar el 80 porque «necesitaría privilegio elevado». Ya
// no es cierto: systemd concede CAP_NET_BIND_SERVICE por AmbientCapabilities
// sin elevar el proceso, que sigue corriendo como el usuario nas.
//
// Se conserva la prueba —cambiada, no borrada— porque el rango sigue
// teniendo que validarse.
func TestValidarPuerto(t *testing.T) {
	c := porDefecto()

	// El 80 es ahora legítimo: ADR-0032.
	c.Puerto = 80
	if err := c.validar(); err != nil {
		t.Errorf("el puerto 80 debe aceptarse desde ADR-0032: %v", err)
	}

	for _, p := range []int{0, -1, 65536, 99999} {
		c.Puerto = p
		if err := c.validar(); err == nil {
			t.Errorf("el puerto %d está fuera de rango y debía rechazarse", p)
		}
	}
}
