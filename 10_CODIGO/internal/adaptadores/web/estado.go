package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"nasd/internal/adaptadores/sistema"
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
	return append(out, evaluarSalidas(i)...)
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

type vistaEstado struct {
	// FilasNodo y FilasServicio son las dos tablas que se refrescan solas.
	// La plantilla las pinta una vez y a partir de ahí las mantiene el flujo
	// (ADR-0051); sin JavaScript se quedan en esta foto, que es correcta.
	FilasNodo     []filaViva
	FilasServicio []filaViva
	// Lentos son los indicadores que NO se refrescan: medirlos cuesta statfs,
	// /proc/diskstats o un proceso hijo. La pantalla dice que están medidos al
	// abrir, en vez de dejar creer que también son de ahora mismo.
	Lentos []indicador
	// Peor es el veredicto más grave de todos: lo que se lee de un vistazo.
	Peor   veredicto
	Avisos []string
	// PuedeAdministrar decide si la barra lleva a Administración. Misma regla
	// y mismo motivo que en el listado (acotadoPorRed, sesion.go): esta página
	// SÍ se ve desde Internet, así que sin esto ofrecería un enlace que
	// responde 403 — el botón muerto que ADR-0055 ya evitó para «Estado».
	PuedeAdministrar bool
	// Novedades es la marca del botón «Seguridad». Misma que en el listado.
	Novedades int
	// Version es lo único que queda en el pie: qué binario está corriendo.
	// Viene por campos —nombre, revisión, fecha, huella— y no como una sola
	// cadena, para que la plantilla pueda impedir que se rompa uno por dentro
	// al envolver. Ver versionDelBinario.
	Version []string
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
		return filaViva(porClave[clave])
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
	return nodo, servicio
}

func (s *Servidor) verEstado(w http.ResponseWriter, r *http.Request) {
	n := sistema.Leer(r.Context(), s.volumen)

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
	v := vistaEstado{
		PuedeAdministrar: !acotadoPorRed(r),
		Novedades:        s.novedades.Cuantas(),
		FilasNodo:        filasNodo,
		FilasServicio:    filasServicio,
		Lentos:           evaluarLentos(n),
		Peor:             peorDe(indicadores),
		Avisos:           n.Avisos,
		Version:          versionDelBinario(),
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.plantillas.ExecuteTemplate(w, "estado.html", v); err != nil {
		s.reg.Error("render de /estado", "error", err)
	}
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
