package web

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync"

	"nasd/internal/almacen"
)

// Núcleo del protocolo tus — ADR-0027.
//
// Tres verbos y un entero:
//
//	POST  /subidas        crear         -> 201 + Location
//	HEAD  /subidas/{id}   consultar     -> Upload-Offset
//	PATCH /subidas/{id}   enviar bytes  -> 204 + Upload-Offset
//
// LA DECISIÓN QUE ELIMINA UNA CLASE ENTERA DE DEFECTOS: el desplazamiento no
// se guarda en ningún sitio. ES el tamaño del archivo parcial. No existe
// metadato de progreso que pueda desincronizarse de los datos, y sobrevive
// gratis a un corte de corriente.

const versionTus = "1.0.0"

// registroDeSubidas guarda solo lo que NO se deduce del tamaño: a dónde va el
// archivo y cómo se llama.
//
// Charter §6.1 exige documentar todo estado compartido entre goroutines.
// Este es uno de los dos del programa: lo protege un mutex y ninguna
// operación de disco se hace con el mutex tomado.
type registroDeSubidas struct {
	mu sync.Mutex
	m  map[string]*subidaEnCurso
}

type subidaEnCurso struct {
	mu       sync.Mutex // serializa los PATCH de una misma subida
	escritor almacen.EscrituraAtomica
	ruta     almacen.RutaSegura
	total    int64
}

func nuevoRegistroDeSubidas() *registroDeSubidas {
	return &registroDeSubidas{m: make(map[string]*subidaEnCurso)}
}

func (r *registroDeSubidas) guardar(id string, s *subidaEnCurso) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.m[id] = s
}

func (r *registroDeSubidas) buscar(id string) (*subidaEnCurso, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.m[id]
	return s, ok
}

func (r *registroDeSubidas) borrar(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.m, id)
}

func cabecerasTus(w http.ResponseWriter) {
	w.Header().Set("Tus-Resumable", versionTus)
	w.Header().Set("Cache-Control", "no-store")
}

func (s *Servidor) tusCrear(w http.ResponseWriter, r *http.Request) {
	cabecerasTus(w)

	total, err := strconv.ParseInt(r.Header.Get("Upload-Length"), 10, 64)
	if err != nil || total < 0 {
		http.Error(w, "Upload-Length ausente o inválido", http.StatusBadRequest)
		return
	}
	destino, err := almacen.NuevaRuta(decodificar(r.Header.Get("Nas-Destino")))
	if err != nil {
		s.fallo(w, r, err)
		return
	}
	nombre, err := almacen.NombreDeArchivo(decodificar(r.Header.Get("Nas-Nombre")))
	if err != nil {
		s.fallo(w, r, err)
		return
	}
	ruta, err := destino.Hija(nombre)
	if err != nil {
		s.fallo(w, r, err)
		return
	}

	// CrearReanudable anota el destino EN DISCO junto al parcial, de modo que
	// la subida sobreviva a un reinicio del servicio (ADR-0027).
	// RF-23 se comprueba ya aquí, para fallar pronto en lugar de tras
	// transferir 5 GB.
	parcial, escritor, err := s.almacen.CrearReanudable(r.Context(), ruta, total)
	if err != nil {
		s.fallo(w, r, err)
		return
	}

	id := parcial.ID
	s.subidas.guardar(id, &subidaEnCurso{escritor: escritor, ruta: ruta, total: total})
	s.reg.Info("subida creada", "id", id, "ruta", ruta.Rel(), "bytes", total)

	w.Header().Set("Location", "/subidas/"+id)
	w.Header().Set("Upload-Offset", "0")
	w.WriteHeader(http.StatusCreated)
}

// recuperar busca una subida en memoria y, si no está, intenta reabrirla
// desde el disco.
//
// Esto es lo que hace que una subida sobreviva a un reinicio del servicio:
// el .meta dice adónde iba y el tamaño del parcial dice por dónde iba.
func (s *Servidor) recuperar(r *http.Request, id string) (*subidaEnCurso, bool) {
	if sub, ok := s.subidas.buscar(id); ok {
		return sub, true
	}
	parcial, escritor, err := s.almacen.ReabrirParcial(r.Context(), id)
	if err != nil {
		return nil, false
	}
	sub := &subidaEnCurso{escritor: escritor, ruta: parcial.Ruta, total: parcial.Total}
	s.subidas.guardar(id, sub)
	s.reg.Info("subida recuperada del disco",
		"id", id, "ruta", parcial.Ruta.Rel(), "desplazamiento", parcial.Escrito)
	return sub, true
}

func (s *Servidor) tusEstado(w http.ResponseWriter, r *http.Request) {
	cabecerasTus(w)
	sub, ok := s.recuperar(r, r.PathValue("id"))
	if !ok {
		http.Error(w, "no existe", http.StatusNotFound)
		return
	}
	sub.mu.Lock()
	defer sub.mu.Unlock()
	w.Header().Set("Upload-Offset", strconv.FormatInt(sub.escritor.Escrito(), 10))
	w.Header().Set("Upload-Length", strconv.FormatInt(sub.total, 10))
	w.WriteHeader(http.StatusOK)
}

func (s *Servidor) tusEnviar(w http.ResponseWriter, r *http.Request) {
	cabecerasTus(w)
	id := r.PathValue("id")
	sub, ok := s.recuperar(r, id)
	if !ok {
		http.Error(w, "no existe", http.StatusNotFound)
		return
	}
	sub.mu.Lock()
	defer sub.mu.Unlock()

	declarado, err := strconv.ParseInt(r.Header.Get("Upload-Offset"), 10, 64)
	if err != nil {
		http.Error(w, "Upload-Offset inválido", http.StatusBadRequest)
		return
	}
	// El desplazamiento real es el tamaño de lo ya escrito. Nunca se escribe
	// en una posición que venga del cliente: se comprueba y se rechaza.
	if real := sub.escritor.Escrito(); declarado != real {
		s.reg.Warn("desplazamiento incoherente", "id", id, "declarado", declarado, "real", real)
		s.fallo(w, r, almacen.ErrDesplazamiento)
		return
	}

	n, err := io.Copy(sub.escritor, lecturaConPlazo(w, r.Body, s.plazoInactividad))
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		// NO se descarta: el parcial se conserva para poder reanudar. Eso es
		// precisamente RF-12.
		s.reg.Warn("subida interrumpida", "id", id, "bytes", n, "error", err)
		w.Header().Set("Upload-Offset", strconv.FormatInt(sub.escritor.Escrito(), 10))
		w.WriteHeader(http.StatusNoContent)
		return
	}

	escrito := sub.escritor.Escrito()
	w.Header().Set("Upload-Offset", strconv.FormatInt(escrito, 10))

	if escrito >= sub.total {
		if err := sub.escritor.Confirmar(); err != nil {
			s.subidas.borrar(id)
			s.fallo(w, r, err)
			return
		}
		s.subidas.borrar(id)
		s.reg.Info("subida confirmada", "id", id, "ruta", sub.ruta.Rel(), "bytes", escrito)
		w.Header().Set("Nas-Completada", "1")
	}
	w.WriteHeader(http.StatusNoContent)
}

// decodificar deshace el encodeURIComponent del cliente.
//
// Se usa PathUnescape y NO QueryUnescape: este último convierte «+» en
// espacio, con lo que un archivo llamado «a+b.jpg» se habría guardado como
// «a b.jpg». encodeURIComponent nunca produce «+» para el espacio —usa
// %20—, así que PathUnescape es el inverso correcto.
func decodificar(s string) string {
	if v, err := url.PathUnescape(s); err == nil {
		return v
	}
	return s
}

func escaparURL(s string) string { return url.PathEscape(s) }
