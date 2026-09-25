package tools

import (
	"encoding/json"
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

func TestFinalizeBashLosslessDeduplication(t *testing.T) {
	for _, tc := range []struct {
		name, stdout, stderr string
		fields               map[string]any
	}{
		{"stdout", "ok\n", "", nil},
		{"stderr", "", "warning\n", nil},
		{"empty", "", "", nil},
		{"warning", "build succeeded\n", "warning: deprecated\n", map[string]any{"exit_code": 0}},
		{"failure", "test started\n", "FAIL: assertion\n", map[string]any{"exit_code": 1}},
		{"timeout", "running\n", "", map[string]any{"exit_code": -1, "timed_out": true, "promoted_process_id": "proc-test"}},
		{"denied", "", "permission denied\n", map[string]any{"exit_code": 1, "sandbox": map[string]any{"denied": true, "mode": "workspace"}}},
		{"whitespace", "  界🙂\r\n\r\n\tline  \n", "\nerror\t ", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fields := map[string]any{
				"output": tc.stdout + tc.stderr, "stdout_tail": tc.stdout, "stderr_tail": tc.stderr,
				"stdout_tail_truncated": false, "stderr_tail_truncated": false, "truncated": false,
				"stdout_bytes": len(tc.stdout), "stderr_bytes": len(tc.stderr),
				"verification":     map[string]any{"passed": false, "summary": "preserve verification"},
				"next_suggestions": []string{"inspect evidence"},
				"extension":        map[string]any{"integer": json.Number("9007199254740993"), "decimal": json.Number("1.234567890123456789"), "null": nil},
			}
			for key, value := range tc.fields {
				fields[key] = value
			}
			raw := toolresult.FromText(bashEnvelope(fields))
			raw.IsError = tc.name == "failure" || tc.name == "denied"
			before := raw.Clone()
			got, d := finalizeBuiltInToolResult("", "bash", "dedup", raw, 0)
			if !d.Applied || d.Reason != reasonDeduplicated || got.ModelText == nil {
				t.Fatalf("lossless candidate not settled: %+v", d)
			}
			original, _ := parseToolEnvelope(raw.TextProjection())
			restored, _ := parseToolEnvelope(got.TextProjection())
			if _, exists := restored["output"]; exists {
				t.Fatal("duplicate output retained")
			}
			restored["output"] = restored["stdout_tail"].(string) + restored["stderr_tail"].(string)
			if !reflect.DeepEqual(restored, original) {
				t.Fatalf("inverse transform changed evidence: got %#v want %#v", restored, original)
			}
			producer := got.Clone()
			producer.ModelText = nil
			if !reflect.DeepEqual(raw, before) || !reflect.DeepEqual(producer, before) {
				t.Fatal("producer payload changed")
			}
			if d.OmittedBytes != 0 || d.OmittedLines != 0 || d.OmittedRecords != 0 || d.ArtifactRef != "" || d.ArtifactWritten || d.ArtifactReused || d.ArtifactFailed {
				t.Fatalf("deduplication reported missing evidence: %+v", d)
			}
			if d.OriginalBytes != len(raw.TextProjection()) || d.ProjectedBytes != len(got.TextProjection()) || d.ProjectedTokens != estimateResultTokens(got.TextProjection()) || d.ProjectionHash != projectionHash(got.TextProjection()) || d.OriginalHash != projectionHash(raw.TextProjection()) {
				t.Fatalf("incorrect size/hash diagnostics: %+v", d)
			}
			for _, input := range []toolresult.Result{got, toolresult.FromText(got.TextProjection())} {
				again, _ := finalizeBuiltInToolResult("", "bash", "dedup", input, 0)
				if !reflect.DeepEqual(again, input) {
					t.Fatal("deduplication is not idempotent")
				}
			}
			t.Logf("model bytes %d -> %d; estimated tokens %d -> %d", d.OriginalBytes, d.ProjectedBytes, d.OriginalTokens, d.ProjectedTokens)
		})
	}
}

func TestFinalizeBashDeduplicationRejectsIncompleteEvidence(t *testing.T) {
	base := map[string]any{"output": "", "stdout_tail": "", "stderr_tail": "", "stdout_tail_truncated": false, "stderr_tail_truncated": false}
	for key := range base {
		for _, mutation := range []string{"missing", "null", "wrong_type", "different"} {
			t.Run(key+"/"+mutation, func(t *testing.T) {
				m := cloneShallow(base)
				switch mutation {
				case "missing":
					delete(m, key)
				case "null":
					m[key] = nil
				case "wrong_type":
					m[key] = 0
				case "different":
					if strings.HasSuffix(key, "truncated") {
						m[key] = true
					} else {
						m[key] = "unique evidence\n"
					}
				}
				raw := toolresult.FromText(mustMarshalMap(m))
				got, d := finalizeBuiltInToolResult("", "bash", "invalid", raw, 0)
				if !reflect.DeepEqual(got, raw) || d.Applied {
					t.Fatalf("incomplete evidence rewritten: %+v", d)
				}
			})
		}
	}
	for _, text := range []string{mustMarshalMap(base) + "\nwarning", mustMarshalMap(base) + "\n{}", "null", "[]"} {
		raw := toolresult.FromText(text)
		got, d := finalizeBuiltInToolResult("", "bash", "invalid-json", raw, 0)
		if !reflect.DeepEqual(got, raw) || d.Applied {
			t.Fatalf("non-envelope text rewritten: %q", text)
		}
	}
}

func TestFinalizeBashDeduplicationBudgetBoundary(t *testing.T) {
	m := map[string]any{
		"output": strings.Repeat("line\n", 1000), "stdout_tail": strings.Repeat("line\n", 1000), "stderr_tail": "",
		"stdout_tail_truncated": false, "stderr_tail_truncated": false,
	}
	raw := toolresult.FromText(mustMarshalMap(m))
	delete(m, "output")
	candidate, _ := marshalEnvelope(m)
	budget := estimateResultTokens(candidate)
	got, d := finalizeBuiltInToolResult("", "bash", "boundary", raw, budget)
	if d.Reason != reasonDeduplicated || got.TextProjection() != candidate || estimateResultTokens(raw.TextProjection()) <= budget {
		t.Fatalf("candidate at budget boundary not settled: %+v", d)
	}
	// With no writable artifact, an oversized candidate must not be settled.
	got, d = finalizeBuiltInToolResult("", "bash", "boundary", raw, budget-1)
	if !reflect.DeepEqual(got, raw) || !d.ArtifactFailed || d.Applied {
		t.Fatalf("oversized candidate bypassed recovery: %+v", d)
	}
	got, d = finalizeBuiltInToolResult(t.TempDir(), "bash", "boundary", raw, budget-1)
	if !d.Applied || d.Reason != reasonProjected || !d.ArtifactWritten || estimateResultTokens(got.TextProjection()) > budget-1 {
		t.Fatalf("oversized candidate bypassed existing projection: %+v", d)
	}
}

func TestProjectBash_DropsRedundantOutput(t *testing.T) {
	const budget = defaultProjectionTokenBudget
	bigOutput := strings.Repeat("noise line of build output\n", 3000) // ~80KB, redundant with tails
	raw := bashEnvelope(map[string]any{
		"output":      bigOutput,
		"stdout_tail": "last few stdout lines\nok\n",
		"stderr_tail": "",
	})
	pc := projectorContext{CallID: "c1", BudgetTokens: budget, ArtifactRef: "/s/tool-results/shell-logs/x.log"}
	out, om, ok := projectBashResult(raw, pc)
	if !ok {
		t.Fatalf("bash projector declined")
	}
	if got := estimateResultTokens(out); got > budget {
		t.Fatalf("projected bash = %d tokens, over budget %d", got, budget)
	}
	if om.Bytes != len(bigOutput) {
		t.Fatalf("dropped-output bytes = %d, want %d", om.Bytes, len(bigOutput))
	}
	m := parseOut(t, out)
	if _, present := m["output"]; present {
		t.Fatalf("redundant output field must be dropped")
	}
	// Failure/facts evidence preserved.
	for _, key := range []string{"exit_code", "duration_ms", "timed_out", "workspace_revision", "full_log_ref"} {
		if _, ok := m[key]; !ok {
			t.Fatalf("bash projection dropped required field %q", key)
		}
	}
	if !strings.Contains(m["stdout_tail"].(string), "ok") {
		t.Fatalf("small stdout tail should be preserved")
	}
}

func TestProjectBash_TrimsStdoutButKeepsStderrAndVerification(t *testing.T) {
	const budget = defaultProjectionTokenBudget
	hugeStdout := strings.Repeat("stdout progress line\n", 4000)
	stderr := "FAIL: TestThing at handlers_test.go:42\nexpected 3 got 4\n"
	raw := bashEnvelope(map[string]any{
		"exit_code":   1,
		"output":      hugeStdout,
		"stdout_tail": hugeStdout,
		"stderr_tail": stderr,
		"verification": map[string]any{
			"passed":        false,
			"failing_tests": []string{"TestThing"},
			"summary":       "1 test failed at handlers_test.go:42",
		},
	})
	pc := projectorContext{CallID: "c2", BudgetTokens: budget, ArtifactRef: "/s/tool-results/shell-logs/x.log"}
	out, _, ok := projectBashResult(raw, pc)
	if !ok {
		t.Fatalf("bash projector declined")
	}
	if got := estimateResultTokens(out); got > budget {
		t.Fatalf("projected bash = %d tokens, over budget %d", got, budget)
	}
	m := parseOut(t, out)
	// stderr (higher priority) fully preserved.
	if m["stderr_tail"].(string) != stderr {
		t.Fatalf("stderr tail must be preserved intact, got %q", m["stderr_tail"])
	}
	// stdout trimmed to fit.
	if lineCount(m["stdout_tail"].(string)) >= lineCount(hugeStdout) {
		t.Fatalf("stdout tail should have been trimmed")
	}
	// verification evidence kept.
	if _, ok := m["verification"]; !ok {
		t.Fatalf("verification evidence must never be dropped")
	}
}

func TestProjectBash_EndToEndReusesFullLogRef(t *testing.T) {
	dir := t.TempDir()
	bigOutput := strings.Repeat("x", 300000) // force over-budget
	raw := toolresult.FromText(bashEnvelope(map[string]any{
		"output":      bigOutput,
		"stdout_tail": strings.Repeat("tail line\n", 2000),
		"stderr_tail": "",
	}))
	got, d := finalizeBuiltInToolResult(dir, "bash", "c-e2e", raw, 0)
	if !d.Applied || d.Reason != reasonProjected {
		t.Fatalf("bash finalize diag = %+v", d)
	}
	if !d.ArtifactReused || d.ArtifactWritten {
		t.Fatalf("bash must reuse full_log_ref, not persist a duplicate: %+v", d)
	}
	if d.ArtifactRef != "/s/tool-results/shell-logs/x.log" {
		t.Fatalf("artifact ref = %q, want the embedded full_log_ref", d.ArtifactRef)
	}
	if !strings.Contains(got.TextProjection(), "/s/tool-results/shell-logs/x.log") {
		t.Fatalf("projected bash should reference the full log")
	}
}

func TestProjectBash_OverBudgetDeclines(t *testing.T) {
	raw := bashEnvelope(map[string]any{
		"output":      "ok\n",
		"stdout_tail": "ok\n",
		"stderr_tail": "",
		"verification": map[string]any{
			"passed":  false,
			"summary": strings.Repeat("failing assertion detail ", 4000),
		},
	})
	_, _, ok := projectBashResult(raw, projectorContext{BudgetTokens: defaultProjectionTokenBudget, ArtifactRef: "/s/tool-results/shell-logs/x.log"})
	if ok {
		t.Fatal("bash projection must decline when verification evidence still exceeds the budget")
	}
	got, d := finalizeBuiltInToolResult(t.TempDir(), "bash", "over", toolresult.FromText(raw), 0)
	if d.Applied {
		t.Fatalf("over-budget bash must fail open: %+v", d)
	}
	if got.TextProjection() != raw {
		t.Fatal("declined over-budget bash must keep the original envelope")
	}
}

func TestProjectReadFileOversizedLineDoesNotOfferNonAdvancingContinuation(t *testing.T) {
	raw := mustMarshalMap(map[string]any{"path": "long.txt", "content": strings.Repeat("x", 20000) + "\n", "start_line": 1, "total_lines": 1})
	_, _, ok := projectReadFileResult(raw, projectorContext{BudgetTokens: defaultProjectionTokenBudget, ArtifactRef: "/s/long.txt"})
	if ok {
		t.Fatal("a line that cannot fit must use the existing generic artifact recovery instead of a non-advancing line cursor")
	}
}
