package respaldo

import (
	"strings"
	"testing"
	"time"
)

// El archivo REAL, copiado tal cual del nodo el 2026-09-02.
//
// Se prueba contra el original y no contra un ejemplo escrito a mano, porque lo
// que hay que garantizar no es que el analizador funcione: es que funciona con
// lo que el cliente escribe de verdad, incluidas las claves que a este paquete
// no le interesan.
const estadoReal = `# ESTADO.txt - lo que lee el indicador (seccion 10.2)
# Escrito de forma atomica: nunca se lee a medias.
estado=Protegido
momento=2026-09-02T15:40:25
detalle=8 raices al dia, 7 archivos copiados
disco_detalle=COMPLETA: las dos pasadas, 67 archivos nuevos
disco_estado=Protegido
disco_momento=2026-09-02T11:41:47
cambio=0.0
centinelas=8/8
copias=8
fallos=0
huerfanos=14
proxima_inicio=2026-09-02T19:00:00
proxima_ventana=19:00-22:00
raices=8
ventana=12:00-15:00
ventana_atiempo=False
ventana_indice=2/3
`

func TestLeeElArchivoRealDelNodo(t *testing.T) {
	e, err := Leer(strings.NewReader(estadoReal))
	if err != nil {
		t.Fatalf("el ESTADO.txt real no se pudo leer: %v", err)
	}
	if e.Veredicto != "Protegido" {
		t.Errorf("veredicto = %q; se esperaba Protegido", e.Veredicto)
	}
	if e.Raices != 8 || e.Copias != 8 || e.Fallos != 0 || e.Huerfanos != 14 {
		t.Errorf("contadores = %d raíces, %d copias, %d fallos, %d huérfanos",
			e.Raices, e.Copias, e.Fallos, e.Huerfanos)
	}
	if e.Centinelas != "8/8" {
		t.Errorf("centinelas = %q; se esperaba 8/8", e.Centinelas)
	}
	if e.Ventana != "12:00-15:00" {
		t.Errorf("ventana = %q", e.Ventana)
	}
	esperado := time.Date(2026, 9, 2, 15, 40, 25, 0, time.Local)
	if !e.Momento.Equal(esperado) {
		t.Errorf("momento = %v; se esperaba %v", e.Momento, esperado)
	}
}

// LAS CLAVES DE LA COPIA FRÍA NO SE ABSORBEN, y es una decisión de alcance: el
// nodo no ve ese disco y opinar sobre él sería inventarse una fuente.
func TestNoSeQuedaConLoQueNoLeIncumbe(t *testing.T) {
	e, err := Leer(strings.NewReader(estadoReal))
	if err != nil {
		t.Fatal(err)
	}
	// El estado del disco frío es «Protegido» en el archivo real. Si algún día
	// alguien lo mapeara al veredicto de este paquete, esta prueba no lo vería;
	// lo que sí se fija es que el detalle que se conserva es el del NODO.
	if strings.Contains(e.Detalle, "pasadas") {
		t.Errorf("el detalle absorbió la línea de la copia fría: %q", e.Detalle)
	}
}

func TestSinVeredictoNoHayEstado(t *testing.T) {
	// Un archivo con líneas pero sin «estado=» no es medio estado: es un archivo
	// inservible, y devolver un cero silencioso lo dejaría pasar por SinDatos.
	for _, caso := range []struct{ nombre, texto string }{
		{"vacío", ""},
		{"solo comentarios", "# nada\n# de nada\n"},
		{"sin la clave estado", "momento=2026-09-02T15:40:25\nraices=8\n"},
	} {
		if _, err := Leer(strings.NewReader(caso.texto)); err != ErrVacio {
			t.Errorf("%s: err = %v; se esperaba ErrVacio", caso.nombre, err)
		}
	}
}

// UN CONTADOR ILEGIBLE NO TUMBA LA LECTURA. Perder un número es un dato menos;
// perder el archivo entero es quedarse sin saber si hubo respaldo.
func TestUnContadorRotoNoSeLlevaElArchivo(t *testing.T) {
	e, err := Leer(strings.NewReader("estado=Protegido\nraices=muchas\nfallos=3\n"))
	if err != nil {
		t.Fatalf("un contador roto tumbó la lectura entera: %v", err)
	}
	if e.Raices != 0 {
		t.Errorf("raices = %d; un valor ilegible debe quedar en 0", e.Raices)
	}
	if e.Fallos != 3 {
		t.Errorf("fallos = %d; el contador bueno de al lado se perdió", e.Fallos)
	}
}

// EL UMBRAL LO PUBLICA EL CLIENTE, y su ausencia tiene que ser distinguible de
// un cero: si no lo fuera, el nodo no podría decir «usé mi valor de repuesto».
func TestElUmbralPublicadoSeLeeYSuAusenciaSeNota(t *testing.T) {
	con, err := Leer(strings.NewReader("estado=Protegido\nhoras_para_avisar=22\nhueco_maximo_horas=12\n"))
	if err != nil {
		t.Fatal(err)
	}
	if con.HorasParaAvisar != 22 {
		t.Errorf("horas_para_avisar = %d; se esperaba 22", con.HorasParaAvisar)
	}
	if con.HuecoMaximoHoras != 12 {
		t.Errorf("hueco_maximo_horas = %v; se esperaba 12", con.HuecoMaximoHoras)
	}

	sin, err := Leer(strings.NewReader(estadoReal))
	if err != nil {
		t.Fatal(err)
	}
	if sin.HorasParaAvisar != 0 {
		t.Errorf("un archivo sin la clave debe dejar 0 y no un umbral inventado; salió %d", sin.HorasParaAvisar)
	}
}

// LO QUE ENTRA ES TEXTO AJENO. Un archivo enorme escrito por SMB no puede
// convertirse en memoria del servicio: RNF-01 exige que el servicio no crezca
// con el tamaño de lo que le llega.
func TestNoSeTragaUnArchivoEnorme(t *testing.T) {
	var b strings.Builder
	b.WriteString("estado=Protegido\n")
	// Muy por encima de los dos topes a la vez: líneas y bytes.
	for i := 0; i < 50_000; i++ {
		b.WriteString("basura=")
		b.WriteString(strings.Repeat("x", 300))
		b.WriteString("\n")
	}
	e, err := Leer(strings.NewReader(b.String()))
	if err != nil {
		t.Fatalf("debía leer lo que cupo y parar, no fallar: %v", err)
	}
	if e.Veredicto != "Protegido" {
		t.Errorf("se perdió la primera línea útil: %q", e.Veredicto)
	}
}

func TestUnaLineaMasLargaQueElTopeNoRompeElResto(t *testing.T) {
	texto := "estado=Protegido\ndetalle=" + strings.Repeat("y", topeValor+50) + "\nfallos=2\n"
	e, err := Leer(strings.NewReader(texto))
	if err != nil {
		t.Fatalf("una línea larga tumbó la lectura: %v", err)
	}
	if e.Detalle != "" {
		t.Errorf("un valor por encima del tope debe descartarse, no truncarse a medias")
	}
	if e.Fallos != 2 {
		t.Errorf("la línea de después de la larga se perdió: fallos = %d", e.Fallos)
	}
}

// LAS DOS FORMAS DE LA MARCA. La de hoy es la de PowerShell «-Format s»; la
// otra se acepta por si algún día el cliente escribiera con zona.
func TestLasDosFormasDeLaMarcaDeTiempo(t *testing.T) {
	sinZona := momentoDelCliente("2026-09-02T15:40:25")
	if sinZona.IsZero() || sinZona.Location() != time.Local {
		t.Errorf("la marca sin zona debe leerse en la zona del nodo; salió %v", sinZona)
	}
	conZona := momentoDelCliente("2026-09-02T15:40:25-06:00")
	if conZona.IsZero() {
		t.Errorf("RFC 3339 no se reconoció")
	}
	if !momentoDelCliente("ayer por la tarde").IsZero() {
		t.Errorf("una marca ilegible debe dar el cero, para que quien juzgue lo pueda decir")
	}
}
