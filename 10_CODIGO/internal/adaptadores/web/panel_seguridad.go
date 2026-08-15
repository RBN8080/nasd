package web

import (
	"net/http"
	"strconv"
	"time"

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

type vistaSeguridad struct {
	Resumen  seguridad.Resumen
	Origenes []seguridad.Origen
	Eventos  []seguridad.Evento
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
	if red, ok := redDesde(q.Get("red")); ok {
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

	v := vistaSeguridad{
		Resumen:         resumen,
		Origenes:        origenes,
		Eventos:         cronologia,
		Horas:           int(horas),
		IP:              f.IP,
		Ruta:            f.Ruta,
		Motivo:          q.Get("motivo"),
		Gravedad:        q.Get("gravedad"),
		Red:             q.Get("red"),
		Ventanas:        opcionesDeVentana(int(horas)),
		Motivos:         opcionesDeMotivo(q.Get("motivo")),
		Gravedades:      opcionesDeGravedad(q.Get("gravedad")),
		Redes:           opcionesDeRed(q.Get("red")),
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
