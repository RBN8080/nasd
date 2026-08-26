package web

import (
	"fmt"
	"net/http"

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
// cada cifra ya enlaza a la página que la explica del todo (/estado,
// /administracion, /seguridad).

type vistaResumen struct {
	// DiscoUsoPorcentaje, DiscoLibre y DiscoTotal vienen de sistema.Leer, la
	// MISMA fuente que evaluarVolumen en estado.go. Van ya formateados
	// (ADR-0017): el servidor decide cómo se lee un byte, no la plantilla.
	DiscoResumen string
	DiscoOK      bool

	Cuentas int

	TemperaturaResumen string
	TemperaturaOK      bool

	EnMarcha string

	// Interrumpidas son las subidas que quedaron a medias en CUALQUIER
	// cuenta — s.almacenRaiz.Reanudables, la misma consulta que Nuevo() ya
	// hace una vez al arrancar (servidor.go). Aquí se repite bajo demanda,
	// al abrir la página, no en un flujo en vivo.
	Interrumpidas []subidaInterrumpida

	// CanalAvisos resume la salud del canal externo — mismos datos que
	// /estado, sin repetir su tabla entera: solo si está configurado y solo
	// lo mínimo para saber si hace falta ir a mirar.
	CanalConfigurado bool
	CanalOK          bool
	CanalResumen     string

	Marco marco
	// ConDetalle es siempre falso: Resumen no tiene panel de detalle propio.
	// Existe solo para que _marco.html pueda preguntarlo sin que
	// html/template falle por campo inexistente.
	ConDetalle bool
}

type subidaInterrumpida struct {
	Ruta       string
	Progreso   string
	Porcentaje int
}

func (s *Servidor) verResumen(w http.ResponseWriter, r *http.Request) {
	n := sistema.Leer(r.Context(), s.volumen)
	inst := s.instantaneaCompleta()

	v := vistaResumen{
		Cuentas: len(s.usuarios.Lista()),
		Marco:   s.construirMarco(r, "resumen", "Resumen", ""),
	}

	if n.Datos.Disponible && n.Datos.TotalBytes > 0 {
		v.DiscoOK = true
		v.DiscoResumen = fmt.Sprintf("%.0f %% usado · %s libres de %s",
			n.Datos.UsoPorcentaje(), legibleBytes(n.Datos.LibresBytes), legibleBytes(n.Datos.TotalBytes))
	}
	if n.Vivo.TemperaturaOK {
		v.TemperaturaOK = true
		v.TemperaturaResumen = fmt.Sprintf("%.1f °C", n.Vivo.TemperaturaC)
	}
	if n.Vivo.UptimeOK {
		v.EnMarcha = duracionLegible(n.Vivo.Uptime)
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

	if inst.Canal.Configurado {
		v.CanalConfigurado = true
		v.CanalOK = inst.Canal.Fallidos == 0 && !inst.Canal.SecretoRechazado
		v.CanalResumen = fmt.Sprintf("%d entregados · %d fallidos", inst.Canal.Entregados, inst.Canal.Fallidos)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := s.plantillas.ExecuteTemplate(w, "resumen.html", v); err != nil {
		s.reg.Error("render de /resumen", "error", err)
	}
}
