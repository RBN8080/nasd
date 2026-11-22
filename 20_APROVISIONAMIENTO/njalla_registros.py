#!/usr/bin/env python3
"""Nucleo local para registros NAS existentes; no instala ni activa servicios.

Contrato edit-record contrastado con libdns/njalla types.go, 2026-11-22.
Pendiente probar escrituras con Njalla en ventana autorizada. No ejecutar
pruebas de escritura contra DNS real como parte de la preparacion local.
"""

import ipaddress
import json
import random
import re
import subprocess
import time
from dataclasses import dataclass
from typing import Any

from token_njalla import validar_token

ZONA = "example.org"
PERMITIDOS = {
    ("nas", "AAAA"),
    ("_acme-challenge.nas", "TXT"),
    ("vpn-nas", "A"),
    ("vpn-nas", "AAAA"),
}
INACTIVO = "nas-acme-inactivo"


class ErrorDNS(RuntimeError):
    """Error seguro: nunca contiene cuerpos remotos ni credenciales."""


class Transitorio(ErrorDNS):
    """La llamada pudo aplicarse aunque no se recibiera respuesta."""


@dataclass(frozen=True)
class Registro:
    id: str
    nombre: str
    tipo: str
    contenido: str
    ttl: int


class Cliente:
    def __init__(self, token: str, origen: str) -> None:
        validar_token(token)
        direccion = ipaddress.IPv6Address(origen)
        if not direccion.is_global:
            raise ErrorDNS("El origen API debe ser IPv6 global")
        self._token = token
        self.origen = str(direccion)

    def llamar(self, metodo: str, parametros: dict[str, Any]) -> Any:
        if (
            metodo not in ("list-records", "edit-record")
            or parametros.get("domain") != ZONA
        ):
            raise ErrorDNS("Metodo o zona fuera del alcance NAS")
        if metodo == "edit-record":
            if (parametros.get("name"), parametros.get("type")) not in PERMITIDOS:
                raise ErrorDNS("Registro fuera del alcance NAS")
            if (
                set(parametros) != {"domain", "id", "name", "type", "content", "ttl"}
                or not isinstance(parametros["id"], str)
                or not parametros["id"]
                or type(parametros["ttl"]) is not int
                or not 1 <= parametros["ttl"] <= 604800
            ):
                raise ErrorDNS("Parametros de edicion inesperados")
            normalizar(parametros["type"], parametros["content"])
        try:
            respuesta = subprocess.run(
                [
                    "curl",
                    "--disable",
                    "--silent",
                    "--show-error",
                    "--ipv6",
                    "--interface",
                    self.origen,
                    "--noproxy",
                    "*",
                    "--proto",
                    "=https",
                    "--connect-timeout",
                    "5",
                    "--max-time",
                    "20",
                    "--max-filesize",
                    "1048576",
                    "--header",
                    "@-",
                    "--header",
                    "Content-Type: application/json",
                    "--data",
                    json.dumps({"method": metodo, "params": parametros}),
                    "--write-out",
                    "\n%{http_code}",
                    "https://njal.la/api/1/",
                ],
                input="Authorization: Njalla " + self._token + "\n",
                text=True,
                capture_output=True,
                timeout=25,
                check=False,
            )
        except subprocess.TimeoutExpired:
            raise Transitorio("Tiempo de transporte agotado") from None
        if respuesta.returncode in (5, 6, 7, 18, 28, 52, 55, 56):
            raise Transitorio("Transporte API interrumpido")
        if respuesta.returncode:
            raise ErrorDNS("Transporte API rechazado")
        try:
            cuerpo, estado = respuesta.stdout.rsplit("\n", 1)
            codigo = int(estado)
        except ValueError:
            raise Transitorio("Respuesta HTTP incompleta") from None
        if codigo == 429 or 500 <= codigo <= 599:
            raise Transitorio("API temporalmente no disponible")
        if codigo != 200:
            raise ErrorDNS("API no autorizada o solicitud rechazada")
        try:
            datos = json.loads(cuerpo)
        except json.JSONDecodeError:
            raise Transitorio("Respuesta JSON incompleta") from None
        if not isinstance(datos, dict):
            raise Transitorio("Respuesta API inesperada")
        if datos.get("error") is not None:
            error = datos["error"]
            codigo_api = error.get("code") if isinstance(error, dict) else None
            if type(codigo_api) is int and (
                codigo_api == 429 or 500 <= codigo_api <= 599
            ):
                raise Transitorio("API reporto un fallo temporal")
            raise ErrorDNS("API rechazo la operacion")
        if "result" not in datos:
            raise Transitorio("Respuesta API sin resultado")
        return datos["result"]

    def listar(self) -> list[dict[str, Any]]:
        for intento in range(3):
            try:
                datos = self.llamar("list-records", {"domain": ZONA})
                registros = datos.get("records") if isinstance(datos, dict) else None
                if not isinstance(registros, list) or not all(
                    isinstance(r, dict) for r in registros
                ):
                    raise ErrorDNS("Formato de registros inesperado")
                return registros
            except Transitorio:
                if intento == 2:
                    raise
                esperar(intento)
        raise ErrorDNS("Lectura no completada")


def esperar(intento: int) -> None:
    time.sleep(2**intento + random.uniform(0, 0.5))


def seleccionar(
    registros: list[dict[str, Any]], identificador: str, nombre: str, tipo: str
) -> Registro:
    if (nombre, tipo) not in PERMITIDOS or not identificador:
        raise ErrorDNS("Registro fuera del alcance NAS o sin ID")
    encontrados = []
    for dato in registros:
        n = dato.get("name")
        if n == nombre + "." + ZONA:
            n = nombre
        if str(dato.get("id")) == identificador and (
            n != nombre or dato.get("type") != tipo
        ):
            raise ErrorDNS("ID pertenece a otro nombre o tipo")
        if n == nombre and dato.get("type") == tipo:
            encontrados.append(dato)
        if n == nombre and dato.get("type") == "CNAME":
            raise ErrorDNS("CNAME incompatible con el registro NAS")
    if len(encontrados) != 1 or str(encontrados[0].get("id")) != identificador:
        raise ErrorDNS("Falta registro exacto o hay duplicados; no se crea ninguno")
    dato = encontrados[0]
    if not isinstance(dato.get("content"), str) or type(dato.get("ttl")) is not int:
        raise ErrorDNS("Contenido o TTL de registro inesperado")
    if not 1 <= dato["ttl"] <= 604800:
        raise ErrorDNS("TTL fuera de limites")
    return Registro(identificador, nombre, tipo, dato["content"], dato["ttl"])


def normalizar(tipo: str, valor: str) -> str:
    if tipo == "A":
        try:
            direccion_v4 = ipaddress.IPv4Address(valor)
        except ipaddress.AddressValueError:
            raise ErrorDNS("A invalido") from None
        if not direccion_v4.is_global:
            raise ErrorDNS("A no global")
        return str(direccion_v4)
    if tipo == "AAAA":
        try:
            direccion = ipaddress.IPv6Address(valor)
        except ipaddress.AddressValueError:
            raise ErrorDNS("AAAA invalido") from None
        if not direccion.is_global:
            raise ErrorDNS("AAAA no global")
        return str(direccion)
    if tipo == "TXT" and (
        valor == INACTIVO or re.fullmatch(r"[A-Za-z0-9_-]{43}", valor)
    ):
        return valor
    raise ErrorDNS("Valor DNS fuera del alcance NAS")


def editar(
    cliente: Cliente,
    identificador: str,
    nombre: str,
    tipo: str,
    nuevo: str,
    esperado: str | None = None,
) -> bool:
    """Editar por ID sin crear/borrar; reconciliar respuesta ambigua antes de repetir.

    El llamador debe serializar operaciones y exigir la autorizacion de activacion.
    Devuelve False si ya estaba aplicado. Njalla no ofrece compare-and-swap: sigue
    siendo necesaria una ventana sin otros escritores sobre estos registros.
    """
    nuevo = normalizar(tipo, nuevo)
    previo = seleccionar(cliente.listar(), identificador, nombre, tipo)
    contenido = normalizar(tipo, previo.contenido)
    if contenido == nuevo:
        return False
    if esperado is not None and contenido != normalizar(tipo, esperado):
        raise ErrorDNS("El valor actual no pertenece a esta operacion")
    for intento in range(3):
        ambiguo = False
        try:
            cliente.llamar(
                "edit-record",
                {
                    "domain": ZONA,
                    "id": identificador,
                    "name": nombre,
                    "type": tipo,
                    "content": nuevo,
                    "ttl": previo.ttl,
                },
            )
        except Transitorio:
            ambiguo = True
        actual = seleccionar(cliente.listar(), identificador, nombre, tipo)
        if actual.ttl != previo.ttl:
            raise ErrorDNS("El TTL cambio durante la operacion")
        valor = normalizar(tipo, actual.contenido)
        if valor == nuevo:
            return True
        if valor != contenido:
            raise ErrorDNS("Otro escritor cambio el registro; no se sobrescribe")
        if not ambiguo:
            raise ErrorDNS("API acepto sin aplicar el valor solicitado")
        if intento < 2:
            esperar(intento)
    raise ErrorDNS("Escritura no confirmada tras reconciliar")
