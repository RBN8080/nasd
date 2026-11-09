package web

import (
	"cmp"
	"context"
	"fmt"
	"net/http"
	"slices"
	"time"

	"nasd/internal/almacen"
	"nasd/internal/autenticacion"
)

// Subidas a medias — Archivos y Resumen (v1.135.0).
//
// Hasta aquí solo Resumen las enseñaba, con «reanudación automática», y no la
// había: el navegador solo reanudaba si se volvía a elegir el mismo archivo en
// la misma carpeta y desde el mismo navegador. Nada permitía terminarlas ni
// quitarlas; esperaban siete días a la expiración (ADR-0029).
//
// Ahora cada una lleva dos órdenes, que subida.js resuelve con los verbos tus
// que ya existían —ninguna ruta nueva—:
//
//	Reintentar  si llegaron todos los bytes, un PATCH vacío la publica; si
//	            no, se elige el archivo otra vez y se sigue donde iba.
//	Descartar   DELETE /subidas/{id}.
//
// Reintentar solo se ofrece en las propias: el superusuario no tiene el
// archivo de otra cuenta. Descartar, en todas las que se ven.

// enCursoSi es cuánto hace, como mucho, que llegó el último byte de una
// subida que se está enviando ahora: no se le ofrece descartar.
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
	EnCurso  bool
	Hace     string
	Caduca   string
}

// cuentaDeQuienMira es como el almacén anota al dueño de un parcial: vacío
// para el superusuario, el nombre de la cuenta para los demás.
func cuentaDeQuienMira(r *http.Request) string {
	if u := usuarioDe(r); u != autenticacion.NombreSuperusuario {
		return u
	}
	return ""
}

// subidasPendientes reúne las subidas a medias que alcanza alm, la más
// reciente primero. Con soloPropias se queda con las de quien mira: el
// almacén del superusuario las ve todas, y en Archivos solo interesan las
// suyas.
func (s *Servidor) subidasPendientes(ctx context.Context, alm almacen.Almacen, quien string, soloPropias bool) []subidaPendiente {
	ps, err := alm.Reanudables(ctx)
	if err != nil {
		s.reg.Warn("no se pudo revisar las subidas a medias", "error", err)
		return nil
	}
	slices.SortFunc(ps, func(a, b almacen.Parcial) int { return b.Modificado.Compare(a.Modificado) })

	var out []subidaPendiente
	for _, p := range ps {
		propia := p.Cuenta == quien
		if soloPropias && !propia {
			continue
		}
		inactiva := time.Since(p.Modificado)
		v := subidaPendiente{
			ID:       p.ID,
			Nombre:   p.Ruta.Nombre(),
			Carpeta:  "Inicio",
			Progreso: fmt.Sprintf("%s de %s", legibleBytes(uint64(max(p.Escrito, 0))), legibleBytes(uint64(max(p.Total, 0)))),
			Total:    p.Total,
			Completa: p.Escrito >= p.Total,
			EnCurso:  inactiva < enCursoSi,
			Hace:     "hace " + duracionCorta(inactiva),
		}
		if padre := p.Ruta.Padre(); !padre.EsRaiz() {
			v.Carpeta = padre.Rel()
		}
		if propia {
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
			v.Caduca = "se borra sola en " + duracionCorta(queda)
		} else {
			v.Caduca = "se borra en el próximo barrido"
		}
		out = append(out, v)
	}
	return out
}
