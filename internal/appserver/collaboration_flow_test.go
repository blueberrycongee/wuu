package appserver

import (
	"context"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/providers"
)

type collaborationFlowCall struct {
	request  providers.ChatRequest
	response chan providers.ChatResponse
	failure  chan error
}

type collaborationFlowProvider struct{ calls chan *collaborationFlowCall }

func (provider *collaborationFlowProvider) Chat(ctx context.Context, request providers.ChatRequest) (providers.ChatResponse, error) {
	call := &collaborationFlowCall{request: request, response: make(chan providers.ChatResponse, 1), failure: make(chan error, 1)}
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

func (provider *collaborationFlowProvider) next(t *testing.T) *collaborationFlowCall {
	t.Helper()
	select {
	case call := <-provider.calls:
		return call
	case <-time.After(collaborationTestWaitTimeout):
		t.Fatal("collaboration did not reach the next model call")
		return nil
	}
}

func newCollaborationFlowFixture(t *testing.T) (*collaborationRPCFixture, *collaborationFlowProvider) {
	t.Helper()
	fixture := newCollaborationRPCFixture(t)
	provider := &collaborationFlowProvider{calls: make(chan *collaborationFlowCall, 16)}
	fixture.server.rt.StreamRunner.Client = providers.AdaptStreamClient(provider)
	fixture.server.channelService.SetWakeSink(fixture.server)
	return fixture, provider
}
