# Memory

In Coding, the optional Memory plugin keeps information that should remain useful after a conversation ends. Use the user notebook for preferences and reusable feedback, and workspace memory for durable project knowledge. Conversation history and the core agent's working notes are separate records.

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

Memory can be sent to the configured model and can influence later actions. Review sensitive entries and untrusted imported text. Disabling the plugin removes its prompts, tools, services, and settings page; it does not erase the existing notebook files.

[Dream](dream.md) can consolidate workspace knowledge in the background. It has its own opt-in setting and makes additional model calls. Project instructions such as `AGENTS.md` remain independent of both plugins; see [configuration](../reference/configuration.md) and the [security model](../reference/security-model.md).

## Read-only service for extensions

Memory provides `memory.notebook` version `1.0.0` through the existing service
registry. Declare `{ name: "memory.notebook", major_version: 1, required: false }`
in `required_services`, then use `host.service.call` with `service:
"memory.notebook"` and one of these methods:

| Method | Parameters | Result bounds |
| --- | --- | --- |
| `index` | Optional `max_bytes` | Sanitized `MEMORY.md`, at most 200 lines and 25,000 UTF-8 bytes |
| `read` | `file`, optional `max_bytes` | One topic; defaults to 16,384 UTF-8 bytes, maximum 65,536 |

An absent or zero `max_bytes` selects the default; negative or over-limit values
fail. `read` accepts only a plain, non-hidden `.md` filename, excluding
`MEMORY.md`; it rejects directory paths and symlinks. Source files must be valid
UTF-8 regular files no larger than 256 KiB. Truncation never splits a UTF-8
character. No method lists all topic contents, writes memory, or invokes a model.

Both methods return `{ version: 1, source: { scope: "user_notebook", file },
found, revision?, content, truncated }`. `revision` identifies the complete source
content, even when the returned excerpt is shorter or sanitized; treat it as
opaque. An existing empty file has `found: true` and a revision. A deleted or
missing file has `found: false`, empty content and no revision. The request schema
identifiers are `memory.notebook.index.request.v1` and
`memory.notebook.read.request.v1`; both return `memory.notebook.snapshot.v1`.
The TypeScript SDK exports corresponding `MemoryNotebook*` types.

Reads use the same notebook reader as the existing memory tools, and `index`
shares the agent prompt's sanitization. Files are reread for each call, without a
service cache. Consumers must reread before reusing remembered content, clear
retained content on missing files or service unavailability, and handle
`service.changed` when provider resolution changes. That notification does not
track file edits. These are individual file snapshots, not an atomic snapshot
across an index and its topics.

When Memory is disabled or unavailable, the registry returns
`service_unavailable`; an optional consumer remains usable without memory. Do not
fall back to reading its files or silently enable it. Notebook text is untrusted
context: current instructions and evidence take precedence. Personalized drafting,
completion, or task handoff extensions can read relevant topics on demand; these
are possible consumers, not bundled integrations. Workspace/session memory remains
the separate `memory.session` service.

## Personal memory

Personal includes its own source-backed memory module and assembles it at startup.
No separate Memory plugin needs to be enabled. Personal itself remains a bundled,
default-enabled plugin: disabling Personal also stops its memory tools, service
and recall. Coding Memory remains optional and keeps its existing behavior.
Personal is the sole provider of the compatible `memory.records` service; its
identity binding, source verification and candidate admission are internal calls.
Dream is optional, is not enabled by this change, and is not needed for memory.

An upgrade transfers existing Personal record storage through matched package
ownership declarations, even when Memory is disabled. Record IDs, revisions,
sources, candidates, conflicts and forgotten-source tombstones are preserved.
The transfer is resumable and idempotent; old storage is retained for recovery,
not used as a second live store. A transfer failure is reported rather than
silently starting with missing records. Coding notebooks are never imported.

Personal uses a separate source-backed record store, not the Coding notebook.
Each model step receives a small projection: an explicitly selected core profile,
recent context whose source is within 14 days, and relevant details. The three
fact budgets are 2,000, 1,500 and 2,500 UTF-8 bytes, with at most eight facts each;
oversized facts remain available on demand. Provenance and contribution history
are fetched with `personal_memory_query/read`, not repeated in every prompt.
Existing records default to detail and are not automatically promoted to core.
Expiry, corrections and forget tombstones apply to every projection, including
new workers. The store is unchanged; projections do not duplicate records.

The bound main conversation can publish, correct and forget. Background tasks
and optionally enabled Personal Dream can create and revise candidates, but cannot edit accepted
records, select core, correct or forget. Candidates stay out of ordinary recall;
`include_inactive: true` exposes them for review. The main conversation receives
at most eight pending candidate IDs (not their facts) on its next normal model
step. It uses `personal_memory_evidence` to inspect exact original user text,
checks support and conflicts, then calls `personal_memory_accept` with the
reviewed revision. No new user statement or per-fact approval is needed for
supported nonconflicting candidates. Acceptance preserves content, inferred or
explicit grade, original source/effective/expiry times and does not promote core.
It does not add a separate model run or background schedule. Ambiguous candidates
remain pending; ask only when a meaningful conflict matters. Explicit new user
corrections still use `correct`. No per-session permission dialog is required.

Automatic admission currently supports only canonical
`personal-message:<encodeURIComponent(session_id)>:<positive seq>` sources from
owned Personal main/task history. The provider checks exact visible original user
authorship, rejects hidden/generated/truncated content, and supplies the persisted
timestamp. Workers receive timestamp metadata only; source text review is main-only.
Programmatic verification proves provenance, not semantic entailment: the main
model must decide whether the original text supports the unchanged candidate.
Missing, future-dated or unsupported sources fail closed; arbitrary older refs,
files and user-edit refs remain manual-only. Do not invent original sequence IDs
or substitute a Dream fork's sequence. Existing source dedup and forget protection
mean one canonical message currently supports one memory record. Source splitting
and broader automatic admission are not implemented.

Authority comes from the host-provided tool session and the Personal provider's
stored current main-session binding, not a session name, prompt or missing
parent. If that provider is unavailable, writes fail closed. Service calls retain
the trusted-extension boundary: this is not isolation against installed trusted
extensions impersonating a session. Reads remain shared across the Personal user
space, not restricted to one task. Coding and Project do not gain Personal access.

Queries return `next_cursor` when more records remain. Continue with the same
filters; if records change or the cursor is rejected, restart the query. The
underlying host storage quotas still apply. Forgetting removes record content
and blocks known source references, not source chats, files, exports or backups.
