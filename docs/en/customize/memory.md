# Memory

The Memory plugin keeps information that should remain useful after a conversation ends. Use the user notebook for preferences and reusable feedback, and workspace memory for durable project knowledge. Conversation history and the core agent's working notes are separate records.

## User notebook

Enable Memory in **Extensions**, then open its settings page under **Settings → Extensions**. You can inspect the notebook's source files, generate an overview, or ask the memory manager to add, correct, or remove information. The manager reports the files it changed; inspect those files when the exact result matters.

The notebook lives in `memory/` under Wuu's home directory, normally `~/.wuu`. `MEMORY.md` is its index, while individual Markdown files hold the actual memories. Saving or forgetting a topic requires updating both the topic file and its index entry. The plugin supplies `memory_list`, `memory_read`, `memory_write`, and `memory_delete` for these operations.

The plugin includes a bounded version of the index in the agent's context so it can find relevant topics. It does not load every topic file into every request. An overview is model-generated and can become stale; the source files are the record of what is saved. Reading source files does not require a model call, but generating an overview or managing memory through conversation does.

## Workspace memory

The plugin's `session_memory` tool exposes `project_memory` for stable knowledge about one workspace. It is stored in `memory/MEMORY.md` under that workspace's state directory, separate from the user notebook. The agent reads it on demand and can append or replace its contents.

Good candidates include a confirmed architecture decision, a recurring workflow lesson, or a tool quirk that is hard to rediscover. Prefer current repository instructions for shared rules and source code for facts that are already easy to look up.

## Session state

Memory also exposes `summary`, `checkpoint`, and `notes` targets through `session_memory`. These are plugin-managed files scoped to a session; `checkpoint` is a compatibility target.

The core `notes` tool is different. It maintains persistent virtual working notes for the active session, including goals, constraints, progress, and checks. Those notes survive context resets, restarts, and model changes without writing project files or requiring the Memory plugin. Neither kind of session note is a cross-project user memory.

Before requesting a fresh context with `new_context`, the built-in agent saves
a recovery note and can pass `checkpoint: {path, revision}` using the latest
`notes` result. Wuu validates the note in the current session and carries ready
`notes` read arguments into the next window. The revision covers the entire
notes collection, not just that file; a later write requires rereading the same
path at the current revision. Existing calls without a checkpoint still use
note discovery and archived history. The host carries a reference, not a new
summary, and the model reads the saved evidence before continuing.

## Keep memory useful

Save confirmed, lasting information. Do not use memory as a transcript archive, secret store, or log of temporary task progress. When a memory conflicts with current evidence, correct it rather than following it blindly.

Memory can be sent to the configured model and can influence later actions. Review sensitive entries and untrusted imported text. Disabling the plugin removes its prompts, tools, and settings page; it does not erase the existing notebook files.

[Dream](dream.md) can consolidate workspace knowledge in the background. It has its own opt-in setting and makes additional model calls. Project instructions such as `AGENTS.md` remain independent of both plugins; see [configuration](../reference/configuration.md) and the [security model](../reference/security-model.md).
