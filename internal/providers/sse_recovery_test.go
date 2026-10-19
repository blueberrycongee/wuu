package providers_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/authstorage"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/providers/anthropic"
	"github.com/blueberrycongee/wuu/internal/providers/codex"
	"github.com/blueberrycongee/wuu/internal/providers/grokbuild"
	"github.com/blueberrycongee/wuu/internal/providers/openai"
	"github.com/blueberrycongee/wuu/internal/providers/xaisub"
)

// Completed turns must reuse the transport pool, including Codex's per-turn
// credential adapter and the client wrapper used for compressed redirects.
func TestSSEClientsReuseConnections(t *testing.T) {
	for _, protocol := range []string{"chat", "responses", "anthropic", "codex", "grokbuild", "xaisub"} {
		for _, compressed := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/gzip=%v", protocol, compressed), func(t *testing.T) {
				terminal := "data: [DONE]\n\n"
				if protocol == "anthropic" {
					terminal = "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
				}
				var calls atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					if got := r.Header.Get("Content-Encoding") == "gzip"; got != compressed {
						t.Errorf("gzip=%v, want %v", got, compressed)
					}
					_, _ = io.Copy(io.Discard, r.Body)
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprint(w, terminal)
				}))
				defer server.Close()
				endpoint := "/responses"
				switch protocol {
				case "chat", "grokbuild":
					endpoint = "/chat/completions"
				case "anthropic":
					endpoint = "/v1/messages"
				}
				optIn := ""
				if compressed {
					optIn = server.URL + endpoint
				}
				t.Setenv("WUU_EXPERIMENTAL_REQUEST_GZIP_ENDPOINTS", optIn)
				var client providers.StreamClient
				var err error
				switch protocol {
				case "anthropic":
					client, err = anthropic.New(anthropic.ClientConfig{BaseURL: server.URL, APIKey: "test", HTTPClient: server.Client()})
				case "codex":
					client, err = codex.New(codex.ClientConfig{BaseURL: server.URL, APIKey: "test", HTTPClient: server.Client(), StreamTransport: providers.StreamTransportSSE})
				case "grokbuild":
					client, err = grokbuild.New(grokbuild.ClientConfig{BaseURL: server.URL, APIKey: "test", HTTPClient: server.Client()})
				case "xaisub":
					// Use a disposable credential store, never the developer's login.
					t.Setenv("WUU_HOME", "")
					home := t.TempDir()
					store, storeErr := authstorage.ForHome(home)
					if storeErr != nil {
						t.Fatal(storeErr)
					}
					if storeErr = store.Set(xaisub.AuthProviderID, authstorage.Credentials{Type: "oauth", AccessToken: "test", ExpiresAt: time.Now().Add(time.Hour)}); storeErr != nil {
						t.Fatal(storeErr)
					}
					client, err = xaisub.New(xaisub.ClientConfig{BaseURL: server.URL, Home: home, HTTPClient: server.Client(), StreamTransport: providers.StreamTransportSSE})
				default:
					client, err = openai.New(openai.ClientConfig{BaseURL: server.URL, APIKey: "test", HTTPClient: server.Client(), WireAPI: protocol, ResponsesTransport: providers.StreamTransportSSE})
				}
				if err != nil {
					t.Fatal(err)
				}
				var reused atomic.Int32
				for i := 0; i < 3; i++ {
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					ctx = httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) {
						if info.Reused {
							reused.Add(1)
						}
					}})
					events, err := client.StreamChat(ctx, providers.ChatRequest{Model: "test", Messages: []providers.ChatMessage{{Role: "user", Content: strings.Repeat("synthetic record ", 5000)}}})
					if err != nil {
						cancel()
						t.Fatal(err)
					}
					done := 0
					for event := range events {
						if event.Type == providers.EventError {
							t.Error(event.Error)
						}
						if event.Type == providers.EventDone {
							done++
						}
					}
					cancel()
					if done != 1 {
						t.Fatalf("terminal events=%d", done)
					}
				}
				if calls.Load() != 3 || reused.Load() != 2 {
					t.Fatalf("requests=%d reused=%d; want 3 requests on one connection", calls.Load(), reused.Load())
				}
			})
		}
	}
}

func TestSSEClientsRecoverTruncatedEvent(t *testing.T) {
	for _, protocol := range []string{"chat", "responses", "anthropic"} {
		for _, profile := range []providers.InferenceWorkloadProfile{providers.InferenceProfileInteractive, providers.InferenceProfileBackgroundAgent} {
			for _, tail := range []string{"partial-json", "missing-delimiter"} {
				t.Run(fmt.Sprintf("%s/%s/%s", protocol, profile, tail), func(t *testing.T) {
					terminal := "data: [DONE]\n\n"
					if protocol == "anthropic" {
						terminal = "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
					}
					var attempts atomic.Int32
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.Header().Set("Content-Type", "text/event-stream")
						if attempts.Add(1) == 1 {
							if tail == "missing-delimiter" {
								fmt.Fprint(w, strings.TrimSuffix(terminal, "\n"))
							} else {
								fmt.Fprint(w, "event: content_block_delta\ndata: {\"type\":")
							}
							return
						}
						fmt.Fprint(w, terminal)
					}))
					defer server.Close()
					var inner providers.StreamClient
					var err error
					if protocol == "anthropic" {
						inner, err = anthropic.New(anthropic.ClientConfig{BaseURL: server.URL, APIKey: "test"})
					} else {
						wire := ""
						if protocol == "responses" {
							wire = "responses"
						}
						inner, err = openai.New(openai.ClientConfig{BaseURL: server.URL, APIKey: "test", WireAPI: wire, ResponsesTransport: providers.StreamTransportSSE})
					}
					if err != nil {
						t.Fatal(err)
					}
					retries := 0
					client := providers.NewReliableStreamClient(inner, func(_ int, _ int, err error, _ time.Duration) {
						retries++
						if !providers.IsRetryable(err) {
							t.Errorf("non-retryable truncation: %v", err)
						}
					}, providers.WithStreamRetryWait(func(context.Context, time.Duration) error { return nil }))
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					events, err := client.StreamChat(ctx, providers.ChatRequest{Model: "test", Messages: []providers.ChatMessage{{Role: "user", Content: "hello"}}, Operation: providers.NewInferenceOperation(providers.InferenceOperationAgentRound, profile)})
					if err != nil {
						t.Fatal(err)
					}
					done := 0
					for event := range events {
						if event.Type == providers.EventError {
							t.Errorf("recovery failed: %v", event.Error)
						}
						if event.Type == providers.EventDone {
							done++
						}
					}
					if done != 1 || attempts.Load() != 2 || retries != 1 {
						t.Fatalf("done=%d, attempts=%d, retries=%d", done, attempts.Load(), retries)
					}
				})
			}
		}
	}
}

func TestSSEClientsHandleLargeEvents(t *testing.T) {
	for _, protocol := range []string{"chat", "responses", "anthropic"} {
		for _, oversized := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/oversized=%v", protocol, oversized), func(t *testing.T) {
				payload := strings.Repeat("x", 2<<20)
				if oversized {
					payload = strings.Repeat("x", providers.MaxStreamEventBytes+1)
				}
				wire := fmt.Sprintf("data: {\"choices\":[{\"delta\":{\"content\":%q}}]}\n\n", payload)
				terminal := "data: [DONE]\n\n"
				switch protocol {
				case "responses":
					wire = fmt.Sprintf("data: {\"type\":\"response.output_text.delta\",\"delta\":%q}\n\n", payload)
				case "anthropic":
					wire = fmt.Sprintf("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":%q}}\n\n", payload)
					terminal = "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
				}
				var attempts atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					attempts.Add(1)
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprint(w, wire, terminal)
				}))
				defer server.Close()
				var inner providers.StreamClient
				var err error
				if protocol == "anthropic" {
					inner, err = anthropic.New(anthropic.ClientConfig{BaseURL: server.URL, APIKey: "test"})
				} else {
					api := ""
					if protocol == "responses" {
						api = "responses"
					}
					inner, err = openai.New(openai.ClientConfig{BaseURL: server.URL, APIKey: "test", WireAPI: api, ResponsesTransport: providers.StreamTransportSSE})
				}
				if err != nil {
					t.Fatal(err)
				}
				client := providers.NewReliableStreamClient(inner, nil, providers.WithStreamRetryWait(func(context.Context, time.Duration) error { return nil }))
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				events, err := client.StreamChat(ctx, providers.ChatRequest{Model: "test", Messages: []providers.ChatMessage{{Role: "user", Content: "hello"}}, Operation: providers.NewInferenceOperation(providers.InferenceOperationAgentRound, providers.InferenceProfileInteractive)})
				if err != nil {
					t.Fatal(err)
				}
				var content strings.Builder
				var terminalErr error
				done := 0
				for event := range events {
					switch event.Type {
					case providers.EventContentDelta:
						content.WriteString(event.Content)
					case providers.EventError:
						terminalErr = event.Error
					case providers.EventDone:
						done++
					}
				}
				if attempts.Load() != 1 {
					t.Fatalf("large event caused %d billable requests", attempts.Load())
				}
				if !oversized {
					if terminalErr != nil || done != 1 || content.String() != payload {
						t.Fatalf("large event: done=%d content bytes=%d err=%v", done, content.Len(), terminalErr)
					}
					return
				}
				var limitErr *providers.StreamEventTooLargeError
				var recoveryErr *providers.StreamRecoveryError
				if done != 0 || content.Len() != 0 || !errors.As(terminalErr, &limitErr) || !errors.As(terminalErr, &recoveryErr) {
					t.Fatalf("oversized event: done=%d content bytes=%d err=%v", done, content.Len(), terminalErr)
				}
				if recoveryErr.Recovery.RetryCount != 0 || recoveryErr.Recovery.StopReason != "non_retryable" || recoveryErr.Recovery.FailureCategory != providers.FailureResponseTooLarge {
					t.Fatalf("oversized recovery = %+v", recoveryErr.Recovery)
				}
			})
		}
	}
}

func TestOpenAISSEMalformedCompleteEventIsNotRetried(t *testing.T) {
	for _, wire := range []string{"", "responses"} {
		t.Run("wire="+wire, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data: {invalid}\n\n")
			}))
			defer server.Close()
			client, err := openai.New(openai.ClientConfig{BaseURL: server.URL, APIKey: "test", WireAPI: wire, ResponsesTransport: providers.StreamTransportSSE})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			events, err := client.StreamChat(ctx, providers.ChatRequest{Model: "test", Messages: []providers.ChatMessage{{Role: "user", Content: "hello"}}})
			if err != nil {
				t.Fatal(err)
			}
			sawError := false
			for event := range events {
				if event.Type == providers.EventDone {
					t.Fatal("malformed event completed successfully")
				}
				if event.Type == providers.EventError {
					sawError = true
					if event.Error == nil || providers.IsRetryable(event.Error) {
						t.Fatalf("malformed JSON error = %v", event.Error)
					}
				}
			}
			if !sawError {
				t.Fatal("malformed event was silently discarded")
			}
		})
	}
}

func compressionTestClient(t *testing.T, protocol string, server *httptest.Server) providers.StreamClient {
	t.Helper()
	var client providers.StreamClient
	var err error
	if protocol == "anthropic" {
		client, err = anthropic.New(anthropic.ClientConfig{BaseURL: server.URL, APIKey: "synthetic-key", HTTPClient: server.Client()})
	} else {
		client, err = openai.New(openai.ClientConfig{BaseURL: server.URL, APIKey: "synthetic-key", HTTPClient: server.Client(), WireAPI: protocol, ResponsesTransport: providers.StreamTransportSSE})
	}
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestHTTPClientsRequestCompression(t *testing.T) {
	for _, protocol := range []string{"chat", "responses", "anthropic"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%v", protocol, stream), func(t *testing.T) {
				bodies := make(chan []byte, 2)
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					raw, err := io.ReadAll(r.Body)
					if err != nil {
						t.Error(err)
						return
					}
					if r.ContentLength != int64(len(raw)) || r.Header.Get("Content-Type") != "application/json" {
						t.Error("invalid wire length or content type")
					}
					if requests.Add(1) == 1 {
						if r.Header.Get("Content-Encoding") != "" {
							t.Error("compression enabled by default")
						}
					} else {
						if r.Header.Get("Content-Encoding") != "gzip" {
							t.Error("opted-in request was not compressed")
							return
						}
						reader, err := gzip.NewReader(bytes.NewReader(raw))
						if err != nil {
							t.Error(err)
							return
						}
						decoded, err := io.ReadAll(reader)
						_ = reader.Close()
						if err != nil || len(raw) >= len(decoded) {
							t.Errorf("compressed bytes=%d JSON bytes=%d error=%v", len(raw), len(decoded), err)
						}
						t.Logf("protocol=%s stream=%v JSON_bytes=%d gzip_bytes=%d", protocol, stream, len(decoded), len(raw))
						raw = decoded
					}
					bodies <- raw
					w.Header().Set("Content-Type", "application/json")
					if stream {
						w.Header().Set("Content-Type", "text/event-stream")
						if protocol == "anthropic" {
							fmt.Fprint(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
						} else {
							fmt.Fprint(w, "data: [DONE]\n\n")
						}
					} else {
						switch protocol {
						case "chat":
							fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}]}`)
						case "responses":
							fmt.Fprint(w, `{"id":"resp_test","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}]}`)
						case "anthropic":
							fmt.Fprint(w, `{"id":"msg_test","role":"assistant","content":[{"type":"text","text":"done"}],"stop_reason":"end_turn"}`)
						}
					}
				}))
				defer server.Close()
				client := compressionTestClient(t, protocol, server)
				req := providers.ChatRequest{
					Model: "test-model",
					Messages: []providers.ChatMessage{
						{Role: "user", Content: "Inspect the synthetic records."},
						{Role: "assistant", ToolCalls: []providers.ToolCall{{ID: "call_test", Name: "read_file", Arguments: `{"path":"synthetic.txt"}`}}},
						{Role: "tool", ToolCallID: "call_test", Name: "read_file", Content: strings.Repeat("synthetic record 界🙂\n", 10000)},
					},
					Tools: []providers.ToolDefinition{{Name: "read_file", Description: "Read a synthetic file", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}, "required": []string{"path"}}}},
				}
				for _, enabled := range []bool{false, true} {
					optIn := ""
					if enabled {
						optIn = server.URL + "/chat/completions," + server.URL + "/responses," + server.URL + "/v1/messages"
					}
					t.Setenv("WUU_EXPERIMENTAL_REQUEST_GZIP_ENDPOINTS", optIn)
					if stream {
						events, err := client.StreamChat(context.Background(), req)
						if err != nil {
							t.Fatal(err)
						}
						done := 0
						for event := range events {
							if event.Type == providers.EventError {
								t.Fatal(event.Error)
							}
							if event.Type == providers.EventDone {
								done++
							}
						}
						if done != 1 {
							t.Fatalf("stream done events=%d", done)
						}
					} else {
						response, err := client.Chat(context.Background(), req)
						if err != nil || response.Content != "done" {
							t.Fatalf("response=%+v error=%v", response, err)
						}
					}
				}
				if len(bodies) != 2 || !bytes.Equal(<-bodies, <-bodies) {
					t.Fatal("compression changed serialized JSON or submission count")
				}
			})
		}
	}
}

func TestHTTPClientsCompressedFailureDoesNotResend(t *testing.T) {
	for _, protocol := range []string{"chat", "responses", "anthropic"} {
		for _, failure := range []string{"unsupported", "connection lost", "cancelled"} {
			t.Run(protocol+"/"+failure, func(t *testing.T) {
				var attempts atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					attempts.Add(1)
					if r.Header.Get("Content-Encoding") != "gzip" {
						t.Error("request was not compressed")
					}
					_, _ = io.Copy(io.Discard, r.Body)
					if failure == "connection lost" {
						conn, _, err := w.(http.Hijacker).Hijack()
						if err != nil {
							t.Error(err)
							return
						}
						_ = conn.Close()
						return
					}
					http.Error(w, "request encoding unsupported", http.StatusUnsupportedMediaType)
				}))
				defer server.Close()
				t.Setenv("WUU_EXPERIMENTAL_REQUEST_GZIP_ENDPOINTS", server.URL+"/chat/completions,"+server.URL+"/responses,"+server.URL+"/v1/messages")
				client := compressionTestClient(t, protocol, server)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				wantAttempts := int32(1)
				if failure == "cancelled" {
					cancel()
					wantAttempts = 0
				}
				_, err := client.StreamChat(ctx, providers.ChatRequest{Model: "test-model", Messages: []providers.ChatMessage{{Role: "user", Content: strings.Repeat("synthetic", 20000)}}})
				if err == nil || attempts.Load() != wantAttempts {
					t.Fatalf("error=%v attempts=%d want=%d", err, attempts.Load(), wantAttempts)
				}
				if failure == "unsupported" && providers.IsRetryable(err) {
					t.Fatal("unsupported compression must not trigger recovery")
				}
				if failure == "cancelled" && !errors.Is(err, context.Canceled) {
					t.Fatalf("cancellation was lost: %v", err)
				}
			})
		}
	}
}

func TestHTTPClientsCompressedRedirects(t *testing.T) {
	for _, protocol := range []string{"chat", "responses", "anthropic"} {
		for _, status := range []int{http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
			for _, allowDestination := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%d/allow=%v", protocol, status, allowDestination), func(t *testing.T) {
					var initialHits, destinationHits atomic.Int32
					bodies := make(chan []byte, 2)
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						wire, err := io.ReadAll(r.Body)
						if err != nil {
							t.Error(err)
						}
						if r.Header.Get("Content-Encoding") != "gzip" || r.ContentLength != int64(len(wire)) {
							t.Error("redirect lost gzip metadata")
						}
						select {
						case bodies <- wire:
						default:
							t.Error("unexpected request replay")
						}
						if r.URL.Path != "/destination" {
							initialHits.Add(1)
							http.Redirect(w, r, "/destination", status)
							return
						}
						destinationHits.Add(1)
						w.Header().Set("Content-Type", "text/event-stream")
						if protocol == "anthropic" {
							fmt.Fprint(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
						} else {
							fmt.Fprint(w, "data: [DONE]\n\n")
						}
					}))
					defer server.Close()
					endpoints := server.URL + "/chat/completions," + server.URL + "/responses," + server.URL + "/v1/messages"
					if allowDestination {
						endpoints += "," + server.URL + "/destination"
					}
					t.Setenv("WUU_EXPERIMENTAL_REQUEST_GZIP_ENDPOINTS", endpoints)
					client := providers.NewReliableStreamClient(compressionTestClient(t, protocol, server), nil,
						providers.WithStreamRetryWait(func(context.Context, time.Duration) error { return nil }))
					events, terminalErr := client.StreamChat(context.Background(), providers.ChatRequest{
						Model: "test-model", Messages: []providers.ChatMessage{{Role: "user", Content: strings.Repeat("synthetic", 20000)}},
						Operation: providers.NewInferenceOperation(providers.InferenceOperationAgentRound, providers.InferenceProfileInteractive),
					})
					done := 0
					if events != nil {
						for event := range events {
							if event.Type == providers.EventError {
								terminalErr = event.Error
							}
							if event.Type == providers.EventDone {
								done++
							}
						}
					}
					if initialHits.Load() != 1 {
						t.Fatalf("initial compressed POST sent %d times", initialHits.Load())
					}
					if allowDestination {
						if terminalErr != nil || done != 1 || destinationHits.Load() != 1 || len(bodies) != 2 || !bytes.Equal(<-bodies, <-bodies) {
							t.Fatalf("redirect replay: error=%v done=%d destination hits=%d", terminalErr, done, destinationHits.Load())
						}
					} else if terminalErr == nil || providers.IsRetryable(terminalErr) || destinationHits.Load() != 0 || done != 0 {
						t.Fatalf("blocked redirect: error=%v done=%d destination hits=%d", terminalErr, done, destinationHits.Load())
					}
				})
			}
		}
	}
}
