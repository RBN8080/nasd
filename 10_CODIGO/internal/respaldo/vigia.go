package respaldo

import (
	"os"
	"sync"
	"time"
)

// El Vigía — quien abre el archivo, y el único trozo de este paquete que toca
// el disco.
//
// # POR QUÉ EL DISCO SE TOCA AQUÍ Y NO EN EL ADAPTADOR WEB
//
// Es el mismo reparto que aviso.CargarRegistro: el paquete conoce su propio
// archivo y nadie más tiene por qué saber cómo se lee. Lo que el adaptador
// recibe es un valor ya resuelto, así que evaluar() sigue siendo una función
// pura sobre valores y se prueba sin disco, igual que los demás indicadores.
//
// # POR QUÉ HAY CACHÉ, Y NO ES UNA OPTIMIZACIÓN PREMATURA
//
// instantaneaCompleta() la llama TAMBIÉN el flujo en vivo (ADR-0051), que
// produce un marco por segundo mientras alguien tenga /estado abierto. El
// cliente escribe este archivo TRES VECES AL DÍA. Sin caché serían 86 400
// lecturas diarias para obtener siempre lo mismo — exactamente el cálculo con
// el que 30_CLIENTE_RESPALDO §10.2 justifica que su icono resuelva el umbral
// una sola vez al arrancar.
//
// Treinta segundos es la vigencia: por debajo del minuto que tarda el ciclo de
// mantenimiento en volver a mirar, así que el aviso no se retrasa, y muy por
// encima de la cadencia del flujo en vivo.
const vigencia = 30 * time.Second

// Vigia lee el estado publicado por el cliente, con caché.
//
// El cero de este tipo NO se usa: se construye con NuevoVigia. Un Vigia con la
// ruta vacía es «no configurado», y entonces el indicador NO SE PINTA — un
// semáforo permanentemente gris enseña a ignorar el panel entero, que es lo
// que ADR-0065 corrigió retirando 24 elementos sin pregunta detrás.
type Vigia struct {
	ruta string

	mu     sync.Mutex
	leido  time.Time
	previo Lectura
}

// Lectura es lo que el Vigía puede afirmar en este momento: o un estado, o por
// qué no hay ninguno.
//
// LAS TRES SITUACIONES VAN SEPARADAS y no se funden en un error a secas,
// porque piden cosas distintas de quien mira:
//
//	Configurado=false  no se preguntó. No se pinta nada.
//	Presente=false     se preguntó y no hay archivo. El cliente nunca ha
//	                   publicado, o publica en otro sitio: es de configuración.
//	Error!=nil         hay archivo y no se puede usar. Eso sí es una avería.
type Lectura struct {
	Configurado bool   `json:"configurado"`
	Presente    bool   `json:"presente"`
	Estado      Estado `json:"estado"`
	// Error NO viaja en el JSON de /estado, y no es por esconderlo: un error de
	// Go serializa como «{}» y publicar un objeto vacío donde debería haber una
	// causa es peor que no publicar nada. La causa legible sale en el Valor del
	// indicador, que es donde alguien la va a leer.
	Error error `json:"-"`
}

// NuevoVigia devuelve el vigía de una ruta. Ruta vacía es «no configurado», y
// es un estado legítimo: un nodo sin cliente de respaldo funciona igual.
func NuevoVigia(ruta string) *Vigia { return &Vigia{ruta: ruta} }

// Leer devuelve la última lectura, releyendo el archivo si la caché caducó.
//
// NUNCA DEVUELVE UN ERROR AL LLAMANTE, y es a propósito: quien la llama es el
// que compone /estado, y una página de estado que no se pinta porque el archivo
// de otra máquina está raro sería el peor cambio posible. El problema viaja
// DENTRO del valor, que es donde se puede enseñar.
func (v *Vigia) Leer() Lectura {
	if v == nil || v.ruta == "" {
		return Lectura{}
	}
	return v.leerEn(time.Now())
}

// leerEn es Leer con el reloj inyectado, para que la caducidad de la caché se
// pueda probar sin dormir.
func (v *Vigia) leerEn(ahora time.Time) Lectura {
	v.mu.Lock()
	defer v.mu.Unlock()

	if !v.leido.IsZero() && ahora.Sub(v.leido) < vigencia {
		return v.previo
	}

	l := Lectura{Configurado: true}
	f, err := os.Open(v.ruta)
	if err != nil {
		// AUSENTE NO ES AVERÍA, y separarlo importa: mientras el cliente no
		// haya corrido ni una vez contra este nodo, el archivo no existe y eso
		// no es nada roto. Cualquier otro error de apertura —permisos, E/S— sí
		// se cuenta como avería.
		if !os.IsNotExist(err) {
			l.Error = err
		}
		v.leido, v.previo = ahora, l
		return l
	}
	defer f.Close()

	e, err := Leer(f)
	if err != nil {
		l.Presente = true
		l.Error = err
	} else {
		l.Presente = true
		l.Estado = e
	}
	v.leido, v.previo = ahora, l
	return l
}
