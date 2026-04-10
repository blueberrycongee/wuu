package coordinator

import "testing"

func TestLookupWorkerType_OnlyWorker(t *testing.T) {
	// Only one built-in type ships now. Specialized roles
	// (verification, research) are injected via prompt presets at
	// spawn time, not via separate worker types.
	wt, err := LookupWorkerType("worker")
	if err != nil {
		t.Fatalf("LookupWorkerType(worker) failed: %v", err)
	}
	if wt.Name != "worker" {
		t.Errorf("got name %q, want worker", wt.Name)
	}
	if wt.SystemPrompt == "" {
		t.Error("worker has empty SystemPrompt")
	}
}

func TestLookupWorkerType_DefaultsToWorker(t *testing.T) {
	wt, err := LookupWorkerType("")
	if err != nil {
		t.Fatal(err)
	}
	if wt.Name != "worker" {
		t.Fatalf("expected default = worker, got %q", wt.Name)
	}
}

func TestLookupWorkerType_RemovedTypesRejected(t *testing.T) {
	// The old explorer/planner/verifier types no longer exist —
	// their job is now done by prompt presets pasted into the
	// generic worker prompt. Trying to look them up must error so
	// any caller still asking for them gets a clear failure
	// instead of silently falling back to default behavior.
	for _, name := range []string{"explorer", "planner", "verifier"} {
		if _, err := LookupWorkerType(name); err == nil {
			t.Errorf("LookupWorkerType(%q) should error after type collapse", name)
		}
	}
}

func TestLookupWorkerType_Unknown(t *testing.T) {
	_, err := LookupWorkerType("nope")
	if err == nil {
		t.Fatal("expected error for unknown type")
	}
}

func TestFilterToolsForWorker_Worker(t *testing.T) {
	wt, _ := LookupWorkerType("worker")
	full := []string{
		"read_file", "write_file", "edit_file", "run_shell",
		"grep", "glob", "spawn_agent", "list_agents",
	}
	filtered := FilterToolsForWorker(wt, full)
	allowed := map[string]bool{}
	for _, n := range filtered {
		allowed[n] = true
	}
	// Worker has nil AllowedTools → all non-orchestration tools allowed.
	for _, expected := range []string{"read_file", "write_file", "edit_file", "run_shell", "grep", "glob"} {
		if !allowed[expected] {
			t.Errorf("worker missing %s", expected)
		}
	}
	// Orchestration tools always blocked (no recursive spawning).
	for _, blocked := range []string{"spawn_agent", "list_agents"} {
		if allowed[blocked] {
			t.Errorf("worker should not have %s (orchestration tool)", blocked)
		}
	}
}

func TestWorkerType_DefaultIsolation(t *testing.T) {
	// The single shipped type defaults to inplace — workers share
	// the parent repo unless the caller explicitly opts into a
	// worktree. See the IsolationInplace doc comment for the
	// rationale.
	wt, err := LookupWorkerType("worker")
	if err != nil {
		t.Fatal(err)
	}
	if wt.DefaultIsolation != IsolationInplace {
		t.Errorf("worker: want default isolation %q, got %q",
			IsolationInplace, wt.DefaultIsolation)
	}
}

func TestNormalizeIsolation(t *testing.T) {
	worker, _ := LookupWorkerType("worker")

	cases := []struct {
		name    string
		raw     string
		wt      WorkerType
		want    IsolationMode
		wantErr bool
	}{
		{"empty falls back to type default", "", worker, IsolationInplace, false},
		{"explicit inplace", "inplace", worker, IsolationInplace, false},
		{"explicit worktree", "worktree", worker, IsolationWorktree, false},
		{"case insensitive", "InPlace", worker, IsolationInplace, false},
		{"empty type with empty default falls back to inplace", "", WorkerType{}, IsolationInplace, false},
		{"unknown rejected", "yolo", worker, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NormalizeIsolation(tc.raw, tc.wt)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("want %q, got %q", tc.want, got)
			}
		})
	}
}
