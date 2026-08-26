// Flujo en vivo del panel de administración — P-7, ADR-0056.
//
// Hermano pequeño de estado.js, y con su MISMA REGLA: este archivo no calcula
// nada. El servidor manda el texto de la pastilla ya escrito y el veredicto ya
// decidido (ver marcoDeCuentas en panel.go); aquí solo se sustituye texto. Por
// eso la página sin JavaScript y la página con él no pueden decir cosas
// distintas: es literalmente el mismo texto, compuesto en el mismo sitio.
//
// QUÉ ARREGLA. La columna «Sesión» solo decía la verdad en el instante de
// cargar la página: quien cerraba sesión seguía figurando como activo hasta que
// alguien recargaba a mano.
//
// ES UNA MEJORA PROGRESIVA, como menus.js. Si este archivo no carga, el panel
// sigue entero —el servidor ya pintó la pastilla correcta— y lo único que se
// pierde es que se actualice sola. Ninguna función del panel depende de él.
'use strict';

// Tras este número de fallos SEGUIDOS se deja de insistir. Mismo motivo y mismo
// número que en estado.js: una pestaña olvidada cuya sesión caducó pediría
// entrada cada pocos segundos para siempre.
const FALLOS_PARA_RENDIRSE = 10;

let fallos = 0;

// El índice se construye UNA vez sobre lo que ya pintó el servidor, igual que
// en estado.js y por los mismos dos motivos: no repetir la búsqueda en cada
// marco, y no construir selectores CSS con texto que llega por la red.
//
// Se ancla en data-usuario y no en el texto de la celda: el nombre de la cuenta
// es dato de usuario, y buscar por él sería justo esa costumbre.
function indexar() {
  const filas = new Map();
  for (const fila of document.querySelectorAll('.fls [data-usuario]')) {
    filas.set(fila.dataset.usuario, {
      pastilla: fila.querySelector('.pastilla'),
    });
  }
  return filas;
}

// Solo se escribe si el texto cambió. La pastilla de una cuenta cambia dos
// veces al día como mucho, así que casi todos los marcos no tocan el DOM.
function ponerTexto(elemento, texto) {
  if (elemento && elemento.textContent !== texto) {
    elemento.textContent = texto;
  }
}

function aplicar(indice, cuentas) {
  for (const fila of cuentas) {
    const destino = indice.get(fila.nombre);
    // Una cuenta que no está en la página es una fila que el servidor conoce y
    // esta carga del HTML no —un alta hecha desde otra pestaña o por terminal—.
    // Se ignora a propósito: P-7 pide la PASTILLA en vivo, no la tabla. La fila
    // nueva aparece al recargar. Mejor eso que inventar filas aquí, que
    // obligaría a este archivo a componer HTML y a saber de columnas.
    if (!destino) continue;

    // Vacía y no un guion: el CSS esconde la pastilla en cuanto se queda sin
    // texto (.pastilla:empty). El elemento se mantiene en el DOM para poder
    // volver a escribir en él sin crear nodos.
    ponerTexto(destino.pastilla, fila.texto || '');

    // La clase llega hecha del servidor — ver filaCuenta.Clase.
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
  l.dataset.vivo = estado;
  ponerTexto(l, texto);
}

function arrancar() {
  const cuentas = indexar();
  // Sin ninguna cuenta dada de alta la tabla no tiene filas que refrescar, y
  // abrir el flujo solo pondría al nodo a muestrear para nadie.
  if (cuentas.size === 0) return;

  const flujo = new EventSource('/administracion/flujo');

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
    aplicar(cuentas, marco.cuentas || []);
  };

  flujo.onerror = function () {
    // EventSource pasa por aquí también en cada reconexión normal, así que esto
    // NO significa que algo se haya roto: significa que ahora mismo no hay
    // conexión. Lo que se dice es exactamente eso.
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
