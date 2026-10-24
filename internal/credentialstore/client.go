package credentialstore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

// Client captures the selected generation's service call. The owner must keep
// that generation alive through all OAuth and plugin cleanup that uses it.
type Client struct {
	call func(context.Context, string, json.RawMessage) (json.RawMessage, error)
}

func NewClient(call func(context.Context, string, json.RawMessage) (json.RawMessage, error)) *Client {
	return &Client{call: call}
}
func (c *Client) Get(ctx context.Context, namespace, key string) ([]byte, error) {
	var result EntryResult
	if err := c.invoke(ctx, "get", EntryRequest{Namespace: namespace, Key: key}, &result); err != nil {
		return nil, err
	}
	if result.Value == nil {
		return nil, ErrNotFound
	}
	return result.Value, nil
}
func (c *Client) Set(ctx context.Context, namespace, key string, value []byte) error {
	if value == nil {
		value = []byte{}
	}
	return c.invoke(ctx, "set", SetRequest{EntryRequest: EntryRequest{Namespace: namespace, Key: key}, Value: value}, new(struct{}))
}
func (c *Client) Delete(ctx context.Context, namespace, key string) error {
	return c.invoke(ctx, "delete", EntryRequest{Namespace: namespace, Key: key}, new(struct{}))
}

// Update may repeat the pure local callback after a definitive comparison
// conflict. Transport errors are not conflicts and never replay a write.
func (c *Client) Update(ctx context.Context, namespace, key string, update func([]byte) ([]byte, error)) error {
	if update == nil {
		return errors.New("credential update callback is required")
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		current, err := c.Get(ctx, namespace, key)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		next, err := update(bytes.Clone(current))
		if err != nil {
			clear(current)
			clear(next)
			return err
		}
		var result CompareExchangeResult
		err = c.invoke(ctx, "compare-exchange", CompareExchangeRequest{EntryRequest: EntryRequest{Namespace: namespace, Key: key}, Expected: current, Value: next}, &result)
		clear(current)
		clear(next)
		if err != nil {
			return err
		}
		if result.Exchanged {
			return nil
		}
	}
}
func (c *Client) invoke(ctx context.Context, method string, input, output any) error {
	if c == nil || c.call == nil {
		return credentialError("service_unavailable", "credential binding is unavailable")
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return credentialError("invalid_params", "credential request is invalid")
	}
	defer clear(raw)
	rawResult, err := c.call(ctx, method, raw)
	defer clear(rawResult)
	if err != nil {
		var serviceErr *pluginhost.HostServiceError
		if errors.As(err, &serviceErr) {
			return credentialError(serviceErr.Code, "selected credential provider failed; a write may have been applied")
		}
		return credentialError("credential_unavailable", "selected credential provider failed; a write may have been applied")
	}
	if err := json.Unmarshal(rawResult, output); err != nil {
		return credentialError("invalid_response", "credential provider returned an invalid response")
	}
	return nil
}
