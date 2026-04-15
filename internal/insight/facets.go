package insight

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/providers"
)

// ExtractFacet sends a session transcript to the LLM and extracts a structured Facet.
func ExtractFacet(ctx context.Context, client providers.Client, model string, sessionID string, transcript string) (Facet, error) {
	// Per-request timeout to avoid hanging on unresponsive APIs.
	reqCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	resp, err := client.Chat(reqCtx, providers.ChatRequest{
		Model: model,
		Messages: []providers.ChatMessage{
			{Role: "user", Content: facetExtractionPrompt + transcript},
		},
		Temperature: 0.1,
	})
	if err != nil {
		return Facet{}, err
	}

	facet, err := parseFacetResponse(resp.Content)
	if err != nil {
		return Facet{}, err
	}
	facet.SessionID = sessionID
	facet.ExtractedAt = time.Now().Unix()
	return facet, nil
}

// parseFacetResponse attempts to parse the LLM response as a Facet JSON.
// It tries direct parse first, then falls back to extracting JSON from code fences.
func parseFacetResponse(content string) (Facet, error) {
	content = strings.TrimSpace(content)

	// Try direct parse.
	var facet Facet
	if err := json.Unmarshal([]byte(content), &facet); err == nil {
		return facet, nil
	}

	// Try extracting from code fence.
	extracted := extractJSON(content)
	if extracted != "" {
		if err := json.Unmarshal([]byte(extracted), &facet); err == nil {
			return facet, nil
		}
	}

	// Fallback: extract the first syntactically valid JSON object.
	if candidate := extractFirstJSONObject(content); candidate != "" {
		if err := json.Unmarshal([]byte(candidate), &facet); err == nil {
			return facet, nil
		}
	}

	// Last resort: return a minimal facet.
	return Facet{
		Goal:    "unable to extract",
		Outcome: "unclear",
		Summary: truncateStr(content, 200),
	}, nil
}

var jsonFenceRe = regexp.MustCompile("(?s)```(?:json)?\\s*\n?(.*?)```")

func extractJSON(s string) string {
	matches := jsonFenceRe.FindStringSubmatch(s)
	if len(matches) >= 2 {
		return strings.TrimSpace(matches[1])
	}
	return ""
}

// extractFirstJSONObject finds the first valid JSON object in s using
// json.Decoder. Unlike strings.Index("{") + strings.LastIndex("}"),
// this handles nested braces, escaped strings, and multiple objects.
func extractFirstJSONObject(s string) string {
	idx := strings.Index(s, "{")
	if idx < 0 {
		return ""
	}
	dec := json.NewDecoder(strings.NewReader(s[idx:]))
	var raw json.RawMessage
	if err := dec.Decode(&raw); err != nil {
		return ""
	}
	return string(raw)
}
