#!/usr/bin/env python3
"""Ensayos de ganchos, propagacion y cambio TLS en directorios efimeros."""

import os
import subprocess
import tempfile
import threading
import time
import unittest
from contextlib import nullcontext
from pathlib import Path
from unittest.mock import patch

import nas_dominio as dominio
import nas_tls as tls
from njalla_registros import INACTIVO, ErrorDNS
from pruebas_njalla_registros import IP_ANTES, IP_NUEVA, Simulado

CONF = dominio.Config(IP_ANTES, "eth0", "un-id", "un-id", True)


class Integracion(unittest.TestCase):
    def test_respuesta_autoritativa_exacta(self) -> None:
        cabecera = (
            ";; ->>HEADER<<- opcode: QUERY, status: NOERROR, id: 1\n;; flags: qr aa;"
        )
        fqdn = "_acme-challenge.nas.example.org"
        fila = f'{fqdn}. 300 IN TXT "{"A" * 43}"'
        self.assertTrue(
            dominio.respuesta_dns(cabecera + "\n" + fila, fqdn, "TXT", "A" * 43)
        )
        for texto in (
            fila,
            cabecera + "\n" + fila + "\n" + fila,
            (cabecera + "\n" + fila).replace("qr aa", "qr ra"),
            (cabecera + "\n" + fila).replace("NOERROR", "SERVFAIL"),
        ):
            self.assertFalse(dominio.respuesta_dns(texto, fqdn, "TXT", "A" * 43))

    @patch.object(dominio, "propagado")
    def test_auth_cleanup_y_otra_validacion(self, propagado) -> None:
        api = Simulado()
        api.registros[0].update(
            name="_acme-challenge.nas", type="TXT", content=INACTIVO
        )
        with tempfile.TemporaryDirectory() as temporal:
            carpeta = Path(temporal)
            dominio.autenticar(api, CONF, "A" * 43, carpeta)
            self.assertEqual(api.registros[0]["content"], "A" * 43)
            with self.assertRaises(ErrorDNS):
                dominio.autenticar(api, CONF, "B" * 43, carpeta)
            with self.assertRaises(ErrorDNS):
                dominio.limpiar(api, CONF, "B" * 43, carpeta)
            dominio.limpiar(api, CONF, "A" * 43, carpeta)
            self.assertEqual(api.registros[0]["content"], INACTIVO)
            self.assertFalse((carpeta / "validacion").exists())
            dominio.limpiar(api, CONF, "A" * 43, carpeta)
            self.assertEqual(api.escrituras, 2)
            self.assertEqual(propagado.call_count, 2)

    @patch.object(dominio, "propagado", side_effect=ErrorDNS("sin propagar"))
    def test_auth_fallido_conserva_estado_para_limpieza(self, _propagado) -> None:
        api = Simulado()
        api.registros[0].update(
            name="_acme-challenge.nas", type="TXT", content=INACTIVO
        )
        with tempfile.TemporaryDirectory() as temporal:
            carpeta = Path(temporal)
            with self.assertRaises(ErrorDNS):
                dominio.autenticar(api, CONF, "A" * 43, carpeta)
            self.assertEqual(dominio.privado(carpeta / "validacion").strip(), "A" * 43)
            with self.assertRaises(ErrorDNS):
                dominio.limpiar(api, CONF, "A" * 43, carpeta)
            self.assertTrue((carpeta / "validacion").exists())

    def test_no_limpia_txt_tras_perder_estado(self) -> None:
        api = Simulado()
        api.registros[0].update(
            name="_acme-challenge.nas", type="TXT", content="A" * 43
        )
        with tempfile.TemporaryDirectory() as temporal, self.assertRaises(ErrorDNS):
            dominio.limpiar(api, CONF, "A" * 43, Path(temporal))
        self.assertEqual(api.escrituras, 0)

    def test_bloqueo_entre_invocaciones(self) -> None:
        with tempfile.TemporaryDirectory() as temporal:
            carpeta = Path(temporal)
            with (
                dominio.bloquear(carpeta),
                self.assertRaises(BlockingIOError),
                dominio.bloquear(carpeta),
            ):
                self.fail("Segundo escritor entro al bloqueo")

    @patch.object(dominio.subprocess, "run")
    def test_no_publica_si_prefijo_no_coincide(self, ejecutar) -> None:
        ejecutar.return_value = subprocess.CompletedProcess([], 0, "[]", "")
        with self.assertRaises(ErrorDNS):
            dominio.origen_presente(CONF)

    def test_config_ids_pendientes_y_permisos(self) -> None:
        with tempfile.TemporaryDirectory() as temporal:
            archivo = Path(temporal) / "config"
            archivo.write_text(
                f'origen="{IP_ANTES}"\ninterfaz="eth0"\naaaa_id=""\ntxt_id="x"\nconvivencia=true\n'
            )
            archivo.chmod(0o600)
            self.assertEqual(dominio.Config.cargar(archivo).aaaa_id, "")
            archivo.chmod(0o644)
            with self.assertRaises(ErrorDNS):
                dominio.Config.cargar(archivo)

    @patch.object(tls, "validar_par")
    def test_activacion_conjunta_y_reversion(self, _validar) -> None:
        with tempfile.TemporaryDirectory() as temporal:
            base = Path(temporal) / "tls"
            origen = Path(temporal) / "emitido"
            base.mkdir()
            origen.mkdir()
            for nombre in ("fullchain.pem", "privkey.pem"):
                (base / nombre).write_text("anterior")
                (origen / nombre).write_text("nuevo")
            tls.activar(origen, {dominio.NUEVO}, base, os.getegid())
            for nombre in ("fullchain.pem", "privkey.pem"):
                self.assertEqual((base / nombre).read_text(), "nuevo")
                self.assertTrue((base / nombre).is_symlink())
            self.assertEqual((base / "privkey.pem").stat().st_mode & 0o777, 0o640)
            tls.volver(base)
            self.assertEqual((base / "fullchain.pem").read_text(), "anterior")
            self.assertEqual((base / "privkey.pem").read_text(), "anterior")

    @patch.object(tls, "validar_par", side_effect=ErrorDNS("certificado rechazado"))
    def test_certificado_rechazado_no_cambia_par(self, _validar) -> None:
        with tempfile.TemporaryDirectory() as temporal:
            base = Path(temporal)
            (base / "fullchain.pem").write_text("anterior")
            with self.assertRaises(ErrorDNS):
                tls.activar(base, {dominio.NUEVO}, base, os.getegid())
            self.assertEqual((base / "fullchain.pem").read_text(), "anterior")
            self.assertFalse((base / "actual").exists())

    def test_openssl_rechaza_certificado_invalido(self) -> None:
        with tempfile.TemporaryDirectory() as temporal:
            base = Path(temporal)
            (base / "fullchain.pem").write_text("no es certificado")
            with self.assertRaises(ErrorDNS):
                tls.validar_par(base, {dominio.NUEVO})

    @patch.object(tls, "validar_par")
    def test_conversion_interrumpida_conserva_par_y_reintenta(self, _validar) -> None:
        with tempfile.TemporaryDirectory() as temporal:
            base = Path(temporal) / "tls"
            origen = Path(temporal) / "nuevo"
            base.mkdir()
            origen.mkdir()
            for nombre in ("fullchain.pem", "privkey.pem"):
                (base / nombre).write_text("anterior")
                (origen / nombre).write_text("nuevo")
            real = tls.enlace_atomico

            def interrumpir(carpeta: Path, nombre: str, destino: str) -> None:
                if nombre == "privkey.pem":
                    raise OSError("Interrupcion simulada")
                real(carpeta, nombre, destino)

            with (
                patch.object(tls, "enlace_atomico", side_effect=interrumpir),
                self.assertRaises(OSError),
            ):
                tls.activar(origen, {dominio.NUEVO}, base, os.getegid())
            for nombre in ("fullchain.pem", "privkey.pem"):
                self.assertEqual((base / nombre).read_text(), "anterior")
            tls.activar(origen, {dominio.NUEVO}, base, os.getegid())
            for nombre in ("fullchain.pem", "privkey.pem"):
                self.assertEqual((base / nombre).read_text(), "nuevo")

    @patch.object(tls.os, "geteuid", return_value=0)
    @patch.object(tls, "bloquear", return_value=nullcontext())
    @patch.object(tls, "privado", return_value=str(tls.LIN_TRANSICION))
    @patch.object(tls.Config, "cargar", return_value=CONF)
    @patch.object(tls, "activar")
    def test_despliegue_solo_linaje_elegido(self, activar, *_mocks) -> None:
        with patch.object(tls.sys, "argv", ["nas_tls", "desplegar"]):
            for linaje in (
                str(tls.LIN_ANTIGUO),
                "/etc/nas/njalla/acme-ensayo/live/nas-transicion",
            ):
                with patch.dict(os.environ, {"RENEWED_LINEAGE": linaje}):
                    self.assertEqual(tls.main(), 0)
                activar.assert_not_called()
            with patch.dict(os.environ, {"RENEWED_LINEAGE": str(tls.LIN_TRANSICION)}):
                self.assertEqual(tls.main(), 0)
            activar.assert_called_once_with(
                tls.LIN_TRANSICION, {dominio.NUEVO, dominio.ANTIGUO}
            )

    def test_reversion_no_sigue_destino_externo(self) -> None:
        with tempfile.TemporaryDirectory() as temporal:
            base = Path(temporal)
            (base / "anterior").symlink_to("/tmp")
            with self.assertRaises(ErrorDNS):
                tls.volver(base)

    def test_validacion_openssl_con_ca_efimera(self) -> None:
        # Certificados ficticios generados fuera del repositorio: comprobar la
        # cadena, ambos SAN, pareja de clave y margen de caducidad de verdad.
        with tempfile.TemporaryDirectory() as temporal:
            base = Path(temporal)
            confianza = base / "ca"
            confianza.mkdir()

            def ejecutar(*args: str) -> None:
                subprocess.run(
                    ["openssl", *args],
                    cwd=base,
                    capture_output=True,
                    check=True,
                    timeout=15,
                )

            ejecutar(
                "req",
                "-x509",
                "-newkey",
                "ec",
                "-pkeyopt",
                "ec_paramgen_curve:P-256",
                "-noenc",
                "-keyout",
                "ca.key",
                "-out",
                "ca/ca.pem",
                "-days",
                "30",
                "-subj",
                "/CN=CA ficticia NAS",
            )
            ejecutar(
                "req",
                "-new",
                "-newkey",
                "ec",
                "-pkeyopt",
                "ec_paramgen_curve:P-256",
                "-noenc",
                "-keyout",
                "privkey.pem",
                "-out",
                "peticion.pem",
                "-subj",
                "/CN=nas.example.org",
                "-addext",
                "subjectAltName=DNS:" + dominio.NUEVO + ",DNS:" + dominio.ANTIGUO,
            )
            (base / "extensiones").write_text(
                "basicConstraints=CA:FALSE\nextendedKeyUsage=serverAuth\n"
            )
            ejecutar(
                "x509",
                "-req",
                "-in",
                "peticion.pem",
                "-CA",
                "ca/ca.pem",
                "-CAkey",
                "ca.key",
                "-CAcreateserial",
                "-out",
                "cert.pem",
                "-days",
                "30",
                "-copy_extensions",
                "copy",
                "-extfile",
                "extensiones",
            )
            (base / "chain.pem").write_bytes((confianza / "ca.pem").read_bytes())
            (base / "fullchain.pem").write_bytes(
                (base / "cert.pem").read_bytes() + (base / "chain.pem").read_bytes()
            )
            ejecutar("rehash", "ca")
            nombres = {dominio.NUEVO, dominio.ANTIGUO}
            tls.validar_par(base, nombres, confianza)
            with self.assertRaises(ErrorDNS):
                tls.validar_par(base, {dominio.NUEVO}, confianza)
            clave = (base / "privkey.pem").read_bytes()
            (base / "privkey.pem").write_bytes((base / "ca.key").read_bytes())
            with self.assertRaises(ErrorDNS):
                tls.validar_par(base, nombres, confianza)
            (base / "privkey.pem").write_bytes(clave)
            ejecutar(
                "x509",
                "-req",
                "-in",
                "peticion.pem",
                "-CA",
                "ca/ca.pem",
                "-CAkey",
                "ca.key",
                "-out",
                "cert.pem",
                "-days",
                "1",
                "-copy_extensions",
                "copy",
                "-extfile",
                "extensiones",
            )
            (base / "fullchain.pem").write_bytes(
                (base / "cert.pem").read_bytes() + (base / "chain.pem").read_bytes()
            )
            with self.assertRaises(ErrorDNS):
                tls.validar_par(base, nombres, confianza)

    def test_respuesta_dns_rechaza_valor_inesperado_sin_abortar(self) -> None:
        cabecera = (
            ";; ->>HEADER<<- opcode: QUERY, status: NOERROR, id: 1\n;; flags: qr aa;"
        )
        fqdn = "_acme-challenge.nas.example.org"
        for fila in (
            f'{fqdn}. 300 IN TXT "otro-valor-no-acme"',
            f'{fqdn}. 300 IN TXT "sin-comilla-de-cierre',
        ):
            self.assertFalse(
                dominio.respuesta_dns(cabecera + "\n" + fila, fqdn, "TXT", "A" * 43)
            )

    @patch.object(dominio.time, "sleep")
    def test_propagado_reintenta_tras_timeout_de_dig(self, _sleep) -> None:
        fqdn = "_acme-challenge.nas.example.org"
        ok = subprocess.CompletedProcess(
            [],
            0,
            ";; ->>HEADER<<- opcode: QUERY, status: NOERROR, id: 1\n"
            ";; flags: qr aa;\n"
            f'{fqdn}. 300 IN TXT "{"A" * 43}"\n',
            "",
        )
        with patch.object(
            dominio.subprocess,
            "run",
            side_effect=[subprocess.TimeoutExpired(["dig"], 5), ok, ok, ok],
        ) as ejecutar:
            dominio.propagado(fqdn, "TXT", "A" * 43, segundos=30)
        self.assertEqual(ejecutar.call_count, 4)

    def test_bloqueo_espera_acotada_y_libera(self) -> None:
        with tempfile.TemporaryDirectory() as temporal:
            carpeta = Path(temporal)
            listo = threading.Event()

            def retener() -> None:
                with dominio.bloquear(carpeta):
                    listo.set()
                    time.sleep(0.25)

            hilo = threading.Thread(target=retener)
            hilo.start()
            listo.wait(timeout=2)
            with self.assertRaises(BlockingIOError), dominio.bloquear(carpeta, 0.05):
                self.fail("Entro antes de vencer el bloqueo")
            with dominio.bloquear(carpeta, 2.0):
                pass
            hilo.join(timeout=2)

    @patch.object(tls, "validar_par")
    def test_pares_conserva_permisos_con_umask_estricto(self, _validar) -> None:
        with tempfile.TemporaryDirectory() as temporal:
            base = Path(temporal) / "tls"
            origen = Path(temporal) / "emitido"
            base.mkdir()
            origen.mkdir()
            for nombre in ("fullchain.pem", "privkey.pem"):
                (base / nombre).write_text("anterior")
                (origen / nombre).write_text("nuevo")
            previo = os.umask(0o077)
            try:
                tls.activar(origen, {dominio.NUEVO}, base, os.getegid())
            finally:
                os.umask(previo)
            self.assertEqual((base / "pares").stat().st_mode & 0o777, 0o755)
            self.assertEqual((base / "actual").stat().st_mode & 0o777, 0o750)
            self.assertEqual((base / "fullchain.pem").stat().st_mode & 0o777, 0o644)
            self.assertEqual((base / "privkey.pem").stat().st_mode & 0o777, 0o640)

    def test_ipv4_publica_respaldo_y_rechazo_privada(self) -> None:
        with patch.object(
            dominio.subprocess,
            "run",
            side_effect=[
                subprocess.CompletedProcess([], 0, "192.168.1.38\n", ""),
                subprocess.CompletedProcess([], 0, "9.9.9.9\n", ""),
            ],
        ):
            self.assertEqual(dominio.ipv4_publica(), "9.9.9.9")
        with (
            patch.object(
                dominio.subprocess,
                "run",
                side_effect=[
                    subprocess.TimeoutExpired(["curl"], 20),
                    subprocess.CompletedProcess([], 22, "", "502"),
                ],
            ),
            self.assertRaises(ErrorDNS),
        ):
            dominio.ipv4_publica()

    def test_config_opcional_vpn_retrocompatible(self) -> None:
        with tempfile.TemporaryDirectory() as temporal:
            archivo = Path(temporal) / "dominio.toml"
            archivo.write_text(
                f'origen="{IP_ANTES}"\ninterfaz="eth0"\naaaa_id="1"\ntxt_id="2"\n'
                'vpn_a_id="3"\nvpn_aaaa_id="4"\nconvivencia=false\n'
            )
            archivo.chmod(0o600)
            conf = dominio.Config.cargar(archivo)
            self.assertEqual((conf.vpn_a_id, conf.vpn_aaaa_id), ("3", "4"))

    @patch.object(dominio, "origen_presente")
    @patch.object(dominio, "propagado")
    @patch.object(dominio, "ipv4_publica", return_value="9.9.9.9")
    def test_ddns_actualiza_web_y_vpn(self, _ipv4, propagado, _origen) -> None:
        api_web = Simulado()
        api_web.registros = [
            {
                "id": "w1",
                "name": "nas",
                "type": "AAAA",
                "content": IP_ANTES,
                "ttl": 300,
            }
        ]
        api_vpn = Simulado()
        api_vpn.registros = [
            {
                "id": "v1",
                "name": "vpn-nas",
                "type": "A",
                "content": "1.1.1.1",
                "ttl": 300,
            },
            {
                "id": "v2",
                "name": "vpn-nas",
                "type": "AAAA",
                "content": IP_ANTES,
                "ttl": 300,
            },
        ]
        conf = dominio.Config(IP_NUEVA, "eth0", "w1", "t1", True, "v1", "v2")

        def obtener_cliente(_c: dominio.Config, uso: str) -> Simulado:
            return api_web if uso == "ddns" else api_vpn

        with patch.object(dominio, "cliente", side_effect=obtener_cliente):
            dominio.ejecutar("ddns", conf)
        self.assertEqual(api_web.registros[0]["content"], IP_NUEVA)
        self.assertEqual(api_vpn.registros[0]["content"], "9.9.9.9")
        self.assertEqual(api_vpn.registros[1]["content"], IP_NUEVA)
        self.assertEqual(propagado.call_count, 3)

    def test_config_autoridad_propia(self) -> None:
        base = (
            f'origen="{IP_ANTES}"\ninterfaz="eth0"\naaaa_id="1"\ntxt_id="2"\n'
            "convivencia=false\n"
        )
        validos = {
            "": ("", (), "njalla"),
            'primario="198.51.100.7"\npropios=["198.51.100.7","ns2.he.net"]\n': (
                "198.51.100.7",
                ("198.51.100.7", "ns2.he.net"),
                "njalla",
            ),
            'primario="198.51.100.7"\npropios=["198.51.100.7"]\nautoridad="propia"\n': (
                "198.51.100.7",
                ("198.51.100.7",),
                "propia",
            ),
        }
        invalidos = (
            'autoridad="propia"\n',
            'primario="198.51.100.7"\n',
            'primario="198.51.100.7"\npropios=["a b"]\n',
            'primario="198.51.100.7"\npropios=["198.51.100.7"]\nautoridad="otra"\n',
        )
        with tempfile.TemporaryDirectory() as temporal:
            archivo = Path(temporal) / "dominio.toml"
            for extra, esperado in validos.items():
                archivo.write_text(base + extra)
                archivo.chmod(0o600)
                conf = dominio.Config.cargar(archivo)
                self.assertEqual(
                    (conf.primario, conf.propios, conf.autoridad), esperado
                )
            for extra in invalidos:
                archivo.write_text(base + extra)
                archivo.chmod(0o600)
                with self.assertRaises(ErrorDNS):
                    dominio.Config.cargar(archivo)

    @patch.object(dominio, "origen_presente")
    @patch.object(dominio, "propagado")
    @patch.object(dominio, "propio", side_effect=ErrorDNS("VPS caido"))
    def test_copia_propia_no_frena_a_njalla(self, propio, propagado, _origen) -> None:
        api = Simulado()
        conf = dominio.Config(
            IP_NUEVA, "eth0", "un-id", "t1", True,
            primario="198.51.100.7", propios=("198.51.100.7",),
        )
        with patch.object(dominio, "cliente", return_value=api):
            dominio.ejecutar("ddns", conf)
        self.assertEqual(api.registros[0]["content"], IP_NUEVA)
        propio.assert_called_once_with(
            conf, "ddns", "nas.example.org", "AAAA", IP_NUEVA
        )
        self.assertEqual(propagado.call_count, 1)

    @patch.object(dominio, "propagado")
    def test_la_que_manda_corta_la_otra_avisa(self, _propagado) -> None:
        conf = dominio.Config(
            IP_ANTES, "eth0", "un-id", "t1", True,
            primario="198.51.100.7", propios=("198.51.100.7",), autoridad="propia",
        )
        orden: list[str] = []
        with (
            patch.object(dominio, "propio", side_effect=lambda *a: orden.append("p")),
            patch.object(dominio, "editar", side_effect=ErrorDNS("Njalla caido")),
        ):
            dominio.escribir(Simulado(), conf, "ddns", "un-id", "nas", "AAAA", IP_NUEVA)
        self.assertEqual(orden, ["p"])
        with (
            patch.object(dominio, "propio", side_effect=ErrorDNS("VPS caido")),
            patch.object(dominio, "editar") as editar,
        ):
            with self.assertRaises(ErrorDNS):
                dominio.escribir(
                    Simulado(), conf, "ddns", "un-id", "nas", "AAAA", IP_NUEVA
                )
            editar.assert_not_called()

    def test_propio_no_reescribe_si_ya_esta(self) -> None:
        conf = dominio.Config(
            IP_ANTES, "eth0", "un-id", "t1", True,
            primario="198.51.100.7", propios=("198.51.100.7",),
        )
        with (
            patch.object(dominio, "propagado") as propagado,
            patch.object(dominio.subprocess, "run") as ejecutar,
        ):
            dominio.propio(conf, "vpn", "vpn-nas.example.org", "A", "9.9.9.9")
        ejecutar.assert_not_called()
        propagado.assert_called_once_with(
            "vpn-nas.example.org", "A", "9.9.9.9",
            segundos=0, servidores=("198.51.100.7",),
        )

    def test_propio_firma_con_la_clave_de_su_uso(self) -> None:
        conf = dominio.Config(
            IP_ANTES, "eth0", "un-id", "t1", True,
            primario="198.51.100.7", propios=("198.51.100.7", "ns2.he.net"),
        )
        fqdn = "_acme-challenge.nas.example.org"
        clave = dominio.CLAVES_PROPIAS / "nas-acme.key"
        hecho = subprocess.CompletedProcess([], 0, "", "")
        with (
            patch.object(
                dominio, "propagado", side_effect=[ErrorDNS("otro"), None]
            ) as propagado,
            patch.object(dominio, "privado", return_value="") as privado,
            patch.object(dominio.subprocess, "run", return_value=hecho) as ejecutar,
        ):
            dominio.propio(conf, "acme", fqdn, "TXT", "A" * 43)
        privado.assert_called_once_with(clave)
        self.assertEqual(ejecutar.call_args.args[0][:3], ["nsupdate", "-k", str(clave)])
        guion = ejecutar.call_args.kwargs["input"]
        self.assertIn("server 198.51.100.7\nzone example.org\n", guion)
        self.assertIn(f'update add {fqdn}. 300 TXT "{"A" * 43}"\nsend\n', guion)
        self.assertEqual(propagado.call_args.kwargs["servidores"], conf.propios)
        rechazo = subprocess.CompletedProcess([], 2, "", "")
        with (
            patch.object(dominio, "propagado", side_effect=ErrorDNS("otro")),
            patch.object(dominio, "privado", return_value=""),
            patch.object(dominio.subprocess, "run", return_value=rechazo),
        ):
            with self.assertRaises(ErrorDNS):
                dominio.propio(conf, "ddns", "nas.example.org", "AAAA", IP_NUEVA)
        with self.assertRaises(ErrorDNS):
            dominio.propio(conf, "ddns", "nas.example.org", "AAAA", "fe80::1")


if __name__ == "__main__":
    unittest.main()
