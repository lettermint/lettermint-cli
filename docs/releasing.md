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
| Variable | `AZURE_CLIENT_ID` | Azure application client ID |
| Variable | `AZURE_TENANT_ID` | Azure tenant ID |
| Variable | `AZURE_SUBSCRIPTION_ID` | Azure subscription ID |
| Variable | `ARTIFACT_SIGNING_ENDPOINT` | Regional HTTPS signing endpoint |
| Variable | `ARTIFACT_SIGNING_ACCOUNT_NAME` | Artifact Signing account name |
| Variable | `ARTIFACT_SIGNING_CERTIFICATE_PROFILE_NAME` | Public Trust certificate profile name |
| Secret | `MACOS_SIGN_P12` | Base64 Developer ID Application certificate and private key |
| Secret | `MACOS_SIGN_PASSWORD` | Password for that certificate |
| Secret | `MACOS_NOTARY_ISSUER_ID` | App Store Connect issuer ID |
| Secret | `MACOS_NOTARY_KEY_ID` | App Store Connect key ID |
| Secret | `MACOS_NOTARY_KEY` | Base64 App Store Connect private key |
| Secret | `HOMEBREW_TAP_TOKEN` | Token restricted to the Homebrew tap |

The signing steps receive the signing secrets. Apple settings, submission, and wait steps receive the notarization credentials. Other steps use only public publisher identifiers. Never include a client secret in the executable. The workflow stops if a required setting is missing. Windows signing uses Azure Artifact Signing with OIDC. Configure the Azure application's federated credential with issuer `https://token.actions.githubusercontent.com`, subject `repo:lettermint/lettermint-cli:environment:release`, and audience `api://AzureADTokenExchange`. Assign the Artifact Signing Certificate Profile Signer role at the selected certificate profile. Its verified publisher must be `Lettermint B.V.`. GitHub does not store a Windows private key or Azure client secret.

The package job uses the pinned ArtifactSigning PowerShell module. It puts the release tag in the PowerShell installer, then signs the installers and both Windows executables before packaging. Signature checks require a trusted Lettermint publisher and a timestamp. The installer compares publisher subjects because Azure can use different short-lived certificates for each signature. A retry with saved packages skips Azure login and signing.

GoReleaser signs the macOS executables without submitting them to Apple. The workflow saves all package bytes and checks them on all six native platforms before the Apple wait. Linux and Windows checks include installation and removal. macOS checks verify the Apple Developer ID certificate, its team, and the executable. They also check that the verifiers reject a different team, modified bytes, and an ad hoc signature. The verifier reads the signed certificate because some signing tools leave the executable's `TeamIdentifier` field empty.

Two macOS jobs then submit the signed executables, one per architecture. Each job completes the upload and saves the Apple submission ID before it waits. The saved state binds the submission to the release commit, workflow run, architecture, publisher, and file hashes. The notarization script deletes its temporary key file when each Apple command ends. Keys never enter the saved artifacts.

Each Apple wait has a 60-minute limit. The job has a 75-minute limit to allow time for setup and upload. A timeout blocks publication, but Apple can continue processing the submission. Both submissions must reach `Accepted` before the final macOS installer checks run. Those jobs check notarization again on the original package bytes. Standalone executables cannot have a notarization ticket stapled to them, so this process does not change the executables or archives after submission.

The workflow copies `scripts/install.sh` into the release assets and sets its public Apple team ID from `MACOS_SIGN_TEAM_ID` before it calculates checksums. Keep the placeholder in the source file. The released file uses LF line endings, including when the build runs on Windows. Its checksum and build provenance are included with the release. The shell script checks the downloaded macOS executable's signature; the script itself has no Authenticode signature.

Create `lettermint/homebrew-tap` with `main` as its default branch. Give `HOMEBREW_TAP_TOKEN` access only to that repository, with Contents and Pull requests write permissions. Do not use a token with access to other private repositories. Require the tap's cask checks and manual review before merge.

## Publish a version

1. Merge the release changes to `main`. Update `CHANGELOG.md` and check the skill examples. Test login, logout, send, content export, forwarding, and replay with a test account.
2. Create a version tag at the intended commit on `main`. Use `v1.0.0` for a stable version or `v1.0.0-rc.1` for a pre-release. Version tags must not move. Build metadata and leading zeroes are not supported.
3. Create the GitHub release for that tag. Enter the title and notes. For a version with a suffix, select **Set as a pre-release**. Select **Publish release** to start the build. Publishing a draft also starts it.
4. Monitor the Release workflow. It checks the exact tag and runs CI. GoReleaser then builds, signs, and packages without publishing. The workflow scans, saves, and checks the packages on all six native platforms. It then submits the macOS executables to Apple and waits for acceptance before the final macOS installer checks.
5. Check that all jobs pass. The workflow checks signatures, expected publishers, notarization, version output, installation, replacement, removal, Unicode paths, and both PowerShell versions. It then generates and verifies build provenance.
6. Download and verify the release files. Only six archives, `install.sh`, the signed `install.ps1` and `uninstall.ps1`, `checksums.txt`, and `provenance.jsonl` are attached. Checksums cover all archives and scripts. Provenance covers those files and the checksum file. Build directories and signing material are never release assets.
7. For the newest stable release, review the automatic cask PR in `lettermint/homebrew-tap`. The workflow checks cask style, online audit, installation, replacement, and removal before it opens the PR. Tap CI checks both Mac architectures and rejects an older version. Merge manually after the checks pass. Pre-releases do not update the tap.

Use a pre-release to test the complete signed process before the first stable release. Test a failed upload and retry. Confirm that the saved files are reused and that the release notes stay unchanged. Also check installation and upgrade from a prior signed version when one exists. First-release CI uses the new package to test replacement and downgrade protection; it cannot test compatibility with a prior signed release that does not yet exist.

The website download links under `https://lettermint.co/cli/` redirect to the latest stable release assets. See the [download paths](installation.md#website-download-links). Before the first stable release, use files from the selected pre-release page. After publication, check the website links and test installation through those links. Keep signed PowerShell files unchanged when serving them.

## Retry a failed release

Use **Re-run failed jobs** in the original workflow run. **Re-run all jobs** also uses the saved packages. The workflow stores the original packages, each Apple submission record, and the verified packages with provenance as separate immutable Actions artifacts for 90 days. A retry does not sign the packages again.

If an Apple wait times out, the job reports the saved submission ID and stops. A retry restores that ID and checks it against the original package bytes before it resumes the wait. It does not upload the executable again. If Apple has already accepted the submission, the wait completes without another submission. An `Invalid` or `Rejected` result blocks publication. The job saves Apple's rejection log as a separate Actions artifact when the log is available.

If an upload starts but its submission record is lost, the retry stops. The workflow cannot prove whether Apple received that file. Recover the original submission record and matching packages, or use a new version. Do not delete submission records to force another upload. Correct missing or malformed Apple settings, then retry. The settings check runs before the upload step.

Uploads add only missing files. Each existing asset must have exactly the same bytes as the saved file. A different file, an expired saved artifact, or missing saved packages when public assets already exist stops the workflow. Do not delete artifacts, move the tag, or replace assets to bypass this check. Use a new version if the original files cannot be recovered. A failure before any packages were saved can restart the build.

A failed cask PR step does not remove published CLI assets. Fix the tap token or tap check, then retry that job. A retry for an older release does not downgrade the stable cask.

## Check saved release packages

Open the **Test** workflow and select **Run workflow**. Enter the original Release workflow run ID in `release_run_id`. The selected branch's verification scripts check the saved package bytes on all six platforms. The original run must have an unexpired package artifact and a release commit on `main`.

These checks require no signing secrets and have read-only GitHub permissions. They check real signatures and native execution. Linux and Windows checks also exercise the original installers. macOS checks exercise the current publisher verification functions against the saved binaries. They do not submit to Apple, create provenance, publish files, or update Homebrew. The Release workflow must still verify Apple acceptance and the macOS installer before publication.

To check a Windows installer fix, select `candidate_installers`. This mode first verifies the original executable and script signatures. It then tests the current unsigned installer with the saved signed executable. Only the candidate script's signature result is substituted; binary signature and checksum checks still run. Use this mode to test installer behavior before signing a new version. It does not prove that the candidate script is signed. The Release workflow always checks the actual signed installer without this substitution.

Snapshots use no production signing keys. They cannot pass the production signature checks and cannot be uploaded by the Release workflow. The CLI has no self-update command. The package manager retains control of each installation.

See [GitHub release events](https://docs.github.com/en/actions/reference/workflows-and-actions/events-that-trigger-workflows#release), [GoReleaser signing](https://goreleaser.com/customization/sign/notarize/), [Apple notarization](https://developer.apple.com/documentation/security/customizing-the-notarization-workflow), and [Homebrew casks](https://goreleaser.com/customization/publish/homebrew_casks/) for the tools used in this process.
