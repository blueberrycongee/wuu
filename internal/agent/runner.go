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

// Run executes one prompt with optional tool-use loop.
func (r *Runner) Run(ctx context.Context, prompt string) (string, error) {
	if r.Client == nil {
		return "", errors.New("client is required")
	}
	if strings.TrimSpace(r.Model) == "" {
		return "", errors.New("model is required")
	}
	if strings.TrimSpace(prompt) == "" {
		return "", errors.New("prompt is required")
	}

	maxSteps := r.MaxSteps
	if maxSteps <= 0 {
		maxSteps = 8
	}

	messages := []providers.ChatMessage{}
	if strings.TrimSpace(r.SystemPrompt) != "" {
		messages = append(messages, providers.ChatMessage{Role: "system", Content: r.SystemPrompt})
	}
	messages = append(messages, providers.ChatMessage{Role: "user", Content: prompt})

	for step := 0; step < maxSteps; step++ {
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
			return "", err
		}

		assistant := providers.ChatMessage{
			Role:      "assistant",
			Content:   resp.Content,
			ToolCalls: resp.ToolCalls,
		}
		messages = append(messages, assistant)

		if len(resp.ToolCalls) == 0 {
			if strings.TrimSpace(resp.Content) == "" {
				return "", errors.New("model returned empty answer")
			}
			return resp.Content, nil
		}

		if r.Tools == nil {
			return "", errors.New("model requested tools but none are configured")
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

	return "", fmt.Errorf("max steps exceeded (%d)", maxSteps)
}

func errorJSON(err error) string {
	payload := map[string]any{"error": err.Error()}
	b, marshalErr := json.Marshal(payload)
	if marshalErr != nil {
		return `{"error":"tool execution failed"}`
	}
	return string(b)
}
