package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/credentialstore"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/permissionpolicy"
	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/pluginsettings"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
)

// The logical result can precede a producer's final receipt or cleanup error.
// This source controls that boundary; documents and generation ownership are real.
type nonturnReceiptClient struct {
	started       chan providers.ChatRequest
	release       chan struct{}
	finished      chan struct{}
	completionErr error
}

func (c *nonturnReceiptClient) start(ctx context.Context, req providers.ChatRequest) error {
	_, complete, err := providers.BeginModelReceipt(ctx)
	if err != nil {
		return err
	}
	lease := &providers.ProviderLease{}
	if _, err := lease.RecordSubmission(req.Attempt, providers.InferenceSubmissionMeta{Provider: "fixture", Protocol: "controlled", Transport: "memory", Mode: "stream"}); err != nil {
		complete(nil)
		return err
	}
	c.started <- req
	go func() {
		defer close(c.finished)
		defer complete(c.completionErr)
		<-c.release
		lease.SucceedWithUsage(&providers.TokenUsage{InputTokens: 13, OutputTokens: 5})
	}()
	if c.completionErr != nil {
		<-c.finished
	}
	return nil
}

func (c *nonturnReceiptClient) Chat(ctx context.Context, req providers.ChatRequest) (providers.ChatResponse, error) {
	if err := c.start(ctx, req); err != nil {
		return providers.ChatResponse{}, err
	}
	return providers.ChatResponse{Content: "Captured result"}, nil
}

func (c *nonturnReceiptClient) StreamChat(ctx context.Context, req providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	if err := c.start(ctx, req); err != nil {
		return nil, err
	}
	events := make(chan providers.StreamEvent, 2)
	events <- providers.StreamEvent{Type: providers.EventContentDelta, Content: "Captured result"}
	events <- providers.StreamEvent{Type: providers.EventDone}
	close(events)
	return events, nil
}

type nonturnReceiptDocuments struct {
	*persistence.Service
	closed chan struct{}
	once   sync.Once
}

func (d *nonturnReceiptDocuments) Close(ctx context.Context) error {
	err := d.Service.Close(ctx)
	d.once.Do(func() { close(d.closed) })
	return err
}

func newNonturnReceiptRuntime(t *testing.T, client providers.StreamClient) (*runtime.Session, string, <-chan struct{}) {
	t.Helper()
	root, home, documentsDir := t.TempDir(), t.TempDir(), t.TempDir()
	const documentsID = "fixture.nonturn-documents"
	documents := &nonturnReceiptDocuments{Service: persistence.NewService(documentsID, documentsDir, "jsonl"), closed: make(chan struct{})}
	modules := []string{"wuu.session-state", documentsID, "kernel", loopdriver.DefaultDriverID, "wuu.session-execution", "wuu.tool-execution", runtime.StateServicesConsumer, credentialstore.FileModule, pluginsettings.Module, config.Module, permissionpolicy.Module, runtime.ArtifactModule, runtime.DataQueryModule, "wuu.session.default", "wuu.tools.default", "wuu.prompt.default", "wuu.compaction.default", "wuu.title.default"}
	cfg := config.Default()
	cfg.Composition = &composition.Config{Version: composition.Version, Modules: &modules}
	cfg.DefaultProvider = "fixture"
	cfg.Providers = map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: "https://example.test/v1", APIKey: "fixture", Model: "fixture-model"}}
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, "config.json")
	if err := os.WriteFile(configPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	rt, err := runtime.NewSession(runtime.Options{RootDir: root, HomeDir: home, ConfigPath: configPath, Config: cfg, NoTools: true,
		NativeProviders: []runtime.NativeProviderFactory{{ID: documentsID, Build: func(runtime.NativeProviderContext) (pluginhost.Client, error) {
			return documents, nil
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = rt.Cleanup() })
	rt.StreamRunner.Client, rt.TitleClient = client, client
	return rt, documentsDir, documents.closed
}

func TestNonturnReceiptFailurePreventsDelivery(t *testing.T) {
	for _, entry := range []string{"title", "commit-message", "text-polish"} {
		t.Run(entry, func(t *testing.T) {
			completionErr := errors.New("receipt cleanup failed")
			client := &nonturnReceiptClient{started: make(chan providers.ChatRequest, 1), release: make(chan struct{}), finished: make(chan struct{}), completionErr: completionErr}
			close(client.release)
			rt, _, _ := newNonturnReceiptRuntime(t, client)
			out := &lockedBuffer{}
			srv := New(rt, out)
			defer srv.Close()
			if entry == "title" {
				binding := testRuntimeState(t, rt)
				const threadID = "receipt-failure-title"
				if _, err := binding.Sessions.CreateWithMetadata(threadID, rt.RootDir); err != nil {
					t.Fatal(err)
				}
				result, err := srv.generateThreadTitleCore(binding, threadID, []providers.ChatMessage{{Role: "user", Content: "Generate a title"}}, true, true, true, nil)
				if !errors.Is(err, completionErr) || result.Persisted || result.Notified {
					t.Fatalf("receipt failure was delivered as title success: result=%+v err=%v", result, err)
				}
				metadata, found, err := binding.Sessions.Find(threadID)
				if err != nil || !found || metadata.Title != "" {
					t.Fatalf("failed completion persisted a title: %+v, %v", metadata, err)
				}
				return
			}
			request := Request{ID: mustJSON(entry)}
			var err error
			if entry == "commit-message" {
				request.Params = mustJSON(GitCommitMessageParams{Diff: "+change"})
				err = srv.handleGitCommitMessage(request)
			} else {
				request.Params = mustJSON(TextPolishParams{Text: "Polish this"})
				err = srv.handleTextPolish(request)
			}
			if err != nil {
				t.Fatal(err)
			}
			response := responseByID(t, parseOutput(t, out.String()), entry)
			if response["error"] == nil || !strings.Contains(toString(response["error"]), completionErr.Error()) || response["result"] != nil {
				t.Fatalf("receipt failure was delivered as RPC success: %+v", response)
			}
		})
	}
}

func TestTitleReceiptTimeoutRetainsSelectedDocuments(t *testing.T) {
	client := &nonturnReceiptClient{started: make(chan providers.ChatRequest, 1), release: make(chan struct{}), finished: make(chan struct{})}
	var release sync.Once
	rt, documentsDir, documentsClosed := newNonturnReceiptRuntime(t, client)
	t.Cleanup(func() { release.Do(func() { close(client.release) }) })
	srv := New(rt, &lockedBuffer{})
	defer srv.Close()
	binding, err := rt.RetainState()
	if err != nil {
		t.Fatal(err)
	}
	defer binding.Close()
	const threadID = "receipt-timeout-title"
	if _, err := binding.Sessions.CreateWithMetadata(threadID, rt.RootDir); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := srv.generateThreadTitleCore(binding, threadID, []providers.ChatMessage{{Role: "user", Content: "Keep the original receipt"}}, true, false, true, nil)
		done <- err
	}()
	var request providers.ChatRequest
	select {
	case request = <-client.started:
	case <-time.After(10 * time.Second):
		t.Fatal("title did not start its model request")
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("held receipt did not report incomplete completion: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("title completion did not honor the existing receipt budget")
	}
	metadata, _, err := binding.Sessions.Find(threadID)
	if err != nil || metadata.Title != "" {
		t.Errorf("incomplete receipt published a title: %+v, %v", metadata, err)
	}
	documents := binding.Documents
	if err := binding.Close(); err != nil {
		t.Fatal(err)
	}
	// Release every external authority holder while the producer is still held.
	srv.Close()
	_, _ = rt.Cleanup()
	if _, err := documents.Read(context.Background(), persistence.Query{Namespace: "wuu.inference_submissions.v1"}); err != nil {
		t.Errorf("timed-out completion released the selected documents before its producer: %v", err)
	}
	release.Do(func() { close(client.release) })
	select {
	case <-client.finished:
	case <-time.After(5 * time.Second):
		t.Fatal("receipt producer did not finish")
	}
	// Cleanup after timeout runs asynchronously; observing the closed provider
	// establishes that its journal's final drain has completed before inspection.
	select {
	case <-documentsClosed:
	case <-time.After(5 * time.Second):
		t.Fatal("finished receipt retained its selected documents")
	}
	_, snapshot, err := persistence.Inspect(context.Background(), documentsDir)
	if err != nil {
		t.Fatal(err)
	}
	submissionID := request.Execution.Snapshot().Submissions[0].ID
	count := 0
	for _, document := range snapshot.Documents {
		if document.Namespace != "wuu.inference_submissions.v1" {
			continue
		}
		count++
		var receipt providers.InferenceSubmissionJournalRecord
		if err := json.Unmarshal(document.Value, &receipt); err != nil {
			t.Fatal(err)
		}
		if receipt.ID != submissionID || receipt.Outcome != providers.InferenceSubmissionSucceeded || receipt.ReportedUsage == nil || receipt.ReportedUsage.InputTokens != 13 || receipt.ReportedUsage.OutputTokens != 5 {
			t.Errorf("late usage did not remain on the original receipt: %+v", receipt)
		}
	}
	if count != 1 {
		t.Fatalf("physical receipts = %d, want exactly one", count)
	}
}

func TestRecommendedTitleTemperature_AlignsWithOpenCode(t *testing.T) {
	t.Parallel()
	cases := []struct {
		model     string
		wantValue float64
		wantSend  bool
	}{
		// Kimi K2.x thinking + numbered variants all use fixed 1.0 (matches
		// platform.kimi.ai docs for kimi-k2.6 and opencode's transform.ts).
		{"kimi-k2.8", 1.0, true},
		{"kimi-k2.8-preview", 1.0, true},
		{"kimi-k2.6", 1.0, true},
		{"kimi-k2.5", 1.0, true},
		{"kimi-k2-thinking", 1.0, true},
		{"kimi-k2p5", 1.0, true},
		{"kimi-k2-5-preview", 1.0, true},
		// Vanilla kimi-k2 uses 0.6.
		{"kimi-k2", 0.6, true},
		{"moonshotai/kimi-k2", 0.6, true},
		// Claude omits temperature entirely.
		{"claude-3-5-sonnet-20241022", 0, false},
		{"claude-haiku-4-5", 0, false},
		// Qwen, Gemini, GLM follow opencode's per-family mapping.
		{"qwen3-coder-30b", 0.55, true},
		{"gemini-2.5-pro", 1.0, true},
		{"glm-4.6", 1.0, true},
		{"glm-4.7-air", 1.0, true},
		// Unknown providers/models do not assume a temperature.
		{"gpt-5", 0, false},
		{"", 0, false},
	}
	for _, c := range cases {
		got, send := recommendedTitleTemperature(c.model)
		if send != c.wantSend || got != c.wantValue {
			t.Errorf("recommendedTitleTemperature(%q) = (%v, %v); want (%v, %v)", c.model, got, send, c.wantValue, c.wantSend)
		}
	}
}

// scriptedStreamClient emits a pre-recorded sequence of stream events for
// title-generation tests so we can exercise the streaming aggregator without
// any real provider.
type scriptedStreamClient struct {
	mu       sync.Mutex
	requests []providers.ChatRequest
	chunks   []string
	prefix   string // optional <think>…</think> prefix
}

func (c *scriptedStreamClient) Chat(_ context.Context, req providers.ChatRequest) (providers.ChatResponse, error) {
	c.mu.Lock()
	c.requests = append(c.requests, req)
	c.mu.Unlock()
	return providers.ChatResponse{Content: strings.Join(c.chunks, "")}, nil
}

func (c *scriptedStreamClient) StreamChat(_ context.Context, req providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	c.mu.Lock()
	c.requests = append(c.requests, req)
	chunks := append([]string(nil), c.chunks...)
	prefix := c.prefix
	c.mu.Unlock()

	ch := make(chan providers.StreamEvent, len(chunks)+3)
	go func() {
		defer close(ch)
		if prefix != "" {
			ch <- providers.StreamEvent{Type: providers.EventThinkingDelta, Content: prefix}
			ch <- providers.StreamEvent{Type: providers.EventThinkingDone}
		}
		for _, chunk := range chunks {
			ch <- providers.StreamEvent{Type: providers.EventContentDelta, Content: chunk}
		}
		ch <- providers.StreamEvent{Type: providers.EventDone}
	}()
	return ch, nil
}

func TestStreamTitleText_AggregatesContentDeltasAndSkipsThinking(t *testing.T) {
	t.Parallel()
	client := &scriptedStreamClient{
		prefix: "thinking out loud about the title",
		chunks: []string{"Fix ", "login ", "crash"},
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	got, err := streamTitleText(ctx, client, providers.ChatRequest{Model: "kimi-k2.6"})
	if err != nil {
		t.Fatalf("streamTitleText: %v", err)
	}
	if got != "Fix login crash" {
		t.Fatalf("aggregated title = %q; want %q", got, "Fix login crash")
	}
}

func TestStreamTitleText_HonorsContentReplaceAndMessageEvents(t *testing.T) {
	t.Parallel()
	ch := make(chan providers.StreamEvent, 5)
	ch <- providers.StreamEvent{Type: providers.EventContentDelta, Content: "draft"}
	ch <- providers.StreamEvent{Type: providers.EventContentReplace, Content: "Final title"}
	ch <- providers.StreamEvent{Type: providers.EventDone}
	close(ch)
	got, err := streamTitleText(context.Background(), staticStreamClient{events: ch}, providers.ChatRequest{})
	if err != nil {
		t.Fatalf("streamTitleText: %v", err)
	}
	if got != "Final title" {
		t.Fatalf("got %q; want %q", got, "Final title")
	}
}

func TestStreamTitleText_PropagatesStreamError(t *testing.T) {
	t.Parallel()
	ch := make(chan providers.StreamEvent, 2)
	ch <- providers.StreamEvent{Type: providers.EventError, Error: context.Canceled}
	close(ch)
	_, err := streamTitleText(context.Background(), staticStreamClient{events: ch}, providers.ChatRequest{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context canceled", err)
	}
}

type retryingTitleStreamClient struct {
	mu         sync.Mutex
	calls      int
	operations []providers.InferenceOperation
}

func (c *retryingTitleStreamClient) Chat(_ context.Context, _ providers.ChatRequest) (providers.ChatResponse, error) {
	return providers.ChatResponse{}, nil
}

func (c *retryingTitleStreamClient) StreamChat(_ context.Context, req providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	c.mu.Lock()
	c.calls++
	call := c.calls
	c.operations = append(c.operations, req.Operation)
	c.mu.Unlock()

	ch := make(chan providers.StreamEvent, 3)
	if call == 1 {
		ch <- providers.StreamEvent{Type: providers.EventContentDelta, Content: "stale draft"}
		ch <- providers.StreamEvent{Type: providers.EventError, Error: &providers.HTTPError{
			StatusCode: 500,
			Body:       "temporary upstream failure",
			RetryAfter: time.Nanosecond,
		}}
	} else {
		ch <- providers.StreamEvent{Type: providers.EventContentDelta, Content: "Final title"}
		ch <- providers.StreamEvent{Type: providers.EventDone}
	}
	close(ch)
	return ch, nil
}

func TestStreamTitleText_RetrySupersedesPartialAttempt(t *testing.T) {
	client := &retryingTitleStreamClient{}
	got, deltas, err := streamTitleTextWithDeltas(context.Background(), client, providers.ChatRequest{Model: "test"})
	if err != nil {
		t.Fatalf("streamTitleTextWithDeltas: %v", err)
	}
	if got != "Final title" {
		t.Fatalf("title = %q, want only recovered attempt", got)
	}
	if joined := strings.Join(deltas, "|"); joined != "stale draft|[attempt-reset]|Final title" {
		t.Fatalf("deltas = %q", joined)
	}
	if client.calls != 2 || len(client.operations) != 2 {
		t.Fatalf("calls/operations = %d/%d, want 2/2", client.calls, len(client.operations))
	}
	first, second := client.operations[0], client.operations[1]
	if first.ID == "" || first.ID != second.ID || first.Kind != providers.InferenceOperationTitle || first.WorkloadProfile != providers.InferenceProfileBestEffort {
		t.Fatalf("operation metadata not stable across retry: %+v / %+v", first, second)
	}
}

type staticStreamClient struct {
	events chan providers.StreamEvent
}

func (s staticStreamClient) Chat(_ context.Context, _ providers.ChatRequest) (providers.ChatResponse, error) {
	return providers.ChatResponse{}, nil
}
func (s staticStreamClient) StreamChat(_ context.Context, _ providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	return s.events, nil
}

func TestCleanGeneratedThreadTitle_StripsCommonNoise(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want string
	}{
		// Reasoning fence and quoted English Title: prefix (existing behavior).
		{"<think>hidden</think>\nTitle: \"调试登录崩溃并修复认证流程\"", "调试登录崩溃并修复认证流程"},
		// Chinese 标题: prefix (half-width).
		{"标题: 修复登录崩溃", "修复登录崩溃"},
		// Chinese 标题: prefix (full-width colon).
		{"标题: 修复登录崩溃", "修复登录崩溃"},
		// Bullet prefix variants.
		{"- Refactoring user service", "Refactoring user service"},
		{"* Refactoring user service", "Refactoring user service"},
		// Surrounding 《》 brackets common in Chinese model output.
		{"《修复登录崩溃》", "修复登录崩溃"},
		// Multi-line: pick first non-empty.
		{"\n\nFix login crash\n\nignored extra line", "Fix login crash"},
		// Empty / whitespace-only input.
		{"   \n  \n", ""},
	}
	for _, c := range cases {
		got := cleanGeneratedThreadTitle(c.in)
		if got != c.want {
			t.Errorf("cleanGeneratedThreadTitle(%q) = %q; want %q", c.in, got, c.want)
		}
	}
}

func TestCleanGeneratedThreadTitle_TruncatesAtConfiguredLimit(t *testing.T) {
	t.Parallel()
	// Oversized ASCII titles are cropped to the generated-title limit.
	in := strings.Repeat("a", 120)
	got := cleanGeneratedThreadTitle(in)
	if got != strings.Repeat("a", titleMaxRuneLength) {
		t.Fatalf("got length %d; want %d", len([]rune(got)), titleMaxRuneLength)
	}
	// Chinese characters use the same rune limit, without splitting UTF-8 bytes.
	cn := strings.Repeat("一", 120)
	gotCN := cleanGeneratedThreadTitle(cn)
	if len([]rune(gotCN)) != titleMaxRuneLength {
		t.Fatalf("got %d runes; want %d", len([]rune(gotCN)), titleMaxRuneLength)
	}
}

func TestFirstUserMessageForTitle_RequiresExactlyOneUser(t *testing.T) {
	t.Parallel()
	type c struct {
		name    string
		history []providers.ChatMessage
		want    string
		ok      bool
	}
	cases := []c{
		{
			name: "single user message returns content",
			history: []providers.ChatMessage{
				{Role: "user", Content: "hello"},
				{Role: "assistant", Content: "hi"},
			},
			want: "hello",
			ok:   true,
		},
		{
			name: "two user messages reject (already past first turn)",
			history: []providers.ChatMessage{
				{Role: "user", Content: "hello"},
				{Role: "assistant", Content: "hi"},
				{Role: "user", Content: "again"},
			},
			ok: false,
		},
		{
			name: "no user message",
			history: []providers.ChatMessage{
				{Role: "system", Content: "system"},
				{Role: "assistant", Content: "hi"},
			},
			ok: false,
		},
		{
			name: "whitespace-only user does not count",
			history: []providers.ChatMessage{
				{Role: "user", Content: "   \n\t"},
				{Role: "user", Content: "real prompt"},
			},
			want: "real prompt",
			ok:   true,
		},
		{
			name: "agent notification does not count as user prompt",
			history: []providers.ChatMessage{
				{
					Role:    "user",
					Name:    "wuu_agent_notification",
					Content: `{"author":"/root/reviewer","recipient":"/root","content":"<subagent_notification>done</subagent_notification>"}`,
				},
				{Role: "user", Content: "real prompt"},
			},
			want: "real prompt",
			ok:   true,
		},
		{
			name: "unnamed inter-agent message does not count as user prompt",
			history: []providers.ChatMessage{
				{
					Role:    "user",
					Content: `{"author":"/root/review_plugin_platform","recipient":"/root","content":"continue with the desktop loader","trigger_turn":false}`,
				},
				{Role: "user", Content: "real prompt"},
			},
			want: "real prompt",
			ok:   true,
		},
	}
	for _, tc := range cases {
		got, ok := firstUserMessageForTitle(tc.history, false)
		if ok != tc.ok {
			t.Errorf("%s: ok=%v; want %v (first=%q)", tc.name, ok, tc.ok, got)
			continue
		}
		if ok && got != tc.want {
			t.Errorf("%s: first=%q; want %q", tc.name, got, tc.want)
		}
	}
}

// TestFirstUserMessageForTitle_ForceUsesFirstRegardlessOfCount covers the
// regenerate-title / probe path. force=true must return the first user
// message even when subsequent user messages exist (the production
// first-turn path uses force=false to mirror opencode's "only the very
// first turn gets a title" gating).
func TestFirstUserMessageForTitle_ForceUsesFirstRegardlessOfCount(t *testing.T) {
	t.Parallel()
	history := []providers.ChatMessage{
		{Role: "user", Content: "first prompt"},
		{Role: "assistant", Content: "first answer"},
		{Role: "user", Content: "second prompt"},
		{Role: "assistant", Content: "second answer"},
	}
	got, ok := firstUserMessageForTitle(history, true)
	if !ok || got != "first prompt" {
		t.Errorf("force=true: got (%q, %v); want (%q, true)", got, ok, "first prompt")
	}
	// And the no-user-message case must still fail even with force.
	if _, ok := firstUserMessageForTitle([]providers.ChatMessage{{Role: "assistant", Content: "hi"}}, true); ok {
		t.Error("force=true must still fail when no user message exists")
	}
}
