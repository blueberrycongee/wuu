package providers

import (
	"context"
	"errors"
)

type UnavailableClient struct{ Reason string }

func (c *UnavailableClient) Chat(context.Context, ChatRequest) (ChatResponse, error) {
	return ChatResponse{}, errors.New(c.Reason)
}

func (c *UnavailableClient) StreamChat(context.Context, ChatRequest) (<-chan StreamEvent, error) {
	return nil, errors.New(c.Reason)
}
