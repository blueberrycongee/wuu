package agentcontrol

import (
	"testing"
)

func TestLookupWorkerType_DefaultsToGeneralPurpose(t *testing.T) {
	wt, err := LookupWorkerType("")
	if err != nil {
		t.Fatal(err)
	}
	if wt.Name != DefaultSubagentType {
		t.Fatalf("expected default = %s, got %q", DefaultSubagentType, wt.Name)
	}
}

func TestRequiresReportWorkerTypeIsInternal(t *testing.T) {
	if _, err := LookupWorkerType(requiresReportWorkerType); err != nil {
		t.Fatalf("internal lookup must keep requires-report worker available: %v", err)
	}
	if _, err := LookupPublicWorkerType(requiresReportWorkerType); err == nil {
		t.Fatal("public lookup must reject requires-report worker")
	}
	for _, name := range AvailableWorkerTypeNames() {
		if name == requiresReportWorkerType {
			t.Fatalf("public roster exposed internal requires-report worker: %v", AvailableWorkerTypeNames())
		}
	}
}

func TestLookupWorkerType_Unknown(t *testing.T) {
	_, err := LookupWorkerType("nope")
	if err == nil {
		t.Fatal("expected error for unknown type")
	}
}

func TestFilterToolsForWorker_BlocksRecursiveAgentControls(t *testing.T) {
	wt, _ := LookupWorkerType(DefaultSubagentType)
	full := []string{
		"read_file", "write_file", "edit_file", "bash",
		"grep", "glob", "spawn_agent", "send_message",
		"close_agent", "agent_report",
	}
	filtered := FilterToolsForWorker(wt, full)
	allowed := map[string]bool{}
	for _, n := range filtered {
		allowed[n] = true
	}
	for _, expected := range []string{"read_file", "write_file", "edit_file", "bash", "grep", "glob", "agent_report"} {
		if !allowed[expected] {
			t.Errorf("general-purpose agent missing %s", expected)
		}
	}
	for _, blocked := range []string{"spawn_agent", "send_message", "close_agent"} {
		if allowed[blocked] {
			t.Errorf("general-purpose agent should not receive recursive control tool %s", blocked)
		}
	}
}

func TestFilterToolsForWorker_DisallowedToolsRespected(t *testing.T) {
	wt := WorkerType{
		Name:            "restricted",
		DisallowedTools: []string{"write_file", "edit_file", "apply_patch"},
	}
	full := []string{"read_file", "write_file", "edit_file", "apply_patch", "bash", "agent_report"}
	filtered := FilterToolsForWorker(wt, full)
	allowed := map[string]bool{}
	for _, n := range filtered {
		allowed[n] = true
	}
	for _, blocked := range []string{"write_file", "edit_file", "apply_patch"} {
		if allowed[blocked] {
			t.Errorf("restricted worker should not receive denied tool %s", blocked)
		}
	}
	for _, expected := range []string{"read_file", "bash", "agent_report"} {
		if !allowed[expected] {
			t.Errorf("restricted worker missing %s", expected)
		}
	}
}

func TestFilterToolsForWorker_AllowlistRespected(t *testing.T) {
	wt := WorkerType{
		Name:         "readonly",
		AllowedTools: []string{"read_file", "grep", "glob", "bash", "agent_report"},
	}
	full := []string{"read_file", "write_file", "edit_file", "apply_patch", "bash", "grep", "glob", "agent_report"}
	filtered := FilterToolsForWorker(wt, full)
	allowed := map[string]bool{}
	for _, n := range filtered {
		allowed[n] = true
	}
	for _, blocked := range []string{"write_file", "edit_file", "apply_patch"} {
		if allowed[blocked] {
			t.Errorf("allowlisted worker should not receive write tool %s", blocked)
		}
	}
	if !allowed["read_file"] || !allowed["bash"] || !allowed["agent_report"] {
		t.Errorf("allowlisted worker missing expected read/report tools: %v", filtered)
	}
}

func TestNormalizeIsolation(t *testing.T) {
	agent, _ := LookupWorkerType(DefaultSubagentType)

	cases := []struct {
		name    string
		raw     string
		wt      WorkerType
		want    IsolationMode
		wantErr bool
	}{
		{"empty falls back to type default", "", agent, IsolationInplace, false},
		{"explicit inplace", "inplace", agent, IsolationInplace, false},
		{"explicit worktree", "worktree", agent, IsolationWorktree, false},
		{"case insensitive", "InPlace", agent, IsolationInplace, false},
		{"empty type with empty default falls back to inplace", "", WorkerType{}, IsolationInplace, false},
		{"unknown rejected", "yolo", agent, "", true},
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
