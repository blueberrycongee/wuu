package appserver

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/providers"
)

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

func TestCleanGeneratedThreadTitle_TruncatesAtOpenCodeLimit(t *testing.T) {
	t.Parallel()
	// 120 ASCII chars should be cropped to 100 (titleMaxRuneLength).
	in := strings.Repeat("a", 120)
	got := cleanGeneratedThreadTitle(in)
	if got != strings.Repeat("a", titleMaxRuneLength) {
		t.Fatalf("got length %d; want %d", len([]rune(got)), titleMaxRuneLength)
	}
	// 120 Chinese chars (3 bytes each) should also crop to 100 runes.
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
