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

func TestRenderBashModelView(t *testing.T) {
	for _, tc := range []struct {
		name   string
		fields map[string]any
		want   []string
		absent []string
	}{
		{
			name:   "success",
			fields: map[string]any{"stdout_tail": "ok\n", "stderr_tail": "", "duration_ms": 83},
			want:   []string{"exit 0 · 83ms\nok"},
			absent: []string{"classification", "full_log", "note:", "go test ./..."},
		},
		{
			name:   "empty",
			fields: map[string]any{"stdout_tail": "", "stderr_tail": ""},
			want:   []string{"(no output)"},
		},
		{
			name:   "failure",
			fields: map[string]any{"exit_code": 1, "stdout_tail": "started\n", "stderr_tail": "FAIL: assertion\n", "next_suggestions": []string{"inspect the output"}},
			want:   []string{"exit 1", "started\n--- stderr ---\nFAIL: assertion", "note: inspect the output"},
		},
		{
			name:   "timeout promoted",
			fields: map[string]any{"exit_code": -1, "timed_out": true, "promoted_process_id": "proc-9", "stdout_tail": "running\n", "stderr_tail": "", "duration_ms": 300000},
			want:   []string{"timed out after 5m00s · still running as background process proc-9", "running"},
			absent: []string{"exit -1"},
		},
		{
			name:   "sandbox denied",
			fields: map[string]any{"exit_code": 1, "stdout_tail": "", "stderr_tail": "permission denied\n", "sandbox": map[string]any{"mode": "workspace", "enforcement": "full", "denied": true}},
			want:   []string{"sandbox: a file write outside the current workspace boundary was denied"},
		},
		{
			name: "verification failed",
			fields: map[string]any{"exit_code": 1, "stdout_tail": "--- FAIL: TestThing\n", "stderr_tail": "", "verification": map[string]any{
				"kind": "verification", "scope": "targeted", "passed": false,
				"failure_summary": map[string]any{"failed": true, "failing_tests": []string{"TestThing"}},
				"repeat_guard":    map[string]any{"previous_failed_runs": 2, "max_failed_runs_without_revision_change": 3},
			}},
			want: []string{"verification: failed (scope targeted)", "- TestThing", "repeat guard: 2 of 3 failed runs"},
		},
		{
			name:   "verification passed",
			fields: map[string]any{"stdout_tail": "ok\n", "stderr_tail": "", "verification": map[string]any{"kind": "verification", "scope": "full", "passed": true, "failure_summary": map[string]any{"failed": false}}},
			want:   []string{"verification: passed (scope full)"},
		},
		{
			name:   "truncated streams point at the full log",
			fields: map[string]any{"stdout_tail": "head\n... 500 bytes omitted ...\ntail\n", "stderr_tail": "", "stdout_tail_truncated": true, "stdout_bytes": 600},
			want:   []string{"[stdout: ", " of 600 bytes shown; full log: /s/tool-results/shell-logs/x.log]"},
		},
		{
			name:   "rewritten verification command",
			fields: map[string]any{"stdout_tail": "ok\n", "stderr_tail": "", "requested_command": "npx vitest", "resolved_command": "./node_modules/.bin/vitest"},
			want:   []string{"ran: ./node_modules/.bin/vitest"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			view, om, ok := renderBashModelView(bashEnvelope(tc.fields), bashProjectionTokenBudget)
			if !ok || om.Lines != 0 {
				t.Fatalf("view not rendered: ok=%v om=%+v", ok, om)
			}
			if strings.HasPrefix(view, "{") {
				t.Fatalf("model view is still JSON: %s", view)
			}
			for _, want := range tc.want {
				if !strings.Contains(view, want) {
					t.Fatalf("view missing %q:\n%s", want, view)
				}
			}
			for _, absent := range tc.absent {
				if strings.Contains(view, absent) {
					t.Fatalf("view leaked %q:\n%s", absent, view)
				}
			}
		})
	}
}

func TestFinalizeBashRendersViewAndKeepsProducer(t *testing.T) {
	raw := toolresult.FromText(bashEnvelope(map[string]any{"exit_code": 1, "stdout_tail": "started\n", "stderr_tail": "FAIL\n"}))
	raw.IsError = true
	before := raw.Clone()
	got, d := finalizeBuiltInToolResult("", "bash", "view", raw, 0)
	if !d.Applied || d.Reason != reasonRendered || got.ModelText == nil || d.OmittedLines != 0 {
		t.Fatalf("view not settled: %+v", d)
	}
	if !strings.HasPrefix(got.TextProjection(), "exit 1") || got.Content[0].Text != before.Content[0].Text {
		t.Fatalf("producer payload changed or view wrong: %s", got.TextProjection())
	}
	if d.ArtifactRef != "/s/tool-results/shell-logs/x.log" || !d.ArtifactReused || d.ArtifactWritten {
		t.Fatalf("view must reference the embedded full log without writing a copy: %+v", d)
	}
	if d.ProjectedBytes != len(got.TextProjection()) || d.ProjectionHash != projectionHash(got.TextProjection()) || d.OriginalHash != projectionHash(raw.TextProjection()) {
		t.Fatalf("incorrect size/hash diagnostics: %+v", d)
	}
	again, _ := finalizeBuiltInToolResult("", "bash", "view", got, 0)
	if !reflect.DeepEqual(again, got) {
		t.Fatal("settled view was rewritten")
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
	if !strings.Contains(view, stderr[:len(stderr)-1]) {
		t.Fatalf("stderr must survive intact:\n%s", view)
	}
	if !strings.Contains(view, "lines omitted; full log: /s/tool-results/shell-logs/x.log") || !strings.HasPrefix(view, "exit 1") {
		t.Fatalf("trimmed view lacks recovery marker:\n%s", view)
	}
	if !strings.Contains(view, "stdout progress line") {
		t.Fatal("trimmed view dropped all stdout instead of keeping head and tail")
	}
	// The facts alone can exceed the budget; then the view declines so generic
	// settlement archives the envelope.
	if _, _, ok := renderBashModelView(raw, 8); ok {
		t.Fatal("view must decline when even the facts exceed the budget")
	}
}

func TestRenderBashModelViewDeclinesNonRunEnvelopes(t *testing.T) {
	for _, text := range []string{
		`{"action":"start_background","id":"proc-1","status":"running"}`,
		`{"action":"list","processes":[]}`,
		`{"output":"ok"}` + "\nwarning",
		"null",
		"plain text",
	} {
		if view, _, ok := renderBashModelView(text, bashProjectionTokenBudget); ok {
			t.Fatalf("non-run envelope rendered: %q -> %q", text, view)
		}
		raw := toolresult.FromText(text)
		got, d := finalizeBuiltInToolResult("", "bash", "legacy", raw, 0)
		if d.Applied || !reflect.DeepEqual(got, raw) {
			t.Fatalf("non-run envelope rewritten: %+v", d)
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
