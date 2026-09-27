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
)

// Exercise the real SDK/app-server/provider boundary: configuration events alone
// used to report the requested model even when the provider still received the
// persisted selection.
func TestRunResumedAndForkedSelectionReachesProvider(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("WUU_HOME", filepath.Join(root, "state"))
	t.Setenv("WUU_SAFE_MODE", "1")
	type request struct {
		Model  string `json:"model"`
		Effort string `json:"reasoning_effort"`
		Path   string
	}
	var mu sync.Mutex
	var requests []request
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode provider request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		req.Path = r.URL.Path
		mu.Lock()
		requests = append(requests, req)
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"done\"}}]}\n\ndata: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	configPath := filepath.Join(root, "config.json")
	config := []byte(fmt.Sprintf(`{"default_provider":"first","providers":{
		"first":{"type":"openai-compatible","base_url":%q,"api_key":"synthetic","model":"gpt-5.5"},
		"second":{"type":"openai-compatible","base_url":%q,"api_key":"synthetic","model":"gpt-5.4"}
	},"agent":{"permission_mode":"read_only","effort":"high"}}`, server.URL+"/first", server.URL+"/second"))
	if err := os.WriteFile(configPath, config, 0o600); err != nil {
		t.Fatal(err)
	}
	run := func(opts Options, want request) string {
		t.Helper()
		mu.Lock()
		requests = nil
		mu.Unlock()
		opts.Workdir, opts.ConfigPath, opts.Prompt = root, configPath, "reply briefly"
		opts.NoTools, opts.JSON, opts.Timeout = true, true, 10*time.Second
		var stdout bytes.Buffer
		opts.Stdout = &stdout
		if err := Run(context.Background(), opts); err != nil {
			t.Fatalf("Run: %v\n%s", err, &stdout)
		}
		mu.Lock()
		got := append([]request(nil), requests...)
		mu.Unlock()
		if len(got) == 0 {
			t.Fatal("provider received no request")
		}
		for _, req := range got {
			if req != want {
				t.Fatalf("provider request = %+v, want %+v", req, want)
			}
		}
		for _, event := range parseJSONLines(t, stdout.String()) {
			if event["type"] == "result" {
				return event["thread_id"].(string)
			}
		}
		t.Fatal("missing terminal result")
		return ""
	}
	initial := request{Model: "gpt-5.5", Effort: "high", Path: "/first/chat/completions"}
	id := run(Options{}, initial)
	fork := run(Options{ForkID: id, Provider: "second", Model: "gpt-5.4", Effort: "low"},
		request{Model: "gpt-5.4", Effort: "low", Path: "/second/chat/completions"})
	if fork == id {
		t.Fatal("fork reused the source session")
	}
	// A fork's overrides must not mutate the source or the config defaults.
	run(Options{ResumeID: id}, initial)
	run(Options{ResumeID: id, Model: "gpt-5.4", Variant: "low"},
		request{Model: "gpt-5.4", Effort: "low", Path: "/first/chat/completions"})
	run(Options{ResumeID: id}, request{Model: "gpt-5.4", Effort: "low", Path: "/first/chat/completions"})
	run(Options{}, initial)
	gotConfig, err := os.ReadFile(configPath)
	if err != nil || !bytes.Equal(gotConfig, config) {
		t.Fatalf("session selection changed config: err=%v", err)
	}
}
