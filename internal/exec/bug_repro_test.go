package exec

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/runtime"
)

// TestCancellationEmitsTurnInterrupted verifies that when the parent
// context is canceled mid-turn, the JSONL event stream ends with
// turn_interrupted (not turn_failed).
func TestCancellationEmitsTurnInterrupted(t *testing.T) {
	root := t.TempDir()
	// This test exercises a real local app-server controller, which persists
	// sessions. Keep its user-level state out of the developer's actual ~/.wuu;
	// otherwise every run leaks a "do work" session into the Desktop's
	// projectless conversation list.
	t.Setenv("HOME", root)
	t.Setenv("WUU_HOME", filepath.Join(root, ".wuu"))
	t.Setenv("CODEX_HOME", filepath.Join(root, ".codex"))
	t.Setenv("WUU_SAFE_MODE", "")
	started := make(chan struct{})
	var once sync.Once
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode model request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if request.Model != "gpt-test" {
			t.Errorf("selected provider model = %q, want gpt-test", request.Model)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n")
		w.(http.Flusher).Flush()
		once.Do(func() { close(started) })
		<-r.Context().Done()
	}))
	defer endpoint.Close()
	// Configure the selected model graph before activation; replacing the
	// legacy StreamRunner field cannot replace its captured provider.
	configPath := filepath.Join(root, ".wuu.json")
	if err := writeMinimalConfig(configPath, endpoint.URL+"/v1"); err != nil {
		t.Fatalf("write config: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cfg, loadedPath, err := loadExecConfig(root, os.Getenv("HOME"), Options{ConfigPath: configPath})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	rt, err := runtime.NewSession(runtime.Options{
		RootDir: root, HomeDir: os.Getenv("HOME"), ConfigPath: loadedPath,
		ConfigLoadMode: runtime.ConfigLoadFile, Config: cfg, NoTools: true,
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	controller := newLocalControllerForRuntime(ctx, rt)

	var stdout bytes.Buffer
	runCtx, runCancel := context.WithCancel(context.Background())
	defer runCancel()

	runDone := make(chan error, 1)
	go func() {
		runDone <- Run(runCtx, Options{
			Prompt:     "do work",
			JSON:       true,
			Stdout:     &stdout,
			Controller: controller,
		})
	}()

	// Wait for the provider to be hit (so the turn has started).
	select {
	case <-started:
	case err := <-runDone:
		t.Fatalf("Run ended before selected provider started: %v\n%s", err, stdout.String())
	case <-time.After(10 * time.Second):
		t.Fatalf("provider never started")
	}

	// Cancel the parent context.
	runCancel()

	select {
	case err := <-runDone:
		if ExitCode(err) != ExitInterrupted {
			t.Logf("Run returned err=%v (exit=%d)", err, ExitCode(err))
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("Run did not return after cancel")
	}

	// Drain events.
	events := parseJSONLines(t, stdout.String())
	// Find the last turn-related event.
	var lastTurnEvent string
	for _, ev := range events {
		switch ev["type"] {
		case "turn_failed", "turn_interrupted":
			lastTurnEvent = ev["type"].(string)
		case "result":
			lastTurnEvent = "result:" + ev["status"].(string)
		}
	}

	if lastTurnEvent == "turn_failed" {
		t.Fatalf("final event is turn_failed, expected turn_interrupted")
	}
	if lastTurnEvent != "result:interrupted" && lastTurnEvent != "turn_interrupted" {
		t.Fatalf("unexpected final turn event: %q", lastTurnEvent)
	}

}

func writeMinimalConfig(path, endpoint string) error {
	content := fmt.Sprintf(`{
		"default_provider": "test",
		"providers": {
			"test": {
				"type": "openai-compatible",
				"base_url": %q,
				"api_key": "sk-test",
				"model": "gpt-test"
			}
		},
		"agent": {"permission_mode": "standard"}
	}`, endpoint)
	return os.WriteFile(path, []byte(content), 0o600)
}
