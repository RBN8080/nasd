package web

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"nasd/internal/almacen"
)

// Flechas del visor — pasar de foto sin volver al listado.
//
// Lo que se comprueba aquí es SIEMPRE lo mismo: que la secuencia por la que
// avanzan las flechas es exactamente la que el listado enseña, con el mismo
// criterio de orden y con los mismos archivos dentro. Cualquier divergencia
// entre las dos se manifiesta ante el usuario como «se salta una», que es un
// defecto que nadie sabe reproducir.

// reFlecha saca el destino de una de las dos flechas del cuerpo de la página.
// Se ancla en la clase y no en el texto del enlace porque el glifo es
// presentación: cambiar «‹» por otra cosa no debe romper estas pruebas.
var reFlecha = regexp.MustCompile(`class="paso paso-(anterior|siguiente)"[^>]*href="([^"]*)"`)

// flechasDe devuelve los destinos de las dos flechas, o "" donde no hay.
func flechasDe(cuerpo string) (anterior, siguiente string) {
	for _, m := range reFlecha.FindAllStringSubmatch(cuerpo, -1) {
		if m[1] == "anterior" {
			anterior = m[2]
		} else {
			siguiente = m[2]
		}
	}
	return anterior, siguiente
}

// agregarConFecha es agregar() con fecha de modificación, que es lo que el
// orden por fecha necesita para no comparar ceros.
func (a *almacenDeApertura) agregarConFecha(t *testing.T, nombre string, cuando time.Time) almacen.RutaSegura {
	t.Helper()
	ruta := a.agregar(t, nombre, []byte("x"))
	a.entradas[len(a.entradas)-1].Modificado = cuando
	return ruta
}

func comprobarFlechas(t *testing.T, s *Servidor, url, queAnterior, queSiguiente string) {
	t.Helper()
	anterior, siguiente := flechasDe(peticionConSesion(t, s, url).Body.String())
	if anterior != queAnterior {
		t.Errorf("%s: flecha anterior -> %q; se esperaba %q", url, anterior, queAnterior)
	}
	if siguiente != queSiguiente {
		t.Errorf("%s: flecha siguiente -> %q; se esperaba %q", url, siguiente, queSiguiente)
	}
}

// El caso corriente y los dos extremos. En el primero y en el último NO hay
// flecha: un control apagado se distingue mal de uno que no responde, y la
// ausencia ya dice que se acabaron.
func TestLasFlechasLlevanAlVecinoYDesaparecenEnLosExtremos(t *testing.T) {
	s, a := servidorDeApertura(t)
	for _, n := range []string{"01.jpg", "02.jpg", "03.jpg"} {
		a.agregar(t, n, []byte("\xff\xd8\xff"))
	}

	comprobarFlechas(t, s, "/abrir/02.jpg", "/abrir/01.jpg", "/abrir/03.jpg")
	comprobarFlechas(t, s, "/abrir/01.jpg", "", "/abrir/02.jpg")
	comprobarFlechas(t, s, "/abrir/03.jpg", "/abrir/02.jpg", "")
}

// FOTOS Y VÍDEOS SON UNA SOLA SECUENCIA, decidido por el responsable: una
// carpeta traída del iPhone intercala .HEIC con los .MOV de las Live Photos, y
// saltarse los vídeos avanzaría en un orden distinto del que se ve en el
// listado. Lo demás queda fuera: pasar de una foto al PDF de al lado con la
// flecha no es lo que nadie espera de una galería.
func TestLaSecuenciaLlevaFotosYVideosYNadaMas(t *testing.T) {
	s, a := servidorDeApertura(t)
	a.agregar(t, "a.jpg", []byte("\xff\xd8\xff"))
	a.agregar(t, "b.mov", []byte("ftyp"))
	a.agregar(t, "c.pdf", []byte("%PDF-1.4"))
	a.agregar(t, "d.zip", []byte("PK\x03\x04"))
	a.agregar(t, "e.png", []byte("\x89PNG\r\n\x1a\n"))

	// Desde el vídeo se salta por encima del PDF y del ZIP hasta la siguiente
	// imagen, sin dejar hueco.
	comprobarFlechas(t, s, "/abrir/b.mov", "/abrir/a.jpg", "/abrir/e.png")

	// Y lo que no es galería no lleva flechas, aunque se abra en el visor.
	if cuerpo := peticionConSesion(t, s, "/abrir/c.pdf").Body.String(); strings.Contains(cuerpo, "class=\"paso") {
		t.Error("un PDF lleva flechas de galería")
	}
}

// Coherencia con el listado, que oculta y cuenta los nombres que empiezan por
// punto —los «.sb-XXXX» que deja iOS—. Si las flechas no los excluyeran,
// llevarían a archivos que la carpeta no enseña.
func TestLosOcultosNoEntranEnLaSecuencia(t *testing.T) {
	s, a := servidorDeApertura(t)
	a.agregar(t, "a.jpg", []byte("\xff\xd8\xff"))
	a.agregar(t, ".b.jpg", []byte("\xff\xd8\xff"))
	a.agregar(t, "c.jpg", []byte("\xff\xd8\xff"))

	comprobarFlechas(t, s, "/abrir/a.jpg", "", "/abrir/c.jpg")
}

// EL ORDEN MANDA SOBRE LA SECUENCIA, no solo sobre el listado.
//
// Los tres nombres están elegidos para que el orden alfabético y el de fecha
// NO coincidan: si las flechas siguieran el criterio de por omisión en lugar
// del que trae la URL, esta prueba fallaría en las dos comprobaciones.
func TestLasFlechasSiguenElOrdenQueTraeLaURL(t *testing.T) {
	s, a := servidorDeApertura(t)
	base := time.Date(2026, 8, 19, 12, 0, 0, 0, time.Local)
	a.agregarConFecha(t, "medio.jpg", base.Add(-time.Hour))
	a.agregarConFecha(t, "nuevo.jpg", base)
	a.agregarConFecha(t, "viejo.jpg", base.Add(-24*time.Hour))

	// Por nombre: medio, nuevo, viejo.
	comprobarFlechas(t, s, "/abrir/medio.jpg", "", "/abrir/nuevo.jpg")

	// Por fecha, lo más reciente primero: nuevo, medio, viejo. Y el orden
	// viaja en los dos enlaces, o el siguiente paso volvería al alfabético.
	comprobarFlechas(t, s, "/abrir/medio.jpg?orden=modificado",
		"/abrir/nuevo.jpg?orden=modificado", "/abrir/viejo.jpg?orden=modificado")
}

// El orden entra al visor desde el listado, así que el listado tiene que
// ponerlo en el enlace. Antes de esto, ordenar por fecha y abrir una foto
// devolvía —y ahora además avanzaba— por nombre.
func TestElListadoLlevaElOrdenHastaElVisor(t *testing.T) {
	s, a := servidorDeApertura(t)
	a.agregar(t, "foto.png", []byte("\x89PNG\r\n\x1a\n"))

	cuerpo := peticionConSesion(t, s, "/?orden=tamano").Body.String()
	if !strings.Contains(cuerpo, `href="/abrir/foto.png?orden=tamano"`) {
		t.Error("el listado ordenado por tamaño no le pasa el orden al visor")
	}

	// Y el orden de por omisión NO se escribe: los enlaces del uso normal
	// quedan limpios, igual que en urlDeListadoCon.
	limpio := peticionConSesion(t, s, "/").Body.String()
	if strings.Contains(limpio, "?orden=nombre") {
		t.Error("el orden por omisión se está escribiendo en los enlaces")
	}
}

// LAS FLECHAS NO PUEDEN VIVIR DENTRO DE LO QUE visor.js BORRA.
//
// noCompatible() elimina #visor y #cabecera-visor enteros cuando el navegador
// declara que no puede con el formato. Un HEIC del iPhone abierto en Brave es
// exactamente ese caso, y es también el archivo que más falta hace poder
// saltarse: con las flechas dentro de esos elementos, desaparecerían justo
// ahí. Esta prueba fija la estructura, no el estilo.
func TestLasFlechasSobrevivenAlMensajeDeNoCompatible(t *testing.T) {
	s, a := servidorDeApertura(t)
	a.agregar(t, "a.jpg", []byte("\xff\xd8\xff"))
	a.agregar(t, "b.heic", []byte("ftypheic"))

	cuerpo := peticionConSesion(t, s, "/abrir/b.heic").Body.String()

	visor := strings.Index(cuerpo, `id="visor"`)
	galeria := strings.Index(cuerpo, `class="galeria"`)
	if visor < 0 || galeria < 0 {
		t.Fatalf("falta el visor (%d) o la galería (%d)", visor, galeria)
	}
	cierre := strings.Index(cuerpo[visor:], "</main>")
	if cierre < 0 {
		t.Fatal("el <main> del visor no se cierra")
	}
	if galeria < visor+cierre {
		t.Error("la galería está dentro de #visor: visor.js la borraría junto con él")
	}
}
