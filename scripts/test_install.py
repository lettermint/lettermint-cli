"""Local installer checks. All downloads and platform signing tools are test doubles."""

import hashlib
import io
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tarfile
import tempfile
import unittest


SCRIPT = Path(__file__).with_name("install.sh")
RELEASES = "https://github.com/lettermint/lettermint-cli/releases"
FAKE_TOOL = r'''
import json, os, pathlib, shutil, sys
name = pathlib.Path(sys.argv[0]).name
args = sys.argv[1:]
with open(os.environ["TEST_LOG"], "a") as out:
    out.write(json.dumps([name, args]) + "\n")
if name == "uname":
    print(os.environ.get("TEST_SYSTEM", "Linux") if args == ["-s"] else os.environ.get("TEST_ARCH", "x86_64"))
elif name == "sysctl":
    print(os.environ.get("TEST_ROSETTA", "0"))
elif name == "curl":
    assert args[args.index("--proto") + 1] == "=https"
    assert args[args.index("--proto-redir") + 1] == "=https"
    assert "--tlsv1.2" in args and "--fail" in args
    url = args[-1]
    if url.endswith("/latest"):
        print(os.environ.get("TEST_LATEST", "https://github.com/lettermint/lettermint-cli/releases/tag/v1.0.0"), end="")
    else:
        prefix = "https://github.com/lettermint/lettermint-cli/releases/download/"
        assert url.startswith(prefix)
        source = pathlib.Path(os.environ["TEST_ASSETS"]) / url[len(prefix):]
        if not source.is_file(): sys.exit(22)
        shutil.copyfile(source, args[args.index("--output") + 1])
elif name == "codesign":
    if "--display" in args:
        print("TeamIdentifier=" + os.environ.get("TEST_TEAM", "ABC1234567"), file=sys.stderr)
    elif "--check-notarization" in args:
        sys.exit(int(os.environ.get("TEST_NOTARY_FAIL", "0")))
    else:
        sys.exit(int(os.environ.get("TEST_SIGNATURE_FAIL", "0")))
elif name == "gh":
    assert args[:2] == ["attestation", "verify"]
    assert args[args.index("--repo") + 1] == "lettermint/lettermint-cli"
    assert args[args.index("--signer-workflow") + 1] == "lettermint/lettermint-cli/.github/workflows/release.yml"
    assert args[args.index("--source-ref") + 1].startswith("refs/tags/v")
    sys.exit(int(os.environ.get("TEST_PROVENANCE_FAIL", "0")))
elif name == "mv":
    sentinel = pathlib.Path(os.environ["TEST_ASSETS"]) / "mv-failed"
    if args[-2].endswith("/marker") and not sentinel.exists():
        sentinel.touch()
        sys.exit(1)
    os.execv("/bin/mv", ["/bin/mv", *args])
else:
    raise AssertionError(name)
'''


@unittest.skipIf(os.name == 'nt', 'The shell installer supports macOS and Linux.')
class InstallerTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="lettermint-installer-tests-")
        self.root = Path(self.temp.name)
        self.home = self.root / "home"
        self.home.mkdir()
        self.bin = self.home / "工具 and spaces" / "bin"
        self.assets = self.root / "assets"
        self.assets.mkdir()
        self.tools = self.root / "tools"
        self.tools.mkdir()
        self.log = self.root / "calls.jsonl"
        self.ran = self.root / "binary-ran"
        self.env = {
            **os.environ,
            "HOME": str(self.home),
            "PATH": str(self.tools) + ":/usr/bin:/bin:/usr/sbin:/sbin",
            "TMPDIR": str(self.root),
            "TEST_ASSETS": str(self.assets),
            "TEST_LOG": str(self.log),
            "TEST_RAN": str(self.ran),
        }
        for name in ("curl", "uname", "sysctl", "codesign", "gh"):
            self.fake_tool(name)
        self.fixture("v1.0.0")

    def tearDown(self):
        self.temp.cleanup()

    def fake_tool(self, name, body=None):
        path = self.tools / name
        path.write_text(f"#!{sys.executable}\n" + (FAKE_TOOL if body is None else body))
        path.chmod(0o755)

    def fixture(self, version, system="linux", arch="amd64", reported=None, member="regular"):
        directory = self.assets / version
        directory.mkdir(exist_ok=True)
        archive = directory / f"lettermint_{version[1:]}_{system}_{arch}.tar.gz"
        binary = (
            '#!/bin/sh\n[ "$*" = "version --json --no-input" ] || exit 3\n'
            ': > "$TEST_RAN"\n'
            "printf '%s\\n' '" + json.dumps({"version": reported or version[1:]}) + "'\n"
        ).encode()
        with tarfile.open(archive, "w:gz") as tar:
            item = tarfile.TarInfo("lettermint")
            item.mode = 0o755
            if member == "symlink":
                item.type = tarfile.SYMTYPE
                item.linkname = "/bin/sh"
                tar.addfile(item)
            elif member == "directory":
                item.type = tarfile.DIRTYPE
                tar.addfile(item)
            else:
                item.size = len(binary)
                tar.addfile(item, io.BytesIO(binary))
                if member == "duplicate":
                    tar.addfile(item, io.BytesIO(binary))
            # This path must never be extracted.
            item = tarfile.TarInfo("../outside-archive")
            item.size = 1
            tar.addfile(item, io.BytesIO(b"x"))
        checksum = hashlib.sha256(archive.read_bytes()).hexdigest()
        with (directory / "checksums.txt").open("a") as out:
            out.write(f"{checksum}  {archive.name}\n")
        (directory / "provenance.jsonl").write_text("test fixture\n")
        return archive

    def run_installer(self, *args, ok=True, script=SCRIPT, shell="/bin/sh", stdin=False):
        command = [shell, "-s", "--"] if stdin else [shell, str(script)]
        result = subprocess.run(
            [*command, "--bin-dir", str(self.bin), *args], env=self.env,
            capture_output=True, text=True, timeout=15,
            input=script.read_text() if stdin else None,
        )
        message = result.stdout + result.stderr
        if ok:
            self.assertEqual(result.returncode, 0, message)
        else:
            self.assertNotEqual(result.returncode, 0, message)
        self.assertFalse((self.bin / ".lettermint-install.lock").exists(), message)
        self.assertEqual(list(self.bin.glob(".lettermint-stage.*")), [], message)
        return result

    def snapshot(self):
        return ((self.bin / "lettermint").read_bytes(), (self.bin / ".lettermint-install").read_bytes())

    def mac_script(self):
        script = self.root / "mac-install.sh"
        script.write_text(SCRIPT.read_text().replace("REPLACE_WITH_APPLE_TEAM_ID", "ABC1234567"))
        self.env["TEST_SYSTEM"] = "Darwin"
        self.fixture("v1.0.0", system="darwin")
        return script

    def test_latest_install_in_unicode_path_and_uninstall_keeps_profiles(self):
        profile = self.home / ".config" / "lettermint" / "profiles.json"
        profile.parent.mkdir(parents=True)
        profile.write_text("saved profile")
        result = self.run_installer()
        self.assertIn("Installed Lettermint v1.0.0", result.stdout)
        self.assertIn("Add this directory to PATH", result.stdout)
        self.assertTrue(os.access(self.bin / "lettermint", os.X_OK))
        self.assertFalse((self.root / "outside-archive").exists())
        self.run_installer("--uninstall")
        self.assertFalse((self.bin / "lettermint").exists())
        self.assertFalse((self.bin / ".lettermint-install").exists())
        self.assertEqual(profile.read_text(), "saved profile")

    def test_stdin_install_and_bash_reinstall(self):
        self.run_installer("--version", "v1.0.0", stdin=True)
        before = self.snapshot()
        self.env["PATH"] = str(self.bin) + ":" + self.env["PATH"]
        self.run_installer("--version", "v1.0.0", shell="/bin/bash")
        self.assertEqual(before, self.snapshot())

    def test_explicit_prerelease_upgrade_and_downgrade(self):
        for version in ("v1.0.0-rc.1", "v1.0.0-rc.2", "v1.1.0"):
            self.fixture(version)
        self.run_installer("--version", "v1.0.0-rc.1")
        self.run_installer("--version", "v1.0.0-rc.2")
        self.run_installer("--version", "v1.0.0")
        stable = self.snapshot()
        self.run_installer("--version", "v1.0.0-rc.2", ok=False)
        self.assertEqual(stable, self.snapshot())
        self.run_installer("--version", "v1.1.0")
        self.run_installer("--version", "v1.0.0", ok=False)
        self.run_installer("--version", "v1.0.0", "--allow-downgrade")
        self.assertEqual(stable, self.snapshot())

    def test_semver_ordering(self):
        functions = SCRIPT.read_text().rsplit('main "$@"', 1)[0]
        source = self.root / "semver.sh"
        source.write_text(functions + '\nis_older "$1" "$2"\n')
        pairs = [
            ("v1.0.0-alpha", "v1.0.0-alpha.1"),
            ("v1.0.0-alpha.1", "v1.0.0-alpha.beta"),
            ("v1.0.0-beta.2", "v1.0.0-beta.11"),
            ("v1.0.0-rc.1", "v1.0.0"),
            ("v2.0.0", "v10.0.0"),
            ("v1.9.0", "v1.10.0"),
        ]
        for lower, higher in pairs:
            for a, b, expected in ((lower, higher, 0), (higher, lower, 1), (lower, lower, 1)):
                with self.subTest(a=a, b=b):
                    result = subprocess.run(["/bin/sh", str(source), a, b], env=self.env)
                    self.assertEqual(result.returncode, expected)

    def test_help_invalid_options_and_versions_do_not_download(self):
        self.run_installer("--help")
        self.assertFalse(self.bin.exists())
        for version in ("1.0.0", "v01.0.0", "v1.0.0-01", "v1.0.0-a..b", "v1.0.0+build", "../../secret", "v1.0.0\n"):
            with self.subTest(version=version):
                self.run_installer("--version", version, ok=False)
        self.run_installer("--bad", ok=False)
        self.run_installer("--version", ok=False)
        self.run_installer("--uninstall", "--version", "v1.0.0", ok=False)
        self.assertFalse(self.log.exists())

    def test_latest_rejects_foreign_redirect_and_prerelease(self):
        for latest in ("https://other.example/tag/v1.0.0", RELEASES + "/tag/v1.0.0-rc.1"):
            self.env["TEST_LATEST"] = latest
            self.run_installer(ok=False)
            self.assertFalse(self.ran.exists())

    def test_checksum_mismatch_and_duplicates_keep_installed_version(self):
        self.run_installer()
        before = self.snapshot()
        self.ran.unlink()
        archive = self.fixture("v1.1.0")
        archive.write_bytes(archive.read_bytes() + b"tamper")
        self.run_installer("--version", "v1.1.0", ok=False)
        checksums = self.assets / "v1.0.0" / "checksums.txt"
        checksums.write_text(checksums.read_text() * 2)
        self.run_installer("--version", "v1.0.0", ok=False)
        self.assertFalse(self.ran.exists())
        self.assertEqual(before, self.snapshot())

    def test_bad_version_and_missing_assets_leave_no_install(self):
        self.fixture("v1.1.0", reported="9.9.9")
        self.run_installer("--version", "v1.1.0", ok=False)
        self.run_installer("--version", "v8.0.0", ok=False)
        self.assertFalse((self.bin / "lettermint").exists())

    def test_nonregular_and_duplicate_archive_members_fail_before_execution(self):
        for i, member in enumerate(("symlink", "directory", "duplicate")):
            version = f"v2.0.{i}"
            self.fixture(version, member=member)
            self.run_installer("--version", version, ok=False)
            self.assertFalse(self.ran.exists())

    def test_other_installers_and_symlinks_are_preserved(self):
        self.bin.mkdir(parents=True)
        target = self.bin / "lettermint"
        target.write_text("owned by another installer")
        self.run_installer(ok=False)
        self.assertEqual(target.read_text(), "owned by another installer")
        target.unlink()
        other = self.root / "brew-binary"
        other.write_text("Homebrew")
        target.symlink_to(other)
        self.run_installer(ok=False)
        self.assertTrue(target.is_symlink())
        self.assertEqual(other.read_text(), "Homebrew")

    def test_other_path_installation_is_preserved(self):
        self.fake_tool("lettermint", "raise SystemExit('must not execute')\n")
        result = self.run_installer(ok=False)
        self.assertIn("already installed", result.stderr)
        self.assertFalse((self.bin / "lettermint").exists())

    def test_changed_owned_binary_is_not_removed(self):
        self.run_installer()
        target = self.bin / "lettermint"
        target.write_text("changed")
        self.run_installer("--uninstall", ok=False)
        self.run_installer(ok=False)
        self.assertEqual(target.read_text(), "changed")

    def test_hash_tool_failure_stops_install(self):
        self.fake_tool("sha256sum", "raise SystemExit(1)\n")
        self.run_installer(ok=False)
        self.assertFalse(self.ran.exists())
        self.assertFalse((self.bin / "lettermint").exists())

    def test_atomic_replacement_rolls_back_after_marker_failure(self):
        self.run_installer()
        before = self.snapshot()
        self.fixture("v1.1.0")
        self.fake_tool("mv")
        self.run_installer("--version", "v1.1.0", ok=False)
        self.assertEqual(before, self.snapshot())

    def test_fresh_install_rolls_back_after_marker_failure(self):
        self.fake_tool("mv")
        self.run_installer(ok=False)
        self.assertFalse((self.bin / "lettermint").exists())
        self.assertFalse((self.bin / ".lettermint-install").exists())

    def test_arm64_and_unsupported_platform(self):
        self.env["TEST_ARCH"] = "aarch64"
        self.fixture("v1.0.0", arch="arm64")
        self.run_installer()
        self.env["TEST_SYSTEM"] = "MINGW64_NT"
        self.assertIn("PowerShell", self.run_installer(ok=False).stderr)
        self.env["TEST_SYSTEM"] = "Linux"
        self.env["TEST_ARCH"] = "riscv64"
        self.run_installer(ok=False)

    def test_macos_checks_signatures_team_and_notarization_before_running(self):
        script = self.mac_script()
        self.env["TEST_SYSTEM"] = "Darwin"
        self.run_installer(ok=False)  # The release must set the Apple team ID.
        self.assertFalse(self.ran.exists())
        for key, value in (("TEST_SIGNATURE_FAIL", "1"), ("TEST_TEAM", "OTHER12345"), ("TEST_NOTARY_FAIL", "1")):
            self.env[key] = value
            self.run_installer(script=script, ok=False)
            self.assertFalse(self.ran.exists())
            self.env.pop(key)
        self.run_installer(script=script)
        self.assertTrue(self.ran.exists())

    def test_rosetta_uses_arm64(self):
        script = self.mac_script()
        self.fixture("v1.0.0", system="darwin", arch="arm64")
        self.env["TEST_ROSETTA"] = "1"
        self.run_installer(script=script)
        self.assertIn("darwin_arm64.tar.gz", self.log.read_text())

    def test_optional_provenance_is_enforced(self):
        self.env["TEST_PROVENANCE_FAIL"] = "1"
        self.run_installer("--verify-provenance", ok=False)
        self.assertFalse(self.ran.exists())
        self.env.pop("TEST_PROVENANCE_FAIL")
        self.run_installer("--verify-provenance")
        calls = [json.loads(line) for line in self.log.read_text().splitlines()]
        self.assertTrue(any(name == "gh" for name, _ in calls))


if __name__ == "__main__":
    unittest.main(verbosity=2)
