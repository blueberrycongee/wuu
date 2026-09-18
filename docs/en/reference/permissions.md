# Permission modes

Permission modes control which local paths the agent can access and modify. In the
desktop you can switch them in the runtime menu next to the input box; in the CLI you
can override a single run with `wuu exec --permission-mode`.

## The three modes

| Mode | Suited to | Behavior |
|---|---|---|
| Standard `standard` | Everyday project tasks | Reads and writes the file roots registered with the current runtime, including the agent home, user workspaces, and system temporary directories, and keeps protection for sensitive paths and high-risk operations |
| Read only `read_only` | Understanding code, investigation, and review | Keeps the same read scope but refuses file modifications |
| Unconfined `unconfined` | Trusted tasks that explicitly need files outside the workspace | Removes Wuu's path boundary, allowing access to and modification of everything the current system user can operate on |

Use standard mode unless there is a clear reason not to. Use read-only when you only
need analysis. Treat `unconfined` as handing the current logged-in user's local
permissions to the agent, and enable it briefly only for trusted tasks.

## Approve for me

In the desktop permission menu, **Approve for me** is a Standard-mode option for
the built-in Wuu engine. It is not a fourth permission mode and does not raise
the workspace boundary.

When it is on, high-risk native tool calls — shell, Git, process, browser, MCP,
and other destructive or high-risk tools — are reviewed before they run.
Ordinary workspace-local reads stay on the normal allow path. The reviewer can
allow the exact call, deny it, or leave the main agent to explain the action in
conversation and wait for informed user approval of that target. A timeout or
review failure is not a safety verdict: the action does not run, and the agent
must not bypass the check.

Approve for me cannot switch the session to Unconfined, read or write Wuu
credential files, or write known sensitive paths through the dedicated file
tools. `wuu exec` does not expose this option; non-interactive runs remain
allow-or-deny.

Even under `unconfined`, the dedicated tools keep the following defense in depth:

- known sensitive paths such as `.env`, SSH private keys, and credential
  configurations cannot be written through the dedicated file tools, nor staged or
  committed through the structured Git tools;
- the app credential files under `~/.wuu` (or `WUU_HOME`) — `auth.json`,
  `credentials.json`, `remote.json`, `phone.json` — cannot be read or written directly
  through these dedicated tools;
- common key formats in command output are always redacted.

These are protections of specific tools, not a promise for every execution path.
Arbitrary shell commands, scripts, and child processes inherit the system permissions
of the Wuu process and may bypass the file and Git restrictions above; output
redaction also cannot recognize every key or indirect disclosure path. So "credentials
are inaccessible" or "sensitive files cannot be committed" must not be treated as a
system-level security guarantee.

## System permissions and isolation

In `standard` and `read_only`, Wuu's command tools apply a filesystem sandbox.
Standard mode allows writes to registered working directories and a private temporary
directory; read-only mode restricts file writes. macOS uses a built-in backend. If no
backend is available, execution is refused until a sandbox extension is configured or
the user explicitly switches to `unconfined`.

This sandbox preserves the process identity, inherited environment, and network
access. It does not guarantee credential secrecy or cover every plugin, MCP server,
or external program. Use a container, virtual machine, or separate system account
for further isolation when handling malicious repositories, dependencies, or native code.

## CLI examples

```bash
wuu exec --permission-mode read_only "explain this repository without modifying files"
wuu exec --permission-mode standard "fix the tests and verify"
```

`wuu exec` is a non-interactive entry point; permission decisions are either allow or
deny, and it never pops a confirmation dialog in a script.

## Configuration ownership

On normal startup, project configuration cannot replace the permission mode you
chose. This prevents a repository from silently expanding local permissions when you
open it. See the [configuration model](configuration.md) for the full load order, and
the [security model](security-model.md) for the detailed risk boundary.
