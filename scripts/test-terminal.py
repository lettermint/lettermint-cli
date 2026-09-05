"""Check the built CLI in pipes and a real Unix pseudo-terminal."""

import errno
import json
import os
from pathlib import Path
import select
import subprocess
import sys
import time


binary = str(Path(sys.argv[1] if len(sys.argv) > 1 else "./lettermint").resolve())


def pipe(*args):
    return subprocess.run([binary, *args], capture_output=True, timeout=10)


version = pipe("version")
assert version.returncode == 0 and json.loads(version.stdout)["version"]
plain = pipe("version", "--plain", "--color", "always")
assert plain.returncode == 0 and plain.stdout.startswith(b"Lettermint ")
assert b"\x1b" not in plain.stdout
failure = pipe("version", "--unknown", "--json")
assert failure.returncode == 1 and not failure.stdout
assert json.loads(failure.stderr)["error"]["code"] == "command_failed"

if os.name != "nt":
    import fcntl
    import pty
    import struct
    import termios

    def terminal(args, changes=None, width=100, stdout_pipe=False, stderr_pipe=False):
        master, slave = pty.openpty()
        fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 30, width, 0, 0))
        environment = dict(os.environ)
        for key in ("NO_COLOR", "CLICOLOR", "CLICOLOR_FORCE"):
            environment.pop(key, None)
        environment.update(TERM="xterm-256color", COLORTERM="truecolor")
        environment.update(changes or {})
        process = subprocess.Popen(
            [binary, *args],
            stdin=subprocess.DEVNULL,
            stdout=subprocess.PIPE if stdout_pipe else slave,
            stderr=subprocess.PIPE if stderr_pipe else slave,
            env=environment,
        )
        os.close(slave)
        output = bytearray()
        deadline = time.monotonic() + 10
        try:
            while time.monotonic() < deadline:
                ready, _, _ = select.select([master], [], [], 0.1)
                if not ready:
                    continue
                try:
                    chunk = os.read(master, 65536)
                except OSError as error:
                    if error.errno == errno.EIO:
                        break
                    raise
                if not chunk:
                    break
                output.extend(chunk)
            else:
                raise AssertionError("CLI did not finish in the terminal")
            stdout, stderr = process.communicate(timeout=2)
            return process.returncode, bytes(output), stdout, stderr
        finally:
            if process.poll() is None:
                process.kill()
                process.wait()
            os.close(master)

    mark = "▝██████▘".encode()
    code, welcome, _, _ = terminal([])
    assert code == 0 and mark in welcome and b"Start here" in welcome
    assert b"\x1b[" in welcome
    assert b"Lettermint CLI" in welcome
    assert b"Send email and develop with Lettermint" not in welcome
    assert b"Email from your terminal." not in welcome
    code, narrow, _, _ = terminal([], width=40)
    assert code == 0 and b"Lettermint CLI" in narrow and mark not in narrow
    code, human, _, _ = terminal(["skills", "list"])
    assert code == 0 and b"Version" in human and b"lettermint-cli" in human
    assert mark not in human and b'"skills"' not in human
    code, machine, _, _ = terminal(["version", "--json", "--color", "always"])
    assert code == 0 and json.loads(machine)["version"] and b"\x1b" not in machine
    for changes in ({"NO_COLOR": "1"}, {"TERM": "dumb"}):
        code, output, _, _ = terminal(["skills", "list"], changes)
        assert code == 0 and b"\x1b" not in output and b"lettermint-cli" in output
    code, forced, _, _ = terminal(["skills", "list", "--color", "always"], {"NO_COLOR": "1"})
    assert code == 0 and b"\x1b[" in forced
    code, output, stdout, _ = terminal(["version"], stdout_pipe=True)
    assert code == 0 and json.loads(stdout)["version"] and not output
    code, output, _, stderr = terminal(["version", "--unknown"], stderr_pipe=True)
    assert code == 1 and not output and stderr.startswith(b"Error:") and b"\x1b" not in stderr
    code, output, _, _ = terminal(["completion", "bash", "--color", "always"])
    assert code == 0 and b"\x1b" not in output and mark not in output
    code, output, _, _ = terminal(["version", "--unknown", "--json"])
    assert code == 1 and json.loads(output)["error"]["code"] == "command_failed"

print("Terminal and pipe checks passed.")
