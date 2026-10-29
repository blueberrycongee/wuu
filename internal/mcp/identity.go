package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/credentialstore"
)

const maxIdentityResultBytes = 64 * 1024

// IdentityStatus describes only identity returned by the admitted package probe.
// Kind is provider-reported; neither personhood nor email verification is inferred.
type IdentityStatus struct {
	State      string             `json:"state"`
	VerifiedAt string             `json:"verified_at,omitempty"`
	Account    *IdentityAccount   `json:"account,omitempty"`
	Workspace  *IdentityWorkspace `json:"workspace,omitempty"`
	ErrorCode  string             `json:"error_code,omitempty"`
}
type IdentityAccount struct {
	ID    string `json:"id"`
	Label string `json:"label,omitempty"`
	Email string `json:"email,omitempty"`
	Kind  string `json:"kind,omitempty"`
}
type IdentityWorkspace struct {
	ID    string `json:"id"`
	Label string `json:"label,omitempty"`
}

type storedIdentity struct {
	GrantID    string         `json:"grant_id"`
	Resource   string         `json:"resource"`
	Descriptor string         `json:"descriptor"`
	Status     IdentityStatus `json:"status"`
}

func unavailableIdentity(code string) *IdentityStatus {
	return &IdentityStatus{State: "unavailable", ErrorCode: code}
}

func normalizeIdentity(probe *config.MCPIdentityProbe, result *CallToolResult) *IdentityStatus {
	if result == nil || result.IsError {
		return unavailableIdentity("identity_probe_failed")
	}
	var data []byte
	switch probe.Result {
	case "structured_content":
		data = result.StructuredContent
	case "single_text_json":
		if len(result.Content) != 1 || result.Content[0].Type != "text" {
			return unavailableIdentity("identity_invalid_response")
		}
		data = []byte(result.Content[0].Text)
	default:
		return unavailableIdentity("identity_invalid_declaration")
	}
	if len(data) > maxIdentityResultBytes {
		return unavailableIdentity("identity_response_too_large")
	}
	var object map[string]any
	if err := json.Unmarshal(data, &object); err != nil || object == nil {
		return unavailableIdentity("identity_invalid_response")
	}
	valid := true
	read := func(pointer string, required bool, limit int) string {
		if pointer == "" {
			if required {
				valid = false
			}
			return ""
		}
		var value any = object
		exists := true
		for _, part := range strings.Split(pointer[1:], "/") {
			key := strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
			switch node := value.(type) {
			case map[string]any:
				value, exists = node[key]
			case []any:
				index, err := strconv.Atoi(key)
				if err != nil || index < 0 || index >= len(node) || strconv.Itoa(index) != key {
					value = nil
					exists = false
				} else {
					value = node[index]
				}
			default:
				value = nil
				exists = false
			}
		}
		if !exists && !required {
			return ""
		}
		text, ok := value.(string)
		if !ok || len(text) > limit || strings.ContainsFunc(text, unicode.IsControl) || (required && strings.TrimSpace(text) == "") {
			valid = false
			return ""
		}
		return text
	}
	account := &IdentityAccount{ID: read(probe.Account.ID, true, 512), Label: read(probe.Account.Label, false, 512), Email: read(probe.Account.Email, false, 320), Kind: read(probe.Account.Kind, false, 512)}
	var workspace *IdentityWorkspace
	if probe.Workspace != nil {
		workspace = &IdentityWorkspace{ID: read(probe.Workspace.ID, true, 512), Label: read(probe.Workspace.Label, false, 512)}
	}
	if !valid {
		return unavailableIdentity("identity_invalid_response")
	}
	return &IdentityStatus{State: "verified", VerifiedAt: time.Now().UTC().Format(time.RFC3339), Account: account, Workspace: workspace}
}

func identityDescriptor(cfg ServerConfig) string {
	if cfg.IdentitySource == "" || cfg.IdentityProbe == nil || cfg.IdentityProbe.Validate() != nil {
		return ""
	}
	override, ok := cfg.ToolOverrides[cfg.IdentityProbe.Tool]
	if !ok || override.ReadOnly == nil || !*override.ReadOnly {
		return ""
	}
	data, err := json.Marshal(struct {
		Source string
		Probe  *config.MCPIdentityProbe
	}{cfg.IdentitySource, cfg.IdentityProbe})
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// RefreshIdentity runs only for an explicit owner action. It does not authorize,
// reconnect, or request new scopes. Failures do not change transport/auth state.
func (m *Manager) RefreshIdentity(parent context.Context, name string) (OAuthStatus, error) {
	name = strings.TrimSpace(name)
	status, err := m.OAuthStatus(parent, name)
	if err != nil {
		return status, err
	}
	if !status.Authenticated {
		return status, nil
	}
	m.mu.Lock()
	cfg := cloneServerConfig(m.configs[name])
	descriptor := identityDescriptor(cfg)
	server, client := m.servers[name], m.clients[name]
	if m.closed || server == nil || server.revoked || !cfg.IsEnabled() || descriptor == "" || client == nil || !m.statuses[name].Connected {
		m.mu.Unlock()
		return status, nil
	}
	found := false
	for _, tool := range client.Tools() {
		if tool.Name == cfg.IdentityProbe.Tool {
			found = true
			break
		}
	}
	if server.identityCancel != nil {
		server.identityCancel()
	}
	ctx, cancel := context.WithTimeout(parent, defaultConnectionTimeout)
	stop := context.AfterFunc(server.ctx, cancel)
	server.identityCancel = cancel
	server.identityAttempt++
	owner := &oauthOwner{manager: m, server: server, name: name, identityClient: client, identityGeneration: server.generation, identityDescriptor: descriptor, identityAttempt: server.identityAttempt}
	oauth := m.oauth
	m.operations.Add(1)
	m.mu.Unlock()
	defer func() {
		stop()
		cancel()
		m.mu.Lock()
		if server.identityAttempt == owner.identityAttempt {
			server.identityCancel = nil
		}
		m.mu.Unlock()
		m.operations.Done()
	}()
	var grant, loginGeneration string
	err = owner.authorize(ctx, func() error {
		return oauth.store.Update(ctx, oauthNamespace(name), "oauth", func(data []byte) ([]byte, error) {
			if len(data) == 0 {
				return nil, ErrOAuthSuperseded
			}
			var token storedOAuthToken
			if err := json.Unmarshal(data, &token); err != nil {
				return nil, err
			}
			if token.AccessToken == "" {
				return nil, credentialstore.ErrNotFound
			}
			if !sameOAuthResource(token.ResourceURL, cfg.URL) {
				return nil, ErrOAuthResourceMismatch
			}
			if token.GrantID == "" {
				id, err := randomURLToken(32)
				if err != nil {
					return nil, err
				}
				token.GrantID = id
				token.Identity = nil
			}
			grant, loginGeneration = token.GrantID, token.LoginGeneration
			return json.Marshal(token)
		})
	})
	if err != nil {
		if ctx.Err() != nil || errors.Is(err, ErrOAuthSuperseded) || errors.Is(err, ErrServerRevoked) || errors.Is(err, ErrManagerClosed) || errors.Is(err, credentialstore.ErrNotFound) || errors.Is(err, ErrOAuthResourceMismatch) {
			status.Identity = nil
			if current, currentErr := m.OAuthStatus(parent, name); currentErr == nil {
				return current, nil
			}
			return status, nil
		}
		status.Identity = unavailableIdentity("identity_credentials_unavailable")
		return status, nil
	}
	// This dedicated call caps the decoded payload and uses the client's normal
	// request path, which rejects server sampling and elicitation requests.
	identity := unavailableIdentity("identity_tool_unavailable")
	if found {
		result, callErr := client.callIdentityTool(ctx, cfg.IdentityProbe)
		identity = normalizeIdentity(cfg.IdentityProbe, result)
		if callErr != nil {
			identity = unavailableIdentity("identity_probe_failed")
			if errors.Is(callErr, errIdentityResultTooLarge) {
				identity = unavailableIdentity("identity_response_too_large")
			}
		}
	}
	err = owner.authorize(ctx, func() error {
		return oauth.store.Update(ctx, oauthNamespace(name), "oauth", func(data []byte) ([]byte, error) {
			if len(data) == 0 {
				return nil, ErrOAuthSuperseded
			}
			var token storedOAuthToken
			if err := json.Unmarshal(data, &token); err != nil {
				return nil, err
			}
			if token.AccessToken == "" || token.GrantID != grant || token.LoginGeneration != loginGeneration {
				return nil, ErrOAuthSuperseded
			}
			if !sameOAuthResource(token.ResourceURL, cfg.URL) {
				return nil, ErrOAuthResourceMismatch
			}
			token.Identity = &storedIdentity{GrantID: grant, Resource: canonicalOAuthResource(cfg.URL), Descriptor: descriptor, Status: *identity}
			return json.Marshal(token)
		})
	})
	if err != nil {
		if ctx.Err() == nil && !errors.Is(err, ErrOAuthSuperseded) && !errors.Is(err, ErrServerRevoked) && !errors.Is(err, ErrManagerClosed) && !errors.Is(err, credentialstore.ErrNotFound) && !errors.Is(err, ErrOAuthResourceMismatch) {
			status.Identity = unavailableIdentity("identity_credentials_unavailable")
			return status, nil
		}
		// A retired operation must not publish even an error for its former owner.
		status.Identity = nil
		if current, currentErr := m.OAuthStatus(parent, name); currentErr == nil {
			return current, nil
		}
		return status, nil
	}
	return m.OAuthStatus(parent, name)
}

var errIdentityResultTooLarge = errors.New("identity response exceeds limit")

func (c *Client) callIdentityTool(ctx context.Context, probe *config.MCPIdentityProbe) (*CallToolResult, error) {
	ctx, cancel := context.WithTimeout(ctx, c.ToolTimeout())
	defer cancel()
	resultBytes, err := callWithProtocol(ctx, c.transport, c.inFlight, "tools/call", CallToolParams{Name: probe.Tool, Arguments: probe.Arguments}, c.protocolVersion)
	if err != nil {
		return nil, err
	}
	if len(resultBytes) > maxIdentityResultBytes {
		return nil, errIdentityResultTooLarge
	}
	var result CallToolResult
	if err := json.Unmarshal(resultBytes, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (m *OAuthManager) identityStatus(ctx context.Context, cfg ServerConfig) (OAuthStatus, error) {
	token, err := m.loadResourceToken(ctx, cfg.Name, cfg.URL)
	if errors.Is(err, credentialstore.ErrNotFound) || errors.Is(err, ErrOAuthResourceMismatch) {
		return OAuthStatus{ServerID: cfg.Name}, nil
	}
	if err != nil {
		return OAuthStatus{}, err
	}
	status := oauthStatus(cfg.Name, token)
	descriptor := identityDescriptor(cfg)
	if cfg.IsEnabled() && token.GrantID != "" && descriptor != "" && token.Identity != nil && token.Identity.GrantID == token.GrantID && token.Identity.Resource == canonicalOAuthResource(cfg.URL) && token.Identity.Descriptor == descriptor {
		status.Identity = &token.Identity.Status
	}
	return status, nil
}
