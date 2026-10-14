package executionenv

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Profiles select an execution boundary. Invalid selections must never become local runs.
func TestSelectionFailsClosed(t *testing.T) {
	for _, cfg := range []Config{
		{Default: "missing"},
		{Default: "box", Profiles: map[string]Profile{"box": {Backend: "typo"}}},
		{Default: "box", Profiles: map[string]Profile{"box": {Backend: "ssh", Host: "-oProxyCommand=bad", Workspace: "/workspace"}}},
		{Default: "box", Profiles: map[string]Profile{"box": {Backend: "docker", Image: "-bad"}}},
	} {
		if err := cfg.Validate(); err == nil {
			t.Fatalf("accepted invalid selection: %+v", cfg)
		}
	}
}

func TestEnvironmentIdentity(t *testing.T) {
	p := Profile{Backend: "docker", Image: "example.invalid/executor:1", Workspace: "/workspace"}
	a := Identity("user-a", "thread-a", "dev", p)
	if a == Identity("user-a", "thread-b", "dev", p) {
		t.Fatal("sessions shared an isolated environment")
	}
	p.Shared = true
	if Identity("user-a", "thread-a", "dev", p) != Identity("user-a", "thread-b", "dev", p) {
		t.Fatal("explicit shared profile did not share")
	}
	if Identity("user-a", "thread-a", "dev", p) == Identity("user-b", "thread-a", "dev", p) {
		t.Fatal("different user stores shared an environment")
	}
	changed := p
	changed.Network = "none"
	if Identity("user-a", "thread-a", "dev", p) == Identity("user-a", "thread-a", "dev", changed) {
		t.Fatal("network policy change reused an environment")
	}
}

// Inspection must never provision resources, select a new default, or reveal
// adapter arguments. Lifecycle reads must also remain available during startup.
func TestSummaryDoesNotSelectOrProvision(t *testing.T) {
	store := t.TempDir()
	artifactDir := filepath.Join(store, "not-created")
	manager := NewManager()
	summary, err := manager.Summary(store, "thread", artifactDir, "/host")
	if err != nil || summary.State != "not_initialized" || summary.ID != "" || summary.Name != "" {
		t.Fatalf("unselected summary: %+v %v", summary, err)
	}
	if _, err := os.Stat(artifactDir); !os.IsNotExist(err) {
		t.Fatalf("inspection created state: %v", err)
	}
	profile := Profile{Backend: "command", Command: []string{"not-installed", "secret-argument"}, ForwardEnv: []string{"PRIVATE_KEY"}, Workspace: "/worker"}
	_, _, err = manager.Open(store, "thread", artifactDir, Config{Default: "dev", Profiles: map[string]Profile{"dev": profile}})
	if err != nil {
		t.Fatal(err)
	}
	summary, err = manager.Summary(store, "thread", artifactDir, "/host")
	if err != nil || summary.Name != "dev" || summary.Backend != "command" || summary.Root != "/worker" || summary.State != "not_initialized" || summary.ID == "" {
		t.Fatalf("pinned summary: %+v %v", summary, err)
	}
	raw, _ := json.Marshal(summary)
	if strings.Contains(string(raw), "secret") || strings.Contains(string(raw), "PRIVATE_KEY") || strings.Contains(string(raw), "not-installed") {
		t.Fatalf("profile leaked: %s", raw)
	}
	if _, err := os.Stat(filepath.Join(store, "execution-environments")); !os.IsNotExist(err) {
		t.Fatalf("inspection provisioned a worker: %v", err)
	}
	restarted := NewManager()
	restored, err := restarted.Summary(store, "thread", artifactDir, "/other-host")
	if err != nil || restored != summary {
		t.Fatalf("restored pin: %+v %v", restored, err)
	}
}
