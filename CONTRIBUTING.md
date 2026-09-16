# Contributions

Open an issue to describe the problem and intended result. Keep changes focused. Add a test for changed behavior and run Go tests, race checks, and vet. Keep command examples and embedded skills consistent with the executable.

## Local development

Use the Go version in [go.mod](go.mod):

```sh
git clone https://github.com/lettermint/lettermint-cli.git
cd lettermint-cli
go build -o lettermint ./cmd/lettermint
./lettermint --help
```

Development builds need the approved public OAuth client ID for login:

```sh
./lettermint auth login --name work --client-id PUBLIC_CLIENT_ID
```

Replace `PUBLIC_CLIENT_ID` with the approved ID. A client secret is not used. Signed releases include the public client ID.

## Checks

Run the checks before you submit code changes:

```sh
go test ./...
go test -race ./...
go vet ./...
```

For terminal changes, build the CLI and run `python3 scripts/test-terminal.py ./lettermint` on macOS or Linux. Windows CI runs the PowerShell checks. Review changes to the saved output in `internal/presentation/testdata` before updating those files. Use `UPDATE_GOLDEN=1 go test ./internal/presentation` only when the new output is intended.

## Pull requests

Use original code and examples. Include the license and source of any reused work. Do not include credentials, customer email, webhook payloads, or private application source in this repository.

Keep documentation about CLI use and maintenance. Do not include private backend table designs, internal tickets, or service deployment procedures.

Use a Conventional Commit title, such as `fix: keep the profile context during refresh`. A pull request must describe the change, its tests, and any remaining limits.

## Releases

See the [release procedure](docs/releasing.md) for signing and publishing.

For release changes, run `python3 -m unittest discover -s scripts -p 'test_*.py' -v`, `goreleaser check`, and the secret checks in `.github/workflows/test.yml`. Release tests require Python 3.11 or later. Test signed changes with a pre-release before a stable release.
