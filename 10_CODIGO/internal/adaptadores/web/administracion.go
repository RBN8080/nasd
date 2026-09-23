package web

import (
	"errors"
	"net/http"
	"strings"

	"nasd/internal/almacen"
	"nasd/internal/seguridad"
)

// Administración desde la web — RF-16, RF-17, RF-18, RF-19.

func csrfRecibido(r *http.Request) string {
	if v := r.Header.Get("Nas-Csrf"); v != "" {
		return v
	}
	// ParseForm es idempotente y, sobre una petición multipart, NO consume
	// el cuerpo: deja ese trabajo a ParseMultipartForm, que aquí no se llama.
	_ = r.ParseForm()
	return r.PostForm.Get("csrf")
}

// csrfDe extrae el testigo CSRF de la sesión de esta petición.
func (s *Servidor) csrfDe(r *http.Request) string {
	c, err := r.Cookie(nombreCookie)
	if err != nil {
		return ""
	}
	t, _ := s.sesiones.Csrf(c.Value)
	return t
}

// exigirCSRF comprueba el testigo en toda petición que cambia algo.
//
// SameSite=Lax ya frena el POST entre sitios, pero con operaciones
// irreversibles y sin papelera (D-15) una sola capa no basta: bastaría un
// navegador antiguo para destruir datos que no se recuperan.
func (s *Servidor) exigirCSRF(w http.ResponseWriter, r *http.Request) bool {
	c, err := r.Cookie(nombreCookie)
	if err != nil {
		s.pedirAcceso(w, r, "")
		return false
	}
	if !s.sesiones.CsrfValido(c.Value, csrfRecibido(r)) {
		s.reg.Warn("testigo CSRF inválido",
			"origen", origenDe(r), "ruta", r.URL.Path, "metodo", r.Method)
		marcarRechazo(r, seguridad.TestigoCSRF)
		http.Error(w, "petición no autorizada", http.StatusForbidden)
		return false
	}
	return true
}

// RF-16 — renombrar

func (s *Servidor) renombrar(w http.ResponseWriter, r *http.Request, alm almacen.Almacen) {
	if err := r.ParseForm(); err != nil {
		s.fallo(w, r, err)
		return
	}
	if !s.exigirCSRF(w, r) {
		return
	}

	origen, err := almacen.NuevaRuta(r.PostFormValue("origen"))
	if err != nil {
		s.fallo(w, r, err)
		return
	}
	// Hija valida el nombre como componente único: una barra aquí ya no se
	// reinterpreta como ruta, se rechaza por lo que es.
	destino, err := origen.Padre().Hija(strings.TrimSpace(r.PostFormValue("destino")))
	if err != nil {
		s.fallo(w, r, err)
		return
	}

	if err := alm.Renombrar(r.Context(), origen, destino); err != nil {
		// RF-19: toda operación destructiva deja constancia, también cuando falla.
		s.reg.Warn("renombrar FALLÓ",
			"origen", origen.Rel(), "destino", destino.Rel(),
			"remoto", origenDe(r), "error", err)
		s.fallo(w, r, err)
		return
	}
	// RF-19: marca de tiempo la pone el registro, ruta y resultado van aquí.
	s.reg.Info("renombrado",
		"origen", origen.Rel(), "destino", destino.Rel(), "remoto", origenDe(r))
	s.redirigir(w, r, origen.Padre(), "Renombrado: "+destino.Nombre(), false)
}

// confirmarBorrado — el primer paso de RF-18.
//
// RF-18 exige que «ninguna acción destructiva se ejecute con un solo clic
// accidental». Aquí se muestra QUÉ se va a destruir —cuántos archivos y
// cuántos bytes— porque sin papelera (D-15) y con copia única (D-12) esta
// pantalla es la última oportunidad de darse cuenta.
func (s *Servidor) confirmarBorrado(w http.ResponseWriter, r *http.Request, alm almacen.Almacen) {
	ruta, err := almacen.NuevaRuta(r.PathValue("ruta"))
	if err != nil || ruta.EsRaiz() {
		s.fallo(w, r, almacen.ErrRutaInvalida)
		return
	}
	conteo, err := alm.Resumen(r.Context(), ruta)
	if err != nil {
		s.fallo(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	datos := map[string]any{
		"Ruta":   ruta,
		"Conteo": conteo,
		"Csrf":   s.csrfDe(r),
	}
	if err := s.plantillas.ExecuteTemplate(w, "borrar.html", datos); err != nil {
		s.reg.Error("render de la confirmación de borrado", "error", err)
	}
}

// borrar — el segundo paso de RF-18, el que destruye.
func (s *Servidor) borrar(w http.ResponseWriter, r *http.Request, alm almacen.Almacen) {
	if err := r.ParseForm(); err != nil {
		s.fallo(w, r, err)
		return
	}
	if !s.exigirCSRF(w, r) {
		return
	}

	ruta, err := almacen.NuevaRuta(r.PostFormValue("ruta"))
	if err != nil || ruta.EsRaiz() {
		s.fallo(w, r, almacen.ErrRutaInvalida)
		return
	}
	// La confirmación no es implícita: el formulario manda un campo que solo
	// existe si se pasó por la pantalla de confirmación.
	if r.PostFormValue("confirmado") != "si" {
		s.fallo(w, r, almacen.ErrRutaInvalida)
		return
	}

	// Se cuenta ANTES de destruir: después ya no se puede saber qué había, y
	// sin papelera el registro es lo único que queda (RF-19).
	conteo, err := alm.Resumen(r.Context(), ruta)
	if err != nil {
		s.fallo(w, r, err)
		return
	}

	// Un árbol solo se tala si el usuario venía de la confirmación que le
	// dijo cuántos archivos contenía.
	if conteo.EsDirectorio {
		err = alm.BorrarArbol(r.Context(), ruta)
	} else {
		err = alm.Borrar(r.Context(), ruta)
	}
	if err != nil {
		s.reg.Warn("borrado FALLÓ",
			"ruta", ruta.Rel(), "remoto", origenDe(r), "error", err)
		s.fallo(w, r, err)
		return
	}

	s.contadores.borrados.Add(1)

	// RF-19. Es WARN y no INFO a propósito: sin papelera ni segunda copia,
	// un borrado es el suceso más grave que registra este servicio y debe
	// destacar al mirar el diario.
	s.reg.Warn("BORRADO",
		"ruta", ruta.Rel(),
		"archivos", conteo.Archivos,
		"directorios", conteo.Directorios,
		"bytes", conteo.Bytes,
		"remoto", origenDe(r),
		"resultado", "ok",
	)
	s.redirigir(w, r, ruta.Padre(), "Borrado: "+ruta.Nombre(), false)
}

// traducirAdministracion añade a fallo() los errores propios de esta parte.
func mensajeAdministracion(err error) (int, string, bool) {
	switch {
	case errors.Is(err, almacen.ErrNoVacio):
		return http.StatusConflict, "el directorio no está vacío", true
	case errors.Is(err, almacen.ErrDentroDeSiMismo):
		return http.StatusBadRequest, "no se puede mover una carpeta dentro de sí misma", true
	}
	return 0, "", false
}
