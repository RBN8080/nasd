// Flujo en vivo del panel de seguridad — /seguridad/flujo.
//
// MEJORA PROGRESIVA ESTRICTA, como estado.js y cuentas.js: sin este archivo la
// página funciona entera y solo deja de refrescarse sola. Nada de lo que se ve
// depende de que esto llegue a ejecutarse.
//
// EventSource y nada más: sin dependencias, sin CDN, sin marco. Reconecta solo
// y para siempre ante un corte, que es el comportamiento correcto para un panel
// que se queda abierto.
//
// LO QUE REFRESCA Y LO QUE NO. Refresca las cinco cifras de «Volumen
// observado» y el rótulo de conexión. NO toca las tablas: mover filas bajo el
// cursor mientras alguien va a pulsar «Soltar» es peor que no moverlas, y esa
// decisión ya se tomó en /administracion. De la contención solo se compara el
// RECUENTO, para descubrir un aviso y dejar que sea la persona quien recargue.
(() => {
  'use strict';

  // A los diez fallos seguidos se deja de intentar y se dice. Sin este tope,
  // una sesión caducada dejaría la pestaña reintentando en silencio para
  // siempre y el rótulo diría «reconectando…» sin que nunca vaya a reconectar.
  const FALLOS_PARA_RENDIRSE = 10;
  let fallos = 0;

  // El índice se construye UNA vez y no por marco: los nodos no cambian, solo
  // su texto. Recorrer el DOM cuatro veces por minuto para encontrar lo mismo
  // sería trabajo tirado.
  function indexar() {
    const m = new Map();
    for (const caja of document.querySelectorAll('[data-cifra]')) {
      const n = caja.querySelector('.cifra');
      if (n) m.set(caja.dataset.cifra, n);
    }
    return m;
  }

  // Escribir solo si cambió. El navegador no repinta lo que no se toca, y así
  // una selección de texto sobre una cifra quieta no se pierde en cada marco.
  function ponerTexto(el, texto) {
    if (el && el.textContent !== texto) el.textContent = texto;
  }

  function aplicar(indice, cifras) {
    for (const c of cifras) {
      // Una clave que no está en la página se ignora sin ruido: es lo que pasa
      // durante un despliegue, con el navegador enseñando la página vieja y el
      // servidor mandando marcos nuevos.
      ponerTexto(indice.get(c.clave), c.valor);
    }
  }

  function marcarLatido(estado, texto) {
    const l = document.getElementById('latido');
    if (!l) return;
    l.hidden = false;
    l.className = 'p ' + (estado === 'si' ? 'p-ok' : 'p-av');
    ponerTexto(l, texto);
  }

  // El aviso solo aparece, nunca desaparece: si la contención cambió y volvió a
  // su sitio mientras se miraba, lo que se está viendo sigue sin ser lo que hay.
  function vigilarContencion(aviso, cuantas) {
    if (!aviso || aviso.hidden === false) return;
    if (Number(aviso.dataset.contencion) !== cuantas) aviso.hidden = false;
  }

  function arrancar() {
    const cifras = indexar();
    // Sin cuadros que refrescar no se abre el flujo: poner al nodo a muestrear
    // para nadie es gasto puro. Mismo cortocircuito que cuentas.js.
    if (cifras.size === 0) return;
    const aviso = document.getElementById('contencion-nueva');

    // EL FLUJO LLEVA LA MISMA CONSULTA QUE LA PÁGINA, y esa es la pieza que lo
    // hace honesto: las cifras de este panel dependen del filtro —origen,
    // ventana y motivo—, así que un flujo sin filtro estaría refrescando las
    // cifras de otra vista encima de las que se están leyendo.
    const flujo = new EventSource('/seguridad/flujo' + location.search);

    flujo.onopen = () => {
      fallos = 0;
      marcarLatido('si', 'en línea');
    };

    flujo.onmessage = (e) => {
      let marco;
      try {
        marco = JSON.parse(e.data);
      } catch {
        // Un marco ilegible se ignora: el siguiente llega en dos segundos y
        // trae el estado entero, así que no hay nada que reconstruir.
        return;
      }
      fallos = 0;
      marcarLatido('si', 'en línea');
      aplicar(cifras, marco.cifras || []);
      vigilarContencion(aviso, marco.contencion || 0);
    };

    flujo.onerror = () => {
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
})();

// El lector del mapa de «Procedencia» — ADR-0089.
//
// MEJORA PROGRESIVA ESTRICTA, como el flujo de arriba: sin este bloque no
// falta ni un dato. Cada trazo del mapa lleva su <title>, que es lo que el
// navegador enseña al posar el cursor, y la lista de al lado dice lo mismo
// escrito. Lo que añade esto es que la información aparezca SIN posar —con el
// dedo y con el tabulador—, que es justo lo que un <title> no sabe hacer: en
// un teléfono no se enseña nunca.
//
// NO RECALCULA NADA. La capa que se pinta la decide el servidor y viaja en la
// URL (?capa=), así que aquí no hay cifras que derivar: cada elemento trae ya
// escrita su línea en «data-lectura» y esto solo la copia al sitio donde se
// lee. Es la misma disciplina que ADR-0017 aplica al resto de la página —
// quien decide cómo se escribe un dato es Go, no el navegador.
(() => {
  'use strict';

  const lector = document.getElementById('lector-mapa');
  const seccion = document.getElementById('procedencia');
  // Sin mapa en la página no hay nada que enganchar. Pasa siempre que no hay
  // base de operadores instalada, que es un estado normal y no un fallo.
  if (!lector || !seccion) return;

  const reposo = lector.dataset.reposo || '';

  // El realce se mueve de un elemento a otro, nunca se acumula: el país que
  // se está leyendo es uno.
  let marcado = null;
  function marcar(iso) {
    if (marcado === iso) return;
    marcado = iso;
    for (const el of seccion.querySelectorAll('[data-iso]')) {
      el.classList.toggle('act', iso !== null && el.dataset.iso === iso);
    }
  }

  function leer(el) {
    if (!el) return;
    const texto = el.dataset.lectura;
    if (!texto) return;
    lector.textContent = texto;
    marcar(el.dataset.iso);
  }

  function soltar() {
    lector.textContent = reposo;
    marcar(null);
  }

  // Delegación en la sección entera: el mapa y la lista comparten el mismo
  // «data-iso», así que posar sobre China o sobre su fila hacen lo mismo — y
  // se realzan las dos a la vez, que es lo que las relaciona sin una leyenda
  // que lo explique.
  seccion.addEventListener('pointerover', (e) => {
    const el = e.target.closest('[data-lectura]');
    if (el) leer(el);
  });
  // focusin y no focus: focus no burbujea, y sin esto el tabulador movería el
  // realce sin escribir la línea.
  seccion.addEventListener('focusin', (e) => {
    const el = e.target.closest('[data-lectura]');
    if (el) leer(el);
  });
  seccion.addEventListener('pointerleave', soltar);

  // En un teléfono no hay «salir» de un elemento: se toca otro o se toca
  // fuera. Sin esto, la línea se quedaría con el último país tocado para
  // siempre, diciendo algo que ya no se está mirando.
  document.addEventListener('pointerdown', (e) => {
    if (!seccion.contains(e.target)) soltar();
  });
})();
