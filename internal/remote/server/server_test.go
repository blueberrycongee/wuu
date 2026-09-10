package server

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type startupWriter struct{ address chan string }

func (w startupWriter) Write(p []byte) (int, error) {
	line := string(p)
	if strings.HasPrefix(line, "wuu relay listening on ") {
		w.address <- strings.Fields(strings.TrimPrefix(line, "wuu relay listening on "))[0]
	}
	return len(p), nil
}

func TestRunStartsAccountServiceAndStopsOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dir := t.TempDir()
	ready := startupWriter{address: make(chan string, 1)}
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, []string{"--addr", "127.0.0.1:0", "--accounts", filepath.Join(dir, "accounts.db"), "--state", filepath.Join(dir, "relay.json")}, ready)
	}()
	var address string
	select {
	case address = <-ready.address:
	case err := <-done:
		t.Fatalf("startup failed: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("service did not start")
	}
	client := &http.Client{Timeout: 3 * time.Second}
	response, err := client.Get(fmt.Sprintf("http://%s/v1/account/config", address))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("account service status = %d", response.StatusCode)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("service did not stop")
	}
	if response, err := client.Get("http://" + address + "/healthz"); err == nil {
		response.Body.Close()
		t.Fatal("listener survived service shutdown")
	}
}
