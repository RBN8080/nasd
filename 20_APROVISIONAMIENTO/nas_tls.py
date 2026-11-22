#!/usr/bin/env python3
"""Despliegue TLS por linaje seleccionado y cambio atomico del par de nasd."""

import grp
import os
import re
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

from nas_dominio import ANTIGUO, NUEVO, Config, bloquear, privado
from njalla_registros import ErrorDNS

BASE = Path("/etc/nas/tls")
SELECTOR = Path("/etc/nas/tls-lineage")
LIN_ANTIGUO = Path("/etc/letsencrypt/live") / ANTIGUO
LIN_TRANSICION = Path("/etc/letsencrypt/live/nas-transicion")
LIN_FINAL = Path("/etc/letsencrypt/live") / NUEVO


def openssl(*argumentos: str) -> bytes:
    resultado = subprocess.run(
        ["openssl", *argumentos], capture_output=True, timeout=10, check=False
    )
    if resultado.returncode:
        raise ErrorDNS("Certificado o clave no validos; no se activa el par")
    return resultado.stdout


def validar_par(
    linaje: Path, nombres: set[str], confianza: Path = Path("/etc/ssl/certs")
) -> None:
    certificado = str(linaje / "fullchain.pem")
    san = openssl(
        "x509", "-in", certificado, "-noout", "-ext", "subjectAltName"
    ).decode("ascii")
    if set(re.findall(r"DNS:([^,\s]+)", san)) != nombres:
        raise ErrorDNS("Los SAN no coinciden exactamente con la fase elegida")
    openssl("x509", "-in", certificado, "-noout", "-checkend", "604800")
    cert_pub = openssl("x509", "-in", certificado, "-pubkey", "-noout")
    key_pub = openssl("pkey", "-in", str(linaje / "privkey.pem"), "-pubout")
    if cert_pub != key_pub:
        raise ErrorDNS("Certificado y clave no forman un par")
    for nombre in nombres:
        openssl(
            "verify",
            "-purpose",
            "sslserver",
            "-verify_hostname",
            nombre,
            "-CApath",
            str(confianza),
            "-untrusted",
            str(linaje / "chain.pem"),
            str(linaje / "cert.pem"),
        )


def enlace_atomico(base: Path, nombre: str, destino: str) -> None:
    temporal = base / (nombre + ".pendiente")
    if temporal.exists() or temporal.is_symlink():
        raise ErrorDNS("Hay un enlace pendiente; revisar antes de repetir")
    temporal.symlink_to(destino)
    try:
        os.replace(temporal, base / nombre)
        fd = os.open(base, os.O_DIRECTORY)
        try:
            os.fsync(fd)
        finally:
            os.close(fd)
    finally:
        if temporal.is_symlink():
            temporal.unlink()


def copiar_par(origen: Path, base: Path, grupo: int) -> str:
    pares = base / "pares"
    pares.mkdir(mode=0o755, exist_ok=True)
    carpeta = Path(tempfile.mkdtemp(prefix="par-", dir=pares))
    os.chown(carpeta, -1, grupo)
    carpeta.chmod(0o750)
    for nombre, permiso in (("fullchain.pem", 0o644), ("privkey.pem", 0o640)):
        destino = carpeta / nombre
        # No conservar mtime: el cargador Go detecta el cambio por fecha.
        with (origen / nombre).open("rb") as entrada, destino.open("xb") as salida:
            os.fchmod(salida.fileno(), permiso)
            os.fchown(salida.fileno(), -1, grupo)
            shutil.copyfileobj(entrada, salida)
            salida.flush()
            os.fsync(salida.fileno())
    return str(carpeta.relative_to(base))


def comprobar_enlace(base: Path, nombre: str) -> str:
    ruta = base / nombre
    if not ruta.is_symlink():
        raise ErrorDNS("El par TLS no usa el enlace esperado")
    destino = os.readlink(ruta)
    resuelto = (base / destino).resolve(strict=True)
    if not resuelto.is_relative_to((base / "pares").resolve()) or not resuelto.is_dir():
        raise ErrorDNS("Enlace TLS fuera del directorio de pares")
    for pieza in ("fullchain.pem", "privkey.pem"):
        if not (resuelto / pieza).is_file():
            raise ErrorDNS("Par TLS incompleto")
    return destino


def activar(
    linaje: Path, nombres: set[str], base: Path = BASE, grupo: int | None = None
) -> None:
    validar_par(linaje, nombres)
    if grupo is None:
        grupo = grp.getgrnam("nas").gr_gid
    if not base.is_dir() or base.is_symlink():
        raise ErrorDNS("Directorio TLS base inesperado")
    nuevo = copiar_par(linaje, base, grupo)
    actuales = [base / n for n in ("fullchain.pem", "privkey.pem")]
    if not (base / "actual").is_symlink():
        if (base / "actual").exists():
            raise ErrorDNS("Hay otro archivo en la ruta del selector TLS")
        if any(not p.is_file() or p.is_symlink() for p in actuales):
            raise ErrorDNS("Estado inicial de TLS inesperado; no se reemplaza")
        anterior = copiar_par(base, base, grupo)
        enlace_atomico(base, "actual", anterior)
    anterior = comprobar_enlace(base, "actual")
    # La conversion inicial cambia primero las rutas por enlaces al MISMO par.
    # Si se interrumpe, ambas rutas siguen leyendo el certificado anterior.
    for pieza in ("fullchain.pem", "privkey.pem"):
        ruta = base / pieza
        if ruta.is_symlink() and os.readlink(ruta) != "actual/" + pieza:
            raise ErrorDNS("Enlace de certificado inesperado")
        if not ruta.is_symlink():
            enlace_atomico(base, pieza, "actual/" + pieza)
    enlace_atomico(base, "anterior", anterior)
    enlace_atomico(base, "actual", nuevo)


def volver(base: Path = BASE) -> None:
    anterior = comprobar_enlace(base, "anterior")
    actual = comprobar_enlace(base, "actual")
    enlace_atomico(base, "actual", anterior)
    enlace_atomico(base, "anterior", actual)


def main() -> int:
    try:
        if os.geteuid() != 0:
            raise ErrorDNS("Ejecute como root")
        with bloquear():
            if sys.argv[1:] == ["volver"]:
                # Restaurar tambien el selector y DNS segun MIGRACION_DOMINIO.md.
                volver()
                return 0
            if sys.argv[1:] != ["desplegar"]:
                raise ErrorDNS("Accion TLS desconocida")
            elegido = Path(privado(SELECTOR).strip())
            if elegido not in (LIN_ANTIGUO, LIN_TRANSICION, LIN_FINAL):
                raise ErrorDNS("Linaje seleccionado desconocido")
            if os.environ.get("RENEWED_LINEAGE", "") != str(elegido):
                return 0
            conf = Config.cargar()
            if elegido == LIN_ANTIGUO:
                nombres = {ANTIGUO}
            elif elegido == LIN_TRANSICION and conf.convivencia:
                nombres = {NUEVO, ANTIGUO}
            elif elegido == LIN_FINAL and not conf.convivencia:
                nombres = {NUEVO}
            else:
                raise ErrorDNS("Linaje incompatible con fase de convivencia")
            activar(elegido, nombres)
        return 0
    except (OSError, ValueError, ErrorDNS, subprocess.SubprocessError) as error:
        print(
            "FALLO TLS NAS: "
            + type(error).__name__
            + "; par anterior conservado/revisar estado",
            file=sys.stderr,
        )
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
