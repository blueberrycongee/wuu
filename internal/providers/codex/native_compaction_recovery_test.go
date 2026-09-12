package codex

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/authstorage"
	"github.com/blueberrycongee/wuu/internal/compact"
	"github.com/blueberrycongee/wuu/internal/providers"
)

type nativeRecoveryJournal struct {
	codexJournal
	mu         sync.Mutex
	attempts   []providers.InferenceAttemptJournalRecord
	terminals  []providers.InferenceAttemptTerminalRecord
	operations []providers.InferenceOperationTerminalRecord
}

func (j *nativeRecoveryJournal) PrepareAttempt(r providers.InferenceAttemptJournalRecord) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.attempts = append(j.attempts, r)
	return nil
}

func (j *nativeRecoveryJournal) PrepareRecoveryAttempt(_ context.Context, r providers.InferenceRecoveryAttemptJournalRecord) error {
	return j.PrepareAttempt(r.NextAttempt)
}

func (j *nativeRecoveryJournal) CompleteAttempt(r providers.InferenceAttemptTerminalRecord) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.terminals = append(j.terminals, r)
	return nil
}

func (j *nativeRecoveryJournal) CompleteOperation(r providers.InferenceOperationTerminalRecord) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.operations = append(j.operations, r)
	return nil
}

func TestNativeCompactionAuthRecovery(t *testing.T) {
	for _, mode := range []string{"refresh", "refresh_failed", "still_unauthorized", "api_key"} {
		t.Run(mode, func(t *testing.T) {
			home := t.TempDir()
			stale := fakeJWT(t, time.Now().Add(time.Hour), "acct_compact")
			fresh := fakeJWT(t, time.Now().Add(2*time.Hour), "acct_compact")
			store, err := authstorage.ForHome(home)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.Set("openai-codex", authstorage.Credentials{Type: "oauth", AccessToken: stale, RefreshToken: "refresh-old"}); err != nil {
				t.Fatal(err)
			}
			journal := &nativeRecoveryJournal{}
			var refreshCalls, apiCalls atomic.Int32
			tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				refreshCalls.Add(1)
				journal.mu.Lock()
				prepared := len(journal.attempts) == 2 && len(journal.terminals) == 1
				journal.mu.Unlock()
				if !prepared {
					t.Error("refresh happened before the recovery attempt was durably prepared")
				}
				if mode == "refresh_failed" {
					w.WriteHeader(http.StatusBadRequest)
					_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"access_token":` + jsonString(fresh) + `,"refresh_token":"refresh-new"}`))
			}))
			defer tokenServer.Close()
			t.Setenv("CODEX_REFRESH_TOKEN_URL_OVERRIDE", tokenServer.URL)
			apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				call := apiCalls.Add(1)
				if call == 1 || mode == "still_unauthorized" {
					w.WriteHeader(http.StatusUnauthorized)
					_, _ = w.Write([]byte(`{"error":{"message":"expired token"}}`))
					return
				}
				if r.Header.Get("Authorization") != "Bearer "+fresh {
					t.Error("retry did not use refreshed credentials")
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = w.Write([]byte("event: response.output_item.done\ndata: {\"type\":\"response.output_item.done\",\"item\":{\"id\":\"cmp_auth\",\"type\":\"compaction\",\"encrypted_content\":\"opaque\"}}\n\nevent: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[]}}\n\n"))
			}))
			defer apiServer.Close()
			cfg := ClientConfig{BaseURL: apiServer.URL, Home: home, HTTPClient: apiServer.Client(), StreamTransport: providers.StreamTransportSSE}
			if mode == "api_key" {
				cfg.APIKey = stale
			}
			client, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			messages := []providers.ChatMessage{{Role: "user", Content: "keep this work"}}
			result, err := compact.CompactWithNativeOrSummary(providers.WithInferenceJournal(context.Background(), journal), messages, client, "gpt-5-codex", compact.Budget{ContextTokens: 100000}, compact.NativeOptions{Provider: "openai-codex"})
			wantCalls, wantRefresh, wantAttempts := int32(2), int32(1), 2
			wantOutcome := providers.InferenceOutcomeFailed
			if mode == "refresh" {
				wantOutcome = providers.InferenceOutcomeSucceeded
				if err != nil || len(result) != 1 || len(result[0].ProviderItems) != 1 {
					t.Fatalf("native recovery = %#v, %v", result, err)
				}
			} else if err == nil || len(result) != 1 || result[0].Content != messages[0].Content {
				t.Fatalf("failed compaction must retain original history: %#v, %v", result, err)
			}
			if mode == "refresh_failed" {
				wantCalls = 1
			}
			if mode == "api_key" {
				wantCalls, wantRefresh, wantAttempts = 1, 0, 1
			}
			if apiCalls.Load() != wantCalls || refreshCalls.Load() != wantRefresh {
				t.Fatalf("API/refresh calls = %d/%d", apiCalls.Load(), refreshCalls.Load())
			}
			journal.mu.Lock()
			defer journal.mu.Unlock()
			if len(journal.attempts) != wantAttempts || len(journal.terminals) != wantAttempts || len(journal.operations) != 1 {
				t.Fatalf("journal counts = %d/%d/%d", len(journal.attempts), len(journal.terminals), len(journal.operations))
			}
			for i, attempt := range journal.attempts {
				if attempt.AttemptID != journal.terminals[i].AttemptID || attempt.Ordinal != i+1 {
					t.Fatalf("attempt lifecycle mismatch at %d", i)
				}
			}
			if journal.operations[0].Outcome != wantOutcome || journal.terminals[wantAttempts-1].Outcome != wantOutcome {
				t.Fatalf("unexpected terminal outcome: %+v", journal.operations)
			}
		})
	}
}
