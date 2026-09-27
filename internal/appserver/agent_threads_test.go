package appserver

import (
	"errors"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/participant"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/subagent"
)

func TestAgentFromSnapshotResolvesParticipantSummary(t *testing.T) {
	sessDir := t.TempDir()
	p := participant.Participant{
		ID:     "prt-1234567890abcdef",
		Kind:   participant.KindEphemeral,
		Name:   "Researcher·auth-audit",
		Role:   "researcher",
		Avatar: "🔎",
	}
	if err := session.UpsertParticipant(sessDir, p); err != nil {
		t.Fatalf("UpsertParticipant: %v", err)
	}

	s := New(&runtime.Session{SessionDir: sessDir}, nil)
	snap := subagent.SubAgentSnapshot{
		ID:            "agent-1",
		ParticipantID: p.ID,
		Type:          "researcher",
		TaskName:      "auth-audit",
		Status:        subagent.StatusRunning,
		StartedAt:     time.Now().UTC(),
	}

	agent := s.agentFromSnapshot(nil, snap)
	if agent.Participant == nil {
		t.Fatal("expected participant summary, got nil")
	}
	want := p.Summary()
	if *agent.Participant != want {
		t.Fatalf("participant summary = %+v, want %+v", *agent.Participant, want)
	}

	// Overwrite the store row so a store lookup would now return different
	// data; the second resolve returning the original summary proves the
	// in-memory cache was hit rather than the store re-queried.
	mutated := p
	mutated.Name = "Mutated·should-not-appear"
	mutated.Avatar = "🤖"
	if err := session.UpsertParticipant(sessDir, mutated); err != nil {
		t.Fatalf("UpsertParticipant (mutate): %v", err)
	}
	again := s.agentFromSnapshot(nil, snap)
	if again.Participant == nil || *again.Participant != want {
		t.Fatalf("cached participant summary = %+v, want %+v", again.Participant, want)
	}
}

func TestAgentFromSnapshotParticipantFallback(t *testing.T) {
	s := New(&runtime.Session{SessionDir: t.TempDir()}, nil)

	// ParticipantID set but no matching store row.
	snap := subagent.SubAgentSnapshot{
		ID:            "agent-2",
		ParticipantID: "prt-missing",
		Type:          "worker",
		TaskName:      "fix-tests",
		Status:        subagent.StatusRunning,
	}
	agent := s.agentFromSnapshot(nil, snap)
	if agent.Participant == nil {
		t.Fatal("expected fallback participant summary, got nil")
	}
	if agent.Participant.Name == "" {
		t.Fatal("fallback participant name must not be empty")
	}
	if agent.Participant.ID != "prt-missing" {
		t.Fatalf("fallback participant id = %q, want %q", agent.Participant.ID, "prt-missing")
	}
	if agent.Participant.Kind != string(participant.KindEphemeral) {
		t.Fatalf("fallback participant kind = %q, want ephemeral", agent.Participant.Kind)
	}

	// Legacy snapshot without ParticipantID also falls back.
	legacy := subagent.SubAgentSnapshot{
		ID:     "agent-3",
		Type:   "verifier",
		Status: subagent.StatusCompleted,
	}
	agent = s.agentFromSnapshot(nil, legacy)
	if agent.Participant == nil || agent.Participant.Name == "" {
		t.Fatalf("legacy snapshot fallback participant = %+v, want non-nil with name", agent.Participant)
	}
}

func TestAgentThreadModelInheritsRootUnlessSnapshotIsPinned(t *testing.T) {
	s := New(&runtime.Session{ProviderName: "workspace-provider", Model: "workspace-model", RootDir: t.TempDir()}, nil)
	now := time.Now().UTC()
	root := newThreadState("root", nil, "kimi", "k3", s.rt.RootDir, false, now)
	s.threads[root.ID] = root

	inherited, _ := s.ensureAgentThreadState(root.ID, nil, subagent.SubAgentSnapshot{ID: "agent-inherited"}, now)
	if inherited.ModelProvider != "kimi" || inherited.Model != "k3" {
		t.Fatalf("inherited model = %s/%s, want kimi/k3", inherited.ModelProvider, inherited.Model)
	}

	pinned, _ := s.ensureAgentThreadState(root.ID, nil, subagent.SubAgentSnapshot{
		ID:       "agent-pinned",
		Model:    "sonnet-4",
		ModelPin: "anthropic:sonnet-4",
	}, now)
	if pinned.ModelProvider != "anthropic" || pinned.Model != "sonnet-4" {
		t.Fatalf("pinned model = %s/%s, want anthropic/sonnet-4", pinned.ModelProvider, pinned.Model)
	}
}

func TestTurnStatusForInterruptedSubAgentSnapshot(t *testing.T) {
	reason := errors.New("interrupted: previous process exited")
	status, err := turnStatusForSubAgentSnapshot(subagent.SubAgentSnapshot{
		Status: subagent.StatusInterrupted,
		Error:  reason,
	})
	if status != TurnStatusInterrupted {
		t.Fatalf("status = %q, want %q", status, TurnStatusInterrupted)
	}
	if !errors.Is(err, reason) {
		t.Fatalf("error = %v, want %v", err, reason)
	}
}
