# Execution environments

Choose **Settings → Runtime → Execution environments** to run workspace tools in a container or on a remote machine. Local execution remains the default. Install the same Wuu revision in the environment; a protocol mismatch stops execution.

Create a profile, then select it as the default for new conversations. The choice and profile are saved with each conversation. Editing or deleting the profile does not move an existing conversation. Older conversations retain local execution. Forks inherit the profile and receive a separate environment unless sharing is enabled. Inplace subagents and read-only side conversations use their parent’s environment.

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
| Command | Installed trusted adapter implementing the private worker protocol | The adapter owns provisioning and enforcement |

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

Optional fields include `shared`, `user`, `host_workspace`, `mount_read_only`, `forward_env`, `worker`, `lifetime_seconds`, `python`, and SSH `host`, `port`, `identity_file`, `known_hosts_file`. A command adapter uses an executable-and-arguments array in `command`. Adapters are trusted local programs; selecting one grants it the host permissions of Wuu.

## Reproducible validation

With the current worker image built and Docker running:

```sh
WUU_EXECUTION_E2E_IMAGE=wuu-execution:local go test ./internal/executionworker -run TestDocker -count=1 -v
```

For Singularity/Apptainer, set `WUU_EXECUTION_E2E_SIF` to a current worker SIF image and run `go test ./internal/executionworker -run TestSingularity -count=1 -v`.

For SSH, set `WUU_EXECUTION_E2E_SSH` to a JSON SSH profile for a disposable test server, then run `go test ./internal/executionworker -run TestSSHEnvironmentEndToEnd -count=1 -v`. The test creates files and processes in its own environment directories.

Desktop acceptance uses `npm --prefix desktop run test:e2e:execution-environments`. It exercises the real renderer with a synthetic settings bridge and writes screenshots plus a JSON receipt under `desktop/out/e2e/execution-environments`. It does not authenticate cloud accounts. Live provider validation requires credentials and a published worker image in that provider.
