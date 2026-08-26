package web

import (
	"fmt"
	"net/http"
	"time"

	"nasd/internal/adaptadores/sistema"
)

// Resumen — /resumen, el aterrizaje del superusuario (ADR-0075).
//
// SOLO EL SUPERUSUARIO, con la MISMA envoltura que /estado y por el mismo
// motivo: lo que aquí se enseña —disco, temperatura, cuentas, subidas
// interrumpidas de TODO el nodo— no informa de nada propio a una cuenta
// normal y le entregaría reconocimiento del sistema entero.
//
// NO MIDE NADA NUEVO. Cada cifra sale de una función que /estado,
// /administracion o el propio arranque ya calculan — sistema.Leer,
// s.usuarios.Lista, s.almacenRaiz.Reanudables, instantaneaCompleta. Esta
// página los reúne; no añade un solo punto de medición al nodo.
//
// NO TIENE PANEL DE DETALLE: no hay una fila de Resumen que abrir aparte —
// cada cifra enlaza a la página que la explica del todo.

type vistaResumen struct {
	// Todo lo que se pinta viene YA FORMATEADO (ADR-0017): el servidor decide
	// cómo se lee un byte y una duración, y la plantilla solo lo coloca.
	DiscoOK         bool
	DiscoUsado      string
	DiscoPorcentaje string

	Cuentas int

	TemperaturaOK  bool
	Temperatura    string
	TemperaturaPie string

	EnMarcha      string
	ServicioDesde string

	// LatidoOK distingue «no hay testigo configurado» de «lo hay y no ha
	// salido». Sin esa distinción, un nodo sin la capa de avisos y uno con el
	// testigo caído enseñarían el mismo hueco — la ambigüedad que ADR-0074
	// existe para cerrar.
	LatidoOK  bool
	Latido    string
	LatidoPie string

	// Interrumpidas son las subidas que quedaron a medias en CUALQUIER cuenta
	// — s.almacenRaiz.Reanudables, la misma consulta que Nuevo() ya hace una
	// vez al arrancar (servidor.go). Aquí se repite al abrir la página, no en
	// un flujo en vivo.
	Interrumpidas []subidaInterrumpida

	// CanalConfigurado y los suyos resumen la salud del canal externo con los
	// mismos datos de /estado, sin repetir su tabla entera: solo si está
	// configurado, y solo lo mínimo para saber si hace falta ir a mirar.
	CanalConfigurado bool
	CanalOK          bool
	CanalResumen     string

	Marco marco
	// ConDetalle es siempre falso: Resumen no tiene panel de detalle propio.
	ConDetalle bool
}

type subidaInterrumpida struct {
	Ruta     string
	Progreso string
	// Porcentaje va al ancho de un <rect> de SVG, y NO a un «style="width:…%"»:
	// la CSP no admite estilos en línea (ADR-0060) y uno se descartaría en
	// silencio, dejando la barra vacía sin que nada fallara a gritos.
	Porcentaje int
}

func (s *Servidor) verResumen(w http.ResponseWriter, r *http.Request) {
	n := sistema.Leer(r.Context(), s.volumen)
	// Esta lectura ya está pagada: se le pasa al rail para que no la vuelva a
	// hacer por su cuenta un minuto después. Ver volumenReciente (marco.go).
	s.anotarVolumen(n)
	inst := s.instantaneaCompleta()

	v := vistaResumen{
		Cuentas:       len(s.usuarios.Lista()),
		ServicioDesde: inst.DesdeElArranque,
		Marco:         s.construirMarco(r, "resumen", "Resumen", ""),
	}

	if d := n.Datos; d.Disponible && d.TotalBytes > 0 {
		v.DiscoOK = true
		v.DiscoUsado = legibleBytes(d.TotalBytes - d.LibresBytes)
		v.DiscoPorcentaje = fmt.Sprintf("%.0f %%", d.UsoPorcentaje())
	}

	if n.Vivo.TemperaturaOK {
		v.TemperaturaOK = true
		v.Temperatura = fmt.Sprintf("%.1f°", n.Vivo.TemperaturaC)
	}
	// El pie de la temperatura dice si el SoC está limitado AHORA, que es lo
	// único accionable: el bit pegajoso de «alcanzado alguna vez» ya está
	// aceptado por escrito en 00_RECTOR.md §4.5, y pintarlo aquí dejaría esta
	// tarjeta en ámbar para siempre — la ceguera al banner de D-16.
	switch t := n.Throttled; {
	case !t.Disponible:
		v.TemperaturaPie = "sin lectura de limitación"
	case t.LimitadoAhora || t.TermicoBlandoAhora:
		v.TemperaturaPie = "limitado ahora mismo"
	default:
		v.TemperaturaPie = "sin limitación activa"
	}

	if n.Vivo.UptimeOK {
		v.EnMarcha = duracionCorta(n.Vivo.Uptime)
	}

	// LO QUE ESTA TARJETA AFIRMA ES LO QUE EL NODO PUEDE AFIRMAR, y ni una
	// palabra más: que CREE haber emitido el latido. Si el testigo lo recibió
	// solo lo sabe el testigo (ADR-0074).
	if l := inst.Latido; l.Configurado {
		v.LatidoOK = true
		if l.UltimoExito.IsZero() {
			v.Latido, v.LatidoPie = "nunca", "todavía no ha salido ninguno"
		} else {
			v.Latido = duracionCorta(time.Since(l.UltimoExito))
			v.LatidoPie = "el nodo cree haberlo emitido"
		}
	} else {
		v.LatidoPie = "sin testigo configurado"
	}

	if ps, err := s.almacenRaiz.Reanudables(r.Context()); err != nil {
		s.reg.Warn("resumen: no se pudo revisar las subidas a medias", "error", err)
	} else {
		for _, p := range ps {
			pct := 0
			if p.Total > 0 {
				pct = int(p.Escrito * 100 / p.Total)
			}
			v.Interrumpidas = append(v.Interrumpidas, subidaInterrumpida{
				Ruta:       p.Ruta.Rel(),
				Progreso:   fmt.Sprintf("%s de %s", legibleBytes(uint64(p.Escrito)), legibleBytes(uint64(p.Total))),
				Porcentaje: pct,
			})
		}
	}

	if c := inst.Canal; c.Configurado {
		v.CanalConfigurado = true
		v.CanalOK = c.Fallidos == 0 && !c.SecretoRechazado
		v.CanalResumen = fmt.Sprintf("%d entregados · %d fallidos", c.Entregados, c.Fallidos)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := s.plantillas.ExecuteTemplate(w, "resumen.html", v); err != nil {
		s.reg.Error("render de /resumen", "error", err)
	}
}
