package tools

import (
	"context"
	"fmt"
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
	return fmt.Errorf("tool %q denied by approve for me: error_kind=review_denied reason=%q model_next_action=%q", toolName, reason, "do not retry an equivalent command; choose a clearly safer approach or tell the user the risk")
}
