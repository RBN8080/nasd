#!/usr/bin/env python3
"""Prueba custodia y salida de errores con token ficticio; nunca llama a red."""

import contextlib
import io
import json
import subprocess
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

import token_njalla as token

FICTICIO = "token-ficticio-sin-acceso-0000"


class Custodia(unittest.TestCase):
    def test_custodia_no_reemplaza_y_permisos(self) -> None:
        with tempfile.TemporaryDirectory() as carpeta:
            destino = Path(carpeta) / "privado" / "ddns.token"
            token.guardar(destino, FICTICIO)
            self.assertEqual(token.leer(destino), FICTICIO)
            self.assertEqual(destino.stat().st_mode & 0o777, 0o600)
            with self.assertRaises(FileExistsError):
                token.guardar(destino, "otro-token-ficticio-0000")
            self.assertEqual(token.leer(destino), FICTICIO)
            destino.chmod(0o644)
            with self.assertRaises(PermissionError):
                token.leer(destino)

    def test_no_sigue_enlace(self) -> None:
        with tempfile.TemporaryDirectory() as carpeta:
            destino = Path(carpeta) / "secreto"
            destino.write_text(FICTICIO)
            enlace = Path(carpeta) / "enlace"
            enlace.symlink_to(destino)
            with self.assertRaises(OSError):
                token.leer(enlace)

    def test_no_inyeccion_cabeceras(self) -> None:
        for valor in ("", "corto", FICTICIO + "\nX: algo", FICTICIO + "\r", "x" * 4097):
            with self.subTest(valor=len(valor)), self.assertRaises(ValueError):
                token.validar_token(valor)

    def test_curl_solo_lectura_sin_token_en_argumentos(self) -> None:
        resultado = subprocess.CompletedProcess(
            [], 0, json.dumps({"result": {"records": []}}), ""
        )
        with patch.object(token.subprocess, "run", return_value=resultado) as ejecutar:
            with contextlib.redirect_stdout(io.StringIO()) as salida:
                self.assertEqual(token.comprobar(FICTICIO), 0)
            args, kwargs = ejecutar.call_args
            self.assertNotIn(FICTICIO, str(args))
            self.assertNotIn("env", kwargs)
            self.assertIn(FICTICIO, kwargs["input"])
            cuerpo = json.loads(args[0][args[0].index("--data") + 1])
            self.assertEqual(cuerpo["method"], "list-records")
            self.assertIn(token.ORIGEN, args[0])
            self.assertNotIn(FICTICIO, salida.getvalue())

    def test_error_api_no_refleja_secreto(self) -> None:
        for codigo, cuerpo in (
            (22, FICTICIO),
            (0, FICTICIO),
            (0, json.dumps({"error": FICTICIO})),
            (0, "[]"),
            (0, '{"result": {"records": [null]}}'),
        ):
            with self.subTest(codigo=codigo, cuerpo=bool(cuerpo)):
                resultado = subprocess.CompletedProcess([], codigo, cuerpo, FICTICIO)
                with patch.object(token.subprocess, "run", return_value=resultado):
                    with contextlib.redirect_stdout(io.StringIO()) as salida:
                        self.assertEqual(token.comprobar(FICTICIO), 2)
                    self.assertNotIn(FICTICIO, salida.getvalue())


if __name__ == "__main__":
    unittest.main()
