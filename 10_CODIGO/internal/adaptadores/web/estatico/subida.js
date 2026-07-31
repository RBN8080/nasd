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

function fila(nombre) {
  const li = document.createElement('li');
  const n = document.createElement('span');
  const e = document.createElement('span');
  n.textContent = nombre;
  e.textContent = '0 %';
  li.append(n, e);
  document.getElementById('progreso').append(li);
  return e;
}

async function iniciarSubida() {
  const zona = document.getElementById('zona-subida');
  const entrada = document.getElementById('archivos');
  if (!entrada.files.length) return;

  const destino = zona.dataset.destino || '';
  let algunFallo = false;

  for (const archivo of entrada.files) {
    const estado = fila(archivo.name);
    try {
      await subirArchivo(archivo, destino, estado);
      estado.textContent = 'completado';
    } catch (err) {
      algunFallo = true;
      estado.textContent = 'error: ' + err.message;
    }
  }

  if (!algunFallo) location.reload();
}

async function subirArchivo(archivo, destino, estado) {
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
          'Upload-Offset': String(offset),
          'Content-Type': 'application/offset+octet-stream',
        },
        body: trozo,
      });
    } catch (err) {
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
