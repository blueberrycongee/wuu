package tui

import (
	"errors"
	"fmt"
	"strings"

	"github.com/blueberrycongee/wuu/internal/providers"
)

// errorDetailBodyBudget caps the provider response body snippet we include
// in the error card. The Anthropic/OpenAI upstream can echo back the full
// offending prompt on a 400, which is useless and crowds the chat. 400
// characters is enough to see the error code + message, without filling
// the screen with noise.
const errorDetailBodyBudget = 400

// formatErrorDetails returns a multi-line, human-readable detail block to
// render beneath the red "ERROR: <summary>" headline. The summary alone
// often hides what you need to act on — HTTP status, retry-after, or the
// verbatim upstream body — so we pull those out here.
//
// Returns "" when there is nothing more useful to say beyond the summary.
func formatErrorDetails(err error) string {
	if err == nil {
		return ""
	}

	var lines []string

	// HTTPError — status code, retry-after, and a bounded body snippet.
	var httpErr *providers.HTTPError
	if errors.As(err, &httpErr) {
		header := fmt.Sprintf("HTTP %d", httpErr.StatusCode)
		if httpErr.RetryAfter > 0 {
			header += fmt.Sprintf(" · retry after %s", httpErr.RetryAfter.Round(1e9))
		}
		lines = append(lines, header)
		if body := strings.TrimSpace(httpErr.Body); body != "" {
			lines = append(lines, truncateDetail(body, errorDetailBodyBudget))
		}
		return strings.Join(lines, "\n")
	}

	// StreamError — the in-band provider error that arrived as an SSE
	// payload. Code is often the most specific thing we have.
	var streamErr *providers.StreamError
	if errors.As(err, &streamErr) {
		if streamErr.Code != "" {
			lines = append(lines, "code: "+streamErr.Code)
		}
		msg := strings.TrimSpace(streamErr.Message)
		if msg != "" {
			lines = append(lines, truncateDetail(msg, errorDetailBodyBudget))
		}
		return strings.Join(lines, "\n")
	}

	// Generic fallback: show the full wrapped chain if it adds information
	// beyond the summary already printed. Same budget applies.
	raw := strings.TrimSpace(err.Error())
	if raw == "" {
		return ""
	}
	return truncateDetail(raw, errorDetailBodyBudget)
}

func truncateDetail(s string, maxChars int) string {
	s = strings.TrimSpace(s)
	if maxChars <= 0 || len(s) <= maxChars {
		return s
	}
	// Prefer to cut at the last newline before the budget so we leave
	// whole lines visible when possible.
	cut := maxChars
	if nl := strings.LastIndex(s[:maxChars], "\n"); nl > maxChars/2 {
		cut = nl
	}
	return s[:cut] + "…"
}
