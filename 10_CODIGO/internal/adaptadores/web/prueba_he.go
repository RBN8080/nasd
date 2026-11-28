package web

import (
	"net/http"
	"strconv"
)

// RUTA PÚBLICA TEMPORAL PARA LA PRUEBA WEB IPv6 DE HURRICANE ELECTRIC
// (nivel Enthusiast de su certificación). Aprobada por el responsable el
// 2026-11-28; se retira, borrando este archivo y su línea en Rutas(), en
// cuanto HE dé la prueba por pasada.
//
// Es una excepción declarada a ADR-0072: amplía la superficie pública en UNA
// ruta exacta. Devuelve un texto fijo compilado en el binario y no lee nada
// del disco, así que no abre camino hacia el almacén. HE solo comprueba que el
// archivo se descargue por IPv6; el contenido no lo pide.
const (
	rutaPruebaHE      = "/xdrz46cxb.txt"
	contenidoPruebaHE = "xdrz46cxb\n"
)

func servirPruebaHE(w http.ResponseWriter, _ *http.Request) {
	h := w.Header()
	h.Set("Content-Type", "text/plain; charset=utf-8")
	h.Set("Content-Length", strconv.Itoa(len(contenidoPruebaHE)))
	h.Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(contenidoPruebaHE))
}
