package tools

import (
	"context"
	"fmt"
	"github.com/blueberrycongee/wuu/internal/providers"
	"strings"
)

type AuthorizationRequest struct {
	SessionID      string
	ActorID        string
	CWD            string
	PermissionMode string
	Tool           ToolInfo
	Arguments      string
}

type AuthorizationDecision struct {
	Outcome string
	Reason  string
}

// Authorizer is an optional policy seam that may further restrict a tool call.
// The workspace boundary remains authoritative and cannot be elevated by an
// authorizer decision.
type Authorizer interface {
	Authorize(ctx context.Context, request AuthorizationRequest) (AuthorizationDecision, error)
}

func authorizationDenied(toolName, reason string) error {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "the configured authorization provider denied this action"
	}
	return fmt.Errorf("tool %q denied by authorization provider: error_kind=authorization_denied reason=%q", toolName, reason)
}

func reviewDenied(toolName, reason string) error {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "approve for me denied this action"
	}
	return reviewBlocked(toolName, "review_denied", reason, "do not bypass this decision or retry an equivalent operation; choose a materially safer alternative or explain the exact action, target and risk in the conversation and wait for explicit user approval; after informed approval submit that specific action for fresh review, never skip the permission boundary")
}

func reviewBlocked(toolName, kind, reason, next string) error {
	return fmt.Errorf("tool %q blocked by approve for me: error_kind=%s action_executed=false reason=%q model_next_action=%q", toolName, kind, reason, next)
}

func reviewFailed(toolName, reason string) error {
	return reviewBlocked(toolName, "review_failed", reason, "automatic review could not be completed; this is not a determination that the action is unsafe or that the user denied permission; do not bypass the check or repeatedly retry; explain the failure in the conversation and resolve it or ask the user for guidance before a fresh review")
}

// AtomicAuthorizer fences its exact selected policy decision through the effect.
// Snapshot-only providers remain usable for ordinary calls, but cannot dispatch
// a retained reviewed action. Unsupported fencing must not call use.
type AtomicAuthorizer interface {
	Authorizer
	SupportsAtomicAuthorization() bool
	WithAuthorization(context.Context, AuthorizationRequest, func(context.Context) error) error
}
type fileAuthorizationContextKey struct{}
type fileAuthorization struct {
	toolkit *Toolkit
	request AuthorizationRequest
}

func (t *Toolkit) authorizationRequest(info ToolInfo, call providers.ToolCall) AuthorizationRequest {
	return AuthorizationRequest{SessionID: t.env.SessionID, ActorID: t.env.AgentID, CWD: t.env.RootDir, PermissionMode: t.env.PermissionMode, Tool: info, Arguments: call.Arguments}
}
func (t *Toolkit) hasFileAuthorization(ctx context.Context, info ToolInfo, call providers.ToolCall) bool {
	proof, ok := ctx.Value(fileAuthorizationContextKey{}).(fileAuthorization)
	return ok && proof.toolkit == t && proof.request == t.authorizationRequest(info, call) && ctx.Err() == nil
}
