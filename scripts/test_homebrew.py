import base64
from contextlib import chdir
import copy
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch


spec = importlib.util.spec_from_file_location("homebrew_pr", Path(__file__).with_name("homebrew-pr.py"))
homebrew = importlib.util.module_from_spec(spec)
spec.loader.exec_module(homebrew)


class HomebrewTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.enterContext(chdir(self.root))
        self.enterContext(patch.dict(os.environ, GH_TOKEN="test-token"))
        self.ctx = {"tag": "v1.2.3", "prerelease": False}
        self.text = 'cask "lettermint" do\n  version "1.2.3"\nend\n'
        cask = self.root / "release-bundle/cask/lettermint.rb"
        cask.parent.mkdir(parents=True)
        cask.write_text(self.text)
        self.pr = {
            "number": 12, "html_url": f"https://github.com/{homebrew.TAP}/pull/12",
            "state": "open", "draft": False,
            "base": {"ref": "main", "sha": "a" * 40, "repo": {"full_name": homebrew.TAP}},
            "head": {"ref": "release/v1.2.3", "sha": "b" * 40, "repo": {"full_name": homebrew.TAP}},
        }
        self.files = [{"filename": homebrew.CASK, "status": "modified"}]
        self.cask = self.content(self.text)
        self.existing = []
        self.api = self.enterContext(patch.object(homebrew.release, "api", side_effect=self.api_response))
        self.command = self.enterContext(patch.object(homebrew.release, "run", side_effect=self.run_response))
        self.brew = self.enterContext(patch.object(homebrew.subprocess, "run"))
        self.optional = self.enterContext(patch.object(homebrew, "optional", side_effect=[
            self.content(self.text.replace("1.2.3", "1.2.2")), None, None,
        ]))
        self.mutate = self.enterContext(patch.object(homebrew, "mutate", return_value=self.pr))
        self.enterContext(patch.object(homebrew.release, "context", return_value=self.ctx))
        self.verify = self.enterContext(patch.object(homebrew.release, "verify_bundle"))

    @staticmethod
    def content(text):
        return {"type": "file", "sha": "c" * 40, "content": base64.b64encode(text.encode()).decode()}

    def api_response(self, endpoint):
        prefix = f"repos/{homebrew.TAP}"
        if endpoint == f"{prefix}/pulls/12":
            return copy.deepcopy(self.pr)
        if endpoint == f"{prefix}/compare/{'a' * 40}...{'b' * 40}":
            return {"files": self.files}
        if endpoint == f"{prefix}/contents/{homebrew.CASK}?ref={'b' * 40}":
            return self.cask
        if endpoint == f"{prefix}/git/ref/heads/main":
            return {"object": {"sha": "a" * 40}}
        if endpoint == f"{prefix}/pulls?state=open&head=lettermint:release/v1.2.3&base=main":
            return self.existing
        self.fail(f"Unexpected API endpoint: {endpoint}")

    def run_response(self, *args):
        if args == ("brew", "--repository", "lettermint/tap"):
            return str(self.root / "tap")
        if args == ("lettermint", "version", "--json"):
            return json.dumps({"version": "1.2.3"})
        if args[:3] == ("gh", "pr", "merge"):
            return ""
        self.fail(f"Unexpected command: {args}")

    def assert_merge_requested(self):
        self.command.assert_any_call("gh", "pr", "merge", "12", "--repo", homebrew.TAP,
                                     "--auto", "--squash", "--match-head-commit", "b" * 40)

    def assert_no_merge(self):
        self.assertFalse(any(call.args[:3] == ("gh", "pr", "merge") for call in self.command.call_args_list))

    def test_new_pull_request_requests_automatic_merge(self):
        homebrew.main()
        self.verify.assert_called_once_with(Path("release-bundle"), self.ctx, provenance=True)
        self.assert_merge_requested()
        request = self.mutate.call_args_list[-1]
        self.assertEqual(request.args[:2], (f"repos/{homebrew.TAP}/pulls", "POST"))
        self.assertEqual(request.args[2]["head"], "release/v1.2.3")
        self.assertIn("merge automatically", request.args[2]["body"])

    def test_existing_pull_request_retry_requests_merge_without_duplicate(self):
        self.existing = [self.pr]
        self.optional.side_effect = [self.content(self.text.replace("1.2.3", "1.2.2")),
                                     {"object": {"sha": "b" * 40}}, self.content(self.text)]
        homebrew.main()
        self.mutate.assert_not_called()
        self.assert_merge_requested()

    def test_same_or_newer_version_on_main_does_nothing(self):
        for version in ("1.2.3", "1.3.0"):
            with self.subTest(version=version):
                self.optional.side_effect = [self.content(self.text.replace("1.2.3", version))]
                homebrew.main()
        self.mutate.assert_not_called()
        self.brew.assert_not_called()
        self.assert_no_merge()

    def test_prerelease_stops_before_tap_changes(self):
        self.ctx["prerelease"] = True
        with self.assertRaisesRegex(ValueError, "Pre-releases"):
            homebrew.main()
        self.optional.assert_not_called()
        self.mutate.assert_not_called()

    def test_different_release_branch_cask_is_not_overwritten(self):
        different = self.content(self.text + "# changed\n")
        different["sha"] = "d" * 40
        self.optional.side_effect = [self.content(self.text.replace("1.2.3", "1.2.2")),
                                     {"object": {"sha": "b" * 40}}, different]
        with self.assertRaisesRegex(ValueError, "different cask"):
            homebrew.main()
        self.mutate.assert_not_called()
        self.assert_no_merge()

    def test_unexpected_paths_and_renames_block_merge(self):
        for files in ([], self.files + [{"filename": "README.md", "status": "modified"}],
                      [{"filename": homebrew.CASK, "status": "renamed"}]):
            with self.subTest(files=files):
                self.files = files
                with self.assertRaisesRegex(ValueError, "only the release cask"):
                    homebrew.enable_auto_merge(12, "release/v1.2.3", self.text)
        self.assert_no_merge()

    def test_changed_content_at_pr_head_blocks_merge(self):
        self.cask = self.content(self.text + "# changed\n")
        with self.assertRaisesRegex(ValueError, "differs"):
            homebrew.enable_auto_merge(12, "release/v1.2.3", self.text)
        self.assert_no_merge()

    def test_wrong_pr_target_or_source_blocks_merge(self):
        original = copy.deepcopy(self.pr)
        for kind in ("closed", "draft", "base", "head", "fork", "deleted-repo"):
            with self.subTest(kind=kind):
                self.pr = copy.deepcopy(original)
                if kind == "closed":
                    self.pr["state"] = "closed"
                elif kind == "draft":
                    self.pr["draft"] = True
                elif kind in ("base", "head"):
                    self.pr[kind]["ref"] = "unexpected"
                elif kind == "fork":
                    self.pr["head"]["repo"]["full_name"] = "other/homebrew-tap"
                else:
                    self.pr["head"]["repo"] = None
                with self.assertRaisesRegex(ValueError, "does not match"):
                    homebrew.enable_auto_merge(12, "release/v1.2.3", self.text)
        self.assert_no_merge()

    def test_merge_request_failure_fails_job_and_retry_reuses_pr(self):
        original = self.run_response

        def reject_merge(*args):
            if args[:3] == ("gh", "pr", "merge"):
                raise subprocess.CalledProcessError(1, args)
            return original(*args)

        self.command.side_effect = reject_merge
        with self.assertRaises(subprocess.CalledProcessError):
            homebrew.main()
        self.existing = [self.pr]
        self.mutate.reset_mock()
        self.optional.side_effect = [self.content(self.text.replace("1.2.3", "1.2.2")),
                                     {"object": {"sha": "b" * 40}}, self.content(self.text)]
        self.command.side_effect = original
        homebrew.main()
        self.mutate.assert_not_called()
        self.assert_merge_requested()

    def test_missing_app_token_stops_before_validation(self):
        with patch.dict(os.environ, GH_TOKEN=""), self.assertRaisesRegex(ValueError, "release app token"):
            homebrew.main()
        self.verify.assert_not_called()


if __name__ == "__main__":
    unittest.main()
