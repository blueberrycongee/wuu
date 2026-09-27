package tools

import (
	"os"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/toolresult"
)

func overBudgetText() string {
	// ~200_000 ASCII chars ≈ 50_000 estimated tokens, well over the 2,048 budget.
	return strings.Repeat("word ", 40000)
}

func TestFinalize_EligibilityExactNameOnly(t *testing.T) {
	big := toolresult.FromText(overBudgetText())
	cases := []struct {
		name     string
		eligible bool
	}{
		{"read_file", true},
		{"list_files", true},
		{"bash", true},
		{"grep", true},
		{"glob", true},
		// Exact-match discipline: MCP, mutation, coordination, and case/space
		// variants must never be eligible.
		{"mcp_server_bash", false},
		{"mcp_x_grep", false},
		{"apply_patch", false},
		{"edit_file", false},
		{"write_file", false},
		{"", false},
		{"load_skill", false},
		{"BASH", false},
		{" grep", false},
		{"grep ", false},
	}
	for _, c := range cases {
		_, d := finalizeBuiltInToolResult("", c.name, "call-1", big, 0)
		if d.Eligible != c.eligible {
			t.Errorf("tool %q eligible = %v, want %v (reason %q)", c.name, d.Eligible, c.eligible, d.Reason)
		}
		if !c.eligible && d.Applied {
			t.Errorf("tool %q must not be projected", c.name)
		}
	}
}

func TestFinalize_UnderBudgetIsStableIdentity(t *testing.T) {
	small := toolresult.FromText("short list result")
	got, d := finalizeBuiltInToolResult("", "list_files", "c1", small, 0)
	if !d.Eligible || d.Reason != reasonUnderBudget || d.Applied {
		t.Fatalf("under-budget diag = %+v", d)
	}
	if got.TextProjection() != "short list result" {
		t.Fatalf("under-budget result must be unchanged, got %q", got.TextProjection())
	}
	if d.OriginalTokens != d.ProjectedTokens || d.ProjectionHash != d.OriginalHash {
		t.Fatalf("identity must not change tokens/hash: %+v", d)
	}
}

func TestFinalize_OverBudgetNoProjectorFailsOpen(t *testing.T) {
	// Remove list_files's real projector to exercise the no-projector branch.
	withoutProjector(t, "list_files")
	big := toolresult.FromText(overBudgetText())
	got, d := finalizeBuiltInToolResult("", "list_files", "c1", big, 0)
	if !d.Eligible || d.Reason != reasonNoProjector || d.Applied {
		t.Fatalf("no-projector diag = %+v", d)
	}
	if got.TextProjection() != big.TextProjection() {
		t.Fatalf("missing projector must preserve the full result")
	}
}

// withoutProjector removes a registered projector for the test and restores it.
func withoutProjector(t *testing.T, tool string) {
	t.Helper()
	prev, had := toolProjectors[tool]
	delete(toolProjectors, tool)
	t.Cleanup(func() {
		if had {
			toolProjectors[tool] = prev
		}
	})
}

func TestFinalize_NonTextOnlyNotEligible(t *testing.T) {
	rich := toolresult.Result{Content: []toolresult.ContentPart{
		{Type: toolresult.ContentTypeText, Text: "caption"},
		{Type: toolresult.ContentTypeImage, Data: "aW1n", MIMEType: "image/png"},
	}}
	_, d := finalizeBuiltInToolResult("", "read_file", "c1", rich, 0)
	if d.Eligible {
		t.Fatalf("multi-part (non-text-only) result must not be eligible: %+v", d)
	}
}

func TestEnsureProjectionArtifact(t *testing.T) {
	// Reuse an existing embedded reference; never write a duplicate.
	ref, reused, ok := ensureProjectionArtifact("/should/be/ignored", "c1", "raw", "existing/full.log")
	if !ok || !reused || ref != "existing/full.log" {
		t.Fatalf("reuse: ref=%q reused=%v ok=%v", ref, reused, ok)
	}

	// No session dir and no embedded ref: cannot guarantee recovery -> fail.
	if _, _, ok := ensureProjectionArtifact("", "c1", "raw", ""); ok {
		t.Fatalf("empty sessionDir with no ref must fail (ok=false)")
	}

	// Persist to the session artifact dir.
	dir := t.TempDir()
	ref, reused, ok = ensureProjectionArtifact(dir, "c1", "raw-content", "")
	if !ok || reused || ref == "" {
		t.Fatalf("persist: ref=%q reused=%v ok=%v", ref, reused, ok)
	}
	data, err := os.ReadFile(ref)
	if err != nil {
		t.Fatalf("read persisted artifact: %v", err)
	}
	if string(data) != "raw-content" {
		t.Fatalf("persisted artifact content = %q", string(data))
	}
}

// withFakeProjector registers a projector for tool and restores prior state.
func withFakeProjector(t *testing.T, tool string, p toolProjector) {
	t.Helper()
	prev, had := toolProjectors[tool]
	toolProjectors[tool] = p
	t.Cleanup(func() {
		if had {
			toolProjectors[tool] = prev
		} else {
			delete(toolProjectors, tool)
		}
	})
}

func TestFinalize_ProjectedPathAppliesAndIsDeterministic(t *testing.T) {
	withFakeProjector(t, "list_files", func(raw string, pc projectorContext) (string, projectionOmission, bool) {
		return "PROJECTED ref=" + pc.ArtifactRef, projectionOmission{Records: 3, Lines: 7, Bytes: 42}, true
	})
	dir := t.TempDir()
	big := toolresult.FromText(overBudgetText())
	big.IsError = true // must be preserved through projection

	got, d := finalizeBuiltInToolResult(dir, "list_files", "c1", big, 0)
	if !d.Applied || d.Reason != reasonProjected {
		t.Fatalf("projected diag = %+v", d)
	}
	if !strings.HasPrefix(got.TextProjection(), "PROJECTED ref=") {
		t.Fatalf("projected text = %q", got.TextProjection())
	}
	if !got.IsError {
		t.Fatalf("projection must preserve IsError")
	}
	if !d.ArtifactWritten || d.ArtifactReused || d.ArtifactRef == "" {
		t.Fatalf("artifact diag = %+v", d)
	}
	if d.OmittedRecords != 3 || d.OmittedLines != 7 || d.OmittedBytes != 42 {
		t.Fatalf("omission diag = %+v", d)
	}
	if d.ProjectionHash == d.OriginalHash || d.ProjectedTokens >= d.OriginalTokens {
		t.Fatalf("projection must change hash and reduce tokens: %+v", d)
	}

	got2, d2 := finalizeBuiltInToolResult(dir, "list_files", "c1", big, 0)
	if got2.TextProjection() != got.TextProjection() || d2.ProjectionHash != d.ProjectionHash {
		t.Fatalf("projection must be deterministic: %q/%q", got.TextProjection(), got2.TextProjection())
	}
}

func TestFinalize_ProjectorDeclineFailsOpenButKeepsArtifact(t *testing.T) {
	withFakeProjector(t, "list_files", func(raw string, pc projectorContext) (string, projectionOmission, bool) {
		return "", projectionOmission{}, false
	})
	dir := t.TempDir()
	big := toolresult.FromText(overBudgetText())
	got, d := finalizeBuiltInToolResult(dir, "list_files", "c1", big, 0)
	if d.Applied || d.Reason != reasonFailOpen {
		t.Fatalf("decline diag = %+v", d)
	}
	if got.TextProjection() != big.TextProjection() {
		t.Fatalf("decline must preserve the full result")
	}
	if d.ArtifactRef == "" || !d.ArtifactWritten {
		t.Fatalf("artifact should still be recorded on fail-open: %+v", d)
	}
}

func TestFinalize_ArtifactUnrecoverableFailsOpen(t *testing.T) {
	withFakeProjector(t, "list_files", func(raw string, pc projectorContext) (string, projectionOmission, bool) {
		return "PROJECTED", projectionOmission{}, true
	})
	big := toolresult.FromText(overBudgetText())
	// Empty sessionDir and no embedded ref -> artifact cannot be guaranteed.
	got, d := finalizeBuiltInToolResult("", "list_files", "c1", big, 0)
	if d.Applied || d.Reason != reasonFailOpen || !d.ArtifactFailed {
		t.Fatalf("unrecoverable-artifact diag = %+v", d)
	}
	if got.TextProjection() != big.TextProjection() {
		t.Fatalf("must preserve full result when artifact is unrecoverable")
	}
}
