package personalconnectors

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

// HTTPSCallbackTransport is trusted host composition, never plugin/model RPC.
// The implementation must authenticate its relay delivery, bind it to the
// registered redirect URI, and keep authorization codes out of UI/logs/storage.
// Register installs one in-memory callback and returns an idempotent cancel.
type HTTPSCallbackTransport interface {
	Register(context.Context, string, func(string) error) (func(), error)
}
type OAuthClient struct {
	Provider     string `json:"provider"`
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret,omitempty"`
	RedirectURI  string `json:"redirect_uri,omitempty"`
}
type ConnectRequest struct {
	WorkReadPolicy string   `json:"work_read_policy,omitempty"`
	Provider       string   `json:"provider"`
	ConnectionID   string   `json:"connection_id"`
	AccountID      string   `json:"account_id,omitempty"`
	Domain         string   `json:"domain"`
	Scopes         []string `json:"scopes"`
	Resources      []string `json:"resources"`
}
type OAuthToken struct {
	AccountLabel string
	AccessToken  string
	RefreshToken string
	ExpiresIn    int64
	Scopes       []string
	AccountID    string
	WorkspaceID  string
	Raw          json.RawMessage
}
type oauthDefinition struct {
	AuthorizationURL string
	TokenURL         string
	PKCE             bool
	AuthParams       map[string]string
}
type AuthorizationAttempt struct {
	ID               string    `json:"attempt_id"`
	State            string    `json:"state"`
	ErrorCode        string    `json:"error_code,omitempty"`
	ExpiresAt        time.Time `json:"expires_at"`
	AuthorizationURL string    `json:"authorization_url,omitempty"`
}
type oauthPending struct {
	stopTransport                                              func()
	mu                                                         sync.Mutex
	actor                                                      Actor
	request                                                    ConnectRequest
	client                                                     OAuthClient
	view                                                       AuthorizationAttempt
	state, verifier, redirect, previousRevision, previousEpoch string
	ctx                                                        context.Context
	cancel                                                     context.CancelFunc
	server                                                     *http.Server
}
