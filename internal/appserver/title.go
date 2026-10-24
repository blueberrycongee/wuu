package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/modelroles"
	"github.com/blueberrycongee/wuu/internal/modelvariant"
	"github.com/blueberrycongee/wuu/internal/modelwire"
	"github.com/blueberrycongee/wuu/internal/providerfactory"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/titleservice"
)

// titleGenerationTimeout caps the streaming call for a single title attempt.
// Long enough for a thinking model to reason briefly, short enough that a
// stalled provider does not strand the goroutine.
const titleGenerationTimeout = 60 * time.Second

// TitleGenerationResult captures the observable state of a single title
// generation attempt. Every field is populated whenever the pipeline reaches
// that step, so callers (production hot path, `wuu probe-title`, and
// `thread/regenerate-title`) can answer the same diagnostic questions:
//
//   - Did we even attempt this thread? (SkipReason != "")
//   - Which model / temperature / request did we send?
//   - What came back from the provider?
//   - What was the cleaned title?
//   - Did we persist it, and did we notify the renderer?
//
// Production hot path only checks Error and PersistedTitle. `wuu probe-title`
// prints the whole struct for human inspection.
type TitleGenerationResult struct {
	ThreadID    string
	Model       string
	Temperature float64
	// SendTemperature reports whether Temperature was included in the
	// request. False means we deliberately omitted the field (e.g. for
	// claude), so the title struct reports Temperature=0.
	SendTemperature bool
	FirstUserPrompt string
	RequestMessages int
	ContentDeltas   []string
	AggregatedText  string
	CleanedTitle    string
	Persisted       bool
	PersistedTitle  string
	PreviewAfter    string
	Notified        bool
	SkipReason      string
	StreamErr       error
	PersistErr      error
	NotificationErr error
	StartedAt       time.Time
	CompletedAt     time.Time
}

// generateThreadTitle invokes the captured optional title policy.
func (s *Server) generateThreadTitle(binding *runtime.State, threadID string, history []providers.ChatMessage, threadRuntime *runtime.ThreadRuntime) {
	_, _ = s.generateThreadTitleCore(binding, threadID, history, true, true, false, threadRuntime)
}

// generateThreadTitleCore is the workhorse behind generateThreadTitle,
// `wuu probe-title`, and `thread/regenerate-title`. It exposes the same
// observability fields to all three callers and supports dry-run mode
// (persist=false) and silent mode (notify=false) for the CLI path.
//
// Every step is logged via providers.DebugLogf so the production path leaves
// a structured trace in ~/.wuu/log/debug.log that can be correlated by
// thread ID. The CLI path additionally prints to stdout.
//
// When forceFirstUser is true, the pipeline uses the first user message
// regardless of how many subsequent user messages there are. This is the
// behavior thread/regenerate-title wants (re-title with the original
// prompt). Automatic retries use the persisted original summary when available;
// without that durable source, only a first-turn history is eligible.
//
// threadRuntime is the conversation's per-thread runtime. When the title role
// inherits the main model, the title follows this runtime's pinned model rather
// than the workspace default (which drifts when another session switches
// model). It may be nil (probe / regenerate-title / no runtime), in which case
// the captured workspace selection supplies the title model.
func (s *Server) generateThreadTitleCore(binding *runtime.State, threadID string, history []providers.ChatMessage, persist, notify, forceFirstUser bool, threadRuntime *runtime.ThreadRuntime) (TitleGenerationResult, error) {
	models, modelErr := binding.Models()
	titleRole := models.Roles.Title
	model := titleInferenceModel{
		name:   firstNonEmpty(titleRole.APIModel, models.Roles.Main.APIModel, models.Roles.Main.Model),
		effort: titleRole.LegacyEffort, options: titleRole.ProviderOptions,
		err: modelErr,
	}
	if models.Title != nil {
		model.client = providers.AdaptStreamClient(models.Title)
	}
	if models.Title != nil && titleRole.Inherited && threadRuntime != nil && threadRuntime.StreamRunner != nil {
		runner := threadRuntime.StreamRunner
		if pinned := firstNonEmpty(runner.APIModel, runner.Model); pinned != "" {
			model.name, model.effort, model.options = pinned, runner.Effort, runner.ProviderOptions
			if runner.Client != nil {
				model.client = runner.Client
			}
		}
	}
	return s.generateThreadTitleUsingModel(binding, threadID, history, persist, notify, forceFirstUser, model)
}

type titleInferenceModel struct {
	client       providers.StreamClient
	name, effort string
	options      map[string]any
	buildClient  func() (providers.StreamClient, error)
	err          error
}

func (s *Server) generateThreadTitleUsingModel(binding *runtime.State, threadID string, history []providers.ChatMessage, persist, notify, forceFirstUser bool, model titleInferenceModel) (res TitleGenerationResult, retErr error) {
	res.ThreadID = threadID
	res.StartedAt = time.Now().UTC()
	defer func() {
		res.CompletedAt = time.Now().UTC()
		providers.DebugLogf("title[%s]: completed in %dms (skipped=%q persisted=%v notified=%v)",
			threadID, res.CompletedAt.Sub(res.StartedAt).Milliseconds(), res.SkipReason, res.Persisted, res.Notified)
	}()

	if s == nil || s.rt == nil {
		res.SkipReason = "server not initialized"
		return res, nil
	}
	policy := binding.TitlePolicy()
	if policy == nil {
		res.SkipReason = "no title policy selected"
		return res, nil
	}
	res.Model = model.name
	reqEffort, reqProviderOptions := model.effort, model.options

	if persist {
		key := sessionCacheKey{binding.Sessions, threadID}
		s.mu.Lock()
		if s.titleGenerations[key] {
			s.mu.Unlock()
			res.SkipReason = "title generation already running"
			return res, nil
		}
		if s.titleGenerations == nil {
			s.titleGenerations = make(map[sessionCacheKey]bool)
		}
		s.titleGenerations[key] = true
		s.mu.Unlock()
		defer func() {
			s.mu.Lock()
			delete(s.titleGenerations, key)
			s.mu.Unlock()
		}()
	}

	input := titleservice.PlanRequest{Model: res.Model, Force: forceFirstUser, Persist: persist}
	for _, message := range history {
		if isThreadTitleUserMessage(message) {
			input.HumanMessages = append(input.HumanMessages, chatMessageDisplayContent(message))
		}
	}
	if metadata, found, err := binding.Sessions.Find(threadID); err != nil {
		res.PersistErr = err
		return res, err
	} else if found {
		input.Title = metadata.Title
		input.Summary = metadata.Summary
	}
	if thread := s.threadForState(binding, threadID); thread != nil {
		thread.mu.Lock()
		input.ReadOnly, input.Ephemeral, input.ParentID = thread.ReadOnly, thread.Ephemeral, thread.ParentID
		if thread.Title != "" {
			input.Title = thread.Title
		}
		thread.mu.Unlock()
	}
	ctx, cancel := context.WithTimeout(context.Background(), titleGenerationTimeout)
	defer cancel()
	plan, err := policy.Plan(ctx, input)
	if err != nil {
		res.StreamErr = err
		return res, err
	}
	res.FirstUserPrompt = plan.Source
	if plan.Action == "skip" {
		res.SkipReason = plan.Reason
		return res, nil
	}
	if plan.Action == "complete" {
		res.CleanedTitle = plan.Title
	} else {
		if model.err != nil {
			res.StreamErr = model.err
			return res, model.err
		}
		if model.buildClient != nil {
			model.client, err = model.buildClient()
			if err != nil {
				res.StreamErr = err
				return res, err
			}
		}
		if model.client == nil {
			res.SkipReason = "no TitleClient configured"
			return res, nil
		}
		if res.Model == "" {
			res.SkipReason = "no model resolved"
			return res, nil
		}
		messages := make([]providers.ChatMessage, 0, len(plan.Messages))
		for _, message := range plan.Messages {
			messages = append(messages, modelwire.ChatMessageFromWire(message))
		}
		req := providers.ChatRequest{Model: res.Model, Messages: messages,
			Operation: providers.NewInferenceOperation(providers.InferenceOperationTitle, providers.InferenceProfileBestEffort),
			Effort:    reqEffort, ProviderOptions: modelvariant.CloneOptions(reqProviderOptions)}
		if plan.Temperature != nil {
			req.Temperature = *plan.Temperature
			if req.ProviderOptions == nil {
				req.ProviderOptions = make(map[string]any)
			}
			// The option preserves explicit zero through the public model wire.
			req.ProviderOptions["temperature"] = *plan.Temperature
			res.Temperature = *plan.Temperature
			res.SendTemperature = true
		}
		res.RequestMessages = len(req.Messages)
		ctx = providers.WithInferenceJournal(ctx, binding.InferenceJournalRuntime.ForOwner(threadID))
		ctx, finishReceipts, err := binding.BeginModelReceipts(ctx)
		if err != nil {
			res.StreamErr = err
			return res, err
		}
		text, deltas, err := streamTitleTextWithDeltas(ctx, model.client, req)
		res.ContentDeltas, res.AggregatedText = deltas, text
		if err == nil {
			res.CleanedTitle, err = policy.Resolve(ctx, text)
		}
		err = errors.Join(err, finishReceipts())
		if err != nil {
			res.StreamErr = err
			return res, err
		}
	}

	if res.CleanedTitle == "" {
		res.SkipReason = "empty title after cleaning"
		providers.DebugLogf("title[%s]: empty title after cleaning; skipping persist", threadID)
		return res, nil
	}
	providers.DebugLogf("title[%s]: cleaned title: %q", threadID, res.CleanedTitle)

	if !persist {
		providers.DebugLogf("title[%s]: dry-run, not persisting", threadID)
		return res, nil
	}

	metadata, err := binding.Sessions.UpdateGeneratedTitle(threadID, res.CleanedTitle)
	if err != nil {
		res.PersistErr = err
		providers.DebugLogf("title[%s]: persist failed: %v", threadID, err)
		return res, err
	}
	res.Persisted = true
	res.PersistedTitle = metadata.Title

	thread, err := s.threadAfterMetadataUpdate(binding, metadata)
	if err != nil {
		res.NotificationErr = err
		providers.DebugLogf("title[%s]: failed to reload thread after persist: %v", threadID, err)
		return res, err
	}
	res.PreviewAfter = thread.Preview

	if notify {
		if err := s.notifyThreadUpdated(binding, thread); err != nil {
			res.NotificationErr = err
			providers.DebugLogf("title[%s]: notification failed: %v", threadID, err)
			return res, err
		}
		res.Notified = true
	}
	return res, nil
}

// streamTitleText opens a streaming chat and aggregates content deltas until
// the model finishes. Thinking deltas are intentionally ignored: opencode does
// the same (Stream.filter(textDelta)) and we additionally strip <think> blocks
// from the final text as belt-and-suspenders.
func streamTitleText(ctx context.Context, client providers.StreamClient, req providers.ChatRequest) (string, error) {
	text, _, err := streamTitleTextWithDeltas(ctx, client, req)
	return text, err
}

// streamTitleTextWithDeltas is like streamTitleText but also returns every
// content delta separately so callers (probe / regenerate-title) can inspect
// the streaming behavior — useful when the model returns a non-monotonic
// sequence (replace / message events reset the buffer).
func streamTitleTextWithDeltas(ctx context.Context, client providers.StreamClient, req providers.ChatRequest) (string, []string, error) {
	req.Operation = providers.EnsureInferenceOperation(req.Operation, providers.InferenceOperationTitle, providers.InferenceProfileBestEffort)
	var err error
	req, err = providers.EnsureInferenceExecutionContext(ctx, req, providers.InferenceOperationTitle, providers.InferenceProfileBestEffort)
	if err != nil {
		return "", nil, err
	}
	reliableClient := providers.NewReliableStreamClient(client, nil)
	events, err := reliableClient.StreamChat(ctx, req)
	if err != nil {
		return "", nil, finishTitleFailure(req.Execution, err)
	}
	var b strings.Builder
	var deltas []string
	for ev := range events {
		switch ev.Type {
		case providers.EventContentDelta:
			if ev.Content != "" {
				deltas = append(deltas, ev.Content)
				b.WriteString(ev.Content)
			}
		case providers.EventContentReplace:
			deltas = append(deltas, "[replace:"+ev.Content+"]")
			b.Reset()
			b.WriteString(ev.Content)
		case providers.EventMessage:
			if ev.Message != nil && ev.Message.Content != "" {
				deltas = append(deltas, "[message:"+ev.Message.Content+"]")
				b.Reset()
				b.WriteString(ev.Message.Content)
			}
		case providers.EventLifecycle:
			if ev.Lifecycle != nil && ev.Lifecycle.Phase == providers.StreamPhaseReconnecting && ev.Lifecycle.ResetPartial {
				deltas = append(deltas, "[attempt-reset]")
				b.Reset()
			}
		case providers.EventError:
			if ev.Error != nil {
				return b.String(), deltas, finishTitleFailure(req.Execution, ev.Error)
			}
			err := errors.New("title stream error")
			return b.String(), deltas, finishTitleFailure(req.Execution, err)
		case providers.EventDone:
			// keep draining to let the producer close cleanly
		}
	}
	if err := ctx.Err(); err != nil {
		return b.String(), deltas, finishTitleFailure(req.Execution, err)
	}
	if err := req.Execution.Complete(providers.InferenceOutcomeSucceeded, providers.NormalizedFailure{}); err != nil {
		return b.String(), deltas, err
	}
	return b.String(), deltas, nil
}

func finishTitleFailure(execution *providers.InferenceExecution, err error) error {
	if err == nil || execution == nil {
		return err
	}
	failure := providers.NormalizeFailure(err)
	outcome := providers.InferenceOutcomeFailed
	if failure.Category == providers.FailureCanceled || failure.Category == providers.FailureDeadline {
		outcome = providers.InferenceOutcomeCanceled
	}
	if journalErr := execution.Complete(outcome, failure); journalErr != nil {
		return errors.Join(err, journalErr)
	}
	return err
}

// handleThreadRegenerateTitle is the JSON-RPC entry point for "rerun the
// title pipeline against the current provider/model". The desktop uses
// it to manually re-title a thread (after a model switch, for example)
// and to surface the full TitleGenerationResult in a dev panel.
//
// The result is always returned to the caller regardless of dry-run, so
// the UI can show what *would* be persisted. When not dry-run, a
// thread/updated notification is also fired (handled by the core).
func (s *Server) handleThreadRegenerateTitle(ctx context.Context, req Request) error {
	binding, stateErr := s.rt.RetainState()
	if stateErr != nil {
		return s.writeResponse(req.ID, nil, stateErr)
	}
	defer binding.Close()

	var params ThreadRegenerateTitleParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, fmt.Errorf("parse params: %w", err))
	}
	threadID := strings.TrimSpace(params.ThreadID)
	if threadID == "" {
		return s.writeResponse(req.ID, nil, errors.New("thread_id is required"))
	}

	// Build history from the on-disk session (the in-memory thread state
	// may be missing — e.g. after a desktop restart).
	history, err := loadChatMessages(binding.Sessions, threadID)
	if err != nil {
		return s.writeResponse(req.ID, nil, fmt.Errorf("load history for thread %q: %w", threadID, err))
	}

	// If the caller overrode the model or provider, we need a fresh
	// runtime whose TitleClient targets that. We rebuild the runtime
	// only when an override is requested; otherwise reuse s.rt so the
	// request matches the user's live provider exactly.
	if params.ModelOverride != "" || params.ProviderName != "" {
		return s.regenerateTitleWithOverride(binding, ctx, req, threadID, history, params)
	}

	result, err := s.generateThreadTitleCore(binding, threadID, history, !params.DryRun, !params.DryRun, true, nil)
	if err != nil {
		// Still return the result so the caller can see what went wrong.
		_ = s.writeResponse(req.ID, ThreadRegenerateTitleResult{TitleGenerationResult: result}, err)
		return nil
	}
	return s.writeResponse(req.ID, ThreadRegenerateTitleResult{TitleGenerationResult: result}, nil)
}

// Title overrides reuse the request's selected configuration, credentials and
// journal. Creating another workspace runtime could select a different graph.
func (s *Server) regenerateTitleWithOverride(binding *runtime.State, ctx context.Context, req Request, threadID string, history []providers.ChatMessage, params ThreadRegenerateTitleParams) error {
	model := titleInferenceModel{name: params.ModelOverride}
	if binding.TitlePolicy() != nil {
		// Resolve metadata for the policy, but require a working model only if
		// its plan asks for inference. Client construction can acquire resources.
		resolved, err := func() (titleInferenceModel, error) {
			services, err := binding.Services()
			if err != nil {
				return titleInferenceModel{}, err
			}
			view, err := services.Config.Read(ctx)
			if err != nil {
				return titleInferenceModel{}, err
			}
			roles, err := modelroles.Resolve(view.Config, modelroles.ResolveOptions{ProviderName: params.ProviderName, Model: params.ModelOverride, Effort: view.Config.Agent.Effort, Variant: view.Config.Agent.Variant})
			if err != nil {
				return titleInferenceModel{}, err
			}
			role := roles.Title
			return titleInferenceModel{name: role.APIModel, effort: role.LegacyEffort, options: role.ProviderOptions,
				buildClient: func() (providers.StreamClient, error) {
					client, err := providerfactory.BuildStreamClient(role.RuleProviderConfig, role.Provider, providerfactory.Bindings{Credentials: services.Credentials, ChatGPT: services.ChatGPT})
					if err != nil {
						return nil, fmt.Errorf("build title override client: %w", err)
					}
					return client, nil
				}}, nil
		}()
		if err != nil {
			model.err = err
		} else {
			model = resolved
		}
	}
	result, err := s.generateThreadTitleUsingModel(binding, threadID, history, !params.DryRun, !params.DryRun, true, model)
	return s.writeResponse(req.ID, ThreadRegenerateTitleResult{TitleGenerationResult: result}, err)
}
