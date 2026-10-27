package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/blueberrycongee/wuu/internal/compactionservice"
	"github.com/blueberrycongee/wuu/internal/providers"
)

var errCompactionNoteTruncated = errors.New("context note fork output was truncated")

// boundedCompactionNoteFork enforces the same output contract for background
// checkpoints and handoff briefs. Never install a byte-sliced or truncated
// document: either can silently remove the task's constraints or next steps.
func boundedCompactionNoteFork(fork CompactionNoteFork) CompactionNoteFork {
	return func(ctx context.Context, history []providers.ChatMessage, plan CompactionNotePlan) (CompactionNoteForkResult, error) {
		var usage *providers.TokenUsage
		var outputErr error
		// Retries share the caller's deadline and original snapshot. Do not feed
		// rejected prose back as source facts or grow an already bounded input.
		for attempt := 0; attempt < 3; attempt++ {
			if err := ctx.Err(); err != nil {
				return CompactionNoteForkResult{Usage: usage}, err
			}
			requestPlan := plan
			if plan.MaxBytes > 0 {
				target := max(1, plan.MaxBytes/2>>attempt)
				requestPlan.Prompt += fmt.Sprintf("\nWrite a complete replacement document targeting at most %d UTF-8 bytes, with an absolute limit of %d bytes. Use concise state and exact history references rather than copied detail.", target, plan.MaxBytes)
			}
			if outputErr != nil {
				requestPlan.Prompt += fmt.Sprintf("\nThe previous attempt was rejected: %v. Regenerate a substantially shorter complete document from the original source; do not continue the rejected output.", outputErr)
			}
			result, err := fork(ctx, history, requestPlan)
			if result.Usage != nil {
				if usage == nil {
					usage = &providers.TokenUsage{}
				}
				usage.InputTokens += result.Usage.InputTokens
				usage.OutputTokens += result.Usage.OutputTokens
				usage.CacheReadTokens += result.Usage.CacheReadTokens
				usage.CacheCreationTokens += result.Usage.CacheCreationTokens
				usage.CacheCreationUnknown = usage.CacheCreationUnknown || result.Usage.CacheCreationUnknown
			}
			if ctx.Err() != nil {
				return CompactionNoteForkResult{Usage: usage}, ctx.Err()
			}
			if err != nil && !errors.Is(err, errCompactionNoteTruncated) {
				return CompactionNoteForkResult{Usage: usage}, err
			}
			markdown := strings.TrimSpace(result.Markdown)
			outputErr = err
			if err == nil {
				if markdown == "" {
					return CompactionNoteForkResult{Usage: usage}, errors.New("context note fork returned empty Markdown")
				}
				if plan.MaxBytes <= 0 || len(markdown) <= plan.MaxBytes {
					return CompactionNoteForkResult{Markdown: markdown, Usage: usage}, nil
				}
				outputErr = fmt.Errorf("compaction note is %d UTF-8 bytes; limit is %d", len(markdown), plan.MaxBytes)
			}
		}
		return CompactionNoteForkResult{Usage: usage}, fmt.Errorf("context note output rejected after 3 attempts: %w", outputErr)
	}
}

// The selected provider owns retry admission and the complete inference input.
// Host checks only cancellation, model output validity and usage accounting.
func selectedCompactionNoteFork(client *compactionservice.Client, fork CompactionNoteFork) CompactionNoteFork {
	return func(ctx context.Context, history []providers.ChatMessage, plan CompactionNotePlan) (CompactionNoteForkResult, error) {
		var usage *providers.TokenUsage
		var failure string
		for attempt := 0; ; attempt++ {
			if err := ctx.Err(); err != nil {
				return CompactionNoteForkResult{Usage: usage}, err
			}
			selected, err := client.NoteAttempt(ctx, compactionservice.NoteAttemptRequest{Messages: history, Plan: compactionservice.HandoffPlan{Prompt: plan.Prompt, MaxBytes: plan.MaxBytes}, Attempt: attempt, PreviousFailure: failure})
			if err != nil {
				return CompactionNoteForkResult{Usage: usage}, err
			}
			if !selected.Invoke {
				return CompactionNoteForkResult{Usage: usage}, fmt.Errorf("selected note policy stopped inference: %s", failure)
			}
			if err = providers.ValidateToolCallHistory(selected.Messages); err != nil {
				return CompactionNoteForkResult{Usage: usage}, err
			}
			current := plan
			current.Prompt = selected.Prompt
			current.MaxBytes = selected.MaxBytes
			current.PreparedMessages = selected.Messages
			result, err := fork(ctx, history, current)
			if result.Usage != nil {
				if usage == nil {
					usage = &providers.TokenUsage{}
				}
				usage.InputTokens += result.Usage.InputTokens
				usage.OutputTokens += result.Usage.OutputTokens
				usage.CacheReadTokens += result.Usage.CacheReadTokens
				usage.CacheCreationTokens += result.Usage.CacheCreationTokens
				usage.CacheCreationUnknown = usage.CacheCreationUnknown || result.Usage.CacheCreationUnknown
			}
			if ctx.Err() != nil {
				return CompactionNoteForkResult{Usage: usage}, ctx.Err()
			}
			if err != nil && !errors.Is(err, errCompactionNoteTruncated) {
				return CompactionNoteForkResult{Usage: usage}, err
			}
			markdown := strings.TrimSpace(result.Markdown)
			if err == nil {
				if markdown == "" {
					return CompactionNoteForkResult{Usage: usage}, errors.New("context note fork returned empty Markdown")
				}
				if current.MaxBytes <= 0 || len(markdown) <= current.MaxBytes {
					return CompactionNoteForkResult{Markdown: markdown, Usage: usage}, nil
				}
				err = fmt.Errorf("compaction note is %d UTF-8 bytes; limit is %d", len(markdown), current.MaxBytes)
			}
			failure = err.Error()
		}
	}
}
