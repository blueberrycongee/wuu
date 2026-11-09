// Package plugincredentials keeps extension credentials in the host's existing
// OS credential store and binds their use to a declared HTTPS audience.
package plugincredentials

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/blueberrycongee/wuu/internal/credentialstore"
	"golang.org/x/net/http/httpguts"
)

const maxBodyBytes = 4 << 20

// Binding is constructed from the admitted plugin manifest, never RPC input.
// Changing the source identity, home or allowed origins requires a fresh secret.
type Binding struct {
	Home    string
	Subject string
	Project string
	Key     string
	Origins []string
}

func (b Binding) identity() (string, string) {
	origins := append([]string(nil), b.Origins...)
	sort.Strings(origins)
	raw, _ := json.Marshal([]any{b.Home, b.Subject, b.Project, b.Key, origins})
	digest := sha256.Sum256(raw)
	return "plugin-credentials", hex.EncodeToString(digest[:])
}

// Open uses the same OS backend as MCP OAuth. It never silently falls back to
// an ordinary file, environment variable, plugin setting or plugin storage.
func Open() (credentialstore.Store, error) {
	store, err := credentialstore.NewDesktopStore()
	if err != nil {
		return nil, errors.New("OS credential storage is unavailable on this platform")
	}
	return store, nil
}

func Configured(ctx context.Context, store credentialstore.Store, binding Binding) (bool, error) {
	namespace, key := binding.identity()
	value, err := store.Get(ctx, namespace, key)
	defer clear(value)
	if errors.Is(err, credentialstore.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, errors.New("OS credential storage is unavailable; unlock Keychain or Secret Service and retry")
	}
	return len(value) > 0, nil
}

// Set replaces a token. An empty value explicitly removes it. The caller must
// originate from the host settings UI, never a plugin tool or generated bundle.
func Set(ctx context.Context, store credentialstore.Store, binding Binding, value string) error {
	if len(value) > 16<<10 || strings.ContainsAny(value, "\r\n\x00\t ") {
		return errors.New("credential must be a token without whitespace, within 16384 bytes")
	}
	namespace, key := binding.identity()
	var err error
	if value == "" {
		err = store.Delete(ctx, namespace, key)
	} else {
		data := []byte(value)
		defer clear(data)
		err = store.Set(ctx, namespace, key, data)
	}
	if err != nil {
		return errors.New("credential was not saved; unlock OS Keychain or Secret Service and retry")
	}
	return nil
}

type RequestParams struct {
	Credential string            `json:"credential"`
	URL        string            `json:"url"`
	Method     string            `json:"method"`
	Headers    map[string]string `json:"headers,omitempty"`
	Body       string            `json:"body,omitempty"`
}

type RequestResult struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
}

// Request attaches a Bearer token only after validating the entire destination.
// Its fixed timeout, blocked redirects, bounded bodies and sanitized errors apply
// equally to every plugin. A transport error cannot establish a write's outcome.
func Request(ctx context.Context, store credentialstore.Store, binding Binding, params RequestParams) (RequestResult, error) {
	var result RequestResult
	destination, err := url.Parse(params.URL)
	if err != nil || destination.Scheme != "https" || destination.Host == "" || destination.User != nil || destination.Fragment != "" || destination.Opaque != "" {
		return result, errors.New("credential request requires an HTTPS URL without user information or fragment")
	}
	origin := destination.Scheme + "://" + destination.Host
	allowed := false
	for _, candidate := range binding.Origins {
		allowed = allowed || origin == candidate
	}
	if !allowed {
		return result, errors.New("credential request origin is not declared by this plugin")
	}
	switch params.Method {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodHead:
	default:
		return result, errors.New("credential request method is unsupported")
	}
	if len(params.Body) > maxBodyBytes || len(params.URL) > 16<<10 || len(params.Headers) > 32 {
		return result, errors.New("credential request exceeds size limits")
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, params.Method, destination.String(), strings.NewReader(params.Body))
	if err != nil {
		return result, errors.New("credential request is invalid")
	}
	for name, value := range params.Headers {
		// Extensions own API-specific headers; the host owns authentication,
		// connection framing and routing. Never forward cookies or proxy auth.
		switch strings.ToLower(name) {
		case "authorization", "proxy-authorization", "cookie", "cookie2", "host", "connection", "proxy-connection", "upgrade", "transfer-encoding", "content-length", "te", "trailer", "forwarded", "x-forwarded-host":
			return result, errors.New("credential request contains a host-controlled header")
		}
		if !httpguts.ValidHeaderFieldName(name) || !httpguts.ValidHeaderFieldValue(value) || len(value) > 4096 {
			return result, errors.New("credential request contains an invalid header")
		}
		req.Header.Set(name, value)
	}
	namespace, key := binding.identity()
	secret, err := store.Get(ctx, namespace, key)
	defer clear(secret)
	if errors.Is(err, credentialstore.ErrNotFound) || err == nil && len(secret) == 0 {
		return result, errors.New("credential is not configured; enter it in this plugin's settings")
	}
	if err != nil {
		return result, errors.New("OS credential storage is unavailable; unlock Keychain or Secret Service and retry")
	}
	token := string(secret)
	req.Header.Set("Authorization", "Bearer "+token)
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		return result, errors.New("credential request failed or timed out; a write may already have been applied")
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		return result, errors.New("credential request redirect was blocked")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxBodyBytes+1))
	if err != nil || len(body) > maxBodyBytes || !utf8.Valid(body) {
		return result, errors.New("credential response is unreadable or exceeds 4 MiB; a write may already have been applied")
	}
	// Services can echo request headers in an error. Never return the token,
	// including JSON-escaped or URL-escaped representations, to the extension.
	encoded, _ := json.Marshal(token)
	redactor := strings.NewReplacer(token, "[redacted]", string(encoded[1:len(encoded)-1]), "[redacted]", url.QueryEscape(token), "[redacted]")
	result = RequestResult{Status: response.StatusCode, Headers: map[string]string{}, Body: redactor.Replace(string(body))}
	for _, name := range []string{"Content-Type", "Retry-After", "Request-Id", "X-Request-Id"} {
		if value := response.Header.Get(name); value != "" {
			result.Headers[strings.ToLower(name)] = redactor.Replace(value)
		}
	}
	return result, nil
}

// BindingFor preserves the local setting key while separating package identities.
func BindingFor(home, project, subject, key string, origins []string) (Binding, error) {
	if home == "" || subject == "" || key == "" || len(origins) == 0 {
		return Binding{}, errors.New("credential binding is incomplete")
	}
	if !strings.HasPrefix(subject, "plugin:project:") {
		project = ""
	}
	return Binding{Home: home, Project: project, Subject: subject, Key: key, Origins: origins}, nil
}
