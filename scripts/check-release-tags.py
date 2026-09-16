#!/usr/bin/env python3
"""Check real GoReleaser packages with stable and RC tags on the same commit."""

import json
import os
from pathlib import Path
import platform
import subprocess
import tempfile

import release


SOURCE = Path(__file__).resolve().parents[1]


def command(root, env, *args):
    result = subprocess.run(args, cwd=root, env=env, text=True,
                            stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=360)
    if result.returncode:
        raise RuntimeError(f"{' '.join(args)} failed:\n{result.stdout}")
    return result.stdout.strip()


def check():
    # Read the production configuration. Only disable signing in this isolated fixture.
    config = (SOURCE / ".goreleaser.yaml").read_text()
    hook = '{{ if .IsSnapshot }}go version{{ else }}pwsh -NoProfile -File scripts/sign-windows.ps1 -Path "{{ .Path }}"{{ end }}'
    if config.count(hook) != 1:
        raise ValueError("Update the fixture for the current Windows signing hook.")
    config = config.replace(hook, "go version")
    workflow = (SOURCE / ".github/workflows/release.yml").read_text()
    build_step = workflow.split("      - name: Build, sign, and package without publishing\n", 1)[1].split("      - name:", 1)[0]
    if "          GORELEASER_CURRENT_TAG: ${{ github.event.release.tag_name }}\n" not in build_step:
        raise ValueError("The release build must select the event's exact tag.")

    env = {key: value for key, value in os.environ.items()
           if not key.startswith(("GITHUB_", "GIT_", "GORELEASER_", "MACOS_SIGN_", "ARTIFACT_SIGNING_", "AZURE_"))
           and key not in ("GH_TOKEN", "GH_ENTERPRISE_TOKEN")}
    env["CLI_OAUTH_CLIENT_ID"] = "fixture"
    with tempfile.TemporaryDirectory(prefix="lettermint-release-tags-") as temporary:
        root = Path(temporary)
        (root / ".goreleaser.yaml").write_text(config)
        (root / ".gitignore").write_text("dist/\nsmoke/\n")
        (root / "go.mod").write_text("module example.invalid/release-fixture\n\ngo 1.25\n")
        main = root / "cmd/lettermint/main.go"
        main.parent.mkdir(parents=True)
        main.write_text('package main\nimport ("encoding/json"; "os")\n'
                        'var version, clientID string\n'
                        'func main() { json.NewEncoder(os.Stdout).Encode(map[string]string{"version": version}) }\n')
        for name in ("LICENSE", "README.md", "CHANGELOG.md", "skills/lettermint-cli/SKILL.md"):
            path = root / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text("Release fixture\n")
        command(root, env, "git", "init", "--initial-branch=main")
        command(root, env, "git", "config", "--local", "user.name", "Release fixture")
        command(root, env, "git", "config", "--local", "user.email", "fixture@example.invalid")
        command(root, env, "git", "config", "--local", "commit.gpgsign", "false")
        command(root, env, "git", "config", "--local", "tag.gpgsign", "false")
        command(root, env, "git", "remote", "add", "origin", "https://github.com/lettermint/lettermint-cli.git")
        command(root, env, "git", "add", ".")
        command(root, env, "git", "commit", "-m", "Release fixture")
        commit = command(root, env, "git", "rev-parse", "HEAD")
        tags = ("v1.0.0", "v1.0.0-rc.5")
        for tag in tags:
            command(root, env, "git", "tag", tag)

        system = {"Darwin": "darwin", "Linux": "linux", "Windows": "windows"}[platform.system()]
        arch = {"x86_64": "amd64", "AMD64": "amd64", "arm64": "arm64", "aarch64": "arm64"}[platform.machine()]
        for tag in tags:
            command(root, {**env, "GORELEASER_CURRENT_TAG": tag}, "goreleaser", "release",
                    "--clean", "--skip=publish,notarize", "--timeout=5m")
            dist = root / "dist"
            release.verify_build_metadata(dist, {"tag": tag, "commit": commit})
            expected = {release.archive_name(tag, *target) for target in release.PLATFORMS}
            actual = {path.name for path in dist.iterdir() if path.suffix in (".gz", ".zip")}
            if actual != expected:
                raise ValueError(f"Wrong archives for {tag}: {sorted(actual)}")
            casks = list(dist.glob("**/lettermint.rb"))
            if len(casks) != 1:
                raise ValueError("Expected one generated cask.")
            cask = casks[0].read_text()
            if f'version "{tag[1:]}"' not in cask:
                raise ValueError(f"Wrong cask version for {tag}:\n{cask}")
            resolved_cask = cask.replace("#{version}", tag[1:])
            for mac_arch in ("amd64", "arm64"):
                url = f"https://github.com/lettermint/lettermint-cli/releases/download/{tag}/{release.archive_name(tag, 'darwin', mac_arch)}"
                if url not in resolved_cask:
                    raise ValueError(f"Cask does not contain {url}:\n{cask}")
            target = root / "smoke" / tag
            release.unpack(dist / release.archive_name(tag, system, arch), target)
            binary = target / ("lettermint.exe" if system == "windows" else "lettermint")
            result = json.loads(command(root, env, str(binary)))
            if result != {"version": tag[1:]}:
                raise ValueError(f"Wrong binary version for {tag}: {result}")
            print(f"{tag}: metadata, six archives, native version, and cask passed", flush=True)


if __name__ == "__main__":
    check()
