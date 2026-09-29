package mcp

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/blueberrycongee/wuu/internal/credentialstore"
)

type OAuthManager struct {
	store     credentialstore.Store
	client    *http.Client
	mu        sync.Mutex
	pending   map[string]pendingOAuth
	refreshMu map[string]*sync.Mutex
}

type OAuthStartOptions struct {
	ServerID     string
	ResourceURL  string
	RedirectURI  string
	Scopes       []string
	ClientID     string
	ClientSecret string
	Headers      map[string]string
}

type OAuthStartResult struct {
	AuthorizationURL string   `json:"authorization_url"`
	State            string   `json:"state"`
	Scopes           []string `json:"scopes,omitempty"`
}

type OAuthFinishOptions struct {
	ServerID string
	State    string
	Code     string
}

type OAuthStatus struct {
	ServerID      string    `json:"server_id"`
	Authenticated bool      `json:"authenticated"`
	ExpiresAt     time.Time `json:"expires_at,omitempty"`
	Scopes        []string  `json:"scopes,omitempty"`
}

type protectedResourceMetadata struct {
	Resource             string   `json:"resource"`
	AuthorizationServers []string `json:"authorization_servers"`
	ScopesSupported      []string `json:"scopes_supported,omitempty"`
}

type authorizationServerMetadata struct {
	Issuer                        string   `json:"issuer"`
	AuthorizationEndpoint         string   `json:"authorization_endpoint"`
	TokenEndpoint                 string   `json:"token_endpoint"`
	RegistrationEndpoint          string   `json:"registration_endpoint,omitempty"`
	ScopesSupported               []string `json:"scopes_supported,omitempty"`
	CodeChallengeMethodsSupported []string `json:"code_challenge_methods_supported,omitempty"`
	TokenEndpointAuthMethods      []string `json:"token_endpoint_auth_methods_supported,omitempty"`
}

type pendingOAuth struct {
	State         string
	Verifier      string
	RedirectURI   string
	ResourceURL   string
	Scopes        []string
	ClientID      string
	ClientSecret  string
	TokenAuthMode string
	TokenEndpoint string
}

type storedOAuthToken struct {
	AccessToken   string    `json:"access_token"`
	RefreshToken  string    `json:"refresh_token,omitempty"`
	TokenType     string    `json:"token_type,omitempty"`
	ExpiresAt     time.Time `json:"expires_at,omitempty"`
	Scopes        []string  `json:"scopes,omitempty"`
	ClientID      string    `json:"client_id"`
	ClientSecret  string    `json:"client_secret,omitempty"`
	TokenAuthMode string    `json:"token_auth_mode,omitempty"`
	TokenEndpoint string    `json:"token_endpoint"`
	ResourceURL   string    `json:"resource_url,omitempty"`
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	TokenType    string `json:"token_type,omitempty"`
	ExpiresIn    int64  `json:"expires_in,omitempty"`
	Scope        string `json:"scope,omitempty"`
}

func NewOAuthManager(store credentialstore.Store, client *http.Client) *OAuthManager {
	if client == nil {
		client = http.DefaultClient
	}
	return &OAuthManager{store: store, client: client, pending: map[string]pendingOAuth{}, refreshMu: map[string]*sync.Mutex{}}
}

func (m *OAuthManager) Start(ctx context.Context, options OAuthStartOptions) (OAuthStartResult, error) {
	if m == nil || m.store == nil {
		return OAuthStartResult{}, errors.New("MCP OAuth credential store is not configured")
	}
	serverID := strings.TrimSpace(options.ServerID)
	resourceURL := strings.TrimSpace(options.ResourceURL)
	redirectURI := strings.TrimSpace(options.RedirectURI)
	if serverID == "" || resourceURL == "" || redirectURI == "" {
		return OAuthStartResult{}, errors.New("server_id, resource_url, and redirect_uri are required")
	}
	resourceMeta, challengedScopes, err := m.discoverProtectedResource(ctx, resourceURL, options.Headers)
	if err != nil {
		return OAuthStartResult{}, err
	}
	if len(resourceMeta.AuthorizationServers) == 0 {
		return OAuthStartResult{}, errors.New("protected resource metadata has no authorization_servers")
	}
	authMeta, err := m.discoverAuthorizationServer(ctx, resourceMeta.AuthorizationServers[0])
	if err != nil {
		return OAuthStartResult{}, err
	}
	clientID := strings.TrimSpace(options.ClientID)
	clientSecret := options.ClientSecret
	tokenAuthMode := selectTokenAuthMode(authMeta.TokenEndpointAuthMethods, clientSecret)
	if clientID == "" {
		clientID, clientSecret, tokenAuthMode, err = m.registerClient(ctx, authMeta.RegistrationEndpoint, redirectURI)
		if err != nil {
			return OAuthStartResult{}, err
		}
	}
	state, err := randomURLToken(32)
	if err != nil {
		return OAuthStartResult{}, err
	}
	verifier, err := randomURLToken(48)
	if err != nil {
		return OAuthStartResult{}, err
	}
	challengeSum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(challengeSum[:])
	scopes := append([]string(nil), options.Scopes...)
	if len(scopes) == 0 {
		scopes = append(scopes, challengedScopes...)
	}
	if len(scopes) == 0 {
		scopes = append(scopes, resourceMeta.ScopesSupported...)
	}
	if len(scopes) == 0 {
		scopes = append(scopes, authMeta.ScopesSupported...)
	}
	query := url.Values{
		"response_type":         {"code"},
		"client_id":             {clientID},
		"redirect_uri":          {redirectURI},
		"state":                 {state},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"resource":              {resourceURL},
	}
	if len(scopes) > 0 {
		query.Set("scope", strings.Join(scopes, " "))
	}
	authorizationURL, err := url.Parse(authMeta.AuthorizationEndpoint)
	if err != nil {
		return OAuthStartResult{}, fmt.Errorf("parse authorization endpoint: %w", err)
	}
	authorizationURL.RawQuery = query.Encode()
	pending := pendingOAuth{State: state, Verifier: verifier, RedirectURI: redirectURI, ResourceURL: resourceURL, Scopes: append([]string(nil), scopes...), ClientID: clientID, ClientSecret: clientSecret, TokenAuthMode: tokenAuthMode, TokenEndpoint: authMeta.TokenEndpoint}
	m.mu.Lock()
	m.pending[serverID+"\x00"+state] = pending
	m.mu.Unlock()
	return OAuthStartResult{AuthorizationURL: authorizationURL.String(), State: state, Scopes: append([]string(nil), scopes...)}, nil
}

func (m *OAuthManager) Finish(ctx context.Context, options OAuthFinishOptions) (OAuthStatus, error) {
	key := strings.TrimSpace(options.ServerID) + "\x00" + strings.TrimSpace(options.State)
	m.mu.Lock()
	pending, ok := m.pending[key]
	if ok {
		delete(m.pending, key)
	}
	m.mu.Unlock()
	if !ok || pending.State != options.State {
		return OAuthStatus{}, errors.New("invalid or expired OAuth state")
	}
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {options.Code},
		"client_id":     {pending.ClientID},
		"redirect_uri":  {pending.RedirectURI},
		"code_verifier": {pending.Verifier},
		"resource":      {pending.ResourceURL},
	}
	response, err := m.exchangeToken(ctx, pending.TokenEndpoint, pending.ClientSecret, pending.TokenAuthMode, form)
	if err != nil {
		return OAuthStatus{}, err
	}
	token := storedOAuthToken{AccessToken: response.AccessToken, RefreshToken: response.RefreshToken, TokenType: response.TokenType, ExpiresAt: tokenExpiry(response.ExpiresIn), Scopes: scopesFromResponse(response.Scope, pending.Scopes), ClientID: pending.ClientID, ClientSecret: pending.ClientSecret, TokenAuthMode: pending.TokenAuthMode, TokenEndpoint: pending.TokenEndpoint, ResourceURL: pending.ResourceURL}
	if err := m.saveToken(ctx, options.ServerID, token); err != nil {
		return OAuthStatus{}, err
	}
	return oauthStatus(options.ServerID, token), nil
}

func (m *OAuthManager) Status(ctx context.Context, serverID string) (OAuthStatus, error) {
	token, err := m.loadToken(ctx, serverID)
	if errors.Is(err, credentialstore.ErrNotFound) {
		return OAuthStatus{ServerID: serverID}, nil
	}
	if err != nil {
		return OAuthStatus{}, err
	}
	return oauthStatus(serverID, token), nil
}

func (m *OAuthManager) AccessToken(ctx context.Context, serverID string) (string, error) {
	return m.accessToken(ctx, serverID, false)
}

// RefreshAccessToken forces a fresh read and refresh when the server rejected
// a token that still looked valid locally.
func (m *OAuthManager) RefreshAccessToken(ctx context.Context, serverID string) (string, error) {
	return m.accessToken(ctx, serverID, true)
}

func (m *OAuthManager) accessToken(ctx context.Context, serverID string, forceRefresh bool) (string, error) {
	refreshLock := m.refreshLock(serverID)
	refreshLock.Lock()
	defer refreshLock.Unlock()
	token, err := m.loadToken(ctx, serverID)
	if err != nil {
		return "", err
	}
	if !forceRefresh && (token.ExpiresAt.IsZero() || time.Now().Before(token.ExpiresAt)) {
		return token.AccessToken, nil
	}
	if strings.TrimSpace(token.RefreshToken) == "" {
		return "", errors.New("MCP OAuth token expired and has no refresh token")
	}
	form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {token.RefreshToken}, "client_id": {token.ClientID}}
	if token.ResourceURL != "" {
		form.Set("resource", token.ResourceURL)
	}
	response, err := m.exchangeToken(ctx, token.TokenEndpoint, token.ClientSecret, token.TokenAuthMode, form)
	if err != nil {
		// Another process may have rotated the refresh token while this
		// request was in flight. Prefer the credential now in the store over
		// reporting the stale refresh failure.
		if latest, loadErr := m.loadToken(ctx, serverID); loadErr == nil &&
			latest.AccessToken != token.AccessToken &&
			(latest.ExpiresAt.IsZero() || time.Now().Before(latest.ExpiresAt)) {
			return latest.AccessToken, nil
		}
		return "", err
	}
	token.AccessToken = response.AccessToken
	if response.RefreshToken != "" {
		token.RefreshToken = response.RefreshToken
	}
	token.TokenType = response.TokenType
	token.ExpiresAt = tokenExpiry(response.ExpiresIn)
	token.Scopes = scopesFromResponse(response.Scope, token.Scopes)
	if err := m.saveToken(ctx, serverID, token); err != nil {
		return "", err
	}
	return token.AccessToken, nil
}

func (m *OAuthManager) refreshLock(serverID string) *sync.Mutex {
	key := strings.TrimSpace(serverID)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.refreshMu == nil {
		m.refreshMu = make(map[string]*sync.Mutex)
	}
	if lock := m.refreshMu[key]; lock != nil {
		return lock
	}
	lock := &sync.Mutex{}
	m.refreshMu[key] = lock
	return lock
}

func (m *OAuthManager) Remove(ctx context.Context, serverID string) error {
	return m.store.Delete(ctx, oauthNamespace(serverID), "oauth")
}

func (m *OAuthManager) discoverProtectedResource(ctx context.Context, resource string, headers map[string]string) (protectedResourceMetadata, []string, error) {
	parsed, err := url.Parse(resource)
	if err != nil {
		return protectedResourceMetadata{}, nil, err
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return protectedResourceMetadata{}, nil, errors.New("MCP OAuth resource URL must be absolute")
	}

	metadataURL, challengedScopes := m.protectedResourceChallenge(ctx, resource, headers)
	candidates := make([]string, 0, 3)
	if metadataURL != "" {
		candidates = append(candidates, metadataURL)
	}
	origin := parsed.Scheme + "://" + parsed.Host
	if path := strings.TrimRight(parsed.EscapedPath(), "/"); path != "" {
		candidates = append(candidates, origin+"/.well-known/oauth-protected-resource"+path)
	}
	candidates = append(candidates, origin+"/.well-known/oauth-protected-resource")

	var discoveryErrors []string
	for _, endpoint := range dedupeURLs(candidates) {
		var metadata protectedResourceMetadata
		requestHeaders := headers
		if !sameOrigin(resource, endpoint) {
			requestHeaders = nil
		}
		if err := m.getJSONWithHeaders(ctx, endpoint, requestHeaders, &metadata); err != nil {
			discoveryErrors = append(discoveryErrors, err.Error())
			continue
		}
		if metadata.Resource != "" && strings.TrimRight(metadata.Resource, "/") != strings.TrimRight(resource, "/") {
			discoveryErrors = append(discoveryErrors, fmt.Sprintf("%s returned mismatched resource %q", endpoint, metadata.Resource))
			continue
		}
		return metadata, challengedScopes, nil
	}
	return protectedResourceMetadata{}, challengedScopes, fmt.Errorf("discover OAuth protected resource: %s", strings.Join(discoveryErrors, "; "))
}

func (m *OAuthManager) discoverAuthorizationServer(ctx context.Context, issuer string) (authorizationServerMetadata, error) {
	parsed, err := url.Parse(issuer)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return authorizationServerMetadata{}, fmt.Errorf("invalid OAuth authorization server issuer %q", issuer)
	}
	origin := parsed.Scheme + "://" + parsed.Host
	path := strings.TrimRight(parsed.EscapedPath(), "/")
	var candidates []string
	if path != "" {
		candidates = append(candidates,
			origin+"/.well-known/oauth-authorization-server"+path,
			origin+"/.well-known/openid-configuration"+path,
			origin+path+"/.well-known/openid-configuration",
		)
	} else {
		candidates = append(candidates,
			origin+"/.well-known/oauth-authorization-server",
			origin+"/.well-known/openid-configuration",
		)
	}
	var discoveryErrors []string
	for _, endpoint := range candidates {
		var metadata authorizationServerMetadata
		if err := m.getJSON(ctx, endpoint, &metadata); err != nil {
			discoveryErrors = append(discoveryErrors, err.Error())
			continue
		}
		if metadata.Issuer != "" && !sameIssuer(metadata.Issuer, issuer) {
			discoveryErrors = append(discoveryErrors, fmt.Sprintf("%s returned mismatched issuer %q", endpoint, metadata.Issuer))
			continue
		}
		if metadata.AuthorizationEndpoint == "" || metadata.TokenEndpoint == "" {
			discoveryErrors = append(discoveryErrors, fmt.Sprintf("%s is missing authorization or token endpoints", endpoint))
			continue
		}
		if len(metadata.CodeChallengeMethodsSupported) > 0 && !containsString(metadata.CodeChallengeMethodsSupported, "S256") {
			return authorizationServerMetadata{}, errors.New("authorization server does not support PKCE S256")
		}
		return metadata, nil
	}
	return authorizationServerMetadata{}, fmt.Errorf("discover OAuth authorization server: %s", strings.Join(discoveryErrors, "; "))
}

func (m *OAuthManager) protectedResourceChallenge(ctx context.Context, resource string, headers map[string]string) (string, []string) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, resource, nil)
	if err != nil {
		return "", nil
	}
	applyHTTPHeaders(req, headers)
	resp, err := m.client.Do(req)
	if err != nil {
		return "", nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		return "", nil
	}
	for _, challenge := range resp.Header.Values("WWW-Authenticate") {
		if !strings.EqualFold(firstAuthToken(challenge), "Bearer") {
			continue
		}
		metadataURL := authParameter(challenge, "resource_metadata")
		scopes := strings.Fields(authParameter(challenge, "scope"))
		if metadataURL != "" || len(scopes) > 0 {
			return metadataURL, scopes
		}
	}
	return "", nil
}

func (m *OAuthManager) registerClient(ctx context.Context, endpoint, redirectURI string) (string, string, string, error) {
	if strings.TrimSpace(endpoint) == "" {
		return "", "", "", errors.New("OAuth client_id is required because the server does not support dynamic registration")
	}
	body, _ := json.Marshal(map[string]any{"client_name": "Wuu", "application_type": "native", "redirect_uris": []string{redirectURI}, "token_endpoint_auth_method": "none", "grant_types": []string{"authorization_code", "refresh_token"}, "response_types": []string{"code"}})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", "", "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := m.client.Do(req)
	if err != nil {
		return "", "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", "", "", fmt.Errorf("OAuth dynamic registration returned %s", resp.Status)
	}
	var registered struct {
		ClientID                string `json:"client_id"`
		ClientSecret            string `json:"client_secret,omitempty"`
		TokenEndpointAuthMethod string `json:"token_endpoint_auth_method,omitempty"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&registered); err != nil {
		return "", "", "", err
	}
	if registered.ClientID == "" {
		return "", "", "", errors.New("OAuth dynamic registration returned no client_id")
	}
	mode := registered.TokenEndpointAuthMethod
	if mode == "" {
		mode = selectTokenAuthMode(nil, registered.ClientSecret)
	}
	return registered.ClientID, registered.ClientSecret, mode, nil
}

func (m *OAuthManager) exchangeToken(ctx context.Context, endpoint, clientSecret, authMode string, form url.Values) (tokenResponse, error) {
	if authMode == "client_secret_post" && clientSecret != "" {
		form.Set("client_secret", clientSecret)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return tokenResponse{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	switch authMode {
	case "client_secret_post", "none":
	default:
		if clientSecret != "" {
			req.SetBasicAuth(form.Get("client_id"), clientSecret)
		}
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return tokenResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return tokenResponse{}, fmt.Errorf("OAuth token endpoint returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var token tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&token); err != nil {
		return token, err
	}
	if token.AccessToken == "" {
		return token, errors.New("OAuth token response has no access_token")
	}
	return token, nil
}

func (m *OAuthManager) getJSON(ctx context.Context, endpoint string, out any) error {
	return m.getJSONWithHeaders(ctx, endpoint, nil, out)
}

func (m *OAuthManager) getJSONWithHeaders(ctx context.Context, endpoint string, headers map[string]string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	applyHTTPHeaders(req, headers)
	resp, err := m.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s returned %s", endpoint, resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func applyHTTPHeaders(req *http.Request, headers map[string]string) {
	for name, value := range headers {
		if strings.TrimSpace(name) != "" && strings.TrimSpace(value) != "" {
			req.Header.Set(name, value)
		}
	}
}

func firstAuthToken(challenge string) string {
	challenge = strings.TrimSpace(challenge)
	if index := strings.IndexAny(challenge, " \t"); index >= 0 {
		return challenge[:index]
	}
	return challenge
}

func authParameter(challenge, target string) string {
	challenge = strings.TrimSpace(challenge)
	if index := strings.IndexAny(challenge, " \t"); index >= 0 {
		challenge = challenge[index+1:]
	} else {
		return ""
	}
	for len(challenge) > 0 {
		challenge = strings.TrimLeft(challenge, " \t,")
		nameEnd := strings.IndexByte(challenge, '=')
		if nameEnd < 0 {
			return ""
		}
		name := strings.TrimSpace(challenge[:nameEnd])
		challenge = strings.TrimSpace(challenge[nameEnd+1:])
		var value string
		if strings.HasPrefix(challenge, "\"") {
			challenge = challenge[1:]
			var decoded strings.Builder
			escaped := false
			end := -1
			for index, character := range challenge {
				if escaped {
					decoded.WriteRune(character)
					escaped = false
					continue
				}
				if character == '\\' {
					escaped = true
					continue
				}
				if character == '"' {
					end = index
					break
				}
				decoded.WriteRune(character)
			}
			if end < 0 {
				return ""
			}
			value = decoded.String()
			challenge = challenge[end+1:]
		} else {
			end := strings.IndexByte(challenge, ',')
			if end < 0 {
				value = strings.TrimSpace(challenge)
				challenge = ""
			} else {
				value = strings.TrimSpace(challenge[:end])
				challenge = challenge[end+1:]
			}
		}
		if strings.EqualFold(name, target) {
			return value
		}
	}
	return ""
}

func dedupeURLs(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func sameIssuer(left, right string) bool {
	return strings.TrimRight(strings.TrimSpace(left), "/") == strings.TrimRight(strings.TrimSpace(right), "/")
}

func sameOrigin(left, right string) bool {
	leftURL, leftErr := url.Parse(left)
	rightURL, rightErr := url.Parse(right)
	if leftErr != nil || rightErr != nil {
		return false
	}
	return strings.EqualFold(leftURL.Scheme, rightURL.Scheme) && strings.EqualFold(leftURL.Host, rightURL.Host)
}

func selectTokenAuthMode(supported []string, clientSecret string) string {
	if clientSecret == "" {
		return "none"
	}
	if len(supported) == 0 || containsString(supported, "client_secret_basic") {
		return "client_secret_basic"
	}
	if containsString(supported, "client_secret_post") {
		return "client_secret_post"
	}
	return "client_secret_basic"
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func (m *OAuthManager) saveToken(ctx context.Context, serverID string, token storedOAuthToken) error {
	data, err := json.Marshal(token)
	if err != nil {
		return err
	}
	return m.store.Set(ctx, oauthNamespace(serverID), "oauth", data)
}

func (m *OAuthManager) loadToken(ctx context.Context, serverID string) (storedOAuthToken, error) {
	data, err := m.store.Get(ctx, oauthNamespace(serverID), "oauth")
	if err != nil {
		return storedOAuthToken{}, err
	}
	var token storedOAuthToken
	if err := json.Unmarshal(data, &token); err != nil {
		return token, err
	}
	return token, nil
}

func oauthNamespace(serverID string) string { return "mcp:" + strings.TrimSpace(serverID) }

func oauthStatus(serverID string, token storedOAuthToken) OAuthStatus {
	return OAuthStatus{ServerID: serverID, Authenticated: token.AccessToken != "", ExpiresAt: token.ExpiresAt, Scopes: append([]string(nil), token.Scopes...)}
}

func tokenExpiry(seconds int64) time.Time {
	if seconds <= 0 {
		return time.Time{}
	}
	return time.Now().Add(time.Duration(seconds) * time.Second)
}

func scopesFromResponse(scope string, fallback []string) []string {
	if strings.TrimSpace(scope) == "" {
		return append([]string(nil), fallback...)
	}
	return strings.Fields(scope)
}

func randomURLToken(size int) (string, error) {
	data := make([]byte, size)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}
