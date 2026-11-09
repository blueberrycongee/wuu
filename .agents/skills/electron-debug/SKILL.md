---
name: electron-debug
description: Debug Electron desktop issues across renderer, preload, main process, and Go app-server.
trigger-condition: Use for desktop crashes, stale IPC, app-server mismatches, or Electron dev restart problems.
allowed-tools: [read_file, grep, glob, bash]
required-context: [desktop process state, changed main or preload files, app-server PID, failing IPC channel]
examples: [white screen after IPC change, stale go run cache, debug panel visibility]
verification-checklist: [correct process restarted, IPC handler exists, production debug controls remain hidden]
progressive-disclosure: Inspect process state before assuming source changes reached the running desktop.
---

# Electron Debug

Debug the running stack, not only the source tree.

1. Determine whether the bug is renderer, preload, main process, or app-server.
2. Check for stale Electron main processes and stale `go run` app-server binaries.
3. Verify IPC channels are registered on both preload and main sides.
4. Restart the full stack when main process code changed.
5. Keep production debug UI gated behind debug controls.

When runtime behavior contradicts source, treat the live process as the ground truth.

## Product and visual gates

- Production builds must never expose debug UI or the debug-controls switch.
- Development builds hide debug UI by default. Gate every developer-only control through the development-only Settings switch.
- Tests that need debug UI must enable it explicitly and preserve production coverage proving it is hidden.
- The user owns final visual validation. Do not take screenshots, automate the UI, or claim visual approval unless explicitly asked in that turn.
- For UI changes, verify code correctness, relevant tests/builds, and that the latest Desktop runtime starts. Leave the current build running for the user to inspect.
