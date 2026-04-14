package hooks

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// PromptModelClient is the minimal interface a PromptHook needs to
// query an LLM. It matches the subset of providers.Client used here
// so the hooks package doesn't import the provider package.
type PromptModelClient interface {
	ChatJSON(ctx context.Context, model, system, user string) (string, error)
}

// PromptHook evaluates a condition by sending a prompt to a small/fast
// LLM and parsing the structured {ok, reason} response. Aligned with
// Claude Code's execPromptHook — lightweight LLM-as-middleware.
//
// If ok is true the hook passes. If ok is false the hook blocks,
// and the reason is propagated as both the block reason and as
// additional_context so the main model can see the evaluation.
type PromptHook struct {
	PromptTemplate string        // may contain $ARGUMENTS placeholder
	Model          string        // model name; empty = use default
	Timeout        time.Duration // per-call timeout
	Client         PromptModelClient
}

// Type returns the discriminator for this hook variant.
func (h *PromptHook) Type() string { return "prompt" }

// promptResult is the structured response we ask the LLM to produce.
type promptResult struct {
	OK     bool   `json:"ok"`
	Reason string `json:"reason,omitempty"`
}

// Execute sends the evaluation prompt to the model and interprets the
// structured response. $ARGUMENTS in the template is replaced with the
// JSON-encoded hook input.
func (h *PromptHook) Execute(ctx context.Context, input *Input) (*Output, error) {
	if h.Client == nil {
		return &Output{}, nil // no model client → pass through
	}

	timeout := h.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Substitute $ARGUMENTS with the JSON-encoded input.
	inputJSON, _ := json.Marshal(input)
	userPrompt := strings.ReplaceAll(h.PromptTemplate, "$ARGUMENTS", string(inputJSON))

	systemPrompt := "You are evaluating a hook condition. Respond with a JSON object: {\"ok\": true/false, \"reason\": \"...\"} where ok=true means the condition is met and the operation should proceed, ok=false means it should be blocked. The reason field explains your decision."

	raw, err := h.Client.ChatJSON(runCtx, h.Model, systemPrompt, userPrompt)
	if err != nil {
		// Model errors are non-blocking — treat as pass.
		return &Output{
			Reason: fmt.Sprintf("prompt hook evaluation failed: %v", err),
		}, nil
	}

	var result promptResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		// Parse failure is non-blocking.
		return &Output{
			Reason: fmt.Sprintf("prompt hook returned invalid JSON: %s", raw),
		}, nil
	}

	out := &Output{
		Context: result.Reason, // always propagate reason as context
	}

	if !result.OK {
		out.Decision = "block"
		out.Reason = result.Reason
		if out.Reason == "" {
			out.Reason = "prompt hook evaluation returned ok=false"
		}
		return out, fmt.Errorf("%w: %s", ErrBlocked, out.Reason)
	}

	return out, nil
}
