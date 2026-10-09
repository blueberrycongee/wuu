package openai

import (
	"context"
	"net/http"
	"strings"

	"github.com/blueberrycongee/wuu/internal/providers"
)

const responsesTurnStateHeader = "x-codex-turn-state"

type responsesTurnRoutingKey struct{}
type responsesTurnRouting struct {
	state *providers.TurnRouting
	scope string
}

func (c *Client) withResponsesTurnRouting(ctx context.Context, req providers.ChatRequest) context.Context {
	// Bind to the actual endpoint and credentials, not a caller-supplied rate-limit scope.
	headers := http.Header{}
	for key, value := range c.headers {
		headers.Set(key, value)
	}
	scope := c.baseURL + "\x00" + string(providers.NewProviderScope(c.baseURL, c.apiKey, openAIOrganization(c.headers))) + "\x00" + headers.Get("ChatGPT-Account-ID") + "\x00" + responsePromptCacheKey(req.CacheHint)
	routing := responsesTurnRouting{state: req.TurnRouting, scope: scope}
	routing.state.Token(scope)
	return context.WithValue(ctx, responsesTurnRoutingKey{}, routing)
}

func responsesTurnRoutingFromContext(ctx context.Context) responsesTurnRouting {
	routing, _ := ctx.Value(responsesTurnRoutingKey{}).(responsesTurnRouting)
	return routing
}

func (r responsesTurnRouting) apply(headers http.Header) {
	if token := r.state.Token(r.scope); token != "" {
		headers.Set(responsesTurnStateHeader, token)
	}
}

func (r responsesTurnRouting) remember(headers http.Header) {
	r.state.Remember(r.scope, headers.Get(responsesTurnStateHeader))
}

func (r responsesTurnRouting) rememberEvent(event responsesStreamEvent) {
	if event.Type != "response.metadata" {
		return
	}
	for key, value := range event.Headers {
		if strings.EqualFold(key, responsesTurnStateHeader) {
			if token, ok := value.(string); ok {
				r.state.Remember(r.scope, token)
			}
		}
	}
}
