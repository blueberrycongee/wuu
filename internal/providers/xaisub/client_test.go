package xaisub

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/authstorage"
	"github.com/blueberrycongee/wuu/internal/providers"
)

func TestClientChatUsesStoredAccessToken(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("WUU_HOME", "")
	if err := os.MkdirAll(filepath.Join(home, ".wuu"), 0o700); err != nil {
		t.Fatal(err)
	}
	store := testSelectedAuth(t, home)
	if err := store.Set(AuthProviderID, authstorage.Credentials{
		Type:         "oauth",
		AccessToken:  "supergrok-token",
		RefreshToken: "rt",
		ExpiresAt:    time.Now().Add(time.Hour),
		AuthMode:     "xai",
	}); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer supergrok-token" {
			t.Fatalf("Authorization = %q", got)
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"reasoning.encrypted_content"`) {
			t.Fatalf("payload missing encrypted reasoning include: %s", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}`))
	}))
	defer server.Close()

	client, err := New(ClientConfig{BaseURL: server.URL, Credentials: testSelectedCredentials(t, home), HTTPClient: server.Client()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	resp, err := client.Chat(context.Background(), providers.ChatRequest{
		Model:    DefaultModel,
		Messages: []providers.ChatMessage{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Content != "ok" {
		t.Fatalf("content = %q", resp.Content)
	}
}
