package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/blueberrycongee/wuu/internal/compactionservice"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
)

// selectedNoteProvider is a captured policy client, not another provider registry.
type selectedNoteProvider struct {
	client *compactionservice.Client
	policy compactionservice.Policy
}

func (p *selectedNoteProvider) CompactionKey() string        { return p.policy.Key }
func (p *selectedNoteProvider) CompactionPriority() int      { return 0 }
func (p *selectedNoteProvider) CompactionNotesEnabled() bool { return p.policy.NotesEnabled }
func (p *selectedNoteProvider) ContextWindowsEnabled() bool  { return p.policy.ContextWindows }
func (p *selectedNoteProvider) Compact(ctx context.Context, model string, messages []providers.ChatMessage) ([]providers.ChatMessage, error) {
	out, err := p.client.Compact(ctx, compactionservice.Request{PolicyKey: p.policy.Key, Model: model, Messages: messages})
	return out.Messages, err
}
func (p *selectedNoteProvider) PlanCompactionNote(ctx context.Context, model string, messages, delta []providers.ChatMessage, previous CompactionNote) (CompactionNotePlan, error) {
	out, err := p.client.NotePlan(ctx, compactionservice.NoteRequest{PolicyKey: p.policy.Key, Model: model, Messages: messages, Delta: delta, PreviousNote: previous.Markdown, PreviousCoveredMessages: previous.CoveredMessages, Force: true})
	return CompactionNotePlan{Prompt: out.Prompt, IntervalTokens: out.IntervalTokens, MaxBytes: out.MaxBytes}, err
}
func (p *selectedNoteProvider) PlanHandoffBrief(ctx context.Context, model string, messages []providers.ChatMessage, previous CompactionNote, intent, source string, through int) (CompactionNotePlan, error) {
	out, err := p.client.Handoff(ctx, compactionservice.HandoffRequest{PolicyKey: p.policy.Key, Model: model, Messages: messages, PreviousNote: previous.Markdown, PreviousCoveredMessages: previous.CoveredMessages, Intent: intent, SourceSessionID: source, SourceThroughSeq: through})
	return CompactionNotePlan{Prompt: out.Prompt, MaxBytes: out.MaxBytes}, err
}
func (p *selectedNoteProvider) CompactWithNote(ctx context.Context, model string, messages []providers.ChatMessage, note CompactionNote) (CompactionNoteReplacement, error) {
	out, err := p.client.CompactNote(ctx, compactionservice.NoteRequest{PolicyKey: p.policy.Key, Model: model, Messages: messages, Note: note.Markdown, CoveredMessages: note.CoveredMessages})
	return CompactionNoteReplacement{Messages: out.Messages, CoveredMessages: out.CoveredMessages}, err
}

// HandoffProvider captures the selected policy before any note/model work. A
// service failure never falls through to the local compatibility registry.
func (r *StreamRunner) HandoffProvider(ctx context.Context) (HandoffBriefProvider, error) {
	if r.SelectedCompaction != nil {
		policy, err := r.SelectedCompaction.Describe(ctx, r.compactionDescription())
		if err != nil {
			return nil, err
		}
		return &selectedNoteProvider{client: r.SelectedCompaction, policy: policy}, nil
	}
	if r.CompactionRegistry != nil {
		if p, ok := r.CompactionRegistry.Resolve(nil).(HandoffBriefProvider); ok && p != nil {
			return p, nil
		}
	}
	return nil, errors.New("handoff brief provider is unavailable")
}
func (r *StreamRunner) compactionDescription() compactionservice.DescribeRequest {
	return compactionservice.DescribeRequest{ContextTokens: r.ContextWindowOverride, InputTokens: r.MaxInputTokens, OutputReserveTokens: r.OutputReserveTokens, CompactThresholdTokens: r.CompactThresholdTokens}
}

func compactSelected(ctx context.Context, client *compactionservice.Client, policy compactionservice.Policy, request compactionservice.Request, cfg LoopConfig) (result []providers.ChatMessage, resultErr error) {
	request.PolicyKey = policy.Key
	if !policy.NotesEnabled {
		out, err := client.Compact(ctx, request)
		return out.Messages, err
	}
	p := &selectedNoteProvider{client: client, policy: policy}
	original := providers.CloneChatMessages(request.Messages)
	defer func() {
		if resultErr == nil {
			return
		}
		if cfg.OnCompactionNote != nil {
			cfg.OnCompactionNote("failed", resultErr)
		}
		if ctx.Err() != nil || errors.Is(resultErr, context.Canceled) || errors.Is(resultErr, context.DeadlineExceeded) {
			return
		}
		code := "note_failed"
		var serviceErr *pluginhost.HostServiceError
		if errors.As(resultErr, &serviceErr) {
			code = serviceErr.Code
		}
		recovered, err := client.Recover(ctx, compactionservice.RecoveryRequest{Request: request, Failure: compactionservice.Failure{Code: code, Message: resultErr.Error()}})
		if err != nil {
			resultErr = errors.Join(resultErr, err)
			return
		}
		if err = providers.ValidateToolCallHistory(recovered.Messages); err != nil {
			resultErr = errors.Join(resultErr, err)
			return
		}
		if len(recovered.Messages) == 0 || estimateFreshContextMessages(recovered.Messages) >= estimateFreshContextMessages(original) {
			resultErr = errors.Join(resultErr, errors.New("selected recovery did not shrink history"))
			return
		}
		result, resultErr = recovered.Messages, nil
	}()
	note, valid, err := loadValidCompactionNote(ctx, cfg.CompactionNoteStore, policy.Key, request.Messages)
	if err != nil {
		return nil, err
	}
	if !valid {
		note, _, err = generateSelectedNote(ctx, p, cfg.CompactionNoteStore, cfg.ForkCompactionNote, request.Model, request.Messages, true)
		if errors.Is(err, ErrCompactionNoteSuperseded) {
			note, valid, err = loadValidCompactionNote(ctx, cfg.CompactionNoteStore, policy.Key, request.Messages)
			if err == nil && !valid {
				err = ErrCompactionNoteSuperseded
			}
		}
		if err != nil {
			return nil, err
		}
		if cfg.OnCompactionNote != nil {
			cfg.OnCompactionNote("forced", nil)
		}
	} else if cfg.OnCompactionNote != nil {
		cfg.OnCompactionNote("context_note", nil)
	}
	replacement, err := p.CompactWithNote(ctx, request.Model, request.Messages, note)
	if err != nil {
		return nil, err
	}
	if err = providers.ValidateToolCallHistory(replacement.Messages); err != nil {
		return nil, err
	}
	if estimateFreshContextMessages(replacement.Messages) >= estimateFreshContextMessages(original) {
		return nil, errors.New("selected note replacement did not shrink history")
	}
	note.CoveredMessages = replacement.CoveredMessages
	note.CoveredHash = CompactionHistoryHash(replacement.Messages[:replacement.CoveredMessages])
	if cfg.CompactionNoteStore != nil {
		if err = cfg.CompactionNoteStore.StoreCompactionNote(ctx, policy.Key, note); err != nil {
			return nil, err
		}
	}
	return replacement.Messages, nil
}

func generateSelectedNote(ctx context.Context, p *selectedNoteProvider, store CompactionNoteStore, fork CompactionNoteFork, model string, messages []providers.ChatMessage, force bool) (CompactionNote, *providers.TokenUsage, error) {
	if err := ctx.Err(); err != nil {
		return CompactionNote{}, nil, err
	}
	if fork == nil {
		return CompactionNote{}, nil, errors.New("compaction note model transport is unavailable")
	}
	var saved CompactionNote
	var exists bool
	var err error
	if store != nil {
		saved, exists, err = store.LoadCompactionNote(ctx, p.policy.Key)
		if err != nil {
			return CompactionNote{}, nil, err
		}
	}
	previous := saved
	if !exists || !validCompactionNote(previous, messages) {
		previous = CompactionNote{}
	}
	budget, _ := ctx.Value(compactionNoteBudgetKey{}).(int)
	plan, err := p.client.NotePlan(ctx, compactionservice.NoteRequest{PolicyKey: p.policy.Key, Model: model, Messages: messages, PreviousNote: previous.Markdown, PreviousCoveredMessages: previous.CoveredMessages, InputTokens: budget, Force: force})
	if err != nil {
		return CompactionNote{}, nil, err
	}
	if !plan.Due {
		return previous, nil, ErrCompactionNoteNotDue
	}
	if err = providers.ValidateToolCallHistory(plan.Messages); err != nil {
		return CompactionNote{}, nil, err
	}
	generated, err := fork(ctx, plan.Messages, CompactionNotePlan{Prompt: plan.Prompt, IntervalTokens: plan.IntervalTokens, MaxBytes: plan.MaxBytes})
	if err != nil {
		return CompactionNote{}, generated.Usage, err
	}
	if err = ctx.Err(); err != nil {
		return CompactionNote{}, generated.Usage, err
	}
	markdown := strings.TrimSpace(generated.Markdown)
	if markdown == "" || (plan.MaxBytes > 0 && len(markdown) > plan.MaxBytes) {
		return CompactionNote{}, generated.Usage, fmt.Errorf("note output violates selected plan")
	}
	note := CompactionNote{Markdown: markdown, CoveredMessages: plan.CoveredMessages, CoveredHash: CompactionHistoryHash(messages[:plan.CoveredMessages])}
	if store != nil {
		if conditional, ok := store.(ConditionalCompactionNoteStore); ok {
			accepted, err := conditional.CompareAndSwapCompactionNote(ctx, p.policy.Key, saved, exists, note)
			if err != nil {
				return CompactionNote{}, generated.Usage, err
			}
			if !accepted {
				return CompactionNote{}, generated.Usage, ErrCompactionNoteSuperseded
			}
		} else if err = store.StoreCompactionNote(ctx, p.policy.Key, note); err != nil {
			return CompactionNote{}, generated.Usage, err
		}
	}
	return note, generated.Usage, nil
}

// ContextWindowsAvailableContext exposes selected policy failures to runtime
// setup instead of converting an unavailable provider into a local default.
func (r *StreamRunner) ContextWindowsAvailableContext(ctx context.Context) (bool, error) {
	if r.SelectedCompaction != nil {
		policy, err := r.SelectedCompaction.Describe(ctx, r.compactionDescription())
		return policy.ContextWindows, err
	}
	return r.ContextWindowsAvailable(), nil
}
