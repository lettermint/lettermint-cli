# Send and inspect messages

Check `lettermint messages send --help`. Prepare a JSON file with the sender, recipients, subject, and HTML or text. Include attachments as base64 content if required. Scheduling and batch sends are not part of v1.

```sh
lettermint messages send --profile work --project PROJECT_ID --file message.json --idempotency-key change-1042 --json --no-input
lettermint messages get MESSAGE_ID --profile work --project PROJECT_ID --json --no-input
lettermint messages events MESSAGE_ID --profile work --project PROJECT_ID --json --no-input
lettermint messages content MESSAGE_ID --profile work --project PROJECT_ID --format raw --output message.eml --no-input
```

Use the same idempotency key and exact input after an uncertain send result. Do not generate a new key for a retry. An accepted response means the mail pipeline accepted the message. It does not mean the recipient received it. Use the delivery events to check delivery.

Message metadata and message content have separate permissions. If content access is denied, stop that operation. Do not use another command, profile, or webhook to obtain the same content. Source export preserves bytes. Keep exported files private.

For exact content on PowerShell, use the CLI `--output` option. Shell redirection in older PowerShell versions can change text encoding. Raw export returns the original stored JSON or MIME source. It does not construct a new MIME message.
