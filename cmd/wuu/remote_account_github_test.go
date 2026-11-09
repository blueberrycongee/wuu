package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/remote/account"
	"github.com/blueberrycongee/wuu/internal/remote/host"
	"github.com/blueberrycongee/wuu/internal/remote/secure"
)

func TestRemoteGithubLoginKeepsHostSecretsOutOfRenderer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "remote.json")
	store, err := host.LoadOrCreateStore(path, "test host")
	if err != nil {
		t.Fatal(err)
	}
	requestID := strings.Repeat("a", 43)
	serverURL := ""
	receivedChallenge := ""
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		switch r.URL.Path {
		case "/v1/account/github/start":
			receivedChallenge, _ = body["challenge"].(string)
			json.NewEncoder(w).Encode(map[string]any{"request_id": requestID, "authorize_url": serverURL + "/v1/account/github/authorize?state=" + requestID})
		case "/v1/account/github/poll":
			if body["request_id"] != requestID {
				t.Error("lost login request")
			}
			json.NewEncoder(w).Encode(map[string]any{"status": "authorized", "username": "gh-test"})
		case "/v1/account/github/complete":
			pub, _ := secure.DecodeKey(body["pub"].(string))
			proof, _ := base64.RawURLEncoding.DecodeString(body["proof"].(string))
			if body["role"] != "host" || !secure.VerifyRelayAuth(pub, []byte("wuu/account/enroll/v1:gh-test"), proof, "host") {
				t.Error("host enrollment not signed")
			}
			json.NewEncoder(w).Encode(account.Session{Token: "private-host-session", Username: "gh-test", Pub: body["pub"].(string)})
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer api.Close()
	serverURL = api.URL
	output := captureStdout(t, func() {
		if err = runGithubAccount(context.Background(), path, "github-start", api.URL, store); err != nil {
			t.Fatal(err)
		}
	})
	if receivedChallenge == "" || strings.Contains(output, "verifier") {
		t.Fatal("invalid authorization boundary")
	}
	info, err := os.Stat(path + ".github-login")
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("pending secret not private")
	}
	// Reopen the persisted request, as a new CLI invocation does after app restart.
	reopened, err := host.LoadOrCreateStore(path, "test host")
	if err != nil {
		t.Fatal(err)
	}
	output = captureStdout(t, func() {
		if err = runGithubAccount(context.Background(), path, "github-poll", "", reopened); err != nil {
			t.Fatal(err)
		}
	})
	if strings.Contains(output, "private-host-session") || !strings.Contains(output, "gh-test") {
		t.Fatal("renderer received host credential or no account")
	}
	final, err := host.LoadOrCreateStore(path, "test host")
	if err != nil {
		t.Fatal(err)
	}
	if final.Account() == nil || final.Account().Token != "private-host-session" {
		t.Fatal("host account not persisted")
	}
	if readGithubPending(path) != nil {
		t.Fatal("completed request not removed")
	}
}
