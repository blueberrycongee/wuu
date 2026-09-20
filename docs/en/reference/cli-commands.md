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

## Integration and diagnostics

[`wuu app-server`](../automation/app-server.md) provides the persistent subprocess protocol used by clients. [`wuu remote` and `wuu relay`](../automation/remote.md) run remote-control components. For a problem report, capture the version, command, workspace, and a minimal redacted error; see [troubleshooting](../help/troubleshooting.md).
