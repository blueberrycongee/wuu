package providers

import (
	"encoding/json"
	"errors"
	"strings"
)

// ProviderFailureDetail returns a bounded, credential-redacted provider message.
// Only the structured error message crosses the adapter boundary; response
// bodies, echoed request fields and unstructured proxy pages remain private.
func ProviderFailureDetail(err error) string {
	var message string
	var httpErr *HTTPError
	var streamErr *StreamError
	switch {
	case errors.As(err, &httpErr):
		body := httpErr.Body
		// HTTP adapters may prefix the JSON response with the HTTP status.
		start := strings.IndexByte(body, '{')
		if start < 0 {
			return ""
		}
		var payload struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
			Message string `json:"message"`
		}
		if json.Unmarshal([]byte(body[start:]), &payload) != nil {
			return ""
		}
		message = payload.Error.Message
		if message == "" {
			message = payload.Message
		}
	case errors.As(err, &streamErr):
		message = streamErr.Message
	}
	if strings.TrimSpace(message) == "" {
		return ""
	}
	return sanitizeInferenceFailureMessage(errors.New(message))
}
