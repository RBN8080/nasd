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
# Antes de leer tira la cache de paginas (posix_fadvise), para leer del DISCO y
# no de la RAM: un cotejo que lee la cache no prueba que el disco este sano.
import hashlib
import os
import sys


def abrir(ruta):
    f = open(ruta, 'rb', buffering=0)
    fd = f.fileno()
    if hasattr(os, 'posix_fadvise'):
        os.posix_fadvise(fd, 0, 0, os.POSIX_FADV_DONTNEED)
    return f


def hash_bloque(f, off, n):
    f.seek(off)
    leido = bytearray()
    while len(leido) < n:
        trozo = f.read(n - len(leido))
        if not trozo:
            break
        leido += trozo
    if hasattr(os, 'posix_fadvise'):
        os.posix_fadvise(f.fileno(), off, n, os.POSIX_FADV_DONTNEED)
    return hashlib.sha256(bytes(leido)).hexdigest()


def todos(f, size, bs):
    off = 0
    while off < size:
        n = min(bs, size - off)
        yield hash_bloque(f, off, n)
        off += n


def modo_full(ruta, bs):
    f = abrir(ruta)
    try:
        size = os.fstat(f.fileno()).st_size
        sys.stdout.write('SIZE %d\n' % size)
        for h in todos(f, size, bs):
            sys.stdout.write(h + '\n')
    finally:
        f.close()


def modo_ranges(ruta, bs):
    f = abrir(ruta)
    try:
        size = os.fstat(f.fileno()).st_size
        sys.stdout.write('SIZE %d\n' % size)
        for crudo in sys.stdin.buffer:
            # Windows PowerShell 5.1 antepone un BOM al canalizar texto a un
            # programa externo, aunque se le pida UTF-8 sin el. Medido el
            # 2026-10-18: la primera linea llegaba como '﻿0'. Se leen bytes
            # y se decodifican aqui para que de igual la codificacion del sistema.
            linea = crudo.decode('utf-8', 'replace').strip().lstrip('﻿')
            if not linea:
                continue
            off = int(linea) * bs
            if off >= size:
                sys.stdout.write('-\n')
                continue
            sys.stdout.write(hash_bloque(f, off, min(bs, size - off)) + '\n')
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
            f = abrir(ruta)
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
