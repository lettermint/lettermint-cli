#!/usr/bin/env python3
"""Check a native release binary and its shell installer."""

import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile

import macos

binary = Path("smoke/lettermint").resolve()
tag = json.loads(Path(os.environ["GITHUB_EVENT_PATH"]).read_text())["release"]["tag_name"]
before_notarization = sys.argv[1:] == ["--before-notarization"]
if sys.argv[1:] and not before_notarization:
    sys.exit("Unknown verification argument.")
if before_notarization and sys.platform != "darwin":
    sys.exit("Pre-notarization checks require macOS.")
if sys.platform == "darwin":
    team = os.environ.get("MACOS_SIGN_TEAM_ID")
    if not team:
        sys.exit("The expected Apple team identifier is required.")
    macos.verify_signature(binary, team)
reported = json.loads(subprocess.check_output([str(binary), "version", "--json"], text=True))
if reported["version"] != tag[1:]:
    sys.exit("The release binary has the wrong version.")
if before_notarization:
    subprocess.run([sys.executable, "scripts/test-macos-signature.py", str(binary), team], check=True)
    subprocess.run([sys.executable, "scripts/test-terminal.py", str(binary)], check=True)
    print("Native macOS checks passed. Notarization and installer checks remain required.")
    sys.exit(0)
if sys.platform == "darwin":
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
