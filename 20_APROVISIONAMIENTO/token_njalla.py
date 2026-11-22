#!/usr/bin/env python3
"""Entrada oculta local y custodia root remota; solo prueba list-records."""

import argparse
import base64
import getpass
import json
import os
from pathlib import Path
import shlex
import stat
import subprocess
import sys

ORIGEN = "3fff:2a0:101e:3d82::38"
CARPETA = Path("/etc/nas/njalla")


def validar_token(token: str) -> None:
    if not 16 <= len(token) <= 4096 or any(not 33 <= ord(c) <= 126 for c in token):
        raise ValueError("Formato de token invalido; no se guardo")


def guardar(destino: Path, token: str) -> None:
    validar_token(token)
    destino.parent.mkdir(mode=0o700, exist_ok=True)
    info = destino.parent.lstat()
    if not stat.S_ISDIR(info.st_mode) or info.st_uid != os.geteuid() or info.st_mode & 0o077:
        raise PermissionError("El directorio debe ser privado y del usuario ejecutor")
    # O_EXCL impide reemplazar archivos y enlaces existentes. No hay token en argv.
    fd = os.open(destino, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    try:
        with os.fdopen(fd, "w", encoding="ascii") as archivo:
            archivo.write(token + "\n")
            archivo.flush()
            os.fsync(archivo.fileno())
    except OSError:
        destino.unlink()
        raise


def leer(destino: Path) -> str:
    fd = os.open(destino, os.O_RDONLY | os.O_NOFOLLOW)
    with os.fdopen(fd, encoding="ascii") as archivo:
        info = os.fstat(archivo.fileno())
        if not stat.S_ISREG(info.st_mode) or info.st_uid != os.geteuid() or info.st_mode & 0o077:
            raise PermissionError("El token debe ser un archivo privado del usuario ejecutor")
        token = archivo.read(4098).rstrip("\n")
    validar_token(token)
    return token


def comprobar(token: str) -> int:
    validar_token(token)
    peticion = {"method": "list-records", "params": {"domain": "example.org"}}
    # La cabecera entra por stdin, nunca en argv/env, archivos temporales o logs.
    # --disable omite curlrc; no se siguen redirecciones ni se usan proxies.
    resultado = subprocess.run(
        ["curl", "--disable", "--silent", "--show-error", "--fail", "--ipv6",
         "--interface", ORIGEN, "--noproxy", "*", "--proto", "=https",
         "--connect-timeout", "5", "--max-time", "20", "--max-filesize", "1048576",
         "--header", "@-", "--header", "Content-Type: application/json",
         "--data", json.dumps(peticion), "https://njal.la/api/1/"],
        input="Authorization: Njalla " + token + "\n", text=True,
        capture_output=True, timeout=25, check=False,
    )
    if resultado.returncode:
        # Ni stderr ni el cuerpo remoto se imprimen: pueden reflejar datos sensibles.
        print("FALLO: transporte/HTTP de API; token conservado. curl=" + str(resultado.returncode))
        return 2
    try:
        respuesta = json.loads(resultado.stdout)
    except json.JSONDecodeError:
        print("FALLO: API no devolvio JSON; token conservado")
        return 2
    if not isinstance(respuesta, dict) or "error" in respuesta:
        print("FALLO: API rechazo list-records; token conservado")
        return 2
    datos = respuesta.get("result")
    registros = datos.get("records") if isinstance(datos, dict) else None
    if not isinstance(registros, list) or not all(isinstance(r, dict) for r in registros):
        print("FALLO: formato de registros inesperado; token conservado")
        return 2
    propios = sum(r.get("name") in ("nas", "nas.example.org") for r in registros)
    print(f"OK: list-records por IPv6 autorizada; {len(registros)} registros visibles, {propios} de nas")
    print("Solo lectura comprobada; permisos de escritura y aislamiento aun no probados")
    return 0


def remoto(tipo: str, solo_comprobar: bool) -> int:
    if os.geteuid() != 0:
        raise PermissionError("Se requiere root en el NAS")
    destino = CARPETA / (tipo + ".token")
    if not solo_comprobar:
        token = sys.stdin.read(4098).rstrip("\n")
        guardar(destino, token)
        print(f"GUARDADO: {destino}, privado de root", flush=True)
    return comprobar(leer(destino))


def local(tipo: str, solo_comprobar: bool) -> int:
    token = ""
    if not solo_comprobar:
        # getpass no puede degradarse a una entrada visible.
        import warnings
        with warnings.catch_warnings():
            warnings.simplefilter("error", getpass.GetPassWarning)
            token = getpass.getpass(f"Pega el token {tipo} de Njalla (oculto) y pulsa Enter: ")
        validar_token(token)
    fuente = base64.b64encode(Path(__file__).read_bytes()).decode("ascii")
    codigo = "import base64; exec(base64.b64decode(" + repr(fuente) + "))"
    comando = "sudo -n python3 -c " + shlex.quote(codigo) + " --remoto --tipo " + tipo
    if solo_comprobar:
        comando += " --comprobar"
    resultado = subprocess.run(
        ["ssh", "-F", str(Path.home() / ".ssh" / "config"), "-o", "BatchMode=yes",
         "-o", "ConnectTimeout=8", "rpi", comando],
        input=token, text=True, capture_output=True, timeout=50, check=False,
    )
    # El programa remoto imprime solo estados y conteos; descartar stderr de SSH.
    print(resultado.stdout, end="")
    if resultado.returncode:
        print("No se completo el guardado/comprobacion. No vuelvas a pegar el token sin revisar el estado.")
    return resultado.returncode


def main() -> int:
    opciones = argparse.ArgumentParser(description=__doc__)
    opciones.add_argument("--tipo", choices=("ddns", "acme"), required=True)
    opciones.add_argument("--comprobar", action="store_true")
    opciones.add_argument("--remoto", action="store_true", help=argparse.SUPPRESS)
    args = opciones.parse_args()
    try:
        return remoto(args.tipo, args.comprobar) if args.remoto else local(args.tipo, args.comprobar)
    except (OSError, ValueError, subprocess.TimeoutExpired, getpass.GetPassWarning) as error:
        print("FALLO controlado: " + type(error).__name__ + "; revisar estado sin compartir el token")
        return 2
    except (KeyboardInterrupt, EOFError):
        print("\nEntrada cancelada; revisar estado antes de repetir")
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
