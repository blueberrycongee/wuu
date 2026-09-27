package xaisub

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/blueberrycongee/wuu/internal/authstorage"
	"github.com/blueberrycongee/wuu/internal/version"
)

const (
	// AuthProviderID is the durable auth-store key for SuperGrok OAuth.
	// All xai-subscription providers share this session, matching how
	// openai-codex stores ChatGPT credentials under a type key.
	AuthProviderID = "xai-subscription"

	DefaultBaseURL = "https://api.x.ai/v1"
	DefaultModel   = "grok-4.7"

	defaultClientID = "b1a00492-073a-47ea-816f-4c329264a828"
	defaultScope    = "openid profile email offline_access grok-cli:access api:access"
	defaultReferrer = "wuu"

	defaultDeviceCodeURL = "https://auth.x.ai/oauth2/device/code"
	defaultTokenURL      = "https://auth.x.ai/oauth2/token"
	deviceCodeGrantType  = "urn:ietf:params:oauth:grant-type:device_code"

	refreshSkew               = 120 * time.Second
	defaultTokenLifetime      = 3600 * time.Second
	defaultPollInterval       = 5 * time.Second
	minPollInterval           = time.Second
	slowDownIncrement         = 5 * time.Second
	oauthPollingSafetyMargin  = 3 * time.Second
	defaultDeviceCodeLifetime = 5 * time.Minute
)

// OAuthConfig configures the SuperGrok credential source.
type OAuthConfig struct {
	BaseURL    string
	Home       string
	HTTPClient *http.Client
}

// OAuthSource resolves SuperGrok bearer tokens from Wuu's auth store.
type OAuthSource struct {
	baseURL    string
	home       string
	httpClient *http.Client
	mu         sync.Mutex
	cached     credentials
	hasCached  bool
}

type credentials struct {
	accessToken  string
	refreshToken string
	expiresAt    time.Time
	source       string
	refreshable  bool
}

// DeviceCode is the RFC 8628 authorization session shown to the user.
type DeviceCode struct {
	DeviceCode              string
	UserCode                string
	VerificationURI         string
	VerificationURIComplete string
	Interval                time.Duration
	ExpiresIn               time.Duration
}

// TokenResponse is the OAuth token endpoint payload Wuu persists.
type TokenResponse struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    time.Duration
}

// NewOAuthSource creates a SuperGrok OAuth credential source.
func NewOAuthSource(cfg OAuthConfig) *OAuthSource {
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	home := strings.TrimSpace(cfg.Home)
	if home == "" {
		home = os.Getenv("HOME")
	}
	return &OAuthSource{
		baseURL:    baseURL,
		home:       home,
		httpClient: cfg.HTTPClient,
	}
}

func (s *OAuthSource) Credentials(ctx context.Context, forceRefresh bool) (credentials, error) {
	if strings.TrimSpace(s.home) == "" {
		return credentials{}, errors.New("home directory is required for xAI SuperGrok OAuth")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return credentials{}, err
	}
	if !forceRefresh && s.hasCached && strings.TrimSpace(s.cached.accessToken) != "" &&
		!credentialExpiring(s.cached, refreshSkew) {
		return s.cached, nil
	}

	store, err := authstorage.ForHome(s.home)
	if err != nil {
		return credentials{}, err
	}
	state, err := store.Get(AuthProviderID)
	if err != nil {
		return credentials{}, errors.New("no wuu xAI SuperGrok OAuth credentials found; run `wuu login xai` or sign in from Settings")
	}
	creds := credentialsFromStore(state)
	if strings.TrimSpace(creds.accessToken) == "" && strings.TrimSpace(creds.refreshToken) == "" {
		return credentials{}, errors.New("no wuu xAI SuperGrok OAuth credentials found; run `wuu login xai` or sign in from Settings")
	}
	if forceRefresh || credentialExpiring(creds, refreshSkew) {
		if strings.TrimSpace(creds.refreshToken) == "" {
			return credentials{}, errors.New("xAI SuperGrok OAuth credentials are expired; sign in again")
		}
		refreshed, refreshErr := refreshAccessToken(ctx, s.httpClient, creds.refreshToken)
		if refreshErr != nil {
			return credentials{}, refreshErr
		}
		state = persistTokenState(state, refreshed, s.baseURL)
		if saveErr := store.Set(AuthProviderID, state); saveErr != nil {
			return credentials{}, saveErr
		}
		creds = credentialsFromStore(state)
	}
	s.cached = creds
	s.hasCached = true
	return creds, nil
}

// LocalOAuthStatus reports whether Wuu already holds a SuperGrok session.
func LocalOAuthStatus(home string) (string, error) {
	home = strings.TrimSpace(home)
	if home == "" {
		home = os.Getenv("HOME")
	}
	if home == "" {
		return "", errors.New("home directory is required for xAI SuperGrok OAuth")
	}
	store, err := authstorage.ForHome(home)
	if err != nil {
		return "", err
	}
	state, err := store.Get(AuthProviderID)
	if err != nil {
		return "", fmt.Errorf("xAI SuperGrok OAuth credentials not found: %w", err)
	}
	creds := credentialsFromStore(state)
	if strings.TrimSpace(creds.accessToken) == "" && strings.TrimSpace(creds.refreshToken) == "" {
		return "", errors.New("xAI SuperGrok OAuth credentials not found")
	}
	if credentialExpiring(creds, 0) && strings.TrimSpace(creds.refreshToken) == "" {
		return "", errors.New("xAI SuperGrok OAuth credentials are expired; sign in again")
	}
	return "wuu-auth-store", nil
}

// PersistTokens writes a SuperGrok session into Wuu's auth store.
func PersistTokens(home string, tokens TokenResponse, baseURL string) error {
	home = strings.TrimSpace(home)
	if home == "" {
		home = os.Getenv("HOME")
	}
	if home == "" {
		return errors.New("home directory is required for xAI SuperGrok OAuth")
	}
	store, err := authstorage.ForHome(home)
	if err != nil {
		return err
	}
	state, _ := store.Get(AuthProviderID)
	state = persistTokenState(state, tokens, baseURL)
	return store.Set(AuthProviderID, state)
}

// DeleteTokens removes the SuperGrok session from Wuu's auth store.
func DeleteTokens(home string) error {
	home = strings.TrimSpace(home)
	if home == "" {
		home = os.Getenv("HOME")
	}
	if home == "" {
		return errors.New("home directory is required for xAI SuperGrok OAuth")
	}
	store, err := authstorage.ForHome(home)
	if err != nil {
		return err
	}
	return store.DeleteProvider(AuthProviderID)
}

// RequestDeviceCode starts an RFC 8628 SuperGrok login.
func RequestDeviceCode(ctx context.Context, httpClient *http.Client) (DeviceCode, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	resp, err := postForm(ctx, httpClient, deviceCodeURL(), url.Values{
		"client_id": {clientID()},
		"scope":     {defaultScope},
		"referrer":  {defaultReferrer},
	})
	if err != nil {
		return DeviceCode{}, err
	}
	if !resp.ok {
		return DeviceCode{}, requestFailure("device authorization", resp)
	}
	return parseDeviceCode(resp.body)
}

// ExchangeDeviceCode performs one token poll for an outstanding device code.
func ExchangeDeviceCode(ctx context.Context, httpClient *http.Client, device DeviceCode) (TokenResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	resp, err := postForm(ctx, httpClient, tokenURL(), url.Values{
		"grant_type":  {deviceCodeGrantType},
		"client_id":   {clientID()},
		"device_code": {device.DeviceCode},
	})
	if err != nil {
		return TokenResponse{}, err
	}
	if resp.ok {
		return parseTokenResponse(resp.body, "")
	}
	errorCode, _ := resp.body["error"].(string)
	switch errorCode {
	case "authorization_pending":
		return TokenResponse{}, errAuthorizationPending
	case "slow_down":
		return TokenResponse{}, errSlowDown
	case "access_denied", "authorization_denied":
		return TokenResponse{}, errors.New("xAI SuperGrok authorization was denied")
	case "expired_token":
		return TokenResponse{}, errors.New("xAI SuperGrok device code expired; sign in again")
	default:
		return TokenResponse{}, requestFailure("device token polling", resp)
	}
}

func refreshAccessToken(ctx context.Context, httpClient *http.Client, refreshToken string) (TokenResponse, error) {
	refreshToken = strings.TrimSpace(refreshToken)
	if refreshToken == "" {
		return TokenResponse{}, errors.New("xAI SuperGrok OAuth refresh token is missing")
	}
	resp, err := postForm(ctx, httpClient, tokenURL(), url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {clientID()},
		"refresh_token": {refreshToken},
	})
	if err != nil {
		return TokenResponse{}, err
	}
	if !resp.ok {
		return TokenResponse{}, requestFailure("token refresh", resp)
	}
	return parseTokenResponse(resp.body, refreshToken)
}

func credentialsFromStore(state authstorage.Credentials) credentials {
	return credentials{
		accessToken:  strings.TrimSpace(state.AccessToken),
		refreshToken: strings.TrimSpace(state.RefreshToken),
		expiresAt:    state.ExpiresAt,
		source:       firstNonEmpty(state.Source, "wuu-auth-store"),
		refreshable:  strings.TrimSpace(state.RefreshToken) != "",
	}
}

func persistTokenState(previous authstorage.Credentials, tokens TokenResponse, baseURL string) authstorage.Credentials {
	expiresIn := tokens.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = defaultTokenLifetime
	}
	refresh := strings.TrimSpace(tokens.RefreshToken)
	if refresh == "" {
		refresh = strings.TrimSpace(previous.RefreshToken)
	}
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return authstorage.Credentials{
		Type:         "oauth",
		AccessToken:  strings.TrimSpace(tokens.AccessToken),
		RefreshToken: refresh,
		AuthMode:     "xai",
		Source:       firstNonEmpty(previous.Source, "wuu"),
		BaseURL:      baseURL,
		ExpiresAt:    time.Now().UTC().Add(expiresIn),
		LastRefresh:  time.Now().UTC().Format(time.RFC3339),
	}
}

func credentialExpiring(creds credentials, skew time.Duration) bool {
	if strings.TrimSpace(creds.accessToken) == "" {
		return true
	}
	if !creds.expiresAt.IsZero() && !creds.expiresAt.After(time.Now().Add(skew)) {
		return true
	}
	return accessTokenIsExpiring(creds.accessToken, skew)
}

func accessTokenIsExpiring(token string, skew time.Duration) bool {
	claims := jwtClaims(token)
	exp, ok := claims["exp"].(float64)
	if !ok || exp <= 0 {
		return false
	}
	return time.Unix(int64(exp), 0).Before(time.Now().Add(skew))
}

func jwtClaims(token string) map[string]any {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		payload, err = base64.URLEncoding.DecodeString(parts[1])
		if err != nil {
			return nil
		}
	}
	var claims map[string]any
	dec := json.NewDecoder(bytes.NewReader(payload))
	if err := dec.Decode(&claims); err != nil {
		return nil
	}
	return claims
}

type oauthHTTPResponse struct {
	ok     bool
	status int
	body   map[string]any
}

func postForm(ctx context.Context, httpClient *http.Client, endpoint string, fields url.Values) (oauthHTTPResponse, error) {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(fields.Encode()))
	if err != nil {
		return oauthHTTPResponse{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent())
	resp, err := httpClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return oauthHTTPResponse{}, errors.New("login cancelled")
		}
		return oauthHTTPResponse{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	parsed := map[string]any{}
	if len(bytes.TrimSpace(body)) > 0 {
		if err := json.Unmarshal(body, &parsed); err != nil {
			parsed = map[string]any{}
		}
	}
	return oauthHTTPResponse{ok: resp.StatusCode >= 200 && resp.StatusCode < 300, status: resp.StatusCode, body: parsed}, nil
}

func parseDeviceCode(body map[string]any) (DeviceCode, error) {
	deviceCode := requiredString(body, "device_code")
	userCode := requiredString(body, "user_code")
	if deviceCode == "" || userCode == "" {
		return DeviceCode{}, errors.New("xAI SuperGrok device code response is missing device_code / user_code")
	}
	rawURI := requiredString(body, "verification_uri")
	if rawURI == "" {
		return DeviceCode{}, errors.New("xAI SuperGrok device code response is missing verification_uri")
	}
	verificationURI, err := validateHTTPSURI(rawURI)
	if err != nil {
		return DeviceCode{}, err
	}
	complete := ""
	if raw, _ := body["verification_uri_complete"].(string); strings.TrimSpace(raw) != "" {
		complete, err = validateHTTPSURI(raw)
		if err != nil {
			return DeviceCode{}, err
		}
	}
	return DeviceCode{
		DeviceCode:              deviceCode,
		UserCode:                userCode,
		VerificationURI:         verificationURI,
		VerificationURIComplete: complete,
		Interval:                positiveDurationSeconds(body["interval"], defaultPollInterval),
		ExpiresIn:               positiveDurationSeconds(body["expires_in"], defaultDeviceCodeLifetime),
	}, nil
}

func parseTokenResponse(body map[string]any, previousRefresh string) (TokenResponse, error) {
	access := requiredString(body, "access_token")
	if access == "" {
		return TokenResponse{}, errors.New("xAI SuperGrok OAuth response missing access_token")
	}
	refresh, _ := body["refresh_token"].(string)
	refresh = strings.TrimSpace(refresh)
	if refresh == "" {
		refresh = strings.TrimSpace(previousRefresh)
	}
	if refresh == "" {
		return TokenResponse{}, errors.New("xAI SuperGrok OAuth response missing refresh_token")
	}
	return TokenResponse{
		AccessToken:  access,
		RefreshToken: refresh,
		ExpiresIn:    positiveDurationSeconds(body["expires_in"], defaultTokenLifetime),
	}, nil
}

func requiredString(body map[string]any, field string) string {
	value, _ := body[field].(string)
	return strings.TrimSpace(value)
}

func positiveDurationSeconds(value any, fallback time.Duration) time.Duration {
	switch v := value.(type) {
	case float64:
		if v > 0 {
			return time.Duration(v * float64(time.Second))
		}
	case json.Number:
		if n, err := v.Float64(); err == nil && n > 0 {
			return time.Duration(n * float64(time.Second))
		}
	}
	if fallback > 0 {
		return fallback
	}
	return defaultPollInterval
}

func validateHTTPSURI(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return "", errors.New("untrusted verification URI in xAI OAuth response")
	}
	return parsed.String(), nil
}

func requestFailure(action string, resp oauthHTTPResponse) error {
	errorCode, _ := resp.body["error"].(string)
	description, _ := resp.body["error_description"].(string)
	detail := strings.TrimSpace(strings.Join(filterEmpty(errorCode, description), ": "))
	if detail != "" {
		return fmt.Errorf("xAI SuperGrok OAuth %s failed (HTTP %d): %s", action, resp.status, detail)
	}
	return fmt.Errorf("xAI SuperGrok OAuth %s failed (HTTP %d)", action, resp.status)
}

func filterEmpty(values ...string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			out = append(out, strings.TrimSpace(value))
		}
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func clientID() string {
	if v := strings.TrimSpace(os.Getenv("WUU_XAI_OAUTH_CLIENT_ID")); v != "" {
		return v
	}
	return defaultClientID
}

func deviceCodeURL() string {
	if v := strings.TrimSpace(os.Getenv("WUU_XAI_DEVICE_CODE_URL")); v != "" {
		return v
	}
	return defaultDeviceCodeURL
}

func tokenURL() string {
	if v := strings.TrimSpace(os.Getenv("WUU_XAI_TOKEN_URL")); v != "" {
		return v
	}
	return defaultTokenURL
}

func userAgent() string {
	return "wuu/" + strings.TrimSpace(version.String())
}

var (
	errAuthorizationPending = errors.New("xAI SuperGrok authorization is pending")
	errSlowDown             = errors.New("xAI SuperGrok authorization asked to slow down")
)
