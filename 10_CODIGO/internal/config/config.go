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
	"strconv"
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
	// A diferencia de Direccion, DireccionTLS sí es 0.0.0.0: el 443 es la
	// superficie que la Fase 6 abre a propósito (ADR-0048). El 80 se queda
	// donde estaba, enlazado a la IP de la LAN.
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
}

// Valores de referencia [R] — no son criterio de aceptación (01_REQUISITOS §2).
// Se sustituyen por medición cuando exista.
func porDefecto() Config {
	return Config{
		Volumen:          "/srv/nas",
		Direccion:        "127.0.0.1",
		Puerto:           8080,
		DireccionTLS:     "0.0.0.0",
		PuertoTLS:        443,
		PlazoCabeceras:   10 * time.Second,
		PlazoOcioso:      120 * time.Second,
		PlazoInactividad: 60 * time.Second,
		DuracionSesion:   7 * 24 * time.Hour,
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
