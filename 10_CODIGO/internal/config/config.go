// Package config lee la configuración del servicio.
//
// P4 del charter: la configuración que varía entre entornos vive en el
// entorno, nunca en el árbol de fuentes. Cero secretos en el repositorio.
// P3: nada de magic numbers — toda constante operativa se nombra aquí, con
// su unidad y su razón.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	// Volumen es el punto de montaje del disco de datos (ADR-0019).
	// Dentro debe haber datos/ y estado/parciales/.
	Volumen string

	// Direccion es la interfaz de escucha. ADR-0018: se enlaza a la IP de
	// la LAN, no a 0.0.0.0, de modo que el proceso ni siquiera escuche
	// fuera de ella. El cortafuegos es el segundo control, no el único.
	Direccion string
	Puerto    int

	// --- Fase 6 · TLS (ADR-0046) -----------------------------------------
	//
	// TLS termina AQUÍ, no en un proxy delante, y el motivo no es preferencia:
	// el limitador de intentos identifica al cliente con r.RemoteAddr, y tras
	// un proxy todas las peticiones llegarían desde 127.0.0.1 — cinco fallos
	// de cualquiera dejarían fuera a todo el mundo (07_AUDITORIAS §7.2).
	//
	// A diferencia de Direccion, DireccionTLS escucha en todas las
	// direcciones IPv6: el 443 es la superficie que la Fase 6 abre a
	// propósito (ADR-0048). El 80 se queda donde estaba, enlazado a la IP
	// de la LAN.
	//
	// EL VALOR CORRECTO ES "::" Y NO "0.0.0.0", y la diferencia no es
	// cosmética: en Go, enlazar a "0.0.0.0" abre SOLO IPv4.
	//
	// Importa porque la única vía de entrada viable desde Internet es IPv6
	// —el CGNAT del operador (ADR-0044) complica la IPv4—, así que un socket
	// solo-IPv4 dejaría la Fase 6 muerta sin que nada lo delatara: el puerto
	// aparece abierto, el cortafuegos correcto, y no entra nadie.
	//
	// "::" YA NO SIGNIFICA DOBLE PILA — corregido el 2026-08-06. web.
	// EscucharTLS abre este socket con la red "tcp6", no "tcp": acepta
	// cualquier IPv6, y el kernel rechaza IPv4 antes de llegar a TLS. Hasta
	// esa fecha "tcp" + "::" SÍ era doble pila y aceptaba IPv4 también, y
	// eso dejaba el 443 respondiendo TLS también en la IP de la LAN, con un
	// certificado que no la nombra — nadie lo decidió, lo encontró Safari en
	// iOS al sondear HTTPS antes de una navegación en pestaña nueva (RF-25).
	DireccionTLS string
	PuertoTLS    int

	// Rutas del certificado. Si CUALQUIERA de las dos está vacía, TLS queda
	// apagado y nasd sirve solo HTTP — que es el estado válido antes de que
	// 15_tls.sh haya emitido nada.
	Certificado string
	ClaveTLS    string

	// Plazos — ADR-0026. Los absolutos NO se fijan: ver servidor.go.
	PlazoCabeceras   time.Duration
	PlazoOcioso      time.Duration
	PlazoInactividad time.Duration

	// DuracionSesion — RF-15. Sin TLS (ADR-0018) una cookie robada vale lo
	// que dure, así que no se pone eterna. Siete días equilibra comodidad y
	// exposición para un uso doméstico. [R]
	DuracionSesion time.Duration

	// InactividadSesion — RF-15, ADR-0059. Tope DESLIZANTE: sin actividad
	// este tiempo, la sesión deja de ser válida aunque DuracionSesion siga
	// lejos. Los dos relojes se aplican a la vez (internal/autenticacion).
	// Cinco minutos, decidido por el responsable frente a un minuto: cada
	// verificación de contraseña cuesta 3.2-3.6 s de PBKDF2 en el nodo
	// (web.IteracionesPBKDF2), y un minuto castigaba la lectura normal.
	InactividadSesion time.Duration

	// DirectorioEstado es donde vive el registro de usuarios (ADR-0055).
	//
	// NO es el disco de datos, y la diferencia importa: en el disco de datos
	// lo vería SMB, y ese disco está pensado para sobrevivir a la placa y
	// viajar — que es justo lo que no se quiere de unas credenciales.
	//
	// /var/lib/nasd es lo que systemd llama StateDirectory: estado persistente
	// y escribible de un servicio (systemd.exec(5)). La unidad lo declara y
	// systemd lo crea con el dueño correcto antes de arrancar.
	DirectorioEstado string
}

// RutaUsuarios es el archivo del registro de cuentas.
func (c Config) RutaUsuarios() string {
	return filepath.Join(c.DirectorioEstado, "usuarios")
}

// RutaUsoDisco es el archivo donde P-4 etapa 3 guarda la última medida de
// uso de disco de cada cuenta, para que sobreviva a un reinicio del
// servicio (internal/metricas).
func (c Config) RutaUsoDisco() string {
	return filepath.Join(c.DirectorioEstado, "uso-disco")
}

// RutaSeguridad es el historial de rechazos (internal/seguridad).
//
// Va en DirectorioEstado y no en el disco de datos por la misma razón que el
// registro de usuarios: allí lo vería SMB, y este archivo lleva las
// direcciones de origen de quien intenta entrar. Publicarlo por SMB sería
// regalar el mapa de lo que se está observando — el mismo argumento con el
// que ADR-0037 puso el diario en estado/ y no en datos/.
func (c Config) RutaSeguridad() string {
	return filepath.Join(c.DirectorioEstado, "seguridad")
}

// RutaConexiones es el historial de conexiones entrantes de Internet
// (internal/seguridad, ADR-0064). Mismo directorio y mismo argumento que
// RutaSeguridad: son direcciones de quien toca la puerta, y por SMB serían el
// mapa de lo que se está observando.
//
// ARCHIVO APARTE Y NO UNA SECCIÓN DEL ANTERIOR: son dos anillos con tamaños,
// ritmos y contenidos distintos, y meterlos en el mismo archivo obligaría a
// reescribir los dos cada vez que se ensuciara uno.
func (c Config) RutaConexiones() string {
	return filepath.Join(c.DirectorioEstado, "conexiones")
}

// RutaCuarentena es la lista de direcciones apartadas por conducta
// (internal/seguridad). Mismo directorio y mismo argumento que las dos de
// arriba: son direcciones de quien toca la puerta.
//
// A DIFERENCIA DE ELLAS, ESTO NO ES OBSERVACIÓN SINO DECISIÓN: dice a quién se
// le está cerrando la puerta ahora mismo. Por eso importa que sobreviva al
// arranque —15 en 14 días medidos— y por eso vive en un archivo propio: los
// anillos se reescriben enteros cada minuto y esto casi nunca cambia.
func (c Config) RutaCuarentena() string {
	return filepath.Join(c.DirectorioEstado, "cuarentena")
}

// RutaLista son los bloqueos puestos a mano desde el panel.
//
// APARTE DE LA CUARENTENA, y no por simetría: son dos cosas con dueño
// distinto. La cuarentena la escribe el nodo y se puede perder sin más
// consecuencia que volver a apartar a quien insista; esto lo escribe una
// persona, lleva su motivo redactado y perderlo sería perder una decisión que
// nadie puede reconstruir.
func (c Config) RutaLista() string {
	return filepath.Join(c.DirectorioEstado, "bloqueos")
}

// RutaNovedades guarda cuándo se miró el panel de seguridad por última vez.
//
// Es UN dato y aun así tiene archivo propio: lo escribe alguien al MIRAR, no
// al pasar algo, así que su ritmo no se parece al de nada más. Meterlo en la
// lista de bloqueos obligaría a reescribir las decisiones cada vez que se abre
// una página.
func (c Config) RutaNovedades() string {
	return filepath.Join(c.DirectorioEstado, "novedades")
}

// RutaHallazgos son las respuestas inesperadas del propio servidor
// (internal/seguridad/hallazgos.go).
//
// MISMO directorio que las anteriores, y aquí el argumento de no publicarlo por
// SMB es el más fuerte de todos: este archivo es la lista de rutas que el nodo
// está sirviendo y no debería. Dejarlo donde lo vea cualquiera de la LAN sería
// entregar el mapa del problema junto con el problema.
//
// ARCHIVO PROPIO Y NO UNA SECCIÓN DEL ANILLO DE RECHAZOS, y no por simetría:
// aquel guarda sucesos y este guarda RUTAS agregadas, que es otra cosa y otro
// ritmo. En un nodo sano este archivo ni siquiera llega a existir.
func (c Config) RutaHallazgos() string {
	return filepath.Join(c.DirectorioEstado, "hallazgos")
}

// RutaToques es el historial que deja nas-sensor — RF-33, ADR-0066.
//
// NO VA EN DirectorioEstado, y es la única ruta de este archivo que no lo hace:
// ese directorio es el StateDirectory de nasd, y quien escribe aquí es OTRO
// servicio, con su propio usuario y su propio StateDirectory. nasd solo LEE.
//
// Esa separación es lo que permite cumplir la condición del responsable de no
// tocar lo que ya funciona: la unidad de nasd no cambia ni una directiva.
// ProtectSystem=strict deja el sistema en solo lectura, no inaccesible, y el
// directorio del sensor va con grupo «nas» para que esta lectura funcione sin
// concederle a nasd ningún permiso nuevo.
//
// Literal y no configurable, por lo mismo que prefijoLAN en internal/seguridad:
// una opción que nadie va a cambiar es exactamente el «por si acaso» que el
// estilo de este proyecto prohíbe.
func (c Config) RutaToques() string {
	return "/var/lib/nas-sensor/toques"
}

// RutaGeoIP es la base que resuelve una dirección a su país y su operador
// (internal/geoip).
//
// A DIFERENCIA DE LAS ANTERIORES, ESTA NO VIVE EN DirectorioEstado sino en el
// disco de datos, bajo estado/. El motivo es el tamaño: son ~35 MB que se
// reescriben enteros cada vez que el temporizador refresca la base, y
// DirectorioEstado (/var/lib) está en el medio de arranque, que es consumible
// (P9). El disco de datos tiene 897 GB libres y no se desgasta por esto.
//
// Va en estado/ y NO en datos/ por lo mismo que el diario (ADR-0037):
// ADR-0019 comparte por Samba únicamente datos/, y esto no tiene nada que
// hacer ahí.
func (c Config) RutaGeoIP() string {
	return filepath.Join(c.Volumen, "estado", "geoip")
}

// RutaMiniaturas es el directorio donde se cachean las miniaturas EXIF que
// extrae nas-miniatura (10_CODIGO/nas-miniatura/miniatura.c), un programa
// aparte en C sin dependencias — rector §7.nonies.bis, 2026-08-15.
//
// Mismo criterio que RutaGeoIP y por el mismo motivo: va en estado/ y no en
// datos/ porque ADR-0019 comparte por Samba únicamente datos/, y una
// miniatura generada por el servicio no es un archivo del usuario. A
// diferencia de GeoIP, aquí el propio servicio ESCRIBE (GeoIP solo lee una
// base que prepara un script aparte), pero el directorio sigue cayendo
// dentro de ReadWritePaths=/srv/nas (05_instalar_servicio.sh) sin ampliar
// ese permiso ni un centímetro.
func (c Config) RutaMiniaturas() string {
	return filepath.Join(c.Volumen, "estado", "miniaturas")
}

// Valores de referencia [R] — no son criterio de aceptación (01_REQUISITOS §2).
// Se sustituyen por medición cuando exista.
func porDefecto() Config {
	return Config{
		Volumen:          "/srv/nas",
		Direccion:        "127.0.0.1",
		Puerto:           8080,
		DireccionTLS:     "::",
		PuertoTLS:        443,
		PlazoCabeceras:   10 * time.Second,
		PlazoOcioso:      120 * time.Second,
		PlazoInactividad: 60 * time.Second,
		DuracionSesion:   7 * 24 * time.Hour,
		// El valor por defecto es el que manda en producción: el TOML del
		// nodo lo instala 05_instalar_servicio.sh solo la PRIMERA vez
		// (if [ ! -f "$CONFIG" ]), así que una clave nueva en el script no
		// llega a un nodo que ya tiene su archivo — solo el binario la trae.
		InactividadSesion: 5 * time.Minute,
		DirectorioEstado:  "/var/lib/nasd",
	}
}

// Cargar lee el archivo indicado. Si ruta está vacía, devuelve los valores
// por defecto. Las variables de entorno NASD_* tienen prioridad sobre el
// archivo (factor III de The Twelve-Factor App, charter P4).
func Cargar(ruta string) (Config, error) {
	c := porDefecto()

	if ruta != "" {
		f, err := os.Open(ruta)
		if err != nil {
			return c, fmt.Errorf("abrir la configuración: %w", err)
		}
		defer f.Close()
		v, err := leerTOML(f)
		if err != nil {
			return c, fmt.Errorf("%s: %w", ruta, err)
		}
		if s, ok := v["volumen"]; ok {
			c.Volumen = s
		}
		if s, ok := v["red.direccion"]; ok {
			c.Direccion = s
		}
		if s, ok := v["red.puerto"]; ok {
			n, err := strconv.Atoi(s)
			if err != nil {
				return c, fmt.Errorf("red.puerto: %w", err)
			}
			c.Puerto = n
		}
		if s, ok := v["red.direccion_tls"]; ok {
			c.DireccionTLS = s
		}
		if s, ok := v["red.puerto_tls"]; ok {
			n, err := strconv.Atoi(s)
			if err != nil {
				return c, fmt.Errorf("red.puerto_tls: %w", err)
			}
			c.PuertoTLS = n
		}
		if s, ok := v["tls.certificado"]; ok {
			c.Certificado = s
		}
		if s, ok := v["tls.clave"]; ok {
			c.ClaveTLS = s
		}
		if s, ok := v["sesion.duracion_horas"]; ok {
			n, err := strconv.Atoi(s)
			if err != nil || n < 1 {
				return c, fmt.Errorf("sesion.duracion_horas: debe ser un entero positivo")
			}
			c.DuracionSesion = time.Duration(n) * time.Hour
		}
		// sesion.inactividad_s — ADR-0059. NO CONFUNDIR con
		// plazos.inactividad_s: aquella es el plazo de E/S de ADR-0026 (se
		// renueva por bloque de bytes dentro de UNA petición); esta es el
		// tope deslizante de la SESIÓN entera (se renueva por petición).
		if s, ok := v["sesion.inactividad_s"]; ok {
			n, err := strconv.Atoi(s)
			if err != nil || n < 1 {
				return c, fmt.Errorf("sesion.inactividad_s: debe ser un entero positivo")
			}
			c.InactividadSesion = time.Duration(n) * time.Second
		}
		if s, ok := v["estado.directorio"]; ok {
			c.DirectorioEstado = s
		}
		if s, ok := v["plazos.inactividad_s"]; ok {
			n, err := strconv.Atoi(s)
			if err != nil {
				return c, fmt.Errorf("plazos.inactividad_s: %w", err)
			}
			c.PlazoInactividad = time.Duration(n) * time.Second
		}
	}

	if s := os.Getenv("NASD_VOLUMEN"); s != "" {
		c.Volumen = s
	}
	if s := os.Getenv("NASD_DIRECCION"); s != "" {
		c.Direccion = s
	}
	if s := os.Getenv("NASD_PUERTO"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil {
			return c, fmt.Errorf("NASD_PUERTO: %w", err)
		}
		c.Puerto = n
	}
	if s := os.Getenv("NASD_ESTADO"); s != "" {
		c.DirectorioEstado = s
	}
	// STATE_DIRECTORY la pone systemd a partir de StateDirectory= y MANDA
	// sobre lo demás: es la ruta que systemd ha creado de verdad, con el dueño
	// del servicio. Si el TOML dijera otra cosa, ganaría una carpeta que quizá
	// ni existe.
	//
	// Puede traer varias rutas separadas por «:» si la unidad declara varias
	// (systemd.exec(5)); aquí solo hay una, y se toma la primera.
	if s := os.Getenv("STATE_DIRECTORY"); s != "" {
		primera, _, _ := strings.Cut(s, ":")
		c.DirectorioEstado = primera
	}

	return c, c.validar()
}

// validar falla ruidosamente y temprano (P5). Un servicio mal configurado no
// debe arrancar y aceptar peticiones que fracasarán después.
func (c Config) validar() error {
	if c.Volumen == "" {
		return fmt.Errorf("volumen: no puede estar vacío")
	}
	if c.Puerto < 1 || c.Puerto > 65535 {
		return fmt.Errorf("puerto %d: fuera de rango", c.Puerto)
	}
	// Los puertos <1024 exigen CAP_NET_BIND_SERVICE. ADR-0032 la concede por
	// AmbientCapabilities en la unidad systemd, SIN elevar el proceso: sigue
	// corriendo como el usuario nas. Si falta esa línea, el enlace falla al
	// arrancar y se ve de inmediato.
	if c.Direccion == "" {
		return fmt.Errorf("direccion: no puede estar vacía; use la IP de la LAN (ADR-0018)")
	}
	// Sin sitio donde vivir el registro no se puede dar de alta a nadie, y el
	// fallo aparecería mucho más tarde, al intentarlo (P5).
	if c.DirectorioEstado == "" {
		return fmt.Errorf("estado.directorio: no puede estar vacío (ADR-0055)")
	}

	// TLS: o están las dos rutas o no está ninguna. Media configuración es la
	// forma más cara de descubrir un error, porque arrancaría sirviendo HTTP
	// mientras alguien cree que hay TLS (P5).
	if (c.Certificado == "") != (c.ClaveTLS == "") {
		return fmt.Errorf("tls: certificado y clave se configuran juntos o ninguno (ADR-0046)")
	}
	if c.TLSActivo() {
		if c.PuertoTLS < 1 || c.PuertoTLS > 65535 {
			return fmt.Errorf("puerto_tls %d: fuera de rango", c.PuertoTLS)
		}
		if c.PuertoTLS == c.Puerto {
			return fmt.Errorf("puerto_tls %d: no puede ser el mismo que el de HTTP", c.PuertoTLS)
		}
		if c.DireccionTLS == "" {
			return fmt.Errorf("direccion_tls: no puede estar vacía si hay certificado")
		}
	}
	return nil
}

// TLSActivo indica si hay certificado configurado. Sin él, nasd sirve solo
// HTTP, que es el estado correcto antes de que 15_tls.sh haya emitido nada.
func (c Config) TLSActivo() bool {
	return c.Certificado != "" && c.ClaveTLS != ""
}
