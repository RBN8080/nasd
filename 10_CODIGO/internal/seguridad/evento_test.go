package seguridad

import (
	"net/netip"
	"testing"
)

// TestClasificarRed cubre las tres redes y, sobre todo, el caso que de verdad
// puede romperse sin que nadie lo note.
func TestClasificarRed(t *testing.T) {
	casos := []struct {
		nombre string
		ip     string
		quiero Red
	}{
		{"LAN doméstica (ADR-0018)", "192.168.1.38", RedLocal},
		{"otro equipo de la LAN", "192.168.1.18", RedLocal},
		{"túnel WireGuard", "10.77.0.3", RedTunel},
		{"nodo del túnel", "10.77.0.1", RedTunel},
		{"bucle local", "127.0.0.1", RedLocal},
		{"Internet", "203.0.113.7", RedInternet},

		// EL CASO QUE IMPORTA, y no es una rareza sino el camino NORMAL:
		// nasd escucha TLS en [::]:443 (tls.go), un socket IPv6 que entrega
		// las direcciones IPv4 como ::ffff:a.b.c.d. Sin desenvolverlas, TODO
		// el tráfico de Internet por HTTPS se clasificaría como «internet»
		// por accidente y el de casa TAMBIÉN, que es el fallo peor: el panel
		// diría que tu propia visita viene de fuera.
		{"LAN llegando por el socket IPv6", "::ffff:192.168.1.18", RedLocal},
		{"túnel llegando por el socket IPv6", "::ffff:10.77.0.3", RedTunel},
		{"Internet llegando por el socket IPv6", "::ffff:203.0.113.7", RedInternet},

		// IPv6 nativa: el nodo tiene AAAA publicado (06_ACCESO_REMOTO §8.4),
		// así que esto llega de verdad y debe caer en Internet, no en
		// «desconocida».
		{"IPv6 pública", "3fff:2a0:101e:3d82::1", RedInternet},
	}
	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			if got := ClasificarRed(netip.MustParseAddr(c.ip)); got != c.quiero {
				t.Fatalf("%s → %v, se esperaba %v", c.ip, got, c.quiero)
			}
		})
	}
}

// TestUnaDireccionInvalidaNoSeDaPorLocal es fallo cerrado: lo que no se
// reconoce nunca se trata como de casa.
func TestUnaDireccionInvalidaNoSeDaPorLocal(t *testing.T) {
	if got := ClasificarRed(netip.Addr{}); got == RedLocal || got == RedTunel {
		t.Fatalf("una dirección inválida se clasificó como %v", got)
	}
}

// TestLaGravedadNoConfundeRutinaConAmenaza fija la condición explícita del
// responsable: «no clasifiques automáticamente todo rechazo como ataque».
// Los dos motivos que produce el uso normal del NAS no pueden subir de
// rutina, o el panel se ahogaría en su propio ruido — que es exactamente el
// defecto del contador único que esto viene a sustituir.
func TestLaGravedadNoConfundeRutinaConAmenaza(t *testing.T) {
	for _, m := range []Motivo{SinSesion, SesionCaducada} {
		if m.Gravedad() != Rutina {
			t.Fatalf("%q está marcado como %q y es el caso normal de uso",
				m.Etiqueta(), m.Gravedad())
		}
	}
	// Y al revés: llegar a estas cuatro exige una sesión ya iniciada o un
	// patrón deliberado, así que no pueden quedar enterradas como rutina.
	for _, m := range []Motivo{PermisoInsuficiente, LimiteDeIntentos, TestigoCSRF, RecursoReservado} {
		if m.Gravedad() != Atencion {
			t.Fatalf("%q está marcado como %q y no se llega ahí navegando",
				m.Etiqueta(), m.Gravedad())
		}
	}
}

// TestTruncarAgenteNoParteUnaRuna: el archivo se lee como JSON, y una
// secuencia UTF-8 rota lo dejaría ilegible entero. La cabecera debería ser
// ASCII (RFC 9110 §10.1.5), así que este caso solo llega desde un cliente que
// ya envía algo mal formado — es decir, exactamente el que hay que soportar.
func TestTruncarAgenteNoParteUnaRuna(t *testing.T) {
	// «é» son dos bytes: se coloca uno a caballo del tope.
	s := ""
	for len(s) < topeAgente-1 {
		s += "a"
	}
	s += "éééé"
	got := TruncarAgente(s)
	if len(got) > topeAgente {
		t.Fatalf("el truncado devuelve %d bytes y el tope es %d", len(got), topeAgente)
	}
	for i, r := range got {
		if r == '�' {
			t.Fatalf("el truncado dejó una runa rota en la posición %d", i)
		}
	}
}
