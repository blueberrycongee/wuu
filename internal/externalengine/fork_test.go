package externalengine

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agentengine"
	"github.com/blueberrycongee/wuu/internal/enginecatalog"
	"github.com/blueberrycongee/wuu/internal/providers"
)

func forkTestUserMessage(id, text, parent string) openCodeMessage {
	return openCodeMessage{
		Info:  openCodeInfo{ID: id, Role: "user", ParentID: parent},
		Parts: []openCodePart{{Type: "text", Text: text}},
	}
}

func forkTestAssistantMessage(id, parent string) openCodeMessage {
	return openCodeMessage{
		Info:  openCodeInfo{ID: id, Role: "assistant", ParentID: parent},
		Parts: []openCodePart{{Type: "text", Text: "reply"}},
	}
}

// The wire carries a prompt digest, never the text itself; test messages use
// the same algorithm the appserver projects onto EngineHistoryMessage.
func forkTestPrompt(text string) providers.ChatMessage {
	return providers.ChatMessage{Role: "user", ContentSHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(text)))}
}

// The cutoff selection is the honest-boundary contract: only a user prompt or
// a completed turn can bound a fork, and a retained prefix that diverges from
// the native transcript must fail instead of guessing a different cutoff.
func TestOpenCodeForkCutoffBoundaries(t *testing.T) {
	native := []openCodeMessage{
		forkTestUserMessage("m_u1", "first", ""),
		forkTestAssistantMessage("m_a1", "m_u1"),
		forkTestUserMessage("m_u2", "second", ""),
		forkTestAssistantMessage("m_a2", "m_u2"),
		forkTestUserMessage("m_u3", "third", ""),
		forkTestAssistantMessage("m_a3", "m_u3"),
	}
	prompt := forkTestPrompt
	answer := func() providers.ChatMessage {
		return providers.ChatMessage{Role: "assistant", Content: "reply"}
	}
	for _, test := range []struct {
		name              string
		native            []openCodeMessage
		retained, removed []providers.ChatMessage
		turnBoundary      bool
		want, wantErr     string
	}{
		{
			name:         "turn boundary cuts at the next prompt",
			native:       native,
			retained:     []providers.ChatMessage{prompt("first"), answer(), prompt("second"), answer()},
			turnBoundary: true,
			want:         "m_u3",
		},
		{
			name:         "end boundary copies the whole session",
			native:       native,
			retained:     []providers.ChatMessage{prompt("first"), answer(), prompt("second"), answer(), prompt("third"), answer()},
			turnBoundary: true,
			want:         "",
		},
		{
			name:     "prompt boundary keeps the prompt but not its reply",
			native:   native,
			retained: []providers.ChatMessage{prompt("first"), answer(), prompt("second")},
			want:     "m_a2",
		},
		{
			name:     "prompt at the native tail copies everything",
			native:   native[:3],
			retained: []providers.ChatMessage{prompt("first"), answer(), prompt("second")},
			want:     "",
		},
		{
			name:     "prompt boundary ignores trailing meta records",
			native:   native,
			retained: []providers.ChatMessage{prompt("first"), answer(), prompt("second"), {Role: "meta"}},
			want:     "m_a2",
		},
		{
			name:     "mid-turn item cannot be a boundary",
			native:   native,
			retained: []providers.ChatMessage{prompt("first"), answer()},
			wantErr:  "final reply",
		},
		{
			name:     "edit boundary keeps the verified prefix",
			native:   native,
			retained: []providers.ChatMessage{prompt("first"), answer(), prompt("second"), answer()},
			removed:  []providers.ChatMessage{prompt("third"), answer()},
			want:     "m_u3",
		},
		{
			name:     "edit boundary requires the next native prompt",
			native:   native,
			retained: []providers.ChatMessage{prompt("first"), answer(), prompt("second"), answer()},
			removed:  []providers.ChatMessage{prompt("different"), answer()},
			wantErr:  "does not match",
		},
		{
			name:     "edit boundary past the native end has no cutoff",
			native:   native,
			retained: []providers.ChatMessage{prompt("first"), answer(), prompt("second"), answer(), prompt("third"), answer()},
			removed:  []providers.ChatMessage{prompt("another"), answer()},
			wantErr:  "does not match",
		},
		{
			name:     "diverged visible prefix fails",
			native:   native,
			retained: []providers.ChatMessage{prompt("first"), answer(), prompt("wrong"), answer()},
			wantErr:  "different turn prefixes",
		},
		{
			name:     "undigested prompt cannot be verified",
			native:   native,
			retained: []providers.ChatMessage{prompt("first"), answer(), {Role: "user", Content: "second"}},
			wantErr:  "no prompt digest",
		},
		{
			name:     "visible history longer than native fails",
			native:   native[:3],
			retained: []providers.ChatMessage{prompt("first"), answer(), prompt("second"), answer(), prompt("third")},
			wantErr:  "longer than native",
		},
		{
			name: "empty fork of an empty session",
			want: "",
		},
		{
			name:    "empty retained cannot describe native history",
			native:  native,
			wantErr: "empty visible history",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := openCodeForkCutoff(test.native, test.retained, test.removed, test.turnBoundary)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("err = %v, want %q", err, test.wantErr)
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("cutoff = %q, %v; want %q", got, err, test.want)
			}
		})
	}
}

func TestOpenCodeForkHistoryUsesNativeMessageCutoff(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	engine := New(enginecatalog.Entry{ID: "opencode", Name: "OpenCode", Protocol: "opencode", Args: []string{"-test.run=^TestOpenCodeHelper$", "--", "wuu-opencode-helper", "fork"}}, binary, t.TempDir())
	retained := []providers.ChatMessage{
		forkTestPrompt("first"), {Role: "assistant", Content: "reply one"},
		forkTestPrompt("second"), {Role: "assistant", Content: "reply two"},
	}
	ref, err := engine.ForkHistory(ctx, "ses_native", t.TempDir(), retained, nil, agentengine.HistoryForkBoundary{WholeTurn: true, TurnBoundary: true})
	if err != nil {
		t.Fatal(err)
	}
	if ref != "ses_fork" {
		t.Fatalf("ref = %q, want ses_fork", ref)
	}
}

func TestACPForkHistoryUsesAdvertisedSessionFork(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	engine := testEngineID(t, "devin", "fork")
	ref, err := engine.ForkHistory(ctx, "native-session", "", nil, nil, agentengine.HistoryForkBoundary{WholeTurn: true, TurnBoundary: true, EndBoundary: true})
	if err != nil {
		t.Fatal(err)
	}
	if ref != "native-fork" {
		t.Fatalf("ref = %q, want native-fork", ref)
	}
}

func TestACPForkHistoryRejectsPartialBoundariesBeforeSpawning(t *testing.T) {
	engine := testEngineID(t, "devin", "fork")
	for _, test := range []struct {
		name        string
		removed     []providers.ChatMessage
		endBoundary bool
	}{
		{"edit boundary", []providers.ChatMessage{{Role: "user", Content: "prompt"}}, false},
		{"mid-session fork", nil, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := engine.ForkHistory(context.Background(), "native-session", "", nil, test.removed, agentengine.HistoryForkBoundary{EndBoundary: test.endBoundary})
			if err == nil || !strings.Contains(err.Error(), "end of the session") {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestACPForkHistoryRequiresSessionReload(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	// sessionCapabilities.fork without loadSession/resume: the forked session
	// could never be resumed, so the child would lose its native context.
	engine := testEngineID(t, "devin", "fork-no-load")
	_, err := engine.ForkHistory(ctx, "native-session", "", nil, nil, agentengine.HistoryForkBoundary{WholeTurn: true, TurnBoundary: true, EndBoundary: true})
	if err == nil || !strings.Contains(err.Error(), "cannot reload a forked session") {
		t.Fatalf("err = %v", err)
	}
}

func TestACPForkHistoryFailsClosedWithoutCapability(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	// A normal agent that never advertises sessionCapabilities.fork: the fork
	// attempt must surface the unsupported boundary, not send a guessed RPC.
	engine := testEngineID(t, "devin", "stream")
	_, err := engine.ForkHistory(ctx, "native-session", "", nil, nil, agentengine.HistoryForkBoundary{WholeTurn: true, TurnBoundary: true, EndBoundary: true})
	if err == nil || !strings.Contains(err.Error(), "does not expose a verified native history cutoff") {
		t.Fatalf("err = %v", err)
	}
}
