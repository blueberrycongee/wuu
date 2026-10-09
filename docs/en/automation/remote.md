# Remote hosts and Relay

Remote control connects a paired client to a Wuu host through a Relay. The host
runs the agent and accesses the workspace; the Relay routes the end-to-end encrypted
app-server connection. Pairing grants control of the host, not just a read-only view.

The desktop Settings → Phone access page can pair the native iOS app without an
account. Build the app from [the native client project](../../../clients/native/README.md);
there is no public App Store distribution yet. Account login on the desktop remains
development-only. Build the CLI using the [development guide](../project/development.md).

## Connect the iOS app

1. Keep Wuu open on your computer. In Settings → Phone access, allow access and
   show a pairing code.
2. Put the phone on the same Wi-Fi, or configure a reachable Tailscale address or
   relay as described below. In the app, choose Connect computer and scan the
   pairing code, or paste its link and choose Connect.
3. Choose a conversation, or start a new one and select a working folder. You can
   also enter an existing absolute folder path on the computer. Messages and task
   controls use the encrypted connection to that computer.

The app saves the pinned computer identity and phone key in the iOS keychain.
It restores the last computer, folder and conversation after relaunch. Returning
from the background creates a fresh encrypted connection; sent messages are never
automatically replayed. The computer must be online to create, send or stop work.
Account-based history sync and notifications require an account connection.

While a reply is running, follow-ups wait in a compact queue above the input.
Tap a preview to read it in full, remove it, or steer the current reply. Pending
steering can be moved back to the queue. Once the computer starts processing a
message, it leaves the queue. Scroll up to load older messages automatically;
tap an operation summary to see its details.

For Tailscale or a reverse proxy, configure the desktop process with
`WUU_WEB_URL` set to the reachable HTTP(S) origin and `WUU_WEB_LISTEN` set to the
local address to listen on (for example, a Tailscale address and port). For a
separate relay, also set `WUU_WEB_RELAY_URL` to its `wss://…/v1/connect` endpoint.
The pairing code carries that endpoint. Both devices must be able to reach it;
installing Tailscale alone does not change an already saved relay address.
Generate a new code and pair again after changing the advertised endpoint.

Removing a computer on the phone forgets its local identity. To revoke that
phone's access, remove the device in the computer's Phone access settings.

## Prepare a Relay

The host and client need a reachable WebSocket endpoint ending in `/v1/connect`.
For local development on one machine:

```bash
wuu relay --addr 127.0.0.1:8787
```

Keep this process running and use `ws://127.0.0.1:8787/v1/connect`. A different
device cannot reach your computer through its own loopback address. For network
use, deploy a Relay reachable by both sides and use `wss://` with TLS. The server
supports TLS certificate/key flags and deployment behind a reverse proxy; encrypted
message content does not remove the need to protect routing metadata, service
availability, or operator access.

Account service and browser hosting are optional server features with separate
configuration. The basic paired CLI flow below does not require account registration.
Server options are defined in
[`internal/remote/server/server.go`](../../../internal/remote/server/server.go).

## Start and pair a host

On the workspace computer:

```bash
wuu remote init --relay ws://127.0.0.1:8787/v1/connect --name my-mac
wuu remote host --workdir /path/to/project --pair
```

Initialization creates or updates the host identity and prints its fingerprint.
The host connects outward to the Relay and prints a pairing URI after registration.
The default pairing window is 10 minutes and closes after the first successful
pairing. Use `--pair-timeout 30m` or `--pair-once=false` only when needed.

`remote host` accepts `--provider`, `--model`, `--workspace-id`, and `--relay`
overrides. Keep the host running. A Wuu home can have only one remote-host owner;
close the other host before starting another against the same home.

On the development client, import the URI privately:

```bash
wuu remote phone pair --uri 'wuu://pair?...' --name dev-client
wuu remote phone status
wuu remote phone send "summarize the current workspace"
wuu remote phone send --thread THREAD_ID "continue the investigation"
wuu remote phone watch
```

`phone` is a CLI development client and can run on another computer. `send` starts
a new session unless `--thread` is supplied, then streams the turn's output.
`watch` displays connection state and abbreviated notifications. These are not the
[`wuu exec` JSONL](jsonl-events.md) output contract.

## State and device removal

Host identity and paired devices live in `WUU_HOME/remote.json`; client identity
lives in `WUU_HOME/phone.json`, with `~/.wuu` as the default home. A phone subcommand's
`--store FILE` selects a separate client identity. These files and pairing URIs
contain sensitive access material; do not attach them to issue reports.

```bash
wuu remote status --json
wuu remote devices --json
wuu remote devices remove DEVICE_FINGERPRINT
```

Removal updates the saved device list, not the running host's in-memory store.
For reliable revocation, stop the host, remove the device, then restart the host
without reopening pairing. Do not assume removal alone disconnects an active client.

## Diagnose a connection

If pairing fails, check that both sides use the same reachable Relay and that the
pairing window is still open. A saved identity in `remote status` does not prove
the host is online. Check the running host's logs and the client's connection status.

A dropped client connection does not by itself stop the host's agent. Reconnect
and inspect the existing session before resending a task. Event replay is bounded;
do not treat it as unlimited durable log storage.

Paired clients use the host's app-server control surface. Trust them as controllers,
including their ability to request configuration changes. Agent operations remain
subject to the effective [permission mode](../reference/permissions.md); pairing
is not a separate per-device read-only policy. For a custom client, see
[app-server integration](app-server.md).
