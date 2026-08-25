package aviso

import (
	"fmt"
	"html"
	"strings"
	"time"
)

// El mensaje — profesional, compacto y accionable, sin afirmar de más.
//
// # LO QUE ESTE ARCHIVO NO HACE
//
// NO filtra. Todo lo que llega aquí ya pasó por la minimización de evaluar.go,
// que es la única frontera de privacidad del paquete. Si el filtro viviera
// también aquí habría dos sitios capaces de equivocarse, y el segundo sería el
// que nadie revisa.
//
// # POR QUÉ ESCAPA TODO LO QUE INTERPOLA
//
// Porque las rutas las elige quien sondea. Una petición a «/<b>x&y» es
// perfectamente construible, y con parse_mode=HTML —que es como sale el
// mensaje— rompería el formato o inyectaría marcado en el móvil del
// responsable.
//
// No es un riesgo teórico en este proyecto: el 2026-08-14 un comentario HTML
// del propio código citando «{{fecha}}» fue EJECUTADO por html/template y
// produjo un 200 OK con la página cortada a la mitad. Aquella vez el texto era
// nuestro; aquí lo escribe un extraño.
//
// La regla es de lista positiva: el marcado lo pone ESTE archivo y nada más.
// Todo valor que venga de fuera —rótulos incluidos, aunque hoy sean
// constantes— pasa por html.EscapeString.

// topeMensaje es cuántos caracteres caben.
//
// # 3500 Y NO 4096, QUE ES EL LÍMITE REAL
//
// Telegram corta en 4096 caracteres UTF-8 DESPUÉS de interpretar las entidades
// (ADR-0074). Contar hasta el límite exacto obligaría a medir el texto ya
// renderizado, que es justo lo que no se puede hacer desde aquí: «&amp;» ocupa
// cinco caracteres en lo que se envía y uno en lo que se lee.
//
// El margen de 596 caracteres cubre el peor caso realista —un mensaje entero de
// rutas escapadas— sin tener que simular al proveedor. Se paga en texto que
// casi nunca se usa y compra que un aviso no se pierda por pasarse de largo.
const topeMensaje = 3500

// Aviso es un mensaje listo para entregar, sin saber por dónde saldrá.
//
// Es lo único que cruza la frontera hacia el adaptador: este paquete no conoce
// Telegram, y el adaptador no conoce situaciones ni severidades derivadas.
type Aviso struct {
	Severidad Severidad
	// Titulo es la línea corta que el proveedor pone como encabezado.
	Titulo string
	// Texto es el cuerpo, YA ESCAPADO y con el marcado de este archivo.
	Texto string
	// Silencioso pide que llegue sin sonar. Lo decide la severidad y no el
	// modo: el resumen aterriza en silencio y lo crítico vibra, pase lo que
	// pase (ver Severidad.Interrumpe).
	Silencioso bool
}

// Mensaje compone el aviso de una situación.
func Mensaje(s Situacion) Aviso {
	var b strings.Builder

	fmt.Fprintf(&b, "%s <b>%s · %s</b>\n\n",
		s.Severidad.Marca(), esc("SEGURIDAD"), esc(s.Severidad.Etiqueta()))
	fmt.Fprintf(&b, "<b>%s</b>\n\n", esc(s.Clase.Titulo()))

	for _, h := range s.Hechos {
		fmt.Fprintf(&b, "%s: %s\n", esc(h.Rotulo), esc(h.Valor))
	}

	// LA CUENTA ACUMULADA, y solo cuando dice algo. Una situación vista una vez
	// no necesita anunciar que se ha visto una vez; una vista cuarenta veces sí,
	// porque es la diferencia entre un incidente y una insistencia.
	if s.Veces > 1 {
		fmt.Fprintf(&b, "Observaciones: %s\n", esc(fmt.Sprintf("%d desde las %s",
			s.Veces, s.Primera.Format("15:04"))))
	}
	fmt.Fprintf(&b, "Estado del NAS: %s\n", esc(estadoDelNodo(s.Severidad)))

	fmt.Fprintf(&b, "\n<b>%s</b>\n%s", esc("Acción recomendada"), esc(s.Recomendacion()))

	return Aviso{
		Severidad:  s.Severidad,
		Titulo:     s.Severidad.Marca() + " NAS · " + s.Severidad.Etiqueta(),
		Texto:      acotar(b.String()),
		Silencioso: !s.Severidad.Interrumpe(),
	}
}

// Recomendacion es la acción concreta si la hay, y la de la clase si no.
func (s Situacion) Recomendacion() string {
	if strings.TrimSpace(s.Accion) != "" {
		return s.Accion
	}
	return s.Clase.Recomendacion()
}

// MensajeResumen compone el resumen periódico.
//
// SE EMITE AUNQUE TODO SEA CERO. Ver el comentario de cabecera de digest.go:
// con 8 peticiones de Internet en 21 días, el silencio del canal no puede
// distinguirse de su avería si el canal solo habla cuando hay algo.
func MensajeResumen(r Resumen) Aviso {
	var b strings.Builder

	horas := int(r.Hasta.Sub(r.Desde).Hours())
	fmt.Fprintf(&b, "%s <b>%s</b>\n\n", Verde.Marca(),
		esc(fmt.Sprintf("NAS · RESUMEN %d h", horas)))

	if r.Tranquilo() {
		// Una sola línea en vez de ocho ceros. Ocho ceros se dejan de leer a la
		// tercera vez, y entonces el resumen deja de cumplir su segunda función
		// —demostrar que el canal vive— justo por exceso de celo.
		fmt.Fprintf(&b, "%s\n", esc("Sin actividad externa, sin señales y sin hallazgos."))
	} else {
		linea(&b, "Conexiones de Internet", r.ConexionesInternet)
		linea(&b, "Orígenes distintos", r.OrigenesDeInternet)
		linea(&b, "Peticiones rechazadas", r.Rechazos)
		linea(&b, "Rutas inexistentes", r.RutasInexistentes)
		linea(&b, "Sondeos de software ajeno", r.SondasAjenas)
		linea(&b, "Orígenes con señal", r.OrigenesConSenal)
		linea(&b, "Cuarentenas aplicadas", r.Cuarentenas)
		linea(&b, "Hallazgos sobre el nodo", r.Hallazgos)
	}

	// LO RETENIDO VA ENTERO Y NO COMO NÚMERO. Es lo que distingue este resumen
	// de una estadística: una situación amarilla que el modo silencio no dejó
	// salir tiene que poder leerse, con su origen y su evidencia.
	if len(r.Retenidas) > 0 {
		fmt.Fprintf(&b, "\n<b>%s</b>\n", esc("Retenido por el modo silencio"))
		for _, s := range r.Retenidas {
			fmt.Fprintf(&b, "\n%s %s\n", s.Severidad.Marca(), esc(s.Clase.Titulo()))
			for _, h := range s.Hechos {
				fmt.Fprintf(&b, "  %s: %s\n", esc(h.Rotulo), esc(h.Valor))
			}
		}
		if r.RetenidasOmitidas > 0 {
			fmt.Fprintf(&b, "\n%s\n", esc(fmt.Sprintf(
				"…y %d más que no caben. Están todas en /seguridad.", r.RetenidasOmitidas)))
		}
	}

	// LA CIFRA QUE HACE COMPROBABLE LA PROMESA de esta capa. Sin ella,
	// «reducimos el ruido» es una afirmación que nadie puede verificar; con
	// ella, quien lea ve cuántas observaciones se absorbieron para producir
	// cuántos mensajes.
	if r.Avisadas > 0 || r.Absorbidas > 0 {
		fmt.Fprintf(&b, "\n%s\n", esc(fmt.Sprintf(
			"Avisos emitidos: %d · observaciones absorbidas sin avisar: %d",
			r.Avisadas, r.Absorbidas)))
	}

	fmt.Fprintf(&b, "\n%s", esc("El detalle completo está en /seguridad."))

	return Aviso{
		Severidad:  Verde,
		Titulo:     Verde.Marca() + " NAS · resumen",
		Texto:      acotar(b.String()),
		Silencioso: true,
	}
}

// MensajeCanalRecuperado avisa de que el propio canal estuvo mudo.
//
// Es el único naranja que nace en el nodo, y solo se puede entregar en el
// instante en que el canal vuelve — por eso no hay una versión «el canal está
// caído»: no habría por dónde mandarla.
func MensajeCanalRecuperado(desde time.Time, fallos int, ahora time.Time) Aviso {
	s := Situacion{
		Clase:     ClaseCanalRecuperado,
		Severidad: Naranja,
		Primera:   desde,
		Ultima:    ahora,
	}
	s.conHecho("Sin servicio desde", desde.Format("15:04 del 02/01"))
	s.conHecho("Duración", duracionCorta(ahora.Sub(desde)))
	s.conHecho("Entregas fallidas", fmt.Sprintf("%d", fallos))
	s.conHecho("Qué NO significa", "no dice nada sobre la seguridad del nodo: es el canal, no el NAS")
	return Mensaje(s)
}

func linea(b *strings.Builder, rotulo string, n int) {
	fmt.Fprintf(b, "%s: %s\n", esc(rotulo), esc(fmt.Sprintf("%d", n)))
}

// estadoDelNodo traduce la severidad a la frase que cierra el informe.
//
// Solo el rojo pide revisión. Un amarillo significa, por definición, que los
// controles siguieron funcionando —el nodo detectó, apartó y registró—, así que
// decir «revisión requerida» ahí sería alarmar por algo que ya se manejó.
func estadoDelNodo(s Severidad) string {
	if s == Rojo {
		return "Revisión requerida"
	}
	return "Protegido"
}

// esc es la única puerta por la que un valor entra al mensaje.
//
// html.EscapeString cubre <, >, &, ' y ", que es exactamente el conjunto que
// parse_mode=HTML interpreta. Es de la biblioteca estándar (P8, ADR-0013): no
// se escribe un escapador a mano para algo que ya está resuelto y probado.
func esc(s string) string { return html.EscapeString(s) }

// acotar recorta el mensaje al tope Y DICE que lo ha recortado.
//
// # SE CORTA POR LÍNEA Y SE AVISA, EN VEZ DE TRUNCAR A SECAS
//
// Un mensaje cortado a mitad de palabra se lee como un fallo del sistema; uno
// que dice «recortado» se lee como lo que es. Es el mismo criterio con el que el
// panel publica SondasVistas y RutasVistas junto a sus tablas: tres listas de la
// misma página no pueden tener distinta idea de la honestidad, y este mensaje
// tampoco.
//
// Se cuenta en RUNAS y no en bytes: el límite del proveedor son caracteres, y
// este texto lleva acentos y emojis, así que contar bytes recortaría de más.
func acotar(s string) string {
	if len([]rune(s)) <= topeMensaje {
		return s
	}
	const aviso = "\n\n…mensaje recortado. El detalle completo está en /seguridad."
	corte := topeMensaje - len([]rune(aviso))
	r := []rune(s)[:corte]
	// Hasta el último salto de línea, para no partir una línea de evidencia por
	// la mitad y dejarla ilegible.
	if i := strings.LastIndexByte(string(r), '\n'); i > 0 {
		return string([]rune(string(r)[:i])) + aviso
	}
	return string(r) + aviso
}
