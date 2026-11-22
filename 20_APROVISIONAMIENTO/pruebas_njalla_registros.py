#!/usr/bin/env python3
"""Transacciones DNS simuladas. No usar credenciales ni red real."""

import copy
import subprocess
import unittest
from unittest.mock import patch

import njalla_registros as dns

IP_ANTES = "3fff:2a0:101e:3d82::38"
IP_NUEVA = "3fff:2a0:101e:3d83::38"


class Simulado(dns.Cliente):
    def __init__(self, modo: str = "normal") -> None:
        super().__init__("token-ficticio-sin-acceso-0000", IP_ANTES)
        self.registros = [{"id": "un-id", "name": "nas", "type": "AAAA",
                           "content": IP_ANTES, "ttl": 300}]
        self.modo = modo
        self.escrituras = 0

    def llamar(self, metodo: str, parametros: dict) -> dict:
        if metodo == "list-records":
            return {"records": copy.deepcopy(self.registros)}
        assert metodo == "edit-record"
        self.escrituras += 1
        if self.modo == "denegado":
            raise dns.ErrorDNS("API rechazo la operacion")
        if self.modo == "timeout-antes" and self.escrituras == 1:
            raise dns.Transitorio("timeout")
        if self.modo == "timeout-siempre":
            raise dns.Transitorio("timeout")
        if self.modo == "conflicto":
            self.registros[0]["content"] = "3fff:2a0:101e:3d84::38"
            raise dns.Transitorio("timeout")
        if self.modo == "sin-efecto":
            return {}
        self.registros[0]["content"] = parametros["content"]
        if self.modo == "timeout-despues":
            raise dns.Transitorio("timeout")
        return {}


class Registros(unittest.TestCase):
    def test_edicion_y_ya_aplicado(self) -> None:
        cliente = Simulado()
        self.assertTrue(dns.editar(cliente, "un-id", "nas", "AAAA", IP_NUEVA))
        self.assertFalse(dns.editar(cliente, "un-id", "nas", "AAAA", IP_NUEVA))
        self.assertEqual(cliente.escrituras, 1)

    def test_ambiguo_aplicado_no_repite(self) -> None:
        cliente = Simulado("timeout-despues")
        self.assertTrue(dns.editar(cliente, "un-id", "nas", "AAAA", IP_NUEVA))
        self.assertEqual(cliente.escrituras, 1)

    def test_reversion_con_valor_esperado(self) -> None:
        cliente = Simulado()
        dns.editar(cliente, "un-id", "nas", "AAAA", IP_NUEVA, IP_ANTES)
        self.assertTrue(dns.editar(cliente, "un-id", "nas", "AAAA", IP_ANTES, IP_NUEVA))
        self.assertEqual(cliente.registros[0]["content"], IP_ANTES)
        self.assertEqual(cliente.escrituras, 2)

    def test_reversion_con_valor_esperado(self) -> None:
        cliente = Simulado()
        dns.editar(cliente, "un-id", "nas", "AAAA", IP_NUEVA, IP_ANTES)
        self.assertTrue(dns.editar(cliente, "un-id", "nas", "AAAA", IP_ANTES, IP_NUEVA))
        self.assertEqual(cliente.registros[0]["content"], IP_ANTES)
        self.assertEqual(cliente.escrituras, 2)

    @patch.object(dns, "esperar")
    def test_ambiguo_no_aplicado_reintenta(self, _esperar) -> None:
        cliente = Simulado("timeout-antes")
        self.assertTrue(dns.editar(cliente, "un-id", "nas", "AAAA", IP_NUEVA))
        self.assertEqual(cliente.escrituras, 2)

    @patch.object(dns, "esperar")
    def test_limite_reintentos(self, _esperar) -> None:
        cliente = Simulado("timeout-siempre")
        with self.assertRaises(dns.ErrorDNS):
            dns.editar(cliente, "un-id", "nas", "AAAA", IP_NUEVA)
        self.assertEqual(cliente.escrituras, 3)

    def test_denegado_conflicto_y_sin_efecto(self) -> None:
        for modo in ("denegado", "conflicto", "sin-efecto"):
            with self.subTest(modo=modo):
                cliente = Simulado(modo)
                with self.assertRaises(dns.ErrorDNS):
                    dns.editar(cliente, "un-id", "nas", "AAAA", IP_NUEVA)
                self.assertEqual(cliente.escrituras, 1)

    def test_sin_registro_duplicado_id_o_tipo_incorrecto(self) -> None:
        for caso in ("ausente", "duplicado", "id", "tipo", "cname"):
            cliente = Simulado()
            if caso == "ausente":
                cliente.registros = []
            elif caso == "duplicado":
                cliente.registros *= 2
            elif caso == "id":
                cliente.registros[0]["id"] = "otro"
            elif caso == "tipo":
                cliente.registros[0]["type"] = "A"
            else:
                cliente.registros.append({"name": "nas", "type": "CNAME"})
            with self.subTest(caso=caso), self.assertRaises(dns.ErrorDNS):
                dns.editar(cliente, "un-id", "nas", "AAAA", IP_NUEVA)
            self.assertEqual(cliente.escrituras, 0)

    def test_txt_solo_limpia_valor_propio(self) -> None:
        cliente = Simulado()
        cliente.registros[0].update(name="_acme-challenge.nas", type="TXT", content="A" * 43)
        with self.assertRaises(dns.ErrorDNS):
            dns.editar(cliente, "un-id", "_acme-challenge.nas", "TXT", dns.INACTIVO, "B" * 43)
        self.assertEqual(cliente.escrituras, 0)
        self.assertTrue(dns.editar(cliente, "un-id", "_acme-challenge.nas", "TXT", dns.INACTIVO, "A" * 43))
        self.assertFalse(dns.editar(cliente, "un-id", "_acme-challenge.nas", "TXT", dns.INACTIVO, "A" * 43))

    def test_fuera_de_alcance(self) -> None:
        for nombre, tipo, valor in (("@", "AAAA", IP_NUEVA), ("nas", "A", "192.0.2.1"),
                                    ("nas", "AAAA", "::1"), ("nas", "AAAA", "fd00::38"),
                                    ("_acme-challenge.nas", "TXT", "")):
            cliente = Simulado()
            with self.subTest(nombre=nombre, tipo=tipo), self.assertRaises(dns.ErrorDNS):
                dns.editar(cliente, "un-id", nombre, tipo, valor)
            self.assertEqual(cliente.escrituras, 0)

    def test_transporte_no_refleja_secreto(self) -> None:
        cliente = dns.Cliente("token-ficticio-sin-acceso-0000", IP_ANTES)
        for codigo in (401, 403, 429, 503):
            respuesta = subprocess.CompletedProcess([], 0, cliente._token + "\n" + str(codigo), "")
            with patch.object(dns.subprocess, "run", return_value=respuesta) as ejecutar:
                with self.subTest(codigo=codigo), self.assertRaises(dns.ErrorDNS) as error:
                    cliente.llamar("list-records", {"domain": dns.ZONA})
                self.assertNotIn(cliente._token, str(error.exception))
                self.assertNotIn(cliente._token, str(ejecutar.call_args.args))

    def test_rechaza_metodos_extra_y_otras_zonas(self) -> None:
        cliente = Simulado()
        for metodo, zona in (("add-record", dns.ZONA), ("remove-record", dns.ZONA),
                             ("list-records", "otro.org")):
            with self.subTest(metodo=metodo), self.assertRaises(dns.ErrorDNS):
                dns.Cliente.llamar(cliente, metodo, {"domain": zona})


if __name__ == "__main__":
    unittest.main()
