package sistema

import (
	"testing"
	"time"
)

// Las muestras son del formato real de una Raspberry Pi 3B+ con Raspberry Pi
// OS. Se prueban aquí, en el host, porque los analizadores son funciones puras
// (D-06): así el análisis queda cubierto sin necesitar el nodo delante.

func TestAnalizarUptime(t *testing.T) {
	d, err := analizarUptime("128492.61 505871.28\n")
	if err != nil {
		t.Fatal(err)
	}
	if got := d.Round(time.Second); got != 128492*time.Second+610*time.Millisecond {
		// Se compara con holgura de redondeo, no al nanosegundo.
		if d < 128492*time.Second || d > 128493*time.Second {
			t.Fatalf("uptime = %v", got)
		}
	}
}

func TestAnalizarCarga(t *testing.T) {
	c1, c5, c15, err := analizarCarga("0.15 0.10 0.09 1/212 30456\n")
	if err != nil {
		t.Fatal(err)
	}
	if c1 != 0.15 || c5 != 0.10 || c15 != 0.09 {
		t.Fatalf("carga = %v %v %v", c1, c5, c15)
	}
	if _, _, _, err := analizarCarga("0.15\n"); err == nil {
		t.Fatal("una línea corta debería fallar, no devolver ceros")
	}
}

// El iowait NO cuenta como CPU ocupada. En este nodo el techo es el bus USB
// 2.0 (RES-02): contarlo pintaría el 100 % durante cualquier subida grande y
// dispararía una alarma por algo que es funcionamiento normal.
func TestAnalizarCPUExcluyeIowait(t *testing.T) {
	const muestra = `cpu  1000 0 500 8000 500 0 0 0 0 0
cpu0 250 0 125 2000 125 0 0 0 0 0
intr 12345
`
	total, ocioso, err := analizarCPU(muestra)
	if err != nil {
		t.Fatal(err)
	}
	if total != 10000 {
		t.Fatalf("total = %d, se esperaban 10000", total)
	}
	if ocioso != 8500 {
		t.Fatalf("ocioso = %d, se esperaban 8500 (idle 8000 + iowait 500)", ocioso)
	}
}

func TestPorcentajeCPU(t *testing.T) {
	p, ok := porcentajeCPU(0, 0, 1000, 750)
	if !ok || p != 25 {
		t.Fatalf("porcentaje = %v (ok=%v), se esperaba 25", p, ok)
	}
	// Contador que no avanza: no se inventa un 0 %, se dice que no se pudo.
	if _, ok := porcentajeCPU(1000, 750, 1000, 750); ok {
		t.Fatal("sin avance del contador no debe darse por bueno un valor")
	}
}

func TestAnalizarMeminfo(t *testing.T) {
	const muestra = `MemTotal:         948120 kB
MemFree:           98304 kB
MemAvailable:     651264 kB
Buffers:           30000 kB
`
	total, disp, err := analizarMeminfo(muestra)
	if err != nil {
		t.Fatal(err)
	}
	if total != 948120*1024 || disp != 651264*1024 {
		t.Fatalf("meminfo = %d / %d", total, disp)
	}
	// Sin MemAvailable no se cae en MemFree por su cuenta: falla y se dice.
	if _, _, err := analizarMeminfo("MemTotal: 948120 kB\nMemFree: 98304 kB\n"); err == nil {
		t.Fatal("faltando MemAvailable debería fallar")
	}
}

// Los dos valores que este proyecto tiene MEDIDOS y escritos en la
// documentación. Si algún día se leen al revés, esta prueba lo dice.
func TestAnalizarThrottledValoresMedidosDelNodo(t *testing.T) {
	casos := []struct {
		entrada         string
		ahora, ocurrida bool
		descripcion     string
	}{
		{"0x80000\n", false, true,
			"límite térmico blando alcanzado alguna vez, apagado ahora (§12.3, 55.8 °C)"},
		{"0x80008\n", true, true,
			"límite térmico blando ACTIVO además de ocurrido (v1.5.0, 60.1 °C)"},
		{"0x0\n", false, false, "nodo limpio"},
	}
	for _, c := range casos {
		th, err := analizarThrottled(c.entrada)
		if err != nil {
			t.Fatalf("%s: %v", c.descripcion, err)
		}
		if !th.Disponible {
			t.Fatalf("%s: debería estar disponible", c.descripcion)
		}
		if th.TermicoBlandoAhora != c.ahora || th.TermicoBlandoOcurrida != c.ocurrida {
			t.Fatalf("%s: ahora=%v ocurrida=%v", c.descripcion,
				th.TermicoBlandoAhora, th.TermicoBlandoOcurrida)
		}
	}
}

func TestAnalizarThrottledSubtension(t *testing.T) {
	// Bits 0 y 16: subtensión ahora y ocurrida. El charter §3.6 los midió a
	// cero, y son los que decidirían que el disco necesita fuente externa.
	th, err := analizarThrottled("0x10001")
	if err != nil {
		t.Fatal(err)
	}
	if !th.SubtensionAhora || !th.SubtensionOcurrida {
		t.Fatalf("0x10001 debería encender los dos bits de subtensión: %+v", th)
	}
	if th.Hex() != "0x10001" {
		t.Fatalf("Hex() = %s", th.Hex())
	}
}

func TestThrottledNoDisponibleNoEsCero(t *testing.T) {
	var th Throttled
	if th.Hex() != "no disponible" {
		t.Fatalf("un throttled sin leer NO puede presentarse como 0x0, dio %q", th.Hex())
	}
}

// vcgencmd imprime «throttled=0x0», sysfs imprime «0x0» a secas. Un solo
// analizador para las dos fuentes, y aquí se prueba que de verdad lo es: si
// alguna quedara sin cubrir, se descubriría en el nodo y no en el host.
func TestAnalizarThrottledAdmiteLasDosFormas(t *testing.T) {
	casos := []string{"0x80008", "throttled=0x80008", "  throttled=0x80008 \n"}
	for _, c := range casos {
		th, err := analizarThrottled(c)
		if err != nil {
			t.Fatalf("%q: %v", c, err)
		}
		if th.Bruto != 0x80008 || !th.TermicoBlandoAhora || !th.TermicoBlandoOcurrida {
			t.Fatalf("%q dio %+v", c, th)
		}
		if th.Parcial {
			t.Fatalf("%q: la palabra completa del firmware no es parcial", c)
		}
	}
}

// El respaldo del hwmon solo sabe de subtensión. Lo peligroso sería que sus
// bits térmicos —siempre falsos— se leyeran como «no ha habido limitación
// térmica», que es afirmar algo que esa fuente no puede saber.
func TestElRespaldoDeHwmonSeDeclaraParcialYNoImprimeCero(t *testing.T) {
	sin := throttledDeAlarma(0)
	if !sin.Disponible || !sin.Parcial {
		t.Fatalf("debería estar disponible y marcado parcial: %+v", sin)
	}
	if sin.SubtensionAhora || sin.SubtensionOcurrida {
		t.Fatalf("alarma a 0 no debe encender subtensión: %+v", sin)
	}
	if sin.Hex() == "0x0" {
		t.Fatal("un respaldo parcial NO puede imprimirse como 0x0: se leería como " +
			"la palabra completa del firmware diciendo que no hubo limitación alguna")
	}

	con := throttledDeAlarma(1)
	if !con.SubtensionAhora || !con.SubtensionOcurrida {
		t.Fatalf("alarma a 1 debe encender subtensión: %+v", con)
	}
}

func TestAnalizarMontaje(t *testing.T) {
	const muestra = `/dev/sda2 / ext4 rw,noatime 0 0
/dev/sdb1 /srv/nas ext4 rw,relatime 0 0
tmpfs /run tmpfs rw,nosuid 0 0
`
	dispositivo, ro, ok := analizarMontaje(muestra, "/srv/nas")
	if !ok || dispositivo != "/dev/sdb1" || ro {
		t.Fatalf("srv/nas = %q ro=%v ok=%v", dispositivo, ro, ok)
	}
	if _, _, ok := analizarMontaje(muestra, "/no/existe"); ok {
		t.Fatal("un punto inexistente no puede darse por encontrado")
	}
}

func TestAnalizarMontajeDetectaSoloLectura(t *testing.T) {
	// El síntoma clásico de un medio de arranque agonizante.
	const muestra = "/dev/sda2 / ext4 ro,noatime,errors=remount-ro 0 0\n"
	_, ro, ok := analizarMontaje(muestra, "/")
	if !ok || !ro {
		t.Fatalf("debería detectarse ro: ro=%v ok=%v", ro, ok)
	}
}

func TestAnalizarMontajeConEspacioEscapado(t *testing.T) {
	const muestra = "/dev/sdc1 /media/usuario/DISCO\\040EXTERNO ext4 rw 0 0\n"
	dispositivo, _, ok := analizarMontaje(muestra, "/media/usuario/DISCO EXTERNO")
	if !ok || dispositivo != "/dev/sdc1" {
		t.Fatalf("el escapado octal de /proc/mounts no se deshizo: %q ok=%v", dispositivo, ok)
	}
}

func TestAnalizarDiskstats(t *testing.T) {
	const muestra = `   8       0 sda 12345 100 987654 4000 5000 200 123456 3000 0 5000 7000
   8       2 sda2 11000 90 900000 3900 4800 190 120000 2900 0 4900 6800
   8      16 sdb 500 10 40000 200 9000 50 8000000 12000 0 9000 12200
   8      17 sdb1 480 8 39000 190 8900 45 7999000 11900 0 8900 12000
`
	leido, escrito, ok := analizarDiskstats(muestra, "sdb1")
	if !ok {
		t.Fatal("sdb1 debería encontrarse")
	}
	if leido != 39000*512 || escrito != 7999000*512 {
		t.Fatalf("sdb1 = %d leídos, %d escritos", leido, escrito)
	}
	if _, _, ok := analizarDiskstats(muestra, "nvme0n1"); ok {
		t.Fatal("un dispositivo ausente no puede darse por encontrado")
	}
}

func TestAnalizarKilobytesYEntero(t *testing.T) {
	// lifetime_write_kbytes está en KiB; errors_count es una cuenta pelada.
	// Que sean funciones distintas es el punto de la prueba.
	b, err := analizarKilobytes("2097152\n")
	if err != nil || b != 2097152*1024 {
		t.Fatalf("kilobytes = %d, %v", b, err)
	}
	n, err := analizarEntero("3\n")
	if err != nil || n != 3 {
		t.Fatalf("entero = %d, %v", n, err)
	}
}

func TestUsoPorcentaje(t *testing.T) {
	v := Volumen{Disponible: true, TotalBytes: 1000, LibresBytes: 250}
	if got := v.UsoPorcentaje(); got != 75 {
		t.Fatalf("uso = %v", got)
	}
	// Un volumen que no se pudo leer devuelve -1, no 0: un 0 % de uso se
	// leería como «vacío y sano», que es lo contrario de «no se sabe».
	if got := (Volumen{}).UsoPorcentaje(); got != -1 {
		t.Fatalf("un volumen no disponible debe dar -1, dio %v", got)
	}
}

func TestRAMUsoPorcentajeUsaDisponible(t *testing.T) {
	n := Nodo{Vivo: Vivo{RAMOK: true, RAMTotalBytes: 1000, RAMDisponibleBytes: 400}}
	if got := n.RAMUsoPorcentaje(); got != 60 {
		t.Fatalf("uso de RAM = %v", got)
	}
	if got := (Nodo{}).RAMUsoPorcentaje(); got != -1 {
		t.Fatalf("sin lectura debe dar -1, dio %v", got)
	}
}

func TestNombreDeDispositivo(t *testing.T) {
	if got := nombreDeDispositivo("/dev/sdb1"); got != "sdb1" {
		t.Fatalf("= %q", got)
	}
	if got := nombreDeDispositivo("sdb1"); got != "sdb1" {
		t.Fatalf("= %q", got)
	}
}
