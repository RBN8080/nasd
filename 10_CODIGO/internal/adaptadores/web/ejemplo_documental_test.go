//go:build documentacion

package web

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Exporta las plantillas reales con datos de prueba, sin arrancar el NAS.
// Solo se compila al regenerar la documentación; no entra en el binario.
func TestExportarEjemploDocumental(t *testing.T) {
	destino := os.Getenv("NASD_EJEMPLO_DESTINO")
	if destino == "" {
		t.Skip("exportación documental no solicitada")
	}
	s, _ := servidorMultiusuario(t)
	s.anotarVolumen(nodoSano())
	cookie := superusuarioEn(t, s)
	quitarScripts := regexp.MustCompile(`(?s)<script\b[^>]*>.*?</script>`)
	quitarComentarios := regexp.MustCompile(`(?s)<!--.*?-->`)
	quitarCSRF := regexp.MustCompile(`name="csrf" value="[^"]*"`)
	for nombre, ruta := range map[string]string{
		"resumen": "/resumen", "archivos": "/", "estado": "/estado",
		"seguridad": "/seguridad", "cuentas": "/administracion",
	} {
		var cuerpo string
		if nombre == "estado" || nombre == "resumen" {
			m := marco{Modulo: nombre, Titulo: strings.ToUpper(nombre[:1]) + nombre[1:], Nodo: "nas-ejemplo", NodoSub: "en línea · ejemplo", Inicial: "S"}
			for _, mod := range modulos {
				item := itemMarco{Rotulo: mod.rotulo, Ruta: mod.ruta, Icono: mod.icono, Activo: mod.clave == nombre}
				m.Todos = append(m.Todos, item)
			}
			m.Grupos = []grupoMarco{{Items: m.Todos}}
			var vista any
			if nombre == "estado" {
				n := nodoSano()
				nodo, servicio := filasVivas(n.Vivo, Instantanea{})
				vista = vistaEstado{Marco: m, Version: []string{"nasd · ejemplo", "datos ficticios"}, Grupos: []grupoEstado{
					{"Nodo", nodo}, {"Servicio", servicio},
					{"Almacenamiento y hardware", conVeredictoPintado(comoFilas(evaluarLentos(n)))},
				}}
			} else {
				vista = vistaResumen{Marco: m, DiscoOK: true, DiscoUsado: "16 GB", DiscoPorcentaje: "2", Cuentas: 2, Temperatura: "55.8 °C", CPU: "12 %", CPUPie: "ejemplo", TemperaturaPie: "sin limitación", Interrumpidas: []subidaInterrumpida{{Ruta: "fotos/ejemplo.jpg", Progreso: "60 %", Porcentaje: 60}}}
			}
			var b bytes.Buffer
			if err := s.plantillas.ExecuteTemplate(&b, nombre+".html", vista); err != nil {
				t.Fatal(err)
			}
			cuerpo = b.String()
		} else {
			w := pedirDesde(t, s.Rutas(), http.MethodGet, ruta, "192.168.1.18:5000", cookie)
			if w.Code != http.StatusOK {
				t.Fatalf("%s: HTTP %d", ruta, w.Code)
			}
			cuerpo = w.Body.String()
		}
		cuerpo = quitarScripts.ReplaceAllString(cuerpo, "")
		cuerpo = quitarComentarios.ReplaceAllString(cuerpo, "")
		cuerpo = quitarCSRF.ReplaceAllString(cuerpo, `name="csrf" value="ejemplo"`)
		cuerpo = strings.ReplaceAll(cuerpo, "/estatico/", "estatico/")
		if err := os.WriteFile(filepath.Join(destino, nombre+".html"), []byte(cuerpo), 0600); err != nil {
			t.Fatal(err)
		}
	}
}
