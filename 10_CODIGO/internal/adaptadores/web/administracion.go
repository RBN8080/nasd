package web

import (
	"errors"
	"net/http"
	"strings"

	"nasd/internal/almacen"
)

// Administración desde la web — RF-16, RF-17, RF-18, RF-19.
//
// Llega DESPUÉS de la autenticación, y no por casualidad: RN-06 lo exige.
// Entregar el borrado antes habría dejado el disco entero administrable por
// cualquiera en la LAN durante todo el intervalo.

// csrfRecibido saca el testigo de donde venga: cabecera o campo del
// formulario.
//
// NO se usa r.PostFormValue, y el motivo importa: esa función llama por
// dentro a ParseMultipartForm si el formulario no está parseado —justo lo
// que ADR-0025 prohíbe, porque vuelca a os.TempDir(), que con PrivateTmp=yes
// es RAM—. Hoy no ocurre porque todos los manejadores llaman antes a
// ParseForm, pero eso es una trampa esperando a que alguien añada uno nuevo
// sin acordarse.
//
// r.PostForm.Get() lee lo ya parseado y NUNCA dispara el análisis multipart.
//
// La cabecera «Nas-Csrf» además aporta protección por sí misma: un
// formulario de otro sitio no puede fijar cabeceras propias, así que las
// peticiones del cliente JavaScript quedan cubiertas dos veces.
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
		http.Error(w, "petición no autorizada", http.StatusForbidden)
		return false
	}
	return true
}

// RF-16 y RF-17 — renombrar y mover son la misma operación.
//
// El formulario manda la ruta actual y el destino completo. Si el destino no
// lleva barra, se interpreta como renombrar dentro de la misma carpeta.
func (s *Servidor) renombrar(w http.ResponseWriter, r *http.Request) {
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
	propuesto := strings.TrimSpace(r.PostFormValue("destino"))

	var destino almacen.RutaSegura
	if strings.Contains(propuesto, "/") {
		// Mover: el destino es una ruta completa (RF-17).
		destino, err = almacen.NuevaRuta(propuesto)
	} else {
		// Renombrar: mismo directorio, nombre nuevo (RF-16).
		destino, err = origen.Padre().Hija(propuesto)
	}
	if err != nil {
		s.fallo(w, r, err)
		return
	}

	if err := s.almacen.Renombrar(r.Context(), origen, destino); err != nil {
		// RF-19: toda operación destructiva deja constancia, también cuando falla.
		s.reg.Warn("renombrar/mover FALLÓ",
			"origen", origen.Rel(), "destino", destino.Rel(),
			"remoto", origenDe(r), "error", err)
		s.fallo(w, r, err)
		return
	}
	// RF-19: marca de tiempo la pone el registro, ruta y resultado van aquí.
	s.reg.Info("renombrado o movido",
		"origen", origen.Rel(), "destino", destino.Rel(), "remoto", origenDe(r))
	s.redirigir(w, r, origen.Padre(), "Movido a "+destino.Rel(), false)
}

// confirmarBorrado — el primer paso de RF-18.
//
// RF-18 exige que «ninguna acción destructiva se ejecute con un solo clic
// accidental». Aquí se muestra QUÉ se va a destruir —cuántos archivos y
// cuántos bytes— porque sin papelera (D-15) y con copia única (D-12) esta
// pantalla es la última oportunidad de darse cuenta.
func (s *Servidor) confirmarBorrado(w http.ResponseWriter, r *http.Request) {
	ruta, err := almacen.NuevaRuta(r.PathValue("ruta"))
	if err != nil || ruta.EsRaiz() {
		s.fallo(w, r, almacen.ErrRutaInvalida)
		return
	}
	conteo, err := s.almacen.Resumen(r.Context(), ruta)
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
func (s *Servidor) borrar(w http.ResponseWriter, r *http.Request) {
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
	conteo, err := s.almacen.Resumen(r.Context(), ruta)
	if err != nil {
		s.fallo(w, r, err)
		return
	}

	// Un árbol solo se tala si el usuario venía de la confirmación que le
	// dijo cuántos archivos contenía.
	if conteo.EsDirectorio {
		err = s.almacen.BorrarArbol(r.Context(), ruta)
	} else {
		err = s.almacen.Borrar(r.Context(), ruta)
	}
	if err != nil {
		s.reg.Warn("borrado FALLÓ",
			"ruta", ruta.Rel(), "remoto", origenDe(r), "error", err)
		s.fallo(w, r, err)
		return
	}

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
