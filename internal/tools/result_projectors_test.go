package tools

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/toolresult"
)

func parseOut(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("projected output is not valid JSON: %v\n%s", err, snip(s, 200))
	}
	return m
}

func snip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func globEnvelope(n int) string {
	files := make([]string, n)
	for i := range files {
		files[i] = fmt.Sprintf("internal/pkg/module%04d/deeply/nested/path/file_%04d.go", i, i)
	}
	return mustMarshalMap(map[string]any{
		"action":             "glob",
		"pattern":            "**/*.go",
		"workspace_revision": "git:abc123:worktree:deadbeef",
		"total":              n,
		"truncated":          false,
		"files":              files,
		"next_suggestions":   []string{"narrow the glob pattern or path before reading files"},
	})
}

func mustMarshalMap(m map[string]any) string {
	b, _ := json.Marshal(m)
	return string(b)
}

func listEnvelope(n int) string {
	entries := make([]map[string]any, n)
	for i := range entries {
		entries[i] = map[string]any{
			"name":   fmt.Sprintf("file_%04d_with_a_reasonably_long_name.txt", i),
			"path":   fmt.Sprintf("some/dir/file_%04d_with_a_reasonably_long_name.txt", i),
			"is_dir": false,
			"size":   1024 + i,
		}
	}
	return mustMarshalMap(map[string]any{
		"action":              "list",
		"path":                "some/dir",
		"workspace_revision":  "git:abc123:worktree:deadbeef",
		"total":               n,
		"truncated":           false,
		"omitted_entry_count": 0,
		"omitted_protected":   0,
		"entries":             entries,
	})
}

func grepContentEnvelope(n int) string {
	matches := make([]map[string]any, n)
	for i := range matches {
		matches[i] = map[string]any{
			"file":    fmt.Sprintf("internal/pkg/file_%04d.go", i),
			"line":    i + 1,
			"content": fmt.Sprintf("func Handler%04d(ctx context.Context) error { return doWork(ctx, %d) }", i, i),
		}
	}
	return mustMarshalMap(map[string]any{
		"action":               "grep",
		"pattern":              "func Handler",
		"workspace_revision":   "git:abc123:worktree:deadbeef",
		"total":                n,
		"truncated":            false,
		"matches":              matches,
		"omitted_match_count":  0,
		"content_truncated":    false,
		"returned_match_count": n,
	})
}

func assertProjected(t *testing.T, out string, om projectionOmission, ok bool, arrayKey string, total, budget int, wantRef string) map[string]any {
	t.Helper()
	if !ok {
		t.Fatalf("projector declined an over-budget %s result", arrayKey)
	}
	if got := estimateResultTokens(out); got > budget {
		t.Fatalf("projected %s = %d tokens, over budget %d", arrayKey, got, budget)
	}
	m := parseOut(t, out)
	arr, isArr := m[arrayKey].([]any)
	if !isArr {
		t.Fatalf("projected %s missing array key %q", arrayKey, arrayKey)
	}
	kept := len(arr)
	if kept >= total {
		t.Fatalf("projection kept %d of %d records — nothing dropped", kept, total)
	}
	if om.Records != total-kept {
		t.Fatalf("omission.Records = %d, want %d", om.Records, total-kept)
	}
	// Scalar evidence preserved.
	if fmt.Sprint(m["total"]) != fmt.Sprint(total) {
		t.Fatalf("total not preserved: got %v want %d", m["total"], total)
	}
	if m["workspace_revision"] != "git:abc123:worktree:deadbeef" {
		t.Fatalf("workspace_revision not preserved: %v", m["workspace_revision"])
	}
	// Projection metadata present and points at the artifact.
	proj, isProj := m["projection"].(map[string]any)
	if !isProj {
		t.Fatalf("projected result missing projection metadata")
	}
	if proj["projected"] != true {
		t.Fatalf("projection.projected = %v", proj["projected"])
	}
	if proj["artifact_ref"] != wantRef {
		t.Fatalf("projection.artifact_ref = %v, want %q", proj["artifact_ref"], wantRef)
	}
	if m["truncated"] != true {
		t.Fatalf("projected result must be marked truncated")
	}
	return m
}

func TestProjectGlobResult(t *testing.T) {
	const total, budget = 5000, defaultProjectionTokenBudget
	pc := projectorContext{CallID: "c1", BudgetTokens: budget, ArtifactRef: "/s/tool-results/c1.txt"}
	raw := globEnvelope(total)
	out, om, ok := projectGlobResult(raw, pc)
	assertProjected(t, out, om, ok, "files", total, budget, "/s/tool-results/c1.txt")

	// Deterministic.
	out2, _, _ := projectGlobResult(raw, pc)
	if out2 != out {
		t.Fatalf("glob projection not deterministic")
	}
}

func TestProjectListFilesResult(t *testing.T) {
	const total, budget = 1000, defaultProjectionTokenBudget
	pc := projectorContext{CallID: "c2", BudgetTokens: budget, ArtifactRef: "/s/tool-results/c2.txt"}
	raw := listEnvelope(total)
	out, om, ok := projectListFilesResult(raw, pc)
	m := assertProjected(t, out, om, ok, "entries", total, budget, "/s/tool-results/c2.txt")
	// A kept entry keeps its full structure (not a sliced string).
	arr := m["entries"].([]any)
	first := arr[0].(map[string]any)
	if first["name"] == nil || first["path"] == nil {
		t.Fatalf("kept entry lost structure: %+v", first)
	}
}

func TestProjectGrepContentResult(t *testing.T) {
	const total, budget = 3000, defaultProjectionTokenBudget
	pc := projectorContext{CallID: "c3", BudgetTokens: budget, ArtifactRef: "/s/tool-results/c3.txt"}
	raw := grepContentEnvelope(total)
	out, om, ok := projectGrepResult(raw, pc)
	m := assertProjected(t, out, om, ok, "matches", total, budget, "/s/tool-results/c3.txt")
	// Kept matches retain file/line/content structure.
	arr := m["matches"].([]any)
	first := arr[0].(map[string]any)
	if first["file"] == nil || first["line"] == nil || first["content"] == nil {
		t.Fatalf("kept match lost structure: %+v", first)
	}
	// returned/omitted counters stay consistent with what remains.
	proj := m["projection"].(map[string]any)
	if fmt.Sprint(proj["returned_match_count"]) != fmt.Sprint(len(arr)) {
		t.Fatalf("returned_match_count %v != kept %d", proj["returned_match_count"], len(arr))
	}
	if fmt.Sprint(proj["omitted_match_count"]) != fmt.Sprint(total-len(arr)) {
		t.Fatalf("omitted_match_count %v != %d", proj["omitted_match_count"], total-len(arr))
	}
}

func TestProjectGrepFilesWithMatchesMode(t *testing.T) {
	const total, budget = 4000, defaultProjectionTokenBudget
	files := make([]string, total)
	for i := range files {
		files[i] = fmt.Sprintf("internal/pkg/module%04d/file_%04d.go", i, i)
	}
	raw := mustMarshalMap(map[string]any{
		"action":             "grep",
		"pattern":            "TODO",
		"workspace_revision": "git:abc123:worktree:deadbeef",
		"total":              total,
		"truncated":          false,
		"files":              files,
	})
	pc := projectorContext{CallID: "c4", BudgetTokens: budget, ArtifactRef: "/s/c4.txt"}
	out, om, ok := projectGrepResult(raw, pc)
	assertProjected(t, out, om, ok, "files", total, budget, "/s/c4.txt")
}

func TestProjectors_MalformedFailOpen(t *testing.T) {
	pc := projectorContext{CallID: "c", BudgetTokens: defaultProjectionTokenBudget, ArtifactRef: "/s/c.txt"}
	cases := []struct {
		name string
		fn   toolProjector
		raw  string
	}{
		{"glob-not-json", projectGlobResult, "not json at all"},
		{"glob-missing-array", projectGlobResult, `{"action":"glob","total":3}`},
		{"list-wrong-type", projectListFilesResult, `{"entries":"oops"}`},
		{"grep-no-known-array", projectGrepResult, `{"action":"grep","total":1,"blah":[1,2]}`},
	}
	for _, c := range cases {
		if _, _, ok := c.fn(c.raw, pc); ok {
			t.Errorf("%s: expected fail-open (ok=false)", c.name)
		}
	}
}

func TestProjectListFiles_EndToEndFinalize(t *testing.T) {
	dir := t.TempDir()
	raw := toolresult.FromText(listEnvelope(5000))
	got, d := finalizeBuiltInToolResult(dir, "list_files", "c-e2e", raw, 0)
	if !d.Applied || d.Reason != reasonProjected {
		t.Fatalf("finalize diag = %+v", d)
	}
	if d.ProjectedTokens > defaultProjectionTokenBudget {
		t.Fatalf("finalized projection over budget: %d", d.ProjectedTokens)
	}
	if !strings.Contains(got.TextProjection(), d.ArtifactRef) {
		t.Fatalf("projected text should reference the artifact %q", d.ArtifactRef)
	}
	// The artifact holds the full raw result for recovery.
	if d.ArtifactRef == "" || !d.ArtifactWritten {
		t.Fatalf("expected a written artifact: %+v", d)
	}
}
