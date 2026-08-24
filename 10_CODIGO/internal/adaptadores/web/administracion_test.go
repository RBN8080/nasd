package web

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"nasd/internal/almacen"
	"nasd/internal/autenticacion"
)

// P-9, ADR-0058: una negativa DELIBERADA del dominio —el contenedor de
// usuarios no se toca por la vía normal (fsposix.esReservado)— salía por la
// web como 500 «error interno», indistinguible de un fallo real. Esta prueba
// fija que ErrReservado se traduce en 403, y no en el default de fallo().
//
// No se prueba contra fsposix real: lo que se afirma aquí es el MAPEO en la
// capa web, no la regla de aislamiento en sí —esa vive, contra disco de
// verdad, en fsposix/aislamiento_test.go (mismo criterio que acceso_test.go
// declara para el reparto).
type almacenReservado struct{ almacenVacio }

func (almacenReservado) Resumen(context.Context, almacen.RutaSegura) (almacen.Conteo, error) {
	return almacen.Conteo{EsDirectorio: true}, nil
}

func (almacenReservado) BorrarArbol(context.Context, almacen.RutaSegura) error {
	return almacen.ErrReservado
}

func (almacenReservado) Renombrar(context.Context, almacen.RutaSegura, almacen.RutaSegura) error {
	return almacen.ErrReservado
}

func servidorConAlmacenReservado(t *testing.T) *Servidor {
	t.Helper()
	linea, err := autenticacion.Derivar(claveDePrueba, iteracionesDePrueba)
	if err != nil {
		t.Fatalf("Derivar: %v", err)
	}
	s, err := Nuevo(Opciones{
		Almacen:          almacenReservado{},
		AlmacenDe:        func(string) (almacen.Almacen, error) { return almacenReservado{}, nil },
		PromoverUsuario:  func(string) error { return nil },
		Registro:         slog.New(slog.NewJSONHandler(io.Discard, nil)),
		PlazoInactividad: time.Minute,
		Credencial:       linea,
		Usuarios:         registroDePrueba(t),
		DuracionSesion:   time.Hour,
		Metricas:         metricasDePrueba(t),
		Seguridad:        seguridadDePrueba(t),
		Cuarentena:       cuarentenaDePrueba(t),
		Lista:            listaDePrueba(t),
		Novedades:        novedadesDePrueba(t),
		Hallazgos:        hallazgosDePrueba(t),
		Conexiones:       conexionesDePrueba(t),
	})
	if err != nil {
		t.Fatalf("Nuevo: %v", err)
	}
	return s
}

func TestBorrarUnaRutaReservadaSaleComo403YNoComo500(t *testing.T) {
	s := servidorConAlmacenReservado(t)
	h := s.Rutas()
	cookie, csrf := sesionAbierta(t, s)

	got := postCon(t, h, cookie, "/borrar", url.Values{
		"ruta":       {"homeUsers"},
		"confirmado": {"si"},
		"csrf":       {csrf},
	})
	if got == http.StatusInternalServerError {
		t.Fatalf("POST /borrar sobre una ruta reservada -> 500; "+
			"una negativa deliberada (ErrReservado) no puede leerse como "+
			"un fallo del servicio (P-9). Se esperaba %d", http.StatusForbidden)
	}
	if got != http.StatusForbidden {
		t.Errorf("POST /borrar sobre una ruta reservada -> %d; se esperaba %d",
			got, http.StatusForbidden)
	}

	// El cuerpo tiene que decir POR QUÉ, no solo "error interno": quien lo
	// lea debe poder distinguir "está protegido" de "algo se rompió".
	r := desdeCasa(httptest.NewRequest("POST", "/borrar", strings.NewReader(url.Values{
		"ruta":       {"homeUsers"},
		"confirmado": {"si"},
		"csrf":       {csrf},
	}.Encode())))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if !strings.Contains(w.Body.String(), "panel de Usuarios") {
		t.Errorf("cuerpo del 403 no explica el motivo: %q", w.Body.String())
	}
}

func TestRenombrarUnaRutaReservadaSaleComo403YNoComo500(t *testing.T) {
	s := servidorConAlmacenReservado(t)
	h := s.Rutas()
	cookie, csrf := sesionAbierta(t, s)

	got := postCon(t, h, cookie, "/renombrar", url.Values{
		"origen":  {"homeUsers"},
		"destino": {"botin"},
		"csrf":    {csrf},
	})
	if got != http.StatusForbidden {
		t.Errorf("POST /renombrar sobre una ruta reservada -> %d; se esperaba %d",
			got, http.StatusForbidden)
	}
}
