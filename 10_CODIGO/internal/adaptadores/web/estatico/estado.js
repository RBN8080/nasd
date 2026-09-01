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
'use strict';

// Tras este número de fallos SEGUIDOS se deja de insistir.
//
// EventSource reintenta solo y para siempre, que es lo correcto ante un corte
// de red. Pero una pestaña olvidada cuya sesión caducó —siete días, RF-15—
// recibiría un rechazo cada pocos segundos hasta que alguien la cierre. Se
// corta y se dice por qué, en vez de picar al servidor de por vida.
const FALLOS_PARA_RENDIRSE = 10;

let fallos = 0;

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
    claves.set(nodo.dataset.clave, {
      cifra: nodo.querySelector('.cifra'),
      accion: nodo.querySelector('.accion'),
      pastilla: nodo.querySelector('.pastilla'),
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

function aplicar(indice, filas) {
  for (const fila of filas) {
    const destino = indice.get(fila.clave);
    // Una clave que no está en la página es una fila que el servidor conoce y
    // esta página no enseña —todas las de /resumen menos dos—. Se ignora:
    // mejor una fila de menos que un error que deje el resto del marco sin
    // aplicar.
    if (!destino) continue;

    ponerTexto(destino.cifra, fila.valor);
    ponerTexto(destino.accion, fila.accion || '');
    // Vacía y no un guion: una fila sin umbral no tiene veredicto que enseñar,
    // y el CSS esconde la pastilla en cuanto se queda sin texto. El elemento se
    // mantiene para poder volver a escribir en él sin tocar el DOM.
    ponerTexto(destino.pastilla, fila.etiqueta || '');

    // La clase llega hecha del servidor. Componerla aquí —'p-' + veredicto—
    // pondría la traducción de veredicto a color en DOS sitios, y el día que
    // una cambiara la otra seguiría pintando lo de antes sin fallar a gritos.
    const clase = fila.clase ? 'pastilla p ' + fila.clase : 'pastilla p';
    if (destino.pastilla && destino.pastilla.className !== clase) {
      destino.pastilla.className = clase;
    }
  }
}

function marcarLatido(estado, texto) {
  const l = document.getElementById('latido');
  if (!l) return;
  l.hidden = false;
  l.className = 'p ' + (estado === 'si' ? 'p-ok' : 'p-av');
  ponerTexto(l, texto);
}

function arrancar() {
  const filas = indexar();
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
    marcarLatido('si', 'en línea');
    aplicar(filas, marco.nodo || []);
    aplicar(filas, marco.servicio || []);
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
