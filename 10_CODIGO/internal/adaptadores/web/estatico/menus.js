// Cerrar los menús al pulsar fuera.
//
// POR QUÉ EXISTE ESTE ARCHIVO, HABIENDO DICHO QUE LOS MENÚS SON HTML PURO.
//
// Los menús siguen siendo <details>/<summary> y siguen funcionando sin esto:
// se abren y se cierran pulsando su propio botón, que es el comportamiento
// nativo del elemento y el que ADR-0027 necesita para que un navegador sin
// JavaScript pueda crear carpetas y usar el formulario de subida de respaldo.
// Lo que HTML no ofrece —y no hay forma de conseguir en CSS sin inventar un
// artefacto de capas invisibles— es cerrarlos al pulsar en otro sitio, que es
// lo que hace cualquier menú de Drive, OneDrive o Dropbox y lo que el
// responsable reportó como molesto el 05/08/2026.
//
// Es una MEJORA PROGRESIVA en el sentido estricto: si este archivo no carga,
// no se pierde ninguna función, solo la comodidad. Por eso vive aparte de
// subida.js, que sí es una función.
'use strict';

// «pointerdown» y no «click»: cubre ratón y dedo con un solo evento, y salta
// al APOYAR y no al soltar, que es como se comportan los menús nativos.
//
// Se dispara ANTES que el «click» que abre o cierra el <summary>, y eso es lo
// que conserva intacto el comportamiento del botón: al pulsar el propio
// summary de un menú abierto, este manejador lo deja como está —el objetivo
// está dentro— y a continuación el click nativo lo cierra. Sin la
// comprobación de pertenencia se cerraría dos veces y volvería a abrirse.
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
