// Package compactionservice owns selected context replacement and handoff policy.
// Providers propose content; the host validates and durably commits it.
package compactionservice

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/blueberrycongee/wuu/internal/compact"
	"github.com/blueberrycongee/wuu/internal/providers"
)

const ServiceName = "agent.compaction"
const NativeModule = "wuu.compaction.default"

type DescribeRequest struct {
	ContextTokens          int `json:"context_tokens"`
	InputTokens            int `json:"input_tokens"`
	OutputReserveTokens    int `json:"output_reserve_tokens"`
	CompactThresholdTokens int `json:"compact_threshold_tokens"`
}
type Policy struct {
	Key            string                  `json:"key"`
	NotesEnabled   bool                    `json:"notes_enabled"`
	ContextWindows bool                    `json:"context_windows"`
	TargetTokens   int                     `json:"target_tokens"`
	Guidance       []providers.ChatMessage `json:"guidance"`
}

// Nested budget/options and transcript records preserve their existing Go JSON
// spelling. They are policy data, not credential or caller authority.
type Request struct {
	PolicyKey string                  `json:"policy_key,omitempty"`
	Model     string                  `json:"model"`
	Messages  []providers.ChatMessage `json:"messages"`
	Budget    compact.Budget          `json:"budget"`
	Options   compact.NativeOptions   `json:"options"`
	Hint      BudgetHint              `json:"hint"`
}
type BudgetHint struct {
	Reason               string `json:"reason"`
	LastSuccessfulTokens int    `json:"last_successful_tokens"`
	FailedRequestTokens  int    `json:"failed_request_tokens"`
	TargetTotalTokens    int    `json:"target_total_tokens"`
}
type WindowRequest struct {
	PolicyKey    string                     `json:"policy_key,omitempty"`
	Messages     []providers.ChatMessage    `json:"messages"`
	HistoryHead  int                        `json:"history_head"`
	FixedTokens  int                        `json:"fixed_tokens"`
	TargetTokens int                        `json:"target_tokens"`
	MediaInput   providers.MediaInputPolicy `json:"media_input"`
}
type HandoffRequest struct {
	PolicyKey               string                  `json:"policy_key,omitempty"`
	Model                   string                  `json:"model,omitempty"`
	PreviousCoveredMessages int                     `json:"previous_covered_messages,omitempty"`
	Messages                []providers.ChatMessage `json:"messages"`
	PreviousNote            string                  `json:"previous_note"`
	Intent                  string                  `json:"intent"`
	SourceSessionID         string                  `json:"source_session_id"`
	SourceThroughSeq        int                     `json:"source_through_seq"`
}
type HandoffPlan struct {
	Prompt   string `json:"prompt"`
	MaxBytes int    `json:"max_bytes"`
}
type Replacement struct {
	Messages []providers.ChatMessage `json:"messages"`
}

// Client keeps one captured service callback. It never retries or selects a
// different provider after a failure, including unsupported policies.
type Client struct {
	call func(context.Context, string, json.RawMessage) (json.RawMessage, error)
}

func NewClient(call func(context.Context, string, json.RawMessage) (json.RawMessage, error)) *Client {
	return &Client{call: call}
}
func (c *Client) invoke(ctx context.Context, method string, input any, output any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return err
	}
	raw, err = c.call(ctx, method, raw)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, output)
}
func (c *Client) Describe(ctx context.Context, in DescribeRequest) (Policy, error) {
	var out struct {
		Key            *string                  `json:"key"`
		NotesEnabled   *bool                    `json:"notes_enabled"`
		ContextWindows *bool                    `json:"context_windows"`
		TargetTokens   *int                     `json:"target_tokens"`
		Guidance       *[]providers.ChatMessage `json:"guidance"`
	}
	if err := c.invoke(ctx, "describe", in, &out); err != nil {
		return Policy{}, err
	}
	if out.Key == nil || *out.Key == "" || out.NotesEnabled == nil || out.ContextWindows == nil || out.TargetTokens == nil || out.Guidance == nil || (*out.ContextWindows && *out.TargetTokens <= 0) {
		return Policy{}, fmt.Errorf("invalid compaction policy result")
	}
	return Policy{Key: *out.Key, NotesEnabled: *out.NotesEnabled, ContextWindows: *out.ContextWindows, TargetTokens: *out.TargetTokens, Guidance: *out.Guidance}, nil
}
func (c *Client) replacement(ctx context.Context, method string, in any) (Replacement, error) {
	var out struct {
		Messages *[]providers.ChatMessage `json:"messages"`
	}
	if err := c.invoke(ctx, method, in, &out); err != nil {
		return Replacement{}, err
	}
	if out.Messages == nil {
		return Replacement{}, fmt.Errorf("invalid compaction result: messages required")
	}
	return Replacement{Messages: *out.Messages}, nil
}
func (c *Client) Compact(ctx context.Context, in Request) (Replacement, error) {
	return c.replacement(ctx, "compact", in)
}
func (c *Client) Window(ctx context.Context, in WindowRequest) (Replacement, error) {
	return c.replacement(ctx, "window", in)
}
func (c *Client) Handoff(ctx context.Context, in HandoffRequest) (HandoffPlan, error) {
	var out HandoffPlan
	if err := c.invoke(ctx, "handoff", in, &out); err != nil {
		return out, err
	}
	if out.Prompt == "" || out.MaxBytes < 0 {
		return HandoffPlan{}, fmt.Errorf("invalid handoff plan")
	}
	return out, nil
}
