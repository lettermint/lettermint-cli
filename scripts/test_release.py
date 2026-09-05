import json
import os
from pathlib import Path
import shutil
import tarfile
import tempfile
import unittest
from unittest.mock import patch

import release


class ReleaseTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name) / "bundle"
        self.ctx = {"tag": "v1.2.3", "commit": "a" * 40, "release_id": 12,
                    "run_id": 34, "prerelease": False}

    def bundle(self, provenance=False):
        (self.root / "assets").mkdir(parents=True)
        (self.root / "cask").mkdir()
        (self.root / "cask/lettermint.rb").write_text("cask")
        for name in release.asset_names(self.ctx, provenance):
            (self.root / "assets" / name).write_text(name)
        checksums = "".join(f"{release.digest(self.root / 'assets' / name)}  {name}\n"
                            for name in sorted(release.asset_names(self.ctx)) if name != "checksums.txt")
        (self.root / "assets/checksums.txt").write_text(checksums)
        release.write_manifest(self.root, self.ctx, provenance)

    def test_versions(self):
        for valid in ("v1.0.0", "v0.1.2-rc.1", "v1.0.0-alpha-1"):
            release.version(valid)
        for invalid in ("1.0.0", "v01.0.0", "v1.0.0-rc..1", "v1.0.0-01", "v1.2.3+build", "v1.0.0\n", "v1.2.3;echo"):
            with self.subTest(invalid=invalid), self.assertRaises(ValueError):
                release.version(invalid)

    def test_prerelease_flag_matches_tag(self):
        event = {"action": "published", "release": {"id": 12, "tag_name": "v1.0.0-rc.1", "prerelease": False, "draft": False}}
        path = Path(self.temp.name) / "event.json"
        path.write_text(json.dumps(event))
        with patch.dict(os.environ, GITHUB_EVENT_PATH=str(path), GITHUB_REPOSITORY=release.REPOSITORY,
                        GITHUB_SHA="a" * 40, GITHUB_RUN_ID="34"):
            with self.assertRaises(ValueError):
                release.context()
            event["release"]["prerelease"] = True
            path.write_text(json.dumps(event))
            self.assertTrue(release.context()["prerelease"])

    def test_missing_signing_settings_stop_release(self):
        settings = dict.fromkeys(release.SIGNING_SETTINGS, "present")
        settings["MACOS_SIGN_TEAM_ID"] = "AB12345678"
        settings["WINDOWS_SIGN_THUMBPRINT"] = "A" * 40
        release.check_settings(settings)
        for name in release.SIGNING_SETTINGS:
            with self.subTest(name=name), self.assertRaisesRegex(ValueError, name):
                release.check_settings({key: value for key, value in settings.items() if key != name})

    def test_bundle_rejects_extra_files_changed_bytes_and_wrong_commit(self):
        self.bundle()
        release.verify_bundle(self.root, self.ctx)
        extra = self.root / "assets/private.key"
        extra.write_text("fixture")
        with self.assertRaisesRegex(ValueError, "unexpected"):
            release.verify_bundle(self.root, self.ctx)
        extra.unlink()
        with self.assertRaisesRegex(ValueError, "do not match"):
            release.verify_bundle(self.root, {**self.ctx, "commit": "b" * 40})
        (self.root / "assets/install.ps1").write_text("changed")
        with self.assertRaisesRegex(ValueError, "changed"):
            release.verify_bundle(self.root, self.ctx)

    def test_restore_reuses_exact_artifact(self):
        self.bundle()
        saved = Path(self.temp.name) / "saved"
        shutil.copytree(self.root, saved)
        def download(*args):
            self.assertEqual(args[:3], ("gh", "run", "download"))
            shutil.copytree(saved, self.root)
        with patch.object(release, "pages", return_value=[{"artifacts": [{"name": "release-12-packages", "expired": False}]}]), patch.object(release, "run", side_effect=download):
            self.assertTrue(release.restore(self.root, self.ctx, "packages"))
        release.verify_bundle(self.root, self.ctx)

    def test_missing_or_expired_artifact_cannot_rebuild_published_files(self):
        with patch.object(release, "pages", return_value=[{"artifacts": []}]), patch.object(release, "current_release", return_value={"assets": [{"name": "existing"}]}):
            with self.assertRaisesRegex(ValueError, "Do not rebuild"):
                release.restore(self.root, self.ctx, "packages")
        with patch.object(release, "pages", return_value=[{"artifacts": [{"name": "release-12-packages", "expired": True}]}]):
            with self.assertRaisesRegex(ValueError, "Do not rebuild"):
                release.restore(self.root, self.ctx, "packages")

    def test_deleted_verified_artifact_cannot_be_recreated(self):
        with patch.dict(os.environ, GITHUB_RUN_ATTEMPT="2"), patch.object(release, "pages", side_effect=[[{"artifacts": []}], [{"jobs": [{"name": "attest", "conclusion": "success"}]}]]), patch.object(release, "current_release", return_value={"assets": []}):
            with self.assertRaisesRegex(ValueError, "Do not rebuild"):
                release.restore(self.root, self.ctx, "verified")

    def test_upload_adds_only_missing_files_without_editing_notes(self):
        self.bundle(provenance=True)
        names = release.asset_names(self.ctx, True)
        existing = [{"name": names[0], "id": 1}]
        all_assets = [{"name": name, "id": index} for index, name in enumerate(names)]
        metadata = {"name": "Release title", "body": "Keep these notes", "prerelease": False}
        with patch.object(release, "gate"), patch.object(release, "current_release", return_value=metadata), patch.object(release, "existing_assets", side_effect=[existing, all_assets]), patch.object(release, "check_remote_asset") as check, patch.object(release, "run") as command:
            release.upload(self.root, self.ctx)
        self.assertEqual(command.call_count, len(names) - 1)
        self.assertEqual(check.call_count, len(names) + 1)
        for call in command.call_args_list:
            self.assertEqual(call.args[:3], ("gh", "release", "upload"))
            self.assertNotIn("--clobber", call.args)
        self.assertEqual(metadata, {"name": "Release title", "body": "Keep these notes", "prerelease": False})

    def test_conflicting_existing_asset_stops_before_upload(self):
        self.bundle(provenance=True)
        with patch.object(release, "gate"), patch.object(release, "existing_assets", return_value=[{"name": "install.ps1", "id": 1}]), patch.object(release, "check_remote_asset", side_effect=ValueError("different bytes")), patch.object(release, "run") as command:
            with self.assertRaisesRegex(ValueError, "different bytes"):
                release.upload(self.root, self.ctx)
            command.assert_not_called()

    def test_immutable_or_changed_release_stops(self):
        data = {"tag_name": "v1.2.3", "draft": False, "prerelease": False, "immutable": True}
        with patch.object(release, "api", return_value=data), self.assertRaisesRegex(ValueError, "Immutable"):
            release.current_release(self.ctx)

    def test_main_ancestry_and_tag_commit_are_required(self):
        with patch.object(release, "current_release"), patch.object(release, "run", side_effect=["", "", "b" * 40]):
            with self.assertRaisesRegex(ValueError, "original commit"):
                release.gate(self.ctx)
        with patch.object(release, "current_release"), patch.object(release, "run", side_effect=["", "", "a" * 40, "a" * 40, ""]) as command:
            release.gate(self.ctx)
            self.assertEqual(command.call_args.args, ("git", "merge-base", "--is-ancestor", "a" * 40, "refs/remotes/origin/main"))

    def test_prereleases_and_older_releases_do_not_update_tap(self):
        releases = [{"tag_name": "v2.0.0", "prerelease": False, "draft": False},
                    {"tag_name": "v3.0.0-rc.1", "prerelease": True, "draft": False}]
        self.assertFalse(release.newest_stable("v2.1.0-rc.1", releases))
        self.assertFalse(release.newest_stable("v1.0.0", releases))
        self.assertTrue(release.newest_stable("v2.0.0", releases))
        self.assertTrue(release.newest_stable("v2.1.0", releases))

    def test_archive_paths_and_links(self):
        for name in ("../file", "/tmp/file", "a/../../file", "C:/file", "a\\file"):
            with self.subTest(name=name), self.assertRaises(ValueError):
                release.safe_member(name)
        archive = Path(self.temp.name) / "package.tar.gz"
        with tarfile.open(archive, "w:gz") as package:
            item = tarfile.TarInfo("link")
            item.type = tarfile.SYMTYPE
            item.linkname = "../../outside"
            package.addfile(item)
        with self.assertRaisesRegex(ValueError, "regular"):
            release.unpack(archive, Path(self.temp.name) / "unpack")


if __name__ == "__main__":
    unittest.main()
