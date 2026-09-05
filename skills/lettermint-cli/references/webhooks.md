# Local webhooks

Start the local HTTP application before you start the listener.

```sh
lettermint webhooks listen --profile work --project PROJECT_ID --events message.inbound,message.delivered --forward-to http://localhost:3000/webhooks/lettermint --json --no-input
```

Inbound, outbound, and suppression events use this one command. Without `--events`, the listener receives all message events and `suppression.added` and `suppression.removed` for the selected project. Machine tracking events require `--include-machine-events`. A route filter applies to both message directions.

To select only suppression changes, use `--events suppression.added,suppression.removed`. Project suppressions apply to the project's outbound routes. Team suppressions apply to all outbound routes in the team and require `team_suppressions:read`. Their payload keeps `context.scope=team` and does not claim a source project. A listener filtered to an inbound route does not receive suppression events. Global and subscription-group suppressions do not produce these public events.

Message subscriptions require message read and content permissions. Suppression subscriptions require `suppressions:read`. Both require webhook management and access to the selected project. A permission error does not authorize another profile or broader access. Suppression payloads remain available for 15 minutes after capture, including after the suppression is removed. Replay does not extend this period.

The CLI forwards exact payload bytes. It signs each local request with the session secret. Obtain the secret with `lettermint listeners secret SESSION_ID --profile work --json --no-input`. Keep it in the local application's secret store. Do not print it in a shared log. Validate `X-Lettermint-Signature` with the Lettermint webhook verifier and its timestamp check.

The CLI forwards sequentially to loopback addresses. It does not follow redirects. It records failed HTTP attempts and continues. A lost acknowledgement can cause a duplicate. Use the stable delivery ID to make the handler idempotent.

```sh
lettermint listeners list --profile work --json --no-input
lettermint listeners replay SESSION_ID --sequence 12 --profile work --json --no-input
lettermint listeners stop SESSION_ID --profile work --yes --json --no-input
```

Replay creates a new ordered attempt with the original bytes and a new signature timestamp. Replay does not extend source retention. Stop on an expired payload or a permission error. Local attempts do not change permanent webhook configuration or inbound processing state.
