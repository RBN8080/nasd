package web

import (
	"cmp"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"nasd/internal/geoip"
	"nasd/internal/seguridad"
)

// Panel de seguridad — /seguridad, etapa 2.
//
// SOLO EL SUPERUSUARIO, con la MISMA envoltura que /estado y /administracion.
// El motivo es más fuerte aquí que allí: esta página publica las direcciones
// de origen de todo el que ha tocado el nodo. A un usuario normal no le
// informa de nada suyo y a cambio le entregaría el mapa de la vigilancia.
//
// NO LLEVA conAlmacen: no toca ni un archivo. Todo sale del anillo en memoria.
//
// LLEVA FLUJO EN VIVO DESDE EL 2026-09-10 — /seguridad/flujo, ver
// vivo_seguridad.go. Aquí ponía que NO lo llevaba y que era deliberado: «un
// panel forense se lee, se filtra y se piensa; refrescarlo cuatro veces por
// segundo movería las filas bajo el cursor justo mientras se intenta leer una».
//
// Esa frase sigue siendo cierta en su parte importante, y por eso el flujo NO
// manda las tablas: refresca las cinco cifras de «Volumen observado» y el
// rótulo de conexión, y de la contención solo el recuento, con el que avisa de
// que hay algo nuevo sin mover nada. Lo que cambió es la conclusión —el
// responsable pidió ver los números moverse mientras mira— y el ritmo: dos
// segundos, no los 250 ms de /estado.

// ventanaPorOmision es el «últimas 24 horas» que pidió el responsable.
const ventanaPorOmision = 24 * time.Hour

// ventanas ofrecidas. Se quedan en cuatro a propósito: con un anillo de 2000
// eventos, ofrecer «último mes» prometería una profundidad que la estructura
// no puede dar en un nodo con tráfico.
var ventanas = []struct {
	Etiqueta string
	Horas    int
}{
	{"1 hora", 1},
	{"24 horas", 24},
	{"7 días", 168},
	{"Todo lo guardado", 0},
}

// filaOrigen es UNA DIRECCION con todo lo que se sabe de ella, en las tres
// capas — ADR-0065 y ADR-0066.
//
// # POR QUE UNA SOLA FILA Y NO TRES TABLAS
//
// Paquete, conexion y peticion son la MISMA pregunta a distinta profundidad, y
// cada capa es superconjunto de la siguiente: todo rechazo tuvo una conexion, y
// toda conexion empezo por un paquete. Con tres tablas hay que cruzar tres
// filas a ojo para reconstruir una sola historia; en columnas se lee de un
// vistazo, y es la historia del 16/08 la que lo demuestra:
//
//	2a06:4883:5000::65   DRIFTNET · GB   47 paq.   1 conex.   1 rech.
//	185.220.14.7         OVH · FR        12 paq.   0 conex.   0 rech.
//
// La segunda fila es lo que antes era INVISIBLE: alguien recorrio puertos
// cerrados y se fue sin llegar a hablar.
//
// NO SE MEZCLAN LAS CIFRAS, y eso es lo que respeta la regla de ADR-0064: son
// tres hechos distintos en tres columnas distintas. Lo que aquel ADR prohibio
// fue SUMARLOS -- que «Rechazos» contara cosas que no son rechazos --, no
// ponerlos uno al lado del otro. El sujeto es el mismo: la direccion.
//
// Vive aqui y no en internal/seguridad para que aquel paquete no dependa de
// internal/geoip: modela el hecho registrado y no tiene por que saber que
// existe una base de operadores. Quien une las piezas es este adaptador, igual
// que la raiz de composicion une fsposix con web (ADR-0014).
type filaOrigen struct {
	IP  netip.Addr
	Red seguridad.Red
	// Geo esta vacio cuando no hay base instalada, cuando el origen no es de
	// Internet -una IP privada no tiene operador- o cuando la base no cubre
	// ese rango. La plantilla distingue los tres casos de un dato real.
	Geo      geoip.Info
	TieneGeo bool

	// Capa 1 — PAQUETES. Solo de Internet y solo con sensor instalado.
	Toques  int
	Puertos []uint16

	// Capa 2 — CONEXIONES. Solo de Internet por construccion del anillo.
	Conexiones int

	// Capa 3 — RECHAZOS, con todo lo que solo existe cuando hubo peticion.
	Rechazos  int
	PorMotivo map[seguridad.Motivo]int
	Cuentas   []string
	Senales   []seguridad.Senal
	Gravedad  seguridad.Gravedad

	// LA EVIDENCIA, que es lo que faltaba: qué se pidió exactamente, con qué
	// método, qué contestó el servidor y cuántas veces.
	//
	// Sin esto, el panel afirmaba «Exploración automatizada» y quien lo leyera
	// tenía que creerse el umbral o irse al diario a reconstruirlo a mano. Una
	// inferencia cuya evidencia hay que buscar en otro sitio es indistinguible
	// de una inventada — y este panel se definió desde el primer día como uno
	// que no emite veredictos (ADR-0061).
	Evidencia    []seguridad.Sonda
	SondasVistas int
	// RutasInexistentes es la ENTRADA del umbral de exploración. Va dentro de
	// la explicación de la señal y NO como columna de la tabla: ADR-0065 quitó
	// esa columna con razón, porque enseñaba el cálculo al lado del resultado
	// sin decir que eran lo mismo.
	RutasInexistentes int

	// EL ESTADO DE ENFORCEMENT DE ESTA MISMA DIRECCION. Nulos cuando no hay.
	//
	// Se traen a la fila y no se dejan solo en sus tablas de arriba porque la
	// pregunta «¿y a este le estamos cerrando la puerta?» se hace MIRANDO la
	// fila del origen, no cruzando tres tablas a ojo. Es el mismo argumento con
	// el que ADR-0066 fundió las tres capas en columnas en vez de tres tablas.
	Apartado *seguridad.Apartado
	Bloqueo  *seguridad.Entrada

	Primera time.Time
	Ultima  time.Time
	// Seleccionada marca la fila cuyo detalle esta abierto.
	Seleccionada bool
}

// Frenados son las conexiones EFECTIVAMENTE CERRADAS a esta dirección, vengan
// de la política que vengan.
//
// # NO SE SUMAN LAS DOS, Y ESE ES EL PUNTO
//
// Una conexión TCP se cierra UNA vez. Si a esta dirección la alcanzan a la vez
// la cuarentena y un bloqueo manual, solo una de las dos contabilizó cada
// cierre —la lista, por la precedencia de seguridad.Decidir— y la otra estará
// en cero. Sumar los dos contadores produciría el doble de frenados que
// conexiones cerradas, que es exactamente la ficción que este trabajo vino a
// quitar. Se toma el mayor: el que de verdad estuvo contando.
func (f filaOrigen) Frenados() int64 {
	var n int64
	if f.Apartado != nil {
		n = f.Apartado.Frenados
	}
	if f.Bloqueo != nil && f.Bloqueo.Frenados > n {
		n = f.Bloqueo.Frenados
	}
	return n
}

// Frenada dice si a esta dirección se le está cerrando la puerta ahora mismo.
// Lo usa la plantilla para advertir de que sus cifras de rechazos se han
// congelado: después de cerrar la conexión ya no hay peticiones que contar.
func (f filaOrigen) Frenada() bool { return f.Apartado != nil || f.Bloqueo != nil }

// Destacar dice si la fila merece enfasis. Mismo criterio que tenia
// seguridad.Origen y por el mismo motivo: la gravedad de atencion se destaca
// venga de donde venga, y desde Internet se destaca todo, porque ver a alguien
// de fuera tocando la puerta ya es la senal.
func (f filaOrigen) Destacar() bool {
	return f.Gravedad == seguridad.Atencion || f.Red.DeFuera()
}

type vistaSeguridad struct {
	Resumen seguridad.Resumen
	// Origenes es LA tabla de la página: una fila por dirección, con las tres
	// capas en columnas. Ver filaOrigen.
	Origenes []filaOrigen
	Eventos  []seguridad.Evento

	// Las cifras de cada capa EN LA VENTANA, que el Resumen pinta como tres
	// filas etiquetadas.
	Conexiones int
	Toques     int
	// TotalConexiones son las conexiones desde que existe el registro, y se
	// pinta SOLO cuando supera a las de la ventana —para eso está
	// HayMasConexiones—. Ese es el caso en el que la cifra informa: dice que
	// hay historia que esta vista no enseña. Cuando coinciden, repetir el
	// mismo número con otra etiqueta era ruido, y es lo que había.
	//
	// Sobrevive a un reinicio desde el arreglo del 18/08 (totalDeCabecera).
	// EL DE PAQUETES TAMPOCO SE PINTABA, y por un motivo que ya no se sostiene:
	// nas-sensor no releía su archivo, así que su «desde siempre» volvía a cero
	// en cada arranque suyo y un número falso no se enseña con una etiqueta más
	// prudente. Desde que el sensor relee (nas-sensor/analisis.c, leer_toque y
	// leer_total) la cifra es verdad, y se enseña en TotalToques — pero solo en
	// el estado vacío, que es donde contesta una pregunta.
	TotalConexiones  int64
	HayMasConexiones bool
	// HayToques dice si el sensor está instalado. La plantilla lo usa para NO
	// pintar una columna de ceros, que se leería como «nadie me toca» cuando
	// significa «no lo estoy mirando» — mismo criterio que HayGeo.
	HayToques bool
	// TotalToques son los paquetes que el sensor ha capturado DESDE SIEMPRE,
	// tal como publica en su cabecera.
	//
	// Se enseña justo cuando la tabla sale vacía, porque es la única cifra que
	// distingue «no ha tocado nadie» de «el sensor está muerto»: sube con un
	// SYN de la LAN aunque el panel después no lo enseñe. Es lo que el propio
	// sensor.c dice que existe para eso.
	//
	// SOLO ES CREÍBLE DESDE QUE nas-sensor RELEE SU ARCHIVO. Hasta entonces se
	// ponía a cero en cada arranque —y en cada despliegue, que reinicia la
	// unidad—, así que un cero podía significar «recién arrancado» y no «no ha
	// visto nada». Esa es la razón de que esta cifra no se pintara aquí.
	TotalToques int64

	// PaquetesDesde y HuecoDePaquetes existen porque LAS TRES COLUMNAS NO
	// TIENEN LA MISMA MEMORIA, y callarlo convierte la tabla en una
	// contradicción.
	//
	// SIGUEN SIN TENERLA, aunque por mucho menos que antes. Hasta que el sensor
	// aprendió a releer su archivo, cada arranque suyo vaciaba la capa de
	// paquetes mientras los dos anillos de Go recuperaban la suya, y con 15
	// arranques en 14 días medidos ese desajuste era el estado NORMAL: el
	// efecto visible era una fila con «0 paquetes · 1 conexiones», que niega la
	// escalera que la propia tabla enseña.
	//
	// Ahora las tres releen, así que el hueco solo aparece por lo que siempre
	// debió ser: que el anillo del sensor tiene 2000 líneas y se llena antes
	// que los otros dos. Se sigue diciendo desde cuándo alcanza, porque la
	// pregunta que contesta no ha cambiado.
	//
	// La respuesta NO es esconder el desajuste ni pintar un cero: es decir
	// desde cuándo alcanza la capa de abajo. Es el mismo criterio que HayGeo y
	// que la fecha de la base de operadores — una cifra sin su alcance es una
	// afirmación sin respaldo.
	PaquetesDesde   time.Time
	HuecoDePaquetes bool
	// UltimoRechazo y UltimoFrenado son lo que se sabe cuando NO hay nada que
	// pintar, y existen porque un hueco en blanco no es una respuesta.
	//
	// # EL DEFECTO QUE ESTO CORRIGE, Y COSTÓ UNA NOCHE
	//
	// El 03/09 el responsable creyó que le habían vulnerado el NAS: el módulo de
	// seguridad llevaba tres días sin enseñar nada. No pasaba nada — el panel
	// abre filtrado a Internet, y desde Internet no había llamado nadie desde el
	// 31/08. Pero «no ha pasado nada» y «dejó de grabarse» se pintaban EXACTAMENTE
	// IGUAL: «Ningún rechazo en esta ventana con estos filtros», y ni una fecha.
	//
	// Una ausencia no se puede comprobar; una fecha sí. Con ella, además, la
	// prueba desde el móvil en datos móviles pasa a ser legible: si esa fecha
	// salta a hoy, la puerta del router y el registro están vivos los dos.
	//
	// # POR QUÉ EL FRENADO VA AL LADO
	//
	// Porque es la otra mitad de la explicación, y la que el responsable no tenía
	// forma de ver. A un origen bloqueado se le cuelga ANTES de que hable HTTP,
	// así que deja de aparecer en esta tabla aunque siga llamando: es
	// exactamente lo que le pasó con DRIFTNET. La plantilla ya lo advertía, pero
	// dentro del desplegable de la FILA de ese origen — que desaparece con él.
	// El aviso se esfumaba justo cuando hacía falta.
	UltimoRechazo ultimoHecho
	UltimoFrenado ultimoHecho
	// FiltroActivo es el rótulo que acompaña al título: «Internet · 24 horas».
	//
	// Sustituye a la frase que explicaba en prosa que la página abre filtrada
	// (ADR-0065). Aquella existía solo porque el desplegable que lo demuestra
	// queda media pantalla más abajo; el estado se enseña donde se mira
	// primero, no se cuenta.
	//
	// Los seis campos que había aquí —Horas, IP, Ruta, Motivo, Gravedad y
	// Red— se retiraron con él: decían devolver lo tecleado «para que los
	// campos del formulario lo conserven tras recargar», pero solo IP y Ruta
	// llegaban a pintarse. Los desplegables nunca los usaron: conservan lo
	// elegido con la marca Elegido de sus propias opciones.
	FiltroActivo string

	// SinFiltroDeRed es cierto cuando se está mirando «Cualquier origen».
	//
	// Decide si la tabla de orígenes enseña la columna «Procedencia». Con el
	// filtro por omisión esa columna decía «Internet» en TODAS las filas —una
	// columna constante no informa de nada— pero en cuanto se mezclan redes
	// vuelve a ser la que distingue el móvil de casa de un extraño. No se
	// retira: se enseña cuando tiene algo que decir (ADR-0065).
	SinFiltroDeRed bool
	// SoloInternet es cierto cuando se mira EXACTAMENTE Internet, que es el
	// filtro por omision. Decide si se ensenan las columnas de paquetes y
	// conexiones: las dos capas son de Internet por construccion, asi que con
	// cualquier otro filtro saldrian vacias, y una celda vacia se lee como
	// «cero» cuando significa «esta pregunta no aplica aqui».
	SoloInternet bool

	Ventanas []opcionFiltro
	Motivos  []opcionFiltro
	Redes    []opcionFiltro

	// TopeCronologico es cuántos eventos se listan en la vista cronológica.
	// Se publica para que la página pueda decir que está recortando en vez de
	// dar a entender que eso es todo.
	TopeCronologico int
	HayMas          bool
	// TopeRutas es lo mismo para la tabla de rutas, que recortaba a diez SIN
	// DECIRLO mientras la cronología sí lo avisaba. Dos tablas de la misma
	// página no pueden tener distinta idea de la honestidad.
	TopeRutas   int
	HayMasRutas bool
	// Capacidad es cuántos rechazos caben en el anillo.
	//
	// SE PUBLICA PORQUE LA CIFRA ANTERIOR ERA IMPRECISA: la nota decía «se
	// conservan los {Resumen.Eventos} más recientes de N vistos», pero
	// Resumen.Eventos es lo FILTRADO EN LA VENTANA, no lo que cabe en el
	// anillo. Con «?horas=1» llegaba a decir «se conservan los 3 más recientes
	// de 5000», que es falso: se conservan 2000.
	Capacidad int
	// FechaGeo es cuando se preparo la base. Se publica porque se refresca
	// una vez al mes y puede ir por detras de la realidad: una base vieja que
	// no se anuncia es una afirmacion que dejo de ser verdad sin avisar.
	FechaGeo time.Time
	// HayGeo dice si la base esta instalada. La plantilla lo usa para NO
	// pintar una columna vacia que se leeria como «no se sabe de nadie»
	// cuando en realidad es «no se ha instalado la base».
	HayGeo bool
	// Apartados son las direcciones que el nodo ha apartado SOLO, por
	// conducta. Es lo único de esta página que no es historia: es estado
	// vigente, y por eso se pinta arriba del todo y con su acción al lado.
	Apartados []seguridad.Apartado
	// Bloqueos son las entradas puestas a mano. Como los apartados, es estado
	// vigente y no historia, así que tampoco obedece a los filtros.
	Bloqueos []filaBloqueo
	// Hallazgos son las rutas que este NAS no publica y aun así atendió con
	// contenido.
	//
	// FUERA DE LOS FILTROS, como los apartados y los bloqueos, y por un motivo
	// que es suyo: un hallazgo no caduca porque pasen 24 horas. Que el nodo
	// devolviera 200 en /.git/config el martes sigue siendo verdad hoy si nadie
	// ha ido a mirar, y esconderlo tras la ventana sería apagar la única alarma
	// de la página dejando el problema en pie.
	//
	// SE PINTA DENTRO DE «CONTENCIÓN ACTIVA» DESDE EL 2026-09-10, junto a los
	// apartados y los bloqueos, por decisión del responsable. Este comentario
	// decía antes que iba aparte porque habla del NODO y no de quien llama, y
	// esa distinción sigue siendo cierta —la tabla conserva su título y su
	// rótulo de «requieren revisión»—. Lo que las agrupa no es quién las causa,
	// es qué hay que hacer con ellas: las tres son lo vigente y lo único de la
	// página que puede terminar en una acción. Ver seguridad.html.
	Hallazgos []seguridad.Hallazgo
	// SinContener es la frase que nombra ÚNICAMENTE los grupos de contención que
	// están vacíos, o "" si los tres tienen algo. Ver fraseDeVacios.
	SinContener string
	// Contencion es cuántas filas se pintaron en «Contención activa». Viaja a
	// la página como data-contencion para que el flujo en vivo pueda comparar y
	// avisar de que ya no es lo que se está viendo. Ver marcoSeguridad.
	Contencion int
	// TotalHallazgos son los sucesos vistos desde siempre, que puede ser mucho
	// mayor que las rutas distintas que se listan: una sola ruta expuesta y
	// pedida mil veces son mil sucesos y una fila.
	TotalHallazgos int64
	// CierresFallidos son los net.Conn.Close() que no se pudieron completar.
	//
	// SOLO SE PINTA CUANDO NO ES CERO, y no por estética: mientras vale cero, la
	// palabra «Frenados» significa exactamente conexiones cerradas y no hace
	// falta matizarla. En cuanto deja de valer cero, el panel está afirmando
	// menos de lo que pasa —hubo conexiones que debían cerrarse y siguieron
	// vivas— y eso hay que decirlo donde se leen las cifras que afecta.
	CierresFallidos int64
	// TopeSondas es cuántas líneas de evidencia se enseñan por origen. Se
	// publica para que la evidencia pueda decir que recorta, igual que ya hacen
	// la cronología y la tabla de rutas.
	TopeSondas int
	// Csrf viaja porque esta página dejó de ser de solo lectura el día que
	// se le pudo soltar a alguien.
	Csrf string
	// PuedeAdministrar decide si la barra lleva a Administración. Misma regla
	// que en el listado y en /estado (acotadoPorRed, sesion.go). Esta página
	// se ve desde Internet a propósito —mirar quién toca el nodo es justo lo
	// que se quiere poder hacer desde fuera— y por eso necesita la distinción.
	PuedeAdministrar bool
	// Marco es el cromo compartido — ADR-0075.
	Marco marco
	// FiltroAbierto conserva el panel de filtros desplegado despues de
	// filtrar. Sin esto habria que volver a abrirlo para corregir una
	// eleccion, que es justo cuando uno lo necesita.
	FiltroAbierto bool
	// Paneles es «Actividad por día»: UNO por serie, cada uno con su propio eje
	// de valores. Ver panelGrafica en grafica_seguridad.go, donde está escrito
	// por qué son dos marcos y no dos líneas en uno.
	//
	// Salen del conteo persistido del anillo (seguridad.Anillo.Serie) y NO de
	// los eventos filtrados: la ventana del filtro gobierna la tabla, y una
	// serie de doce días alimentada por una ventana de veinticuatro horas tenía
	// once columnas en cero por construcción. ActividadDesde dice desde qué día
	// hay dato de verdad.
	Paneles        []panelGrafica
	ActividadDesde time.Time
	// ConPaquetes decide si se dibuja la capa de abajo. Pide DOS cosas a la
	// vez: que haya sensor, y que la pagina este mirando Internet.
	//
	// LO SEGUNDO NO ES UN CAPRICHO: LeerToques descarta lo de casa en su
	// propia frontera —es el filtro «solo Internet» del que habla toques.go—,
	// asi que la capa de paquetes SOLO existe para Internet. Dibujarla junto a
	// unos rechazos filtrados por LAN pondria dos redes distintas en la misma
	// grafica, que es el defecto que pordia.go ya evito separando el conteo
	// por red.
	ConPaquetes bool
	// Detalle es el origen seleccionado por «?origen=», o nil si no vino el
	// parámetro o la dirección no está en Origenes. Se busca en la MISMA
	// lista que pinta la tabla, así que las dos no pueden discrepar.
	Detalle    *filaOrigen
	ConDetalle bool
}

// filaBloqueo es una entrada de la lista más lo único que ella sola no puede
// saber: si la base de operadores con la que se compuso ya quedó vieja.
//
// # POR QUÉ ESTO NO ES UN ADORNO
//
// El comentario de seguridad.Entrada.BaseGeo dice que el alcance «es una FOTO,
// no una regla viva»: si mañana el operador anuncia un tramo nuevo, la entrada
// NO lo cubre, y eso es deliberado —una regla viva cambiaría lo bloqueado sin
// que nadie lo decidiera—. El mismo comentario prometía la contrapartida: «con
// la fecha delante, el panel puede decir que la base es más nueva que la
// entrada y ofrecer revisarla».
//
// Esa segunda mitad no estaba implementada: la fecha se guardaba y no se
// pintaba en ninguna parte. Sin ella, la foto envejece EN SILENCIO, que es
// justo el modo de fallo contra el que se guardaba la fecha.
type filaBloqueo struct {
	seguridad.Entrada
	// BaseMasNueva es cierto cuando hay base instalada y su fecha es posterior
	// a la de la foto. No dice que la entrada esté mal —puede seguir siendo
	// exacta—: dice que se puede volver a mirar.
	BaseMasNueva bool
}

// opcionFiltro es un valor para un <select>: la clave estable que viaja por
// la URL y la etiqueta que se lee.
type opcionFiltro struct {
	Valor    string
	Etiqueta string
	Elegido  bool
}

// topeCronologico acota la lista de eventos que se PINTA, no la que se
// analiza: el resumen y la agrupación siguen viendo todo lo filtrado.
//
// Sin este tope, un sondeo de 2000 peticiones produciría una página de 2000
// filas que un Pi 3B+ tiene que renderizar y un móvil tiene que descargar —
// para no decir nada que la tabla de orígenes no diga ya mejor agregado.
const topeCronologico = 200

// soltarApartado retira una dirección de la cuarentena antes de que caduque.
//
// # POR QUÉ NO PIDE LA CONTRASEÑA OTRA VEZ
//
// La baja de una cuenta sí la pide (RF-27, 04_SEGURIDAD §2.ter) porque no se
// deshace. Esto se deshace solo: si la conducta sigue, la siguiente evaluación
// vuelve a apartar a la misma dirección en menos de un minuto. Cobrar una
// derivación de 3.6 s por un gesto reversible sería confundir la ceremonia
// con el control.
//
// # POR QUÉ SE PUEDE HACER DESDE INTERNET
//
// No lleva soloDesdeDentro, al revés que borrar o administrar cuentas. Soltar
// a alguien no destruye nada, y el caso de uso es exactamente el contrario al
// que aquella regla protege: alguien que se ha quedado fuera por error tiene
// que poder arreglarlo desde donde esté.
func (s *Servidor) soltarApartado(w http.ResponseWriter, r *http.Request) {
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
	// Se registra SOLO si había algo que soltar: un diario que anota
	// «soltado» ante una dirección que nunca estuvo apartada convierte el
	// registro en algo que no se puede leer para reconstruir qué pasó.
	if s.cuarentena.Soltar(ip) {
		s.reg.Info("apartado soltado a mano",
			"origen", ip.String(), "usuario", usuarioDe(r), "desde", origenDe(r))
	}
	http.Redirect(w, r, "/seguridad", http.StatusSeeOther)
}

// fraseDeVacios nombra los grupos de «Contención activa» que están vacíos, y
// solo esos. Devuelve "" cuando los tres tienen algo, porque entonces no hay
// nada que decir: las tres tablas están a la vista.
//
// # POR QUÉ NO ES UNA PASTILLA DE ESTADO
//
// Aquí hubo una que decía «sin hallazgos», y el responsable la tumbó el
// 2026-09-10 con la pregunta que la desarma: «entonces si pongo un bloqueo a
// mano, ¿se cambia de color?». Una pastilla que resume TRES listas tiene que
// elegir un color en cuanto una de ellas deja de estar vacía, y cualquier
// elección afirma algo falso sobre las otras dos —o dice «todo bien» habiendo
// un hallazgo, o dice «atención» estando los apartados en su sitio—.
//
// Enumerar solo lo vacío no puede equivocarse, porque no afirma nada en
// positivo: cada trozo de la frase desaparece con el grupo que lo motivaba. Es
// la misma regla del vacío con fecha de la cronología (ADR-0083): decir qué NO
// hay es una afirmación, y tiene que ser tan exacta como las demás.
//
// El formato va aquí y no en la plantilla por ADR-0017: componer esta frase con
// {{if}} anidados serían ocho combinaciones escritas a mano en HTML, y siete de
// ellas no las probaría nadie.
func fraseDeVacios(apartados, bloqueos, hallazgos int) string {
	var partes []string
	if apartados == 0 {
		partes = append(partes, "sin apartados por conducta")
	}
	if bloqueos == 0 {
		partes = append(partes, "sin bloqueos puestos a mano")
	}
	if hallazgos == 0 {
		partes = append(partes, "sin respuestas inesperadas del servidor")
	}
	if len(partes) == 0 {
		return ""
	}
	// EL «sin» SE REPITE EN CADA PARTE, y no se factoriza al principio. Con uno
	// solo, «Sin bloqueos puestos a mano y respuestas inesperadas del servidor»
	// se lee como si el nodo hubiera tenido respuestas inesperadas. Visto al
	// mirar la página renderizada, no al escribirla.
	//
	// «y» solo delante del último, que es como se enumera en español.
	frase := partes[0]
	switch len(partes) {
	case 2:
		frase = partes[0] + " y " + partes[1]
	case 3:
		frase = partes[0] + ", " + partes[1] + " y " + partes[2]
	}
	return strings.ToUpper(frase[:1]) + frase[1:] + "."
}

// filtroDeSeguridad interpreta los parámetros de la URL del panel: la ventana,
// el motivo y la red de origen.
//
// # VIVE APARTE PARA QUE LA PÁGINA Y SU FLUJO NO PUEDAN DISCREPAR — 2026-09-10
//
// Estaba escrito dentro de verSeguridad, y ahí se quedaba mientras el panel era
// lo único que lo necesitaba. Desde que /seguridad/flujo refresca las cifras en
// vivo hay DOS sitios que tienen que entender «?red=&horas=168» exactamente
// igual: copiarlo sería crear dos criterios para la misma pregunta, y el día
// que uno de los dos cambiara, el flujo estaría refrescando las cifras de otra
// vista sin que nada fallara a gritos. Es la misma regla con la que el rótulo
// del filtro se compone de las MISMAS listas que pintan los desplegables.
//
// Devuelve también las horas y la red en crudo porque de ellas salen las
// opciones de los desplegables, que el flujo no necesita y la página sí.
func filtroDeSeguridad(q url.Values) (f seguridad.Filtro, horas time.Duration, redElegida string) {
	horas = ventanaPorOmision / time.Hour
	if v := q.Get("horas"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			horas = time.Duration(n)
		}
	}
	// LOS FILTROS POR DIRECCIÓN, RUTA Y GRAVEDAD SE RETIRARON (ADR-0065). Los
	// dos primeros porque el responsable declaró por uso real que no le
	// servían; el tercero porque filtraba por una columna que el panel no
	// enseña —la gravedad solo se ve como el color de una fila, y el mapa
	// motivo→gravedad vive únicamente en Motivo.Gravedad()—.
	//
	// horas=0 significa «todo lo guardado»: Desde queda en el cero de
	// time.Time, que el anillo interpreta como sin límite inferior.
	if horas > 0 {
		f.Desde = time.Now().Add(-horas * time.Hour)
	}
	// Sin comprobar la cadena vacía por separado: MotivoDesde("") ya devuelve
	// false, porque la forma estable de MotivoDesconocido es «desconocido» y
	// ninguna casa con "".
	if m, ok := seguridad.MotivoDesde(q.Get("motivo")); ok {
		f.Motivo = &m
	}

	// EL PANEL ABRE EN «INTERNET» Y NO EN «TODO», por encargo del responsable
	// del 2026-08-16: «quisiera que lo de la lan y túnel wireguard quedara en
	// segundo lugar y únicamente reporte conexiones entrantes en internet».
	//
	// Segundo lugar, NO fuera: la LAN y el túnel siguen enteros a una opción
	// del desplegable. Lo que cambia es cuál es el estado limpio de la página.
	// Antes había que acordarse de filtrar para ver lo único que se declaró
	// importante, con la casa —que es casi todo el tráfico— tapándolo.
	//
	// q.Has y no q.Get: los dos devuelven "" para «no vino el parámetro» y
	// para «vino vacío», y aquí significan lo contrario. El <select> manda
	// siempre red= (vacío en «Cualquier origen»), así que elegir «todo» se
	// distingue de no haber elegido nada. Consecuencia buscada: «Quitar
	// filtros», que no manda campos, devuelve a Internet-solo.
	redElegida = seguridad.RedInternet.String()
	if q.Has("red") {
		redElegida = q.Get("red")
	}
	if red, ok := redDesde(redElegida); ok {
		f.Red = &red
	}
	return f, horas, redElegida
}

func (s *Servidor) verSeguridad(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f, horas, redElegida := filtroDeSeguridad(q)

	eventos := s.seguridad.Filtrados(f)
	origenes := seguridad.PorOrigen(eventos)
	resumen := seguridad.Resumir(eventos, origenes, s.seguridad.Total())
	// El resumen sabe si el anillo está lleno; la página lo dice para que un
	// anillo saturado no se lea como «esto es todo lo que ha pasado».
	resumen.Truncado = resumen.TotalHistorico > int64(seguridad.Capacidad)

	cronologia := eventos
	hayMas := false
	if len(cronologia) > topeCronologico {
		cronologia = cronologia[:topeCronologico]
		hayMas = true
	}

	// Las conexiones NO pasan por el Filtro: no tienen motivo, ni gravedad, ni
	// ruta que filtrar, y son de Internet por construcción. Lo único que
	// comparten con los rechazos es la VENTANA, y por eso es lo único que se
	// les aplica — inventarles los demás filtros sería ofrecer controles que
	// no pueden hacer nada.
	conexiones := s.conexiones.Desde(f.Desde)

	// LOS TOQUES SE LEEN DEL DISCO, no de memoria: quien los escribe es otro
	// proceso (nas-sensor) y nasd solo lee. Un fallo aqui NO tumba la pagina --
	// se anota en el diario y las columnas de esa capa se esconden, que es
	// mejor que ensenar una cifra construida sobre lo que si se pudo leer.
	//
	// SE PIDE LO MAS HONDO DE LAS DOS COSAS QUE LO USAN: la tabla quiere la
	// ventana del filtro y la grafica quiere las doce columnas de la serie.
	// Una sola lectura y un recorte despues (Historial.EnVentana); con dos
	// lecturas serian dos fotos distintas de un archivo que reescribe otro
	// proceso cada minuto.
	ahora := time.Now()
	desdeToques := seguridad.InicioDeSerie(ahora)
	if f.Desde.IsZero() || f.Desde.Before(desdeToques) {
		// El cero de «Todo lo guardado» no acota nada, y por eso gana.
		desdeToques = f.Desde
	}
	hist, err := seguridad.LeerToques(s.rutaToques, desdeToques)
	if err != nil {
		s.reg.Warn("no se pudo leer el historial de toques",
			"ruta", s.rutaToques, "error", err)
	}
	enVentana := hist.EnVentana(f.Desde)
	tocados := seguridad.PorOrigenTocado(enVentana)

	// Las tres listas se construyen antes del literal porque el rótulo del
	// filtro activo sale de ELLAS y no de los parámetros crudos de la URL: si
	// salieran de sitios distintos, el rótulo podría decir «Internet» mientras
	// el desplegable enseña otra cosa.
	ventanas := opcionesDeVentana(int(horas))
	motivos := opcionesDeMotivo(q.Get("motivo"))
	redes := opcionesDeRed(redElegida)

	// El estado vigente de los DOS controles se lee UNA vez y se reparte: la
	// tabla de arriba lo pinta entero y unirOrigenes lo cuelga de la fila de
	// cada dirección. Leerlo dos veces daría dos fotos de instantes distintos y
	// la misma página podría enseñar a alguien apartado arriba y libre abajo.
	apartados := s.cuarentena.Vigentes(ahora)
	bloqueos := s.lista.Vigentes(ahora)

	hallazgos := s.hallazgos.Todos()

	v := vistaSeguridad{
		PuedeAdministrar: !acotadoPorRed(r),
		Apartados:        apartados,
		Bloqueos:         s.filasDeBloqueo(bloqueos),
		Hallazgos:        hallazgos,
		SinContener:      fraseDeVacios(len(apartados), len(bloqueos), len(hallazgos)),
		Contencion:       len(apartados) + len(bloqueos) + len(hallazgos),
		TotalHallazgos:   s.hallazgos.Total(),
		CierresFallidos:  s.cierresFallidos.Load(),
		TopeSondas:       seguridad.TopeSondas,
		Csrf:             s.csrfDe(r),
		Resumen:          resumen,
		Origenes: s.unirOrigenes(
			tocados, seguridad.PorOrigenConectado(conexiones), origenes,
			apartados, bloqueos, f.Motivo != nil),
		Conexiones:       len(conexiones),
		TotalConexiones:  s.conexiones.Total(),
		HayMasConexiones: s.conexiones.Total() > int64(len(conexiones)),
		Toques:           len(enVentana),
		HayToques:        hist.Hay,
		TotalToques:      hist.Total,
		PaquetesDesde:    hist.Desde,
		HuecoDePaquetes:  hist.Hay && !hist.Desde.IsZero() && hist.Desde.After(f.Desde),
		UltimoRechazo:    ultimoRechazoDe(s.seguridad, f.Red),
		UltimoFrenado:    ultimoFrenadoDe(bloqueos, apartados),
		HayGeo:           s.geo != nil,
		FechaGeo:         s.geo.Fecha(),
		Eventos:          cronologia,
		FiltroActivo:     rotuloDeFiltro(redes, ventanas, motivos),
		SinFiltroDeRed:   f.Red == nil,
		SoloInternet:     f.Red != nil && *f.Red == seguridad.RedInternet,
		Ventanas:         ventanas,
		Motivos:          motivos,
		Redes:            redes,
		TopeCronologico:  topeCronologico,
		HayMas:           hayMas,
		TopeRutas:        seguridad.TopeRutas,
		HayMasRutas:      resumen.RutasVistas > seguridad.TopeRutas,
		Capacidad:        seguridad.Capacidad,
		Marco:            s.construirMarco(r, "seguridad", "Seguridad", ""),
	}

	// LA SERIE NO PASA POR EL FILTRO DE VENTANA, y ese era el defecto: con
	// «24 horas» —que es como abre el panel— once de las doce columnas salían
	// en cero para todo el mundo. Sí obedece al filtro de RED, que es el que
	// decide de quién se está hablando en toda la página. Ver pordia.go.
	// Y LA CAPA DE PAQUETES ENTRA AQUI, con los toques que ya se leyeron: sin
	// ella la serie solo dibuja lo que llego a pedir algo por HTTP, y un
	// escaneo del 443 que muere en el saludo TLS sale como un dia vacio.
	diasActividad, actividadDesde := s.seguridad.Serie(ahora, f.Red)
	v.ConPaquetes = hist.Hay && v.SoloInternet
	if v.ConPaquetes {
		diasActividad = seguridad.ConToques(diasActividad, hist.Toques)
	}
	v.Paneles = graficaDeActividad(diasActividad, v.ConPaquetes)
	v.ActividadDesde = actividadDesde

	v.FiltroAbierto = q.Get("abierto") != ""

	if ip, err := netip.ParseAddr(q.Get("origen")); err == nil {
		for i := range v.Origenes {
			if v.Origenes[i].IP == ip {
				v.Origenes[i].Seleccionada = true
				v.Detalle = &v.Origenes[i]
				v.ConDetalle = true
				break
			}
		}
	}

	// LA VISITA SE SELLA AL PINTAR, y con eso la marca de la barra se apaga.
	// Va aquí y no en la barra: mirar el panel es lo que significa «ya lo he
	// visto», y la barra se pinta en páginas donde no se ha visto nada.
	s.novedades.Visto(time.Now())

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.plantillas.ExecuteTemplate(w, "seguridad.html", v); err != nil {
		s.reg.Error("render del panel de seguridad", "error", err)
	}
}

// Las tres funciones siguientes construyen las opciones de los desplegables a
// partir de los MISMOS tipos del dominio, recorriéndolos. Escribir las
// opciones a mano en la plantilla dejaría un motivo nuevo sin filtro y nadie
// se enteraría hasta necesitarlo.

func opcionesDeVentana(elegida int) []opcionFiltro {
	out := make([]opcionFiltro, 0, len(ventanas))
	for _, v := range ventanas {
		out = append(out, opcionFiltro{
			Valor:    strconv.Itoa(v.Horas),
			Etiqueta: v.Etiqueta,
			Elegido:  elegida == v.Horas,
		})
	}
	return out
}

func opcionesDeMotivo(elegido string) []opcionFiltro {
	out := []opcionFiltro{{Valor: "", Etiqueta: "Todos los motivos", Elegido: elegido == ""}}
	// El tope es seguridad.UltimoMotivo y NO un valor escrito a mano. Estaba
	// escrito a mano —«<= PeticionMalformada»— y por eso «Solo desde dentro»
	// llevaba desde el 2026-08-19 sin aparecer en el desplegable: se añadió el
	// motivo, se actualizó el tope de MotivoDesde y este se quedó atrás. Un
	// filtro que falta no da error, solo deja de ofrecer una opción.
	for m := seguridad.MotivoDesconocido; m <= seguridad.UltimoMotivo; m++ {
		out = append(out, opcionFiltro{
			Valor: m.String(), Etiqueta: m.Etiqueta(), Elegido: elegido == m.String(),
		})
	}
	return out
}

// elegida devuelve la opción marcada, o la vacía si ninguna lo está — que es lo
// que pasa con un valor inventado en la URL, como «?horas=5».
func elegida(opciones []opcionFiltro) opcionFiltro {
	for _, o := range opciones {
		if o.Elegido {
			return o
		}
	}
	return opcionFiltro{}
}

// ultimoHecho es lo último que se sabe de una capa: cuándo fue y qué fue.
//
// Es lo que convierte «aquí no hay nada» en una afirmación comprobable. Hay
// deliberadamente separado de Cuando: un instante cero se pintaría como el año
// 1, y decir una fecha falsa es peor que no decir ninguna.
type ultimoHecho struct {
	Hay    bool
	Cuando time.Time
	Que    string
}

// ultimoRechazoDe busca el rechazo más reciente de esta red en TODO lo
// guardado, no solo en la ventana que se está mirando.
func ultimoRechazoDe(a *seguridad.Anillo, red *seguridad.Red) ultimoHecho {
	e, hay := a.Ultimo(red)
	if !hay {
		return ultimoHecho{}
	}
	return ultimoHecho{Hay: true, Cuando: e.Momento, Que: e.Origen.String()}
}

// ultimoFrenadoDe busca el cierre de puerta más reciente entre los dos
// controles, para que un vacío pueda decir «no aparece porque se le cuelga».
//
// SE MIRAN LOS DOS Y GANA EL MÁS RECIENTE, no la lista por ser la lista: aquí
// no se está atribuyendo un mérito —eso es cosa de puerta.go— sino contestando
// «¿cuándo llamó alguien por última vez y se le colgó?». La respuesta es un
// instante, y el instante es el mayor de los dos.
func ultimoFrenadoDe(bloqueos []seguridad.Entrada, apartados []seguridad.Apartado) ultimoHecho {
	var out ultimoHecho
	considerar := func(cuando time.Time, que string) {
		if cuando.IsZero() || (out.Hay && !cuando.After(out.Cuando)) {
			return
		}
		out = ultimoHecho{Hay: true, Cuando: cuando, Que: que}
	}
	for _, b := range bloqueos {
		considerar(b.UltimoFrenado, b.Etiqueta)
	}
	for _, a := range apartados {
		considerar(a.UltimoFrenado, a.IP.String())
	}
	return out
}

// rotuloDeFiltro compone el «Internet · 24 horas» que acompaña al título.
//
// El motivo solo aparece cuando se ha elegido uno: «Todos los motivos» es la
// AUSENCIA de filtro, y anunciarla en cada carga sería el mismo ruido que este
// trabajo viene a quitar. La red y la ventana van siempre, porque la página
// siempre tiene una de cada y la de por omisión no es la neutra.
func rotuloDeFiltro(redes, ventanas, motivos []opcionFiltro) string {
	partes := make([]string, 0, 3)
	if e := elegida(redes); e.Etiqueta != "" {
		partes = append(partes, e.Etiqueta)
	}
	if e := elegida(ventanas); e.Etiqueta != "" {
		partes = append(partes, e.Etiqueta)
	}
	if e := elegida(motivos); e.Valor != "" {
		partes = append(partes, e.Etiqueta)
	}
	return strings.Join(partes, " · ")
}

func opcionesDeRed(elegido string) []opcionFiltro {
	out := []opcionFiltro{{Valor: "", Etiqueta: "Cualquier origen", Elegido: elegido == ""}}
	// Internet PRIMERO en el desplegable, por lo mismo que va primero en el
	// resumen: es el filtro que el responsable va a querer casi siempre.
	for _, red := range []seguridad.Red{
		seguridad.RedInternet, seguridad.RedTunel, seguridad.RedLocal, seguridad.RedNodo,
	} {
		out = append(out, opcionFiltro{
			Valor: red.String(), Etiqueta: red.Etiqueta(), Elegido: elegido == red.String(),
		})
	}
	return out
}

func redDesde(s string) (seguridad.Red, bool) {
	for _, red := range []seguridad.Red{
		seguridad.RedInternet, seguridad.RedTunel, seguridad.RedLocal, seguridad.RedNodo,
	} {
		if red.String() == s {
			return red, true
		}
	}
	return seguridad.RedDesconocida, false
}

// filasDeBloqueo acompaña cada entrada con si la base de operadores ya es más
// nueva que la foto con la que se compuso. Ver filaBloqueo.
//
// SIN BASE INSTALADA NO SE DICE NADA: no se puede comparar contra una fecha que
// no existe, y marcar todas las entradas como «revisables» porque falta la base
// sería inventarse una alarma.
func (s *Servidor) filasDeBloqueo(entradas []seguridad.Entrada) []filaBloqueo {
	base := s.geo.Fecha()
	out := make([]filaBloqueo, 0, len(entradas))
	for _, e := range entradas {
		out = append(out, filaBloqueo{
			Entrada: e,
			// Solo para el alcance «operador»: es el único cuya foto puede
			// quedarse corta al anunciarse un tramo nuevo. Una dirección suelta
			// y un rango escrito a mano no dependen de la base para seguir
			// siendo exactos, y marcarlos sería mandar a revisar algo que no
			// puede haber cambiado.
			BaseMasNueva: e.Alcance == seguridad.AlcanceOperador &&
				!base.IsZero() && !e.BaseGeo.IsZero() && base.After(e.BaseGeo),
		})
	}
	return out
}

// unirOrigenes funde las TRES capas en una fila por direccion.
//
// La union se hace por direccion y no por ningun otro campo porque es lo unico
// que las tres comparten: un paquete no tiene ruta, y una conexion no tiene
// motivo. Una direccion puede aparecer en las tres, en dos o en una sola, y
// cada combinacion significa algo distinto y legible:
//
//	paquetes, conexiones y rechazos  ->  llego, hablo y se le nego algo
//	paquetes y conexiones, sin rechazos -> murio en el saludo TLS (DRIFTNET)
//	solo paquetes  ->  recorrio puertos cerrados y se fue
//
// Las dos primeras capas SOLO traen direcciones de Internet -- una por
// construccion del anillo, la otra porque LeerToques descarta lo de casa --,
// asi que con el filtro en cualquier otra red esas columnas quedan vacias y la
// plantilla las esconde en vez de pintar ceros.
// SOLO CON RECHAZOS: cuando hay un filtro de MOTIVO puesto, la tabla se acota a
// las direcciones que produjeron ese motivo.
//
// Hace falta porque las otras dos capas no saben de motivos -- un paquete no
// tiene ninguno -- y sin esto, filtrar por «Credencial incorrecta» seguiria
// ensenando cada direccion que solo envio paquetes, con un 0 en la columna de
// rechazos. Un filtro que deja pasar justo lo que no cumple es peor que no
// tenerlo: el defecto que ADR-0065 acaba de retirar de esta misma pagina.
func (s *Servidor) unirOrigenes(
	tocados []seguridad.OrigenTocado,
	conectados []seguridad.OrigenConectado,
	origenes []seguridad.Origen,
	apartados []seguridad.Apartado,
	bloqueos []seguridad.Entrada,
	soloConRechazos bool,
) []filaOrigen {
	porIP := make(map[netip.Addr]*filaOrigen)
	dame := func(ip netip.Addr, red seguridad.Red) *filaOrigen {
		f, ok := porIP[ip]
		if !ok {
			f = &filaOrigen{IP: ip, Red: red}
			porIP[ip] = f
		}
		return f
	}
	// Los instantes se funden en un solo intervalo: el mas temprano y el mas
	// tardio de las tres capas. Sin esto, una fila con paquetes y rechazos
	// tendria dos intervalos y habria que elegir cual mentir.
	extender := func(f *filaOrigen, primera, ultima time.Time) {
		if f.Primera.IsZero() || primera.Before(f.Primera) {
			f.Primera = primera
		}
		if ultima.After(f.Ultima) {
			f.Ultima = ultima
		}
	}

	for _, o := range tocados {
		f := dame(o.IP, seguridad.RedInternet)
		f.Toques, f.Puertos = o.Toques, o.Puertos
		extender(f, o.Primera, o.Ultima)
	}
	for _, o := range conectados {
		f := dame(o.IP, seguridad.RedInternet)
		f.Conexiones = o.Conexiones
		extender(f, o.Primera, o.Ultima)
	}
	for _, o := range origenes {
		f := dame(o.IP, o.Red)
		// La red REAL la manda el rechazo: es la unica capa que la clasifica
		// evento por evento, y las otras dos son de Internet por construccion.
		f.Red = o.Red
		f.Rechazos, f.PorMotivo = o.Eventos, o.PorMotivo
		f.Cuentas, f.Senales, f.Gravedad = o.Cuentas, o.Senales, o.Gravedad
		f.Evidencia, f.SondasVistas = o.Evidencia, o.SondasVistas
		f.RutasInexistentes = o.RutasInexistentes
		extender(f, o.Primera, o.Ultima)
	}

	// EL ESTADO DE ENFORCEMENT SE CUELGA DE LAS FILAS QUE YA EXISTEN, y no crea
	// ninguna: un apartado sin conexiones ni rechazos en la ventana no debe
	// inventarse una fila con todo a cero, porque esa fila diría «vino y no
	// hizo nada» cuando lo que pasa es que la ventana no lo alcanza. Para eso
	// está la tabla de apartados, que vive fuera de los filtros.
	for i := range apartados {
		if f, hay := porIP[apartados[i].IP]; hay {
			f.Apartado = &apartados[i]
		}
	}
	for _, f := range porIP {
		// La entrada que lo cubre se busca con el MISMO recorrido que usa la
		// puerta —la primera vigente que lo contenga— para que el panel no
		// pueda atribuirle el bloqueo a una entrada distinta de la que de
		// verdad lo cerraría.
		for i := range bloqueos {
			if bloqueos[i].Contiene(f.IP) {
				f.Bloqueo = &bloqueos[i]
				break
			}
		}
	}

	filas := make([]filaOrigen, 0, len(porIP))
	for _, f := range porIP {
		if soloConRechazos && f.Rechazos == 0 {
			continue
		}
		// LA GEO SE RESUELVE AL PINTAR Y SOLO PARA LOS DE INTERNET. Una
		// direccion privada no tiene operador que buscar, y ensenar «el operador»
		// junto a los propios aparatos del responsable seria ruido en la unica
		// tabla que existe para mirar hacia fuera.
		//
		// Al pintar y no al anotar, igual que las senales: es dato derivado.
		// Persistirlo congelaria el operador del dia en que llego, y refrescar
		// la base cada mes no corregiria el historial. El coste es
		// despreciable: una busqueda binaria por direccion mostrada, no por
		// evento.
		if f.Red.DeFuera() {
			f.Geo, f.TieneGeo = s.geo.Buscar(f.IP)
		}
		filas = append(filas, *f)
	}

	slices.SortFunc(filas, func(x, y filaOrigen) int {
		if c := cmp.Compare(y.Gravedad, x.Gravedad); c != 0 {
			return c
		}
		// Luego la actividad TOTAL de las tres capas: quien mas ha tocado el
		// nodo, sea en la capa que sea, va antes.
		if c := cmp.Compare(y.Toques+y.Conexiones+y.Rechazos,
			x.Toques+x.Conexiones+x.Rechazos); c != 0 {
			return c
		}
		// Tercer criterio para que el orden sea DETERMINISTA: sin el, dos
		// filas empatadas salen en el orden aleatorio del recorrido del mapa y
		// la tabla baila entre recargas sin que nada haya cambiado.
		return strings.Compare(x.IP.String(), y.IP.String())
	})
	return filas
}
