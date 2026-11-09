package providers

import (
	"encoding/json"
	"net/http"
	"strings"
)

// NewHTTPError retains a bounded response body's original shape, including
// direct-admission diagnostics that do not have an API error envelope.
func NewHTTPError(family string, response *http.Response, body []byte) *HTTPError {
	var envelope struct {
		Error struct {
			Code  string `json:"code"`
			Param string `json:"param"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &envelope)
	return &HTTPError{
		ProviderFamily:  family,
		ProviderCode:    envelope.Error.Code,
		ProviderParam:   envelope.Error.Param,
		RequestID:       strings.TrimSpace(response.Header.Get("x-request-id")),
		StatusCode:      response.StatusCode,
		Body:            string(body),
		RetryAfter:      ParseRetryAfter(response),
		ContextOverflow: DetectContextOverflow(string(body)),
	}
}
