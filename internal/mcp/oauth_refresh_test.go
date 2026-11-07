package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/credentialstore"
)

// These cases exercise persisted coordination, independent of the optional
// in-process refresh coordinator used by retained runtime generations.
func TestOAuthIndependentManagersSerializeRefresh(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var exchanges atomic.Int32
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if exchanges.Add(1) == 1 {
			close(started)
			<-release
		}
		_ = json.NewEncoder(w).Encode(tokenResponse{AccessToken: "rotated", RefreshToken: "rotated-refresh", ExpiresIn: 3600})
	}))
	defer endpoint.Close()
	defer close(release)
	path := filepath.Join(t.TempDir(), "oauth.json")
	seedOAuthRefresh(t, path, endpoint.URL)
	first := NewOAuthManager(credentialstore.NewFileStore(path), endpoint.Client())
	second := NewOAuthManager(credentialstore.NewFileStore(path), endpoint.Client())
	done := make(chan error, 1)
	go func() { _, err := first.AccessToken(context.Background(), "docs", endpoint.URL); done <- err }()
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	if _, err := second.RefreshAccessToken(ctx, "docs", endpoint.URL); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("independent caller did not wait for the refresh: %v", err)
	}
	if got := exchanges.Load(); got != 1 {
		t.Fatalf("one grant was rotated %d times concurrently", got)
	}
	release <- struct{}{}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if token, err := second.AccessToken(context.Background(), "docs", endpoint.URL); err != nil || token != "rotated" {
		t.Fatalf("independent caller did not reuse rotated token: %q %v", token, err)
	}
}

func TestOAuthUnknownRefreshDoesNotReplayAfterRestart(t *testing.T) {
	for _, failure := range []string{"invalid-response", "canceled"} {
		t.Run(failure, func(t *testing.T) {
			started, release := make(chan struct{}), make(chan struct{})
			var exchanges atomic.Int32
			endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				resource := "http://" + r.Host
				switch r.URL.Path {
				case "/.well-known/oauth-protected-resource":
					_ = json.NewEncoder(w).Encode(map[string]any{"resource": resource, "authorization_servers": []string{resource}})
					return
				case "/.well-known/oauth-authorization-server":
					_ = json.NewEncoder(w).Encode(map[string]any{"issuer": resource, "authorization_endpoint": resource + "/authorize", "token_endpoint": resource + "/token"})
					return
				}
				if r.Method != http.MethodPost {
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				_ = r.ParseForm()
				if r.Form.Get("grant_type") == "authorization_code" {
					_ = json.NewEncoder(w).Encode(tokenResponse{AccessToken: "reauthorized", RefreshToken: "new-refresh", ExpiresIn: 3600})
					return
				}
				attempt := exchanges.Add(1)
				if failure == "canceled" && attempt == 1 {
					close(started)
					<-release
				}
				_, _ = w.Write([]byte("truncated response"))
			}))
			defer endpoint.Close()
			defer close(release)
			path := filepath.Join(t.TempDir(), "oauth.json")
			seedOAuthRefresh(t, path, endpoint.URL)
			manager := NewOAuthManager(credentialstore.NewFileStore(path), endpoint.Client())
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { _, err := manager.AccessToken(ctx, "docs", endpoint.URL); done <- err }()
			if failure == "canceled" {
				<-started
				cancel()
			}
			if err := <-done; err == nil {
				t.Fatal("unknown refresh succeeded")
			}
			restarted := NewOAuthManager(credentialstore.NewFileStore(path), endpoint.Client())
			if token, err := restarted.AccessToken(context.Background(), "docs", endpoint.URL); token != "" || !errors.Is(err, ErrOAuthRequired) {
				t.Fatalf("unknown rotation must require reauthorization: %q %v", token, err)
			}
			if got := exchanges.Load(); got != 1 {
				t.Fatalf("unknown rotation was replayed %d times", got)
			}
			if status, err := restarted.Status(context.Background(), "docs", endpoint.URL); err != nil || status.Authenticated {
				t.Fatalf("unknown grant is still advertised as authenticated: %+v %v", status, err)
			}
			oldGrant, err := restarted.boundTokenProvider(context.Background(), "docs", endpoint.URL)
			if err != nil {
				t.Fatal(err)
			}
			start, err := restarted.Start(context.Background(), OAuthStartOptions{ServerID: "docs", ResourceURL: endpoint.URL, RedirectURI: "http://127.0.0.1/callback", ClientID: "fixture"})
			if err != nil {
				t.Fatal(err)
			}
			status, err := restarted.Finish(context.Background(), OAuthFinishOptions{ServerID: "docs", ResourceURL: endpoint.URL, State: start.State, Code: "new-code"})
			if err != nil || !status.Authenticated {
				t.Fatalf("normal login did not recover uncertain refresh: %+v %v", status, err)
			}
			if token, err := restarted.AccessToken(context.Background(), "docs", endpoint.URL); err != nil || token != "reauthorized" {
				t.Fatalf("new login remained fenced: %q %v", token, err)
			}
			if token, err := oldGrant(context.Background(), false); token != "" || !errors.Is(err, ErrOAuthSuperseded) {
				t.Fatalf("old connection borrowed reauthorized account: %q %v", token, err)
			}
		})
	}
}

func TestOAuthRefreshProcessDeathDoesNotReplay(t *testing.T) {
	if path := os.Getenv("WUU_TEST_OAUTH_REFRESH_STORE"); path != "" {
		manager := NewOAuthManager(credentialstore.NewFileStore(path), nil)
		_, err := manager.AccessToken(context.Background(), "docs", os.Getenv("WUU_TEST_OAUTH_REFRESH_RESOURCE"))
		t.Fatal("child refresh unexpectedly returned:", err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	var exchanges atomic.Int32
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if exchanges.Add(1) == 1 {
			close(started)
			<-release
		}
		_ = json.NewEncoder(w).Encode(tokenResponse{AccessToken: "rotated", ExpiresIn: 3600})
	}))
	defer endpoint.Close()
	defer close(release)
	path := filepath.Join(t.TempDir(), "oauth.json")
	seedOAuthRefresh(t, path, endpoint.URL)
	child := exec.Command(os.Args[0], "-test.run=^TestOAuthRefreshProcessDeathDoesNotReplay$")
	child.Env = append(os.Environ(), "WUU_TEST_OAUTH_REFRESH_STORE="+path, "WUU_TEST_OAUTH_REFRESH_RESOURCE="+endpoint.URL)
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer child.Process.Kill()
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("child did not begin its refresh")
	}
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = child.Wait()
	restarted := NewOAuthManager(credentialstore.NewFileStore(path), endpoint.Client())
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	if token, err := restarted.AccessToken(ctx, "docs", endpoint.URL); token != "" || err == nil {
		t.Fatalf("surviving process reused an uncertain grant: %q %v", token, err)
	}
	if got := exchanges.Load(); got != 1 {
		t.Fatalf("process death replayed a rotating token: %d requests", got)
	}
}

func TestOAuthBoundRefreshCannotPublishAfterAccountChange(t *testing.T) {
	for _, change := range []string{"logout", "replace"} {
		t.Run(change, func(t *testing.T) {
			started, release := make(chan struct{}), make(chan struct{})
			endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				close(started)
				<-release
				_ = json.NewEncoder(w).Encode(tokenResponse{AccessToken: "old-account-rotated", ExpiresIn: 3600})
			}))
			defer endpoint.Close()
			defer close(release)
			path := filepath.Join(t.TempDir(), "oauth.json")
			seedOAuthRefresh(t, path, endpoint.URL)
			store := credentialstore.NewFileStore(path)
			manager := NewOAuthManager(store, endpoint.Client())
			provider, err := manager.boundTokenProvider(context.Background(), "docs", endpoint.URL)
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { _, err := provider(context.Background(), false); done <- err }()
			<-started
			other := NewOAuthManager(credentialstore.NewFileStore(path), endpoint.Client())
			if change == "logout" {
				if err := other.Remove(context.Background(), "docs"); err != nil {
					t.Fatal(err)
				}
			} else {
				data, _ := json.Marshal(storedOAuthToken{GrantID: "new-account", AccessToken: "new-account-access", ResourceURL: endpoint.URL})
				if err := store.Set(context.Background(), oauthNamespace("docs"), "oauth", data); err != nil {
					t.Fatal(err)
				}
			}
			release <- struct{}{}
			if err := <-done; !errors.Is(err, ErrOAuthSuperseded) {
				t.Fatalf("account change did not fence refresh: %v", err)
			}
			if token, err := provider(context.Background(), false); token != "" || err == nil {
				t.Fatalf("old connection borrowed another grant: %q %v", token, err)
			}
			if change == "replace" {
				if token, err := other.AccessToken(context.Background(), "docs", endpoint.URL); err != nil || token != "new-account-access" {
					t.Fatalf("replacement was overwritten: %q %v", token, err)
				}
			}
		})
	}
}

func seedOAuthRefresh(t *testing.T, path, resource string) {
	t.Helper()
	data, err := json.Marshal(storedOAuthToken{GrantID: "original-grant", CredentialVersion: "original-version", AccessToken: "expired", RefreshToken: "original-refresh", ExpiresAt: time.Now().Add(-time.Hour), TokenEndpoint: resource, ResourceURL: resource})
	if err != nil {
		t.Fatal(err)
	}
	if err := credentialstore.NewFileStore(path).Set(context.Background(), oauthNamespace("docs"), "oauth", data); err != nil {
		t.Fatal(err)
	}
}

func TestOAuthDefiniteRefreshFailureKeepsCredential(t *testing.T) {
	for _, failure := range []string{"unavailable", "invalid-grant", "unreachable"} {
		t.Run(failure, func(t *testing.T) {
			var exchanges atomic.Int32
			var refreshTokens []string
			resource := ""
			var endpoint *httptest.Server
			if failure == "unreachable" {
				listener, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				resource = "http://" + listener.Addr().String()
				if err := listener.Close(); err != nil {
					t.Fatal(err)
				}
			} else {
				endpoint = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					_ = r.ParseForm()
					refreshTokens = append(refreshTokens, r.Form.Get("refresh_token"))
					switch failure {
					case "unavailable":
						if exchanges.Add(1) == 1 {
							w.WriteHeader(http.StatusServiceUnavailable)
							return
						}
						_ = json.NewEncoder(w).Encode(tokenResponse{AccessToken: "recovered", RefreshToken: "rotated-refresh", ExpiresIn: 3600})
					case "invalid-grant":
						exchanges.Add(1)
						w.WriteHeader(http.StatusBadRequest)
						_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
					}
				}))
				defer endpoint.Close()
				resource = endpoint.URL
			}
			path := filepath.Join(t.TempDir(), "oauth.json")
			seedOAuthRefresh(t, path, resource)
			client := http.DefaultClient
			if endpoint != nil {
				client = endpoint.Client()
			}
			manager := NewOAuthManager(credentialstore.NewFileStore(path), client)
			_, err := manager.AccessToken(context.Background(), "docs", resource)
			if err == nil {
				t.Fatal("definite refresh failure returned a token")
			}
			if errors.Is(err, ErrOAuthRefreshUncertain) {
				t.Fatalf("definite failure marked the grant uncertain: %v", err)
			}
			if failure == "invalid-grant" {
				if !errors.Is(err, ErrOAuthRequired) {
					t.Fatalf("invalid_grant must require sign-in: %v", err)
				}
			} else if errors.Is(err, ErrOAuthRequired) {
				t.Fatalf("transient failure must not require sign-in: %v", err)
			}
			record := storedOAuthToken{}
			data, err := credentialstore.NewFileStore(path).Get(context.Background(), oauthNamespace("docs"), "oauth")
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(data, &record); err != nil {
				t.Fatal(err)
			}
			if record.Refresh != nil {
				t.Fatalf("claim survived a definite failure: %+v", record.Refresh)
			}
			if record.RefreshToken != "original-refresh" {
				t.Fatalf("credential changed after a definite failure: %+v", record)
			}
			switch failure {
			case "unavailable":
				restarted := NewOAuthManager(credentialstore.NewFileStore(path), endpoint.Client())
				token, err := restarted.AccessToken(context.Background(), "docs", resource)
				if err != nil || token != "recovered" {
					t.Fatalf("retry after restart did not recover: %q %v", token, err)
				}
				if got := exchanges.Load(); got != 2 {
					t.Fatalf("endpoint saw %d exchanges", got)
				}
				for i, refreshToken := range refreshTokens {
					if refreshToken != "original-refresh" {
						t.Fatalf("exchange %d replayed %q instead of the original refresh token", i, refreshToken)
					}
				}
			case "invalid-grant":
				if got := exchanges.Load(); got != 1 {
					t.Fatalf("endpoint saw %d exchanges", got)
				}
			}
		})
	}
}
