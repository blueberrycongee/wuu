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

func (c *Client) AcquireCommit(ctx context.Context, r CommitRequest) (CommitGuard, error) {
	var result CommitGuard
	if err := c.invoke(ctx, "acquire-commit", r, &result); err != nil {
		return CommitGuard{}, err
	}
	if !result.matches(r) || result.Principal.Validate() != nil || result.ExpiresAt.IsZero() {
		return CommitGuard{}, errors.New("invalid execution commit admission result")
	}
	return result, nil
}

// ReleaseCommit must use the captured generation's cleanup route. It never
// substitutes not-committed for an unknown publication or retries that effect.
func (c *Client) ReleaseCommit(ctx context.Context, r ReleaseCommitRequest) (CommitRelease, error) {
	var result CommitRelease
	if err := c.invoke(ctx, "release-commit", r, &result); err != nil {
		return CommitRelease{}, err
	}
	if !result.Released {
		return CommitRelease{}, errors.New("execution commit release omitted confirmation")
	}
	return result, nil
}
func (c *Client) InspectCommit(ctx context.Context, r CommitRequest) (CommitStatus, error) {
	var result CommitStatus
	if err := c.invoke(ctx, "inspect-commit", r, &result); err != nil {
		return CommitStatus{}, err
	}
	if !result.Guard.matches(r) || !bounded(result.Caller, 1024) || result.Outcome != "" && result.Outcome != "committed" && result.Outcome != "not-committed" || result.Outcome == "committed" && !digestValid(result.ReceiptDigest) || result.Outcome != "committed" && result.ReceiptDigest != "" || result.Outcome != "not-committed" && (result.Guard.Principal.Validate() != nil || result.Guard.ExpiresAt.IsZero()) {
		return CommitStatus{}, errors.New("invalid execution commit inspection result")
	}
	return result, nil
}
func (c *Client) ReconcileCommit(ctx context.Context, r ReconcileCommitRequest) (CommitRelease, error) {
	var result CommitRelease
	if err := c.invoke(ctx, "reconcile-commit", r, &result); err != nil {
		return CommitRelease{}, err
	}
	if !result.Released {
		return CommitRelease{}, errors.New("execution commit reconciliation omitted confirmation")
	}
	return result, nil
}

func (c *Client) ListCommits(ctx context.Context) (CommitList, error) {
	var result CommitList
	if err := c.invoke(ctx, "list-commits", struct{}{}, &result); err != nil {
		return CommitList{}, err
	}
	if result.Commits == nil {
		return CommitList{}, errors.New("execution commit listing omitted intents")
	}
	for _, entry := range result.Commits {
		if !entry.Request.valid() || !entry.Guard.matches(entry.Request) || entry.Guard.Principal.Validate() != nil || entry.Guard.ExpiresAt.IsZero() || !bounded(entry.Caller, 1024) || entry.Outcome != "" || entry.ReceiptDigest != "" {
			return CommitList{}, errors.New("invalid unresolved execution commit intent")
		}
	}
	return result, nil
}
