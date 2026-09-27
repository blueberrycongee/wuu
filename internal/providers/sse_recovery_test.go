package providers_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/providers/anthropic"
	"github.com/blueberrycongee/wuu/internal/providers/openai"
)

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
