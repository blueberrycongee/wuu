// Package access defines resource authorization, independently of authentication
// and of tool execution permissions. Every decision covers every resource.
package access

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/blueberrycongee/wuu/internal/principal"
	"strings"
)

const ServiceName = "resource.authorization"
const ServiceMajor = 1
const NativeModule = "wuu.resource-policy.default"
const ConsumerModule = "wuu.resource-access"

type Resource struct {
	Kind string `json:"kind"`
	ID   string `json:"id,omitempty"`
}
type Request struct {
	Principal principal.Identity `json:"principal"`
	// Owner is transport-established authority, never accepted from RPC params.
	Owner      bool                       `json:"owner"`
	Action     string                     `json:"action"`
	Resources  []Resource                 `json:"resources"`
	Attributes map[string]json.RawMessage `json:"attributes,omitempty"`
}
type Decision struct {
	Allow  bool   `json:"allow"`
	Reason string `json:"reason,omitempty"`
}
type Policy interface {
	Authorize(context.Context, Request) (Decision, error)
}
type PolicyFunc func(context.Context, Request) (Decision, error)

func (f PolicyFunc) Authorize(ctx context.Context, r Request) (Decision, error) { return f(ctx, r) }

type OwnerOnly struct{}

func (OwnerOnly) Authorize(_ context.Context, r Request) (Decision, error) {
	return Decision{Allow: r.Owner, Reason: "owner-required"}, nil
}

type Denied struct{ Action, Reason string }

func (e *Denied) Error() string { return fmt.Sprintf("access denied for %s: %s", e.Action, e.Reason) }
func (r Request) Validate() error {
	if err := r.Principal.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(r.Action) == "" || len(r.Resources) == 0 {
		return errors.New("action and resources are required")
	}
	for _, resource := range r.Resources {
		if strings.TrimSpace(resource.Kind) == "" || (resource.Kind != "global" && strings.TrimSpace(resource.ID) == "") || (resource.Kind == "global" && resource.ID != "") {
			return errors.New("invalid resource")
		}
	}
	return nil
}

// Check never caches grants. Policy failure, cancellation, invalid resources,
// missing providers and negative decisions all deny access.
func Check(ctx context.Context, policy Policy, request Request) error {
	if err := request.Validate(); err != nil {
		return &Denied{request.Action, "resource-invalid"}
	}
	if err := ctx.Err(); err != nil {
		return &Denied{request.Action, "cancelled"}
	}
	if policy == nil {
		return &Denied{request.Action, "policy-unavailable"}
	}
	decision, err := policy.Authorize(ctx, request)
	if err != nil {
		return &Denied{request.Action, "policy-failed"}
	}
	if err := ctx.Err(); err != nil {
		return &Denied{request.Action, "cancelled"}
	}
	if !decision.Allow {
		reason := decision.Reason
		if reason == "" {
			reason = "no-allow"
		}
		return &Denied{request.Action, reason}
	}
	return nil
}

type attributesKey struct{}

// WithAttributes captures normalized requested effects, not identity claims.
// Adapters must omit prompts, credentials and content. Unknown business actions
// may define namespaced effect keys without changing the authentication model.
func WithAttributes(ctx context.Context, attributes map[string]json.RawMessage) context.Context {
	copy := make(map[string]json.RawMessage, len(attributes))
	for key, value := range attributes {
		copy[key] = append(json.RawMessage(nil), value...)
	}
	return context.WithValue(ctx, attributesKey{}, copy)
}
func AttributesFromContext(ctx context.Context) map[string]json.RawMessage {
	original, _ := ctx.Value(attributesKey{}).(map[string]json.RawMessage)
	copy := make(map[string]json.RawMessage, len(original))
	for key, value := range original {
		copy[key] = append(json.RawMessage(nil), value...)
	}
	return copy
}
