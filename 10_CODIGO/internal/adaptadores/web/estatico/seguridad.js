// Flujo en vivo del panel de seguridad — /seguridad/flujo.
//
// MEJORA PROGRESIVA ESTRICTA, como estado.js y cuentas.js: sin este archivo la
// página funciona entera y solo deja de refrescarse sola. Nada de lo que se ve
// depende de que esto llegue a ejecutarse.
//
// EventSource y nada más: sin dependencias, sin CDN, sin marco. Reconecta solo
// ante un corte, que es el comportamiento correcto para un panel que se queda
// abierto.
//
// LO QUE REFRESCA Y LO QUE NO. Refresca las cinco cifras de «Volumen
// observado» y los segmentos de la cinta. NO toca las tablas: mover filas bajo
// el cursor mientras alguien va a pulsar «Soltar» es peor que no moverlas, y
// esa decisión ya se tomó en /administracion. De la contención solo se compara
// el RECUENTO, para descubrir un aviso y dejar que sea la persona quien
// recargue.
//
// # EL PILOTO SUSTITUYE A «#latido», Y AFIRMA MÁS QUE ÉL
//
// Aquella pastilla decía «en línea» y nada más, y NACÍA OCULTA: sin JavaScript
// la página era una foto fija y no lo decía en ninguna parte. El piloto nace
// diciendo de cuándo es la foto —lo escribe el servidor— y, con el flujo vivo,
// lleva la HORA DEL ÚLTIMO MARCO avanzando de segundo en segundo.
//
// Eso cierra un agujero que la pastilla tenía abierto: un EventSource puede
// quedarse en una conexión medio abierta sin disparar «onerror» nunca, y
// entonces el rótulo seguiría diciendo «en línea» encima de datos congelados —
// exactamente el modo de fallo que ADR-0083 y 00_RECTOR.md §12.5 persiguen.
// Aquí hay un vigilante que no espera a que el navegador avise: si pasan más
// de PLAZO_SIN_DATOS sin un marco, el reloj SE PARA y el piloto vira a ámbar.
(() => {
  'use strict';

  // A los diez fallos seguidos se deja de intentar y se dice. Sin este tope,
  // una sesión caducada dejaría la pestaña reintentando en silencio para
  // siempre y el rótulo diría «reconectando…» sin que nunca vaya a reconectar.
  const FALLOS_PARA_RENDIRSE = 10;

  // El marco llega cada 2 s (intervaloSeguridadVivo). Cinco segundos son dos
  // marcos y medio perdidos: lo bastante para no encender el ámbar por un hipo
  // de la red, y lo bastante poco para que un flujo muerto se note mientras se
  // mira la pantalla.
  const PLAZO_SIN_DATOS = 5000;

  // EL FORMATO DE LA HORA LO FIJA GO, no este archivo (ADR-0017). Esto solo
  // reconoce el reloj que Go ya escribió para poder hacerlo AVANZAR entre
  // marcos; si algún día allí se cambiara el formato, este patrón dejaría de
  // reconocerlo y el piloto se limitaría a enseñar lo que llegue, quieto. Es
  // la forma segura de equivocarse: pierde el segundero, nunca inventa una
  // hora.
  const RELOJ = /^(\d{2}):(\d{2}):(\d{2})$/;

  let fallos = 0;
  let vivo = false;
  let ultimoMarco = 0;
  let reloj = null;   // [h, m, s] del último marco, o null si no se pudo leer
  let horaCruda = ''; // lo que mandó Go, tal cual, por si no se pudo leer

  function dd(n) {
    return String(n).padStart(2, '0');
  }

  function horaTexto() {
    if (!reloj) return horaCruda;
    return dd(reloj[0]) + ':' + dd(reloj[1]) + ':' + dd(reloj[2]);
  }

  // Avanzar un segundo es aritmética, no formato: los campos y el separador
  // siguen siendo los que Go eligió.
  function avanzarReloj() {
    if (!reloj) return;
    reloj[2]++;
    if (reloj[2] > 59) { reloj[2] = 0; reloj[1]++; }
    if (reloj[1] > 59) { reloj[1] = 0; reloj[0]++; }
    if (reloj[0] > 23) { reloj[0] = 0; }
  }

  function sincronizarReloj(hora) {
    horaCruda = hora || '';
    const m = RELOJ.exec(horaCruda);
    reloj = m ? [Number(m[1]), Number(m[2]), Number(m[3])] : null;
  }

  // El índice se construye UNA vez y no por marco: los nodos no cambian, solo
  // su texto. Recorrer el DOM cada dos segundos para encontrar lo mismo sería
  // trabajo tirado. Cada clave guarda VARIOS destinos porque la cinta lleva su
  // texto dos veces —el original y la copia que cierra el bucle sin hueco— y
  // las dos tienen que decir lo mismo o se vería un salto a mitad del ciclo.
  function indexar(atributo, dentro) {
    const m = new Map();
    for (const caja of document.querySelectorAll('[' + atributo + ']')) {
      const n = caja.querySelector(dentro);
      if (!n) continue;
      const clave = caja.getAttribute(atributo);
      if (!m.has(clave)) m.set(clave, []);
      m.get(clave).push({ caja: caja, valor: n });
    }
    return m;
  }

  // Escribir solo si cambió. El navegador no repinta lo que no se toca, y así
  // una selección de texto sobre una cifra quieta no se pierde en cada marco.
  function ponerTexto(el, texto) {
    if (el && el.textContent !== texto) el.textContent = texto;
  }

  function aplicarCifras(indice, cifras) {
    for (const c of cifras) {
      // Una clave que no está en la página se ignora sin ruido: es lo que pasa
      // durante un despliegue, con el navegador enseñando la página vieja y el
      // servidor mandando marcos nuevos.
      const donde = indice.get(c.clave);
      if (!donde) continue;
      for (const d of donde) ponerTexto(d.valor, c.valor);
    }
  }

  // LOS SEGMENTOS SE ESCRIBEN, NO SE CONSTRUYEN. Llega el texto ya compuesto
  // por Go y esto solo lo copia a su ancla: ni una decisión de redacción vive
  // en este archivo (ADR-0017). La clase sí cambia —la contención se pinta en
  // ámbar solo cuando hay algo que contener— y por eso viaja con el valor.
  function aplicarCinta(indice, segmentos) {
    for (const sg of segmentos) {
      const donde = indice.get(sg.clave);
      if (!donde) continue;
      const clase = ('sg ' + (sg.clase || '')).trim();
      for (const d of donde) {
        ponerTexto(d.valor, sg.valor);
        if (d.caja.className !== clase) d.caja.className = clase;
      }
    }
  }

  // La duración de la animación también viaja como clase, nunca como estilo:
  // con «style-src 'self'» un estilo en línea se descarta en silencio.
  function ponerVelocidad(clase) {
    if (!clase) return;
    for (const pista of document.querySelectorAll('.marq-pista')) {
      if (pista.classList.contains(clase)) continue;
      pista.classList.remove('v1', 'v2', 'v3', 'v4', 'v5', 'v6');
      pista.classList.add(clase);
    }
  }

  // SI EL TEXTO YA CABE, QUE NO SE MUEVA. Go elige el cubo de velocidad por
  // número de caracteres y puede errar por exceso en una pantalla ancha; esto
  // lo mide de verdad. Se compara contra la PRIMERA copia y no contra la pista
  // entera, que mide el doble: si se midiera la pista, esconder la copia
  // volvería a hacerla caber y la clase oscilaría en cada pasada.
  function ajustarMovimiento() {
    for (const caja of document.querySelectorAll('.marq-caja')) {
      const pista = caja.querySelector('.marq-pista');
      const tira = pista && pista.querySelector('.tira');
      if (!tira) continue;
      pista.classList.toggle('marq-quieta', tira.scrollWidth <= caja.clientWidth);
    }
  }

  function marcarPiloto(estado, texto) {
    const p = document.getElementById('piloto');
    if (!p) return;
    const clase = 'pil pil-' + estado;
    if (p.className !== clase) p.className = clase;
    ponerTexto(p.querySelector('.pil-txt'), texto);
    ponerTexto(p.querySelector('.pil-hr'), horaTexto());
  }

  // El aviso solo aparece, nunca desaparece: si la contención cambió y volvió a
  // su sitio mientras se miraba, lo que se está viendo sigue sin ser lo que hay.
  function vigilarContencion(caja, aviso, cuantas) {
    if (!caja || !aviso || aviso.hidden === false) return;
    if (Number(caja.dataset.contencion) !== cuantas) aviso.hidden = false;
  }

  function arrancar() {
    const cifras = indexar('data-cifra', '.cifra');
    const cinta = indexar('data-seg', '.vl');
    // Sin nada que refrescar no se abre el flujo: poner al nodo a muestrear
    // para nadie es gasto puro. Mismo cortocircuito que cuentas.js.
    if (cifras.size === 0 && cinta.size === 0) return;

    const caja = document.querySelector('.marq');
    const aviso = document.getElementById('contencion-nueva');
    ajustarMovimiento();
    window.addEventListener('resize', ajustarMovimiento);

    // EL FLUJO LLEVA LA MISMA CONSULTA QUE LA PÁGINA, y esa es la pieza que lo
    // hace honesto: las cifras de este panel dependen del filtro —origen,
    // ventana y motivo—, así que un flujo sin filtro estaría refrescando las
    // cifras de otra vista encima de las que se están leyendo.
    const flujo = new EventSource('/seguridad/flujo' + location.search);

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
      vivo = true;
      ultimoMarco = Date.now();
      sincronizarReloj(marco.hora);
      marcarPiloto('vivo', 'en vivo');
      aplicarCifras(cifras, marco.cifras || []);
      aplicarCinta(cinta, marco.cinta || []);
      ponerVelocidad(marco.velocidad);
      ajustarMovimiento();
      vigilarContencion(caja, aviso, marco.contencion || 0);
    };

    flujo.onerror = () => {
      fallos++;
      vivo = false;
      if (fallos >= FALLOS_PARA_RENDIRSE) {
        flujo.close();
        marcarPiloto('corte', 'sin conexión — recargue');
        return;
      }
      marcarPiloto('aviso', 'reconectando…');
    };

    // EL RELOJ CORRE AL SEGUNDO AUNQUE EL DATO LLEGUE CADA DOS.
    //
    // La cadencia del flujo NO se toca: está razonada en vivo_seguridad.go
    // —relee dos anillos y el archivo del sensor, 100 KB de disco, en un A53—.
    // Lo que corre al segundo es el reloj, y solo MIENTRAS el flujo lo
    // confirme: cada marco lo resincroniza con la hora del nodo, y en cuanto
    // se pasa el plazo sin noticias el reloj se queda quieto y el piloto vira
    // a ámbar. Un reloj detenido es lo que delata un panel congelado, y a un
    // segundo de resolución se nota al instante.
    setInterval(() => {
      if (!vivo) return;
      if (Date.now() - ultimoMarco > PLAZO_SIN_DATOS) {
        vivo = false;
        marcarPiloto('aviso', 'sin datos desde');
        return;
      }
      avanzarReloj();
      marcarPiloto('vivo', 'en vivo');
    }, 1000);
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
