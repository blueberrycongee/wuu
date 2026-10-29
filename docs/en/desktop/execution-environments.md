# Execution environments

Choose **Settings → Built-in agent → Execution environments** to run workspace tools in a container or on a remote machine. Local execution remains the default. Install the same Wuu revision in the environment; a protocol mismatch stops execution.

Create a profile under **Settings → Built-in agent → Execution environments**, then select it as the default for new conversations in the same section. The choice and profile are saved with each conversation. Editing or deleting the profile does not move an existing conversation. Older conversations retain local execution. Forks inherit the profile and receive a separate environment unless sharing is enabled. Inplace subagents and read-only side conversations use their parent’s environment.

File tools, search, shell commands, Git, programmatic tool calling and background processes all use that environment. Process controls and completion events return to the desktop. Published artifacts are copied back as immutable snapshots, with a 256 MiB limit per artifact. Model connections, plugins, browser integration, notes and history remain on the host. This setting does not sandbox extensions.

## Start with Docker

Build the supplied image from a Wuu checkout:

```sh
docker build -f containers/execution/Dockerfile -t wuu-execution:local .
```

Create a Docker profile with image `wuu-execution:local` and workspace `/workspace`. The image includes Wuu, Git, ripgrep, Python and Node.js. Dependencies needed by your project can be added in a derived image. The Docker service must already be running.

The workspace starts empty. Clone a repository inside it or explicitly mount a host workspace directory. A mounted directory is shared between every conversation that uses that directory, even when their containers are separate. Use the read-only mount option to prevent container writes to it. On Linux, mounted workspaces default to the host user and group to preserve file ownership; the optional container user overrides this choice. Nothing is copied automatically between the local project and the environment. Desktop editor, terminal and Git panels continue to refer to the local project; use the agent’s file, Git and process tools to inspect the environment, and publish artifacts to preview results.

## Backends

| Backend | Required setup | Resources and network |
| --- | --- | --- |
| Docker | Running Docker service and worker image | CPU and memory limits; optional network isolation |
| SSH | Reachable Unix host, Wuu executable, authenticated SSH access and a verified host key | Limits and network policy belong to the server |
| Singularity / Apptainer | Installed runtime and worker image | Runtime CPU/memory and network namespace options; host configuration must permit them |
| Modal | Python 3.10+, `modal` 1.5+ and configured SDK credentials; accessible worker image | CPU/memory limits and network isolation |
| Daytona | Python 3.10+, `daytona` SDK and configured credentials; accessible worker image | Whole CPU cores, memory rounded up to GiB and network isolation |
| Vercel Sandbox | Python 3.10+, authenticated `sandbox` CLI 3+ and worker image in the provider’s image registry | Whole CPU cores; memory follows CPU allocation; network isolation |
| Command | Installed trusted adapter implementing the versioned execution backend protocol | The adapter owns provisioning and enforcement |

Cloud dependencies are optional; they are not installed automatically. The Python executable field can point to a virtual environment containing the SDK. Configure cloud credentials using the provider’s normal SDK or CLI login flow. The host transport uses them to provision the environment; they are not implicitly forwarded to workspace commands. The environment’s own base image or server may already contain credentials.

For Singularity/Apptainer, set `WUU_EXECUTION_E2E_SIF` to a current worker SIF image and run `go test ./internal/executionworker -run TestSingularity -count=1 -v`.

For SSH, set the host or SSH alias, optional port, identity file and known-hosts file. Strict host-key verification is required; connect and verify the server through your normal SSH workflow first. Wuu creates a conversation-specific subdirectory beneath the configured remote workspace. The remote account must be able to create it. Node.js 22.19+ is required for programmatic tool calling. Git and ripgrep are required for their respective tools.

Linux command write restrictions require Landlock ABI 3 or later. If unavailable, standard/read-only commands fail visibly rather than running without protection. See [permissions](../reference/permissions.md). Container and VM isolation are separate from the conversation’s permission mode.

## Sharing, retention and recovery

By default, conversations have separate environment identities. **Share between conversations** opts into one filesystem; tool state and process ownership remain separate. Host mounts deliberately share their source directory regardless of this switch.

**Keep environment files** retains a Docker container, Singularity workspace, SSH workspace or cloud filesystem across clean disconnects. Modal takes a filesystem snapshot, Daytona stops the environment, and Vercel uses persistent sandboxes. Shared environments are retained for other conversations. Retention does not guarantee that running processes survive a cloud stop, provider timeout or machine restart. Missing retained state produces an error instead of silently replacing a saved filesystem.

Without retention, Docker containers are removed on runtime shutdown; Singularity state is removed after its connection closes; SSH workspaces are removed when the detached worker exits after its idle timeout. Cloud environments are stopped/deleted on clean disconnect. Abrupt host termination can leave resources behind. Retained/shared cloud resources and snapshots may continue to incur charges; remove them using the provider’s dashboard or CLI when no longer needed. Removing a profile only removes configuration.

The lifetime field controls the cloud environment’s lifetime or auto-stop interval and the disconnected worker’s idle timeout. Zero uses 600 seconds. Docker container retention is independent of worker idle timeout. A transport disconnect never replays the interrupted tool: its outcome may be unknown. A subsequent call can reconnect to the saved worker. Background process controls address the environment’s registry, never a host PID.

Only named environment variables are forwarded. A missing variable rejects startup. Forwarded values travel through the authenticated worker connection. Do not forward secrets that workspace commands should not access.

The built-in engine owns this execution route. External engines reject a selected remote environment. Subagents inherit the environment using inplace isolation; automatic host Git-worktree isolation is rejected. Create Git worktrees explicitly inside the environment when needed.

## User configuration

Profiles are user-owned. Project configuration cannot install or override execution adapters.

```json
{
  "execution_environments": {
    "default": "isolated",
    "profiles": {
      "isolated": {
        "backend": "docker",
        "image": "wuu-execution:local",
        "workspace": "/workspace",
        "network": "none",
        "cpus": 2,
        "memory_mb": 2048,
        "persistent": true
      }
    }
  }
}
```

Optional fields include `shared`, `user`, `host_workspace`, `mount_read_only`, `forward_env`, `worker`, `lifetime_seconds`, `shutdown_timeout_ms`, `python`, and SSH `host`, `port`, `identity_file`, `known_hosts_file`. A command adapter uses an executable-and-arguments array in `command`. Adapters are trusted local programs; selecting one grants it the host permissions of Wuu.

`shutdown_timeout_ms` sets the host's total EOF-to-exit budget for draining
requests and cleaning up provider resources. Positive values must be integers
from 1 to 2,147,483,647 milliseconds; omitted or zero uses the backend default:
7 seconds for native workers, 90 seconds for cloud backends, and 60 seconds for
command adapters. The command default allows the SDK's 35-second cancellation
drain and 10-second delegated cleanup plus transport overhead; successful cleanup
returns immediately rather than waiting out the budget. For delegated or nested
adapters, configure an outer budget
longer than the entire inner request drain and cleanup chain. If the budget
expires, Wuu terminates the transport and reports failure; remote resource state
may remain unknown.

## Implement a backend with the public SDK

`runExecutionBackendV4` and the `ExecutionBackendV4` types are exported by
`@wuu/plugin-sdk`. They serve the existing worker protocol, not a second host
service or the plugin `runJSONLRuntime` protocol. A backend can implement file
operations and process ownership without importing Wuu internals. Select its
installed executable with a user-owned profile, for example
`{ "backend": "command", "command": ["node", "/absolute/path/backend.mjs"], "workspace": "/workspace" }`.
The [independent backend fixture](../../../scripts/external-composition-acceptance/external-worker.ts)
is a complete Node-only example with file read/write/list, process output,
reconciliation records and cleanup. It deliberately rejects confined shell
execution; it is a trusted test implementation, not a production sandbox.

The adapter owns framing, authentication-token binding, request cancellation,
and callback routing. The provider supplies `initialize`, a typed `methods` map,
and `close`:

- **Handshake:** newline-delimited JSON requests contain `{ id, method, token, data }`.
  Replies contain `{ id, data }` or `{ id, error }`, where `error` is a string.
  Frames are at most 16 MiB including the newline. First initialization pins the
  token and immutable `{ version: 4, root, session, identity?, environment?,
  require_existing_workspace?, remove_workspace_on_exit? }` binding. No operation
  runs before successful initialization; reinitialization cannot change the
  binding. A cancelled/failed provider initialization requires a new connection.
  The token protects a private connection, not unauthenticated remote transport;
  use authenticated SSH or an equivalently secured transport between machines.
- **Files and tools:** `execute` receives an `ExecutionToolRequestV4` with a
  workspace tool name, encoded `call.arguments`, actor and permission mode, and
  returns the ordinary `ToolResult`. Paths belong to the initialized backend.
  The provider implements its operations, enforces read-only/path/OS boundaries,
  honors initialization retention flags and explicitly forwarded environment
  values, and rejects unsupported policies. The SDK does not create directories,
  forward the adapter's environment to commands, or implement a filesystem fallback.
- **Processes:** implement `process/start`, `process/list`, `process/get`,
  `process/read`, `process/stop` and whichever additional controls are supported.
  `ExecutionProcessV4` records use snake_case and stay bound to
  `root_thread_id: binding.session`. Read options and output intentionally retain
  Go wire casing (`MaxBytes`, `OffsetBytes`, `Wait`, `MinDwell`; `Process`, `Output`,
  `StartOffset`, `EndOffset`, `TotalBytes`, `Truncated`, `TimedOut`, `Duration`).
  Durations in these process records are **nanoseconds**, unlike `timeout_ms`.
  `emitProcess` publishes `{ Type, Cause, Process }` events on the same connection.
- **Stateless Code Mode:** advertise `code.stateless.v1` only when `run_code` enforces `disable_state` for the entire program, rejecting all session state access. The host requires this capability when a remote reader replacement is selected; it does not infer it from a handler. Ordinary Code Mode requests remain unchanged.

- **Acceptance and recovery:** explicitly advertise `capabilities: ["process.start.v1"]`
  only when start replies and persisted records returned by `process/list`
  preserve the host's `operation_id`. The helper checks the reply identity and
  requires both handlers and `close`; durability belongs to the provider. This capability
  does not promise duplicate-start deduplication. Requests are never replayed by
  the helper, and a lost reply is not proof that an effect did not occur.
- **Lifetime:** handlers can overlap; protect shared state. `context.signal`
  ends with its addressed request. `cancel` aborts it and the terminal reply waits
  for the handler to drain. An accepted detached process must not use that signal
  as its lifetime: use `context.lifetimeSignal` and stop owned process trees in
  `close`. EOF, malformed frames and output failure cancel and drain handlers
  before cleanup; even blocked input cannot postpone cleanup after output failure.
  Unsupported methods return errors, never a local substitute.
- **Code and artifacts:** optional `run_code` handlers use `context.invokeTool`
  only while their own parent request is live. Version 4 requires per-program
  `result_view` semantics; do not advertise a successful code implementation that
  ignores them. Artifact export uses `export/read` with a token and byte offset,
  returning base64 `{ data, eof }`, followed by `export/close`. Providers own
  immutable export snapshots. Omit unsupported handlers rather than faking success.

For a partial backend, `delegateExecutionBackendV4` provides an explicit peer
client. It does not discover or start a default worker:

```ts
const peer = delegateExecutionBackendV4({
  command: ["node", "/absolute/path/selected-peer.mjs"],
  capabilities: ["process.start.v1"],
});
await runExecutionBackendV4({
  ...peer,
  methods: {
    ...peer.methods,
    async execute(request, context) {
      // Add an audit operation here, then explicitly delegate unchanged input.
      return peer.methods.execute!(request, context);
    },
  },
});
```

The peer starts lazily at initialization and must accept the same complete
binding, protocol version, workspace root and requested capabilities. Each call
retains that binding and its current cancellation signal. Code callbacks use
the exact outer request's `invokeTool`; process events remain in the outer
connection lifetime. A missing/failed peer is never retried or replaced. Terminal
transport failure closes the peer immediately, even while the outer input stays
open. Cancellation waits up to 35 seconds for the peer's terminal reply before
closing an unresponsive peer. A spawned peer receives EOF for cleanup and has a
10-second exit budget by default. Set command-only `shutdown_grace_ms` to an
integer from 1 to 2,147,483,647 milliseconds when the selected peer needs another
budget, for example `90_000` for a cloud adapter paired with
`"shutdown_timeout_ms": 150000` in its outer command profile. The outer profile's
`shutdown_timeout_ms` must exceed the complete request drain and cleanup chain,
including each nested delegate. A peer that exceeds its budget is terminated and
cleanup failure is reported; remote resource state may remain unknown, so this is
not proof that all resources stopped. An explicitly attached `{ input, output,
close }` transport can be supplied instead of `command`; its `close` owns physical
cleanup and cannot be combined with `shutdown_grace_ms`. Spawned peers inherit the
adapter's environment unless `env` is supplied, so only select trusted peers and
provide a restricted environment when appropriate.

Native worker shutdown stops and reaps all processes it still owns, including
managed processes, without stopping another worker's processes in a shared
registry. Cleanup errors propagate to the caller. If native process cleanup or
host transport cleanup fails, Wuu preserves the worker workspace and skips host
container/state removal so an explicit recovery can inspect them.

Delegation is valid only for a coherent execution world. If an override replaces
files with an unrelated in-memory or remote filesystem, omit delegated process
methods and `process.start.v1` rather than running commands against different
files. The [audit and memory-filesystem acceptance fixtures](../../../scripts/external-composition-acceptance/README.md)
exercise both choices through the real host.

Plugin consumers use the separate [conversation-bound process API](../customize/plugin-authoring.md#conversation-bound-shell-processes).
`getProcessEnvironment` and accepted snapshots expose the selected location;
consumers cannot use that metadata to change the binding or acquire authority.

## Reproducible validation

With the current worker image built and Docker running:

```sh
WUU_EXECUTION_E2E_IMAGE=wuu-execution:local go test ./internal/executionworker -run TestDocker -count=1 -v
```

For Singularity/Apptainer, set `WUU_EXECUTION_E2E_SIF` to a current worker SIF image and run `go test ./internal/executionworker -run TestSingularity -count=1 -v`.

For SSH, set `WUU_EXECUTION_E2E_SSH` to a JSON SSH profile for a disposable test server, then run `go test ./internal/executionworker -run TestSSHEnvironmentEndToEnd -count=1 -v`. The test creates files and processes in its own environment directories.

Desktop acceptance uses `npm --prefix desktop run test:e2e:execution-environments`. It exercises the real renderer with a synthetic settings bridge and writes screenshots plus a JSON receipt under `desktop/out/e2e/execution-environments`. It does not authenticate cloud accounts. Live provider validation requires credentials and a published worker image in that provider.

## Worker compatibility

The host and worker must use execution protocol version 4. Upgrade the worker
binary or rebuild its image when upgrading the host. Older workers are rejected
before execution: programs require the current isolated interpreter and
actor-scoped JSON state and per-program result-view contracts. They cannot fall back to native program access. No existing permission is expanded.

`host.process` remote starts additionally require the additive
`process.start.v1` capability in the version-4 handshake. An older version-4
worker can still serve its existing methods, but cannot silently ignore the
operation-identity contract for new plugin process starts.
