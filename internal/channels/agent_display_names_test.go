package channels

import (
	"context"
	"errors"
	"testing"
)

func TestDuplicateAgentNamesAfterMigrationKeepIndependentIdentities(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if s != nil {
			_ = s.Close()
		}
	}()
	first := createTestAgent(t, s, "New Agent")
	// Reproduce the unique index installed by earlier versions.
	if _, err := s.db.Exec(`CREATE UNIQUE INDEX idx_named_agents_name ON named_agents(name) WHERE kind = 'named'`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	second := createTestAgent(t, s, "New Agent")
	if first.Agent.ID == second.Agent.ID || first.Agent.MemoryDir == second.Agent.MemoryDir || first.Token == second.Token {
		t.Fatal("duplicate display names shared identity state")
	}
	if _, err := s.AuthenticateAgent(ctx, first.Agent.ID, second.Token); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("cross-identity authentication: %v", err)
	}
	firstRoom, err := s.OpenDirectMessage(ctx, "human-1", first.Agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	secondRoom, err := s.OpenDirectMessage(ctx, "human-1", second.Agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if firstRoom.ID == secondRoom.ID {
		t.Fatal("duplicate display names shared a direct message")
	}
	if _, err := s.UpdateNamedAgent(ctx, UpdateNamedAgentParams{ID: second.Agent.ID, Name: "Another"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateNamedAgent(ctx, UpdateNamedAgentParams{ID: second.Agent.ID, Name: first.Agent.Name}); err != nil {
		t.Fatalf("rename to existing display name: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	agents, err := s.ListNamedAgents(ctx)
	if err != nil || len(agents) != 2 {
		t.Fatalf("reopened identities = %v, %v", agents, err)
	}
}

func TestDuplicateAgentNamesRequireUnambiguousMentions(t *testing.T) {
	s := openTestService(t, nil)
	first := createTestAgent(t, s, "Alex")
	second := createTestAgent(t, s, "Alex")
	room := createTestRoom(t, s, first, second)
	for _, test := range []struct {
		body string
		want []string
	}{
		{"@Alex please review", nil},
		{"@" + second.Agent.ID + " please review", []string{second.Agent.ID}},
		{"@all please review", []string{first.Agent.ID, second.Agent.ID}},
	} {
		result, err := s.SendHuman(context.Background(), HumanSendParams{RoomID: room.ID, HumanID: "human-1", Body: test.body})
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Message.Mentions) != len(test.want) {
			t.Fatalf("%q mentions = %v", test.body, result.Message.Mentions)
		}
		for _, id := range test.want {
			found := false
			for _, actual := range result.Message.Mentions {
				if actual == id {
					found = true
				}
			}
			if !found {
				t.Fatalf("%q missed %s", test.body, id)
			}
		}
	}
}
