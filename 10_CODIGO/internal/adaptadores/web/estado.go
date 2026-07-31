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
	Nombre    string    `json:"nombre"`
	Valor     string    `json:"valor"`
	Veredicto veredicto `json:"veredicto"`
	// Accion es lo que hay que hacer si no está en «ok». Vacío cuando no hay
	// nada que hacer.
	Accion string `json:"accion,omitempty"`
}

// evaluar es la ÚNICA fuente de veredictos del producto.
//
// La usan la pantalla y el ciclo de mantenimiento. Que sea una sola función es
// lo que garantiza que el diario y /estado no puedan contar cosas distintas.
func evaluar(n sistema.Nodo, i Instantanea) []indicador {
	var out []indicador
	add := func(ind indicador) { out = append(out, ind) }

	// --- Almacenamiento -----------------------------------------------------
	add(evaluarVolumen("Disco de datos", n.Datos,
		"Liberar espacio o borrar lo que ya no haga falta. Recuerde que no hay papelera (D-15)"))
	add(evaluarVolumen("Medio de arranque", n.Arranque,
		"P9: el medio de arranque es consumible. Reconstruir el nodo con 20_APROVISIONAMIENTO/; "+
			"el disco de datos sobrevive porque D-04 los mantiene separados"))

	// --- Nodo ---------------------------------------------------------------
	if n.RAMOK {
		uso := n.RAMUsoPorcentaje()
		v := vOK
		accion := ""
		if uso >= ramAtencion {
			v = vAtencion
			accion = "Comprobar el RSS de nasd con «systemctl status nasd». Si crece con el " +
				"tamaño del archivo que se sube, RNF-01 está roto y es un defecto, no falta de RAM"
		}
		add(indicador{"RAM usada", fmt.Sprintf("%.0f %% de %s", uso, legibleBytes(n.RAMTotalBytes)), v, accion})
	} else {
		add(indicador{"RAM usada", "no disponible", vDesconocido, ""})
	}

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
		add(indicador{"Temperatura del SoC", fmt.Sprintf("%.1f °C", n.TemperaturaC), v, accion})
	} else {
		add(indicador{"Temperatura del SoC", "no disponible", vDesconocido, ""})
	}

	add(evaluarThrottled(n.Throttled))

	// --- Servicio -----------------------------------------------------------
	add(evaluarPorcentaje("Disponibilidad de la web (SLI-1)", i.Disponibilidad(), sloDisponibilidad,
		vAtencion, "Buscar los 5xx en el diario: «journalctl -u nasd -o json | grep '\"estado\":5'»"))

	// SLI-2 es el único con objetivo del 100 %: un archivo que se transfirió
	// entero y no quedó publicado es pérdida de datos, y con copia única no
	// hay segunda oportunidad.
	add(evaluarPorcentaje("Integridad de la publicación (SLI-2)", i.IntegridadDeSubida(), 100,
		vFallo, "INCIDENTE. Revisar «subida confirmada» frente a los fallos de Confirmar() en el "+
			"diario y comprobar el estado del sistema de archivos con dmesg"))

	add(evaluarPorcentaje("Listados por debajo de 2 s (SLI-3)", i.LatenciaDeListado(), sloListados,
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
	add(indicador{"Subidas en curso", fmt.Sprintf("%d de %d", i.SubidasEnCurso, maxSubidasEnCurso), v, accion})

	return out
}

func evaluarVolumen(nombre string, v sistema.Volumen, accionEspacio string) indicador {
	if !v.Disponible {
		return indicador{nombre, "no disponible", vDesconocido, ""}
	}

	// Un remontaje de solo lectura y los errores de ext4 pesan MÁS que la
	// ocupación: el disco puede estar medio vacío y aun así estar muriéndose.
	if v.SoloLecturaOK && v.SoloLectura {
		return indicador{nombre, "MONTADO DE SOLO LECTURA", vFallo,
			"Síntoma clásico de medio agonizante. Mirar dmesg y actuar antes de escribir nada más. " +
				accionEspacio}
	}
	if v.ErroresOK && v.Errores > 0 {
		return indicador{nombre, fmt.Sprintf("%d errores de ext4", v.Errores), vFallo,
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
		return indicador{nombre, valor, vFallo, accionEspacio}
	case uso >= discoAtencion:
		return indicador{nombre, valor, vAtencion, accionEspacio}
	}
	if !v.SoloLecturaOK {
		// Se dice en voz alta que falta una comprobación, en lugar de pintar
		// un verde que también significaría «no lo he mirado».
		valor += " · sin comprobar el remontaje de solo lectura"
	}
	return indicador{nombre, valor, vOK, ""}
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
	const nombre = "Limitación del SoC"
	if !t.Disponible {
		return indicador{nombre, "no disponible por ninguna vía", vDesconocido,
			"Comprobar a mano por SSH: «vcgencmd get_throttled». Si eso funciona y el " +
				"servicio no lo ve, le falta SupplementaryGroups=video en la unidad (ADR-0036)"}
	}

	// La fuente parcial solo sabe de subtensión. Se dice, en vez de dejar que
	// los bits térmicos —que ahí valen siempre falso— se lean como «no ha
	// habido limitación térmica».
	if t.Parcial {
		if t.SubtensionAhora {
			return indicador{nombre, t.Hex(), vFallo,
				"La alimentación no da. El charter §3.6 midió cero subtensión: esto es NUEVO. " +
					"Revisar fuente y cable antes de seguir escribiendo en el disco"}
		}
		return indicador{nombre, t.Hex() + " · lo térmico SIN MEDIR", vDesconocido,
			"Solo se está leyendo la alarma de subtensión del hwmon. Para la palabra completa " +
				"hace falta que la unidad lleve SupplementaryGroups=video (ADR-0036); " +
				"mientras tanto RNF-11 no se está midiendo desde el servicio"}
	}

	switch {
	case t.SubtensionAhora:
		return indicador{nombre, t.Hex() + " — SUBTENSIÓN AHORA", vFallo,
			"La alimentación no da. El charter §3.6 midió cero subtensión: esto es NUEVO. " +
				"Revisar fuente y cable antes de seguir escribiendo en el disco"}
	case t.SubtensionOcurrida:
		return indicador{nombre, t.Hex() + " — hubo subtensión desde el arranque", vAtencion,
			"No estaba en la medición del charter §3.6. Revisar la fuente"}
	case t.LimitadoAhora, t.FrecuenciaCapadaAhora:
		return indicador{nombre, t.Hex() + " — limitando ahora", vAtencion,
			"El nodo está rebajando frecuencia. Bajo carga es esperable sin disipador (RES-06)"}
	case t.TermicoBlandoAhora:
		return indicador{nombre, t.Hex() + " — límite térmico blando ACTIVO", vAtencion,
			"Está ocurriendo ahora mismo. La mitigación sigue siendo un disipador (§4.5)"}
	case t.TermicoBlandoOcurrida:
		return indicador{nombre, t.Hex() + " — límite térmico blando alcanzado alguna vez", vOK,
			""}
	}
	return indicador{nombre, t.Hex() + " — sin limitación", vOK, ""}
}

func evaluarPorcentaje(nombre string, valor, objetivo float64, siFalla veredicto, accion string) indicador {
	if valor < 0 {
		// Sin muestras no se aprueba ni se suspende. Un 100 % sobre cero
		// peticiones es exactamente el defecto de «aprobar sin verificar» que
		// 00_RECTOR.md §12.5 lleva cuatro veces corrigiendo.
		return indicador{nombre, "sin muestras todavía", vDesconocido, ""}
	}
	v := vOK
	if valor < objetivo {
		v = siFalla
	} else {
		accion = ""
	}
	return indicador{nombre, fmt.Sprintf("%.2f %% (objetivo ≥ %.0f %%)", valor, objetivo), v, accion}
}

// ---------------------------------------------------------------------------
// Manejador
// ---------------------------------------------------------------------------

type vistaEstado struct {
	Volumen     string
	Servicio    Instantanea
	Indicadores []indicador
	// Peor es el veredicto más grave de todos: lo que se lee de un vistazo.
	Peor veredicto
	// Detalles y Trabajo son filas ya formateadas. La plantilla no calcula
	// nada: ADR-0017 pone el render en el servidor, y eso incluye las
	// decisiones de formato.
	Detalles []pareja
	Trabajo  []pareja
	Avisos   []string
	Csrf     string
}

type pareja struct{ Clave, Valor string }

// detallesDelNodo son las cifras de contexto: no disparan alertas por sí
// solas, pero son lo que se mira cuando una alerta ya saltó.
func detallesDelNodo(n sistema.Nodo) []pareja {
	if !n.Disponible {
		return []pareja{{"Nodo", "métricas no disponibles fuera de Linux (D-06: esto es el host)"}}
	}
	d := []pareja{
		{"Encendido desde hace", duracionLegible(n.Uptime)},
		{"Carga (1 · 5 · 15 min)", fmt.Sprintf("%.2f · %.2f · %.2f", n.Carga1, n.Carga5, n.Carga15)},
	}
	if n.CPUOK {
		d = append(d, pareja{"CPU ocupada",
			fmt.Sprintf("%.0f %% (media de %s, sin contar espera de disco)", n.CPU, VentanaCPULegible)})
	}
	if n.FrecuenciaOK {
		v := fmt.Sprintf("%d de %d MHz", n.FrecuenciaMHz, n.FrecuenciaMaxMHz)
		if n.FrecuenciaMHz < n.FrecuenciaMaxMHz {
			// Por debajo del máximo puede ser ahorro en reposo o limitación
			// térmica; el indicador de arriba lo distingue, esto solo informa.
			v += " — por debajo del máximo"
		}
		d = append(d, pareja{"Frecuencia de la CPU", v})
	}
	if n.RAMOK {
		d = append(d, pareja{"RAM disponible",
			legibleBytes(n.RAMDisponibleBytes) + " de " + legibleBytes(n.RAMTotalBytes)})
	}
	for _, v := range []sistema.Volumen{n.Datos, n.Arranque} {
		if v.EsOK {
			d = append(d, pareja{"E/S en " + v.Punto + " desde el arranque",
				legibleBytes(v.LeidoBytes) + " leídos · " + legibleBytes(v.EscritoBytes) + " escritos"})
		}
	}
	return d
}

// VentanaCPULegible existe solo para no repetir el número en la interfaz.
var VentanaCPULegible = sistema.VentanaCPU.String()

func trabajoDelServicio(i Instantanea) []pareja {
	return []pareja{
		{"Servicio en marcha desde hace", i.DesdeElArranque},
		{"Peticiones atendidas", fmt.Sprintf("%d (%d correctas · %d de cliente · %d de servidor)",
			i.Peticiones, i.Exito, i.ErroresCliente, i.ErroresServidor)},
		{"Listados", fmt.Sprintf("%d, de los que %d pasaron de 2 s", i.Listados, i.ListadosLentos)},
		{"Transferido", legibleBytes(uint64(i.BytesSubidos)) + " subidos · " +
			legibleBytes(uint64(i.BytesDescargados)) + " descargados"},
		{"Subidas", fmt.Sprintf("%d creadas · %d confirmadas · %d fallidas · %d descartadas · %d expiradas",
			i.SubidasCreadas, i.SubidasConfirmadas, i.SubidasFallidas,
			i.SubidasDescartadas, i.SubidasExpiradas)},
		{"Borrados registrados", fmt.Sprintf("%d — cada uno consta en el diario en nivel WARN (RF-19)", i.Borrados)},
		{"Accesos fallidos", fmt.Sprintf("%d", i.AccesosFallidos)},
		{"Sesiones abiertas", fmt.Sprintf("%d", i.SesionesAbiertas)},
	}
}

func (s *Servidor) verEstado(w http.ResponseWriter, r *http.Request) {
	n := sistema.Leer(r.Context(), s.volumen)

	inst := s.contadores.instantanea()
	inst.SubidasEnCurso = s.subidas.cuantas()
	inst.SesionesAbiertas = s.sesiones.Abiertas()

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

	v := vistaEstado{
		Volumen:     s.volumen,
		Servicio:    inst,
		Indicadores: indicadores,
		Peor:        peorDe(indicadores),
		Detalles:    detallesDelNodo(n),
		Trabajo:     trabajoDelServicio(inst),
		Avisos:      n.Avisos,
		Csrf:        s.csrfDe(r),
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.plantillas.ExecuteTemplate(w, "estado.html", v); err != nil {
		s.reg.Error("render de /estado", "error", err)
	}
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
