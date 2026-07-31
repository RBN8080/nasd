package almacen

import "time"

// Entrada describe un archivo o un directorio del almacén.
//
// NO guarda contenido. Nunca. RES-01: 592 MB.
type Entrada struct {
	Ruta        RutaSegura
	Nombre      string
	EsDirectori bool
	Tamano      int64
	Modificado  time.Time
}

// Parcial describe una subida incompleta que vive en estado/parciales/.
//
// Existe para que una subida sobreviva al REINICIO DEL SERVICIO, no solo al
// cierre del navegador (ADR-0027). El desplegue de ADR-0029 aplica igual.
type Parcial struct {
	ID   string
	Ruta RutaSegura // destino final, anotado en disco junto al parcial
	// Total es el tamaño anunciado por el cliente. Sirve para saber cuándo
	// está completa; NO es la fuente del desplazamiento.
	Total int64
	// Escrito es el tamaño REAL del archivo parcial. Es el Upload-Offset:
	// no hay metadato de progreso que pueda desincronizarse (ADR-0027).
	Escrito int64
}
