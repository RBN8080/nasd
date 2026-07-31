// Package sistema lee la salud del NODO: CPU, temperatura, RAM, E/S,
// limitación térmica y desgaste del medio de arranque.
//
// Es la mitad de hardware de lo que exige el charter §8. La otra mitad —los
// indicadores del propio servicio— vive en el adaptador web, porque son cosas
// distintas: aquí se mide la máquina, allí se mide el producto.
//
// # CÓMO SE LEE LA LIMITACIÓN DEL SoC — corregido contra el nodo real
//
// ADR-0034 apostó por sysfs y se equivocó. MEDIDO en el nodo el 2026-07-31:
// ninguna de las tres rutas candidatas existe, y «find /sys -name '*throttled*'»
// no devuelve nada. Este kernel simplemente no lo publica.
//
// ADR-0036 rehace la decisión y permite «vcgencmd», con el coste medido y no
// supuesto. También corrige de qué dispositivo se trata: strace sobre el
// binario real muestra que abre
//
//	/dev/vcio_gencmd   0660 root:video
//
// y NO /dev/vcio (0600 root:root, inalcanzable), que es lo que suponía
// ADR-0034. La distribución ya abre ese nodo al grupo «video» con su propia
// regla de udev, así que no hace falta ninguna nueva.
//
// En la unidad hacen falta TRES líneas, y cada una hace algo distinto —se
// probó quitando cada una y falla—: SupplementaryGroups=video da el permiso
// de grupo, DeviceAllow abre la política de cgroup que PrivateDevices cierra,
// y BindPaths HACE APARECER el nodo dentro del /dev privado. DeviceAllow por
// sí solo NO lo crea.
//
// PrivateDevices=yes SE MANTIENE: verificado que el /dev del servicio queda
// con los nodos mínimos más vcio_gencmd, y nada más.
//
// Orden de intentos, de mejor a peor:
//
//  1. sysfs — se conserva aunque hoy no exista: no cuesta nada y un kernel
//     futuro podría traerlo. Es la única fuente que no lanza procesos.
//  2. vcgencmd — completo, y lo que ADR-0036 autoriza.
//  3. hwmon «in0_lcrit_alarm» del driver rpi_volt — PARCIAL: solo dice si hay
//     subtensión, que es el único bit de nivel «fallo». Existe como red de
//     seguridad para que, si alguien vuelve a endurecer la unidad y quita el
//     grupo video, el servicio NO se quede ciego ante lo más grave.
//
// Si no hay ninguna, SE DICE. Ver Throttled.Disponible y Throttled.Parcial:
// este paquete nunca inventa un cero. Un indicador que miente es peor que no
// tenerlo (00_RECTOR.md §12.5).
//
// # QUÉ ES PORTABLE Y QUÉ NO
//
// Este archivo contiene los TIPOS y los ANALIZADORES, que son funciones puras
// de cadena a valor y compilan en cualquier sistema. La lectura real de
// archivos vive en lector_linux.go. Esa separación no es estética: permite
// probar el análisis en el host (D-06) sin un /proc de Raspberry delante.
package sistema

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// ErrNoDisponible lo devuelve el lector en sistemas que no son Linux.
var ErrNoDisponible = errors.New("métricas del nodo: solo disponibles en Linux")

// Nodo es una fotografía de la máquina en un instante.
//
// Ningún campo es de fiar sin su bandera de disponibilidad al lado. Lo que no
// se pudo leer se cuenta en Avisos y no se rellena con ceros.
type Nodo struct {
	Momento time.Time

	// Disponible es falso fuera de Linux. Los demás campos no valen nada.
	Disponible bool

	// Uptime del NODO, no del servicio.
	Uptime time.Duration

	Carga1, Carga5, Carga15 float64

	// CPU es el porcentaje ocupado durante la ventana corta de muestreo.
	// Ver VentanaCPU: es una media de esa ventana, no un instantáneo.
	CPU   float64
	CPUOK bool

	TemperaturaC  float64
	TemperaturaOK bool

	FrecuenciaMHz    int
	FrecuenciaMaxMHz int
	FrecuenciaOK     bool

	RAMTotalBytes      uint64
	RAMDisponibleBytes uint64
	RAMOK              bool

	Throttled Throttled

	// Datos es el volumen del disco de datos; Arranque, el medio del sistema.
	// D-04 los mantiene separados a propósito y hay que mirarlos por separado:
	// el de datos se llena, el de arranque se gasta (P9).
	Datos    Volumen
	Arranque Volumen

	// Avisos recoge lo que no se pudo leer, con su motivo. No se silencia
	// nada: si /estado muestra menos de lo que debería, aquí consta por qué.
	Avisos []string
}

// VentanaCPU es lo que se muestrea /proc/stat para calcular el porcentaje.
//
// Se hacen DOS lecturas separadas por esta ventana en lugar de guardar la
// anterior entre llamadas. Cuesta un cuarto de segundo en un extremo que se
// consulta a mano, y a cambio el paquete no tiene estado compartido —que es
// justo lo que ADR-0013 dejó vinculante documentar y acotar—.
const VentanaCPU = 250 * time.Millisecond

// Throttled es el estado de limitación del SoC.
//
// Los bits los define la documentación de Raspberry Pi. Los «Ahora» dicen qué
// está pasando en este instante; los «Ocurrida» son pegajosos desde el
// arranque y NO se apagan solos: 0x80000 significa que el límite térmico
// blando se alcanzó alguna vez, aunque ahora mismo el nodo esté fresco.
// Confundirlos es la lectura errónea más fácil de este dato.
type Throttled struct {
	Disponible bool
	// Parcial indica que SOLO se conoce la subtensión, porque el valor vino
	// del hwmon de respaldo y no de la palabra completa del firmware. Los
	// campos térmicos de esta estructura NO significan nada cuando es cierto.
	Parcial bool
	// Origen es de dónde salió, para poder comprobarlo a mano.
	Origen string
	Bruto  uint64

	SubtensionAhora       bool // bit 0
	FrecuenciaCapadaAhora bool // bit 1
	LimitadoAhora         bool // bit 2
	TermicoBlandoAhora    bool // bit 3

	SubtensionOcurrida       bool // bit 16
	FrecuenciaCapadaOcurrida bool // bit 17
	LimitadoOcurrida         bool // bit 18
	TermicoBlandoOcurrida    bool // bit 19
}

// Volumen es el estado de un sistema de archivos montado.
type Volumen struct {
	Disponible  bool
	Punto       string
	Dispositivo string

	TotalBytes  uint64
	LibresBytes uint64

	// LeidoBytes y EscritoBytes son acumulados DESDE EL ARRANQUE
	// (/proc/diskstats). Sirven para ver actividad, no desgaste.
	LeidoBytes   uint64
	EscritoBytes uint64
	EsOK         bool

	// EscrituraDeVidaBytes es el acumulado de ext4 DESDE QUE SE FORMATEÓ, y
	// sobrevive a los reinicios. Este es el número que importa para P9: el
	// medio de arranque es consumible y esto mide cuánto se ha gastado.
	EscrituraDeVidaBytes uint64
	VidaOK               bool

	// Errores es el contador de errores del sistema de archivos ext4.
	// Cualquier valor distinto de cero es un síntoma, no una curiosidad.
	Errores   uint64
	ErroresOK bool

	// SoloLectura: un medio que empieza a fallar se remonta de solo lectura.
	// Es el síntoma clásico, y por eso se mira.
	//
	// SoloLecturaOK es falso cuando NO SE PUDO SABER, que es distinto de
	// «está bien». Ocurre si no se puede leer /proc/1/mounts: dentro del
	// espacio de nombres del servicio, ProtectSystem=strict deja «/» montada
	// de solo lectura SIEMPRE, así que leer nuestro propio /proc/mounts daría
	// una alarma falsa permanente. Antes «no se sabe» que un dato inventado.
	SoloLectura   bool
	SoloLecturaOK bool
}

// UsoPorcentaje es la ocupación del volumen, o -1 si no se pudo leer.
func (v Volumen) UsoPorcentaje() float64 {
	if !v.Disponible || v.TotalBytes == 0 {
		return -1
	}
	return float64(v.TotalBytes-v.LibresBytes) * 100 / float64(v.TotalBytes)
}

// RAMUsoPorcentaje usa MemAvailable, no MemFree.
//
// MemFree en Linux es casi siempre bajo porque el kernel usa la RAM libre
// como caché de disco, y esa caché se cede en cuanto alguien la necesita.
// Alarmar con MemFree daría un rojo permanente que no significa nada.
func (n Nodo) RAMUsoPorcentaje() float64 {
	if !n.RAMOK || n.RAMTotalBytes == 0 {
		return -1
	}
	return float64(n.RAMTotalBytes-n.RAMDisponibleBytes) * 100 / float64(n.RAMTotalBytes)
}

// ---------------------------------------------------------------------------
// Analizadores. Funciones puras: entra texto, sale valor. Se prueban en el
// host contra muestras reales de una Raspberry, sin necesitar la Raspberry.
// ---------------------------------------------------------------------------

// analizarUptime lee /proc/uptime: «12345.67 45678.90».
func analizarUptime(s string) (time.Duration, error) {
	campos := strings.Fields(s)
	if len(campos) < 1 {
		return 0, fmt.Errorf("uptime: formato inesperado")
	}
	seg, err := strconv.ParseFloat(campos[0], 64)
	if err != nil {
		return 0, fmt.Errorf("uptime: %w", err)
	}
	return time.Duration(seg * float64(time.Second)), nil
}

// analizarCarga lee /proc/loadavg: «0.15 0.10 0.09 1/212 1234».
func analizarCarga(s string) (c1, c5, c15 float64, err error) {
	campos := strings.Fields(s)
	if len(campos) < 3 {
		return 0, 0, 0, fmt.Errorf("loadavg: formato inesperado")
	}
	v := make([]float64, 3)
	for i := range v {
		if v[i], err = strconv.ParseFloat(campos[i], 64); err != nil {
			return 0, 0, 0, fmt.Errorf("loadavg: %w", err)
		}
	}
	return v[0], v[1], v[2], nil
}

// analizarCPU lee la primera línea de /proc/stat y devuelve el total de
// «jiffies» y cuántos de ellos fueron ociosos.
//
// El ocioso son DOS campos, idle e iowait, y hay que sumar los dos: el tiempo
// esperando al disco no es CPU ocupada. En un nodo cuyo techo es el bus USB
// (RES-02) esa distinción no es teórica — contar iowait como ocupado pintaría
// el 100 % durante cualquier subida grande.
func analizarCPU(s string) (total, ocioso uint64, err error) {
	linea, _, _ := strings.Cut(s, "\n")
	campos := strings.Fields(linea)
	if len(campos) < 5 || campos[0] != "cpu" {
		return 0, 0, fmt.Errorf("stat: se esperaba la línea agregada «cpu»")
	}
	for i, c := range campos[1:] {
		n, err := strconv.ParseUint(c, 10, 64)
		if err != nil {
			return 0, 0, fmt.Errorf("stat: campo %d: %w", i, err)
		}
		total += n
		if i == 3 || i == 4 { // idle, iowait
			ocioso += n
		}
	}
	return total, ocioso, nil
}

// porcentajeCPU combina dos muestras de analizarCPU.
func porcentajeCPU(total0, ocioso0, total1, ocioso1 uint64) (float64, bool) {
	// El contador es monótono; si no crece, la ventana fue demasiado corta
	// para que el reloj de jiffies avanzara y no hay nada que calcular.
	if total1 <= total0 {
		return 0, false
	}
	dt := float64(total1 - total0)
	do := float64(ocioso1 - ocioso0)
	p := (dt - do) * 100 / dt
	if p < 0 {
		p = 0
	}
	if p > 100 {
		p = 100
	}
	return p, true
}

// analizarMeminfo devuelve MemTotal y MemAvailable en bytes.
func analizarMeminfo(s string) (total, disponible uint64, err error) {
	var vistos int
	for l := range strings.SplitSeq(s, "\n") {
		clave, resto, ok := strings.Cut(l, ":")
		if !ok {
			continue
		}
		var destino *uint64
		switch clave {
		case "MemTotal":
			destino = &total
		case "MemAvailable":
			destino = &disponible
		default:
			continue
		}
		campos := strings.Fields(resto)
		if len(campos) < 1 {
			continue
		}
		kb, err := strconv.ParseUint(campos[0], 10, 64)
		if err != nil {
			return 0, 0, fmt.Errorf("meminfo %s: %w", clave, err)
		}
		*destino = kb * 1024
		vistos++
	}
	if vistos < 2 {
		return 0, 0, fmt.Errorf("meminfo: faltan MemTotal o MemAvailable")
	}
	return total, disponible, nil
}

// analizarMiligrados lee /sys/class/thermal/thermal_zone0/temp: «55814».
func analizarMiligrados(s string) (float64, error) {
	n, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0, fmt.Errorf("temperatura: %w", err)
	}
	return n / 1000, nil
}

// analizarKiloHercios lee scaling_cur_freq / cpuinfo_max_freq y da MHz.
func analizarKiloHercios(s string) (int, error) {
	n, err := strconv.ParseUint(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("frecuencia: %w", err)
	}
	return int(n / 1000), nil
}

// analizarThrottled lee el valor hexadecimal del firmware.
//
// Admite las dos formas que existen: la de sysfs, «0x80000» a secas, y la de
// vcgencmd, «throttled=0x80000». Un solo analizador para las dos fuentes evita
// que una se quede sin probar.
func analizarThrottled(s string) (Throttled, error) {
	t := strings.TrimSpace(s)
	if _, resto, ok := strings.Cut(t, "="); ok {
		t = strings.TrimSpace(resto)
	}
	// Algunos kernels lo publican con el prefijo y otros sin él.
	n, err := strconv.ParseUint(strings.TrimPrefix(t, "0x"), 16, 64)
	if err != nil {
		return Throttled{}, fmt.Errorf("throttled %q: %w", t, err)
	}
	return descomponerThrottled(n), nil
}

// throttledDeAlarma construye un Throttled PARCIAL a partir de la alarma de
// subtensión del hwmon rpi_volt, que es lo único que ese driver expone.
//
// Se marca Parcial para que nadie lea los bits térmicos —que aquí valen
// siempre falso— como «no ha habido limitación térmica». Eso sería justo la
// mentira que este paquete existe para no contar.
func throttledDeAlarma(valor uint64) Throttled {
	return Throttled{
		Disponible:         true,
		Parcial:            true,
		SubtensionAhora:    valor != 0,
		SubtensionOcurrida: valor != 0,
	}
}

func descomponerThrottled(n uint64) Throttled {
	bit := func(i uint) bool { return n&(1<<i) != 0 }
	return Throttled{
		Disponible: true,
		Bruto:      n,

		SubtensionAhora:       bit(0),
		FrecuenciaCapadaAhora: bit(1),
		LimitadoAhora:         bit(2),
		TermicoBlandoAhora:    bit(3),

		SubtensionOcurrida:       bit(16),
		FrecuenciaCapadaOcurrida: bit(17),
		LimitadoOcurrida:         bit(18),
		TermicoBlandoOcurrida:    bit(19),
	}
}

// Hex devuelve el valor tal como lo imprimiría vcgencmd, para poder cotejarlo
// a mano contra las mediciones que ya constan en la documentación.
func (t Throttled) Hex() string {
	switch {
	case !t.Disponible:
		return "no disponible"
	case t.Parcial:
		// NO se imprime «0x0»: se leería como la palabra completa del firmware
		// diciendo que no ha habido ninguna limitación, y esta fuente no sabe
		// nada de lo térmico.
		if t.SubtensionAhora {
			return "solo subtensión: SÍ"
		}
		return "solo subtensión: no"
	}
	return "0x" + strconv.FormatUint(t.Bruto, 16)
}

// analizarMontaje busca un punto de montaje en el texto de /proc/mounts y
// devuelve su dispositivo y si está montado de solo lectura.
func analizarMontaje(s, punto string) (dispositivo string, soloLectura, ok bool) {
	for l := range strings.SplitSeq(s, "\n") {
		campos := strings.Fields(l)
		if len(campos) < 4 {
			continue
		}
		if desescaparMontaje(campos[1]) != punto {
			continue
		}
		// La primera opción es siempre «ro» o «rw».
		ro := slices.Contains(strings.Split(campos[3], ","), "ro")
		// Se sigue recorriendo: si algo se montó dos veces sobre el mismo
		// punto, el último gana, que es lo que ve el sistema.
		dispositivo, soloLectura, ok = campos[0], ro, true
	}
	return dispositivo, soloLectura, ok
}

// desescaparMontaje deshace el escapado octal de /proc/mounts, que codifica
// espacios como \040. Sin esto, un punto de montaje con espacio no se
// encontraría nunca.
func desescaparMontaje(s string) string {
	if !strings.ContainsRune(s, '\\') {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if n, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// sectorDiskstats es fijo en /proc/diskstats, con independencia del tamaño de
// sector real del dispositivo: el kernel siempre reporta en sectores de 512 B.
const sectorDiskstats = 512

// analizarDiskstats busca un dispositivo —«sdb1», sin /dev/— y devuelve los
// bytes leídos y escritos desde el arranque.
func analizarDiskstats(s, dispositivo string) (leido, escrito uint64, ok bool) {
	for l := range strings.SplitSeq(s, "\n") {
		campos := strings.Fields(l)
		// major minor nombre + 11 campos mínimos del formato clásico.
		if len(campos) < 10 || campos[2] != dispositivo {
			continue
		}
		sectoresLeidos, err1 := strconv.ParseUint(campos[5], 10, 64)
		sectoresEscritos, err2 := strconv.ParseUint(campos[9], 10, 64)
		if err1 != nil || err2 != nil {
			return 0, 0, false
		}
		return sectoresLeidos * sectorDiskstats, sectoresEscritos * sectorDiskstats, true
	}
	return 0, 0, false
}

// analizarKilobytes lee los contadores de ext4 que están en KiB, como
// /sys/fs/ext4/<dev>/lifetime_write_kbytes, y devuelve bytes.
func analizarKilobytes(s string) (uint64, error) {
	n, err := analizarEntero(s)
	if err != nil {
		return 0, err
	}
	return n * 1024, nil
}

// analizarEntero lee un contador pelado, como errors_count. Existe separado de
// analizarKilobytes porque una CUENTA no son kilobytes: reutilizar aquel
// obligaba a multiplicar por 1024 para dividir después, y esa clase de vaivén
// es donde se cuela un factor perdido.
func analizarEntero(s string) (uint64, error) {
	return strconv.ParseUint(strings.TrimSpace(s), 10, 64)
}

// nombreDeDispositivo pasa «/dev/sdb1» a «sdb1», que es como lo nombran
// /proc/diskstats y /sys/fs/ext4.
func nombreDeDispositivo(ruta string) string {
	if i := strings.LastIndexByte(ruta, '/'); i >= 0 {
		return ruta[i+1:]
	}
	return ruta
}
