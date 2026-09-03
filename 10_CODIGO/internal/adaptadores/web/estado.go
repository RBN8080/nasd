package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"nasd/internal/adaptadores/sistema"
	"nasd/internal/respaldo"
)

// Extremo de estado — RF-24, charter §8 («Salud: endpoint de estado»).
//
// # POR QUÉ VA DETRÁS DE LA SESIÓN
//
// Se registra en el mux PROTEGIDO. No hay un /salud público al lado, y es una
// decisión, no un olvido:
//
//   - No hay nada que lo consuma sin credencial. No existe balanceador, ni
//     orquestador, ni sistema de monitorización que lo raspe: el charter §9.2
//     fija tres nodos y gestión artesanal. Quien mira esto es una persona con
//     un navegador, y esa persona tiene contraseña.
//   - La vivacidad del proceso ya la vigila systemd, mejor que un GET: Type=
//     notify más WatchdogSec, que reinicia si el latido se para. Un extremo
//     público añadiría una segunda respuesta a una pregunta ya contestada.
//   - Lo que muestra no es inocuo. Capacidad del disco, versiones de estado,
//     temperatura y ritmo de uso son reconocimiento gratis para quien esté en
//     la LAN, y sin TLS (ADR-0018) la LAN no equivale a confianza. P8: no se
//     regala superficie.
//
// Si algún día aparece algo que necesite sondear sin credencial, se abrirá un
// extremo mínimo —200 y nada más— con su ADR. No antes.
//
// # POR QUÉ UN VEREDICTO Y NO SOLO NÚMEROS
//
// El charter §8 exige que toda alerta sea accionable. Una rejilla de cifras no
// lo es: obliga a recordar qué valor era preocupante. Cada indicador lleva
// aquí su veredicto y, si no está bien, QUÉ HACER. La misma función la usa el
// ciclo de mantenimiento para avisar por el diario, de modo que la pantalla y
// la alerta no puedan discrepar nunca.

type veredicto string

// Etiqueta es el veredicto EN PALABRAS. Existe porque un semáforo que solo
// distingue por color no lo lee quien no distingue esos colores, y porque el
// valor crudo —«ok», «atencion»— es contrato: viaja en /estado?formato=json y
// en el diario, así que no se puede cambiar para arreglar una pantalla.
func (v veredicto) Etiqueta() string {
	switch v {
	case vOK:
		return "Correcto"
	case vAtencion:
		return "Atención"
	case vFallo:
		return "Fallo"
	case vDesconocido:
		return "Sin medir"
	}
	return ""
}

// Clase es la pastilla de la maqueta que le corresponde. La traducción vive
// aquí y no en cada plantilla para que las cuatro páginas que pintan un
// veredicto no puedan discrepar sobre qué color es «atención».
func (v veredicto) Clase() string {
	switch v {
	case vOK:
		return "p-ok"
	case vAtencion:
		return "p-av"
	case vFallo:
		return "p-fa"
	case vDesconocido:
		return "p-na"
	}
	// Sin veredicto no hay color: una fila de CONTEXTO —«Uso de CPU»— no
	// aprueba ni suspende, no es que se desconozca. La pastilla se queda
	// vacía y el CSS la esconde, pero el elemento sigue ahí para que el
	// flujo en vivo pueda escribir en él sin crear ni destruir nodos.
	return ""
}

const (
	vOK          veredicto = "ok"
	vAtencion    veredicto = "atencion"
	vFallo       veredicto = "fallo"
	vDesconocido veredicto = "desconocido"
)

// Umbrales. Son [R] —criterio de ingeniería, no medición— salvo donde se dice
// otra cosa. Cada uno lleva su motivo porque un umbral sin motivo se acaba
// moviendo para que deje de avisar.
const (
	// Disco: al 85 % conviene mirarlo; al 95 % una subida grande puede no
	// caber, y con copia única no hay a dónde desbordar.
	discoAtencion = 85.0
	discoFallo    = 95.0

	// RAM: RES-01 son 592 MB y RNF-01 exige que el servicio no crezca con el
	// tamaño del archivo. Si esto sube estando el servicio en reposo, lo que
	// se está comiendo la RAM es otra cosa.
	ramAtencion = 85.0

	// Térmica: el nodo alcanza el límite blando a ~60 °C (RES-06) y el duro
	// ronda los 80 °C. El primero está ACEPTADO en §4.5; el segundo no.
	tempAtencion = 70.0
	tempFallo    = 80.0

	// SLI-1, SLI-2 y SLI-3. Los objetivos viven en 05_OPERACION.md; aquí solo
	// están sus umbrales de aviso, y deben coincidir con aquel documento.
	sloDisponibilidad = 99.0
	sloListados       = 95.0

	// Aviso antes de tocar techo, no al tocarlo.
	fraccionAvisoSubidas = 0.8
)

type indicador struct {
	// Clave ancla la fila en el DOM para que el flujo en vivo sepa qué celda
	// refrescar (ADR-0051). Es un identificador, NO un rótulo: cambiarla deja
	// la fila congelada en la pantalla sin que nada falle a gritos.
	Clave     string    `json:"clave"`
	Nombre    string    `json:"nombre"`
	Valor     string    `json:"valor"`
	Veredicto veredicto `json:"veredicto"`
	// Accion es lo que hay que hacer si no está en «ok». Vacío cuando no hay
	// nada que hacer.
	Accion string `json:"accion,omitempty"`
}

// evaluar es la ÚNICA fuente de veredictos del producto.
//
// La usan la pantalla, el flujo en vivo y el ciclo de mantenimiento. Que sea
// una sola función es lo que garantiza que el diario y /estado no puedan
// contar cosas distintas.
//
// Se parte en dos mitades por COSTE DE MEDICIÓN, no por tema: lo que sale de
// sistema.Vivo se puede reevaluar varias veces por segundo, y lo que necesita
// statfs o vcgencmd no. Ver ADR-0051 y el tipo sistema.Vivo. La partición es
// solo de cálculo: sigue habiendo un único criterio por indicador.
func evaluar(n sistema.Nodo, i Instantanea) []indicador {
	out := append(evaluarVivos(n.Vivo, i), evaluarLentos(n)...)
	out = append(out, evaluarSalidas(i)...)
	return append(out, evaluarRespaldo(i, time.Now())...)
}

// horasParaAvisarPorOmision es el umbral que se usa si el cliente NO publica el
// suyo.
//
// 22 h, y sale de la cadencia medida del cliente (30_CLIENTE_RESPALDO
// §12.quindecies): tres ventanas al día dejan un hueco recibol de 12 h, y el
// peor hueco medido entre corridas buenas sobre 200 meses simulados fue de
// 18.4 h. Es el MISMO número que el icono de la barra del equipo.
//
// ES UN VALOR DE REPUESTO, NO EL CRITERIO. El criterio lo publica el cliente en
// su propio ESTADO.txt, porque el umbral vive en un archivo del equipo que este
// nodo no puede leer. Si el cliente lo cambiara y aquí quedara este número
// escrito a mano, las dos pantallas se contradirían sin que nada fallara — que
// es lo que 05_OPERACION §4.1 prohíbe. Cuando se usa este valor, el indicador
// LO DICE.
const horasParaAvisarPorOmision = 22

// evaluarRespaldo juzga el respaldo del cliente de Windows con lo que él mismo
// publicó en este nodo.
//
// # QUÉ PUEDE AFIRMAR EL NODO QUE EL EQUIPO NO PUEDE AFIRMAR DE SÍ MISMO
//
// Que lo último que llegó aquí es viejo. El icono de la barra del equipo se
// apaga con el equipo, y 30_CLIENTE_RESPALDO §10.2 ya declara ese punto ciego
// con todas las letras: «un icono ausente se parece a "todo bien"». Este nodo
// está encendido siempre y ya recibe el archivo, así que la pregunta le sale
// gratis.
//
// # SE PINTA SOLO SI ESTÁ CONFIGURADO
//
// Mismo criterio que las dos salidas de evaluarSalidas: un nodo sin cliente de
// respaldo no enseña un semáforo en gris para siempre (ADR-0065).
//
// # LA EDAD SE MIDE, PERO NO SE FÍA DEL RELOJ AJENO
//
// La marca la escribe el equipo con SU reloj y sin zona horaria. Si viniera del
// futuro, la resta daría una edad negativa que se leería como recién hecha —una
// mentira tranquilizadora, el peor tipo—. Se comprueba y se dice.
func evaluarRespaldo(i Instantanea, ahora time.Time) []indicador {
	if !i.Respaldo.Configurado {
		return nil
	}

	ind := indicador{Clave: "respaldo_cliente", Nombre: "Respaldo del equipo"}
	l := i.Respaldo

	switch {
	case l.Error != nil && !l.Presente:
		ind.Valor = "no se puede leer el estado publicado: " + l.Error.Error()
		ind.Veredicto = vFallo
		ind.Accion = "Compruebe permisos y E/S en la ruta de respaldo.estado del archivo de configuración."

	case !l.Presente:
		// Configurado y sin archivo. NO es avería: es lo normal hasta que el
		// cliente corra por primera vez contra este nodo. Mismo trato que un
		// canal configurado que aún no ha entregado nada.
		ind.Valor = "configurado, el cliente no ha publicado nada todavía"
		ind.Veredicto = vDesconocido

	case l.Error != nil:
		ind.Valor = "el estado publicado no se puede usar: " + l.Error.Error()
		ind.Veredicto = vAtencion
		ind.Accion = "El archivo está pero no se entiende. Abra el tablero en el equipo y lance una corrida."

	default:
		ind = veredictoDeRespaldo(ind, l.Estado, ahora)
	}

	return []indicador{ind}
}

// veredictoDeRespaldo decide sobre un estado que SÍ se pudo leer.
//
// EL ORDEN DE LOS CASOS ES UNA DECISIÓN, no el orden en que se ocurrieron: es
// la misma lección de aviso/evaluar.go, donde la precedencia tuvo que quedar
// escrita para que no dependiera del cortocircuito de un «||».
//
//  1. LA EDAD GANA A TODO. Un «Protegido» de hace tres días sigue diciendo
//     Protegido, porque es la foto de una corrida que salió bien —el archivo no
//     envejece solo—. Preguntar primero por el veredicto del cliente dejaría un
//     equipo apagado hace una semana pintado en verde, que es exactamente el
//     punto ciego que este indicador viene a cubrir.
//  2. Después lo que el cliente dijo de su corrida.
func veredictoDeRespaldo(ind indicador, e respaldo.Estado, ahora time.Time) indicador {
	umbral := e.HorasParaAvisar
	deRepuesto := ""
	if umbral <= 0 {
		umbral = horasParaAvisarPorOmision
		deRepuesto = fmt.Sprintf(" (el cliente no publicó su umbral; se usan %d h)", horasParaAvisarPorOmision)
	}

	if e.Momento.IsZero() {
		ind.Valor = "el estado publicado no dice cuándo se escribió"
		ind.Veredicto = vAtencion
		ind.Accion = "Lance una corrida desde el tablero del equipo para que vuelva a publicarse."
		return ind
	}

	edad := ahora.Sub(e.Momento)
	if edad < 0 {
		// El reloj del equipo va por delante del de este nodo. Se dice en vez
		// de enseñar «hace -3 h», que se leería como recién hecho.
		ind.Valor = fmt.Sprintf("la marca del equipo (%s) va por delante del reloj del nodo",
			e.Momento.Format("02/01 15:04"))
		ind.Veredicto = vAtencion
		ind.Accion = "Compare la hora del equipo con la del nodo: mientras no cuadren, la antigüedad del respaldo no se puede medir."
		return ind
	}

	if edad > time.Duration(umbral)*time.Hour {
		ind.Valor = fmt.Sprintf("sin corrida desde hace %s (avisa a las %d h)%s",
			duracionLegible(edad), umbral, deRepuesto)
		ind.Veredicto = vAtencion
		ind.Accion = "El equipo lleva sin respaldar más de lo que su cadencia promete. " +
			"Compruebe que está encendido y que su tarea programada sigue registrada."
		return ind
	}

	switch e.Veredicto {
	case "Falla":
		ind.Valor = fmt.Sprintf("la última corrida FALLÓ hace %s: %s", duracionLegible(edad), e.Detalle)
		ind.Veredicto = vFallo
		ind.Accion = "Abra el tablero en el equipo: lo copiado aquí es de antes de ese fallo."
	case "Atencion":
		// El freno de la capa 2 del cliente cae aquí. NO es rojo: es una parada
		// prudente que espera una decisión de una persona, y el cliente ya lo
		// separa así en su propio icono.
		ind.Valor = fmt.Sprintf("requiere atención desde hace %s: %s", duracionLegible(edad), e.Detalle)
		ind.Veredicto = vAtencion
		ind.Accion = "Abra el tablero en el equipo. Si fue el freno por tasa de cambio, no copia nada hasta que se autorice."
	case "Copiando":
		ind.Valor = "copiando ahora mismo"
		ind.Veredicto = vOK
	case "SinDatos":
		ind.Valor = "el cliente publicó que no tiene datos de su última corrida"
		ind.Veredicto = vDesconocido
	default:
		// Protegido, y cualquier palabra futura que el cliente estrene. Se
		// enseña la palabra tal cual en vez de traducirla a un verde silencioso:
		// si un día publica algo que aquí no se conoce, que se vea.
		ind.Valor = fmt.Sprintf("%s · hace %s · %s", e.Veredicto, duracionLegible(edad), e.Detalle)
		ind.Veredicto = vOK
		if e.Fallos > 0 {
			ind.Valor = fmt.Sprintf("%s · hace %s · %d raíces no se copiaron bien",
				e.Veredicto, duracionLegible(edad), e.Fallos)
			ind.Veredicto = vAtencion
			ind.Accion = "Abra el tablero en el equipo y mire qué raíces fallaron."
		}
	}
	return ind
}

// evaluarSalidas juzga las DOS vías por las que el nodo habla hacia fuera.
//
// # POR QUÉ ESTO ES UN INDICADOR Y NO UN ADORNO
//
// Un canal de avisos callado y uno roto se ven exactamente igual desde el sofá.
// Este nodo recibió 8 peticiones de Internet en 21 días medidos, así que el
// silencio es su estado NORMAL: sin estas dos filas no habría forma de
// distinguir «no ha pasado nada» de «lleva tres semanas sin poder entregar».
//
// Es el mismo defecto que ADR-0064 corrigió en el panel de seguridad —el
// control funcionaba y lo que faltaba era VERLO— aplicado a la capa nueva antes
// de que muerda.
//
// # SE PINTAN SOLO SI ESTÁN CONFIGURADAS
//
// Un nodo sin la capa instalada no enseña dos semáforos en gris. Un indicador
// permanentemente «desconocido» enseña a ignorar el panel, que es justo lo que
// ADR-0065 vino a corregir retirando 24 elementos sin pregunta detrás.
func evaluarSalidas(i Instantanea) []indicador {
	var out []indicador

	if i.Canal.Configurado {
		ind := indicador{Clave: "canal_avisos", Nombre: "Canal de avisos"}
		switch {
		case i.Canal.SecretoRechazado:
			// El único fallo que NO se arregla esperando, y por eso es el único
			// que sube a «fallo»: hay que ir a cambiar el secreto.
			ind.Valor = "credencial rechazada por el proveedor"
			ind.Veredicto = vFallo
			ind.Accion = "Regenere el token del bot y reescriba /etc/nasd/avisos; " +
				"después «sudo systemctl restart nasd». Hasta entonces no sale ningún aviso."
		case i.Canal.Descartados > 0:
			ind.Valor = fmt.Sprintf("%d avisos descartados por cola llena", i.Canal.Descartados)
			ind.Veredicto = vFallo
			ind.Accion = "El canal lleva tiempo sin poder entregar. Revise el diario " +
				"(«journalctl -u nasd -g 'cola de avisos'») y la conectividad del nodo."
		case i.Canal.Fallidos > 0 && i.Canal.UltimoExito.Before(i.Canal.UltimoIntento):
			// El último intento falló: está caído AHORA. Un fallo antiguo con un
			// éxito posterior no alarma, porque ya se recuperó.
			ind.Valor = "sin entregar: " + i.Canal.UltimoError
			ind.Veredicto = vAtencion
			ind.Accion = "Puede ser una caída pasajera del proveedor o de la red; se reintenta solo. " +
				"Si persiste, compruebe que el nodo alcanza Internet."
		case i.Canal.Entregados == 0:
			// Configurado y sin haber entregado nada todavía. NO es un fallo:
			// es lo normal en un nodo recién arrancado y tranquilo. Se dice
			// como desconocido, que es lo único que se puede afirmar.
			ind.Valor = "configurado, sin nada que entregar todavía"
			ind.Veredicto = vDesconocido
		default:
			ind.Valor = fmt.Sprintf("%d entregados · último a las %s",
				i.Canal.Entregados, i.Canal.UltimoExito.Format("15:04"))
			ind.Veredicto = vOK
		}
		out = append(out, ind)
	}

	if i.Latido.Configurado {
		ind := indicador{Clave: "latido", Nombre: "Latido al testigo externo"}
		switch {
		case i.Latido.Latidos == 0 && i.Latido.Fallidos > 0:
			// Nunca ha llegado NI UNO. Es la avería que deja al nodo sin
			// vigilancia externa entera, y por eso es fallo y no atención.
			ind.Valor = "ningún latido ha salido: " + i.Latido.UltimoError
			ind.Veredicto = vFallo
			ind.Accion = "Compruebe latido_url en /etc/nasd/avisos y que el nodo alcanza Internet. " +
				"Sin latido, una caída del nodo NO se detecta desde fuera."
		case i.Latido.UltimoError != "":
			ind.Valor = fmt.Sprintf("%d emitidos, el último falló: %s", i.Latido.Latidos, i.Latido.UltimoError)
			ind.Veredicto = vAtencion
			ind.Accion = "El testigo tolera varios latidos perdidos antes de alarmar (ADR-0074). " +
				"Si no se recupera, revise la conectividad."
		case i.Latido.Latidos == 0:
			ind.Valor = "configurado, todavía sin latir"
			ind.Veredicto = vDesconocido
		default:
			ind.Valor = fmt.Sprintf("%d emitidos · último a las %s",
				i.Latido.Latidos, i.Latido.UltimoExito.Format("15:04"))
			ind.Veredicto = vOK
		}
		out = append(out, ind)
	}
	return out
}

// evaluarVivos son los indicadores que se recalculan en cada muestreo.
func evaluarVivos(n sistema.Vivo, i Instantanea) []indicador {
	var out []indicador
	add := func(ind indicador) { out = append(out, ind) }

	// --- Nodo ---------------------------------------------------------------
	if n.TemperaturaOK {
		v, accion := vOK, ""
		switch {
		case n.TemperaturaC >= tempFallo:
			v = vFallo
			accion = "Límite térmico duro. Poner un disipador y reducir la carga. §4.5 aceptó el " +
				"límite BLANDO, no este"
		case n.TemperaturaC >= tempAtencion:
			v = vAtencion
			accion = "Subiendo hacia el límite duro. La mitigación es un disipador; consta como " +
				"riesgo aceptado en 00_RECTOR.md §4.5"
		}
		add(indicador{"temperatura", "Temperatura del SoC",
			fmt.Sprintf("%.1f °C", n.TemperaturaC), v, accion})
	} else {
		add(indicador{"temperatura", "Temperatura del SoC", "no disponible", vDesconocido, ""})
	}

	// RAM usada y RAM disponible eran DOS filas en dos tablas distintas y son
	// el mismo dato mirado del derecho y del revés. Se dicen juntas: el
	// porcentaje alerta, los megabytes libres son los que se entienden.
	if n.RAMOK {
		uso := n.RAMUsoPorcentaje()
		v := vOK
		accion := ""
		if uso >= ramAtencion {
			v = vAtencion
			accion = "Comprobar el RSS de nasd con «systemctl status nasd». Si crece con el " +
				"tamaño del archivo que se sube, RNF-01 está roto y es un defecto, no falta de RAM"
		}
		add(indicador{"memoria", "Uso de memoria",
			fmt.Sprintf("%.0f %% — %s libres de %s", uso,
				legibleBytes(n.RAMDisponibleBytes), legibleBytes(n.RAMTotalBytes)), v, accion})
	} else {
		add(indicador{"memoria", "Uso de memoria", "no disponible", vDesconocido, ""})
	}

	// --- Servicio -----------------------------------------------------------
	add(evaluarPorcentaje("disponibilidad", "Peticiones sin error del servidor (SLI-1)",
		i.Disponibilidad(), sloDisponibilidad,
		vAtencion, "Buscar los 5xx en el diario: «journalctl -u nasd -o json | grep '\"estado\":5'»"))

	// SLI-2 es el único con objetivo del 100 %: un archivo que se transfirió
	// entero y no quedó publicado es pérdida de datos, y con copia única no
	// hay segunda oportunidad.
	add(evaluarPorcentaje("integridad", "Subidas publicadas sin fallo (SLI-2)",
		i.IntegridadDeSubida(), 100,
		vFallo, "INCIDENTE. Revisar «subida confirmada» frente a los fallos de Confirmar() en el "+
			"diario y comprobar el estado del sistema de archivos con dmesg"))

	add(evaluarPorcentaje("latencia", "Listados por debajo de 2 s (SLI-3)",
		i.LatenciaDeListado(), sloListados,
		vAtencion, "Suele ser un directorio con muchísimas entradas (D-11 no acota el árbol). "+
			"Comprobar cuál con los tiempos del diario"))

	// --- Recursos internos --------------------------------------------------
	// Se lee del propio Instantanea y NO de un parámetro aparte: un tercer
	// argumento podía discrepar del campo y hacer que el panel dijera «0 de
	// 64» mientras el JSON de al lado decía otra cosa.
	v, accion := vOK, ""
	if float64(i.SubidasEnCurso) >= fraccionAvisoSubidas*maxSubidasEnCurso {
		v = vAtencion
		accion = "Cerca del techo de ADR-0029. Si no hay nadie subiendo nada, es acumulación de " +
			"subidas abandonadas: el barrido las desaloja, pero conviene mirar el origen"
	}
	add(indicador{"subidas-en-curso", "Subidas en curso",
		fmt.Sprintf("%d de %d", i.SubidasEnCurso, maxSubidasEnCurso), v, accion})

	return out
}

// evaluarLentos son los indicadores cuya medición cuesta: statfs y
// /proc/diskstats para los volúmenes, y un proceso hijo para la limitación del
// SoC. Se calculan al abrir la página y en cada ciclo de mantenimiento, no en
// el flujo en vivo.
func evaluarLentos(n sistema.Nodo) []indicador {
	return []indicador{
		evaluarVolumen("disco-datos", "Disco de datos", n.Datos,
			"Liberar espacio o borrar lo que ya no haga falta. Recuerde que no hay papelera (D-15)"),
		evaluarVolumen("disco-arranque", "Medio de arranque", n.Arranque,
			"P9: el medio de arranque es consumible. Reconstruir el nodo con 20_APROVISIONAMIENTO/; "+
				"el disco de datos sobrevive porque D-04 los mantiene separados"),
		evaluarThrottled(n.Throttled),
	}
}

func evaluarVolumen(clave, nombre string, v sistema.Volumen, accionEspacio string) indicador {
	if !v.Disponible {
		return indicador{clave, nombre, "no disponible", vDesconocido, ""}
	}

	// Un remontaje de solo lectura y los errores de ext4 pesan MÁS que la
	// ocupación: el disco puede estar medio vacío y aun así estar muriéndose.
	if v.SoloLecturaOK && v.SoloLectura {
		return indicador{clave, nombre, "MONTADO DE SOLO LECTURA", vFallo,
			"Síntoma clásico de medio agonizante. Mirar dmesg y actuar antes de escribir nada más. " +
				accionEspacio}
	}
	if v.ErroresOK && v.Errores > 0 {
		return indicador{clave, nombre, fmt.Sprintf("%d errores de ext4", v.Errores), vFallo,
			"El sistema de archivos ha registrado errores. Desmontar y pasar «fsck -f» cuanto antes"}
	}

	uso := v.UsoPorcentaje()
	valor := fmt.Sprintf("%.0f %% usado · %s libres de %s",
		uso, legibleBytes(v.LibresBytes), legibleBytes(v.TotalBytes))
	if v.VidaOK {
		// P9: lo escrito en toda la vida del sistema de archivos es la mejor
		// medida de desgaste que hay sin SMART, y sobrevive a los reinicios.
		valor += " · " + legibleBytes(v.EscrituraDeVidaBytes) + " escritos desde el formateo"
	}
	switch {
	case uso >= discoFallo:
		return indicador{clave, nombre, valor, vFallo, accionEspacio}
	case uso >= discoAtencion:
		return indicador{clave, nombre, valor, vAtencion, accionEspacio}
	}
	if !v.SoloLecturaOK {
		// Se dice en voz alta que falta una comprobación, en lugar de pintar
		// un verde que también significaría «no lo he mirado».
		valor += " · sin comprobar el remontaje de solo lectura"
	}
	return indicador{clave, nombre, valor, vOK, ""}
}

// evaluarThrottled distingue lo YA ACEPTADO de lo NUEVO, y esa distinción es
// el corazón de que este panel sirva para algo.
//
// El límite térmico blando ya se alcanzó en este nodo —0x80000 medido, §12.3—
// y el responsable lo aceptó por escrito en §4.5. Pintarlo en ámbar de forma
// permanente convertiría el panel en un semáforo que siempre está en ámbar, y
// entonces deja de mirarse justo cuando aparece algo de verdad. Es el mismo
// razonamiento que D-16 aplicó a la ceguera al banner.
//
// La subtensión es lo contrario: el charter §3.6 la midió a CERO, así que
// cualquier bit encendido ahí es información nueva y accionable.
func evaluarThrottled(t sistema.Throttled) indicador {
	// La envoltura evita repetir clave y nombre en las nueve salidas, que era
	// justo donde una de ellas podía quedarse con un rótulo distinto.
	ind := func(valor string, v veredicto, accion string) indicador {
		return indicador{"limitacion", "Limitación del SoC (throttling)", valor, v, accion}
	}

	if !t.Disponible {
		return ind("no disponible por ninguna vía", vDesconocido,
			"Comprobar a mano por SSH: «vcgencmd get_throttled». Si eso funciona y el "+
				"servicio no lo ve, le falta SupplementaryGroups=video en la unidad (ADR-0036)")
	}

	// La fuente parcial solo sabe de subtensión. Se dice, en vez de dejar que
	// los bits térmicos —que ahí valen siempre falso— se lean como «no ha
	// habido limitación térmica».
	if t.Parcial {
		if t.SubtensionAhora {
			return ind(t.Hex(), vFallo,
				"La alimentación no da. El charter §3.6 midió cero subtensión: esto es NUEVO. "+
					"Revisar fuente y cable antes de seguir escribiendo en el disco")
		}
		return ind(t.Hex()+" · lo térmico SIN MEDIR", vDesconocido,
			"Solo se está leyendo la alarma de subtensión del hwmon. Para la palabra completa "+
				"hace falta que la unidad lleve SupplementaryGroups=video (ADR-0036); "+
				"mientras tanto RNF-11 no se está midiendo desde el servicio")
	}

	switch {
	case t.SubtensionAhora:
		return ind(t.Hex()+" — SUBTENSIÓN AHORA", vFallo,
			"La alimentación no da. El charter §3.6 midió cero subtensión: esto es NUEVO. "+
				"Revisar fuente y cable antes de seguir escribiendo en el disco")
	case t.SubtensionOcurrida:
		return ind(t.Hex()+" — hubo subtensión desde el arranque", vAtencion,
			"No estaba en la medición del charter §3.6. Revisar la fuente")
	case t.LimitadoAhora, t.FrecuenciaCapadaAhora:
		return ind(t.Hex()+" — limitando ahora", vAtencion,
			"El nodo está rebajando frecuencia. Bajo carga es esperable sin disipador (RES-06)")
	case t.TermicoBlandoAhora:
		return ind(t.Hex()+" — límite térmico blando ACTIVO", vAtencion,
			"Está ocurriendo ahora mismo. La mitigación sigue siendo un disipador (§4.5)")
	case t.TermicoBlandoOcurrida:
		return ind(t.Hex()+" — límite térmico blando alcanzado alguna vez", vOK, "")
	}
	return ind(t.Hex()+" — sin limitación", vOK, "")
}

func evaluarPorcentaje(clave, nombre string, valor, objetivo float64, siFalla veredicto, accion string) indicador {
	if valor < 0 {
		// Sin muestras no se aprueba ni se suspende. Un 100 % sobre cero
		// peticiones es exactamente el defecto de «aprobar sin verificar» que
		// 00_RECTOR.md §12.5 lleva cuatro veces corrigiendo.
		return indicador{clave, nombre, "sin muestras todavía", vDesconocido, ""}
	}
	v := vOK
	if valor < objetivo {
		v = siFalla
	} else {
		accion = ""
	}
	return indicador{clave, nombre,
		fmt.Sprintf("%.2f %% (objetivo ≥ %.0f %%)", valor, objetivo), v, accion}
}

// ---------------------------------------------------------------------------
// Manejador
// ---------------------------------------------------------------------------

// grupoEstado es un bloque de la tabla con su rótulo. Son los MISMOS cuatro
// que la página ya tenía como encabezados —Nodo, Servicio, Almacenamiento y
// hardware— más las dos salidas, que hasta hoy solo existían en el JSON.
type grupoEstado struct {
	Rotulo string
	Filas  []filaViva
}

type vistaEstado struct {
	// Grupos es la tabla entera. Los dos primeros los mantiene el flujo en
	// vivo (ADR-0051); sin JavaScript se quedan en esta foto, que es correcta.
	Grupos []grupoEstado
	// Peor es el veredicto más grave de todos: lo que se lee de un vistazo.
	Peor   veredicto
	Avisos []string
	// PuedeAdministrar decide si el rail lleva a Cuentas. Misma regla y mismo
	// motivo que en el listado (acotadoPorRed, sesion.go).
	PuedeAdministrar bool
	// Novedades es la marca de «Seguridad» en el rail.
	Novedades int
	// Version es lo único que queda en el pie: qué binario está corriendo.
	// Viene por campos —nombre, revisión, fecha, huella— y no como una sola
	// cadena, para que la plantilla pueda impedir que se rompa uno por dentro
	// al envolver. Ver versionDelBinario.
	Version []string
	// Marco es el cromo compartido — ADR-0075.
	Marco marco
	// Detalle es el indicador seleccionado por «?ind=», o nil si no vino el
	// parámetro o no coincide con ninguno de los quince. NUNCA se inventa uno
	// para una clave que no exista: el panel se queda cerrado, que es la
	// misma regla con la que el resto de esta página no rellena lo que no
	// pudo medir (ver Avisos).
	Detalle    *filaViva
	ConDetalle bool
}

// filaViva es una fila de las dos tablas que se refrescan solas.
//
// ADR-0017 pone el render en el servidor y eso incluye el FORMATO: aquí el
// valor ya viene escrito tal cual se lee en pantalla. El navegador no calcula
// ni compone nada, solo sustituye texto — que es lo que permite que la página
// sin JavaScript y el flujo digan exactamente lo mismo.
type filaViva struct {
	// Clave es el ancla en el DOM. Es un identificador y no un rótulo: si
	// cambia sin cambiar la plantilla, el navegador deja de encontrar la celda
	// y la fila se queda congelada sin que nada falle a gritos.
	Clave string `json:"clave"`
	// Nombre NO viaja en el flujo: no cambia nunca y repetirlo cuatro veces
	// por segundo sería pagar ancho de banda por una constante.
	Nombre    string    `json:"-"`
	Valor     string    `json:"valor"`
	Veredicto veredicto `json:"veredicto,omitempty"`
	Accion    string    `json:"accion,omitempty"`
	// Etiqueta y Clase son el veredicto YA TRADUCIDO a lo que se pinta.
	// Viajan en el marco por lo mismo que Valor: ADR-0017 pone el formato en
	// el servidor, así que el navegador no traduce «ok» a «Correcto» ni a un
	// color — sustituye texto y clase, y no puede inventarse una tercera
	// versión de la misma tabla.
	Etiqueta string `json:"etiqueta,omitempty"`
	Clase    string `json:"clase,omitempty"`
	// Seleccionada marca la fila cuyo detalle está abierto. No viaja: es de
	// esta carga y no del flujo.
	Seleccionada bool `json:"-"`
}

// conVeredictoPintado rellena Etiqueta y Clase de una tanda de filas. Se llama
// en UN sitio —al final de filasVivas— para que ninguna fila pueda salir con
// veredicto y sin su traducción.
func conVeredictoPintado(filas []filaViva) []filaViva {
	for i := range filas {
		if filas[i].Veredicto == "" {
			continue
		}
		filas[i].Etiqueta = filas[i].Veredicto.Etiqueta()
		filas[i].Clase = filas[i].Veredicto.Clase()
	}
	return filas
}

// contexto es una fila sin semáforo: no dispara alertas por sí sola, pero es
// lo que se mira cuando una alerta ya saltó.
func contexto(clave, nombre, valor string) filaViva {
	return filaViva{Clave: clave, Nombre: nombre, Valor: valor}
}

const sinMedida = "no disponible"

// filasVivas arma las dos tablas que se refrescan solas.
//
// ES LA ÚNICA FUNCIÓN QUE DECIDE QUÉ SE VE Y CÓMO SE ESCRIBE EN ELLAS, y la
// usan las dos vías: el render inicial de la plantilla y cada marco del flujo.
// Que sea una sola es lo que impide que la página y el flujo discrepen, por el
// mismo motivo por el que evaluar() es una sola.
func filasVivas(n sistema.Vivo, i Instantanea) (nodo, servicio []filaViva) {
	// Los indicadores ya traen valor formateado y veredicto; aquí solo se
	// colocan en su sitio, intercalados con las filas de contexto que les dan
	// sentido. Un porcentaje de CPU al lado de la temperatura explica la
	// temperatura; en dos tablas distintas, no explicaba nada.
	porClave := make(map[string]indicador, 8)
	for _, ind := range evaluarVivos(n, i) {
		porClave[ind.Clave] = ind
	}
	// La conversión directa vale porque los dos tipos tienen exactamente los
	// mismos campos. Siguen siendo tipos distintos a propósito —un indicador
	// lleva veredicto por definición y una fila de contexto no— y si algún día
	// dejan de coincidir, esta línea deja de compilar en vez de fallar callada.
	deIndicador := func(clave string) filaViva {
		return comoFila(porClave[clave])
	}

	// conContexto pega las cifras crudas al valor de un indicador.
	//
	// TRES PARES DE FILAS DECÍAN LO MISMO DOS VECES: el conteo y su división.
	// «Peticiones HTTP: N · correctas · rechazadas · con error» iba seguida de
	// «Peticiones sin error (SLI-1): 99.87 %», que es esa misma fila dividida.
	// Igual con listados y con subidas. Se fusionan en la fila del indicador,
	// que es la que lleva veredicto y acción.
	//
	// LA FUSIÓN ES DE PRESENTACIÓN Y VIVE AQUÍ, no en evaluar(): allí el mismo
	// indicador alimenta las alertas del diario (mantenimiento.go), donde el
	// conteo sería ruido —lo accionable es el porcentaje— y donde el texto se
	// lee sin tabla alrededor.
	conContexto := func(clave, cifras string) filaViva {
		f := deIndicador(clave)
		f.Valor += " · " + cifras
		return f
	}

	// --- El nodo ------------------------------------------------------------
	cpu := sinMedida
	if n.CPUOK {
		cpu = fmt.Sprintf("%.0f %%", n.CPU)
	}
	frecuencia := sinMedida
	if n.FrecuenciaOK {
		frecuencia = fmt.Sprintf("%d de %d MHz", n.FrecuenciaMHz, n.FrecuenciaMaxMHz)
		if n.FrecuenciaMHz < n.FrecuenciaMaxMHz {
			// Por debajo del máximo puede ser ahorro en reposo o limitación
			// térmica; el indicador de limitación lo distingue, esto informa.
			frecuencia += " (por debajo del máximo)"
		}
	}
	encendido := sinMedida
	if n.UptimeOK {
		encendido = duracionLegible(n.Uptime)
	}

	nodo = []filaViva{
		deIndicador("temperatura"),
		deIndicador("memoria"),
		contexto("cpu", "Uso de CPU", cpu),
		contexto("frecuencia", "Frecuencia de CPU", frecuencia),
		// La CARGA MEDIA se retiró (ADR-0065): tres cifras con «Uso de CPU»
		// justo encima, en un nodo de un solo usuario. Sigue en el JSON, que es
		// donde la buscaría quien sepa leerla.
		contexto("encendido", "Tiempo de actividad del nodo", encendido),
	}

	// --- El servicio --------------------------------------------------------
	// PASÓ DE DOCE FILAS A CINCO (ADR-0065). Tres se fusionaron con su
	// porcentaje —ver conContexto— y cuatro se retiraron por no contestar nada
	// que esta pantalla deba contestar:
	//
	//   - «Intentos de acceso fallidos» duplicaba «Contraseñas incorrectas» de
	//     /seguridad, con otra ventana y otro reinicio: dos números condenados
	//     a discrepar.
	//   - «Sesiones activas» daba un número donde /administracion da los
	//     nombres, y en vivo.
	//   - «Borrados registrados» se pone a cero al reiniciar y no dice qué,
	//     cuándo ni quién. Sin papelera (D-15) la pregunta es real, pero la
	//     contesta la auditoría de acciones —etapa 6 del panel—, no un contador.
	//   - «Datos transferidos» se pone a cero al reiniciar y nadie actúa sobre
	//     él. No es la «E/S» que exige el charter §8, que es de disco.
	//
	// LOS CUATRO CONTADORES SIGUEN ENTEROS en Instantanea y en el JSON: lo que
	// se retira es la fila, no el dato. 05_OPERACION.md calcula los SLI de ahí.
	servicio = []filaViva{
		contexto("servicio-desde", "Tiempo de actividad del servicio", i.DesdeElArranque),
		conContexto("disponibilidad", fmt.Sprintf("%d peticiones · %d con error de servidor",
			i.Peticiones, i.ErroresServidor)),
		conContexto("latencia", fmt.Sprintf("%d listados · %d por encima de 2 s",
			i.Listados, i.ListadosLentos)),
		conContexto("integridad", fmt.Sprintf("%d publicadas · %d fallidas · %d descartadas · %d expiradas",
			i.SubidasConfirmadas, i.SubidasFallidas, i.SubidasDescartadas, i.SubidasExpiradas)),
		deIndicador("subidas-en-curso"),
	}
	return conVeredictoPintado(nodo), conVeredictoPintado(servicio)
}

func (s *Servidor) verEstado(w http.ResponseWriter, r *http.Request) {
	n := sistema.Leer(r.Context(), s.volumen)
	// Esta lectura ya está pagada: se le pasa al rail para que no la vuelva
	// a hacer por su cuenta un minuto después. Ver volumenReciente.
	s.anotarVolumen(n)

	inst := s.instantaneaCompleta()
	indicadores := evaluar(n, inst)

	w.Header().Set("Cache-Control", "no-store")

	if quiereJSON(r) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		cod := json.NewEncoder(w)
		cod.SetIndent("", "  ")
		if err := cod.Encode(comoJSON(n, inst, indicadores)); err != nil {
			s.reg.Error("serialización de /estado", "error", err)
		}
		return
	}

	filasNodo, filasServicio := filasVivas(n.Vivo, inst)
	lentos := conVeredictoPintado(comoFilas(evaluarLentos(n)))
	salidas := conVeredictoPintado(comoFilas(evaluarSalidas(inst)))
	// SE VUELVE A LLAMAR AQUÍ, y no se reaprovecha `indicadores`, por la misma
	// razón que las dos líneas de arriba: esta página arma la tabla por GRUPOS
	// y cada grupo tiene su propio evaluador. Reaprovechar la lista plana
	// obligaría a repartirla por clave, que es una segunda fuente de verdad
	// sobre a qué grupo pertenece cada fila.
	//
	// EL VIGÍA TIENE CACHÉ, así que las dos llamadas del mismo render no leen
	// el disco dos veces (ver respaldo.vigencia).
	respaldos := conVeredictoPintado(comoFilas(evaluarRespaldo(inst, time.Now())))

	// LOS CINCO GRUPOS DE LA TABLA. Los tres últimos solo aparecen si tienen
	// algo: un nodo sin la capa de avisos instalada no enseña dos semáforos
	// en gris permanente, que es lo que ADR-0065 vino a retirar.
	v := vistaEstado{
		PuedeAdministrar: !acotadoPorRed(r),
		Novedades:        s.novedades.Cuantas(),
		Peor:             peorDe(indicadores),
		Avisos:           n.Avisos,
		Version:          versionDelBinario(),
		Marco:            s.construirMarco(r, "estado", "Estado", ""),
	}
	for _, g := range []grupoEstado{
		{"Nodo", filasNodo},
		{"Servicio", filasServicio},
		{"Almacenamiento y hardware", lentos},
		{"Salidas hacia fuera", salidas},
		{"Respaldo del equipo", respaldos},
	} {
		if len(g.Filas) > 0 {
			v.Grupos = append(v.Grupos, g)
		}
	}

	if clave := r.URL.Query().Get("ind"); clave != "" {
		v.Detalle = marcarSeleccionada(clave, v.Grupos)
		v.ConDetalle = v.Detalle != nil
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.plantillas.ExecuteTemplate(w, "estado.html", v); err != nil {
		s.reg.Error("render de /estado", "error", err)
	}
}

// comoFilas convierte indicadores en filas. Los dos tipos tienen los mismos
// campos de cabecera y siguen siendo distintos a propósito —un indicador lleva
// veredicto por definición y una fila de contexto no—, así que la conversión
// se escribe una vez aquí en vez de campo a campo en cada llamada.
func comoFilas(is []indicador) []filaViva {
	out := make([]filaViva, len(is))
	for i, ind := range is {
		out[i] = comoFila(ind)
	}
	return out
}

// comoFila es la conversión de uno solo. Se escribe a mano y no con una
// conversión de tipo porque filaViva tiene ya campos que indicador no tiene
// —Etiqueta, Clase, Seleccionada—: el compilador lo diría, pero decirlo aquí
// evita tener que descubrirlo.
func comoFila(ind indicador) filaViva {
	return filaViva{
		Clave: ind.Clave, Nombre: ind.Nombre, Valor: ind.Valor,
		Veredicto: ind.Veredicto, Accion: ind.Accion,
	}
}

// marcarSeleccionada resuelve el «?ind=» del panel de detalle contra los
// indicadores REALES de esta página — ninguno inventado — y de paso deja
// marcada su fila.
//
// Recorre los grupos que la propia página ya construyó para pintarse, así que
// el detalle y la tabla no pueden decir cosas distintas: es la MISMA fuente,
// mirada dos veces. Una clave que no coincide con ninguna deja el panel
// cerrado (nil) en lugar de responder con un error: no es «esto no existe»,
// es «no hay nada que seleccionar».
func marcarSeleccionada(clave string, grupos []grupoEstado) *filaViva {
	for gi := range grupos {
		for fi := range grupos[gi].Filas {
			if grupos[gi].Filas[fi].Clave != clave {
				continue
			}
			grupos[gi].Filas[fi].Seleccionada = true
			return &grupos[gi].Filas[fi]
		}
	}
	return nil
}

// duracionCorta es la forma de tarjeta: dos unidades y sin espacios, «4d 20h».
// La usa Resumen, que tiene el ancho de un tercio de fila; duracionLegible
// sigue siendo la de las tablas, donde cabe el detalle. Vive aquí, junto a
// duracionLegible, para que las dos formas de escribir una duración se lean
// una al lado de la otra y no se inventen una tercera.
func duracionCorta(d time.Duration) string {
	d = d.Round(time.Minute)
	dias, horas, minutos := int(d.Hours())/24, int(d.Hours())%24, int(d.Minutes())%60
	switch {
	case dias > 0:
		return fmt.Sprintf("%dd %dh", dias, horas)
	case horas > 0:
		return fmt.Sprintf("%dh %dmin", horas, minutos)
	}
	return fmt.Sprintf("%dmin", minutos)
}

// instantaneaCompleta reúne los contadores con los dos datos que no viven en
// ellos. Existe porque hacían falta las mismas tres líneas en tres sitios
// —la página, el flujo y el ciclo de mantenimiento— y en el tercero era fácil
// olvidar una y publicar «0 sesiones abiertas» sin que fallara nada.
func (s *Servidor) instantaneaCompleta() Instantanea {
	i := s.contadores.instantanea()
	i.SubidasEnCurso = s.subidas.cuantas()
	i.SesionesAbiertas = s.sesiones.Abiertas()
	// Las dos salidas se preguntan aquí y no en los contadores porque las
	// publica el adaptador de canal, no este paquete. Nulas significan «no
	// configurado», y entonces sus indicadores no se pintan: un semáforo en
	// gris permanente enseña a ignorar el panel entero (ADR-0065).
	if s.saludCanal != nil {
		i.Canal = s.saludCanal()
	}
	if s.saludLatido != nil {
		i.Latido = s.saludLatido()
	}
	// El vigía trae su propia caché: esta función la llama TAMBIÉN el flujo en
	// vivo, una vez por segundo, y el archivo lo escribe el cliente tres veces
	// al día. Ver el comentario de respaldo.vigencia.
	i.Respaldo = s.vigiaRespaldo.Leer()
	return i
}

// quiereJSON admite las dos formas: la correcta por contenido negociado y la
// que se puede teclear en la barra del navegador.
func quiereJSON(r *http.Request) bool {
	if r.URL.Query().Get("formato") == "json" {
		return true
	}
	return strings.Contains(r.Header.Get("Accept"), "application/json")
}

func peorDe(is []indicador) veredicto {
	peor := vOK
	rango := map[veredicto]int{vOK: 0, vDesconocido: 1, vAtencion: 2, vFallo: 3}
	for _, i := range is {
		if rango[i.Veredicto] > rango[peor] {
			peor = i.Veredicto
		}
	}
	return peor
}

// ---------------------------------------------------------------------------
// Serialización
// ---------------------------------------------------------------------------

// Los campos que pueden no haberse podido leer son PUNTEROS, para que el JSON
// diga «null» y no «0». La diferencia entre «0 °C» y «no lo sé» es justamente
// la que hace útil o inútil este extremo.
type estadoJSON struct {
	Momento     string      `json:"momento"`
	Veredicto   veredicto   `json:"veredicto"`
	Indicadores []indicador `json:"indicadores"`
	Servicio    Instantanea `json:"servicio"`
	Nodo        nodoJSON    `json:"nodo"`
	Avisos      []string    `json:"avisos,omitempty"`
}

type nodoJSON struct {
	Disponible       bool        `json:"disponible"`
	UptimeSegundos   *int64      `json:"uptime_segundos"`
	Carga1           *float64    `json:"carga_1min"`
	Carga5           *float64    `json:"carga_5min"`
	Carga15          *float64    `json:"carga_15min"`
	CPUPorcentaje    *float64    `json:"cpu_porcentaje"`
	TemperaturaC     *float64    `json:"temperatura_c"`
	FrecuenciaMHz    *int        `json:"frecuencia_mhz"`
	FrecuenciaMaxMHz *int        `json:"frecuencia_max_mhz"`
	RAMTotalBytes    *uint64     `json:"ram_total_bytes"`
	RAMDispBytes     *uint64     `json:"ram_disponible_bytes"`
	Throttled        *string     `json:"throttled"`
	Datos            volumenJSON `json:"disco_datos"`
	Arranque         volumenJSON `json:"medio_arranque"`
}

type volumenJSON struct {
	Disponible           bool    `json:"disponible"`
	Punto                string  `json:"punto"`
	Dispositivo          string  `json:"dispositivo,omitempty"`
	TotalBytes           *uint64 `json:"total_bytes"`
	LibresBytes          *uint64 `json:"libres_bytes"`
	LeidoBytes           *uint64 `json:"leido_bytes_desde_arranque"`
	EscritoBytes         *uint64 `json:"escrito_bytes_desde_arranque"`
	EscrituraDeVidaBytes *uint64 `json:"escrito_bytes_de_por_vida"`
	ErroresExt4          *uint64 `json:"errores_ext4"`
	SoloLectura          *bool   `json:"solo_lectura"`
}

func comoJSON(n sistema.Nodo, i Instantanea, is []indicador) estadoJSON {
	e := estadoJSON{
		Momento:     n.Momento.Format("2006-01-02T15:04:05Z07:00"),
		Veredicto:   peorDe(is),
		Indicadores: is,
		Servicio:    i,
		Avisos:      n.Avisos,
		Nodo:        nodoJSON{Disponible: n.Disponible},
	}
	if !n.Disponible {
		e.Nodo.Datos = volumenJSON{Punto: n.Datos.Punto}
		e.Nodo.Arranque = volumenJSON{Punto: n.Arranque.Punto}
		return e
	}

	seg := int64(n.Uptime.Seconds())
	e.Nodo.UptimeSegundos = &seg
	e.Nodo.Carga1, e.Nodo.Carga5, e.Nodo.Carga15 = &n.Carga1, &n.Carga5, &n.Carga15
	if n.CPUOK {
		e.Nodo.CPUPorcentaje = &n.CPU
	}
	if n.TemperaturaOK {
		e.Nodo.TemperaturaC = &n.TemperaturaC
	}
	if n.FrecuenciaOK {
		e.Nodo.FrecuenciaMHz, e.Nodo.FrecuenciaMaxMHz = &n.FrecuenciaMHz, &n.FrecuenciaMaxMHz
	}
	if n.RAMOK {
		e.Nodo.RAMTotalBytes, e.Nodo.RAMDispBytes = &n.RAMTotalBytes, &n.RAMDisponibleBytes
	}
	if n.Throttled.Disponible {
		h := n.Throttled.Hex()
		e.Nodo.Throttled = &h
	}
	e.Nodo.Datos = volumenComoJSON(n.Datos)
	e.Nodo.Arranque = volumenComoJSON(n.Arranque)
	return e
}

func volumenComoJSON(v sistema.Volumen) volumenJSON {
	j := volumenJSON{Disponible: v.Disponible, Punto: v.Punto, Dispositivo: v.Dispositivo}
	if !v.Disponible {
		return j
	}
	j.TotalBytes, j.LibresBytes = &v.TotalBytes, &v.LibresBytes
	if v.EsOK {
		j.LeidoBytes, j.EscritoBytes = &v.LeidoBytes, &v.EscritoBytes
	}
	if v.VidaOK {
		j.EscrituraDeVidaBytes = &v.EscrituraDeVidaBytes
	}
	if v.ErroresOK {
		j.ErroresExt4 = &v.Errores
	}
	if v.SoloLecturaOK {
		j.SoloLectura = &v.SoloLectura
	}
	return j
}

// legibleBytes reutiliza el mismo criterio que la función «tamano» de las
// plantillas, para que un GB signifique lo mismo en las dos pantallas.
func legibleBytes(n uint64) string {
	const u = 1024
	if n < u {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := uint64(u), 0
	for m := n / u; m >= u; m /= u {
		div *= u
		exp++
	}
	return fmt.Sprintf("%.1f %s", float64(n)/float64(div), []string{"KB", "MB", "GB", "TB"}[exp])
}
