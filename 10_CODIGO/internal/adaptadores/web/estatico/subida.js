// Cliente del núcleo del protocolo tus — ADR-0027.
//
// Escrito a mano e incrustado en el binario: ADR-0017 prohíbe CDN y
// dependencias externas, y "sin framework" nunca fue "sin JavaScript".
// Trocear, seguir el progreso y reanudar no se hacen con un formulario.
'use strict';

// Tamaño de bloque [R]. Con el techo de ~31 MB/s del bus (RES-02), 8 MB es
// ~0.3 s de transferencia: bastante grande para no penalizar por latencia y
// bastante pequeño para que reanudar no pierda mucho trabajo.
const BLOQUE = 8 * 1024 * 1024;

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
// El servidor SÍ conservaba el desplazamiento —ADR-0027 acertaba: el
// desplazamiento ES el tamaño del parcial— pero el cliente no sabía volver.
const CLAVE_SUBIDAS = 'nas.subidas';

// Testigo CSRF de la sesión, que la página deja en el DOM.
//
// Va en una cabecera propia y no en el cuerpo: un formulario de otro sitio
// no puede fijar cabeceras, así que exigirla es en sí misma una barrera.
function csrf() {
  const z = document.getElementById('zona-subida');
  return (z && z.dataset.csrf) || '';
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

// Estado de la sesión de subida en curso.
//
// EL DEFECTO QUE ESTO CORRIGE (reportado en uso real el 2026-07-31): pulsar
// «Subir» otra vez mientras ya estaba subiendo arrancaba un SEGUNDO bucle
// sobre los mismos archivos. No corrompía nada —el servidor rechaza los
// desplazamientos que no cuadran con un 409— pero duplicaba la transferencia
// y mostraba dos progresos.
let subiendoAhora = false;

function fila(nombre) {
  const li = document.createElement('li');
  const n = document.createElement('span');
  const e = document.createElement('span');
  const acciones = document.createElement('span');
  n.textContent = nombre;
  e.textContent = 'en cola';
  li.append(n, e, acciones);
  document.getElementById('progreso').append(li);
  return { estado: e, acciones: acciones };
}

// Botón que detiene esta subida concreta.
//
// PAUSAR y DESCARTAR son cosas distintas y se ofrecen por separado:
//   - Pausar deja el parcial en el servidor: se reanuda desde donde iba.
//   - Descartar lo destruye ahora, en lugar de dejar basura hasta que
//     expire dentro de días (ADR-0029).
function botonesDeControl(contenedor, control) {
  const pausar = document.createElement('button');
  pausar.type = 'button';
  pausar.textContent = 'Pausar';
  pausar.onclick = () => { control.motivo = 'pausa'; control.abortar.abort(); };

  const descartar = document.createElement('button');
  descartar.type = 'button';
  descartar.textContent = 'Descartar';
  descartar.onclick = () => { control.motivo = 'descartar'; control.abortar.abort(); };

  contenedor.append(pausar, descartar);
  return () => { pausar.remove(); descartar.remove(); };
}

async function iniciarSubida() {
  // Guarda contra reentrada: sin esto, pulsar otra vez duplicaba el bucle.
  if (subiendoAhora) return;

  const zona = document.getElementById('zona-subida');
  const entrada = document.getElementById('archivos');
  const boton = document.getElementById('boton-subir');
  if (!entrada.files.length) return;

  subiendoAhora = true;
  if (boton) { boton.disabled = true; boton.textContent = 'Subiendo...'; }

  const destino = zona.dataset.destino || '';
  let algunFallo = false, algunaPausa = false;

  try {
    for (const archivo of entrada.files) {
      const f = fila(archivo.name);
      const control = { abortar: new AbortController(), motivo: null };
      const quitarBotones = botonesDeControl(f.acciones, control);
      try {
        await subirArchivo(archivo, destino, f.estado, control);
        f.estado.textContent = 'completado';
      } catch (err) {
        if (control.motivo === 'pausa') {
          algunaPausa = true;
          f.estado.textContent = 'pausada — se reanuda al volver a subirla';
        } else if (control.motivo === 'descartar') {
          f.estado.textContent = 'descartada';
        } else {
          algunFallo = true;
          f.estado.textContent = 'error: ' + err.message;
        }
      } finally {
        quitarBotones();
      }
    }
  } finally {
    subiendoAhora = false;
    if (boton) { boton.disabled = false; boton.textContent = 'Subir'; }
  }

  if (!algunFallo && !algunaPausa) location.reload();
}

async function subirArchivo(archivo, destino, estado, control) {
  const clave = claveDe(archivo, destino);
  let url = null;
  let offset = 0;

  // 1. ¿Había una subida a medias de ESTE mismo archivo a ESTE destino?
  //    Se pregunta al servidor, que es quien tiene la verdad: el
  //    desplazamiento es el tamaño del parcial, no un dato que guardemos.
  const recordada = subidasRecordadas()[clave];
  if (recordada) {
    try {
      const r = await fetch(recordada, { method: 'HEAD', headers: { 'Tus-Resumable': '1.0.0' } });
      if (r.ok) {
        url = recordada;
        offset = parseInt(r.headers.get('Upload-Offset') || '0', 10);
        estado.textContent = 'reanudando desde ' + Math.floor((offset / archivo.size) * 100) + ' %';
      } else {
        // El servidor ya no la conoce (p. ej. se reinició el servicio).
        olvidar(clave);
      }
    } catch (e) {
      olvidar(clave);
    }
  }

  // 2. Si no hay nada que reanudar, se crea.
  if (!url) {
  const creacion = await fetch('/subidas', {
    method: 'POST',
    headers: {
      'Tus-Resumable': '1.0.0',
      'Nas-Csrf': csrf(),
      'Upload-Length': String(archivo.size),
      // AMBAS van codificadas, y no es opcional: las cabeceras HTTP solo
      // admiten Latin-1, así que un acento en la ruta o en el nombre hace
      // que fetch() lance «String contains non ISO-8859-1 code point» y la
      // subida ni siquiera se intente.
      //
      // El destino se enviaba en crudo y rompía al subir desde cualquier
      // carpeta con acentos. El servidor decodifica ambas (ver tus.go).
      'Nas-Destino': encodeURIComponent(destino),
      'Nas-Nombre': encodeURIComponent(archivo.name),
    },
  });
  if (creacion.status === 409) throw new Error('ya existe (no se sobrescribe)');
  if (!creacion.ok) throw new Error('no se pudo crear (' + creacion.status + ')');

    url = creacion.headers.get('Location');
    recordar(clave, url);
  }

  // 3. Enviar por bloques, reanudando desde el desplazamiento que diga el
  //    servidor. Nunca desde el que creamos nosotros: la fuente de verdad es
  //    el tamaño del archivo parcial en el servidor (ADR-0027).
  while (offset < archivo.size) {
    const trozo = archivo.slice(offset, Math.min(offset + BLOQUE, archivo.size));
    let r;
    try {
      r = await fetch(url, {
        method: 'PATCH',
        headers: {
          'Tus-Resumable': '1.0.0',
          'Nas-Csrf': csrf(),
          'Upload-Offset': String(offset),
          'Content-Type': 'application/offset+octet-stream',
        },
        body: trozo,
        signal: control.abortar.signal,
      });
    } catch (err) {
      // Detenida por el usuario: no es un corte de red, no se reintenta.
      if (control.motivo === 'pausa') {
        throw new Error('pausada');
      }
      if (control.motivo === 'descartar') {
        await fetch(url, { method: 'DELETE', headers: { 'Tus-Resumable': '1.0.0', 'Nas-Csrf': csrf() } })
          .catch(() => {});
        olvidar(clave);
        throw new Error('descartada');
      }
      // Corte de red: se consulta el estado y se reanuda. RF-12.
      offset = await consultarDesplazamiento(url);
      continue;
    }
    if (r.status === 409) {
      offset = await consultarDesplazamiento(url);
      continue;
    }
    if (!r.ok) throw new Error('fallo al enviar (' + r.status + ')');

    offset = parseInt(r.headers.get('Upload-Offset') || '0', 10);
    estado.textContent = Math.floor((offset / archivo.size) * 100) + ' %';
  }

  // Completada: se deja de recordar para no reanudar algo ya terminado.
  olvidar(clave);
}

async function consultarDesplazamiento(url) {
  const r = await fetch(url, { method: 'HEAD', headers: { 'Tus-Resumable': '1.0.0' } });
  if (!r.ok) throw new Error('la subida ya no existe en el servidor');
  return parseInt(r.headers.get('Upload-Offset') || '0', 10);
}
