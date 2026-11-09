package sessiontrace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadEventsPreservesPayloadBytes(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "session-trace.jsonl")
	if err := AppendTurn(path,
		TurnRecord{ThreadID: "thread-1", TurnID: "turn-1", Status: "completed", InputTokens: 9007199254740993, OutputTokens: 2},
		FinalRecord{Status: "completed"},
		nil, nil, nil, nil, nil,
	); err != nil {
		t.Fatal(err)
	}

	events, err := ReadEvents(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 {
		t.Fatal("ReadEvents returned no events")
	}
	types := map[string]bool{}
	for _, event := range events {
		types[event.Type] = true
	}
	if !types["turn"] || !types["final"] {
		t.Fatalf("event types = %v, want turn and final", types)
	}
}

func TestReadEventsMissingFile(t *testing.T) {
	t.Parallel()

	_, err := ReadEvents(filepath.Join(t.TempDir(), "missing.jsonl"))
	if err == nil || !os.IsNotExist(err) {
		t.Fatalf("ReadEvents error = %v, want not-exist", err)
	}
}

func TestTurnEventsReplayWithoutLocalStorage(t *testing.T) {
	events, err := TurnEvents(TurnRecord{ThreadID: "thread", TurnID: "turn", Status: "completed", InputTokens: 17}, FinalRecord{Status: "completed", FinalAnswerPreview: "access_token=private-value"}, nil, nil, []RequestContextRecord{{StepIndex: 1, InputTokens: 17}}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	summary, err := ReplayEvents(events)
	if err != nil || !summary.Complete || len(summary.Turns) != 1 || len(summary.ContextRequests) != 1 || summary.ContextRequests[0].InputTokens != 17 {
		t.Fatalf("pure replay lost facts: %+v %v", summary, err)
	}
	if summary.Path != "" || strings.Contains(summary.Final.FinalAnswerPreview, "private-value") {
		t.Fatal("pure event batch exposed a local path or unredacted credential")
	}
	if _, err := ReplayEvents(nil); err == nil {
		t.Fatal("empty events reported a completed replay")
	}
}
