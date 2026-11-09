package appserver

import (
	"context"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/providers"
)

// gatedProviderTimeout guards against deadlocks; it is not a latency budget.
const gatedProviderTimeout = 10 * time.Second

// gatedCall is one model request held until the test answers or fails it.
type gatedCall struct {
	workflowID string
	request    providers.ChatRequest
	response   chan providers.ChatResponse
	failure    chan error
}

// gatedProvider hands every model request to the test, so turn ordering is
// coordinated deterministically instead of with sleeps.
type gatedProvider struct{ calls chan *gatedCall }

func newGatedProvider(buffer int) *gatedProvider {
	return &gatedProvider{calls: make(chan *gatedCall, buffer)}
}

func (provider *gatedProvider) Chat(ctx context.Context, request providers.ChatRequest) (providers.ChatResponse, error) {
	call := &gatedCall{request: request, response: make(chan providers.ChatResponse, 1), failure: make(chan error, 1)}
	if workflow := providers.InferenceWorkflowFromContext(ctx); workflow != nil {
		call.workflowID = workflow.ID
	}
	select {
	case provider.calls <- call:
	case <-ctx.Done():
		return providers.ChatResponse{}, ctx.Err()
	}
	select {
	case response := <-call.response:
		return response, nil
	case err := <-call.failure:
		return providers.ChatResponse{}, err
	case <-ctx.Done():
		return providers.ChatResponse{}, ctx.Err()
	}
}

func (provider *gatedProvider) next(t *testing.T) *gatedCall {
	t.Helper()
	select {
	case call := <-provider.calls:
		return call
	case <-time.After(gatedProviderTimeout):
		t.Fatal("no model call arrived")
		return nil
	}
}
