package toolservice

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/blueberrycongee/wuu/internal/modelwire"
	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

// Client invokes a captured service binding. The caller retains its generation
// and authenticates execution scope. No method retries or chooses a fallback.
type Client struct {
	call func(context.Context, string, json.RawMessage) (json.RawMessage, error)
}

func NewClient(call func(context.Context, string, json.RawMessage) (json.RawMessage, error)) *Client {
	return &Client{call: call}
}
func (c *Client) invoke(ctx context.Context, method string, input any) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	return c.call(ctx, method, raw)
}
func (c *Client) List(ctx context.Context) (ListResult, error) {
	raw, err := c.invoke(ctx, "list", struct{}{})
	if err != nil {
		return ListResult{}, err
	}
	var view struct {
		Tools          *[]Definition      `json:"tools"`
		Catalog        *[]json.RawMessage `json:"catalog"`
		CatalogPrompt  *string            `json:"catalog_prompt"`
		SystemFragment *string            `json:"system_fragment"`
	}
	if err = json.Unmarshal(raw, &view); err != nil {
		return ListResult{}, err
	}
	if view.Tools == nil || view.Catalog == nil || view.CatalogPrompt == nil || view.SystemFragment == nil {
		return ListResult{}, fmt.Errorf("%s.list: incomplete response", ServiceName)
	}
	var result ListResult
	if err = json.Unmarshal(raw, &result); err != nil {
		return ListResult{}, err
	}
	seen := make(map[string]bool, len(result.Tools))
	for _, d := range result.Tools {
		if d.Name == "" || seen[d.Name] || (!d.Freeform && d.InputSchema == nil) {
			return ListResult{}, fmt.Errorf("%s.list: invalid tool definition %q", ServiceName, d.Name)
		}
		seen[d.Name] = true
	}
	return result, nil
}
func (c *Client) Resolve(ctx context.Context, call providers.ToolCall) (ResolveResult, error) {
	raw, err := c.invoke(ctx, "resolve", CallRequest{call})
	if err != nil {
		return ResolveResult{}, err
	}
	var wire struct {
		Found    *bool                      `json:"found"`
		Metadata *Metadata                  `json:"metadata"`
		Supports bool                       `json:"supports"`
		Display  *providers.ToolCallDisplay `json:"display"`
	}
	if err = json.Unmarshal(raw, &wire); err != nil {
		return ResolveResult{}, err
	}
	if wire.Found == nil || (*wire.Found && wire.Metadata == nil) {
		return ResolveResult{}, fmt.Errorf("%s.resolve: incomplete response", ServiceName)
	}
	return ResolveResult{Found: *wire.Found, Metadata: wire.Metadata, Supports: wire.Supports, Display: wire.Display}, nil
}
func (c *Client) Execute(ctx context.Context, call providers.ToolCall) (ExecutionResult, error) {
	raw, err := c.invoke(ctx, "execute", CallRequest{call})
	if err != nil {
		return ExecutionResult{}, err
	}
	var wire struct {
		Result *toolresult.Result `json:"result"`
	}
	if err = json.Unmarshal(raw, &wire); err != nil {
		return ExecutionResult{}, err
	}
	if wire.Result == nil {
		return ExecutionResult{}, fmt.Errorf("%s.execute: missing result", ServiceName)
	}
	if err = wire.Result.Validate(); err != nil {
		return ExecutionResult{}, fmt.Errorf("%s.execute: %w", ServiceName, err)
	}
	var result ExecutionResult
	if err = json.Unmarshal(raw, &result); err != nil {
		return ExecutionResult{}, err
	}
	return result, nil
}
func (c *Client) ExecuteResult(ctx context.Context, call providers.ToolCall) (toolresult.Result, error) {
	result, err := c.Execute(ctx, call)
	return result.Result, err
}
func (c *Client) Freeze(ctx context.Context) error {
	_, err := c.invoke(ctx, "freeze", struct{}{})
	return err
}
func (c *Client) Unfreeze(ctx context.Context) error {
	_, err := c.invoke(ctx, "unfreeze", struct{}{})
	return err
}

// Prepare invokes only the captured selected provider. It does not refresh a
// native catalog when an external provider fails or omits this method.
func (c *Client) Prepare(ctx context.Context, history []providers.ChatMessage) ([]providers.ChatMessage, error) {
	request := PrepareRequest{History: make([]pluginprotocol.ModelMessageV1, 0, len(history))}
	for _, message := range history {
		request.History = append(request.History, modelwire.ChatMessageToWire(message))
	}
	raw, err := c.invoke(ctx, "prepare", request)
	if err != nil {
		return nil, err
	}
	var response PrepareResult
	if err = json.Unmarshal(raw, &response); err != nil {
		return nil, err
	}
	if response.Messages == nil {
		return nil, fmt.Errorf("%s.prepare: missing messages array", ServiceName)
	}
	messages := make([]providers.ChatMessage, 0, len(response.Messages))
	for _, message := range response.Messages {
		messages = append(messages, modelwire.ChatMessageFromWire(message))
	}
	return messages, nil
}

// Finalize applies selected result settlement once, including normalized errors.
func (c *Client) Finalize(ctx context.Context, call providers.ToolCall, result toolresult.Result) (toolresult.Result, error) {
	raw, err := c.invoke(ctx, "finalize", FinalizeRequest{Call: call, Result: &result})
	if err != nil {
		return toolresult.Result{}, err
	}
	var wire struct {
		Result *toolresult.Result `json:"result"`
	}
	if err = json.Unmarshal(raw, &wire); err != nil {
		return toolresult.Result{}, err
	}
	if wire.Result == nil {
		return toolresult.Result{}, fmt.Errorf("%s.finalize: missing result", ServiceName)
	}
	if err = wire.Result.Validate(); err != nil {
		return toolresult.Result{}, err
	}
	return *wire.Result, nil
}
