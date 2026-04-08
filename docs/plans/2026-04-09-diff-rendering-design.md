# Diff Rendering Design

**Goal:** Display unified diffs in tool cards when `edit_file` or `write_file` modifies files, aligned with Claude Code's visual semantics.

**Approach:** Pure Go implementation using `sergi/go-diff` for diff computation, lipgloss for terminal styling.

## Data Layer

### edit_file

Already has `text` (old) and `newContent` (new). Compute unified diff hunks and include in result JSON:

```json
{
  "path": "internal/foo.go",
  "diff": {
    "hunks": [
      {
        "old_start": 10,
        "new_start": 10,
        "lines": [
          {"op": "equal", "content": "  context line"},
          {"op": "delete", "content": "  old line"},
          {"op": "insert", "content": "  new line"}
        ]
      }
    ]
  }
}
```

### write_file

Read old file before writing. If file exists, compute diff (same as edit_file). If new file, return `{"diff": {"new_file": true, "lines": N}}`.

## Render Layer

New file `internal/tui/render_diff.go`.

Visual design:

```
  10   context line
  11 - old line here          ← dark red background
  11 + new line here          ← dark green background
  12   context line
```

- Gutter: right-aligned line number + marker (`-`/`+`/` `)
- Delete lines: background `#4A221D`, foreground red
- Insert lines: background `#213A2B`, foreground green
- Context lines: normal foreground, no background
- Hunk separator: `⋮` (dimmed)

## Tool Card Integration

`render_toolcard.go` detects `diff` field in result JSON:
- Present → call `renderDiff()` instead of plain text truncate
- Absent → existing behavior unchanged
- Collapsed mode: show `+N/-M` stats after tool name

## Files Changed

| File | Change |
|------|--------|
| `internal/tools/toolkit.go` | Compute diff in edit_file/write_file |
| `internal/tui/render_diff.go` | New: diff renderer |
| `internal/tui/render_toolcard.go` | Detect diff, call renderer |
| `internal/tui/theme.go` | Add DiffAdd/DiffDelete colors |
| `go.mod` | Add `sergi/go-diff` |
