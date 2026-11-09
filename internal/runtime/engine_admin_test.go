package runtime

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/blueberrycongee/wuu/internal/agentengine"
	"github.com/blueberrycongee/wuu/internal/codexengine"
	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
	"github.com/blueberrycongee/wuu/internal/providers"
)

// These are protocol safety contracts: unsupported history must fail rather
// than replay, and remote local-cli authentication must never launch a process.
func TestEngineAdminSafety(t *testing.T) {
	admin := &nativeEngineAdmin{engineID: agentengine.EngineID("replacement")}
	for _, method := range []string{"history_fork", "auth", "unknown"} {
		raw := json.RawMessage(`{"method_id":"local-cli","remote":true}`)
		if _, err := admin.Invoke(context.Background(), method, raw); err == nil {
			t.Fatalf("%s unexpectedly accepted", method)
		}
	}
	native := &nativeEngineAdmin{engineID: "codex", factory: codexengine.NewEngine(nil)}
	if _, err := native.Invoke(context.Background(), "history_fork", json.RawMessage(`{"ref":"test","removed":[]}`)); err == nil {
		t.Fatal("empty edit boundary was accepted")
	}
	disabled := &nativeEngineAdmin{engineID: "replacement", factory: canonicalHistoryFactory{}, disabled: true}
	for _, method := range []string{"models", "auth", "quota", "history_fork"} {
		if _, err := disabled.Invoke(context.Background(), method, json.RawMessage(`{}`)); err == nil {
			t.Fatalf("disabled engine accepted %s", method)
		}
	}

}

func TestEngineAdminCanonicalHistory(t *testing.T) {
	admin := &nativeEngineAdmin{engineID: agentengine.EngineWuu, factory: canonicalHistoryFactory{}}
	raw, err := admin.Invoke(context.Background(), "history_fork", json.RawMessage(`{"ref":"old","retained":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	var result pluginprotocol.EngineHistoryForkResult
	if err := json.Unmarshal(raw, &result); err != nil || result.Ref != "" {
		t.Fatalf("unexpected canonical history result: %s %v", raw, err)
	}
}

// The wire must carry identity evidence engines can verify — digests and
// boundary flags — because the JSON round trip is where a bare ChatMessage
// would silently lose the content an engine checks against native text.
func TestEngineAdminHistoryForkCarriesIdentityEvidence(t *testing.T) {
	forker := &recordingHistoryForker{}
	admin := &nativeEngineAdmin{engineID: "opencode", factory: forker}
	req, err := json.Marshal(pluginprotocol.EngineHistoryForkRequest{
		Ref: "native-session",
		Retained: []pluginprotocol.EngineHistoryMessage{
			{Role: "user", ContentSHA256: "digest-one"},
			{Role: "assistant", ProviderItemID: "item-1", EngineTurnID: "turn-1"},
		},
		WholeTurn:    true,
		TurnBoundary: true,
		EndBoundary:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := admin.Invoke(context.Background(), "history_fork", req)
	if err != nil {
		t.Fatal(err)
	}
	var result pluginprotocol.EngineHistoryForkResult
	if err := json.Unmarshal(raw, &result); err != nil || result.Ref != "forked-session" {
		t.Fatalf("unexpected fork result: %s %v", raw, err)
	}
	if len(forker.retained) != 2 || forker.retained[0].ContentSHA256 != "digest-one" || forker.retained[0].Role != "user" {
		t.Fatalf("retained lost its identity evidence: %+v", forker.retained)
	}
	if forker.retained[1].ProviderItemID != "item-1" || forker.retained[1].EngineTurnID != "turn-1" {
		t.Fatalf("native item identity lost: %+v", forker.retained[1])
	}
	if forker.retained[0].Content != "" || forker.retained[1].Content != "" {
		t.Fatalf("replayable content crossed the engine boundary: %+v", forker.retained)
	}
	if !forker.boundary.WholeTurn || !forker.boundary.TurnBoundary || !forker.boundary.EndBoundary {
		t.Fatalf("boundary flags lost: %+v", forker.boundary)
	}
}

type recordingHistoryForker struct {
	agentengine.Factory
	retained, removed []providers.ChatMessage
	boundary          agentengine.HistoryForkBoundary
}

func (f *recordingHistoryForker) Descriptor(context.Context) (agentengine.Descriptor, error) {
	return agentengine.Descriptor{ID: "recording"}, nil
}

func (f *recordingHistoryForker) ForkHistory(_ context.Context, ref, _ string, retained, removed []providers.ChatMessage, boundary agentengine.HistoryForkBoundary) (string, error) {
	f.retained, f.removed, f.boundary = retained, removed, boundary
	return "forked-session", nil
}

type canonicalHistoryFactory struct{ agentengine.Factory }

func (canonicalHistoryFactory) Descriptor(context.Context) (agentengine.Descriptor, error) {
	return agentengine.Descriptor{ID: "replacement", Capabilities: []string{"canonical-history"}}, nil
}
