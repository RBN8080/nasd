package web

import (
	"bytes"
	"context"
	"fmt"
	"io"
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

// Abrir solo sirve aquí para saber si un destino está ocupado (pendientes.go).
func (a *almacenDeSubidas) Abrir(_ context.Context, r almacen.RutaSegura) (io.ReadSeekCloser, almacen.Entrada, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	c, ok := a.publicados[r.Rel()]
	if !ok {
		return nil, almacen.Entrada{}, almacen.ErrNoExiste
	}
	return lectorEnMemoria{strings.NewReader(c)}, almacen.Entrada{Ruta: r, Nombre: r.Nombre(), Tamano: int64(len(c))}, nil
}

type lectorEnMemoria struct{ *strings.Reader }

func (lectorEnMemoria) Close() error { return nil }

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

// Las subidas a medias viven SOLO en Resumen (decisión del responsable,
// 2026-11-09), cada una con su menú ⋯. Reintentar solo donde puede salir
// bien: en las propias y con el destino libre. Descartar, en todas.
func TestLasSubidasAMediasSoloEnResumenConSuMenu(t *testing.T) {
	s, alm, cookie, _ := servidorDeSubidas(t)
	propia := alm.parcial(t, "", "fotos/a.jpg", 10, "1234")
	ajena := alm.parcial(t, "juan", "b.jpg", 5, "12345")
	ocupada := alm.parcial(t, "", "PORTAPAPELES.txt", 0, "")
	alm.publicados["PORTAPAPELES.txt"] = "ya estaba"

	archivos := pedirDesde(t, s.Rutas(), http.MethodGet, "/", "192.168.1.18:5000", cookie).Body.String()
	if strings.Contains(archivos, "data-subida=") || strings.Contains(archivos, "Subidas a medias") {
		t.Error("Archivos vuelve a enseñar subidas a medias encima de los archivos")
	}

	resumen := pedirDesde(t, s.Rutas(), http.MethodGet, "/resumen", "192.168.1.18:5000", cookie).Body.String()
	for _, id := range []string{propia, ajena, ocupada} {
		if !strings.Contains(resumen, `data-subida="/subidas/`+id+`"`) {
			t.Errorf("Resumen no enseña la subida %s", id)
		}
	}
	if n := strings.Count(resumen, `<details class="menu menu-fila" data-acciones hidden>`); n != 3 {
		t.Errorf("Resumen trae %d menús ⋯; se esperaba uno por subida (3)", n)
	}
	if n := strings.Count(resumen, `data-accion="reintentar"`); n != 1 {
		t.Errorf("Resumen ofrece Reintentar %d veces; se esperaba 1, solo en la propia con destino libre", n)
	}
	if n := strings.Count(resumen, `data-accion="descartar"`); n != 3 {
		t.Errorf("Resumen ofrece Descartar %d veces; se esperaban 3", n)
	}
	for _, texto := range []string{"De <b>juan</b>", "y no se sobrescribe", `href="/ver/fotos"`, "data-elegir"} {
		if !strings.Contains(resumen, texto) {
			t.Errorf("a Resumen le falta %q", texto)
		}
	}
	if strings.Contains(resumen, "reanudación automática") {
		t.Error("Resumen vuelve a prometer una reanudación automática que no existe")
	}
}

// EL AVISO CRÍTICO FALSO DEL 2026-11-09: reintentar una subida cuyo archivo ya
// estaba en el disco acaba en 409 (RF-23), y eso no es una publicación perdida.
// Contarlo puso el SLI-2 a 0 % y mandó «🔴 NAS · CRÍTICO» al teléfono.
func TestYaExisteNoCuentaComoSubidaFallida(t *testing.T) {
	s, alm, cookie, csrf := servidorDeSubidas(t)
	id := alm.parcial(t, "", "PORTAPAPELES.txt", 0, "")
	alm.publicados["PORTAPAPELES.txt"] = "ya estaba"
	w := tus(t, s, cookie, csrf, http.MethodPatch, "/subidas/"+id, map[string]string{
		"Upload-Offset": "0", "Content-Type": "application/offset+octet-stream",
	})
	if w.Code != http.StatusConflict {
		t.Fatalf("publicar sobre un archivo que ya existe -> %d; se esperaba 409", w.Code)
	}
	if n := s.contadores.subidasFallidas.Load(); n != 0 {
		t.Errorf("subidas fallidas = %d; un «ya existe» no es un fallo del SLI-2", n)
	}
	// Sin publicaciones ni fallos, el SLI-2 no tiene muestras (-1): ni aprueba
	// ni suspende. Lo que no puede es bajar del 100 %.
	if v := s.instantaneaCompleta().IntegridadDeSubida(); v >= 0 && v < 100 {
		t.Errorf("SLI-2 = %.2f %% tras un «ya existe»; no debía bajar", v)
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
