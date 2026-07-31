//go:build linux

package sistema

import (
	"context"
	"fmt"
	"os"
	"syscall"
	"time"
)

// rutasThrottled son los sitios donde el firmware de Raspberry publica el
// estado de limitación, en orden de preferencia.
//
// SE PRUEBAN TODAS Y SI NINGUNA EXISTE SE DICE QUE NO ESTÁ DISPONIBLE.
// No se devuelve 0x0, que se leería como «el nodo nunca se ha limitado» —
// exactamente lo contrario de la verdad conocida: 00_RECTOR.md §12.3 tiene
// medido 0x80000, el límite térmico blando ya alcanzado.
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
	n := Nodo{Momento: time.Now(), Disponible: true}

	leer := func(ruta string) (string, bool) {
		b, err := os.ReadFile(ruta)
		if err != nil {
			n.avisar("no se pudo leer %s: %v", ruta, err)
			return "", false
		}
		return string(b), true
	}

	if s, ok := leer("/proc/uptime"); ok {
		if d, err := analizarUptime(s); err != nil {
			n.avisar("%v", err)
		} else {
			n.Uptime = d
		}
	}

	if s, ok := leer("/proc/loadavg"); ok {
		c1, c5, c15, err := analizarCarga(s)
		if err != nil {
			n.avisar("%v", err)
		} else {
			n.Carga1, n.Carga5, n.Carga15 = c1, c5, c15
		}
	}

	n.medirCPU(ctx)

	if s, ok := leer("/proc/meminfo"); ok {
		total, disp, err := analizarMeminfo(s)
		if err != nil {
			n.avisar("%v", err)
		} else {
			n.RAMTotalBytes, n.RAMDisponibleBytes, n.RAMOK = total, disp, true
		}
	}

	if s, ok := leer("/sys/class/thermal/thermal_zone0/temp"); ok {
		if c, err := analizarMiligrados(s); err != nil {
			n.avisar("%v", err)
		} else {
			n.TemperaturaC, n.TemperaturaOK = c, true
		}
	}

	n.medirFrecuencia()
	n.medirThrottled()

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

func (n *Nodo) medirFrecuencia() {
	const base = "/sys/devices/system/cpu/cpu0/cpufreq/"
	actual, err1 := os.ReadFile(base + "scaling_cur_freq")
	maxima, err2 := os.ReadFile(base + "cpuinfo_max_freq")
	if err1 != nil || err2 != nil {
		n.avisar("no se pudo leer la frecuencia de la CPU en %s", base)
		return
	}
	a, e1 := analizarKiloHercios(string(actual))
	m, e2 := analizarKiloHercios(string(maxima))
	if e1 != nil || e2 != nil {
		n.avisar("frecuencia de la CPU: formato inesperado")
		return
	}
	n.FrecuenciaMHz, n.FrecuenciaMaxMHz, n.FrecuenciaOK = a, m, true
}

func (n *Nodo) medirThrottled() {
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
	// Ni un cero ni un silencio: consta que este nodo no lo publica.
	n.avisar("limitación del SoC no disponible: ninguna de las rutas de sysfs " +
		"existe en este kernel. NO se asume 0x0. Se comprueba a mano con " +
		"«vcgencmd get_throttled» por SSH (RNF-11)")
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
