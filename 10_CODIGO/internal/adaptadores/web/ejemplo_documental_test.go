//go:build documentacion

package web

import (
	"bytes"
	"context"
	"iter"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"nasd/internal/almacen"
)

type almacenDocumental struct{ almacenVacio }

func (almacenDocumental) Listar(context.Context, almacen.RutaSegura) iter.Seq2[almacen.Entrada, error] {
	return func(yield func(almacen.Entrada, error) bool) {
		ruta, _ := almacen.NuevaRuta("ejemplo.txt")
		yield(almacen.Entrada{Ruta: ruta, Nombre: "ejemplo.txt", Tamano: 1024,
			Modificado: time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)}, nil)
	}
}

// Exporta las plantillas reales con datos de prueba, sin arrancar el NAS.
// Solo se compila al regenerar la documentación; no entra en el binario.
func TestExportarEjemploDocumental(t *testing.T) {
	destino := os.Getenv("NASD_EJEMPLO_DESTINO")
	if destino == "" {
		t.Skip("exportación documental no solicitada")
	}
	s, _ := servidorMultiusuario(t)
	s.almacenRaiz = almacenDocumental{}
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
				if len(m.Grupos) == 0 || m.Grupos[len(m.Grupos)-1].Rotulo != mod.grupo {
					m.Grupos = append(m.Grupos, grupoMarco{Rotulo: mod.grupo})
				}
				g := &m.Grupos[len(m.Grupos)-1]
				g.Items = append(g.Items, item)
			}
			m.Disco = s.medidorDeDisco(nodoSano())
			var vista any
			if nombre == "estado" {
				n := nodoSano()
				n.Vivo.CPU, n.Vivo.CPUOK = 12, true
				n.Vivo.Uptime, n.Vivo.UptimeOK = 4*24*time.Hour, true
				nodo, servicio := filasVivas(n.Vivo, Instantanea{UptimeSegundos: 86400})
				vista = vistaEstado{Marco: m, Version: []string{"nasd · ejemplo", "datos ficticios"}, Grupos: []grupoEstado{
					{"Nodo", nodo}, {"Servicio", servicio},
					{"Almacenamiento y hardware", conVeredictoPintado(comoFilas(evaluarLentos(n)))},
				}}
			} else {
				vista = vistaResumen{Marco: m, DiscoOK: true, DiscoUsado: "16 GB", DiscoPorcentaje: "2 %", Cuentas: 2, Temperatura: "55.8 °C", CPU: "12 %", CPUPie: "ejemplo", TemperaturaPie: "sin limitación", Pendientes: []subidaPendiente{{ID: "0123456789abcdef0123456789abcdef", Nombre: "ejemplo.jpg", Carpeta: "fotos", Enlace: "/ver/fotos", Progreso: "3 MB de 5 MB", Total: 5000000, Porcentaje: 60, Hace: "hace 2h 5min", Caduca: "se borra sola en 6d 21h"}}}
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
