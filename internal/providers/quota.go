package providers

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
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

type quotaReadFlight struct {
	done    chan struct{}
	cancel  context.CancelFunc
	waiters int
	body    []byte
	err     error
}

var quotaReads = struct {
	sync.Mutex
	pending map[[32]byte]*quotaReadFlight
}{pending: make(map[[32]byte]*quotaReadFlight)}

var quotaDefaultTransport, _ = http.DefaultTransport.(*http.Transport)

// ReadQuotaJSON shares only concurrently pending default-policy GET reads.
// Adapters resolve credentials before constructing the request, so changed
// tokens, account headers, endpoints and client timeouts cannot share a flight.
// Neither request identities nor completed responses are retained or logged.
func ReadQuotaJSON(client *http.Client, req *http.Request) ([]byte, error) {
	if client == nil {
		client = &http.Client{Timeout: quotaRequestTimeout}
	}
	transport, standardTransport := http.DefaultTransport.(*http.Transport)
	deadline, hasDeadline := req.Context().Deadline()
	if req.Method != http.MethodGet || req.URL == nil || req.Cancel != nil || (req.Body != nil && req.Body != http.NoBody) ||
		client.Transport != nil || client.Jar != nil || client.CheckRedirect != nil ||
		!standardTransport || transport != quotaDefaultTransport ||
		(hasDeadline && time.Until(deadline) > quotaRequestTimeout) {
		// Custom policies can attach identity in Do or depend on context. Keep
		// those reads, and explicitly longer deadlines, on their original path.
		return readQuotaJSON(client, req)
	}
	if req.Context().Err() != nil {
		return nil, NewQuotaError(QuotaErrorNetwork)
	}
	hash := sha256.New()
	// These fields contain only JSON-supported types. Encoding directly into
	// the digest avoids retaining a second plaintext credential serialization.
	_ = json.NewEncoder(hash).Encode(struct {
		Method, URL, Host string
		Header            http.Header
		Timeout           time.Duration
		Close             bool
	}{req.Method, req.URL.String(), req.Host, req.Header, client.Timeout, req.Close})
	var key [32]byte
	copy(key[:], hash.Sum(nil))
	quotaReads.Lock()
	if req.Context().Err() != nil {
		quotaReads.Unlock()
		return nil, NewQuotaError(QuotaErrorNetwork)
	}
	flight := quotaReads.pending[key]
	if flight == nil {
		// Each caller owns only its wait. The shared read keeps the existing
		// default bound and stops earlier when no valid caller still needs it.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(req.Context()), quotaRequestTimeout)
		// A caller may return on cancellation while another still waits. Own
		// the request and client policy before that caller can reuse either.
		request := req.Clone(ctx)
		sharedClient := &http.Client{Transport: transport, Timeout: client.Timeout}
		flight = &quotaReadFlight{done: make(chan struct{}), cancel: cancel}
		quotaReads.pending[key] = flight
		go func() {
			body, err := readQuotaJSON(sharedClient, request)
			quotaReads.Lock()
			flight.body, flight.err = body, err
			if quotaReads.pending[key] == flight {
				delete(quotaReads.pending, key)
			}
			close(flight.done)
			quotaReads.Unlock()
			cancel()
		}()
	}
	flight.waiters++
	quotaReads.Unlock()
	defer func() {
		quotaReads.Lock()
		flight.waiters--
		if flight.waiters == 0 && quotaReads.pending[key] == flight {
			delete(quotaReads.pending, key)
			flight.cancel()
		}
		quotaReads.Unlock()
	}()
	select {
	case <-req.Context().Done():
		return nil, NewQuotaError(QuotaErrorNetwork)
	case <-flight.done:
		if req.Context().Err() != nil {
			return nil, NewQuotaError(QuotaErrorNetwork)
		}
		return bytes.Clone(flight.body), flight.err
	}
}

// Neither upstream response bodies nor transport details are surfaced through
// safe errors. This same operation serves isolated and shared reads.
func readQuotaJSON(client *http.Client, req *http.Request) ([]byte, error) {
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
