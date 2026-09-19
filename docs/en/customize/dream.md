# Background memory consolidation (Dream)

Dream turns useful knowledge from a completed conversation into workspace memory. It runs in a private background session, uses a model, and is off by default.

## Enable Dream

Enable the Dream plugin, then open **Settings → Plugins → Dream** and turn on consolidation. A provider of the `memory.session` service must also be available; the bundled Memory plugin provides it. Dream skips a run if it cannot read current workspace memory through that service.

| Setting | Meaning | Default |
|---|---|---|
| `enabled` | Allow background consolidation | `false` |
| `interval_days` | Minimum days between successful runs, from 1 to 365 | `7` |
| `min_sessions` | Number of distinct candidate sessions needed, from 1 to 100 | `5` |
| `model_alias` | Optional model selection for the private session | Empty, inherit from the parent |

Settings and run state belong to the plugin's workspace storage. Changes take effect without editing the core configuration.

## How a run starts

A successful conversation turn makes its session a candidate. Further successful turns update that session's timestamp rather than increasing the candidate count. The plugin checks candidates after completion and from its timer.

An automatic run needs Dream enabled, enough candidates, and the configured interval since the last successful run. Only one run starts at a time; a failed run also has a one-hour retry backoff.

Dream reads current `project_memory`, selects the most recent candidate, and forks that conversation into a private session. It does **not** combine the full histories of all candidates into one prompt. After a successful run, it clears the candidate set and records the completion time.

## What it saves

The consolidation prompt asks for durable architecture decisions, conventions, tool quirks, and recurring workflow lessons. It directs the model to update `project_memory` through `session_memory`, avoid source changes, and omit secrets, raw transcripts, temporary progress, PR numbers, and commit hashes.

These are instructions to the model, not proof that every proposed memory is correct. Inspect [workspace memory](memory.md#workspace-memory) and correct unwanted entries. Deleting an entry does not guarantee that a later conversation will never cause similar information to be saved again.

## Cost and lifecycle

Each run sends the forked context and current workspace memory to the selected model. It can add cost and write information that later conversations read. Use Dream only when that background processing is appropriate for the workspace.

Disabling or unloading the plugin stops its timer. Persisted settings and run state are restored when it loads again; interrupted runs are recorded as failures rather than left permanently running. See the [security model](../reference/security-model.md) before enabling it for sensitive work.
