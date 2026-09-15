#!/usr/bin/env python3
"""Check a saved release with current verification code without publishing."""

import argparse
import json
import os
from pathlib import Path
import re
import subprocess
import sys

import release


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--run", required=True, type=int)
    parser.add_argument("--system", required=True, choices=["darwin", "linux", "windows"])
    parser.add_argument("--arch", required=True, choices=["amd64", "arm64"])
    parser.add_argument("--candidate-installers", action="store_true", help="Test current installer code separately from the saved signatures")
    args = parser.parse_args()
    if args.run <= 0:
        raise ValueError("A release run ID is required.")
    run = release.api(f"repos/{release.REPOSITORY}/actions/runs/{args.run}")
    if run["event"] != "release" or run["path"] != ".github/workflows/release.yml":
        raise ValueError("The fixture must come from the official release workflow.")
    artifacts = [item for page in release.pages(f"repos/{release.REPOSITORY}/actions/runs/{args.run}/artifacts?per_page=100")
                 for item in page["artifacts"] if re.fullmatch(r"release-[0-9]+-packages", item["name"])]
    if len(artifacts) != 1 or artifacts[0]["expired"]:
        raise ValueError("The run must have one unexpired package artifact.")
    release_id = int(artifacts[0]["name"].split("-")[1])
    metadata = release.api(f"repos/{release.REPOSITORY}/releases/{release_id}")
    ctx = {"tag": metadata["tag_name"], "commit": run["head_sha"], "release_id": release_id,
           "run_id": args.run, "prerelease": metadata["prerelease"]}
    release.version(ctx["tag"])
    comparison = release.api(f"repos/{release.REPOSITORY}/compare/{ctx['commit']}...main")
    if comparison["status"] not in ("ahead", "identical"):
        raise ValueError("The saved release commit is not an ancestor of main.")
    root = Path("release-bundle")
    if root.exists() or Path("smoke").exists():
        raise ValueError("Run saved-release checks in a clean checkout.")
    if not release.restore_files(root, ctx, "packages"):
        raise ValueError("The package artifact disappeared.")
    release.verify_bundle(root, ctx)
    # This is a fixture check. The hashed installer records the original expected team.
    setting = re.search(r"^EXPECTED_MACOS_TEAM_ID='([A-Z0-9]{10})'$", (root / "assets/install.sh").read_text(), re.MULTILINE)
    if not setting:
        raise ValueError("The saved shell installer has no valid Apple team ID.")
    team = setting[1]
    release.unpack(root / "assets" / release.archive_name(ctx["tag"], args.system, args.arch), Path("smoke"))
    event = Path("smoke/fixture-event.json").resolve()
    event.write_text(json.dumps({"action": "published", "release": metadata}))
    environ = {key: value for key, value in os.environ.items() if key not in ("GH_TOKEN", "GITHUB_TOKEN")}
    environ.update(GITHUB_EVENT_PATH=str(event), MACOS_SIGN_TEAM_ID=team)
    print(f"Checking saved {ctx['tag']} bytes from run {args.run} on {args.system}/{args.arch}.", flush=True)
    if args.system == "windows":
        for shell in ("pwsh", "powershell"):
            subprocess.run([shell, "-NoProfile", "-File", "scripts/verify-windows-release.ps1",
                            *(["-CandidateInstaller"] if args.candidate_installers else [])], env=environ, check=True)
    else:
        command = [sys.executable, "scripts/verify-unix-release.py"]
        if args.system == "darwin":
            command.append("--before-notarization")
        subprocess.run(command, env=environ, check=True)
    release.verify_bundle(root, ctx)
    print("Saved package checks passed. No release files were signed, submitted, attested, or published.")


if __name__ == "__main__":
    try:
        main()
    except (ValueError, KeyError, OSError, subprocess.CalledProcessError) as error:
        sys.exit(f"Saved release check failed: {error}")
