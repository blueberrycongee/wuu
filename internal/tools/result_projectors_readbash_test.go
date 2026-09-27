package tools

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/toolresult"
)

func readFileEnvelope(numLines int) (rawText string, firstLine, lastLine string) {
	var content strings.Builder
	for i := 1; i <= numLines; i++ {
		line := fmt.Sprintf("|func handler%04d(ctx context.Context) error { return process(ctx, %d) }", i, i)
		if i == 1 || i%10 == 0 {
			line = fmt.Sprint(i) + line
		}
		content.WriteString(line)
		content.WriteByte('\n')
		if i == 1 {
			firstLine = line
		}
		if i == numLines {
			lastLine = line
		}
	}
	raw := mustMarshalMap(map[string]any{
		"action":             "read",
		"path":               "internal/pkg/handlers.go",
		"workspace_revision": "git:abc123:worktree:deadbeef",
		"content":            content.String(),
		"num_lines":          numLines,
		"start_line":         1,
		"total_lines":        numLines,
		"truncated":          false,
	})
	return raw, firstLine, lastLine
}

func TestProjectReadFileResult(t *testing.T) {
	const numLines, budget = 5000, defaultProjectionTokenBudget
	raw, firstLine, _ := readFileEnvelope(numLines)
	pc := projectorContext{CallID: "c1", BudgetTokens: budget, ArtifactRef: "/s/tool-results/c1.txt"}

	out, om, ok := projectReadFileResult(raw, pc)
	if !ok {
		t.Fatalf("read_file projector declined")
	}
	if got := estimateResultTokens(out); got > budget {
		t.Fatalf("projected read_file = %d tokens, over budget %d", got, budget)
	}
	if om.Lines <= 0 {
		t.Fatalf("expected omitted lines, got %d", om.Lines)
	}
	m := parseOut(t, out)
	content, _ := m["content"].(string)
	if !strings.HasPrefix(content, firstLine+"\n") {
		t.Fatalf("first line not preserved: %q", content)
	}
	original := parseOut(t, raw)["content"].(string)
	if !strings.HasPrefix(original, content) || lineCount(content) != numLines-om.Lines {
		t.Fatalf("projected content is not a continuous prefix of whole lines")
	}
	if int(m["num_lines"].(float64)) != lineCount(content) {
		t.Fatalf("displayed line count does not match content")
	}
	// File facts preserved.
	if fmt.Sprint(m["total_lines"]) != fmt.Sprint(numLines) || m["path"] != "internal/pkg/handlers.go" {
		t.Fatalf("read_file metadata not preserved: %v", m)
	}
	proj := m["projection"].(map[string]any)
	if fmt.Sprint(proj["omitted_lines"]) != fmt.Sprint(om.Lines) {
		t.Fatalf("omitted_lines mismatch: %v vs %d", proj["omitted_lines"], om.Lines)
	}
	// Deterministic.
	out2, _, _ := projectReadFileResult(raw, pc)
	if out2 != out {
		t.Fatalf("read_file projection not deterministic")
	}
}

func TestProjectReadFile_MalformedFailOpen(t *testing.T) {
	pc := projectorContext{CallID: "c", BudgetTokens: defaultProjectionTokenBudget, ArtifactRef: "/s/c.txt"}
	if _, _, ok := projectReadFileResult(`{"action":"read","content":42}`, pc); ok {
		t.Fatalf("non-string content must fail open")
	}
	if _, _, ok := projectReadFileResult("not json", pc); ok {
		t.Fatalf("invalid json must fail open")
	}
}

func bashEnvelope(m map[string]any) string {
	base := map[string]any{
		"action":             "run",
		"command":            "go test ./...",
		"classification":     map[string]any{"category": "test"},
		"exit_code":          0,
		"duration_ms":        1234,
		"timed_out":          false,
		"truncated":          true,
		"workspace_revision": "git:abc123:worktree:deadbeef",
		"stdout_bytes":       100000,
		"stderr_bytes":       0,
		"full_log_ref":       "/s/tool-results/shell-logs/x.log",
		"full_log_bytes":     100000,
	}
	for k, v := range m {
		base[k] = v
	}
	return mustMarshalMap(base)
}

// The view is the model-facing contract of a command result: the output a
// terminal would show plus the facts that change the next step. These tests
// pin those facts, not the sentences around them.
func TestRenderBashModelView(t *testing.T) {
	for _, tc := range []struct {
		name   string
		fields map[string]any
		exact  string
		want   []string
		absent []string
	}{
		{
			name:   "success is just the output",
			fields: map[string]any{"stdout_tail": "\nok\n", "stderr_tail": ""},
			exact:  "ok",
		},
		{
			name:   "empty success",
			fields: map[string]any{"stdout_tail": "", "stderr_tail": ""},
			exact:  "(no output)",
		},
		{
			name:   "failure keeps both streams and the exit code",
			fields: map[string]any{"exit_code": 1, "stdout_tail": "started\n", "stderr_tail": "FAIL: assertion\n", "next_suggestions": []string{"generic advice"}},
			exact:  "started\nFAIL: assertion\nExit code 1",
		},
		{
			name:   "timeout names the background process",
			fields: map[string]any{"exit_code": -1, "timed_out": true, "promoted_process_id": "proc-9", "stdout_tail": "running\n", "stderr_tail": "", "duration_ms": 300000},
			want:   []string{"running\n", "proc-9", "5m00s"},
			absent: []string{"Exit code"},
		},
		{
			name:   "sandbox denial is reported",
			fields: map[string]any{"exit_code": 1, "stdout_tail": "", "stderr_tail": "permission denied\n", "sandbox": map[string]any{"mode": "workspace", "enforcement": "full", "denied": true}},
			want:   []string{"permission denied\nExit code 1\n", "sandbox"},
		},
		{
			name:   "truncated stream names the full log",
			fields: map[string]any{"stdout_tail": "head\n... 500 bytes omitted ...\ntail\n", "stderr_tail": "", "stdout_tail_truncated": true, "stdout_bytes": 600},
			want:   []string{"head\n... 500 bytes omitted ...\ntail\n", "/s/tool-results/shell-logs/x.log"},
		},
		{
			name:   "rewritten verification command",
			fields: map[string]any{"stdout_tail": "ok\n", "stderr_tail": "", "requested_command": "npx vitest", "resolved_command": "./node_modules/.bin/vitest"},
			want:   []string{"ok\n", "./node_modules/.bin/vitest"},
		},
		{
			name: "passing verification adds nothing",
			fields: map[string]any{"stdout_tail": "ok\n", "stderr_tail": "", "verification": map[string]any{
				"kind": "verification", "scope": "full", "passed": true, "failure_summary": map[string]any{"failed": false},
			}},
			exact: "ok",
		},
		{
			name: "complete failing verification does not repeat its summary",
			fields: map[string]any{"exit_code": 1, "stdout_tail": "--- FAIL: TestThing\n", "stderr_tail": "", "verification": map[string]any{
				"kind": "verification", "scope": "targeted", "passed": false,
				"failure_summary": map[string]any{"failed": true, "failing_tests": []string{"TestThing"}},
				"repeat_guard":    map[string]any{"previous_failed_runs": 0, "max_failed_runs_without_revision_change": 2},
			}},
			exact: "--- FAIL: TestThing\nExit code 1",
		},
		{
			name: "cut failing verification keeps its summary and the repeat guard",
			fields: map[string]any{"exit_code": 1, "stdout_tail": "... 900 bytes omitted ...\nok\n", "stdout_tail_truncated": true, "stderr_tail": "", "verification": map[string]any{
				"kind": "verification", "scope": "targeted", "passed": false,
				"failure_summary": map[string]any{"failed": true, "failing_tests": []string{"TestThingHidden"}},
				"repeat_guard":    map[string]any{"previous_failed_runs": 1, "max_failed_runs_without_revision_change": 2},
			}},
			want: []string{"Exit code 1", "TestThingHidden", "2 times"},
		},
		{
			name:   "terminal control codes are stripped",
			fields: map[string]any{"stdout_tail": "\x1b[32mPASS\x1b[0m\r\nbuilding 10%\rbuilding 100%\n", "stderr_tail": ""},
			exact:  "PASS\nbuilding 100%",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			view, om, ok := renderBashModelView(bashEnvelope(tc.fields), commandProjectionTokenBudget)
			if !ok || om.Lines != 0 {
				t.Fatalf("view not rendered: ok=%v om=%+v", ok, om)
			}
			if tc.exact != "" && view != tc.exact {
				t.Fatalf("view mismatch\n got: %q\nwant: %q", view, tc.exact)
			}
			for _, want := range tc.want {
				if !strings.Contains(view, want) {
					t.Fatalf("view missing %q:\n%s", want, view)
				}
			}
			for _, absent := range append(tc.absent, "classification", "workspace_revision", `"exit_code"`) {
				if strings.Contains(view, absent) {
					t.Fatalf("view leaked %q:\n%s", absent, view)
				}
			}
		})
	}
}

func TestRenderBashModelViewBackgroundStart(t *testing.T) {
	raw := mustMarshalMap(map[string]any{"action": "start", "id": "proc-7", "status": "running", "command": "npm run dev", "tty": true})
	view, _, ok := renderBashModelView(raw, commandProjectionTokenBudget)
	if !ok || !strings.Contains(view, "proc-7") || strings.Contains(view, "{") {
		t.Fatalf("background start view = %q ok=%v", view, ok)
	}
}

func TestRenderProcessModelView(t *testing.T) {
	running := map[string]any{"id": "proc-1", "status": "running", "command": "npm run dev"}
	exited := map[string]any{"id": "proc-2", "status": "stopped", "command": "npm test", "exit_code": 1, "terminal_cause": "natural_exit", "completion_mode": "detached"}
	for _, tc := range []struct {
		name   string
		raw    map[string]any
		want   []string
		absent []string
	}{
		{
			name: "live read shows its output and where to continue",
			raw: map[string]any{"action": "read", "process_id": "proc-1", "output": "\x1b[1mready\x1b[0m\r\n", "start_offset": 0, "end_offset": 18, "total_bytes": 18,
				"status": "running", "process": running, "next_suggestions": []string{"do not chain waits"}},
			want:   []string{"proc-1", "running", "\nready\n", "offset_bytes=18", "do not chain waits"},
			absent: []string{"\x1b", "\r"},
		},
		{
			name:   "complete read of an exited process",
			raw:    map[string]any{"action": "read", "process_id": "proc-2", "output": "", "start_offset": 0, "end_offset": 0, "total_bytes": 0, "process": exited},
			want:   []string{"proc-2", "code 1"},
			absent: []string{"offset_bytes"},
		},
		{
			name: "list has one line per process",
			raw:  map[string]any{"action": "list", "processes": []any{running, exited}},
			want: []string{"proc-1 running · npm run dev\n", "proc-2", "code 1", "npm test", "detached"},
		},
		{
			name: "write",
			raw:  map[string]any{"action": "write", "process_id": "proc-1", "bytes_written": 6, "process": running},
			want: []string{"6 bytes", "proc-1"},
		},
		{
			name: "stop",
			raw:  map[string]any{"action": "stop", "id": "proc-1", "status": "stopped", "terminal_cause": "requested_stop"},
			want: []string{"proc-1", "stopped"},
		},
		{
			name: "update",
			raw:  map[string]any{"action": "update", "process_id": "proc-1", "completion_mode": "detached", "recheck_minutes": 10},
			want: []string{"proc-1", "10 min"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			view, _, ok := renderProcessModelView(mustMarshalMap(tc.raw), commandProjectionTokenBudget)
			if !ok {
				t.Fatal("view not rendered")
			}
			for _, want := range tc.want {
				if !strings.Contains(view, want) {
					t.Fatalf("view missing %q:\n%s", want, view)
				}
			}
			for _, absent := range append(tc.absent, "{", `"process_id"`) {
				if strings.Contains(view, absent) {
					t.Fatalf("view leaked %q:\n%s", absent, view)
				}
			}
		})
	}
}

func TestFinalizeCommandViewsKeepProducerPayload(t *testing.T) {
	for _, tc := range []struct{ tool, text, prefix string }{
		{"bash", bashEnvelope(map[string]any{"exit_code": 1, "stdout_tail": "started\n", "stderr_tail": "FAIL\n"}), "started\nFAIL\nExit code 1"},
		{"process", mustMarshalMap(map[string]any{"action": "stop", "id": "proc-1", "status": "stopped", "terminal_cause": "requested_stop"}), "Process proc-1"},
	} {
		raw := toolresult.FromText(tc.text)
		before := raw.Clone()
		got, d := finalizeBuiltInToolResult("", tc.tool, "view", raw, 0)
		if !d.Applied || d.Reason != reasonRendered || got.ModelText == nil || d.OmittedLines != 0 {
			t.Fatalf("%s view not settled: %+v", tc.tool, d)
		}
		if !strings.HasPrefix(got.TextProjection(), tc.prefix) || got.Content[0].Text != before.Content[0].Text {
			t.Fatalf("%s producer payload changed or view wrong: %q", tc.tool, got.TextProjection())
		}
		if d.ProjectedBytes != len(got.TextProjection()) || d.ProjectionHash != projectionHash(got.TextProjection()) || d.OriginalHash != projectionHash(raw.TextProjection()) {
			t.Fatalf("%s incorrect size/hash diagnostics: %+v", tc.tool, d)
		}
		again, _ := finalizeBuiltInToolResult("", tc.tool, "view", got, 0)
		if !reflect.DeepEqual(again, got) {
			t.Fatalf("settled %s view was rewritten", tc.tool)
		}
	}
}

func TestRenderBashModelViewTrimsStdoutBeforeStderr(t *testing.T) {
	stdout := strings.Repeat("stdout progress line\n", 4000)
	stderr := "FAIL: TestThing at handlers_test.go:42\nexpected 3 got 4\n"
	raw := bashEnvelope(map[string]any{"exit_code": 1, "stdout_tail": stdout, "stderr_tail": stderr})
	view, om, ok := renderBashModelView(raw, defaultProjectionTokenBudget)
	if !ok || om.Lines == 0 {
		t.Fatalf("over-budget view not trimmed: ok=%v om=%+v", ok, om)
	}
	if got := estimateResultTokens(view); got > defaultProjectionTokenBudget {
		t.Fatalf("view = %d tokens, over budget %d", got, defaultProjectionTokenBudget)
	}
	if !strings.Contains(view, stderr[:len(stderr)-1]) || !strings.HasSuffix(view, "Exit code 1") {
		t.Fatalf("stderr and exit code must survive intact:\n%s", view)
	}
	if !strings.Contains(view, "lines omitted; full log: /s/tool-results/shell-logs/x.log") || !strings.HasPrefix(view, "stdout progress line") {
		t.Fatalf("trimmed view lacks the head or the recovery marker:\n%s", view)
	}
	// The status lines alone can exceed the budget; then the view declines so
	// generic settlement archives the envelope.
	if _, _, ok := renderBashModelView(raw, 2); ok {
		t.Fatal("view must decline when even the status lines exceed the budget")
	}
}

func TestRenderCommandViewsDeclineUnknownEnvelopes(t *testing.T) {
	for _, text := range []string{
		`{"action":"start_background","id":"proc-1","status":"running"}`,
		`{"action":"start"}`,
		`{"output":"ok"}` + "\nwarning",
		"null",
		"plain text",
	} {
		if view, _, ok := renderBashModelView(text, commandProjectionTokenBudget); ok {
			t.Fatalf("unknown bash envelope rendered: %q -> %q", text, view)
		}
		raw := toolresult.FromText(text)
		got, d := finalizeBuiltInToolResult("", "bash", "legacy", raw, 0)
		if d.Applied || !reflect.DeepEqual(got, raw) {
			t.Fatalf("unknown bash envelope rewritten: %+v", d)
		}
	}
	for _, text := range []string{`{"action":"start","id":"proc-1"}`, `{"action":"read"`, "[]"} {
		if view, _, ok := renderProcessModelView(text, commandProjectionTokenBudget); ok {
			t.Fatalf("unknown process envelope rendered: %q -> %q", text, view)
		}
	}
}

func TestProjectReadFileOversizedLineDoesNotOfferNonAdvancingContinuation(t *testing.T) {
	raw := mustMarshalMap(map[string]any{"path": "long.txt", "content": strings.Repeat("x", 20000) + "\n", "start_line": 1, "total_lines": 1})
	_, _, ok := projectReadFileResult(raw, projectorContext{BudgetTokens: defaultProjectionTokenBudget, ArtifactRef: "/s/long.txt"})
	if ok {
		t.Fatal("a line that cannot fit must use the existing generic artifact recovery instead of a non-advancing line cursor")
	}
}
