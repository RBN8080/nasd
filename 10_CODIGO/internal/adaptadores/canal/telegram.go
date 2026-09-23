package canal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"nasd/internal/aviso"
)

//

const (
	// plazoPorIntento acota UN intento, no la entrega entera.
	//
	// Diez segundos: la API responde en menos de uno desde una red, y
	// este plazo no está para el caso normal sino para el TCP que se queda
	// colgado sin cerrar. RNF-12 exige plazo declarado en toda E/S y aquí se
	// declara explícitamente en vez de heredar el de http.DefaultClient, que
	// NO TIENE NINGUNO — un cliente sin plazo total es una goroutine que puede
	// no volver nunca.
	plazoPorIntento = 10 * time.Second

	// intentos es cuántas veces se prueba un aviso reintentable.
	//
	// TRES, y no más: con la espera de abajo, tres intentos cubren un reinicio
	// del proveedor sin convertir un aviso en una tarea de fondo eterna. Lo que
	// no entre en tres intentos entra en el siguiente ciclo de vigilancia, que
	// vuelve a evaluar la misma situación si sigue viva.
	intentos = 3

	// esperaBase es el primer descanso entre intentos; se dobla y se acota.
	//
	// LA FÓRMULA ES LA MISMA QUE LA DE nas-mantener-nat —min(base·2ⁿ, 300 s)—
	// y no una propia: aquel proceso lleva dos semanas haciendo exactamente esto
	// contra una red que se cae, y copiar su comportamiento vale más
	// que inventar otro que habría que volver a calibrar.
	esperaBase = 2 * time.Second
	esperaTope = 300 * time.Second

	// topeCuerpoRespuesta acota lo que se lee del proveedor.
	//
	// El cuerpo de un error de Telegram son unos cientos de bytes. Sin tope,
	// un proveedor averiado —o un intermediario que devuelva una página de
	// error— decidiría cuánta memoria reserva nasd, que es la clase de cosa
	// que este proyecto acota en todas partes (topeAgente, topeRuta,
	// topeLineaLista).
	topeCuerpoRespuesta = 8 << 10
)

// ErrSecretoRechazado indica que el proveedor no acepta la credencial.
//
// Es un valor y no un texto para que la cola pueda distinguirlo sin comparar
// cadenas, igual que hace internal/seguridad con las barandillas de la lista.
// Importa porque es el único fallo que NO se arregla esperando: hay que ir a
// cambiar el secreto.
var ErrSecretoRechazado = errors.New("el proveedor rechazó la credencial del canal")

// Telegram entrega avisos por la Bot API.
type Telegram struct {
	cliente *http.Client
	// destino lleva el token DENTRO. Nunca se registra, nunca se devuelve en un
	// error y nunca se expone por un método.
	destino string
	chat    string
	// espera es el primer descanso entre intentos. Es un CAMPO y no la
	// constante directamente para que las pruebas puedan bajarlo a un
	// milisegundo: una prueba que tarda seis segundos en comprobar un reintento
	// es una prueba que alguien acaba borrando de «make verificar», y entonces
	// el reintento deja de estar cubierto.
	//
	// No se expone: NuevoTelegram lo fija siempre a esperaBase, así que en
	// producción no hay forma de tocarlo ni por descuido.
	espera time.Duration
}

// NuevoTelegram construye el canal. Falla ruidosamente y temprano (P5) en vez
// de dejar un canal a medias que solo se descubra roto al primer aviso — que
// sería, por definición, el peor momento.
func NuevoTelegram(token, chat string) (*Telegram, error) {
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("el canal de avisos necesita un token de bot")
	}
	if strings.TrimSpace(chat) == "" {
		return nil, errors.New("el canal de avisos necesita un identificador de chat")
	}
	return &Telegram{
		// Un cliente propio y no http.DefaultClient: el plazo es de este canal
		// y no debe poder cambiarlo nadie más del programa.
		cliente: &http.Client{Timeout: plazoPorIntento},
		destino: "https://api.telegram.org/bot" + strings.TrimSpace(token) + "/sendMessage",
		chat:    strings.TrimSpace(chat),
		espera:  esperaBase,
	}, nil
}

func (*Telegram) Nombre() string { return "telegram" }

// Enviar entrega el aviso, reintentando lo que se pueda reintentar.
//
// Respeta la cancelación del contexto en las esperas Y en la petición: al
// apagar el servicio, un aviso a medio entregar no puede retrasar el cierre
// ordenado que los historiales necesitan para volcarse.
func (t *Telegram) Enviar(ctx context.Context, a aviso.Aviso) error {
	var ultimo error
	espera := t.espera
	if espera <= 0 {
		espera = esperaBase
	}

	for intento := 1; intento <= intentos; intento++ {
		reintentable, sugerida, err := t.unIntento(ctx, a)
		if err == nil {
			return nil
		}
		ultimo = err
		if !reintentable {
			return err
		}
		if intento == intentos {
			break
		}
		// El proveedor manda sobre nuestra fórmula cuando dice cuánto esperar:
		// un 429 con retry_after es información, no una opinión.
		descanso := espera
		if sugerida > 0 {
			descanso = sugerida
		}
		if descanso > esperaTope {
			descanso = esperaTope
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(descanso):
		}
		if espera *= 2; espera > esperaTope {
			espera = esperaTope
		}
	}
	return fmt.Errorf("tras %d intentos: %w", intentos, ultimo)
}

// unIntento hace UNA llamada y clasifica lo que pase.
//
// Devuelve si merece la pena reintentar y cuánto pide esperar el proveedor,
// porque las dos cosas se saben aquí —donde está la respuesta— y no en quien
// reintenta.
//
// El error va el ÚLTIMO, que es la convención de Go y lo que staticcheck exige
// (ST1008). El orden importa poco para leerlo y mucho para que la puerta de
// «make verificar» —donde advertencia es error— siga en verde.
func (t *Telegram) unIntento(ctx context.Context, a aviso.Aviso) (reintentable bool, esperar time.Duration, err error) {
	campos := url.Values{}
	campos.Set("chat_id", t.chat)
	campos.Set("text", a.Texto)
	// HTML y no MarkdownV2: MarkdownV2 obliga a escapar dieciocho caracteres
	// distintos, varios de ellos frecuentes en una ruta (`-`, `.`, `_`), y cada
	// uno olvidado es un mensaje que el proveedor rechaza entero. HTML solo
	// interpreta <, > y &, que es exactamente lo que html.EscapeString cubre.
	campos.Set("parse_mode", "HTML")
	// Las URL de un aviso no deben generar tarjetas de vista previa: son rutas
	// de sondeo, no enlaces que alguien quiera abrir.
	campos.Set("disable_web_page_preview", "true")
	if a.Silencioso {
		campos.Set("disable_notification", "true")
	}

	pet, err := http.NewRequestWithContext(ctx, http.MethodPost, t.destino,
		strings.NewReader(campos.Encode()))
	if err != nil {
		// No se envuelve: el error de net/http lleva la URL, y la URL lleva el
		// token.
		return false, 0, errors.New("no se pudo preparar la petición al canal de avisos")
	}
	pet.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := t.cliente.Do(pet)
	if err != nil {
		// Tampoco se envuelve, y por lo mismo. Se distingue la cancelación
		// —que no es un fallo del proveedor— de un problema de red, que sí lo
		// es y se reintenta: DNS caído, sin ruta, TLS o el otro extremo mudo
		// entran todos aquí.
		if ctx.Err() != nil {
			return false, 0, ctx.Err()
		}
		return true, 0, errors.New("no se pudo alcanzar el canal de avisos (red, DNS o TLS)")
	}
	defer resp.Body.Close()

	cuerpo, _ := io.ReadAll(io.LimitReader(resp.Body, topeCuerpoRespuesta))
	if resp.StatusCode == http.StatusOK {
		return false, 0, nil
	}

	var r respuestaTelegram
	_ = json.Unmarshal(cuerpo, &r) // un cuerpo ilegible no cambia la decisión
	esperar = time.Duration(r.Parameters.RetryAfter) * time.Second

	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		// 401/403 — el token no vale, lo revocaron, o el bot no puede escribir
		// en ese chat. NO SE REINTENTA: esperando no se arregla, y el nodo
		// gastaría batería y cuota contra una puerta que no va a abrirse. Se
		// devuelve un valor reconocible para que la cola pueda encender el
		// indicador de /estado en vez de acumular fallos en silencio.
		return false, 0, fmt.Errorf("%w: %s", ErrSecretoRechazado, motivo(r, resp.StatusCode))

	case resp.StatusCode == http.StatusTooManyRequests:
		// 429 — hay cuota. Con 1-3 avisos al día previstos esto no debería
		// ocurrir nunca; si ocurre, es que algo aguas arriba está emitiendo de
		// más y conviene que quede escrito.
		return true, esperar, fmt.Errorf("el canal de avisos pide esperar: %s", motivo(r, resp.StatusCode))

	case resp.StatusCode >= 500:
		return true, esperar, fmt.Errorf("el canal de avisos está averiado: %s", motivo(r, resp.StatusCode))

	case resp.StatusCode == http.StatusBadRequest:
		// 400 — el mensaje está mal construido: marcado roto, texto demasiado
		// largo, chat inexistente. Reintentarlo daría exactamente el mismo
		// resultado. Es un DEFECTO NUESTRO y hay que verlo, no esconderlo tras
		// tres reintentos idénticos.
		return false, 0, fmt.Errorf("el canal de avisos rechazó el mensaje: %s", motivo(r, resp.StatusCode))
	}
	return false, 0, fmt.Errorf("respuesta inesperada del canal de avisos: %s", motivo(r, resp.StatusCode))
}

// respuestaTelegram es lo poco que hace falta leer de un error.
//
// Se modelan CUATRO campos y no la respuesta entera: lo demás no cambia
// ninguna decisión de este archivo, y un tipo que refleja una API ajena entera
// es un tipo que hay que mantener al ritmo de esa API.
type respuestaTelegram struct {
	OK          bool   `json:"ok"`
	ErrorCode   int    `json:"error_code"`
	Description string `json:"description"`
	Parameters  struct {
		RetryAfter int `json:"retry_after"`
	} `json:"parameters"`
}

// motivo compone un texto seguro para el diario.
//
// La descripción la escribe el proveedor y NO contiene el token —la Bot API no
// lo repite—, pero se acota igualmente: lo que entra en el diario del nodo
// desde fuera se acota siempre, por la misma razón que topeAgente existe.
func motivo(r respuestaTelegram, estado int) string {
	d := strings.TrimSpace(r.Description)
	if d == "" {
		return fmt.Sprintf("HTTP %d", estado)
	}
	const topeDescripcion = 200
	if len(d) > topeDescripcion {
		d = d[:topeDescripcion]
	}
	return fmt.Sprintf("HTTP %d: %s", estado, d)
}
