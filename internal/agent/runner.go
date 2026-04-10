package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/blueberrycongee/wuu/internal/providers"
)

// ToolExecutor executes model-requested tool calls.
type ToolExecutor interface {
	Definitions() []providers.ToolDefinition
	Execute(ctx context.Context, call providers.ToolCall) (string, error)
}

// Runner manages one multi-step coding turn.
type Runner struct {
	Client       providers.Client
	Tools        ToolExecutor
	Model        string
	SystemPrompt string
	MaxSteps     int
	Temperature  float64
}

// RunResult is the structured outcome of a Runner.RunWithUsage call.
type RunResult struct {
	Content      string
	InputTokens  int
	OutputTokens int
}

// Run executes one prompt with optional tool-use loop.
func (r *Runner) Run(ctx context.Context, prompt string) (string, error) {
	res, err := r.RunWithUsage(ctx, prompt, nil)
	if err != nil {
		return "", err
	}
	return res.Content, nil
}

// RunWithUsage is like Run but reports per-call token usage to the
// optional onUsage callback (called once per LLM round-trip) and
// returns cumulative totals in the result.
func (r *Runner) RunWithUsage(ctx context.Context, prompt string, onUsage func(input, output int)) (RunResult, error) {
	if r.Client == nil {
		return RunResult{}, errors.New("client is required")
	}
	if strings.TrimSpace(r.Model) == "" {
		return RunResult{}, errors.New("model is required")
	}
	if strings.TrimSpace(prompt) == "" {
		return RunResult{}, errors.New("prompt is required")
	}

	// MaxSteps == 0 means unlimited (no step cap). Aligned with
	// Claude Code's default behavior — the model decides when to
	// stop by emitting a final assistant message. Users who want a
	// runaway safety net can set MaxSteps to a positive number.
	maxSteps := r.MaxSteps

	messages := []providers.ChatMessage{}
	if strings.TrimSpace(r.SystemPrompt) != "" {
		messages = append(messages, providers.ChatMessage{Role: "system", Content: r.SystemPrompt})
	}
	messages = append(messages, providers.ChatMessage{Role: "user", Content: prompt})

	totalIn, totalOut := 0, 0

	for step := 0; maxSteps == 0 || step < maxSteps; step++ {
		req := providers.ChatRequest{
			Model:       r.Model,
			Messages:    messages,
			Temperature: r.Temperature,
		}
		if r.Tools != nil {
			req.Tools = r.Tools.Definitions()
		}

		resp, err := r.Client.Chat(ctx, req)
		if err != nil {
			return RunResult{InputTokens: totalIn, OutputTokens: totalOut}, err
		}
		if resp.Usage != nil {
			totalIn += resp.Usage.InputTokens
			totalOut += resp.Usage.OutputTokens
			if onUsage != nil {
				onUsage(resp.Usage.InputTokens, resp.Usage.OutputTokens)
			}
		}

		assistant := providers.ChatMessage{
			Role:      "assistant",
			Content:   resp.Content,
			ToolCalls: resp.ToolCalls,
		}
		messages = append(messages, assistant)

		if len(resp.ToolCalls) == 0 {
			if strings.TrimSpace(resp.Content) == "" {
				return RunResult{InputTokens: totalIn, OutputTokens: totalOut}, errors.New("model returned empty answer")
			}
			return RunResult{Content: resp.Content, InputTokens: totalIn, OutputTokens: totalOut}, nil
		}

		if r.Tools == nil {
			return RunResult{InputTokens: totalIn, OutputTokens: totalOut}, errors.New("model requested tools but none are configured")
		}

		for _, call := range resp.ToolCalls {
			toolResult, execErr := r.Tools.Execute(ctx, call)
			if execErr != nil {
				toolResult = errorJSON(execErr)
			}
			messages = append(messages, providers.ChatMessage{
				Role:       "tool",
				Name:       call.Name,
				ToolCallID: call.ID,
				Content:    toolResult,
			})
		}
	}

	return RunResult{InputTokens: totalIn, OutputTokens: totalOut}, fmt.Errorf("max steps exceeded (%d)", maxSteps)
}

func errorJSON(err error) string {
	payload := map[string]any{"error": err.Error()}
	b, marshalErr := json.Marshal(payload)
	if marshalErr != nil {
		return `{"error":"tool execution failed"}`
	}
	return string(b)
}
