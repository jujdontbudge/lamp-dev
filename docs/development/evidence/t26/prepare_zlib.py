"""Prepare public, pinned third-party source for the disposable VM fixture.
No source is executed on the host. Keep LICENSE and the exact upstream files.
The large generated crc32.h is omitted: the demo uses DYNAMIC_CRC_TABLE.
"""
import hashlib
import io
from pathlib import Path
import tarfile
import urllib.request

base = Path.home() / "lamp-t26"
base.mkdir(exist_ok=True)
url = "https://codeload.github.com/madler/zlib/tar.gz/refs/tags/v1.3.1"
with urllib.request.urlopen(url) as response:
    data = response.read()
assert hashlib.sha256(data).hexdigest() == "17e88863f3600672ab49182f217281b6fc4d3c762bde361935e436a95214d05c"
(base / "zlib-v1.3.1.tar.gz").write_bytes(data)
with tarfile.open(fileobj=io.BytesIO(data)) as source, tarfile.open(base / "zlib-build-inputs.tar.gz", "w:gz", compresslevel=9) as dest:
    for entry in source:
        rel = entry.name.split("/", 1)[-1]
        selected = ("/" not in rel and (rel.endswith((".c", ".h", ".in")) or rel in {"Makefile.in", "configure", "README", "LICENSE", "zlib.map"})) or rel in {"test/example.c", "test/minigzip.c"}
        if entry.isfile() and selected and rel != "crc32.h":
            dest.addfile(entry, source.extractfile(entry))
