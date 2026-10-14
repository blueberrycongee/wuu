# CLI commands

Use the CLI to initialize configuration, run work, inspect saved sessions, and manage local extensions. Run `wuu --help` for the command summary. In examples below, replace uppercase IDs and paths with your own values, and put flags before positional arguments unless the command documents another form.

## Initialize and identify the build

```bash
wuu init
wuu version
wuu version --long
wuu version --json
```

`init` creates user configuration and refuses to overwrite an existing file. Use `wuu init --force` only when you intend to replace it. Configure a working model connection before starting a task.

`wuu models --provider NAME --json` performs live model lookup for an `openai-codex` provider. It is not a general offline listing of every configured provider and currently rejects other provider types. It needs the corresponding connection and authentication.

## Run, continue, and review

```bash
wuu exec --workdir /path/to/project "Run the relevant tests and explain failures"
wuu exec --continue "Continue the investigation"
wuu exec resume THREAD_ID "Continue this task"
wuu exec fork THREAD_ID "Try another approach"
wuu exec review --uncommitted --permission-mode read_only
wuu exec review --base main --permission-mode read_only
wuu exec review --commit COMMIT_SHA --permission-mode read_only
```

`wuu -c` and `wuu -r THREAD_ID` are shortcuts for exec continuation and resume. A fork creates a new conversation from saved context; do not confuse it with a desktop worktree fork. Review uses the selected permission mode, so choose Read only for inspection.

For stdin, attachments, JSONL output, schemas, timeouts, and exit codes, see [`wuu exec`](../automation/exec.md). `wuu run` remains a compatibility entry point; use `exec` for new scripts.

## Inspect sessions

```bash
wuu session list --json
wuu session list --all-workdirs --include-archived
wuu session show --json THREAD_ID
wuu session show --last
wuu session trace --json THREAD_ID
wuu session search --workdir /path/to/project "keyword"
```

Lists default to the current workspace. `show` reads metadata and history; `trace` replays the saved trace without rerunning tools or contacting the model. Search checks metadata and history. Prefer supported JSON output for scripts rather than parsing display text.

## Archive, export, and delete

```bash
wuu session archive THREAD_ID
wuu session export --out conversation.jsonl THREAD_ID
wuu session export --json --out conversation.json THREAD_ID
wuu session delete THREAD_ID
```

Archive hides a session from ordinary lists without deleting it. Export normally writes a metadata header and history records as JSONL; `--json` writes one object containing metadata and history. `--out` creates or overwrites the output file. Review exports before sharing them.

Delete removes saved session data and its associated workspace artifacts. Confirm the ID and retain any results you need before using it.

## Inspect execution runs

```bash
wuu runs --json
wuu runs read RUN_ID
```

These commands read persisted execution-run manifests. They are separate from the Automation plugin's task and run list in the desktop.

## Skills and plugins

```bash
wuu skills lint .wuu/skills
wuu plugin list
wuu plugin install ./my-plugin.zip
wuu plugin approve my-plugin
wuu plugin disable my-plugin
wuu plugin remove my-plugin
```

See [skills](../customize/skill-authoring.md) for lint behavior and [plugins](../customize/plugins.md) for local installation and updates. Authors can use `create`, `dev`, `validate`, `build`, `test`, and `pack`; those commands are covered in the [authoring reference](../customize/plugin-authoring.md).

## Evaluate built-in tasks

```bash
wuu eval --list
wuu eval --provider NAME --task test_failure_fix --artifacts-dir ./eval-results --json
wuu eval --provider NAME --environment inherited --task mcp_live_discovery
wuu eval --replay-trace /path/to/eval-trace.jsonl --json
```

Each task runs in a fresh Wuu process. The default `--environment isolated`
creates a fresh home and Wuu state directory for every task. It keeps the selected
provider, configured model roles and aliases, agent settings, and PTC settings.
API keys and bearer tokens are resolved using the normal configured/environment/
saved-credential precedence, including catalog and selected-model defaults, before
isolation, then passed to the worker in memory.
They are not copied into a config file or the evaluation report.

Personal instructions, skills, plugins, memory, hooks, MCP servers, engine
settings, and execution-environment settings are not imported. Bundled plugins
and skills use their normal defaults; catalog tasks add their own declared
fixtures and MCP dependencies. Background model-catalog refresh is disabled.
The report and replay trace record the environment mode, agent/PTC settings,
active plugin fingerprints, guidance fingerprints, and configured MCP names.

Use `--environment inherited` when intentionally evaluating your existing
configuration, extensions, or subscription credentials. It reads and can modify
your normal Wuu state, and results are labeled inherited. Home-backed subscription
authentication requires this mode; isolated runs fail with an actionable message
rather than copying a refresh token. Providers whose selected models require
different credential pairs also require inherited mode. The explicitly live
`--live-codex-oauth` workflow selects inherited mode unless you supply an
incompatible environment.

`--artifacts-dir` chooses a parent for a new, private run directory; omitting it
uses the system temporary directory. The exact directory appears in the output.
Each run retains `report.json`, per-task `eval-trace.jsonl` and `worker.log`, and,
in isolated mode, the task's Wuu runtime state. `--output FILE` additionally
writes the JSON report to that file. Task workspaces and temporary homes are
removed after execution unless `--keep-workdirs` is set. Review evidence before
sharing it; runtime logs and tool artifacts can contain task content. Temporary
locations may be cleaned by the operating system, so choose a durable directory
for evidence you want to keep.

This isolates Wuu home state, not the machine or network. Installed tools,
`PATH`, other inherited environment variables, and network access still affect
a run. Use the same machine/toolchain, selected tasks, provider/model settings,
timeout, and environment mode when comparing results. A trace replay performs
no model calls or tool execution.

## Integration and diagnostics

[`wuu app-server`](../automation/app-server.md) provides the persistent subprocess protocol used by clients. [`wuu remote` and `wuu relay`](../automation/remote.md) run remote-control components. For a problem report, capture the version, command, workspace, and a minimal redacted error; see [troubleshooting](../help/troubleshooting.md).
