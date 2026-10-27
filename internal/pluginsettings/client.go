package pluginsettings

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

// Client uses one captured service generation. Update callbacks must be pure:
// a definite CAS conflict can repeat them; an uncertain write never does.
type Client struct {
	call func(context.Context, string, json.RawMessage) (json.RawMessage, error)
}

func NewClient(call func(context.Context, string, json.RawMessage) (json.RawMessage, error)) *Client {
	return &Client{call: call}
}
func (c *Client) Read(ctx context.Context, subject string, scope Scope) (Document, error) {
	var view SettingsView
	err := c.invoke(ctx, "read", ReadRequest{Subject: subject, Scope: scope}, &view)
	return view.Document, err
}
func (c *Client) ReadState(ctx context.Context, subject string, scope Scope) (StateDocument, error) {
	var view StateView
	err := c.invoke(ctx, "read-state", ReadRequest{Subject: subject, Scope: scope}, &view)
	return view.Document, err
}
func (c *Client) Update(ctx context.Context, subject string, scope Scope, fingerprint string, update func(map[string]json.RawMessage) error) (Document, error) {
	if update == nil {
		return Document{}, errors.New("settings update callback is required")
	}
	for {
		if err := ctx.Err(); err != nil {
			return Document{}, err
		}
		var view SettingsView
		if err := c.invoke(ctx, "read", ReadRequest{Subject: subject, Scope: scope}, &view); err != nil {
			return Document{}, err
		}
		if view.Document.Values == nil {
			view.Document.Values = map[string]json.RawMessage{}
		}
		if err := update(view.Document.Values); err != nil {
			return Document{}, err
		}
		var result SettingsView
		err := c.invoke(ctx, "update", UpdateRequest{ReadRequest: ReadRequest{Subject: subject, Scope: scope}, ExpectedRevision: view.Revision, Fingerprint: fingerprint, Values: view.Document.Values}, &result)
		var hostErr *pluginhost.HostServiceError
		if errors.As(err, &hostErr) && hostErr.Code == "settings_conflict" {
			continue
		}
		return result.Document, err
	}
}
func (c *Client) UpdateState(ctx context.Context, subject string, scope Scope, update func(map[string]string) error) (StateDocument, error) {
	if update == nil {
		return StateDocument{}, errors.New("plugin state update callback is required")
	}
	for {
		if err := ctx.Err(); err != nil {
			return StateDocument{}, err
		}
		var view StateView
		if err := c.invoke(ctx, "read-state", ReadRequest{Subject: subject, Scope: scope}, &view); err != nil {
			return StateDocument{}, err
		}
		if view.Document.Values == nil {
			view.Document.Values = map[string]string{}
		}
		if err := update(view.Document.Values); err != nil {
			return StateDocument{}, err
		}
		var result StateView
		err := c.invoke(ctx, "update-state", UpdateStateRequest{ReadRequest: ReadRequest{Subject: subject, Scope: scope}, ExpectedRevision: view.Revision, Values: view.Document.Values}, &result)
		var hostErr *pluginhost.HostServiceError
		if errors.As(err, &hostErr) && hostErr.Code == "settings_conflict" {
			continue
		}
		return result.Document, err
	}
}

// MoveStatePrefix asks the selected provider to transfer ownership using durable
// fences and receipts. The caller must authorize both exact subjects.
// An uncertain result is never retried here: retrying the identical transfer is
// safe only under the provider's durable fence-and-receipt contract.
func (c *Client) MoveStatePrefix(ctx context.Context, transfer StateTransfer) error {
	var result struct{}
	err := c.invoke(ctx, "transfer-state", transfer, &result)
	var hostErr *pluginhost.HostServiceError
	if errors.As(err, &hostErr) && hostErr.Code == "method_not_found" {
		return settingsError("storage_transfer_unsupported", "selected settings provider does not support ownership transfers")
	}
	return err
}

func (c *Client) invoke(ctx context.Context, method string, input, output any) error {
	if c == nil || c.call == nil {
		return settingsError("service_unavailable", "settings binding is unavailable")
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return settingsError("invalid_params", "settings request is invalid")
	}
	raw, err = c.call(ctx, method, raw)
	if err != nil {
		return err
	}
	if json.Unmarshal(raw, output) != nil {
		return settingsError("invalid_response", "settings provider returned an invalid response")
	}
	return nil
}
