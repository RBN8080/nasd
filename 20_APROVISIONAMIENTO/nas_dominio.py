#!/usr/bin/env python3
"""DDNS y ganchos DNS-01 del NAS. Instalar/activar requiere ventana autorizada."""

import argparse
import fcntl
import json
import os
import re
import shlex
import stat
import subprocess
import sys
import time
import tomllib
from collections.abc import Iterator
from contextlib import contextmanager
from dataclasses import dataclass
from pathlib import Path

from njalla_registros import (
    INACTIVO,
    Cliente,
    ErrorDNS,
    editar,
    normalizar,
    seleccionar,
)
from token_njalla import leer

CONFIG = Path("/etc/nas/njalla/dominio.toml")
ESTADO = Path("/run/nas-njalla")
NUEVO = "nas.example.org"
ANTIGUO = "nas-ejemplo.duckdns.org"
AUTORITATIVOS = ("1-you.njalla.no", "2-can.njalla.in", "3-get.njalla.fo")
ECO_IPV4 = ("https://api.ipify.org", "https://ifconfig.me")
# Autoridad propia de example.org (proyecto 05, en el VPS de 06_VPS): RFC 2136 con
# TSIG (RFC 8945). Una clave por uso, limitada en el servidor a sus nombres.
CLAVES_PROPIAS = Path("/etc/nas/dns05")
CLAVE_PROPIA = {"ddns": "nas-ddns", "vpn": "nas-ddns", "acme": "nas-acme"}
# Las copias de Hurricane Electric sirven un cambio unos 2 min después del aviso (123 s
# medidos dos veces el 2026-11-26) y, si el aviso se pierde, al revisar solas cada 900 s.
# Solo el desafío ACME las espera: Let's Encrypt puede preguntar a cualquiera de los NS.
# Para DDNS basta el primario (el servicio corta a los 240 s); HE se pone al día solo.
ESPERA_COPIAS = 960
SERVIDOR_DNS = re.compile(r"[A-Za-z0-9.:-]{1,253}")


def privado(ruta: Path) -> str:
    fd = os.open(ruta, os.O_RDONLY | os.O_NOFOLLOW)
    with os.fdopen(fd, encoding="utf-8") as archivo:
        info = os.fstat(archivo.fileno())
        if (
            not stat.S_ISREG(info.st_mode)
            or info.st_uid != os.geteuid()
            or info.st_mode & 0o077
        ):
            raise ErrorDNS("Archivo de configuracion/estado no privado")
        return archivo.read(65536)


@dataclass(frozen=True)
class Config:
    origen: str
    interfaz: str
    aaaa_id: str
    txt_id: str
    convivencia: bool
    vpn_a_id: str = ""
    vpn_aaaa_id: str = ""
    # Sin primario, solo Njalla (como antes). «autoridad» dice cuál manda: la que los
    # resolvers consultan; la otra se mantiene al día solo para poder volver.
    primario: str = ""
    propios: tuple[str, ...] = ()
    autoridad: str = "njalla"

    @classmethod
    def cargar(cls, ruta: Path = CONFIG) -> "Config":
        datos = tomllib.loads(privado(ruta))
        for clave in ("origen", "interfaz", "aaaa_id", "txt_id"):
            if not isinstance(datos.get(clave), str) or (
                clave in ("origen", "interfaz") and not datos[clave]
            ):
                raise ErrorDNS("Falta un campo de configuracion del dominio")
        for opcional in ("vpn_a_id", "vpn_aaaa_id"):
            if opcional in datos and not isinstance(datos[opcional], str):
                raise ErrorDNS("Campo opcional de configuracion VPN invalido")
        origen = normalizar("AAAA", datos["origen"])
        if not re.fullmatch(r"[A-Za-z0-9_.-]{1,15}", datos["interfaz"]):
            raise ErrorDNS("Interfaz invalida")
        if type(datos.get("convivencia")) is not bool:
            raise ErrorDNS("Falta declarar convivencia")
        primario = datos.get("primario", "")
        propios = datos.get("propios", [])
        autoridad = datos.get("autoridad", "njalla")
        if not isinstance(primario, str) or not isinstance(propios, list):
            raise ErrorDNS("Autoridad propia mal declarada")
        if any(
            not isinstance(s, str) or not SERVIDOR_DNS.fullmatch(s)
            for s in [primario, *propios]
            if s != ""
        ):
            raise ErrorDNS("Servidor de la autoridad propia invalido")
        if autoridad not in ("njalla", "propia") or (
            primario and not propios
        ) or (autoridad == "propia" and not primario):
            raise ErrorDNS("Autoridad propia incompleta")
        return cls(
            origen,
            datos["interfaz"],
            datos["aaaa_id"],
            datos["txt_id"],
            datos["convivencia"],
            datos.get("vpn_a_id", ""),
            datos.get("vpn_aaaa_id", ""),
            primario,
            tuple(propios),
            autoridad,
        )


@contextmanager
def bloquear(carpeta: Path = ESTADO, espera: float = 0.0) -> Iterator[None]:
    carpeta.mkdir(mode=0o700, exist_ok=True)
    info = carpeta.lstat()
    if (
        not stat.S_ISDIR(info.st_mode)
        or info.st_uid != os.geteuid()
        or info.st_mode & 0o077
    ):
        raise ErrorDNS("Directorio de estado no privado")
    fd = os.open(
        carpeta / "operacion.lock", os.O_CREAT | os.O_RDWR | os.O_NOFOLLOW, 0o600
    )
    with os.fdopen(fd, "r+") as archivo:
        limite = time.monotonic() + max(0.0, espera)
        while True:
            try:
                fcntl.flock(archivo, fcntl.LOCK_EX | fcntl.LOCK_NB)
                break
            except BlockingIOError:
                restante = limite - time.monotonic()
                if restante <= 0:
                    raise
                time.sleep(min(0.2, restante))
        yield


def origen_presente(conf: Config) -> None:
    resultado = subprocess.run(
        ["ip", "-6", "-j", "addr", "show", "dev", conf.interfaz, "scope", "global"],
        capture_output=True,
        text=True,
        check=True,
        timeout=5,
    )
    interfaces = json.loads(resultado.stdout)
    for interfaz in interfaces:
        for direccion in interfaz.get("addr_info", []):
            flags = direccion.get("flags", [])
            if (
                direccion.get("local") == conf.origen
                and direccion.get("preferred_life_time") != 0
                and not any(
                    flag in flags for flag in ("tentative", "dadfailed", "deprecated")
                )
            ):
                return
    raise ErrorDNS(
        "IPv6 autorizada ausente/no preferida: revisar prefijo, From y regla ZTE"
    )


def respuesta_dns(texto: str, fqdn: str, tipo: str, esperado: str) -> bool:
    if "status: NOERROR" not in texto or not re.search(r"flags:[^;]*\baa\b", texto):
        return False
    valores = []
    for linea in texto.splitlines():
        if not linea or linea.startswith(";"):
            continue
        try:
            campos = shlex.split(linea)
        except ValueError:
            return False
        if (
            len(campos) >= 5
            and campos[0].rstrip(".").lower() == fqdn
            and campos[3] == tipo
        ):
            valor = "".join(campos[4:]) if tipo == "TXT" else campos[4]
            try:
                valores.append(normalizar(tipo, valor))
            except ErrorDNS:
                return False
    return valores == [normalizar(tipo, esperado)]


def propagado(
    fqdn: str,
    tipo: str,
    esperado: str,
    segundos: float = 180,
    servidores: tuple[str, ...] = AUTORITATIVOS,
) -> None:
    limite = time.monotonic() + segundos
    while True:
        correcto = True
        for servidor in servidores:
            try:
                resultado = subprocess.run(
                    [
                        "dig",
                        "@" + servidor,
                        fqdn,
                        tipo,
                        "+norecurse",
                        "+tcp",
                        "+time=3",
                        "+tries=1",
                        "+noall",
                        "+comments",
                        "+answer",
                    ],
                    capture_output=True,
                    text=True,
                    timeout=5,
                    check=False,
                )
            except subprocess.TimeoutExpired:
                correcto = False
                break
            correcto = (
                correcto
                and resultado.returncode == 0
                and respuesta_dns(resultado.stdout, fqdn, tipo, esperado)
            )
            if not correcto:
                break
        if correcto:
            return
        if time.monotonic() >= limite:
            raise ErrorDNS("Propagacion no confirmada en todos los autoritativos")
        time.sleep(3)


def cliente(conf: Config, tipo: str) -> Cliente:
    return Cliente(leer(CONFIG.parent / (tipo + ".token")), conf.origen)


def propio(conf: Config, uso: str, fqdn: str, tipo: str, valor: str) -> None:
    """Escribe en la autoridad propia solo si el primario no tiene ya el valor.

    Sin escrituras repetidas el serial no sube cada 5 min y los secundarios no reciben
    avisos sin cambio. normalizar() limita el valor a IP global o token ACME.
    Un desafío ACME espera además a todos los «propios» (las copias de HE).
    """
    valor = normalizar(tipo, valor)
    desafio = uso == "acme" and valor != INACTIVO
    try:
        propagado(fqdn, tipo, valor, segundos=0, servidores=(conf.primario,))
        ya_estaba = True
    except ErrorDNS:
        ya_estaba = False
    if not ya_estaba:
        escribir_propio(conf, uso, fqdn, tipo, valor)
    if desafio:
        propagado(fqdn, tipo, valor, segundos=ESPERA_COPIAS, servidores=conf.propios)
    elif not ya_estaba:
        propagado(fqdn, tipo, valor, servidores=(conf.primario,))


def escribir_propio(conf: Config, uso: str, fqdn: str, tipo: str, valor: str) -> None:
    clave = CLAVES_PROPIAS / (CLAVE_PROPIA[uso] + ".key")
    privado(clave)
    dato = '"' + valor + '"' if tipo == "TXT" else valor
    guion = (
        f"server {conf.primario}\nzone example.org\n"
        f"update delete {fqdn}. {tipo}\nupdate add {fqdn}. 300 {tipo} {dato}\nsend\n"
    )
    resultado = subprocess.run(
        ["nsupdate", "-k", str(clave), "-t", "10"],
        input=guion,
        capture_output=True,
        text=True,
        timeout=30,
        check=False,
    )
    if resultado.returncode:
        raise ErrorDNS("Actualizacion firmada rechazada por la autoridad propia")


def escribir(
    api: Cliente,
    conf: Config,
    uso: str,
    identificador: str,
    nombre: str,
    tipo: str,
    valor: str,
    esperado: str | None = None,
    segundos: float = 180,
) -> None:
    """Njalla y, si hay primario, la autoridad propia; primero la que manda.

    Un fallo en la que manda corta; en la otra queda como aviso, porque esa copia solo
    sirve para volver atrás. Con la propia mandando no se toca Njalla: su API rechaza
    los registros mientras el dominio usa NS externos (visto el 2026-11-26), y al volver
    a sus NS basta `autoridad = "njalla"` para que la siguiente pasada la ponga al día.
    """
    fqdn = nombre + ".example.org"

    def njalla() -> None:
        editar(api, identificador, nombre, tipo, valor, esperado)
        propagado(fqdn, tipo, valor, segundos=segundos)

    pasos = {} if conf.autoridad == "propia" else {"njalla": njalla}
    if conf.primario:
        pasos["propia"] = lambda: propio(conf, uso, fqdn, tipo, valor)
    for quien in sorted(pasos, key=lambda q: q != conf.autoridad):
        try:
            pasos[quien]()
        except (OSError, ValueError, ErrorDNS, subprocess.SubprocessError) as error:
            if quien == conf.autoridad:
                raise
            print(
                "AVISO dominio NAS: copia "
                + quien
                + " sin actualizar ("
                + type(error).__name__
                + ")",
                file=sys.stderr,
            )


def autenticar(api: Cliente, conf: Config, valor: str, carpeta: Path = ESTADO) -> None:
    valor = normalizar("TXT", valor)
    if valor == INACTIVO:
        raise ErrorDNS("Falta validacion ACME")
    estado = carpeta / "validacion"
    if estado.exists():
        if privado(estado).strip() != valor:
            raise ErrorDNS("Otra validacion esta pendiente; no se sobrescribe")
    else:
        fd = os.open(estado, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(fd, "w") as archivo:
            archivo.write(valor + "\n")
    escribir(api, conf, "acme", conf.txt_id, "_acme-challenge.nas", "TXT", valor, INACTIVO)


def limpiar(api: Cliente, conf: Config, valor: str, carpeta: Path = ESTADO) -> None:
    valor = normalizar("TXT", valor)
    if valor == INACTIVO:
        raise ErrorDNS("Falta validacion ACME")
    estado = carpeta / "validacion"
    if not estado.exists():
        registro = seleccionar(api.listar(), conf.txt_id, "_acme-challenge.nas", "TXT")
        if registro.contenido == INACTIVO:
            return
        raise ErrorDNS("Sin estado propio: revisar TXT pendiente sin sobrescribirlo")
    if privado(estado).strip() != valor:
        raise ErrorDNS("Limpieza de otra validacion rechazada")
    escribir(api, conf, "acme", conf.txt_id, "_acme-challenge.nas", "TXT", INACTIVO, valor)
    estado.unlink()


def ipv4_publica() -> str:
    for url in ECO_IPV4:
        try:
            respuesta = subprocess.run(
                [
                    "curl",
                    "--disable",
                    "--ipv4",
                    "--silent",
                    "--show-error",
                    "--fail",
                    "--noproxy",
                    "*",
                    "--proto",
                    "=https",
                    "--connect-timeout",
                    "5",
                    "--max-time",
                    "15",
                    "--max-filesize",
                    "64",
                    url,
                ],
                capture_output=True,
                text=True,
                timeout=20,
                check=False,
            )
        except subprocess.TimeoutExpired:
            continue
        if respuesta.returncode == 0:
            try:
                return normalizar("A", respuesta.stdout.strip())
            except ErrorDNS:
                continue
    raise ErrorDNS("No se pudo medir una IPv4 publica valida")


def ejecutar(accion: str, conf: Config) -> None:
    origen_presente(conf)
    if accion == "descubrir":
        objetivos: list[tuple[str, str, str]] = [
            ("ddns", "nas", "AAAA"),
            ("acme", "_acme-challenge.nas", "TXT"),
        ]
        if (CONFIG.parent / "vpn.token").exists():
            objetivos.extend([("vpn", "vpn-nas", "A"), ("vpn", "vpn-nas", "AAAA")])
        for uso, nombre, tipo in objetivos:
            registros = cliente(conf, uso).listar()
            candidatos = [
                r
                for r in registros
                if r.get("name") in (nombre, nombre + ".example.org")
                and r.get("type") == tipo
            ]
            if len(candidatos) != 1:
                print("PENDIENTE: falta registro unico " + nombre + "/" + tipo)
                continue
            identificador = str(candidatos[0].get("id", ""))
            seleccionar(registros, identificador, nombre, tipo)
            print(
                json.dumps(
                    {"campo": uso, "id": identificador, "nombre": nombre, "tipo": tipo}
                )
            )
        return
    if accion == "verificar":
        for uso, identificador, nombre, tipo in (
            ("ddns", conf.aaaa_id, "nas", "AAAA"),
            ("acme", conf.txt_id, "_acme-challenge.nas", "TXT"),
            ("vpn", conf.vpn_a_id, "vpn-nas", "A"),
            ("vpn", conf.vpn_aaaa_id, "vpn-nas", "AAAA"),
        ):
            if not identificador:
                if uso == "acme":
                    raise ErrorDNS(
                        "Falta registrar el ID del TXT antes de preparar ganchos"
                    )
                if uso == "ddns":
                    print(
                        "PENDIENTE: crear AAAA y registrar su ID antes de habilitar DDNS"
                    )
                else:
                    print(
                        "PENDIENTE: registro VPN opcional sin ID ("
                        + nombre
                        + "/"
                        + tipo
                        + ")"
                    )
                continue
            seleccionar(cliente(conf, uso).listar(), identificador, nombre, tipo)
        print("OK: registros con ID y origen comprobados; no se escribio DNS")
        return
    if accion == "ddns":
        escribir(
            cliente(conf, "ddns"), conf, "ddns", conf.aaaa_id, "nas", "AAAA",
            conf.origen, segundos=45,
        )
        if conf.vpn_aaaa_id:
            escribir(
                cliente(conf, "vpn"), conf, "vpn", conf.vpn_aaaa_id, "vpn-nas", "AAAA",
                conf.origen, segundos=45,
            )
        if conf.vpn_a_id:
            escribir(
                cliente(conf, "vpn"), conf, "vpn", conf.vpn_a_id, "vpn-nas", "A",
                ipv4_publica(), segundos=45,
            )
        return
    dominio = os.environ.get("CERTBOT_DOMAIN", "")
    validacion = os.environ.get("CERTBOT_VALIDATION", "")
    if dominio == NUEVO:
        funcion = autenticar if accion == "auth" else limpiar
        funcion(cliente(conf, "acme"), conf, validacion)
    else:
        raise ErrorDNS("Dominio de validacion fuera del alcance")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "accion", choices=("descubrir", "verificar", "ddns", "auth", "limpiar")
    )
    args = parser.parse_args()
    try:
        if os.geteuid() != 0:
            raise ErrorDNS("Ejecute como root")
        conf = Config.cargar()
        espera = 60.0 if args.accion in ("auth", "limpiar") else 0.0
        with bloquear(espera=espera):
            ejecutar(args.accion, conf)
        return 0
    except (OSError, ValueError, ErrorDNS, subprocess.SubprocessError) as error:
        # No mostrar cuerpos de API, comandos con secretos ni tracebacks.
        print(
            "FALLO dominio NAS: "
            + type(error).__name__
            + "; revisar configuracion/estado",
            file=sys.stderr,
        )
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
