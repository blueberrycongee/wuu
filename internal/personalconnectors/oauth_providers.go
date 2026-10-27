package personalconnectors

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Provider URLs are fixed here; neither model arguments nor a callback can
// choose where client secrets or authorization codes are exchanged.
func otherOAuthProvider(provider string) (oauthDefinition, error) {
	switch provider {
	case "notion":
		return oauthDefinition{AuthorizationURL: "https://api.notion.com/v1/oauth/authorize", TokenURL: "https://api.notion.com/v1/oauth/token", AuthParams: map[string]string{"owner": "user"}}, nil
	case "github":
		return oauthDefinition{AuthorizationURL: "https://github.com/login/oauth/authorize", TokenURL: "https://github.com/login/oauth/access_token", PKCE: true, AuthParams: map[string]string{"allow_signup": "false"}}, nil
	case "slack":
		return oauthDefinition{AuthorizationURL: "https://slack.com/oauth/v2/authorize", TokenURL: "https://slack.com/api/oauth.v2.access"}, nil
	case "feishu":
		return oauthDefinition{AuthorizationURL: "https://accounts.feishu.cn/open-apis/authen/v1/authorize", TokenURL: "https://open.feishu.cn/open-apis/authen/v2/oauth/token"}, nil
	default:
		return oauthDefinition{}, fault("unsupported_provider")
	}
}
func otherOAuthTokenRequest(c OAuthClient, values url.Values) (*http.Request, error) {
	def, err := otherOAuthProvider(c.Provider)
	if err != nil {
		return nil, err
	}
	form := url.Values{}
	for k, v := range values {
		form[k] = append([]string{}, v...)
	}
	if !def.PKCE {
		form.Del("code_verifier")
	}
	form.Set("client_id", c.ClientID)
	if c.Provider != "notion" {
		form.Set("client_secret", c.ClientSecret)
	}
	var req *http.Request
	if c.Provider == "notion" || c.Provider == "feishu" {
		body := map[string]string{}
		for k := range form {
			body[k] = form.Get(k)
		}
		if c.Provider == "notion" {
			delete(body, "client_id")
			delete(body, "client_secret")
		}
		req, err = http.NewRequest("POST", def.TokenURL, strings.NewReader(string(providerJSON(body))))
		if err != nil {
			return nil, fault("invalid_oauth_request")
		}
		req.Header.Set("Content-Type", "application/json")
	} else {
		req, err = http.NewRequest("POST", def.TokenURL, strings.NewReader(form.Encode()))
		if err != nil {
			return nil, fault("invalid_oauth_request")
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	req.Header.Set("Accept", "application/json")
	if c.Provider == "notion" {
		req.SetBasicAuth(c.ClientID, c.ClientSecret)
		req.Header.Set("Notion-Version", notionVersion)
	}
	return req, nil
}
func oauthScopeList(s string) []string { return strings.Fields(strings.ReplaceAll(s, ",", " ")) }
func decodeOtherOAuthToken(provider string, body []byte) (OAuthToken, error) {
	var raw struct {
		AccessToken  string          `json:"access_token"`
		RefreshToken string          `json:"refresh_token"`
		ExpiresIn    int64           `json:"expires_in"`
		Scope        string          `json:"scope"`
		TokenType    string          `json:"token_type"`
		Error        json.RawMessage `json:"error"`
		OK           *bool           `json:"ok"`
		Code         *int            `json:"code"`
		BotID        string          `json:"bot_id"`
		WorkspaceID  string          `json:"workspace_id"`
		Team         struct {
			ID string `json:"id"`
		} `json:"team"`
		AuthedUser struct {
			ID           string `json:"id"`
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
			Scope        string `json:"scope"`
			ExpiresIn    int64  `json:"expires_in"`
		} `json:"authed_user"`
	}
	if json.Unmarshal(body, &raw) != nil || len(raw.Error) > 0 && string(raw.Error) != "null" || raw.ExpiresIn < 0 {
		return OAuthToken{}, fault("oauth_exchange_failed")
	}
	token := OAuthToken{AccessToken: raw.AccessToken, RefreshToken: raw.RefreshToken, ExpiresIn: raw.ExpiresIn, Scopes: oauthScopeList(raw.Scope)}
	switch provider {
	case "notion":
		if raw.BotID == "" || raw.WorkspaceID == "" {
			return OAuthToken{}, fault("oauth_identity_missing")
		}
		token.AccountID = raw.BotID
		token.WorkspaceID = raw.WorkspaceID
	case "github":
		if raw.TokenType != "" && !strings.EqualFold(raw.TokenType, "bearer") {
			return OAuthToken{}, fault("oauth_token_type")
		}
	case "slack":
		if raw.OK == nil || !*raw.OK {
			return OAuthToken{}, fault("oauth_exchange_failed")
		}
		if raw.AuthedUser.AccessToken != "" {
			if raw.Team.ID == "" || raw.AuthedUser.ID == "" {
				return OAuthToken{}, fault("oauth_identity_missing")
			}
			token.AccessToken = raw.AuthedUser.AccessToken
			token.RefreshToken = raw.AuthedUser.RefreshToken
			token.ExpiresIn = raw.AuthedUser.ExpiresIn
			token.Scopes = oauthScopeList(raw.AuthedUser.Scope)
			token.WorkspaceID = raw.Team.ID
			token.AccountID = raw.Team.ID + ":" + raw.AuthedUser.ID
		} else if raw.TokenType != "user" || raw.AccessToken == "" {
			return OAuthToken{}, fault("user_token_required")
		}
		// Rotating user-token refresh responses can carry the token at root.
		// auth.test must still establish the exact team/user before storage.

	case "feishu":
		if raw.Code != nil && *raw.Code != 0 {
			return OAuthToken{}, fault("oauth_exchange_failed")
		}
	default:
		return OAuthToken{}, fault("unsupported_provider")
	}
	if token.AccessToken == "" || len(token.AccessToken) > 8192 || strings.ContainsAny(token.AccessToken, "\r\n") || len(token.RefreshToken) > 8192 || strings.ContainsAny(token.RefreshToken, "\r\n") {
		return OAuthToken{}, fault("invalid_oauth_token")
	}
	return token, nil
}

// verifyOtherOAuthIdentity never uses display names or email as authority.
// Workspace IDs from the token response must agree with the authenticated API.
func (b *Broker) verifyOtherOAuthIdentity(ctx context.Context, provider string, token OAuthToken) (OAuthToken, error) {
	p := Plan{Method: "GET"}
	switch provider {
	case "notion":
		p.URL = "https://api.notion.com/v1/users/me"
		p.Headers = map[string]string{"Notion-Version": notionVersion}
	case "github":
		p.URL = "https://api.github.com/user"
		p.Headers = map[string]string{"Accept": "application/vnd.github+json", "X-GitHub-Api-Version": "2022-11-28"}
	case "slack":
		p.URL = "https://slack.com/api/auth.test"
	case "feishu":
		p.URL = "https://open.feishu.cn/open-apis/authen/v1/user_info"
	default:
		return OAuthToken{}, fault("unsupported_provider")
	}
	raw, err := b.identityRequest(ctx, provider, token.AccessToken, p)
	if err != nil {
		return OAuthToken{}, fault("oauth_identity_unverified")
	}
	account, workspace := "", ""
	switch provider {
	case "notion":
		var v struct {
			ID     string `json:"id"`
			Type   string `json:"type"`
			Object string `json:"object"`
		}
		if json.Unmarshal(raw, &v) != nil || v.Type != "bot" || v.Object != "user" || token.WorkspaceID == "" {
			return OAuthToken{}, fault("oauth_identity_unverified")
		}
		account = v.ID
		workspace = token.WorkspaceID
	case "github":
		var v struct {
			ID int64 `json:"id"`
		}
		if json.Unmarshal(raw, &v) != nil || v.ID < 1 {
			return OAuthToken{}, fault("oauth_identity_unverified")
		}
		account = strconv.FormatInt(v.ID, 10)
	case "slack":
		var v struct {
			OK     bool   `json:"ok"`
			TeamID string `json:"team_id"`
			UserID string `json:"user_id"`
		}
		if json.Unmarshal(raw, &v) != nil || !v.OK || v.TeamID == "" || v.UserID == "" {
			return OAuthToken{}, fault("oauth_identity_unverified")
		}
		workspace = v.TeamID
		account = v.TeamID + ":" + v.UserID
	case "feishu":
		var v struct {
			Code *int `json:"code"`
			Data struct {
				TenantKey string `json:"tenant_key"`
				OpenID    string `json:"open_id"`
			} `json:"data"`
		}
		if json.Unmarshal(raw, &v) != nil || v.Code == nil || *v.Code != 0 || v.Data.TenantKey == "" || v.Data.OpenID == "" {
			return OAuthToken{}, fault("oauth_identity_unverified")
		}
		workspace = v.Data.TenantKey
		account = workspace + ":" + v.Data.OpenID
	}
	if account == "" || token.AccountID != "" && token.AccountID != account || token.WorkspaceID != "" && token.WorkspaceID != workspace {
		return OAuthToken{}, fault("oauth_identity_mismatch")
	}
	token.AccountID = account
	token.WorkspaceID = workspace
	return token, nil
}
