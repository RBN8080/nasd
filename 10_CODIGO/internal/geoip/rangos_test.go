package geoip

import (
	"net/netip"
	"testing"
)

// EL CASO REAL, TAL COMO LO MIDIÓ EL RESPONSABLE (IDEAS §28.2).
//
// AS44382 no anuncia UN bloque: anuncia varios, separados por tramos «Not
// routed» que no son suyos. Ese es exactamente el hecho que hizo insuficiente
// el /48 de la ficha de partida, y por eso el panel tiene que poder enseñar
// TODOS los tramos de un operador antes de que nadie elija alcance.
const v6Whitelabel = "::\t::1\t0\tNone\tNot routed\n" +
	"2602:fa5d::\t2602:fa5d:9:ffff:ffff:ffff:ffff:ffff\t44382\tUS\tWHITELABEL\n" +
	"2602:fa5d:a::\t2602:fa5d:f:ffff:ffff:ffff:ffff:ffff\t0\tNone\tNot routed\n" +
	"2602:fa5d:10::\t2602:fa5d:10:ffff:ffff:ffff:ffff:ffff\t44382\tUS\tWHITELABEL\n" +
	"2a06:4883:5000::\t2a06:4883:5000:ffff:ffff:ffff:ffff:ffff\t211298\tGB\tDRIFTNET\n"

func TestBuscarRangoDevuelveLosExtremosDelTramo(t *testing.T) {
	b := prepararDePrueba(t, "", v6Whitelabel)

	r, ok := b.BuscarRango(netip.MustParseAddr("2602:fa5d::8b"))
	if !ok {
		t.Fatal("no encontró el tramo de la dirección que sí visitó el nodo")
	}
	if r.Desde.String() != "2602:fa5d::" {
		t.Errorf("Desde = %s; se esperaba 2602:fa5d::", r.Desde)
	}
	if r.Hasta.String() != "2602:fa5d:9:ffff:ffff:ffff:ffff:ffff" {
		t.Errorf("Hasta = %s", r.Hasta)
	}
	if r.Info.ASN != 44382 {
		t.Errorf("ASN = %d; se esperaba 44382", r.Info.ASN)
	}
	// Y el tramo contiene lo que dice contener, que es de lo que depende el
	// bloqueo entero.
	for _, dentro := range []string{"2602:fa5d::8b", "2602:fa5d:1::1", "2602:fa5d:9:ffff::1"} {
		if !r.Contiene(netip.MustParseAddr(dentro)) {
			t.Errorf("%s debería caer dentro del tramo", dentro)
		}
	}
	if r.Contiene(netip.MustParseAddr("2602:fa5d:10::1")) {
		t.Error("2602:fa5d:10::1 es de OTRO tramo del mismo operador, no de este")
	}
}

// LA PRUEBA QUE JUSTIFICA EL ALCANCE «OPERADOR»: los tramos de un AS están
// desperdigados y separados por tramos ajenos. Un bloqueo que solo cubriera el
// primero parecería puesto y dejaría pasar al resto.
func TestRangosDeDevuelveTodosLosTramosDelOperador(t *testing.T) {
	b := prepararDePrueba(t, "", v6Whitelabel)

	rangos, err := b.RangosDe(44382)
	if err != nil {
		t.Fatalf("RangosDe: %v", err)
	}
	if len(rangos) != 2 {
		t.Fatalf("tramos de AS44382 = %d; se esperaban 2", len(rangos))
	}
	// Los dos, y ninguno de los «Not routed» de en medio.
	if rangos[0].Desde.String() != "2602:fa5d::" || rangos[1].Desde.String() != "2602:fa5d:10::" {
		t.Errorf("tramos equivocados: %v", rangos)
	}
	for _, r := range rangos {
		if r.Info.ASN != 44382 {
			t.Errorf("se coló un tramo de AS%d", r.Info.ASN)
		}
	}

	// El otro operador no se mezcla.
	otros, err := b.RangosDe(211298)
	if err != nil {
		t.Fatalf("RangosDe(211298): %v", err)
	}
	if len(otros) != 1 {
		t.Errorf("tramos de DRIFTNET = %d; se esperaba 1", len(otros))
	}

	// «Not routed» NO es un operador: pedir el cero no puede devolver medio
	// Internet.
	if nada, err := b.RangosDe(0); err != nil || len(nada) != 0 {
		t.Errorf("RangosDe(0) = %d tramos, %v; se esperaba ninguno", len(nada), err)
	}
}
