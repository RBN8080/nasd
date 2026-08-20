package web

import (
	"errors"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"time"

	"nasd/internal/geoip"
	"nasd/internal/seguridad"
)

// Bloquear desde el panel — la segunda acción de /seguridad, y la primera que
// toma una decisión en vez de deshacerla.
//
// # LA PANTALLA ENSEÑA LAS TRES OPCIONES A LA VEZ, Y NO ES ADORNO
//
// El defecto medido que originó todo esto (IDEAS §28.3) fue elegir el alcance
// equivocado: un /48 que parecía cubrir al operador y cubría 1 de sus 11
// redes. Un menú con tres opciones sueltas repetiría el error, porque obliga a
// elegir antes de ver. Aquí las tres se calculan y se enseñan juntas, cada una
// con lo que cubre y con lo que HABRÍA frenado del historial real.
//
// # LA SIMULACIÓN ES LO ÚNICO QUE CONVIERTE ESTO EN INGENIERÍA
//
// El nodo ya tiene la historia —paquetes, conexiones y rechazos—, así que una
// regla se puede probar contra el pasado antes de aplicarla. Es medir antes de
// escribir, que es como se ha trabajado en este proyecto desde el principio.

// ventanaSimulacion es cuánto pasado se recorre para decir qué habría frenado
// una regla. Siete días y no la ventana del panel: lo que interesa aquí no es
// «qué pasó hoy» sino si la regla acierta o barre de más, y para eso hace
// falta más historia que la que cabe en un vistazo.
const ventanaSimulacion = 7 * 24 * time.Hour

// caducidades ofrecidas. Permanente existe pero hay que elegirlo: la
// reputación de un rango envejece, y una lista que no caduca envejece con ella
// sin avisar.
var caducidades = []struct {
	Dias     int
	Etiqueta string
}{
	{30, "30 días"},
	{90, "90 días"},
	{0, "Sin caducidad"},
}

// propuesta es UN alcance posible, ya calculado, con su efecto medido.
type propuesta struct {
	Alcance seguridad.Alcance
	// Cubre es lo que se bloquearía, en direcciones legibles.
	Cubre string
	// Tramos es cuántos tramos distintos abarca. Se enseña porque es el dato
	// que destapa un alcance insuficiente: «11 tramos» frente a «1».
	Tramos int
	// Paquetes, Conexiones y Rechazos son lo que ESTA regla habría frenado en
	// la ventana, contado sobre lo que de verdad pasó.
	Paquetes   int
	Conexiones int
	Rechazos   int
	// Disponible es falso cuando no se puede ofrecer —sin base de operadores
	// no hay rango ni operador que calcular— y entonces la pantalla lo dice en
	// vez de enseñar una opción que no hace nada.
	Disponible bool
	Porque     string
}

type vistaBloqueo struct {
	IP          netip.Addr
	Geo         geoip.Info
	TieneGeo    bool
	Propuestas  []propuesta
	Caducidades []struct {
		Dias     int
		Etiqueta string
	}
	Ventana string
	// Aviso es el motivo por el que un intento anterior se rechazó. Viaja por
	// la URL para que la página lo pueda enseñar tras el redirect.
	Aviso string
	Csrf  string
}

// verBloqueo compone la pantalla de confirmación: qué cubre cada alcance y qué
// habría frenado.
func (s *Servidor) verBloqueo(w http.ResponseWriter, r *http.Request) {
	ip, err := netip.ParseAddr(r.URL.Query().Get("ip"))
	if err != nil {
		marcarRechazo(r, seguridad.PeticionMalformada)
		http.Error(w, "dirección ilegible", http.StatusBadRequest)
		return
	}

	v := vistaBloqueo{
		IP:          ip,
		Caducidades: caducidades,
		Ventana:     "7 días",
		Aviso:       r.URL.Query().Get("aviso"),
		Csrf:        s.csrfDe(r),
	}
	v.Geo, v.TieneGeo = s.geo.Buscar(ip)

	desde := time.Now().Add(-ventanaSimulacion)
	for _, a := range []seguridad.Alcance{
		seguridad.AlcanceDireccion, seguridad.AlcanceRango, seguridad.AlcanceOperador,
	} {
		tramos, etiqueta, porque := s.tramosDe(ip, a)
		p := propuesta{Alcance: a, Cubre: etiqueta, Tramos: len(tramos), Porque: porque}
		if len(tramos) > 0 {
			p.Disponible = true
			p.Paquetes, p.Conexiones, p.Rechazos = s.simular(tramos, desde)
		}
		v.Propuestas = append(v.Propuestas, p)
	}

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.plantillas.ExecuteTemplate(w, "bloquear.html", v); err != nil {
		s.reg.Error("render de la pantalla de bloqueo", "error", err)
	}
}

// tramosDe compone el alcance elegido. Devuelve además cómo describirlo y, si
// no se puede, por qué — que es lo que la pantalla necesita para no ofrecer
// una opción muda.
//
// EL SERVIDOR RECOMPONE SIEMPRE LOS TRAMOS, tanto al proponer como al aplicar.
// Nunca se aceptan los del formulario: si viajaran por el cliente, cualquiera
// con una sesión podría mandar un alcance distinto del que la pantalla enseñó,
// y las barandillas estarían defendiendo una cosa mientras se aplica otra.
func (s *Servidor) tramosDe(ip netip.Addr, a seguridad.Alcance) ([]seguridad.Tramo, string, string) {
	switch a {
	case seguridad.AlcanceDireccion:
		t := seguridad.Tramo{Desde: ip, Hasta: ip}
		return []seguridad.Tramo{t}, t.String(), ""

	case seguridad.AlcanceRango:
		rango, ok := s.geo.BuscarRango(ip)
		if !ok {
			return nil, "", "la base de operadores no cubre esta dirección"
		}
		t := seguridad.Tramo{Desde: rango.Desde, Hasta: rango.Hasta}
		return []seguridad.Tramo{t}, t.String(), ""

	case seguridad.AlcanceOperador:
		info, ok := s.geo.Buscar(ip)
		if !ok || info.ASN == 0 {
			return nil, "", "la base de operadores no sabe de quién es esta dirección"
		}
		// EL COSTE DE ESTA LÍNEA SE MIDE EN EL NODO, no se supone: recorre la
		// base entera (~50 MB). Se paga solo aquí, al componer la propuesta, y
		// nunca al servir una petición normal. El tiempo se registra para que
		// esa medición sea leer el diario y no montar un experimento.
		inicio := time.Now()
		rangos, err := s.geo.RangosDe(info.ASN)
		s.reg.Info("recorrida la base de operadores",
			"asn", info.ASN, "tramos", len(rangos), "ms", time.Since(inicio).Milliseconds())
		if err != nil {
			s.reg.Warn("no se pudo recorrer la base de operadores", "error", err)
			return nil, "", "no se pudo leer la base de operadores entera"
		}
		if len(rangos) == 0 {
			return nil, "", "la base no anuncia ningún tramo de este operador"
		}
		tramos := make([]seguridad.Tramo, 0, len(rangos))
		for _, rg := range rangos {
			tramos = append(tramos, seguridad.Tramo{Desde: rg.Desde, Hasta: rg.Hasta})
		}
		return tramos, "AS" + strconv.FormatUint(uint64(info.ASN), 10) + " · " + info.Nombre +
			" · " + info.Pais, ""
	}
	return nil, "", "alcance desconocido"
}

// simular cuenta qué habría frenado este alcance en la ventana, sobre las tres
// capas que el nodo ya guarda.
//
// Es el mismo historial que pinta el panel, así que lo que aquí sale se puede
// contrastar a ojo con la tabla de orígenes — y si alguna vez no cuadraran,
// una de las dos está mal y se nota.
func (s *Servidor) simular(tramos []seguridad.Tramo, desde time.Time) (paquetes, conexiones, rechazos int) {
	cubre := func(ip netip.Addr) bool {
		for _, t := range tramos {
			if t.Contiene(ip) {
				return true
			}
		}
		return false
	}

	if hist, err := seguridad.LeerToques(s.rutaToques, desde); err == nil {
		for _, t := range hist.Toques {
			if cubre(t.Origen) {
				paquetes++
			}
		}
	}
	for _, c := range s.conexiones.Desde(desde) {
		if cubre(c.Origen) {
			conexiones++
		}
	}
	for _, e := range s.seguridad.Filtrados(seguridad.Filtro{Desde: desde}) {
		if cubre(e.Origen) {
			rechazos++
		}
	}
	return paquetes, conexiones, rechazos
}

// bloquear aplica lo que la pantalla anterior enseñó.
func (s *Servidor) bloquear(w http.ResponseWriter, r *http.Request) {
	if !s.exigirCSRF(w, r) {
		return
	}
	if err := r.ParseForm(); err != nil {
		marcarRechazo(r, seguridad.PeticionMalformada)
		http.Error(w, "formulario ilegible", http.StatusBadRequest)
		return
	}
	ip, err := netip.ParseAddr(r.PostForm.Get("ip"))
	if err != nil {
		marcarRechazo(r, seguridad.PeticionMalformada)
		http.Error(w, "dirección ilegible", http.StatusBadRequest)
		return
	}
	alcance, ok := seguridad.AlcanceDesde(r.PostForm.Get("alcance"))
	if !ok {
		marcarRechazo(r, seguridad.PeticionMalformada)
		http.Error(w, "alcance ilegible", http.StatusBadRequest)
		return
	}

	tramos, etiqueta, porque := s.tramosDe(ip, alcance)
	if len(tramos) == 0 {
		s.volverAlBloqueo(w, r, ip, porque)
		return
	}

	e := seguridad.Entrada{
		Alcance:  alcance,
		Etiqueta: etiqueta,
		Tramos:   tramos,
		Motivo:   r.PostForm.Get("motivo"),
		Autor:    usuarioDe(r),
		BaseGeo:  s.geo.Fecha(),
	}
	if dias, err := strconv.Atoi(r.PostForm.Get("dias")); err == nil && dias > 0 {
		e.Caduca = time.Now().Add(time.Duration(dias) * 24 * time.Hour)
	}

	puesta, err := s.lista.Anadir(e, ipDe(r), time.Now())
	if err != nil {
		// Las barandillas no son un error del programa: son la respuesta a una
		// petición que no se debe atender, y quien la hizo tiene que poder
		// leer POR QUÉ y corregir. Se vuelve a la misma pantalla con el aviso.
		s.reg.Warn("bloqueo rechazado por una barandilla",
			"origen", ip.String(), "alcance", alcance.String(), "motivo", err.Error())
		s.volverAlBloqueo(w, r, ip, err.Error())
		return
	}

	s.reg.Warn("bloqueo puesto a mano",
		"id", puesta.ID, "alcance", alcance.String(), "cubre", etiqueta,
		"tramos", len(tramos), "usuario", usuarioDe(r), "porque", puesta.Motivo)
	http.Redirect(w, r, "/seguridad", http.StatusSeeOther)
}

func (s *Servidor) volverAlBloqueo(w http.ResponseWriter, r *http.Request, ip netip.Addr, aviso string) {
	destino := "/seguridad/bloquear?ip=" + ip.String()
	if aviso != "" {
		destino += "&aviso=" + url.QueryEscape(aviso)
	}
	http.Redirect(w, r, destino, http.StatusSeeOther)
}

// retirarBloqueo quita una entrada de la lista.
func (s *Servidor) retirarBloqueo(w http.ResponseWriter, r *http.Request) {
	if !s.exigirCSRF(w, r) {
		return
	}
	if err := r.ParseForm(); err != nil {
		marcarRechazo(r, seguridad.PeticionMalformada)
		http.Error(w, "formulario ilegible", http.StatusBadRequest)
		return
	}
	id := r.PostForm.Get("id")
	if err := s.lista.Retirar(id); err != nil && !errors.Is(err, seguridad.ErrNoSeEncontro) {
		s.reg.Error("no se pudo retirar el bloqueo", "id", id, "error", err)
	} else if err == nil {
		s.reg.Info("bloqueo retirado", "id", id, "usuario", usuarioDe(r))
	}
	http.Redirect(w, r, "/seguridad", http.StatusSeeOther)
}
