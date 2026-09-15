#!/usr/bin/env python3
"""Check real signed release bytes through both publisher verifiers."""

from pathlib import Path
import shutil
import subprocess
import sys
import tempfile

import macos
import release

binary = Path(sys.argv[1]).resolve()
team = sys.argv[2]
macos.verify_signature(binary, team)
original = release.digest(binary)
wrong_team = "WRONG12345" if team != "WRONG12345" else "OTHER12345"
with tempfile.TemporaryDirectory(prefix="Lettermint signature café ") as directory:
    root = Path(directory)
    changed = root / "modified"
    shutil.copyfile(binary, changed)
    with changed.open("r+b") as handle:
        handle.seek(8192)
        byte = handle.read(1)
        if not byte:
            sys.exit("The fixture is too small for the signed-content check.")
        handle.seek(8192)
        handle.write(bytes([byte[0] ^ 1]))
    adhoc = root / "adhoc"
    shutil.copyfile(binary, adhoc)
    subprocess.run(["codesign", "--force", "--sign", "-", str(adhoc)], check=True)
    for target, expected_team in ((binary, wrong_team), (changed, team), (adhoc, team)):
        try:
            macos.verify_signature(target, expected_team)
        except ValueError:
            pass
        else:
            sys.exit("The Python verifier accepted an invalid signature or publisher.")
    # Run the installer's actual verification function with the native codesign tool.
    source = Path("scripts/install.sh").read_text().rsplit('main "$@"', 1)[0]
    for target, expected_team, expected in ((binary, team, 0), (binary, wrong_team, 1),
                                             (changed, team, 1), (adhoc, team, 1)):
        script = root / "verify.sh"
        script.write_text(source.replace("REPLACE_WITH_APPLE_TEAM_ID", expected_team)
                          + '\nverify_macos_publisher "$1"\n')
        result = subprocess.run(["sh", str(script), str(target)], capture_output=True, text=True)
        if bool(result.returncode) != bool(expected):
            sys.exit("The shell verifier returned an incorrect result: " + result.stderr)
if release.digest(binary) != original:
    sys.exit("Signature tests changed the release binary.")
print("Real macOS signature checks passed, including wrong team, modified content, and ad hoc signing.")
