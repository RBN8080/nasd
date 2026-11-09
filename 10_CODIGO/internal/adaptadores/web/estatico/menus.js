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
//   - abrir uno cierra el otro.
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
      if (menu.open) cerrarSalvo(menu);
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

  document.addEventListener('keydown', (e) => {
    if (e.key !== 'Escape') return;
    for (const m of document.querySelectorAll(ABIERTOS)) cerrar(m, true);
  });

  // «Atrás» puede devolver la página tal como quedó, con el menú abierto.
  window.addEventListener('pageshow', (e) => {
    if (e.persisted) cerrarSalvo(null);
  });
})();
