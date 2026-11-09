package compactionservice

import (
	"context"
	"fmt"
	"github.com/blueberrycongee/wuu/internal/providers"
	"strings"
)

// NoteAttemptRequest is shared by checkpoints and handoff briefs. The host
// validates output and reports failure; the selected provider decides retries
// and owns every word of the next inference prompt.
type NoteAttemptRequest struct {
	Messages        []providers.ChatMessage `json:"messages"`
	Plan            HandoffPlan             `json:"plan"`
	Attempt         int                     `json:"attempt"`
	PreviousFailure string                  `json:"previous_failure,omitempty"`
}
type NoteAttemptPlan struct {
	Messages []providers.ChatMessage `json:"messages"`
	Invoke   bool                    `json:"invoke"`
	Prompt   string                  `json:"prompt"`
	MaxBytes int                     `json:"max_bytes"`
}

func nativeNoteAttempt(in NoteAttemptRequest) (NoteAttemptPlan, error) {
	if in.Attempt < 0 {
		return NoteAttemptPlan{}, fmt.Errorf("attempt must be nonnegative")
	}
	if in.Attempt >= 3 {
		return NoteAttemptPlan{Messages: []providers.ChatMessage{}}, nil
	}
	if strings.TrimSpace(in.Plan.Prompt) == "" || in.Plan.MaxBytes < 0 {
		return NoteAttemptPlan{}, fmt.Errorf("invalid original note plan")
	}
	out := NoteAttemptPlan{Invoke: true, Prompt: in.Plan.Prompt, MaxBytes: in.Plan.MaxBytes}
	if in.Plan.MaxBytes > 0 {
		target := max(1, in.Plan.MaxBytes/2>>in.Attempt)
		out.Prompt += fmt.Sprintf("\nWrite a complete replacement document targeting at most %d UTF-8 bytes, with an absolute limit of %d bytes. Use concise state and exact history references rather than copied detail.", target, in.Plan.MaxBytes)
	}
	if in.PreviousFailure != "" {
		out.Prompt += fmt.Sprintf("\nThe previous attempt was rejected: %s. Regenerate a substantially shorter complete document from the original source; do not continue the rejected output.", in.PreviousFailure)
	}
	out.Messages = providers.CloneChatMessages(in.Messages)
	for i := range out.Messages {
		if out.Messages[i].Seq > 0 {
			out.Messages[i].Content = fmt.Sprintf("[History Seq %d]\n%s", out.Messages[i].Seq, out.Messages[i].Content)
		}
	}
	out.Messages = append(out.Messages, providers.ChatMessage{Role: "user", Content: strings.TrimSpace(out.Prompt), Hidden: true, ReadOnly: true, Origin: "internal", Cause: "compaction_note_fork"})
	return out, nil
}

func (c *Client) NoteAttempt(ctx context.Context, in NoteAttemptRequest) (NoteAttemptPlan, error) {
	var out struct {
		Messages *[]providers.ChatMessage `json:"messages"`
		Invoke   *bool                    `json:"invoke"`
		Prompt   string                   `json:"prompt"`
		MaxBytes int                      `json:"max_bytes"`
	}
	if err := c.invoke(ctx, "note-attempt", in, &out); err != nil {
		return NoteAttemptPlan{}, err
	}
	if out.Messages == nil || out.Invoke == nil || out.MaxBytes < 0 || (*out.Invoke && strings.TrimSpace(out.Prompt) == "") {
		return NoteAttemptPlan{}, fmt.Errorf("invalid note attempt plan")
	}
	return NoteAttemptPlan{Messages: *out.Messages, Invoke: *out.Invoke, Prompt: out.Prompt, MaxBytes: out.MaxBytes}, nil
}
