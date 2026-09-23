
'use strict';


document.addEventListener('pointerdown', function (e) {
  for (const menu of document.querySelectorAll('details.menu[open]')) {
    if (!menu.contains(e.target)) menu.open = false;
  }
});

// Escape cierra el menú abierto sin tener que buscar dónde pulsar. Es la
// convención de los menús de escritorio y no cuesta nada.
document.addEventListener('keydown', function (e) {
  if (e.key !== 'Escape') return;
  for (const menu of document.querySelectorAll('details.menu[open]')) {
    menu.open = false;
  }
});
