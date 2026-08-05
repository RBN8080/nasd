//go:build linux

package sistema

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// rutasThrottled son los sitios donde ALGUNOS kernels de Raspberry publican el
// estado de limitación.
//
// MEDIDO EN EL NODO el 2026-07-31: **ninguna de las tres existe aquí**, y una
// búsqueda amplia por /sys no encuentra nada. Se conservan porque probarlas no
// cuesta nada y un kernel futuro podría traerlas; la fuente real hoy es
// vcgencmd (ADR-0036). Si fallan todas las vías, se dice que no se sabe: no se
// devuelve 0x0, que se leería como «el nodo nunca se ha limitado».
var rutasThrottled = []string{
	"/sys/devices/platform/soc/soc:firmware/get_throttled",
	"/sys/devices/platform/soc/soc:firmware/get_throttled/get_throttled",
	"/sys/firmware/raspberrypi/get_throttled",
}

// Leer toma una fotografía del nodo. NO devuelve error: un dato de salud que
// no se puede leer no debe tumbar el extremo que informa de la salud. Lo que
// falla se anota en Nodo.Avisos y se muestra como «no disponible».
//
// puntoDatos es el punto de montaje del volumen de datos (ADR-0019).
func Leer(ctx context.Context, puntoDatos string) Nodo {
	n := Nodo{Vivo: Vivo{Momento: time.Now(), Disponible: true}}

	leer := func(ruta string) (string, bool) {
		b, err := os.ReadFile(ruta)
		if err != nil {
			n.avisar("no se pudo leer %s: %v", ruta, err)
			return "", false
		}
		return string(b), true
	}

	if d, err := leerUptime(); err != nil {
		n.avisar("%v", err)
	} else {
		n.Uptime, n.UptimeOK = d, true
	}

	if c1, c5, c15, err := leerCarga(); err != nil {
		n.avisar("%v", err)
	} else {
		n.Carga1, n.Carga5, n.Carga15, n.CargaOK = c1, c5, c15, true
	}

	n.medirCPU(ctx)

	if total, disp, err := leerMemoria(); err != nil {
		n.avisar("%v", err)
	} else {
		n.RAMTotalBytes, n.RAMDisponibleBytes, n.RAMOK = total, disp, true
	}

	if c, err := leerTemperatura(); err != nil {
		n.avisar("%v", err)
	} else {
		n.TemperaturaC, n.TemperaturaOK = c, true
	}

	if mhz, max, err := leerFrecuencia(); err != nil {
		n.avisar("%v", err)
	} else {
		n.FrecuenciaMHz, n.FrecuenciaMaxMHz, n.FrecuenciaOK = mhz, max, true
	}

	n.medirThrottled(ctx)

	// /proc/1/mounts y no /proc/mounts, a propósito: ver el comentario de
	// Volumen.SoloLecturaOK. Dentro del espacio de nombres del servicio,
	// ProtectSystem=strict deja «/» en solo lectura siempre.
	montajes, montajesOK := leer("/proc/1/mounts")
	if !montajesOK {
		n.avisar("sin /proc/1/mounts no se puede distinguir un remontaje de " +
			"solo lectura real del que impone ProtectSystem=strict; " +
			"el indicador queda como «no se sabe», no como «correcto»")
	}
	diskstats, _ := leer("/proc/diskstats")

	n.Datos = n.leerVolumen(puntoDatos, montajes, montajesOK, diskstats)
	n.Arranque = n.leerVolumen("/", montajes, montajesOK, diskstats)

	return n
}

func (n *Nodo) avisar(formato string, args ...any) {
	n.Avisos = append(n.Avisos, fmt.Sprintf(formato, args...))
}

// medirCPU muestrea /proc/stat dos veces separadas por VentanaCPU.
func (n *Nodo) medirCPU(ctx context.Context) {
	muestra := func() (total, ocioso uint64, ok bool) {
		b, err := os.ReadFile("/proc/stat")
		if err != nil {
			n.avisar("no se pudo leer /proc/stat: %v", err)
			return 0, 0, false
		}
		t, o, err := analizarCPU(string(b))
		if err != nil {
			n.avisar("%v", err)
			return 0, 0, false
		}
		return t, o, true
	}

	t0, o0, ok := muestra()
	if !ok {
		return
	}

	// La espera respeta la cancelación: si el cliente se va, no se retiene
	// una goroutine un cuarto de segundo por gusto.
	temporizador := time.NewTimer(VentanaCPU)
	defer temporizador.Stop()
	select {
	case <-ctx.Done():
		n.avisar("muestreo de CPU cancelado por el cliente")
		return
	case <-temporizador.C:
	}

	t1, o1, ok := muestra()
	if !ok {
		return
	}
	if p, ok := porcentajeCPU(t0, o0, t1, o1); ok {
		n.CPU, n.CPUOK = p, true
	} else {
		n.avisar("ventana de muestreo demasiado corta para calcular la CPU")
	}
}

// ---------------------------------------------------------------------------
// Una función por métrica. Devuelven error en lugar de anotar avisos porque
// tienen DOS clientes con necesidades distintas: Leer() convierte el error en
// un aviso con su motivo, y LeerVivo() solo baja la bandera.
//
// Que sean estas las únicas que saben dónde vive cada dato es el objetivo: la
// ruta de la temperatura aparece una vez, no dos. Duplicarla era la forma
// evidente de que un día el flujo y la página midieran cosas distintas.
// ---------------------------------------------------------------------------

func leerUptime() (time.Duration, error) {
	b, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0, fmt.Errorf("no se pudo leer /proc/uptime: %w", err)
	}
	return analizarUptime(string(b))
}

func leerCarga() (c1, c5, c15 float64, err error) {
	b, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0, 0, 0, fmt.Errorf("no se pudo leer /proc/loadavg: %w", err)
	}
	return analizarCarga(string(b))
}

func leerMemoria() (total, disponible uint64, err error) {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, 0, fmt.Errorf("no se pudo leer /proc/meminfo: %w", err)
	}
	return analizarMeminfo(string(b))
}

func leerTemperatura() (float64, error) {
	const ruta = "/sys/class/thermal/thermal_zone0/temp"
	b, err := os.ReadFile(ruta)
	if err != nil {
		return 0, fmt.Errorf("no se pudo leer %s: %w", ruta, err)
	}
	return analizarMiligrados(string(b))
}

func leerFrecuencia() (mhz, maxMHz int, err error) {
	const base = "/sys/devices/system/cpu/cpu0/cpufreq/"
	actual, err1 := os.ReadFile(base + "scaling_cur_freq")
	maxima, err2 := os.ReadFile(base + "cpuinfo_max_freq")
	if err1 != nil || err2 != nil {
		return 0, 0, fmt.Errorf("no se pudo leer la frecuencia de la CPU en %s", base)
	}
	a, e1 := analizarKiloHercios(string(actual))
	m, e2 := analizarKiloHercios(string(maxima))
	if e1 != nil || e2 != nil {
		return 0, 0, fmt.Errorf("frecuencia de la CPU: formato inesperado")
	}
	return a, m, nil
}

// LeerCPU toma una muestra suelta del contador de /proc/stat.
//
// Por sí sola no dice nada: hace falta una segunda para restar. Ver MuestraCPU.
func LeerCPU() MuestraCPU {
	b, err := os.ReadFile("/proc/stat")
	if err != nil {
		return MuestraCPU{}
	}
	t, o, err := analizarCPU(string(b))
	if err != nil {
		return MuestraCPU{}
	}
	return MuestraCPU{Total: t, Ocioso: o, OK: true}
}

// LeerVivo toma la fotografía BARATA del nodo, la que se puede repetir varias
// veces por segundo. Ver el comentario del tipo Vivo para qué queda fuera.
//
// Recibe la muestra de CPU del tic anterior y devuelve la de este, para que
// quien llama la pase al siguiente. Con una muestra previa inválida —el primer
// tic de todos— el porcentaje sale sin medir en vez de salir cero.
//
// NO duerme y NO lanza procesos. Son siete lecturas de archivos que el kernel
// sirve desde memoria; el coste es la propia llamada al sistema.
func LeerVivo(previa MuestraCPU) (Vivo, MuestraCPU) {
	v := Vivo{Momento: time.Now(), Disponible: true}

	if d, err := leerUptime(); err == nil {
		v.Uptime, v.UptimeOK = d, true
	}
	if c1, c5, c15, err := leerCarga(); err == nil {
		v.Carga1, v.Carga5, v.Carga15, v.CargaOK = c1, c5, c15, true
	}
	if total, disp, err := leerMemoria(); err == nil {
		v.RAMTotalBytes, v.RAMDisponibleBytes, v.RAMOK = total, disp, true
	}
	if c, err := leerTemperatura(); err == nil {
		v.TemperaturaC, v.TemperaturaOK = c, true
	}
	if mhz, max, err := leerFrecuencia(); err == nil {
		v.FrecuenciaMHz, v.FrecuenciaMaxMHz, v.FrecuenciaOK = mhz, max, true
	}

	actual := LeerCPU()
	if previa.OK && actual.OK {
		if p, ok := porcentajeCPU(previa.Total, previa.Ocioso, actual.Total, actual.Ocioso); ok {
			v.CPU, v.CPUOK = p, true
		}
	}
	return v, actual
}

// medirThrottled recorre las tres fuentes de ADR-0036, de mejor a peor.
func (n *Nodo) medirThrottled(ctx context.Context) {
	// 1. sysfs. En este nodo NO existe —medido el 2026-07-31— pero se intenta
	//    igual: es la única fuente que no lanza un proceso, y cuesta tres
	//    llamadas a open() que fallan.
	for _, ruta := range rutasThrottled {
		b, err := os.ReadFile(ruta)
		if err != nil {
			continue
		}
		t, err := analizarThrottled(string(b))
		if err != nil {
			n.avisar("%v", err)
			continue
		}
		t.Origen = ruta
		n.Throttled = t
		return
	}

	// 2. vcgencmd. ADR-0036 lo autoriza con SupplementaryGroups=video,
	//    DeviceAllow=/dev/vcio_gencmd y BindPaths=/dev/vcio_gencmd. Si falta
	//    cualquiera de las tres, esto falla con «Can't open device file», que
	//    es exactamente lo que hay que ver en el registro si alguien las
	//    retira creyendo que endurece gratis.
	if t, err := throttledPorVcgencmd(ctx); err == nil {
		n.Throttled = t
		return
	} else {
		n.avisar("vcgencmd: %v", err)
	}

	// 3. Respaldo PARCIAL: la alarma de subtensión del driver rpi_volt. Es
	//    world-readable y no necesita ni grupo ni proceso hijo. Solo cubre la
	//    subtensión, y por eso el Throttled queda marcado como parcial.
	if t, ok := throttledPorHwmon(); ok {
		n.Throttled = t
		n.avisar("limitación del SoC PARCIAL: sin vcgencmd solo se conoce la " +
			"subtensión. Los bits térmicos NO se están midiendo (RNF-11)")
		return
	}

	// Ni un cero ni un silencio: consta que no se pudo saber.
	n.avisar("limitación del SoC no disponible por ninguna vía. NO se asume " +
		"0x0. Se comprueba a mano con «vcgencmd get_throttled» por SSH (RNF-11)")
}

// rutaVcgencmd es absoluta a propósito: el PATH de un servicio systemd no es
// el de un intérprete interactivo, y depender de él sería una sorpresa.
const rutaVcgencmd = "/usr/bin/vcgencmd"

// plazoVcgencmd acota el proceso hijo. Es una consulta al firmware que tarda
// milisegundos; si no ha respondido en dos segundos, algo va mal y NO se
// bloquea el extremo de estado por ello (RNF-12: toda E/S con plazo).
const plazoVcgencmd = 2 * time.Second

func throttledPorVcgencmd(ctx context.Context) (Throttled, error) {
	ctx, cancelar := context.WithTimeout(ctx, plazoVcgencmd)
	defer cancelar()

	// Sin argumentos que vengan de fuera: el comando es una constante y no hay
	// intérprete de por medio, así que no hay nada que inyectar.
	salida, err := exec.CommandContext(ctx, rutaVcgencmd, "get_throttled").Output()
	if err != nil {
		return Throttled{}, err
	}
	t, err := analizarThrottled(string(salida))
	if err != nil {
		return Throttled{}, err
	}
	t.Origen = rutaVcgencmd + " get_throttled"
	return t, nil
}

// rutaAlarmaSubtension se busca por el NOMBRE del driver y no por un hwmonN
// fijo: la numeración de /sys/class/hwmon depende del orden de sondeo y puede
// cambiar entre arranques. En este nodo hoy es hwmon1, y mañana podría no.
const nombreDriverVoltaje = "rpi_volt"

func throttledPorHwmon() (Throttled, bool) {
	entradas, err := os.ReadDir("/sys/class/hwmon")
	if err != nil {
		return Throttled{}, false
	}
	for _, e := range entradas {
		base := "/sys/class/hwmon/" + e.Name() + "/"
		nombre, err := os.ReadFile(base + "name")
		if err != nil || strings.TrimSpace(string(nombre)) != nombreDriverVoltaje {
			continue
		}
		b, err := os.ReadFile(base + "in0_lcrit_alarm")
		if err != nil {
			continue
		}
		v, err := analizarEntero(string(b))
		if err != nil {
			continue
		}
		t := throttledDeAlarma(v)
		t.Origen = base + "in0_lcrit_alarm"
		return t, true
	}
	return Throttled{}, false
}

// leerVolumen reúne capacidad, E/S, desgaste y salud de un punto de montaje.
func (n *Nodo) leerVolumen(punto, montajes string, montajesOK bool, diskstats string) Volumen {
	v := Volumen{Punto: punto}

	var fs syscall.Statfs_t
	if err := syscall.Statfs(punto, &fs); err != nil {
		n.avisar("statfs %s: %v", punto, err)
		return v
	}
	v.Disponible = true
	v.TotalBytes = fs.Blocks * uint64(fs.Bsize)
	// Bavail y no Bfree: Bfree incluye los bloques reservados para root, que
	// este servicio no puede usar. Prometer espacio que no se puede escribir
	// es peor que no informar.
	v.LibresBytes = fs.Bavail * uint64(fs.Bsize)

	if montajesOK {
		if dispositivo, ro, ok := analizarMontaje(montajes, punto); ok {
			v.Dispositivo = dispositivo
			v.SoloLectura, v.SoloLecturaOK = ro, true
		}
	}
	if v.Dispositivo == "" {
		return v
	}

	corto := nombreDeDispositivo(v.Dispositivo)
	if leido, escrito, ok := analizarDiskstats(diskstats, corto); ok {
		v.LeidoBytes, v.EscritoBytes, v.EsOK = leido, escrito, true
	}

	// Contadores de ext4. Estos NO dependen del espacio de nombres de montaje
	// y sobreviven a los reinicios, así que son la mejor señal que hay para
	// P9 —cuánto se ha gastado el medio— y para la salud del sistema de
	// archivos. Solo existen si el volumen es ext4, que es el caso por D-07.
	base := "/sys/fs/ext4/" + corto + "/"
	if b, err := os.ReadFile(base + "lifetime_write_kbytes"); err == nil {
		if bytes, err := analizarKilobytes(string(b)); err == nil {
			v.EscrituraDeVidaBytes, v.VidaOK = bytes, true
		}
	}
	if b, err := os.ReadFile(base + "errors_count"); err == nil {
		if c, err := analizarEntero(string(b)); err == nil {
			v.Errores, v.ErroresOK = c, true
		}
	}
	return v
}
