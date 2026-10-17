// Run with: go run ./scripts/provider-latency-e2e -provider <configured name>
// Sends synthetic data through the real agent loop and configured credentials.
// JSONL contains metadata only; never log request headers, bodies or raw errors.
package main

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http/httptrace"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/providerfactory"
	"github.com/blueberrycongee/wuu/internal/providers"
)

// Set with -ldflags when building baseline and candidate executables.
var sourceRevision = "unrecorded"
var sourceDiff = "unrecorded"

type measurement struct {
	mu          sync.Mutex
	start       time.Time
	Submissions map[string]bool `json:"-"`
	Requests    int             `json:"submissions"`
	Connections []bool          `json:"http_connections_reused"`
	WireBytes   []int           `json:"http_body_bytes"`
	Encodings   []string        `json:"http_content_encodings"`
	WroteMS     []int64         `json:"http_request_written_ms"`
	HeaderMS    []int64         `json:"http_first_byte_ms"`
	TLSMS       []int64         `json:"tls_ms"`
	DNSMS       []int64         `json:"dns_ms"`
}

func (*measurement) PrepareOperation(providers.InferenceOperationJournalRecord) error { return nil }
func (*measurement) PrepareAttempt(providers.InferenceAttemptJournalRecord) error     { return nil }
func (m *measurement) UpsertSubmission(r providers.InferenceSubmissionJournalRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Submissions[r.ID] = true
	m.Requests = len(m.Submissions)
	return nil
}
func (*measurement) MarkAttemptFirstEvent(string, string, string, time.Time) error  { return nil }
func (*measurement) CompleteAttempt(providers.InferenceAttemptTerminalRecord) error { return nil }
func (*measurement) PrepareRecoveryAttempt(context.Context, providers.InferenceRecoveryAttemptJournalRecord) error {
	return nil
}
func (*measurement) CompleteOperation(providers.InferenceOperationTerminalRecord) error { return nil }
func (*measurement) CompleteWorkflow(providers.InferenceWorkflowTerminalRecord) error   { return nil }

func (m *measurement) trace() *httptrace.ClientTrace {
	var dns, tlsStart time.Time
	return &httptrace.ClientTrace{
		GotConn: func(info httptrace.GotConnInfo) {
			m.mu.Lock()
			defer m.mu.Unlock()
			m.Connections = append(m.Connections, info.Reused)
		},
		DNSStart: func(httptrace.DNSStartInfo) { m.mu.Lock(); dns = time.Now(); m.mu.Unlock() },
		DNSDone: func(httptrace.DNSDoneInfo) {
			m.mu.Lock()
			defer m.mu.Unlock()
			m.DNSMS = append(m.DNSMS, time.Since(dns).Milliseconds())
		},
		TLSHandshakeStart: func() { m.mu.Lock(); tlsStart = time.Now(); m.mu.Unlock() },
		TLSHandshakeDone: func(tls.ConnectionState, error) {
			m.mu.Lock()
			defer m.mu.Unlock()
			m.TLSMS = append(m.TLSMS, time.Since(tlsStart).Milliseconds())
		},
		WroteHeaderField: func(key string, values []string) {
			m.mu.Lock()
			defer m.mu.Unlock()
			if strings.EqualFold(key, "Content-Length") && len(values) == 1 {
				if n, err := strconv.Atoi(values[0]); err == nil {
					m.WireBytes = append(m.WireBytes, n)
				}
			}
			if strings.EqualFold(key, "Content-Encoding") {
				m.Encodings = append(m.Encodings, values...)
			}
		},
		WroteRequest: func(info httptrace.WroteRequestInfo) {
			m.mu.Lock()
			defer m.mu.Unlock()
			if info.Err == nil {
				m.WroteMS = append(m.WroteMS, time.Since(m.start).Milliseconds())
			}
		},
		GotFirstResponseByte: func() {
			m.mu.Lock()
			defer m.mu.Unlock()
			m.HeaderMS = append(m.HeaderMS, time.Since(m.start).Milliseconds())
		},
	}
}

type markerTool struct{ calls int }

func (*markerTool) Definitions() []providers.ToolDefinition {
	return []providers.ToolDefinition{{Name: "report_marker", Description: "Return the synthetic verification result for the given marker.", InputSchema: map[string]any{
		"type": "object", "properties": map[string]any{"marker": map[string]any{"type": "string"}}, "required": []string{"marker"}, "additionalProperties": false,
	}}}
}
func (t *markerTool) Execute(_ context.Context, call providers.ToolCall) (string, error) {
	t.calls++
	var args struct {
		Marker string `json:"marker"`
	}
	if call.Name != "report_marker" || json.Unmarshal([]byte(call.Arguments), &args) != nil || args.Marker != "LATENCY_OK" {
		return "", errors.New("invalid synthetic tool call")
	}
	return `{"marker":"LATENCY_OK","result":42}`, nil
}

func fixture(records int) []providers.ChatMessage {
	var body strings.Builder
	for i := 0; i < records; i++ {
		digest := sha256.Sum256([]byte(fmt.Sprintf("synthetic-record-%d", i)))
		row := map[string]any{
			"path": fmt.Sprintf("src/module_%03d/worker_%04d.go", i%47, i), "line": i*17%700 + 1,
			"digest": fmt.Sprintf("%x", digest), "status": []string{"ready", "pending", "verified", "skipped"}[i%4],
			"duration_ms": 37 + i*23%1900,
			"code":        fmt.Sprintf("func worker%d(ctx context.Context) error { return queue.Run(ctx, %d) }", i, i%19),
		}
		data, _ := json.Marshal(row)
		body.Write(data)
		body.WriteByte('\n')
	}
	if records == 0 {
		return nil
	}
	return []providers.ChatMessage{
		{Role: "user", Content: "Load these synthetic repository records for later. Do not analyze them."},
		{Role: "assistant", ToolCalls: []providers.ToolCall{{ID: "call_fixture", Name: "read_records", Arguments: `{}`}}},
		{Role: "tool", ToolCallID: "call_fixture", Name: "read_records", Content: body.String()},
	}
}

func emit(v any) {
	if err := json.NewEncoder(os.Stdout).Encode(v); err != nil {
		panic("write report failed")
	}
}

func main() {
	provider := flag.String("provider", "", "configured provider identifier (required)")
	transport := flag.String("transport", "sse", "sse, auto, websocket or websocket-cached")
	records := flag.Int("records", 0, "number of varied synthetic history records")
	repeats := flag.Int("repeats", 2, "independent conversations")
	turns := flag.Int("turns", 2, "turns per conversation")
	tool := flag.Bool("tool", false, "execute one synthetic tool on the first turn")
	forceTool := flag.Bool("force-tool", true, "use forced tool_choice when -tool is set; false tests normal auto choice")
	prewarm := flag.Bool("prewarm", false, "prewarm the conversation before timing its first turn")
	gzipEndpoint := flag.String("gzip-endpoint", "", "exact HTTP request URL to opt into experimental gzip")
	timeout := flag.Duration("timeout", 90*time.Second, "deadline per turn, including recovery")
	label := flag.String("label", "", "comparison label")
	flag.Parse()
	if *provider == "" || *repeats < 1 || *turns < 1 || *records < 0 || *timeout <= 0 {
		panic("invalid evaluation arguments")
	}
	if _, ok := providers.NormalizeStreamTransportMode(*transport); !ok {
		panic("invalid transport")
	}
	// Process-local only; a caller's saved configuration and environment survive.
	if err := os.Setenv("WUU_EXPERIMENTAL_REQUEST_GZIP_ENDPOINTS", *gzipEndpoint); err != nil {
		panic("set compression mode failed")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		panic("home unavailable")
	}
	root := os.Getenv("WUU_HOME")
	if root == "" {
		root = filepath.Join(home, ".wuu")
	}
	cfg, _, err := config.LoadPath(filepath.Join(root, "config.json"))
	if err != nil {
		panic("configuration unavailable")
	}
	pc, ok := cfg.Providers[*provider]
	if !ok {
		panic("provider unavailable")
	}
	pc.StreamTransport = *transport
	build := map[string]string{"go": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH, "source_revision": sourceRevision, "source_diff_sha256": sourceDiff}
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if strings.HasPrefix(setting.Key, "vcs.") {
				build[setting.Key] = setting.Value
			}
		}
	}
	initial := fixture(*records)
	fixtureJSON, _ := json.Marshal(initial)
	emit(map[string]any{"kind": "run", "utc": time.Now().UTC().Format(time.RFC3339), "label": *label, "provider": *provider, "model": pc.Model,
		"transport": *transport, "gzip": *gzipEndpoint != "", "records": *records, "tool": *tool, "force_tool": *tool && *forceTool, "prewarm": *prewarm,
		"repeats": *repeats, "turns": *turns, "timeout_ms": timeout.Milliseconds(), "fixture_sha256": fmt.Sprintf("%x", sha256.Sum256(fixtureJSON)),
		"build": build, "scope": "agent loop through real provider; excludes desktop rendering", "effort": "low", "cost": "unknown; reported token usage retained"})
	failed := false
	for repeat := 0; repeat < *repeats; repeat++ {
		client, err := providerfactory.BuildStreamClient(pc, *provider)
		if err != nil {
			panic("client setup failed")
		}
		key := fmt.Sprintf("latency-eval-%d-%d", time.Now().UnixNano(), repeat)
		runner := agent.StreamRunner{Client: client, ProviderName: *provider, Model: pc.Model, Effort: "low", PromptCacheKey: key,
			SystemPrompt: "This is a synthetic transport evaluation. Follow the final user instruction exactly; ignore the repository records unless asked.", MaxSteps: 3, DisableAutoCompact: true}
		var tools markerTool
		if *tool {
			runner.Tools = &tools
			if *forceTool {
				runner.ForceToolFirstStep = "report_marker"
			}
		}
		if *prewarm {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			started := time.Now()
			warmer, supported := client.(providers.SessionPrewarmer)
			if supported {
				err = warmer.PrewarmSession(ctx, key)
			}
			cancel()
			emit(map[string]any{"kind": "prewarm", "repeat": repeat + 1, "supported": supported, "passed": supported && err == nil, "total_ms": time.Since(started).Milliseconds()})
		}
		history := providers.CloneChatMessages(initial)
		for turn := 0; turn < *turns; turn++ {
			expected := "LATENCY_OK"
			prompt := "Reply with exactly LATENCY_OK. No explanation and no tool call."
			if *tool {
				expected = "LATENCY_OK:42"
				prompt = "Use report_marker exactly once with marker LATENCY_OK. Then reply with exactly LATENCY_OK:42, using the result."
				if turn > 0 {
					prompt = "Using the previous tool result, reply with exactly LATENCY_OK:42. Do not call tools again."
					runner.ForceToolFirstStep = ""
				}
			}
			history = append(history, providers.ChatMessage{Role: "user", Content: prompt})
			m := &measurement{start: time.Now(), Submissions: make(map[string]bool)}
			runner.InferenceJournal = m
			ctx, cancel := context.WithTimeout(context.Background(), *timeout)
			ctx = httptrace.WithClientTrace(ctx, m.trace())
			firstText, firstUseful := int64(0), int64(0)
			done, retries := 0, 0
			var states []providers.ProviderStateSummary
			res, callErr := runner.RunWithCallback(ctx, history, func(e providers.StreamEvent) {
				elapsed := time.Since(m.start).Milliseconds()
				if e.Type == providers.EventContentDelta && e.Content != "" {
					if firstText == 0 {
						firstText = elapsed
					}
					if firstUseful == 0 {
						firstUseful = elapsed
					}
				}
				if e.Type == providers.EventToolUseEnd && firstUseful == 0 {
					firstUseful = elapsed
				}
				if e.Type == providers.EventDone {
					done++
				}
				if e.Type == providers.EventReconnect {
					retries++
				}
				if e.Type == providers.EventProviderState && e.ProviderState != nil {
					states = append(states, *e.ProviderState)
				}
			})
			cancel()
			total := time.Since(m.start).Milliseconds()
			expectedSubmissions := 1
			if *tool && turn == 0 {
				expectedSubmissions = 2
			}
			m.mu.Lock()
			passed := callErr == nil && strings.TrimSpace(res.Content) == expected && m.Requests == expectedSubmissions && !res.Truncated
			if *tool {
				passed = passed && tools.calls == 1
			}
			failure := ""
			if !passed {
				failure = "evaluation_contract"
			}
			if callErr != nil {
				failure = string(providers.NormalizeFailure(callErr).Category)
			}
			status := 0
			var httpErr *providers.HTTPError
			if errors.As(callErr, &httpErr) {
				status = httpErr.StatusCode
			}
			emit(map[string]any{"kind": "turn", "repeat": repeat + 1, "turn": turn + 1, "passed": passed, "failure_category": failure,
				"first_text_ms": firstText, "first_useful_ms": firstUseful, "total_ms": total, "done_events": done, "retry_events": retries,
				"tool_calls": tools.calls, "transport": m, "provider_states": states,
				"answer_matches": strings.TrimSpace(res.Content) == expected, "submission_budget_ok": m.Requests == expectedSubmissions, "http_error_status": status,
				"input_tokens": res.InputTokens, "output_tokens": res.OutputTokens, "cache_read_tokens": res.CacheReadTokens})
			m.mu.Unlock()
			if !passed {
				failed = true
				break
			}
			history = append(history, res.NewMessages...)
		}
	}
	if failed {
		os.Exit(1)
	}
}
