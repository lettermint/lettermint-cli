# Release notes

## Unreleased

- Add a branded terminal welcome, readable tables, action results, and local webhook status lines.
- Select JSON automatically in pipes; add `--plain` and `--color` for human output.
- Keep field errors readable and escape terminal control characters in displayed data.

- Add OAuth profiles and project context.
- Add resource commands and single-message sends with idempotency.
- Add one listener for inbound, outbound, and suppression webhooks with local signatures and replay.
- Add exact content export, JSON output, shell completion, and embedded agent skills.
- Add native platform tests and signed release packaging.
- Add explicit local logout for unusable saved logins and preserve temporary OAuth errors.
- Include API field errors in JSON error output.
- Publish content files only after a complete download, without replacing existing files.

API-token login, fresh CI login, scheduling, batch sending, and historical production replay are outside v1.
