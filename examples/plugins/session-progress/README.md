# Owned session progress

An independent runtime example using only `@wuu/plugin-sdk`. Two consumers share
one observation contract: a bot replaces its status card with `progress.bot`,
and a fleet board replaces its activity row with `progress.fleet`.

The runtime requires the negotiated `session.progress.v1` host feature. It
creates plugin-owned sessions, submits named requests, and opts into live
progress with `host.session.inspect({session_id, request_id, include_progress:
true})`. Ordinary inspect calls still omit the projection.

## Contract boundaries

- Progress belongs to an exact owner/session/request/turn. Another plugin,
  user-owned session, queued request or mixed-input turn cannot borrow it.
- Each read is a **replace-all bounded snapshot**, not a delta to append.
  It contains at most 32 display items and 32 KiB of JSON; assistant text is
  limited to 4096 UTF-8 bytes. Respect `truncated` and unavailable reasons.
- Reasoning, prompts, tool arguments/results, attachments and provider-private
  state are not included. Observation grants no steering or cancellation rights.
- This is not durable event replay, a consumed-message receipt or a reliable
  notification transport. A remote bridge needs separate reconnect/replay and
  delivery semantics. This example does not send to a real external bot.
- The consumer chooses a bounded refresh cadence. Stop refreshing when the
  request ends or the view is dismissed; do not infer failure from quiet output.

Build and install the directory normally after resolving its public SDK dev
dependency. The acceptance runner below reuses local tooling offline and packs
the SDK, so it does not need a package registry or model credentials.

## Reproducible acceptance

```sh
go build -o .tmp/bin/wuu ./cmd/wuu
node packages/plugin-sdk/test/session-progress-package.mjs
```

Optional environment variables: `WUU_SESSION_PROGRESS_BINARY`,
`WUU_SESSION_PROGRESS_OUTPUT`, and `WUU_SESSION_PROGRESS_SDK_TARBALL`.

The runner compiles the example and its acceptance-only engine/observer outside the checkout
against a packed public SDK, installs external runtimes normally, and drives the
real app-server. The deterministic engine uses explicit release/cancel gates, not model inference.
The observer reports submission through the public execution service; the runner
polls for that fact before cancelling the observation. Host callbacks may overlap,
so this marker does not prove host waiter registration or cleanup. The host
contract tests use an internal registration barrier for that stronger check. Neither fixture is
installed by this example's manifest. It also verifies same-session request
separation and package disable/re-enable with fresh generation identity.

Evidence is written to `.tmp/e2e/session-progress/run-*/report.json` and
`transcript.jsonl`, including binary/SDK digests and preterminal bot/fleet
snapshots. The transcript contains synthetic sentinel strings used to prove
that private fields are excluded; it contains no real prompts or credentials.
This verifies runtime/public-package behavior, not rendered GUI layout or model
quality. Mixed-input steering attribution also needs the host's focused runtime
contract tests; an external fixture engine does not implement the default loop's
steer-consumption algorithm.
