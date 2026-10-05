package xaisub

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/authstorage"
)

func TestRequestAndExchangeDeviceCode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s", r.Method)
		}
		body, _ := io.ReadAll(r.Body)
		form, err := url.ParseQuery(string(body))
		if err != nil {
			t.Fatalf("parse form: %v", err)
		}
		switch r.URL.Path {
		case "/oauth2/device/code":
			if form.Get("client_id") != defaultClientID || form.Get("referrer") != "wuu" {
				t.Fatalf("device code form = %v", form)
			}
			if !strings.Contains(form.Get("scope"), "grok-cli:access") {
				t.Fatalf("scope = %q", form.Get("scope"))
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"device_code":               "dev-1",
				"user_code":                 "ABCD-EFGH",
				"verification_uri":          "https://auth.x.ai/device",
				"verification_uri_complete": "https://auth.x.ai/device?user_code=ABCD-EFGH",
				"interval":                  1,
				"expires_in":                600,
			})
		case "/oauth2/token":
			if form.Get("grant_type") != deviceCodeGrantType || form.Get("device_code") != "dev-1" {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token":  "access-1",
				"refresh_token": "refresh-1",
				"expires_in":    3600,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("WUU_XAI_DEVICE_CODE_URL", server.URL+"/oauth2/device/code")
	t.Setenv("WUU_XAI_TOKEN_URL", server.URL+"/oauth2/token")

	device, err := RequestDeviceCode(context.Background(), server.Client())
	if err != nil {
		t.Fatalf("RequestDeviceCode: %v", err)
	}
	if device.UserCode != "ABCD-EFGH" || device.DeviceCode != "dev-1" {
		t.Fatalf("device = %+v", device)
	}
	tokens, err := ExchangeDeviceCode(context.Background(), server.Client(), device)
	if err != nil {
		t.Fatalf("ExchangeDeviceCode: %v", err)
	}
	if tokens.AccessToken != "access-1" || tokens.RefreshToken != "refresh-1" {
		t.Fatalf("tokens = %+v", tokens)
	}
}

func TestOAuthSourceRefreshesStoredCredentials(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("WUU_HOME", "")
	if err := os.MkdirAll(filepath.Join(home, ".wuu"), 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := authstorage.ForHome(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set(AuthProviderID, authstorage.Credentials{
		Type:         "oauth",
		AccessToken:  "old-access",
		RefreshToken: "rt-old",
		ExpiresAt:    time.Now().Add(-time.Minute),
		AuthMode:     "xai",
		Source:       "wuu",
	}); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth2/token" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		form, _ := url.ParseQuery(string(body))
		if form.Get("grant_type") != "refresh_token" || form.Get("refresh_token") != "rt-old" {
			t.Fatalf("refresh form = %v", form)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "new-access",
			"refresh_token": "rt-new",
			"expires_in":    3600,
		})
	}))
	defer server.Close()
	t.Setenv("WUU_XAI_TOKEN_URL", server.URL+"/oauth2/token")

	source := NewOAuthSource(OAuthConfig{Home: home, HTTPClient: server.Client()})
	creds, err := source.Credentials(context.Background(), false)
	if err != nil {
		t.Fatalf("Credentials: %v", err)
	}
	if creds.accessToken != "new-access" || creds.refreshToken != "rt-new" || !creds.refreshable {
		t.Fatalf("creds = %+v", creds)
	}
	stored, err := store.Get(AuthProviderID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.AccessToken != "new-access" || stored.RefreshToken != "rt-new" {
		t.Fatalf("stored = %+v", stored)
	}
}

func TestLoginHubStartAndPoll(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("WUU_HOME", "")
	if err := os.MkdirAll(filepath.Join(home, ".wuu"), 0o700); err != nil {
		t.Fatal(err)
	}
	pending := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth2/device/code":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"device_code":      "dev-2",
				"user_code":        "WXYZ-1234",
				"verification_uri": "https://auth.x.ai/device",
				"interval":         1,
				"expires_in":       600,
			})
		case "/oauth2/token":
			if pending {
				pending = false
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "authorization_pending"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token":  "live-access",
				"refresh_token": "live-refresh",
				"expires_in":    1200,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("WUU_XAI_DEVICE_CODE_URL", server.URL+"/oauth2/device/code")
	t.Setenv("WUU_XAI_TOKEN_URL", server.URL+"/oauth2/token")

	hub := NewLoginHub()
	start, err := hub.Start(context.Background())
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if start.UserCode != "WXYZ-1234" || start.LoginID == "" {
		t.Fatalf("start = %+v", start)
	}
	first, err := hub.Poll(context.Background(), start.LoginID, home, DefaultBaseURL)
	if err != nil {
		t.Fatalf("first poll: %v", err)
	}
	if first.Status != LoginPending {
		t.Fatalf("first status = %q", first.Status)
	}
	second, err := hub.Poll(context.Background(), start.LoginID, home, DefaultBaseURL)
	if err != nil {
		t.Fatalf("second poll: %v", err)
	}
	if second.Status != LoginSuccess {
		t.Fatalf("second status = %+v", second)
	}
	if _, err := LocalOAuthStatus(home); err != nil {
		t.Fatalf("LocalOAuthStatus: %v", err)
	}
}

func TestRejectsNonHTTPSVerificationURI(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"device_code":      "dev",
			"user_code":        "CODE",
			"verification_uri": "http://evil.example/device",
			"expires_in":       60,
		})
	}))
	defer server.Close()
	t.Setenv("WUU_XAI_DEVICE_CODE_URL", server.URL+"/oauth2/device/code")
	if _, err := RequestDeviceCode(context.Background(), server.Client()); err == nil {
		t.Fatal("expected untrusted verification URI error")
	}
}

func TestLoginCancellationDiscardsInFlightTokens(t *testing.T) {
	home := t.TempDir()
	t.Setenv("WUU_HOME", "")
	if err := PersistTokens(home, TokenResponse{AccessToken: "existing-access", RefreshToken: "existing-refresh", ExpiresIn: time.Hour}, DefaultBaseURL); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/device" {
			_ = json.NewEncoder(w).Encode(map[string]any{"device_code": "fixture-device", "user_code": "TEST-CODE", "verification_uri": "https://auth.x.ai/device", "expires_in": 300})
			return
		}
		close(started)
		<-release
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "cancelled-access", "refresh_token": "cancelled-refresh", "expires_in": 1200})
	}))
	defer server.Close()
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	t.Setenv("WUU_XAI_DEVICE_CODE_URL", server.URL+"/device")
	t.Setenv("WUU_XAI_TOKEN_URL", server.URL+"/token")
	hub := NewLoginHub()
	start, err := hub.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan LoginPollStatus, 1)
	go func() {
		status, pollErr := hub.Poll(context.Background(), start.LoginID, home, DefaultBaseURL)
		if pollErr != nil {
			t.Error(pollErr)
		}
		result <- status
	}()
	<-started
	hub.Cancel(start.LoginID)
	close(release)
	if status := <-result; status.Status == LoginSuccess {
		t.Fatal("cancelled login reported success")
	}
	source := NewOAuthSource(OAuthConfig{Home: home})
	credentials, err := source.Credentials(context.Background(), false)
	if err != nil || credentials.accessToken != "existing-access" {
		t.Fatalf("cancelled login replaced existing credentials: %v", err)
	}
}

// A sign-in writes the auth store, not the sources already built from it, so a
// long-lived source (a shared client, an existing conversation) must follow the
// store instead of going on with the token it cached before.
func TestOAuthSourceFollowsAuthStoreAfterSignInAndSignOut(t *testing.T) {
	home := t.TempDir()
	signIn := func(access string) {
		t.Helper()
		if err := PersistTokens(home, TokenResponse{AccessToken: access, RefreshToken: "refresh-" + access, ExpiresIn: time.Hour}, ""); err != nil {
			t.Fatalf("PersistTokens: %v", err)
		}
	}
	signIn("old-access")
	source := NewOAuthSource(OAuthConfig{Home: home})
	token := func() (string, error) {
		creds, err := source.Credentials(context.Background(), false)
		return creds.accessToken, err
	}
	if got, err := token(); err != nil || got != "old-access" {
		t.Fatalf("first token = %q, %v", got, err)
	}

	signIn("new-access")
	if got, err := token(); err != nil || got != "new-access" {
		t.Fatalf("after signing in again the source sent %q (err %v), want the new token", got, err)
	}

	if err := DeleteTokens(home); err != nil {
		t.Fatalf("DeleteTokens: %v", err)
	}
	if got, err := token(); err == nil {
		t.Fatalf("after signing out the source still sent %q", got)
	}
}
