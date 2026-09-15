#!/usr/bin/env python3
"""Submit signed macOS packages once and resume the Apple wait on a retry."""

import argparse
import base64
from contextlib import contextmanager
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile
import zipfile

import release

SETTINGS = ("MACOS_NOTARY_ISSUER_ID", "MACOS_NOTARY_KEY_ID", "MACOS_NOTARY_KEY")
SUBMIT_STEP = "Submit the signed macOS binary"
WAIT_SECONDS = 3600


class PendingError(ValueError):
    pass


def check_settings(environ):
    missing = [name for name in SETTINGS if not environ.get(name)]
    if missing:
        raise ValueError("Missing notarization settings: " + ", ".join(missing))
    if not re.fullmatch(r"[A-Za-z0-9]{10,}", environ["MACOS_NOTARY_KEY_ID"]):
        raise ValueError("MACOS_NOTARY_KEY_ID must be an App Store Connect key ID.")
    if not re.fullmatch(r"[0-9a-fA-F]{8}(?:-[0-9a-fA-F]{4}){3}-[0-9a-fA-F]{12}", environ["MACOS_NOTARY_ISSUER_ID"]):
        raise ValueError("MACOS_NOTARY_ISSUER_ID must be a UUID.")
    try:
        key = base64.b64decode("".join(environ["MACOS_NOTARY_KEY"].split()), validate=True)
    except ValueError:
        raise ValueError("MACOS_NOTARY_KEY must contain a base64 private key.") from None
    if not key.startswith(b"-----BEGIN PRIVATE KEY-----"):
        raise ValueError("MACOS_NOTARY_KEY must contain a PKCS#8 private key.")
    return key


def execute(args, timeout=120):
    environ = {name: value for name, value in os.environ.items() if name not in SETTINGS}
    return subprocess.run(args, capture_output=True, text=True, timeout=timeout, env=environ)


def notary(*args, timeout=120):
    key = check_settings(os.environ)
    with tempfile.TemporaryDirectory(prefix="lettermint-notary-key-") as directory:
        path = Path(directory) / "AuthKey.p8"
        with path.open("xb") as handle:
            path.chmod(0o600)
            handle.write(key)
        return execute(["xcrun", "notarytool", *args, "--key", str(path),
                        "--key-id", os.environ["MACOS_NOTARY_KEY_ID"],
                        "--issuer", os.environ["MACOS_NOTARY_ISSUER_ID"]], timeout=timeout)


def verify_signature(binary, team):
    result = execute(["codesign", "--verify", "--strict", "--verbose=2", str(binary)])
    if result.returncode:
        raise ValueError("The macOS binary has an invalid signature.")
    result = execute(["codesign", "--display", "--verbose=4", str(binary)])
    if result.returncode or f"TeamIdentifier={team}" not in result.stderr.splitlines():
        raise ValueError("The macOS binary has the wrong Apple publisher.")


@contextmanager
def payload(bundle, ctx, arch, team):
    if arch not in ("amd64", "arm64") or not re.fullmatch(r"[A-Z0-9]{10}", team):
        raise ValueError("A supported architecture and Apple team identifier are required.")
    release.verify_bundle(bundle, ctx)
    archive = bundle / "assets" / release.archive_name(ctx["tag"], "darwin", arch)
    with tempfile.TemporaryDirectory(prefix="lettermint-notary-") as directory:
        root = Path(directory)
        release.unpack(archive, root / "package")
        binary = root / "package/lettermint"
        verify_signature(binary, team)
        target = root / "submission.zip"
        # Stable ZIP metadata lets a retry check the original submission payload.
        entry = zipfile.ZipInfo("lettermint", date_time=(1980, 1, 1, 0, 0, 0))
        entry.create_system = 3
        entry.external_attr = 0o100755 << 16
        with zipfile.ZipFile(target, "w", compression=zipfile.ZIP_STORED) as package:
            package.writestr(entry, binary.read_bytes())
        binding = {"schema": 1, "release": ctx, "arch": arch, "team_id": team,
                   "archive_sha256": release.digest(archive),
                   "binary_sha256": release.digest(binary),
                   "payload_sha256": release.digest(target)}
        yield binding, target


def submission_id(value):
    if not isinstance(value, str) or not re.fullmatch(r"[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}", value):
        raise ValueError("Apple returned an invalid submission ID.")
    return value


def read_state(root, binding):
    if {path.name for path in root.iterdir()} != {"submission.json"}:
        raise ValueError("The saved notarization state contains unexpected files.")
    path = root / "submission.json"
    if path.is_symlink() or not path.is_file():
        raise ValueError("The saved notarization state is not a regular file.")
    state = json.loads(path.read_text())
    if not isinstance(state, dict):
        raise ValueError("The saved notarization state is invalid.")
    identifier = submission_id(state.get("submission_id"))
    if state != {**binding, "submission_id": identifier}:
        raise ValueError("The saved submission does not match this release, publisher, or package bytes.")
    return identifier


def submitted_before(ctx, arch):
    for attempt in range(1, int(os.environ.get("GITHUB_RUN_ATTEMPT", "1"))):
        endpoint = f"repos/{release.REPOSITORY}/actions/runs/{ctx['run_id']}/attempts/{attempt}/jobs?per_page=100"
        for page in release.pages(endpoint):
            for job in page["jobs"]:
                if job["name"] != f"notarize ({arch})":
                    continue
                for step in job.get("steps", []):
                    if step["name"] == SUBMIT_STEP and step["conclusion"] != "skipped":
                        return True
    return False


def restore(root, binding):
    ctx, arch = binding["release"], binding["arch"]
    if release.restore_files(root, ctx, f"notary-{arch}"):
        identifier = read_state(root, binding)
        print(f"Resuming Apple submission {identifier} for {arch}.")
        return True
    if submitted_before(ctx, arch):
        raise ValueError("An earlier Apple submission may exist, but its saved state is missing. "
                         "Recover the original submission state before retrying. Do not resubmit.")
    return False


def response(result):
    try:
        data = json.loads(result.stdout)
    except (ValueError, TypeError):
        raise ValueError(f"Apple returned no valid JSON response (exit code {result.returncode}).") from None
    if not isinstance(data, dict):
        raise ValueError("Apple returned an invalid response.")
    return data


def submit(root, binding, archive):
    if root.exists():
        raise ValueError("Notarization state already exists. Resume that submission instead.")
    # A completed upload must be saved before any processing wait starts.
    result = notary("submit", str(archive), "--no-wait", "--output-format", "json", timeout=600)
    if result.returncode:
        raise ValueError(f"Apple upload failed (exit code {result.returncode}). Do not assume no submission exists.")
    data = response(result)
    identifier = submission_id(data.get("id"))
    root.mkdir(parents=True)
    target = root / "submission.json"
    temporary = root / "submission.tmp"
    temporary.write_text(json.dumps({**binding, "submission_id": identifier}, indent=2) + "\n")
    temporary.replace(target)
    print(f"Uploaded {binding['arch']} to Apple. Submission ID: {identifier}.")


def wait(root, binding, log):
    identifier = read_state(root, binding)
    pending = (f"Apple acceptance is not yet confirmed for submission {identifier}. The signed packages and submission ID "
               "are saved. Use Re-run failed jobs in this workflow run to resume.")
    try:
        result = notary("wait", identifier, "--timeout", f"{WAIT_SECONDS}s", "--output-format", "json",
                        timeout=WAIT_SECONDS + 120)
    except subprocess.TimeoutExpired:
        raise PendingError(pending) from None
    if result.returncode == 124:
        raise PendingError(pending)
    data = response(result)
    if data.get("id") != identifier:
        raise ValueError("Apple returned a status for a different submission.")
    status = data.get("status")
    if status == "Accepted" and result.returncode == 0:
        print(f"Apple accepted {binding['arch']}. Submission ID: {identifier}.")
        return
    if status == "In Progress":
        raise PendingError(pending)
    if status in ("Invalid", "Rejected"):
        try:
            notary("log", identifier, str(log))
        except (OSError, ValueError, subprocess.TimeoutExpired):
            pass
        raise ValueError(f"Apple returned {status} for submission {identifier}. Check the notarization log.")
    raise ValueError(f"Apple status check failed for submission {identifier} (exit code {result.returncode}). "
                     "The saved submission can be checked again on a retry.")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", choices=["settings", "restore", "submit", "wait"])
    parser.add_argument("--arch", required=True, choices=["amd64", "arm64"])
    parser.add_argument("--bundle", type=Path, default=Path("release-bundle"))
    parser.add_argument("--state", type=Path, default=Path("notarization-state"))
    args = parser.parse_args()
    if args.command == "settings":
        check_settings(os.environ)
        return
    with payload(args.bundle, release.context(), args.arch, os.environ.get("MACOS_SIGN_TEAM_ID", "")) as (binding, archive):
        if args.command == "restore":
            release.output("restored", str(restore(args.state, binding)).lower())
        elif args.command == "submit":
            submit(args.state, binding, archive)
        else:
            wait(args.state, binding, Path("notarization-log.json"))


if __name__ == "__main__":
    try:
        main()
    except (ValueError, KeyError, OSError, subprocess.SubprocessError) as error:
        message = f"Notarization check: {error}"
        print(message, file=sys.stderr)
        if os.environ.get("GITHUB_STEP_SUMMARY"):
            with open(os.environ["GITHUB_STEP_SUMMARY"], "a") as handle:
                handle.write(message + "\n")
        sys.exit(75 if isinstance(error, PendingError) else 1)
