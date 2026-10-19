# Remote hosts and Relay

Remote control connects a paired client to a Wuu host through a Relay. The host
runs the agent and accesses the workspace; the Relay routes the end-to-end encrypted
app-server connection. Pairing grants control of the host, not just a read-only view.

Phone pairing is a development preview: start the desktop with `npm --prefix desktop run dev:remote`
to show Settings → Phone access and pair the native iOS app without an account.
Release builds hide the phone access page until mobile release acceptance is complete. Build the app from [the native client project](../../../clients/native/README.md);
there is no public App Store distribution yet. Account login on the desktop remains
development-only. Build the CLI using the [development guide](../project/development.md).

## Connect an SSH workspace

The experimental headless host runs Wuu, its installed agent engines, files, Git,
and terminals on a remote macOS or Linux computer. Your local browser displays
the shared Wuu workbench through an OpenSSH tunnel. Install and authenticate
Codex, Claude Code, Pi, OpenCode, or other supported engines on the **remote**
computer; engine discovery and conversation state belong to that computer.

Build from the same Wuu checkout on the remote computer (Node 22+ and the Go
version in `go.mod` are required):

```bash
make setup
npm --prefix desktop run build:remote
npm --prefix desktop run remote:serve -- --workdir /srv/my-project --port 8787 --pair
```

Run the last command in tmux or a service manager so logout does not stop it.
It binds only to `127.0.0.1` and prints a single-use browser pairing link.
The build includes the matching Go core and web assets; `--core` and
`--web-root` can select explicit paths. Do not run the desktop's remote host
and this service against the same Wuu home simultaneously.

The SSH service uses local device pairing and refuses to start if its Wuu home
is signed in to a Wuu account. Existing account credentials are left unchanged.
Use a separate home, for example
`WUU_HOME="$HOME/.wuu-ssh" npm --prefix desktop run remote:serve -- --workdir /srv/my-project --pair`,
or intentionally sign out of that Wuu account first. A separate home has its own
Wuu configuration and conversation history; configure it before starting work.

On your local computer, build/install the CLI from this revision and connect:

```bash
wuu remote ssh --port 8787 user@server
```

Open the pairing link printed by the remote service. Subsequent visits to
`http://127.0.0.1:8787/` use the saved pairing. Keep the SSH command running;
Ctrl-C closes the tunnel. SSH config aliases, keys, host-key verification and
ProxyJump follow your normal OpenSSH configuration. Use `--ssh-port` or
`--ssh-config` for explicit overrides. Wuu keeps this tunnel in the foreground
and does not reuse or leave behind a control master for the destination
connection, even if your SSH config normally enables connection sharing or
backgrounding. Jump hosts still follow their own SSH configuration; existing or
explicitly backgrounded jump-host masters remain user-managed.

On Linux and macOS, running without a controlling terminal isolates SSH and its
foreground proxy children in one process group, so cancellation also cleans up a
jump host still connecting. In an interactive terminal Wuu preserves shell job
control and OpenSSH prompts, including when stdin is redirected. Use Ctrl-C to
signal that foreground job. Signalling only the Wuu PID in this interactive case does not
guarantee cleanup of a proxy that is still authenticating; service automation
should run without a controlling terminal or stop its whole process group.

The CLI's connection message is not an application health check. Wait for the
remote service to print `Wuu workspace ready` before opening the workbench; that
message confirms the listener is running and the host has registered with it.
Agent engines are discovered on the remote computer when the workspace opens.
`ExitOnForwardFailure` catches forwarding setup failures, not an unavailable
remote application. If SSH reports a connection failure, fix the reported cause
and rerun the same command. There is no automatic SSH retry or repeated password
prompt; the browser reconnects when its tunnel becomes available again.

Use a distinct local port for each host so browser identities and drafts stay
separate. If remote port 8787 maps to local port 8788, start the remote service
with `--port 8787 --browser-port 8788 --pair`, then connect with
`wuu remote ssh --port 8788 --remote-port 8787 user@server`. Pair again if the
advertised browser port changes.

Closing the browser or losing SSH leaves agent tasks running on the remote
service. Reconnect the tunnel to restore the conversation and buffered output.
If the startup workspace was removed or relocated, workspace discovery remains
available through the shared scratch workspace; select the desired project again.
Terminals also survive a resumed connection; after a fresh attachment the
workbench asks you to reopen them. Stopping or restarting the remote service
stops its tasks and terminals; saved conversation history remains on disk.
Restart without `--pair` to accept existing devices without opening enrollment.
A failed startup or SIGINT/SIGTERM/SIGHUP shuts down children created by that
service; it never reports readiness after cancellation.

### Run the host as a service

For a Linux machine with systemd, first pair interactively using the command
above, then stop that host before starting the service. Use an absolute Node path
from `command -v node` and your actual checkout, workspace and Wuu home paths:

```ini
# ~/.config/systemd/user/wuu-workspace.service
[Unit]
Description=Wuu SSH workspace

[Service]
Type=simple
WorkingDirectory=/srv/wuu
Environment=WUU_HOME=/home/YOUR_USER/.wuu-ssh
ExecStart=/usr/bin/node /srv/wuu/desktop/out-remote/host.mjs --workdir /srv/my-project --port 8787
Restart=on-failure
RestartSec=5
KillMode=control-group
TimeoutStopSec=15
UMask=0077

[Install]
WantedBy=default.target
```

```bash
systemctl --user daemon-reload
systemctl --user enable --now wuu-workspace
journalctl --user -u wuu-workspace -f
# Intentionally stop the service and its tasks:
systemctl --user stop wuu-workspace
```

A user service may stop at logout unless your administrator enables user
lingering (`loginctl enable-linger YOUR_USER`). Follow the machine's policy;
Wuu does not install or enable services itself. On macOS, use tmux or a launchd
service with the same direct Node command. Service restart restores saved
history, not a process checkpoint: running turns and terminals are stopped.
Do not put `--pair` in an automatically restarting service; open enrollment only
when you intend to pair a new browser, and keep pairing links out of shared logs.

This lifecycle follows the preinstalled-server and loopback SSH forwarding model
used by [code-server](https://coder.com/docs/code-server/guide#port-forwarding-via-ssh).
OpenSSH's [forwarding](https://man.openbsd.org/ssh_config#ExitOnForwardFailure)
and [connection sharing](https://man.openbsd.org/ssh_config#ControlMaster)
remain transport concerns; Wuu's existing session continuity handles reconnects.

This source-build preview uses the browser workbench. Native desktop windows,
embedded browser/computer-use tools and automatic remote installation are not
provided by the headless host. Engine login requiring a native browser must be
completed on the remote computer before connecting. The local machine's SSH
credentials and model configuration are not uploaded.

## Connect the iOS app

1. Keep Wuu open on your computer. In Settings → Phone access, allow access and
   show a pairing code.
2. Put the phone on the same Wi-Fi, or configure a reachable Tailscale address or
   relay as described below. In the app, choose Connect computer and scan the
   pairing code, or paste its link and choose Connect.
3. Choose a conversation, or start a new one and select a working folder. You can
   also enter an existing absolute folder path on the computer. Messages and task
   controls use the encrypted connection to that computer.

Open **… → Conversation settings** to choose an engine before sending the first
message. The list comes from the connected computer's enabled, available engines.
Existing conversations display their bound engine; start a new conversation to use
another one. The same page offers models, reasoning and permission modes exposed
by the selected engine. Changes require an online computer and an idle task.
iOS remembers saved model, reasoning and permission choices for new conversations
using the same computer and engine, including after relaunch; account connections
keep separate preferences. This includes Unrestricted access after confirmation.
Existing conversations retain their own settings. Removed models fall back to the
computer's defaults without discarding a permission choice that the engine still
supports. Engine installation, sign-in and credentials remain on the computer.

The app saves the pinned computer identity and phone key in the iOS keychain.
It restores the last computer, folder and conversation after relaunch. Returning
from the background creates a fresh encrypted connection; sent messages are never
automatically replayed. The computer must be online to create, send or stop work.
Account-based history sync and notifications require an account connection.

While a reply is running, follow-ups wait in a compact queue above the input.
Tap a preview to read it in full, remove it, or steer the current reply. Pending
steering can be moved back to the queue. Once the computer starts processing a
message, it leaves the queue. Scroll up to load older messages automatically;
tap an operation summary to see its details. When the final answer arrives, preceding
process text and tools fold into a summary. Images read or inspected by tools stay
inside the operation details and load when opened; explicitly presented images
remain visible in the conversation. Published image previews and originals load
through the encrypted connection when needed; opening an image checks its stored
content digest.

The native iOS and Android apps render Mermaid code blocks offline, including
while a reply is arriving. Incomplete syntax keeps the last valid diagram until
more text arrives; invalid final syntax shows the source. Use View source or Copy
code to inspect it, and scroll inside wide or tall diagrams. Diagram colors and
text size follow the phone's appearance settings.

Long-press your message bubble or attachments to copy, edit, select text, or
share. Replies show copy and fork buttons after their turn finishes. Forking lets
you choose the current folder or a new Git worktree based on the current commit;
a new worktree requires a Git repository. The new conversation initially loads
recent history, with older messages available by scrolling up.
Editing an earlier user message keeps its attachments and replaces subsequent
conversation when you send it again. Cancel leaves history intact. Editing requires
an online computer, an idle writable conversation, and no pending messages.

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
