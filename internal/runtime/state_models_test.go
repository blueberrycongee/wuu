package runtime

import (
	"context"
	"testing"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/modelroles"
	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
)

// State transfer must keep clients and options together after publication or
// in-place model edits, without exposing the retained options for mutation.
func TestStateModelsKeepCapturedClientsAndOptionsAfterTransfer(t *testing.T) {
	ctx := context.Background()
	documents, err := persistence.Open(ctx, t.TempDir(), "jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer documents.Close()
	oldClient, nextClient := &sessionRecordingClient{}, &sessionRecordingClient{}
	rt := &Session{
		Documents: documents, Sessions: session.NewStore(ctx, documents),
		ProviderName: "original", Model: "original-model", TitleClient: oldClient,
		StreamRunner: &agent.StreamRunner{SystemPrompt: "original prompt", Client: oldClient, ProviderName: "original", Model: "original-model", APIModel: "original-api", Effort: "high", Temperature: 0.3, ProviderOptions: map[string]any{"reasoning": map[string]any{"effort": "high"}}},
		ModelRoles:   modelroles.Set{Title: modelroles.Selection{APIModel: "original-title", ProviderOptions: map[string]any{"mode": "original"}}},
	}
	captured, err := rt.RetainState()
	if err != nil {
		t.Fatal(err)
	}
	transferred := captured.Retain()
	if transferred == nil {
		t.Fatal("state transfer failed")
	}
	defer transferred.Close()
	if err := captured.Close(); err != nil {
		t.Fatal(err)
	}
	rt.StreamRunner.Client, rt.TitleClient = nextClient, nextClient
	rt.StreamRunner.APIModel = "successor-api"
	rt.StreamRunner.SystemPrompt = "successor prompt"
	rt.StreamRunner.ProviderOptions["reasoning"].(map[string]any)["effort"] = "low"
	rt.ModelRoles.Title.ProviderOptions["mode"] = "successor"
	if transferred.SystemPrompt != "original prompt" {
		t.Fatalf("transferred state followed successor prompt: %q", transferred.SystemPrompt)
	}
	models, err := transferred.Models()
	if err != nil {
		t.Fatal(err)
	}
	if models.Roles.Main.APIModel != "original-api" || models.Roles.Main.ProviderOptions["reasoning"].(map[string]any)["effort"] != "high" || models.Roles.Title.ProviderOptions["mode"] != "original" {
		t.Fatalf("captured options changed: %+v", models.Roles)
	}
	if _, err := models.Main.Chat(ctx, providers.ChatRequest{Model: models.Roles.Main.APIModel}); err != nil {
		t.Fatal(err)
	}
	if oldClient.last.Model != "original-api" || nextClient.last.Model != "" || models.Title != oldClient {
		t.Fatal("retained model client followed the successor")
	}
	models.Roles.Main.ProviderOptions["reasoning"].(map[string]any)["effort"] = "caller-edit"
	models, err = transferred.Models()
	if err != nil || models.Roles.Main.ProviderOptions["reasoning"].(map[string]any)["effort"] != "high" {
		t.Fatalf("caller mutated retained options: %+v, %v", models.Roles.Main.ProviderOptions, err)
	}
	if _, err := captured.Models(); err == nil {
		t.Fatal("closed state returned model clients")
	}
}
