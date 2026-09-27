package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/modelvariant"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
)

// threadTitleSystemPrompt is adapted from
// thirdparty/opencode/packages/opencode/src/agent/prompt/title.txt so our
// generated titles match opencode's quality and behavior (single-line,
// language-preserving, no refusals, ≤100 characters).
//
// Keep the behavior aligned with opencode when bumping the upstream prompt.
const threadTitleSystemPrompt = `You are a title generator. You output only a thread title. Nothing else.

<task>
Generate a brief title that would help the user find this conversation later.

Follow all rules in <rules>
Use the <examples> so you know what a good title looks like.
Your output must be:
- A single line
- ≤50 characters
- No explanations
</task>

<rules>
- Use the same language as the user message you are summarizing
- Title must be grammatically correct and read naturally - no word salad
- Do not include tool names in the title (e.g. "read tool", "bash tool", "edit tool")
- Focus on the main topic or question the user needs to retrieve
- Vary your phrasing - avoid repetitive patterns like always starting with "Analyzing"
- When a file is mentioned, focus on WHAT the user wants to do WITH the file, not just that they shared it
- Keep exact: technical terms, numbers, filenames, HTTP codes
- Remove: the, this, my, a, an
- Do not assume tech stack
- Do not use tools
- Do not answer questions; generate only a title for the conversation
- Do not include "summarizing" or "generating" in the title
- Output a meaningful title even if the input is minimal; do not complain about the input
- If the user message is short or conversational (e.g. "hello", "lol", "what's up", "hey"):
  → create a title that reflects the user's tone or intent (such as Greeting, Quick check-in, Light chat, Intro message, etc.)
</rules>

<examples>
"debug 500 errors in production" → Debugging production 500 errors
"refactor user service" → Refactoring user service
"why is app.js failing" → app.js failure investigation
"implement rate limiting" → Rate limiting implementation
"how do I connect postgres to my API" → Postgres API connection
"best practices for React hooks" → React hooks best practices
"@src/auth.ts can you add refresh token support" → Auth refresh token support
"@utils/parser.ts this is broken" → Parser bug fix
"look at @config.json" → Config review
"@App.tsx add dark mode toggle" → Dark mode toggle in App
</examples>`

// titleGenerationTimeout caps the streaming call for a single title attempt.
// Long enough for a thinking model to reason briefly, short enough that a
// stalled provider does not strand the goroutine.
const titleGenerationTimeout = 60 * time.Second

// titleMaxRuneLength matches opencode's 100-character cap. Keep this aligned
// with thirdparty/opencode/packages/opencode/src/session/prompt.ts so we don't
// silently diverge.
const titleMaxRuneLength = 100

// recommendedTitleTemperature mirrors opencode's per-model temperature mapping
// in thirdparty/opencode/packages/opencode/src/provider/transform.ts
// (`temperature()`). The second return value reports whether a temperature
// should be sent at all; when false the caller MUST omit the field so models
// that pin their temperature (e.g. kimi-k2.6) accept the request.
func recommendedTitleTemperature(modelID string) (float64, bool) {
	id := strings.ToLower(strings.TrimSpace(modelID))
	if id == "" {
		return 0, false
	}
	if strings.Contains(id, "qwen") {
		return 0.55, true
	}
	if strings.Contains(id, "claude") {
		// Claude rejects temperature when reasoning is enabled and tolerates
		// the default otherwise; opencode opts to never send one.
		return 0, false
	}
	if strings.Contains(id, "gemini") {
		return 1.0, true
	}
	if strings.Contains(id, "glm-4.6") || strings.Contains(id, "glm-4.7") {
		return 1.0, true
	}
	if strings.Contains(id, "minimax-m2") {
		return 1.0, true
	}
	if strings.Contains(id, "kimi-k2") {
		// kimi-k2-thinking, kimi-k2.5/.6/.7/.8, kimi-k2p5, kimi-k2-5 all want 1.0.
		for _, marker := range []string{"thinking", "k2.", "k2p", "k2-5"} {
			if strings.Contains(id, marker) {
				return 1.0, true
			}
		}
		return 0.6, true
	}
	return 0, false
}

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

// generateThreadTitle asks the title model for a short, sidebar-friendly title
// for the freshly started conversation.
//
// Implementation notes (aligned with opencode's SessionPrompt.ensureTitle):
//   - We stream the response so providers that reject non-streaming requests
//     (kimi-k2.6 returns "Stream must be set to true", and other reasoning
//     models exhibit similar behavior) still work.
//   - Temperature follows opencode's recommendedTitleTemperature mapping
//     instead of a hardcoded value, so pinned-temperature models accept the
//     request rather than 400-ing.
//   - We do not constrain max_tokens: the system prompt already bounds the
//     output, and capping output blocks thinking models from ever emitting a
//     final answer (reasoning consumes the entire budget).
//   - We aggregate text deltas only; thinking deltas (<think>…</think>) are
//     stripped again post-hoc in cleanGeneratedThreadTitle.
func (s *Server) generateThreadTitle(threadID string, history []providers.ChatMessage, threadRuntime *runtime.ThreadRuntime) {
	_, _ = s.generateThreadTitleCore(threadID, history, true, true, false, threadRuntime)
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
// prompt). The production first-turn path leaves it false so a later
// turn cannot accidentally re-trigger title generation.
//
// threadRuntime is the conversation's per-thread runtime. When the title role
// inherits the main model, the title follows this runtime's pinned model rather
// than the workspace default (which drifts when another session switches
// model). It may be nil (probe / regenerate-title / no runtime), in which case
// the workspace runtime resolves the title model as before.
func (s *Server) generateThreadTitleCore(threadID string, history []providers.ChatMessage, persist, notify, forceFirstUser bool, threadRuntime *runtime.ThreadRuntime) (res TitleGenerationResult, retErr error) {
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
	client := s.titleStreamClient()
	if client == nil {
		res.SkipReason = "no TitleClient configured"
		providers.DebugLogf("title[%s]: skipped (%s)", threadID, res.SkipReason)
		return res, nil
	}
	titleRole := s.rt.ModelRoles.Title
	res.Model = firstNonEmpty(
		titleRole.APIModel,
		nonEmptyModel(s.rt.StreamRunner),
		s.rt.Model,
	)
	reqEffort := titleRole.LegacyEffort
	reqProviderOptions := titleRole.ProviderOptions
	// When the title role inherits the main model, follow the conversation's
	// pinned model, not the workspace default: another session switching model
	// repins only the workspace runtime, so a title generated off s.rt would use
	// that other session's model. An explicit (non-inherited) title model stays
	// workspace-global by design.
	if titleRole.Inherited && threadRuntime != nil && threadRuntime.StreamRunner != nil {
		runner := threadRuntime.StreamRunner
		if model := firstNonEmpty(runner.APIModel, runner.Model); model != "" {
			res.Model = model
			reqEffort = runner.Effort
			reqProviderOptions = runner.ProviderOptions
			if runner.Client != nil {
				client = runner.Client
			}
		}
	}
	if res.Model == "" {
		res.SkipReason = "no model resolved"
		providers.DebugLogf("title[%s]: skipped (%s)", threadID, res.SkipReason)
		return res, nil
	}

	firstUser, ok := firstUserMessageForTitle(history, forceFirstUser)
	if forceFirstUser {
		// Provider history is rewritten by compaction. Session Summary is the
		// immutable preview captured from the original first user message, so use
		// it for manual regeneration instead of accidentally titling a retained
		// post-compaction message. Probe/synthetic threads have no metadata and
		// continue to use the supplied history.
		if metadata, found, err := session.Find(s.rt.SessionDir, threadID); err != nil {
			providers.DebugLogf("title[%s]: failed to load original title source: %v", threadID, err)
		} else if found && strings.TrimSpace(metadata.Summary) != "" {
			firstUser = strings.TrimSpace(metadata.Summary)
			ok = true
		}
	}
	if !ok {
		res.SkipReason = "no eligible first user message"
		providers.DebugLogf("title[%s]: skipped (%s)", threadID, res.SkipReason)
		return res, nil
	}
	res.FirstUserPrompt = firstUser

	th := s.thread(threadID)
	if th != nil {
		th.mu.Lock()
		if th.ReadOnly || th.Ephemeral || th.ParentID != "" || strings.TrimSpace(th.Title) != "" {
			th.mu.Unlock()
			res.SkipReason = "thread already titled / read-only / ephemeral / forked"
			providers.DebugLogf("title[%s]: skipped (%s)", threadID, res.SkipReason)
			return res, nil
		}
		th.mu.Unlock()
	}

	temp, sendTemp := recommendedTitleTemperature(res.Model)
	res.Temperature = temp
	res.SendTemperature = sendTemp
	req := providers.ChatRequest{
		Model: res.Model,
		Messages: []providers.ChatMessage{
			{Role: "system", Content: threadTitleSystemPrompt},
			{Role: "user", Content: "Generate a title for this conversation:\n" + firstUser},
		},
		Operation:       providers.NewInferenceOperation(providers.InferenceOperationTitle, providers.InferenceProfileBestEffort),
		Effort:          reqEffort,
		ProviderOptions: modelvariant.CloneOptions(reqProviderOptions),
	}
	if sendTemp {
		req.Temperature = temp
	}
	res.RequestMessages = len(req.Messages)

	providers.DebugLogf("title[%s]: sending request (model=%s temp=%.2f sendTemp=%v messages=%d firstUser=%d chars)",
		threadID, res.Model, temp, sendTemp, res.RequestMessages, len(firstUser))

	ctx, cancel := context.WithTimeout(context.Background(), titleGenerationTimeout)
	defer cancel()
	ctx = providers.WithInferenceJournal(ctx, s.rt.InferenceJournalForOwner(threadID))

	// We use a small adapter over streamTitleText so we can capture per-delta
	// deltas for the probe / regenerate-title result. streamTitleText itself
	// remains untouched (it's exercised by TestStreamTitleText_*).
	text, deltas, err := streamTitleTextWithDeltas(ctx, client, req)
	res.ContentDeltas = deltas
	res.AggregatedText = text
	if err != nil {
		res.StreamErr = err
		providers.DebugLogf("title[%s]: stream error after %d deltas: %v", threadID, len(deltas), err)
		return res, err
	}
	providers.DebugLogf("title[%s]: received %d content deltas, aggregated %d chars",
		threadID, len(deltas), len(text))

	res.CleanedTitle = cleanGeneratedThreadTitle(text)
	if res.CleanedTitle == "" {
		res.SkipReason = "empty title after cleaning"
		providers.DebugLogf("title[%s]: empty title after cleaning (raw=%q); skipping persist", threadID, text)
		return res, nil
	}
	providers.DebugLogf("title[%s]: cleaned title: %q", threadID, res.CleanedTitle)

	if !persist {
		providers.DebugLogf("title[%s]: dry-run, not persisting", threadID)
		return res, nil
	}

	metadata, err := session.UpdateGeneratedTitle(s.rt.SessionDir, threadID, res.CleanedTitle)
	if err != nil {
		res.PersistErr = err
		providers.DebugLogf("title[%s]: persist failed: %v", threadID, err)
		return res, err
	}
	res.Persisted = true
	res.PersistedTitle = metadata.Title

	thread, err := s.threadAfterMetadataUpdate(metadata)
	if err != nil {
		res.NotificationErr = err
		providers.DebugLogf("title[%s]: failed to reload thread after persist: %v", threadID, err)
		return res, err
	}
	res.PreviewAfter = thread.Preview

	if notify {
		if err := s.notifyThreadUpdated(thread); err != nil {
			res.NotificationErr = err
			providers.DebugLogf("title[%s]: notification failed: %v", threadID, err)
			return res, err
		}
		res.Notified = true
	}
	return res, nil
}

// nonEmptyModel returns the APIModel or Model of a StreamRunner, skipping
// nil runners. Centralized so generateThreadTitleCore can stay readable.
func nonEmptyModel(r *agent.StreamRunner) string {
	if r == nil {
		return ""
	}
	return firstNonEmpty(r.APIModel, r.Model)
}

// titleStreamClient resolves the streaming client used for title generation.
// We require an explicitly configured TitleClient (production wires this to
// the main provider client; tests inject their own) and wrap it with
// providers.AdaptStreamClient so non-streaming fakes still work via the
// synthetic stream adapter.
//
// We intentionally do NOT fall back to StreamRunner.Client when TitleClient is
// nil. Falling back would inject a parallel request into the main provider
// during the first turn (consuming prepared test responses, costing real
// users an extra call, and competing for rate limit headroom). The
// production runtime always populates TitleClient, so a nil here means "no
// title client configured" and we should silently skip.
func (s *Server) titleStreamClient() providers.StreamClient {
	if s.rt.TitleClient == nil {
		return nil
	}
	return providers.AdaptStreamClient(s.rt.TitleClient)
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
	history, err := loadChatMessages(s.rt.SessionDir, threadID)
	if err != nil {
		return s.writeResponse(req.ID, nil, fmt.Errorf("load history for thread %q: %w", threadID, err))
	}

	// If the caller overrode the model or provider, we need a fresh
	// runtime whose TitleClient targets that. We rebuild the runtime
	// only when an override is requested; otherwise reuse s.rt so the
	// request matches the user's live provider exactly.
	if params.ModelOverride != "" || params.ProviderName != "" {
		return s.regenerateTitleWithOverride(ctx, req, threadID, history, params)
	}

	result, err := s.generateThreadTitleCore(threadID, history, !params.DryRun, !params.DryRun, true, nil)
	if err != nil {
		// Still return the result so the caller can see what went wrong.
		_ = s.writeResponse(req.ID, ThreadRegenerateTitleResult{TitleGenerationResult: result}, err)
		return nil
	}
	return s.writeResponse(req.ID, ThreadRegenerateTitleResult{TitleGenerationResult: result}, nil)
}

// regenerateTitleWithOverride is the model/provider-override branch of
// handleThreadRegenerateTitle. It spins up a one-off runtime + server
// so the request hits the user-requested provider instead of the live
// one, runs generateThreadTitleCore against that runtime, then merges
// the resulting title back into the live server's thread state and
// fires the thread/updated notification.
func (s *Server) regenerateTitleWithOverride(ctx context.Context, req Request, threadID string, history []providers.ChatMessage, params ThreadRegenerateTitleParams) error {
	cfg, _, err := loadProbeConfig(s.rt.RootDir, os.Getenv("HOME"))
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	probeRT, err := runtime.NewSession(runtime.Options{
		RootDir:       s.rt.RootDir,
		HomeDir:       os.Getenv("HOME"),
		Config:        cfg,
		ProviderName:  params.ProviderName,
		ModelOverride: params.ModelOverride,
	})
	if err != nil {
		return s.writeResponse(req.ID, nil, fmt.Errorf("build override runtime: %w", err))
	}
	defer func() { _, _ = probeRT.Cleanup() }()

	probeSrv := New(probeRT, s.out)
	result, err := probeSrv.generateThreadTitleCore(threadID, history, !params.DryRun, false, true, nil)
	if err != nil {
		_ = s.writeResponse(req.ID, ThreadRegenerateTitleResult{TitleGenerationResult: result}, err)
		return nil
	}
	// Persist + notify on the live server so the existing session index
	// and the desktop's thread state stay authoritative. Skip when
	// dry-run (the probe already returned without persisting).
	if !params.DryRun && result.Persisted {
		metadata, perr := session.UpdateGeneratedTitle(s.rt.SessionDir, threadID, result.CleanedTitle)
		if perr != nil {
			return s.writeResponse(req.ID, ThreadRegenerateTitleResult{TitleGenerationResult: result}, perr)
		}
		thread, terr := s.threadAfterMetadataUpdate(metadata)
		if terr != nil {
			return s.writeResponse(req.ID, ThreadRegenerateTitleResult{TitleGenerationResult: result}, terr)
		}
		_ = s.notifyThreadUpdated(thread)
		result.PersistedTitle = metadata.Title
		result.PreviewAfter = thread.Preview
		result.Notified = true
	}
	return s.writeResponse(req.ID, ThreadRegenerateTitleResult{TitleGenerationResult: result}, nil)
}

// firstUserMessageForTitle returns the user prompt to summarize. opencode only
// generates a title for the very first user turn (`history.filter(real).length
// !== 1` returns); we mirror that constraint with the count == 1 check.
//
// When force is true, the function ignores the count check and returns the
// first non-empty user message regardless of how many follow. This is what
// thread/regenerate-title wants (re-title using the original prompt), and
// what `wuu probe-title --user-prompt` already passes by virtue of the
// synthetic single-user history it constructs.
func firstUserMessageForTitle(history []providers.ChatMessage, force bool) (string, bool) {
	var first string
	count := 0
	for _, msg := range history {
		if !isThreadTitleUserMessage(msg) {
			continue
		}
		content := strings.TrimSpace(chatMessageDisplayContent(msg))
		if content == "" {
			continue
		}
		count++
		if count == 1 {
			first = content
		}
	}
	if force {
		return first, first != ""
	}
	return first, count == 1 && first != ""
}

// cleanGeneratedThreadTitle strips reasoning fences, optional list/title
// prefixes, and surrounding quotes before truncating to the opencode-aligned
// 100-rune cap.
func cleanGeneratedThreadTitle(text string) string {
	text = stripThinkBlocks(text)
	for _, line := range strings.Split(text, "\n") {
		title := strings.TrimSpace(line)
		// Bullet prefix.
		title = strings.TrimPrefix(title, "- ")
		title = strings.TrimPrefix(title, "* ")
		// English "Title:" prefix (model habit).
		for _, prefix := range []string{"Title:", "title:", "TITLE:"} {
			if strings.HasPrefix(title, prefix) {
				title = strings.TrimSpace(strings.TrimPrefix(title, prefix))
			}
		}
		// Chinese 标题: / 标题: prefixes (half-width + full-width colon).
		for _, prefix := range []string{"标题:", "标题:", "题目:", "题目:"} {
			if strings.HasPrefix(title, prefix) {
				title = strings.TrimSpace(strings.TrimPrefix(title, prefix))
			}
		}
		title = strings.Trim(strings.TrimSpace(title), "\"'`“”‘’《》「」『』")
		if title == "" {
			continue
		}
		runes := []rune(title)
		if len(runes) > titleMaxRuneLength {
			title = string(runes[:titleMaxRuneLength])
		}
		return strings.TrimSpace(title)
	}
	return ""
}

// stripThinkBlocks removes inline <think>…</think> reasoning fences emitted by
// models that surface chain-of-thought as plain text instead of a structured
// reasoning channel.
func stripThinkBlocks(text string) string {
	for {
		lower := strings.ToLower(text)
		start := strings.Index(lower, "<think>")
		if start < 0 {
			return text
		}
		endRel := strings.Index(lower[start:], "</think>")
		if endRel < 0 {
			return text[:start]
		}
		end := start + endRel + len("</think>")
		text = text[:start] + text[end:]
	}
}
