package appserver

import (
	"context"
	"errors"
	"math"
	"strings"

	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/sessiontrace"
)

const contextCompositionModeLatestRequest = "latest_request"

func (s *Server) handleThreadContextComposition(req Request) error {
	binding, stateErr := s.rt.RetainState()
	if stateErr != nil {
		return s.writeResponse(req.ID, nil, stateErr)
	}
	defer binding.Close()

	var params ThreadContextCompositionParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	threadID := strings.TrimSpace(params.ThreadID)
	if threadID == "" {
		return s.writeResponse(req.ID, nil, errors.New("thread_id is required"))
	}
	if err := s.ensureContextCompositionThreadExists(binding, threadID); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}

	services, err := binding.Services()
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	indexed, err := services.Index.LatestContext(context.Background(), threadID)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	if len(indexed.Events) == 0 {
		return s.writeResponse(req.ID, ThreadContextCompositionResult{ThreadID: threadID, Available: false, Reason: "no_request_yet", Mode: contextCompositionModeLatestRequest, TracePath: indexed.DiagnosticPath}, nil)
	}
	events := make([]sessiontrace.RawEvent, len(indexed.Events))
	for i, event := range indexed.Events {
		events[i] = sessiontrace.RawEvent(event)
	}
	summary, err := sessiontrace.ReplayEvents(events)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	result := s.contextCompositionFromTrace(binding, threadID, indexed.DiagnosticPath, summary)
	return s.writeResponse(req.ID, result, nil)
}

func (s *Server) ensureContextCompositionThreadExists(binding *runtime.State, threadID string) error {
	if s.threadForState(binding, threadID) != nil {
		return nil
	}
	if s == nil || s.rt == nil {
		return errors.New("runtime session is required")
	}
	if _, ok, err := binding.Sessions.Find(threadID); err != nil {
		return err
	} else if ok {
		return nil
	}
	return session.ErrSessionNotFound
}

func (s *Server) contextCompositionFromTrace(binding *runtime.State, threadID, tracePath string, summary sessiontrace.ReplaySummary) ThreadContextCompositionResult {
	result := ThreadContextCompositionResult{
		ThreadID:  threadID,
		Available: false,
		Reason:    "no_request_context",
		Mode:      contextCompositionModeLatestRequest,
		TracePath: tracePath,
	}
	if len(summary.ContextRequests) == 0 {
		return result
	}

	request := summary.ContextRequests[len(summary.ContextRequests)-1]
	step := latestContextRequestStep(summary)
	turn := summary.LatestTurn
	provider := ""
	model := ""
	contextWindowTokens := 0
	inputLimitTokens := 0
	usableInputTokens := 0
	compactThresholdTokens := 0
	turnID := ""
	selectedStepBudget := false
	if step != nil {
		turnID = strings.TrimSpace(step.TurnID)
		provider = strings.TrimSpace(step.Provider)
	}
	if turn != nil {
		if turnID == "" {
			turnID = strings.TrimSpace(turn.TurnID)
		}
		provider = firstNonEmpty(provider, turn.ProviderName)
		model = firstNonEmpty(turn.APIModel, turn.Model)
		if turn.ModelProfile != nil {
			contextWindowTokens = turn.ModelProfile.ContextWindowTokens
			inputLimitTokens = turn.ModelProfile.InputLimitTokens
			usableInputTokens = turn.ModelProfile.UsableInputTokens
			compactThresholdTokens = turn.ModelProfile.CompactThresholdTokens
		}
		for index := len(turn.ModelSteps) - 1; index >= 0; index-- {
			selected := turn.ModelSteps[index]
			if selected.StepIndex != request.StepIndex {
				continue
			}
			provider, model = selected.Provider, firstNonEmpty(selected.APIModel, selected.Model)
			if selected.Revision != 0 {
				// The receipt's effective ceiling belongs to this step. Do not
				// substitute the initial turn profile or a newly edited catalog.
				contextWindowTokens = selected.ContextWindowTokens
				inputLimitTokens, usableInputTokens, compactThresholdTokens = 0, 0, 0
				selectedStepBudget = true
			}
			break
		}
	}
	if inputLimitTokens <= 0 && !selectedStepBudget {
		inputLimitTokens = s.contextCompositionRuntimeInputLimit(provider, model)
	}
	contextCeilingTokens := effectiveContextCeilingTokens(contextWindowTokens, inputLimitTokens)

	promptTokens := request.InputTokens + request.CacheCreationTokens + request.CacheReadTokens
	totalContextTokens := promptTokens + request.OutputTokens
	promptBytes := request.MessageBytes + request.ToolSchemaBytes
	tokenSource := "byte_estimate"
	if promptTokens > 0 && promptBytes > 0 {
		tokenSource = "provider_usage"
	}
	categories := buildContextCompositionCategories(request, promptBytes, promptTokens)

	result.Available = true
	result.Reason = ""
	result.TurnID = turnID
	result.StepIndex = request.StepIndex
	result.Provider = provider
	result.Model = model
	result.ContextWindowTokens = contextCeilingTokens
	result.InputLimitTokens = inputLimitTokens
	result.UsableInputTokens = usableInputTokens
	result.CompactThresholdTokens = compactThresholdTokens
	result.PromptTokens = promptTokens
	result.TotalContextTokens = totalContextTokens
	result.RetainedTokens = s.latestRetainedContextTokens(binding, threadID)
	result.InputTokens = request.InputTokens
	result.OutputTokens = request.OutputTokens
	result.CacheCreationTokens = request.CacheCreationTokens
	result.CacheReadTokens = request.CacheReadTokens
	result.TokenEstimateSource = tokenSource
	result.MessageCount = request.MessageCount
	result.SystemMessages = request.SystemMessages
	result.HiddenMessages = request.HiddenMessages
	result.ToolCount = request.ToolCount
	result.StablePrefix = request.StablePrefix
	result.TurnPrefix = request.TurnPrefix
	result.DynamicBytes = request.DynamicBytes
	result.SystemHash = strings.TrimSpace(request.SystemHash)
	result.StablePrefixHash = strings.TrimSpace(request.StablePrefixHash)
	result.TurnPrefixHash = strings.TrimSpace(request.TurnPrefixHash)
	result.ToolSurfaceHash = strings.TrimSpace(request.ToolSurfaceHash)
	result.PromptCacheKey = strings.TrimSpace(request.PromptCacheKey)
	result.Categories = categories
	result.SystemSections = contextCompositionSections(request.SystemSections, request.SystemBytes, categoryTokensForBytes)
	result.BlockKindBytes = cloneStringIntMap(request.BlockKindBytes)
	result.SegmentCounts = ContextSegmentCountSummary{
		Lifecycle:   cloneStringIntMap(request.SegmentLifecycleCounts),
		Placement:   cloneStringIntMap(request.SegmentPlacementCounts),
		CachePolicy: cloneStringIntMap(request.SegmentCachePolicyCounts),
	}
	return result
}

func latestContextRequestStep(summary sessiontrace.ReplaySummary) *sessiontrace.RequestStepSummary {
	if len(summary.RequestSteps) == 0 {
		return nil
	}
	return &summary.RequestSteps[len(summary.RequestSteps)-1]
}

func buildContextCompositionCategories(record sessiontrace.RequestContextRecord, promptBytes, promptTokens int) []ContextCompositionCategory {
	messageBytes := max(0, record.MessageBytes)
	systemEnd := min(max(0, record.SystemBytes), messageBytes)
	stableEnd := min(max(record.StablePrefixBytes, systemEnd), messageBytes)
	turnEnd := min(max(record.TurnPrefixBytes, stableEnd), messageBytes)
	afterTurnBytes := max(0, messageBytes-turnEnd)
	requestOnlyBytes := min(max(0, record.DynamicBytes), afterTurnBytes)
	durableTailBytes := max(0, afterTurnBytes-requestOnlyBytes)

	categories := []ContextCompositionCategory{}
	add := func(category ContextCompositionCategory) {
		if category.Bytes <= 0 && category.Tokens <= 0 {
			return
		}
		if category.Tokens <= 0 && category.Bytes > 0 {
			category.Tokens = categoryTokensForBytes(category.Bytes, promptBytes, promptTokens)
		}
		categories = append(categories, category)
	}
	add(ContextCompositionCategory{
		ID:          "system",
		Label:       "系统提示",
		Description: "基础身份、运行规则和环境摘要。",
		Tone:        "system",
		Bytes:       systemEnd,
		Contributes: true,
		Durable:     true,
		CacheScope:  "stable",
	})
	add(ContextCompositionCategory{
		ID:          "stable_history",
		Label:       "稳定历史",
		Description: "当前请求里位于最近用户输入之前、最容易命中缓存的历史前缀。",
		Tone:        "stable",
		Bytes:       stableEnd - systemEnd,
		Contributes: true,
		Durable:     true,
		CacheScope:  "stable",
	})
	add(ContextCompositionCategory{
		ID:          "turn_prefix",
		Label:       "本轮前缀",
		Description: "最近用户输入到本轮可复用边界之间的内容。",
		Tone:        "turn",
		Bytes:       turnEnd - stableEnd,
		Contributes: true,
		Durable:     true,
		CacheScope:  "turn",
	})
	add(ContextCompositionCategory{
		ID:          "durable_tail",
		Label:       "本轮后续历史",
		Description: "工具调用、工具结果或后续消息，已经在本轮请求尾部。",
		Tone:        "tail",
		Bytes:       durableTailBytes,
		Contributes: true,
		Durable:     true,
		CacheScope:  "volatile",
	})
	add(ContextCompositionCategory{
		ID:          "request_only",
		Label:       "动态上下文",
		Description: "只进入这次模型请求，不写入对话历史。",
		Tone:        "dynamic",
		Bytes:       requestOnlyBytes,
		Contributes: true,
		Durable:     false,
		CacheScope:  "volatile",
		RequestOnly: true,
	})
	add(ContextCompositionCategory{
		ID:          "tool_schema",
		Label:       "工具 schema",
		Description: "本次请求暴露给模型的工具定义。",
		Tone:        "tools",
		Bytes:       max(0, record.ToolSchemaBytes),
		Contributes: true,
		Durable:     false,
		CacheScope:  "surface",
	})
	add(ContextCompositionCategory{
		ID:          "loadable_tool_schema",
		Label:       "可加载工具 schema",
		Description: "可按需加载的工具定义；用于可见性，不计入本次常驻请求面。",
		Tone:        "deferred",
		Bytes:       max(0, record.LoadableToolSchemaBytes),
		Tokens:      fallbackTokensForBytes(record.LoadableToolSchemaBytes),
		Contributes: false,
		Durable:     false,
		CacheScope:  "deferred",
		Deferred:    true,
	})
	return categories
}

func contextCompositionSections(sections []sessiontrace.SystemSectionRecord, systemBytes int, tokenCounter func(int, int, int) int) []ContextCompositionSection {
	if len(sections) == 0 {
		return nil
	}
	out := make([]ContextCompositionSection, 0, len(sections))
	for _, section := range sections {
		bytes := max(0, section.Bytes)
		out = append(out, ContextCompositionSection{
			Key:    section.Key,
			Static: section.Static,
			Bytes:  bytes,
			Tokens: tokenCounter(bytes, max(0, systemBytes), fallbackTokensForBytes(systemBytes)),
			Hash:   strings.TrimSpace(section.Hash),
		})
	}
	return out
}

func (s *Server) contextCompositionRuntimeInputLimit(provider, model string) int {
	if s == nil || s.rt == nil {
		return 0
	}
	if provider = strings.TrimSpace(provider); provider != "" && !strings.EqualFold(provider, strings.TrimSpace(s.rt.ProviderName)) {
		return 0
	}
	if model = strings.TrimSpace(model); model != "" && !strings.EqualFold(model, strings.TrimSpace(s.rt.Model)) {
		return 0
	}
	budget := s.rt.ModelBudget
	if budget.InputLimitTokens > 0 {
		return budget.InputLimitTokens
	}
	if contextWindow, _ := budget.EffectiveContextWindow(); contextWindow > 0 {
		return contextWindow
	}
	return 0
}

func effectiveContextCeilingTokens(contextWindowTokens, inputLimitTokens int) int {
	if inputLimitTokens > 0 && (contextWindowTokens <= 0 || inputLimitTokens < contextWindowTokens) {
		return inputLimitTokens
	}
	if contextWindowTokens > 0 {
		return contextWindowTokens
	}
	return 0
}

func categoryTokensForBytes(bytes, promptBytes, promptTokens int) int {
	bytes = max(0, bytes)
	if bytes == 0 {
		return 0
	}
	if promptBytes > 0 && promptTokens > 0 {
		return max(1, int(math.Round(float64(bytes)*float64(promptTokens)/float64(promptBytes))))
	}
	return fallbackTokensForBytes(bytes)
}

func fallbackTokensForBytes(bytes int) int {
	bytes = max(0, bytes)
	if bytes == 0 {
		return 0
	}
	return max(1, int(math.Ceil(float64(bytes)/4.0)))
}

func providerReportedTokenUsage(input, output, cacheCreation, cacheRead int) bool {
	return input > 0 || output > 0 || cacheCreation > 0 || cacheRead > 0
}

// latestProviderRetainedContextTokens returns the newest persisted context
// size that came from provider-reported usage. A context_tokens marker with
// no input, output, or cache counts is a local estimate. Seeding that value
// treats a partial total as ground truth and blocks the pre-send reconcile.
func (s *Server) latestProviderRetainedContextTokens(binding *runtime.State, threadID string) int {
	if th := s.threadForState(binding, threadID); th != nil {
		th.mu.Lock()
		defer th.mu.Unlock()
		for i := len(th.Turns) - 1; i >= 0; i-- {
			turn := th.Turns[i]
			if turn.ContextTokens <= 0 {
				continue
			}
			if !providerReportedTokenUsage(turn.InputTokens, turn.OutputTokens, turn.CacheCreationTokens, turn.CacheReadTokens) {
				return 0
			}
			return turn.ContextTokens
		}
	}
	if s == nil || s.rt == nil {
		return 0
	}
	metas, err := loadMetaMessages(binding.Sessions, threadID)
	if err != nil {
		return 0
	}
	for i := len(metas) - 1; i >= 0; i-- {
		meta := metas[i]
		if meta.ContextTokens <= 0 {
			continue
		}
		if !providerReportedTokenUsage(meta.InputTokens, meta.OutputTokens, meta.CacheCreationTokens, meta.CacheReadTokens) {
			return 0
		}
		return meta.ContextTokens
	}
	return 0
}

func (s *Server) latestRetainedContextTokens(binding *runtime.State, threadID string) int {
	if th := s.threadForState(binding, threadID); th != nil {
		th.mu.Lock()
		defer th.mu.Unlock()
		for i := len(th.Turns) - 1; i >= 0; i-- {
			if th.Turns[i].ContextTokens > 0 {
				return th.Turns[i].ContextTokens
			}
		}
	}
	if s == nil || s.rt == nil {
		return 0
	}
	metas, err := loadMetaMessages(binding.Sessions, threadID)
	if err != nil {
		return 0
	}
	for i := len(metas) - 1; i >= 0; i-- {
		if metas[i].ContextTokens > 0 {
			return metas[i].ContextTokens
		}
	}
	return 0
}
