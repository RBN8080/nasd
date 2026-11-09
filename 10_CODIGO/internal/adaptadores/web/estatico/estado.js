// Flujo en vivo del estado del nodo — ADR-0051.
//
// LO USAN DOS PÁGINAS: /estado, con su tabla de quince indicadores, y /resumen,
// con tres tarjetas. No hay dos archivos ni dos flujos, por el mismo motivo por
// el que solo hay un muestreador (ADR-0056): una copia sería un segundo cliente
// del mismo canal, y el día que uno cambiara el otro seguiría pintando lo de
// antes sin fallar a gritos. Lo único que hace falta para engancharse es un
// «data-clave» con un «.cifra» dentro; lo que la página no tenga —la pastilla,
// la acción, el rótulo de conexión— sencillamente no se toca.
//
// Escrito a mano e incrustado en el binario, como subida.js: ADR-0017 prohíbe
// CDN y dependencias externas. Aquí no hacía falta ni eso — EventSource es
// parte del navegador desde hace más de una década.
//
// ESTE ARCHIVO NO CALCULA NADA, Y ES LA REGLA QUE LO MANTIENE PEQUEÑO. El
// servidor manda el texto ya escrito tal como debe leerse (ADR-0017 pone el
// render en el servidor, y eso incluye el formato). Aquí solo se sustituye
// texto. Por eso la página sin JavaScript y la página con él no pueden decir
// cosas distintas: es literalmente el mismo texto, compuesto en el mismo sitio.
// Dos matices: la geometría de las gráficas se dibuja aquí (ADR-0105), y un
// veredicto nuevo se pinta con 3 s de retraso (ADR-0106, ver
// MARCOS_PARA_CAMBIAR). Ninguno decide nada: el qué sigue viniendo hecho.
'use strict';

// Tras este número de fallos SEGUIDOS se deja de insistir.
//
// EventSource reintenta solo y para siempre, que es lo correcto ante un corte
// de red. Pero una pestaña olvidada cuya sesión caducó —siete días, RF-15—
// recibiría un rechazo cada pocos segundos hasta que alguien la cierre. Se
// corta y se dice por qué, en vez de picar al servidor de por vida.
const FALLOS_PARA_RENDIRSE = 10;

let fallos = 0;

// UN VEREDICTO NUEVO ESPERA 3 s ANTES DE PINTARSE (ADR-0106). La temperatura
// del nodo vive pegada a los 70 °C del aviso —medido ese día: 69.8, 70.4,
// 69.8 °C varias veces por segundo— y cada cruce cambiaba la pastilla y ponía o
// quitaba las tres líneas de «qué hacer». La fila crecía y encogía, y en el
// iPhone, donde Safari no ancla el desplazamiento, todo lo de debajo saltaba
// bajo el dedo: 27 saltos de hasta 87 px en 8 s, medido en marco de teléfono.
// ISA-18.2 llama a eso una alarma que parpadea («chattering») y lo corrige con
// un retardo. El NÚMERO sigue en vivo; el veredicto solo cambia cuando el
// nuevo se mantiene doce marcos seguidos.
const MARCOS_PARA_CAMBIAR = 12; // 3 s a un marco cada 250 ms

// La firma de lo que se ve de un veredicto: clase, rótulo y acción.
function firma(clase, etiqueta, accion) {
  return clase + '\n' + etiqueta + '\n' + accion;
}

// El índice se construye UNA vez, recorriendo lo que ya pintó el servidor.
//
// Se hace así en lugar de buscar cada fila en cada marco por dos motivos: son
// cuatro marcos por segundo y no hay razón para repetir la búsqueda, y sobre
// todo porque construir un selector CSS con texto que llega por la red es una
// costumbre que conviene no tener, aunque hoy ese texto salga de una constante
// nuestra.
//
// UNA CLAVE, UN SITIO EN LA PÁGINA: el mapa guarda un destino suelto porque
// ninguna de las dos páginas repite un indicador. Si alguna llegara a hacerlo,
// el segundo pisaría al primero aquí y uno de los dos se quedaría congelado en
// el valor de la carga al lado del otro actualizándose — el indicador que
// miente de 00_RECTOR.md §12.5. La regla es no repetirlo, no indexar listas.
function indexar() {
  const claves = new Map();
  for (const nodo of document.querySelectorAll('[data-clave]')) {
    const accion = nodo.querySelector('.accion');
    const pastilla = nodo.querySelector('.pastilla');
    claves.set(nodo.dataset.clave, {
      cifra: nodo.querySelector('.cifra'),
      accion,
      pastilla,
      // Lo que pintó el servidor es lo que se ve; nulo si la fila no tiene
      // veredicto que enseñar en esta página (las de /resumen).
      pintado: accion || pastilla
        ? firma(pastilla ? pastilla.className : '', pastilla ? pastilla.textContent : '',
          accion ? accion.textContent : '')
        : null,
      candidato: null,
      veces: 0,
    });
  }
  return claves;
}

// Solo se escribe si el texto cambió. La mayoría de las filas son idénticas de
// un marco al siguiente —los contadores de un NAS doméstico no se mueven
// cuatro veces por segundo— y escribir igual haría trabajar al navegador para
// nada, además de romper la selección de texto de quien esté leyendo.
function ponerTexto(elemento, texto) {
  if (elemento && elemento.textContent !== texto) {
    elemento.textContent = texto;
  }
}

// LAS GRÁFICAS EN VIVO (consola 2, ADR-0105). La CPU dibuja su minigráfica
// con la historia que trae la página (ADR-0106) y las muestras que el flujo YA
// trae, y la temperatura mueve su gráfica de bala. Se tocan ATRIBUTOS de SVG
// —points, d, width— y nunca «style»: la CSP descarta los estilos en línea
// (ADR-0060).
const MUESTRAS = 120; // 30 s a un marco cada 250 ms
const serieCPU = [];

function numero(texto) {
  const m = /-?\d+(?:[.,]\d+)?/.exec(texto || '');
  return m ? parseFloat(m[0].replace(',', '.')) : NaN;
}

// Anotar y dibujar van por separado: la muestra se anota en cuanto llega,
// también con la página en movimiento, y se dibuja cuando se puede pintar.
function anotarCPU(valor) {
  if (Number.isNaN(valor)) return;
  serieCPU.push(valor);
  if (serieCPU.length > MUESTRAS) serieCPU.shift();
}

function redibujarCPU() {
  if (serieCPU.length === 0) return;
  // El techo nunca baja de 25 %: con un nodo casi ocioso, una escala pegada
  // al máximo convertiría un 3 % en una montaña.
  const tope = Math.max(25, ...serieCPU) * 1.15;
  const paso = 100 / (MUESTRAS - 1);
  const x0 = 100 - (serieCPU.length - 1) * paso; // lo más nuevo, a la derecha
  const puntos = serieCPU.map((v, i) =>
    (x0 + i * paso).toFixed(2) + ',' + (38 - (v / tope) * 34).toFixed(2)).join(' ');
  const area = 'M' + x0.toFixed(2) + ',40 L' + puntos.split(' ').join(' L') + ' L100,40 Z';
  for (const svg of document.querySelectorAll('svg[data-serie="cpu"]')) {
    const linea = svg.querySelector('.sp-l');
    const relleno = svg.querySelector('.sp-a');
    if (linea) linea.setAttribute('points', puntos);
    if (relleno) relleno.setAttribute('d', area);
  }
}

// La escala de la bala va de 30 a 90 °C en un viewBox de 60 de ancho.
function moverBala(valor) {
  if (Number.isNaN(valor)) return;
  const ancho = Math.max(0, Math.min(60, valor - 30)).toFixed(1);
  for (const r of document.querySelectorAll('rect[data-bala="temperatura"]')) {
    r.setAttribute('width', ancho);
  }
}

function aplicar(indice, filas) {
  for (const fila of filas) {
    const destino = indice.get(fila.clave);
    // Una clave que no está en la página es una fila que el servidor conoce y
    // esta página no enseña —todas las de /resumen menos dos—. Se ignora:
    // mejor una fila de menos que un error que deje el resto del marco sin
    // aplicar.
    if (!destino) continue;

    ponerTexto(destino.cifra, fila.valor);
    if (fila.clave === 'temperatura') moverBala(numero(fila.valor));
    if (destino.pintado === null) continue;

    // La clase llega hecha del servidor. Componerla aquí —'p-' + veredicto—
    // pondría la traducción de veredicto a color en DOS sitios, y el día que
    // una cambiara la otra seguiría pintando lo de antes sin fallar a gritos.
    const clase = fila.clase ? 'pastilla p ' + fila.clase : 'pastilla p';
    // Vacía y no un guion: una fila sin umbral no tiene veredicto que enseñar,
    // y el CSS esconde la pastilla en cuanto se queda sin texto. El elemento se
    // mantiene para poder volver a escribir en él sin tocar el DOM.
    const nueva = firma(clase, fila.etiqueta || '', fila.accion || '');
    if (nueva === destino.pintado) {
      destino.candidato = null;
      continue;
    }
    if (nueva !== destino.candidato) {
      destino.candidato = nueva;
      destino.veces = 0;
    }
    if (++destino.veces < MARCOS_PARA_CAMBIAR) continue;
    destino.pintado = nueva;
    destino.candidato = null;
    ponerTexto(destino.accion, fila.accion || '');
    ponerTexto(destino.pastilla, fila.etiqueta || '');
    if (destino.pastilla && destino.pastilla.className !== clase) {
      destino.pastilla.className = clase;
    }
  }
}

function marcarLatido(estado, texto) {
  const l = document.getElementById('latido');
  if (!l) return;
  // Cuatro marcos por segundo: solo se toca lo que cambia.
  const clase = 'p ' + (estado === 'si' ? 'p-ok' : 'p-av');
  if (l.hidden) l.hidden = false;
  if (l.className !== clase) l.className = clase;
  ponerTexto(l, texto);
}

// NADA SE ESCRIBE MIENTRAS LA PÁGINA SE MUEVE (ADR-0107). En el iPhone, cada
// marco que toca la página manda una capa nueva al compositor, y con cuatro
// por segundo el desplazamiento iba a tirones: el dedo soltaba y la página
// seguía «congelada», según el responsable. Mientras el dedo o la inercia la
// mueven, el marco se guarda —solo el último— y se pinta en cuanto lleva
// QUIETUD_MS sin moverse. La muestra de CPU no se pierde: se anota igual.
const QUIETUD_MS = 300;
let movida = -Infinity; // última vez que se vio moverse la página
let altura = 0;         // posición vista en la última comprobación
let dedos = 0;          // dedos apoyados en la pantalla

addEventListener('scroll', function () { movida = performance.now(); }, { passive: true });

// UN DEDO APOYADO YA ES MOVIMIENTO (v1.136.0). El primer aviso de
// desplazamiento llega cuando la página ya se mueve, y un marco pintado justo
// en ese hueco tocaba la página al arrancar el gesto. Los oyentes son pasivos:
// no frenan el desplazamiento.
function tocar(e) {
  dedos = e.touches.length;
  movida = performance.now();
}
for (const tipo of ['touchstart', 'touchend', 'touchcancel']) {
  addEventListener(tipo, tocar, { passive: true });
}

// Se mira también la posición, y no solo el evento: así la inercia cuenta como
// movimiento aunque el navegador espacie los avisos de desplazamiento. Un dedo
// que lleva diez segundos quieto ya no frena nada: si se perdió su «touchend»,
// la página no se queda congelada.
function seMueve() {
  const ahora = performance.now();
  if (window.scrollY !== altura) {
    altura = window.scrollY;
    movida = ahora;
  }
  if (dedos > 0 && ahora - movida < 10000) return true;
  return ahora - movida < QUIETUD_MS;
}

function arrancar() {
  const filas = indexar();
  altura = window.scrollY;
  let pendiente = null; // el marco más reciente sin pintar
  let espera = 0;

  function pintar() {
    const marco = pendiente;
    pendiente = null;
    if (!marco) return;
    marcarLatido('si', 'en línea');
    aplicar(filas, marco.nodo || []);
    aplicar(filas, marco.servicio || []);
    redibujarCPU();
  }

  function pintarCuandoPare() {
    if (espera) return;
    espera = setTimeout(function () {
      espera = 0;
      if (seMueve()) pintarCuandoPare();
      else pintar();
    }, QUIETUD_MS);
  }

  // Las gráficas arrancan con lo que pintó el servidor, sin esperar al primer
  // marco. La de CPU trae su historia en «data-muestras» —los últimos 30 s que
  // guarda el nodo, con la lectura de esta carga al final—: así no vuelve a
  // empezar vacía en cada visita (ADR-0106).
  const temp = filas.get('temperatura');
  if (temp && temp.cifra) moverBala(numero(temp.cifra.textContent));
  const spark = document.querySelector('svg[data-serie="cpu"]');
  if (spark) {
    for (const t of (spark.dataset.muestras || '').split(' ')) {
      const v = parseFloat(t);
      if (!Number.isNaN(v)) serieCPU.push(v);
    }
    serieCPU.splice(0, Math.max(0, serieCPU.length - MUESTRAS));
    redibujarCPU();
  }
  const flujo = new EventSource('/estado/flujo');

  flujo.onopen = function () {
    fallos = 0;
    marcarLatido('si', 'en línea');
  };

  flujo.onmessage = function (e) {
    let marco;
    try {
      marco = JSON.parse(e.data);
    } catch (err) {
      // Un marco ilegible no debe tumbar el flujo: el siguiente llega en un
      // cuarto de segundo y la pantalla se pone al día sola.
      return;
    }
    fallos = 0;
    for (const f of marco.nodo || []) {
      if (f.clave === 'cpu') anotarCPU(numero(f.valor));
    }
    pendiente = marco;
    if (seMueve()) pintarCuandoPare();
    else pintar();
  };

  flujo.onerror = function () {
    // EventSource pasa por aquí también en cada reconexión normal, así que
    // esto NO significa que algo se haya roto: significa que ahora mismo no
    // hay conexión. Lo que se dice es exactamente eso.
    fallos++;
    if (fallos >= FALLOS_PARA_RENDIRSE) {
      flujo.close();
      marcarLatido('no', 'sin conexión — recargue la página');
      return;
    }
    marcarLatido('no', 'reconectando…');
  };
}

arrancar();
