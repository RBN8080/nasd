#!/usr/bin/env python3
# blkhash.py - SHA256 por bloque de un archivo (ADR-0104).
#
# Corre EN LA PI, sobre el disco del nodo. El equipo calcula lo mismo en .NET
# (laboratorio.ps1, Get-HuellasPorBloque): mismo SHA256 del mismo rango, asi que
# las dos listas se comparan linea a linea. Nace de _lab/v2/blkhash.py del
# laboratorio; esta es la copia que manda, con el modo `lote` anadido.
#
#   blkhash.py full   <archivo> <bloque>          "SIZE n" y un hash por bloque
#   blkhash.py ranges <archivo> <bloque>          indices por stdin, uno por linea;
#                                                 "SIZE n" y el hash de cada uno
#                                                 ('-' si cae fuera del archivo)
#   blkhash.py lote   <bloque> <lista> <salida>   lee entero cada archivo de la
#                                                 lista (una ruta por linea) y deja
#                                                 <salida>/<i>.blk, AVANCE y FIN
#
# Integridad + rendimiento:
#   - Al abrir tira la cache entera con POSIX_FADV_DONTNEED para leer del DISCO
#     y nunca de la RAM.
#   - En lecturas secuenciales y tramos contiguos activa POSIX_FADV_SEQUENTIAL
#     y purga la cache en ventana deslizante trasera cada 16 MiB en vez de cada
#     1 MiB, para no destruir el readahead del kernel sobre el HDD mecanico.
#   - Superpone lectura de disco (hilo principal) con SHA-256 (pool acotado de
#     2 hilos; hashlib libera el GIL en CPython) manteniendo memoria acotada.
from collections import deque
from concurrent.futures import ThreadPoolExecutor
import hashlib
import os
import sys

VENTANA_PURGA = 16 * 1024 * 1024  # 16 MiB de ventana deslizante para DONTNEED
HILOS_HASH = 2
EN_VUELO_MAX = 4
FADV_DONTNEED = getattr(os, 'POSIX_FADV_DONTNEED', None)
FADV_SEQUENTIAL = getattr(os, 'POSIX_FADV_SEQUENTIAL', None)


def _fadvise(fd, off, length, advice):
    if advice is not None and hasattr(os, 'posix_fadvise'):
        try:
            os.posix_fadvise(fd, off, length, advice)
        except OSError:
            pass


def abrir(ruta, secuencial=False):
    f = open(ruta, 'rb', buffering=0)
    fd = f.fileno()
    _fadvise(fd, 0, 0, FADV_DONTNEED)
    if secuencial:
        _fadvise(fd, 0, 0, FADV_SEQUENTIAL)
    return f


def leer_exacto(f, n):
    leido = bytearray()
    while len(leido) < n:
        trozo = f.read(n - len(leido))
        if not trozo:
            break
        leido += trozo
    return bytes(leido)


def _sha256_hex(datos):
    return hashlib.sha256(datos).hexdigest()


def hash_tramo_secuencial(f, inicio_off, fin_off, bs, pool):
    """Lee [inicio_off, fin_off) secuencialmente con readahead + purga deslizante + SHA256 en pipeline."""
    fd = f.fileno()
    largo_tramo = fin_off - inicio_off
    if largo_tramo <= 0:
        return
    if largo_tramo >= 4 * bs:
        _fadvise(fd, inicio_off, largo_tramo, FADV_SEQUENTIAL)

    f.seek(inicio_off)
    cola = deque()
    off = inicio_off
    purgado_hasta = inicio_off

    while off < fin_off:
        n = min(bs, fin_off - off)
        datos = leer_exacto(f, n)
        cola.append(pool.submit(_sha256_hex, datos))
        off += n

        if off - purgado_hasta >= VENTANA_PURGA:
            _fadvise(fd, purgado_hasta, off - purgado_hasta, FADV_DONTNEED)
            purgado_hasta = off

        while len(cola) >= EN_VUELO_MAX:
            yield cola.popleft().result()

    if off > purgado_hasta:
        _fadvise(fd, purgado_hasta, off - purgado_hasta, FADV_DONTNEED)

    while cola:
        yield cola.popleft().result()


def todos(f, size, bs):
    if size <= 0:
        return
    with ThreadPoolExecutor(max_workers=HILOS_HASH) as pool:
        for h in hash_tramo_secuencial(f, 0, size, bs, pool):
            yield h


def modo_full(ruta, bs):
    f = abrir(ruta, secuencial=True)
    try:
        size = os.fstat(f.fileno()).st_size
        sys.stdout.write('SIZE %d\n' % size)
        for h in todos(f, size, bs):
            sys.stdout.write(h + '\n')
    finally:
        f.close()


def modo_ranges(ruta, bs):
    f = abrir(ruta, secuencial=False)
    try:
        fd = f.fileno()
        size = os.fstat(fd).st_size
        sys.stdout.write('SIZE %d\n' % size)

        indices = []
        for crudo in sys.stdin.buffer:
            # Windows PowerShell 5.1 antepone un BOM al canalizar texto a un
            # programa externo, aunque se le pida UTF-8 sin el. Medido el
            # 2026-10-18: la primera linea llegaba como '\ufeff0'. Se leen bytes
            # y se decodifican aqui para que de igual la codificacion del sistema.
            linea = crudo.decode('utf-8', 'replace').strip().lstrip('\ufeff')
            if not linea:
                continue
            indices.append(int(linea))

        if not indices:
            return

        # Agrupa indices contiguos en tramos para aprovechar readahead y pipeline
        # preservando exactamente el orden pedido por stdin (incluidos '-' fuera
        # de rango o indices repetidos/desordenados).
        with ThreadPoolExecutor(max_workers=HILOS_HASH) as pool:
            pos = 0
            total = len(indices)
            while pos < total:
                idx = indices[pos]
                off = idx * bs
                if idx < 0 or off >= size:
                    sys.stdout.write('-\n')
                    pos += 1
                    continue

                fin_pos = pos + 1
                while fin_pos < total and indices[fin_pos] == indices[fin_pos - 1] + 1:
                    if indices[fin_pos] * bs >= size:
                        break
                    fin_pos += 1

                largo_run = fin_pos - pos
                if largo_run >= 2:
                    inicio_off = idx * bs
                    fin_off = min(size, (indices[fin_pos - 1] + 1) * bs)
                    for h in hash_tramo_secuencial(f, inicio_off, fin_off, bs, pool):
                        sys.stdout.write(h + '\n')
                else:
                    n = min(bs, size - off)
                    f.seek(off)
                    datos = leer_exacto(f, n)
                    _fadvise(fd, off, n, FADV_DONTNEED)
                    sys.stdout.write(_sha256_hex(datos) + '\n')
                pos = fin_pos
    finally:
        f.close()


def escribir(ruta, texto):
    tmp = ruta + '.tmp'
    with open(tmp, 'w', encoding='utf-8', newline='\n') as s:
        s.write(texto)
    os.replace(tmp, ruta)


def modo_lote(bs, lista, salida):
    # LO QUE SE LEE TIENE QUE SER LO QUE HABIA. Se mira tamano y fecha antes y
    # despues de cada archivo: si cambiaron durante la lectura, el manifiesto no
    # describe ningun estado real y se marca ESTABLE 0 para que nadie lo use.
    os.makedirs(salida, exist_ok=True)
    # utf-8-sig: la lista llega por la entrada estandar desde Windows
    # PowerShell, que le pone BOM (ver modo_ranges).
    with open(lista, encoding='utf-8-sig') as e:
        rutas = [l.rstrip('\r\n') for l in e if l.strip()]
    total = len(rutas)
    for i, ruta in enumerate(rutas):
        escribir(os.path.join(salida, 'AVANCE'), '%d %d\n' % (i, total))
        try:
            antes = os.stat(ruta)
            f = abrir(ruta, secuencial=True)
            try:
                hashes = list(todos(f, antes.st_size, bs))
            finally:
                f.close()
            despues = os.stat(ruta)
            estable = int(antes.st_size == despues.st_size and antes.st_mtime_ns == despues.st_mtime_ns)
            cab = 'BS %d SIZE %d MTIMENS %d ESTABLE %d\n' % (bs, antes.st_size, antes.st_mtime_ns, estable)
            escribir(os.path.join(salida, '%d.blk' % i), cab + ''.join(h + '\n' for h in hashes))
        except OSError as err:
            escribir(os.path.join(salida, '%d.err' % i), '%s\n' % err)
    escribir(os.path.join(salida, 'AVANCE'), '%d %d\n' % (total, total))
    escribir(os.path.join(salida, 'FIN'), '%d\n' % total)


def main():
    modo = sys.argv[1]
    if modo == 'full':
        modo_full(sys.argv[2], int(sys.argv[3]))
    elif modo == 'ranges':
        modo_ranges(sys.argv[2], int(sys.argv[3]))
    elif modo == 'lote':
        modo_lote(int(sys.argv[2]), sys.argv[3], sys.argv[4])
    else:
        sys.stderr.write('modo desconocido: %s\n' % modo)
        sys.exit(2)
    sys.stdout.flush()


main()
