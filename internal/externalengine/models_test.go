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
