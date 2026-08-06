package web

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"nasd/internal/almacen"
)

// RF-17 — mover eligiendo la carpeta, no tecleando una ruta.
//
// EL DEFECTO QUE ESTO CORRIGE, reportado en uso real el 2026-08-06.
// Hasta aquí, mover y renombrar compartían extremo y se distinguían por una
// regla que la interfaz nunca enseñó: si el texto escrito llevaba una barra
// era una ruta de destino, y si no, un nombre nuevo en la misma carpeta. El
// responsable hizo lo natural —escribir el NOMBRE DE LA CARPETA a la que
// quería mover— y el servidor lo leyó como «renombra esto a "Videos"». Como
// «Videos» ya existía, RF-23 lo paró con «ya existe un elemento con ese
// nombre; no se sobrescribe».
//
// Conviene ver por qué eso era tan difícil de diagnosticar desde dentro: el
// almacén hizo lo correcto, el mensaje era cierto, y ninguna prueba fallaba.
// Lo que estaba mal era que la interfaz aceptaba dos intenciones distintas
// por el mismo campo y adivinaba cuál era mirando la puntuación.
//
// La cura no es un mensaje mejor ni un ejemplo más claro en el marcador de
// posición: es que el destino DEJE DE TECLEARSE. Aquí se elige navegando, y
// entonces no queda nada que interpretar mal.
//
// SIN JavaScript, igual que el resto de la administración (ADR-0027): son
// páginas servidas por el servidor. El responsable pidió «una ventana o
// vista», y la vista cumple sin colgar de un guion una operación que mueve
// datos que no tienen segunda copia (D-12).

// escaparConsulta escapa un valor que va en la CADENA DE CONSULTA.
//
// No sirve escaparURL aquí, y la diferencia muerde: es url.PathEscape, que
// deja pasar «&» y «=» por ser legales dentro de un segmento de ruta. Un
// nombre de carpeta como «Fotos & vídeos» partiría el parámetro en dos y la
// vista abriría otra carpeta —o ninguna—.
func escaparConsulta(s string) string { return url.QueryEscape(s) }

// urlDeMover es la única forma de construir el enlace a la vista de mover.
// «origen» es lo que se mueve; «aqui», la carpeta que se está mirando.
func urlDeMover(origen, aqui almacen.RutaSegura) string {
	u := "/mover/" + escaparRutaURL(origen.Rel())
	if !aqui.EsRaiz() {
		u += "?a=" + escaparConsulta(aqui.Rel())
	}
	return u
}

type vistaMover struct {
	// Origen es lo que se está moviendo. Tras un movimiento correcto ya
	// apunta al sitio nuevo, que es lo que deja «Cerrar» aterrizando en la
	// carpeta destino sin ninguna rama: el padre del origen ES el destino.
	Origen almacen.RutaSegura
	// Aqui es la carpeta que se está examinando ahora mismo.
	Aqui  almacen.RutaSegura
	Migas []almacen.RutaSegura
	// Carpetas son las ÚNICAS entradas que se listan: el destino de un
	// movimiento solo puede ser una carpeta, así que enseñar archivos sería
	// enseñar cosas que no se pueden pulsar.
	Carpetas []almacen.Entrada
	Truncado bool
	Csrf     string
	// Hecho enciende el aviso verde de operación completada.
	Hecho bool
	// Error explica, DENTRO de la misma vista, por qué no se movió. Sacar al
	// usuario a una página de error le haría perder la carpeta que llevaba
	// navegada.
	Error string
}

// verMover dibuja el navegador de carpetas.
func (s *Servidor) verMover(w http.ResponseWriter, r *http.Request) {
	origen, err := almacen.NuevaRuta(r.PathValue("ruta"))
	if err != nil || origen.EsRaiz() {
		s.fallo(w, r, almacen.ErrRutaInvalida)
		return
	}

	// Por omisión se empieza a mirar donde vive el elemento: mover casi
	// siempre es «a la carpeta de al lado», y así el primer clic ya sirve.
	aqui := origen.Padre()
	if a := r.URL.Query().Get("a"); a != "" {
		if aqui, err = almacen.NuevaRuta(a); err != nil {
			s.fallo(w, r, err)
			return
		}
	}

	v := vistaMover{
		Origen: origen,
		Aqui:   aqui,
		Migas:  aqui.Ascendencia(),
		Csrf:   s.csrfDe(r),
		Hecho:  r.URL.Query().Get("ok") != "",
		Error:  r.URL.Query().Get("err"),
	}

	n := 0
	for e, err := range s.almacen.Listar(r.Context(), aqui) {
		if err != nil {
			s.fallo(w, r, err)
			return
		}
		if n >= maxEntradasPorPagina {
			v.Truncado = true
			break
		}
		// Solo carpetas, y las ocultas siguen ocultas por el mismo motivo
		// que en el listado: iOS deja «.sb-XXXX» al copiar.
		if !e.EsDirectori || strings.HasPrefix(e.Nombre, ".") {
			continue
		}
		// Una carpeta no puede mudarse dentro de sí misma; el almacén lo
		// rechaza con ErrDentroDeSiMismo. Ofrecer ese camino sería ofrecer
		// un callejón sin salida, así que se omite la propia carpeta —y con
		// ella todo su subárbol, que deja de ser alcanzable desde aquí—.
		if e.Ruta.Rel() == origen.Rel() {
			continue
		}
		v.Carpetas = append(v.Carpetas, e)
		n++
	}
	ordenar(v.Carpetas)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Mismo motivo que el listado: esta vista ES el sistema de archivos, y
	// una copia cacheada ofrecería mover a carpetas que ya no existen.
	w.Header().Set("Cache-Control", "no-store, must-revalidate")
	if err := s.plantillas.ExecuteTemplate(w, "mover.html", v); err != nil {
		s.reg.Error("render de la vista de mover", "error", err)
	}
}

// mover ejecuta el movimiento: RF-17.
//
// El nombre NO se pide ni se acepta del formulario: se conserva el del
// origen. Cambiar el nombre es renombrar (RF-16) y tiene su propio gesto;
// mezclar las dos cosas otra vez sería reponer el defecto que esta vista
// existe para corregir.
func (s *Servidor) mover(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.fallo(w, r, err)
		return
	}
	if !s.exigirCSRF(w, r) {
		return
	}

	origen, err := almacen.NuevaRuta(r.PostFormValue("origen"))
	if err != nil || origen.EsRaiz() {
		s.fallo(w, r, almacen.ErrRutaInvalida)
		return
	}
	carpeta, err := almacen.NuevaRuta(r.PostFormValue("carpeta"))
	if err != nil {
		s.fallo(w, r, err)
		return
	}
	destino, err := carpeta.Hija(origen.Nombre())
	if err != nil {
		s.fallo(w, r, err)
		return
	}

	if err := s.almacen.Renombrar(r.Context(), origen, destino); err != nil {
		// RF-19: toda operación destructiva deja constancia, también al fallar.
		s.reg.Warn("mover FALLÓ", "origen", origen.Rel(),
			"destino", destino.Rel(), "remoto", origenDe(r), "error", err)
		// El fallo se cuenta DENTRO de la vista y no en una página aparte:
		// «ya existe algo con ese nombre ahí» es información para seguir
		// eligiendo carpeta, no un callejón sin salida.
		_, motivo, conocido := mensajeAdministracion(err)
		if !conocido {
			if motivo, conocido = mensajeDeMover(err); !conocido {
				s.fallo(w, r, err)
				return
			}
		}
		http.Redirect(w, r, urlDeMover(origen, carpeta)+
			separadorConsulta(carpeta)+"err="+escaparConsulta(motivo),
			http.StatusSeeOther)
		return
	}

	// RF-19: la marca de tiempo la pone el registro; ruta y resultado, aquí.
	s.reg.Info("movido", "origen", origen.Rel(),
		"destino", destino.Rel(), "remoto", origenDe(r))

	// Se vuelve a la MISMA vista, ya apuntando al sitio nuevo, para que el
	// aviso verde se vea donde el responsable lo pidió y sea él quien cierre.
	http.Redirect(w, r, urlDeMover(destino, carpeta)+
		separadorConsulta(carpeta)+"ok=1", http.StatusSeeOther)
}

// separadorConsulta evita el «?a=x?ok=1» que saldría de concatenar a ciegas:
// urlDeMover ya deja un «?» puesto salvo cuando la carpeta es la raíz.
func separadorConsulta(carpeta almacen.RutaSegura) string {
	if carpeta.EsRaiz() {
		return "?"
	}
	return "&"
}

// mensajeDeMover traduce los errores que el usuario puede corregir SIN salir
// de la vista. Los demás siguen su camino normal por fallo().
func mensajeDeMover(err error) (string, bool) {
	if errors.Is(err, almacen.ErrYaExiste) {
		return "Esa carpeta ya tiene un elemento con ese nombre. No se sobrescribe: elija otra carpeta.", true
	}
	return "", false
}
