package hooks

import (
	"context"

	"github.com/blueberrycongee/wuu/internal/providers"
)

// NewProviderModelClient adapts a providers.Client into the smaller
// PromptModelClient interface that PromptHook uses. This keeps the
// PromptHook call-site simple (three strings in, one string out) while
// letting the runtime wire the real model client into the registry.
//
// defaultModel is the agent's configured model name; it is used when a
// hook entry leaves its own `model` field empty. Pass an empty string
// to fall back to the provider's own default on each call.
func NewProviderModelClient(client providers.Client, defaultModel string, defaultJournal providers.InferenceJournal) PromptModelClient {
	return &providerModelClientAdapter{client: client, defaultModel: defaultModel, defaultJournal: defaultJournal}
}

type providerModelClientAdapter struct {
	client         providers.Client
	defaultModel   string
	defaultJournal providers.InferenceJournal
}

func (a *providerModelClientAdapter) ChatJSON(ctx context.Context, model, system, user string) (string, error) {
	if a == nil || a.client == nil {
		return "", nil
	}
	messages := make([]providers.ChatMessage, 0, 2)
	if system != "" {
		messages = append(messages, providers.ChatMessage{Role: "system", Content: system})
	}
	messages = append(messages, providers.ChatMessage{Role: "user", Content: user})

	resolvedModel := model
	if resolvedModel == "" {
		resolvedModel = a.defaultModel
	}
	if providers.InferenceJournalFromContext(ctx) == nil {
		ctx = providers.WithInferenceJournal(ctx, a.defaultJournal)
	}
	resp, err := providers.ExecuteChat(ctx, a.client, providers.ChatRequest{
		Model:    resolvedModel,
		Messages: messages,
	}, providers.InferenceOperationHookPrompt, providers.InferenceProfileContinuationCritical)
	if err != nil {
		return "", err
	}
	return resp.Content, nil
}
