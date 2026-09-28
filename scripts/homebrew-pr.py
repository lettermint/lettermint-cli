#!/usr/bin/env python3
"""Check the stable cask and merge its pull request after required checks pass."""

import base64
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile

import release

TAP = "lettermint/homebrew-tap"
CASK = "Casks/lettermint.rb"


def optional(endpoint):
    result = subprocess.run(["gh", "api", endpoint], capture_output=True, text=True)
    data = json.loads(result.stdout)
    if result.returncode:
        if data.get("status") == "404":
            return None
        raise ValueError("Cannot read the Homebrew tap. Check the token permissions.")
    return data


def mutate(endpoint, method, data):
    with tempfile.TemporaryDirectory() as directory:
        body = Path(directory) / "request.json"
        body.write_text(json.dumps(data))
        return json.loads(release.run("gh", "api", endpoint, "--method", method, "--input", str(body)))


def cask_version(text):
    match = re.search(r'^\s*version "([0-9]+\.[0-9]+\.[0-9]+)"$', text, re.MULTILINE)
    if not match:
        raise ValueError("The cask must contain one stable version.")
    return release.version("v" + match[1])[0]


def enable_auto_merge(number, branch, text):
    pr = release.api(f"repos/{TAP}/pulls/{number}")
    if (pr["state"] != "open" or pr["draft"]
            or pr["base"]["ref"] != "main" or pr["head"]["ref"] != branch
            or pr["base"]["repo"]["full_name"] != TAP
            or (pr["head"]["repo"] or {}).get("full_name") != TAP):
        raise ValueError("The pull request does not match the release branch and tap.")
    head = pr["head"]["sha"]
    base = pr["base"]["sha"]
    # Check immutable commits, then bind the merge request to the same head.
    comparison = release.api(f"repos/{TAP}/compare/{base}...{head}")
    files = comparison["files"]
    if (len(files) != 1 or files[0]["filename"] != CASK
            or files[0]["status"] not in ("added", "modified")):
        raise ValueError("The pull request must change only the release cask.")
    cask = release.api(f"repos/{TAP}/contents/{CASK}?ref={head}")
    if cask["type"] != "file" or base64.b64decode(cask["content"]).decode() != text:
        raise ValueError("The pull request cask differs from the checked release cask.")
    release.run("gh", "pr", "merge", str(number), "--repo", TAP,
                "--auto", "--squash", "--match-head-commit", head)
    print("Automatic merge requested. GitHub must pass the required checks before merge.")


def main():
    if not os.environ.get("GH_TOKEN"):
        raise ValueError("GH_TOKEN must contain the release app token for the Homebrew tap.")
    ctx = release.context()
    root = Path("release-bundle")
    release.verify_bundle(root, ctx, provenance=True)
    if ctx["prerelease"]:
        raise ValueError("Pre-releases cannot update the stable cask.")
    text = (root / "cask/lettermint.rb").read_text()
    candidate = cask_version(text)
    if candidate != release.version(ctx["tag"])[0]:
        raise ValueError("The cask does not match the release.")
    current = optional(f"repos/{TAP}/contents/{CASK}?ref=main")
    if current:
        current_text = base64.b64decode(current["content"]).decode()
        if cask_version(current_text) >= candidate:
            print("The tap already has this version or a newer version. No update is required.")
            return
    subprocess.run(["brew", "tap", "lettermint/tap"], check=True)
    tap_path = Path(release.run("brew", "--repository", "lettermint/tap"))
    target = tap_path / CASK
    target.parent.mkdir(parents=True, exist_ok=True)
    target.write_text(text)
    subprocess.run(["brew", "style", "--fix", "--cask", str(target)], check=True)
    text = target.read_text()
    subprocess.run(["brew", "style", "--cask", str(target)], check=True)
    subprocess.run(["brew", "audit", "--cask", "--online", "lettermint/tap/lettermint"], check=True)
    subprocess.run(["brew", "install", "--cask", "lettermint/tap/lettermint"], check=True)
    try:
        result = json.loads(release.run("lettermint", "version", "--json"))
        if result["version"] != ctx["tag"][1:]:
            raise ValueError("The Homebrew install has the wrong version.")
        subprocess.run(["brew", "reinstall", "--cask", "lettermint/tap/lettermint"], check=True)
    finally:
        subprocess.run(["brew", "uninstall", "--cask", "lettermint/tap/lettermint"], check=True)

    # Use only tap API calls with the tap token. Do not store it in a Git remote.
    branch = "release/" + ctx["tag"]
    if not optional(f"repos/{TAP}/git/ref/heads/{branch}"):
        base = release.api(f"repos/{TAP}/git/ref/heads/main")["object"]["sha"]
        mutate(f"repos/{TAP}/git/refs", "POST", {"ref": "refs/heads/" + branch, "sha": base})
    proposed = optional(f"repos/{TAP}/contents/{CASK}?ref={branch}")
    if proposed and base64.b64decode(proposed["content"]).decode() == text:
        pass
    else:
        if proposed and proposed["sha"] != (current or {}).get("sha"):
            raise ValueError("The release branch has a different cask. Review it before a retry.")
        data = {"message": f"chore: update Lettermint to {ctx['tag']}", "branch": branch,
                "content": base64.b64encode(text.encode()).decode()}
        if proposed:
            data["sha"] = proposed["sha"]
        mutate(f"repos/{TAP}/contents/{CASK}", "PUT", data)
    prs = release.api(f"repos/{TAP}/pulls?state=open&head=lettermint:{branch}&base=main")
    if len(prs) > 1:
        raise ValueError("More than one pull request matches the release branch.")
    pr = prs[0] if prs else mutate(f"repos/{TAP}/pulls", "POST", {
        "head": branch, "base": "main", "title": f"Update Lettermint to {ctx['tag']}",
        "body": f"Update the cask to [Lettermint {ctx['tag']}](https://github.com/{release.REPOSITORY}/releases/tag/{ctx['tag']}).\n\n"
                "The release workflow checked the signed packages, checksums, provenance, cask style, online audit, installation, replacement, and removal.\n\n"
                "This pull request will merge automatically after the required cask checks pass.\n",
    })
    print(pr["html_url"])
    enable_auto_merge(pr["number"], branch, text)


if __name__ == "__main__":
    main()
