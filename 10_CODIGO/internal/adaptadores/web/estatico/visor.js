'use strict';

// Las flechas del teclado repiten lo que ya hacen los dos enlaces de la
// galería. MEJORA PROGRESIVA EN SENTIDO ESTRICTO: no navega por su cuenta, va
// a buscar el «href» que el servidor ya escribió y sigue ese. Si este archivo
// no carga, los botones siguen ahí y no se pierde ninguna función.
//
// Va en su propio bloque, fuera del de compatibilidad, porque tiene que
// funcionar TAMBIÉN cuando no hay medio que mostrar: un HEIC que Brave no
// decodifica es justo el archivo del que uno quiere salir con la flecha.
(function () {
  function irA(selector) {
    const enlace = document.querySelector(selector);
    if (enlace) window.location.assign(enlace.href);
  }

  document.addEventListener('keydown', function (e) {
    // Con una tecla modificadora pulsada esto es un atajo del navegador
    // —Alt+← es «atrás»—, y robárselo sería peor que no estar.
    if (e.altKey || e.ctrlKey || e.metaKey || e.shiftKey) return;
    // CON EL VÍDEO ENFOCADO, LAS FLECHAS SON SUYAS: mueven la reproducción
    // cinco segundos, que es lo que espera cualquiera que acabe de pulsar
    // sobre el reproductor. Robárselas para cambiar de archivo dejaría un
    // vídeo imposible de recorrer. Basta con pulsar fuera para recuperarlas.
    // Los campos de texto van por lo mismo: allí las flechas mueven el cursor.
    const foco = document.activeElement;
    if (foco && (foco.isContentEditable ||
                 /^(INPUT|TEXTAREA|SELECT|VIDEO|AUDIO)$/.test(foco.tagName))) return;

    if (e.key === 'ArrowLeft') irA('.paso-anterior');
    else if (e.key === 'ArrowRight') irA('.paso-siguiente');
    else return;
    e.preventDefault();
  });
})();

(function () {
  const medio = document.querySelector('[data-visor]');
  if (!medio) return;

  let resuelto = false;

  // Quitar el elemento, y no solo esconderlo: es lo que aborta la petición en
  // curso. RF-25 dice «no realizar ninguna otra acción», y seguir trayendo por
  // la red un archivo que ya se ha declarado inabrible es una acción.
  function noCompatible() {
    if (resuelto) return;
    resuelto = true;
    document.getElementById('visor').remove();
    document.getElementById('cabecera-visor').remove();
    document.getElementById('no-compatible').hidden = false;
  }

  switch (medio.dataset.clase) {
    case 'imagen':
      medio.addEventListener('error', noCompatible);
      // El guion carga al final del cuerpo, así que la imagen puede haber
      // fallado ya y su evento «error» haberse perdido. «complete» con ancho
      // natural cero es la forma estándar de leer ese resultado a posteriori.
      if (medio.complete && medio.naturalWidth === 0) noCompatible();
      break;

    case 'video':
    case 'audio':
      // canPlayType devuelve '' cuando el formato no se soporta —el caso de
      // video/quicktime en Chromium—, y ahí no hace falta esperar a nada.
      if (medio.canPlayType(medio.dataset.mime) === '') {
        noCompatible();
        break;
      }
      // Y si el contenedor sí se admite pero el códec de dentro no, quien lo
      // dice es el decodificador al leer los metadatos.
      medio.addEventListener('error', noCompatible);
      break;

    case 'pdf':
      // <object type="application/pdf"> no tiene un evento de error
      // interoperable: sin visor nativo se queda en blanco y calla. Esta
      // propiedad es la respuesta estándar a esa misma pregunta. Si el
      // navegador no la implementa queda «undefined», y entonces no se
      // concluye nada: se deja intentarlo.
      if (navigator.pdfViewerEnabled === false) noCompatible();
      break;
  }
})();
