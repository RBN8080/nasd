#!/usr/bin/env python3
"""Ejecuta el gancho generado con install simulado, sin tocar /etc ni red."""

import os
from pathlib import Path
import subprocess
import tempfile


def main() -> None:
    fuente = Path(__file__).with_name("15_tls.sh").read_text()
    marcador = "cat > /etc/letsencrypt/renewal-hooks/deploy/nas-copiar.sh <<EOF\n"
    cuerpo = fuente.split(marcador, 1)[1].split("\nEOF", 1)[0]
    # Expansion del heredoc del generador: conservar los dolares escapados.
    cuerpo = cuerpo.replace(r"\$", "@DOLAR@")
    linaje = "/etc/letsencrypt/live/anterior.ejemplo.org"
    cuerpo = cuerpo.replace("$NOMBRE", "anterior.ejemplo.org")
    cuerpo = cuerpo.replace("$DIR_TLS", "/destino-simulado")
    cuerpo = cuerpo.replace("@DOLAR@", "$")
    with tempfile.TemporaryDirectory() as temporal:
        base = Path(temporal)
        seleccion, gancho, registro = (base / n for n in ("seleccion", "gancho", "registro"))
        gancho.write_text(cuerpo.replace("/etc/nas/tls-lineage", str(seleccion)))
        casos = [(None, None, 0), ("/otro", None, 0), (linaje, None, 2),
                 (linaje, "/nuevo", 0), (linaje, linaje, 2)]
        for renovado, elegido, esperado in casos:
            seleccion.unlink(missing_ok=True)
            registro.unlink(missing_ok=True)
            if elegido is not None:
                seleccion.write_text(elegido + "\n")
            entorno = {**os.environ, "TEST_LOG": str(registro)}
            entorno.pop("RENEWED_LINEAGE", None)
            if renovado is not None:
                entorno["RENEWED_LINEAGE"] = renovado
            subprocess.run(
                ["sh", "-c", 'install() { echo llamada >> "$TEST_LOG"; }; . "$1"',
                 "prueba", str(gancho)], env=entorno, check=True,
            )
            llamadas = len(registro.read_text().splitlines()) if registro.exists() else 0
            assert llamadas == esperado, (renovado, elegido, llamadas)
    print("OK: 5 escenarios del gancho TLS, sin escribir certificados reales")


if __name__ == "__main__":
    main()
