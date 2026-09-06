# Release procedure

Publishing a GitHub release starts the workflow. The release page appears first. Archives and installers appear only after all required checks pass. The workflow keeps the release title, notes, and pre-release setting.

## Repository setup

Use `main` as the default branch. Enable secret scanning, push protection, and private vulnerability reporting. Keep immutable releases disabled: this process must add files after the release page is published. Protect `main` and the `release` environment. Restrict changes to version tags.

The Test workflow runs native checks on macOS, Linux, and Windows, on amd64 and arm64. Windows checks use PowerShell 5.1 and 7. Go race checks run where the Go toolchain supports them. Release-control tests and Gitleaks also run in CI. Actions use commit SHAs. GoReleaser and Gitleaks use exact versions.

Before the initial push, review the exact committed files. Scan the commit and a clean export of that commit with Gitleaks. Local environment files, signing keys, IDE files, and build output must stay outside the commit. An empty Git history does not need a new Git directory.

## Signing setup

Signing is required for a release. Set these values in the `release` environment before the first pre-release:

| Type | Name | Purpose |
|---|---|---|
| Variable | `CLI_OAUTH_CLIENT_ID` | Approved public OAuth client ID |
| Variable | `MACOS_SIGN_TEAM_ID` | Expected Apple team identifier |
| Variable | `WINDOWS_SIGN_THUMBPRINT` | Expected Windows publisher certificate thumbprint |
| Secret | `WINDOWS_SIGN_P12` | Base64 code-signing certificate and private key |
| Secret | `WINDOWS_SIGN_PASSWORD` | Password for that certificate |
| Secret | `MACOS_SIGN_P12` | Base64 Developer ID Application certificate and private key |
| Secret | `MACOS_SIGN_PASSWORD` | Password for that certificate |
| Secret | `MACOS_NOTARY_ISSUER_ID` | App Store Connect issuer ID |
| Secret | `MACOS_NOTARY_KEY_ID` | App Store Connect key ID |
| Secret | `MACOS_NOTARY_KEY` | Base64 App Store Connect private key |
| Secret | `HOMEBREW_TAP_TOKEN` | Token restricted to the Homebrew tap |

The signing steps receive the signing secrets. Other steps use only public publisher identifiers. Never include a client secret in the executable. The workflow stops if a signing setting is missing. The current Windows signing script uses a certificate with an accessible private key. If the selected provider requires a hardware device or remote service, change and test that signing step before a release.

The workflow copies `scripts/install.sh` into the release assets and sets its public Apple team ID from `MACOS_SIGN_TEAM_ID` before it calculates checksums. Keep the placeholder in the source file. The released file uses LF line endings, including when the build runs on Windows. Its checksum and build provenance are included with the release. The shell script checks the downloaded macOS executable's signature; the script itself has no Authenticode signature.

Create `lettermint/homebrew-tap` with `main` as its default branch. Give `HOMEBREW_TAP_TOKEN` access only to that repository, with Contents and Pull requests write permissions. Do not use a token with access to other private repositories. Require the tap's cask checks and manual review before merge.

## Publish a version

1. Merge the release changes to `main`. Update `CHANGELOG.md` and check the skill examples. Test login, logout, send, content export, forwarding, and replay with a test account.
2. Create a version tag at the intended commit on `main`. Use `v1.0.0` for a stable version or `v1.0.0-rc.1` for a pre-release. Version tags must not move. Build metadata and leading zeroes are not supported.
3. Create the GitHub release for that tag. Enter the title and notes. For a version with a suffix, select **Set as a pre-release**. Select **Publish release** to start the build. Publishing a draft also starts it.
4. Monitor the Release workflow. It checks the exact tag and runs CI. GoReleaser then builds, signs, notarizes, and packages without publishing. The workflow scans extracted package contents and checks the same packages on all six native platforms.
5. Check that all jobs pass. The workflow checks signatures, expected publishers, notarization, version output, installation, replacement, removal, Unicode paths, and both PowerShell versions. It then generates and verifies build provenance.
6. Download and verify the release files. Only six archives, `install.sh`, the signed `install.ps1` and `uninstall.ps1`, `checksums.txt`, and `provenance.jsonl` are attached. Checksums cover all archives and scripts. Provenance covers those files and the checksum file. Build directories and signing material are never release assets.
7. For the newest stable release, review the automatic cask PR in `lettermint/homebrew-tap`. The workflow checks cask style, online audit, installation, replacement, and removal before it opens the PR. Tap CI checks both Mac architectures and rejects an older version. Merge manually after the checks pass. Pre-releases do not update the tap.

Use a pre-release to test the complete signed process before the first stable release. Test a failed upload and retry. Confirm that the saved files are reused and that the release notes stay unchanged. Also check installation and upgrade from a prior signed version when one exists. First-release CI uses the new package to test replacement and downgrade protection; it cannot test compatibility with a prior signed release that does not yet exist.

The website download links under `https://lettermint.co/cli/` redirect to the latest stable release assets. See the [download paths](installation.md#website-download-links). Before the first stable release, use files from the selected pre-release page. After publication, check the website links and test installation through those links. Keep signed PowerShell files unchanged when serving them.

## Retry a failed release

Use **Re-run failed jobs** in the original workflow run. **Re-run all jobs** also uses the saved packages. The workflow stores the original packages and the verified packages with provenance as separate immutable Actions artifacts for 90 days. A retry does not sign them again.

Uploads add only missing files. Each existing asset must have exactly the same bytes as the saved file. A different file, an expired saved artifact, or missing saved packages when public assets already exist stops the workflow. Do not delete artifacts, move the tag, or replace assets to bypass this check. Use a new version if the original files cannot be recovered. A failure before any packages were saved can restart the build.

A failed cask PR step does not remove published CLI assets. Fix the tap token or tap check, then retry that job. A retry for an older release does not downgrade the stable cask.

Snapshots use no production signing keys. They cannot pass the production signature checks and cannot be uploaded by the Release workflow. The CLI has no self-update command. The package manager retains control of each installation.

See [GitHub release events](https://docs.github.com/en/actions/reference/workflows-and-actions/events-that-trigger-workflows#release), [GoReleaser signing](https://goreleaser.com/customization/sign/notarize/), and [Homebrew casks](https://goreleaser.com/customization/publish/homebrew_casks/) for the tools used in this process.
