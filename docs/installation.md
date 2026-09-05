# Install and remove the CLI

These commands apply after the first signed release is published. No release is published from a local build.

## macOS

```sh
brew install --cask lettermint/tap/lettermint
brew upgrade --cask lettermint
brew uninstall --cask lettermint
```

Homebrew owns this installation. Do not replace its executable with a script. For an exact older version, use the matching release archive in a separate directory. You can also review the matching cask change in the tap history. Check the archive checksum and signature before use.

## Windows

Download `install.ps1` from the selected GitHub release. Check its Authenticode signature before you run it. It must show a valid Lettermint publisher certificate.

```powershell
Get-AuthenticodeSignature .\install.ps1
.\install.ps1 -Version v1.0.0
```

The installer requires an exact version. It checks the archive checksum, binary version, and publisher signature before it replaces the executable. It installs under the current user's LocalAppData directory and adds its `bin` directory to the user PATH. Open a new terminal after installation. Run the selected release's installer again to upgrade. Use `-AllowDowngrade` for an intentional downgrade.

To remove a PowerShell installation, download the signed `uninstall.ps1` from the release and run it. The script asks for confirmation. It removes only files that it owns. It keeps saved profiles. Run `lettermint auth logout` before removal if you also want to revoke a grant.

PowerShell 5.1 and PowerShell 7 are supported. The installer rejects an executable owned by another package manager. It does not change agent configuration.

## Linux and manual archives

Download the archive for your OS and architecture from the selected release. Check its SHA-256 value against `checksums.txt`. Verify build provenance with GitHub's attestation verification command before installation:

```sh
gh attestation verify lettermint_1.0.0_linux_amd64.tar.gz \
  --repo lettermint/lettermint-cli \
  --bundle provenance.jsonl \
  --signer-workflow lettermint/lettermint-cli/.github/workflows/release.yml
```

Download `provenance.jsonl` from the same release. A release page can appear before its assets while the release checks run. Do not use an incomplete release.

Extract the executable into a directory that you control. Use the same method to update it. Remove that executable to uninstall. An active OS credential store is required for login. A headless server without a credential store is outside the v1 target.
