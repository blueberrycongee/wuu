package compactionservice

import (
	"context"
	"fmt"
	"strings"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
)

type NoteRequest struct {
	InputTokens             int                     `json:"input_tokens,omitempty"`
	Force                   bool                    `json:"force,omitempty"`
	PolicyKey               string                  `json:"policy_key"`
	Model                   string                  `json:"model"`
	Messages                []providers.ChatMessage `json:"messages"`
	Delta                   []providers.ChatMessage `json:"delta,omitempty"`
	PreviousNote            string                  `json:"previous_note,omitempty"`
	PreviousCoveredMessages int                     `json:"previous_covered_messages,omitempty"`
	Note                    string                  `json:"note,omitempty"`
	CoveredMessages         int                     `json:"covered_messages,omitempty"`
}
type NotePlan struct {
	Messages        []providers.ChatMessage `json:"messages"`
	CoveredMessages int                     `json:"covered_messages"`
	Due             bool                    `json:"due"`
	Prompt          string                  `json:"prompt"`
	IntervalTokens  int                     `json:"interval_tokens"`
	MaxBytes        int                     `json:"max_bytes"`
}
type NoteReplacement struct {
	Messages        []providers.ChatMessage `json:"messages"`
	CoveredMessages int                     `json:"covered_messages"`
}
type Failure struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
type RecoveryRequest struct {
	Request
	Failure Failure `json:"failure"`
}

type capabilityHostKey struct{}

// WithCapabilityHost supplies the retained host's existing contributions to the
// native policy only. It does not select a host or create a second registry.
func WithCapabilityHost(ctx context.Context, host *pluginhost.Host) context.Context {
	return context.WithValue(ctx, capabilityHostKey{}, host)
}

func legacyPolicy(ctx context.Context, key string) (*pluginhost.Host, *pluginhost.RegisteredCapability, error) {
	host, _ := ctx.Value(capabilityHostKey{}).(*pluginhost.Host)
	var selected *pluginhost.RegisteredCapability
	if host != nil {
		for _, candidate := range host.Capabilities(pluginhost.CapabilityAgentCompaction) {
			if candidate.Descriptor.Priority >= 0 && (selected == nil || candidate.Descriptor.Priority > selected.Descriptor.Priority) {
				copy := candidate
				selected = &copy
			}
		}
	}
	actual := "builtin:context-window"
	if selected != nil {
		actual = selected.PluginID + ":" + selected.Descriptor.ID
	}
	if key != "" && key != actual {
		return nil, nil, &pluginhost.HostServiceError{Code: "policy_binding_changed", Message: "captured compaction contribution is unavailable"}
	}
	return host, selected, nil
}

func nativeDescribe(ctx context.Context, in DescribeRequest) (Policy, error) {
	_, selected, err := legacyPolicy(ctx, "")
	if err != nil {
		return Policy{}, err
	}
	policy := Describe(in)
	policy.Key = "builtin:context-window"
	if selected != nil {
		policy.Key = selected.PluginID + ":" + selected.Descriptor.ID
		policy.NotesEnabled = selected.Descriptor.Version == 2
		policy.ContextWindows = selected.Descriptor.Version >= 3
		if !policy.ContextWindows {
			policy.Guidance = []providers.ChatMessage{}
			policy.TargetTokens = 0
		}
	}
	return policy, nil
}

func nativeNote(ctx context.Context, in NoteRequest, operation string) (pluginhost.CompactionOutput, error) {
	host, selected, err := legacyPolicy(ctx, in.PolicyKey)
	if err != nil {
		return pluginhost.CompactionOutput{}, err
	}
	if selected == nil || selected.Descriptor.Version != 2 {
		return pluginhost.CompactionOutput{}, &pluginhost.HostServiceError{Code: "note_unsupported", Message: "selected policy does not provide context notes"}
	}
	delta := in.Delta
	if strings.TrimSpace(in.PreviousNote) == "" {
		delta = nil
	}
	request := pluginhost.CompactionInput{Operation: operation, Model: in.Model, Messages: in.Messages, Delta: delta, PreviousNote: in.PreviousNote, PreviousCoveredMessages: in.PreviousCoveredMessages, Note: in.Note, CoveredMessages: in.CoveredMessages}
	var out pluginhost.CompactionOutput
	if err := host.InvokeCapability(ctx, *selected, request, &out); err != nil {
		return out, err
	}
	return out, nil
}

func nativeHandoff(ctx context.Context, in HandoffRequest) (HandoffPlan, error) {
	host, selected, err := legacyPolicy(ctx, in.PolicyKey)
	if err != nil {
		return HandoffPlan{}, err
	}
	if selected == nil {
		return Handoff(in)
	}
	if selected.Descriptor.Version < 2 {
		return HandoffPlan{}, &pluginhost.HostServiceError{Code: "note_unsupported", Message: "selected policy does not provide handoff briefs"}
	}
	var out pluginhost.CompactionOutput
	err = host.InvokeCapability(ctx, *selected, pluginhost.CompactionInput{Operation: "handoff_brief_plan", Model: in.Model, Messages: in.Messages, PreviousNote: in.PreviousNote, PreviousCoveredMessages: in.PreviousCoveredMessages, Intent: in.Intent, SourceSessionID: in.SourceSessionID, SourceThroughSeq: in.SourceThroughSeq}, &out)
	if err != nil {
		return HandoffPlan{}, err
	}
	if strings.TrimSpace(out.NotePrompt) == "" {
		return HandoffPlan{}, &pluginhost.HostServiceError{Code: "note_unsupported", Message: "handoff plan is unavailable"}
	}
	return HandoffPlan{Prompt: out.NotePrompt, MaxBytes: out.MaxNoteBytes}, nil
}

func (c *Client) NotePlan(ctx context.Context, in NoteRequest) (NotePlan, error) {
	var out NotePlan
	if err := c.invoke(ctx, "note-plan", in, &out); err != nil {
		return out, err
	}
	if out.Messages == nil || out.CoveredMessages < 0 || out.CoveredMessages > len(in.Messages) || (out.Due && strings.TrimSpace(out.Prompt) == "") {
		return NotePlan{}, fmt.Errorf("invalid note plan")
	}
	return out, nil
}
func (c *Client) CompactNote(ctx context.Context, in NoteRequest) (NoteReplacement, error) {
	var out NoteReplacement
	if err := c.invoke(ctx, "compact-note", in, &out); err != nil {
		return out, err
	}
	if out.Messages == nil || out.CoveredMessages <= 0 || out.CoveredMessages > len(out.Messages) {
		return NoteReplacement{}, fmt.Errorf("invalid note replacement coverage")
	}
	return out, nil
}
func (c *Client) Recover(ctx context.Context, in RecoveryRequest) (Replacement, error) {
	return c.replacement(ctx, "recover", in)
}
