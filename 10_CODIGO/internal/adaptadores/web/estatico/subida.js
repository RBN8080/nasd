// Cliente del núcleo del protocolo tus — ADR-0027.
//
// Escrito a mano e incrustado en el binario: ADR-0017 prohíbe CDN y
// dependencias externas, y "sin framework" nunca fue "sin JavaScript".
// Trocear, seguir el progreso y reanudar no se hacen con un formulario.
//
// «SUBIDO» LO DICE EL SERVIDOR, NO EL BUCLE (v1.135.0). Un archivo vacío no
// mandaba ni un PATCH: el bucle terminaba al instante, la página decía
// «completado» y la subida se quedaba a medias en el nodo. Ahora solo cuenta
// lo que el servidor confirma con Nas-Completada; si todos los bytes están
// allí sin publicar, un PATCH vacío la publica.
'use strict';

// Tamaño de bloque [R]. Con el techo de ~31 MB/s del bus (RES-02), 8 MB es
// ~0.3 s de transferencia: bastante grande para no penalizar por latencia y
// bastante pequeño para que reanudar no pierda mucho trabajo.
const BLOQUE = 8 * 1024 * 1024;

// Cortes seguidos que se aguantan sin avanzar antes de rendirse, esperando
// 1, 2, 4, 8 y 15 s: medio minuto cubre un salto de wifi del teléfono y no
// deja la página reintentando para siempre.
const REINTENTOS = 5;

// El aviso de durabilidad se retiró de la interfaz por D-17 (ADR-0030).
// RN-01 y RN-02 siguen vigentes: este producto no es un respaldo, y nada aquí
// puede ofrecer, sugerir ni automatizar el borrado del origen tras subir.

// Subidas a medias, recordadas entre visitas — RF-12.
//
// EL DEFECTO QUE ESTO CORRIGE (encontrado en uso real el 2026-07-31):
// la URL de la subida vivía en una variable local. Al cerrar la pestaña se
// perdía, y el siguiente intento creaba una subida NUEVA que empezaba de
// cero, dejando además el parcial anterior huérfano en el servidor.
//
// Desde otro navegador no hay recuerdo: para eso está «Reintentar» en la
// lista de subidas a medias, que pide el archivo y sigue donde iba.
const CLAVE_SUBIDAS = 'nas.subidas';

// Testigo CSRF de la sesión, que la página deja en el DOM: en la zona de
// subida o en la lista de subidas a medias.
//
// Va en una cabecera propia y no en el cuerpo: un formulario de otro sitio
// no puede fijar cabeceras, así que exigirla es en sí misma una barrera.
function csrf() {
  const z = document.querySelector('[data-csrf]');
  return (z && z.dataset.csrf) || '';
}

function cabeceras(extra) {
  return Object.assign({ 'Tus-Resumable': '1.0.0', 'Nas-Csrf': csrf() }, extra);
}

function claveDe(archivo, destino) {
  return destino + '|' + archivo.name + '|' + archivo.size;
}

function subidasRecordadas() {
  try { return JSON.parse(localStorage.getItem(CLAVE_SUBIDAS) || '{}'); }
  catch (e) { return {}; }
}

function recordar(clave, url) {
  try {
    const m = subidasRecordadas();
    m[clave] = url;
    localStorage.setItem(CLAVE_SUBIDAS, JSON.stringify(m));
  } catch (e) { /* modo privado: se sigue sin recordar */ }
}

function olvidar(clave) {
  try {
    const m = subidasRecordadas();
    delete m[clave];
    localStorage.setItem(CLAVE_SUBIDAS, JSON.stringify(m));
  } catch (e) { /* ignorado */ }
}

function porcentaje(hecho, total) {
  return (total > 0 ? Math.floor((hecho / total) * 100) : 100) + ' %';
}

// Espera que se corta si el usuario pausa o descarta mientras tanto.
function esperar(ms, senal) {
  if (senal.aborted) return Promise.reject(new Error('detenida'));
  return new Promise((listo, mal) => {
    const t = setTimeout(listo, ms);
    senal.addEventListener('abort', () => { clearTimeout(t); mal(new Error('detenida')); }, { once: true });
  });
}

// fetch con el corte de red dicho en castellano: cada navegador lo dice a su
// manera, y en inglés («Failed to fetch», «Load failed»).
async function pedir(url, opciones) {
  try {
    return await fetch(url, opciones);
  } catch (err) {
    if (err.name === 'AbortError') throw err;
    throw new Error('sin conexión con el nodo');
  }
}

// Un 404 con cuerpo vacío es la puerta opaca (ADR-0088): la sesión se cerró.
// Con «no existe», es la subida la que ya no está.
const SIN_SESION = 'la sesión se cerró: recargue la página y vuelva a intentarlo';
const NO_ESTA = 'ya no está en el servidor';

async function motivoDe(r) {
  let texto = '';
  try { texto = (await r.text()).trim(); } catch (e) { /* sin cuerpo */ }
  if (r.status === 404) return texto ? NO_ESTA : SIN_SESION;
  return texto && texto.length <= 160 ? texto : 'el servidor respondió ' + r.status;
}

// Desplazamiento según el servidor; null si la subida no está. HEAD no trae
// cuerpo, así que aquí no se distingue una sesión cerrada.
async function consultar(url) {
  const r = await pedir(url, { method: 'HEAD', headers: cabeceras(), cache: 'no-store' });
  if (r.status === 404) return null;
  if (!r.ok) throw new Error('el servidor respondió ' + r.status);
  return parseInt(r.headers.get('Upload-Offset') || '0', 10);
}

async function descartar(url) {
  const r = await pedir(url, { method: 'DELETE', headers: cabeceras() });
  if (r.ok) return;
  const motivo = await motivoDe(r);
  if (motivo !== NO_ESTA) throw new Error(motivo); // si ya no está, ya está descartada
}

// enviar manda desde «offset» hasta el final y vuelve cuando el servidor
// PUBLICA. Con offset === tamaño el PATCH va vacío: es lo que publica una
// subida que ya tiene todos sus bytes. Lanza si no lo consigue.
//
// Nunca se envía desde un desplazamiento propio tras un fallo: se pregunta
// al servidor, que es la fuente de verdad (ADR-0027).
async function enviar(url, archivo, offset, alAvanzar, control) {
  let fallos = 0;
  let preguntar = false;
  for (;;) {
    let r = null;
    let motivo = 'sin conexión con el nodo';
    if (preguntar) {
      try {
        const o = await consultar(url);
        if (o === null) throw new Error(NO_ESTA + ' (si la sesión se cerró, recargue la página)');
        offset = o;
        preguntar = false;
      } catch (err) {
        if (control.motivo || err.message.startsWith(NO_ESTA)) throw err;
      }
    }
    if (!preguntar) {
      try {
        r = await fetch(url, {
          method: 'PATCH',
          headers: cabeceras({
            'Upload-Offset': String(offset),
            'Content-Type': 'application/offset+octet-stream',
          }),
          body: archivo.slice(offset, Math.min(offset + BLOQUE, archivo.size)),
          signal: control.abortar.signal,
        });
      } catch (err) {
        // Pausa o descarte: no es un corte de red, no se reintenta.
        if (control.motivo) throw err;
      }
    }
    if (r && r.ok) {
      const nuevo = parseInt(r.headers.get('Upload-Offset') || '0', 10);
      alAvanzar(nuevo);
      if (r.headers.get('Nas-Completada') === '1') return;
      if (nuevo > offset) {
        offset = nuevo;
        fallos = 0;
        continue;
      }
      motivo = 'el servidor no avanzó';
    } else if (r) {
      motivo = await motivoDe(r);
      // 409 es también RF-23: el destino apareció mientras se subía (por
      // SMB). Eso, como un 4xx, no se arregla reintentando.
      const reintentable = r.status >= 500 || (r.status === 409 && !motivo.includes('ya existe'));
      if (!reintentable) throw new Error(motivo);
    }
    if (++fallos > REINTENTOS) throw new Error(motivo);
    await esperar(Math.min(1000 * 2 ** (fallos - 1), 15000), control.abortar.signal);
    preguntar = true;
  }
}

// Sube un archivo entero. Si este navegador ya lo había empezado, sigue donde
// iba: lo dice el servidor, porque el desplazamiento es el tamaño del parcial.
async function subirArchivo(archivo, destino, alAvanzar, control) {
  const clave = claveDe(archivo, destino);
  let url = subidasRecordadas()[clave] || null;
  let offset = 0;
  if (url) {
    const o = await consultar(url);
    if (o === null) {
      // El servidor ya no la conoce (expiró o se descartó).
      olvidar(clave);
      url = null;
    } else {
      offset = o;
      alAvanzar(offset);
    }
  }

  if (!url) {
    const r = await pedir('/subidas', {
      method: 'POST',
      headers: cabeceras({
        'Upload-Length': String(archivo.size),
        // AMBAS van codificadas, y no es opcional: las cabeceras HTTP solo
        // admiten Latin-1, así que un acento en la ruta o en el nombre hace
        // que fetch() lance «String contains non ISO-8859-1 code point» y la
        // subida ni siquiera se intente. El servidor decodifica ambas
        // (ver tus.go).
        'Nas-Destino': encodeURIComponent(destino),
        'Nas-Nombre': encodeURIComponent(archivo.name),
      }),
    });
    if (r.status === 409) throw new Error('ya existe un archivo con ese nombre; no se sobrescribe');
    if (!r.ok) throw new Error(await motivoDe(r));
    // Un archivo vacío se publica al crearse (tus.go).
    if (r.headers.get('Nas-Completada') === '1') {
      alAvanzar(archivo.size);
      return;
    }
    url = r.headers.get('Location');
    recordar(clave, url);
  }

  control.url = url;
  await enviar(url, archivo, offset, alAvanzar, control);
  olvidar(clave);
}

// ── El panel «Subir» de Archivos ──────────────────────────────────────────

let enLote = false;

function fila(nombre) {
  const li = document.createElement('li');
  const n = document.createElement('span');
  const estado = document.createElement('span');
  const acciones = document.createElement('span');
  n.textContent = nombre;
  estado.textContent = 'en cola';
  li.dataset.estado = 'cola';
  li.append(n, estado, acciones);
  document.getElementById('progreso').append(li);
  return { li, estado, acciones };
}

function botones(donde, lista) {
  donde.replaceChildren(...lista.map(([texto, alPulsar, clase]) => {
    const b = document.createElement('button');
    b.type = 'button';
    b.textContent = texto;
    if (clase) b.className = clase;
    b.addEventListener('click', alPulsar);
    return b;
  }));
}

// Cuando no queda nada subiendo ni por resolver, se recarga para ver lo
// subido. Si algo falló o está en pausa, la página se queda con sus botones.
function recargarSiTermino() {
  const filas = Array.from(document.querySelectorAll('#progreso li'));
  const abiertas = filas.some((li) => ['cola', 'subiendo', 'fallo', 'pausa'].includes(li.dataset.estado));
  if (!abiertas && filas.some((li) => li.dataset.estado === 'subido')) location.reload();
}

// PAUSAR y DESCARTAR son cosas distintas y se ofrecen por separado:
//   - Pausar deja el parcial en el servidor: se reanuda desde donde iba.
//   - Descartar lo destruye ahora, en lugar de dejar basura hasta que
//     expire dentro de días (ADR-0029).
async function subirUno(archivo, destino, f) {
  const control = { abortar: new AbortController(), motivo: null, url: null };
  f.li.dataset.estado = 'subiendo';
  f.estado.textContent = '0 %';
  botones(f.acciones, [
    ['Pausar', () => { control.motivo = 'pausa'; control.abortar.abort(); }],
    ['Descartar', () => { control.motivo = 'descartar'; control.abortar.abort(); }, 'pel'],
  ]);
  try {
    await subirArchivo(archivo, destino, (hecho) => { f.estado.textContent = porcentaje(hecho, archivo.size); }, control);
    f.li.dataset.estado = 'subido';
    f.estado.textContent = 'subido';
    f.acciones.replaceChildren();
  } catch (err) {
    if (control.motivo === 'descartar') {
      await descartarFila(archivo, destino, f, control.url);
      return;
    }
    const pausa = control.motivo === 'pausa';
    f.li.dataset.estado = pausa ? 'pausa' : 'fallo';
    f.estado.textContent = pausa ? 'en pausa' : 'error: ' + err.message;
    botones(f.acciones, [
      [pausa ? 'Reanudar' : 'Reintentar', () => subirUno(archivo, destino, f).then(recargarSiTermino)],
      ['Descartar', () => descartarFila(archivo, destino, f, control.url).then(recargarSiTermino), 'pel'],
    ]);
  }
}

// Descarta en el servidor lo que llegó a crearse y lo olvida aquí.
async function descartarFila(archivo, destino, f, url) {
  const clave = claveDe(archivo, destino);
  url = url || subidasRecordadas()[clave];
  f.acciones.replaceChildren();
  f.estado.textContent = 'descartando…';
  try {
    if (url) await descartar(url);
    olvidar(clave);
    f.li.dataset.estado = 'descartada';
    f.estado.textContent = 'descartada';
  } catch (err) {
    f.li.dataset.estado = 'fallo';
    f.estado.textContent = 'no se pudo descartar: ' + err.message;
    botones(f.acciones, [['Descartar', () => descartarFila(archivo, destino, f, url).then(recargarSiTermino), 'pel']]);
  }
}

async function iniciarSubida() {
  // Guarda contra reentrada: sin esto, pulsar otra vez duplicaba el bucle.
  if (enLote) return;
  const zona = document.getElementById('zona-subida');
  const entrada = document.getElementById('archivos');
  const boton = document.getElementById('boton-subir');
  if (!entrada.files.length) return;

  enLote = true;
  boton.disabled = true;
  boton.textContent = 'Subiendo…';
  const destino = zona.dataset.destino || '';
  const archivos = Array.from(entrada.files);
  // Los File siguen valiendo para Reintentar; vaciar la selección evita que
  // pulsar «Subir» otra vez repita el lote entero.
  entrada.value = '';
  try {
    const filas = archivos.map((a) => fila(a.name));
    for (let i = 0; i < archivos.length; i++) await subirUno(archivos[i], destino, filas[i]);
  } finally {
    enLote = false;
    boton.disabled = false;
    boton.textContent = 'Subir';
  }
  recargarSiTermino();
}

// ── Subidas a medias (Resumen, _pendientes.html) ──────────────────────────
//
// Cada una lleva su menú ⋯. Al pulsar una orden el menú se cierra, y lo que
// pasa se cuenta debajo de la tarjeta, en «data-estado».

function prepararPendiente(t) {
  const acciones = t.querySelector('[data-acciones]');
  if (!acciones) return; // se está subiendo ahora: sin órdenes
  const url = t.dataset.subida;
  const total = Number(t.dataset.total);
  const estado = t.querySelector('[data-estado]');
  const elegir = t.querySelector('[data-elegir]');

  function ocupada(si) {
    t.toggleAttribute('data-ocupada', si);
    acciones.open = false;
    acciones.hidden = si;
  }

  async function continuar(archivo) {
    ocupada(true);
    const control = { abortar: new AbortController(), motivo: null, url };
    try {
      const desde = await consultar(url);
      if (desde === null) throw new Error(NO_ESTA + ' (si la sesión se cerró, recargue la página)');
      const avance = (hecho) => { estado.textContent = 'Subiendo… ' + porcentaje(hecho, total); };
      avance(desde);
      await enviar(url, archivo, desde, avance, control);
      estado.textContent = 'Subida.';
      ocupada(false);
      location.reload();
    } catch (err) {
      estado.textContent = 'No se pudo: ' + err.message;
      ocupada(false);
    }
  }

  const reintentar = t.querySelector('[data-accion="reintentar"]');
  if (reintentar) {
    reintentar.addEventListener('click', () => {
      // Con todos los bytes en el nodo basta un PATCH vacío: no hace falta
      // el archivo.
      if (t.hasAttribute('data-completa')) {
        continuar(new Blob([]));
        return;
      }
      // Si no, hace falta el archivo. El selector se abre AQUÍ, dentro del
      // clic: los navegadores solo lo abren en respuesta directa a un gesto.
      acciones.open = false;
      elegir.value = '';
      elegir.click();
    });
  }
  if (elegir) {
    elegir.addEventListener('change', () => {
      const archivo = elegir.files[0];
      if (!archivo) return;
      if (archivo.name !== t.dataset.nombre || archivo.size !== total) {
        estado.textContent = 'Ese no es el mismo archivo: hace falta «' + t.dataset.nombre + '», con el mismo tamaño.';
        return;
      }
      continuar(archivo);
    });
  }

  t.querySelector('[data-accion="descartar"]').addEventListener('click', async () => {
    ocupada(true);
    estado.textContent = 'Descartando…';
    try {
      await descartar(url);
      ocupada(false);
      location.reload();
    } catch (err) {
      estado.textContent = 'No se pudo descartar: ' + err.message;
      ocupada(false);
    }
  });

  acciones.hidden = false;
}

// Salir con una subida en marcha la deja a medias: el navegador pregunta
// antes. Safari en iOS no lo admite; allí queda en «Subidas a medias».
window.addEventListener('beforeunload', (e) => {
  if (document.querySelector('#progreso li[data-estado="subiendo"], #progreso li[data-estado="cola"], [data-subida][data-ocupada]')) {
    e.preventDefault();
    e.returnValue = '';
  }
});

// Los botones se enganchan aquí y no con onclick="" en la plantilla: la CSP
// global (ADR-0060) trae script-src 'self' sin 'unsafe-inline', que un
// atributo de evento en línea incumple en silencio —el clic simplemente no
// hace nada, sin error visible salvo en la consola—. Este script se carga al
// final del body, así que todo lo que engancha ya existe en el DOM.
const botonSubir = document.getElementById('boton-subir');
if (botonSubir) botonSubir.addEventListener('click', iniciarSubida);
for (const t of document.querySelectorAll('[data-subida]')) prepararPendiente(t);
