package seguridad

import (
	"cmp"
	"net/netip"
	"path"
	"slices"
	"strings"
	"time"
)

// Agregación y señales — etapa 2 del panel.
//
// # LA REGLA QUE GOBIERNA ESTE ARCHIVO
//
// Un Evento es un HECHO: el servidor negó esto, por esta regla, a este
// origen. Una Senal es una INFERENCIA sobre un conjunto de hechos, y puede
// equivocarse. No se guardan juntas, no se calculan juntas y no se pintan
// igual, porque confundirlas es exactamente lo que el responsable prohibió:
// «no clasifiques automáticamente todo rechazo como ataque».
//
// Las señales se derivan AL MIRAR y nunca se persisten. Consecuencia
// deliberada: afinar un umbral no reescribe la historia, porque la historia
// solo contiene hechos. Si las señales vivieran en el archivo, cambiar un
// número obligaría a reinterpretar —o peor, a creer— lo ya escrito.

// Senal es una sospecha derivada, con su porqué y su tasa de error propia.
type Senal uint8

const (
	// SenalExploracion — el mismo origen pidió muchas rutas DISTINTAS que no
	// existen. Es el patrón de un escáner recorriendo un diccionario.
	SenalExploracion Senal = iota
	// SenalFuerzaBruta — credenciales incorrectas repetidas desde un origen.
	SenalFuerzaBruta
	// SenalSoftwareAjeno — se pidieron rutas de programas que este NAS no
	// ejecuta NI HA EJECUTADO NUNCA. Ver RutaDeSoftwareAjeno.
	SenalSoftwareAjeno
)

// String es la clave estable de una señal, separada de Etiqueta por el mismo
// motivo que en Motivo: la etiqueta se reescribe por gusto, y desde que la
// cuarentena persiste QUÉ señal apartó a alguien, guardar la etiqueta dejaría
// ilegible el archivo ya escrito al cambiar una palabra.
func (s Senal) String() string {
	switch s {
	case SenalExploracion:
		return "exploracion"
	case SenalFuerzaBruta:
		return "fuerza_bruta"
	case SenalSoftwareAjeno:
		return "software_ajeno"
	}
	return "desconocida"
}

// SenalDesde recupera una señal de su forma estable. Mismo contrato que
// MotivoDesde, incluido el booleano: sin él, un archivo escrito por una
// versión futura con señales nuevas se leería como si todas fueran la
// primera del enum, en silencio.
func SenalDesde(s string) (Senal, bool) {
	for x := SenalExploracion; x <= SenalSoftwareAjeno; x++ {
		if x.String() == s {
			return x, true
		}
	}
	return SenalExploracion, false
}

func (s Senal) Etiqueta() string {
	switch s {
	case SenalExploracion:
		return "Exploración automatizada"
	case SenalFuerzaBruta:
		return "Intentos repetidos de contraseña"
	case SenalSoftwareAjeno:
		return "Sondeo de software que aquí no existe"
	}
	return "Señal desconocida"
}

// Explicacion dice en qué se basa la sospecha Y con qué se puede confundir.
//
// La segunda mitad no es cortesía: una señal sin su falso positivo escrito al
// lado se lee como un veredicto, y este panel no emite veredictos.
func (s Senal) Explicacion() string {
	switch s {
	case SenalExploracion:
		return "Este origen pidió al menos " + itoa(umbralExploracion) +
			" rutas distintas que no existen. Puede ser un escáner recorriendo un diccionario. " +
			"También lo produce un marcador antiguo, un cliente mal configurado o un enlace roto que se reintenta."
	case SenalFuerzaBruta:
		return "Este origen falló la contraseña al menos " + itoa(umbralFuerzaBruta) +
			" veces. El limitador ya lo frena a los " + itoa(5) + " intentos por su cuenta. " +
			"También lo produce alguien de casa que no recuerda su contraseña."
	case SenalSoftwareAjeno:
		return "Se pidieron rutas de programas que este NAS no ejecuta (PHP, paneles de administración ajenos, archivos de configuración). " +
			"Aquí no hay ninguno, así que no hay nada que comprometer por esa vía; " +
			"es el rastreo indiscriminado que recibe cualquier dirección pública, no algo dirigido a este nodo."
	}
	return ""
}

// Destacar dice si la señal merece énfasis visual.
//
// # POR QUÉ UNA DE LAS TRES SE CALLA
//
// SenalSoftwareAjeno dispara con UNA sola ruta ajena, y su propia Explicacion
// termina diciendo que «es el rastreo indiscriminado que recibe cualquier
// dirección pública, no algo dirigido a este nodo». Una alerta cuyo texto dice
// que no es nada gasta la credibilidad de las que sí importan — el mismo
// razonamiento con el que evaluarThrottled (web/estado.go) decidió no pintar en
// ámbar permanente el límite térmico ya aceptado.
//
// La señal NO se retira: informa, y RF-29(7) exige que cada inferencia esté con
// su explicación. Lo que pierde es el énfasis (ADR-0065).
//
// NO SE SUBE SU UMBRAL, que era la otra salida: fijar un número sin tráfico
// suficiente medido sería adivinar, que es lo mismo que el responsable rechazó
// en la etapa 5 del panel.
func (s Senal) Destacar() bool { return s != SenalSoftwareAjeno }

// Umbrales de las señales. Cada uno con su porqué, porque un número sin
// justificar se cambia por gusto y entonces la señal deja de significar nada.
const (
	// Ocho rutas DISTINTAS e inexistentes. Alguien navegando con enlaces
	// viejos falla una o dos; ocho distintas ya no es navegar.
	umbralExploracion = 8
	// Cinco: el MISMO número que maxIntentosFallidos del limitador de
	// sesion.go. Deliberadamente igual y no un valor propio: si el panel
	// avisara antes o después de lo que el servidor actúa, las dos cifras se
	// contradirían y habría que explicar cuál manda.
	umbralFuerzaBruta = 5
)

// RutaDeSoftwareAjeno decide si una ruta pide algo que este NAS no tiene.
//
// # POR QUÉ ESTÁ EXPORTADA, Y POR QUÉ ES LA ÚNICA
//
// Empezó privada, porque la única pregunta que la necesitaba —«¿emito
// SenalSoftwareAjeno?»— se hacía aquí mismo. Ahora hay tres, y las tres tienen
// que contestar LO MISMO ante «/.git/config»:
//
//  1. derivar la señal (PorOrigen, aquí abajo);
//  2. construir la evidencia que la sustenta (Sonda, aquí abajo);
//  3. decidir si una respuesta con contenido fue INESPERADA (RespuestaInesperada,
//     hallazgos.go), que es la única de las tres que corre en el camino de cada
//     petición.
//
// Copiar la lista a la tercera habría dejado dos verdades que divergen el día
// que alguien añada «.phtml» a una sola de ellas. Se exporta la FUNCIÓN, no la
// lista: quien pregunta no puede reordenarla ni ampliarla por su cuenta.
//
// # POR QUÉ NO ES UNA LISTA DE RUTAS CONOCIDAS
//
// Una lista de «/wp-login.php, /phpmyadmin, …» caduca sola: cada mes aparecen
// sondas nuevas y la lista se queda corta sin avisar, que es el modo de fallo
// que este proyecto persigue.
//
// Esto se apoya en un HECHO comprobable del propio nodo: nasd es un binario
// de Go con una tabla de rutas fija (web.Rutas()) y NO ejecuta PHP, ni ASP,
// ni CGI, ni tiene repositorio ni volcados de base de datos publicados. Una
// petición con esas extensiones no puede ser un enlace roto de este NAS,
// porque este NAS nunca ha servido nada así. La afirmación se sostiene sobre
// lo que el nodo es, no sobre lo que alguien recuerde apuntar.
func RutaDeSoftwareAjeno(ruta string) bool {
	r := strings.ToLower(ruta)
	// Extensiones de programas que este binario no ejecuta.
	switch strings.ToLower(path.Ext(r)) {
	case ".php", ".asp", ".aspx", ".jsp", ".cgi", ".pl", ".sql", ".bak":
		return true
	}
	// Archivos y directorios que aquí no se publican nunca. Van con la barra
	// o al final para no marcar una carpeta legítima que empiece igual.
	for _, s := range []string{"/.env", "/.git", "/.aws", "/.ssh", "/wp-admin", "/wp-includes"} {
		if r == s || strings.HasPrefix(r, s+"/") || strings.Contains(r, s+"/") {
			return true
		}
	}
	return false
}

// Filtro acota lo que se mira. Los punteros distinguen «sin filtrar» de
// «filtrar por el valor cero», que en Motivo es un valor legítimo
// —MotivoDesconocido— y se perdería con un valor plano.
//
// SE QUEDÓ EN TRES CAMPOS (ADR-0065). Tenía siete: Hasta no lo fijó nunca
// nadie, IP y Ruta alimentaban dos cajas de búsqueda que el responsable
// declaró que no usaba, y Gravedad filtraba por algo que el panel no enseña.
type Filtro struct {
	Desde  time.Time
	Motivo *Motivo
	Red    *Red
}

func (f Filtro) admite(e Evento) bool {
	if f.Motivo != nil && e.Motivo != *f.Motivo {
		return false
	}
	if f.Red != nil && e.Red != *f.Red {
		return false
	}
	return true
}

// Filtrados devuelve los eventos que pasan el filtro, del más reciente al más
// antiguo.
func (a *Anillo) Filtrados(f Filtro) []Evento {
	crudos := a.Desde(f.Desde)
	out := make([]Evento, 0, len(crudos))
	for _, e := range crudos {
		if f.admite(e) {
			out = append(out, e)
		}
	}
	return out
}

// TopeSondas es cuántas líneas de evidencia se conservan por origen.
//
// # POR QUÉ HAY UN TOPE Y POR QUÉ ES ESTE
//
// La evidencia la escribe UN TERCERO: las rutas las elige quien sondea, así
// que sin tope esto sería la única estructura del panel cuyo tamaño decide
// alguien de fuera — exactamente lo que topeCuarentena y topeAgente existen
// para impedir.
//
// Diez, el MISMO número que TopeRutas, y no un valor propio: las dos tablas
// contestan «¿qué se pidió más?» a distinta escala —el nodo entero y un
// origen— y dos topes distintos obligarían a explicar cuál manda. Diez líneas
// bastan para leer un diccionario de escáner: lo que se pierde a partir de ahí
// es más de lo mismo, y SondasVistas dice cuánto se recortó.
const TopeSondas = 10

// Sonda es UNA petición rechazada, agregada por método, ruta y estado.
//
// # ES UN HECHO, NO UNA INFERENCIA, Y POR ESO NO SE PERSISTE
//
// Cada campo salió tal cual de un Evento que sí está en el anillo: esto es
// una VISTA de hechos ya guardados, agrupada para poder leerla. Persistirla
// sería guardar dos veces lo mismo y crear un segundo sitio donde el
// historial puede decir algo distinto.
//
// Existe porque el panel afirmaba «Exploración automatizada» sin enseñar
// sobre qué. Una inferencia cuya evidencia hay que adivinar es indistinguible
// de una inventada, y este panel no emite veredictos (ADR-0061).
//
// NO LLEVA User-Agent NI CUENTA: el primero ya se ve en la cronología y la
// segunda tiene su propia pastilla. Repetirlos aquí multiplicaría por diez
// filas un dato que no cambia entre ellas.
type Sonda struct {
	Metodo string
	Ruta   string
	// Estado es el que el servidor DEVOLVIÓ de verdad. Va en la clave de
	// agrupación y no solo en la fila porque «/.git/config → 404» y
	// «/.git/config → 200» son dos hechos opuestos sobre el nodo, y sumarlos
	// borraría justo la diferencia que hay que ver (ver hallazgos.go).
	Estado  int
	Veces   int
	Primera time.Time
	Ultima  time.Time
}

// claveSonda agrupa. Los tres campos juntos, por lo dicho en Estado.
type claveSonda struct {
	metodo string
	ruta   string
	estado int
}

// Origen agrupa lo que hizo una misma dirección.
type Origen struct {
	IP  netip.Addr
	Red Red
	// Eventos es cuántos rechazos, no cuántas peticiones: una descarga
	// correcta desde esta misma IP no cuenta aquí.
	Eventos int
	Primera time.Time
	Ultima  time.Time
	// PorMotivo lleva el desglose. Es lo que permite ver que un origen con
	// 300 eventos son 300 «sin sesión» de un móvil reconectando, y no 300
	// intentos de contraseña.
	PorMotivo map[Motivo]int
	Cuentas   []string
	Senales   []Senal
	// Evidencia son los hechos que sustentan las señales, agregados y
	// RECORTADOS a TopeSondas. Van del más repetido al menos.
	Evidencia []Sonda
	// SondasVistas son las combinaciones método+ruta+estado DISTINTAS que hubo,
	// que casi siempre son más que las TopeSondas que se pintan. Se publica
	// para que la evidencia pueda decir que recorta, igual que ya hacen la
	// cronología y la tabla de rutas: tres listas de la misma página no pueden
	// tener distinta idea de la honestidad.
	SondasVistas int
	// RutasInexistentes son las rutas DISTINTAS que no existían, es decir la
	// ENTRADA exacta del umbral de SenalExploracion.
	//
	// ADR-0065 retiró una columna que enseñaba justo esto, y con razón: estaba
	// en la tabla, dos columnas antes de la CONCLUSIÓN del mismo umbral, así
	// que el lector veía el cálculo y el resultado a la vez sin que nadie le
	// dijera que eran la misma cosa. Aquí no vuelve a la tabla: vive DENTRO de
	// la explicación de la señal, que es donde la pregunta «¿por qué dice
	// exploración?» se hace de verdad.
	RutasInexistentes int
	// Gravedad es la MAYOR de sus eventos, no la media: un origen con 200
	// rechazos de rutina y uno de atención merece mirarse, y promediar lo
	// escondería.
	Gravedad Gravedad
}

// PorOrigen agrupa los eventos por dirección y deriva las señales.
//
// Ordena por gravedad y luego por número de eventos, no por fecha: quien abre
// este panel quiere ver primero lo que más pide su atención, no lo último que
// pasó — para eso está la vista cronológica.
func PorOrigen(eventos []Evento) []Origen {
	type acumulado struct {
		o       Origen
		cuentas map[string]bool
		ajenas  int
		// sinRutaOK son las rutas DISTINTAS que no existían, y es la base de
		// SenalExploracion. No se confunda con un recuento de rutas a secas:
		// aquel existía solo para pintar una columna que se retiró, porque
		// enseñaba la ENTRADA de un umbral cuya CONCLUSIÓN ya se pintaba dos
		// columnas más allá (ADR-0065).
		sinRutaOK map[string]bool
		// sondas agrupa la evidencia por método+ruta+estado. Es un mapa sin
		// tope, y eso es seguro por dónde vive: su cota es el número de eventos
		// de la ventana, que el anillo ya acota en Capacidad. Lo que SÍ se
		// acota es lo que sale (TopeSondas), porque eso es lo que viaja al
		// navegador y se pinta en un A53.
		sondas map[claveSonda]*Sonda
	}
	porIP := make(map[netip.Addr]*acumulado)

	for _, e := range eventos {
		a, ok := porIP[e.Origen]
		if !ok {
			a = &acumulado{
				o: Origen{
					IP: e.Origen, Red: e.Red,
					Primera: e.Momento, Ultima: e.Momento,
					PorMotivo: make(map[Motivo]int),
				},
				cuentas:   make(map[string]bool),
				sinRutaOK: make(map[string]bool),
				sondas:    make(map[claveSonda]*Sonda),
			}
			porIP[e.Origen] = a
		}
		a.o.Eventos++
		a.o.PorMotivo[e.Motivo]++
		if e.Momento.Before(a.o.Primera) {
			a.o.Primera = e.Momento
		}
		if e.Momento.After(a.o.Ultima) {
			a.o.Ultima = e.Momento
		}
		if g := e.Motivo.Gravedad(); g > a.o.Gravedad {
			a.o.Gravedad = g
		}
		if e.Cuenta != "" {
			a.cuentas[e.Cuenta] = true
		}
		if e.Motivo == RutaInexistente {
			a.sinRutaOK[e.Ruta] = true
		}
		if RutaDeSoftwareAjeno(e.Ruta) {
			a.ajenas++
		}
		// LA EVIDENCIA SE ACUMULA PARA TODOS LOS RECHAZOS, no solo para los que
		// disparan una señal. Si solo se guardara lo que ya sospechamos, la
		// tabla confirmaría siempre lo que la señal dice y nunca podría
		// desmentirla — que es lo que se le pide a una evidencia.
		k := claveSonda{metodo: e.Metodo, ruta: e.Ruta, estado: e.Estado}
		sd, ok := a.sondas[k]
		if !ok {
			sd = &Sonda{
				Metodo: e.Metodo, Ruta: e.Ruta, Estado: e.Estado,
				Primera: e.Momento, Ultima: e.Momento,
			}
			a.sondas[k] = sd
		}
		sd.Veces++
		if e.Momento.Before(sd.Primera) {
			sd.Primera = e.Momento
		}
		if e.Momento.After(sd.Ultima) {
			sd.Ultima = e.Momento
		}
	}

	out := make([]Origen, 0, len(porIP))
	for _, a := range porIP {
		a.o.Cuentas = ordenado(a.cuentas)
		a.o.RutasInexistentes = len(a.sinRutaOK)
		a.o.SondasVistas = len(a.sondas)
		a.o.Evidencia = sondasOrdenadas(a.sondas, TopeSondas)

		// LAS INFERENCIAS, aquí y en ningún otro sitio.
		//
		// SOLO DESDE INTERNET, y lo enseñó la primera captura del panel en
		// produccion: «Sondeo de software que aquí no existe» saltó sobre el
		// PROPIO NODO porque el despliegue había probado /wp-login.php con
		// curl. Una alarma roja sobre uno mismo, en la primera pantalla que
		// vio el responsable.
		//
		// El argumento no es cosmético. Desde la LAN, el túnel o el propio
		// nodo, quien pide YA tiene la casa o la clave: el túnel es mudo sin
		// ella (06_ACCESO_REMOTO §3) y a la LAN se entra por la puerta. Una
		// sospecha de intrusión desde ahí no informa de nada y gasta la
		// credibilidad de las que sí importan — el defecto que 00_RECTOR.md
		// §12.5 lleva persiguiendo en los verificadores de este proyecto.
		//
		// Los HECHOS se siguen registrando igual desde cualquier red: lo que
		// no se emite es la interpretación.
		if a.o.Red.DeFuera() {
			if len(a.sinRutaOK) >= umbralExploracion {
				a.o.Senales = append(a.o.Senales, SenalExploracion)
			}
			if a.o.PorMotivo[CredencialIncorrecta] >= umbralFuerzaBruta {
				a.o.Senales = append(a.o.Senales, SenalFuerzaBruta)
			}
			if a.ajenas > 0 {
				a.o.Senales = append(a.o.Senales, SenalSoftwareAjeno)
			}
		}
		out = append(out, a.o)
	}

	slices.SortFunc(out, func(x, y Origen) int {
		if c := cmp.Compare(y.Gravedad, x.Gravedad); c != 0 {
			return c
		}
		if c := cmp.Compare(y.Eventos, x.Eventos); c != 0 {
			return c
		}
		// Tercer criterio para que el orden sea DETERMINISTA: sin él, dos
		// orígenes empatados salen en el orden aleatorio del recorrido del
		// mapa y la tabla baila entre recargas sin que nada haya cambiado.
		return strings.Compare(x.IP.String(), y.IP.String())
	})
	return out
}

// TopeRutas es cuántas rutas se listan en la tabla de rutas más rechazadas.
// Exportada porque el adaptador web la necesita para poder DECIR que recorta.
const TopeRutas = 10

// Cuenta es un par «algo» y «cuántas veces», para las listas del resumen.
type Cuenta struct {
	Valor string
	Veces int
}

// Resumen es la cabecera del panel: lo que se responde de un vistazo.
type Resumen struct {
	Eventos int
	// TotalHistorico son los rechazos vistos desde siempre, que puede ser
	// mucho mayor que lo que el anillo conserva. Se publica para que un
	// anillo lleno no se lea como «esto es todo lo que ha pasado».
	TotalHistorico int64
	Truncado       bool

	IPsUnicas  int
	DesdeFuera int

	RutasMasPedidas []Cuenta
	// RutasVistas son cuántas rutas DISTINTAS hubo, que casi siempre es más
	// que las TopeRutas que se pintan. Se publica para que la tabla pueda
	// decir que está recortando, como ya hacía la cronología: dos tablas de
	// la misma página no pueden tener distinta idea de la honestidad.
	RutasVistas          int
	AutenticacionFallida int
	// OrigenesConSenal son los que alguna inferencia marcó. Es una CUENTA de
	// sospechas, nunca de ataques confirmados.
	OrigenesConSenal int
}

// Resumir compone la cabecera a partir de los eventos ya filtrados.
func Resumir(eventos []Evento, origenes []Origen, totalHistorico int64) Resumen {
	r := Resumen{
		Eventos:        len(eventos),
		TotalHistorico: totalHistorico,
		IPsUnicas:      len(origenes),
	}
	rutas := make(map[string]int)
	for _, e := range eventos {
		rutas[e.Ruta]++
		if e.Motivo == CredencialIncorrecta {
			r.AutenticacionFallida++
		}
	}
	for _, o := range origenes {
		if o.Red.DeFuera() {
			r.DesdeFuera++
		}
		if len(o.Senales) > 0 {
			r.OrigenesConSenal++
		}
	}
	r.RutasMasPedidas = masFrecuentes(rutas, TopeRutas)
	r.RutasVistas = len(rutas)
	return r
}

// DiasGrafica es cuántos días pinta «Actividad por día» en /seguridad.
//
// FIJO Y NO EL ANCHO DE LA VENTANA ELEGIDA, a propósito: la ventana del
// filtro decide QUÉ eventos entran, no cuántas barras se dibujan. Con
// «?horas=1» la serie sigue teniendo doce columnas — la mayoría en cero — en
// vez de encogerse a una sola, que sería confundir el filtro de la tabla con
// el alcance de la gráfica.
const DiasGrafica = 12

// Dia es un día con su recuento, para la gráfica de actividad de /seguridad.
type Dia struct {
	Fecha    time.Time
	Rechazos int
}

// PorDia agrupa los eventos YA FILTRADOS en los últimos DiasGrafica días de
// calendario local, terminando hoy. Devuelve también DESDE CUÁNDO alcanza lo
// que pinta: el más antiguo de los eventos recibidos, o el cero de time.Time
// si no hay ninguno — nunca se promete una serie más larga que los datos que
// la sostienen (mismo criterio que PaquetesDesde en panel_seguridad.go).
//
// SOLO SE PINTA CON LO QUE EL ANILLO RECUERDA. Esta función no sabe nada de
// Capacidad ni de acumulados por hora: si una ráfaga llenó el anillo y
// «eventos» solo alcanza tres días, la gráfica de doce columnas tendrá nueve
// en cero y Desde lo dirá. Una capa de acumulados que sostenga series más
// largas es requisito de una fase futura (el panel SIEM), no de esta.
func PorDia(eventos []Evento, ahora time.Time) (serie []Dia, desde time.Time) {
	hoy := ahora.Local().Truncate(24 * time.Hour)
	inicio := hoy.AddDate(0, 0, -(DiasGrafica - 1))

	porFecha := make(map[time.Time]int, DiasGrafica)
	for _, e := range eventos {
		f := e.Momento.Local().Truncate(24 * time.Hour)
		if f.Before(inicio) || f.After(hoy) {
			continue
		}
		porFecha[f]++
		if desde.IsZero() || e.Momento.Before(desde) {
			desde = e.Momento
		}
	}

	serie = make([]Dia, DiasGrafica)
	for i := range serie {
		f := inicio.AddDate(0, 0, i)
		serie[i] = Dia{Fecha: f, Rechazos: porFecha[f]}
	}
	return serie, desde
}

// sondasOrdenadas saca la evidencia del mapa, de la más repetida a la menos, y
// la recorta.
//
// EL DESEMPATE ES COMPLETO —ruta, método y estado— y no un criterio parcial:
// con la mitad de la clave fuera, dos sondas que solo difieren en el estado
// salen en el orden aleatorio del recorrido del mapa, y la evidencia baila
// entre recargas sin que nada haya cambiado. Es el mismo defecto que PorOrigen
// y masFrecuentes ya cierran con su tercer criterio, y aquí importa más:
// justificar una inferencia con una tabla que cambia sola no justifica nada.
func sondasOrdenadas(m map[claveSonda]*Sonda, tope int) []Sonda {
	out := make([]Sonda, 0, len(m))
	for _, sd := range m {
		out = append(out, *sd)
	}
	slices.SortFunc(out, func(x, y Sonda) int {
		if c := cmp.Compare(y.Veces, x.Veces); c != 0 {
			return c
		}
		if c := strings.Compare(x.Ruta, y.Ruta); c != 0 {
			return c
		}
		if c := strings.Compare(x.Metodo, y.Metodo); c != 0 {
			return c
		}
		return cmp.Compare(x.Estado, y.Estado)
	})
	if len(out) > tope {
		out = out[:tope]
	}
	return out
}

func masFrecuentes(m map[string]int, tope int) []Cuenta {
	out := make([]Cuenta, 0, len(m))
	for v, n := range m {
		out = append(out, Cuenta{Valor: v, Veces: n})
	}
	slices.SortFunc(out, func(x, y Cuenta) int {
		if c := cmp.Compare(y.Veces, x.Veces); c != 0 {
			return c
		}
		return strings.Compare(x.Valor, y.Valor) // determinismo, ver PorOrigen
	})
	if len(out) > tope {
		out = out[:tope]
	}
	return out
}

func ordenado(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for v := range m {
		out = append(out, v)
	}
	slices.Sort(out)
	return out
}

// itoa evita importar strconv solo para los textos de Explicacion.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// Destacar dice si una fila merece color en el panel.
//
// # POR QUÉ NO BASTA CON LA GRAVEDAD DEL MOTIVO
//
// Se vio en la primera captura real: casi toda la tabla salía en naranja
// porque RutaInexistente es «aviso», y lo que la producía eran los
// /favicon.ico y /apple-touch-icon.png que el iPhone y el Chrome del
// responsable piden solos. Un panel donde casi todo está resaltado no resalta
// nada — el mismo defecto que 00_RECTOR.md §12.5 persigue en los
// verificadores de este proyecto.
//
// La gravedad del motivo NO cambia: es un hecho y sigue siendo la que es, y
// el filtro por gravedad la sigue usando entera. Lo que cambia es el ÉNFASIS,
// que es una decisión de presentación y por eso vive aquí y no en Motivo:
//
//   - Atención SIEMPRE se destaca, venga de donde venga. Son CSRF, permiso
//     insuficiente, límite de intentos y recurso reservado: a ninguna se
//     llega navegando, así que desde la propia LAN también dicen algo.
//   - Desde Internet se destaca TODO, incluida la Rutina. No es una
//     inconsistencia: el resumen ya trata «alguna dirección de Internet»
//     como el propio objetivo del panel («ninguna» es el estado sano), así
//     que un simple «sin sesión» desde fuera ya es la señal — es la única
//     vez que se ve a alguien tocando la puerta, sea quien sea.
//   - Aviso y Rutina desde casa NO se destacan. Una ruta inexistente o una
//     contraseña fallada las produce cualquiera de casa todos los días.
func (o Origen) Destacar() bool {
	return o.Gravedad == Atencion || o.Red.DeFuera()
}

// Destacar, para una fila de la cronología. Mismo criterio y por eso mismo
// nombre: si divergieran, la tabla de arriba y la de abajo resaltarían cosas
// distintas del mismo suceso.
func (e Evento) Destacar() bool {
	return e.Motivo.Gravedad() == Atencion || e.Red.DeFuera()
}
