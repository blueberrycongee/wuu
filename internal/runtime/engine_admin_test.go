package runtime

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/blueberrycongee/wuu/internal/agentengine"
	"github.com/blueberrycongee/wuu/internal/codexengine"
	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
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

type canonicalHistoryFactory struct{ agentengine.Factory }

func (canonicalHistoryFactory) Descriptor(context.Context) (agentengine.Descriptor, error) {
	return agentengine.Descriptor{ID: "replacement", Capabilities: []string{"canonical-history"}}, nil
}
