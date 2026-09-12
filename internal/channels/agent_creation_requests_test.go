package channels

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func TestNamedAgentCreationRequestSurvivesRestartAndDeletion(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	service, err := Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	params := CreateNamedAgentParams{RequestID: "first-agent", Name: "Analyst", Role: "Reviews designs", ProviderOverride: "byok", ModelOverride: "reasoner", EffortOverride: "high", Autostart: true}
	created, err := service.CreateNamedAgent(ctx, params)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	service, err = Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	replayed, err := service.CreateNamedAgent(ctx, params)
	if err != nil || replayed.Agent.ID != created.Agent.ID || replayed.Token != created.Token || replayed.Agent.AvatarKey != created.Agent.AvatarKey {
		t.Fatalf("replay = %#v, %v; want original identity and credentials", replayed.Agent, err)
	}
	changed := params
	changed.EffortOverride = "low"
	if _, err := service.CreateNamedAgent(ctx, changed); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed request error = %v, want conflict", err)
	}
	if err := service.DeleteNamedAgent(ctx, created.Agent.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateNamedAgent(ctx, params); !errors.Is(err, ErrConflict) {
		t.Fatalf("deleted request error = %v, want conflict", err)
	}
	agents, err := service.ListNamedAgents(ctx)
	if err != nil || len(agents) != 0 {
		t.Fatalf("deleted identity was recreated: %#v, %v", agents, err)
	}
}

func TestNamedAgentCreationRequestSerializesConcurrentConnections(t *testing.T) {
	dir := t.TempDir()
	first, err := Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	params := CreateNamedAgentParams{RequestID: "concurrent-create", Name: "Analyst"}
	var results [8]AgentCredential
	var errs [8]error
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results[i], errs[i] = []*Service{first, second}[i%2].CreateNamedAgent(context.Background(), params)
		}()
	}
	close(start)
	wg.Wait()
	for i, result := range results {
		if errs[i] != nil || result.Agent.ID != results[0].Agent.ID || result.Token != results[0].Token {
			t.Fatalf("concurrent result %d = %#v, %v", i, result.Agent, errs[i])
		}
	}
	agents, err := first.ListNamedAgents(context.Background())
	if err != nil || len(agents) != 1 {
		t.Fatalf("identities = %#v, %v, want one", agents, err)
	}
}

func TestNamedAgentCreationFailureDoesNotConsumeRequest(t *testing.T) {
	service := openTestService(t, nil)
	ctx := context.Background()
	if _, err := service.CreateNamedAgent(ctx, CreateNamedAgentParams{Name: "Taken"}); err != nil {
		t.Fatal(err)
	}
	params := CreateNamedAgentParams{RequestID: "retry-after-error", Name: "Taken"}
	if _, err := service.CreateNamedAgent(ctx, params); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate name error = %v, want conflict", err)
	}
	// A failed transaction must not pin this request to the rejected payload.
	params.Name = "Available"
	if _, err := service.CreateNamedAgent(ctx, params); err != nil {
		t.Fatalf("retry after rejected creation: %v", err)
	}
}
