# Command input and recovery

Run `lettermint version` and the command's `--help` before use. Examples use a saved profile named `work`. Replace example IDs, domains, and addresses with your own values.

## Terminal output

Commands show tables and labeled fields in a terminal. Lists keep full IDs and use stacked records when the terminal is too narrow. Long descriptions can be shortened in tables; use the resource's `get` command for full values. Human timestamps use your local timezone and include its name.

Output sent to a pipe or file uses JSON automatically. Use `--json` explicitly in scripts and agents, including when a terminal is attached. Listener results use newline-delimited JSON. Use `--plain` to keep readable text in a file or pipe without color, banners, or animation. Do not combine `--json` and `--plain`.

`--color auto` is the default. It respects `NO_COLOR`, `TERM=dumb`, and terminal support. `--color always` enables color in human output, including when `NO_COLOR` is set. `--color never` disables color. JSON, plain mode, completion scripts, and content exports do not gain color.

The welcome banner appears on bare `lettermint`, top-level help, and interactive login. Routine commands show only their results. Progress, prompts, and errors use standard error. Progress is cleared before a prompt or result.

`webhooks listen` shows the selected context and local destination, then one log line per local attempt. Each line includes the local time, HTTP status, duration, event, sequence, attempt number, and delivery ID. Errors appear on the same line. The replay command appears once at startup; replace `SEQUENCE` with the line's `seq` value. Routine heartbeats and planned reconnects remain quiet.

## Profiles and login recovery

Each profile contains a login for one user and one team. Use `--profile` to select it. Use `--project` and `--route` to replace saved defaults for one command. Changing the project clears an old route default.

Normal logout revokes server access and removes the saved login:

```sh
lettermint auth logout --profile work
```

If the login was already revoked, expired, or lost from the OS credential store, remove the local profile before logging in again:

```sh
lettermint auth logout --profile work --local
lettermint auth login --name work
```

Local logout asks for confirmation. In an approved script, use `--local --yes --no-input`. It does not contact the server. Its result has `revoked=false` and `local_credentials_removed=true`. Server access can remain active; use Applications in the Lettermint dashboard if you also need to revoke it.

A server failure or rate limit does not remove saved credentials. Retry later. A permission error is not a reason to switch to another account or request broader access.

## JSON input

Resource create and update commands use `--file input.json`. Use `--file -` to read one JSON object from standard input. A single-message send accepts either a file or message flags. Do not mix the two input methods.

For example, `project.json` can contain:

```json
{"name":"Order notifications"}
```

```sh
lettermint projects create --profile work --file project.json --json
```

A new project has a transactional route and SMTP disabled by default. Set `initial_routes` and `smtp_enabled` in the file to select other supported options.

Single-message fields include `from`, `to`, `cc`, `bcc`, `reply_to`, `subject`, `html`, `text`, `headers`, `metadata`, `tags`, `settings`, and `attachments`. Metadata values are strings. A tag has `name` and `value`. An attachment has `filename` and base64 `content`, with optional `content_type` and `content_id`.

Use the same `--idempotency-key` and exact input after an uncertain send result. An accepted message is queued for processing; it is not proof of delivery. Scheduling and batch sends are outside v1.

## Lists and content

`--limit` selects 1 to 100 results. Use the returned `next_cursor` with `--cursor` to get the next page. Keep the same profile, project, route, and command. The cursor keeps its original page size.

```sh
lettermint messages list --profile work --project PROJECT_ID --limit 20 --json
lettermint messages content MESSAGE_ID --profile work --project PROJECT_ID --format text --output message.txt
```

Content export supports `raw`, `html`, and `text`. Raw export preserves the original JSON or MIME source. Content access is checked separately from message-list access.

File export keeps the requested destination absent until the download succeeds. It does not replace an existing file. The output directory must support hard links; otherwise the command returns an error without creating a partial output. Standard output is streamed and cannot be rolled back after a failure. On PowerShell, use `--output` for exact bytes instead of shell redirection.

## Errors

Human mode shows the error, field validation messages, and a relevant next step on standard error. JSON mode writes each error as one JSON object to standard error. API field errors are included under `error.details` when available. Standard output is reserved for command results. Login instructions and listener startup information also use standard error.

```json
{"error":{"code":"validation_failed","message":"validation_failed: The input is invalid.","details":{"from":["The sender address is invalid."]}}}
```

Exit codes are 1 for local failures, 2 for API validation errors, 3 for authentication, 4 for permission, 5 for a missing resource, 6 for conflict or expired state, 7 for rate limits, 8 for other API failures, and 130 for cancellation. Use the error code and details to decide the next action. Do not retry a send with a new idempotency key after an uncertain result.

See the [message workflows](../skills/lettermint-cli/references/messages.md) and [webhook workflows](../skills/lettermint-cli/references/webhooks.md) for more examples.
