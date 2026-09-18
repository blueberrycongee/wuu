package server

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/remote/pgtest"
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
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("web fallback"), 0600); err != nil {
		t.Fatal(err)
	}
	databaseURL := pgtest.URL(t)
	t.Setenv("WUU_DATABASE_URL", databaseURL)
	ready := startupWriter{address: make(chan string, 1)}
	done := make(chan error, 1)
	go func() {
		done <- RunStandalone(ctx, []string{"--addr", "127.0.0.1:0", "--state", filepath.Join(dir, "relay.json"), "--web-root", dir}, ready)
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
	probe, err := client.Get("http://" + address + "/readyz")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(probe.Body)
	probe.Body.Close()
	if err != nil || probe.StatusCode != http.StatusOK || string(body) != "ok" {
		t.Fatalf("readiness must reach relay with web root enabled: status=%d body=%q err=%v", probe.StatusCode, body, err)
	}
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

func TestStandaloneRequiresDatabase(t *testing.T) {
	t.Setenv("WUU_DATABASE_URL", "")
	if err := RunStandalone(context.Background(), nil, io.Discard); err == nil {
		t.Fatal("standalone service started without accounts")
	}
}
