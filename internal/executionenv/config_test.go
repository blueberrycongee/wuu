package executionenv

import (
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
