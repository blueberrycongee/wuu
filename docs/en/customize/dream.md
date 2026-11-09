# Background memory consolidation (Dream)

Dream turns useful knowledge from a completed conversation into workspace memory. It runs in a private background session, uses a model, and is off by default.

## Enable Dream

Enable the Dream plugin, then open **Settings → Extensions → Dream** and turn on consolidation. A provider of the `memory.session` service must also be available; the bundled Memory plugin provides it. Dream skips a run if it cannot read current workspace memory through that service.

| Setting | Meaning | Default |
|---|---|---|
| `enabled` | Allow background consolidation | `false` |
| `interval_days` | Minimum days between successful runs, from 1 to 365 | `7` |
| `min_sessions` | Number of distinct candidate sessions needed, from 1 to 100 | `5` |
| `model_alias` | Optional model selection for the private session | Empty, inherit from the parent |

Settings and run state belong to the plugin's workspace storage. Changes take effect without editing the core configuration.

## How a run starts

A successful conversation turn makes its session a candidate. Further successful turns update that session's timestamp rather than increasing the candidate count. Plugin-owned background sessions, including Dream's own runs and Memory jobs, never become candidates. The plugin checks candidates after completion and from its timer.

An automatic run needs Dream enabled, enough candidates, and the configured interval since the last successful run. Only one run starts at a time; a failed run also has a one-hour retry backoff.

Dream reads the current `project_memory` index and, when the optional `memory.notebook` service is available, a health report of the user notebook: index size against its injection budget, index links to missing files, topic files missing from the index, and unverified notes ranked by how often agents read them. It then forks the most recent candidate into a private session and lists up to 20 of the newest candidates. Rather than pasting every transcript into one prompt, the run uses `history_search` and `history_read` to look up evidence in those sessions.

After a successful run, Dream removes only the sessions it reviewed. Older candidates beyond the batch and sessions that completed during the run stay queued for the next run. A failed or interrupted run keeps every candidate. The run's final report appears as **Last report** on the settings page.

## What it saves

The consolidation prompt asks the model to do three things, in order:

1. Verify existing entries against the transcripts and the current repository, then fix or remove what is wrong or stale. Confirmed notebook notes get a `verified` date and a `sources` reference.
2. Consolidate durable decisions, conventions, tool quirks, and lessons that recur across sessions, merging duplicates.
3. Keep both indexes small by moving detail into topic pages and fixing the reported health problems.

Workspace knowledge goes to `project_memory` through `session_memory`. Notebook notes are changed only when the reviewed sessions show they are wrong or stale. The prompt forbids source changes, secrets, raw transcripts, temporary progress, PR numbers, and commit hashes.

These are instructions to the model, not proof that every proposed memory is correct. Inspect [workspace memory](memory.md#workspace-memory) and correct unwanted entries. Deleting an entry does not guarantee that a later conversation will never cause similar information to be saved again.

## Cost and lifecycle

Each run sends the forked context, current workspace memory, the notebook health report, and any history it looks up to the selected model. It can add cost and write information that later conversations read. Use Dream only when that background processing is appropriate for the workspace.

Disabling or unloading the plugin stops its timer. Persisted settings and run state are restored when it loads again; interrupted runs are recorded as failures rather than left permanently running. See the [security model](../reference/security-model.md) before enabling it for sensitive work.
