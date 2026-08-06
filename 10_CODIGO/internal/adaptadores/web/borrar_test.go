package web

import (
	"strings"
	"testing"
)

// La pantalla de confirmación de RF-18 no tenía NINGUNA prueba hasta el
// 2026-08-06, y es la única barrera del sistema: sin papelera (D-15) y con
// copia única (D-12), lo que pase de aquí no se recupera.
//
// Se prueba lo que RF-18 EXIGE —que se vea qué se destruye y que el paso
// destructivo lleve la confirmación explícita—, no la redacción exacta de
// los rótulos, que es cosa de estilo y el responsable puede cambiar sin que
// ninguna prueba se ponga en rojo por ello.
func TestLaConfirmacionDeBorradoEnsenaQueSeVaADestruir(t *testing.T) {
	s, a := servidorDeMover(t)
	a.agregar(t, "IMG_7539.MOV", false)

	cuerpo := peticionConSesion(t, s, "/borrar/IMG_7539.MOV").Body.String()

	// RF-18: «la confirmación muestra el nombre de lo que se va a borrar».
	if !strings.Contains(cuerpo, "IMG_7539.MOV") {
		t.Error("no enseña QUÉ se va a borrar")
	}
	// Y cuánto, que es lo que distingue un descuido de una decisión.
	if !strings.Contains(cuerpo, "196.6 MB") {
		t.Errorf("no enseña el tamaño real de lo que se destruye:\n%s", cuerpo)
	}
	// El campo que solo pone esta pantalla: sin él, /borrar no destruye.
	if !strings.Contains(cuerpo, `name="confirmado" value="si"`) {
		t.Error("falta la confirmación explícita que exige RF-18")
	}
	// El aviso de que no hay vuelta atrás tiene que estar, con la redacción
	// que el responsable fijó el 06/08/2026.
	if !strings.Contains(cuerpo, "Esta acción no se puede deshacer") {
		t.Errorf("falta el aviso de irreversibilidad:\n%s", cuerpo)
	}
}
