package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/blueberrycongee/wuu/internal/modelrequest"
	"github.com/blueberrycongee/wuu/internal/modelroles"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providerfactory"
	"github.com/blueberrycongee/wuu/internal/providers"
)

const (
	modelRequestMaxMessages      = 32
	modelRequestMaxInputBytes    = 256 * 1024
	modelRequestDefaultMaxTokens = 1024
	modelRequestMaxOutputTokens  = 8192
	modelRequestMaxTimeoutMS     = 45_000
)

func (k *kernelHostServices) bindModelSession(session *Session, host *pluginhost.Host) {
	k.mu.Lock()
	k.modelSession = session
	k.modelHost = host
	k.mu.Unlock()
}

type modelRequestInvoker struct {
	parent *kernelHostServices
}

func (k *modelRequestInvoker) ID() string                { return k.parent.ID() }
func (k *modelRequestInvoker) Status() pluginhost.Status { return k.parent.Status() }

func (k *modelRequestInvoker) InvokeService(callCtx context.Context, params pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	if err := callCtx.Err(); err != nil {
		return nil, err
	}
	if params.Method != pluginhost.KernelServiceMethod {
		return nil, serviceError("method_not_found", "kernel service method is unavailable")
	}
	k.parent.mu.RLock()
	active, runtimeSession, host := k.parent.active, k.parent.modelSession, k.parent.modelHost
	k.parent.mu.RUnlock()
	if !active || runtimeSession == nil || host == nil {
		return nil, serviceError("service_unavailable", "model service is unavailable")
	}
	// Registry routing alone is not authority to start inference. Bind it to the
	// caller's live host-owned execution (tool, capability, service callback or
	// background job), so ending or revoking it cancels outstanding provider work.
	scope, scopeErr := host.ResolveExecution(params.Caller, params.ExecutionID)
	if scopeErr != nil {
		return nil, scopeErr
	}
	var input pluginhost.ModelRequestParams
	if err := decodeServiceParams(params.Params, &input); err != nil {
		return nil, err
	}
	if len(input.Messages) == 0 || len(input.Messages) > modelRequestMaxMessages {
		return nil, serviceError("invalid_params", "messages must contain between 1 and 32 entries")
	}
	messages := make([]providers.ChatMessage, 0, len(input.Messages))
	totalBytes := 0
	for _, message := range input.Messages {
		switch message.Role {
		case "system", "user", "assistant":
		default:
			return nil, serviceError("invalid_params", "message role must be system, user or assistant")
		}
		if strings.TrimSpace(message.Content) == "" || !utf8.ValidString(message.Content) {
			return nil, serviceError("invalid_params", "message content must be non-empty UTF-8 text")
		}
		totalBytes += len(message.Content)
		if totalBytes > modelRequestMaxInputBytes {
			return nil, serviceError("limit_exceeded", "messages exceed 262144 UTF-8 bytes")
		}
		messages = append(messages, providers.ChatMessage{Role: message.Role, Content: message.Content})
	}
	if input.MaxOutputTokens == 0 {
		input.MaxOutputTokens = modelRequestDefaultMaxTokens
	}
	if input.MaxOutputTokens < 1 || input.MaxOutputTokens > modelRequestMaxOutputTokens {
		return nil, serviceError("invalid_params", "max_output_tokens must be between 1 and 8192")
	}
	if input.TimeoutMS == 0 {
		input.TimeoutMS = modelRequestMaxTimeoutMS
	}
	if input.TimeoutMS < 1 || input.TimeoutMS > modelRequestMaxTimeoutMS {
		return nil, serviceError("invalid_params", "timeout_ms must be between 1 and 45000")
	}
	input.ModelAlias = strings.TrimSpace(input.ModelAlias)
	if len(input.ModelAlias) > 128 {
		return nil, serviceError("invalid_params", "model_alias exceeds 128 bytes")
	}
	ctx, cancel := context.WithTimeout(scope.Context, time.Duration(input.TimeoutMS)*time.Millisecond)
	defer cancel()
	stop := context.AfterFunc(callCtx, cancel)
	defer stop()
	if err := ctx.Err(); err != nil {
		return nil, modelRequestError(err)
	}

	// Load one coherent host configuration instead of reading the mutable
	// foreground runner while another client changes its selected model.
	cfg, _, err := runtimeSession.LoadEffectiveConfig()
	if err != nil {
		return nil, serviceError("service_unavailable", "model configuration is unavailable")
	}
	var selection modelroles.Selection
	if input.ModelAlias != "" {
		selection, err = modelroles.ResolveAlias(cfg, input.ModelAlias)
		if err != nil {
			return nil, serviceError("invalid_params", "configured model alias is unavailable")
		}
	} else {
		roles, resolveErr := modelroles.Resolve(cfg, modelroles.ResolveOptions{
			Effort: cfg.Agent.Effort, Variant: cfg.Agent.Variant,
		})
		if resolveErr != nil {
			return nil, serviceError("service_unavailable", "configured default model is unavailable")
		}
		selection = roles.Main
	}
	client, err := providerfactory.BuildClient(selection.RuleProviderConfig, selection.Provider)
	if err != nil {
		return nil, serviceError("service_unavailable", "configured model client is unavailable")
	}
	request := providers.ChatRequest{
		Provider: selection.Provider, Model: selection.APIModel,
		Messages: messages, MaxTokens: input.MaxOutputTokens,
		Temperature: cfg.Agent.Temperature, Effort: selection.LegacyEffort,
		ProviderOptions: selection.ProviderOptions,
	}
	// Preserve an existing workflow's owner and budget. Client-request
	// capabilities have no parent inference journal, so attribute them to the
	// validated plugin instead of creating a hidden conversation.
	if providers.InferenceJournalFromContext(ctx) == nil {
		journal := runtimeSession.InferenceJournalForOwner("plugin:" + params.Caller)
		if journal == nil {
			return nil, serviceError("service_unavailable", "inference accounting is unavailable")
		}
		ctx = providers.WithInferenceJournal(ctx, journal)
	}
	// This auxiliary request intentionally has no agent-loop callbacks, injected
	// history or tools. Provider preparation, credentials, capacity, retry and
	// durable accounting use the ordinary host inference path.
	response, err := modelrequest.Execute(ctx, client, request)
	if err != nil {
		if errors.Is(err, modelrequest.ErrUnsupportedLimit) {
			return nil, serviceError("unsupported_limit", err.Error())
		}
		if errors.Is(err, modelrequest.ErrInvalidResponse) {
			return nil, serviceError("invalid_response", err.Error())
		}
		return nil, modelRequestError(err)
	}
	finishReason := response.FinishReason
	if finishReason == "" {
		finishReason = providers.NormalizeFinishReason(response.StopReason, response.Truncated, false)
	}
	if finishReason == "" {
		finishReason = providers.FinishReasonUnknown
	}
	result := pluginhost.ModelRequestResult{
		Text: response.Content, Provider: request.Provider, Model: request.Model,
		FinishReason: string(finishReason), Truncated: response.Truncated || finishReason == providers.FinishReasonLength,
	}
	if response.Usage != nil {
		result.Usage = &pluginhost.ModelRequestUsage{
			InputTokens: response.Usage.InputTokens, OutputTokens: response.Usage.OutputTokens,
			CacheCreationTokens: response.Usage.CacheCreationTokens, CacheReadTokens: response.Usage.CacheReadTokens,
			CacheCreationUnknown: response.Usage.CacheCreationUnknown,
		}
	}
	return marshalServiceResult(result)
}

func modelRequestError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return serviceError("deadline_exceeded", "model request deadline exceeded")
	}
	if errors.Is(err, context.Canceled) {
		return serviceError("request_cancelled", "model request was cancelled")
	}
	// Provider failures can contain response bodies or credential fragments.
	// Keep the typed category, not the provider's raw error, on the plugin wire.
	failure := providers.NormalizeFailure(err)
	return serviceError("inference_failed", "model request failed: "+string(failure.Category))
}
