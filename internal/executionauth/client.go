package executionauth

import (
	"context"
	"encoding/json"
	"errors"
)

// Client calls only the selected generation. Transport uncertainty is surfaced;
// no method silently retries a write or chooses a local implementation.
type Client struct {
	call func(context.Context, string, json.RawMessage) (json.RawMessage, error)
}

func NewClient(call func(context.Context, string, json.RawMessage) (json.RawMessage, error)) *Client {
	return &Client{call: call}
}
func (c *Client) invoke(ctx context.Context, method string, input, output any) error {
	if c == nil || c.call == nil {
		return errors.New("selected execution authorization unavailable")
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return err
	}
	result, err := c.call(ctx, method, raw)
	if err != nil {
		return err
	}
	return decode(result, output)
}
func (c *Client) Issue(ctx context.Context, r IssueRequest) (IssueResult, error) {
	var result IssueResult
	err := c.invoke(ctx, "issue", r, &result)
	if err == nil && (result.Reference.Provider == "" || result.Reference.ID == "" || result.Principal.Validate() != nil || !result.Scope.Equal(r.Scope) || !result.ExpiresAt.Equal(r.ExpiresAt) || !capabilitiesEqual(result.Capabilities, r.Capabilities)) {
		return IssueResult{}, errors.New("invalid execution grant issuance result")
	}
	return result, err
}
func (c *Client) Validate(ctx context.Context, r ValidateRequest) (Validation, error) {
	var wire struct {
		Valid     *bool           `json:"valid"`
		Reason    string          `json:"reason,omitempty"`
		Principal json.RawMessage `json:"principal,omitempty"`
		Scope     json.RawMessage `json:"scope,omitempty"`
		ExpiresAt json.RawMessage `json:"expires_at,omitempty"`
	}
	if err := c.invoke(ctx, "validate", r, &wire); err != nil {
		return Validation{}, err
	}
	if wire.Valid == nil {
		return Validation{}, errors.New("execution grant validation omitted verdict")
	}
	result := Validation{Valid: *wire.Valid, Reason: wire.Reason}
	if !result.Valid {
		return result, nil
	}
	if json.Unmarshal(wire.Principal, &result.Principal) != nil || json.Unmarshal(wire.Scope, &result.Scope) != nil || json.Unmarshal(wire.ExpiresAt, &result.ExpiresAt) != nil || result.Principal.Validate() != nil || !result.Scope.Equal(r.Scope) || result.ExpiresAt.IsZero() {
		return Validation{}, errors.New("invalid execution grant validation result")
	}
	return result, nil
}
func (c *Client) Revoke(ctx context.Context, r RevokeRequest) (RevokeResult, error) {
	var result struct {
		Revoked *bool `json:"revoked"`
	}
	if err := c.invoke(ctx, "revoke", r, &result); err != nil {
		return RevokeResult{}, err
	}
	if result.Revoked == nil {
		return RevokeResult{}, errors.New("execution grant revocation omitted verdict")
	}
	return RevokeResult{Revoked: *result.Revoked}, nil
}

func (c *Client) Describe(ctx context.Context) (PolicyInfo, error) {
	var result PolicyInfo
	err := c.invoke(ctx, "describe", struct{}{}, &result)
	if err == nil && (result.DefaultLifetimeSeconds <= 0 || result.MaxLifetimeSeconds < result.DefaultLifetimeSeconds || len(result.AuthorityKinds) == 0 || len(result.ScopeKinds) == 0) {
		return PolicyInfo{}, errors.New("selected grant policy is incomplete")
	}
	return result, err
}

// Inspect returns issuance metadata, not a permission verdict. Every effect
// still validates its exact action/resources through the current selected policy.
func (c *Client) Inspect(ctx context.Context, r InspectRequest) (IssueResult, error) {
	var result IssueResult
	err := c.invoke(ctx, "inspect", r, &result)
	if err == nil && (result.Reference != r.Reference || result.Scope.Consumer != r.Consumer || result.Scope.Validate() != nil || !validCapabilities(result.Capabilities) || result.Principal.Validate() != nil || result.ExpiresAt.IsZero()) {
		return IssueResult{}, errors.New("invalid execution grant inspection result")
	}
	return result, err
}
