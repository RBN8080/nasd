// Menús desplegables (<details class="menu">): cómo se cierran.
//
// Abrir y cerrar es HTML puro (ADR-0027): sin este archivo, el botón que abre
// el menú también lo cierra, porque queda por encima del velo (estilo.css).
// Esto añade lo que se espera de un menú:
//
//   - pulsar fuera lo cierra, y ese clic no llega a lo que haya debajo;
//   - Escape lo cierra y devuelve el foco al botón;
//   - una orden que no cambia de página (Descargar) también lo cierra;
//   - en el teléfono, un botón «Cerrar» al pie de la hoja;
//   - abrir uno cierra el otro;
//   - en el navegador, el menú ⋯ de una fila flota junto a su botón.
//
// EL VELO ES PARTE DEL MENÚ. Es el ::before del propio <details>, así que un
// clic sobre él llega con target === menu. Hasta v1.134.0 se cerraba con
// «!menu.contains(target)», que con el velo de la consola 2 nunca se cumplía:
// el menú no se cerraba, el velo tapaba la página y había que recargar.
'use strict';

(function () {
  const ABIERTOS = 'details.menu[open]';

  function cerrar(menu, devolverFoco) {
    if (!menu.open) return;
    menu.open = false;
    const boton = menu.querySelector(':scope > summary');
    if (devolverFoco && boton) boton.focus();
  }

  // EN EL NAVEGADOR, EL MENÚ DE UNA FILA FLOTA (v1.138.0). Las filas de
  // Seguridad viven dentro de cuadros con barra propia, y un menú «absolute»
  // ahí dentro quedaba recortado por el cuadro. Se coloca «fixed» junto a su
  // botón. La posición va por CSSOM (style.left), que la CSP permite: lo que
  // descarta es el atributo style del HTML (ADR-0060). Antes de colocar se
  // mide dónde cae el origen, porque un panel «container-type» puede ser el
  // bloque contenedor de lo «fixed» y entonces el origen no es la ventana.
  const ANCHO = window.matchMedia('(min-width: 880px)');

  function colocar(menu) {
    if (!menu.classList.contains('menu-fila')) return;
    const panel = menu.querySelector(':scope > .menu-panel');
    const boton = menu.querySelector(':scope > summary');
    if (!panel || !boton) return;
    if (!ANCHO.matches) {
      menu.classList.remove('flota');
      panel.style.left = '';
      panel.style.top = '';
      return;
    }
    menu.classList.add('flota');
    panel.style.left = '0px';
    panel.style.top = '0px';
    const origen = panel.getBoundingClientRect();
    const b = boton.getBoundingClientRect();
    let x = b.right - origen.width;
    let y = b.bottom + 4;
    if (y + origen.height > window.innerHeight - 8) y = Math.max(8, b.top - 4 - origen.height);
    x = Math.max(8, Math.min(x, window.innerWidth - origen.width - 8));
    panel.style.left = (x - origen.left) + 'px';
    panel.style.top = (y - origen.top) + 'px';
  }

  function cerrarSalvo(menu) {
    for (const m of document.querySelectorAll(ABIERTOS)) {
      if (m !== menu) cerrar(m, false);
    }
  }

  for (const menu of document.querySelectorAll('details.menu')) {
    // Se escucha en el propio <details> y no solo en document: Safari en iOS
    // solo entrega «click» a lo que lo escucha, y el velo no es un control.
    menu.addEventListener('click', (e) => {
      if (e.target === menu) cerrar(menu, false);
    });
    menu.addEventListener('toggle', () => {
      if (!menu.open) return;
      cerrarSalvo(menu);
      colocar(menu);
    });
    // Descargar responde con un adjunto y la página se queda donde está.
    menu.addEventListener('submit', () => {
      setTimeout(() => cerrar(menu, false), 0);
    });

    const panel = menu.querySelector(':scope > .menu-panel');
    if (panel) {
      const x = document.createElement('button');
      x.type = 'button';
      x.className = 'bt menu-cerrar';
      x.textContent = 'Cerrar';
      x.addEventListener('click', () => cerrar(menu, true));
      panel.append(x);
    }
  }

  // Lo que el velo no tapa: en el teléfono, las pestañas quedan por encima.
  //
  // Con composedPath() y no con contains(): «Pausar» se cambia por «Reanudar»
  // mientras el clic aún sube, y un botón ya fuera del DOM no está dentro de
  // nada. Con contains(), pausar una subida cerraba el panel de «Subir».
  document.addEventListener('click', (e) => {
    const recorrido = e.composedPath();
    for (const m of document.querySelectorAll(ABIERTOS)) {
      if (!recorrido.includes(m)) cerrar(m, false);
    }
  });

  // Un menú que flota no sigue a su fila: si algo se desplaza, se cierra. El
  // desplazamiento de dentro del propio menú no cuenta.
  document.addEventListener('scroll', (e) => {
    for (const m of document.querySelectorAll('details.menu.flota[open]')) {
      if (!(e.target instanceof Node) || !m.contains(e.target)) cerrar(m, false);
    }
  }, { passive: true, capture: true });

  document.addEventListener('keydown', (e) => {
    if (e.key !== 'Escape') return;
    for (const m of document.querySelectorAll(ABIERTOS)) cerrar(m, true);
  });

  // «Atrás» puede devolver la página tal como quedó, con el menú abierto.
  window.addEventListener('pageshow', (e) => {
    if (e.persisted) cerrarSalvo(null);
  });
})();
