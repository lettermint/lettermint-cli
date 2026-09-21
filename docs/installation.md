# Install and remove the CLI

## macOS

```sh
brew install lettermint/tap/lettermint
brew upgrade lettermint
brew uninstall lettermint
```

Homebrew owns this installation. Do not replace its executable with a script. For an exact older version, use the matching release archive in a separate directory. You can also review the matching cask change in the tap history. Check the archive checksum and signature before use.

## Windows

For the latest stable release, download `install.ps1` through the website redirect. PowerShell checks the saved script's signature before execution:

```powershell
iwr -UseBasicParsing https://lettermint.co/cli/install.ps1 -OutFile "$env:TEMP\lettermint.ps1"
powershell -NoProfile -ExecutionPolicy AllSigned -File "$env:TEMP\lettermint.ps1"
```

If prompted, confirm that the publisher is **Lettermint B.V.** Open a new terminal after installation. To inspect the signature before execution, run `Get-AuthenticodeSignature "$env:TEMP\lettermint.ps1"` after the download.

Save the file before execution. Do not pipe it to `Invoke-Expression`: the script must read its own file to check its signature.

Each signed installer contains its exact release version. The commands above install that version without a separate API lookup. For an older release or a pre-release, download `install.ps1` from that release and use its exact tag with `-Version`, such as `-Version v1.0.0-rc.1`.

The installer checks the archive checksum, binary version, and publisher signature before it replaces `lettermint.exe`. It installs under the current user's LocalAppData directory and adds its `bin` directory to the user PATH. Run the selected release's installer again to upgrade. Use `-AllowDowngrade` for an intentional downgrade.

To remove a PowerShell installation, download and check the signed removal script:

```powershell
$uninstaller = Join-Path $env:TEMP 'lettermint-uninstall.ps1'
Invoke-WebRequest -UseBasicParsing 'https://lettermint.co/cli/uninstall.ps1' -OutFile $uninstaller
Get-AuthenticodeSignature $uninstaller
& $uninstaller
```

The script asks for confirmation. It removes only files that it owns. It keeps saved profiles. Run `lettermint auth logout` before removal if you also want to revoke a grant.

PowerShell 5.1 and PowerShell 7 are supported. The installer rejects an executable owned by another package manager. It does not change agent configuration.

## Shell installer for Linux and macOS

Download and run the installer for the latest stable release:

```sh
curl -fsSL https://lettermint.co/cli/install.sh -o install.sh
sh install.sh
```

The script installs in `$HOME/.local/bin` without root access. It checks the archive checksum before extraction. On macOS, it also checks the Apple publisher, signature, and notarization before execution. It prints PATH instructions if needed and does not change your shell configuration.

Run the same commands to update a shell installation. To select a version, custom directory, or build provenance check:

```sh
sh install.sh --version v1.0.0
sh install.sh --version v1.0.0-rc.1
sh install.sh --bin-dir "$HOME/bin"
sh install.sh --verify-provenance
```

The provenance check requires the GitHub CLI (`gh`). Use `--allow-downgrade` for an intentional downgrade. For an older release or pre-release, download `install.sh` from that release to use its matching publisher settings.

To remove a shell installation:

```sh
sh install.sh --uninstall
```

If you used `--bin-dir`, pass the same directory for updates and removal. The script removes only an unchanged executable that it owns. It keeps saved profiles. Use Homebrew for an installation owned by Homebrew.

## Manual archives

Download the archive for your OS and architecture from the selected release. Windows ZIP archives contain `lettermint.exe`. macOS and Linux tar archives contain `lettermint`. Check the archive's SHA-256 value against `checksums.txt`. Verify build provenance with GitHub's attestation verification command before installation:

```sh
gh attestation verify lettermint_1.0.0_linux_amd64.tar.gz \
  --repo lettermint/lettermint-cli \
  --bundle provenance.jsonl \
  --signer-workflow lettermint/lettermint-cli/.github/workflows/release.yml
```

Download `provenance.jsonl` from the same release. A release page can appear before its assets while the release checks run. Do not use an incomplete release.

Extract the executable into a directory that you control. Use the same method to update it. Remove that executable to uninstall. An active OS credential store is required for login. A headless server without a credential store is outside the v1 target.

## Website download links

The website uses temporary redirects to the latest stable GitHub release assets:

| Website path | Release file |
| --- | --- |
| `/cli/install.sh` | `install.sh` |
| `/cli/install.ps1` | `install.ps1` |
| `/cli/uninstall.ps1` | `uninstall.ps1` |
| `/cli/checksums.txt` | `checksums.txt` |
| `/cli/provenance.jsonl` | `provenance.jsonl` |

Use `https://lettermint.co` before each path. For an exact version or pre-release, download all files from that release's page. Keep the archive, checksums, and provenance from the same release.
