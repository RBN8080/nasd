package web

import (
	"context"
	"time"

	"nasd/internal/almacen"
)

// Ciclo de vida de las subidas abandonadas — cierra los hallazgos 1, 2 y 3
// del análisis del 2026-07-31, que eran el mismo problema visto desde tres
// ángulos: NADA LIMPIABA LO QUE SE ABANDONA.
//
// Medido antes de esto: 20 subidas creadas y abandonadas dejaban 20
// descriptores abiertos que no se liberaban nunca, 20 entradas en memoria y
// 20 archivos en disco que sobrevivían incluso a un reinicio.
//
// Son tres controles distintos y hay que no confundirlos:
//
//  1. DESALOJO de memoria — libera el descriptor, CONSERVA el parcial.
//     Es barato y frecuente. Una subida desalojada sigue siendo reanudable.
//
//  2. EXPIRACIÓN en disco — BORRA el parcial. Es destructivo, poco frecuente
//     y siempre queda registrado (ADR-0029: nunca en silencio).
//
//  3. LÍMITE de subidas simultáneas — impide que se llegue a acumular.
//     Sin autenticación en la Fase 2, cualquiera en la LAN podía crear
//     subidas en bucle hasta agotar descriptores (04_SEGURIDAD.md §7, fila
//     Denegación, que estaba sin implementar).

const (
	// Tras este tiempo sin recibir bytes, la subida se saca de memoria y se
	// cierra su descriptor. El parcial SIGUE EN DISCO y es reanudable: el
	// siguiente HEAD o PATCH lo reabre. [R]
	inactividadParaDesalojar = 10 * time.Minute

	// Tras este tiempo sin recibir bytes, el parcial se BORRA del disco.
	//
	// ADR-0029: se expira por INACTIVIDAD, no por antigüedad. Una subida
	// legítima de 5 GB puede durar días con pausas, y la antigüedad
	// castigaría justo el caso que RF-12 existe para servir. [R]
	inactividadParaExpirar = 7 * 24 * time.Hour

	// Techo de subidas simultáneas en memoria. Con ~1 descriptor cada una y
	// 592 MB de RAM (RES-01), es holgado para el uso real —un usuario— y
	// acota el abuso. [R]
	maxSubidasEnCurso = 64

	intervaloMantenimiento = 5 * time.Minute
)

// Mantener arranca el ciclo de limpieza. Devuelve la función de parada.
//
// Se ejecuta FUERA del camino de las peticiones (RNF-04, RNF-11): ni bloquea
// un listado ni calienta la CPU en medio de una subida.
func (s *Servidor) Mantener(ctx context.Context) func() {
	ctxM, parar := context.WithCancel(ctx)
	go func() {
		// Una pasada al arrancar: si el proceso anterior murió a lo bruto,
		// puede haber quedado bastante que expirar.
		s.mantener(ctxM)
		t := time.NewTicker(intervaloMantenimiento)
		defer t.Stop()
		for {
			select {
			case <-ctxM.Done():
				return
			case <-t.C:
				s.mantener(ctxM)
			}
		}
	}()
	return parar
}

// almacenDeParciales es lo ÚNICO que la expiración necesita del almacén.
//
// Se declara aquí, estrecho a propósito: el mantenimiento no debe poder
// borrar archivos del usuario ni listar directorios, solo tratar con
// parciales. Y hace la lógica comprobable sin tocar disco.
type almacenDeParciales interface {
	Reanudables(context.Context) ([]almacen.Parcial, error)
	BorrarParcial(context.Context, string) error
}

func (s *Servidor) mantener(ctx context.Context) {
	if n := s.subidas.desalojarInactivas(inactividadParaDesalojar); n > 0 {
		// No es un aviso: desalojar es lo normal y no destruye nada.
		s.reg.Info("subidas desalojadas de memoria",
			"cuantas", n, "siguen_reanudables", true)
	}
	s.expirarParciales(ctx, s.almacen)
}

// expirarParciales borra del disco lo que lleva demasiado tiempo parado.
//
// ADR-0029, y sus reglas se cumplen al pie de la letra:
//   - por inactividad, nunca por antigüedad
//   - NUNCA en silencio: cada borrado deja su registro con qué era y cuánto
//   - jamás toca una subida que esté viva en memoria
func (s *Servidor) expirarParciales(ctx context.Context, a almacenDeParciales) {
	ps, err := a.Reanudables(ctx)
	if err != nil {
		s.reg.Warn("no se pudieron revisar los parciales", "error", err)
		return
	}

	var vivos, bytesVivos int64
	limite := time.Now().Add(-inactividadParaExpirar)

	for _, p := range ps {
		// existe() y NO buscar(): buscar refresca el uso, y al inspeccionar
		// cada parcial rejuvenecía las entradas, impidiendo el desalojo para
		// siempre. El propio control que protege lo vivo mataba la limpieza.
		if s.subidas.existe(p.ID) {
			vivos++
			bytesVivos += p.Escrito
			continue
		}
		if p.Modificado.After(limite) {
			vivos++
			bytesVivos += p.Escrito
			continue
		}
		// Se registra ANTES de borrar: si el borrado falla, al menos consta
		// la intención. Y si algún día se borró algo que hacía falta,
		// constará qué era (ADR-0029).
		s.reg.Warn("parcial expirado por inactividad",
			"id", p.ID,
			"ruta", p.Ruta.Rel(),
			"bytes", p.Escrito,
			"inactivo_horas", int(time.Since(p.Modificado).Hours()),
		)
		if err := a.BorrarParcial(ctx, p.ID); err != nil {
			s.reg.Error("no se pudo borrar el parcial expirado", "id", p.ID, "error", err)
		}
	}

	// Métrica que pedía ADR-0029: el tamaño acumulado es el síntoma
	// observable de que algo se está amontonando (charter §8, P7).
	if vivos > 0 {
		s.reg.Info("parciales en espera", "cuantos", vivos, "bytes", bytesVivos)
	}
}
