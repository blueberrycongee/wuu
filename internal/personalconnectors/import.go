package personalconnectors

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Import accepts an already granted token through a trusted host-only stdin flow.
// It never creates OAuth consent. Provider identity is checked before persistence;
// expiry requires a new verified import, not a silent refresh with unknown scopes.
func (b *Broker) Import(ctx context.Context, a Actor, input io.Reader) (Connection, error) {
	if e := validateActor(a); e != nil {
		return Connection{}, e
	}
	raw, e := io.ReadAll(io.LimitReader(input, 64*1024+1))
	if e != nil || len(raw) > 64*1024 {
		return Connection{}, fault("invalid_grant")
	}
	var g Grant
	if e = decode(raw, &g); e != nil {
		return Connection{}, e
	}
	if g.ActivationID != "" || g.OAuthProvider != "" || g.RefreshToken != "" || g.RefreshLease != "" {
		return Connection{}, fault("oauth_browser_flow_required")
	}
	if g.AccessToken == "" || len(g.AccessToken) > 8192 || strings.ContainsAny(g.AccessToken, "\r\n") || (!g.NonExpiring && !g.ExpiresAt.After(time.Now().Add(time.Minute))) {
		return Connection{}, fault("invalid_grant")
	}
	if g.NonExpiring && !slices.Contains([]string{"notion", "github", "slack"}, g.Connection.Provider) {
		return Connection{}, fault("invalid_nonexpiring_grant")
	}
	if g.Connection.Provider == "notion" {
		return Connection{}, fault("oauth_required_for_workspace_identity")
	}
	if g.Connection.Provider == "feishu" {
		token, err := b.verifyOtherOAuthIdentity(ctx, "feishu", OAuthToken{AccessToken: g.AccessToken})
		if err != nil {
			return Connection{}, err
		}
		if token.AccountID != g.Connection.AccountID {
			return Connection{}, fault("account_identity_mismatch")
		}
		g.Connection.WorkspaceID = token.WorkspaceID
		g.Connection.ScopeVerification = "provider_enforced_per_request"
		if err = b.RegisterVerified(ctx, a, g); err != nil {
			return Connection{}, err
		}
		connections, err := b.List(ctx, a)
		if err != nil {
			return Connection{}, err
		}
		for _, c := range connections {
			if c.ID == g.Connection.ID {
				return c, nil
			}
		}
		return Connection{}, fault("import_persistence_failed")
	}
	endpoint := ""
	switch g.Connection.Provider {
	case "gmail", "google-calendar", "drive":
		endpoint = "https://www.googleapis.com/oauth2/v2/userinfo"
	case "github":
		endpoint = "https://api.github.com/user"
	case "notion":
		endpoint = "https://api.notion.com/v1/users/me"
	case "slack":
		endpoint = "https://slack.com/api/auth.test"
	default:
		return Connection{}, fault("identity_verification_unavailable")
	}
	bounded, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	stop := context.AfterFunc(b.lifetime, cancel)
	defer stop()
	req, e := http.NewRequestWithContext(bounded, "GET", endpoint, nil)
	if e != nil {
		return Connection{}, fault("invalid_grant")
	}
	if g.Connection.Provider == "slack" {
		req.Method = "POST"
	}
	req.Header.Set("Authorization", "Bearer "+g.AccessToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Notion-Version", "2025-09-03")
	resp, e := b.client.Do(req)
	if e != nil {
		return Connection{}, fault("identity_verification_failed")
	}
	defer resp.Body.Close()
	data, e := io.ReadAll(io.LimitReader(resp.Body, 65537))
	if e != nil || len(data) > 65536 || resp.StatusCode != 200 {
		return Connection{}, fault("identity_scope_required")
	}
	var result struct {
		ID            json.RawMessage `json:"id"`
		Email         string          `json:"email"`
		VerifiedEmail bool            `json:"verified_email"`
		Login         string          `json:"login"`
		OK            bool            `json:"ok"`
		TeamID        string          `json:"team_id"`
		UserID        string          `json:"user_id"`
		Type          string          `json:"type"`
	}
	if json.Unmarshal(data, &result) != nil {
		return Connection{}, fault("identity_verification_failed")
	}
	identity := ""
	switch g.Connection.Provider {
	case "gmail", "google-calendar", "drive":
		if !result.VerifiedEmail {
			return Connection{}, fault("identity_verification_failed")
		}
		_ = json.Unmarshal(result.ID, &identity)
	case "github":
		var numericID json.Number
		if json.Unmarshal(result.ID, &numericID) != nil {
			return Connection{}, fault("identity_verification_failed")
		}
		identity = numericID.String()
	case "notion":
		if result.Type != "bot" {
			return Connection{}, fault("bot_identity_required")
		}
		_ = json.Unmarshal(result.ID, &identity)
	case "slack":
		if !result.OK || result.TeamID == "" || result.UserID == "" {
			return Connection{}, fault("identity_verification_failed")
		}
		identity = result.TeamID + ":" + result.UserID
		g.Connection.WorkspaceID = result.TeamID
	}
	if isGoogle(g.Connection.Provider) {
		g.Connection.AccountLabel = result.Email
	}
	if identity == "" || identity != g.Connection.AccountID {
		return Connection{}, fault("account_identity_mismatch")
	}
	if g.Connection.Provider == "gmail" || g.Connection.Provider == "google-calendar" || g.Connection.Provider == "drive" {
		// Google tokeninfo requires its opaque token query. Never log the request URL
		// or return transport/provider text; it stays inside this trusted import path.
		req, e := http.NewRequestWithContext(bounded, "GET", "https://oauth2.googleapis.com/tokeninfo?access_token="+url.QueryEscape(g.AccessToken), nil)
		if e != nil {
			return Connection{}, fault("scope_verification_failed")
		}
		resp, e := b.client.Do(req)
		if e != nil {
			return Connection{}, fault("scope_verification_failed")
		}
		data, e := io.ReadAll(io.LimitReader(resp.Body, 65537))
		resp.Body.Close()
		if e != nil || len(data) > 65536 || resp.StatusCode != 200 {
			return Connection{}, fault("scope_verification_failed")
		}
		var info struct {
			Scope     string `json:"scope"`
			ExpiresIn string `json:"expires_in"`
			Email     string `json:"email"`
		}
		if json.Unmarshal(data, &info) != nil || info.Email != "" && info.Email != result.Email {
			return Connection{}, fault("scope_verification_failed")
		}
		for _, scope := range g.Connection.Scopes {
			if !slices.Contains(strings.Fields(info.Scope), scope) {
				return Connection{}, fault("scope_mismatch")
			}
		}
		seconds, e := strconv.Atoi(info.ExpiresIn)
		if e != nil || seconds < 60 {
			return Connection{}, fault("authorization_expired")
		}
		actual := time.Now().Add(time.Duration(seconds) * time.Second)
		if actual.Before(g.ExpiresAt) {
			g.ExpiresAt = actual
		}
	}
	g.Connection.ScopeVerification = "provider_enforced_per_request"
	if isGoogle(g.Connection.Provider) {
		g.Connection.ScopeVerification = "provider_verified"
	}
	if e = b.RegisterVerified(ctx, a, g); e != nil {
		return Connection{}, e
	}
	connections, e := b.List(ctx, a)
	if e != nil {
		return Connection{}, e
	}
	for _, c := range connections {
		if c.ID == g.Connection.ID {
			return c, nil
		}
	}
	return Connection{}, fault("import_persistence_failed")
}
