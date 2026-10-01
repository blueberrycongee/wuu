package providers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

// QuotaReader reads account allowances without submitting an inference request.
// A failed read may return account metadata, but never fabricated allowance.
type QuotaReader interface {
	ReadQuota(context.Context) (Quota, error)
}

// Quota is an upstream snapshot, not a price estimate or local token ledger.
// ObservedAt remains the successful read time when stale data is retained.
type Quota struct {
	Status       string         `json:"status"`
	Kind         string         `json:"kind,omitempty"`
	Account      *QuotaAccount  `json:"account,omitempty"`
	Plan         string         `json:"plan,omitempty"`
	CheckedAt    string         `json:"checked_at"`
	ObservedAt   string         `json:"observed_at,omitempty"`
	ExpiresAt    string         `json:"expires_at,omitempty"`
	ErrorCode    string         `json:"error_code,omitempty"`
	Windows      []QuotaWindow  `json:"windows,omitempty"`
	Balances     []QuotaBalance `json:"balances,omitempty"`
	ResetCredits *int           `json:"reset_credits,omitempty"`
}

// ID is an opaque, credential-scoped identity. It must not contain a token,
// filesystem path, or a raw key; Label is a vendor-reported account name.
type QuotaAccount struct {
	ID     string `json:"id"`
	Label  string `json:"label,omitempty"`
	Source string `json:"source,omitempty"`
}

// A missing percentage is unknown. Unlimited is set only for an explicit
// upstream no-cap value; missing or zero limits do not establish that state.
type QuotaWindow struct {
	ID            string   `json:"id"`
	Label         string   `json:"label,omitempty"`
	UsedPercent   *float64 `json:"used_percent,omitempty"`
	WindowMinutes int      `json:"window_minutes,omitempty"`
	ResetsAt      string   `json:"resets_at,omitempty"`
	Display       string   `json:"display,omitempty"`
	Model         string   `json:"model,omitempty"`
	Scope         string   `json:"scope,omitempty"`
	Unlimited     bool     `json:"unlimited,omitempty"`
}

// Amount is a decimal string so currency values survive JSON without rounding.
type QuotaBalance struct {
	Currency string `json:"currency"`
	Amount   string `json:"amount"`
}

const (
	QuotaErrorNetwork         = "network"
	QuotaErrorRateLimited     = "rate_limited"
	QuotaErrorSignIn          = "sign_in"
	QuotaErrorInvalidResponse = "invalid_response"
	QuotaErrorUnsupported     = "unsupported"
	quotaJSONMaxBytes         = 1 << 20
	quotaRequestTimeout       = 20 * time.Second
)

// QuotaError contains only a stable public code. It deliberately excludes
// upstream bodies, request URLs, credentials, and transport error strings.
type QuotaError struct {
	code string
}

func (e *QuotaError) Error() string {
	return "quota read failed (" + e.code + ")"
}

// NewQuotaError constructs a quota error with one of the public quota codes.
func NewQuotaError(code string) *QuotaError {
	switch code {
	case QuotaErrorNetwork, QuotaErrorRateLimited, QuotaErrorSignIn, QuotaErrorInvalidResponse, QuotaErrorUnsupported:
		return &QuotaError{code: code}
	default:
		return &QuotaError{code: QuotaErrorInvalidResponse}
	}
}

// QuotaErrorCode returns the stable code for an adapter error. Unexpected
// errors are conservatively reported as invalid_response.
func QuotaErrorCode(err error) string {
	var quotaErr *QuotaError
	if errors.As(err, &quotaErr) && quotaErr != nil {
		return quotaErr.code
	}
	return QuotaErrorInvalidResponse
}

// QuotaAccountID returns a non-reversible identity scoped to a source,
// endpoint, and upstream account ID or credential. Input values are never
// included in the returned value.
func QuotaAccountID(source, endpoint, identity string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(source) + "\x00" + strings.TrimSpace(endpoint) + "\x00" + strings.TrimSpace(identity)))
	return hex.EncodeToString(sum[:16])
}

// ReadQuotaJSON performs the bounded, status-classifying HTTP operation used
// by quota adapters. Neither upstream response bodies nor transport details
// are surfaced through its safe errors.
func ReadQuotaJSON(client *http.Client, req *http.Request) ([]byte, error) {
	if client == nil {
		client = &http.Client{Timeout: quotaRequestTimeout}
	}
	if _, ok := req.Context().Deadline(); !ok {
		ctx, cancel := context.WithTimeout(req.Context(), quotaRequestTimeout)
		defer cancel()
		req = req.Clone(ctx)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, NewQuotaError(QuotaErrorNetwork)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		switch resp.StatusCode {
		case http.StatusUnauthorized, http.StatusForbidden:
			return nil, NewQuotaError(QuotaErrorSignIn)
		case http.StatusTooManyRequests:
			return nil, NewQuotaError(QuotaErrorRateLimited)
		default:
			return nil, NewQuotaError(QuotaErrorInvalidResponse)
		}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, quotaJSONMaxBytes+1))
	if err != nil || len(body) > quotaJSONMaxBytes {
		return nil, NewQuotaError(QuotaErrorInvalidResponse)
	}
	return body, nil
}
