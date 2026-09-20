# Run tasks with `wuu exec`

`wuu exec` runs an agent task without opening the desktop. Use it from a shell,
CI job, system scheduler, or another agent. It uses Wuu's session and execution
services; first [configure a model service](../getting-started/model-services.md).

## Supply a task

```bash
wuu exec --workdir /path/to/repo "fix the failing test and verify it"
wuu exec - < task.md
wuu exec "investigate this failure" < error.log
```

Put flags before the prompt or session ID: argument parsing stops at the first
positional argument. With no positional prompt, piped stdin supplies the task.
With both, stdin is appended inside a `<stdin>` block. A task needs text or an
attachment; a bare `-` explicitly requires nonempty stdin.

Attach local images with repeated `--image` flags and PDFs with repeated `--file`
flags. Other document formats are not accepted by `--file`. Relative attachment
paths resolve from `--workdir`, or the current directory when it is omitted.

```bash
wuu exec --image screenshot.png "find the layout problem"
wuu exec --file report.pdf "summarize the findings"
```

`--image-original` disables resizing for the supplied images. Whether the model
can use an attachment still depends on its provider and input capabilities.

## Continue, fork, or review

```bash
wuu exec resume --last "continue from the failure"
wuu exec resume --json THREAD_ID "continue this task"
wuu exec fork THREAD_ID "try another approach"
wuu exec review --uncommitted
wuu exec review --base main
wuu exec review --commit COMMIT_SHA
```

`resume --last` selects the latest visible session in the workspace. `--continue`
or `-c` in the first position after `exec` is its shortcut. `--resume` or `-r` in
that position is a shortcut for `resume`; without further arguments it lists
available sessions. `fork` creates a separate conversation from the source history.

`review` creates a review prompt for the chosen scope and uses the normal tools
to inspect it. It is not a separate static analyzer. Choose `--permission-mode
read_only` when the task should not edit files.

## Control a run

| Flags | Effect |
| --- | --- |
| `--provider`, `--model`, `--effort`, `--variant` | Select the model route and supported reasoning settings |
| `--profile` | Select an agent profile |
| `--permission-mode` | Use `standard`, `read_only`, or `unconfined` |
| `--workdir` | Set the workspace directory |
| `--config` | Explicitly trust one configuration file |
| `--ignore-user-config` | Explicitly trust project configuration instead of user configuration |
| `--env KEY=VALUE` | Set a run environment variable; repeatable |
| `--no-tools` | Disable local tools |
| `--max-turns N` | Set the model/tool loop limit; zero uses the configured default |
| `--timeout 20m` | Bound the run duration; zero sets no CLI deadline |
| `--ephemeral` | Create an in-memory session instead of a resumable saved session |
| `--json` | Write a JSONL event stream |
| `--output-last-message FILE` | Write the final answer to a file after successful execution |
| `--output-schema FILE` | Validate the final answer against a JSON Schema |
| `--input-json` | Read the task and options as one JSON object from stdin |

Normal startup uses user configuration plus permitted project overrides. The two
explicit trust flags change that boundary; review the supplied configuration before
using them in automation. See [configuration](../reference/configuration.md) for
layering and [permissions](../reference/permissions.md) for command confinement.
There is no interactive approval prompt to resolve a denied operation.

## Capture output

In text mode stdout contains the final answer after successful execution. Progress,
session identifiers, and diagnostics go to stderr. In JSONL mode stdout contains
one event object per line; consume the terminal `result` and the process exit code,
not the first `turn_completed`. A run can contain automatic continuation or
structured-output correction turns.

```bash
wuu exec --json --timeout 20m "review the current changes" > events.jsonl
wuu exec --output-last-message report.md "summarize this repository"
wuu exec --json --output-schema schema.json "return the requested report"
```

The schema path resolves from the workspace. The last-message output path instead
resolves from the invoking process's current directory; an existing file is
overwritten. Successful schema validation adds `structured_result` to the JSONL
result. See [JSONL events](jsonl-events.md) for fields and failure handling.

## Machine input

```bash
wuu exec --input-json <<'JSON'
{
  "prompt": "investigate this failure",
  "stdin": "panic: example failure",
  "workdir": "/path/to/repo",
  "permission_mode": "read_only",
  "json": true,
  "timeout": "10m"
}
JSON
```

The object accepts `prompt`, `stdin`, `files`, `images`, `file_attachments`,
`image_attachments`, `workdir`, `provider`, `model`, `effort`, `variant`,
`permission_mode`, `config`, `profile`, `ignore_user_config`, `env`, `max_turns`,
`no_tools`, `json`, `ephemeral`, `timeout`, `output_last_message`, and
`output_schema`. Path arrays use the same rules as the attachment flags; structured
attachment arrays use the [app-server shapes](../integrations/app-server-protocol.md).
`env` is an array of `KEY=VALUE` strings. Unknown fields and multiple JSON values
are rejected, and a positional prompt cannot accompany `--input-json`.

Prefer supplying an option in one place. Nonempty CLI selections take precedence;
attachment and environment arrays are combined. Boolean `true` and nonzero CLI
values take precedence over the corresponding input values, so an explicitly
passed `false` or zero is not a reliable way to override JSON input. The `review`
subcommand does not accept `--input-json`.

## Exit codes

| Code | Meaning |
| --- | --- |
| `0` | Completed successfully |
| `1` | Run failed without a more specific classification |
| `2` | Invalid arguments, configuration, or input |
| `3` | Permission denied |
| `4` | Timed out |
| `5` | Interrupted or cancelled |
| `6` | Protocol or output-stream error |
| `7` | Provider, model, authentication, or network failure |
| `8` | Unrecovered local/tool failure |
| `9` | Target session already has an active execution |

Argument parsing can fail before any JSONL event is written. Treat a missing
`result`, a failed result, or a nonzero exit code as failure. Do not infer success
from a partial answer. Inspect saved sessions and execution records through
[`wuu session` and `wuu runs`](../reference/cli-commands.md).
