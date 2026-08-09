package web

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"nasd/internal/almacen"
	"nasd/internal/autenticacion"
	"nasd/internal/metricas"
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
	// etapa 2). Es el ÚNICO dato EN VIVO del panel: se decidió así porque
	// cuesta un mapa en memoria, no un recorrido de disco por cuenta.
	Activo bool
	// UsoDisco y Medido son BAJO DEMANDA — P-4, etapa 3 — y por eso van
	// separados de Activo en vez de mezclados en el mismo texto: lo que hay
	// aquí puede tener minutos, días o no existir todavía, y la plantilla
	// lo dice explícitamente en vez de dar a entender que es tan fresco
	// como el resto de la fila. ADR-0017: el tamaño y la variación ya
	// vienen formados —ver formatoUsoDisco—, la plantilla solo compone.
	UsoDisco string
	// Medido es el momento de esa medición; cero si la cuenta nunca se ha
	// refrescado. Se deja como time.Time y no como texto para poder seguir
	// usando la función de plantilla «fecha», la misma que ya usa
	// listado.html.
	Medido time.Time
}

// textoSesionActiva es lo que dice la pastilla cuando hay sesión abierta.
//
// ES UNA CONSTANTE Y NO UN LITERAL EN LA PLANTILLA porque desde P-7 hay DOS
// cosas que pintan esa pastilla: el render del servidor al cargar la página y
// el flujo en vivo que la actualiza después. Con el texto escrito en dos
// sitios, cambiar uno y olvidar el otro haría que la pastilla cambiara de
// palabra sola al primer tic, sin que nada fallara a gritos.
const textoSesionActiva = "activa"

// Sesion y Veredicto son lo que la PLANTILLA pinta, y salen de las mismas
// constantes que marcoDeCuentas mete en el flujo. Ese es todo su motivo: que
// la página cargada y el marco empujado no puedan decir cosas distintas.
func (f filaUsuario) Sesion() string {
	if !f.Activo {
		return ""
	}
	return textoSesionActiva
}

func (f filaUsuario) Veredicto() veredicto {
	if !f.Activo {
		return ""
	}
	return vOK
}

type vistaAdministracion struct {
	Usuarios []filaUsuario
	Mensaje  string
	EsError  bool
	Csrf     string
}

// Sesión en vivo — P-7, ADR-0056.
//
// La pastilla de «Sesión» decía la verdad SOLO en el instante de cargar la
// página: quien cerrara sesión seguía figurando como activo hasta que alguien
// recargara a mano. Se empuja igual que /estado, por el mismo muestreador
// genérico y al mismo ritmo.
//
// LO QUE NO VIAJA AQUÍ, Y ES DELIBERADO: el uso de disco. Es bajo demanda por
// decisión de P-4 etapa 3 —recorre el árbol de cada cuenta— y tiene su propio
// botón. Meterlo en el flujo desharía esa decisión en silencio y pondría el
// bus USB, que es el recurso escaso del nodo (RES-02), a trabajar cuatro veces
// por segundo por tener una pestaña abierta.

// filaCuenta es una fila del flujo del panel. Copia la forma de filaViva —y no
// un simple booleano— porque ADR-0017 pone el render en el servidor: el texto
// de la pastilla y la clase de la fila se componen AQUÍ, exactamente como los
// pinta la plantilla, para que la página con JavaScript y sin él no puedan
// decir cosas distintas.
type filaCuenta struct {
	// Nombre es el ancla en el DOM (data-usuario), no un rótulo para pintar:
	// el nombre ya está escrito en la fila y no cambia nunca.
	Nombre    string    `json:"nombre"`
	Texto     string    `json:"texto"`
	Veredicto veredicto `json:"veredicto,omitempty"`
}

// marcoCuentas es lo que viaja en cada evento del flujo del panel. Van SIEMPRE
// todas las filas y no solo las que cambiaron, por el mismo motivo que
// marcoVivo: abajo se descarta el marco de un espectador lento, y con un
// protocolo de diferencias ese espectador se quedaría con un valor viejo para
// siempre.
type marcoCuentas struct {
	Cuentas []filaCuenta `json:"cuentas"`
}

// abrirLectorCuentas entrega el lector del flujo del panel. A diferencia del de
// /estado no guarda nada entre marcos —no hay ninguna resta que hacer—, así que
// devuelve el método tal cual.
func (s *Servidor) abrirLectorCuentas() func() marcoCuentas {
	return s.marcoDeCuentas
}

// marcoDeCuentas compone quién tiene sesión abierta AHORA.
//
// Las dos lecturas son de memoria pura —un cerrojo y una copia; ni un stat ni
// un byte de disco—, que es lo que permite emitir a 250 ms sin coste apreciable.
// ActivosPorUsuario ya descarta las sesiones caducadas, así que una que expira
// se apaga sola en el tic siguiente sin depender del barrido de Purgar().
func (s *Servidor) marcoDeCuentas() marcoCuentas {
	activos := s.sesiones.ActivosPorUsuario()
	lista := s.usuarios.Lista()

	m := marcoCuentas{Cuentas: make([]filaCuenta, 0, len(lista))}
	for _, u := range lista {
		fila := filaCuenta{Nombre: u.Nombre}
		if activos[u.Nombre] {
			fila.Texto = textoSesionActiva
			fila.Veredicto = vOK
		}
		m.Cuentas = append(m.Cuentas, fila)
	}
	return m
}

func (s *Servidor) verAdministracion(w http.ResponseWriter, r *http.Request) {
	activos := s.sesiones.ActivosPorUsuario()
	// Todas() es una lectura en memoria —lo caro ya ocurrió, si acaso, en el
	// último «Refrescar métricas»—: cargar el panel nunca recorre disco.
	medidas := s.metricas.Todas()
	v := vistaAdministracion{
		Mensaje: r.URL.Query().Get("msg"),
		EsError: r.URL.Query().Get("err") != "",
		Csrf:    s.csrfDe(r),
	}
	for _, u := range s.usuarios.Lista() {
		fila := filaUsuario{Nombre: u.Nombre, Activo: activos[u.Nombre]}
		if m, ok := medidas[u.Nombre]; ok {
			fila.UsoDisco = formatoUsoDisco(m)
			fila.Medido = m.Momento
		}
		v.Usuarios = append(v.Usuarios, fila)
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

// formatoUsoDisco compone el tamaño y la variación con signo desde la
// última medida, en una sola cadena — ADR-0017: la decisión de formato es
// del servidor, la plantilla solo la pega junto a la fecha (fila.Medido).
func formatoUsoDisco(m metricas.Medida) string {
	variacion := "primera medición"
	if m.Variacion != nil {
		switch d := *m.Variacion; {
		case d == 0:
			variacion = "sin cambios"
		case d > 0:
			variacion = "+" + legibleBytes(uint64(d))
		default:
			variacion = "−" + legibleBytes(uint64(-d))
		}
	}
	return legibleBytes(uint64(m.Bytes)) + " (" + variacion + ")"
}

// refrescarMetricas mide el uso de disco de cada cuenta y publica el lote
// entero de una vez — P-4, etapa 3.
//
// ES LA OPERACIÓN CARA DEL PANEL A PROPÓSITO: recorrer el árbol de hasta
// maxUsuarios cuentas no es gratis (Resumen es recursivo, sin caché — ver
// fsposix/administracion.go), así que ocurre SOLO al pulsar el botón y
// nunca al servir /administracion. Es la misma razón por la que
// evaluarLentos, en estado.go, no vive en el flujo en vivo.
func (s *Servidor) refrescarMetricas(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.fallo(w, r, err)
		return
	}
	if !s.exigirCSRF(w, r) {
		return
	}

	nuevas := make(map[string]int64)
	for _, u := range s.usuarios.Lista() {
		alm, err := s.abrirAlmacen(u.Nombre)
		if err != nil {
			s.reg.Warn("refrescar métricas: no se pudo abrir el almacén de la cuenta",
				"nombre", u.Nombre, "error", err)
			continue
		}
		c, err := alm.Resumen(r.Context(), almacen.Raiz())
		if err != nil && !errors.Is(err, almacen.ErrNoExiste) {
			s.reg.Warn("refrescar métricas: no se pudo medir la cuenta",
				"nombre", u.Nombre, "error", err)
			continue
		}
		// ErrNoExiste: la cuenta nunca entró y ParaUsuario nunca creó su
		// carpeta (fsposix/almacen.go) — no es un fallo, son 0 bytes.
		nuevas[u.Nombre] = c.Bytes
	}

	if err := s.metricas.Actualizar(nuevas, time.Now()); err != nil {
		s.reg.Error("refrescar métricas: no se pudo publicar el registro", "error", err)
		s.redirigirAdministracion(w, r, "no se pudieron guardar las métricas: "+err.Error(), true)
		return
	}
	s.reg.Info("métricas de uso de disco refrescadas", "cuentas", len(nuevas), "remoto", origenDe(r))
	s.redirigirAdministracion(w, r, "Métricas actualizadas.", false)
}
