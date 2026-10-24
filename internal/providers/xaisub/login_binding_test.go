package xaisub

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestLoginKeepsSelectedSourceAndReleasesAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	selected := testSelectedCredentials(t, t.TempDir())
	other := testSelectedCredentials(t, t.TempDir())
	started := make(chan struct{})
	var block atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/device" {
			_ = json.NewEncoder(w).Encode(map[string]any{"device_code": "device", "user_code": "CODE", "verification_uri": "https://auth.x.ai/device", "expires_in": 600, "interval": 1})
			return
		}
		_, _ = io.Copy(io.Discard, r.Body)
		if block.Load() {
			close(started)
			<-r.Context().Done()
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "selected-login", "refresh_token": "refresh", "expires_in": 3600})
	}))
	defer server.Close()
	t.Setenv("WUU_XAI_DEVICE_CODE_URL", server.URL+"/device")
	t.Setenv("WUU_XAI_TOKEN_URL", server.URL+"/token")
	hub := NewLoginHub()
	defer hub.Close()
	var released atomic.Int32
	start, err := hub.Start(ctx, selected, func() { released.Add(1) })
	if err != nil {
		t.Fatal(err)
	}
	status, err := hub.Poll(ctx, start.LoginID, DefaultBaseURL)
	if err != nil || status.Status != LoginSuccess || released.Load() != 1 {
		t.Fatalf("selected login status=%s release=%d err=%v", status.Status, released.Load(), err)
	}
	if _, err := OAuthStatus(ctx, selected); err != nil {
		t.Fatal(err)
	}
	if _, err := OAuthStatus(ctx, other); err == nil {
		t.Fatal("login wrote another provider")
	}
	if err := RemoveTokens(ctx, selected); err != nil {
		t.Fatal(err)
	}
	block.Store(true)
	start, err = hub.Start(ctx, selected, func() { released.Add(1) })
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := hub.Poll(ctx, start.LoginID, DefaultBaseURL); done <- err }()
	<-started
	hub.Cancel(start.LoginID)
	<-done
	if released.Load() != 2 {
		t.Fatalf("cancel releases=%d", released.Load())
	}
	if _, err := OAuthStatus(ctx, selected); err == nil {
		t.Fatal("cancelled login persisted credentials")
	}
	hub.Close()
	if _, err := hub.Start(ctx, selected, func() {}); err == nil {
		t.Fatal("closed hub accepted a new login")
	}
}
