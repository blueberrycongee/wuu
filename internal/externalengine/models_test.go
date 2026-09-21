package externalengine

import (
	"encoding/json"
	"testing"
)

func TestModelsFromACPSessionUsesGrokFirstClassCatalog(t *testing.T) {
	session := parseACPSession(t, `{
		"sessionId": "s1",
		"models": {
			"currentModelId": "grok-4.6",
			"availableModels": [
				{"modelId": "grok-4.6", "name": "Grok 4.6"},
				{"modelId": "grok-4.5", "name": "Grok 4.5"}
			]
		},
		"configOptions": [{
			"id": "reasoning_effort",
			"category": "thought_level",
			"type": "select",
			"currentValue": "high",
			"options": [
				{"value": "low", "name": "Low"},
				{"value": "medium", "name": "Medium"},
				{"value": "high", "name": "High"}
			]
		}]
	}`)
	models := modelsFromACPSession(session)
	if len(models) != 2 || models[0].ID != "grok-4.6" || !models[0].IsDefault || models[1].ID != "grok-4.5" {
		t.Fatalf("models = %+v", models)
	}
	if models[0].DisplayName != "Grok 4.6" || models[0].DefaultEffort != "high" {
		t.Fatalf("grok-4.6 = %+v", models[0])
	}
	if got := models[0].SupportedEfforts; len(got) != 3 || got[0] != "low" || got[2] != "high" {
		t.Fatalf("efforts = %q", got)
	}
}

func TestModelsFromACPSessionPrefersConfigOptionsAndDropsDefaultAlias(t *testing.T) {
	session := parseACPSession(t, `{
		"models": {
			"currentModelId": "legacy-low",
			"availableModels": [
				{"modelId": "legacy-low", "name": "Legacy (low)"},
				{"modelId": "legacy-high", "name": "Legacy (high)"}
			]
		},
		"configOptions": [
			{
				"id": "model",
				"category": "model",
				"type": "select",
				"currentValue": "grok-4.6",
				"options": [
					{"value": "default", "name": "Default"},
					{"value": "grok-4.6", "name": "Grok 4.6"},
					{"value": "grok-4.5", "name": "Grok 4.5"}
				]
			},
			{
				"id": "reasoning_effort",
				"category": "thought_level",
				"type": "select",
				"currentValue": "medium",
				"options": [{"value": "medium", "name": "Medium"}]
			}
		]
	}`)
	models := modelsFromACPSession(session)
	if len(models) != 2 || models[0].ID != "grok-4.6" || models[1].ID != "grok-4.5" {
		t.Fatalf("models = %+v", models)
	}
	if !models[0].IsDefault || models[0].DefaultEffort != "medium" {
		t.Fatalf("default = %+v", models[0])
	}
}

func TestPermissionModesFromACPSessionMapHostSelections(t *testing.T) {
	session := parseACPSession(t, `{
		"configOptions": [{
			"id": "mode",
			"category": "mode",
			"type": "select",
			"currentValue": "agent",
			"options": [
				{"value": "read-only", "name": "Read Only"},
				{"value": "agent", "name": "Agent"},
				{"value": "agent-full-access", "name": "Agent (full access)"}
			]
		}]
	}`)
	modes := permissionModesFromACPSession(session)
	if len(modes) != 3 {
		t.Fatalf("modes = %+v", modes)
	}
	if modes[0] != (HostPermissionMode{Mode: "standard", ID: "agent", Label: "Agent"}) {
		t.Fatalf("standard = %+v", modes[0])
	}
	if modes[1] != (HostPermissionMode{Mode: "read_only", ID: "read-only", Label: "Read Only"}) {
		t.Fatalf("read_only = %+v", modes[1])
	}
	if modes[2] != (HostPermissionMode{Mode: "unconfined", ID: "agent-full-access", Label: "Agent (full access)"}) {
		t.Fatalf("unconfined = %+v", modes[2])
	}
}

func TestPermissionModesFromACPSessionOmitAskWhenItIsThePromptingDefault(t *testing.T) {
	session := parseACPSession(t, `{
		"configOptions": [{
			"id": "mode",
			"category": "mode",
			"type": "select",
			"currentValue": "accept-edits",
			"options": [
				{"value": "accept-edits", "name": "Code"},
				{"value": "ask", "name": "Ask"},
				{"value": "bypass", "name": "Bypass Permissions"}
			]
		}]
	}`)
	modes := permissionModesFromACPSession(session)
	if len(modes) != 2 || modes[0].Mode != "standard" || modes[0].ID != "ask" || modes[1].ID != "bypass" {
		t.Fatalf("modes = %+v", modes)
	}
}

func TestPermissionModesFromACPSessionKeepHostSelectionsWithoutNativeIds(t *testing.T) {
	modes := permissionModesFromACPSession(acpSession{ID: "s"})
	if len(modes) != 2 || modes[0].Mode != "standard" || modes[0].ID != "" || modes[1].Mode != "unconfined" {
		t.Fatalf("modes = %+v", modes)
	}
}

func TestModelsFromACPSessionEmptyWhenAgentAdvertisesNothing(t *testing.T) {
	if models := modelsFromACPSession(acpSession{ID: "s"}); len(models) != 0 {
		t.Fatalf("models = %+v", models)
	}
}

func parseACPSession(t *testing.T, raw string) acpSession {
	t.Helper()
	var session acpSession
	if err := json.Unmarshal([]byte(raw), &session); err != nil {
		t.Fatal(err)
	}
	return session
}
