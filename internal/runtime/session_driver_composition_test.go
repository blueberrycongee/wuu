package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
)

// A conversation must bind before inference, keep that identity after rebuilding
// its runtime or forking, and fail before effects if durable admission is lost.
func TestSessionDriverComposition(t *testing.T) {
	for _, name := range []string{"changed-default", "fork", "missing-session", "incompatible-checkpoint"} {
		t.Run(name, func(t *testing.T) {
			s, _ := workerCompositionSession(t, false)
			s.RootDir, s.StateDir, s.SessionDir = t.TempDir(), t.TempDir(), t.TempDir()
			model := &defaultBindingModel{}
			s.StreamRunner = &agent.StreamRunner{Client: model, Model: "controlled"}
			id := "conversation"
			if name != "missing-session" {
				if _, err := s.Sessions.Create(id); err != nil {
					t.Fatal(err)
				}
			}
			if name == "incompatible-checkpoint" {
				if err := s.Sessions.SaveDriverCheckpoint(id, session.DriverCheckpointRecord{ContractVersion: 1, DriverID: "other-driver", DriverVersion: "1.0.0", State: []byte(`{}`)}); err != nil {
					t.Fatal(err)
				}
			}
			run := func() error {
				thread, err := s.NewThreadRuntime(id)
				if err != nil {
					return err
				}
				defer s.releasePluginGeneration(thread.PluginGeneration)
				_, err = thread.StreamRunner.RunWithCallback(context.Background(), []providers.ChatMessage{{Role: "user", Content: "Say hello"}}, nil)
				return err
			}
			err := run()
			if name == "missing-session" || name == "incompatible-checkpoint" {
				if err == nil || model.calls != 0 {
					t.Fatalf("durability failure admitted inference: calls=%d err=%v", model.calls, err)
				}
				return
			}
			if err != nil || model.calls != 1 {
				t.Fatalf("initial turn: calls=%d err=%v", model.calls, err)
			}
			if name == "fork" {
				if _, err := s.Sessions.CreateForkWithMetadata("fork", s.RootDir, session.ForkMetadata{ForkedFromID: id}); err != nil {
					t.Fatal(err)
				}
				id = "fork"
			}
			s.DriverProfile = "unavailable"
			if err := run(); err != nil || model.calls != 2 {
				t.Fatalf("saved conversation changed its driver: calls=%d err=%v", model.calls, err)
			}
		})
	}
}

func TestSessionDriverCheckpointRejectsMissingOwner(t *testing.T) {
	store := sessionDriverCheckpointStore{sessions: testSessionStore(t, t.TempDir()), sessionID: "missing"}
	err := store.Save(context.Background(), loopdriver.Checkpoint{ContractVersion: 1, DriverID: loopdriver.DefaultDriverID, DriverVersion: loopdriver.DefaultDriverVersion, State: []byte(`{}`)})
	if !errors.Is(err, session.ErrSessionNotFound) || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("checkpoint acknowledged a nonexistent owner: %v", err)
	}
}
