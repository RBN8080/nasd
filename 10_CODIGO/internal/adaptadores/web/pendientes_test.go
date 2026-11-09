package web

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"nasd/internal/almacen"
)

// almacenDeSubidas guarda en memoria lo que el núcleo tus le pide: parciales
// con su dueño y lo que se publica. Lo de disco ya se prueba en fsposix.
type almacenDeSubidas struct {
	almacenVacio
	mu         sync.Mutex
	n          int
	parciales  map[string]*parcialEnMemoria
	publicados map[string]string
}

type parcialEnMemoria struct {
	p     almacen.Parcial
	datos bytes.Buffer
}

func nuevoAlmacenDeSubidas() *almacenDeSubidas {
	return &almacenDeSubidas{parciales: map[string]*parcialEnMemoria{}, publicados: map[string]string{}}
}

// parcial deja una subida a medias como la habría dejado el disco.
func (a *almacenDeSubidas) parcial(t *testing.T, cuenta, ruta string, total int64, datos string) string {
	t.Helper()
	r, err := almacen.NuevaRuta(ruta)
	if err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.anotar(cuenta, r, total, datos, time.Now().Add(-time.Hour))
}

func (a *almacenDeSubidas) anotar(cuenta string, r almacen.RutaSegura, total int64, datos string, cuando time.Time) string {
	a.n++
	id := fmt.Sprintf("%032x", a.n)
	pm := &parcialEnMemoria{p: almacen.Parcial{ID: id, Ruta: r, Total: total, Cuenta: cuenta, Modificado: cuando}}
	pm.datos.WriteString(datos)
	a.parciales[id] = pm
	return id
}

func (a *almacenDeSubidas) CrearReanudable(_ context.Context, r almacen.RutaSegura, total int64) (almacen.Parcial, almacen.EscrituraAtomica, error) {
	a.mu.Lock()
	if _, ocupado := a.publicados[r.Rel()]; ocupado {
		a.mu.Unlock()
		return almacen.Parcial{}, nil, almacen.ErrYaExiste
	}
	id := a.anotar("", r, total, "", time.Now())
	a.mu.Unlock()
	return a.ReabrirParcial(context.Background(), id)
}

func (a *almacenDeSubidas) ReabrirParcial(_ context.Context, id string) (almacen.Parcial, almacen.EscrituraAtomica, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	pm, ok := a.parciales[id]
	if !ok {
		return almacen.Parcial{}, nil, almacen.ErrNoExiste
	}
	p := pm.p
	p.Escrito = int64(pm.datos.Len())
	return p, &escrituraEnMemoria{a: a, id: id}, nil
}

func (a *almacenDeSubidas) Reanudables(context.Context) ([]almacen.Parcial, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []almacen.Parcial
	for _, pm := range a.parciales {
		p := pm.p
		p.Escrito = int64(pm.datos.Len())
		out = append(out, p)
	}
	return out, nil
}

type escrituraEnMemoria struct {
	a  *almacenDeSubidas
	id string
}

func (e *escrituraEnMemoria) Write(b []byte) (int, error) {
	e.a.mu.Lock()
	defer e.a.mu.Unlock()
	return e.a.parciales[e.id].datos.Write(b)
}

func (e *escrituraEnMemoria) Escrito() int64 {
	e.a.mu.Lock()
	defer e.a.mu.Unlock()
	if pm, ok := e.a.parciales[e.id]; ok {
		return int64(pm.datos.Len())
	}
	return 0
}

func (e *escrituraEnMemoria) Confirmar() error {
	e.a.mu.Lock()
	defer e.a.mu.Unlock()
	pm := e.a.parciales[e.id]
	if _, ocupado := e.a.publicados[pm.p.Ruta.Rel()]; ocupado {
		return almacen.ErrYaExiste
	}
	e.a.publicados[pm.p.Ruta.Rel()] = pm.datos.String()
	delete(e.a.parciales, e.id)
	return nil
}

func (e *escrituraEnMemoria) Descartar() error {
	e.a.mu.Lock()
	defer e.a.mu.Unlock()
	delete(e.a.parciales, e.id)
	return nil
}

func (e *escrituraEnMemoria) Soltar() error { return nil }

func servidorDeSubidas(t *testing.T) (*Servidor, *almacenDeSubidas, *http.Cookie, string) {
	t.Helper()
	s, _ := servidorMultiusuario(t)
	alm := nuevoAlmacenDeSubidas()
	s.almacenRaiz = alm
	cookie, csrf := sesionAbierta(t, s)
	return s, alm, cookie, csrf
}

func tus(t *testing.T, s *Servidor, cookie *http.Cookie, csrf, metodo, ruta string, cabeceras map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	r := desdeCasa(httptest.NewRequest(metodo, ruta, nil))
	r.AddCookie(cookie)
	r.Header.Set("Tus-Resumable", versionTus)
	r.Header.Set("Nas-Csrf", csrf)
	for k, v := range cabeceras {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	s.Rutas().ServeHTTP(w, r)
	return w
}

// EL DEFECTO DEL 2026-11-08: dos «PORTAPAPELES.txt» vacíos se quedaron a
// medias. Sin bytes el cliente no manda PATCH, y publicar solo ocurría al
// final de uno.
func TestUnArchivoVacioSePublicaAlCrearse(t *testing.T) {
	s, alm, cookie, csrf := servidorDeSubidas(t)
	w := tus(t, s, cookie, csrf, http.MethodPost, "/subidas", map[string]string{
		"Upload-Length": "0", "Nas-Destino": ".", "Nas-Nombre": "PORTAPAPELES.txt",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("POST /subidas de 0 bytes -> %d; se esperaba 201: %s", w.Code, w.Body)
	}
	if w.Header().Get("Nas-Completada") != "1" {
		t.Error("la respuesta no dice que la subida quedó publicada (Nas-Completada)")
	}
	if _, ok := alm.publicados["PORTAPAPELES.txt"]; !ok {
		t.Error("el archivo vacío no se publicó")
	}
	if len(alm.parciales) != 0 {
		t.Errorf("quedaron %d subidas a medias; se esperaba ninguna", len(alm.parciales))
	}
}

// Lo que llegó entero y no se publicó —el caso de arriba, o un corte justo
// antes de publicar— se publica con un PATCH vacío. Es lo que hace
// «Reintentar» sin pedir el archivo.
func TestUnPatchVacioPublicaLoQueYaLlegoEntero(t *testing.T) {
	s, alm, cookie, csrf := servidorDeSubidas(t)
	id := alm.parcial(t, "", "nota.txt", 3, "abc")
	w := tus(t, s, cookie, csrf, http.MethodPatch, "/subidas/"+id, map[string]string{
		"Upload-Offset": "3", "Content-Type": "application/offset+octet-stream",
	})
	if w.Code != http.StatusNoContent || w.Header().Get("Nas-Completada") != "1" {
		t.Fatalf("PATCH vacío -> %d, Nas-Completada=%q; se esperaba 204 y publicada",
			w.Code, w.Header().Get("Nas-Completada"))
	}
	if alm.publicados["nota.txt"] != "abc" {
		t.Errorf("publicado = %q; se esperaba «abc»", alm.publicados["nota.txt"])
	}
}

// Archivos enseña las subidas a medias PROPIAS con sus dos órdenes; Resumen,
// las de todas las cuentas, y solo ofrece Reintentar en las propias: el
// superusuario no tiene el archivo de otra cuenta.
func TestLasSubidasAMediasSeVenConSusOrdenes(t *testing.T) {
	s, alm, cookie, _ := servidorDeSubidas(t)
	propia := alm.parcial(t, "", "fotos/a.jpg", 10, "1234")
	ajena := alm.parcial(t, "juan", "b.jpg", 5, "12345")

	archivos := pedirDesde(t, s.Rutas(), http.MethodGet, "/", "192.168.1.18:5000", cookie).Body.String()
	if !strings.Contains(archivos, `data-subida="/subidas/`+propia+`"`) {
		t.Error("Archivos no enseña la subida a medias propia")
	}
	if strings.Contains(archivos, ajena) {
		t.Error("Archivos enseña la subida a medias de otra cuenta")
	}
	for _, orden := range []string{`data-accion="reintentar"`, `data-accion="descartar"`, `data-elegir`, `href="/ver/fotos"`} {
		if !strings.Contains(archivos, orden) {
			t.Errorf("a la subida a medias de Archivos le falta %s", orden)
		}
	}

	resumen := pedirDesde(t, s.Rutas(), http.MethodGet, "/resumen", "192.168.1.18:5000", cookie).Body.String()
	for _, id := range []string{propia, ajena} {
		if !strings.Contains(resumen, `data-subida="/subidas/`+id+`"`) {
			t.Errorf("Resumen no enseña la subida %s", id)
		}
	}
	if n := strings.Count(resumen, `data-accion="reintentar"`); n != 1 {
		t.Errorf("Resumen ofrece Reintentar %d veces; se esperaba 1, solo en la propia", n)
	}
	if n := strings.Count(resumen, `data-accion="descartar"`); n != 2 {
		t.Errorf("Resumen ofrece Descartar %d veces; se esperaban 2", n)
	}
	if !strings.Contains(resumen, "De <b>juan</b>") {
		t.Error("Resumen no dice de quién es la subida ajena")
	}
	if strings.Contains(resumen, "reanudación automática") {
		t.Error("Resumen vuelve a prometer una reanudación automática que no existe")
	}
}

// Los menús se cierran con menus.js, y ese archivo va en el marco: Seguridad
// tenía un menú y no lo cargaba. Una vez por página, ni cero ni dos.
func TestTodoModuloCargaElCierreDeLosMenus(t *testing.T) {
	s, _, cookie, _ := servidorDeSubidas(t)
	for _, ruta := range []string{"/", "/resumen", "/estado", "/seguridad", "/administracion"} {
		cuerpo := pedirDesde(t, s.Rutas(), http.MethodGet, ruta, "192.168.1.18:5000", cookie).Body.String()
		if n := strings.Count(cuerpo, `<script src="/estatico/menus.js">`); n != 1 {
			t.Errorf("%s carga menus.js %d veces; se esperaba 1", ruta, n)
		}
	}
}
