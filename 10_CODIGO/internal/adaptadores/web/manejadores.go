package web

import (
	"errors"
	"io"
	"net/http"
	"strings"

	"nasd/internal/almacen"
	"nasd/internal/autenticacion"
	"nasd/internal/seguridad"
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
	// EsHTTPS decide si las imágenes abren en pestaña nueva — ver el
	// comentario extenso junto a target=_blank en listado.html. Mismo
	// origen que Secure de la cookie de sesión (ADR-0046): r.TLS != nil.
	EsHTTPS bool
	// Orden es la columna por la que se está ordenando. Viaja en la URL y no
	// en una cookie ni en el servidor: el listado ES el sistema de archivos
	// (regla R1, ADR-0015) y no guarda estado de nadie. Como efecto lateral
	// útil, un enlace copiado conserva el orden que se estaba viendo.
	Orden criterio
	// EsSuperusuario decide si la barra superior enseña «Estado» (ADR-0055).
	//
	// ES SOLO LA MITAD DEL CONTROL, y la que menos vale: esconder el botón no
	// impide la petición. Quien la impide es soloSuperusuario en la tabla de
	// rutas. Esto existe para no ofrecer una puerta que va a responder 403.
	EsSuperusuario bool
	// PuedeAdministrar decide si el menú de cada fila ofrece renombrar, mover
	// y borrar, y si la barra lleva a Administración — las dos cosas cuelgan
	// de la MISMA regla (acotadoPorRed, sesion.go), y «administración» es como
	// la tabla de rutas de servidor.go llama ya a las tres de la fila.
	//
	// Misma mitad del control que la línea de arriba, y por el mismo motivo:
	// quien de verdad niega es soloDesdeDentro en la tabla de rutas. Esto
	// existe para no ofrecer un botón que va a responder 403.
	//
	// Para quien NO es superusuario vale siempre cierto: la regla no le
	// alcanza, porque su sesión ya está enraizada en su carpeta (ADR-0055).
	PuedeAdministrar bool
	// Novedades es la marca del botón «Seguridad» de la barra. Cero no pinta
	// nada: una pastilla con un 0 sería ruido permanente.
	Novedades int
	// Marco es el cromo compartido (rail, título, pestañas) — ADR-0075. Lo
	// arma construirMarco a partir de las MISMAS EsSuperusuario/
	// PuedeAdministrar/Novedades de arriba, así que las dos no pueden discrepar.
	Marco marco
	// ConDetalle es siempre falso aquí: Archivos no tiene panel de detalle
	// (ver el comentario en marco.go sobre por qué "/" es de Archivos y no
	// de Resumen). Existe solo para que _marco.html pueda preguntarlo sin
	// que html/template falle por campo inexistente.
	ConDetalle bool
}

// maxEntradasPorPagina acota lo que se envía al navegador.
//
// El puerto Almacen entrega un iterador precisamente porque D-11 dejó los
// directorios sin cota (RNF-04). Aquí se corta la emisión: el servicio no
// acumula el directorio entero ni obliga al navegador a dibujar 10 000 filas.
const maxEntradasPorPagina = 2000

func (s *Servidor) verListado(w http.ResponseWriter, r *http.Request, alm almacen.Almacen) {
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
		EsHTTPS: r.TLS != nil,
		Orden:   criterioDe(r.URL.Query().Get("orden")),

		EsSuperusuario:   usuarioDe(r) == autenticacion.NombreSuperusuario,
		PuedeAdministrar: !acotadoPorRed(r),
		Novedades:        s.novedades.Cuantas(),
	}
	v.Marco = s.construirMarco(r, "archivos", "Archivos", "")

	n := 0
	for e, err := range alm.Listar(r.Context(), ruta) {
		if err != nil {
			s.fallo(w, r, err)
			return
		}
		if n >= maxEntradasPorPagina {
			v.Truncado = true
			break
		}
		// homeUsers/ se esconde del listado — pedido el 06/08: con el atajo
		// «Usuarios» ya en la barra, verla también aquí es una segunda ruta a
		// lo mismo, y el botón deja de tener sentido propio si la carpeta que
		// abrevia ya estaba a la vista.
		//
		// SOLO en la raíz y SOLO para el superusuario, las dos condiciones a
		// la vez. Para un usuario normal esto NUNCA aplica: aunque tuviera su
		// propia subcarpeta llamada igual dentro de SU espacio —algo que
		// fsposix permite, esReservado no mira su prefijo—, esa carpeta es
		// suya y no la especial, y ocultársela sería el mismo defecto que
		// esta misma versión corrige en la baja: algo del usuario
		// desapareciendo de donde debería verse.
		if ruta.EsRaiz() && v.EsSuperusuario && e.Nombre == homeUsersNombre {
			continue
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

	// Carpetas primero y el criterio que pida la URL — ver orden.go, incluido
	// el límite que tiene ordenar DESPUÉS de haber recortado a
	// maxEntradasPorPagina.
	ordenarPor(v.Entradas, v.Orden)

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

func (s *Servidor) descargar(w http.ResponseWriter, r *http.Request, alm almacen.Almacen) {
	ruta, err := almacen.NuevaRuta(r.PathValue("ruta"))
	if err != nil {
		s.fallo(w, r, err)
		return
	}
	lector, entrada, err := alm.Abrir(r.Context(), ruta)
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
	http.ServeContent(escrituraDelegada{w, escrituraConPlazo(w, s.plazoInactividad, s.tocadorDe(r))},
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
func (s *Servidor) subirMultipart(w http.ResponseWriter, r *http.Request, alm almacen.Almacen) {
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
				marcarRechazo(r, seguridad.TestigoCSRF)
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
				marcarRechazo(r, seguridad.TestigoCSRF)
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
			if err := s.escribir(w, r, alm, ruta, parte); err != nil {
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
func (s *Servidor) escribir(w http.ResponseWriter, r *http.Request, alm almacen.Almacen, ruta almacen.RutaSegura, origen io.Reader) error {
	ea, err := alm.Crear(r.Context(), ruta)
	if err != nil {
		return err
	}
	// Plazo por actividad sobre la lectura del cuerpo (ADR-0026).
	n, err := io.Copy(ea, lecturaConPlazo(w, origen, s.plazoInactividad, s.tocadorDe(r)))
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

func (s *Servidor) crearDirectorio(w http.ResponseWriter, r *http.Request, alm almacen.Almacen) {
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
	if err := alm.CrearDirectorio(r.Context(), nueva); err != nil {
		s.fallo(w, r, err)
		return
	}

	// Crear una carpeta DESDE el navegador de mover tiene que devolver ahí,
	// no al listado: quien está eligiendo destino acaba de fabricarlo y lo
	// siguiente que quiere es entrar en él (RF-17, ADR-0054).
	//
	// No es un redirección abierta aunque venga del formulario: el valor se
	// valida como RutaSegura y la URL la construye urlDeMover con esa ruta
	// ya saneada. No hay forma de que apunte fuera del propio servicio.
	if m := r.PostFormValue("mover"); m != "" {
		origen, err := almacen.NuevaRuta(m)
		if err != nil {
			s.fallo(w, r, err)
			return
		}
		http.Redirect(w, r, urlDeMover(origen, padre), http.StatusSeeOther)
		return
	}
	s.redirigir(w, r, padre, "Carpeta creada", false)
}

func (s *Servidor) redirigir(w http.ResponseWriter, r *http.Request, a almacen.RutaSegura, msg string, esError bool) {
	// urlDeListado escapa POR COMPONENTE. Antes se usaba escaparURL sobre la
	// ruta entera, que convierte también las barras en «%2F»: funcionaba de
	// milagro porque ServeMux las decodifica antes de casar el comodín.
	destino := urlDeListado(a)
	// QueryEscape y no escaparURL (que es PathEscape): este valor va en la
	// CONSULTA, y PathEscape deja pasar «&» por ser legal en un segmento de
	// ruta. Los mensajes llevan nombres de archivo dentro —«Renombrado:
	// a&b.txt»—, así que uno con «&» partía el parámetro y el aviso llegaba
	// cortado. Defecto latente desde que los mensajes incluyen nombres.
	q := "?msg=" + escaparConsulta(msg)
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
	case errors.Is(err, almacen.ErrReservado):
		// ADR-0058. Antes caía al default y salía como 500 «error interno»
		// (P-9): una negativa DELIBERADA del dominio —el contenedor de
		// usuarios no se toca por la vía normal— se leía igual que un fallo
		// real. 403 y no 404: la ruta existe y quien pide ya está
		// autenticado, mismo criterio que soloSuperusuario.
		estado, mensaje = http.StatusForbidden,
			"esa carpeta la administra el panel de Usuarios; no se toca desde aquí"
	}

	// La clasificación de seguridad se deriva del MISMO switch de arriba, y no
	// de una lista paralela: si algún día se añade un error de dominio nuevo,
	// tendrá que pasar por aquí igual que pasa por el código de estado, y lo
	// peor que puede ocurrir es que salga como «desconocido» en el panel —
	// visible— en vez de desaparecer.
	switch {
	case errors.Is(err, almacen.ErrReservado):
		marcarRechazo(r, seguridad.RecursoReservado)
	case errors.Is(err, almacen.ErrNoExiste), errors.Is(err, almacen.ErrEnlaceExterno):
		marcarRechazo(r, seguridad.RutaInexistente)
	case errors.Is(err, almacen.ErrRutaInvalida):
		// Un salto de ruta (CWE-22) entra por aquí: almacen.NuevaRuta lo
		// rechaza antes de tocar nada. Es «malformada» y no un motivo propio
		// porque el servidor solo sabe que la ruta era inválida; llamarlo
		// «intento de escape» sería la clase de interpretación que este
		// modelo deja fuera a propósito.
		marcarRechazo(r, seguridad.PeticionMalformada)
	case estado == http.StatusBadRequest:
		marcarRechazo(r, seguridad.PeticionMalformada)
	}

	// P5 y P7: el fallo se registra siempre, aunque el usuario vea poco.
	s.reg.Warn("fallo", "ruta", r.URL.Path, "estado", estado, "error", err)
	http.Error(w, mensaje, estado)
}
