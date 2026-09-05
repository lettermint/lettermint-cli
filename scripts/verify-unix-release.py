#!/usr/bin/env python3
"""Check a native release binary and a manual archive installation."""

import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile

binary = Path("smoke/lettermint").resolve()
tag = json.loads(Path(os.environ["GITHUB_EVENT_PATH"]).read_text())["release"]["tag_name"]
if sys.platform == "darwin":
    team = os.environ.get("MACOS_SIGN_TEAM_ID")
    if not team:
        sys.exit("The expected Apple team identifier is required.")
    subprocess.run(["codesign", "--verify", "--strict", "--verbose=2", str(binary)], check=True)
    identity = subprocess.run(["codesign", "--display", "--verbose=4", str(binary)], capture_output=True, text=True, check=True)
    if f"TeamIdentifier={team}" not in identity.stderr.splitlines():
        sys.exit("The release binary has the wrong Apple publisher.")
    subprocess.run(["codesign", "--verify", "--strict", "-R=notarized", "--check-notarization", str(binary)], check=True)

with tempfile.TemporaryDirectory(prefix="Lettermint café ") as directory:
    target = Path(directory) / "bin/lettermint"
    target.parent.mkdir()
    # A manual install, replacement, and removal use the same verified archive.
    for _ in range(2):
        shutil.copy2(binary, target)
        result = subprocess.check_output([str(target), "version", "--json"], text=True)
        if json.loads(result)["version"] != tag[1:]:
            sys.exit("The installed binary has the wrong version.")
    subprocess.run([sys.executable, "scripts/test-terminal.py", str(target)], check=True)
    target.unlink()
    if target.exists():
        sys.exit("The manual uninstall failed.")
