package config

import "testing"

// The project admission policy must inherit the anonymous limit only when
// omitted, preserve explicit limits, and reject negative input.
func TestProjectMaxParallelConfiguration(t *testing.T) {
	for _, tc := range []struct{ worker, project, want int }{{0, 0, DefaultAgentMaxParallel}, {7, 0, 7}, {7, 2, 2}} {
		cfg := Default()
		cfg.Agent.MaxParallel, cfg.Agent.ProjectMaxParallel = tc.worker, tc.project
		if got := cfg.Agent.ProjectMaxParallelValue(); got != tc.want {
			t.Fatalf("project limit = %d, want %d", got, tc.want)
		}
	}
	cfg := Default()
	cfg.Agent.ProjectMaxParallel = -1
	if err := cfg.Validate(); err == nil {
		t.Fatal("negative project limit accepted")
	}
}
