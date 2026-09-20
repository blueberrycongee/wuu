package grokbuild

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/blueberrycongee/wuu/internal/providers"
)

// transientProxyAuthError is a Grok Build 401 whose body is only the generic
// proxy phrase "Authentication required". cli-chat-proxy has returned that
// after a long pre-stream hang while the same local Grok CLI login continued
// to succeed on other requests. Replay the payload; do not ask for grok login.
type transientProxyAuthError struct {
	err error
}

func (e *transientProxyAuthError) Error() string {
	if e == nil || e.err == nil {
		return "Grok Build proxy returned a transient authentication rejection"
	}
	return "Grok Build proxy returned a transient authentication rejection: " + e.err.Error()
}

func (e *transientProxyAuthError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func (e *transientProxyAuthError) InferenceRecoveryAction() providers.RecoveryActionKind {
	return providers.RecoveryReplaySame
}

func credentialError(err error) error {
	if err == nil {
		return nil
	}
	if !isAuthStatusError(err) {
		return err
	}
	if isGenericGrokProxyAuthRejection(err) {
		return &transientProxyAuthError{err: err}
	}
	return fmt.Errorf("Grok Build login was rejected; run `grok login` and try again: %w", err)
}

func isAuthStatusError(err error) bool {
	var httpErr *providers.HTTPError
	if errors.As(err, &httpErr) {
		return httpErr.StatusCode == http.StatusUnauthorized || httpErr.StatusCode == http.StatusForbidden
	}
	var streamErr *providers.StreamError
	if errors.As(err, &streamErr) {
		return streamErr.Auth || streamErr.Code == "401" || streamErr.Code == "403"
	}
	return false
}

func isGenericGrokProxyAuthRejection(err error) bool {
	status, body := authErrorSurface(err)
	if status != http.StatusUnauthorized {
		return false
	}
	return isGenericGrokProxyAuthBody(body)
}

func authErrorSurface(err error) (int, string) {
	var httpErr *providers.HTTPError
	if errors.As(err, &httpErr) {
		return httpErr.StatusCode, httpErr.Body
	}
	var streamErr *providers.StreamError
	if errors.As(err, &streamErr) {
		switch strings.TrimSpace(streamErr.Code) {
		case "401":
			return http.StatusUnauthorized, streamErr.Message
		case "403":
			return http.StatusForbidden, streamErr.Message
		}
		if streamErr.Auth {
			return http.StatusUnauthorized, streamErr.Message
		}
	}
	return 0, ""
}

func isGenericGrokProxyAuthBody(body string) bool {
	lower := strings.ToLower(body)
	for _, marker := range []string{
		"invalid or expired credentials",
		"invalid api key",
		"incorrect api key",
		"no auth context",
	} {
		if strings.Contains(lower, marker) {
			return false
		}
	}
	return strings.Contains(lower, "authentication required")
}
