package web

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"nasd/internal/autenticacion"
	"nasd/internal/seguridad"
)

// Endurecimiento de /acceso — ADR-0072 §10, RF-39.
//
// La afirmación que estas pruebas sostienen es una sola:
//
//	Antes de hacer trabajo caro, la petición tiene que demostrar que merece
//	llegar a él.
//
// Y su contraria, que es igual de importante y por eso también está probada:
// las peticiones que SÍ lo merecen siguen costando lo mismo existan o no, para
// que la defensa de enumeración de usuarios no se pierda por el camino.

// postAcceso manda el formulario tal como lo mandaría un navegador. tipo vacío
// significa «el que usa el formulario de verdad».
func postAcceso(t *testing.T, s *Servidor, cuerpo, tipo, origen string) *httptest.ResponseRecorder {
	t.Helper()
	if tipo == "" {
		tipo = "application/x-www-form-urlencoded"
	}
	r := httptest.NewRequest("POST", "/acceso", strings.NewReader(cuerpo))
	r.Header.Set("Content-Type", tipo)
	r.Header.Set("Accept", "text/html")
	if origen != "" {
		r.RemoteAddr = origen
	}
	w := httptest.NewRecorder()
	s.Rutas().ServeHTTP(w, r)
	return w
}

func credenciales(usuario, clave string) string {
	return url.Values{"usuario": {usuario}, "clave": {clave}}.Encode()
}

// L1 — el acceso válido sigue funcionando. Es la primera prueba del archivo a
// propósito: todo lo demás de aquí son formas de decir que no, y una defensa
// que también le dice que no al dueño no es una defensa.
func TestElAccesoValidoSigueFuncionando(t *testing.T) {
	s := servidorConAuth(t)
	w := postAcceso(t, s, credenciales(autenticacion.NombreSuperusuario, claveDePrueba), "", "")

	if w.Code != http.StatusSeeOther {
		t.Fatalf("acceso válido -> %d; se esperaba 303", w.Code)
	}
	if destino := w.Header().Get("Location"); destino != "/" {
		t.Errorf("tras entrar se redirige a %q; se esperaba «/»", destino)
	}
	var sesion, recuerdo *http.Cookie
	for _, c := range w.Result().Cookies() {
		switch c.Name {
		case nombreCookie:
			sesion = c
		case cookieUsuario:
			recuerdo = c
		}
	}
	if sesion == nil || sesion.Value == "" {
		t.Fatal("no se entregó cookie de sesión")
	}
	if !sesion.HttpOnly || sesion.SameSite != http.SameSiteLaxMode {
		t.Error("la cookie de sesión perdió HttpOnly o SameSite")
	}
	if recuerdo == nil || recuerdo.Value != autenticacion.NombreSuperusuario {
		t.Error("el aparato no recordó el nombre")
	}
	if !s.sesiones.Valida(sesion.Value) {
		t.Error("la sesión entregada no es válida")
	}
}

// L2, L5, L6, L7, L8, L10 — LOS OCHO FALLOS SON EL MISMO FALLO DESDE FUERA.
//
// Cada fila es un camino distinto por dentro. Lo que se comprueba es que
// ninguno se pueda separar de los demás mirando la respuesta: ni por el
// estado, ni por las cabeceras, ni por el cuerpo.
func TestTodosLosFallosDeAccesoSonElMismoFalloDesdeFuera(t *testing.T) {
	// Cada caso construye su propio servidor: el límite por origen y el
	// presupuesto criptográfico son estado compartido, y compartirlo entre
	// casos haría que el orden de la tabla cambiara el resultado.
	casos := []struct {
		nombre   string
		preparar func(t *testing.T, s *Servidor)
		cuerpo   string
		tipo     string
		esperado seguridad.Motivo
	}{
		{
			nombre:   "contraseña incorrecta",
			cuerpo:   credenciales(autenticacion.NombreSuperusuario, "esta-no-es-la-buena"),
			esperado: seguridad.CredencialIncorrecta,
		},
		{
			nombre:   "usuario válido que no existe",
			cuerpo:   credenciales("fantasma", "una-clave-cualquiera"),
			esperado: seguridad.CredencialIncorrecta,
		},
		{
			nombre:   "usuario sintácticamente imposible",
			cuerpo:   credenciales("A/B/../etc", "una-clave-cualquiera"),
			esperado: seguridad.PeticionMalformada,
		},
		{
			nombre:   "sin contraseña",
			cuerpo:   credenciales(autenticacion.NombreSuperusuario, ""),
			esperado: seguridad.PeticionMalformada,
		},
		{
			nombre: "cuerpo demasiado grande",
			// Cuatro veces el tope, y con la contraseña BUENA: si el tamaño no
			// cortara antes, este caso entraría.
			cuerpo: credenciales(autenticacion.NombreSuperusuario,
				claveDePrueba+strings.Repeat("x", 4*topeCuerpoAcceso)),
			esperado: seguridad.CuerpoExcesivo,
		},
		{
			nombre:   "Content-Type que el formulario no usa",
			cuerpo:   `{"usuario":"nas","clave":"lo-que-sea"}`,
			tipo:     "application/json",
			esperado: seguridad.FormatoNoAdmitido,
		},
		{
			nombre:   "multipart, que nunca hace falta aquí",
			cuerpo:   "--x\r\nContent-Disposition: form-data; name=\"clave\"\r\n\r\nx\r\n--x--\r\n",
			tipo:     "multipart/form-data; boundary=x",
			esperado: seguridad.FormatoNoAdmitido,
		},
		{
			nombre:   "formulario ilegible",
			cuerpo:   "usuario=%zz&clave=%",
			esperado: seguridad.PeticionMalformada,
		},
		{
			nombre: "origen ya limitado",
			preparar: func(t *testing.T, s *Servidor) {
				for range maxIntentosFallidos {
					s.limitador.fallo("192.0.2.1")
				}
			},
			// Con la contraseña BUENA: el límite manda igual.
			cuerpo:   credenciales(autenticacion.NombreSuperusuario, claveDePrueba),
			esperado: seguridad.LimiteDeIntentos,
		},
		{
			nombre: "sin capacidad criptográfica",
			preparar: func(t *testing.T, s *Servidor) {
				// Se ocupa el presupuesto y se llena la sala, y se acorta la
				// espera para no tardar ocho segundos de verdad.
				s.limitador.espera = time.Millisecond
				s.limitador.activo <- struct{}{}
				for range topeEnEsperaKDF {
					s.limitador.sala <- struct{}{}
				}
			},
			cuerpo:   credenciales(autenticacion.NombreSuperusuario, claveDePrueba),
			esperado: seguridad.SinCapacidadCripto,
		},
	}
	// El octavo fallo —el interno al abrir la sesión— no cabe en esta tabla
	// porque no se puede provocar desde fuera: Abrir solo falla si crypto/rand
	// falla o si el nombre viene vacío, y por la puerta normal ninguna de las
	// dos ocurre. Se comprueba aparte, en
	// TestElFalloAlAbrirLaSesionNoDiceQueLaContrasenaEraBuena.

	var patron respuestaComparable
	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			s := servidorConAuth(t)
			if c.preparar != nil {
				c.preparar(t, s)
			}
			w := postAcceso(t, s, c.cuerpo, c.tipo, "")

			// EXTERIOR: siempre lo mismo.
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("-> %d; todos los fallos de acceso salen con el mismo 401", w.Code)
			}
			for _, cab := range []string{"Retry-After", "WWW-Authenticate", "Location"} {
				if v := w.Header().Get(cab); v != "" {
					t.Errorf("la respuesta lleva %s: %q — eso separa este fallo de los otros", cab, v)
				}
			}
			for _, ck := range w.Result().Cookies() {
				if ck.Name == nombreCookie && ck.Value != "" {
					t.Error("un intento fallido entregó cookie de sesión")
				}
			}
			if !strings.Contains(w.Body.String(), avisoAccesoFallido) {
				t.Errorf("el cuerpo no lleva el aviso genérico:\n%s", w.Body.String())
			}

			obs := respuestaComparable{estado: w.Code, cuerpo: w.Body.String()}
			if patron.estado == 0 {
				patron = obs
			} else if obs.cuerpo != patron.cuerpo {
				t.Errorf("este fallo trae otro cuerpo que los anteriores:\n%s\n---\n%s",
					obs.cuerpo, patron.cuerpo)
			}

			// INTERIOR: cada uno el suyo.
			if got := ultimoEvento(t, s).Motivo; got != c.esperado {
				t.Errorf("se anotó %q; se esperaba %q", got.Etiqueta(), c.esperado.Etiqueta())
			}
		})
	}
}

// L11 — EL FALLO AL ABRIR LA SESIÓN NO DICE QUE LA CONTRASEÑA ERA BUENA.
//
// # POR QUÉ ESTA PRUEBA NO MANDA UNA PETICIÓN
//
// Porque el fallo NO SE PUEDE PROVOCAR desde fuera, y decirlo es más honesto
// que montar un doble que finja que sí. Sesiones.Abrir solo devuelve error si
// crypto/rand falla o si el nombre viene vacío, y por la puerta normal el
// nombre siempre lo pone el verificador. Fingir lo contrario con una interfaz
// inventada para la ocasión probaría el doble, no el servidor.
//
// Lo que SÍ se puede comprobar, y es lo que ADR-0072 §10 exige, es que el
// único camino de salida de ese fallo produzca EXACTAMENTE la misma respuesta
// que una contraseña incorrecta. Antes respondía 500, que es el oráculo
// perfecto: «esa contraseña es buena, vuelve luego».
func TestElFalloAlAbrirLaSesionNoDiceQueLaContrasenaEraBuena(t *testing.T) {
	s := servidorConAuth(t)

	salida := func(motivo seguridad.Motivo) respuestaComparable {
		r := httptest.NewRequest("POST", "/acceso", nil)
		r.Header.Set("Accept", "text/html")
		r, _ = conMarcador(r)
		w := httptest.NewRecorder()
		s.accesoFallido(w, r, motivo)
		return respuestaComparable{
			estado:    w.Code,
			cabeceras: cabecerasDe(w),
			cuerpo:    w.Body.String(),
		}
	}

	interno := salida(seguridad.FalloAlAbrirSesion)
	credencial := salida(seguridad.CredencialIncorrecta)

	if interno.estado != http.StatusUnauthorized {
		t.Errorf("el fallo interno sale con %d; se esperaba el 401 genérico", interno.estado)
	}
	if interno.estado >= 500 {
		t.Error("el fallo interno salió como 5xx: eso lo separa de los demás y es un oráculo")
	}
	if interno != credencial {
		t.Errorf("«no pude abrir la sesión» se distingue de «contraseña incorrecta»:\n%+v\n---\n%+v",
			interno, credencial)
	}
	// Y el motivo SÍ existe por dentro: la avería no se pierde.
	if seguridad.FalloAlAbrirSesion.String() == "desconocido" {
		t.Error("el motivo del fallo interno no está en el enum")
	}
}

// cabecerasDe normaliza las cabeceras de una respuesta para poder compararlas,
// sin Date, que cambia con el reloj y no con lo que se responde.
func cabecerasDe(w *httptest.ResponseRecorder) string {
	var lineas []string
	for k, vs := range w.Result().Header {
		if k == "Date" {
			continue
		}
		lineas = append(lineas, k+": "+strings.Join(vs, ", "))
	}
	sort.Strings(lineas)
	return strings.Join(lineas, "\n")
}

// L3 — LA DEFENSA DE ENUMERACIÓN NO SE PERDIÓ POR EL CAMINO.
//
// Es la propiedad que más fácil habría sido romper al reordenar los controles:
// bastaba con adelantar un «¿existe esta cuenta?» barato antes del KDF y el
// formulario volvería a ser un listador de cuentas medible con un cronómetro.
//
// Se comprueba por el CONTADOR DE DERIVACIONES y no por el reloj: cronometrar
// en una máquina de desarrollo con otras pruebas en paralelo produce una
// prueba que falla sola. Lo que importa es que las dos ramas hagan el mismo
// trabajo, y eso se cuenta.
func TestUnUsuarioValidoQueNoExisteCuestaLoMismoQueUnoQueSi(t *testing.T) {
	s := servidorConAuth(t)
	reg := registroDePrueba(t)
	if err := reg.Alta("ana", "contraseña-de-ana-larga"); err != nil {
		t.Fatalf("alta: %v", err)
	}
	s.usuarios = reg

	// La cuenta existe y la contraseña está mal; y una cuenta que no existe.
	// Las dos tienen que llegar al verificador y salir igual.
	for _, usuario := range []string{"ana", "noexisteestacuenta"} {
		w := postAcceso(t, s, credenciales(usuario, "clave-equivocada-larga"), "", "")
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("%s -> %d", usuario, w.Code)
		}
		e := ultimoEvento(t, s)
		if e.Motivo != seguridad.CredencialIncorrecta {
			t.Errorf("%s se anotó como %q; las dos ramas deben llegar al KDF",
				usuario, e.Motivo.Etiqueta())
		}
	}

	// Y la asimetría que SÍ es legítima y no filtra: la cuenta solo se apunta
	// en el historial si existe (04_SEGURIDAD §6). Eso no lo ve el cliente.
	ev := s.seguridad.Desde(time.Time{})
	var conCuenta, sinCuenta int
	for _, e := range ev {
		if e.Motivo != seguridad.CredencialIncorrecta {
			continue
		}
		if e.Cuenta == "" {
			sinCuenta++
		} else {
			conCuenta++
		}
	}
	if conCuenta != 1 || sinCuenta != 1 {
		t.Errorf("cuentas anotadas: %d con nombre y %d sin; se esperaba una de cada",
			conCuenta, sinCuenta)
	}
}

// L4 — un nombre sintácticamente imposible NO paga el KDF.
//
// Y no filtra nada: las reglas del nombre son públicas y cualquiera las
// comprueba sin preguntarle al servidor.
//
// Se demuestra ocupando el presupuesto criptográfico ENTERO —el activo y la
// sala— y comprobando que la petición pasa por delante sin quedarse esperando:
// si tocara el KDF, se quedaría sin capacidad y el motivo sería otro.
func TestUnNombreImposibleNiSiquieraPideTurnoParaElKDF(t *testing.T) {
	s := servidorConAuth(t)
	s.limitador.espera = time.Hour // si alguien esperase, se notaría
	s.limitador.activo <- struct{}{}
	for range topeEnEsperaKDF {
		s.limitador.sala <- struct{}{}
	}

	hecho := make(chan seguridad.Motivo, 1)
	go func() {
		postAcceso(t, s, credenciales("NO-ES-UN-NOMBRE", "lo-que-sea"), "", "")
		hecho <- s.seguridad.Desde(time.Time{})[0].Motivo
	}()
	select {
	case motivo := <-hecho:
		if motivo != seguridad.PeticionMalformada {
			t.Errorf("se anotó %q; un nombre imposible se rechaza antes de pedir turno", motivo.Etiqueta())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("un nombre sintácticamente imposible se quedó esperando turno para el KDF")
	}
}

// L5 — el cuerpo grande no llega al KDF, y se demuestra igual: con el
// presupuesto criptográfico ocupado, una petición enorme tiene que salir sola.
func TestUnCuerpoEnormeNoAlcanzaElKDF(t *testing.T) {
	s := servidorConAuth(t)
	s.limitador.espera = time.Hour
	s.limitador.activo <- struct{}{}
	for range topeEnEsperaKDF {
		s.limitador.sala <- struct{}{}
	}

	hecho := make(chan seguridad.Motivo, 1)
	go func() {
		// 1 MB con la contraseña BUENA delante: si el tope no cortara la
		// lectura, esta petición se autenticaría.
		postAcceso(t, s, credenciales(autenticacion.NombreSuperusuario,
			claveDePrueba+strings.Repeat("z", 1<<20)), "", "")
		hecho <- s.seguridad.Desde(time.Time{})[0].Motivo
	}()
	select {
	case motivo := <-hecho:
		if motivo != seguridad.CuerpoExcesivo {
			t.Errorf("se anotó %q; se esperaba «cuerpo excesivo» antes del KDF", motivo.Etiqueta())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("un cuerpo de 1 MB llegó a pedir turno para el KDF")
	}
}

// L9 — LA PROPIEDAD DE LA ADMISIÓN, y se prueba sobre la admisión misma y no a
// través de PBKDF2: con 3.2 s por derivación en el nodo, hacerlo por el
// extremo real serían minutos de prueba para comprobar dos canales.
//
// Las dos afirmaciones de ADR-0072 §11:
//
//	derivaciones activas a la vez  <= 1
//	peticiones esperando turno     <= topeEnEsperaKDF, cada una con plazo
func TestLaAdmisionAlKDFNiPasaDeUnaNiFormaColaSinFin(t *testing.T) {
	l := nuevoLimitador()
	l.espera = 50 * time.Millisecond

	var activas, pico atomic.Int64
	var admitidas, rechazadas atomic.Int64
	var mu sync.Mutex

	const empujones = 40
	var wg sync.WaitGroup
	for range empujones {
		wg.Add(1)
		go func() {
			defer wg.Done()
			soltar, ok := l.admitir(context.Background())
			if !ok {
				rechazadas.Add(1)
				return
			}
			admitidas.Add(1)
			n := activas.Add(1)
			mu.Lock()
			if n > pico.Load() {
				pico.Store(n)
			}
			mu.Unlock()
			// El «trabajo caro», en miniatura.
			time.Sleep(5 * time.Millisecond)
			activas.Add(-1)
			soltar()
		}()
	}
	wg.Wait()

	if p := pico.Load(); p > 1 {
		t.Errorf("hubo %d derivaciones a la vez; el presupuesto es de UNA", p)
	}
	if admitidas.Load()+rechazadas.Load() != empujones {
		t.Fatalf("se perdieron peticiones: %d admitidas + %d rechazadas de %d",
			admitidas.Load(), rechazadas.Load(), empujones)
	}
	if rechazadas.Load() == 0 {
		t.Error("40 peticiones simultáneas y ninguna rechazada: eso es una cola sin fin")
	}
	// Y el presupuesto queda libre al terminar: si un camino se olvidara de
	// soltar, el siguiente acceso legítimo del día no entraría.
	if len(l.activo) != 0 || len(l.sala) != 0 {
		t.Errorf("quedó turno sin soltar: activo=%d sala=%d", len(l.activo), len(l.sala))
	}
}

// La espera está ACOTADA, y por eso se puede medir: con el presupuesto ocupado
// y la sala con sitio, admitir vuelve al vencer el plazo y no cuando le
// apetece a quien tiene el turno.
func TestLaEsperaPorElKDFVenceSola(t *testing.T) {
	l := nuevoLimitador()
	l.espera = 30 * time.Millisecond
	l.activo <- struct{}{} // ocupado, y nadie lo va a soltar

	t0 := time.Now()
	soltar, ok := l.admitir(context.Background())
	transcurrido := time.Since(t0)

	if ok {
		soltar()
		t.Fatal("se admitió con el presupuesto ocupado")
	}
	if transcurrido < l.espera {
		t.Errorf("volvió en %v, antes del plazo de %v: no llegó a esperar", transcurrido, l.espera)
	}
	if transcurrido > 20*l.espera {
		t.Errorf("tardó %v con un plazo de %v: la espera no está acotada", transcurrido, l.espera)
	}
	// Y la sala queda libre: quien deja de esperar no retiene su hueco.
	if len(l.sala) != 0 {
		t.Errorf("la sala quedó con %d huecos ocupados por alguien que ya se fue", len(l.sala))
	}
}

// Un cliente que se va deja de costar en el acto: no tiene sentido gastarle
// 3.2 s de CPU al nodo por una respuesta que nadie va a leer.
func TestSiElClienteSeVaSeDejaDeEsperarTurno(t *testing.T) {
	l := nuevoLimitador()
	l.espera = time.Hour
	l.activo <- struct{}{}

	ctx, cancelar := context.WithCancel(context.Background())
	hecho := make(chan bool, 1)
	go func() {
		_, ok := l.admitir(ctx)
		hecho <- ok
	}()
	time.Sleep(10 * time.Millisecond)
	cancelar()

	select {
	case ok := <-hecho:
		if ok {
			t.Error("se admitió a un cliente que ya se había ido")
		}
	case <-time.After(time.Second):
		t.Fatal("el cliente se fue y la petición siguió esperando turno")
	}
}

// L10 — EL RECHAZO POR CAPACIDAD NO ES UNA CONTRASEÑA INCORRECTA.
//
// Es la afirmación de ADR-0071 aplicada aquí: describir lo que pasó. Si esto
// se contara como credencial fallida, el nodo acabaría apartando gente por
// estar él ocupado — y el apartado lo dispara SenalFuerzaBruta, que cuenta
// exactamente ese motivo.
func TestElRechazoPorCapacidadNoCuentaComoContrasenaIncorrecta(t *testing.T) {
	s := servidorConAuth(t)
	s.limitador.espera = time.Millisecond
	s.limitador.activo <- struct{}{}
	for range topeEnEsperaKDF {
		s.limitador.sala <- struct{}{}
	}

	antes := s.contadores.instantanea().AccesosFallidos
	for range umbralFuerzaBrutaDePrueba {
		postAcceso(t, s, credenciales(autenticacion.NombreSuperusuario, "da-igual-cual"), "", "")
	}

	if got := s.contadores.instantanea().AccesosFallidos; got != antes {
		t.Errorf("el contador de accesos fallidos subió a %d: se contaron contraseñas que nunca se verificaron", got)
	}
	if ok, _ := s.limitador.permitido("192.0.2.1"); !ok {
		t.Error("el limitador por origen se cebó con rechazos que no verificaron nada")
	}
	for _, e := range s.seguridad.Desde(time.Time{}) {
		if e.Motivo == seguridad.CredencialIncorrecta {
			t.Fatal("un rechazo por capacidad se anotó como credencial incorrecta")
		}
	}
	// Y la consecuencia que de verdad importa: ninguna señal de fuerza bruta.
	for _, o := range seguridad.PorOrigen(s.seguridad.Desde(time.Time{})) {
		for _, sn := range o.Senales {
			if sn == seguridad.SenalFuerzaBruta {
				t.Fatal("el nodo, por estar ocupado, se acusó a sí mismo de recibir fuerza bruta")
			}
		}
	}
}

// umbralFuerzaBrutaDePrueba son intentos de sobra para disparar la señal si el
// motivo fuera el equivocado. No se importa la constante del paquete seguridad
// —es privada— y tampoco hace falta: lo que se necesita es «bastantes».
const umbralFuerzaBrutaDePrueba = 8

// L12 — NADA DE LO QUE SE MANDA AL FORMULARIO ACABA REGISTRADO.
//
// La contraseña, el cuerpo, la cookie y el testigo. Es la regla de
// 04_SEGURIDAD §6, y este trabajo añadió caminos nuevos por los que podría
// haberse escapado: el cuerpo rechazado por tamaño y el rechazado por formato
// son cuerpos que alguien podría querer «anotar para depurar».
func TestNiLaContrasenaNiElCuerpoNiElTestigoLleganAlHistorial(t *testing.T) {
	s := servidorConAuth(t)
	cookie, _ := sesionAbierta(t, s)

	const secreto = "ESTE-ES-EL-SECRETO-QUE-NO-DEBE-SALIR"
	casos := []string{
		credenciales(autenticacion.NombreSuperusuario, secreto),
		credenciales(secreto, secreto),
		credenciales(autenticacion.NombreSuperusuario, secreto+strings.Repeat("x", 2*topeCuerpoAcceso)),
	}
	for _, cuerpo := range casos {
		r := httptest.NewRequest("POST", "/acceso", strings.NewReader(cuerpo))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.AddCookie(cookie)
		w := httptest.NewRecorder()
		s.Rutas().ServeHTTP(w, r)
	}
	// Y uno con Content-Type inválido, que ni siquiera se llega a leer.
	postAcceso(t, s, secreto, "application/json", "")

	for _, e := range s.seguridad.Desde(time.Time{}) {
		for campo, v := range map[string]string{
			"cuenta": e.Cuenta, "ruta": e.Ruta, "agente": e.Agente,
		} {
			if strings.Contains(v, secreto) {
				t.Errorf("el secreto salió por el campo %s: %q", campo, v)
			}
			if strings.Contains(v, cookie.Value) {
				t.Errorf("el testigo de sesión salió por el campo %s: %q", campo, v)
			}
		}
	}
}

// §34 — EL DIARIO NO SE CONVIERTE EN EL AMPLIFICADOR DE QUIEN INSISTE.
//
// Los dos rechazos que el limitador NO acota —el suyo propio y el de capacidad—
// escriben UNA línea por arranque, y el volumen lo lleva un contador. El hecho
// no se pierde: cada rechazo entra igual en el anillo con su motivo y su
// origen, que es lo que el panel enseña.
func TestLosRechazosBaratosNoEscribenUnaLineaPorPeticion(t *testing.T) {
	s := servidorConAuth(t)
	var diario bytes.Buffer
	s.reg = slog.New(slog.NewJSONHandler(&diario, nil))

	for range maxIntentosFallidos {
		s.limitador.fallo("192.0.2.1")
	}
	const insistencias = 50
	for range insistencias {
		postAcceso(t, s, credenciales(autenticacion.NombreSuperusuario, claveDePrueba), "", "")
	}

	if n := strings.Count(diario.String(), "acceso bloqueado por intentos repetidos"); n != 1 {
		t.Errorf("%d peticiones rechazadas escribieron %d líneas de diario; debe ser UNA", insistencias, n)
	}
	// Y EL HECHO NO SE PIERDE, que es la otra mitad: el contador lleva el
	// volumen y el anillo lleva cada suceso con su motivo.
	if n := s.accesosLimitados.Load(); n != insistencias {
		t.Errorf("el contador agregado dice %d; se esperaban %d", n, insistencias)
	}
	anotados := 0
	for _, e := range s.seguridad.Desde(time.Time{}) {
		if e.Motivo == seguridad.LimiteDeIntentos {
			anotados++
		}
	}
	if anotados != insistencias {
		t.Errorf("el anillo guardó %d rechazos por límite; se esperaban %d: acotar el diario no puede acotar el registro",
			anotados, insistencias)
	}
}
