package web

import (
	"strings"
	"testing"
)

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
