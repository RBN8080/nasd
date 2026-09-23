package canal

import (
	"context"

	"nasd/internal/aviso"
)

// Duplicado manda cada aviso por DOS canales: el de fuera y el diario.
//
// # POR QUÉ TODO LO QUE SALE SE ANOTA TAMBIÉN DENTRO
//
// Tres motivos, y ninguno es redundancia por gusto:
//
//  1. EL PROVEEDOR NO ES UN ARCHIVO. Telegram conserva los mensajes mientras
//     quiera y no es consultable desde el nodo. El diario del NAS sí: vive en
//     el disco de datos (ADR-0037) con unos veinte días reales de profundidad
//     medidos, y responde a un «journalctl -u nasd -g AVISO».
//
//  2. UN FALLO DEL CANAL EXTERIOR NO BORRA EL AVISO. La constancia queda
//     igualmente, que es lo que convierte una caída del proveedor en una
//     molestia en vez de en una pérdida.
//
// # EL ERROR QUE MANDA ES EL DEL PRINCIPAL
//
// El diario no puede fallar de forma interesante —escribe en journald y ya—, y
// si fallara no sería motivo para dar por no entregado un aviso que sí salió.
// La cola necesita saber si el mensaje LLEGÓ AL RESPONSABLE, y eso lo contesta
// solo el canal de fuera.
type Duplicado struct {
	principal Canal
	copia     Canal
}

// NuevoDuplicado envuelve el canal de fuera con la copia al diario.
func NuevoDuplicado(principal, copia Canal) *Duplicado {
	return &Duplicado{principal: principal, copia: copia}
}

// Nombre es el del canal principal: es el que decide si un aviso llega, y por
// tanto el que hay que nombrar en el indicador de /estado y en los errores.
func (d *Duplicado) Nombre() string { return d.principal.Nombre() }

// Enviar anota SIEMPRE y manda fuera después.
//
// El orden importa: la copia local se escribe ANTES de intentar la salida, para
// que quede constancia aunque el envío se cuelgue los diez segundos del plazo o
// el proceso muera en medio. Es el mismo criterio con el que expirarParciales
// registra antes de borrar —«si el borrado falla, al menos consta la
// intención»— y con el que ADR-0070 anota la conexión antes de colgarla.
func (d *Duplicado) Enviar(ctx context.Context, a aviso.Aviso) error {
	_ = d.copia.Enviar(ctx, a)
	return d.principal.Enviar(ctx, a)
}
