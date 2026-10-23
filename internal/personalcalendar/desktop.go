package personalcalendar

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/blueberrycongee/wuu/internal/credentialstore"
)

const clientNamespace = "personal.google-calendar.client.v1"

// DesktopOptions is trusted host composition, never decoded from plugin RPC.
// Nil dependencies use the OS secure store and the ordinary HTTPS client.
type DesktopOptions struct {
	ClientID   string
	StateDir   string
	Store      credentialstore.Store
	HTTPClient *http.Client
}

func validClientID(id string) bool {
	return len(id) > len(".apps.googleusercontent.com") && len(id) <= 512 && strings.HasSuffix(id, ".apps.googleusercontent.com") && !strings.ContainsAny(id, " /\\\r\n\t")
}

func decodeClient(raw []byte) (Config, error) {
	var stored struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&stored) != nil || d.Decode(new(any)) != io.EOF || !validClientID(stored.ClientID) || len(stored.ClientSecret) > 4096 {
		return Config{}, fault("invalid_client_configuration")
	}
	return Config{ClientID: stored.ClientID, ClientSecret: stored.ClientSecret}, nil
}

// ImportDesktopClient imports an already approved client's data into the host
// secure store. It neither creates a Google client nor starts account OAuth.
func ImportDesktopClient(ctx context.Context, store credentialstore.Store, input io.Reader) (string, error) {
	raw, err := io.ReadAll(io.LimitReader(input, 8193))
	if err != nil || len(raw) > 8192 {
		return "", fault("invalid_client_configuration")
	}
	config, err := decodeClient(raw)
	if err != nil {
		return "", err
	}
	if store == nil {
		return "", fault("secure_store_unavailable")
	}
	if err = store.Set(ctx, clientNamespace, key(config.ClientID), raw); err != nil {
		return "", fault("secure_store_unavailable")
	}
	return config.ClientID, nil
}

// NewDesktop does not touch secure storage unless an explicit client ID exists.
// The completion record is rooted in the embedding host's resolved state path.
func NewDesktop(options DesktopOptions) (*Broker, error) {
	if !validClientID(options.ClientID) {
		return nil, fault("oauth_client_not_configured")
	}
	if !filepath.IsAbs(options.StateDir) {
		return nil, fault("activation_record_unavailable")
	}
	store := options.Store
	if store == nil {
		var err error
		store, err = credentialstore.NewDesktopStore()
		if err != nil {
			return nil, fault("secure_store_unavailable")
		}
	}
	raw, err := store.Get(context.Background(), clientNamespace, key(options.ClientID))
	if errors.Is(err, credentialstore.ErrNotFound) {
		return nil, fault("oauth_client_not_configured")
	}
	if err != nil {
		return nil, fault("secure_store_unavailable")
	}
	config, err := decodeClient(raw)
	if err != nil || config.ClientID != options.ClientID {
		return nil, fault("invalid_client_configuration")
	}
	client := options.HTTPClient
	if client == nil {
		client = &http.Client{}
	}
	return newBroker(config, store, client, filepath.Join(options.StateDir, "activation-v1")), nil
}

// ValidateAuthorizationURL constrains the native opener to broker OAuth URLs.
func ValidateAuthorizationURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host != "accounts.google.com" || u.Path != "/o/oauth2/v2/auth" || u.User != nil || u.Fragment != "" {
		return fault("authorization_browser_unavailable")
	}
	if u.Query().Get("client_id") == "" || u.Query().Get("state") == "" || u.Query().Get("code_challenge_method") != "S256" {
		return fault("authorization_browser_unavailable")
	}
	return nil
}
