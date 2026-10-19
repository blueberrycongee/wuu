package providers

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// InferenceRequestHash returns a deterministic identity for the provider
// payload without retaining any of its contents. Runtime-only operation,
// attempt, execution, and telemetry fields are excluded.
func InferenceRequestHash(req ChatRequest) (string, error) {
	payload := struct {
		Adapter                     string `json:",omitempty"`
		Model                       string
		Messages                    []ChatMessage
		Tools                       []ToolDefinition
		Temperature                 float64
		CacheHint                   *CacheHint
		MaxTokens                   int
		Effort                      string
		ProviderOptions             map[string]any
		NativeDeferredToolDiscovery bool
		ForceToolName               string
	}{
		Adapter:                     req.Adapter,
		Model:                       req.Model,
		Messages:                    req.Messages,
		Tools:                       req.Tools,
		Temperature:                 req.Temperature,
		CacheHint:                   req.CacheHint,
		MaxTokens:                   req.MaxTokens,
		Effort:                      req.Effort,
		ProviderOptions:             req.ProviderOptions,
		NativeDeferredToolDiscovery: req.NativeDeferredToolDiscovery,
		ForceToolName:               req.ForceToolName,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("hash inference request: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}
