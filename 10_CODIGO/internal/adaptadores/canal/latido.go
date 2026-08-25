package canal

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"nasd/internal/aviso"
)

// El latido — y la única parte de todo este trabajo que no puede vivir aquí.
//
// # QUÉ ES UN LATIDO Y QUÉ NO ES
//
// Un latido dice UNA cosa: «este proceso sigue mandando señal». Quien tiene que
// concluir «el nodo debía reportar y dejó de hacerlo» es OTRO, fuera del nodo,
// porque un nodo apagado no manda nada — ni siquiera la noticia de que está
// apagado. Ese testigo externo es Healthchecks.io (ADR-0074), y la máquina de
// estados de disponibilidad —normal, ausencia temporal, pérdida prolongada,
// recuperación— vive ALLÍ, no aquí. Es lo que permite además ajustar el umbral
// sin redesplegar el binario.
//
// # LO QUE UN LATIDO NO DEMUESTRA, DICHO ANTES DE QUE NADIE LO SUPONGA
//
// NO es una prueba criptográfica de integridad ni una attestation. Un atacante
// con privilegios suficientes en el nodo puede seguir emitiéndolo
// indefinidamente con el servicio comprometido debajo, y el testigo lo vería
// verde. Lo que este mecanismo detecta es AUSENCIA, no compromiso, y las dos
// cosas no son lo mismo.
//
// Se dice aquí, en el código, y no solo en el ADR, porque es exactamente la
// clase de garantía que alguien da por hecha seis meses después.
//
// # POR QUÉ VIVE DENTRO DE nasd Y NO EN UN PROCESO APARTE
//
// Se estudió el modelo de fallos antes de decidirlo, y sale al revés de la
// intuición:
//
//	Fallo a detectar          Dentro de nasd    En proceso aparte
//	------------------------  ----------------  -----------------------------
//	Corte de corriente        sí                sí
//	Pérdida de conectividad   sí                sí
//	nasd caído o colgado      SÍ                NO -- seguiría latiendo con el
//	                                            servicio muerto: FALSO VERDE
//	Compromiso de root        no                no
//
// Un proceso aparte no aporta ninguna propiedad útil aquí y sí introduce una
// regresión: convertiría «el servicio está muerto» en un latido verde. Un
// binario más sin justificación es además lo que este proyecto no hace.
//
// Lo que un proceso aparte SÍ compraría —resistir un compromiso limitado al
// usuario nas— queda como riesgo aceptado y escrito: si alguien toma el proceso
// nasd, puede callar el latido, y eso se ve desde fuera como una ausencia.

const (
	// plazoLatido acota UN latido. Corto a propósito: si no responde en cinco
	// segundos, el siguiente sale en cinco minutos de todas formas, y lo que no
	// puede pasar es que se acumulen goroutines esperando.
	plazoLatido = 5 * time.Second

	// topeCuerpoLatido acota el diagnóstico que se adjunta.
	//
	// El testigo guarda hasta 100 kB por ping, pero lo que se manda son cinco
	// líneas. El tope existe porque el cuerpo lo compone otro paquete a través
	// de una función, y lo que cruza esa frontera se acota igual que se acota
	// todo lo que entra de fuera.
	topeCuerpoLatido = 2 << 10
)

// Latido manda una señal periódica al testigo externo.
type Latido struct {
	cliente *http.Client
	// destino lleva el identificador del check, que ES el secreto: quien lo
	// tenga puede falsificar latidos y mantener el testigo en verde con el nodo
	// muerto. Nunca se registra ni se devuelve en un error, igual que el token
	// del canal.
	destino   string
	intervalo time.Duration
	reg       *slog.Logger
	// cuerpo compone el diagnóstico que acompaña al latido. Puede ser nil.
	cuerpo func() string

	mu          sync.Mutex
	latidos     int64
	fallidos    int64
	ultimoExito time.Time
	ultimoError string
	racha       int
}

// NuevoLatido construye el emisor. Falla temprano si la URL no sirve, en vez de
// descubrirlo al primer tic (P5).
func NuevoLatido(destino string, intervalo time.Duration, cuerpo func() string, reg *slog.Logger) (*Latido, error) {
	destino = strings.TrimSpace(destino)
	if destino == "" {
		return nil, errors.New("el latido necesita la URL del testigo externo")
	}
	// SOLO https, y el «http://» se rechaza a propósito.
	//
	// La primera versión de esta comprobación aceptaba los dos y el mensaje de
	// error decía «debe empezar por https://»: el código y el texto se
	// contradecían, que es exactamente la clase de defecto silencioso que este
	// proyecto persigue. Se resuelve por el lado estricto porque ESTA URL ES EL
	// SECRETO —quien la tenga puede falsificar latidos— y mandarla en claro la
	// regalaría a cualquiera en el camino.
	//
	// Un testigo autoalojado en la LAN por HTTP sería el caso que lo reabriría.
	// No existe hoy, y este proyecto no escribe código por si acaso: cuando
	// exista, se reabre con su motivo.
	//
	// Se comprueba el prefijo y no se valida la URL entera con net/url: lo que
	// importa es que no sea el identificador del check pegado a secas, que es
	// el error de dedo probable al copiarlo.
	if !strings.HasPrefix(destino, "https://") {
		return nil, errors.New("la URL del testigo externo debe empezar por https://")
	}
	if intervalo <= 0 {
		return nil, errors.New("el intervalo del latido debe ser positivo")
	}
	return &Latido{
		cliente:   &http.Client{Timeout: plazoLatido},
		destino:   destino,
		intervalo: intervalo,
		cuerpo:    cuerpo,
		reg:       reg,
	}, nil
}

// Mantener late hasta que se cancele el contexto.
//
// # SE LATE UNA VEZ AL ARRANCAR, ANTES DEL PRIMER TIC
//
// Y no es una cortesía: el nodo tarda 60-80 s en alcanzar Internet tras
// arrancar (medido), y este servicio se reinicia con frecuencia. Esperar el
// primer intervalo completo sumaría cinco minutos de silencio a cada reinicio,
// que es tiempo que se le resta al margen del testigo por nada.
func (l *Latido) Mantener(ctx context.Context) {
	l.unLatido(ctx)
	t := time.NewTicker(l.intervalo)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			l.unLatido(ctx)
		}
	}
}

// unLatido manda una señal. NO REINTENTA, y es deliberado: el siguiente latido
// sale solo dentro de un intervalo, y el testigo tiene margen de sobra para
// varios perdidos. Reintentar aquí solo serviría para amontonar peticiones
// justo cuando la red no está.
func (l *Latido) unLatido(ctx context.Context) {
	var cuerpo io.Reader
	if l.cuerpo != nil {
		texto := l.cuerpo()
		if len(texto) > topeCuerpoLatido {
			texto = texto[:topeCuerpoLatido]
		}
		cuerpo = strings.NewReader(texto)
	}

	pet, err := http.NewRequestWithContext(ctx, http.MethodPost, l.destino, cuerpo)
	if err != nil {
		l.anotarFallo("no se pudo preparar el latido")
		return
	}
	pet.Header.Set("Content-Type", "text/plain; charset=utf-8")

	resp, err := l.cliente.Do(pet)
	if err != nil {
		if ctx.Err() != nil {
			return // apagado ordenado, no es un fallo del latido
		}
		// El error de net/http lleva la URL, y la URL lleva el secreto del
		// check: NO se envuelve. Misma regla que en telegram.go.
		l.anotarFallo("no se pudo alcanzar el testigo externo (red, DNS o TLS)")
		return
	}
	defer resp.Body.Close()
	// El cuerpo se descarta, pero se DRENA: sin leerlo, la conexión no vuelve
	// al pool y cada latido abriría una nueva. Con 288 latidos al día eso son
	// 288 apretones de TLS que no hacen falta.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, topeCuerpoRespuesta))

	if resp.StatusCode >= 400 {
		l.anotarFallo("el testigo externo rechazó el latido: HTTP " + resp.Status)
		return
	}

	l.mu.Lock()
	l.latidos++
	l.ultimoExito = time.Now()
	l.ultimoError = ""
	racha := l.racha
	l.racha = 0
	l.mu.Unlock()

	if racha > 0 {
		l.reg.Info("el latido vuelve a llegar al testigo externo", "latidos_perdidos", racha)
	}
}

func (l *Latido) anotarFallo(texto string) {
	l.mu.Lock()
	l.fallidos++
	l.ultimoError = texto
	l.racha++
	primero := l.racha == 1
	l.mu.Unlock()

	// UNA línea por racha, no una cada cinco minutos: una noche sin Internet
	// serían 96 líneas idénticas en el disco de datos. Es el mismo reparto que
	// usan la cola de avisos y anotarCierreFallido.
	if primero {
		l.reg.Warn("no se pudo emitir el latido al testigo externo", "error", texto)
	}
}

// Salud publica lo que el nodo sabe de su propio latido.
func (l *Latido) Salud() aviso.SaludLatido {
	if l == nil {
		return aviso.SaludLatido{}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return aviso.SaludLatido{
		Configurado: true,
		Latidos:     l.latidos,
		Fallidos:    l.fallidos,
		UltimoExito: l.ultimoExito,
		UltimoError: l.ultimoError,
	}
}
