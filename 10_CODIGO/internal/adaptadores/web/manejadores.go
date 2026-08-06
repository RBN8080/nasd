package web

import (
	"errors"
	"io"
	"net/http"
	"strings"

	"nasd/internal/almacen"
)

type vistaListado struct {
	Ruta     almacen.RutaSegura
	Migas    []almacen.RutaSegura
	Entradas []almacen.Entrada
	Truncado bool
	Ocultas  int
	Mensaje  string
	EsError  bool
	// Csrf viaja en cada formulario del listado. Sin él, las acciones
	// destructivas quedarían solo tras SameSite=Lax.
	Csrf string
}

// maxEntradasPorPagina acota lo que se envía al navegador.
//
// El puerto Almacen entrega un iterador precisamente porque D-11 dejó los
// directorios sin cota (RNF-04). Aquí se corta la emisión: el servicio no
// acumula el directorio entero ni obliga al navegador a dibujar 10 000 filas.
const maxEntradasPorPagina = 2000

func (s *Servidor) verListado(w http.ResponseWriter, r *http.Request) {
	ruta, err := almacen.NuevaRuta(r.PathValue("ruta"))
	if err != nil {
		s.fallo(w, r, err)
		return
	}

	v := vistaListado{
		Ruta:    ruta,
		Migas:   ruta.Ascendencia(),
		Mensaje: r.URL.Query().Get("msg"),
		EsError: r.URL.Query().Get("err") != "",
		Csrf:    s.csrfDe(r),
	}

	n := 0
	for e, err := range s.almacen.Listar(r.Context(), ruta) {
		if err != nil {
			s.fallo(w, r, err)
			return
		}
		if n >= maxEntradasPorPagina {
			v.Truncado = true
			break
		}
		// Las entradas que empiezan por punto se ocultan, como hace cualquier
		// gestor de archivos. Aquí importa por un motivo concreto: iOS deja
		// directorios temporales «.sb-XXXX» al copiar o descomprimir, y los
		// esconde de su propia interfaz. Mostrarlos solo en la web hacía que
		// el iPhone y el navegador pareciesen discrepar.
		//
		// NO se ocultan en silencio: se cuentan y la vista lo dice.
		if strings.HasPrefix(e.Nombre, ".") {
			v.Ocultas++
			continue
		}
		v.Entradas = append(v.Entradas, e)
		n++
	}

	// Carpetas primero y numeración natural — ver orden.go, incluido el límite
	// que tiene ordenar DESPUÉS de haber recortado a maxEntradasPorPagina.
	ordenar(v.Entradas)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	// SIN CACHÉ, y no es opcional.
	//
	// El listado ES el sistema de archivos (regla R1, ADR-0015): no hay
	// índice ni caché del lado del servidor, precisamente para que un cambio
	// hecho por SMB se vea al instante en la web (RF-21). Dejar que el
	// navegador cachee la página tira por tierra esa garantía: se borra un
	// archivo desde el iPhone y la web sigue mostrándolo.
	//
	// Defecto encontrado en uso real el 2026-07-31.
	w.Header().Set("Cache-Control", "no-store, must-revalidate")
	w.Header().Set("Pragma", "no-cache")

	if err := s.plantillas.ExecuteTemplate(w, "listado.html", v); err != nil {
		s.reg.Error("render del listado", "error", err)
	}
}

func (s *Servidor) descargar(w http.ResponseWriter, r *http.Request) {
	ruta, err := almacen.NuevaRuta(r.PathValue("ruta"))
	if err != nil {
		s.fallo(w, r, err)
		return
	}
	lector, entrada, err := s.almacen.Abrir(r.Context(), ruta)
	if err != nil {
		s.fallo(w, r, err)
		return
	}
	defer lector.Close()

	// 04_SEGURIDAD.md §4: nunca se sirve contenido de datos/ de forma que el
	// navegador pueda ejecutarlo. No se filtran extensiones —el producto es
	// «subir cualquier tipo de información»—; se ataca el vector real.
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+
		strings.ReplaceAll(escaparURL(entrada.Nombre), " ", "%20"))

	// ServeContent implementa RFC 7233 por sí solo: 206, rangos múltiples e
	// If-Range. RF-10 sale de aquí, sin escribirlo (ADR-0025).
	//
	// SOBRE sendfile(2) — corrección del 2026-07-31.
	// ADR-0025 presumía de que Go usaría sendfile en las descargas. NO OCURRE,
	// y es culpa de esta línea: envolver el ResponseWriter rompe la detección
	// de io.ReaderFrom que activa esa optimización.
	//
	// SE MANTIENE EL ENVOLTORIO A PROPÓSITO. El plazo por actividad de
	// ADR-0026 es un requisito (RNF-12) y sendfile es una optimización; y en
	// este nodo la optimización no compra nada, porque el techo lo pone el
	// bus USB 2.0 compartido con la Ethernet (RES-02), no las copias de
	// memoria. Medido: 19.9 MB/s en una descarga de 5 GB, con el proceso en
	// 10 MB de RSS.
	//
	// Lo que se corrige es la AFIRMACIÓN del documento, no el código.
	http.ServeContent(escrituraDelegada{w, escrituraConPlazo(w, s.plazoInactividad)},
		r, entrada.Nombre, entrada.Modificado, lector)
}

// escrituraDelegada mantiene las cabeceras del ResponseWriter original y
// desvía las escrituras al envoltorio con plazo.
type escrituraDelegada struct {
	http.ResponseWriter
	cuerpo io.Writer
}

func (e escrituraDelegada) Write(p []byte) (int, error) { return e.cuerpo.Write(p) }
func (e escrituraDelegada) Unwrap() http.ResponseWriter { return e.ResponseWriter }

// subirMultipart es el camino de respaldo SIN JavaScript (ADR-0027).
// El camino normal de la interfaz es tus; ambos escriben por el mismo
// EscrituraAtomica, así que la durabilidad es un único camino.
func (s *Servidor) subirMultipart(w http.ResponseWriter, r *http.Request) {
	// PROHIBIDO r.ParseMultipartForm / r.FormFile — ADR-0025.
	// Vuelcan el excedente a os.TempDir(), que con PrivateTmp=yes es tmpfs,
	// es decir RAM: 4 GB sobre un presupuesto de 592 MB (RES-01).
	mr, err := r.MultipartReader()
	if err != nil {
		http.Error(w, "se esperaba multipart/form-data", http.StatusBadRequest)
		return
	}

	destino := almacen.Raiz()
	subidos := 0

	// El testigo CSRF llega como una parte más del flujo multipart, y hay que
	// validarlo ANTES de aceptar ningún archivo.
	//
	// No se puede usar exigirCSRF aquí: leería el formulario, y con
	// MultipartReader el cuerpo se recorre una sola vez y en orden (ADR-0025).
	// Por eso el formulario pone «csrf» antes que «archivo».
	csrfOK := false
	cookie, errC := r.Cookie(nombreCookie)
	if errC != nil {
		s.pedirAcceso(w, r, "")
		return
	}

	for {
		parte, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			s.fallo(w, r, err)
			return
		}

		switch parte.FormName() {
		case "csrf":
			b, err := io.ReadAll(io.LimitReader(parte, 1024))
			if err != nil {
				s.fallo(w, r, err)
				return
			}
			csrfOK = s.sesiones.CsrfValido(cookie.Value, strings.TrimSpace(string(b)))
			if !csrfOK {
				s.reg.Warn("testigo CSRF inválido en subida multipart", "origen", origenDe(r))
				http.Error(w, "petición no autorizada", http.StatusForbidden)
				return
			}

		case "destino":
			// El destino debe llegar ANTES que el archivo: con
			// MultipartReader las partes se recorren en orden (ADR-0025).
			b, err := io.ReadAll(io.LimitReader(parte, 4096))
			if err != nil {
				s.fallo(w, r, err)
				return
			}
			destino, err = almacen.NuevaRuta(string(b))
			if err != nil {
				s.fallo(w, r, err)
				return
			}

		case "archivo":
			// Sin testigo validado no se toca el disco. El formulario lo
			// envía primero; si no llegó, la petición no viene de aquí.
			if !csrfOK {
				s.reg.Warn("subida multipart sin testigo CSRF previo", "origen", origenDe(r))
				http.Error(w, "petición no autorizada", http.StatusForbidden)
				return
			}
			nombre, err := almacen.NombreDeArchivo(parte.FileName())
			if err != nil {
				s.fallo(w, r, err)
				return
			}
			ruta, err := destino.Hija(nombre)
			if err != nil {
				s.fallo(w, r, err)
				return
			}
			if err := s.escribir(w, r, ruta, parte); err != nil {
				s.fallo(w, r, err)
				return
			}
			subidos++
		}
		parte.Close()
	}

	if subidos == 0 {
		s.redirigir(w, r, destino, "No se recibió ningún archivo", true)
		return
	}
	s.redirigir(w, r, destino, "Subida completada. Recuerde: el original debe seguir en su dispositivo.", false)
}

// escribir vuelca un io.Reader al almacén con la secuencia atómica completa.
func (s *Servidor) escribir(w http.ResponseWriter, r *http.Request, ruta almacen.RutaSegura, origen io.Reader) error {
	ea, err := s.almacen.Crear(r.Context(), ruta)
	if err != nil {
		return err
	}
	// Plazo por actividad sobre la lectura del cuerpo (ADR-0026).
	n, err := io.Copy(ea, lecturaConPlazo(w, origen, s.plazoInactividad))
	if n > 0 {
		s.contadores.bytesSubidos.Add(n)
	}
	if err != nil {
		ea.Descartar() // en datos/ no queda nada — RNF-05
		return err
	}
	// El camino sin JavaScript comparte los indicadores con el de tus: los dos
	// publican por el mismo EscrituraAtomica, así que el SLI-2 mide la
	// durabilidad del producto y no la de una de sus dos puertas.
	if err := ea.Confirmar(); err != nil {
		s.contadores.subidasFallidas.Add(1)
		return err
	}
	s.contadores.subidasConfirmadas.Add(1)
	return nil
}

func (s *Servidor) crearDirectorio(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.fallo(w, r, err)
		return
	}
	if !s.exigirCSRF(w, r) {
		return
	}
	padre, err := almacen.NuevaRuta(r.PostFormValue("destino"))
	if err != nil {
		s.fallo(w, r, err)
		return
	}
	nueva, err := padre.Hija(strings.TrimSpace(r.PostFormValue("nombre")))
	if err != nil {
		s.fallo(w, r, err)
		return
	}
	if err := s.almacen.CrearDirectorio(r.Context(), nueva); err != nil {
		s.fallo(w, r, err)
		return
	}
	s.redirigir(w, r, padre, "Carpeta creada", false)
}

func (s *Servidor) redirigir(w http.ResponseWriter, r *http.Request, a almacen.RutaSegura, msg string, esError bool) {
	destino := "/ver/" + escaparURL(a.Rel())
	if a.EsRaiz() {
		destino = "/"
	}
	q := "?msg=" + escaparURL(msg)
	if esError {
		q += "&err=1"
	}
	http.Redirect(w, r, destino+q, http.StatusSeeOther)
}

// fallo traduce errores del dominio a códigos de estado.
// El núcleo no sabe que existen los códigos de estado; esta es la frontera.
func (s *Servidor) fallo(w http.ResponseWriter, r *http.Request, err error) {
	// La cancelación del cliente se comprueba LA PRIMERA y sin rodeos.
	//
	// Antes iba al final del switch, con un «errors.Is(err, r.Context().Err())»
	// que funcionaba de milagro por la guarda que llevaba al lado: si el
	// contexto no estaba cancelado, errors.Is(err, nil) es falso salvo que
	// err fuese nil. Frágil, y colocado donde una cancelación podía acabar
	// registrada como error interno.
	if r.Context().Err() != nil {
		// El cliente se fue: no hay a quién responder, y no es un fallo
		// nuestro. Se anota a nivel informativo y se sale.
		s.reg.Info("cliente desconectado", "ruta", r.URL.Path)
		return
	}

	if c, m, ok := mensajeAdministracion(err); ok {
		s.reg.Warn("fallo", "ruta", r.URL.Path, "estado", c, "error", err)
		http.Error(w, m, c)
		return
	}

	estado := http.StatusInternalServerError
	mensaje := "error interno"

	switch {
	case errors.Is(err, almacen.ErrRutaInvalida):
		estado, mensaje = http.StatusBadRequest, "ruta inválida"
	case errors.Is(err, almacen.ErrNoExiste):
		estado, mensaje = http.StatusNotFound, "no existe"
	case errors.Is(err, almacen.ErrEnlaceExterno):
		// 404 hacia el cliente, a propósito: confirmarle que ahí hay un
		// enlace hacia fuera del volumen ya sería información que no le
		// corresponde. En el registro sí queda distinguido (P7).
		estado, mensaje = http.StatusNotFound, "no existe"
	case errors.Is(err, almacen.ErrYaExiste):
		// RF-23: no se sobrescribe sin decisión explícita del usuario.
		estado, mensaje = http.StatusConflict, "ya existe un elemento con ese nombre; no se sobrescribe"
	case errors.Is(err, almacen.ErrNoEsDirectorio):
		estado, mensaje = http.StatusBadRequest, "no es un directorio"
	case errors.Is(err, almacen.ErrEsDirectorio):
		estado, mensaje = http.StatusBadRequest, "es un directorio"
	case errors.Is(err, almacen.ErrOcupado):
		estado, mensaje = http.StatusConflict, "otro cliente está escribiendo ese archivo"
	case errors.Is(err, almacen.ErrDesplazamiento):
		estado, mensaje = http.StatusConflict, "desplazamiento incoherente"
	}

	// P5 y P7: el fallo se registra siempre, aunque el usuario vea poco.
	s.reg.Warn("fallo", "ruta", r.URL.Path, "estado", estado, "error", err)
	http.Error(w, mensaje, estado)
}
