package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"charm.land/catwalk/pkg/catwalk"
	"charm.land/catwalk/pkg/embedded"
)

// DefaultCatwalkURL is the production URL of the catwalk service
// charm operates. Mirrors crush's defaultCatwalkURL constant.
const DefaultCatwalkURL = "https://catwalk.charm.sh"

// CatwalkClient is the small slice of charm.land/catwalk's API the
// sync orchestrator depends on. Defining it here lets tests inject a
// mock client without spinning up an HTTP server.
type CatwalkClient interface {
	GetProviders(ctx context.Context, etag string) ([]catwalk.Provider, error)
}

// CatwalkSyncConfig configures a CatwalkSync. Zero values are valid:
// the syncer falls back to embedded data and disables remote fetch.
type CatwalkSyncConfig struct {
	// Client is the catwalk HTTP client. nil disables remote fetch
	// and the syncer always returns the embedded snapshot.
	Client CatwalkClient
	// CachePath is the on-disk JSON cache file. Empty means
	// "in-memory only" (no persistence between runs). The cache file
	// is created on first successful remote fetch.
	CachePath string
	// FetchTimeout is the per-attempt timeout for the remote call.
	// Zero means 10 seconds.
	FetchTimeout time.Duration
}

// CatwalkSync orchestrates the embedded → cache → remote resolution
// chain for the catwalk model index. Lifted directly from crush's
// catwalkSync, ported to wuu's package layout and idioms.
//
// Behavior on first Get:
//
//  1. Read the on-disk cache. If present and non-empty, that's the
//     starting point; otherwise the embedded snapshot is used.
//  2. If the remote client is configured, attempt a fetch with the
//     cached ETag. On success, update both in-memory state and the
//     disk cache. On 304 / timeout / error, keep the cached value.
//  3. Subsequent Get calls return the in-memory result without
//     hitting the network or the disk again.
//
// All paths gracefully degrade: even if the cache file is corrupt
// AND the remote is unreachable, embedded data is always returned.
type CatwalkSync struct {
	cfg  CatwalkSyncConfig
	once sync.Once
	mu   sync.RWMutex
	data []catwalk.Provider
	etag string
	err  error
}

// NewCatwalkSync constructs an unprimed syncer. Get triggers the
// initial resolution chain on first call.
func NewCatwalkSync(cfg CatwalkSyncConfig) *CatwalkSync {
	if cfg.FetchTimeout <= 0 {
		cfg.FetchTimeout = 10 * time.Second
	}
	return &CatwalkSync{cfg: cfg}
}

// Get returns the current catwalk providers slice plus any error
// encountered during the most recent fetch. The returned slice is
// always non-nil and always non-empty as long as catwalk's embedded
// snapshot itself is non-empty (which it always is in practice).
func (s *CatwalkSync) Get(ctx context.Context) ([]catwalk.Provider, error) {
	s.once.Do(func() { s.populate(ctx) })
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.data, s.err
}

// Refresh forces a re-fetch and re-stores the result on success.
// Useful for tests; production code calls Get and lets sync.Once do
// its job.
func (s *CatwalkSync) Refresh(ctx context.Context) error {
	s.once.Do(func() { s.populate(ctx) }) // ensure baseline is loaded
	return s.fetchOnce(ctx)
}

// CachedETag returns the most recently stored ETag (empty if none).
func (s *CatwalkSync) CachedETag() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.etag
}

func (s *CatwalkSync) populate(ctx context.Context) {
	// Step 1: load whatever's on disk, or fall back to embedded.
	cached, etag, _ := s.loadCache()
	if len(cached) == 0 {
		cached = embedded.GetAll()
	}
	s.mu.Lock()
	s.data = cached
	s.etag = etag
	s.mu.Unlock()

	// Step 2: try to refresh from remote, but only if a client is set.
	if s.cfg.Client == nil {
		return
	}
	_ = s.fetchOnce(ctx)
}

// fetchOnce runs a single remote fetch attempt and stores the result.
// Errors that translate to "use what we already have" (timeout,
// 304 Not Modified, transport error, empty response) are recorded
// but not propagated as failures — the syncer must always return
// usable data.
func (s *CatwalkSync) fetchOnce(ctx context.Context) error {
	if s.cfg.Client == nil {
		return errors.New("no remote client configured")
	}

	fetchCtx, cancel := context.WithTimeout(ctx, s.cfg.FetchTimeout)
	defer cancel()

	s.mu.RLock()
	currentETag := s.etag
	s.mu.RUnlock()

	result, err := s.cfg.Client.GetProviders(fetchCtx, currentETag)
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		s.recordErr(err)
		return err
	case errors.Is(err, catwalk.ErrNotModified):
		// Server says our cache is current. Nothing to do.
		s.recordErr(nil)
		return nil
	case err != nil:
		s.recordErr(err)
		return err
	case len(result) == 0:
		s.recordErr(errors.New("empty providers list from catwalk"))
		return errors.New("empty providers list from catwalk")
	}

	// Compute the new ETag client-side from the marshaled JSON so
	// the next request can ask for "anything newer than this".
	body, mErr := json.Marshal(result)
	newETag := ""
	if mErr == nil {
		newETag = catwalk.Etag(body)
	}

	s.mu.Lock()
	s.data = result
	s.etag = newETag
	s.err = nil
	s.mu.Unlock()

	if s.cfg.CachePath != "" {
		_ = s.storeCache(result, newETag) // best-effort persistence
	}
	return nil
}

func (s *CatwalkSync) recordErr(err error) {
	s.mu.Lock()
	s.err = err
	s.mu.Unlock()
}

// catwalkCacheFile is the on-disk JSON shape we use to persist a
// fetched provider snapshot together with the ETag the server
// associated with it.
type catwalkCacheFile struct {
	ETag      string             `json:"etag"`
	Providers []catwalk.Provider `json:"providers"`
}

func (s *CatwalkSync) loadCache() ([]catwalk.Provider, string, error) {
	if s.cfg.CachePath == "" {
		return nil, "", nil
	}
	data, err := os.ReadFile(s.cfg.CachePath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, "", nil
		}
		return nil, "", fmt.Errorf("read catwalk cache: %w", err)
	}
	var f catwalkCacheFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, "", fmt.Errorf("parse catwalk cache: %w", err)
	}
	return f.Providers, f.ETag, nil
}

func (s *CatwalkSync) storeCache(providers []catwalk.Provider, etag string) error {
	if s.cfg.CachePath == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.cfg.CachePath), 0o755); err != nil {
		return fmt.Errorf("mkdir catwalk cache dir: %w", err)
	}
	body, err := json.Marshal(catwalkCacheFile{ETag: etag, Providers: providers})
	if err != nil {
		return fmt.Errorf("marshal catwalk cache: %w", err)
	}
	tmp := s.cfg.CachePath + ".tmp"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return fmt.Errorf("write catwalk cache tmp: %w", err)
	}
	if err := os.Rename(tmp, s.cfg.CachePath); err != nil {
		return fmt.Errorf("rename catwalk cache: %w", err)
	}
	return nil
}

// DefaultCatwalkCachePath returns the on-disk cache path under the
// user's home directory. Honors $XDG_CACHE_HOME on Linux/macOS and
// falls back to ~/.cache/wuu/catwalk.json. Returns "" if no usable
// home directory can be determined.
func DefaultCatwalkCachePath() string {
	if cache := os.Getenv("XDG_CACHE_HOME"); cache != "" {
		return filepath.Join(cache, "wuu", "catwalk.json")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".cache", "wuu", "catwalk.json")
}
