"""Write a Lumiqot Linux format 1 archive from a build's regular files."""
import hashlib
import io
import json
from pathlib import Path
import re
import stat
import sys
import tarfile

root, version, arch, output = sys.argv[1:]
root = Path(root)
files = []
for source in sorted(root.rglob("*")):
    mode = source.lstat().st_mode
    if stat.S_ISDIR(mode):
        continue
    name = source.relative_to(root).as_posix()
    if not stat.S_ISREG(mode) or not re.fullmatch(r"[A-Za-z0-9._+@/-]{1,512}", name):
        raise SystemExit(f"invalid package file {name}")
    with source.open("rb") as content:
        digest = hashlib.file_digest(content, "sha256").hexdigest()
    files.append({"path": name, "mode": 0o755 if mode & 0o111 else 0o644, "sha256": digest})

manifest = json.dumps({
    "format": 1, "package": "lumiqot-linux", "version": version,
    "os": "linux", "arch": arch, "cli": "bin/lumiqot", "lifecycle": 2, "files": files,
}).encode() + b"\n"

with tarfile.open(output, "w:gz", format=tarfile.USTAR_FORMAT) as archive:
    header = tarfile.TarInfo("manifest.json")
    header.size = len(manifest)
    header.mode = 0o644
    archive.addfile(header, io.BytesIO(manifest))
    for entry in files:
        source = root / entry["path"]
        header = tarfile.TarInfo(entry["path"])
        header.size = source.stat().st_size
        header.mode = entry["mode"]
        with source.open("rb") as content:
            archive.addfile(header, content)
