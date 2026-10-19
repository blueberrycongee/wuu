// Package modelrequest executes explicit one-shot text requests without owning
// model selection, prompts, permission decisions, or invocation lifetimes.
package modelrequest

import (
	"context"
	"errors"
	"strings"

	"github.com/blueberrycongee/wuu/internal/provideroptions"
	"github.com/blueberrycongee/wuu/internal/providers"
)

var (
	ErrUnsupportedLimit = errors.New("configured model provider cannot preserve the requested output token limit")
	ErrInvalidResponse  = errors.New("model did not return a text-only response")
)

// Execute preserves the caller's client, route, context, tuning, and inference
// journal. A positive MaxTokens is a strict requested cap unless the caller
// permits an adapter to omit it; zero keeps the provider's existing token policy.
// Callers supply the explicit text messages
// and tool-free request; this helper adds no context or agent loop and retains
// the supplied client's preparation and transport behavior.
func Execute(ctx context.Context, client providers.Client, request providers.ChatRequest, options Options) (providers.ChatResponse, error) {
	if err := ctx.Err(); err != nil {
		return providers.ChatResponse{}, err
	}
	if client == nil {
		return providers.ChatResponse{}, errors.New("text request client is required")
	}
	base := textClient{Client: client, maxOutputTokens: request.MaxTokens, allowUnboundedOutput: options.AllowUnboundedOutput}
	var scoped providers.Client = base
	if transport, ok := client.(interface{ RequiresStreamingChat() bool }); ok && transport.RequiresStreamingChat() {
		stream, ok := client.(providers.StreamClient)
		if !ok {
			return providers.ChatResponse{}, errors.New("streaming model client is required")
		}
		scoped = streamingTextClient{textClient: base, stream: stream}
	}
	if recovery, ok := client.(providers.InferenceRecoveryApplier); ok {
		if stream, ok := scoped.(streamingTextClient); ok {
			scoped = recoveringStreamingTextClient{streamingTextClient: stream, InferenceRecoveryApplier: recovery}
		} else {
			scoped = recoveringTextClient{textClient: base, InferenceRecoveryApplier: recovery}
		}
	}
	response, err := providers.ExecuteChat(ctx, scoped, request, providers.InferenceOperationAuxiliary, providers.InferenceProfileInteractive)
	if err != nil {
		return response, err
	}
	if err := ctx.Err(); err != nil {
		return response, err
	}
	if len(response.ToolCalls) > 0 || strings.TrimSpace(response.Content) == "" {
		return response, ErrInvalidResponse
	}
	return response, nil
}

type Options struct {
	// AllowUnboundedOutput lets adapters that cannot preserve MaxTokens use
	// their normal output policy. Providers that support the cap still receive it.
	AllowUnboundedOutput bool
}

type textClient struct {
	providers.Client
	maxOutputTokens      int
	allowUnboundedOutput bool
}

// Preparation stays inside the ordinary executor, including retries, so the
// protected request is what gets hashed and durably accounted before submission.
func (c textClient) PrepareInferenceRequest(ctx context.Context, request providers.ChatRequest) (providers.ChatRequest, error) {
	request.ProviderOptions = textOptions(request.ProviderOptions, c.maxOutputTokens > 0)
	if preparer, ok := c.Client.(providers.InferenceRequestPreparer); ok {
		var err error
		request, err = preparer.PrepareInferenceRequest(ctx, request)
		if err != nil {
			return request, err
		}
	}
	if c.maxOutputTokens > 0 && !c.allowUnboundedOutput && (request.MaxTokens <= 0 || request.MaxTokens > c.maxOutputTokens) {
		return request, ErrUnsupportedLimit
	}
	// Keep trusted provider normalization intact. Native clients can prepare the
	// request again before sending, so stripping adapter defaults here would make
	// that second preparation disagree with the journal's request hash.
	return request, nil
}

// Preserve the provider's recovery capability rather than claiming that every
// client can refresh authentication.
type recoveringTextClient struct {
	textClient
	providers.InferenceRecoveryApplier
}

type streamingTextClient struct {
	textClient
	stream providers.StreamClient
}

// Keep the unary executor's preparation, journal and retry ownership while
// using a provider that requires a streaming wire request.
func (c streamingTextClient) Chat(ctx context.Context, request providers.ChatRequest) (providers.ChatResponse, error) {
	events, err := c.stream.StreamChat(ctx, request)
	if err != nil {
		return providers.ChatResponse{}, err
	}
	var response providers.ChatResponse
	for {
		select {
		case <-ctx.Done():
			return response, ctx.Err()
		case event, open := <-events:
			if !open {
				return response, errors.New("model stream ended before completion")
			}
			switch event.Type {
			case providers.EventContentDelta:
				response.Content += event.Content
			case providers.EventContentReplace:
				response.Content = event.Content
			case providers.EventToolUseEnd:
				if event.ToolCall != nil {
					response.ToolCalls = append(response.ToolCalls, *event.ToolCall)
				}
			case providers.EventUsage:
				response.Usage = event.Usage
			case providers.EventError:
				if event.Error != nil {
					return response, event.Error
				}
				return response, errors.New("model stream failed")
			case providers.EventDone:
				if event.Usage != nil {
					response.Usage = event.Usage
				}
				response.StopReason = event.StopReason
				response.FinishReason = event.FinishReason
				response.Truncated = event.Truncated
				return response, nil
			}
		}
	}
}

type recoveringStreamingTextClient struct {
	streamingTextClient
	providers.InferenceRecoveryApplier
}

func textOptions(options map[string]any, bounded bool) map[string]any {
	options = provideroptions.Clone(options)
	// OpenAI-compatible providers merge options into the final wire payload.
	// Tuning must not replace the explicit route, evidence, or tool-free request.
	for _, key := range []string{
		"model", "models", "messages", "input", "instructions", "system", "prompt", "suffix", "prediction",
		"previous_response_id", "conversation", "context_management", "container",
		"tools", "tool_choice", "functions", "function_call", "parallel_tool_calls", "parallelToolCalls",
		"web_search_options", "search_parameters", "plugins",
		"stream", "stream_options", "background", "n", "best_of", "modalities", "audio",
		"store",
	} {
		delete(options, key)
	}
	// Keep omitStore: some configured Responses endpoints reject the store
	// field entirely. It is a host transport-compatibility flag, not a wire value.
	if bounded {
		for _, key := range []string{"maxOutputTokens", "max_output_tokens", "max_tokens", "max_completion_tokens"} {
			delete(options, key)
		}
	}
	return options
}
