#!/usr/bin/env python3
"""Check a native release binary and its shell installer."""

import json
import os
from pathlib import Path
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
    target = Path(directory) / "工具 bin/lettermint"
    assets = Path("release-bundle/assets").resolve()
    installer = assets / "install.sh"
    tools = Path(directory) / "tools"
    tools.mkdir()
    # Assets are not public yet. Serve the saved package bytes to the installer.
    # Keep the real checksum, signature, publisher, notarization, and version checks.
    curl = tools / "curl"
    curl.write_text(f'''#!{sys.executable}
import pathlib, shutil, sys
args = sys.argv[1:]
base = {f"https://github.com/lettermint/lettermint-cli/releases/download/{tag}/"!r}
assets = pathlib.Path({str(assets)!r})
url = args[-1]
if not url.startswith(base):
    sys.exit("Unexpected installer URL: " + url)
name = url[len(base):]
if "/" in name or name not in {{"checksums.txt", *[p.name for p in assets.glob("*.tar.gz")]}}:
    sys.exit("Unexpected installer asset: " + name)
shutil.copyfile(assets / name, args[args.index("--output") + 1])
''', encoding="utf-8")
    curl.chmod(0o755)
    environ = {**os.environ, "PATH": str(tools) + os.pathsep + os.environ["PATH"]}
    install = ["sh", str(installer), "--bin-dir", str(target.parent)]
    for _ in range(2):
        subprocess.run([*install, "--version", tag], env=environ, check=True)
        result = subprocess.check_output([str(target), "version", "--json"], text=True)
        if json.loads(result)["version"] != tag[1:]:
            sys.exit("The installed binary has the wrong version.")
    subprocess.run([sys.executable, "scripts/test-terminal.py", str(target)], check=True)
    subprocess.run([*install, "--uninstall"], env=environ, check=True)
    if target.exists() or (target.parent / ".lettermint-install").exists():
        sys.exit("The shell uninstall failed.")
