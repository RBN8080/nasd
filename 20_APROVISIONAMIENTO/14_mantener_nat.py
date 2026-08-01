#!/usr/bin/env python3
"""Mantiene viva la ruta de entrada del túnel WireGuard. Fase 5.

EL PROBLEMA QUE RESUELVE, medido el 2026-08-01 y no supuesto:

  El nodo está detrás de DOS NAT — el del router de casa y el del operador
  (CGNAT: la WAN del router es 30.84.162.148 y el mundo ve 198.51.100.0).
  Medido con STUN desde este mismo puerto contra tres servidores distintos,
  el mapeo del operador es INDEPENDIENTE DEL DESTINO y PRESERVA EL PUERTO:
  61820 sale como 61820. Es decir, la entrada desde fuera SÍ es posible.

  Lo que no es posible es que sobreviva al silencio. Sin tráfico saliente
  caducan tres cosas a la vez:

    1. la asociación del CGNAT del operador,
    2. el estado de NAT del router de casa,
    3. la entrada ARP del router para 192.168.1.38 — y esta es la más
       traicionera, porque el nodo casi nunca habla CON el router: SSH, SMB y
       la web son de la misma subred y no pasan por él. Cuando esa entrada
       caduca, la DMZ no tiene a dónde entregar y el paquete se pierde sin
       error en ninguna parte.

  Encaja con lo observado: el túnel funcionó de 13:51 a 14:10, exactamente
  mientras el PersistentKeepalive del cliente movía tráfico, y murió al parar.

POR QUÉ UN SOCKET EN CRUDO Y NO UNO NORMAL:

  El puerto 61820 lo tiene WireGuard. Un socket normal no puede enlazarlo, pero
  uno en crudo sí puede EMITIR con ese puerto de origen, que es lo único que
  hace falta: lo que refresca las tres asociaciones es el paquete que sale.

  La respuesta del servidor STUN vuelve al 61820 y la recibe WireGuard, que la
  descarta por no ser un paquete válido suyo. Es inofensivo y además demuestra
  que el camino de vuelta funciona.

  Se envía una petición STUN legítima —no basura— porque un servidor STUN está
  para recibir UDP de desconocidos: es el uso previsto del protocolo.

LÍMITE DECLARADO, y no se disimula: esto depende de que el operador mantenga el
mapeo independiente del destino. Es comportamiento suyo, no configuración
nuestra, y puede cambiar sin avisar. Si cambia, el síntoma será exactamente el
de hoy y lo primero que hay que reejecutar es la medición de STUN.
"""
import os
import random
import socket
import struct
import sys
import time

PUERTO = 61820
# 20 s: por debajo del mínimo de 2 min que RFC 4787 exige a un NAT para UDP, y
# por debajo del PersistentKeepalive de 25 s de los clientes, que es el valor
# que ya demostró sostener el túnel de 13:51 a 14:10.
INTERVALO = 20
# Varios, para no depender de uno solo y para repartir la carga.
DESTINOS = [
    ("stun.l.google.com", 19302),
    ("stun1.l.google.com", 19302),
    ("stun.cloudflare.com", 3478),
]
COOKIE = 0x2112A442


def suma_comprobacion(datos):
    if len(datos) % 2:
        datos += b"\x00"
    total = 0
    for i in range(0, len(datos), 2):
        total += (datos[i] << 8) + datos[i + 1]
    while total >> 16:
        total = (total & 0xFFFF) + (total >> 16)
    return (~total) & 0xFFFF


def paquete_udp(ip_origen, ip_destino, puerto_origen, puerto_destino, carga):
    longitud = 8 + len(carga)
    cabecera = struct.pack(">HHHH", puerto_origen, puerto_destino, longitud, 0)
    pseudo = (
        socket.inet_aton(ip_origen)
        + socket.inet_aton(ip_destino)
        + struct.pack(">BBH", 0, socket.IPPROTO_UDP, longitud)
    )
    suma = suma_comprobacion(pseudo + cabecera + carga)
    if suma == 0:
        suma = 0xFFFF
    return struct.pack(">HHHH", puerto_origen, puerto_destino, longitud, suma) + carga


def ip_local_hacia(destino):
    s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    try:
        s.connect((destino, 53))
        return s.getsockname()[0]
    finally:
        s.close()


def refrescar(sock):
    """Emite un refresco. Devuelve el texto de lo hecho, o None si no pudo."""
    host, puerto_destino = random.choice(DESTINOS)
    try:
        ip_destino = socket.gethostbyname(host)
    except OSError as e:
        print("no resuelve %s: %s" % (host, e), file=sys.stderr, flush=True)
        return None

    ip_origen = ip_local_hacia(ip_destino)
    carga = struct.pack(">HHI", 0x0001, 0, COOKIE) + os.urandom(12)
    sock.sendto(paquete_udp(ip_origen, ip_destino, PUERTO, puerto_destino, carga),
                (ip_destino, puerto_destino))
    return "%s:%d -> %s:%d" % (ip_origen, PUERTO, ip_destino, puerto_destino)


def main():
    bucle = "--demonio" in sys.argv
    try:
        # El kernel construye la cabecera IP; nosotros solo la UDP, que es donde
        # está el puerto de origen que hay que conservar.
        sock = socket.socket(socket.AF_INET, socket.SOCK_RAW, socket.IPPROTO_UDP)
    except PermissionError:
        print("hace falta CAP_NET_RAW", file=sys.stderr)
        return 1

    if not bucle:
        hecho = refrescar(sock)
        sock.close()
        if hecho:
            print("refresco enviado: %s" % hecho)
            return 0
        return 1

    # Modo demonio: un solo proceso que duerme. Un temporizador de systemd cada
    # 20 s arrancaría Python ~4300 veces al día en un nodo cuyo límite térmico
    # blando ya está activo (rector §4.1); esto cuesta ~10 MB y casi nada de CPU.
    print("manteniendo la ruta de entrada viva cada %d s" % INTERVALO, flush=True)
    fallos = 0
    while True:
        try:
            if refrescar(sock) is None:
                fallos += 1
            else:
                fallos = 0
        except OSError as e:
            fallos += 1
            print("fallo al emitir: %s" % e, file=sys.stderr, flush=True)
        # Sin red no se martillea: se espacia, pero NUNCA se abandona — el día
        # que la red vuelva, el túnel tiene que volver solo.
        time.sleep(INTERVALO if fallos == 0 else min(INTERVALO * 2 ** min(fallos, 4), 300))


sys.exit(main())
