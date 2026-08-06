package web

import (
	"runtime/debug"
	"strings"
	"time"
)

// Versión del binario en ejecución, para el pie de /estado.
//
// Sale de la información que Go incrusta al compilar, NO de una constante
// escrita a mano: una cadena de versión mantenida a mano miente el día que
// alguien olvida subirla, y este proyecto ya persigue esa clase de afirmación
// desde ADR-0038. Lo que se muestra es lo que el binario dice de sí mismo.
//
// Depende de que la compilación ocurra dentro del repositorio, que es el caso:
// el Makefile compila desde 10_CODIGO y «desplegar» exige el árbol limpio.

// versionDelBinario devuelve algo como «nasd · 0ba159d · 2026-08-05 18:52».
//
// Si el árbol tenía cambios sin commitear al compilar, se dice. Eso NO debería
// verse en producción —«make desplegar» depende de verificar-limpio— así que
// verlo ahí significa que alguien instaló un binario a mano, y es exactamente
// lo que conviene que salte a la vista.
func versionDelBinario() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "nasd"
	}

	var revision, cuando string
	var sucio bool
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value
		case "vcs.time":
			cuando = s.Value
		case "vcs.modified":
			sucio = s.Value == "true"
		}
	}

	partes := []string{"nasd"}
	if revision != "" {
		// Siete caracteres es lo que usa git por omisión al abreviar, así que
		// el valor se puede pegar tal cual en «git show».
		if len(revision) > 7 {
			revision = revision[:7]
		}
		partes = append(partes, revision)
	}
	if t, err := time.Parse(time.RFC3339, cuando); err == nil {
		// En local: quien lee esta pantalla vive en la zona del nodo, y una
		// hora en UTC obligaría a restar seis mentalmente para nada.
		partes = append(partes, t.Local().Format("2006-01-02 15:04"))
	}
	if sucio {
		partes = append(partes, "ÁRBOL MODIFICADO")
	}
	return strings.Join(partes, " · ")
}
