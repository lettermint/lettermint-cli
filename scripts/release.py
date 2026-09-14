#!/usr/bin/env python3
"""Validate release inputs and move only verified files between release steps."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tarfile
import tempfile
import zipfile

REPOSITORY = "lettermint/lettermint-cli"
PLATFORMS = [(system, arch) for system in ("darwin", "linux", "windows")
             for arch in ("amd64", "arm64")]
SIGNING_SETTINGS = (
    "CLI_OAUTH_CLIENT_ID", "AZURE_CLIENT_ID", "AZURE_TENANT_ID",
    "AZURE_SUBSCRIPTION_ID", "ARTIFACT_SIGNING_ENDPOINT",
    "ARTIFACT_SIGNING_ACCOUNT_NAME", "ARTIFACT_SIGNING_CERTIFICATE_PROFILE_NAME",
    "MACOS_SIGN_P12", "MACOS_SIGN_PASSWORD", "MACOS_NOTARY_ISSUER_ID",
    "MACOS_NOTARY_KEY_ID", "MACOS_NOTARY_KEY", "MACOS_SIGN_TEAM_ID",
)
TAG = re.compile(r"v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z.-]+))?")


def run(*args):
    return subprocess.check_output(args, text=True).strip()


def api(endpoint):
    return json.loads(run("gh", "api", endpoint))


def pages(endpoint):
    return json.loads(run("gh", "api", "--paginate", "--slurp", endpoint))


def version(tag):
    match = TAG.fullmatch(tag)
    if not match:
        raise ValueError("Use a version tag such as v1.0.0 or v1.0.0-rc.1.")
    if match[4]:
        for part in match[4].split("."):
            if not part or (part.isdigit() and len(part) > 1 and part.startswith("0")):
                raise ValueError("The pre-release version is invalid.")
    return tuple(int(match[i]) for i in (1, 2, 3)), match[4]


def context():
    event = json.loads(Path(os.environ["GITHUB_EVENT_PATH"]).read_text())
    release = event["release"]
    tag = release["tag_name"]
    _, suffix = version(tag)
    if event["action"] != "published" or release["draft"]:
        raise ValueError("A published release is required.")
    if bool(suffix) != release["prerelease"]:
        raise ValueError("The version suffix must match the pre-release setting.")
    if os.environ["GITHUB_REPOSITORY"] != REPOSITORY:
        raise ValueError("Releases must run in the official CLI repository.")
    sha = os.environ["GITHUB_SHA"]
    if not re.fullmatch(r"[0-9a-f]{40}", sha):
        raise ValueError("The release commit is invalid.")
    return {"tag": tag, "commit": sha, "release_id": release["id"],
            "run_id": int(os.environ["GITHUB_RUN_ID"]), "prerelease": bool(suffix)}


def current_release(ctx):
    release = api(f"repos/{REPOSITORY}/releases/{ctx['release_id']}")
    if release["tag_name"] != ctx["tag"] or release["draft"] or release["prerelease"] != ctx["prerelease"]:
        raise ValueError("The release settings changed. Restore them before a retry.")
    if release.get("immutable"):
        raise ValueError("Immutable releases cannot accept files after publication.")
    return release


def gate(ctx):
    current_release(ctx)
    run("git", "fetch", "--no-tags", "origin", "+refs/heads/main:refs/remotes/origin/main")
    # The checkout uses the event SHA. Reject a tag moved after publication.
    run("git", "fetch", "--force", "origin", f"refs/tags/{ctx['tag']}:refs/tags/{ctx['tag']}")
    if run("git", "rev-parse", f"refs/tags/{ctx['tag']}^{{commit}}") != ctx["commit"]:
        raise ValueError("The release tag does not match its original commit.")
    if run("git", "rev-parse", "HEAD") != ctx["commit"]:
        raise ValueError("The checkout does not match the release commit.")
    run("git", "merge-base", "--is-ancestor", ctx["commit"], "refs/remotes/origin/main")


def check_settings(environ):
    missing = [name for name in SIGNING_SETTINGS if not environ.get(name)]
    if missing:
        raise ValueError("Missing release settings: " + ", ".join(missing))
    if not re.fullmatch(r"[A-Z0-9]{10}", environ["MACOS_SIGN_TEAM_ID"]):
        raise ValueError("MACOS_SIGN_TEAM_ID must be an Apple team identifier.")
    for name in ("AZURE_CLIENT_ID", "AZURE_TENANT_ID", "AZURE_SUBSCRIPTION_ID"):
        if not re.fullmatch(r"[0-9a-fA-F]{8}(?:-[0-9a-fA-F]{4}){3}-[0-9a-fA-F]{12}", environ[name]):
            raise ValueError(f"{name} must be a UUID.")
    if not re.fullmatch(r"https://[a-z0-9-]+\.codesigning\.azure\.net/?", environ["ARTIFACT_SIGNING_ENDPOINT"]):
        raise ValueError("ARTIFACT_SIGNING_ENDPOINT must be an Azure HTTPS signing endpoint.")
    for name in ("ARTIFACT_SIGNING_ACCOUNT_NAME", "ARTIFACT_SIGNING_CERTIFICATE_PROFILE_NAME"):
        if not re.fullmatch(r"[A-Za-z0-9-]{1,100}", environ[name]):
            raise ValueError(f"{name} must be an Azure resource name.")


def archive_name(tag, system, arch):
    version(tag)
    suffix = "zip" if system == "windows" else "tar.gz"
    return f"lettermint_{tag[1:]}_{system}_{arch}.{suffix}"


def asset_names(ctx, provenance=False):
    names = [archive_name(ctx["tag"], *platform) for platform in PLATFORMS]
    names += ["install.sh", "install.ps1", "uninstall.ps1", "checksums.txt"]
    return names + (["provenance.jsonl"] if provenance else [])


def digest(path):
    with Path(path).open("rb") as handle:
        return hashlib.file_digest(handle, "sha256").hexdigest()


def write_manifest(root, ctx, provenance=False):
    paths = ["assets/" + name for name in asset_names(ctx, provenance)] + ["cask/lettermint.rb"]
    data = {"release": ctx, "sha256": {name: digest(root / name) for name in paths}}
    (root / "manifest.json").write_text(json.dumps(data, indent=2) + "\n")


def verify_bundle(root, ctx, provenance=False):
    expected = {"assets/" + name for name in asset_names(ctx, provenance)} | {"cask/lettermint.rb"}
    data = json.loads((root / "manifest.json").read_text())
    if data["release"] != ctx or set(data["sha256"]) != expected:
        raise ValueError("The saved packages do not match this release.")
    actual = {str(path.relative_to(root)).replace(os.sep, "/") for path in root.rglob("*") if path.is_file()}
    if actual != expected | {"manifest.json"} or any(path.is_symlink() for path in root.rglob("*")):
        raise ValueError("The release bundle contains unexpected files or links.")
    for name, checksum in data["sha256"].items():
        if digest(root / name) != checksum:
            raise ValueError(f"The saved file changed: {name}")
    checksums = "".join(f"{digest(root / 'assets' / name)}  {name}\n"
                        for name in sorted(asset_names(ctx)) if name != "checksums.txt")
    if (root / "assets/checksums.txt").read_text() != checksums:
        raise ValueError("The package checksums are invalid.")


def prepare_shell_installer(source, target, team_id):
    if not re.fullmatch(r"[A-Z0-9]{10}", team_id):
        raise ValueError("MACOS_SIGN_TEAM_ID must be an Apple team identifier.")
    template = source.read_text(encoding="utf-8")
    setting = "EXPECTED_MACOS_TEAM_ID='REPLACE_WITH_APPLE_TEAM_ID'"
    if template.count(setting) != 1:
        raise ValueError("The shell installer must contain one Apple team ID setting.")
    target.write_text(template.replace(setting, f"EXPECTED_MACOS_TEAM_ID='{team_id}'"),
                      encoding="utf-8", newline="\n")


def prepare_windows_installers(source, target, tag):
    version(tag)
    template = (source / "install.ps1").read_text(encoding="utf-8")
    placeholder = "REPLACE_WITH_RELEASE_TAG"
    if template.count(placeholder) != 1:
        raise ValueError("The PowerShell installer must contain one release tag setting.")
    target.mkdir(parents=True, exist_ok=True)
    (target / "install.ps1").write_text(template.replace(placeholder, tag), encoding="utf-8", newline="\r\n")
    shutil.copyfile(source / "uninstall.ps1", target / "uninstall.ps1")


def stage(root, ctx):
    root.mkdir()
    (root / "assets").mkdir()
    (root / "cask").mkdir()
    for system, arch in PLATFORMS:
        name = archive_name(ctx["tag"], system, arch)
        shutil.copyfile(Path("dist") / name, root / "assets" / name)
    for name in ("install.ps1", "uninstall.ps1"):
        shutil.copyfile(Path("packaging") / name, root / "assets" / name)
    prepare_shell_installer(Path("scripts/install.sh"), root / "assets/install.sh",
                            os.environ.get("MACOS_SIGN_TEAM_ID", ""))
    casks = list(Path("dist").glob("**/lettermint.rb"))
    if len(casks) != 1:
        raise ValueError("Exactly one generated cask is required.")
    shutil.copyfile(casks[0], root / "cask/lettermint.rb")
    text = "".join(f"{digest(root / 'assets' / name)}  {name}\n"
                   for name in sorted(asset_names(ctx)) if name != "checksums.txt")
    (root / "assets/checksums.txt").write_text(text, newline="\n")
    write_manifest(root, ctx)
    verify_bundle(root, ctx)


def artifact_name(ctx, kind):
    return f"release-{ctx['release_id']}-{kind}"


def completed_before(ctx, kind):
    job_name = "package" if kind == "packages" else "attest"
    for attempt in range(1, int(os.environ.get("GITHUB_RUN_ATTEMPT", "1"))):
        jobs = [job for page in pages(f"repos/{REPOSITORY}/actions/runs/{ctx['run_id']}/attempts/{attempt}/jobs?per_page=100")
                for job in page["jobs"]]
        if any(job["name"] == job_name and job["conclusion"] == "success" for job in jobs):
            return True
    return False


def restore(root, ctx, kind):
    artifacts = [item for page in pages(f"repos/{REPOSITORY}/actions/runs/{ctx['run_id']}/artifacts?per_page=100")
                 for item in page["artifacts"] if item["name"] == artifact_name(ctx, kind)]
    if len(artifacts) > 1 or any(item["expired"] for item in artifacts):
        raise ValueError("Saved release files are ambiguous or expired. Do not rebuild this release.")
    if not artifacts:
        if current_release(ctx)["assets"] or completed_before(ctx, kind):
            raise ValueError("Release files exist but the saved packages are missing. Do not rebuild this release.")
        return False
    if root.exists():
        shutil.rmtree(root)
    run("gh", "run", "download", str(ctx["run_id"]), "--repo", REPOSITORY,
        "--name", artifact_name(ctx, kind), "--dir", str(root))
    verify_bundle(root, ctx, provenance=kind == "verified")
    return True


def safe_member(name):
    path = Path(name)
    if not name or "\\" in name or ":" in name or path.is_absolute() or ".." in path.parts:
        raise ValueError("An archive contains an unsafe path.")
    return path


def unpack(archive, target):
    target.mkdir(parents=True, exist_ok=True)
    total = 0
    seen = set()

    def write(name, source, size, executable):
        nonlocal total
        path = safe_member(name)
        total += size
        if path in seen or total > 150 * 1024 * 1024:
            raise ValueError("An archive has duplicate files or exceeds the size limit.")
        seen.add(path)
        output = target / path
        output.parent.mkdir(parents=True, exist_ok=True)
        with output.open("xb") as handle:
            shutil.copyfileobj(source, handle)
        output.chmod(0o755 if executable else 0o644)

    if archive.suffix == ".zip":
        with zipfile.ZipFile(archive) as package:
            for item in package.infolist():
                safe_member(item.filename)
                if (item.external_attr >> 16) & 0o170000 == 0o120000:
                    raise ValueError("Archive links are not permitted.")
                if not item.is_dir():
                    with package.open(item) as source:
                        write(item.filename, source, item.file_size, False)
    else:
        with tarfile.open(archive) as package:
            for item in package:
                safe_member(item.name)
                if item.isdir():
                    continue
                if not item.isfile():
                    raise ValueError("Only regular archive files are permitted.")
                with package.extractfile(item) as source:
                    write(item.name, source, item.size, bool(item.mode & 0o111))


def scan_contents(root, ctx, target):
    verify_bundle(root, ctx)
    target.mkdir()
    for system, arch in PLATFORMS:
        directory = target / f"{system}-{arch}"
        unpack(root / "assets" / archive_name(ctx["tag"], system, arch), directory)
        binary = directory / ("lettermint.exe" if system == "windows" else "lettermint")
        # Gitleaks skips binary files. Scan their printable strings as text too.
        strings = re.findall(rb"[\x20-\x7e]{8,}", binary.read_bytes())
        (directory / "binary-strings.txt").write_bytes(b"\n".join(strings))
    for name in ("install.sh", "install.ps1", "uninstall.ps1", "checksums.txt"):
        shutil.copyfile(root / "assets" / name, target / name)


def existing_assets(ctx):
    return [asset for page in pages(f"repos/{REPOSITORY}/releases/{ctx['release_id']}/assets?per_page=100") for asset in page]


def check_remote_asset(asset, local):
    with tempfile.TemporaryDirectory() as directory:
        path = Path(directory) / "asset"
        with path.open("wb") as handle:
            subprocess.run(["gh", "api", "-H", "Accept: application/octet-stream",
                            f"repos/{REPOSITORY}/releases/assets/{asset['id']}"], stdout=handle, check=True)
        if digest(path) != digest(local):
            raise ValueError(f"The existing release asset has different bytes: {local.name}")


def upload(root, ctx):
    gate(ctx)
    verify_bundle(root, ctx, provenance=True)
    assets = existing_assets(ctx)
    names = asset_names(ctx, provenance=True)
    if len({asset["name"] for asset in assets}) != len(assets):
        raise ValueError("The release has duplicate asset names.")
    if any(asset["name"] not in names for asset in assets):
        raise ValueError("The release has an unexpected asset. Review it before a retry.")
    # Check all existing files before adding any missing file. Never use --clobber.
    for asset in assets:
        check_remote_asset(asset, root / "assets" / asset["name"])
    present = {asset["name"] for asset in assets}
    for name in names:
        if name not in present:
            current_release(ctx)
            run("gh", "release", "upload", ctx["tag"], str(root / "assets" / name), "--repo", REPOSITORY)
    final = existing_assets(ctx)
    if {asset["name"] for asset in final} != set(names):
        raise ValueError("The release asset list is incomplete.")
    for asset in final:
        check_remote_asset(asset, root / "assets" / asset["name"])
    # Asset-only requests leave the title, notes, and pre-release status unchanged.
    current_release(ctx)


def newest_stable(tag, releases):
    candidate, suffix = version(tag)
    if suffix:
        return False
    for release in releases:
        if release["draft"] or release["prerelease"]:
            continue
        try:
            other, other_suffix = version(release["tag_name"])
        except ValueError:
            continue
        if not other_suffix and other > candidate:
            return False
    return True


def output(name, value):
    with open(os.environ["GITHUB_OUTPUT"], "a") as handle:
        handle.write(f"{name}={value}\n")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", choices=["gate", "settings", "prepare-windows", "restore", "stage", "verify", "unpack", "scan", "seal", "upload", "tap-gate"])
    parser.add_argument("--root", type=Path, default=Path("release-bundle"))
    parser.add_argument("--kind", choices=["packages", "verified"], default="packages")
    parser.add_argument("--system", choices=["darwin", "linux", "windows"])
    parser.add_argument("--arch", choices=["amd64", "arm64"])
    parser.add_argument("--target", type=Path, default=Path("smoke"))
    args = parser.parse_args()
    if args.command == "settings":
        check_settings(os.environ)
        return
    ctx = context()
    if args.command == "gate":
        gate(ctx)
    elif args.command == "prepare-windows":
        prepare_windows_installers(Path("scripts"), Path("packaging"), ctx["tag"])
    elif args.command == "restore":
        output("restored", str(restore(args.root, ctx, args.kind)).lower())
    elif args.command == "stage":
        stage(args.root, ctx)
    elif args.command == "verify":
        verify_bundle(args.root, ctx, args.kind == "verified")
    elif args.command == "unpack":
        verify_bundle(args.root, ctx)
        unpack(args.root / "assets" / archive_name(ctx["tag"], args.system, args.arch), args.target)
    elif args.command == "scan":
        scan_contents(args.root, ctx, args.target)
    elif args.command == "seal":
        write_manifest(args.root, ctx, provenance=True)
        verify_bundle(args.root, ctx, provenance=True)
    elif args.command == "upload":
        upload(args.root, ctx)
    elif args.command == "tap-gate":
        current_release(ctx)
        releases = [item for page in pages(f"repos/{REPOSITORY}/releases?per_page=100") for item in page]
        output("eligible", str(newest_stable(ctx["tag"], releases)).lower())


if __name__ == "__main__":
    try:
        main()
    except (ValueError, KeyError, OSError, subprocess.CalledProcessError) as error:
        sys.exit(f"Release check failed: {error}")
