package providerfactory

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/providers"
)

type quotaWaitContext struct {
	context.Context
	ready chan struct{}
	once  sync.Once
}

func (c *quotaWaitContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.ready) })
	return c.Context.Done()
}

func TestRebuiltQuotaClientsShareOnlyResolvedCredential(t *testing.T) {
	t.Setenv("WUU_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	var calls atomic.Int32
	entered, release := make(chan struct{}, 3), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		entered <- struct{}{}
		<-release
		_, _ = io.WriteString(w, `{"five_hour":{"utilization":20,"resets_at":"2026-10-02T00:00:00Z"}}`)
	}))
	defer func() { unblock(); server.Close() }()
	configFor := func(token string) config.ProviderConfig {
		return config.ProviderConfig{Type: "anthropic", BaseURL: server.URL, AuthToken: token, Model: "synthetic-model"}
	}
	read := func(ctx context.Context, token string) <-chan providers.Quota {
		client, err := BuildClient(configFor(token), "fixture-connection")
		if err != nil {
			t.Fatal(err)
		}
		reader, ok := client.(providers.QuotaReader)
		if !ok {
			t.Fatal("factory client does not expose quota")
		}
		done := make(chan providers.Quota, 1)
		go func() {
			quota, err := reader.ReadQuota(ctx)
			if err != nil {
				quota.ErrorCode = providers.QuotaErrorCode(err)
			}
			done <- quota
		}()
		return done
	}
	await := func(ch <-chan struct{}) {
		t.Helper()
		select {
		case <-ch:
		case <-time.After(5 * time.Second):
			t.Fatal("quota request did not progress")
		}
	}
	first := read(context.Background(), "synthetic-token-one")
	await(entered)
	observed := &quotaWaitContext{Context: context.Background(), ready: make(chan struct{})}
	second := read(observed, "synthetic-token-one")
	await(observed.ready)
	// A newly resolved credential must get an independent in-flight request.
	third := read(context.Background(), "synthetic-token-two")
	await(entered)
	unblock()
	results := make([]providers.Quota, 0, 3)
	for _, done := range []<-chan providers.Quota{first, second, third} {
		select {
		case q := <-done:
			results = append(results, q)
		case <-time.After(5 * time.Second):
			t.Fatal("quota response did not finish")
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("two rebuilt identical clients plus rotated credential made %d upstream calls, want 2", calls.Load())
	}
	for _, q := range results {
		if q.ErrorCode != "" || len(q.Windows) != 1 || q.Windows[0].UsedPercent == nil || *q.Windows[0].UsedPercent != 20 {
			t.Fatalf("quota payload changed: %+v", q)
		}
	}
	if results[0].Account == nil || results[1].Account == nil || results[2].Account == nil {
		t.Fatal("missing account identity")
	}
	if results[0].Account.ID != results[1].Account.ID || results[0].Account.ID == results[2].Account.ID {
		t.Fatal("credential-scoped account isolation changed")
	}
}
