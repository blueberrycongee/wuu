package agent

import (
	"strings"
	"testing"

	wuucontext "github.com/blueberrycongee/wuu/internal/context"
	"github.com/blueberrycongee/wuu/internal/providers"
)

func TestAssembleModelRequestRequiresExplicitRequestOnlyPolicy(t *testing.T) {
	history := []providers.ChatMessage{{Role: "user", Content: "hi"}}
	implicit := ContextSegment{
		Messages: []providers.ChatMessage{{Role: "user", Content: "implicit context"}},
	}

	assembly := assembleModelRequest(history, []ContextSegment{implicit})
	if len(assembly.Messages) != 1 || len(assembly.RequestOnlyMessages) != 0 || len(assembly.Segments) != 0 {
		t.Fatalf("zero-value segment policy should not enter provider request: %+v", assembly)
	}

	explicit := RequestOnlyContextSegment([]providers.ChatMessage{{Role: "user", Content: "explicit context"}})
	assembly = assembleModelRequest(history, []ContextSegment{explicit})
	if len(assembly.Messages) != 2 || len(assembly.RequestOnlyMessages) != 1 || len(assembly.Segments) != 1 {
		t.Fatalf("explicit request-only segment should enter provider request: %+v", assembly)
	}
	if !assembly.Messages[1].Hidden || assembly.Messages[1].Content != "explicit context" {
		t.Fatalf("explicit request-only projection not normalized: %+v", assembly.Messages[1])
	}
}

func TestRequestOnlyContextProjectionSwitch(t *testing.T) {
	block := wuucontext.Block{
		Kind: wuucontext.BlockTaskState, Title: "Task", Source: "runtime", Content: "state: active", TokenBudget: 200,
	}
	t.Setenv(wuucontext.DynamicContextProjectionEnvVar, "")
	compact := requestOnlyMessagesFromBlocks([]wuucontext.Block{block})
	if len(compact) != 1 || !strings.Contains(compact[0].Content, "rule: Latest update for this key wins.") {
		t.Fatalf("expected compact default projection: %+v", compact)
	}
	for _, omitted := range []string{"title: Task", "source: runtime", "token_budget: 200"} {
		if strings.Contains(compact[0].Content, omitted) {
			t.Fatalf("compact projection should omit %q:\n%s", omitted, compact[0].Content)
		}
	}

	t.Setenv(wuucontext.DynamicContextProjectionEnvVar, "off")
	legacy := requestOnlyMessagesFromBlocks([]wuucontext.Block{block})
	if len(legacy) != 1 || !strings.Contains(legacy[0].Content, "title: Task") ||
		!strings.Contains(legacy[0].Content, "Only the latest context update with this key applies") {
		t.Fatalf("off should restore legacy projection: %+v", legacy)
	}
}

func TestRequestBlockMetricsMatchProjectionSwitch(t *testing.T) {
	block := wuucontext.Block{
		Kind: wuucontext.BlockTaskState, Title: "Task", Source: "runtime", Content: "state: active", TokenBudget: 200,
	}
	measure := func() int {
		t.Helper()
		assembly := assembleModelRequest(nil, RequestOnlyContextBlocks([]wuucontext.Block{block}))
		_, counts, bytesByKind := requestBlockMetrics(assembly)
		kind := string(wuucontext.BlockTaskState)
		if counts[kind] != 1 {
			t.Fatalf("request block count = %d, want 1: %+v", counts[kind], counts)
		}
		return bytesByKind[kind]
	}

	t.Setenv(wuucontext.DynamicContextProjectionEnvVar, "")
	compactBytes := measure()
	wantCompact := len([]byte(strings.TrimSpace(wuucontext.CompileRequestBlocks([]wuucontext.Block{block}))))
	if compactBytes != wantCompact {
		t.Fatalf("compact block bytes = %d, want projected bytes %d", compactBytes, wantCompact)
	}

	t.Setenv(wuucontext.DynamicContextProjectionEnvVar, "off")
	legacyBytes := measure()
	wantLegacy := len([]byte(strings.TrimSpace(wuucontext.CompileBlocks([]wuucontext.Block{block}))))
	if legacyBytes != wantLegacy {
		t.Fatalf("legacy block bytes = %d, want projected bytes %d", legacyBytes, wantLegacy)
	}
	if compactBytes >= legacyBytes {
		t.Fatalf("compact block bytes should be smaller: compact=%d legacy=%d", compactBytes, legacyBytes)
	}
}

// TestReconciledRetainedContextDropsDerivedLedgers locks the issue-128
// migration behavior: derived-ledger updates retained by an earlier turn must
// not re-enter the request stream once they leave the default projection.
// The A/B baseline ("on") keeps the legacy stream intact.
func TestReconciledRetainedContextDropsDerivedLedgers(t *testing.T) {
	ledger := wuucontext.Block{Kind: wuucontext.BlockActiveFiles, Title: "Active files", Source: "read_file", Content: "files:\n- go.mod"}
	kept := wuucontext.Block{Kind: wuucontext.BlockTestFailures, Title: "Test failures", Source: "bash", Content: "go test failed"}
	ledgerMsg := requestOnlyMessagesFromBlocks([]wuucontext.Block{ledger})[0]
	keptMsg := requestOnlyMessagesFromBlocks([]wuucontext.Block{kept})[0]
	retained := []RetainedContextMessage{
		{AfterDurable: 0, Message: ledgerMsg},
		{AfterDurable: 1, Message: keptMsg},
	}
	current := RequestOnlyContextBlocks([]wuucontext.Block{kept})

	t.Setenv(wuucontext.DerivedContextLedgersEnvVar, "")
	got := reconciledRetainedContext(retained, current)
	if len(got) != 1 || got[0].Message.Name != keptMsg.Name {
		t.Fatalf("derived ledger must be dropped from retained context: %+v", got)
	}

	t.Setenv(wuucontext.DerivedContextLedgersEnvVar, "on")
	got = reconciledRetainedContext(retained, current)
	if len(got) != 2 {
		t.Fatalf("A/B baseline keeps the retained ledger stream: %+v", got)
	}
}
