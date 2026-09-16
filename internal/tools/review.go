package tools

import (
	"context"
	"strings"

	"github.com/blueberrycongee/wuu/internal/approvefor"
	"github.com/blueberrycongee/wuu/internal/providers"
)

// Reviewer is the optional host policy that may allow a high-risk native tool
// call after the workspace boundary has already permitted it.
type Reviewer interface {
	Review(ctx context.Context, request approvefor.Request) (approvefor.Decision, error)
}

func (t *Toolkit) reviewToolCall(ctx context.Context, info ToolInfo, call providers.ToolCall) error {
	if t == nil || !t.approveForMe || t.env == nil {
		return nil
	}
	req := approvefor.Request{
		SessionID:      t.env.SessionID,
		CallID:         strings.TrimSpace(call.ID),
		PermissionMode: t.env.PermissionMode,
		Tool: approvefor.Tool{
			Name:        info.Name,
			Kind:        string(info.Kind),
			ReadOnly:    info.ReadOnly,
			Destructive: info.Destructive,
			Risk:        string(info.Risk),
			Reason:      info.Reason,
		},
		Arguments: call.Arguments,
		CWD:       t.env.RootDir,
	}
	if reason := approvefor.HardDenyReason(req); reason != "" {
		return reviewDenied(info.Name, reason)
	}
	if !approvefor.NeedsReview(req) {
		return nil
	}
	if t.reviewer == nil {
		return reviewDenied(info.Name, "approve for me is enabled but no reviewer is available")
	}
	decision, err := t.reviewer.Review(ctx, req)
	if err != nil {
		return reviewDenied(info.Name, "reviewer unavailable")
	}
	switch strings.TrimSpace(decision.Outcome) {
	case approvefor.OutcomeAllow:
		return nil
	default:
		return reviewDenied(info.Name, decision.Reason)
	}
}
