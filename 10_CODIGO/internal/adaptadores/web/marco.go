package web

import (
	"context"
	"html/template"
	"math"
	"net/http"
	"os"
	"strings"
	"time"

	"nasd/internal/adaptadores/sistema"
	"nasd/internal/autenticacion"
)

// marco.go — el cromo compartido de la consola (ADR-0075): rail con iconos y
// medidor de disco, barra superior con la inicial de la sesión, tira de estado
// al pie y pestañas en el teléfono.
//
// # ESTO ES LA MAQUETA 12, NO UNA VERSIÓN DE ELLA
//
// La primera entrega de esta capa falló por traer solo los colores y la
// disposición, dejando debajo las tablas y la tipografía viejas. Cada pieza
// de aquí tiene su equivalente literal en `estatico/estilo.css`, que a su vez
// es la hoja de la maqueta portada. Si algo no se parece, es un defecto.
//
// # ESTO NO DECIDE PERMISOS, LOS LEE
//
// `modulos` no vuelve a derivar autoridad: cada Visible/Marca llama a las
// MISMAS funciones que ya deciden quién entra por la puerta real —usuarioDe,
// acotadoPorRed (sesion.go)— y a s.novedades.Cuantas(). Esconder un elemento
// aquí es cortesía, nunca control: quien teclee la ruta se topa con
// soloSuperusuario o soloDesdeDentro igual.
//
// # AÑADIR UN MÓDULO
//
// Una entrada en `modulos` y una plantilla. Nada del cromo conoce el NOMBRE
// de ningún módulo — solo recorre esta lista.

// Los iconos van como template.HTML porque son CONSTANTES DE ESTE ARCHIVO,
// no datos: no hay ninguna vía por la que un valor de fuera llegue aquí. Se
// guardan como marcado y no como una ruta `d` suelta porque tres de los cinco
// llevan más de una figura, y partirlos obligaría a un tipo con dos campos
// para no ganar nada. Son los mismos trazos de la maqueta, carácter a
// carácter.
const (
	icoResumen   template.HTML = `<path d="M3 11l9-8 9 8v9a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/>`
	icoArchivos  template.HTML = `<path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/>`
	icoEstado    template.HTML = `<path d="M3 12h4l3 8 4-16 3 8h4"/>`
	icoSeguridad template.HTML = `<path d="M12 3l7 3v6c0 4.4-3 8.2-7 9-4-.8-7-4.6-7-9V6z"/>`
	icoCuentas   template.HTML = `<circle cx="9" cy="8" r="3"/><path d="M3 20c0-3.3 2.7-6 6-6s6 2.7 6 6M17 11h4M19 9v4"/>`
)

// itemMarco es un módulo YA resuelto para una petición concreta.
type itemMarco struct {
	Clave  string
	Rotulo string
	Ruta   string
	Icono  template.HTML
	Activo bool
	// Marca es el contador del rail. Cero no se pinta: una pastilla con un 0
	// sería ruido permanente.
	Marca int
}

// grupoMarco es un bloque del rail con su rótulo. Es una LISTA DE LISTAS y no
// una marca por elemento: con lo segundo, el rótulo se repetía una vez por
// módulo y el rail salía con tres «SISTEMA» seguidos — el defecto exacto de
// la primera entrega.
type grupoMarco struct {
	Rotulo string
	Items  []itemMarco
}

// parteDisco es un tramo del medidor del rail.
//
// LOS ANCHOS VAN EN NÚMERO Y NO EN «style="width:…%"», y no es un capricho:
// la CSP de este servidor es «style-src 'self'» sin 'unsafe-inline'
// (ADR-0060), así que un estilo en línea se descarta en silencio y la barra
// sale vacía. Se pintan como <rect> de un SVG, cuyos atributos NO son CSS.
type parteDisco struct {
	Nombre string
	Tamano string
	// X y Ancho son porcentajes sobre 100, ya acumulados.
	X     float64
	Ancho float64
	// Clase es el tono del tramo: las cuentas alternan, «otros» tiene el suyo.
	Clase string
}

// discoRail es el medidor del pie del rail — el mismo de la maqueta.
type discoRail struct {
	Usado  string
	Total  string
	Partes []parteDisco
}

// itemEstado es un elemento de la tira de estado del pie.
type itemEstado struct {
	Nombre string
	Valor  string
	// Punto es "v" (verde) o "a" (ámbar); vacío no pinta ninguno.
	Punto string
}

// marco es lo que `_marco.html` necesita para pintar el cromo.
type marco struct {
	Titulo string
	Sub    string
	// Grupos es el rail: bloques con rótulo. Todos es la MISMA lista aplanada,
	// para las pestañas del teléfono, que no llevan separadores.
	Grupos []grupoMarco
	Todos  []itemMarco
	Csrf   string

	// Nodo y NodoSub son la marca del rail: «nas-ejemplo» y «en línea · 4d 20h».
	Nodo    string
	NodoSub string
	// Inicial es la letra del avatar de la barra superior. La MISMA regla que
	// el formulario de acceso (inicialDe, sesion.go): identifica sin publicar
	// el nombre a quien mire por encima del hombro.
	Inicial string

	// Disco es nulo cuando el volumen no se pudo medir; el rail se queda sin
	// medidor en vez de pintar ceros.
	Disco  *discoRail
	Estado []itemEstado
	// Version es la revisión corta que ya publica el pie: qué binario corre.
	Version string
}

// modulo es UNA fila de la tabla del rail, antes de resolverla.
type modulo struct {
	clave, rotulo, ruta, grupo string
	icono                      template.HTML
	// visible es nil para «siempre visible» (Archivos, hoy el único caso).
	visible func(r *http.Request) bool
	// marca es nil para «sin contador».
	marca func(s *Servidor, r *http.Request) int
}

// esSuperusuario y puedeAdministrarDesde son las DOS reglas que ya gobiernan
// las rutas reales (sesion.go: soloSuperusuario, soloDesdeDentro,
// acotadoPorRed). Se nombran aquí para que la tabla se lea como una frase.
func esSuperusuario(r *http.Request) bool {
	return usuarioDe(r) == autenticacion.NombreSuperusuario
}

func puedeAdministrarDesde(r *http.Request) bool {
	return esSuperusuario(r) && !acotadoPorRed(r)
}

// modulos es la ÚNICA tabla que sabe qué módulos existen, en qué orden y
// quién los ve. El resto del cromo solo la recorre.
//
// «Resumen» vive en su propia ruta —/resumen— y NO en «/»: «/» es, para las
// dos cuentas normales, el listado de archivos, y urlDeListado(raíz) == "/"
// es lo que usan TODAS las migas de pan y el atajo «subir» del listado.
// Servir un panel distinto en «/» solo para el superusuario habría roto ese
// enlace justo para la cuenta que más navega.
var modulos = []modulo{
	{clave: "resumen", rotulo: "Resumen", ruta: "/resumen", icono: icoResumen, visible: esSuperusuario},
	{clave: "archivos", rotulo: "Archivos", ruta: "/", icono: icoArchivos},
	{clave: "estado", rotulo: "Estado", ruta: "/estado", grupo: "Sistema", icono: icoEstado, visible: esSuperusuario},
	{clave: "seguridad", rotulo: "Seguridad", ruta: "/seguridad", grupo: "Sistema", icono: icoSeguridad, visible: esSuperusuario,
		marca: func(s *Servidor, r *http.Request) int { return s.novedades.Cuantas() }},
	{clave: "cuentas", rotulo: "Cuentas", ruta: "/administracion", grupo: "Sistema", icono: icoCuentas, visible: puedeAdministrarDesde},
}

// vidaDelVolumen es cuánto se reutiliza la última medida del nodo.
const vidaDelVolumen = time.Minute

// plazoDeMedida acota lo que puede tardar una medición en segundo plano. No
// hay nadie esperándola, pero tampoco puede quedarse colgada para siempre
// sobre un disco que dejó de responder — RNF-12: plazo declarado en toda E/S.
const plazoDeMedida = 15 * time.Second

// volumenReciente entrega la ÚLTIMA lectura del nodo, sin medir jamás en el
// camino de la petición.
//
// # POR QUÉ NO SE MIDE AQUÍ MISMO, Y ES UN DEFECTO YA COMETIDO
//
// La primera versión de esta función llamaba a sistema.Leer con una caché de
// un minuto, dando por hecho que medir era barato. NO LO ES, y no por el
// statfs: sistema.Leer lee /proc/stat DOS VECES separadas por VentanaCPU
// —250 ms de sueño— porque el porcentaje de CPU no existe como lectura, solo
// como diferencia entre dos muestras. Eso ponía un cuarto de segundo de
// sueño en CADA carga de CADA página de la consola.
//
// No es teoría: lo destapó TestCadaPeticionRenuevaLaInactividad, que desliza
// una sesión con peticiones cada 20 ms y empezó a fallar porque cada una
// tardaba más de 250. Un medidor de disco decorativo no puede costar eso.
//
// Así que se devuelve lo que haya —el cero si todavía no hay nada, y
// entonces el rail simplemente no pinta el medidor— y se lanza la medición
// EN SEGUNDO PLANO si la que hay ya caducó. La siguiente carga la encuentra
// hecha. Como mucho hay UNA medición en vuelo: volMidiendo lo garantiza.
func (s *Servidor) volumenReciente() sistema.Nodo {
	s.volMu.Lock()
	defer s.volMu.Unlock()
	if !s.volMidiendo && time.Since(s.volCuando) >= vidaDelVolumen {
		s.volMidiendo = true
		go s.medirVolumen()
	}
	return s.volCache
}

// medirVolumen hace la lectura cara y la publica. Corre en su propia
// goroutine, sin nadie esperándola.
func (s *Servidor) medirVolumen() {
	ctx, cancelar := context.WithTimeout(context.Background(), plazoDeMedida)
	defer cancelar()
	n := sistema.Leer(ctx, s.volumen)

	s.volMu.Lock()
	defer s.volMu.Unlock()
	s.volCache, s.volCuando, s.volMidiendo = n, time.Now(), false
}

// anotarVolumen guarda una lectura que OTRO ya pagó.
//
// /estado y /resumen miden el nodo de todas formas —es su trabajo—, así que
// su resultado sirve igual para el rail y evita una segunda medición un
// minuto después. Es la misma idea que instantaneaCompleta: si el dato ya
// está en la mano, no se vuelve a pedir.
func (s *Servidor) anotarVolumen(n sistema.Nodo) {
	s.volMu.Lock()
	defer s.volMu.Unlock()
	s.volCache, s.volCuando = n, time.Now()
}

// construirMarco resuelve la tabla contra ESTA petición.
func (s *Servidor) construirMarco(r *http.Request, activo, titulo, sub string) marco {
	m := marco{
		Titulo:  titulo,
		Sub:     sub,
		Csrf:    s.csrfDe(r),
		Nodo:    nombreDelNodo(r),
		Inicial: inicialDe(usuarioDe(r)),
	}

	for _, def := range modulos {
		if def.visible != nil && !def.visible(r) {
			continue
		}
		item := itemMarco{
			Clave:  def.clave,
			Rotulo: def.rotulo,
			Ruta:   def.ruta,
			Icono:  def.icono,
			Activo: def.clave == activo,
		}
		if def.marca != nil {
			item.Marca = def.marca(s, r)
		}
		// Se abre un grupo nuevo solo cuando el rótulo CAMBIA; si no, el
		// módulo cae en el que ya estaba abierto.
		if n := len(m.Grupos); n == 0 || m.Grupos[n-1].Rotulo != def.grupo {
			m.Grupos = append(m.Grupos, grupoMarco{Rotulo: def.grupo})
		}
		g := &m.Grupos[len(m.Grupos)-1]
		g.Items = append(g.Items, item)
		m.Todos = append(m.Todos, item)
	}

	// El pie y la marca del rail hablan del NODO, no de la carpeta que se está
	// mirando, así que solo se arman para quien puede verlos. A una cuenta
	// normal no le informan de nada suyo — mismo criterio que /estado.
	if esSuperusuario(r) {
		n := s.volumenReciente()
		if n.Vivo.UptimeOK {
			m.NodoSub = "en línea · " + duracionLegible(n.Vivo.Uptime)
		}
		m.Disco = s.medidorDeDisco(n)
		m.Estado = s.tiraDeEstado()
		m.Version = revisionCorta()
	}
	return m
}

// nombreDelNodo es el anfitrión por el que se ha llegado, sin puerto: es lo
// que el responsable teclea y lo que reconoce. Se cae al nombre genérico
// cuando la petición no trae Host —solo pasa en pruebas—.
func nombreDelNodo(r *http.Request) string {
	h := r.Host
	if i := strings.LastIndex(h, ":"); i > 0 && !strings.Contains(h[i:], "]") {
		h = h[:i]
	}
	h = strings.Trim(h, "[]")
	if h == "" {
		return "NAS"
	}
	return h
}

// tonosDeCuenta son los tramos del medidor. Alternan entre dos grises, igual
// que en la maqueta con sus dos cuentas; «otros» tiene el suyo aparte.
var tonosDeCuenta = []string{"sg-a", "sg-b"}

// medidorDeDisco compone la barra del pie del rail: cuánto ocupa cada cuenta
// y cuánto es de todo lo demás.
//
// SALE DE DOS FUENTES QUE YA EXISTEN y no mide nada nuevo: el total y lo
// libre, del volumen; el reparto por cuenta, de metricas.Todas(), que es una
// lectura en memoria y NO recorre disco (lo caro ya ocurrió, si acaso, en el
// último «Refrescar métricas»). Por eso una cuenta que nunca se midió no
// aparece: inventarle un tramo sería afirmar un tamaño que nadie ha contado.
func (s *Servidor) medidorDeDisco(n sistema.Nodo) *discoRail {
	v := n.Datos
	if !v.Disponible || v.TotalBytes == 0 {
		return nil
	}
	usado := v.TotalBytes - v.LibresBytes
	d := &discoRail{
		Usado: legibleBytes(usado),
		Total: legibleBytes(v.TotalBytes),
	}

	medidas := s.metricas.Todas()
	var deCuentas uint64
	i := 0
	x := 0.0
	for _, u := range s.usuarios.Lista() {
		m, ok := medidas[u.Nombre]
		if !ok || m.Bytes <= 0 {
			continue
		}
		b := uint64(m.Bytes)
		ancho := float64(b) * 100 / float64(v.TotalBytes)
		d.Partes = append(d.Partes, parteDisco{
			Nombre: u.Nombre,
			Tamano: legibleBytes(b),
			X:      redondear(x),
			Ancho:  redondear(ancho),
			Clase:  tonosDeCuenta[i%len(tonosDeCuenta)],
		})
		x += ancho
		deCuentas += b
		i++
	}

	// «Otros» es lo ocupado que no pertenece a ninguna cuenta medida. Se acota
	// a cero en vez de restar por debajo: las medidas de cuenta pueden ser de
	// hace días y quedar por encima de lo ocupado de ahora, y un tramo
	// negativo pintaría una barra al revés.
	if usado > deCuentas {
		otros := usado - deCuentas
		d.Partes = append(d.Partes, parteDisco{
			Nombre: "otros",
			Tamano: legibleBytes(otros),
			X:      redondear(x),
			Ancho:  redondear(float64(otros) * 100 / float64(v.TotalBytes)),
			Clase:  "sg-o",
		})
	}
	return d
}

// tiraDeEstado es el pie de la consola: qué procesos hay vivos.
//
// NO SE AFIRMA LO QUE NO SE PUEDE COMPROBAR. De `nasd` se sabe con certeza
// —está sirviendo esta página—. De `nas-sensor` no: es otro proceso, y lo
// único observable desde aquí es si su archivo se ha escrito hace poco. Por
// eso se dice «activo» solo con esa evidencia, «sin datos» si el archivo
// existe pero está frío, y no se pinta nada si el sensor ni siquiera está
// configurado — un indicador permanentemente gris enseña a ignorar el panel
// (ADR-0065).
func (s *Servidor) tiraDeEstado() []itemEstado {
	out := []itemEstado{{Nombre: "nasd", Valor: "activo", Punto: "v"}}
	if s.rutaToques == "" {
		return out
	}
	it := itemEstado{Nombre: "nas-sensor", Valor: "sin datos", Punto: "a"}
	if fresco, err := archivoFresco(s.rutaToques, 10*time.Minute); err == nil && fresco {
		it.Valor, it.Punto = "activo", "v"
	}
	return append(out, it)
}

// revisionCorta es la revisión de git del binario, sola. El pie de la consola
// enseña solo eso; la línea completa —fecha, huella SHA-256— sigue estando
// entera en /estado, que es donde se va a comprobar un despliegue.
func revisionCorta() string {
	partes := versionDelBinario()
	if len(partes) < 2 {
		return ""
	}
	return partes[1]
}

// archivoFresco dice si un archivo se ha escrito dentro del plazo dado. Es la
// única forma que este proceso tiene de observar a otro sin hablar con él.
func archivoFresco(ruta string, plazo time.Duration) (bool, error) {
	fi, err := os.Stat(ruta)
	if err != nil {
		return false, err
	}
	return time.Since(fi.ModTime()) < plazo, nil
}

// redondear deja un porcentaje en tres decimales. Va a un atributo de SVG y
// no a CSS, así que se escribe con el punto decimal de Go —que no depende de
// ninguna configuración regional— y sin arrastrar diecisiete cifras.
func redondear(f float64) float64 { return math.Round(f*1000) / 1000 }
