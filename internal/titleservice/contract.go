// Package titleservice defines the selected conversation-title policy.
package titleservice

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/blueberrycongee/wuu/internal/modelwire"
	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
	"github.com/blueberrycongee/wuu/internal/providers"
)

const ServiceName = "session.title"
const NativeModule = "wuu.title.default"

type PlanRequest struct {
	Model         string   `json:"model"`
	HumanMessages []string `json:"human_messages"`
	Title         string   `json:"title,omitempty"`
	Summary       string   `json:"summary,omitempty"`
	ReadOnly      bool     `json:"read_only,omitempty"`
	Ephemeral     bool     `json:"ephemeral,omitempty"`
	ParentID      string   `json:"parent_id,omitempty"`
	Force         bool     `json:"force,omitempty"`
	Persist       bool     `json:"persist"`
}

// A provider may skip, return a title without inference, or supply a complete
// request. The host owns the model transport, accounting and atomic persistence.
type Plan struct {
	Action      string                          `json:"action"`
	Reason      string                          `json:"reason,omitempty"`
	Source      string                          `json:"source,omitempty"`
	Title       string                          `json:"title,omitempty"`
	Messages    []pluginprotocol.ModelMessageV1 `json:"messages,omitempty"`
	Temperature *float64                        `json:"temperature,omitempty"`
}

type Client struct {
	call func(context.Context, string, json.RawMessage) (json.RawMessage, error)
}

func NewClient(call func(context.Context, string, json.RawMessage) (json.RawMessage, error)) *Client {
	return &Client{call: call}
}
func (c *Client) invoke(ctx context.Context, method string, input, output any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return err
	}
	raw, err = c.call(ctx, method, raw)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, output)
}
func (c *Client) Plan(ctx context.Context, in PlanRequest) (Plan, error) {
	var out Plan
	if err := c.invoke(ctx, "plan", in, &out); err != nil {
		return Plan{}, err
	}
	switch out.Action {
	case "skip":
	case "complete":
		if strings.TrimSpace(out.Title) == "" {
			return Plan{}, fmt.Errorf("title policy returned an empty complete title")
		}
	case "infer":
		if len(out.Messages) == 0 {
			return Plan{}, fmt.Errorf("title policy omitted inference messages")
		}
		messages := make([]providers.ChatMessage, 0, len(out.Messages))
		for _, message := range out.Messages {
			messages = append(messages, modelwire.ChatMessageFromWire(message))
		}
		if err := providers.ValidateToolCallHistory(messages); err != nil {
			return Plan{}, err
		}
	default:
		return Plan{}, fmt.Errorf("invalid title policy action %q", out.Action)
	}
	return out, nil
}
func (c *Client) Resolve(ctx context.Context, text string) (string, error) {
	var out struct {
		Title *string `json:"title"`
	}
	if err := c.invoke(ctx, "resolve", struct {
		Text string `json:"text"`
	}{text}, &out); err != nil {
		return "", err
	}
	if out.Title == nil {
		return "", fmt.Errorf("title policy omitted title result")
	}
	return *out.Title, nil
}
