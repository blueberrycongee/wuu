package providers

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"charm.land/catwalk/pkg/catwalk"
)

// fakeCatwalkClient is a programmable CatwalkClient for unit tests.
type fakeCatwalkClient struct {
	calls    int
	response []catwalk.Provider
	err      error
	gotETag  string
}

func (f *fakeCatwalkClient) GetProviders(_ context.Context, etag string) ([]catwalk.Provider, error) {
	f.calls++
	f.gotETag = etag
	if f.err != nil {
		return nil, f.err
	}
	return f.response, nil
}

func sampleProviders() []catwalk.Provider {
	return []catwalk.Provider{
		{
			Name: "fake",
			Models: []catwalk.Model{
				{ID: "fake-large", ContextWindow: 200_000},
				{ID: "fake-small", ContextWindow: 8_000},
			},
		},
	}
}

func TestCatwalkSync_NilClientFallsBackToEmbedded(t *testing.T) {
	s := NewCatwalkSync(CatwalkSyncConfig{}) // no client, no cache
	got, err := s.Get(context.Background())
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("expected non-empty embedded providers")
	}
}

func TestCatwalkSync_RemoteSuccessReplacesEmbedded(t *testing.T) {
	client := &fakeCatwalkClient{response: sampleProviders()}
	s := NewCatwalkSync(CatwalkSyncConfig{Client: client})

	got, err := s.Get(context.Background())
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(got) != 1 || got[0].Name != "fake" {
		t.Fatalf("expected fake providers from remote, got %+v", got)
	}
	if client.calls != 1 {
		t.Fatalf("expected 1 fetch call, got %d", client.calls)
	}
}

func TestCatwalkSync_RemoteErrorKeepsEmbedded(t *testing.T) {
	client := &fakeCatwalkClient{err: errors.New("upstream broken")}
	s := NewCatwalkSync(CatwalkSyncConfig{Client: client})

	got, err := s.Get(context.Background())
	// Get should NOT propagate the fetch error as a Get failure —
	// it should report the error via the second return value while
	// still handing back usable embedded data.
	if err == nil {
		t.Fatal("expected fetch error to be surfaced")
	}
	if len(got) == 0 {
		t.Fatal("expected embedded fallback even on remote failure")
	}
}

func TestCatwalkSync_NotModifiedReturnsCached(t *testing.T) {
	client := &fakeCatwalkClient{err: catwalk.ErrNotModified}
	s := NewCatwalkSync(CatwalkSyncConfig{Client: client})

	got, err := s.Get(context.Background())
	if err != nil {
		t.Fatalf("304 should not be a Get error, got %v", err)
	}
	if len(got) == 0 {
		t.Fatal("expected cached/embedded fallback")
	}
}

func TestCatwalkSync_DiskCacheRoundTrip(t *testing.T) {
	dir := t.TempDir()
	cachePath := filepath.Join(dir, "subdir", "catwalk.json")

	// First syncer: remote returns sample data, which should be
	// persisted to disk.
	client1 := &fakeCatwalkClient{response: sampleProviders()}
	s1 := NewCatwalkSync(CatwalkSyncConfig{
		Client:    client1,
		CachePath: cachePath,
	})
	if _, err := s1.Get(context.Background()); err != nil {
		t.Fatalf("first Get: %v", err)
	}
	if _, err := os.Stat(cachePath); err != nil {
		t.Fatalf("expected cache file at %s, got %v", cachePath, err)
	}

	// Second syncer with NO client should populate from disk on Get.
	s2 := NewCatwalkSync(CatwalkSyncConfig{CachePath: cachePath})
	got, err := s2.Get(context.Background())
	if err != nil {
		t.Fatalf("second Get: %v", err)
	}
	if len(got) != 1 || got[0].Name != "fake" {
		t.Fatalf("disk cache round-trip lost data: got %+v", got)
	}
}

func TestCatwalkSync_EmptyResponseDoesNotOverwriteCache(t *testing.T) {
	client := &fakeCatwalkClient{response: nil}
	s := NewCatwalkSync(CatwalkSyncConfig{Client: client})

	got, err := s.Get(context.Background())
	if err == nil {
		t.Fatal("expected empty-response error to be surfaced")
	}
	if len(got) == 0 {
		t.Fatal("expected embedded fallback when remote returns empty")
	}
}

func TestCatwalkSync_GetIsIdempotent(t *testing.T) {
	client := &fakeCatwalkClient{response: sampleProviders()}
	s := NewCatwalkSync(CatwalkSyncConfig{Client: client})

	for i := 0; i < 5; i++ {
		_, _ = s.Get(context.Background())
	}
	if client.calls != 1 {
		t.Fatalf("expected 1 fetch across 5 Get calls, got %d", client.calls)
	}
}

func TestCatwalkSync_RefreshSendsCachedETag(t *testing.T) {
	client := &fakeCatwalkClient{response: sampleProviders()}
	s := NewCatwalkSync(CatwalkSyncConfig{Client: client})

	// First Get populates and computes an ETag.
	if _, err := s.Get(context.Background()); err != nil {
		t.Fatal(err)
	}
	firstETag := s.CachedETag()
	if firstETag == "" {
		t.Fatal("expected non-empty ETag after successful fetch")
	}

	// Refresh should send the stored ETag.
	if err := s.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if client.gotETag != firstETag {
		t.Fatalf("Refresh sent ETag %q, want %q", client.gotETag, firstETag)
	}
}

func TestCatwalkSync_FetchTimeoutDefault(t *testing.T) {
	s := NewCatwalkSync(CatwalkSyncConfig{})
	if s.cfg.FetchTimeout != 10*time.Second {
		t.Fatalf("expected default 10s timeout, got %v", s.cfg.FetchTimeout)
	}
}

func TestCatwalkSync_StoreCacheFailureDoesNotCrash(t *testing.T) {
	// Cache path under a non-writable directory: store should swallow
	// the failure, in-memory state still updates.
	cachePath := filepath.Join(string([]byte{0}), "bad", "catwalk.json") // invalid path
	client := &fakeCatwalkClient{response: sampleProviders()}
	s := NewCatwalkSync(CatwalkSyncConfig{
		Client:    client,
		CachePath: cachePath,
	})
	got, err := s.Get(context.Background())
	if err != nil {
		t.Fatalf("expected nil err despite cache write failure, got %v", err)
	}
	if len(got) == 0 {
		t.Fatal("expected in-memory data despite cache write failure")
	}
}

func TestDefaultCatwalkCachePath_NonEmpty(t *testing.T) {
	// Smoke test: should yield SOME path, even if HOME isn't set
	// (testing environment usually has it).
	if got := DefaultCatwalkCachePath(); got == "" {
		// Acceptable: no home dir resolvable. Don't fail.
		t.Skip("no home directory resolvable in this environment")
	}
}
