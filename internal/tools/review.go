package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/blueberrycongee/wuu/internal/approvefor"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/statepath"
)

// Reviewer is the optional host policy that may allow a high-risk native tool
// call after the workspace boundary has already permitted it.
type Reviewer interface {
	Review(ctx context.Context, request approvefor.Request) (approvefor.Decision, error)
}

type reviewAuthority struct {
	SessionID, PermissionMode, CWD string
	Enabled                        bool
	Reviewer                       Reviewer
}

func (t *Toolkit) reviewToolCall(ctx context.Context, info ToolInfo, call providers.ToolCall) error {
	if t == nil || t.env == nil {
		return nil
	}
	return reviewAuthorizedToolCall(ctx, reviewAuthority{SessionID: t.env.SessionID, PermissionMode: t.env.PermissionMode, CWD: t.env.RootDir, Enabled: t.approveForMe, Reviewer: t.reviewer}, info, call)
}

// reviewAuthorizedToolCall is shared by native tools and the host process
// gateway; replacing the tool registry cannot bypass an enabled review policy.
func reviewAuthorizedToolCall(ctx context.Context, authority reviewAuthority, info ToolInfo, call providers.ToolCall) error {
	if !authority.Enabled {
		return nil
	}
	req := approvefor.Request{
		SessionID:      authority.SessionID,
		CallID:         strings.TrimSpace(call.ID),
		PermissionMode: authority.PermissionMode,
		Tool: approvefor.Tool{
			Name:        info.Name,
			Kind:        string(info.Kind),
			ReadOnly:    info.ReadOnly,
			Destructive: info.Destructive,
			Risk:        string(info.Risk),
			Reason:      info.Reason,
		},
		Arguments:       call.Arguments,
		CWD:             authority.CWD,
		ReferencedPaths: reviewShellPaths(call, authority.CWD),
	}
	if reason := approvefor.HardDenyReason(req); reason != "" {
		return reviewBlocked(info.Name, "review_forbidden", reason, "this is a hard permission boundary; do not retry, bypass it or ask for conversational approval to override it; explain the restriction and offer a permitted alternative")
	}
	if !approvefor.NeedsReview(req) {
		return nil
	}
	if authority.Reviewer == nil {
		return reviewFailed(info.Name, "approve for me is enabled but no reviewer is available")
	}
	decision, err := authority.Reviewer.Review(ctx, req)
	if err != nil {
		return reviewFailed(info.Name, "reviewer unavailable")
	}
	switch strings.TrimSpace(decision.Outcome) {
	case approvefor.OutcomeAllow:
		return nil
	case approvefor.OutcomeDeny:
		return reviewDenied(info.Name, decision.Reason)
	case approvefor.OutcomeUnsure:
		return reviewBlocked(info.Name, "review_needs_context", decision.Reason, "review could not establish sufficient safety or authorization; do not repeat the same request; explain the exact action, target and risk in the conversation, ask for missing information or explicit approval, then submit the specific action for fresh review; a vague continue is not blanket approval")
	case approvefor.OutcomeCancelled:
		return reviewBlocked(info.Name, "review_cancelled", decision.Reason, "the owning execution was cancelled; no safety verdict was reached and the action was not executed; do not automatically retry")
	default:
		return reviewFailed(info.Name, decision.Reason)
	}
}

// Reuse the shell classifier's parser to check literal operands, rather than
// searching arbitrary document content for credential filenames. Dynamic shell
// expressions still require the ordinary high-risk review.
func reviewShellPaths(call providers.ToolCall, root string) []string {
	if call.Name != "bash" {
		return nil
	}
	var args bashArgs
	if err := decodeArgs(call.Arguments, &args); err != nil {
		return nil
	}
	resolve := func(path string) string {
		for _, prefix := range []string{"$WUU_HOME/", "${WUU_HOME}/"} {
			if strings.HasPrefix(path, prefix) {
				if home, err := statepath.Home(""); err == nil {
					path = filepath.Join(home, strings.TrimPrefix(path, prefix))
				}
				break
			}
		}
		for _, prefix := range []string{"~/", "$HOME/", "${HOME}/"} {
			if strings.HasPrefix(path, prefix) {
				if home, err := os.UserHomeDir(); err == nil {
					path = filepath.Join(home, strings.TrimPrefix(path, prefix))
				}
				break
			}
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(root, path)
		}
		return path
	}
	if args.CWD != "" {
		root = resolve(args.CWD)
	}
	segments, ok := splitShellCommandSegmentsQuoted(args.Command)
	if !ok {
		return nil
	}
	var paths []string
	for _, segment := range segments {
		fields, ok := splitShellFields(segment)
		if !ok || len(fields) == 0 {
			continue
		}
		for _, field := range fields[1:] {
			paths = append(paths, resolve(field))
		}
		if fields[0] == "cd" && len(fields) == 2 {
			root = resolve(fields[1])
		}
	}
	return paths
}
