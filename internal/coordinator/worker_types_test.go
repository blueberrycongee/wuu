package coordinator

import "testing"

func TestLookupWorkerType_Builtins(t *testing.T) {
	for _, name := range []string{"explorer", "planner", "worker", "verifier"} {
		wt, err := LookupWorkerType(name)
		if err != nil {
			t.Errorf("LookupWorkerType(%q) failed: %v", name, err)
			continue
		}
		if wt.Name != name {
			t.Errorf("got name %q, want %q", wt.Name, name)
		}
		if wt.SystemPrompt == "" {
			t.Errorf("%s: empty SystemPrompt", name)
		}
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

func TestLookupWorkerType_Unknown(t *testing.T) {
	_, err := LookupWorkerType("nope")
	if err == nil {
		t.Fatal("expected error for unknown type")
	}
}

func TestFilterToolsForWorker_Explorer(t *testing.T) {
	wt, _ := LookupWorkerType("explorer")
	full := []string{
		"read_file", "write_file", "edit_file", "run_shell",
		"grep", "glob", "list_files", "spawn_agent", "load_skill",
	}
	filtered := FilterToolsForWorker(wt, full)
	allowed := map[string]bool{}
	for _, n := range filtered {
		allowed[n] = true
	}
	// Explorer should NOT have write/edit/run_shell/spawn_agent.
	for _, blocked := range []string{"write_file", "edit_file", "run_shell", "spawn_agent"} {
		if allowed[blocked] {
			t.Errorf("explorer should not have %s", blocked)
		}
	}
	// Explorer SHOULD have read_file, grep, glob, list_files, load_skill.
	for _, expected := range []string{"read_file", "grep", "glob", "list_files", "load_skill"} {
		if !allowed[expected] {
			t.Errorf("explorer missing expected tool %s", expected)
		}
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
	// Orchestration tools always blocked.
	for _, blocked := range []string{"spawn_agent", "list_agents"} {
		if allowed[blocked] {
			t.Errorf("worker should not have %s (orchestration tool)", blocked)
		}
	}
}

func TestWorkerType_DefaultIsolation(t *testing.T) {
	// All built-in types now default to inplace — workers share the
	// parent repo unless the caller explicitly opts into a worktree.
	// See the IsolationInplace doc comment for the rationale.
	cases := map[string]IsolationMode{
		"explorer": IsolationInplace,
		"planner":  IsolationInplace,
		"verifier": IsolationInplace,
		"worker":   IsolationInplace,
	}
	for name, want := range cases {
		wt, err := LookupWorkerType(name)
		if err != nil {
			t.Fatalf("LookupWorkerType(%q): %v", name, err)
		}
		if wt.DefaultIsolation != want {
			t.Errorf("%s: want default isolation %q, got %q", name, want, wt.DefaultIsolation)
		}
	}
}

func TestNormalizeIsolation(t *testing.T) {
	worker, _ := LookupWorkerType("worker")
	explorer, _ := LookupWorkerType("explorer")

	cases := []struct {
		name    string
		raw     string
		wt      WorkerType
		want    IsolationMode
		wantErr bool
	}{
		{"empty falls back to type default (worker)", "", worker, IsolationInplace, false},
		{"empty falls back to type default (explorer)", "", explorer, IsolationInplace, false},
		{"explicit inplace", "inplace", worker, IsolationInplace, false},
		{"explicit worktree", "worktree", explorer, IsolationWorktree, false},
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

func TestFilterToolsForWorker_Verifier(t *testing.T) {
	wt, _ := LookupWorkerType("verifier")
	full := []string{
		"read_file", "write_file", "edit_file", "run_shell",
		"grep", "glob", "list_files", "load_skill",
	}
	filtered := FilterToolsForWorker(wt, full)
	allowed := map[string]bool{}
	for _, n := range filtered {
		allowed[n] = true
	}
	// Verifier needs run_shell (for tests/build) but no write/edit.
	if !allowed["run_shell"] {
		t.Error("verifier should have run_shell")
	}
	for _, blocked := range []string{"write_file", "edit_file"} {
		if allowed[blocked] {
			t.Errorf("verifier should not have %s", blocked)
		}
	}
}
