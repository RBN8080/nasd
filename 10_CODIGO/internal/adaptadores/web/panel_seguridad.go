package web

import (
	"net/http"
	"strconv"
	"time"

	"nasd/internal/geoip"
	"nasd/internal/seguridad"
)

// Panel de seguridad — /seguridad, etapa 2.
//
// SOLO EL SUPERUSUARIO, con la MISMA envoltura que /estado y /administracion.
// El motivo es más fuerte aquí que allí: esta página publica las direcciones
// de origen de todo el que ha tocado el nodo. A un usuario normal no le
// informa de nada suyo y a cambio le entregaría el mapa de la vigilancia.
//
// NO LLEVA conAlmacen: no toca ni un archivo. Todo sale del anillo en memoria.
//
// NO LLEVA FLUJO EN VIVO, y es deliberado —al contrario que /estado
// (ADR-0051) y /administracion (ADR-0056)—. Un panel forense se lee, se
// filtra y se piensa; refrescarlo cuatro veces por segundo movería las filas
// bajo el cursor justo mientras se intenta leer una. Se recarga a mano, que
// es lo que uno hace de todos modos al cambiar un filtro.

// ventanaPorOmision es el «últimas 24 horas» que pidió el responsable.
const ventanaPorOmision = 24 * time.Hour

// ventanas ofrecidas. Se quedan en cuatro a propósito: con un anillo de 2000
// eventos, ofrecer «último mes» prometería una profundidad que la estructura
// no puede dar en un nodo con tráfico.
var ventanas = []struct {
	Etiqueta string
	Horas    int
}{
	{"1 hora", 1},
	{"24 horas", 24},
	{"7 días", 168},
	{"Todo lo guardado", 0},
}

// filaOrigen es un origen agregado MAS su procedencia resuelta.
//
// Existe para que internal/seguridad no dependa de internal/geoip: aquel
// paquete modela el hecho registrado y no tiene por que saber que existe una
// base de operadores. Quien une las dos piezas es este adaptador, igual que
// la raiz de composicion es quien une fsposix con web (ADR-0014).
type filaOrigen struct {
	seguridad.Origen
	// Geo esta vacio cuando no hay base instalada, cuando el origen no es de
	// Internet -una IP privada no tiene operador- o cuando la base no cubre
	// ese rango. La plantilla distingue los tres casos de un dato real.
	Geo      geoip.Info
	TieneGeo bool
}

// filaConectado es una dirección de Internet que abrió conexiones, con su
// procedencia resuelta. Existe por lo mismo que filaOrigen: unir el hecho con
// la base de operadores es trabajo del adaptador, no del dominio.
type filaConectado struct {
	seguridad.OrigenConectado
	Geo      geoip.Info
	TieneGeo bool
}

type vistaSeguridad struct {
	Resumen seguridad.Resumen
	// Conectados son las direcciones de Internet que ABRIERON CONEXIÓN, hayan
	// llegado o no a pedir algo. Va aparte de Origenes y no mezclado con él
	// porque son hechos de distinta naturaleza: aquello son rechazos de
	// peticiones, esto son conexiones. Ver internal/seguridad/conexiones.go.
	Conectados []filaConectado
	// Conexiones son las de la ventana; TotalConexiones, las de siempre. Las
	// dos cifras, por lo mismo que en el anillo de rechazos: un anillo lleno
	// no debe leerse como «esto es todo lo que ha pasado».
	Conexiones      int
	TotalConexiones int64
	Origenes        []filaOrigen
	Eventos         []seguridad.Evento
	// Filtro es lo aplicado, devuelto a la vista para que los campos del
	// formulario conserven lo tecleado tras recargar.
	Horas    int
	IP       string
	Ruta     string
	Motivo   string
	Gravedad string
	Red      string

	Ventanas   []opcionFiltro
	Motivos    []opcionFiltro
	Gravedades []opcionFiltro
	Redes      []opcionFiltro

	// TopeCronologico es cuántos eventos se listan en la vista cronológica.
	// Se publica para que la página pueda decir que está recortando en vez de
	// dar a entender que eso es todo.
	TopeCronologico int
	HayMas          bool
	// FechaGeo es cuando se preparo la base. Se publica porque se refresca
	// una vez al mes y puede ir por detras de la realidad: una base vieja que
	// no se anuncia es una afirmacion que dejo de ser verdad sin avisar.
	FechaGeo time.Time
	// HayGeo dice si la base esta instalada. La plantilla lo usa para NO
	// pintar una columna vacia que se leeria como «no se sabe de nadie»
	// cuando en realidad es «no se ha instalado la base».
	HayGeo bool
}

// opcionFiltro es un valor para un <select>: la clave estable que viaja por
// la URL y la etiqueta que se lee.
type opcionFiltro struct {
	Valor    string
	Etiqueta string
	Elegido  bool
}

// topeCronologico acota la lista de eventos que se PINTA, no la que se
// analiza: el resumen y la agrupación siguen viendo todo lo filtrado.
//
// Sin este tope, un sondeo de 2000 peticiones produciría una página de 2000
// filas que un Pi 3B+ tiene que renderizar y un móvil tiene que descargar —
// para no decir nada que la tabla de orígenes no diga ya mejor agregado.
const topeCronologico = 200

func (s *Servidor) verSeguridad(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	horas := ventanaPorOmision / time.Hour
	if v := q.Get("horas"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			horas = time.Duration(n)
		}
	}
	f := seguridad.Filtro{
		IP:   q.Get("ip"),
		Ruta: q.Get("ruta"),
	}
	// horas=0 significa «todo lo guardado»: Desde queda en el cero de
	// time.Time, que el anillo interpreta como sin límite inferior.
	if horas > 0 {
		f.Desde = time.Now().Add(-horas * time.Hour)
	}
	if m, ok := seguridad.MotivoDesde(q.Get("motivo")); ok && q.Get("motivo") != "" {
		f.Motivo = &m
	}
	if g, ok := gravedadDesde(q.Get("gravedad")); ok {
		f.Gravedad = &g
	}

	// EL PANEL ABRE EN «INTERNET» Y NO EN «TODO», por encargo del responsable
	// del 2026-08-16: «quisiera que lo de la lan y túnel wireguard quedara en
	// segundo lugar y únicamente reporte conexiones entrantes en internet».
	//
	// Segundo lugar, NO fuera: la LAN y el túnel siguen enteros a una opción
	// del desplegable. Lo que cambia es cuál es el estado limpio de la página.
	// Antes había que acordarse de filtrar para ver lo único que se declaró
	// importante, con la casa —que es casi todo el tráfico— tapándolo.
	//
	// q.Has y no q.Get: los dos devuelven "" para «no vino el parámetro» y
	// para «vino vacío», y aquí significan lo contrario. El <select> manda
	// siempre red= (vacío en «Cualquier origen»), así que elegir «todo» se
	// distingue de no haber elegido nada. Consecuencia buscada: «Quitar
	// filtros», que no manda campos, devuelve a Internet-solo.
	redElegida := seguridad.RedInternet.String()
	if q.Has("red") {
		redElegida = q.Get("red")
	}
	if red, ok := redDesde(redElegida); ok {
		f.Red = &red
	}

	eventos := s.seguridad.Filtrados(f)
	origenes := seguridad.PorOrigen(eventos)
	resumen := seguridad.Resumir(eventos, origenes, horas*time.Hour, s.seguridad.Total())
	// El resumen sabe si el anillo está lleno; la página lo dice para que un
	// anillo saturado no se lea como «esto es todo lo que ha pasado».
	resumen.Truncado = resumen.TotalHistorico > int64(seguridad.Capacidad)

	cronologia := eventos
	hayMas := false
	if len(cronologia) > topeCronologico {
		cronologia = cronologia[:topeCronologico]
		hayMas = true
	}

	// Las conexiones NO pasan por el Filtro: no tienen motivo, ni gravedad, ni
	// ruta que filtrar, y son de Internet por construcción. Lo único que
	// comparten con los rechazos es la VENTANA, y por eso es lo único que se
	// les aplica — inventarles los demás filtros sería ofrecer controles que
	// no pueden hacer nada.
	conexiones := s.conexiones.Desde(f.Desde)

	v := vistaSeguridad{
		Resumen:         resumen,
		Origenes:        s.resolverProcedencia(origenes),
		Conectados:      s.resolverConectados(seguridad.PorOrigenConectado(conexiones)),
		Conexiones:      len(conexiones),
		TotalConexiones: s.conexiones.Total(),
		HayGeo:          s.geo != nil,
		FechaGeo:        s.geo.Fecha(),
		Eventos:         cronologia,
		Horas:           int(horas),
		IP:              f.IP,
		Ruta:            f.Ruta,
		Motivo:          q.Get("motivo"),
		Gravedad:        q.Get("gravedad"),
		Red:             redElegida,
		Ventanas:        opcionesDeVentana(int(horas)),
		Motivos:         opcionesDeMotivo(q.Get("motivo")),
		Gravedades:      opcionesDeGravedad(q.Get("gravedad")),
		Redes:           opcionesDeRed(redElegida),
		TopeCronologico: topeCronologico,
		HayMas:          hayMas,
	}

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.plantillas.ExecuteTemplate(w, "seguridad.html", v); err != nil {
		s.reg.Error("render del panel de seguridad", "error", err)
	}
}

// Las tres funciones siguientes construyen las opciones de los desplegables a
// partir de los MISMOS tipos del dominio, recorriéndolos. Escribir las
// opciones a mano en la plantilla dejaría un motivo nuevo sin filtro y nadie
// se enteraría hasta necesitarlo.

func opcionesDeVentana(elegida int) []opcionFiltro {
	out := make([]opcionFiltro, 0, len(ventanas))
	for _, v := range ventanas {
		out = append(out, opcionFiltro{
			Valor:    strconv.Itoa(v.Horas),
			Etiqueta: v.Etiqueta,
			Elegido:  elegida == v.Horas,
		})
	}
	return out
}

func opcionesDeMotivo(elegido string) []opcionFiltro {
	out := []opcionFiltro{{Valor: "", Etiqueta: "Todos los motivos", Elegido: elegido == ""}}
	for m := seguridad.MotivoDesconocido; m <= seguridad.PeticionMalformada; m++ {
		out = append(out, opcionFiltro{
			Valor: m.String(), Etiqueta: m.Etiqueta(), Elegido: elegido == m.String(),
		})
	}
	return out
}

func opcionesDeGravedad(elegido string) []opcionFiltro {
	out := []opcionFiltro{{Valor: "", Etiqueta: "Cualquier gravedad", Elegido: elegido == ""}}
	for _, g := range []seguridad.Gravedad{seguridad.Rutina, seguridad.Aviso, seguridad.Atencion} {
		out = append(out, opcionFiltro{
			Valor: g.String(), Etiqueta: g.Etiqueta(), Elegido: elegido == g.String(),
		})
	}
	return out
}

func opcionesDeRed(elegido string) []opcionFiltro {
	out := []opcionFiltro{{Valor: "", Etiqueta: "Cualquier origen", Elegido: elegido == ""}}
	// Internet PRIMERO en el desplegable, por lo mismo que va primero en el
	// resumen: es el filtro que el responsable va a querer casi siempre.
	for _, red := range []seguridad.Red{
		seguridad.RedInternet, seguridad.RedTunel, seguridad.RedLocal, seguridad.RedNodo,
	} {
		out = append(out, opcionFiltro{
			Valor: red.String(), Etiqueta: red.Etiqueta(), Elegido: elegido == red.String(),
		})
	}
	return out
}

func gravedadDesde(s string) (seguridad.Gravedad, bool) {
	for _, g := range []seguridad.Gravedad{seguridad.Rutina, seguridad.Aviso, seguridad.Atencion} {
		if g.String() == s {
			return g, true
		}
	}
	return seguridad.Rutina, false
}

func redDesde(s string) (seguridad.Red, bool) {
	for _, red := range []seguridad.Red{
		seguridad.RedInternet, seguridad.RedTunel, seguridad.RedLocal, seguridad.RedNodo,
	} {
		if red.String() == s {
			return red, true
		}
	}
	return seguridad.RedDesconocida, false
}

// resolverProcedencia averigua pais y operador de cada origen.
//
// SOLO PARA LOS DE INTERNET, y por dos razones que se sostienen solas: una
// direccion privada -LAN, tunel, el propio nodo- no tiene operador que
// resolver, y ademas ensenar «el operador» junto a los propios aparatos del
// responsable seria ruido en la unica tabla que existe para mirar hacia
// fuera.
//
// SE RESUELVE AL PINTAR Y NO AL ANOTAR, igual que las senales: es dato
// derivado. Persistirlo en el evento congelaria el operador del dia en que
// llego, y refrescar la base mensualmente no corregiria el historial. Asi,
// cada vez que se mira se usa lo mejor que se sabe HOY.
//
// El coste es despreciable: son las ~20 lecturas de 16 bytes de una busqueda
// binaria por cada direccion distinta mostrada, no por evento.
// resolverConectados hace con las conexiones lo mismo que resolverProcedencia
// con los rechazos. NO comprueba DeFuera: aquí todo es de Internet por
// construcción del anillo, y repetir la comprobación daría a entender que
// puede haber otra cosa.
func (s *Servidor) resolverConectados(origenes []seguridad.OrigenConectado) []filaConectado {
	filas := make([]filaConectado, 0, len(origenes))
	for _, o := range origenes {
		f := filaConectado{OrigenConectado: o}
		f.Geo, f.TieneGeo = s.geo.Buscar(o.IP)
		filas = append(filas, f)
	}
	return filas
}

func (s *Servidor) resolverProcedencia(origenes []seguridad.Origen) []filaOrigen {
	filas := make([]filaOrigen, 0, len(origenes))
	for _, o := range origenes {
		f := filaOrigen{Origen: o}
		if o.Red.DeFuera() {
			f.Geo, f.TieneGeo = s.geo.Buscar(o.IP)
		}
		filas = append(filas, f)
	}
	return filas
}
