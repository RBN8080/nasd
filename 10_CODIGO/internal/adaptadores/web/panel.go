package web

import (
	"net/http"
	"strings"

	"nasd/internal/autenticacion"
)

// Panel de administración — P-4, etapa 2 (ADR-0055).
//
// SOLO EL SUPERUSUARIO, con la MISMA envoltura que /estado: soloSuperusuario
// decide en el servidor, y la barra solo esconde el botón por cortesía (ver
// el comentario junto a esa función en sesion.go).
//
// NO VA ENVUELTO EN conAlmacen. Ni el alta ni la baja necesitan el almacén
// ACOTADO A UN USUARIO que esa envoltura entrega —la carpeta se crea sola,
// la primera vez que alguien entra, en fsposix.Almacen.ParaUsuario—; la baja
// SÍ toca disco desde la corrección del 06/08 (promoverUsuario, más abajo),
// pero por una puerta propia y no por el almacén de la sesión, igual que
// verEstado tampoco necesita uno acotado.

// homeUsersNombre es el nombre de la carpeta contenedora de usuarios, tal
// cual lo fija fsposix.SubHomeUsers — repetido aquí y no importado, porque
// este paquete no depende de fsposix (ADR-0055): la traducción de nombres a
// rutas de disco es cosa de la raíz de composición, no del adaptador web. La
// plantilla del listado ya repetía este mismo literal en el atajo «Usuarios».
const homeUsersNombre = "homeUsers"

// filaUsuario es lo que ve el panel de cada cuenta del registro.
type filaUsuario struct {
	Nombre string
	// Activo — sesión vigente ahora mismo (autenticacion.Sesiones,
	// etapa 2). Es el ÚNICO dato en vivo de esta primera versión del panel:
	// se decidió así porque cuesta un mapa en memoria, no un recorrido de
	// disco por cuenta.
	Activo bool
}

type vistaAdministracion struct {
	Usuarios []filaUsuario
	Mensaje  string
	EsError  bool
	Csrf     string
}

func (s *Servidor) verAdministracion(w http.ResponseWriter, r *http.Request) {
	activos := s.sesiones.ActivosPorUsuario()
	v := vistaAdministracion{
		Mensaje: r.URL.Query().Get("msg"),
		EsError: r.URL.Query().Get("err") != "",
		Csrf:    s.csrfDe(r),
	}
	for _, u := range s.usuarios.Lista() {
		v.Usuarios = append(v.Usuarios, filaUsuario{Nombre: u.Nombre, Activo: activos[u.Nombre]})
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Igual que /estado (ADR-0034): quién tiene sesión abierta ahora mismo
	// no es un dato para cachear ni un momento.
	w.Header().Set("Cache-Control", "no-store")
	if err := s.plantillas.ExecuteTemplate(w, "administracion.html", v); err != nil {
		s.reg.Error("render del panel de administración", "error", err)
	}
}

func (s *Servidor) redirigirAdministracion(w http.ResponseWriter, r *http.Request, msg string, esError bool) {
	q := "?msg=" + escaparConsulta(msg)
	if esError {
		q += "&err=1"
	}
	http.Redirect(w, r, "/administracion"+q, http.StatusSeeOther)
}

// altaUsuario da de alta una cuenta desde la web — la misma operación que
// «nasd --crear-usuario», con las mismas reglas: Registro.Alta ya exige
// NombreValido y LongitudMinimaUsuario, así que no se repiten aquí.
func (s *Servidor) altaUsuario(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.fallo(w, r, err)
		return
	}
	if !s.exigirCSRF(w, r) {
		return
	}
	nombre := strings.TrimSpace(r.PostFormValue("nombre"))
	clave := r.PostFormValue("clave")

	if err := s.usuarios.Alta(nombre, clave); err != nil {
		s.reg.Warn("alta de usuario desde el panel FALLÓ",
			"nombre", nombre, "remoto", origenDe(r), "error", err)
		s.redirigirAdministracion(w, r, err.Error(), true)
		return
	}
	s.reg.Info("usuario dado de alta desde el panel", "nombre", nombre, "remoto", origenDe(r))
	s.redirigirAdministracion(w, r, "Cuenta creada: "+nombre+". Su carpeta se prepara al entrar por primera vez.", false)
}

// confirmarBaja — primer paso: quién se va, antes de pedir la contraseña.
func (s *Servidor) confirmarBaja(w http.ResponseWriter, r *http.Request) {
	nombre := r.PathValue("nombre")
	if _, existe := s.usuarios.Buscar(nombre); !existe {
		s.redirigirAdministracion(w, r, "no existe ese usuario", true)
		return
	}
	aviso := ""
	if r.URL.Query().Has("err") {
		aviso = avisoAccesoFallido
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	datos := map[string]any{"Nombre": nombre, "Csrf": s.csrfDe(r), "Aviso": aviso}
	if err := s.plantillas.ExecuteTemplate(w, "baja.html", datos); err != nil {
		s.reg.Error("render de la confirmación de baja", "error", err)
	}
}

// bajaUsuario — segundo paso: exige la contraseña del SUPERUSUARIO otra vez.
//
// NO ES LA CONTRASEÑA DE LA CUENTA QUE SE VA — es una repetición de la
// propia, pedida por el responsable el 06/08. Una sesión abierta demuestra
// que alguien entró una vez, no que sigue siendo él quien está delante del
// aparato ahora: es la barrera contra una sesión de superusuario dejada
// abierta y encontrada por otro, para la única operación de este panel que
// además de administrar cuentas decide qué pasa con el acceso de alguien.
//
// Reutiliza verificarAcceso tal cual, la MISMA función y el MISMO coste de
// 3.6 s que ya paga /acceso — no hace falta un límite de intentos aparte:
// verificarAcceso ya serializa la derivación detrás de s.limitador.verificar,
// así que esta ruta no puede convertirse en un segundo amplificador de CPU
// aunque no tenga su propio contador por origen. Y para llegar aquí hace
// falta YA tener una sesión de superusuario — soloSuperusuario la exige —,
// que es una barrera que /acceso no tiene.
func (s *Servidor) bajaUsuario(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.fallo(w, r, err)
		return
	}
	if !s.exigirCSRF(w, r) {
		return
	}
	nombre := strings.TrimSpace(r.PostFormValue("nombre"))
	clave := r.PostFormValue("clave")

	valida, _ := s.verificarAcceso(autenticacion.NombreSuperusuario, clave)
	if !valida {
		s.reg.Warn("baja de usuario: confirmación de contraseña incorrecta",
			"nombre", nombre, "remoto", origenDe(r))
		http.Redirect(w, r, "/administracion/baja/"+escaparURL(nombre)+"?err=1", http.StatusSeeOther)
		return
	}

	// LA CARPETA SE PROMUEVE ANTES DE TOCAR EL REGISTRO — corrección del
	// 06/08. Reportado en uso real: la cuenta desaparecía de la lista, pero
	// su carpeta seguía dentro de homeUsers/, que el propio panel ya no
	// enseña. Si no se puede promover —un choque de nombre en la raíz, por
	// ejemplo—, la baja se aborta ENTERA y la cuenta sigue activa: lo
	// contrario dejaría una carpeta huérfana en un sitio invisible, que es
	// exactamente el defecto que esto corrige.
	if err := s.promoverUsuario(nombre); err != nil {
		s.reg.Warn("baja de usuario: no se pudo promover su carpeta",
			"nombre", nombre, "remoto", origenDe(r), "error", err)
		s.redirigirAdministracion(w, r, "no se pudo completar la baja de «"+nombre+"»: "+err.Error(), true)
		return
	}
	if err := s.usuarios.Baja(nombre); err != nil {
		s.redirigirAdministracion(w, r, err.Error(), true)
		return
	}
	s.reg.Warn("USUARIO DADO DE BAJA", "nombre", nombre, "remoto", origenDe(r))
	s.redirigirAdministracion(w, r,
		"Cuenta dada de baja: "+nombre+". Su carpeta no se ha destruido: ahora vive en la raíz, junto a homeUsers.", false)
}
