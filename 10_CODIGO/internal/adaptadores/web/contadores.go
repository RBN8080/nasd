package web

import (
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// Indicadores del SERVICIO — la mitad de producto de lo que exige el charter
// §8. La mitad de máquina la lee internal/adaptadores/sistema.
//
// # POR QUÉ CONTADORES EN MEMORIA Y NO UNA BASE DE MÉTRICAS
//
// No hay Prometheus ni nada que raspar: el charter §9.2 fija tres nodos y
// gestión artesanal, y D-05 ya rechazó Docker por 80–100 MB. Montar un
// almacén de series temporales para un servicio de 8 MB de RSS sería la misma
// desproporción, ahora en observabilidad (P8).
//
// Lo que sí hay son contadores atómicos, un extremo que los muestra y un
// diario donde queda la historia. Consecuencia asumida y escrita para que
// nadie la descubra de golpe: **los contadores se ponen a cero al reiniciar el
// servicio.** No son un histórico. Por eso cada instantánea lleva
// DesdeElArranque: un 100 % de disponibilidad sobre 4 peticiones y 30 s de
// vida no dice nada, y el extremo debe permitir verlo.
//
// # ESTADO COMPARTIDO
//
// ADR-0013 dejó vinculante documentar todo estado compartido entre goroutines.
// Este es el TERCERO del programa, tras el registro de subidas y el de
// sesiones. Es de tipo atomic.Int64, sin mutex, y por eso NO hay coherencia
// entre campos: dos contadores leídos en la misma instantánea pueden venir de
// instantes ligeramente distintos. Es aceptable —son indicadores, no
// contabilidad— y se dice aquí para que no se construya nada encima que
// necesite esa coherencia.

// umbralListadoLento marca a partir de cuándo un listado cuenta como lento.
//
// Sale del valor de REFERENCIA [R] de RNF-04 —«≤ 2 s con 10 000 entradas»—,
// que aquel requisito dejó explícitamente como orientativo y no como criterio
// de fallo. Aquí se usa como umbral del SLI-3, que es su sitio natural: un
// objetivo de operación sí puede llevar cifra, porque se mide contra una
// ventana y admite un porcentaje de incumplimiento.
const umbralListadoLento = 2 * time.Second

type contadores struct {
	arranque time.Time

	peticiones atomic.Int64
	exito      atomic.Int64 // 2xx y 3xx
	cliente    atomic.Int64 // 4xx
	servidor   atomic.Int64 // 5xx — el numerador del SLI-1

	listados       atomic.Int64
	listadosLentos atomic.Int64

	bytesSubidos     atomic.Int64
	bytesDescargados atomic.Int64

	subidasCreadas     atomic.Int64
	subidasConfirmadas atomic.Int64
	subidasFallidas    atomic.Int64 // SLI-2: Confirmar() devolvió error
	subidasDescartadas atomic.Int64
	subidasExpiradas   atomic.Int64

	borrados        atomic.Int64
	accesosFallidos atomic.Int64
}

func nuevosContadores() *contadores {
	return &contadores{arranque: time.Now()}
}

// Instantanea es la copia inmutable que se sirve y se serializa.
type Instantanea struct {
	DesdeElArranque string `json:"desde_el_arranque"`
	UptimeSegundos  int64  `json:"uptime_segundos"`

	Peticiones      int64 `json:"peticiones"`
	Exito           int64 `json:"exito_2xx_3xx"`
	ErroresCliente  int64 `json:"errores_4xx"`
	ErroresServidor int64 `json:"errores_5xx"`

	Listados       int64 `json:"listados"`
	ListadosLentos int64 `json:"listados_lentos"`

	BytesSubidos     int64 `json:"bytes_subidos"`
	BytesDescargados int64 `json:"bytes_descargados"`

	SubidasCreadas     int64 `json:"subidas_creadas"`
	SubidasConfirmadas int64 `json:"subidas_confirmadas"`
	SubidasFallidas    int64 `json:"subidas_fallidas"`
	SubidasDescartadas int64 `json:"subidas_descartadas"`
	SubidasExpiradas   int64 `json:"subidas_expiradas"`
	SubidasEnCurso     int   `json:"subidas_en_curso"`

	Borrados         int64 `json:"borrados"`
	AccesosFallidos  int64 `json:"accesos_fallidos"`
	SesionesAbiertas int   `json:"sesiones_abiertas"`
}

func (c *contadores) instantanea() Instantanea {
	uptime := time.Since(c.arranque)
	return Instantanea{
		DesdeElArranque: duracionLegible(uptime),
		UptimeSegundos:  int64(uptime.Seconds()),

		Peticiones:      c.peticiones.Load(),
		Exito:           c.exito.Load(),
		ErroresCliente:  c.cliente.Load(),
		ErroresServidor: c.servidor.Load(),

		Listados:       c.listados.Load(),
		ListadosLentos: c.listadosLentos.Load(),

		BytesSubidos:     c.bytesSubidos.Load(),
		BytesDescargados: c.bytesDescargados.Load(),

		SubidasCreadas:     c.subidasCreadas.Load(),
		SubidasConfirmadas: c.subidasConfirmadas.Load(),
		SubidasFallidas:    c.subidasFallidas.Load(),
		SubidasDescartadas: c.subidasDescartadas.Load(),
		SubidasExpiradas:   c.subidasExpiradas.Load(),

		Borrados:        c.borrados.Load(),
		AccesosFallidos: c.accesosFallidos.Load(),
	}
}

// anotarRespuesta clasifica una petición terminada. La llama el envoltorio de
// registro, así que TODA petición pasa por aquí sin que nadie tenga que
// acordarse en cada manejador.
func (c *contadores) anotarRespuesta(estado int, bytes int64, duracion time.Duration, clase claseDeRuta) {
	c.peticiones.Add(1)
	switch {
	case estado >= 500:
		c.servidor.Add(1)
	case estado >= 400:
		c.cliente.Add(1)
	default:
		c.exito.Add(1)
	}

	switch clase {
	case rutaListado:
		c.listados.Add(1)
		if duracion >= umbralListadoLento {
			c.listadosLentos.Add(1)
		}
	case rutaDescarga:
		// Solo lo que de verdad es contenido. Se exige un 2xx además de que
		// haya bytes: el cuerpo de un 404 o de un 400 también tiene tamaño,
		// y contarlo habría inflado los bytes descargados con las respuestas
		// de error —incluidas las de los intentos de salto de ruta—.
		if estado < 300 && bytes > 0 {
			c.bytesDescargados.Add(bytes)
		}
	}
}

// claseDeRuta agrupa las peticiones por lo que le importa a un SLI, no por el
// manejador que las atiende.
type claseDeRuta int

const (
	rutaOtra claseDeRuta = iota
	rutaListado
	rutaDescarga
)

func clasificar(metodo, ruta string) claseDeRuta {
	if metodo != "GET" {
		return rutaOtra
	}
	switch {
	case ruta == "/" || strings.HasPrefix(ruta, "/ver/"):
		return rutaListado
	case strings.HasPrefix(ruta, "/descargar/"):
		return rutaDescarga
	}
	return rutaOtra
}

// disponibilidad es el SLI-1: fracción de peticiones que NO fueron 5xx.
//
// Devuelve -1 cuando no hay peticiones todavía. No devuelve 100: un servicio
// recién arrancado no ha demostrado nada, y pintar un verde por ausencia de
// datos es el mismo defecto que este proyecto ya corrigió cuatro veces en sus
// propios verificadores (00_RECTOR.md §12.5).
func (i Instantanea) Disponibilidad() float64 {
	if i.Peticiones == 0 {
		return -1
	}
	return float64(i.Peticiones-i.ErroresServidor) * 100 / float64(i.Peticiones)
}

// IntegridadDeSubida es el SLI-2. Mismo criterio: -1 si no hay muestras.
func (i Instantanea) IntegridadDeSubida() float64 {
	total := i.SubidasConfirmadas + i.SubidasFallidas
	if total == 0 {
		return -1
	}
	return float64(i.SubidasConfirmadas) * 100 / float64(total)
}

// LatenciaDeListado es el SLI-3: fracción de listados por debajo del umbral.
func (i Instantanea) LatenciaDeListado() float64 {
	if i.Listados == 0 {
		return -1
	}
	return float64(i.Listados-i.ListadosLentos) * 100 / float64(i.Listados)
}

// duracionLegible evita imprimir «321h14m7.0000021s» en una pantalla que se
// mira desde el móvil.
func duracionLegible(d time.Duration) string {
	d = d.Round(time.Second)
	dias := int(d.Hours()) / 24
	horas := int(d.Hours()) % 24
	minutos := int(d.Minutes()) % 60
	segundos := int(d.Seconds()) % 60
	n := strconv.Itoa
	switch {
	case dias > 0:
		return n(dias) + " d " + n(horas) + " h " + n(minutos) + " min"
	case horas > 0:
		return n(horas) + " h " + n(minutos) + " min"
	case minutos > 0:
		return n(minutos) + " min " + n(segundos) + " s"
	default:
		return n(segundos) + " s"
	}
}
