package web

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"path"
	"slices"
	"time"

	"nasd/internal/almacen"
	"nasd/internal/autenticacion"
)

// Subidas a medias — solo en Resumen, con un menú ⋯ por subida (v1.136.0).
//
// En v1.135.0 salían también encima del listado de Archivos, y el responsable
// las quiere solo aquí: «solo en la ventana de resumen está bien y ahí que sea
// con un menú desplegable por cada archivo» (2026-11-09). Antes de eso, Resumen
// las enseñaba con «reanudación automática», y no la había.
//
// Las órdenes del menú las resuelve subida.js con los verbos tus que ya
// existían —ninguna ruta nueva—:
//
//	Reintentar  si llegaron todos los bytes, un PATCH vacío la publica; si
//	            no, se elige el archivo otra vez y se sigue donde iba.
//	Descartar   DELETE /subidas/{id}.
//
// Reintentar solo se ofrece cuando puede salir bien: en las propias, y si el
// destino sigue libre. El 2026-11-09 se reintentaron dos PORTAPAPELES.txt cuyo
// archivo ya estaba en el disco, y las dos acabaron en «ya existe».

// enCursoSi es cuánto hace, como mucho, que llegó el último byte de una
// subida que se está enviando ahora: no se le ofrece el menú.
const enCursoSi = time.Minute

type subidaPendiente struct {
	ID     string
	Nombre string
	// Carpeta es dónde se publicará, como la ve su dueño; Enlace la abre en
	// Archivos y va vacío si la subida es de otra cuenta.
	Carpeta string
	Enlace  string
	// Cuenta va vacía si la subida es de quien mira.
	Cuenta   string
	Progreso string
	// Total es lo que el navegador compara con el archivo que se elija al
	// reintentar: si no mide lo mismo, no es el mismo archivo.
	Total int64
	// Porcentaje va al ancho de un <rect> de SVG: la CSP descarta los estilos
	// en línea (ADR-0060).
	Porcentaje int
	// Completa: tiene todos sus bytes y solo falta publicarla.
	Completa bool
	// Ocupado: ya hay algo con ese nombre en el destino, así que publicarla
	// chocaría con RF-23. Solo cabe descartarla.
	Ocupado bool
	EnCurso bool
	Hace    string
	Caduca  string
}

// cuentaDeQuienMira es como el almacén anota al dueño de un parcial: vacío
// para el superusuario, el nombre de la cuenta para los demás.
func cuentaDeQuienMira(r *http.Request) string {
	if u := usuarioDe(r); u != autenticacion.NombreSuperusuario {
		return u
	}
	return ""
}

// subidasPendientes reúne las subidas a medias de todas las cuentas, la más
// reciente primero. Lee del almacén del superusuario, el único que las ve
// todas; Resumen es solo suyo.
func (s *Servidor) subidasPendientes(ctx context.Context, quien string) []subidaPendiente {
	ps, err := s.almacenRaiz.Reanudables(ctx)
	if err != nil {
		s.reg.Warn("no se pudo revisar las subidas a medias", "error", err)
		return nil
	}
	slices.SortFunc(ps, func(a, b almacen.Parcial) int { return b.Modificado.Compare(a.Modificado) })

	out := make([]subidaPendiente, 0, len(ps))
	for _, p := range ps {
		inactiva := time.Since(p.Modificado)
		v := subidaPendiente{
			ID:       p.ID,
			Nombre:   p.Ruta.Nombre(),
			Carpeta:  "Inicio",
			Progreso: fmt.Sprintf("%s de %s", legibleBytes(uint64(max(p.Escrito, 0))), legibleBytes(uint64(max(p.Total, 0)))),
			Total:    p.Total,
			Completa: p.Escrito >= p.Total,
			Ocupado:  s.destinoOcupado(ctx, p),
			EnCurso:  inactiva < enCursoSi,
			Hace:     "hace " + duracionCorta(inactiva),
		}
		if padre := p.Ruta.Padre(); !padre.EsRaiz() {
			v.Carpeta = padre.Rel()
		}
		if p.Cuenta == quien {
			v.Enlace = urlDeListado(p.Ruta.Padre())
		} else {
			v.Cuenta = cmp.Or(p.Cuenta, autenticacion.NombreSuperusuario)
		}
		if p.Total > 0 {
			v.Porcentaje = int(min(p.Escrito, p.Total) * 100 / p.Total)
		} else {
			v.Porcentaje = 100
		}
		if queda := inactividadParaExpirar - inactiva; queda > 0 {
			v.Caduca = "Se borra sola en " + duracionCorta(queda) + "."
		} else {
			v.Caduca = "Se borra en el próximo barrido."
		}
		out = append(out, v)
	}
	return out
}

// destinoOcupado mira si ya hay algo donde se publicaría la subida. La ruta
// del parcial es relativa a la carpeta de su cuenta; desde el almacén del
// superusuario, esa carpeta es homeUsers/<cuenta>.
func (s *Servidor) destinoOcupado(ctx context.Context, p almacen.Parcial) bool {
	destino := p.Ruta
	if p.Cuenta != "" {
		r, err := almacen.NuevaRuta(path.Join(homeUsersNombre, p.Cuenta, p.Ruta.Rel()))
		if err != nil {
			return false
		}
		destino = r
	}
	f, _, err := s.almacenRaiz.Abrir(ctx, destino)
	if err == nil {
		f.Close()
		return true
	}
	return errors.Is(err, almacen.ErrEsDirectorio)
}
