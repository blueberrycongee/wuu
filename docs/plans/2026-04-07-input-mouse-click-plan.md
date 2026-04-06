# Input Mouse Click Cursor Positioning — Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Allow users to click inside the input textarea to position the cursor at the clicked location, supporting multi-line input.

**Architecture:** Add a mouse click handler branch in `model.go`'s existing `tea.MouseMsg` case. When a left click lands inside the input area (determined by `layout.Input` coordinates and border offsets), compute the target row/column and move the cursor using `CursorUp()`/`CursorDown()` + `SetCursor(col)`.

**Tech Stack:** Go, Bubble Tea (`bubbletea`), `bubbles/textarea`

---

### Task 1: Write failing test for single-line click positioning

**Files:**
- Modify: `internal/tui/model_test.go`

**Step 1: Write the failing test**

```go
func TestMouseClickPositionsCursor(t *testing.T) {
	m := NewModel(Config{
		Provider:   "test",
		Model:      "test-model",
		ConfigPath: "/tmp/.wuu.json",
		RunPrompt: func(_ctx context.Context, prompt string) (string, error) {
			return prompt, nil
		},
	})
	m.width = 100
	m.height = 24
	m.relayout()

	m.input.SetValue("hello world")
	m.input.SetCursor(0) // cursor at start

	// Click at column 7 inside the input area.
	// Non-compact: border adds 1 col on left, prompt "> " adds 2 cols.
	// So to hit text column 4, click at X = 1 (border) + 2 (prompt) + 4 = 7.
	inputY := m.layout.Input.Y + 1 // +1 for top border in non-compact
	updated, _ := m.Update(tea.MouseMsg{
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
		X:      7,
		Y:      inputY,
	})
	after := updated.(Model)

	li := after.input.LineInfo()
	if li.CharOffset != 4 {
		t.Fatalf("expected cursor at column 4, got %d", li.CharOffset)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `cd /Users/blueberrycongee/wuu && go test ./internal/tui/ -run TestMouseClickPositionsCursor -v`
Expected: FAIL — the MouseMsg is not handled for input area clicks yet.

**Step 3: Commit failing test**

```bash
git add internal/tui/model_test.go
git commit -m "test: add failing test for mouse click cursor positioning"
```

---

### Task 2: Implement mouse click cursor positioning

**Files:**
- Modify: `internal/tui/model.go:322-332` (the `tea.MouseMsg` case)

**Step 1: Add the click handler**

In `model.go`, after the existing jump-to-bottom mouse handler (line ~331), add a new branch before the closing of the `tea.MouseMsg` case. The logic:

```go
// Mouse click inside input area — reposition cursor.
if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
	borderOff := 0
	if !m.layout.Compact {
		borderOff = 1
	}
	promptW := 2 // "> " prompt width

	// Check if click is inside the input area (accounting for border).
	inputTop := m.layout.Input.Y + borderOff
	inputBot := inputTop + m.layout.Input.Height
	inputLeft := m.layout.Input.X + borderOff

	if msg.Y >= inputTop && msg.Y < inputBot && msg.X >= inputLeft {
		targetRow := msg.Y - inputTop
		targetCol := msg.X - inputLeft - promptW
		if targetCol < 0 {
			targetCol = 0
		}

		// Move to target row.
		currentRow := m.input.Line()
		for currentRow < targetRow && currentRow < m.input.LineCount()-1 {
			m.input.CursorDown()
			currentRow++
		}
		for currentRow > targetRow && currentRow > 0 {
			m.input.CursorUp()
			currentRow--
		}

		// Move to target column.
		m.input.SetCursor(targetCol)
		return m, nil
	}
}
```

**Step 2: Run test to verify it passes**

Run: `cd /Users/blueberrycongee/wuu && go test ./internal/tui/ -run TestMouseClickPositionsCursor -v`
Expected: PASS

**Step 3: Run all tests to check for regressions**

Run: `cd /Users/blueberrycongee/wuu && go test ./internal/tui/ -v`
Expected: All tests PASS

**Step 4: Commit**

```bash
git add internal/tui/model.go
git commit -m "feat: mouse click to position cursor in input textarea"
```

---

### Task 3: Write test for multi-line click positioning

**Files:**
- Modify: `internal/tui/model_test.go`

**Step 1: Write the test**

```go
func TestMouseClickPositionsCursorMultiLine(t *testing.T) {
	m := NewModel(Config{
		Provider:   "test",
		Model:      "test-model",
		ConfigPath: "/tmp/.wuu.json",
		RunPrompt: func(_ctx context.Context, prompt string) (string, error) {
			return prompt, nil
		},
	})
	m.width = 100
	m.height = 24
	m.relayout()

	m.input.SetValue("first line\nsecond line")
	m.input.CursorStart() // cursor at start of first line

	// Click on second line (row 1), column 3.
	borderOff := 1 // non-compact
	promptW := 2
	inputY := m.layout.Input.Y + borderOff + 1 // +1 for second row
	clickX := m.layout.Input.X + borderOff + promptW + 3

	updated, _ := m.Update(tea.MouseMsg{
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
		X:      clickX,
		Y:      inputY,
	})
	after := updated.(Model)

	if after.input.Line() != 1 {
		t.Fatalf("expected cursor on line 1, got %d", after.input.Line())
	}
	li := after.input.LineInfo()
	if li.CharOffset != 3 {
		t.Fatalf("expected cursor at column 3, got %d", li.CharOffset)
	}
}
```

**Step 2: Run test**

Run: `cd /Users/blueberrycongee/wuu && go test ./internal/tui/ -run TestMouseClickPositionsCursorMultiLine -v`
Expected: PASS (implementation from Task 2 already handles multi-line)

**Step 3: Commit**

```bash
git add internal/tui/model_test.go
git commit -m "test: add multi-line mouse click cursor positioning test"
```

---

### Task 4: Manual smoke test

**Step 1:** Run `go build -o wuu ./cmd/wuu && ./wuu` and verify:
- Type some text, click in the middle — cursor jumps there
- Paste multi-line text, click on different lines — cursor moves correctly
- Click past end of line — cursor goes to line end
- Click on the prompt area (far left) — cursor goes to column 0
