# Built-in prompts

`system.md` contains shared identity, trust, authorization and file-reference
contracts. `system_main.md` contains general main-agent collaboration and task
completion guidance. Both are embedded by `system.go`; session-specific tools,
environment, user instructions and optional extension behavior are appended by
the runtime.

The main-agent guidance is adapted from OpenAI Codex. See [NOTICE](NOTICE) and
[LICENSE-Codex](LICENSE-Codex) for provenance and licensing. Adaptations preserve
Wuu's authorization boundaries, tool names and rendering contracts. They do not
assume that a question tool, automatic compaction or any optional plugin exists.

For maintained behavior and ownership, see the
[agent command system](../docs/en/reference/agent-command-system.md#task-completion-and-context)
and its [Chinese version](../docs/zh-cn/reference/agent-command-system.md#任务完成与上下文).
