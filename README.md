# Lettermint CLI

Send email, inspect messages, and test inbound and outbound webhooks with one Go executable. This repository contains the CLI and its agent skills. The Lettermint API enforces user, team, project, and content permissions on each call.

This is an unreleased v1 implementation. Hosted login needs the CLI API and an official public OAuth client in the target environment. A development build accepts that public ID through `auth login --client-id`. Never put a client secret in the CLI.

## Build and test

Use the Go version in `go.mod`.

```sh
go build -o lettermint ./cmd/lettermint
go test ./...
go test -race ./...
go vet ./...
```

## Start

```sh
lettermint version
lettermint auth login --name work
lettermint projects list --profile work --json
lettermint context set --profile work --project PROJECT_ID
lettermint messages send --profile work --project PROJECT_ID \
  --file message.json --idempotency-key order-1042-confirmation --json
```

Example `message.json` (replace the addresses with your verified sender and intended recipient):

```json
{
  "from": "Orders <orders@example.com>",
  "to": ["customer@example.net"],
  "subject": "Order 1042 confirmation",
  "text": "We received your order.",
  "metadata": {"order_id": "1042"}
}
```

An accepted response means that Lettermint accepted the message for processing. Check `messages events MESSAGE_ID` for delivery results. After an uncertain response, use the same input and idempotency key. Do not retry with a new key.

## Local webhooks

```sh
lettermint webhooks listen --project PROJECT_ID \
  --events message.inbound,message.delivered \
  --forward-to http://localhost:3000/webhooks/lettermint
```

The command writes the session ID to standard error. Use `listeners secret SESSION_ID` in another terminal to get its local signing secret. Keep the secret private. Both terminals must use the same profile. With no `--events`, the listener includes all message events and `suppression.added` and `suppression.removed`. Suppressions use the selected project and route. Team suppressions also require team suppression read access. Machine tracking events require `--include-machine-events`.

Local requests run in sequence. A failed attempt is recorded and acknowledged. Use `listeners replay SESSION_ID --sequence 1` to make another attempt while the original content remains available. The replay has the same delivery ID and new signature time. A lost acknowledgement can cause a duplicate. See [webhook details](skills/lettermint-cli/references/webhooks.md).

## Scripts and agents

Normal terminal commands show readable tables and status messages. Output sent to a pipe or file uses JSON automatically. Use `--plain` for readable logs without color, banners, or animation. Use `--color auto|always|never` to control color in human output. Automatic color respects `NO_COLOR` and `TERM=dumb`.

The welcome banner appears on `lettermint`, top-level help, and interactive login. Content exports and shell completion keep their original output format.

Use `--json --no-input` and an explicit `--profile` and `--project`. Use `--yes` only when a destructive operation is intended. Resource create and update commands accept `--file FILE`, or `--file -` for standard input. `messages send` accepts either a file or message flags. Use command help for the installed version.

```sh
lettermint skills list --json
lettermint skills export --output ./lettermint-skills
lettermint completion powershell
```

The executable contains the skill version that matches its commands. Skills also live in `skills/lettermint-cli/SKILL.md` for standard repository discovery. Installation does not change agent settings.

## Credentials and context

Profiles contain a grant for one user and one team. The OS credential store holds access and refresh tokens. The CLI fails if that store is unavailable. Profile metadata contains no tokens. A project change clears the saved route for the old project. Explicit flags override saved context. A running command keeps its original context.

Use `auth logout --profile NAME` to revoke a grant and its sessions. Permission errors do not cause an account switch or request for broader access.

If that login is no longer usable, run `auth logout --profile NAME --local`, confirm local removal, then log in again. Local removal does not revoke server access. Temporary server failures keep the saved login. See [login recovery](docs/usage.md#profiles-and-login-recovery).

## Installation and release

See [installation](docs/installation.md) and [release procedure](docs/releasing.md). The release process builds macOS, Linux, and Windows archives for amd64 and arm64. Native CI tests run on each platform. Windows arm64 does not support Go race instrumentation; its ordinary tests and vet still run.

See [command input and errors](docs/usage.md), [contribution guidance](CONTRIBUTING.md), and [security reporting](SECURITY.md).
