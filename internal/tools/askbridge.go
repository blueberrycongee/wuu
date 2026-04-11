package tools

import (
	"context"
	"fmt"
	"strings"
)

// AskUserBridge is the interface the ask_user tool uses to hand a
// multiple-choice question off to the human user and wait for the
// answer. The tool call payload deserializes into AskUserRequest,
// the tool handler calls AskUser, and the returned AskUserResponse
// is marshaled back to the model as the tool result.
//
// The actual implementation lives in internal/tui (so it can render
// a modal dialog in the Bubble Tea app), but the Toolkit only ever
// touches this interface. Workers — which receive a Toolkit without
// a bridge set — get a clear "bridge not configured" error if they
// try to call ask_user, which is the intended isolation: only the
// main agent talking to a live TUI is allowed to interrupt the human.
type AskUserBridge interface {
	AskUser(ctx context.Context, req AskUserRequest) (AskUserResponse, error)
}

// AskUserRequest is the shape the tool call decodes into and the
// payload the bridge sees.
type AskUserRequest struct {
	Questions []AskUserQuestion `json:"questions"`
}

// AskUserQuestion is one multiple-choice question. The ask_user tool
// accepts 1-4 questions per call, each with 2-4 options. An "Other"
// escape hatch is appended by the TUI for single-select questions so
// the user is never forced into the model's preselected option space.
type AskUserQuestion struct {
	// Question is the full question text the user reads.
	Question string `json:"question"`

	// Header is a short chip label (<= 12 chars) surfaced in the
	// nav bar when stepping through multiple questions.
	Header string `json:"header"`

	// Options is the choice list (2-4 items). "Other" is NOT part
	// of this slice — the TUI appends it automatically for
	// single-select questions.
	Options []AskUserOption `json:"options"`

	// MultiSelect allows the user to toggle multiple options. When
	// false, the user picks exactly one option per question.
	MultiSelect bool `json:"multi_select,omitempty"`
}

// AskUserOption is one choice inside a question.
type AskUserOption struct {
	// Label is the short display text (1-5 words).
	Label string `json:"label"`

	// Description explains what this option means or what its
	// trade-offs are. Rendered under the label in the option list.
	Description string `json:"description"`

	// Preview is optional markdown content rendered side-by-side
	// with the option list when any option in the question has one.
	// Intended for code snippets, ASCII mockups, or diagrams the
	// user needs to visually compare.
	Preview string `json:"preview,omitempty"`
}

// AskUserResponse is what comes back from the bridge. Answers is
// keyed by question text; each value is the selected option's
// label, a comma-separated list of labels for multi-select, or
// the user's free-text input for "Other".
type AskUserResponse struct {
	Answers map[string]string `json:"answers"`

	// Cancelled is set when the user dismissed the dialog (Esc or
	// Ctrl+C) without answering. The tool handler turns this into
	// an error so the model learns the answer is not available and
	// can adjust its plan instead of acting on nothing.
	Cancelled bool `json:"cancelled,omitempty"`
}

// Validate enforces the basic schema constraints the tool advertises
// in its input_schema. Called from the tool handler after decoding
// so the bridge never sees malformed requests.
func (r *AskUserRequest) Validate() error {
	if len(r.Questions) == 0 {
		return fmt.Errorf("ask_user: at least one question is required")
	}
	if len(r.Questions) > 4 {
		return fmt.Errorf("ask_user: at most 4 questions allowed (got %d)", len(r.Questions))
	}
	seenQ := make(map[string]struct{}, len(r.Questions))
	for i, q := range r.Questions {
		if strings.TrimSpace(q.Question) == "" {
			return fmt.Errorf("ask_user: question %d has empty question text", i+1)
		}
		if _, dup := seenQ[q.Question]; dup {
			return fmt.Errorf("ask_user: duplicate question text %q (question texts must be unique — they are used as map keys in the response)", q.Question)
		}
		seenQ[q.Question] = struct{}{}

		if strings.TrimSpace(q.Header) == "" {
			return fmt.Errorf("ask_user: question %d has empty header (short chip label required)", i+1)
		}
		if len([]rune(q.Header)) > 12 {
			return fmt.Errorf("ask_user: question %d header %q too long (max 12 chars)", i+1, q.Header)
		}
		if len(q.Options) < 2 {
			return fmt.Errorf("ask_user: question %d needs at least 2 options (got %d)", i+1, len(q.Options))
		}
		if len(q.Options) > 4 {
			return fmt.Errorf("ask_user: question %d has too many options (max 4, got %d)", i+1, len(q.Options))
		}
		seenLabel := make(map[string]struct{}, len(q.Options))
		for j, opt := range q.Options {
			if strings.TrimSpace(opt.Label) == "" {
				return fmt.Errorf("ask_user: question %d option %d has empty label", i+1, j+1)
			}
			if strings.EqualFold(strings.TrimSpace(opt.Label), "other") {
				return fmt.Errorf("ask_user: question %d lists %q explicitly; an \"Other\" escape hatch is appended automatically — remove it", i+1, opt.Label)
			}
			if _, dup := seenLabel[opt.Label]; dup {
				return fmt.Errorf("ask_user: question %d has duplicate option label %q", i+1, opt.Label)
			}
			seenLabel[opt.Label] = struct{}{}
		}
	}
	return nil
}
