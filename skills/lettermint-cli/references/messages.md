# Send and inspect messages

Check `lettermint messages send --help`. Prepare a JSON file with the sender, recipients, subject, and HTML or text. Include attachments as base64 content if required. Scheduling and batch sends are not part of v1.

```sh
lettermint messages send --profile work --project PROJECT_ID --file message.json --json --no-input
lettermint messages get MESSAGE_ID --profile work --json --no-input
lettermint messages events MESSAGE_ID --profile work --json --no-input
lettermint messages content MESSAGE_ID --profile work --format raw --output message.eml --no-input
```

An idempotency key is optional. Without a key, each command sends a new message. For a send that must support retries, supply a key on the first attempt:

```sh
lettermint messages send --profile work --project PROJECT_ID --file message.json --idempotency-key change-1042 --json --no-input
```

After an uncertain result, repeat this command with the same profile, project, route, key, and exact input. Do not generate a new key for a retry. If the first attempt had no key, check the message list before sending again. Another send can create a duplicate, even if you add a key to the retry.

To send a message again, use `resend`. It creates a new message with its own ID and emails the recipients again, so use `--yes` only if the user approved the resend. Repeat `--recipient` to select some of the original recipients; otherwise every original recipient receives it. Reuse an idempotency key as for a send.

```sh
lettermint messages resend MESSAGE_ID --profile work --recipient reader@example.net --yes --json --no-input
```

A message can be resent while its content is retained, at most 5 times in 24 hours. Check delivery with the events of the new message ID.

Message lookup and resend ignore saved project and route defaults. Use `--project` only to restrict a lookup. The selected profile still controls the team and access permissions. An accepted response means the mail pipeline accepted the message. It does not mean the recipient received it. Use the delivery events to check delivery.

Message metadata and message content have separate permissions. If content access is denied, stop that operation. Do not use another command, profile, or webhook to obtain the same content. Source export preserves bytes. Keep exported files private.

For exact content on PowerShell, use the CLI `--output` option. Shell redirection in older PowerShell versions can change text encoding. Raw export returns the original stored JSON or MIME source. It does not construct a new MIME message.
