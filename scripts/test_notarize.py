import base64
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
from unittest.mock import patch
import zipfile

import notarize
import release


class NotarizeTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="notary café ")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.bundle = self.root / "bundle"
        self.state = self.root / "state"
        self.log = self.root / "log.json"
        self.ctx = {"tag": "v1.2.3-rc.1", "commit": "a" * 40, "release_id": 12,
                    "run_id": 34, "prerelease": True}
        self.identifier = "12345678-1234-1234-1234-123456789abc"
        self.team = "AB12345678"
        (self.bundle / "assets").mkdir(parents=True)
        (self.bundle / "cask").mkdir()
        (self.bundle / "cask/lettermint.rb").write_text("cask")
        for name in release.asset_names(self.ctx):
            (self.bundle / "assets" / name).write_text(name)
        for arch in ("amd64", "arm64"):
            archive = self.bundle / "assets" / release.archive_name(self.ctx["tag"], "darwin", arch)
            with tarfile.open(archive, "w:gz") as package:
                binary = ("signed binary " + arch).encode()
                entry = tarfile.TarInfo("lettermint")
                entry.size = len(binary)
                entry.mode = 0o755
                package.addfile(entry, io.BytesIO(binary))
        checksums = "".join(f"{release.digest(self.bundle / 'assets' / name)}  {name}\n"
                            for name in sorted(release.asset_names(self.ctx)) if name != "checksums.txt")
        (self.bundle / "assets/checksums.txt").write_text(checksums)
        release.write_manifest(self.bundle, self.ctx)
        with patch.object(notarize, "verify_signature"), notarize.payload(self.bundle, self.ctx, "amd64", self.team) as (binding, archive):
            self.binding = binding
            self.archive = self.root / "payload.zip"
            shutil.copyfile(archive, self.archive)

    def saved(self, **changes):
        self.state.mkdir(exist_ok=True)
        (self.state / "submission.json").write_text(json.dumps({**self.binding, "submission_id": self.identifier, **changes}))

    def result(self, status="Accepted", code=0, **changes):
        return subprocess.CompletedProcess([], code, json.dumps({"id": self.identifier, "status": status, **changes}), "")

    def test_payload_preserves_signed_bytes_and_has_stable_metadata(self):
        for _ in range(2):
            with patch.object(notarize, "verify_signature") as verify, notarize.payload(self.bundle, self.ctx, "amd64", self.team) as (binding, archive):
                self.assertEqual(binding, self.binding)
                self.assertEqual(archive.read_bytes(), self.archive.read_bytes())
                verify.assert_called_once()
                with zipfile.ZipFile(archive) as package:
                    self.assertEqual(package.namelist(), ["lettermint"])
                    self.assertEqual(package.read("lettermint"), b"signed binary amd64")
                    self.assertEqual(package.getinfo("lettermint").external_attr >> 16, 0o100755)

    def test_altered_release_packages_cannot_be_submitted(self):
        archive = self.bundle / "assets" / release.archive_name(self.ctx["tag"], "darwin", "amd64")
        archive.write_bytes(b"changed")
        with patch.object(notarize, "verify_signature") as verify, self.assertRaisesRegex(ValueError, "saved file changed"):
            with notarize.payload(self.bundle, self.ctx, "amd64", self.team):
                self.fail("The altered bundle was accepted")
        verify.assert_not_called()

    def test_invalid_signature_or_wrong_publisher_stops_before_upload(self):
        binary = self.root / "binary"
        for results in ([self.result(code=1)], [self.result(), self.result()],
                        [self.result(), subprocess.CompletedProcess([], 0, "", "TeamIdentifier=OTHER12345\n")]):
            with self.subTest(results=results), patch.object(notarize, "execute", side_effect=results), self.assertRaises(ValueError):
                notarize.verify_signature(binary, self.team)
        with patch.object(notarize, "execute", side_effect=[self.result(), subprocess.CompletedProcess([], 0, "", f"TeamIdentifier={self.team}\n")]):
            notarize.verify_signature(binary, self.team)

    def test_submit_saves_id_only_after_successful_upload_and_does_not_wait(self):
        original = self.archive.read_bytes()
        with patch.object(notarize, "notary", return_value=self.result("In Progress")) as command:
            notarize.submit(self.state, self.binding, self.archive)
        command.assert_called_once_with("submit", str(self.archive), "--no-wait", "--output-format", "json", timeout=600)
        self.assertEqual(notarize.read_state(self.state, self.binding), self.identifier)
        self.assertEqual(self.archive.read_bytes(), original)

    def test_failed_or_uncertain_upload_does_not_create_completed_state(self):
        for result in (self.result(code=1), self.result(id="bad-id"), subprocess.CompletedProcess([], 0, "not JSON", "")):
            with self.subTest(result=result), patch.object(notarize, "notary", return_value=result), self.assertRaises(ValueError):
                notarize.submit(self.state, self.binding, self.archive)
            self.assertFalse(self.state.exists())
        with patch.object(notarize, "notary", side_effect=subprocess.TimeoutExpired("submit", 600)), self.assertRaises(subprocess.TimeoutExpired):
            notarize.submit(self.state, self.binding, self.archive)
        self.assertFalse(self.state.exists())

    def test_existing_state_is_never_overwritten_by_another_submission(self):
        self.saved()
        original = (self.state / "submission.json").read_bytes()
        with patch.object(notarize, "notary") as command, self.assertRaisesRegex(ValueError, "already exists"):
            notarize.submit(self.state, self.binding, self.archive)
        command.assert_not_called()
        self.assertEqual((self.state / "submission.json").read_bytes(), original)

    def test_state_rejects_other_releases_architectures_publishers_and_bytes(self):
        changes = [{"arch": "arm64"}, {"team_id": "OTHER12345"}, {"schema": 2},
                   {"archive_sha256": "b" * 64}, {"binary_sha256": "b" * 64},
                   {"payload_sha256": "b" * 64}, {"unexpected": "value"}]
        changes += [{"release": {**self.ctx, key: value}} for key, value in
                    (("commit", "b" * 40), ("tag", "v1.2.4-rc.1"), ("run_id", 35), ("release_id", 13))]
        for change in changes:
            with self.subTest(change=change):
                self.saved(**change)
                with self.assertRaisesRegex(ValueError, "does not match"):
                    notarize.read_state(self.state, self.binding)
        self.saved()
        (self.state / "extra").write_text("unexpected")
        with self.assertRaisesRegex(ValueError, "unexpected files"):
            notarize.read_state(self.state, self.binding)

    def test_restore_reuses_original_state_and_never_submits(self):
        self.saved()
        with patch.object(release, "restore_files", return_value=True) as restore, patch.object(notarize, "notary") as command:
            self.assertTrue(notarize.restore(self.state, self.binding))
        restore.assert_called_once_with(self.state, self.ctx, "notary-amd64")
        command.assert_not_called()

    def test_retry_stops_if_submission_started_but_its_state_is_missing(self):
        for conclusion in ("success", "failure", "cancelled", None):
            jobs = [{"jobs": [{"name": "notarize (amd64)", "steps": [{"name": notarize.SUBMIT_STEP, "conclusion": conclusion}]}]}]
            with self.subTest(conclusion=conclusion), patch.dict(os.environ, GITHUB_RUN_ATTEMPT="2"), patch.object(release, "restore_files", return_value=False), patch.object(release, "pages", return_value=jobs), patch.object(notarize, "notary") as command, self.assertRaisesRegex(ValueError, "Do not resubmit"):
                notarize.restore(self.state, self.binding)
            command.assert_not_called()

    def test_retry_can_start_if_no_submission_step_ran(self):
        for jobs in ([], [{"name": "notarize (amd64)", "steps": [{"name": notarize.SUBMIT_STEP, "conclusion": "skipped"}]}],
                     [{"name": "notarize (arm64)", "steps": [{"name": notarize.SUBMIT_STEP, "conclusion": "success"}]}]):
            with self.subTest(jobs=jobs), patch.dict(os.environ, GITHUB_RUN_ATTEMPT="2"), patch.object(release, "restore_files", return_value=False), patch.object(release, "pages", return_value=[{"jobs": jobs}]):
                self.assertFalse(notarize.restore(self.state, self.binding))

    def test_expired_or_ambiguous_state_cannot_start_a_new_submission(self):
        item = {"name": "release-12-notary-amd64", "expired": False}
        for artifacts in ([{**item, "expired": True}], [item, item]):
            with self.subTest(artifacts=artifacts), patch.object(release, "pages", return_value=[{"artifacts": artifacts}]), patch.object(notarize, "notary") as command, self.assertRaisesRegex(ValueError, "ambiguous or expired"):
                notarize.restore(self.state, self.binding)
            command.assert_not_called()

    def test_wait_timeout_then_retry_accepts_the_same_id_and_leaves_bytes_unchanged(self):
        self.saved()
        before = (self.state / "submission.json").read_bytes()
        package_before = self.archive.read_bytes()
        with patch.object(notarize, "notary", side_effect=[self.result("In Progress", 124), self.result()]) as command:
            with self.assertRaisesRegex(notarize.PendingError, "Re-run failed jobs"):
                notarize.wait(self.state, self.binding, self.log)
            notarize.wait(self.state, self.binding, self.log)
        self.assertEqual(command.call_count, 2)
        for call in command.call_args_list:
            self.assertEqual(call.args[:2], ("wait", self.identifier))
        self.assertEqual((self.state / "submission.json").read_bytes(), before)
        self.assertEqual(self.archive.read_bytes(), package_before)

    def test_local_timeout_preserves_submission_state(self):
        self.saved()
        with patch.object(notarize, "notary", side_effect=subprocess.TimeoutExpired("wait", 3720)), self.assertRaises(notarize.PendingError):
            notarize.wait(self.state, self.binding, self.log)
        self.assertEqual(notarize.read_state(self.state, self.binding), self.identifier)

    def test_rejected_submissions_fetch_logs_and_never_succeed(self):
        self.saved()
        for status in ("Invalid", "Rejected"):
            with self.subTest(status=status), patch.object(notarize, "notary", side_effect=[self.result(status), self.result()]) as command, self.assertRaisesRegex(ValueError, status):
                notarize.wait(self.state, self.binding, self.log)
            self.assertEqual(command.call_args.args, ("log", self.identifier, str(self.log)))

    def test_status_errors_cannot_be_treated_as_acceptance(self):
        self.saved()
        for result in (self.result(code=1), self.result(id="other"), self.result("Unknown"),
                       subprocess.CompletedProcess([], 1, "network failure", "")):
            with self.subTest(result=result), patch.object(notarize, "notary", return_value=result), self.assertRaises(ValueError):
                notarize.wait(self.state, self.binding, self.log)

    def test_missing_settings_stop_before_apple_calls(self):
        for missing in notarize.SETTINGS:
            environ = {name: "present" for name in notarize.SETTINGS if name != missing}
            with self.subTest(missing=missing), patch.dict(os.environ, environ, clear=True), patch.object(notarize, "execute") as command, self.assertRaisesRegex(ValueError, missing):
                notarize.notary("wait", self.identifier)
            command.assert_not_called()

    def test_malformed_settings_stop_before_apple_calls(self):
        key = b"-----BEGIN " + b"PRIVATE KEY-----\nfixture\n"
        settings = {"MACOS_NOTARY_KEY": base64.b64encode(key).decode(),
                    "MACOS_NOTARY_KEY_ID": "KEY1234567", "MACOS_NOTARY_ISSUER_ID": self.identifier}
        for name, value in (("MACOS_NOTARY_KEY", "not-base64"),
                            ("MACOS_NOTARY_KEY", base64.b64encode(b"not a key").decode()),
                            ("MACOS_NOTARY_KEY_ID", "short"), ("MACOS_NOTARY_ISSUER_ID", "invalid")):
            with self.subTest(name=name, value=value), patch.dict(os.environ, {**settings, name: value}), patch.object(notarize, "execute") as command, self.assertRaisesRegex(ValueError, name):
                notarize.notary("wait", self.identifier)
            command.assert_not_called()

    @unittest.skipIf(os.name == "nt", "The macOS job uses POSIX executable tools.")
    def test_script_resumes_after_runner_state_is_restored_without_another_upload(self):
        tools = self.root / "tools"
        tools.mkdir()
        fake = '''import json, os, pathlib, shutil, sys
name = pathlib.Path(sys.argv[0]).name
args = sys.argv[1:]
if name == 'codesign':
    if '--display' in args:
        print('TeamIdentifier=' + os.environ['MACOS_SIGN_TEAM_ID'], file=sys.stderr)
elif name == 'gh':
    if args[0] == 'api':
        print(json.dumps([{'artifacts': [{'name': 'release-12-notary-amd64', 'expired': False}]}]))
    elif args[:2] == ['run', 'download']:
        shutil.copytree(os.environ['SAVED_STATE'], args[args.index('--dir') + 1])
    else:
        sys.exit('Unexpected gh command')
elif name == 'xcrun':
    if args[:2] not in (['notarytool', 'submit'], ['notarytool', 'wait']):
        sys.exit('Unexpected Apple command')
    with open(os.environ['CALLS'], 'a') as handle:
        handle.write(args[1] + '\\n')
    status = os.environ.get('APPLE_STATUS', 'In Progress')
    print(json.dumps({'id': os.environ['SUBMISSION_ID'], 'status': status}))
    if args[1] == 'wait' and status == 'In Progress':
        sys.exit(124)
else:
    sys.exit('Unexpected tool')
'''
        for name in ("codesign", "gh", "xcrun"):
            path = tools / name
            path.write_text(f"#!{sys.executable}\n" + fake)
            path.chmod(0o755)
        event = self.root / "event.json"
        event.write_text(json.dumps({"action": "published", "release": {
            "id": 12, "tag_name": self.ctx["tag"], "prerelease": True, "draft": False}}))
        key = b"-----BEGIN " + b"PRIVATE KEY-----\nfixture\n"
        environ = {**os.environ, "PATH": str(tools) + os.pathsep + os.environ["PATH"],
                   "GITHUB_EVENT_PATH": str(event), "GITHUB_REPOSITORY": release.REPOSITORY,
                   "GITHUB_SHA": self.ctx["commit"], "GITHUB_RUN_ID": "34", "GITHUB_RUN_ATTEMPT": "2",
                   "GITHUB_OUTPUT": str(self.root / "outputs"), "GITHUB_STEP_SUMMARY": str(self.root / "summary"),
                   "MACOS_NOTARY_KEY": base64.b64encode(key).decode(), "MACOS_NOTARY_KEY_ID": "KEY1234567",
                   "MACOS_NOTARY_ISSUER_ID": self.identifier, "MACOS_SIGN_TEAM_ID": self.team,
                   "CALLS": str(self.root / "calls"), "SUBMISSION_ID": self.identifier,
                   "SAVED_STATE": str(self.root / "saved-state")}
        def run(command):
            return subprocess.run([sys.executable, str(Path(notarize.__file__).resolve()), command,
                                   "--arch", "amd64", "--bundle", str(self.bundle), "--state", str(self.state)],
                                  cwd=self.root, env=environ, capture_output=True, text=True, timeout=30)
        result = run("submit")
        self.assertEqual(result.returncode, 0, result.stderr)
        original = (self.state / "submission.json").read_bytes()
        result = run("wait")
        self.assertEqual(result.returncode, 75, result.stderr)
        self.assertIn("Re-run failed jobs", (self.root / "summary").read_text())
        shutil.move(self.state, self.root / "saved-state")
        result = run("restore")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual((self.root / "outputs").read_text(), "restored=true\n")
        self.assertEqual((self.state / "submission.json").read_bytes(), original)
        environ["APPLE_STATUS"] = "Accepted"
        result = run("wait")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual((self.root / "calls").read_text().splitlines(), ["submit", "wait", "wait"])
        release.verify_bundle(self.bundle, self.ctx)

    def test_key_is_private_removed_after_errors_and_not_passed_in_environment(self):
        key = b"-----BEGIN " + b"PRIVATE KEY-----\nfixture\n-----END PRIVATE KEY-----\n"
        settings = {"MACOS_NOTARY_KEY": base64.b64encode(key).decode(),
                    "MACOS_NOTARY_KEY_ID": "KEY1234567", "MACOS_NOTARY_ISSUER_ID": self.identifier}
        paths = []
        def command(args, **kwargs):
            path = Path(args[args.index("--key") + 1])
            paths.append(path)
            self.assertEqual(path.read_bytes(), key)
            self.assertEqual(path.stat().st_mode & 0o777, 0o600)
            self.assertNotIn(settings["MACOS_NOTARY_KEY"], args)
            self.assertFalse(set(notarize.SETTINGS) & set(kwargs["env"]))
            raise subprocess.TimeoutExpired("wait", 1)
        with patch.dict(os.environ, settings), patch.object(subprocess, "run", side_effect=command), self.assertRaises(subprocess.TimeoutExpired):
            notarize.notary("wait", self.identifier)
        self.assertFalse(paths[0].exists())


if __name__ == "__main__":
    unittest.main()
