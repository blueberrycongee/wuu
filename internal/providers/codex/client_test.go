package codex

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/authstorage"
	"github.com/blueberrycongee/wuu/internal/compact"
	"github.com/blueberrycongee/wuu/internal/providers"
)

type codexJournal struct{}

func (codexJournal) PrepareOperation(providers.InferenceOperationJournalRecord) error { return nil }
func (codexJournal) PrepareAttempt(providers.InferenceAttemptJournalRecord) error     { return nil }
func (codexJournal) UpsertSubmission(providers.InferenceSubmissionJournalRecord) error {
	return nil
}
func (codexJournal) MarkAttemptFirstEvent(string, string, string, time.Time) error { return nil }
func (codexJournal) CompleteAttempt(providers.InferenceAttemptTerminalRecord) error {
	return nil
}
func (codexJournal) PrepareRecoveryAttempt(context.Context, providers.InferenceRecoveryAttemptJournalRecord) error {
	return nil
}
func (codexJournal) CompleteOperation(providers.InferenceOperationTerminalRecord) error {
	return nil
}
func (codexJournal) CompleteWorkflow(providers.InferenceWorkflowTerminalRecord) error { return nil }

func TestClientDoesNotUseCodexCLIAuthUnlessEnabled(t *testing.T) {
	home := t.TempDir()
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	token := fakeJWT(t, time.Now().Add(time.Hour), "acct_disabled")
	writeCodexCLIAuth(t, codexHome, token, "refresh-token")

	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"unexpected"}]}]}`))
	}))
	defer server.Close()

	client, err := New(ClientConfig{BaseURL: server.URL, Home: home, HTTPClient: server.Client()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = client.Chat(context.Background(), providers.ChatRequest{
		Model:    "gpt-5-codex",
		Messages: []providers.ChatMessage{{Role: "user", Content: "hello"}},
	})
	if err == nil {
		t.Fatal("expected local Codex credentials to require reuse_codex_credentials")
	}
	if !strings.Contains(err.Error(), "reuse_codex_credentials") {
		t.Fatalf("expected error to mention reuse_codex_credentials, got %v", err)
	}
	if called {
		t.Fatal("client should not call Codex backend when local Codex credential reuse is disabled")
	}
}

func TestClientUsesCodexCLIAuthReadOnlyWhenEnabled(t *testing.T) {
	home := t.TempDir()
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	token := fakeJWT(t, time.Now().Add(time.Hour), "acct_123")
	writeCodexCLIAuth(t, codexHome, token, "refresh-token")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Fatalf("path = %q, want /responses", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+token {
			t.Fatalf("Authorization = %q", got)
		}
		if got := r.Header.Get("originator"); got != "codex_cli_rs" {
			t.Fatalf("originator = %q", got)
		}
		if got := r.Header.Get("OpenAI-Beta"); got != "responses=experimental" {
			t.Fatalf("OpenAI-Beta = %q", got)
		}
		if got := r.Header.Get("chatgpt-account-id"); got != "acct_123" {
			t.Fatalf("chatgpt-account-id = %q", got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if _, exists := body["temperature"]; exists {
			t.Fatalf("Codex request must omit unsupported temperature, got %#v", body["temperature"])
		}
		if _, exists := body["max_output_tokens"]; exists {
			t.Fatalf("Codex request must omit unsupported max_output_tokens, got %#v", body["max_output_tokens"])
		}
		if body["instructions"] == "" {
			t.Fatalf("Codex request must include instructions: %#v", body)
		}
		if body["store"] != false {
			t.Fatalf("Codex request must set store=false, got %#v", body["store"])
		}
		include, _ := body["include"].([]any)
		if len(include) != 1 || include[0] != "reasoning.encrypted_content" {
			t.Fatalf("Codex request must include reasoning.encrypted_content, got %#v", body["include"])
		}
		if pt, _ := body["parallel_tool_calls"].(bool); !pt {
			t.Fatalf("Codex request must enable parallel_tool_calls, got %#v", body["parallel_tool_calls"])
		}
		text, _ := body["text"].(map[string]any)
		if text["verbosity"] != "low" {
			t.Fatalf("Codex request must default text.verbosity to low, got %#v", text["verbosity"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}`))
	}))
	defer server.Close()

	client, err := New(ClientConfig{BaseURL: server.URL, Home: home, HTTPClient: server.Client(), ReuseCodexCredentials: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := providers.WithInferenceJournal(context.Background(), codexJournal{})
	resp, err := providers.ExecuteChat(ctx, client, providers.ChatRequest{
		Model:       "gpt-5-codex",
		Temperature: 0.2,
		MaxTokens:   321,
		ProviderOptions: map[string]any{
			"maxOutputTokens": 999,
		},
		Messages: []providers.ChatMessage{
			{Role: "system", Content: "sys"},
			{Role: "user", Content: "hello"},
		},
	}, providers.InferenceOperationAgentRound, providers.InferenceProfileInteractive)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Content != "ok" {
		t.Fatalf("content = %q", resp.Content)
	}
	store, err := authstorage.ForHome(home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get("openai-codex"); err == nil {
		t.Fatal("Codex CLI read-only fallback should not write wuu auth state")
	}
}

func TestClientStreamNormalizesRequestBeforeJournalHash(t *testing.T) {
	token := fakeJWT(t, time.Now().Add(time.Hour), "acct_stream_journal")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: response.output_text.delta\n" +
			"data: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\n" +
			"event: response.completed\n" +
			"data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n"))
	}))
	defer server.Close()

	client, err := New(ClientConfig{
		BaseURL:         server.URL,
		APIKey:          token,
		HTTPClient:      server.Client(),
		StreamTransport: providers.StreamTransportSSE,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := providers.WithInferenceJournal(context.Background(), codexJournal{})
	req, err := providers.EnsureInferenceExecutionContext(ctx, providers.ChatRequest{
		Model:       "gpt-5-codex",
		Temperature: 0.7,
		MaxTokens:   123,
		ProviderOptions: map[string]any{
			"maxOutputTokens": 456,
		},
		Messages: []providers.ChatMessage{{Role: "user", Content: "hello"}},
	}, providers.InferenceOperationTitle, providers.InferenceProfileBestEffort)
	if err != nil {
		t.Fatal(err)
	}
	reliable := providers.NewReliableStreamClient(client, nil)
	events, err := reliable.StreamChat(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	var content string
	var done bool
	for event := range events {
		if event.Type == providers.EventError {
			t.Fatalf("stream error: %v", event.Error)
		}
		if event.Type == providers.EventContentDelta {
			content += event.Content
		}
		if event.Type == providers.EventDone {
			done = true
		}
	}
	if content != "ok" || !done {
		t.Fatalf("stream content/done = %q/%t", content, done)
	}
}

func TestOAuthSourceExplicitAPIKey(t *testing.T) {
	token := fakeJWT(t, time.Now().Add(time.Hour), "acct_explicit")
	source := NewOAuthSource(OAuthConfig{APIKey: token})

	creds, err := source.Credentials(context.Background(), false)
	if err != nil {
		t.Fatalf("Credentials: %v", err)
	}
	if creds.accessToken != token {
		t.Fatalf("accessToken = %q, want explicit token", creds.accessToken)
	}
	if creds.accountID != "acct_explicit" {
		t.Fatalf("accountID = %q, want acct_explicit", creds.accountID)
	}
	if creds.source != "explicit" {
		t.Fatalf("source = %q, want explicit", creds.source)
	}
}

func TestLocalOAuthStatusUsesCodexCLIAuth(t *testing.T) {
	home := t.TempDir()
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	token := fakeJWT(t, time.Now().Add(time.Hour), "acct_status")
	writeCodexCLIAuth(t, codexHome, token, "refresh-token")

	source, err := LocalOAuthStatus(home)
	if err != nil {
		t.Fatalf("LocalOAuthStatus: %v", err)
	}
	if source != "codex-cli" {
		t.Fatalf("source = %q, want codex-cli", source)
	}
}

func TestLocalOAuthStatusUsesRefreshableWuuAuth(t *testing.T) {
	home := t.TempDir()
	stale := fakeJWT(t, time.Now().Add(-time.Hour), "acct_status_wuu")
	store, err := authstorage.ForHome(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set("openai-codex", authstorage.Credentials{Type: "oauth", AccessToken: stale, RefreshToken: "refresh-token"}); err != nil {
		t.Fatalf("SaveCodexOAuth: %v", err)
	}

	source, err := LocalOAuthStatus(home)
	if err != nil {
		t.Fatalf("LocalOAuthStatus: %v", err)
	}
	if source != "wuu-auth-store" {
		t.Fatalf("source = %q, want wuu-auth-store", source)
	}
}

func TestLocalOAuthStatusReportsMissingCredentials(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex-missing"))

	_, err := LocalOAuthStatus(home)
	if err == nil {
		t.Fatal("expected missing credentials error")
	}
	if !strings.Contains(err.Error(), "Codex CLI OAuth credentials not found") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestClientRefreshesStoredWuuCodexOAuth(t *testing.T) {
	home := t.TempDir()
	stale := fakeJWT(t, time.Now().Add(-time.Hour), "acct_old")
	fresh := fakeJWT(t, time.Now().Add(time.Hour), "acct_new")
	store, err := authstorage.ForHome(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set("openai-codex", authstorage.Credentials{Type: "oauth", AccessToken: stale, RefreshToken: "refresh-old", AuthMode: "chatgpt", Source: "test"}); err != nil {
		t.Fatalf("SaveCodexOAuth: %v", err)
	}

	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s", r.Method)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm: %v", err)
		}
		if got := r.Form.Get("grant_type"); got != "refresh_token" {
			t.Fatalf("grant_type = %q", got)
		}
		if got := r.Form.Get("refresh_token"); got != "refresh-old" {
			t.Fatalf("refresh_token = %q", got)
		}
		if got := r.Form.Get("client_id"); got != clientID {
			t.Fatalf("client_id = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":` + jsonString(fresh) + `,"refresh_token":"refresh-new"}`))
	}))
	defer tokenServer.Close()
	t.Setenv("CODEX_REFRESH_TOKEN_URL_OVERRIDE", tokenServer.URL)

	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+fresh {
			t.Fatalf("Authorization = %q", got)
		}
		if got := r.Header.Get("OpenAI-Beta"); got != "responses=experimental" {
			t.Fatalf("OpenAI-Beta = %q", got)
		}
		if got := r.Header.Get("chatgpt-account-id"); got != "acct_new" {
			t.Fatalf("chatgpt-account-id = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"refreshed"}]}]}`))
	}))
	defer apiServer.Close()

	client, err := New(ClientConfig{BaseURL: apiServer.URL, Home: home, HTTPClient: apiServer.Client()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	resp, err := client.Chat(context.Background(), providers.ChatRequest{
		Model:    "gpt-5-codex",
		Messages: []providers.ChatMessage{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Content != "refreshed" {
		t.Fatalf("content = %q", resp.Content)
	}
	state, err := store.Get("openai-codex")
	if err != nil {
		t.Fatalf("LoadCodexOAuth: %v", err)
	}
	if state.AccessToken != fresh || state.RefreshToken != "refresh-new" {
		t.Fatalf("unexpected stored tokens: %#v", state)
	}
}

func TestClientRecordsAuthRefreshReplayAsSecondSubmission(t *testing.T) {
	home := t.TempDir()
	stale := fakeJWT(t, time.Now().Add(time.Hour), "acct_old")
	fresh := fakeJWT(t, time.Now().Add(2*time.Hour), "acct_new")
	store, err := authstorage.ForHome(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set("openai-codex", authstorage.Credentials{
		Type: "oauth", AccessToken: stale, RefreshToken: "refresh-old", AuthMode: "chatgpt", Source: "test",
	}); err != nil {
		t.Fatalf("store credentials: %v", err)
	}

	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm: %v", err)
		}
		if got := r.Form.Get("refresh_token"); got != "refresh-old" {
			t.Fatalf("refresh_token = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":` + jsonString(fresh) + `,"refresh_token":"refresh-new"}`))
	}))
	defer tokenServer.Close()
	t.Setenv("CODEX_REFRESH_TOKEN_URL_OVERRIDE", tokenServer.URL)

	var apiCalls atomic.Int32
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := apiCalls.Add(1)
		if call == 1 {
			if got := r.Header.Get("Authorization"); got != "Bearer "+stale {
				t.Fatalf("first Authorization = %q", got)
			}
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"message":"expired token"}}`))
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+fresh {
			t.Fatalf("refreshed Authorization = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"recovered"}]}]}`))
	}))
	defer apiServer.Close()

	client, err := New(ClientConfig{BaseURL: apiServer.URL, Home: home, HTTPClient: apiServer.Client()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	req, err := providers.EnsureInferenceExecutionContext(context.Background(), providers.ChatRequest{
		Model: "gpt-5-codex", Messages: []providers.ChatMessage{{Role: "user", Content: "hello"}},
	}, providers.InferenceOperationAgentRound, providers.InferenceProfileInteractive)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := providers.ExecuteChat(context.Background(), client, req, providers.InferenceOperationAgentRound, providers.InferenceProfileInteractive)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Content != "recovered" || apiCalls.Load() != 2 {
		t.Fatalf("response/calls = %q/%d", resp.Content, apiCalls.Load())
	}
	ledger := req.Execution.Snapshot()
	if ledger.Attempts != 2 || len(ledger.Submissions) != 2 {
		t.Fatalf("inference ledger = %+v, want two attempts and two auth submissions", ledger)
	}
}

func TestClientModelsUsesCodexOAuth(t *testing.T) {
	home := t.TempDir()
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	token := fakeJWT(t, time.Now().Add(time.Hour), "acct_models")
	writeCodexCLIAuth(t, codexHome, token, "refresh-token")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			t.Fatalf("path = %q, want /models", r.URL.Path)
		}
		if got := r.URL.Query().Get("client_version"); got != "1.0.0" {
			t.Fatalf("client_version = %q", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+token {
			t.Fatalf("Authorization = %q", got)
		}
		if got := r.Header.Get("originator"); got != "codex_cli_rs" {
			t.Fatalf("originator = %q", got)
		}
		if got := r.Header.Get("OpenAI-Beta"); got != "responses=experimental" {
			t.Fatalf("OpenAI-Beta = %q", got)
		}
		if got := r.Header.Get("chatgpt-account-id"); got != "acct_models" {
			t.Fatalf("chatgpt-account-id = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
		  "models": [
		    {"slug":"hidden-model","visibility":"hide","priority":1},
		    {"slug":"gpt-5.4","display_name":"GPT-5.4","priority":20,"supported_in_api":true},
		    {"slug":"gpt-5.5","display_name":"GPT-5.5","priority":9,"default_reasoning_level":"medium","supported_reasoning_levels":[{"effort":"low"},{"effort":"medium"}],"supported_in_api":true}
		  ]
		}`))
	}))
	defer server.Close()

	client, err := New(ClientConfig{BaseURL: server.URL, Home: home, HTTPClient: server.Client(), ReuseCodexCredentials: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	models, err := client.Models(context.Background())
	if err != nil {
		t.Fatalf("Models: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("models len = %d, want 2: %#v", len(models), models)
	}
	if models[0].Slug != "gpt-5.5" || models[1].Slug != "gpt-5.4" {
		t.Fatalf("unexpected model order: %#v", models)
	}
	if got := models[0].SupportedReasoning; len(got) != 2 || got[0] != "low" || got[1] != "medium" {
		t.Fatalf("unexpected reasoning levels: %#v", got)
	}
}

func TestCompactWithCodexClientUsesNormalResponsesEndpoint(t *testing.T) {
	home := t.TempDir()
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	token := fakeJWT(t, time.Now().Add(time.Hour), "acct_compact")
	writeCodexCLIAuth(t, codexHome, token, "refresh-token")

	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.URL.Path == "/responses/compact" {
			t.Fatalf("wuu compact must not use Codex remote compact endpoint")
		}
		if r.URL.Path != "/responses" {
			t.Fatalf("path = %q, want /responses", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+token {
			t.Fatalf("Authorization = %q", got)
		}

		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if _, exists := body["tools"]; exists {
			t.Fatalf("compact summary request must not include tools: %#v", body["tools"])
		}
		if _, exists := body["max_output_tokens"]; exists {
			t.Fatalf("compact summary request must omit unsupported max_output_tokens: %#v", body["max_output_tokens"])
		}
		input, ok := body["input"].([]any)
		if !ok || len(input) != 1 {
			t.Fatalf("expected one normal Responses input item, got %#v", body["input"])
		}

		if stream, _ := body["stream"].(bool); stream {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("event: response.output_text.delta\n"))
			_, _ = w.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"<analysis>draft</analysis><summary>summary via normal responses</summary>\"}\n\n"))
			_, _ = w.Write([]byte("event: response.completed\n"))
			_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[]}}\n\n"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"<analysis>draft</analysis><summary>summary via normal responses</summary>"}]}]}`))
	}))
	defer server.Close()

	client, err := New(ClientConfig{BaseURL: server.URL, Home: home, HTTPClient: server.Client(), ReuseCodexCredentials: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	messages := []providers.ChatMessage{
		{Role: "user", Content: "first"},
		{Role: "assistant", Content: "first reply"},
		{Role: "user", Content: "second"},
		{Role: "assistant", Content: "second reply"},
		{Role: "user", Content: "third"},
		{Role: "assistant", Content: "third reply"},
		{Role: "user", Content: "fourth"},
		{Role: "assistant", Content: "fourth reply"},
	}

	result, err := compact.Compact(context.Background(), messages, client, "gpt-5-codex")
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if len(paths) != 1 || paths[0] != "/responses" {
		t.Fatalf("unexpected request paths: %#v", paths)
	}
	if len(result) == 0 || result[0].Role != "system" || !compact.IsConversationSummaryContent(result[0].Content) {
		t.Fatalf("expected compacted summary at history root, got %#v", result)
	}
	if !strings.Contains(result[0].Content, "summary via normal responses") {
		t.Fatalf("expected cleaned summary from normal Responses call, got %q", result[0].Content)
	}
}

func TestNativeCompactionPreparesInferenceAttemptBeforeSubmission(t *testing.T) {
	token := fakeJWT(t, time.Now().Add(time.Hour), "acct_native_compact")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Fatalf("path = %q, want /responses", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		input, ok := body["input"].([]any)
		if !ok || len(input) == 0 {
			t.Fatalf("missing native compact input: %#v", body["input"])
		}
		trigger, _ := input[len(input)-1].(map[string]any)
		if trigger["type"] != "compaction_trigger" {
			t.Fatalf("last input = %#v, want compaction_trigger", trigger)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: response.output_item.done\n" +
			"data: {\"type\":\"response.output_item.done\",\"item\":{\"id\":\"cmp_1\",\"type\":\"compaction\",\"status\":\"completed\",\"encrypted_content\":\"opaque\"},\"output_index\":0}\n\n" +
			"event: response.completed\n" +
			"data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":8,\"output_tokens\":0}}}\n\n"))
	}))
	defer server.Close()

	client, err := New(ClientConfig{
		BaseURL:         server.URL,
		APIKey:          token,
		HTTPClient:      server.Client(),
		StreamTransport: providers.StreamTransportSSE,
	})
	if err != nil {
		t.Fatal(err)
	}
	messages := []providers.ChatMessage{
		{Role: "user", Content: "first"},
		{Role: "assistant", Content: "first reply"},
		{Role: "user", Content: "second"},
		{Role: "assistant", Content: "second reply"},
	}
	result, err := compact.CompactWithNativeOrSummary(
		providers.WithInferenceJournal(context.Background(), codexJournal{}),
		messages,
		client,
		"gpt-5-codex",
		compact.Budget{ContextTokens: 100_000, KeepRecentTokens: 1},
		compact.NativeOptions{Provider: "openai-codex"},
	)
	if err != nil {
		t.Fatalf("native compact: %v", err)
	}
	if len(result) != 1 || len(result[0].ProviderItems) != 1 || result[0].ProviderItems[0].Type != "compaction" {
		t.Fatalf("unexpected native replacement: %#v", result)
	}
}

func TestCodexRequestAppliesDefaultsButAllowsOverride(t *testing.T) {
	// Empty ProviderOptions: defaults are filled in.
	out := codexRequest(providers.ChatRequest{})
	if out.Temperature != 0 {
		t.Fatalf("temperature = %v, want 0", out.Temperature)
	}
	include, ok := out.ProviderOptions["include"].([]string)
	if !ok || len(include) != 1 || include[0] != "reasoning.encrypted_content" {
		t.Fatalf("include default = %#v", out.ProviderOptions["include"])
	}
	if pt, _ := out.ProviderOptions["parallelToolCalls"].(bool); !pt {
		t.Fatalf("parallelToolCalls default = %#v", out.ProviderOptions["parallelToolCalls"])
	}
	if out.ProviderOptions["textVerbosity"] != "low" {
		t.Fatalf("textVerbosity default = %#v", out.ProviderOptions["textVerbosity"])
	}

	// User-provided values must be preserved, not overwritten.
	custom := codexRequest(providers.ChatRequest{
		ProviderOptions: map[string]any{
			"include":           []string{"reasoning.text"},
			"parallelToolCalls": false,
			"textVerbosity":     "high",
		},
	})
	customInclude, _ := custom.ProviderOptions["include"].([]string)
	if len(customInclude) != 1 || customInclude[0] != "reasoning.text" {
		t.Fatalf("include override lost: %#v", custom.ProviderOptions["include"])
	}
	if pt, _ := custom.ProviderOptions["parallelToolCalls"].(bool); pt {
		t.Fatalf("parallelToolCalls override lost: %#v", custom.ProviderOptions["parallelToolCalls"])
	}
	if custom.ProviderOptions["textVerbosity"] != "high" {
		t.Fatalf("textVerbosity override lost: %#v", custom.ProviderOptions["textVerbosity"])
	}

	options := map[string]any{
		"maxOutputTokens":       999,
		"temperatureSupported":  false,
		"temperature_supported": false,
	}
	capped := codexRequest(providers.ChatRequest{MaxTokens: 123, ProviderOptions: options})
	if capped.MaxTokens != 0 {
		t.Fatalf("Codex request must clear MaxTokens, got %d", capped.MaxTokens)
	}
	if _, ok := capped.ProviderOptions["maxOutputTokens"]; ok {
		t.Fatalf("Codex request must strip maxOutputTokens option: %#v", capped.ProviderOptions)
	}
	if _, ok := capped.ProviderOptions["temperatureSupported"]; ok {
		t.Fatalf("Codex request must strip temperatureSupported option: %#v", capped.ProviderOptions)
	}
	if _, ok := capped.ProviderOptions["temperature_supported"]; ok {
		t.Fatalf("Codex request must strip temperature_supported option: %#v", capped.ProviderOptions)
	}
	if _, ok := options["maxOutputTokens"]; !ok {
		t.Fatalf("codexRequest should not mutate caller provider options")
	}
	if _, ok := options["temperatureSupported"]; !ok {
		t.Fatalf("codexRequest should not mutate caller provider options")
	}
}

func writeCodexCLIAuth(t *testing.T, codexHome, accessToken, refreshToken string) {
	t.Helper()
	payload := map[string]any{
		"auth_mode": "chatgpt",
		"tokens": map[string]string{
			"access_token":  accessToken,
			"refresh_token": refreshToken,
		},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(codexHome, "auth.json"), data, 0o600); err != nil {
		t.Fatalf("write auth.json: %v", err)
	}
}

func fakeJWT(t *testing.T, exp time.Time, accountID string) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	claims := map[string]any{
		"exp": exp.Unix(),
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": accountID,
		},
	}
	data, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("Marshal claims: %v", err)
	}
	payload := base64.RawURLEncoding.EncodeToString(data)
	return header + "." + payload + ".sig"
}

func jsonString(value string) string {
	data, _ := json.Marshal(value)
	return string(data)
}

func TestCodexProviderScopeSurvivesTokenRefresh(t *testing.T) {
	first := codexProviderScope("https://chatgpt.com/backend-api/codex", credentials{
		accessToken: "old-secret-token",
		accountID:   "account-1",
	})
	refreshed := codexProviderScope("https://chatgpt.com/backend-api/codex", credentials{
		accessToken: "new-secret-token",
		accountID:   "account-1",
	})
	other := codexProviderScope("https://chatgpt.com/backend-api/codex", credentials{
		accessToken: "other-secret-token",
		accountID:   "account-2",
	})
	if first != refreshed || first == other {
		t.Fatalf("scopes = %q / %q / %q", first, refreshed, other)
	}
	for _, secret := range []string{"old-secret-token", "new-secret-token", "account-1"} {
		if strings.Contains(string(first), secret) {
			t.Fatalf("scope leaked %q: %q", secret, first)
		}
	}
}
