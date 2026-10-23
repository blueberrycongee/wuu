package appserver

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/approvefor"
	"github.com/blueberrycongee/wuu/internal/personalreview"
	"github.com/blueberrycongee/wuu/internal/providers"
)

// personalActionReviewer is an additive adapter for the opt-in Personal gate.
// No production route constructs it yet. The gate resolves host-owned evidence
// before calling it; a tool or client payload must never construct ReviewInput.
// It deliberately has no Server fallback and cannot select a Coding model.
type personalActionReviewer struct{ runner *agent.StreamRunner }

func (r personalActionReviewer) Review(ctx context.Context, input personalreview.ReviewInput) (approvefor.Decision, error) {
	messages := make([]string, 0, len(input.UserMessages)+1)
	for _, message := range input.UserMessages {
		messages = append(messages, fmt.Sprintf("Host human message %s: %s", message.ID, message.Text))
	}
	if confirmation := input.Confirmation; confirmation != nil {
		if confirmation.Choice != personalreview.ApproveOnce || confirmation.OwnerID != input.Action.OwnerID || confirmation.Digest != input.Action.Digest {
			return approvefor.Decision{Outcome: approvefor.OutcomeFailed, Reason: "Personal confirmation does not match this action"}, nil
		}
		messages = append(messages, fmt.Sprintf("Authenticated owner %s selected Approve once for action %s, digest %s, at %s. This confirms only the complete action and arguments supplied in this request.", confirmation.OwnerID, input.Action.ID, confirmation.Digest, confirmation.At.UTC().Format("2006-01-02T15:04:05Z")))
	}
	evidence, _ := json.Marshal(struct {
		Scope          personalreview.Scope    `json:"scope"`
		Effect         personalreview.Effect   `json:"effect"`
		Summary        personalreview.Summary  `json:"summary"`
		Digest         string                  `json:"action_digest"`
		HandlerVersion string                  `json:"handler_version"`
		PolicyRevision int64                   `json:"policy_revision"`
		Consent        *personalreview.Consent `json:"host_matched_consent,omitempty"`
	}{input.Action.Scope, input.Action.Effect, input.Action.Summary, input.Action.Digest, input.Action.HandlerVersion, input.Action.PolicyRevision, input.Consent})
	intent, _ := json.Marshal(messages)
	if len(evidence)+len(intent) > nativeReviewHistoryLimit {
		return approvefor.Decision{Outcome: approvefor.OutcomeFailed, Reason: "Complete Personal authorization evidence exceeds reviewer budget"}, nil
	}
	request := approvefor.Request{SessionID: input.Action.Scope.SessionID, CallID: input.Action.ID, PermissionMode: "standard", Tool: approvefor.Tool{Name: input.Action.Operation, Kind: "plugin", Risk: "high", Reason: string(evidence)}, Arguments: string(input.Action.Arguments), UserMessages: messages}
	// The native reviewer still owns its prompt, parser, timeout, current-model
	// request construction and failure semantics. Only resolved Personal evidence
	// is supplied; inherited context is not an authorization resolver.
	ctx = agent.ContextWithHistory(ctx, []providers.ChatMessage{})
	return (nativeToolReviewer{runner: r.runner}).Review(ctx, request)
}
