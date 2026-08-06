// Decidir si ESTE navegador puede con ESTE archivo — RF-25.
//
// NUNCA POR «User-Agent», Y ESA ES LA REGLA ENTERA DE ESTE ARCHIVO.
//
// La cadena de agente dice qué navegador dice ser, no qué códecs trae: cambia
// con la versión, con el sistema y con la configuración del usuario, y en
// Brave se falsea a propósito. Aquí se pregunta a las APIs que responden por
// la capacidad REAL —canPlayType, navigator.pdfViewerEnabled— y al propio
// decodificador, por su evento de error. Es la diferencia entre saber y
// suponer, y es lo que hace que un HEIC del iPhone se abra en Safari y caiga
// al mensaje en Brave sin que el servidor tenga que conocer a ninguno de los
// dos (WHATWG HTML §4.8.11 y §8.9).
'use strict';

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
