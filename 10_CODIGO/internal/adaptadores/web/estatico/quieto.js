// La página quieta: lo común a los tres flujos en vivo (Estado y Resumen,
// Seguridad, Cuentas). Lo carga el marco antes que el archivo de cada módulo.
//
// 1. NADA SE ESCRIBE MIENTRAS ALGO SE MUEVE (nació en estado.js, v1.133.1). En
//    el iPhone, tocar la página a mitad de un desplazamiento lo deja a
//    tirones. Aquí se sabe si algo se mueve —la página o un cuadro con barra
//    propia— y «pintor» guarda el último marco y lo pinta al parar. Antes solo
//    lo tenía Estado; Seguridad escribía cada 1–2 s sin mirar.
//
// 2. ABRIR UN DETALLE NO MUEVE LA PÁGINA. El detalle, el filtro y la capa del
//    mapa viven en la URL (ADR-0076): pulsarlos recarga la página, y volvía al
//    principio. Aquí se anota dónde estaba cada barra antes de salir y se
//    repone al llegar. Sin JavaScript, la página carga arriba como antes.
'use strict';

(function () {
  const QUIETUD_MS = 300;
  const HAY_SCROLLEND = 'onscrollend' in window;
  let movida = -Infinity; // última vez que se vio moverse algo
  let altura = window.scrollY;
  let dedos = 0;          // dedos apoyados en la pantalla
  let enMarcha = false;   // un desplazamiento que aún no ha avisado de su fin
  const alParar = [];

  function avisar() {
    for (const f of alParar) f();
  }

  // En captura: «scroll» no burbujea, y así se oye también el de los cuadros
  // con barra propia (.sc-cuerpo) y el de .cuerpo en el navegador. Pasivos: no
  // frenan el desplazamiento.
  document.addEventListener('scroll', function () {
    enMarcha = true;
    movida = performance.now();
  }, { passive: true, capture: true });
  document.addEventListener('scrollend', function () {
    enMarcha = false;
    if (dedos === 0) avisar();
  }, { passive: true, capture: true });

  // UN DEDO APOYADO YA ES MOVIMIENTO (v1.136.0): el primer aviso de
  // desplazamiento llega cuando la página ya se mueve.
  function tocar(e) {
    dedos = e.touches.length;
    movida = performance.now();
    // Soltar sin haber desplazado nada —un toque— también es parar.
    if (dedos === 0 && !enMarcha) avisar();
  }
  for (const tipo of ['touchstart', 'touchend', 'touchcancel']) {
    addEventListener(tipo, tocar, { passive: true });
  }

  // Se mira también la posición, y no solo el evento: así la inercia cuenta
  // aunque el navegador espacie los avisos. Un dedo que lleva diez segundos
  // quieto ya no frena nada, y un «scrollend» perdido tampoco: tres segundos
  // sin noticias de movimiento cuentan como parada.
  function seMueve() {
    const ahora = performance.now();
    if (window.scrollY !== altura) {
      altura = window.scrollY;
      movida = ahora;
    }
    if (dedos > 0 && ahora - movida < 10000) return true;
    if (HAY_SCROLLEND && enMarcha && ahora - movida < 3000) return true;
    if (HAY_SCROLLEND && !enMarcha) return false;
    return ahora - movida < QUIETUD_MS;
  }

  // pintor(pintar) devuelve la función a la que se le da cada marco. Solo se
  // conserva el último: al parar se pinta ese, no la cola entera.
  function pintor(pintar) {
    let pendiente = null;
    let hay = false;
    let espera = 0;

    function ahora() {
      if (!hay) return;
      const marco = pendiente;
      pendiente = null;
      hay = false;
      pintar(marco);
    }
    function cuandoPare() {
      if (espera) return;
      espera = setTimeout(function () {
        espera = 0;
        if (seMueve()) cuandoPare();
        else ahora();
      }, QUIETUD_MS);
    }
    alParar.push(function () {
      if (espera) {
        clearTimeout(espera);
        espera = 0;
      }
      ahora();
    });
    return function (marco) {
      pendiente = marco;
      hay = true;
      if (seMueve()) cuandoPare();
      else ahora();
    };
  }

  window.nasQuieto = { seMueve: seMueve, pintor: pintor };

  // ── La posición se conserva al recargar la misma página ──────────────────
  const CLAVE = 'nas-posicion';
  const VIGENCIA_MS = 15000;
  const BARRAS = '.cuerpo, .sc-cuerpo';

  function anotar() {
    try {
      const barras = [];
      for (const el of document.querySelectorAll(BARRAS)) barras.push(el.scrollTop);
      sessionStorage.setItem(CLAVE, JSON.stringify({
        ruta: location.pathname, cuando: Date.now(), y: window.scrollY, barras: barras,
      }));
    } catch (e) { /* sin almacén, la página carga arriba como siempre */ }
  }

  function reponer() {
    let p;
    try {
      p = JSON.parse(sessionStorage.getItem(CLAVE) || 'null');
      sessionStorage.removeItem(CLAVE);
    } catch (e) { return; }
    if (!p || p.ruta !== location.pathname || Date.now() - p.cuando > VIGENCIA_MS) return;
    const barras = document.querySelectorAll(BARRAS);
    // Si la página cambió de forma (otro filtro, otro número de cuadros), las
    // barras de dentro ya no son las mismas: se repone solo la de la página.
    if (barras.length === p.barras.length) {
      barras.forEach(function (el, i) { el.scrollTop = p.barras[i]; });
    } else if (barras.length && p.barras.length) {
      barras[0].scrollTop = p.barras[0];
    }
    if (p.y) window.scrollTo(0, p.y);
  }

  // Un enlace que lleva a ESTA misma página con otra consulta: detalle,
  // ventana, capa, cerrar. Y los formularios, que vuelven aquí tras actuar.
  document.addEventListener('click', function (e) {
    const a = e.target.closest ? e.target.closest('a[href]') : null;
    if (!a || a.target || e.defaultPrevented) return;
    let destino;
    try { destino = new URL(a.href, location.href); } catch (err) { return; }
    if (destino.origin === location.origin && destino.pathname === location.pathname) anotar();
  });
  document.addEventListener('submit', anotar);

  reponer();
})();
