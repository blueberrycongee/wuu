package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/statepath"
)

func TestAgentProfileToolsCreateAndListProfiles(t *testing.T) {
	root := t.TempDir()
	wuuHome := filepath.Join(t.TempDir(), "wuu-home")
	t.Setenv("WUU_HOME", wuuHome)

	kit, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	createResp, err := kit.Execute(context.Background(), providers.ToolCall{
		Name: "create_agent_profile",
		Arguments: `{
			"name":"qa_laowang",
			"role":"QA reviewer",
			"description":"Remembers recurring QA checks."
		}`,
	})
	if err != nil {
		t.Fatalf("create_agent_profile: %v", err)
	}
	var created struct {
		Action  string `json:"action"`
		Created bool   `json:"created"`
		Profile struct {
			Name        string `json:"name"`
			Role        string `json:"role"`
			Description string `json:"description"`
		} `json:"profile"`
	}
	if err := json.Unmarshal([]byte(createResp), &created); err != nil {
		t.Fatalf("parse create response: %v", err)
	}
	if created.Action != "create_agent_profile" {
		t.Fatalf("create action = %q, want create_agent_profile", created.Action)
	}
	if !created.Created || created.Profile.Name != "qa_laowang" || created.Profile.Role != "QA reviewer" {
		t.Fatalf("unexpected create response: %+v", created)
	}

	listResp, err := kit.Execute(context.Background(), providers.ToolCall{Name: "list_agent_profiles", Arguments: `{}`})
	if err != nil {
		t.Fatalf("list_agent_profiles: %v", err)
	}
	var listed struct {
		Action   string `json:"action"`
		Count    int    `json:"count"`
		Profiles []struct {
			Name string `json:"name"`
			Role string `json:"role"`
		} `json:"profiles"`
	}
	if err := json.Unmarshal([]byte(listResp), &listed); err != nil {
		t.Fatalf("parse list response: %v", err)
	}
	if listed.Action != "list_agent_profiles" {
		t.Fatalf("list action = %q, want list_agent_profiles", listed.Action)
	}
	if listed.Count != 1 || listed.Profiles[0].Name != "qa_laowang" || listed.Profiles[0].Role != "QA reviewer" {
		t.Fatalf("unexpected profile list: %+v", listed)
	}
	records := kit.ToolTelemetry()
	if len(records) != 2 || records[0].ResultAction != "create_agent_profile" || records[1].ResultAction != "list_agent_profiles" {
		t.Fatalf("profile telemetry actions mismatch: %+v", records)
	}
}

func TestAgentProfileToolsHideLegacyWorkflowMetadata(t *testing.T) {
	root := t.TempDir()
	wuuHome := filepath.Join(t.TempDir(), "wuu-home")
	t.Setenv("WUU_HOME", wuuHome)

	profileDir, err := statepath.ProfileDir(wuuHome, "legacy_qa")
	if err != nil {
		t.Fatalf("ProfileDir: %v", err)
	}
	if err := os.MkdirAll(profileDir, 0o755); err != nil {
		t.Fatalf("mkdir profile dir: %v", err)
	}
	legacyMetadata := `{
  "name": "legacy_qa",
  "source": "workflow",
  "workflow_name": "release-qa",
  "role": "QA reviewer",
  "description": "Legacy workflow-created profile.",
  "created_at": "2026-01-02T03:04:05Z",
  "last_resolved_at": "2026-01-02T03:04:05Z"
}
`
	if err := os.WriteFile(filepath.Join(profileDir, "profile.json"), []byte(legacyMetadata), 0o644); err != nil {
		t.Fatalf("write legacy profile metadata: %v", err)
	}

	kit, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	listResp, err := kit.Execute(context.Background(), providers.ToolCall{Name: "list_agent_profiles", Arguments: `{}`})
	if err != nil {
		t.Fatalf("list_agent_profiles: %v", err)
	}
	for _, bad := range []string{`"workflow_name"`, `"source":"workflow"`, "release-qa"} {
		if strings.Contains(listResp, bad) {
			t.Fatalf("list_agent_profiles should hide legacy workflow metadata %q: %s", bad, listResp)
		}
	}
	if !strings.Contains(listResp, `"name":"legacy_qa"`) || !strings.Contains(listResp, `"role":"QA reviewer"`) {
		t.Fatalf("list_agent_profiles should keep neutral profile facts: %s", listResp)
	}
}
