# Permission modes

Choose the permission mode beside the desktop composer, or pass `--permission-mode` to `wuu exec`. The same labels are available across engines, but the built-in Wuu engine and external programs enforce them differently.

## The three modes

For the Wuu engine:

| Mode | Dedicated tools | Commands |
|---|---|---|
| Standard `standard` | Read and write within registered file roots, subject to sensitive-path guards | Filesystem sandbox permits writes within execution roots and a private temporary directory |
| Read only `read_only` | Same read scope; mutating calls are refused | Allowed commands run with filesystem writes restricted |
| Unconfined `unconfined` | Removes the normal path boundary; dedicated sensitive-path guards remain | Removes Wuu's filesystem process sandbox |

Registered file roots can include the agent's scoped home, user workspaces, and the system temporary directory. Commands do not receive the whole shared temporary directory as a writable root: they get a private temporary directory instead.

Use **Read only** for investigation and **Standard** for ordinary changes. Unconfined gives commands the local authority of the user running Wuu. Choose it only when you understand why the task needs that access.

## System permissions and isolation

The process sandbox controls filesystem writes, not all reads, network access, process visibility, or inherited environment variables. In particular, read-only does not mean that commands cannot disclose data. Dedicated file-tool boundaries and subprocess confinement are separate protections.

The built-in backend is available on macOS. On other platforms, or when that backend cannot run, confined commands require a configured `sandbox.process@1` extension. A missing, failed, or partially enforcing backend causes execution to fail; Wuu does not silently retry unconfined.

This is not a sandbox for all installed code. Plugins, hooks, MCP servers, external engines, and commands typed manually into the desktop terminal have their own execution paths. Use an isolated environment or separate OS account for untrusted repositories and dependencies. See the [security model](security-model.md).

## Approve for me

**Approve for me** is a desktop option for the Wuu engine in Standard mode. It adds model review before selected native tool calls; it does not create a fourth mode or expand the workspace boundary.

All shell execution goes through review, including commands classified as read-only. Other destructive or high-risk calls, and applicable Git, process, browser, and MCP actions, are also reviewed. Ordinary low-risk reads do not require it. Review uses the active conversation's model and can add model requests, cost, and latency.

The reviewer can allow the call, deny it, or leave it unresolved so the agent can explain the proposed action and seek informed user approval in conversation. A timeout, cancellation, or review failure leaves the action unexecuted; it is not permission to bypass review. The option cannot elevate the session to Unconfined or override dedicated credential and sensitive-write guards.

`wuu exec` does not expose this option. Its native permission checks allow or deny calls without opening an interactive approval dialog.

## Sensitive paths

Dedicated file tools refuse writes to known sensitive paths such as environment files and SSH private keys, including in Unconfined mode. Wuu credential files such as `auth.json`, `credentials.json`, `remote.json`, and `phone.json` under Wuu's home remain blocked for direct reads and writes. Structured Git tools also guard sensitive staging and commits.

Tool output redaction recognizes common secret patterns, but cannot recognize every secret or encoding. These guards do not guarantee secrecy across arbitrary shell programs, third-party code, or network requests.

## External engines

The adapters pass the selected mode to the external program:

| Wuu selection | Codex | Claude Code |
|---|---|---|
| Standard | `workspace-write`, approvals `on-request` | `dontAsk` |
| Read only | `read-only`, approvals `never` | `plan` |
| Unconfined | `danger-full-access`, approvals `never` | `bypassPermissions` |

These are adapter settings, not a claim that the engines provide identical protection. Claude Code's headless transport has no permission-prompt bridge; Standard uses `dontAsk` so permission requests are denied rather than waiting indefinitely. External program versions and configuration determine their native behavior.

## CLI examples

```bash
wuu exec --permission-mode read_only "Explain the repository without changing files"
wuu exec --permission-mode standard "Fix the failing test and run the relevant checks"
```

Normal project configuration cannot silently replace the user's permission mode. Explicit automation configuration has separate trust rules; see [configuration](configuration.md).
