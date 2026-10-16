package web

import (
	"net/netip"
	"testing"
	"time"

	"nasd/internal/seguridad"
)

// EL RESUMEN DIARIO CUENTA LOS APARTADOS DE SU VENTANA — ADR-0099. quizaResumen
// le pasaba nil a reunir, así que «Cuarentenas aplicadas» salía siempre en cero.
// Lo que se fija aquí es el filtro: entran los nacidos dentro de la ventana, y
// el apartado de hace un mes —que sigue vigente porque no caduca— no.
func TestElResumenCuentaSoloLosApartadosDeSuVentana(t *testing.T) {
	ahora := time.Now()
	desde := ahora.Add(-24 * time.Hour)
	vigentes := []seguridad.Apartado{
		{IP: netip.MustParseAddr("2a0a:20c0:f001:307::4"), Desde: ahora.Add(-8 * time.Hour), ASN: 264617},
		{IP: netip.MustParseAddr("2604:a880:800:14::5633:b000"), Desde: ahora.AddDate(0, -1, 0), ASN: 14061},
		{IP: netip.MustParseAddr("203.0.113.7"), Desde: desde, Hasta: desde.Add(seguridad.DuracionCuarentena)},
	}

	got := apartadosDesde(vigentes, desde)
	if len(got) != 2 {
		t.Fatalf("apartados en la ventana = %d; se esperaban 2", len(got))
	}
	for _, a := range got {
		if a.ASN == 14061 {
			t.Error("entró en el resumen de hoy un apartado de hace un mes")
		}
	}
}
