package toolservice

import (
	"context"
	"encoding/json"
	"fmt"

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
