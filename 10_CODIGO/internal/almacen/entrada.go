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
