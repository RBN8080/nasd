package respaldo

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func escribir(t *testing.T, contenido string) string {
	t.Helper()
	ruta := filepath.Join(t.TempDir(), "ESTADO.txt")
	if err := os.WriteFile(ruta, []byte(contenido), 0o600); err != nil {
		t.Fatal(err)
	}
	return ruta
}

// SIN RUTA NO SE PREGUNTA NADA. Es lo que hace que un nodo sin cliente de
// respaldo se comporte exactamente igual que antes de que este paquete
// existiera: el indicador no se pinta y no se abre ningún archivo.
func TestSinRutaNoEstaConfigurado(t *testing.T) {
	if l := NuevoVigia("").Leer(); l.Configurado {
		t.Errorf("una ruta vacía no puede quedar configurada: %+v", l)
	}
	// Y un vigía nulo tampoco revienta: instantaneaCompleta lo llama siempre.
	var v *Vigia
	if l := v.Leer(); l.Configurado {
		t.Errorf("un vigía nulo debe leerse como no configurado")
	}
}

// AUSENTE NO ES AVERÍA. Mientras el cliente no haya corrido ni una vez contra
// este nodo el archivo no existe, y eso no es nada roto.
func TestArchivoAusenteNoEsAveria(t *testing.T) {
	l := NuevoVigia(filepath.Join(t.TempDir(), "no_existe.txt")).Leer()
	if !l.Configurado {
		t.Errorf("con ruta, tiene que quedar configurado")
	}
	if l.Presente {
		t.Errorf("no hay archivo: Presente debe ser falso")
	}
	if l.Error != nil {
		t.Errorf("la ausencia se cuenta como avería: %v", l.Error)
	}
}

// PRESENTE E ILEGIBLE SÍ ES OTRA COSA, y la diferencia es la que decide entre
// «todavía no ha corrido» y «hay algo mal».
func TestArchivoPresenteYVacioSeDistingueDeAusente(t *testing.T) {
	l := NuevoVigia(escribir(t, "# solo comentarios\n")).Leer()
	if !l.Presente {
		t.Errorf("el archivo está: Presente debe ser cierto")
	}
	if l.Error == nil {
		t.Errorf("un archivo presente que no dice nada tiene que llegar con su causa")
	}
}

func TestLeeUnEstadoBueno(t *testing.T) {
	l := NuevoVigia(escribir(t, estadoReal)).Leer()
	if !l.Presente || l.Error != nil {
		t.Fatalf("lectura = %+v", l)
	}
	if l.Estado.Veredicto != "Protegido" || l.Estado.Raices != 8 {
		t.Errorf("estado = %+v", l.Estado)
	}
}

// LA CACHÉ EXISTE POR UN MOTIVO MEDIDO: instantaneaCompleta la llama también el
// flujo en vivo, una vez por segundo, y el cliente escribe tres veces al día.
// Sin ella serían 86 400 lecturas diarias para obtener siempre lo mismo.
func TestLaCacheEvitaReleerYCaducaSola(t *testing.T) {
	ruta := escribir(t, estadoReal)
	v := NuevoVigia(ruta)

	t0 := time.Now()
	if l := v.leerEn(t0); l.Estado.Raices != 8 {
		t.Fatalf("primera lectura = %+v", l)
	}

	// Se cambia el archivo por debajo: dentro de la vigencia NO debe verse.
	if err := os.WriteFile(ruta, []byte("estado=Falla\nraices=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if l := v.leerEn(t0.Add(vigencia - time.Second)); l.Estado.Raices != 8 {
		t.Errorf("dentro de la vigencia se releyó el disco: %+v", l.Estado)
	}

	// Pasada la vigencia, sí.
	if l := v.leerEn(t0.Add(vigencia + time.Second)); l.Estado.Veredicto != "Falla" {
		t.Errorf("la caché no caducó: %+v", l.Estado)
	}
}

// UN ARCHIVO QUE DESAPARECE NO DEJA CONGELADO EL VALOR BUENO. Sería la mentira
// más cómoda de todas: seguir enseñando el último verde conocido.
func TestSiElArchivoDesapareceDejaDeAfirmarloBueno(t *testing.T) {
	ruta := escribir(t, estadoReal)
	v := NuevoVigia(ruta)
	t0 := time.Now()
	_ = v.leerEn(t0)

	if err := os.Remove(ruta); err != nil {
		t.Fatal(err)
	}
	l := v.leerEn(t0.Add(vigencia + time.Second))
	if l.Presente {
		t.Errorf("el archivo ya no está y la lectura sigue diciendo que sí: %+v", l)
	}
	if l.Estado.Veredicto != "" {
		t.Errorf("se conservó el veredicto viejo: %q", l.Estado.Veredicto)
	}
}
