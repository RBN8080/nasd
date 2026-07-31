package web

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"nasd/internal/autenticacion"
)

// Autenticación de la web — RF-15, D-14 / ADR-0021.
//
// RN-06 es vinculante: la autenticación se entrega ANTES que cualquier
// operación destructiva. Este archivo es ese «antes».

const (
	nombreCookie = "nas_sesion"

	// Iteraciones de PBKDF2. Recomendación OWASP vigente para HMAC-SHA256.
	//
	// MEDIDO EN EL NODO (2026-07-31): 3 232 ms por verificación en el A53 del
	// 3B+, que no tiene aceleración criptográfica. Es lento a propósito —de
	// eso trata un KDF— y solo se paga al iniciar sesión.
	//
	// No se baja el coste para «ir más rápido»: el flanco que abre no es el
	// hash sino la saturación de CPU, y eso se ataca limitando la tasa
	// (limitadorAcceso), no debilitando la derivación.
	IteracionesPBKDF2 = 600000

	// Ventana y tope de intentos fallidos por origen antes de rechazar.
	// Con 3.2 s por verificación, 5 intentos son ~16 s de CPU: suficiente
	// para un despiste humano, insuficiente para un ataque. [R]
	maxIntentosFallidos = 5
	ventanaIntentos     = 5 * time.Minute
)

// limitadorAcceso frena los intentos de inicio de sesión.
//
// Dos controles, y hacen cosas distintas:
//
//  1. El mutex SERIALIZA las verificaciones. Una sola derivación a la vez,
//     así que por muchos clientes que empujen, el gasto queda acotado a un
//     núcleo. Importa porque el nodo ya opera con el límite térmico blando
//     activo (RNF-11) y las cuatro CPU al 100 % lo empeorarían.
//
//  2. El contador por origen corta la repetición. Sin él, serializar solo
//     convierte la saturación en una cola infinita.
type limitadorAcceso struct {
	verificar sync.Mutex

	mu       sync.Mutex
	fallidos map[string]*intentos
}

type intentos struct {
	n     int
	desde time.Time
}

func nuevoLimitador() *limitadorAcceso {
	return &limitadorAcceso{fallidos: make(map[string]*intentos)}
}

// permitido indica si ese origen puede intentarlo, y cuánto esperar si no.
func (l *limitadorAcceso) permitido(origen string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	it, ok := l.fallidos[origen]
	if !ok {
		return true, 0
	}
	if time.Since(it.desde) > ventanaIntentos {
		delete(l.fallidos, origen)
		return true, 0
	}
	if it.n >= maxIntentosFallidos {
		return false, ventanaIntentos - time.Since(it.desde)
	}
	return true, 0
}

func (l *limitadorAcceso) fallo(origen string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	it, ok := l.fallidos[origen]
	if !ok || time.Since(it.desde) > ventanaIntentos {
		l.fallidos[origen] = &intentos{n: 1, desde: time.Now()}
		return
	}
	it.n++
}

func (l *limitadorAcceso) acierto(origen string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.fallidos, origen)
}

// purgar limpia los contadores vencidos. Sin esto el mapa crecería con cada
// origen que falle alguna vez y jamás menguaría.
func (l *limitadorAcceso) purgar() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for k, it := range l.fallidos {
		if time.Since(it.desde) > ventanaIntentos {
			delete(l.fallidos, k)
			n++
		}
	}
	return n
}

func origenDe(r *http.Request) string {
	if h, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return h
	}
	return r.RemoteAddr
}

// exigirSesion protege todo lo que no sea el propio formulario de acceso.
//
// RF-15 pide literalmente que «sin sesión válida, toda ruta distinta del
// formulario de acceso responde 401 o 403». Se cumple al pie de la letra:
// se responde 401 — no una redirección 303, que sería más cómoda pero
// incumpliría el criterio— y el CUERPO del 401 lleva el formulario, así que
// el navegador muestra algo usable sin falsear el código de estado.
func (s *Servidor) exigirSesion(siguiente http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(nombreCookie)
		if err == nil && s.sesiones.Valida(c.Value) {
			siguiente.ServeHTTP(w, r)
			return
		}
		s.pedirAcceso(w, r, "")
	})
}

func (s *Servidor) pedirAcceso(w http.ResponseWriter, r *http.Request, aviso string) {
	w.Header().Set("Cache-Control", "no-store")
	if !aceptaHTML(r) {
		http.Error(w, "no autenticado", http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusUnauthorized)
	if err := s.plantillas.ExecuteTemplate(w, "acceso.html", map[string]string{"Aviso": aviso}); err != nil {
		s.reg.Error("render del formulario de acceso", "error", err)
	}
}

// aceptaHTML distingue a un navegador de un cliente de la API.
//
// No cambia el código de estado —siempre es 401— solo si el cuerpo lleva el
// formulario o un texto plano. Un navegador anuncia text/html; el fetch del
// cliente tus y curl mandan "*/*".
func aceptaHTML(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "text/html")
}

func (s *Servidor) mostrarAcceso(w http.ResponseWriter, r *http.Request) {
	// Si ya hay sesión, no tiene sentido pedirla otra vez.
	if c, err := r.Cookie(nombreCookie); err == nil && s.sesiones.Valida(c.Value) {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.plantillas.ExecuteTemplate(w, "acceso.html", map[string]string{}); err != nil {
		s.reg.Error("render del formulario de acceso", "error", err)
	}
}

func (s *Servidor) procesarAcceso(w http.ResponseWriter, r *http.Request) {
	origen := origenDe(r)

	if ok, espera := s.limitador.permitido(origen); !ok {
		s.reg.Warn("acceso bloqueado por intentos repetidos",
			"origen", origen, "espera_s", int(espera.Seconds()))
		w.Header().Set("Retry-After", "300")
		s.pedirAcceso(w, r, "Demasiados intentos fallidos. Espere unos minutos.")
		return
	}
	if err := r.ParseForm(); err != nil {
		s.pedirAcceso(w, r, "Petición inválida.")
		return
	}
	clave := r.PostFormValue("clave")

	// Serializado: una derivación a la vez, pase lo que pase.
	s.limitador.verificar.Lock()
	valida := autenticacion.Verificar(s.credencial, clave)
	s.limitador.verificar.Unlock()

	if !valida {
		s.limitador.fallo(origen)
		s.contadores.accesosFallidos.Add(1)
		// NUNCA se registra la contraseña ni parte de ella (04_SEGURIDAD §6).
		s.reg.Warn("intento de acceso fallido", "origen", origen)
		s.pedirAcceso(w, r, "Contraseña incorrecta.")
		return
	}

	testigo, err := s.sesiones.Abrir()
	if err != nil {
		s.reg.Error("no se pudo abrir la sesión", "error", err)
		http.Error(w, "error interno", http.StatusInternalServerError)
		return
	}
	s.limitador.acierto(origen)
	s.reg.Info("sesión iniciada", "origen", origen, "sesiones_abiertas", s.sesiones.Abiertas())

	http.SetCookie(w, &http.Cookie{
		Name:     nombreCookie,
		Value:    testigo,
		Path:     "/",
		HttpOnly: true,                 // inalcanzable desde JavaScript
		SameSite: http.SameSiteLaxMode, // frena el CSRF entre sitios
		MaxAge:   int(s.duracionSesion.Seconds()),
		// Secure NO se pone: ADR-0018 deja la v1 sin TLS, y marcarla Secure
		// impediría que el navegador la enviara por HTTP. Es la consecuencia
		// directa de aquella decisión, no un descuido: sin TLS la cookie
		// viaja en claro por la LAN.
	})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Servidor) salir(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(nombreCookie); err == nil {
		s.sesiones.Cerrar(c.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name: nombreCookie, Value: "", Path: "/", HttpOnly: true, MaxAge: -1,
	})
	s.reg.Info("sesión cerrada", "origen", origenDe(r))
	http.Redirect(w, r, "/acceso", http.StatusSeeOther)
}
