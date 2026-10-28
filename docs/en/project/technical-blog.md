# Wuu: Turning a Local AI Agent into an Observable, Extensible Workbench

Many coding agents begin with a request such as “fix this project for me”. Reliability depends on what happens after that request: which files the agent reads, which tools it calls, where commands run, how permissions are enforced, and whether an update can disrupt work already in progress. Wuu is designed to make those steps observable, recoverable, and extensible.

## From CLI to desktop application

Wuu’s core is written in Go, with the CLI entry point in [`cmd/wuu/main.go`](../../../cmd/wuu/main.go). The CLI does more than send a prompt: it routes model services, sessions, execution records, plugins, skills, and the app-server. The Electron desktop client owns workspace, conversation, file preview, Review, and terminal interactions. It communicates with the Go core through the app-server, keeping UI state and task execution in separate layers.

A normal task can be described as follows: the user creates a thread in a workspace, chooses a model and permission mode, and submits a turn. The app-server creates the execution context. The agent loop asks the model for the next step, and the model uses file tools, Shell, browser, or plugin tools to act. Tool results, streamed text, and state changes are written back to the session, so the desktop can show both the process and the final diff. The CLI reuses the same core capabilities for scripts through JSONL or text output.

## The hard part of an agent loop is state

An agent loop handles more than a sequence of chat messages. The model may emit reasoning and then request a tool; a tool may be cancelled, time out, or return a large result; and the user may interrupt before the current answer is complete. Wuu keeps turns, execution leases, cancellation signals, and tool scopes in the host. A late tool result therefore cannot reactivate an execution that has already ended.

Context length is another constraint for long-running work. Request assembly, context budgets, and compaction in `internal/agent` organize history as the window approaches its limit while retaining the summaries and runtime information needed to continue. A long conversation does not simply lose its direction when it runs out of tokens; the host persists session history, workspace state, and the active task.

## Plugins own business behavior, not core boundaries

Wuu does not put every feature in the kernel. Todo, memory, automation, goals, and subagents are delivered as TypeScript plugins through the public `@wuu/plugin-sdk`. The Go runtime remains responsible for host services, tool dispatch, session persistence, permissions, and cleanup.

Plugins communicate with the host over JSONL and negotiate capabilities, services, and dependencies before activation. The runtime manages a group of plugins, tools, hooks, skills, and MCP connections as a generation. An update first prepares a candidate, verifies dependencies and persistence, and only then switches the active generation. Turns that are already running continue with the old generation, while idle resources can be cleaned up. This prevents an old task from observing newly replaced plugin state and lets a failed update retain the previous working runtime.

## Permissions must reach the execution path

Wuu stores the permission mode as part of a thread. Read-only mode rejects mutation tools. Standard mode permits work inside the registered workspace boundary. Unconfined mode removes ordinary path restrictions. Even there, dedicated tools continue to reject writes to sensitive paths such as credential files, environment files, and SSH private keys.

The tool layer answers “what does the agent want to do?” The process sandbox answers “where can the command actually write?” Wuu uses Seatbelt on macOS and Landlock on Linux. If a restricted backend is unavailable or can only apply a partial restriction, the command fails instead of silently falling back to unconfined execution. Plugins, MCP services, and external engines still need separate evaluation because they are trusted code or independent execution paths rather than a generic security sandbox.

## Why observability matters

When file changes, command output, model streams, and tool calls become part of the session, Review is more than a result viewer: it is a debugging surface. A developer can investigate in read-only mode, switch to standard mode for changes, and use execution records to identify which turn, tool, or plugin produced a problem. Persistent sessions, cancellable execution, and generation cleanup also provide shared foundations for crash recovery, hot reload, and remote calls.

Wuu’s technical direction has three parts: a Go core for execution and state, Electron for presenting the process, and a public plugin contract for changing business capabilities. The result is more than a chat window that generates patches. It is local software that can be observed, controlled, and extended.
