package aviso

import "time"

// La salud de la comunicación — porque un canal callado y uno roto se ven
// exactamente igual.
//
// # POR QUÉ ESTOS TIPOS VIVEN EN EL DOMINIO Y NO EN EL ADAPTADOR
//
// Los rellena el adaptador, que es quien habla con la red, y los lee el
// adaptador web para pintar /estado. Si vivieran en el primero, el segundo
// tendría que importarlo y quedarían dos adaptadores acoplados entre sí sin que
// ninguno sea el dominio del otro.
//
// Puestos aquí, los dos dependen del mismo sitio que ya dependen para todo lo
// demás de esta capa, y ninguno depende del otro. Es el mismo corte que ADR-0014
// aplica a almacen.Almacen: el puerto lo declara el dominio y los adaptadores lo
// cumplen desde los dos lados.
//
// # LAS DOS SALUDES NO SE FUNDEN EN UNA, Y ESO ES EL PUNTO
//
// «El nodo no reporta», «el testigo no está» y «el canal de avisos no está» son
// TRES cosas distintas, y el encargo pedía expresamente poder distinguirlas
// hasta donde sea técnicamente posible. Fundir las dos estructuras habría hecho
// imposible la única distinción que el nodo puede afirmar por sí mismo:
//
//	SaludCanal   dice si los avisos SALEN.
//	SaludLatido  dice si el latido SALE.
//
// Ninguna de las dos puede decir si el testigo externo los está RECIBIENDO —eso
// solo lo sabe el testigo— y por eso ninguna lo afirma.

// SaludCanal es lo que el nodo puede afirmar sobre la entrega de avisos.
type SaludCanal struct {
	// Configurado distingue «no hay canal» de «hay canal y no ha mandado
	// nada». Sin este campo, un nodo sin credencial y un nodo tranquilo
	// enseñarían los mismos ceros, que es la clase de ambigüedad que
	// seguridad.Historial.Hay ya cierra para el sensor.
	Configurado bool
	Nombre      string
	Entregados  int64
	Fallidos    int64
	// Descartados son los avisos que se tiraron por cola llena. Si esto no es
	// cero, el canal lleva tiempo sin poder entregar.
	Descartados   int64
	UltimoExito   time.Time
	UltimoIntento time.Time
	// UltimoError NUNCA lleva la URL ni el token del proveedor. Ver la cabecera
	// de adaptadores/canal/telegram.go.
	UltimoError string
	// SecretoRechazado es el único fallo que no se arregla esperando: pide ir a
	// cambiar la credencial. Se publica aparte para que el indicador pueda
	// decir QUÉ HACER en vez de solo que algo falla.
	SecretoRechazado bool
	EnCola           int
}

// SaludLatido es lo que el nodo puede afirmar sobre su propio latido.
//
// OJO CON LEERLO AL REVÉS: dice que el nodo CREE estar latiendo, no que el
// testigo lo esté recibiendo. Un latido que sale y se pierde por el camino
// aparece aquí como un éxito, y solo el testigo puede desmentirlo. Es
// exactamente por eso por lo que el testigo vive fuera (ADR-0074).
type SaludLatido struct {
	Configurado bool
	Latidos     int64
	Fallidos    int64
	UltimoExito time.Time
	UltimoError string
}
